-- kdelta local cache: serialized protobuf payloads with indexed key columns.
-- Timestamps are unix seconds (UTC). Invalidation semantics:
--   * a new scan supersedes the previous one; resources and impact
--     assessments cascade away with the scan they belonged to
--   * version lists expire by age (enforced on read)
--   * change sets are content for a fixed (package, kind, range): kept until
--     regenerated
--   * upstream resolutions expire by age (enforced on read; failed
--     resolutions expire sooner)
CREATE TABLE scans (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  scanned_at INTEGER NOT NULL,
  proto      BLOB    NOT NULL -- kdelta.v1.ScanResponse
);

CREATE TABLE resources (
  scan_id INTEGER NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
  ref     TEXT    NOT NULL, -- canonical "detector:namespace/name"
  proto   BLOB    NOT NULL, -- kdelta.v1.Resource
  PRIMARY KEY (scan_id, ref)
);

CREATE TABLE version_lists (
  system       TEXT    NOT NULL,
  name         TEXT    NOT NULL,
  registry_url TEXT    NOT NULL, -- empty when unresolved
  kind         INTEGER NOT NULL, -- kdelta.v1.VersionKind
  fetched_at   INTEGER NOT NULL,
  proto        BLOB    NOT NULL, -- kdelta.v1.ListVersionsResponse
  PRIMARY KEY (system, name, registry_url, kind)
);

CREATE TABLE change_sets (
  system       TEXT    NOT NULL,
  name         TEXT    NOT NULL,
  registry_url TEXT    NOT NULL,
  kind         INTEGER NOT NULL,
  from_version TEXT    NOT NULL,
  to_version   TEXT    NOT NULL,
  created_at   INTEGER NOT NULL,
  proto        BLOB    NOT NULL, -- kdelta.v1.ChangeSet
  PRIMARY KEY (system, name, registry_url, kind, from_version, to_version)
);

CREATE TABLE impact_assessments (
  scan_id      INTEGER NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
  target_ref   TEXT    NOT NULL,
  stream_id    TEXT    NOT NULL,
  from_version TEXT    NOT NULL,
  to_version   TEXT    NOT NULL,
  created_at   INTEGER NOT NULL,
  proto        BLOB    NOT NULL, -- kdelta.v1.ImpactAssessment
  PRIMARY KEY (scan_id, target_ref, stream_id, from_version, to_version)
);

CREATE TABLE upstream_resolutions (
  system       TEXT    NOT NULL,
  name         TEXT    NOT NULL,
  registry_url TEXT    NOT NULL, -- empty records a failed resolution (negative cache)
  fetched_at   INTEGER NOT NULL,
  PRIMARY KEY (system, name)
);
