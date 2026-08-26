// Package store is kdelta's local cache: scan snapshots, version lists,
// change sets, and impact assessments, so determining impact does not
// re-scan, re-resolve versions, or re-extract changes every time.
//
// Payloads are serialized protobuf messages keyed by indexed columns.
// Invalidation follows the pipeline: a new scan supersedes the previous one
// (resources and impact assessments belong to a scan and cascade away with
// it), version lists and upstream resolutions expire by age (failed
// resolutions sooner), and change sets persist until regenerated — change
// data is cluster-independent.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	// Registers the pure-Go "sqlite" database/sql driver (CGO-free, so the
	// static container build keeps working).
	_ "modernc.org/sqlite"

	kdeltav1 "github.com/shadiramadan/kdelta/gen/kdelta/v1"
	"github.com/shadiramadan/kdelta/internal/ref"
)

//go:embed schema.sql
var schemaSQL string

// ErrNotFound is returned when no fresh entry exists for a key.
var ErrNotFound = errors.New("store: not found")

// errSchemaMismatch marks a database whose schema version does not match
// this build; Open reacts by wiping and recreating the cache.
var errSchemaMismatch = errors.New("cache schema version mismatch")

const schemaVersion = 2

// Store is a handle to the local cache database. Safe for concurrent use
// within one process.
type Store struct {
	db *sql.DB
}

// DefaultPath is the per-user cache database location.
func DefaultPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving user cache dir: %w", err)
	}
	return filepath.Join(dir, "kdelta", "kdelta.db"), nil
}

// sqliteDSN escapes the filesystem path for a file: URI so paths containing
// '?', '#', or '%' survive verbatim and the pragma query stays intact.
func sqliteDSN(path string) string {
	escaped := strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(path)
	return "file:" + escaped + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
}

// Open opens (creating if needed) the cache database at path. A database
// whose schema version does not match this build — written by an older or a
// newer kdelta — is wiped and recreated: the store is a pure cache, so
// rebuilding it costs refetches, never data.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating cache directory: %w", err)
	}
	for attempt := 0; ; attempt++ {
		db, err := sql.Open("sqlite", sqliteDSN(path))
		if err != nil {
			return nil, fmt.Errorf("opening %s: %w", path, err)
		}
		// SQLite allows one writer; serializing through a single connection
		// avoids SQLITE_BUSY without giving up anything at kdelta's scale.
		db.SetMaxOpenConns(1)
		s := &Store{db: db}
		err = s.migrate()
		if err == nil {
			return s, nil
		}
		_ = db.Close()
		if attempt == 0 && errors.Is(err, errSchemaMismatch) {
			for _, stale := range []string{path, path + "-wal", path + "-shm"} {
				_ = os.Remove(stale)
			}
			continue
		}
		return nil, err
	}
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	switch version {
	case 0:
		// One transaction for schema + version stamp: an interrupted init
		// must not leave tables behind with user_version still 0 (SQLite DDL
		// and user_version are both transactional).
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.Exec(schemaSQL); err != nil {
			return fmt.Errorf("applying schema: %w", err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
			return fmt.Errorf("recording schema version: %w", err)
		}
		return tx.Commit()
	case schemaVersion:
		return nil
	default:
		return fmt.Errorf("cache schema version %d does not match this build (%d): %w",
			version, schemaVersion, errSchemaMismatch)
	}
}

