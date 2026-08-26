package helm

import (
	"context"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"
	"k8s.io/client-go/kubernetes/fake"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/detect"
	"github.com/shadiramadan/kdelta/internal/ref"
)

// seedRelease stores a release revision the way Helm itself would (encoded
// release secret via the storage driver).
func seedRelease(t *testing.T, client *fake.Clientset, rel *release.Release) {
	t.Helper()
	secrets := driver.NewSecrets(client.CoreV1().Secrets(rel.Namespace))
	if err := secrets.Create(makeKey(rel), rel); err != nil {
		t.Fatalf("seeding release %s/%s v%d: %v", rel.Namespace, rel.Name, rel.Version, err)
	}
}

func makeKey(rel *release.Release) string {
	return "sh.helm.release.v1." + rel.Name + ".v" + string(rune('0'+rel.Version))
}

func certManagerRelease(version int, chartVersion, appVersion string, status release.Status) *release.Release {
	return &release.Release{
		Name:      "cert-manager",
		Namespace: "cert-manager",
		Version:   version,
		Info:      &release.Info{Status: status},
		Chart: &chart.Chart{Metadata: &chart.Metadata{
			Name:       "cert-manager",
			Version:    chartVersion,
			AppVersion: appVersion,
			Home:       "https://cert-manager.io",
			Sources:    []string{"https://github.com/cert-manager/cert-manager"},
		}},
	}
}

func TestDetectEmitsLatestRevisionWithStreams(t *testing.T) {
	client := fake.NewClientset()
	seedRelease(t, client, certManagerRelease(1, "v1.17.1", "v1.17.1", release.StatusSuperseded))
	seedRelease(t, client, certManagerRelease(2, "v1.17.2", "v1.17.2", release.StatusDeployed))

	result, err := NewWithClient(client).Detect(context.Background(), detect.Request{})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(result.Resources) != 1 {
		t.Fatalf("got %d resources, want 1 (latest revision only)", len(result.Resources))
	}
	r := result.Resources[0]

	if got := ref.String(r.GetRef()); got != "helm:cert-manager/cert-manager" {
		t.Errorf("ref = %q, want helm:cert-manager/cert-manager", got)
	}
	if r.GetUpstream().GetSystem() != "helm" || r.GetUpstream().GetName() != "cert-manager" {
		t.Errorf("upstream = %v, want helm/cert-manager", r.GetUpstream())
	}
	if r.GetUpstream().GetRegistryUrl() != "" {
		t.Errorf("registry_url = %q, want empty (unresolvable from the cluster)", r.GetUpstream().GetRegistryUrl())
	}

	streams := r.GetStreams()
	if len(streams) != 2 {
		t.Fatalf("got %d streams, want 2 (chart, app)", len(streams))
	}
	if streams[0].GetId() != "chart" || streams[0].GetCurrent().GetValue() != "v1.17.2" {
		t.Errorf("primary stream = %s@%s, want chart@v1.17.2", streams[0].GetId(), streams[0].GetCurrent().GetValue())
	}
	// The chart stream has no version source at detect time (the repository
	// is resolved downstream) but shares the github release-notes chain.
	if streams[0].GetSource() != nil {
		t.Errorf("chart version source = %v, want none (unresolvable from the cluster)", streams[0].GetSource())
	}
	chartNotes := streams[0].GetChanges()
	if len(chartNotes) != 1 || chartNotes[0].GetReleaseNotes().GetRepositoryUrl() != "https://github.com/cert-manager/cert-manager" {
		t.Errorf("chart change sources = %v, want one github release-notes source", chartNotes)
	}
	app := streams[1]
	if app.GetId() != "app" || app.GetCurrent().GetValue() != "v1.17.2" {
		t.Errorf("app stream = %s@%s, want app@v1.17.2", app.GetId(), app.GetCurrent().GetValue())
	}

	// The chart's github source becomes the app stream's version source and
	// release-notes change source.
	github := app.GetSource().GetGithubReleases()
	if github.GetOwner() != "cert-manager" || github.GetRepo() != "cert-manager" {
		t.Errorf("app version source = %v, want github cert-manager/cert-manager", app.GetSource())
	}
	if len(app.GetChanges()) != 1 || app.GetChanges()[0].GetReleaseNotes() == nil {
		t.Errorf("app change sources = %v, want one release-notes source", app.GetChanges())
	}

	if r.GetConditions()[0].GetStatus() != kdeltav1.ConditionStatus_CONDITION_STATUS_TRUE {
		t.Errorf("Deployed condition = %v, want TRUE", r.GetConditions()[0])
	}
	if r.GetObjects()[0].GetName() != "sh.helm.release.v1.cert-manager.v2" {
		t.Errorf("backing object = %q, want the v2 release secret", r.GetObjects()[0].GetName())
	}
}

func TestDetectFiltersUnsafeLinkSchemes(t *testing.T) {
	client := fake.NewClientset()
	rel := certManagerRelease(1, "v1.17.2", "v1.17.2", release.StatusDeployed)
	// A tenant-controlled chart smuggles a script-scheme URL into metadata.
	rel.Chart.Metadata.Home = "javascript:alert(document.cookie)"
	rel.Chart.Metadata.Sources = []string{
		"https://github.com/cert-manager/cert-manager",
		"data:text/html,<script>evil()</script>",
	}
	seedRelease(t, client, rel)

	result, err := NewWithClient(client).Detect(context.Background(), detect.Request{})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	for _, link := range result.Resources[0].GetLinks() {
		if link.GetUrl() != "https://github.com/cert-manager/cert-manager" {
			t.Errorf("unsafe link leaked into output: %q", link.GetUrl())
		}
	}
	if len(result.Resources[0].GetLinks()) != 1 {
		t.Errorf("links = %v, want only the single https source", result.Resources[0].GetLinks())
	}
}

func TestDetectScopesToRequestedNamespaces(t *testing.T) {
	client := fake.NewClientset()
	seedRelease(t, client, certManagerRelease(1, "v1.17.2", "v1.17.2", release.StatusDeployed))
	other := certManagerRelease(1, "9.9.9", "9.9.9", release.StatusDeployed)
	other.Name = "unrelated"
	other.Namespace = "elsewhere"
	seedRelease(t, client, other)

	result, err := NewWithClient(client).Detect(context.Background(), detect.Request{
		Namespaces: []string{"cert-manager"},
	})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(result.Resources) != 1 || result.Resources[0].GetRef().GetNamespace() != "cert-manager" {
		t.Fatalf("got %v, want only the cert-manager namespace release", result.Resources)
	}
}
