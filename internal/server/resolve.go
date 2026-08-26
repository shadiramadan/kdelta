package server

import (
	"context"
	"time"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/resolve"
	"github.com/shadiramadan/kdelta/internal/store"
)

// Resolution-cache freshness: chart→repository mappings are far more stable
// than version lists, so successes live a week; failed attempts retry daily,
// which is polite to Artifact Hub yet self-heals when a chart appears there.
const (
	resolutionTTL        = 7 * 24 * time.Hour
	resolutionFailureTTL = 24 * time.Hour
)

// resolutionCache adapts the store to resolve.Cache, binding the TTLs and
// swallowing store errors: resolution is best-effort enrichment, so a cache
// hiccup reads as a miss instead of failing the scan (unlike the paid-model
// caches, which fail closed).
type resolutionCache struct {
	store *store.Store
}

func (c resolutionCache) Resolution(ctx context.Context, system, name string) (string, bool) {
	url, err := c.store.Resolution(ctx, system, name, resolutionTTL, resolutionFailureTTL)
	if err != nil {
		return "", false
	}
	return url, true
}

func (c resolutionCache) PutResolution(ctx context.Context, system, name, url string) {
	_ = c.store.PutResolution(ctx, system, name, url)
}

// resolveUpstreams enriches detector output with upstream identity the
// cluster does not record (a Helm release's chart repository). Best-effort:
// a no-op without an upstream client, and failures leave resources exactly
// as detected.
func (s *KdeltaService) resolveUpstreams(ctx context.Context, resources []*kdeltav1.Resource) {
	if s.upstream == nil {
		return
	}
	deps := resolve.Deps{Upstream: s.upstream}
	if s.store != nil {
		deps.Cache = resolutionCache{store: s.store}
	}
	resolve.Upstreams(ctx, deps, resources)
}
