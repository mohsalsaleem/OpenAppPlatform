package controller

import (
	"context"
	"errors"
	"strings"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

type DiscoveredResource struct {
	operator.Resource
	ApplicationID string `json:"applicationId,omitempty"`
	Component     string `json:"component,omitempty"`
}

// Discover adds local reservation evidence without mutating the provider.
func (c *Controller) Discover(ctx context.Context, target string) ([]DiscoveredResource, error) {
	adapter, err := c.Adapter(ctx, target)
	if err != nil {
		return nil, err
	}
	resources, err := adapter.Discover(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := c.Store.Pool.Query(ctx, "SELECT resource_id,application_id,component FROM oap_bindings WHERE target_id=$1", target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bound := map[string]DiscoveredResource{}
	for rows.Next() {
		var id string
		var r DiscoveredResource
		if err = rows.Scan(&id, &r.ApplicationID, &r.Component); err != nil {
			return nil, err
		}
		bound[id] = r
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := []DiscoveredResource{}
	for _, r := range resources {
		item := bound[r.ID]
		item.Resource = r
		out = append(out, item)
	}
	return out, nil
}

// EnableManagement records consent for existing-resource deploy/restart only.
// Provider source, environment, domains and ownership are deliberately unchanged.
func (c *Controller) EnableManagement(ctx context.Context, id, component string, version int64) (domain.Application, error) {
	if version < 1 {
		return domain.Application{}, errors.New("expectedVersion must be positive")
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
	if app.Version != version {
		return app, domain.ErrConflict
	}
	var active bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oap_deployments WHERE application_id=$1 AND state IN ('queued','running','attention'))", id).Scan(&active); err != nil {
		return app, err
	}
	if active {
		return app, domain.ErrConflict
	}
	target, err := c.Store.TargetTx(ctx, tx, app.Manifest.TargetID)
	if err != nil {
		return app, err
	}
	adapter, err := c.Factory(target)
	if err != nil {
		return app, err
	}
	found := false
	for i := range app.Manifest.Components {
		comp := &app.Manifest.Components[i]
		if comp.Name != component {
			continue
		}
		found = true
		if comp.Management != "observe" {
			return app, errors.New("component is not observe-only")
		}
		resource, err := adapter.Inspect(ctx, comp.ResourceID)
		if err != nil {
			return app, err
		}
		if target.Operator != "coolify" || resource.ArtifactKind != "image" || !adapter.Capabilities().Standard || !adapter.Capabilities().ManagementHandoff {
			return app, errors.New("management handoff currently supports Coolify image-backed services only")
		}
		if strings.HasPrefix(resource.Description, "OpenAppPlatform:") {
			return app, errors.New("resource already has provider ownership; inspect its original application")
		}
		if resource.Image != comp.Image || resource.Port != comp.Port {
			return app, errors.New("resource configuration changed; review it in the operator before handoff")
		}
		comp.Management = ""
	}
	if !found {
		return app, errors.New("unknown component")
	}
	if err = app.Manifest.Validate(); err != nil {
		return app, err
	}
	updated, err := c.Store.UpdateApplicationTx(ctx, tx, id, app.Manifest, version)
	if err != nil {
		return app, err
	}
	return updated, tx.Commit(ctx)
}
