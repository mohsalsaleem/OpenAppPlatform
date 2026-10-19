// Package sqlite implements the operational store and queue on SQLite in WAL
// mode with full synchronous commits, so an acknowledged write survives a
// crash of the OAP process. One connection serves all calls: OAP is the only
// writer, matching SQLite's single-writer model.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	_ "modernc.org/sqlite"
)

// Store is the SQLite operational store.
type Store struct {
	db *sql.DB
	// beforeCommit, if set, runs inside each write transaction just before it
	// commits. Tests use it to inject failures.
	beforeCommit func() error
}

var migrations = []string{
	`CREATE TABLE journal (
		revision  INTEGER PRIMARY KEY AUTOINCREMENT,
		at        INTEGER NOT NULL,
		kind      TEXT    NOT NULL,
		operation TEXT    NOT NULL DEFAULT '',
		step      INTEGER NOT NULL DEFAULT 0,
		state     TEXT    NOT NULL DEFAULT '',
		data      BLOB
	)`,
	`CREATE INDEX journal_operation ON journal (operation, revision)`,
	`CREATE TABLE queue (
		seq        INTEGER PRIMARY KEY AUTOINCREMENT,
		key        TEXT    NOT NULL UNIQUE,
		kind       TEXT    NOT NULL,
		payload    BLOB,
		attempts   INTEGER NOT NULL DEFAULT 0,
		leased_by  TEXT    NOT NULL DEFAULT '',
		lease_ends INTEGER NOT NULL DEFAULT 0
	)`,
}

// Open opens or creates the store at path and applies pending migrations.
// The path ":memory:" opens a private in-memory database, for tests.
func Open(ctx context.Context, path string) (*Store, error) {
	q := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "synchronous(FULL)", "busy_timeout(5000)", "foreign_keys(ON)"} {
		q.Add("_pragma", p)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var applied int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&applied); err != nil {
		return err
	}
	if applied > len(migrations) {
		return fmt.Errorf("sqlite: database schema version %d is newer than this OAP (%d)", applied, len(migrations))
	}
	for v := applied + 1; v <= len(migrations); v++ {
		err := s.tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, migrations[v-1]); err != nil {
				return fmt.Errorf("migration %d: %w", v, err)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, v)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// tx runs fn in a write transaction that commits only if fn and the
// beforeCommit hook succeed.
func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	if s.beforeCommit != nil {
		if err := s.beforeCommit(); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// ErrEmpty is returned when Append is called without entries.
var ErrEmpty = errors.New("sqlite: no entries to append")

// Append stores entries atomically and returns the last revision assigned.
func (s *Store) Append(ctx context.Context, entries ...ops.Entry) (uint64, error) {
	if len(entries) == 0 {
		return 0, ErrEmpty
	}
	var last uint64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		for _, e := range entries {
			res, err := tx.ExecContext(ctx,
				`INSERT INTO journal (at, kind, operation, step, state, data) VALUES (?, ?, ?, ?, ?, ?)`,
				e.At.UnixNano(), e.Kind, e.Operation, e.Step, e.State, e.Data)
			if err != nil {
				return err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			last = uint64(id)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return last, nil
}

// Since returns up to limit entries after revision, oldest first.
func (s *Store) Since(ctx context.Context, revision uint64, limit int) ([]ops.Entry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT revision, at, kind, operation, step, state, data FROM journal WHERE revision > ? ORDER BY revision LIMIT ?`,
		revision, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ops.Entry
	for rows.Next() {
		var e ops.Entry
		var at int64
		if err := rows.Scan(&e.Revision, &at, &e.Kind, &e.Operation, &e.Step, &e.State, &e.Data); err != nil {
			return nil, err
		}
		e.At = time.Unix(0, at).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// Head returns the latest revision, or 0 for an empty journal.
func (s *Store) Head(ctx context.Context) (uint64, error) {
	// sqlite_sequence keeps the highest revision ever assigned, even if rows
	// were removed, so revisions never go backwards.
	var head sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name = 'journal'`).Scan(&head)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return uint64(head.Int64), err
}

// Check runs SQLite's integrity check and returns an error describing any damage.
func (s *Store) Check(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		if line != "ok" {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("sqlite: integrity check failed: %v", problems)
	}
	return nil
}
