package controller

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"time"
)

type Instance struct {
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
		for ordinal := 1; ordinal <= comp.Instances; ordinal++ {
			item := Instance{Component: comp.Name, Ordinal: ordinal, Status: "not deployed", CheckedAt: time.Now().UTC()}
			for _, b := range bindings {
				if b.Component == comp.Name && b.Ordinal == ordinal {
					item.ResourceID = b.ResourceID
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
				}
			}
			out = append(out, item)
		}
	}
	return out, nil
}
