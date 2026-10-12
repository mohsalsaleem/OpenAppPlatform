package controller

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store/db"
)

type DatabaseLinkRequest struct {
	Name            string `json:"name"`
	ResourceID      string `json:"resourceId"`
	Endpoint        string `json:"endpoint"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Acknowledge     bool   `json:"acknowledge"`
}

func validateDatabaseResource(t domain.Target, comp domain.Component, r operator.Resource) error {
	if comp.Management != "observe" || (t.Operator == "coolify" && r.ArtifactKind != "database") || (t.Operator == "docker" && r.ArtifactKind != "image") {
		return errors.New("select an existing database resource supported by this target")
	}
	if r.ID != comp.ResourceID {
		return errors.New("database identity changed")
	}
	if len(r.Description) >= 16 && r.Description[:16] == "OpenAppPlatform:" {
		return errors.New("OAP-owned workloads cannot be linked as external databases")
	}
	return nil
}

// LinkDatabase appends one observe-only reference. It performs GETs only at the provider.
func (c *Controller) LinkDatabase(ctx context.Context, id string, v DatabaseLinkRequest) (domain.Application, error) {
	if c.RequireIdentity {
		p, ok := domain.Identity(ctx)
		if !ok || p.Role != "owner" || p.Kind != "session" {
			return domain.Application{}, errors.New("human owner access is required")
		}
	}
	if v.ExpectedVersion < 1 || !v.Acknowledge {
		return domain.Application{}, errors.New("review and acknowledge the database link with the current definition version")
	}
	tx, err := c.Store.Pool.Begin(ctx)
	if err != nil {
		return domain.Application{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", id); err != nil {
		return domain.Application{}, err
	}
	app, err := c.Store.ApplicationTx(ctx, tx, id)
	if err != nil {
		return app, err
	}
	if app.Version != v.ExpectedVersion {
		return app, domain.ErrConflict
	}
	var active bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oap_deployments WHERE application_id=$1 AND state IN ('queued','running','attention')) OR EXISTS(SELECT 1 FROM oap_route_publications WHERE application_id=$1 AND state='publishing')", id).Scan(&active); err != nil {
		return app, err
	}
	if active {
		return app, domain.ErrConflict
	}
	t, err := c.Store.TargetTx(ctx, tx, app.Manifest.TargetID)
	if err != nil {
		return app, err
	}
	adapter, err := c.Factory(t)
	if err != nil {
		return app, err
	}
	if !adapter.Capabilities().DatabaseLinks {
		return app, errors.New("target does not support existing database links")
	}
	r, err := adapter.Inspect(ctx, v.ResourceID)
	if err != nil {
		return app, errors.New("selected database cannot be inspected in this target")
	}
	comp := domain.Component{Name: v.Name, Kind: "database", Image: r.Image, ResourceID: v.ResourceID, Management: "observe", Instances: 1, Strategy: "standard", DatabaseEndpoint: v.Endpoint}
	if err = validateDatabaseResource(t, comp, r); err != nil {
		return app, err
	}
	app.Manifest.Components = append(app.Manifest.Components, comp)
	if err = app.Manifest.Validate(); err != nil {
		return app, err
	}
	if _, err = tx.Exec(ctx, db.InsertObservedBinding, t.ID, r.ID, id, comp.Name); err != nil {
		return app, domain.ErrConflict
	}
	updated, err := c.Store.UpdateApplicationTx(ctx, tx, id, app.Manifest, v.ExpectedVersion)
	if err != nil {
		return app, err
	}
	return updated, tx.Commit(ctx)
}
