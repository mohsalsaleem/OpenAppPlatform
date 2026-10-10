package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Store struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Store, error) {
	p, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, e
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, e
	}
	return &Store{p}, nil
}

func mapError(e error) error {
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var p *pgconn.PgError
	if errors.As(e, &p) && p.Code == "23505" {
		return domain.ErrConflict
	}
	return e
}
func (s *Store) SaveTarget(ctx context.Context, t domain.Target) error {
	b, e := json.Marshal(t)
	if e != nil {
		return e
	}
	_, e = s.Pool.Exec(ctx, "INSERT INTO oap_targets(id,spec,token_env) VALUES($1,$2,$3) ON CONFLICT(id) DO UPDATE SET spec=$2,token_env=$3", t.ID, b, t.TokenEnv)
	return e
}
func (s *Store) Target(ctx context.Context, id string) (domain.Target, error) {
	var t domain.Target
	var b []byte
	var env string
	e := s.Pool.QueryRow(ctx, "SELECT spec,token_env FROM oap_targets WHERE id=$1", id).Scan(&b, &env)
	if e != nil {
		return t, mapError(e)
	}
	e = json.Unmarshal(b, &t)
	t.TokenEnv = env
	return t, e
}
func (s *Store) Targets(ctx context.Context) ([]domain.Target, error) {
	rows, e := s.Pool.Query(ctx, "SELECT spec FROM oap_targets ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Target{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var t domain.Target
		if e = json.Unmarshal(b, &t); e != nil {
			return nil, e
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) CreateApplication(ctx context.Context, m domain.Manifest) (domain.Application, error) {
	a := domain.Application{ID: domain.NewID(), Manifest: m}
	a.GroupID = a.ID
	b, e := json.Marshal(m)
	if e != nil {
		return a, e
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return a, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "INSERT INTO oap_application_groups(id,name) VALUES($1,$2)", a.ID, m.Name); e != nil {
		return a, mapError(e)
	}
	e = tx.QueryRow(ctx, "INSERT INTO oap_applications(id,name,environment,spec,group_id) VALUES($1,$2,$3,$4,$1) RETURNING created_at,updated_at,version", a.ID, m.Name, m.Environment, b).Scan(&a.CreatedAt, &a.UpdatedAt, &a.Version)
	if e != nil {
		return a, mapError(e)
	}
	for _, c := range m.Components {
		if c.ResourceID != "" {
			if _, e = tx.Exec(ctx, "INSERT INTO oap_bindings(target_id,resource_id,application_id,component,ordinal) VALUES($1,$2,$3,$4,1)", m.TargetID, c.ResourceID, a.ID, c.Name); e != nil {
				return a, mapError(e)
			}
		}
	}
	return a, tx.Commit(ctx)
}
func (s *Store) Bind(ctx context.Context, targetID, resourceID, appID, component string, ordinal int) error {
	_, e := s.Pool.Exec(ctx, `INSERT INTO oap_bindings(target_id,resource_id,application_id,component,ordinal)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT(target_id,resource_id) DO NOTHING`, targetID, resourceID, appID, component, ordinal)
	if e != nil {
		return mapError(e)
	}
	var owner, comp string
	var number int
	if e = s.Pool.QueryRow(ctx, "SELECT application_id,component,ordinal FROM oap_bindings WHERE target_id=$1 AND resource_id=$2", targetID, resourceID).Scan(&owner, &comp, &number); e != nil {
		return e
	}
	if owner != appID || comp != component || number != ordinal {
		return domain.ErrConflict
	}
	_, e = s.Pool.Exec(ctx, "UPDATE oap_bindings SET retired_at=NULL WHERE target_id=$1 AND resource_id=$2 AND application_id=$3 AND component=$4 AND ordinal=$5", targetID, resourceID, appID, component, ordinal)
	if e != nil {
		return e
	}
	return nil
}

func scanApp(row pgx.Row) (domain.Application, error) {
	var a domain.Application
	var b []byte
	e := row.Scan(&a.ID, &b, &a.CreatedAt, &a.UpdatedAt, &a.Version, &a.GroupID)
	if e != nil {
		return a, mapError(e)
	}
	e = json.Unmarshal(b, &a.Manifest)
	return a, e
}
func (s *Store) Application(ctx context.Context, id string) (domain.Application, error) {
	return scanApp(s.Pool.QueryRow(ctx, "SELECT id,spec,created_at,updated_at,version,group_id FROM oap_applications WHERE id=$1", id))
}
func (s *Store) Applications(ctx context.Context) ([]domain.Application, error) {
	rows, e := s.Pool.Query(ctx, "SELECT id,spec,created_at,updated_at,version,group_id FROM oap_applications ORDER BY created_at DESC")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Application{}
	for rows.Next() {
		a, e := scanApp(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func ScanDeployment(row pgx.Row) (domain.Deployment, error) {
	var d domain.Deployment
	var spec, steps, source []byte
	e := row.Scan(&d.ID, &d.ApplicationID, &d.State, &spec, &steps, &d.CreatedAt, &d.UpdatedAt, &d.DefinitionVersion, &d.Operation, &source)
	if e != nil {
		return d, mapError(e)
	}
	if e = json.Unmarshal(spec, &d.Manifest); e != nil {
		return d, e
	}
	if len(source) > 0 && string(source) != "null" {
		if e = json.Unmarshal(source, &d.Source); e != nil {
			return d, e
		}
	}
	e = json.Unmarshal(steps, &d.Steps)
	return d, e
}

const deploymentColumns = "id,application_id,state,spec,steps,created_at,updated_at,definition_version,operation,source"

// DeploymentTx reuses the enqueue transaction connection while its advisory lock is held.
func (s *Store) DeploymentTx(ctx context.Context, tx pgx.Tx, id string) (domain.Deployment, error) {
	return ScanDeployment(tx.QueryRow(ctx, "SELECT "+deploymentColumns+" FROM oap_deployments WHERE id=$1", id))
}
func (s *Store) Deployment(ctx context.Context, id string) (domain.Deployment, error) {
	return ScanDeployment(s.Pool.QueryRow(ctx, "SELECT "+deploymentColumns+" FROM oap_deployments WHERE id=$1", id))
}
func (s *Store) Deployments(ctx context.Context, appID string) ([]domain.Deployment, error) {
	rows, e := s.Pool.Query(ctx, "SELECT "+deploymentColumns+" FROM oap_deployments WHERE application_id=$1 ORDER BY created_at DESC LIMIT 50", appID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.Deployment{}
	for rows.Next() {
		d, e := ScanDeployment(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) SaveDeployment(ctx context.Context, d domain.Deployment) error {
	b, e := json.Marshal(d.Steps)
	if e != nil {
		return e
	}
	_, e = s.Pool.Exec(ctx, "UPDATE oap_deployments SET state=$2,steps=$3,updated_at=now() WHERE id=$1", d.ID, d.State, b)
	return e
}

func (s *Store) ApplicationTx(ctx context.Context, tx pgx.Tx, id string) (domain.Application, error) {
	return scanApp(tx.QueryRow(ctx, "SELECT id,spec,created_at,updated_at,version,group_id FROM oap_applications WHERE id=$1", id))
}
func (s *Store) UpdateApplicationTx(ctx context.Context, tx pgx.Tx, id string, m domain.Manifest, expectedVersion int64) (domain.Application, error) {
	b, e := json.Marshal(m)
	if e != nil {
		return domain.Application{}, e
	}
	return scanApp(tx.QueryRow(ctx, `UPDATE oap_applications SET spec=$2,version=version+1,updated_at=now() WHERE id=$1 AND version=$3 RETURNING id,spec,created_at,updated_at,version,group_id`, id, b, expectedVersion))
}

// Bindings are the current instance references, independent of release history.
type Binding struct {
	Retired    bool
	Component  string
	Ordinal    int
	ResourceID string
}

func (s *Store) Bindings(ctx context.Context, id string) ([]Binding, error) {
	rows, e := s.Pool.Query(ctx, "SELECT component,ordinal,resource_id,retired_at IS NOT NULL FROM oap_bindings WHERE application_id=$1 ORDER BY component,ordinal", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Binding{}
	for rows.Next() {
		var b Binding
		if e = rows.Scan(&b.Component, &b.Ordinal, &b.ResourceID, &b.Retired); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) SaveDeploymentTx(ctx context.Context, tx pgx.Tx, d *domain.Deployment) error {
	b, e := json.Marshal(d.Steps)
	if e != nil {
		return e
	}
	return tx.QueryRow(ctx, "UPDATE oap_deployments SET state=$2,steps=$3,updated_at=now() WHERE id=$1 RETURNING updated_at", d.ID, d.State, b).Scan(&d.UpdatedAt)
}

func (s *Store) TargetTx(ctx context.Context, tx pgx.Tx, id string) (domain.Target, error) {
	var t domain.Target
	var spec []byte
	var env string
	e := tx.QueryRow(ctx, "SELECT spec,token_env FROM oap_targets WHERE id=$1", id).Scan(&spec, &env)
	if e != nil {
		return t, mapError(e)
	}
	e = json.Unmarshal(spec, &t)
	t.TokenEnv = env
	return t, e
}
func (s *Store) BindingTx(ctx context.Context, tx pgx.Tx, id, component string, ordinal int) (string, error) {
	var ref string
	e := tx.QueryRow(ctx, "SELECT resource_id FROM oap_bindings WHERE application_id=$1 AND component=$2 AND ordinal=$3 AND retired_at IS NULL", id, component, ordinal).Scan(&ref)
	return ref, mapError(e)
}

func (s *Store) RetireBindingTx(ctx context.Context, tx pgx.Tx, target, ref, app, component string, ordinal int) error {
	command, e := tx.Exec(ctx, "UPDATE oap_bindings SET retired_at=COALESCE(retired_at,now()) WHERE target_id=$1 AND resource_id=$2 AND application_id=$3 AND component=$4 AND ordinal=$5", target, ref, app, component, ordinal)
	if e != nil {
		return e
	}
	if command.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}
