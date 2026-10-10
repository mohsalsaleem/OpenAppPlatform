package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store/db"
	"sort"
)

func (s *Store) ApplicationGroups(ctx context.Context) ([]domain.ApplicationGroup, error) {
	rows, e := db.New(s.Pool).ListApplicationGroups(ctx)
	if e != nil {
		return nil, e
	}
	out := []domain.ApplicationGroup{}
	for _, row := range rows {
		group := domain.ApplicationGroup{ID: row.ID, Name: row.Name, Version: row.Version}
		if e = json.Unmarshal(row.JsonbAgg, &group.Environments); e != nil {
			return nil, e
		}
		out = append(out, group)
	}
	return out, nil
}

// LinkEnvironment only moves metadata. Existing environment IDs and authorization
// scopes remain intact; the source must be standalone, not another multi-env app.
func (s *Store) LinkEnvironment(ctx context.Context, groupID, applicationID string, expectedGroup, expectedSource int64) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var source string
	if e = tx.QueryRow(ctx, db.GetApplicationGroupID, applicationID).Scan(&source); e != nil {
		return mapError(e)
	}
	if source == groupID {
		return domain.ErrConflict
	}
	locks := []string{source, groupID}
	sort.Strings(locks)
	for _, id := range locks {
		var version int64
		if e = tx.QueryRow(ctx, db.LockApplicationGroup, id).Scan(&version); e != nil {
			return mapError(e)
		}
		expected := expectedGroup
		if id == source {
			expected = expectedSource
		}
		if version != expected {
			return domain.ErrConflict
		}
	}
	var count int
	if e = tx.QueryRow(ctx, db.CountGroupEnvironments, source).Scan(&count); e != nil {
		return e
	}
	if count != 1 {
		return errors.New("only a standalone environment can be linked")
	}
	result, e := tx.Exec(ctx, db.MoveEnvironmentIfGroup, groupID, applicationID, source)
	if e != nil {
		return mapError(e)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	if _, e = tx.Exec(ctx, db.BumpApplicationGroups, locks); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (s *Store) UnlinkEnvironment(ctx context.Context, groupID, applicationID string, expected int64) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var version int64
	if e = tx.QueryRow(ctx, db.LockApplicationGroup, groupID).Scan(&version); e != nil {
		return mapError(e)
	}
	if version != expected {
		return domain.ErrConflict
	}
	var count int
	if e = tx.QueryRow(ctx, db.CountGroupEnvironments, groupID).Scan(&count); e != nil {
		return e
	}
	if count < 2 {
		return domain.ErrConflict
	}
	var name string
	if e = tx.QueryRow(ctx, db.LockEnvironmentName, applicationID, groupID).Scan(&name); e != nil {
		return mapError(e)
	}
	id := domain.NewID()
	if _, e = tx.Exec(ctx, db.InsertApplicationGroup, id, name); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, db.MoveEnvironment, id, applicationID); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, db.BumpApplicationGroup, groupID); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
