package controller

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store/db"
	"sort"
)

// EnqueueSelected deploys only the requested components. Its frozen definition
// still contains dependencies for readiness inspection and conflict guards.
func (c *Controller) EnqueueSelected(ctx context.Context, app, key string, images map[string]string, version int64, components []string) (domain.Deployment, error) {
	if len(components) == 0 {
		return c.EnqueueVersion(ctx, app, key, images, version)
	}
	selected := append([]string(nil), components...)
	sort.Strings(selected)
	for i, name := range selected {
		if name == "" || (i > 0 && selected[i-1] == name) {
			return domain.Deployment{}, errors.New("components must be nonempty and unique")
		}
	}
	for name := range images {
		if !selectedComponent(selected, name) {
			return domain.Deployment{}, errors.New("image override must belong to a selected component")
		}
	}
	return c.enqueueOperation(ctx, app, key, images, version, nil, nil, nil, nil, selected)
}
func selectedComponent(selection []string, name string) bool {
	for _, v := range selection {
		if v == name {
			return true
		}
	}
	return false
}

func releaseClaims(release domain.Deployment) (map[string]string, error) {
	components := map[string]domain.Component{}
	for _, component := range release.Manifest.Components {
		components[component.Name] = component
	}
	claims := map[string]string{}
	for _, step := range release.Steps {
		if _, ok := components[step.Component]; !ok {
			return nil, errors.New("unknown release component")
		}
		claims[step.Component] = "write"
	}
	if len(claims) == 0 {
		return nil, errors.New("release must select at least one known component")
	}
	seen := map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if seen[name] {
			return nil
		}
		seen[name] = true
		component, ok := components[name]
		if !ok {
			return errors.New("unknown release dependency")
		}
		dependencies := append([]string(nil), component.DependsOn...)
		for _, service := range component.Services {
			dependencies = append(dependencies, service)
		}
		for _, dependency := range dependencies {
			if claims[dependency] != "write" {
				claims[dependency] = "read"
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}
		return nil
	}
	for _, step := range release.Steps {
		if err := visit(step.Component); err != nil {
			return nil, err
		}
	}
	return claims, nil
}

// The caller holds the application admission lock. Read/read sharing is safe;
// a write conflicts with every active reader/writer, including attention work.
func checkReleaseClaims(ctx context.Context, tx pgx.Tx, app string, claims map[string]string) error {
	queries := db.New(tx)
	unresolved, err := queries.HasUnresolvedRoutePublication(ctx, db.HasUnresolvedRoutePublicationParams{ApplicationID: app})
	if err != nil {
		return err
	}
	if unresolved {
		return domain.ErrConflict
	}
	legacy, err := queries.HasLegacyActiveRelease(ctx, app)
	if err != nil {
		return err
	}
	if legacy {
		return domain.ErrConflict
	}
	for component, mode := range claims {
		conflict, err := queries.HasComponentConflict(ctx, db.HasComponentConflictParams{ApplicationID: app, Component: component, Column3: mode})
		if err != nil {
			return err
		}
		if conflict {
			return domain.ErrConflict
		}
	}

	return nil
}
func saveReleaseClaims(ctx context.Context, tx pgx.Tx, release string, claims map[string]string) error {
	for component, mode := range claims {
		if err := db.New(tx).InsertReleaseClaim(ctx, db.InsertReleaseClaimParams{ReleaseID: release, Component: component, Mode: mode}); err != nil {
			return err
		}
	}
	return nil
}
