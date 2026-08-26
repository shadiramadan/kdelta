package resolve

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/upstream"
)

func jetstackDeployed() deployed {
	return deployed{
		name:         "cert-manager",
		chartVersion: "v1.17.2",
		appVersion:   "v1.17.2",
		identity: map[string]bool{
			"cert-manager.io":                      true,
			"github.com/cert-manager/cert-manager": true,
		},
	}
}

func jetstackEntry() upstream.HelmChartVersion {
	return upstream.HelmChartVersion{
		Version:    "v1.17.2",
		AppVersion: "v1.17.2",
		Home:       "https://cert-manager.io",
		Sources:    []string{"https://github.com/cert-manager/cert-manager"},
	}
}

func TestVerify(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*deployed, *upstream.HelmChartVersion)
		want   matchLevel
	}{
		{name: "identity URLs overlap", mutate: func(*deployed, *upstream.HelmChartVersion) {}, want: strongMatch},
		{name: "chart version absent", mutate: func(d *deployed, _ *upstream.HelmChartVersion) {
			d.chartVersion = "v9.9.9"
		}, want: noMatch},
		{name: "appVersion conflict", mutate: func(_ *deployed, e *upstream.HelmChartVersion) {
			e.AppVersion = "v0.0.1"
		}, want: noMatch},
		{name: "identity disagreement is a hard reject", mutate: func(_ *deployed, e *upstream.HelmChartVersion) {
			e.Home = "https://evil.example"
			e.Sources = []string{"https://github.com/evil/mirror"}
		}, want: noMatch},
		{name: "bare deployed metadata degrades to version match", mutate: func(d *deployed, _ *upstream.HelmChartVersion) {
			d.identity = map[string]bool{}
		}, want: versionMatch},
		{name: "bare index metadata degrades to version match", mutate: func(_ *deployed, e *upstream.HelmChartVersion) {
			e.Home = ""
			e.Sources = nil
		}, want: versionMatch},
		{name: "scheme and trailing slash do not defeat the match", mutate: func(_ *deployed, e *upstream.HelmChartVersion) {
			e.Home = "http://cert-manager.io/"
			e.Sources = nil
		}, want: strongMatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, entry := jetstackDeployed(), jetstackEntry()
			tt.mutate(&d, &entry)
			if got := verify(d, []upstream.HelmChartVersion{entry}); got != tt.want {
				t.Errorf("verify = %v, want %v", got, tt.want)
			}
		})
	}
}

func pkg(repoURL string, official, verified bool, orgs int) upstream.ArtifactHubPackage {
	return upstream.ArtifactHubPackage{
		Name:                         "cert-manager",
		NormalizedName:               "cert-manager",
		Official:                     official,
		ProductionOrganizationsCount: orgs,
		Repository: upstream.ArtifactHubRepository{
			URL:               repoURL,
			Official:          official,
			VerifiedPublisher: verified,
		},
	}
}

func TestDecide(t *testing.T) {
	jetstack := pkg("https://charts.jetstack.io", true, true, 41)
	wikimedia := pkg("https://helm-charts.wikimedia.org/stable", false, false, 0)
	mirror := pkg("https://evil.example/charts", false, true, 0)

	withIdentity := jetstackDeployed()
	bare := jetstackDeployed()
	bare.identity = map[string]bool{}

	tests := []struct {
		name    string
		d       deployed
		results []verifiedCandidate
		wantURL string
		wantOK  bool
	}{
		{
			name: "single strong match wins regardless of flags (fork attribution)",
			d:    withIdentity,
			results: []verifiedCandidate{
				{pkg: wikimedia, level: strongMatch},
				{pkg: jetstack, level: versionMatch},
			},
			wantURL: wikimedia.Repository.URL, wantOK: true,
		},
		{
			name: "mirrored index: the strict trust dominator wins",
			d:    withIdentity,
			results: []verifiedCandidate{
				{pkg: mirror, level: strongMatch},
				{pkg: jetstack, level: strongMatch},
			},
			wantURL: jetstack.Repository.URL, wantOK: true,
		},
		{
			name: "mirrored index with equal trust stays unresolved",
			d:    withIdentity,
			results: []verifiedCandidate{
				{pkg: pkg("https://a.example", false, true, 0), level: strongMatch},
				{pkg: pkg("https://b.example", false, true, 0), level: strongMatch},
			},
			wantOK: false,
		},
		{
			name: "identity declared but never matched stays unresolved",
			d:    withIdentity,
			results: []verifiedCandidate{
				{pkg: jetstack, level: versionMatch},
			},
			wantOK: false,
		},
		{
			name: "bare metadata accepts a single official version match",
			d:    bare,
			results: []verifiedCandidate{
				{pkg: jetstack, level: versionMatch},
				{pkg: wikimedia, level: versionMatch},
			},
			wantURL: jetstack.Repository.URL, wantOK: true,
		},
		{
			name: "bare metadata with two officials stays unresolved",
			d:    bare,
			results: []verifiedCandidate{
				{pkg: jetstack, level: versionMatch},
				{pkg: pkg("https://other.example", true, true, 3), level: versionMatch},
			},
			wantOK: false,
		},
		{name: "no candidates", d: withIdentity, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			winner, ok := decide(tt.d, tt.results)
			if ok != tt.wantOK {
				t.Fatalf("decide ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && winner.Repository.URL != tt.wantURL {
				t.Errorf("decide winner = %q, want %q", winner.Repository.URL, tt.wantURL)
			}
		})
	}
}

