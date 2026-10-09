package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// Migrate applies immutable SQL migrations atomically and records their checksums.
// Version 001 remains idempotent so existing pre-migration previews are adopted.
func (s *Store) Migrate(ctx context.Context) error { return s.migrateFS(ctx, migrations) }
func (s *Store) migrateFS(ctx context.Context, source fs.FS) error {
	files, e := fs.Glob(source, "migrations/*.sql")
	if e != nil {
		return e
	}
	sort.Strings(files)
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(716382)"); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS oap_schema_migrations(version integer PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())`); e != nil {
		return e
	}
	seen := map[int]bool{}
	for _, file := range files {
		name := strings.TrimPrefix(file, "migrations/")
		version, e := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if e != nil || version < 1 || seen[version] {
			return errors.New("invalid or duplicate migration version")
		}
		seen[version] = true
		data, e := fs.ReadFile(source, file)
		if e != nil {
			return e
		}
		sum := sha256.Sum256(data)
		checksum := hex.EncodeToString(sum[:])
		var previous string
		var applied bool
		if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oap_schema_migrations WHERE version=$1)", version).Scan(&applied); e != nil {
			return e
		}
		if applied {
			if e = tx.QueryRow(ctx, "SELECT checksum FROM oap_schema_migrations WHERE version=$1", version).Scan(&previous); e != nil {
				return e
			}
			if previous != checksum {
				return fmt.Errorf("migration %03d checksum differs; applied migrations are immutable", version)
			}
			continue
		}
		if _, e = tx.Exec(ctx, string(data)); e != nil {
			return fmt.Errorf("migration %03d failed: %w", version, e)
		}
		if _, e = tx.Exec(ctx, "INSERT INTO oap_schema_migrations(version,checksum) VALUES($1,$2)", version, checksum); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
