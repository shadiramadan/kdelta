package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"k8s.io/client-go/rest"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/gen/kdelta/v1/kdeltav1connect"
	"github.com/shadiramadan/kdelta/internal/detect"
	"github.com/shadiramadan/kdelta/internal/store"
	"github.com/shadiramadan/kdelta/internal/upstream"
)

// unresolvedHelmResource is detector-shaped output: helm system, empty
// registry URL, sourceless chart stream, identity links.
func unresolvedHelmResource() *kdeltav1.Resource {
	return &kdeltav1.Resource{
		Ref:         &kdeltav1.ResourceRef{Detector: "helm", Namespace: "cert-manager", Name: "cert-manager"},
		DisplayName: "cert-manager",
		Upstream:    &kdeltav1.PackageKey{System: "helm", Name: "cert-manager"},
		Links: []*kdeltav1.Link{
			{Kind: kdeltav1.LinkKind_LINK_KIND_HOMEPAGE, Url: "https://cert-manager.io"},
			{Kind: kdeltav1.LinkKind_LINK_KIND_SOURCE, Url: "https://github.com/cert-manager/cert-manager"},
		},
		Streams: []*kdeltav1.VersionStream{{
			Id:      "chart",
			Kind:    kdeltav1.VersionKind_VERSION_KIND_CHART,
			Scheme:  kdeltav1.VersioningScheme_VERSIONING_SCHEME_SEMVER,
			Current: &kdeltav1.DetectedVersion{Value: "v1.17.2"},
		}},
	}
}

// fakeArtifactHub serves a search resolving cert-manager to this server's
// own /charts repository, counting requests.
func fakeArtifactHub(t *testing.T, indexYAML string) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var searches, indexes atomic.Int32
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/api/v1/packages/search", func(w http.ResponseWriter, _ *http.Request) {
		searches.Add(1)
		fmt.Fprintf(w, `{"packages":[{"name":"cert-manager","normalized_name":"cert-manager","official":true,
			"repository":{"url":"%s/charts","name":"cert-manager","kind":0,"verified_publisher":true,"official":true}}]}`,
			srv.URL)
	})
	mux.HandleFunc("/charts/index.yaml", func(w http.ResponseWriter, _ *http.Request) {
		indexes.Add(1)
		fmt.Fprint(w, indexYAML)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &searches, &indexes
}

const matchingIndexYAML = `entries:
  cert-manager:
  - version: v1.17.2
    home: https://cert-manager.io
    sources:
    - https://github.com/cert-manager/cert-manager
`

func newResolveTestClient(t *testing.T, hubURL string) kdeltav1connect.KdeltaServiceClient {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "kdelta.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	svc := NewKdeltaService(Deps{
		Store: st,
		Detectors: detect.NewRegistry(fakeDetector{result: detect.Result{
			Resources: []*kdeltav1.Resource{unresolvedHelmResource()},
		}}),
		RESTConfig: func() (*rest.Config, error) { return &rest.Config{}, nil },
		Upstream:   &upstream.Client{ArtifactHubBaseURL: hubURL, InsecurePrivateEgress: true},
	})
	srv := httptest.NewServer(Handler(svc))
	t.Cleanup(srv.Close)
	return kdeltav1connect.NewKdeltaServiceClient(srv.Client(), srv.URL+RPCPrefix)
}

func TestScanResolvesHelmChartRepo(t *testing.T) {
	hub, _, _ := fakeArtifactHub(t, matchingIndexYAML)
	client := newResolveTestClient(t, hub.URL)

	scan, err := client.Scan(context.Background(), connect.NewRequest(&kdeltav1.ScanRequest{}))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	resource := scan.Msg.GetResources()[0]
	wantRepo := hub.URL + "/charts"
	if got := resource.GetUpstream().GetRegistryUrl(); got != wantRepo {
		t.Errorf("registry_url = %q, want %q", got, wantRepo)
	}
	chart := resource.GetStreams()[0]
	if chart.GetSource().GetHelmRepository().GetRepositoryUrl() != wantRepo {
		t.Errorf("chart source = %v, want helm repo %s", chart.GetSource(), wantRepo)
	}
	if chart.GetSourceProvenance().GetMethod() != kdeltav1.ProvenanceMethod_PROVENANCE_METHOD_FETCHED {
		t.Errorf("source provenance = %v, want FETCHED", chart.GetSourceProvenance())
	}

	// The resolved shape is what was cached: GetResource serves it back.
	got, err := client.GetResource(context.Background(), connect.NewRequest(&kdeltav1.GetResourceRequest{
		Ref: resource.GetRef(),
	}))
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if got.Msg.GetResource().GetUpstream().GetRegistryUrl() != wantRepo {
		t.Errorf("cached resource registry_url = %q, want %q", got.Msg.GetResource().GetUpstream().GetRegistryUrl(), wantRepo)
	}
}

func TestScanReusesCachedResolution(t *testing.T) {
	hub, searches, indexes := fakeArtifactHub(t, matchingIndexYAML)
	client := newResolveTestClient(t, hub.URL)
	ctx := context.Background()

	if _, err := client.Scan(ctx, connect.NewRequest(&kdeltav1.ScanRequest{})); err != nil {
		t.Fatalf("first Scan: %v", err)
	}
	gotSearches, gotIndexes := searches.Load(), indexes.Load()
	if gotSearches != 1 || gotIndexes != 1 {
		t.Fatalf("first scan made %d searches and %d index fetches, want 1 and 1", gotSearches, gotIndexes)
	}

	scan, err := client.Scan(ctx, connect.NewRequest(&kdeltav1.ScanRequest{}))
	if err != nil {
		t.Fatalf("second Scan: %v", err)
	}
	if searches.Load() != gotSearches || indexes.Load() != gotIndexes {
		t.Errorf("second scan hit the network (%d searches, %d index fetches); want the cached resolution",
			searches.Load(), indexes.Load())
	}
	if scan.Msg.GetResources()[0].GetUpstream().GetRegistryUrl() == "" {
		t.Error("second scan lost the cached resolution")
	}
}

func TestScanResolutionIsBestEffort(t *testing.T) {
	t.Run("artifact hub down leaves the resource unresolved", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)
		client := newResolveTestClient(t, srv.URL)

		scan, err := client.Scan(context.Background(), connect.NewRequest(&kdeltav1.ScanRequest{}))
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		resource := scan.Msg.GetResources()[0]
		if resource.GetUpstream().GetRegistryUrl() != "" || resource.GetStreams()[0].GetSource() != nil {
			t.Errorf("resource resolved against a failing Artifact Hub: %v", resource.GetUpstream())
		}
	})

	t.Run("verification mismatch stays unresolved and is negative-cached", func(t *testing.T) {
		hub, searches, _ := fakeArtifactHub(t, `entries:
  cert-manager:
  - version: v1.17.2
    home: https://evil.example
    sources:
    - https://github.com/evil/mirror
`)
		client := newResolveTestClient(t, hub.URL)
		ctx := context.Background()

		scan, err := client.Scan(ctx, connect.NewRequest(&kdeltav1.ScanRequest{}))
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if scan.Msg.GetResources()[0].GetStreams()[0].GetSource() != nil {
			t.Error("identity-mismatched candidate was accepted")
		}
		first := searches.Load()
		if _, err := client.Scan(ctx, connect.NewRequest(&kdeltav1.ScanRequest{})); err != nil {
			t.Fatalf("second Scan: %v", err)
		}
		if searches.Load() != first {
			t.Errorf("failed resolution retried on the next scan; want the negative cache to hold")
		}
	})
}
