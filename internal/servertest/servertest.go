// Package servertest provides a behavioral-test backend: a real kdelta
// server over HTTP with a seeded scan, a fake upstream forge, and a scripted
// agent runner. Tests drive the public surfaces (CLI, RPC) against it and
// assert user-visible behavior.
package servertest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"
	"k8s.io/client-go/rest"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/agent"
	"github.com/shadiramadan/kdelta/internal/detect"
	"github.com/shadiramadan/kdelta/internal/server"
	"github.com/shadiramadan/kdelta/internal/store"
	"github.com/shadiramadan/kdelta/internal/upstream"
)

// Backend is a served kdelta instance backed by fixtures.
type Backend struct {
	// URL of the running server (pass to --server / clients).
	URL string
	// Store is the backend's cache, pre-seeded with CertManagerResource.
	Store *store.Store
	// Runner is the scripted agent runner serving AI flows.
	Runner *ScriptedRunner
	// Forge counts the fake forge's requests so tests can assert cache
	// behavior (e.g. a second scan resolving without network calls).
	Forge *ForgeCounters
	// ChartRepoURL is the fake chart repository the forge's Artifact Hub
	// search resolves cert-manager to.
	ChartRepoURL string
}

// ForgeCounters counts requests served by the fake forge.
type ForgeCounters struct {
	// ArtifactHubSearches counts Artifact Hub search requests.
	ArtifactHubSearches atomic.Int32
	// IndexFetches counts chart-repository index.yaml requests.
	IndexFetches atomic.Int32
}

// CertManagerRef is the seeded resource's canonical ref.
const CertManagerRef = "helm:cert-manager/cert-manager"

// CertManagerResource models the demo cluster's cert-manager release as
// detected: a chart stream without a resolved source and an app stream
// backed by the fake forge. The seeded scan keeps this unresolved shape so
// the no-version-source paths stay covered; running a Scan against the
// backend resolves the chart stream through the forge's Artifact Hub and
// chart-repository fixtures.
func CertManagerResource() *kdeltav1.Resource {
	return &kdeltav1.Resource{
		Ref:         &kdeltav1.ResourceRef{Detector: "helm", Namespace: "cert-manager", Name: "cert-manager"},
		DisplayName: "cert-manager",
		Upstream:    &kdeltav1.PackageKey{System: "helm", Name: "cert-manager"},
		Links: []*kdeltav1.Link{
			{Kind: kdeltav1.LinkKind_LINK_KIND_SOURCE, Url: "https://github.com/cert-manager/cert-manager"},
			{Kind: kdeltav1.LinkKind_LINK_KIND_HOMEPAGE, Url: "https://cert-manager.io"},
		},
		Streams: []*kdeltav1.VersionStream{
			{
				Id:      "chart",
				Kind:    kdeltav1.VersionKind_VERSION_KIND_CHART,
				Scheme:  kdeltav1.VersioningScheme_VERSIONING_SCHEME_SEMVER,
				Current: &kdeltav1.DetectedVersion{Value: "v1.17.2"},
				// Like the real detector: release notes are known from the
				// chart's github source even while the version source awaits
				// resolution.
				Changes: []*kdeltav1.ChangeSource{{Source: &kdeltav1.ChangeSource_ReleaseNotes{
					ReleaseNotes: &kdeltav1.ReleaseNotesSource{RepositoryUrl: "https://github.com/cert-manager/cert-manager"},
				}}},
			},
			{
				Id:      "app",
				Kind:    kdeltav1.VersionKind_VERSION_KIND_APP,
				Scheme:  kdeltav1.VersioningScheme_VERSIONING_SCHEME_LOOSE_SEMVER,
				Current: &kdeltav1.DetectedVersion{Value: "v1.17.2"},
				Source: &kdeltav1.VersionSource{Source: &kdeltav1.VersionSource_GithubReleases{
					GithubReleases: &kdeltav1.GitHubReleasesSource{Owner: "cert-manager", Repo: "cert-manager"},
				}},
				Changes: []*kdeltav1.ChangeSource{{Source: &kdeltav1.ChangeSource_ReleaseNotes{
					ReleaseNotes: &kdeltav1.ReleaseNotesSource{RepositoryUrl: "https://github.com/cert-manager/cert-manager"},
				}}},
			},
		},
		Conditions: []*kdeltav1.Condition{{
			Type:   "Deployed",
			Status: kdeltav1.ConditionStatus_CONDITION_STATUS_TRUE,
			Reason: "deployed",
		}},
		Objects: []*kdeltav1.KubernetesObjectRef{{
			ApiVersion: "v1", Kind: "Secret",
			Namespace: "cert-manager", Name: "sh.helm.release.v1.cert-manager.v1",
		}},
	}
}

