package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "kdelta.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func testScan(names ...string) *kdeltav1.ScanResponse {
	scan := &kdeltav1.ScanResponse{ScannedAt: timestamppb.Now()}
	for _, name := range names {
		scan.Resources = append(scan.Resources, &kdeltav1.Resource{
			Ref:         &kdeltav1.ResourceRef{Detector: "helm", Namespace: "ns", Name: name},
			DisplayName: name,
		})
	}
	return scan
}

func certManagerKey() *kdeltav1.PackageKey {
	return &kdeltav1.PackageKey{System: "helm", Name: "cert-manager", RegistryUrl: "https://charts.example.com"}
}

func TestScanRoundTripAndResourceLookup(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, _, err := s.LatestScan(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LatestScan on empty store = %v, want ErrNotFound", err)
	}

	id, err := s.SaveScan(ctx, testScan("cert-manager", "other"))
	if err != nil {
		t.Fatalf("SaveScan: %v", err)
	}
	gotID, scan, err := s.LatestScan(ctx)
	if err != nil {
		t.Fatalf("LatestScan: %v", err)
	}
	if gotID != id || len(scan.GetResources()) != 2 {
		t.Errorf("LatestScan = id %d with %d resources, want id %d with 2", gotID, len(scan.GetResources()), id)
	}

	resource, err := s.Resource(ctx, &kdeltav1.ResourceRef{Detector: "helm", Namespace: "ns", Name: "cert-manager"})
	if err != nil {
		t.Fatalf("Resource: %v", err)
	}
	if resource.GetDisplayName() != "cert-manager" {
		t.Errorf("Resource display name = %q, want %q", resource.GetDisplayName(), "cert-manager")
	}
	if _, err := s.Resource(ctx, &kdeltav1.ResourceRef{Detector: "helm", Namespace: "ns", Name: "absent"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Resource(absent) = %v, want ErrNotFound", err)
	}
}

func TestRescanSupersedesAndInvalidatesImpact(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	target := &kdeltav1.ResourceRef{Detector: "helm", Namespace: "ns", Name: "cert-manager"}
	firstID, err := s.SaveScan(ctx, testScan("cert-manager", "stale-resource"))
	if err != nil {
		t.Fatalf("SaveScan: %v", err)
	}
	assessment := &kdeltav1.ImpactAssessment{
		Target: target, StreamId: "chart", FromVersion: "v1.17.2", ToVersion: "v1.21.1",
		Summary: "computed from first scan",
	}
	if err := s.PutImpact(ctx, firstID, assessment); err != nil {
		t.Fatalf("PutImpact: %v", err)
	}
	if _, err := s.Impact(ctx, target, "chart", "v1.17.2", "v1.21.1"); err != nil {
		t.Fatalf("Impact before rescan: %v", err)
	}

	if _, err := s.SaveScan(ctx, testScan("cert-manager")); err != nil {
		t.Fatalf("SaveScan (rescan): %v", err)
	}

	// The old scan's impact and resources must be gone.
	if _, err := s.Impact(ctx, target, "chart", "v1.17.2", "v1.21.1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Impact after rescan = %v, want ErrNotFound (invalidated)", err)
	}
	if _, err := s.Resource(ctx, &kdeltav1.ResourceRef{Detector: "helm", Namespace: "ns", Name: "stale-resource"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("stale resource survived rescan, want ErrNotFound")
	}
	if _, err := s.Resource(ctx, target); err != nil {
		t.Errorf("current resource missing after rescan: %v", err)
	}
}

func TestVersionListFreshness(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	key := certManagerKey()

	list := &kdeltav1.ListVersionsResponse{Current: "v1.17.2", Latest: "v1.21.1", VersionsBehind: 12}
	if err := s.PutVersionList(ctx, key, kdeltav1.VersionKind_VERSION_KIND_CHART, list); err != nil {
		t.Fatalf("PutVersionList: %v", err)
	}

	got, err := s.VersionList(ctx, key, kdeltav1.VersionKind_VERSION_KIND_CHART, time.Hour)
	if err != nil {
		t.Fatalf("VersionList (fresh): %v", err)
	}
	if got.GetLatest() != "v1.21.1" {
		t.Errorf("latest = %q, want v1.21.1", got.GetLatest())
	}

	if _, err := s.VersionList(ctx, key, kdeltav1.VersionKind_VERSION_KIND_CHART, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("VersionList with zero maxAge = %v, want ErrNotFound (stale)", err)
	}
	if _, err := s.VersionList(ctx, key, kdeltav1.VersionKind_VERSION_KIND_APP, time.Hour); !errors.Is(err, ErrNotFound) {
		t.Errorf("VersionList for other kind = %v, want ErrNotFound", err)
	}
}

func TestChangeSetRoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	cs := &kdeltav1.ChangeSet{
		Package:     certManagerKey(),
		Kind:        kdeltav1.VersionKind_VERSION_KIND_APP,
		FromVersion: "v1.17.2",
		ToVersion:   "v1.18.0",
		Versions: []*kdeltav1.VersionChanges{{
			Version: "v1.18.0",
			Changes: []*kdeltav1.Change{{
				Type:          kdeltav1.ChangeType_CHANGE_TYPE_DEFAULT_CHANGED,
				Summary:       "rotationPolicy default changed from Never to Always",
				AffectedPaths: []string{"spec.privateKey.rotationPolicy"},
				Confidence:    1.0,
			}},
		}},
	}
	if err := s.PutChangeSet(ctx, cs); err != nil {
		t.Fatalf("PutChangeSet: %v", err)
	}
	got, err := s.ChangeSet(ctx, certManagerKey(), kdeltav1.VersionKind_VERSION_KIND_APP, "v1.17.2", "v1.18.0")
	if err != nil {
		t.Fatalf("ChangeSet: %v", err)
	}
	if len(got.GetVersions()) != 1 || got.GetVersions()[0].GetChanges()[0].GetType() != kdeltav1.ChangeType_CHANGE_TYPE_DEFAULT_CHANGED {
		t.Errorf("round-tripped change set lost content: %v", got)
	}
	if err := s.PutChangeSet(ctx, &kdeltav1.ChangeSet{FromVersion: "a", ToVersion: "b"}); err == nil {
		t.Error("PutChangeSet without package key succeeded, want error")
	}
}

func TestReopenPersistsAndMigrationIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kdelta.db")
	ctx := context.Background()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.SaveScan(ctx, testScan("cert-manager")); err != nil {
		t.Fatalf("SaveScan: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if _, scan, err := reopened.LatestScan(ctx); err != nil || len(scan.GetResources()) != 1 {
		t.Errorf("LatestScan after reopen = %v resources, err %v; want 1 resource", len(scan.GetResources()), err)
	}
}

func TestResolutionRoundTripAndTTL(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	const week, day = 7 * 24 * time.Hour, 24 * time.Hour

	if _, err := s.Resolution(ctx, "helm", "cert-manager", week, day); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolution on empty store = %v, want ErrNotFound", err)
	}

	if err := s.PutResolution(ctx, "helm", "cert-manager", "https://charts.example.com"); err != nil {
		t.Fatalf("PutResolution: %v", err)
	}
	url, err := s.Resolution(ctx, "helm", "cert-manager", week, day)
	if err != nil || url != "https://charts.example.com" {
		t.Errorf("Resolution = %q, %v; want the cached URL", url, err)
	}
	if _, err := s.Resolution(ctx, "helm", "cert-manager", 0, day); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired success = %v, want ErrNotFound", err)
	}

	// An empty URL is a negative entry: fresh reads say "don't retry",
	// expiry uses the (shorter) failure age.
	if err := s.PutResolution(ctx, "helm", "private-chart", ""); err != nil {
		t.Fatalf("PutResolution(negative): %v", err)
	}
	url, err = s.Resolution(ctx, "helm", "private-chart", week, day)
	if err != nil || url != "" {
		t.Errorf("fresh negative = %q, %v; want empty URL and nil error", url, err)
	}
	if _, err := s.Resolution(ctx, "helm", "private-chart", week, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired negative = %v, want ErrNotFound", err)
	}

	// A later successful attempt upserts over the negative entry.
	if err := s.PutResolution(ctx, "helm", "private-chart", "https://charts.internal.example"); err != nil {
		t.Fatalf("PutResolution(upsert): %v", err)
	}
	if url, err = s.Resolution(ctx, "helm", "private-chart", week, day); err != nil || url == "" {
		t.Errorf("upserted resolution = %q, %v; want the new URL", url, err)
	}
}

// TestOpenWipesMismatchedSchema pins the cache's schema policy: a database
// stamped with any other schema version — older or newer build — is wiped
// and recreated rather than migrated.
func TestOpenWipesMismatchedSchema(t *testing.T) {
	for _, version := range []int{1, schemaVersion + 1} {
		t.Run(fmt.Sprintf("version=%d", version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "kdelta.db")
			ctx := context.Background()

			s, err := Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if _, err := s.SaveScan(ctx, testScan("cert-manager")); err != nil {
				t.Fatalf("SaveScan: %v", err)
			}
			if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
				t.Fatalf("stamping version: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			reopened, err := Open(path)
			if err != nil {
				t.Fatalf("reopen across schema version %d: %v", version, err)
			}
			defer reopened.Close()
			if _, _, err := reopened.LatestScan(ctx); !errors.Is(err, ErrNotFound) {
				t.Errorf("LatestScan after wipe = %v, want ErrNotFound (cache rebuilt empty)", err)
			}
			if err := reopened.PutResolution(ctx, "helm", "cert-manager", "https://charts.example.com"); err != nil {
				t.Errorf("PutResolution on rebuilt cache: %v", err)
			}
		})
	}
}