// SaveScan stores a scan snapshot as the current one, superseding (and
// cascading away) all previous scans, their resources, and any impact
// assessments derived from them.
func (s *Store) SaveScan(ctx context.Context, scan *kdeltav1.ScanResponse) (int64, error) {
	blob, err := proto.Marshal(scan)
	if err != nil {
		return 0, fmt.Errorf("marshaling scan: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		"INSERT INTO scans (scanned_at, proto) VALUES (?, ?)",
		scan.GetScannedAt().AsTime().Unix(), blob)
	if err != nil {
		return 0, fmt.Errorf("inserting scan: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, r := range scan.GetResources() {
		rblob, err := proto.Marshal(r)
		if err != nil {
			return 0, fmt.Errorf("marshaling resource: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO resources (scan_id, ref, proto) VALUES (?, ?, ?)",
			id, ref.String(r.GetRef()), rblob); err != nil {
			return 0, fmt.Errorf("inserting resource %s: %w", ref.String(r.GetRef()), err)
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM scans WHERE id <> ?", id); err != nil {
		return 0, fmt.Errorf("superseding previous scans: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// LatestScan returns the current scan snapshot.
func (s *Store) LatestScan(ctx context.Context) (int64, *kdeltav1.ScanResponse, error) {
	var (
		id   int64
		blob []byte
	)
	err := s.db.QueryRowContext(ctx,
		"SELECT id, proto FROM scans ORDER BY id DESC LIMIT 1").Scan(&id, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, ErrNotFound
	}
	if err != nil {
		return 0, nil, err
	}
	scan := &kdeltav1.ScanResponse{}
	if err := proto.Unmarshal(blob, scan); err != nil {
		return 0, nil, fmt.Errorf("unmarshaling scan: %w", err)
	}
	return id, scan, nil
}

// Resource returns one resource from the current scan.
func (s *Store) Resource(ctx context.Context, r *kdeltav1.ResourceRef) (*kdeltav1.Resource, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT r.proto FROM resources r
		 JOIN scans sc ON sc.id = r.scan_id
		 WHERE r.ref = ?
		 ORDER BY sc.id DESC LIMIT 1`, ref.String(r)).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	resource := &kdeltav1.Resource{}
	if err := proto.Unmarshal(blob, resource); err != nil {
		return nil, fmt.Errorf("unmarshaling resource: %w", err)
	}
	return resource, nil
}

func packageKeyColumns(key *kdeltav1.PackageKey) (system, name, registry string) {
	return key.GetSystem(), key.GetName(), key.GetRegistryUrl()
}

// PutVersionList caches the enumerated versions for a package stream kind.
func (s *Store) PutVersionList(ctx context.Context, key *kdeltav1.PackageKey, kind kdeltav1.VersionKind, list *kdeltav1.ListVersionsResponse) error {
	blob, err := proto.Marshal(list)
	if err != nil {
		return fmt.Errorf("marshaling version list: %w", err)
	}
	system, name, registry := packageKeyColumns(key)
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO version_lists (system, name, registry_url, kind, fetched_at, proto)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (system, name, registry_url, kind)
		 DO UPDATE SET fetched_at = excluded.fetched_at, proto = excluded.proto`,
		system, name, registry, int64(kind), time.Now().Unix(), blob)
	return err
}

// VersionList returns cached versions no older than maxAge, or ErrNotFound.
func (s *Store) VersionList(ctx context.Context, key *kdeltav1.PackageKey, kind kdeltav1.VersionKind, maxAge time.Duration) (*kdeltav1.ListVersionsResponse, error) {
	system, name, registry := packageKeyColumns(key)
	var (
		fetchedAt int64
		blob      []byte
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT fetched_at, proto FROM version_lists
		 WHERE system = ? AND name = ? AND registry_url = ? AND kind = ?`,
		system, name, registry, int64(kind)).Scan(&fetchedAt, &blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if time.Since(time.Unix(fetchedAt, 0)) > maxAge {
		return nil, ErrNotFound
	}
	list := &kdeltav1.ListVersionsResponse{}
	if err := proto.Unmarshal(blob, list); err != nil {
		return nil, fmt.Errorf("unmarshaling version list: %w", err)
	}
	return list, nil
}

// PutResolution records where a package's registry was resolved to; an empty
// registryURL records that resolution was attempted and failed, so callers
// back off retrying (negative cache).
func (s *Store) PutResolution(ctx context.Context, system, name, registryURL string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO upstream_resolutions (system, name, registry_url, fetched_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (system, name)
		 DO UPDATE SET registry_url = excluded.registry_url, fetched_at = excluded.fetched_at`,
		system, name, registryURL, time.Now().Unix())
	return err
}

// Resolution returns the cached registry resolution for a package. A ("",
// nil) result is a fresh failed attempt — do not retry yet. ErrNotFound
// means no attempt is cached or the cached one has expired: successes after
// maxAge, failures after failureMaxAge.
func (s *Store) Resolution(ctx context.Context, system, name string, maxAge, failureMaxAge time.Duration) (string, error) {
	var (
		registryURL string
		fetchedAt   int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT registry_url, fetched_at FROM upstream_resolutions
		 WHERE system = ? AND name = ?`, system, name).Scan(&registryURL, &fetchedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	age := maxAge
	if registryURL == "" {
		age = failureMaxAge
	}
	if time.Since(time.Unix(fetchedAt, 0)) > age {
		return "", ErrNotFound
	}
	return registryURL, nil
}

// PutChangeSet caches a resolved change set, replacing any previous content
// for the same package and range.
func (s *Store) PutChangeSet(ctx context.Context, cs *kdeltav1.ChangeSet) error {
	if cs.GetPackage() == nil {
		return errors.New("change set has no package key")
	}
	blob, err := proto.Marshal(cs)
	if err != nil {
		return fmt.Errorf("marshaling change set: %w", err)
	}
	system, name, registry := packageKeyColumns(cs.GetPackage())
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO change_sets (system, name, registry_url, kind, from_version, to_version, created_at, proto)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (system, name, registry_url, kind, from_version, to_version)
		 DO UPDATE SET created_at = excluded.created_at, proto = excluded.proto`,
		system, name, registry, int64(cs.GetKind()), cs.GetFromVersion(), cs.GetToVersion(),
		time.Now().Unix(), blob)
	return err
}

// ChangeSet returns the cached change set for a package and range.
func (s *Store) ChangeSet(ctx context.Context, key *kdeltav1.PackageKey, kind kdeltav1.VersionKind, fromVersion, toVersion string) (*kdeltav1.ChangeSet, error) {
	system, name, registry := packageKeyColumns(key)
	var blob []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT proto FROM change_sets
		 WHERE system = ? AND name = ? AND registry_url = ? AND kind = ?
		   AND from_version = ? AND to_version = ?`,
		system, name, registry, int64(kind), fromVersion, toVersion).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	cs := &kdeltav1.ChangeSet{}
	if err := proto.Unmarshal(blob, cs); err != nil {
		return nil, fmt.Errorf("unmarshaling change set: %w", err)
	}
	return cs, nil
}

// PutImpact caches an assessment against the scan generation it was computed
// from; it disappears with that scan.
func (s *Store) PutImpact(ctx context.Context, scanID int64, a *kdeltav1.ImpactAssessment) error {
	blob, err := proto.Marshal(a)
	if err != nil {
		return fmt.Errorf("marshaling impact assessment: %w", err)
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO impact_assessments (scan_id, target_ref, stream_id, from_version, to_version, created_at, proto)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (scan_id, target_ref, stream_id, from_version, to_version)
		 DO UPDATE SET created_at = excluded.created_at, proto = excluded.proto`,
		scanID, ref.String(a.GetTarget()), a.GetStreamId(),
		a.GetFromVersion(), a.GetToVersion(), time.Now().Unix(), blob)
	return err
}

// Impact returns the cached assessment computed from the current scan.
func (s *Store) Impact(ctx context.Context, target *kdeltav1.ResourceRef, streamID, fromVersion, toVersion string) (*kdeltav1.ImpactAssessment, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT ia.proto FROM impact_assessments ia
		 JOIN scans sc ON sc.id = ia.scan_id
		 WHERE ia.target_ref = ? AND ia.stream_id = ?
		   AND ia.from_version = ? AND ia.to_version = ?
		 ORDER BY sc.id DESC LIMIT 1`,
		ref.String(target), streamID, fromVersion, toVersion).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a := &kdeltav1.ImpactAssessment{}
	if err := proto.Unmarshal(blob, a); err != nil {
		return nil, fmt.Errorf("unmarshaling impact assessment: %w", err)
	}
	return a, nil
}
