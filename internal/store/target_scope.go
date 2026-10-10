package store

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store/db"
	"strings"
)

// CreateTargetScope is create-only; it cannot repoint an existing connection.
func (s *Store) CreateTargetScope(ctx context.Context, t domain.Target) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	t.URL = strings.TrimRight(t.URL, "/")
	scope := "oap-target:" + t.Operator + ":" + t.URL + ":" + t.ProjectID + ":" + t.Environment
	if _, e = tx.Exec(ctx, db.LockTargetScope, scope); e != nil {
		return e
	}
	var exists bool
	if e = tx.QueryRow(ctx, db.TargetScopeExists, t.ID, t.Operator, t.URL, t.ProjectID, t.Environment).Scan(&exists); e != nil {
		return e
	}
	if exists {
		return domain.ErrConflict
	}
	raw, e := json.Marshal(t)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, db.InsertTarget, t.ID, raw, t.TokenEnv); e != nil {
		return mapError(e)
	}
	return tx.Commit(ctx)
}
