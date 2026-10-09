package controller

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"strings"
	"time"
)

type Instance struct {
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
	app, err := c.Store.Application(ctx, id)
	if err != nil {
		return nil, err
	}
	bindings, err := c.Store.Bindings(ctx, id)
	if err != nil {
		return nil, err
	}
	adapter, err := c.Adapter(ctx, app.Manifest.TargetID)
	if err != nil {
		return nil, err
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
			item := Instance{Component: comp.Name, Ordinal: ordinal, Status: "not deployed", CheckedAt: time.Now().UTC()}
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
	return out, nil
}
