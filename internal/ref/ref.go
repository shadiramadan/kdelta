// Package ref converts ResourceRefs to and from their canonical string form:
// "<detector>:<namespace>/<name>", or "<detector>:<name>" for cluster-scoped
// resources. The string form is what the CLI accepts and the store keys on.
package ref

import (
	"fmt"
	"strings"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
)

// String renders the canonical form. Structural validity is the caller's
// concern (protovalidate at the API boundary).
func String(r *kdeltav1.ResourceRef) string {
	if r.GetNamespace() == "" {
		return r.GetDetector() + ":" + r.GetName()
	}
	return r.GetDetector() + ":" + r.GetNamespace() + "/" + r.GetName()
}

// Parse reads the canonical form back into a ResourceRef.
func Parse(s string) (*kdeltav1.ResourceRef, error) {
	detector, rest, ok := strings.Cut(s, ":")
	if !ok || detector == "" || rest == "" {
		return nil, fmt.Errorf("invalid resource ref %q: want detector:[namespace/]name", s)
	}
	r := &kdeltav1.ResourceRef{Detector: detector}
	if namespace, name, namespaced := strings.Cut(rest, "/"); namespaced {
		if namespace == "" || name == "" {
			return nil, fmt.Errorf("invalid resource ref %q: empty namespace or name", s)
		}
		r.Namespace = namespace
		r.Name = name
	} else {
		r.Name = rest
	}
	return r, nil
}
