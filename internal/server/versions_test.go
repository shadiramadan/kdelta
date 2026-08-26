package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/gen/kdelta/v1/kdeltav1connect"
	"github.com/shadiramadan/kdelta/internal/agent"
	"github.com/shadiramadan/kdelta/internal/store"
	"github.com/shadiramadan/kdelta/internal/upstream"
)

// fakeGitHub serves a minimal releases API for cert-manager-like history.
func fakeGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/cert-manager/cert-manager/releases" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("page") != "1" {
			fmt.Fprint(w, "[]")
			return
		}
		fmt.Fprint(w, `[
			{"tag_name":"v1.22.0-alpha.0","prerelease":true,"body":"alpha notes","html_url":"https://example.com/v1.22.0-alpha.0","published_at":"2026-08-01T00:00:00Z"},
			{"tag_name":"v1.21.1","body":"fixed renewal panic","html_url":"https://example.com/v1.21.1","published_at":"2026-07-29T00:00:00Z"},
			{"tag_name":"v1.21.0","body":"helm schema validation tightened","html_url":"https://example.com/v1.21.0","published_at":"2026-07-08T00:00:00Z"},
			{"tag_name":"v1.18.0","body":"rotationPolicy default changed from Never to Always","html_url":"https://example.com/v1.18.0","published_at":"2025-06-10T00:00:00Z"},
			{"tag_name":"v1.17.2","body":"current","html_url":"https://example.com/v1.17.2","published_at":"2025-02-01T00:00:00Z"},
			{"tag_name":"v1.17.1","body":"old","html_url":"https://example.com/v1.17.1","published_at":"2025-01-01T00:00:00Z"},
			{"tag_name":"draft","draft":true,"body":"draft"}
		]`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func certManagerResource() *kdeltav1.Resource {
	return &kdeltav1.Resource{
		Ref:      &kdeltav1.ResourceRef{Detector: "helm", Namespace: "cert-manager", Name: "cert-manager"},
		Upstream: &kdeltav1.PackageKey{System: "helm", Name: "cert-manager"},
		Streams: []*kdeltav1.VersionStream{
			{
				Id:      "chart",
				Kind:    kdeltav1.VersionKind_VERSION_KIND_CHART,
				Scheme:  kdeltav1.VersioningScheme_VERSIONING_SCHEME_SEMVER,
				Current: &kdeltav1.DetectedVersion{Value: "v1.17.2"},
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
	}
}

type fakeRunner struct {
	extractCalls int
	assessCalls  int
}

func (f *fakeRunner) ExtractChanges(_ context.Context, req agent.ExtractRequest) ([]*kdeltav1.VersionChanges, error) {
	f.extractCalls++
	out := make([]*kdeltav1.VersionChanges, 0, len(req.Releases))
	for _, release := range req.Releases {
		out = append(out, &kdeltav1.VersionChanges{
			Version: release.Version,
			Changes: []*kdeltav1.Change{{
				Type:    kdeltav1.ChangeType_CHANGE_TYPE_DEFAULT_CHANGED,
				Summary: "extracted: " + release.Version,
			}},
		})
	}
	return out, nil
}

func (f *fakeRunner) AssessImpact(_ context.Context, req agent.AssessRequest) (*kdeltav1.ImpactAssessment, error) {
	f.assessCalls++
	return &kdeltav1.ImpactAssessment{
		OverallSeverity: kdeltav1.ImpactSeverity_IMPACT_SEVERITY_HIGH,
		Summary:         "assessed " + req.FromVersion + " -> " + req.ToVersion,
		Resources: []*kdeltav1.ResourceImpact{{
			Ref:         req.Target,
			Severity:    kdeltav1.ImpactSeverity_IMPACT_SEVERITY_HIGH,
			Explanation: "the target itself upgrades",
		}},
	}, nil
}

func newVersionsTestClient(t *testing.T, runner agent.Runner) (kdeltav1connect.KdeltaServiceClient, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "kdelta.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.SaveScan(context.Background(), &kdeltav1.ScanResponse{
		Resources: []*kdeltav1.Resource{certManagerResource()},
		ScannedAt: timestamppb.Now(),
	}); err != nil {
		t.Fatalf("seeding scan: %v", err)
	}

	github := fakeGitHub(t)
	svc := NewKdeltaService(Deps{
		Store:    st,
		Upstream: &upstream.Client{GitHubBaseURL: github.URL, InsecurePrivateEgress: true},
		Agent:    runner,
	})
	srv := httptest.NewServer(Handler(svc))
	t.Cleanup(srv.Close)
	return kdeltav1connect.NewKdeltaServiceClient(srv.Client(), srv.URL+RPCPrefix), st
}

func TestListVersions(t *testing.T) {
	client, st := newVersionsTestClient(t, nil)
	ctx := context.Background()

	res, err := client.ListVersions(ctx, connect.NewRequest(&kdeltav1.ListVersionsRequest{
		Ref:      &kdeltav1.ResourceRef{Detector: "helm", Namespace: "cert-manager", Name: "cert-manager"},
		StreamId: "app",
	}))
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if res.Msg.GetCurrent() != "v1.17.2" || res.Msg.GetLatest() != "v1.21.1" {
		t.Errorf("current/latest = %s/%s, want v1.17.2/v1.21.1", res.Msg.GetCurrent(), res.Msg.GetLatest())
	}
	if res.Msg.GetVersionsBehind() != 3 {
		t.Errorf("versions_behind = %d, want 3 (stable releases only)", res.Msg.GetVersionsBehind())
	}
	var values []string
	for _, v := range res.Msg.GetVersions() {
		values = append(values, v.GetValue())
	}
	want := []string{"v1.17.2", "v1.18.0", "v1.21.0", "v1.21.1", "v1.22.0-alpha.0"}
	if fmt.Sprint(values) != fmt.Sprint(want) {
		t.Errorf("versions = %v, want %v (ascending from current)", values, want)
	}

	// The full list is cached for the package/kind.
	if _, err := st.VersionList(ctx, &kdeltav1.PackageKey{System: "helm", Name: "cert-manager"},
		kdeltav1.VersionKind_VERSION_KIND_APP, versionCacheTTL); err != nil {
		t.Errorf("version list not cached: %v", err)
	}

	// The chart stream has no source and must say so clearly.
	_, err = client.ListVersions(ctx, connect.NewRequest(&kdeltav1.ListVersionsRequest{
		Ref: &kdeltav1.ResourceRef{Detector: "helm", Namespace: "cert-manager", Name: "cert-manager"},
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("primary (chart) stream error = %v, want FailedPrecondition", err)
	}
}

func collectChanges(t *testing.T, client kdeltav1connect.KdeltaServiceClient, req *kdeltav1.GetChangesRequest) (*kdeltav1.ChangeSet, []string) {
	t.Helper()
	stream, err := client.GetChanges(context.Background(), connect.NewRequest(req))
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	defer stream.Close()
	var stages []string
	var result *kdeltav1.ChangeSet
	for stream.Receive() {
		switch event := stream.Msg().GetEvent().(type) {
		case *kdeltav1.GetChangesResponse_Progress:
			stages = append(stages, event.Progress.GetStage())
		case *kdeltav1.GetChangesResponse_Result:
			result = event.Result
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("GetChanges stream: %v", err)
	}
	if result == nil {
		t.Fatal("GetChanges produced no result")
	}
	return result, stages
}

func TestGetChangesVerbatimWithoutAgent(t *testing.T) {
	client, _ := newVersionsTestClient(t, nil)

	set, _ := collectChanges(t, client, &kdeltav1.GetChangesRequest{
		Ref:      &kdeltav1.ResourceRef{Detector: "helm", Namespace: "cert-manager", Name: "cert-manager"},
		StreamId: "app",
	})
	if set.GetFromVersion() != "v1.17.2" || set.GetToVersion() != "v1.21.1" {
		t.Errorf("range = %s..%s, want v1.17.2..v1.21.1 (defaults)", set.GetFromVersion(), set.GetToVersion())
	}
	if len(set.GetVersions()) != 3 {
		t.Fatalf("got %d versions, want 3 (v1.18.0, v1.21.0, v1.21.1)", len(set.GetVersions()))
	}
	first := set.GetVersions()[0]
	if first.GetVersion() != "v1.18.0" {
		t.Errorf("first version = %s, want v1.18.0 (ascending)", first.GetVersion())
	}
	if first.GetProvenance().GetMethod() != kdeltav1.ProvenanceMethod_PROVENANCE_METHOD_FETCHED {
		t.Errorf("provenance = %v, want FETCHED (verbatim fallback)", first.GetProvenance().GetMethod())
	}
	if first.GetChanges()[0].GetDetail() != "rotationPolicy default changed from Never to Always" {
		t.Errorf("verbatim body lost: %q", first.GetChanges()[0].GetDetail())
	}
}

func TestGetChangesExtractsAndCaches(t *testing.T) {
	runner := &fakeRunner{}
	client, _ := newVersionsTestClient(t, runner)
	req := &kdeltav1.GetChangesRequest{
		Ref:         &kdeltav1.ResourceRef{Detector: "helm", Namespace: "cert-manager", Name: "cert-manager"},
		StreamId:    "app",
		FromVersion: "v1.17.2",
		ToVersion:   "v1.21.1",
	}

	set, _ := collectChanges(t, client, req)
	if runner.extractCalls != 1 {
		t.Fatalf("extract calls = %d, want 1", runner.extractCalls)
	}
	if got := set.GetVersions()[0].GetChanges()[0].GetType(); got != kdeltav1.ChangeType_CHANGE_TYPE_DEFAULT_CHANGED {
		t.Errorf("extracted change type = %v, want DEFAULT_CHANGED", got)
	}

	_, stages := collectChanges(t, client, req)
	if runner.extractCalls != 1 {
		t.Errorf("extract calls after cached fetch = %d, want still 1", runner.extractCalls)
	}
	if len(stages) == 0 || stages[0] != "cache" {
		t.Errorf("second call stages = %v, want cache hit", stages)
	}
}

func TestAssessImpactCachesPerScan(t *testing.T) {
	runner := &fakeRunner{}
	client, _ := newVersionsTestClient(t, runner)
	req := &kdeltav1.AssessImpactRequest{
		Ref:         &kdeltav1.ResourceRef{Detector: "helm", Namespace: "cert-manager", Name: "cert-manager"},
		StreamId:    "app",
		FromVersion: "v1.17.2",
		ToVersion:   "v1.21.1",
	}

	run := func() *kdeltav1.ImpactAssessment {
		stream, err := client.AssessImpact(context.Background(), connect.NewRequest(req))
		if err != nil {
			t.Fatalf("AssessImpact: %v", err)
		}
		defer stream.Close()
		var result *kdeltav1.ImpactAssessment
		for stream.Receive() {
			if event, ok := stream.Msg().GetEvent().(*kdeltav1.AssessImpactResponse_Result); ok {
				result = event.Result
			}
		}
		if err := stream.Err(); err != nil {
			t.Fatalf("AssessImpact stream: %v", err)
		}
		if result == nil {
			t.Fatal("no assessment result")
		}
		return result
	}

	first := run()
	if runner.assessCalls != 1 {
		t.Fatalf("assess calls = %d, want 1", runner.assessCalls)
	}
	if first.GetOverallSeverity() != kdeltav1.ImpactSeverity_IMPACT_SEVERITY_HIGH || len(first.GetResources()) != 1 {
		t.Errorf("assessment = %v, want HIGH with one resource rollup entry", first)
	}
	if first.GetProvenance() == nil && first.GetSummary() == "" {
		t.Errorf("assessment lost content: %v", first)
	}

	second := run()
	if runner.assessCalls != 1 {
		t.Errorf("assess calls after cache = %d, want still 1 (scan unchanged)", runner.assessCalls)
	}
	if second.GetSummary() != first.GetSummary() {
		t.Errorf("cached assessment differs: %q vs %q", second.GetSummary(), first.GetSummary())
	}
}

func TestGetChangesValidationRejectsBadRef(t *testing.T) {
	client, _ := newVersionsTestClient(t, nil)
	stream, err := client.GetChanges(context.Background(), connect.NewRequest(&kdeltav1.GetChangesRequest{
		Ref: &kdeltav1.ResourceRef{Detector: "Not Valid!", Name: "x"},
	}))
	if err != nil {
		t.Fatalf("GetChanges: %v", err)
	}
	defer stream.Close()
	for stream.Receive() {
	}
	if connect.CodeOf(stream.Err()) != connect.CodeInvalidArgument {
		t.Errorf("stream error = %v, want InvalidArgument (protovalidate)", stream.Err())
	}
}
