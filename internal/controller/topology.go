package controller

import (
	"context"
	"errors"
	"reflect"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
)

// Removal changes one owned component only. Unlink dependencies and routes in a
// separate configure plan first; remaining workload configuration stays frozen.
func desiredComponentRemoval(current, desired domain.Manifest) (domain.Component, error) {
	if err := desired.Validate(); err != nil {
		return domain.Component{}, err
	}
	if len(desired.Components) != len(current.Components)-1 {
		return domain.Component{}, errors.New("remove-component removes exactly one component")
	}
	names := map[string]bool{}
	for _, component := range desired.Components {
		names[component.Name] = true
	}
	clone := current
	clone.Components = nil
	var removed domain.Component
	for _, component := range current.Components {
		if names[component.Name] {
			clone.Components = append(clone.Components, component)
		} else {
			if removed.Name != "" || component.Management != "" || component.ResourceID != "" || (component.Kind != "web" && component.Kind != "worker") {
				return domain.Component{}, errors.New("remove-component requires one owned web or worker")
			}
			removed = component
		}
	}
	if removed.Name == "" || !reflect.DeepEqual(clone, desired) {
		return domain.Component{}, errors.New("remove-component preserves remaining configuration; unlink routes and dependencies separately")
	}
	return removed, nil
}

func (c *Controller) EnqueueRemoveComponent(ctx context.Context, app, key string, desired domain.Manifest, version int64) (domain.Deployment, error) {
	if version < 1 || (c.RequireIdentity && store.ActiveActionPlan(ctx) == "") {
		return domain.Deployment{}, errors.New("component removal requires an exact reviewed action plan")
	}
	return c.enqueueOperation(ctx, app, key, nil, version, nil, nil, nil, nil, nil, &desired)
}
