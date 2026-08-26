package resolve

import (
	"context"
	"net/url"
	"sort"
	"strings"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/upstream"
)

// maxCandidateFetches bounds index.yaml fetches per package: only the
// highest-trust shortlisted candidates are verified.
const maxCandidateFetches = 5

// Trust model. Three principals feed this algorithm: the cluster tenant
// (controls the deployed chart's name, versions, home, and sources), any
// Artifact Hub publisher (controls the candidate listing — registration is
// open, and same-named charts from unrelated repositories are normal), and
// each candidate repository owner (controls their index.yaml). Therefore the
// deployed release's own metadata is the PRIMARY matcher — it attributes
// forks to their real repository and defeats name-squatters — while Artifact
// Hub's official/verified flags only order candidates and break ties. Fetch
// targets come exclusively from the Artifact Hub listing (a tenant can never
// name one), every fetch passes the egress guard, and when in doubt at any
// step the package is left unresolved: exactly the pre-resolution behavior.

// deployed is the release-side identity the cluster recorded.
type deployed struct {
	name         string
	chartVersion string
	appVersion   string
	// identity holds the canonicalized home/source URLs of the deployed
	// chart, the release's claim about which project it packages.
	identity map[string]bool
}

func deployedFrom(r *kdeltav1.Resource) deployed {
	d := deployed{name: r.GetUpstream().GetName(), identity: map[string]bool{}}
	for _, stream := range r.GetStreams() {
		value := stream.GetCurrent().GetValue()
		switch stream.GetKind() {
		case kdeltav1.VersionKind_VERSION_KIND_CHART:
			if d.chartVersion == "" {
				d.chartVersion = value
			}
		case kdeltav1.VersionKind_VERSION_KIND_APP:
			if d.appVersion == "" {
				d.appVersion = value
			}
		}
	}
	for _, link := range r.GetLinks() {
		kind := link.GetKind()
		if kind != kdeltav1.LinkKind_LINK_KIND_HOMEPAGE && kind != kdeltav1.LinkKind_LINK_KIND_SOURCE {
			continue
		}
		if c, ok := canonicalURL(link.GetUrl()); ok {
			d.identity[c] = true
		}
	}
	return d
}

// canonicalURL reduces an http(s) URL to a comparable form: scheme dropped
// (http == https), host lowercased, trailing slash and fragment removed.
// Matching is on the exact canonical URL, never the bare host — host-level
// matching ("github.com") would relate nearly every chart to every other.
func canonicalURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	c := strings.ToLower(u.Host) + strings.TrimSuffix(u.Path, "/")
	if u.RawQuery != "" {
		c += "?" + u.RawQuery
	}
	return c, true
}

type matchLevel int

const (
	// noMatch: the candidate does not carry the deployed chart, or both
	// sides declare identity URLs and they disagree (hard reject).
	noMatch matchLevel = iota
	// versionMatch: the candidate carries the deployed version but identity
	// URLs are absent on at least one side, so identity is unverifiable.
	versionMatch
	// strongMatch: version match plus at least one identity-URL overlap.
	strongMatch
)

// verify grades one candidate repository's index against the deployed
// release.
func verify(d deployed, entries []upstream.HelmChartVersion) matchLevel {
	var entry *upstream.HelmChartVersion
	for i := range entries {
		if entries[i].Version == d.chartVersion {
			entry = &entries[i]
			break
		}
	}
	if entry == nil {
		return noMatch
	}
	if d.appVersion != "" && entry.AppVersion != "" && d.appVersion != entry.AppVersion {
		return noMatch
	}
	remote := map[string]bool{}
	if c, ok := canonicalURL(entry.Home); ok {
		remote[c] = true
	}
	for _, source := range entry.Sources {
		if c, ok := canonicalURL(source); ok {
			remote[c] = true
		}
	}
	if len(d.identity) == 0 || len(remote) == 0 {
		return versionMatch
	}
	for c := range remote {
		if d.identity[c] {
			return strongMatch
		}
	}
	return noMatch
}

