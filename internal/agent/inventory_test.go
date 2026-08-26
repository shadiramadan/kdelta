package agent

import (
	"testing"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
)

// TestInventorySummaryWithholdsSecretObjects verifies that Secret object refs
// (e.g. Helm release storage secrets) are stripped from the inventory handed
// to the agent, so an injected instruction gets no secret names to target.
func TestInventorySummaryWithholdsSecretObjects(t *testing.T) {
	scan := &kdeltav1.ScanResponse{
		Resources: []*kdeltav1.Resource{{
			Ref: &kdeltav1.ResourceRef{Detector: "helm", Namespace: "cert-manager", Name: "cert-manager"},
			Objects: []*kdeltav1.KubernetesObjectRef{
				{ApiVersion: "v1", Kind: "Secret", Namespace: "cert-manager", Name: "sh.helm.release.v1.cert-manager.v1"},
				{ApiVersion: "apps/v1", Kind: "Deployment", Namespace: "cert-manager", Name: "cert-manager"},
			},
		}},
	}

	summary := inventorySummary(scan)
	objects := summary.GetResources()[0].GetObjects()
	if len(objects) != 1 {
		t.Fatalf("summary objects = %v, want only the non-Secret object", objects)
	}
	if objects[0].GetKind() == "Secret" {
		t.Errorf("Secret object leaked into the agent inventory: %v", objects[0])
	}
	if objects[0].GetKind() != "Deployment" {
		t.Errorf("kept object = %v, want the Deployment", objects[0])
	}
}
