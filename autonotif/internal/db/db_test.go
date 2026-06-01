package db

import (
	"context"
	"database/sql"
	"testing"
)

func TestOpenRunsMigrations(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:", func(ctx context.Context, db *sql.DB) error {
		_, err := db.ExecContext(ctx, `CREATE TABLE example (id INTEGER PRIMARY KEY)`)
		return err
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	var name string
	if err := database.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'example'`).Scan(&name); err != nil {
		t.Fatalf("migration table missing: %v", err)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(context.Background(), " "); err == nil {
		t.Fatal("Open() expected error for empty path")
	}
}
