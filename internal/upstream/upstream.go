// Package upstream fetches version and release information from the
// distribution points named by VersionSources and ChangeSources. Fetchers are
// deterministic HTTP reads — no AI here.
package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"sigs.k8s.io/yaml"
)

// Client fetches from upstream sources. The zero value is usable; fields
// exist for tests and rate-limit tokens.
type Client struct {
	// HTTP overrides the default client. Injecting one bypasses the
	// dial-time egress check but never the URL-level one in get.
	HTTP *http.Client
	// GitHubBaseURL defaults to the public API; tests point it elsewhere.
	GitHubBaseURL string
	// GitHubToken raises the API rate limit when set.
	GitHubToken string
	// ArtifactHubBaseURL defaults to the public API; tests point it
	// elsewhere.
	ArtifactHubBaseURL string
	// InsecurePrivateEgress disables the https requirement and the
	// private/loopback/link-local destination block in the egress guard. It
	// exists solely so tests can reach httptest servers on 127.0.0.1; no
	// production construction site sets it and nothing plumbs it through.
	InsecurePrivateEgress bool

	// guarded is the lazily built default client with the dial-time egress
	// check installed; guardOnce keeps the transport (and its connection
	// pool) shared across fetches.
	guardOnce sync.Once
	guarded   *http.Client
}

const (
	// maxRedirects bounds redirect chains; every hop re-passes the URL-level
	// egress check.
	maxRedirects = 5
	// artifactHubSearchLimit is the result-count cap requested from the
	// search API; same-named charts beyond it are not considered.
	artifactHubSearchLimit = 25
	// artifactHubKindHelm is Artifact Hub's package kind for Helm charts.
	artifactHubKindHelm = 0
)

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	c.guardOnce.Do(func() {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		if !c.InsecurePrivateEgress {
			// The dial-time check sees the resolved IP of every connection
			// attempt (redirect targets included), which is what defeats DNS
			// names pointing at internal ranges and DNS rebinding.
			dialer.Control = func(_, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				ip := net.ParseIP(host)
				if ip == nil || !isPublicIP(ip) {
					return fmt.Errorf("egress guard: refusing to dial %s", host)
				}
				return nil
			}
		}
		c.guarded = &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				DialContext:         dialer.DialContext,
				ForceAttemptHTTP2:   true,
				MaxIdleConns:        20,
				IdleConnTimeout:     90 * time.Second,
				TLSHandshakeTimeout: 10 * time.Second,
			},
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return errors.New("too many redirects")
				}
				return c.checkEgressURL(req.URL.String())
			},
		}
	})
	return c.guarded
}

func (c *Client) githubBase() string {
	if c.GitHubBaseURL != "" {
		return strings.TrimSuffix(c.GitHubBaseURL, "/")
	}
	return "https://api.github.com"
}

func (c *Client) artifactHubBase() string {
	if c.ArtifactHubBaseURL != "" {
		return strings.TrimSuffix(c.ArtifactHubBaseURL, "/")
	}
	return "https://artifacthub.io"
}

// CheckEgressURL reports whether the guard allows fetching rawURL: http(s)
// only, no userinfo, and — unless InsecurePrivateEgress — https with a
// destination outside loopback, private, link-local (including the cloud
// metadata service), and CGNAT ranges. The dial-time check in the default
// client re-verifies the resolved IP of every connection.
func (c *Client) CheckEgressURL(rawURL string) error {
	return c.checkEgressURL(rawURL)
}

func (c *Client) checkEgressURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("egress guard: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("egress guard: scheme %q of %s is not http(s)", u.Scheme, rawURL)
	}
	if u.Host == "" {
		return fmt.Errorf("egress guard: %s has no host", rawURL)
	}
	if u.User != nil {
		return fmt.Errorf("egress guard: URL for %s carries userinfo", u.Host)
	}
	if c.InsecurePrivateEgress {
		return nil
	}
	if u.Scheme != "https" {
		return fmt.Errorf("egress guard: %s is not https", rawURL)
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && !isPublicIP(ip) {
		return fmt.Errorf("egress guard: destination %s is in a blocked range", u.Hostname())
	}
	return nil
}

// isPublicIP reports whether ip is a publicly routable unicast address —
// false for loopback, private (RFC1918/ULA), link-local (including the cloud
// metadata service), CGNAT, multicast, unspecified, and broadcast.
func isPublicIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1]&0xc0 == 64 { // 100.64.0.0/10 (CGNAT)
			return false
		}
		if v4.Equal(net.IPv4bcast) {
			return false
		}
	}
	return true
}

// get is the single HTTP chokepoint every upstream fetch flows through: it
// enforces the egress guard (https-only with private/link-local/metadata
// destinations blocked, re-checked per redirect hop and at dial time),
// builds the request, applies headers, performs it, enforces the read limit,
// and turns a non-200 into an error carrying the first line of the body.
// Rate-limit tokens attach here so every fetcher inherits them at once.
func (c *Client) get(ctx context.Context, rawURL string, limit int64, headers map[string]string) ([]byte, error) {
	if err := c.checkEgressURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	// Artifact Hub asks API callers to identify themselves; do it for every
	// upstream fetch rather than the default Go-http-client string.
	req.Header.Set("User-Agent", "kdelta (+https://github.com/shadiramadan/kdelta)")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", rawURL, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", rawURL, err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: %s (%s)", rawURL, res.Status, firstLine(body))
	}
	return body, nil
}

