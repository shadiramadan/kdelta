// Package resolve fills in upstream identity that detectors cannot observe
// from the cluster: an installed Helm release does not record its chart
// repository, so after detection the chart stream's version source is
// resolved from public indexes (Artifact Hub) and verified against the
// deployed release's own chart metadata. Resolution is best-effort
// enrichment — any failure leaves the resource exactly as detected.
package resolve

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/upstream"
)

const (
	// stepBudget bounds the whole resolution step so scan latency stays
	// bounded even when the network blackholes.
	stepBudget = 60 * time.Second
	// packageTimeout bounds one package's search plus candidate fetches.
	packageTimeout = 15 * time.Second
	// maxPackagesPerScan caps network resolutions per scan; packages beyond
	// it stay unresolved until a later scan.
	maxPackagesPerScan = 20
)

// Cache remembers resolution outcomes across scans. Implementations bind
// their own freshness policy and swallow their own errors: resolution is
// cheap deterministic enrichment, so a cache hiccup reads as a miss instead
// of failing the scan (unlike the paid-model caches, which fail closed).
type Cache interface {
	// Resolution returns the cached outcome for a package: a non-empty URL
	// is a resolved registry; an empty URL with found=true is a fresh failed
	// attempt (do not retry yet); found=false means nothing usable is cached.
	Resolution(ctx context.Context, system, name string) (url string, found bool)
	// PutResolution records an outcome; an empty url records a failure.
	PutResolution(ctx context.Context, system, name, url string)
}

// Deps are the resolution step's dependencies.
type Deps struct {
	// Upstream performs the Artifact Hub and chart-index fetches; nil
	// disables resolution entirely.
	Upstream *upstream.Client
	// Cache is optional; nil resolves from the network every scan.
	Cache Cache
}

type packageKey struct{ system, name string }

// Upstreams resolves the upstream identity of every eligible resource in
// place: Helm resources whose PackageKey has no registry URL and whose chart
// stream has no version source. Each package is resolved once per scan and
// the result applied to all its releases. Best-effort: failures leave
// resources untouched.
func Upstreams(ctx context.Context, deps Deps, resources []*kdeltav1.Resource) {
	if deps.Upstream == nil {
		return
	}
	groups := map[packageKey][]*kdeltav1.Resource{}
	var order []packageKey
	for _, r := range resources {
		if !eligible(r) {
			continue
		}
		k := packageKey{system: r.GetUpstream().GetSystem(), name: r.GetUpstream().GetName()}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	if len(order) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, stepBudget)
	defer cancel()
	fetched := 0
	for _, k := range order {
		if deps.Cache != nil {
			if url, found := deps.Cache.Resolution(ctx, k.system, k.name); found {
				if url != "" {
					apply(groups[k], url)
				}
				continue
			}
		}
		if fetched >= maxPackagesPerScan || ctx.Err() != nil {
			continue
		}
		fetched++
		pctx, pcancel := context.WithTimeout(ctx, packageTimeout)
		url, ok := resolveHelm(pctx, deps.Upstream, deployedFrom(groups[k][0]))
		pcancel()
		if ok {
			apply(groups[k], url)
		}
		if deps.Cache != nil {
			deps.Cache.PutResolution(ctx, k.system, k.name, url)
		}
	}
}

// eligible reports whether a resource needs (and can receive) resolution.
func eligible(r *kdeltav1.Resource) bool {
	up := r.GetUpstream()
	if up.GetSystem() != "helm" || up.GetName() == "" || up.GetRegistryUrl() != "" {
		return false
	}
	chart := chartStream(r)
	return chart != nil && chart.GetSource() == nil
}

func chartStream(r *kdeltav1.Resource) *kdeltav1.VersionStream {
	for _, stream := range r.GetStreams() {
		if stream.GetKind() == kdeltav1.VersionKind_VERSION_KIND_CHART {
			return stream
		}
	}
	return nil
}

// apply stamps a verified repository URL onto every release of the package:
// the PackageKey gains its registry and the chart stream gains a
// HelmRepositorySource with FETCHED provenance.
func apply(resources []*kdeltav1.Resource, repoURL string) {
	provenance := &kdeltav1.Provenance{
		Method:      kdeltav1.ProvenanceMethod_PROVENANCE_METHOD_FETCHED,
		SourceUrl:   repoURL,
		CollectedAt: timestamppb.Now(),
	}
	for _, r := range resources {
		r.GetUpstream().RegistryUrl = repoURL
		chart := chartStream(r)
		chart.Source = &kdeltav1.VersionSource{
			Source: &kdeltav1.VersionSource_HelmRepository{
				HelmRepository: &kdeltav1.HelmRepositorySource{
					RepositoryUrl: repoURL,
					Chart:         r.GetUpstream().GetName(),
				},
			},
		}
		chart.SourceProvenance = provenance
	}
}
