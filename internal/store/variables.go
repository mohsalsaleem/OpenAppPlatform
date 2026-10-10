package store

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
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
	rows, e := j.s.Pool.Query(ctx, "SELECT key,provider_uuid,value_hash,intent_hash FROM oap_runtime_variables WHERE target_id=$1 AND resource_id=$2", j.target, ref)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]operator.VariableOwnership{}
	for rows.Next() {
		var key string
		var value operator.VariableOwnership
		if e = rows.Scan(&key, &value.UUID, &value.Hash, &value.Intent); e != nil {
			return nil, e
		}
		out[key] = value
	}
	return out, rows.Err()
}
func (j *variableJournal) Begin(ctx context.Context, ref, key, uuid, hash string) error {
	result, e := j.s.Pool.Exec(ctx, `INSERT INTO oap_runtime_variables(target_id,resource_id,key,provider_uuid,intent_hash) VALUES($1,$2,$3,$4,$5)
 ON CONFLICT(target_id,resource_id,key) DO UPDATE SET intent_hash=$5 WHERE oap_runtime_variables.provider_uuid=$4`, j.target, ref, key, uuid, hash)
	if e != nil {
		return e
	}
	if result.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}
func (j *variableJournal) Commit(ctx context.Context, ref, key, uuid, hash string) error {
	result, e := j.s.Pool.Exec(ctx, "UPDATE oap_runtime_variables SET provider_uuid=$4,value_hash=$5,intent_hash='' WHERE target_id=$1 AND resource_id=$2 AND key=$3 AND intent_hash=$5 AND (provider_uuid='' OR provider_uuid=$4)", j.target, ref, key, uuid, hash)
	if e != nil {
		return e
	}
	if result.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}
func (j *variableJournal) Forget(ctx context.Context, ref, key, uuid string) error {
	_, e := j.s.Pool.Exec(ctx, "DELETE FROM oap_runtime_variables WHERE target_id=$1 AND resource_id=$2 AND key=$3 AND provider_uuid=$4", j.target, ref, key, uuid)
	return e
}
