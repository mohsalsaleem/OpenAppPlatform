package store

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store/db"
)

type variableJournal struct {
	s                      *Store
	target, app, component string
	ordinal                int
}

func (s *Store) VariableJournal(target, app, component string, ordinal int) operator.VariableJournal {
	return &variableJournal{s, target, app, component, ordinal}
}
func (j *variableJournal) Reserve(ctx context.Context, ref string) error {
	return j.s.Bind(ctx, j.target, ref, j.app, j.component, j.ordinal)
}
func (j *variableJournal) Variables(ctx context.Context, ref string) (map[string]operator.VariableOwnership, error) {
	rows, e := db.New(j.s.Pool).ListRuntimeVariables(ctx, db.ListRuntimeVariablesParams{TargetID: j.target, ResourceID: ref})
	if e != nil {
		return nil, e
	}
	out := map[string]operator.VariableOwnership{}
	for _, row := range rows {
		out[row.Key] = operator.VariableOwnership{UUID: row.ProviderUuid, Hash: row.ValueHash, Intent: row.IntentHash}
	}
	return out, nil
}
func (j *variableJournal) Begin(ctx context.Context, ref, key, uuid, hash string) error {
	result, e := j.s.Pool.Exec(ctx, db.BeginRuntimeVariable, j.target, ref, key, uuid, hash)
	if e != nil {
		return e
	}
	if result.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}
func (j *variableJournal) Commit(ctx context.Context, ref, key, uuid, hash string) error {
	result, e := j.s.Pool.Exec(ctx, db.CommitRuntimeVariable, j.target, ref, key, uuid, hash)
	if e != nil {
		return e
	}
	if result.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}
func (j *variableJournal) Forget(ctx context.Context, ref, key, uuid string) error {
	_, e := j.s.Pool.Exec(ctx, db.DeleteRuntimeVariable, j.target, ref, key, uuid)
	return e
}
