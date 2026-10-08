package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenAppliesSchemaAndDefaults(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	want := []string{"files", "users", "items", "tags", "item_tags", "collections",
		"item_collections", "item_ai", "jobs"}
	for _, table := range want {
		var name string
		err := d.QueryRowContext(context.Background(),
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}

	var users int
	if err := d.QueryRow(`SELECT COUNT(*) FROM users WHERE id=1 AND name='owner'`).Scan(&users); err != nil || users != 1 {
		t.Errorf("owner row: %d %v", users, err)
	}
	var cols int
	if err := d.QueryRow(`SELECT COUNT(*) FROM collections WHERE id=1`).Scan(&cols); err != nil || cols != 1 {
		t.Errorf("default collection: %d %v", cols, err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "y.db")
	d1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if _, err := d1.Exec(`INSERT INTO tags(name, created_at) VALUES('keep', 1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	d1.Close()

	d2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer d2.Close()
	var n int
	if err := d2.QueryRow(`SELECT COUNT(*) FROM tags WHERE name='keep'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("data lost on reopen: %d %v", n, err)
	}
	// foreign keys stay on, so orphan tags are rejected by the schema contract
	if _, err := d2.Exec(`INSERT INTO item_tags(item_id, tag_id) VALUES(9999, 9999)`); err == nil {
		t.Error("expected foreign key violation")
	}
}

func TestPragmasApplied(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "z.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	var mode string
	if err := d.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %s, want wal", mode)
	}
	var fk int
	if err := d.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d", fk)
	}
}
