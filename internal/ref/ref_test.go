package ref

import (
	"testing"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
)

func TestStringAndParseRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		ref  *kdeltav1.ResourceRef
		want string
	}{
		{
			name: "namespaced",
			ref:  &kdeltav1.ResourceRef{Detector: "helm", Namespace: "cert-manager", Name: "cert-manager"},
			want: "helm:cert-manager/cert-manager",
		},
		{
			name: "cluster-scoped",
			ref:  &kdeltav1.ResourceRef{Detector: "argocd", Name: "argocd"},
			want: "argocd:argocd",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := String(tt.ref)
			if got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
			parsed, err := Parse(got)
			if err != nil {
				t.Fatalf("Parse(%q): %v", got, err)
			}
			if parsed.GetDetector() != tt.ref.GetDetector() ||
				parsed.GetNamespace() != tt.ref.GetNamespace() ||
				parsed.GetName() != tt.ref.GetName() {
				t.Errorf("Parse(%q) = %v, want %v", got, parsed, tt.ref)
			}
		})
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	for _, s := range []string{"", "helm", ":x", "helm:", "helm:ns/", "helm:/name"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", s)
		}
	}
}
