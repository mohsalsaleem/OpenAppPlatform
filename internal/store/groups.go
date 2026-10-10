package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"sort"
)

func (s *Store) ApplicationGroups(ctx context.Context) ([]domain.ApplicationGroup, error) {
	rows, e := s.Pool.Query(ctx, `SELECT g.id,g.name,g.version,jsonb_agg(jsonb_build_object(
 'id',a.id,'groupId',a.group_id,'manifest',a.spec,'createdAt',a.created_at,'updatedAt',a.updated_at,'version',a.version) ORDER BY (a.environment='staging') DESC,a.environment)
 FROM oap_application_groups g JOIN oap_applications a ON a.group_id=g.id GROUP BY g.id ORDER BY g.name,g.id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.ApplicationGroup{}
	for rows.Next() {
		var g domain.ApplicationGroup
		var raw []byte
		if e = rows.Scan(&g.ID, &g.Name, &g.Version, &raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &g.Environments); e != nil {
			return nil, e
		}
		out = append(out, g)
	}
	return out, rows.Err()
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
	if e = tx.QueryRow(ctx, "SELECT group_id FROM oap_applications WHERE id=$1", applicationID).Scan(&source); e != nil {
		return mapError(e)
	}
	if source == groupID {
		return domain.ErrConflict
	}
	locks := []string{source, groupID}
	sort.Strings(locks)
	for _, id := range locks {
		var version int64
		if e = tx.QueryRow(ctx, "SELECT version FROM oap_application_groups WHERE id=$1 FOR UPDATE", id).Scan(&version); e != nil {
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
	if e = tx.QueryRow(ctx, "SELECT count(*) FROM oap_applications WHERE group_id=$1", source).Scan(&count); e != nil {
		return e
	}
	if count != 1 {
		return errors.New("only a standalone environment can be linked")
	}
	result, e := tx.Exec(ctx, "UPDATE oap_applications SET group_id=$1 WHERE id=$2 AND group_id=$3", groupID, applicationID, source)
	if e != nil {
		return mapError(e)
	}
	if result.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	if _, e = tx.Exec(ctx, "UPDATE oap_application_groups SET version=version+1 WHERE id=ANY($1)", locks); e != nil {
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
	if e = tx.QueryRow(ctx, "SELECT version FROM oap_application_groups WHERE id=$1 FOR UPDATE", groupID).Scan(&version); e != nil {
		return mapError(e)
	}
	if version != expected {
		return domain.ErrConflict
	}
	var count int
	if e = tx.QueryRow(ctx, "SELECT count(*) FROM oap_applications WHERE group_id=$1", groupID).Scan(&count); e != nil {
		return e
	}
	if count < 2 {
		return domain.ErrConflict
	}
	var name string
	if e = tx.QueryRow(ctx, "SELECT name FROM oap_applications WHERE id=$1 AND group_id=$2 FOR UPDATE", applicationID, groupID).Scan(&name); e != nil {
		return mapError(e)
	}
	id := domain.NewID()
	if _, e = tx.Exec(ctx, "INSERT INTO oap_application_groups(id,name) VALUES($1,$2)", id, name); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE oap_applications SET group_id=$1 WHERE id=$2", id, applicationID); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "UPDATE oap_application_groups SET version=version+1 WHERE id=$1", groupID); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
