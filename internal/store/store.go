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
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store/db"
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
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = db.New(s.Pool).UpsertTarget(ctx, db.UpsertTargetParams{ID: t.ID, Spec: b, TokenEnv: t.TokenEnv})
	return err
}
func (s *Store) Target(ctx context.Context, id string) (domain.Target, error) {
	row, err := db.New(s.Pool).GetTarget(ctx, id)
	if err != nil {
		return domain.Target{}, mapError(err)
	}
	var target domain.Target
	err = json.Unmarshal(row.Spec, &target)
	target.TokenEnv = row.TokenEnv
	return target, err
}
func (s *Store) Targets(ctx context.Context) ([]domain.Target, error) {
	rows, err := db.New(s.Pool).ListTargets(ctx)
	if err != nil {
		return nil, err
	}
	out := []domain.Target{}
	for _, raw := range rows {
		var target domain.Target
		if err = json.Unmarshal(raw, &target); err != nil {
			return nil, err
		}
		out = append(out, target)
	}
	return out, nil
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
	if _, e = tx.Exec(ctx, db.InsertApplicationGroup, a.ID, m.Name); e != nil {
		return a, mapError(e)
	}
	e = tx.QueryRow(ctx, db.InsertApplication, a.ID, m.Name, m.Environment, b).Scan(&a.CreatedAt, &a.UpdatedAt, &a.Version)
	if e != nil {
		return a, mapError(e)
	}
	for _, c := range m.Components {
		if c.ResourceID != "" {
			if _, e = tx.Exec(ctx, db.InsertObservedBinding, m.TargetID, c.ResourceID, a.ID, c.Name); e != nil {
				return a, mapError(e)
			}
		}
	}
	return a, tx.Commit(ctx)
}
func (s *Store) Bind(ctx context.Context, targetID, resourceID, appID, component string, ordinal int) error {
	_, e := s.Pool.Exec(ctx, db.ReserveBinding, targetID, resourceID, appID, component, ordinal)
	if e != nil {
		return mapError(e)
	}
	var owner, comp string
	var number int
	if e = s.Pool.QueryRow(ctx, db.GetBindingOwner, targetID, resourceID).Scan(&owner, &comp, &number); e != nil {
		return e
	}
	if owner != appID || comp != component || number != ordinal {
		return domain.ErrConflict
	}
	_, e = s.Pool.Exec(ctx, db.ReactivateBinding, targetID, resourceID, appID, component, ordinal)
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
	return applicationRow(db.New(s.Pool).GetApplication(ctx, id))
}
func (s *Store) Applications(ctx context.Context) ([]domain.Application, error) {
	rows, err := db.New(s.Pool).ListApplications(ctx)
	if err != nil {
		return nil, err
	}
	out := []domain.Application{}
	for _, row := range rows {
		app, err := applicationRow(db.GetApplicationRow(row), nil)
		if err != nil {
			return nil, err
		}
		out = append(out, app)
	}
	return out, nil
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
	if e == nil && len(d.Steps) > 0 && d.Steps[0].Control != nil {
		d.Control = d.Steps[0].Control
		if d.Control.Mode == "cancel" {
			d.State = "cancelled"
		} else if d.Control.Mode == "abandon" {
			d.State = "abandoned"
		}
	}
	return d, e
}

const deploymentColumns = "id,application_id,state,spec,steps,created_at,updated_at,definition_version,operation,source"

// DeploymentTx reuses the enqueue transaction connection while its advisory lock is held.
func (s *Store) DeploymentTx(ctx context.Context, tx pgx.Tx, id string) (domain.Deployment, error) {
	return ScanDeployment(tx.QueryRow(ctx, db.GetDeployment, id))
}
func (s *Store) Deployment(ctx context.Context, id string) (domain.Deployment, error) {
	return ScanDeployment(s.Pool.QueryRow(ctx, db.GetDeployment, id))
}
func (s *Store) Deployments(ctx context.Context, appID string) ([]domain.Deployment, error) {
	rows, e := s.Pool.Query(ctx, db.ListDeployments, appID)
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
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = s.SaveDeploymentTx(ctx, tx, &d); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ApplicationTx(ctx context.Context, tx pgx.Tx, id string) (domain.Application, error) {
	return applicationRow(db.New(tx).GetApplication(ctx, id))
}
func (s *Store) UpdateApplicationTx(ctx context.Context, tx pgx.Tx, id string, m domain.Manifest, expectedVersion int64) (domain.Application, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return domain.Application{}, err
	}
	row, err := db.New(tx).UpdateApplication(ctx, db.UpdateApplicationParams{ID: id, Spec: raw, Version: expectedVersion})
	return applicationRow(db.GetApplicationRow(row), err)
}

// Bindings are the current instance references, independent of release history.
type Binding struct {
	Retired    bool
	Component  string
	Ordinal    int
	ResourceID string
}

func (s *Store) Bindings(ctx context.Context, id string) ([]Binding, error) {
	rows, err := db.New(s.Pool).ListBindings(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []Binding{}
	for _, row := range rows {
		out = append(out, Binding{Component: row.Component, Ordinal: int(row.Ordinal), ResourceID: row.ResourceID, Retired: row.Retired})
	}
	return out, nil
}
func (s *Store) SaveDeploymentTx(ctx context.Context, tx pgx.Tx, d *domain.Deployment) error {
	raw, err := json.Marshal(d.Steps)
	if err != nil {
		return err
	}
	at, err := db.New(tx).UpdateDeploymentReturningTimestamp(ctx, db.UpdateDeploymentReturningTimestampParams{ID: d.ID, State: deploymentStorageState(*d), Steps: raw})
	if err == nil {
		d.UpdatedAt = at.Time
		err = recordNotification(ctx, tx, *d)
	}
	return err
}

func (s *Store) TargetTx(ctx context.Context, tx pgx.Tx, id string) (domain.Target, error) {
	row, err := db.New(tx).GetTarget(ctx, id)
	if err != nil {
		return domain.Target{}, mapError(err)
	}
	var target domain.Target
	err = json.Unmarshal(row.Spec, &target)
	target.TokenEnv = row.TokenEnv
	return target, err
}
func (s *Store) BindingTx(ctx context.Context, tx pgx.Tx, id, component string, ordinal int) (string, error) {
	ref, err := db.New(tx).GetActiveBinding(ctx, db.GetActiveBindingParams{ApplicationID: id, Component: component, Ordinal: int32(ordinal)})
	return ref, mapError(err)
}

func (s *Store) RetireBindingTx(ctx context.Context, tx pgx.Tx, target, ref, app, component string, ordinal int) error {
	command, e := tx.Exec(ctx, db.RetireBinding, target, ref, app, component, ordinal)
	if e != nil {
		return e
	}
	if command.RowsAffected() != 1 {
		return domain.ErrConflict
	}
	return nil
}

func deploymentStorageState(d domain.Deployment) string {
	if len(d.Steps) > 0 && d.Steps[0].Control != nil {
		control := d.Steps[0].Control
		if control.Mode == "cancel" {
			return "failed"
		}
		if control.Mode == "abandon" {
			if control.ResolvedAt == nil {
				return "attention"
			}
			return "failed"
		}
	}
	return d.State
}

func applicationRow(row db.GetApplicationRow, err error) (domain.Application, error) {
	if err != nil {
		return domain.Application{}, mapError(err)
	}
	app := domain.Application{ID: row.ID, GroupID: row.GroupID, Version: row.Version, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
	err = json.Unmarshal(row.Spec, &app.Manifest)
	return app, err
}
