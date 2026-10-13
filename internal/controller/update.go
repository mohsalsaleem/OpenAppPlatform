package controller

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"maps"
	"slices"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store/db"
)

// UpdateApplication changes future release configuration. Existing releases keep
// their snapshots. Component removal and scale-down require explicit retirement.
func (c *Controller) UpdateApplication(ctx context.Context, id string, m domain.Manifest, version int64) (domain.Application, error) {
	if e := m.Validate(); e != nil {
		return domain.Application{}, e
	}
	if version < 1 {
		return domain.Application{}, errors.New("expectedVersion must be positive")
	}
	tx, e := c.Store.Pool.Begin(ctx)
	if e != nil {
		return domain.Application{}, e
	}
	defer tx.Rollback(ctx)

	if store.ActiveActionPlan(ctx) != "" {
		if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", id); e != nil {
			return domain.Application{}, e
		}
		replay, err := c.Store.BeginActionPlan(ctx, tx)
		if err != nil {
			return domain.Application{}, err
		}
		if replay != nil {
			return c.Store.ApplicationTx(ctx, tx, replay.ApplicationID)
		}
	}
	updated, e := c.updateApplicationTx(ctx, tx, id, m, version)
	if e != nil {
		return domain.Application{}, e
	}
	if e = c.Store.FinishActionPlan(ctx, tx, store.ActionResult{ApplicationID: updated.ID, DefinitionVersion: updated.Version, State: "saved"}); e != nil {
		return updated, e
	}
	return updated, tx.Commit(ctx)
}

func (c *Controller) updateApplicationTx(ctx context.Context, tx pgx.Tx, id string, m domain.Manifest, version int64) (domain.Application, error) {
	if err := c.authorizeSecurity(ctx, m); err != nil {
		return domain.Application{}, err
	}
	var e error
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", id); e != nil {
		return domain.Application{}, e
	}
	unresolved, e := db.New(tx).HasUnresolvedRoutePublication(ctx, db.HasUnresolvedRoutePublicationParams{ApplicationID: id})
	if e != nil {
		return domain.Application{}, e
	}
	if unresolved {
		return domain.Application{}, domain.ErrConflict
	}
	current, e := c.Store.ApplicationTx(ctx, tx, id)
	if e != nil {
		return domain.Application{}, e
	}
	var retiring bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oap_deployments WHERE application_id=$1 AND operation='scale-down' AND state IN ('queued','running','attention'))", id).Scan(&retiring); e != nil {
		return domain.Application{}, e
	}
	if retiring {
		return domain.Application{}, domain.ErrConflict
	}
	if current.Version != version {
		return domain.Application{}, domain.ErrConflict
	}
	if current.Manifest.Name != m.Name || current.Manifest.Environment != m.Environment || current.Manifest.TargetID != m.TargetID {
		return domain.Application{}, errors.New("application identity and target cannot change through configuration editing")
	}
	if len(current.Manifest.Components) != len(m.Components) {
		return domain.Application{}, errors.New("component addition and removal require an explicit topology change workflow")
	}
	target, e := c.Store.TargetTx(ctx, tx, m.TargetID)
	if e != nil {
		return domain.Application{}, e
	}
	adapter, e := c.Factory(target)
	if e != nil {
		return domain.Application{}, e
	}
	if e = operator.ValidateRuntime(m, adapter.Capabilities()); e != nil {
		return domain.Application{}, e
	}
	existing := map[string]domain.Component{}
	for _, comp := range current.Manifest.Components {
		existing[comp.Name] = comp
	}
	for _, comp := range m.Components {
		old, ok := existing[comp.Name]
		if !ok || old.Kind != comp.Kind || old.DatabaseEndpoint != comp.DatabaseEndpoint || old.ResourceID != comp.ResourceID || old.Management != comp.Management {
			return domain.Application{}, errors.New("component identity and resource adoption cannot change through configuration editing")
		}
		if old.Limits != nil && comp.Limits == nil {
			return domain.Application{}, errors.New("saved limits require explicit replacement values")
		}
		if len(old.Volumes) > 0 && !slices.Equal(old.Volumes, comp.Volumes) {
			return domain.Application{}, errors.New("saved volume mounts cannot be changed or removed; migrate data through the operator")
		}
		if old.HealthCheck != nil && comp.HealthCheck == nil {
			return domain.Application{}, errors.New("select image health check explicitly to remove a managed HTTP override")
		}
		if comp.Instances < old.Instances {
			return domain.Application{}, errors.New("scale-down requires explicit instance retirement")
		}
		if old.ResourceID != "" && (comp.Image != old.Image || comp.Port != old.Port || comp.HostPort != old.HostPort || !maps.Equal(comp.Env, old.Env) || !maps.Equal(comp.Services, old.Services) || !maps.Equal(comp.ServiceEndpoints, old.ServiceEndpoints)) {
			return domain.Application{}, errors.New("adopted resource runtime configuration must be changed through the operator")
		}
	}
	updated, e := c.Store.UpdateApplicationTx(ctx, tx, id, m, version)
	if e != nil {
		return domain.Application{}, e
	}
	return updated, nil
}
