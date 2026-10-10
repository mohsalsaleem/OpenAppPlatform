package controller

import (
	"context"
	"errors"
	"fmt"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

var errDependencyFailed = errors.New("selected dependency failed")

// Dependencies gate preparation and dispatch; they never expand the release.
func (c *Controller) dependenciesReady(ctx context.Context, adapter operator.Adapter, release domain.Deployment, component domain.Component) (bool, error) {
	for _, name := range component.DependsOn {
		var dependency domain.Component
		for _, candidate := range release.Manifest.Components {
			if candidate.Name == name {
				dependency = candidate
				break
			}
		}
		if dependency.Name == "" {
			return false, errors.New("frozen dependency is absent")
		}
		selected := 0
		for _, step := range release.Steps {
			if step.Component != name {
				continue
			}
			selected++
			if step.Phase == "failed" {
				return false, fmt.Errorf("%w: %s; dependent operation was not dispatched", errDependencyFailed, name)
			}
			if step.Phase != "succeeded" {
				return false, nil
			}
		}
		if selected != 0 {
			if selected != dependency.Instances {
				return false, fmt.Errorf("dependency %s has an incomplete replica selection", name)
			}
			continue
		}
		// An unselected dependency is checked against the frozen replica count and
		// its current active bindings, rather than the latest editable definition.
		bindings, err := c.Store.Bindings(ctx, release.ApplicationID)
		if err != nil {
			return false, err
		}
		for ordinal := 1; ordinal <= dependency.Instances; ordinal++ {
			resourceID := ""
			for _, binding := range bindings {
				if binding.Component == name && binding.Ordinal == ordinal && !binding.Retired {
					resourceID = binding.ResourceID
					break
				}
			}
			if resourceID == "" {
				return false, fmt.Errorf("dependency %s / %d has no active binding", name, ordinal)
			}
			resource, err := adapter.Inspect(ctx, resourceID)
			if err != nil {
				return false, fmt.Errorf("dependency %s / %d cannot be inspected", name, ordinal)
			}
			if dependency.ResourceID != "" {
				if dependency.ResourceID != resourceID {
					return false, fmt.Errorf("dependency %s resource binding changed", name)
				}
			} else if resource.Description != "OpenAppPlatform:"+release.ApplicationID+":"+name {
				return false, fmt.Errorf("dependency %s / %d ownership changed", name, ordinal)
			}
			if err = verifyHealthCheck(ctx, adapter, resourceID, dependency, false); err != nil {
				return false, fmt.Errorf("dependency %s / %d native health configuration differs", name, ordinal)
			}
			if !releaseReady(dependency, resource.Status) {
				return false, fmt.Errorf("dependency %s / %d is not ready under the frozen policy", name, ordinal)
			}
		}
	}
	return true, nil
}
