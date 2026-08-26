package upstream

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArtifactHubSearchCharts(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		fmt.Fprint(w, `{"packages":[
			{"name":"cert-manager","normalized_name":"cert-manager","official":true,
			 "production_organizations_count":41,
			 "repository":{"url":"https://charts.jetstack.io","name":"cert-manager","kind":0,
			               "verified_publisher":true,"official":true}},
			{"name":"cert-manager","normalized_name":"cert-manager",
			 "repository":{"url":"oci://ghcr.io/example/charts/cert-manager","name":"mirror","kind":0}}
		]}`)
	}))
	t.Cleanup(srv.Close)

	c := &Client{ArtifactHubBaseURL: srv.URL, InsecurePrivateEgress: true}
	packages, err := c.ArtifactHubSearchCharts(context.Background(), "cert-manager")
	if err != nil {
		t.Fatalf("ArtifactHubSearchCharts: %v", err)
	}
	if !strings.HasPrefix(gotPath, "/api/v1/packages/search?") || !strings.Contains(gotPath, "ts_query_web=cert-manager") {
		t.Errorf("request path = %q, want the search endpoint with ts_query_web", gotPath)
	}
	if len(packages) != 2 {
		t.Fatalf("got %d packages, want 2", len(packages))
	}
	first := packages[0]
	if first.Repository.URL != "https://charts.jetstack.io" || !first.Official ||
		!first.Repository.VerifiedPublisher || first.ProductionOrganizationsCount != 41 {
		t.Errorf("first package parsed as %+v", first)
	}
}

func TestHelmChartVersionsParsesIndexAndDropsUnsafeEntries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `entries:
  cert-manager:
  - version: v1.17.2
    appVersion: v1.17.2
    home: https://cert-manager.io
    sources:
    - https://github.com/cert-manager/cert-manager
  - version: "v1.0.0\x1b[31m"
    appVersion: v1.0.0
`)
	}))
	t.Cleanup(srv.Close)

	c := &Client{InsecurePrivateEgress: true}
	versions, err := c.HelmChartVersions(context.Background(), srv.URL, "cert-manager")
	if err != nil {
		t.Fatalf("HelmChartVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("got %d entries, want 1 (the control-character entry dropped)", len(versions))
	}
	v := versions[0]
	if v.Version != "v1.17.2" || v.Home != "https://cert-manager.io" ||
		len(v.Sources) != 1 || v.Sources[0] != "https://github.com/cert-manager/cert-manager" {
		t.Errorf("entry parsed as %+v", v)
	}

	if _, err := c.HelmChartVersions(context.Background(), srv.URL, "absent"); err == nil {
		t.Error("HelmChartVersions for an absent chart: want an error")
	}
}

func TestEgressGuard(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		insecure bool
		blocked  string // substring of the expected error; empty means allowed
	}{
		{name: "https public host", url: "https://charts.example.com/index.yaml"},
		{name: "http non-loopback", url: "http://charts.example.com/index.yaml", blocked: "not https"},
		{name: "metadata service", url: "https://169.254.169.254/latest/meta-data", blocked: "blocked range"},
		{name: "rfc1918 literal", url: "https://10.0.0.8/index.yaml", blocked: "blocked range"},
		{name: "loopback literal", url: "https://127.0.0.1/index.yaml", blocked: "blocked range"},
		{name: "userinfo", url: "https://user:pass@charts.example.com/", blocked: "userinfo"},
		{name: "non-http scheme", url: "ftp://charts.example.com/", blocked: "not http(s)"},
		{name: "insecure mode allows http loopback", url: "http://127.0.0.1:1234/index.yaml", insecure: true},
		{name: "insecure mode still requires http(s)", url: "javascript:alert(1)", insecure: true, blocked: "not http(s)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{InsecurePrivateEgress: tt.insecure}
			err := c.CheckEgressURL(tt.url)
			if tt.blocked == "" {
				if err != nil {
					t.Fatalf("CheckEgressURL(%q) = %v, want allowed", tt.url, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.blocked) {
				t.Fatalf("CheckEgressURL(%q) = %v, want error containing %q", tt.url, err, tt.blocked)
			}
		})
	}
}

// TestEgressGuardBlocksAtDialTime proves the guard rejects private
// destinations on the default client's dial path (the layer that also covers
// DNS names resolving to internal IPs) before any request is sent.
func TestEgressGuardBlocksAtDialTime(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("the guarded client reached a loopback server")
	}))
	t.Cleanup(srv.Close)

	c := &Client{}
	// Bypass the URL-layer literal-IP check by rewriting to a host the
	// URL check cannot classify; the dial still targets loopback.
	_, err := c.get(context.Background(), strings.Replace(srv.URL, "127.0.0.1", "localhost", 1), 1<<10, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to dial") {
		t.Fatalf("got %v, want a refusing-to-dial error", err)
	}
}
