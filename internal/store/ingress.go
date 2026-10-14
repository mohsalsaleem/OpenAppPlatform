package store

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"time"
)

// LatestIngressTx includes withdrawal publications. A nil ingress in the latest
// frozen release is a durable tombstone, not permission to restore older routes.
func (s *Store) LatestIngressTx(ctx context.Context, tx pgx.Tx, target, app string) (domain.Deployment, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT deployment_id FROM oap_ingress_publications WHERE target_id=$1 AND application_id=$2 AND phase='applied' ORDER BY created_at DESC,deployment_id DESC LIMIT 1`, target, app).Scan(&id)
	if err != nil {
		return domain.Deployment{}, mapError(err)
	}
	return s.DeploymentTx(ctx, tx, id)
}

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
	if key != authority {
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
	result, err := tx.Exec(ctx, `INSERT INTO oap_ingress_authority(target_id,application_id,authority_hash,config_hash) VALUES($1,$2,$3,$4) ON CONFLICT(target_id) DO UPDATE SET config_hash=EXCLUDED.config_hash WHERE oap_ingress_authority.authority_hash=EXCLUDED.authority_hash AND oap_ingress_authority.config_hash IN ($5,$4)`, p.TargetID, p.ApplicationID, p.AuthorityHash, p.CandidateHash, p.ExpectedHash)
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

// IngressSiblings returns last published release definitions, never unsaved or
// unshipped desired specs. A sibling's uncertain publication fences the target.
func (s *Store) IngressSiblings(ctx context.Context, target, app string) ([]domain.Deployment, error) {
	var pending bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM oap_ingress_publications WHERE target_id=$1 AND application_id<>$2 AND phase<>'applied')`, target, app).Scan(&pending); err != nil {
		return nil, err
	}
	if pending {
		return nil, domain.ErrConflict
	}
	rows, err := s.Pool.Query(ctx, `SELECT latest.deployment_id FROM (SELECT DISTINCT ON(application_id) deployment_id,application_id FROM oap_ingress_publications WHERE target_id=$1 AND application_id<>$2 AND phase='applied' ORDER BY application_id,created_at DESC,deployment_id DESC) latest JOIN oap_deployments d ON d.id=latest.deployment_id WHERE d.spec->'ingress' IS NOT NULL AND d.spec->'ingress'<>'null'::jsonb ORDER BY latest.application_id LIMIT 32`, target, app)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) >= 32 {
		return nil, domain.ErrConflict
	}
	out := make([]domain.Deployment, 0, len(ids))
	for _, id := range ids {
		d, e := s.Deployment(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, nil
}
