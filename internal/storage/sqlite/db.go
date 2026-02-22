// Package sqlite implements the storage interfaces using SQLite via modernc.org/sqlite.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"runtime"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store implements storage.Store using SQLite.
type Store struct {
	write *sql.DB // single-writer connection
	read  *sql.DB // multi-reader pool
}

// New opens a SQLite database, runs migrations, and returns a Store.
func New(dsn string) (*Store, error) {
	pragmas := "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"

	// For :memory: databases, use shared cache so read/write pools share the same data
	var fullDSN string
	if dsn == ":memory:" {
		fullDSN = "file::memory:?mode=memory&cache=shared&" + pragmas
	} else {
		fullDSN = "file:" + dsn + "?" + pragmas
	}

	write, err := sql.Open("sqlite", fullDSN)
	if err != nil {
		return nil, fmt.Errorf("open write db: %w", err)
	}
	write.SetMaxOpenConns(1)

	read, err := sql.Open("sqlite", fullDSN)
	if err != nil {
		write.Close()
		return nil, fmt.Errorf("open read db: %w", err)
	}
	read.SetMaxOpenConns(max(4, runtime.NumCPU()))

	if err := runMigrations(write); err != nil {
		write.Close()
		read.Close()
		return nil, fmt.Errorf("migrations: %w", err)
	}

	return &Store{write: write, read: read}, nil
}

// runMigrations applies embedded SQL migrations using goose.
// fs.Sub strips the "migrations/" prefix so goose sees files at the FS root.
func runMigrations(db *sql.DB) error {
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("sub fs: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, fsys)
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	_, err = provider.Up(context.Background())
	return err
}

// Ping verifies database connectivity by pinging the read pool.
func (s *Store) Ping(ctx context.Context) error {
	return s.read.PingContext(ctx)
}

// Close closes both database connections.
func (s *Store) Close() error {
	return errors.Join(s.write.Close(), s.read.Close())
}
