package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"strconv"
	"strings"
)

func migrationChecksums(source fs.FS) (map[int]string, error) {
	files, err := fs.Glob(source, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	known := map[int]string{}
	for _, file := range files {
		version, err := strconv.Atoi(strings.SplitN(strings.TrimPrefix(file, "migrations/"), "_", 2)[0])
		if err != nil || version < 1 || known[version] != "" {
			return nil, errors.New("invalid or duplicate migration version")
		}
		data, err := fs.ReadFile(source, file)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		known[version] = hex.EncodeToString(sum[:])
	}
	return known, nil
}

// CheckSchema is a read-only image preflight. Missing known migrations may be
// applied by an update; unknown or changed applied migrations block downgrade.
func (s *Store) CheckSchema(ctx context.Context) error { return s.checkSchemaFS(ctx, migrations) }
func (s *Store) checkSchemaFS(ctx context.Context, source fs.FS) error {
	known, err := migrationChecksums(source)
	if err != nil {
		return err
	}
	rows, err := s.Pool.Query(ctx, "SELECT version,checksum FROM oap_schema_migrations")
	if err != nil {
		return errors.New("migration ledger is unavailable; preflight requires an initialized database")
	}
	defer rows.Close()
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return err
		}
		if known[version] != checksum {
			return errors.New("image is incompatible with the applied metadata migrations")
		}
	}
	return rows.Err()
}
