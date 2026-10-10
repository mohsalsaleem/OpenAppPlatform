package store

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
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
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", scope); e != nil {
		return e
	}
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM oap_targets WHERE id=$1 OR (spec->>'operator'=$2 AND rtrim(spec->>'url','/')=$3 AND spec->>'projectId'=$4 AND spec->>'environment'=$5))`, t.ID, t.Operator, t.URL, t.ProjectID, t.Environment).Scan(&exists); e != nil {
		return e
	}
	if exists {
		return domain.ErrConflict
	}
	raw, e := json.Marshal(t)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO oap_targets(id,spec,token_env) VALUES($1,$2,$3)", t.ID, raw, t.TokenEnv); e != nil {
		return mapError(e)
	}
	return tx.Commit(ctx)
}
