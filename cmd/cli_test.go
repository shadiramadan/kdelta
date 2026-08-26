package cmd

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/servertest"
)

// The tests here are behavioral: each one runs a real CLI invocation against
// a real served backend (seeded cache, fake upstream forge, scripted agent)
// and asserts on what a user would see on stdout/stderr. Each invocation
// builds a fresh command tree via newRootCmd(), so there is no shared flag
// state to reset and the tests can run in parallel.

// runCLI executes `kdelta --server <backend> args...` against a fresh command
// tree and returns (stdout, stderr, err).
func runCLI(t *testing.T, backend *servertest.Backend, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd()
	var out, errOut strings.Builder
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"--server", backend.URL}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func mustContain(t *testing.T, label, haystack string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(haystack, want) {
			t.Errorf("%s missing %q; got:\n%s", label, want, haystack)
		}
	}
}

func TestResourcesListsCachedScan(t *testing.T) {
	backend := servertest.New(t)
	stdout, stderr, err := runCLI(t, backend, "resources")
	if err != nil {
		t.Fatalf("resources: %v", err)
	}
	mustContain(t, "stdout", stdout, servertest.CertManagerRef+"\tchart@v1.17.2")
	mustContain(t, "stderr", stderr, "1 resources (cached")
}

func TestResourcesJSONRoundTrips(t *testing.T) {
	backend := servertest.New(t)
	stdout, _, err := runCLI(t, backend, "resources", "-o", "json")
	if err != nil {
		t.Fatalf("resources -o json: %v", err)
	}
	var res kdeltav1.ListResourcesResponse
	if err := protojson.Unmarshal([]byte(stdout), &res); err != nil {
		t.Fatalf("stdout is not the response as protobuf JSON: %v\n%s", err, stdout)
	}
	if got := len(res.GetResources()); got != 1 {
		t.Fatalf("expected 1 resource in JSON output, got %d", got)
	}
	if got := res.GetResources()[0].GetRef().GetName(); got != "cert-manager" {
		t.Errorf("resource name = %q, want cert-manager", got)
	}
}

func TestResourcesRejectsUnknownOutputFormat(t *testing.T) {
	backend := servertest.New(t)
	_, _, err := runCLI(t, backend, "resources", "-o", "yaml")
	if err == nil || !strings.Contains(err.Error(), `unsupported output format "yaml"`) {
		t.Fatalf("expected unsupported-format error, got %v", err)
	}
}

func TestGetRendersResourceDetail(t *testing.T) {
	backend := servertest.New(t)
	stdout, _, err := runCLI(t, backend, "get", servertest.CertManagerRef)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	mustContain(t, "stdout", stdout,
		servertest.CertManagerRef,
		"upstream: helm/cert-manager",
		"link: source https://github.com/cert-manager/cert-manager",
		"link: homepage https://cert-manager.io",
		"chart\tv1.17.2",
		"app\tv1.17.2",
		"github cert-manager/cert-manager",
		"no version source",
		"condition: Deployed=true deployed",
		"object: v1 Secret cert-manager/sh.helm.release.v1.cert-manager.v1",
	)
}

