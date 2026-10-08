// Package db opens the SQLite database and applies the schema.
package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go driver, no cgo
)

const schema = `
CREATE TABLE IF NOT EXISTS files (
  sha256     TEXT PRIMARY KEY,
  ext        TEXT NOT NULL,
  mime       TEXT NOT NULL,
  size       INTEGER NOT NULL,
  width      INTEGER NOT NULL DEFAULT 0,
  height     INTEGER NOT NULL DEFAULT 0,
  animated   INTEGER NOT NULL DEFAULT 0,
  frames     INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
  id        INTEGER PRIMARY KEY,
  name      TEXT UNIQUE NOT NULL,
  pass_hash TEXT,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS items (
  id              INTEGER PRIMARY KEY,
  user_id         INTEGER NOT NULL DEFAULT 1,
  sha256          TEXT NOT NULL REFERENCES files(sha256),
  name            TEXT NOT NULL DEFAULT '',
  source_platform TEXT NOT NULL DEFAULT '',
  source_hint     TEXT NOT NULL DEFAULT '',
  note            TEXT NOT NULL DEFAULT '',
  favorite        INTEGER NOT NULL DEFAULT 0,
  use_count       INTEGER NOT NULL DEFAULT 0,
  last_used_at    INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL,
  deleted_at      INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_items_user_sha ON items(user_id, sha256);
CREATE INDEX IF NOT EXISTS ix_items_created ON items(deleted_at, created_at DESC);

CREATE TABLE IF NOT EXISTS tags (
  id         INTEGER PRIMARY KEY,
  name       TEXT UNIQUE NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS item_tags (
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  source  TEXT NOT NULL DEFAULT 'manual',
  PRIMARY KEY (item_id, tag_id)
);

CREATE TABLE IF NOT EXISTS collections (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL DEFAULT 1,
  name       TEXT NOT NULL,
  parent_id  INTEGER REFERENCES collections(id),
  sort       INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS item_collections (
  item_id       INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  collection_id INTEGER NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
  PRIMARY KEY (item_id, collection_id)
);

CREATE TABLE IF NOT EXISTS item_ai (
  item_id     INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
  description TEXT NOT NULL DEFAULT '',
  ocr         TEXT NOT NULL DEFAULT '',
  model       TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS jobs (
  id         INTEGER PRIMARY KEY,
  item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL DEFAULT 'tag',
  status     TEXT NOT NULL DEFAULT 'pending',
  attempts   INTEGER NOT NULL DEFAULT 0,
  error      TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ix_jobs_status ON jobs(status, id);
`

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows a single writer; keeping one connection avoids lock churn.
	d.SetMaxOpenConns(1)
	if err := d.Ping(); err != nil {
		d.Close()
		return nil, err
	}
	if _, err := d.Exec(schema); err != nil {
		d.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	// Default single user (multi-user ready).
	if _, err := d.Exec(`INSERT OR IGNORE INTO users(id, name, created_at) VALUES(1, 'owner', 0)`); err != nil {
		d.Close()
		return nil, err
	}
	// Default collections.
	if _, err := d.Exec(`INSERT OR IGNORE INTO collections(id, user_id, name, sort, created_at) VALUES(1, 1, '默认收藏', 1, 0)`); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}
