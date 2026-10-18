-- SPDX-License-Identifier: AGPL-3.0-only
-- Folio schema v2: the activity feed (every MCP tool call and web UI action).
-- Rows hold ids, names, outcomes and durations only: never request bodies,
-- document content or tokens.

CREATE TABLE activity (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  ts          TEXT    NOT NULL,                  -- RFC 3339 UTC with milliseconds
  source      TEXT    NOT NULL CHECK (source IN ('mcp', 'ui')),
  actor       TEXT    NOT NULL,
  action      TEXT    NOT NULL,
  target_kind TEXT    NOT NULL DEFAULT '',
  target_id   TEXT    NOT NULL DEFAULT '',
  outcome     TEXT    NOT NULL CHECK (outcome IN ('ok', 'error')),
  error_code  TEXT    NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  detail      TEXT    NOT NULL DEFAULT '{}' CHECK (json_valid(detail))
) STRICT;
CREATE INDEX activity_ts ON activity (ts);