// shortlist filters and orders Artifact Hub candidates: helm-kind, exact
// name, a fetchable http(s) repository URL, deduplicated, highest trust
// first, truncated to maxCandidateFetches.
func shortlist(c *upstream.Client, name string, packages []upstream.ArtifactHubPackage) []upstream.ArtifactHubPackage {
	seen := map[string]bool{}
	var kept []upstream.ArtifactHubPackage
	for _, p := range packages {
		if !p.HelmChart() || !strings.EqualFold(p.NormalizedName, name) {
			continue
		}
		canonical, ok := canonicalURL(p.Repository.URL)
		if !ok || seen[canonical] {
			continue
		}
		if c.CheckEgressURL(p.Repository.URL) != nil {
			continue
		}
		seen[canonical] = true
		kept = append(kept, p)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := trustKey(kept[i]), trustKey(kept[j])
		for t := range a {
			if a[t] != b[t] {
				return a[t] > b[t]
			}
		}
		return kept[i].Repository.URL < kept[j].Repository.URL
	})
	if len(kept) > maxCandidateFetches {
		kept = kept[:maxCandidateFetches]
	}
	return kept
}

// trustKey orders candidates by Artifact Hub's trust signals: official flag,
// verified publisher, production-use count.
func trustKey(p upstream.ArtifactHubPackage) [3]int {
	var k [3]int
	if p.Official || p.Repository.Official {
		k[0] = 1
	}
	if p.Repository.VerifiedPublisher {
		k[1] = 1
	}
	k[2] = p.ProductionOrganizationsCount
	return k
}

type verifiedCandidate struct {
	pkg   upstream.ArtifactHubPackage
	level matchLevel
}

// decide picks the winning candidate, or none. Strong matches (deployed
// metadata agrees with the candidate's index) win outright when unique;
// among several — an index mirrored by an impostor — only a strict trust
// dominator wins. Without any strong match, a deployed release that DID
// declare identity URLs stays unresolved (verification was possible and
// nothing earned it); only a metadata-bare release falls back to a single
// Artifact-Hub-official version match.
func decide(d deployed, results []verifiedCandidate) (upstream.ArtifactHubPackage, bool) {
	var strong, weak []verifiedCandidate
	for _, r := range results {
		switch r.level {
		case strongMatch:
			strong = append(strong, r)
		case versionMatch:
			weak = append(weak, r)
		}
	}
	switch {
	case len(strong) == 1:
		return strong[0].pkg, true
	case len(strong) > 1:
		sort.SliceStable(strong, func(i, j int) bool {
			a, b := trustKey(strong[i].pkg), trustKey(strong[j].pkg)
			for t := range a {
				if a[t] != b[t] {
					return a[t] > b[t]
				}
			}
			return false
		})
		if trustKey(strong[0].pkg) != trustKey(strong[1].pkg) {
			return strong[0].pkg, true
		}
		return upstream.ArtifactHubPackage{}, false
	case len(d.identity) > 0:
		return upstream.ArtifactHubPackage{}, false
	default:
		var officials []verifiedCandidate
		for _, r := range weak {
			if r.pkg.Official || r.pkg.Repository.Official {
				officials = append(officials, r)
			}
		}
		if len(officials) == 1 {
			return officials[0].pkg, true
		}
		return upstream.ArtifactHubPackage{}, false
	}
}

// resolveHelm resolves one Helm package to its verified chart repository
// URL. ok is false when no candidate earns the resolution.
func resolveHelm(ctx context.Context, c *upstream.Client, d deployed) (string, bool) {
	if d.chartVersion == "" {
		return "", false
	}
	packages, err := c.ArtifactHubSearchCharts(ctx, d.name)
	if err != nil {
		return "", false
	}
	var results []verifiedCandidate
	for _, p := range shortlist(c, d.name, packages) {
		entries, err := c.HelmChartVersions(ctx, p.Repository.URL, d.name)
		if err != nil {
			continue // a failed or blocked candidate fetch is a non-match
		}
		if level := verify(d, entries); level != noMatch {
			results = append(results, verifiedCandidate{pkg: p, level: level})
		}
	}
	winner, ok := decide(d, results)
	if !ok {
		return "", false
	}
	repoURL := strings.TrimSuffix(winner.Repository.URL, "/")
	if c.CheckEgressURL(repoURL) != nil {
		return "", false
	}
	return repoURL, true
}