func TestShortlistFiltersAndOrders(t *testing.T) {
	c := &upstream.Client{InsecurePrivateEgress: true}
	packages := []upstream.ArtifactHubPackage{
		pkg("https://low.example", false, false, 0),
		pkg("oci://ghcr.io/example/charts/cert-manager", false, true, 0), // no index.yaml
		pkg("https://charts.jetstack.io", true, true, 41),
		pkg("https://charts.jetstack.io/", true, true, 41), // duplicate after canonicalization
		{NormalizedName: "not-cert-manager", Repository: upstream.ArtifactHubRepository{URL: "https://other.example"}},
	}
	kept := shortlist(c, "cert-manager", packages)
	if len(kept) != 2 {
		t.Fatalf("shortlist kept %d candidates, want 2 (oci, duplicate, and other-name dropped): %+v", len(kept), kept)
	}
	if kept[0].Repository.URL != "https://charts.jetstack.io" {
		t.Errorf("highest-trust candidate = %q, want charts.jetstack.io first", kept[0].Repository.URL)
	}
}

// fakeCache records puts and serves canned entries.
type fakeCache struct {
	entries map[string]string // name -> url ("" = fresh negative)
	puts    map[string]string
}

func (f *fakeCache) Resolution(_ context.Context, _, name string) (string, bool) {
	url, ok := f.entries[name]
	return url, ok
}

func (f *fakeCache) PutResolution(_ context.Context, _, name, url string) {
	if f.puts == nil {
		f.puts = map[string]string{}
	}
	f.puts[name] = url
}

func helmResource(name, chartVersion string) *kdeltav1.Resource {
	return &kdeltav1.Resource{
		Ref:      &kdeltav1.ResourceRef{Detector: "helm", Namespace: "ns", Name: name},
		Upstream: &kdeltav1.PackageKey{System: "helm", Name: name},
		Links: []*kdeltav1.Link{
			{Kind: kdeltav1.LinkKind_LINK_KIND_HOMEPAGE, Url: "https://cert-manager.io"},
			{Kind: kdeltav1.LinkKind_LINK_KIND_SOURCE, Url: "https://github.com/cert-manager/cert-manager"},
		},
		Streams: []*kdeltav1.VersionStream{{
			Id:      "chart",
			Kind:    kdeltav1.VersionKind_VERSION_KIND_CHART,
			Current: &kdeltav1.DetectedVersion{Value: chartVersion},
		}},
	}
}

// fakeHub serves an Artifact Hub search naming this server's own /charts
// repo, and the repo's index.yaml.
func fakeHub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/api/v1/packages/search", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"packages":[{"name":"cert-manager","normalized_name":"cert-manager","official":true,
			"repository":{"url":"%s/charts","name":"cert-manager","kind":0,"verified_publisher":true,"official":true}}]}`,
			srv.URL)
	})
	mux.HandleFunc("/charts/index.yaml", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `entries:
  cert-manager:
  - version: v1.17.2
    home: https://cert-manager.io
    sources:
    - https://github.com/cert-manager/cert-manager