// fakeForge serves the seeded resource's upstream world from one server: a
// GitHub release history, an Artifact Hub search that resolves cert-manager
// to this server's own /charts repository, and that repository's index.
func fakeForge(t *testing.T) (*httptest.Server, *ForgeCounters) {
	t.Helper()
	counters := &ForgeCounters{}
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/cert-manager/cert-manager/releases", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			fmt.Fprint(w, "[]")
			return
		}
		fmt.Fprint(w, `[
			{"tag_name":"v1.21.1","body":"fixed renewal panic","html_url":"https://example.com/v1.21.1","published_at":"2026-07-29T00:00:00Z"},
			{"tag_name":"v1.18.0","body":"rotationPolicy default changed from Never to Always","html_url":"https://example.com/v1.18.0","published_at":"2025-06-10T00:00:00Z"},
			{"tag_name":"v1.17.2","body":"current","html_url":"https://example.com/v1.17.2","published_at":"2025-02-01T00:00:00Z"}
		]`)
	})
	mux.HandleFunc("/api/v1/packages/search", func(w http.ResponseWriter, _ *http.Request) {
		counters.ArtifactHubSearches.Add(1)
		fmt.Fprintf(w, `{"packages":[{"name":"cert-manager","normalized_name":"cert-manager","official":true,
			"production_organizations_count":41,
			"repository":{"url":"%s/charts","name":"cert-manager","kind":0,"verified_publisher":true,"official":true}}]}`,
			srv.URL)
	})
	mux.HandleFunc("/charts/index.yaml", func(w http.ResponseWriter, _ *http.Request) {
		counters.IndexFetches.Add(1)
		fmt.Fprint(w, `entries:
  cert-manager:
  - version: v1.21.1
    appVersion: v1.21.1
    home: https://cert-manager.io
    sources:
    - https://github.com/cert-manager/cert-manager
  - version: v1.18.0
    appVersion: v1.18.0
    home: https://cert-manager.io
    sources:
    - https://github.com/cert-manager/cert-manager
  - version: v1.17.2
    appVersion: v1.17.2
    home: https://cert-manager.io
    sources:
    - https://github.com/cert-manager/cert-manager
`)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, counters
}

// ScriptedRunner is a deterministic agent.Runner for behavioral tests.
type ScriptedRunner struct {
	ExtractCalls int
	AssessCalls  int
}

func (r *ScriptedRunner) ExtractChanges(_ context.Context, req agent.ExtractRequest) ([]*kdeltav1.VersionChanges, error) {
	r.ExtractCalls++
	out := make([]*kdeltav1.VersionChanges, 0, len(req.Releases))
	for _, release := range req.Releases {
		vc := &kdeltav1.VersionChanges{
			Version: release.Version,
			Changes: []*kdeltav1.Change{{
				Type:          kdeltav1.ChangeType_CHANGE_TYPE_DEFAULT_CHANGED,
				Summary:       "extracted from " + release.Version + ": " + release.Body,
				AffectedPaths: []string{"spec.privateKey.rotationPolicy"},
				Confidence:    0.9,
			}},
			Provenance: &kdeltav1.Provenance{
				Method: kdeltav1.ProvenanceMethod_PROVENANCE_METHOD_AI_EXTRACTED,
				Model:  "scripted",
			},
		}
		out = append(out, vc)
		if req.Partial != nil {
			req.Partial(vc)
		}
	}
	return out, nil
}

func (r *ScriptedRunner) AssessImpact(_ context.Context, req agent.AssessRequest) (*kdeltav1.ImpactAssessment, error) {
	r.AssessCalls++
	return &kdeltav1.ImpactAssessment{
		OverallSeverity: kdeltav1.ImpactSeverity_IMPACT_SEVERITY_HIGH,
		Summary:         "scripted assessment of " + req.FromVersion + " -> " + req.ToVersion,
		Resources: []*kdeltav1.ResourceImpact{{
			Ref:         &kdeltav1.ResourceRef{Detector: "impact", Namespace: "demo-ns-1", Name: "legacy-app-tls"},
			Severity:    kdeltav1.ImpactSeverity_IMPACT_SEVERITY_HIGH,
			Explanation: "rotationPolicy is unset, so the new Always default applies",
		}},
		Findings: []*kdeltav1.ImpactFinding{{
			Severity:   kdeltav1.ImpactSeverity_IMPACT_SEVERITY_HIGH,
			Likelihood: 0.9,
			Affected:   &kdeltav1.ResourceRef{Detector: "impact", Namespace: "demo-ns-1", Name: "legacy-app-tls"},
			Title:      "private key will rotate on next renewal",
			Rationale:  "the certificate omits spec.privateKey.rotationPolicy",
			Actions: []*kdeltav1.RecommendedAction{{
				Kind:          kdeltav1.ActionKind_ACTION_KIND_CONFIG_CHANGE,
				Description:   "set rotationPolicy explicitly",
				BeforeUpgrade: true,
			}},
		}},
		Gaps: []*kdeltav1.InformationGap{{Description: "values.yaml not captured"}},
	}, nil
}

// scriptedDetector re-emits the seeded resource on Scan.
type scriptedDetector struct{}

func (scriptedDetector) Name() string { return "helm" }

func (scriptedDetector) Detect(context.Context, detect.Request) (detect.Result, error) {
	return detect.Result{Resources: []*kdeltav1.Resource{CertManagerResource()}}, nil
}

// New starts a fully wired backend and seeds its cache with one scan.
func New(t *testing.T) *Backend {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "kdelta.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.SaveScan(context.Background(), &kdeltav1.ScanResponse{
		Resources: []*kdeltav1.Resource{CertManagerResource()},
		ScannedAt: timestamppb.Now(),
	}); err != nil {
		t.Fatalf("seeding scan: %v", err)
	}

	runner := &ScriptedRunner{}
	forge, counters := fakeForge(t)
	svc := server.NewKdeltaService(server.Deps{
		Store:      st,
		Detectors:  detect.NewRegistry(scriptedDetector{}),
		RESTConfig: func() (*rest.Config, error) { return &rest.Config{}, nil },
		Upstream: &upstream.Client{
			GitHubBaseURL:      forge.URL,
			ArtifactHubBaseURL: forge.URL,
			// The forge is an httptest server on loopback http.
			InsecurePrivateEgress: true,
		},
		Agent: runner,
	})
	srv := httptest.NewServer(server.Handler(svc))
	t.Cleanup(srv.Close)
	return &Backend{
		URL:          srv.URL,
		Store:        st,
		Runner:       runner,
		Forge:        counters,
		ChartRepoURL: forge.URL + "/charts",
	}
}
