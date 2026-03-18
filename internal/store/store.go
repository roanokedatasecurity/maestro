package store

import (
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store is the Maestro persistence layer. All access to the SQLite database
// goes through typed methods on Store — no raw SQL leaks outside this package.
type Store struct {
	db   *sql.DB
	Path string
}

// Open opens (or creates) the SQLite database at path and applies any pending
// migrations. The caller must call Close() when done.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open sqlite at %q: %w", path, err)
	}
	// SQLite is single-writer; cap to one connection to avoid SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	s := &Store{db: db, Path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate applies embedded SQL migrations in filename order, skipping any that
// have already been recorded in the schema_migrations table.
func (s *Store) migrate() error {
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT     PRIMARY KEY,
			applied_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	for _, entry := range entries {
		version := entry.Name()

		var count int
		if err := s.db.QueryRow(
			"SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version,
		).Scan(&count); err != nil {
			return fmt.Errorf("check migration %s: %w", version, err)
		}
		if count > 0 {
			continue // already applied
		}

		sql, err := migrationsFS.ReadFile("migrations/" + version)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", version, err)
		}
		if _, err := s.db.Exec(string(sql)); err != nil {
			return fmt.Errorf("apply migration %s: %w", version, err)
		}
		if _, err := s.db.Exec(
			"INSERT INTO schema_migrations (version) VALUES (?)", version,
		); err != nil {
			return fmt.Errorf("record migration %s: %w", version, err)
		}
	}
	return nil
}

// newID returns a random 32-character hex string suitable for use as a primary
// key. Uses crypto/rand — no external dependency required.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("maestro/store: newID: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// nullTime scans a nullable DATETIME column into *time.Time.
func nullTime(nt *sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	return &nt.Time
}

// parseTime parses a non-nullable DATETIME column. The modernc SQLite driver
// returns datetime values in RFC3339 format ("2006-01-02T15:04:05Z"); we also
// accept the bare SQLite format for forward-compatibility.
func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
