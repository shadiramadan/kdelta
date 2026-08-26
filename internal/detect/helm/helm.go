// Package helm detects installed Helm releases by reading Helm's release
// storage (Secrets of type helm.sh/release.v1) and emits one resource per
// release with chart and app version streams.
//
// SECURITY: this detector is the reason deployed kdelta needs secret read
// access (RBAC cannot filter secrets by type). It decodes only Helm-typed
// release payloads — the storage driver's label filter selects them — and
// must never surface other secret data.
package helm

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/client-go/kubernetes"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/detect"
)

// Detector discovers Helm releases.
type Detector struct {
	// clientset overrides the cluster connection; nil builds one from the
	// request's rest.Config (tests inject a fake here).
	clientset kubernetes.Interface
}

func New() *Detector { return &Detector{} }

// NewWithClient injects the cluster client, for tests.
func NewWithClient(client kubernetes.Interface) *Detector {
	return &Detector{clientset: client}
}

func (d *Detector) Name() string { return "helm" }

func (d *Detector) Detect(ctx context.Context, req detect.Request) (detect.Result, error) {
	client := d.clientset
	if client == nil {
		var err error
		if client, err = kubernetes.NewForConfig(req.Config); err != nil {
			return detect.Result{}, fmt.Errorf("building cluster client: %w", err)
		}
	}

	namespaces := req.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{""} // all namespaces
	}
	latest := map[string]*release.Release{}
	for _, namespace := range namespaces {
		if err := ctx.Err(); err != nil {
			return detect.Result{}, err
		}
		// The driver lists only Helm-owned release secrets and decodes every
		// stored revision; keep the newest revision per release.
		releases, err := driver.NewSecrets(client.CoreV1().Secrets(namespace)).List(func(*release.Release) bool { return true })
		if err != nil {
			return detect.Result{}, fmt.Errorf("listing releases in %q: %w", namespace, err)
		}
		for _, rel := range releases {
			key := rel.Namespace + "/" + rel.Name
			if current, ok := latest[key]; !ok || rel.Version > current.Version {
				latest[key] = rel
			}
		}
	}

	result := detect.Result{}
	for _, rel := range latest {
		result.Resources = append(result.Resources, resourceFromRelease(rel))
	}
	return result, nil
}

func resourceFromRelease(rel *release.Release) *kdeltav1.Resource {
	meta := rel.Chart.Metadata
	provenance := &kdeltav1.Provenance{
		Method:      kdeltav1.ProvenanceMethod_PROVENANCE_METHOD_OBSERVED,
		Detector:    "helm",
		CollectedAt: timestamppb.New(time.Now()),
	}

	// The chart's source links usually name the application repository —
	// enough to enumerate app versions and read release notes upstream.
	owner, repo, hasGitHub := githubRepo(meta.Sources)

	chart := &kdeltav1.VersionStream{
		Id:     "chart",
		Kind:   kdeltav1.VersionKind_VERSION_KIND_CHART,
		Scheme: kdeltav1.VersioningScheme_VERSIONING_SCHEME_SEMVER,
		Current: &kdeltav1.DetectedVersion{
			Value:      meta.Version,
			Provenance: provenance,
		},
	}
	if hasGitHub {
		// The chart stream has no version source until the chart repository
		// is resolved downstream, but its release notes live in the same
		// repository: single-repo charts tag releases with the chart version,
		// and for divergent versioning the range join finds no releases and
		// reports that honestly.
		chart.Changes = releaseNotesChain(owner, repo)
	}
	streams := []*kdeltav1.VersionStream{chart}

	if meta.AppVersion != "" {
		app := &kdeltav1.VersionStream{
			Id:     "app",
			Kind:   kdeltav1.VersionKind_VERSION_KIND_APP,
			Scheme: kdeltav1.VersioningScheme_VERSIONING_SCHEME_LOOSE_SEMVER,
			Current: &kdeltav1.DetectedVersion{
				Value:      meta.AppVersion,
				Provenance: provenance,
			},
		}
		if hasGitHub {
			app.Source = &kdeltav1.VersionSource{
				Source: &kdeltav1.VersionSource_GithubReleases{
					GithubReleases: &kdeltav1.GitHubReleasesSource{Owner: owner, Repo: repo},
				},
			}
			app.SourceProvenance = provenance
			app.Changes = releaseNotesChain(owner, repo)
		}
		streams = append(streams, app)
	}

	// Chart metadata (sources, home) is attacker-controllable: any tenant who
	// can install a release supplies it. Only surface http(s) links so a
	// javascript:/data: URL can never reach a rendered href in the UI or a
	// terminal hyperlink.
	links := make([]*kdeltav1.Link, 0, len(meta.Sources)+1)
	for _, source := range meta.Sources {
		if isSafeHTTPURL(source) {
			links = append(links, &kdeltav1.Link{
				Kind: kdeltav1.LinkKind_LINK_KIND_SOURCE,
				Url:  source,
			})
		}
	}
	if isSafeHTTPURL(meta.Home) {
		links = append(links, &kdeltav1.Link{
			Kind: kdeltav1.LinkKind_LINK_KIND_HOMEPAGE,
			Url:  meta.Home,
		})
	}

	deployed := kdeltav1.ConditionStatus_CONDITION_STATUS_FALSE
	if rel.Info.Status == release.StatusDeployed {
		deployed = kdeltav1.ConditionStatus_CONDITION_STATUS_TRUE
	}

	return &kdeltav1.Resource{
		Ref: &kdeltav1.ResourceRef{
			Detector:  "helm",
			Namespace: rel.Namespace,
			Name:      rel.Name,
		},
		DisplayName: rel.Name,
		Upstream: &kdeltav1.PackageKey{
			System: "helm",
			Name:   meta.Name,
			// Installed releases do not record their chart repository;
			// resolution happens downstream (dataset, index lookups).
		},
		Links:   links,
		Streams: streams,
		Objects: []*kdeltav1.KubernetesObjectRef{{
			ApiVersion: "v1",
			Kind:       "Secret",
			Namespace:  rel.Namespace,
			Name:       fmt.Sprintf("sh.helm.release.v1.%s.v%d", rel.Name, rel.Version),
		}},
		Conditions: []*kdeltav1.Condition{{
			Type:   "Deployed",
			Status: deployed,
			Reason: string(rel.Info.Status),
		}},
		Provenance: provenance,
	}
}

// releaseNotesChain is the change-source chain for a stream whose release
// notes are published on the GitHub repository's releases page.
func releaseNotesChain(owner, repo string) []*kdeltav1.ChangeSource {
	return []*kdeltav1.ChangeSource{{
		Source: &kdeltav1.ChangeSource_ReleaseNotes{
			ReleaseNotes: &kdeltav1.ReleaseNotesSource{
				RepositoryUrl: "https://github.com/" + owner + "/" + repo,
			},
		},
	}}
}

// isSafeHTTPURL reports whether raw parses as an absolute http(s) URL with a
// host. It gates attacker-controllable chart metadata before it becomes a
// rendered link (guards against javascript:/data: and other script schemes).
func isSafeHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// githubRepo extracts owner/repo from the first chart source hosted on
// github.com.
func githubRepo(sources []string) (owner, repo string, ok bool) {
	for _, source := range sources {
		u, err := url.Parse(source)
		if err != nil || u.Host != "github.com" {
			continue
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], true
		}
	}
	return "", "", false
}
