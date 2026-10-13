package store

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"time"
)

// IngressPublication is private dispatch evidence. Native config/ETag never enter
// API projections. The controller holds the target's publication lock around it.
type IngressPublication struct {
	DeploymentID, ApplicationID, TargetID, AuthorityHash, ExpectedHash, ExpectedETag, CandidateHash, Phase string
	Config                                                                                                 json.RawMessage
	DispatchedAt                                                                                           *time.Time
}

func (s *Store) IngressPublication(ctx context.Context, id string) (IngressPublication, error) {
	p := IngressPublication{DeploymentID: id}
	err := s.Pool.QueryRow(ctx, `SELECT application_id,target_id,authority_hash,expected_hash,expected_etag,candidate_hash,candidate_config,phase,dispatched_at FROM oap_ingress_publications WHERE deployment_id=$1`, id).Scan(&p.ApplicationID, &p.TargetID, &p.AuthorityHash, &p.ExpectedHash, &p.ExpectedETag, &p.CandidateHash, &p.Config, &p.Phase, &p.DispatchedAt)
	return p, mapError(err)
}
func (s *Store) PrepareIngress(ctx context.Context, p IngressPublication) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO oap_ingress_publications(deployment_id,application_id,target_id,authority_hash,expected_hash,expected_etag,candidate_hash,candidate_config,phase) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'prepared')`, p.DeploymentID, p.ApplicationID, p.TargetID, p.AuthorityHash, p.ExpectedHash, p.ExpectedETag, p.CandidateHash, []byte(p.Config))
	return mapError(err)
}
func (s *Store) IngressExpected(ctx context.Context, target, app, authority, bootstrap string) (string, error) {
	var owner, hash, key string
	err := s.Pool.QueryRow(ctx, `SELECT application_id,authority_hash,config_hash FROM oap_ingress_authority WHERE target_id=$1`, target).Scan(&owner, &key, &hash)
	if mapError(err) == domain.ErrNotFound {
		return bootstrap, nil
	}
	if err != nil {
		return "", err
	}
	if owner != app || key != authority {
		return "", domain.ErrConflict
	}
	return hash, nil
}
func (s *Store) DispatchIngress(ctx context.Context, id string) error {
	result, err := s.Pool.Exec(ctx, `UPDATE oap_ingress_publications SET phase='dispatching',dispatched_at=now() WHERE deployment_id=$1 AND phase='prepared'`, id)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}
func (s *Store) ConfirmIngress(ctx context.Context, p IngressPublication) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `INSERT INTO oap_ingress_authority(target_id,application_id,authority_hash,config_hash) VALUES($1,$2,$3,$4) ON CONFLICT(target_id) DO UPDATE SET config_hash=EXCLUDED.config_hash WHERE oap_ingress_authority.application_id=EXCLUDED.application_id AND oap_ingress_authority.authority_hash=EXCLUDED.authority_hash`, p.TargetID, p.ApplicationID, p.AuthorityHash, p.CandidateHash)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	_, err = tx.Exec(ctx, `UPDATE oap_ingress_publications SET phase='applied',dispatched_at=COALESCE(dispatched_at,now()) WHERE deployment_id=$1`, p.DeploymentID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