// GitHubRelease is one published release, notes included.
type GitHubRelease struct {
	Tag         string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
}

// GitHubReleases lists a repository's releases, newest first, drafts
// excluded. Paginates up to maxPages pages of 100.
func (c *Client) GitHubReleases(ctx context.Context, owner, repo string) ([]GitHubRelease, error) {
	const maxPages = 3
	var releases []GitHubRelease
	headers := map[string]string{"Accept": "application/vnd.github+json"}
	if c.GitHubToken != "" {
		headers["Authorization"] = "Bearer " + c.GitHubToken
	}
	for page := 1; page <= maxPages; page++ {
		u := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100&page=%d",
			c.githubBase(), url.PathEscape(owner), url.PathEscape(repo), page)
		body, err := c.get(ctx, u, 20<<20, headers)
		if err != nil {
			return nil, err
		}
		var pageReleases []GitHubRelease
		if err := json.Unmarshal(body, &pageReleases); err != nil {
			return nil, fmt.Errorf("decoding releases for %s/%s: %w", owner, repo, err)
		}
		for _, r := range pageReleases {
			if !r.Draft {
				releases = append(releases, r)
			}
		}
		if len(pageReleases) < 100 {
			break
		}
	}
	return releases, nil
}

// ArtifactHubRepository is the repository an Artifact Hub package is
// published from.
type ArtifactHubRepository struct {
	URL               string `json:"url"`
	Name              string `json:"name"`
	Kind              int    `json:"kind"`
	VerifiedPublisher bool   `json:"verified_publisher"`
	Official          bool   `json:"official"`
}

// ArtifactHubPackage is one Artifact Hub search result.
type ArtifactHubPackage struct {
	Name                         string                `json:"name"`
	NormalizedName               string                `json:"normalized_name"`
	Official                     bool                  `json:"official"`
	ProductionOrganizationsCount int                   `json:"production_organizations_count"`
	Repository                   ArtifactHubRepository `json:"repository"`
}

// HelmChart reports whether the package is published from a Helm chart
// repository (as opposed to another Artifact Hub package kind).
func (p ArtifactHubPackage) HelmChart() bool {
	return p.Repository.Kind == artifactHubKindHelm
}

// ArtifactHubSearchCharts searches Artifact Hub for Helm charts matching
// name. Results are candidates only: same-named charts from unrelated
// repositories are expected, and callers must verify a candidate before
// trusting it.
func (c *Client) ArtifactHubSearchCharts(ctx context.Context, name string) ([]ArtifactHubPackage, error) {
	u := fmt.Sprintf("%s/api/v1/packages/search?ts_query_web=%s&kind=%d&facets=false&limit=%d",
		c.artifactHubBase(), url.QueryEscape(name), artifactHubKindHelm, artifactHubSearchLimit)
	body, err := c.get(ctx, u, 2<<20, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Packages []ArtifactHubPackage `json:"packages"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decoding artifact hub search for %q: %w", name, err)
	}
	return result.Packages, nil
}

// HelmChartVersion is one chart entry from a repository index.
type HelmChartVersion struct {
	Version    string    `json:"version"`
	AppVersion string    `json:"appVersion"`
	Created    time.Time `json:"created"`
	Deprecated bool      `json:"deprecated"`
	// Home and Sources identify the packaged project. They exist to verify a
	// candidate repository against deployed chart metadata and are never
	// emitted into resources or rendered output.
	Home    string   `json:"home"`
	Sources []string `json:"sources"`
}

// HelmChartVersions reads a chart repository's index for one chart. Entries
// whose version strings are oversized or carry control characters are
// dropped: the index is third-party content and these strings are rendered
// and compared downstream.
func (c *Client) HelmChartVersions(ctx context.Context, repoURL, chart string) ([]HelmChartVersion, error) {
	u := strings.TrimSuffix(repoURL, "/") + "/index.yaml"
	body, err := c.get(ctx, u, 50<<20, nil)
	if err != nil {
		return nil, err
	}
	var index struct {
		Entries map[string][]HelmChartVersion `json:"entries"`
	}
	if err := yaml.Unmarshal(body, &index); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", u, err)
	}
	entries, ok := index.Entries[chart]
	if !ok {
		return nil, fmt.Errorf("chart %q not present in %s", chart, u)
	}
	versions := make([]HelmChartVersion, 0, len(entries))
	for _, v := range entries {
		if !safeVersionString(v.Version) || (v.AppVersion != "" && !safeVersionString(v.AppVersion)) {
			continue
		}
		versions = append(versions, v)
	}
	return versions, nil
}

// safeVersionString bounds a third-party version string before it is
// rendered or compared: non-empty, at most 128 bytes, no control characters.
func safeVersionString(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// firstLine trims an HTTP error body to a single printable line: the body is
// attacker-supplied text that flows into error strings rendered in
// terminals.
func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = strings.ToValidUTF8(s[:200], "")
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
