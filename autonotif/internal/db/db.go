package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const DefaultPath = "/data/autonotif.db"

type Migration func(context.Context, *sql.DB) error

type Database struct {
	*sql.DB
}

func Open(ctx context.Context, path string, migrations ...Migration) (*Database, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("sqlite path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	database := &Database{DB: db}
	if err := database.configure(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := database.Migrate(ctx, migrations...); err != nil {
		_ = db.Close()
		return nil, err
	}
	return database, nil
}

func (d *Database) Migrate(ctx context.Context, migrations ...Migration) error {
	for _, migration := range migrations {
		if migration == nil {
			continue
		}
		if err := migration(ctx, d.DB); err != nil {
			return err
		}
	}
	return nil
}

func (d *Database) configure(ctx context.Context) error {
	pragmas := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
	}
	for _, pragma := range pragmas {
		if _, err := d.ExecContext(ctx, pragma); err != nil {
			return err
		}
	}
	return nil
}