func TestGetUnknownResourceFails(t *testing.T) {
	backend := servertest.New(t)
	_, _, err := runCLI(t, backend, "get", "helm:nowhere/nothing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestScanDetectsAndReports(t *testing.T) {
	backend := servertest.New(t)
	stdout, stderr, err := runCLI(t, backend, "scan")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	mustContain(t, "stdout", stdout, servertest.CertManagerRef+"\tv1.17.2")
	mustContain(t, "stderr", stderr, "1 resources detected")
}

func TestVersionsListsUpstreamReleases(t *testing.T) {
	backend := servertest.New(t)
	stdout, stderr, err := runCLI(t, backend, "versions", servertest.CertManagerRef, "--stream", "app")
	if err != nil {
		t.Fatalf("versions: %v", err)
	}
	mustContain(t, "stdout", stdout, "* v1.17.2", "  v1.18.0", "> v1.21.1")
	mustContain(t, "stderr", stderr, "current v1.17.2, latest v1.21.1 (2 behind)")
}

func TestVersionsWithoutSourceExplainsItself(t *testing.T) {
	backend := servertest.New(t)
	// The default (chart) stream has no resolved upstream; the error must
	// steer the user toward the streams that do.
	_, _, err := runCLI(t, backend, "versions", servertest.CertManagerRef)
	if err == nil || !strings.Contains(err.Error(), "no version source") {
		t.Fatalf("expected no-version-source error, got %v", err)
	}
}

func TestChangesExtractsOnceThenServesFromCache(t *testing.T) {
	backend := servertest.New(t)
	stdout, stderr, err := runCLI(t, backend, "changes", servertest.CertManagerRef, "--stream", "app")
	if err != nil {
		t.Fatalf("changes: %v", err)
	}
	// A live extraction streams version blocks incrementally (no leading
	// header — blocks render as partial events arrive).
	mustContain(t, "stdout", stdout,
		"[DEFAULT_CHANGED] extracted from v1.18.0: rotationPolicy default changed from Never to Always",
		"affects spec.privateKey.rotationPolicy",
		"extracted from v1.21.1",
	)
	if strings.Contains(stdout, "v1.17.2 -> v1.21.1") {
		t.Errorf("streamed run must render incrementally, not as one final change set; got:\n%s", stdout)
	}
	if strings.Contains(stdout, "v1.17.2:") || strings.Contains(stdout, "extracted from v1.17.2") {
		t.Errorf("changelog range must exclude the deployed version itself; got:\n%s", stdout)
	}
	mustContain(t, "stderr (progress)", stderr, "»")
	if backend.Runner.ExtractCalls != 1 {
		t.Fatalf("extract calls after first run = %d, want 1", backend.Runner.ExtractCalls)
	}

	// The same range again is immutable and must be served from cache — no
	// partial events, one complete render with the range header.
	stdout2, _, err := runCLI(t, backend, "changes", servertest.CertManagerRef, "--stream", "app")
	if err != nil {
		t.Fatalf("changes (cached): %v", err)
	}
	mustContain(t, "stdout (cached)", stdout2,
		"cert-manager v1.17.2 -> v1.21.1",
		"extracted from v1.18.0",
	)
	if backend.Runner.ExtractCalls != 1 {
		t.Errorf("extract calls after cached run = %d, want still 1", backend.Runner.ExtractCalls)
	}
}

func TestChangesJSONEmitsChangeSet(t *testing.T) {
	backend := servertest.New(t)
	stdout, _, err := runCLI(t, backend, "changes", servertest.CertManagerRef, "--stream", "app", "-o", "json")
	if err != nil {
		t.Fatalf("changes -o json: %v", err)
	}
	var cs kdeltav1.ChangeSet
	if err := protojson.Unmarshal([]byte(stdout), &cs); err != nil {
		t.Fatalf("stdout is not a ChangeSet as protobuf JSON: %v\n%s", err, stdout)
	}
	if cs.GetFromVersion() != "v1.17.2" || cs.GetToVersion() != "v1.21.1" {
		t.Errorf("range = %s -> %s, want v1.17.2 -> v1.21.1", cs.GetFromVersion(), cs.GetToVersion())
	}
	if len(cs.GetVersions()) != 2 {
		t.Errorf("versions in set = %d, want 2 (v1.18.0, v1.21.1)", len(cs.GetVersions()))
	}
}

func TestImpactAssessesOnceThenServesFromCache(t *testing.T) {
	backend := servertest.New(t)
	stdout, stderr, err := runCLI(t, backend, "impact", servertest.CertManagerRef, "--stream", "app")
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	mustContain(t, "stdout", stdout,
		servertest.CertManagerRef+" app: v1.17.2 -> v1.21.1  [HIGH]",
		"scripted assessment of v1.17.2 -> v1.21.1",
		"Affected resources:",
		"[HIGH] impact:demo-ns-1/legacy-app-tls",
		"rotationPolicy is unset, so the new Always default applies",
		"private key will rotate on next renewal",
		"-> (before upgrade) set rotationPolicy explicitly",
		"Unknowns:",
		"? values.yaml not captured",
	)
	mustContain(t, "stderr (progress)", stderr, "»")
	if backend.Runner.AssessCalls != 1 {
		t.Fatalf("assess calls after first run = %d, want 1", backend.Runner.AssessCalls)
	}

	stdout2, _, err := runCLI(t, backend, "impact", servertest.CertManagerRef, "--stream", "app")
	if err != nil {
		t.Fatalf("impact (cached): %v", err)
	}
	mustContain(t, "stdout (cached)", stdout2, "[HIGH] impact:demo-ns-1/legacy-app-tls")
	if backend.Runner.AssessCalls != 1 {
		t.Errorf("assess calls after cached run = %d, want still 1", backend.Runner.AssessCalls)
	}
}

func TestImpactJSONEmitsAssessment(t *testing.T) {
	backend := servertest.New(t)
	stdout, _, err := runCLI(t, backend, "impact", servertest.CertManagerRef, "--stream", "app", "-o", "json")
	if err != nil {
		t.Fatalf("impact -o json: %v", err)
	}
	var assessment kdeltav1.ImpactAssessment
	if err := protojson.Unmarshal([]byte(stdout), &assessment); err != nil {
		t.Fatalf("stdout is not an ImpactAssessment as protobuf JSON: %v\n%s", err, stdout)
	}
	if assessment.GetOverallSeverity() != kdeltav1.ImpactSeverity_IMPACT_SEVERITY_HIGH {
		t.Errorf("overall severity = %v, want HIGH", assessment.GetOverallSeverity())
	}
	if got := len(assessment.GetFindings()); got != 1 {
		t.Errorf("findings = %d, want 1", got)
	}
}

func TestResourceRefShellCompletion(t *testing.T) {
	backend := servertest.New(t)
	stdout, _, err := runCLI(t, backend, "__complete", "get", "")
	if err != nil {
		t.Fatalf("__complete get: %v", err)
	}
	mustContain(t, "completions", stdout, servertest.CertManagerRef+"\tchart v1.17.2")
}

func TestStreamFlagShellCompletion(t *testing.T) {
	backend := servertest.New(t)
	stdout, _, err := runCLI(t, backend, "__complete", "changes", servertest.CertManagerRef, "--stream", "")
	if err != nil {
		t.Fatalf("__complete --stream: %v", err)
	}
	mustContain(t, "completions", stdout, "chart\tv1.17.2", "app\tv1.17.2")
}

func TestScanResolvesChartSourceForDefaultVersions(t *testing.T) {
	backend := servertest.New(t)

	// Before a scan resolves it, the seeded chart stream has no source and
	// the default invocation names the resolvable sibling.
	_, _, err := runCLI(t, backend, "versions", servertest.CertManagerRef)
	if err == nil || !strings.Contains(err.Error(), "resolvable streams: app") {
		t.Fatalf("pre-scan versions error = %v, want it to name the resolvable streams", err)
	}

	if _, _, err := runCLI(t, backend, "scan"); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// The default (chart) stream now lists chart versions from the resolved
	// repository — the whole point of upstream resolution.
	stdout, stderr, err := runCLI(t, backend, "versions", servertest.CertManagerRef)
	if err != nil {
		t.Fatalf("versions on the default stream after scan: %v", err)
	}
	mustContain(t, "stdout", stdout, "* v1.17.2", "> v1.21.1")
	mustContain(t, "stderr", stderr, "current v1.17.2, latest v1.21.1 (2 behind)")

	stdout, _, err = runCLI(t, backend, "get", servertest.CertManagerRef)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	mustContain(t, "stdout", stdout,
		"upstream: helm/cert-manager "+backend.ChartRepoURL,
		"helm repo "+backend.ChartRepoURL)
	if strings.Contains(stdout, "registry unresolved") {
		t.Errorf("get still reports an unresolved registry:\n%s", stdout)
	}
}

func TestChangesDefaultStreamAfterScan(t *testing.T) {
	backend := servertest.New(t)
	if _, _, err := runCLI(t, backend, "scan"); err != nil {
		t.Fatalf("scan: %v", err)
	}

	stdout, _, err := runCLI(t, backend, "changes", servertest.CertManagerRef)
	if err != nil {
		t.Fatalf("changes on the default stream after scan: %v", err)
	}
	mustContain(t, "stdout", stdout, "v1.18.0", "rotationPolicy")
	if backend.Runner.ExtractCalls != 1 {
		t.Errorf("ExtractCalls = %d, want 1", backend.Runner.ExtractCalls)
	}
}
