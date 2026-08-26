// Package detect defines the detector abstraction scan is built from. Each
// detector discovers one signal (today Helm releases; applications, labels,
// and images are planned)
// and emits resources with version streams plus hierarchy edges; the registry
// aggregates them. New resource types are supported by registering a new
// detector.
package detect

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"k8s.io/client-go/rest"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
)

// ErrNotApplicable is returned by a detector that cannot run against the
// target cluster for a benign, expected reason — its CRD is not installed, or
// the connection lacks access to the resource it needs. The registry treats it
// as "this detector contributed nothing here" and continues, rather than
// failing the whole scan. Detectors must reserve it for applicability, not for
// genuine failures (a malformed response, a transport error), which stay fatal.
var ErrNotApplicable = errors.New("detector not applicable to this cluster")

// Request scopes one scan.
type Request struct {
	// Config is the cluster connection; detectors build the clients they
	// need from it.
	Config *rest.Config
	// Namespaces to scan; empty means all the connection allows.
	Namespaces []string
	// Selector is a label selector expression filtering candidate objects.
	Selector string
}

// Result is what a detector (or a whole scan) discovered.
type Result struct {
	Resources []*kdeltav1.Resource
	Edges     []*kdeltav1.ResourceEdge
}

// Detector discovers resources of one kind.
type Detector interface {
	// Name identifies the detector ("helm"); it is the ref's detector
	// segment and the value matched by a scan's detector filter.
	Name() string
	Detect(ctx context.Context, req Request) (Result, error)
}

// Registry holds the registered detectors.
type Registry struct {
	detectors []Detector
}

func NewRegistry(detectors ...Detector) *Registry {
	return &Registry{detectors: detectors}
}

// Names lists the registered detector names in registration order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.detectors))
	for _, d := range r.detectors {
		names = append(names, d.Name())
	}
	return names
}

// Run executes the named detectors (all, when only is empty) and aggregates
// their results. A genuine detector error fails the scan: partial inventories
// would silently skew impact analysis downstream. A detector that reports
// ErrNotApplicable (its CRD is absent, or the connection cannot reach its
// resource) is skipped instead — expected on clusters where an optional
// detector has nothing to find.
func (r *Registry) Run(ctx context.Context, req Request, only []string) (Result, error) {
	var result Result
	matched := 0
	for _, d := range r.detectors {
		if len(only) > 0 && !slices.Contains(only, d.Name()) {
			continue
		}
		matched++
		part, err := d.Detect(ctx, req)
		if errors.Is(err, ErrNotApplicable) {
			continue
		}
		if err != nil {
			return Result{}, fmt.Errorf("detector %s: %w", d.Name(), err)
		}
		result.Resources = append(result.Resources, part.Resources...)
		result.Edges = append(result.Edges, part.Edges...)
	}
	if len(only) > 0 && matched != len(only) {
		return Result{}, fmt.Errorf("unknown detector in %v (registered: %v)", only, r.Names())
	}
	return result, nil
}
