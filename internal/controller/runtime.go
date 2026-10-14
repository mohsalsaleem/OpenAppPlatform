package controller

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"strings"
	"time"
)

type Instance struct {
	Kind       string             `json:"kind,omitempty"`
	Retired    bool               `json:"retired"`
	Component  string             `json:"component"`
	Ordinal    int                `json:"ordinal"`
	ResourceID string             `json:"resourceId,omitempty"`
	Status     string             `json:"status"`
	Resource   *operator.Resource `json:"resource,omitempty"`
	Error      string             `json:"error,omitempty"`
	CheckedAt  time.Time          `json:"checkedAt"`
}

func (c *Controller) Instances(ctx context.Context, id string) ([]Instance, error) {
	out, _, err := c.instancesBounded(ctx, id, 0)
	return out, err
}

// A positive limit is used by incident sampling; legacy instance inspection
// retains its existing complete projection.
func (c *Controller) instancesBounded(ctx context.Context, id string, limit int) ([]Instance, bool, error) {
	app, err := c.Store.Application(ctx, id)
	if err != nil {
		return nil, false, err
	}
	bindings, err := c.Store.Bindings(ctx, id)
	if err != nil {
		return nil, false, err
	}
	adapter, err := c.Adapter(ctx, app.Manifest.TargetID)
	if err != nil {
		return nil, false, err
	}
	out := []Instance{}
	for _, comp := range app.Manifest.Components {
		maximum := comp.Instances
		for _, b := range bindings {
			if b.Component == comp.Name && b.Ordinal > maximum {
				maximum = b.Ordinal
			}
		}
		for ordinal := 1; ordinal <= maximum; ordinal++ {
			if limit > 0 && len(out) >= limit {
				return out, true, nil
			}
			if limit > 0 && ctx.Err() != nil {
				return out, true, ctx.Err()
			}
			item := Instance{Kind: comp.Kind, Component: comp.Name, Ordinal: ordinal, Status: "not deployed", CheckedAt: time.Now().UTC()}
			for _, b := range bindings {
				if b.Component == comp.Name && b.Ordinal == ordinal {
					item.ResourceID = b.ResourceID
					item.Retired = b.Retired
					break
				}
			}
			if item.ResourceID != "" {
				resource, e := adapter.Inspect(ctx, item.ResourceID)
				item.CheckedAt = time.Now().UTC()
				if e != nil {
					item.Status = "unavailable"
					item.Error = "Cannot inspect this instance; check the target and resource."
				} else {
					if comp.Kind == "database" {
						resource.URL = ""
						resource.Port = 0
						resource.ArtifactKind = "database"
					}
					item.Status = resource.Status
					item.Resource = &resource
					if item.Retired {
						item.Status = "retired"
						if strings.HasPrefix(resource.Status, "running") || resource.Status == "restarting" {
							item.Status = "retired:running"
							item.Error = "Retired instance is running again; inspect the operator."
						}
					}
				}
			}
			out = append(out, item)
		}
	}
	return out, false, nil
}