`)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestUpstreamsResolvesAndCaches(t *testing.T) {
	hub := fakeHub(t)
	client := &upstream.Client{ArtifactHubBaseURL: hub.URL, InsecurePrivateEgress: true}
	cache := &fakeCache{}
	resource := helmResource("cert-manager", "v1.17.2")

	Upstreams(context.Background(), Deps{Upstream: client, Cache: cache}, []*kdeltav1.Resource{resource})

	wantRepo := hub.URL + "/charts"
	if got := resource.GetUpstream().GetRegistryUrl(); got != wantRepo {
		t.Errorf("registry_url = %q, want %q", got, wantRepo)
	}
	source := resource.GetStreams()[0].GetSource().GetHelmRepository()
	if source.GetRepositoryUrl() != wantRepo || source.GetChart() != "cert-manager" {
		t.Errorf("chart source = %v, want helm repo %s", source, wantRepo)
	}
	provenance := resource.GetStreams()[0].GetSourceProvenance()
	if provenance.GetMethod() != kdeltav1.ProvenanceMethod_PROVENANCE_METHOD_FETCHED {
		t.Errorf("source provenance = %v, want FETCHED", provenance)
	}
	if cache.puts["cert-manager"] != wantRepo {
		t.Errorf("cached resolution = %q, want %q", cache.puts["cert-manager"], wantRepo)
	}
}

func TestUpstreamsIsBestEffort(t *testing.T) {
	t.Run("no upstream client", func(t *testing.T) {
		resource := helmResource("cert-manager", "v1.17.2")
		Upstreams(context.Background(), Deps{}, []*kdeltav1.Resource{resource})
		if resource.GetUpstream().GetRegistryUrl() != "" || resource.GetStreams()[0].GetSource() != nil {
			t.Error("resource mutated without an upstream client")
		}
	})

	t.Run("artifact hub down", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)
		client := &upstream.Client{ArtifactHubBaseURL: srv.URL, InsecurePrivateEgress: true}
		cache := &fakeCache{}
		resource := helmResource("cert-manager", "v1.17.2")
		Upstreams(context.Background(), Deps{Upstream: client, Cache: cache}, []*kdeltav1.Resource{resource})
		if resource.GetStreams()[0].GetSource() != nil {
			t.Error("resource resolved against a failing Artifact Hub")
		}
		if url, ok := cache.puts["cert-manager"]; !ok || url != "" {
			t.Errorf("failed resolution not negative-cached: %q, %v", url, ok)
		}
	})

	t.Run("fresh negative skips the network", func(t *testing.T) {
		var hits int
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
		t.Cleanup(srv.Close)
		client := &upstream.Client{ArtifactHubBaseURL: srv.URL, InsecurePrivateEgress: true}
		cache := &fakeCache{entries: map[string]string{"cert-manager": ""}}
		Upstreams(context.Background(), Deps{Upstream: client, Cache: cache}, []*kdeltav1.Resource{helmResource("cert-manager", "v1.17.2")})
		if hits != 0 {
			t.Errorf("fresh negative still made %d network requests", hits)
		}
	})

	t.Run("cache hit applies without the network", func(t *testing.T) {
		client := &upstream.Client{ArtifactHubBaseURL: "https://unreachable.invalid", InsecurePrivateEgress: true}
		cache := &fakeCache{entries: map[string]string{"cert-manager": "https://charts.jetstack.io"}}
		resource := helmResource("cert-manager", "v1.17.2")
		Upstreams(context.Background(), Deps{Upstream: client, Cache: cache}, []*kdeltav1.Resource{resource})
		if resource.GetUpstream().GetRegistryUrl() != "https://charts.jetstack.io" {
			t.Errorf("cache hit not applied: %q", resource.GetUpstream().GetRegistryUrl())
		}
	})

	t.Run("nil upstream key and resolved resources are skipped", func(t *testing.T) {
		var hits int
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
		t.Cleanup(srv.Close)
		client := &upstream.Client{ArtifactHubBaseURL: srv.URL, InsecurePrivateEgress: true}
		noKey := &kdeltav1.Resource{Ref: &kdeltav1.ResourceRef{Detector: "helm", Namespace: "ns", Name: "bare"}}
		already := helmResource("cert-manager", "v1.17.2")
		already.Upstream.RegistryUrl = "https://charts.jetstack.io"
		Upstreams(context.Background(), Deps{Upstream: client}, []*kdeltav1.Resource{noKey, already})
		if hits != 0 {
			t.Errorf("ineligible resources still made %d network requests", hits)
		}
	})
}
