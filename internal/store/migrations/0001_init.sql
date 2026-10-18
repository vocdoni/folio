-- SPDX-License-Identifier: AGPL-3.0-only
-- Folio schema v1. Timestamps are RFC 3339 UTC text; JSON columns hold canonical JSON.

CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) STRICT;

CREATE TABLE brands (
  id         TEXT    NOT NULL,
  version    INTEGER NOT NULL CHECK (version >= 1),
  spec       TEXT    NOT NULL CHECK (json_valid(spec)),
  note       TEXT    NOT NULL DEFAULT '',
  created_by TEXT    NOT NULL,
  created_at TEXT    NOT NULL,
  PRIMARY KEY (id, version)
) STRICT;

CREATE TABLE brand_heads (
  id             TEXT    PRIMARY KEY,
  latest_version INTEGER NOT NULL,
  archived_at    TEXT,
  FOREIGN KEY (id, latest_version) REFERENCES brands (id, version)
) STRICT;

CREATE TABLE templates (
  id         TEXT    NOT NULL,
  version    INTEGER NOT NULL CHECK (version >= 1),
  spec       TEXT    NOT NULL CHECK (json_valid(spec)),
  note       TEXT    NOT NULL DEFAULT '',
  created_by TEXT    NOT NULL,
  created_at TEXT    NOT NULL,
  PRIMARY KEY (id, version)
) STRICT;

CREATE TABLE template_heads (
  id             TEXT    PRIMARY KEY,
  latest_version INTEGER NOT NULL,
  archived_at    TEXT,
  FOREIGN KEY (id, latest_version) REFERENCES templates (id, version)
) STRICT;

CREATE TABLE assets (
  id               TEXT    PRIMARY KEY,           -- UUIDv7
  name             TEXT    NOT NULL,
  description      TEXT    NOT NULL DEFAULT '',
  mime             TEXT    NOT NULL CHECK (mime IN ('image/png', 'image/jpeg', 'image/svg+xml')),
  sha256           TEXT    NOT NULL,              -- sanitised content blob
  size             INTEGER NOT NULL,
  width            INTEGER NOT NULL,
  height           INTEGER NOT NULL,
  rendition_sha256 TEXT,                          -- PNG rendition (SVG only), used by the DOCX path
  created_by       TEXT    NOT NULL,
  created_at       TEXT    NOT NULL,
  deleted_at       TEXT
) STRICT;
CREATE INDEX assets_list ON assets (created_at DESC, id DESC);

CREATE TABLE documents (
  id                 TEXT    PRIMARY KEY,         -- UUIDv7
  title              TEXT    NOT NULL,
  template_id        TEXT    NOT NULL,
  client             TEXT    NOT NULL DEFAULT '',
  status             TEXT    NOT NULL CHECK (status IN ('draft', 'published', 'trashed')),
  latest_revision    INTEGER NOT NULL,
  published_revision INTEGER,
  idempotency_key    TEXT CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 128),
  created_by         TEXT    NOT NULL,
  created_at         TEXT    NOT NULL,
  updated_at         TEXT    NOT NULL,
  trashed_at         TEXT
) STRICT;
CREATE UNIQUE INDEX documents_idempotency ON documents (created_by, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
CREATE INDEX documents_list ON documents (created_at DESC, id DESC);
CREATE INDEX documents_trashed ON documents (trashed_at) WHERE trashed_at IS NOT NULL;

CREATE TABLE revisions (
  document_id      TEXT    NOT NULL REFERENCES documents (id) ON DELETE CASCADE,
  revision         INTEGER NOT NULL CHECK (revision >= 1),
  parent_revision  INTEGER,
  template_id      TEXT    NOT NULL,
  template_version INTEGER NOT NULL,
  brand_id         TEXT    NOT NULL,
  brand_version    INTEGER NOT NULL,
  input_sha256     TEXT    NOT NULL,
  input_blob       TEXT    NOT NULL,
  computed         TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(computed)),
  warnings         TEXT    NOT NULL DEFAULT '[]' CHECK (json_valid(warnings)),
  review           TEXT CHECK (review IS NULL OR json_valid(review)),
  render_ms        INTEGER NOT NULL DEFAULT 0,
  created_by       TEXT    NOT NULL,
  created_at       TEXT    NOT NULL,
  PRIMARY KEY (document_id, revision),
  FOREIGN KEY (template_id, template_version) REFERENCES templates (id, version),
  FOREIGN KEY (brand_id, brand_version) REFERENCES brands (id, version)
) STRICT;

CREATE TABLE files (
  id               TEXT    PRIMARY KEY,           -- UUIDv4 from crypto/rand: the download capability
  document_id      TEXT,
  revision         INTEGER,
  template_id      TEXT,
  template_version INTEGER,
  kind             TEXT    NOT NULL CHECK (kind IN ('pdf', 'docx', 'preview')),
  page             INTEGER,
  name             TEXT    NOT NULL,
  mime             TEXT    NOT NULL,
  sha256           TEXT    NOT NULL,
  size             INTEGER NOT NULL,
  created_at       TEXT    NOT NULL,
  expires_at       TEXT,
  CHECK ((document_id IS NOT NULL AND revision IS NOT NULL AND template_id IS NULL AND template_version IS NULL)
      OR (document_id IS NULL AND revision IS NULL AND template_id IS NOT NULL AND template_version IS NOT NULL)),
  CHECK ((kind = 'preview') = (page IS NOT NULL)),
  FOREIGN KEY (document_id, revision) REFERENCES revisions (document_id, revision) ON DELETE CASCADE,
  FOREIGN KEY (template_id, template_version) REFERENCES templates (id, version)
) STRICT;
CREATE INDEX files_revision ON files (document_id, revision);
CREATE INDEX files_template ON files (template_id, template_version);
CREATE INDEX files_expiry ON files (expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE audit (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  ts          TEXT    NOT NULL,
  actor       TEXT    NOT NULL,
  action      TEXT    NOT NULL,
  target_kind TEXT    NOT NULL,
  target_id   TEXT    NOT NULL,
  outcome     TEXT    NOT NULL,
  detail      TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(detail))
) STRICT;
CREATE INDEX audit_ts ON audit (ts);
