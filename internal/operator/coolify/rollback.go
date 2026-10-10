package coolify

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) CheckRollbackConfiguration(ctx context.Context, ref string, component domain.Component) error {
	if _, err := c.Inspect(ctx, ref); err != nil {
		return errors.New("rollback resource cannot be verified in its scope")
	}
	var current struct {
		Ports    string `json:"ports_exposes"`
		Mappings string `json:"ports_mappings"`
	}
	if err := c.request(ctx, "GET", "/applications/"+url.PathEscape(ref), nil, &current); err != nil {
		return errors.New("cannot verify rollback ports")
	}
	mappings := ""
	if component.HostPort != 0 {
		mappings = strconv.Itoa(component.HostPort) + ":" + strconv.Itoa(component.Port)
	}
	if strings.TrimSpace(current.Ports) != strconv.Itoa(component.Port) || strings.TrimSpace(current.Mappings) != mappings {
		return errors.New("rollback port configuration differs")
	}
	if len(component.Env) > 0 {
		var rows []variable
		if err := c.request(ctx, "GET", "/applications/"+url.PathEscape(ref)+"/envs", nil, &rows); err != nil {
			return errors.New("cannot verify rollback variables")
		}
		for key, value := range component.Env {
			count := 0
			for _, row := range rows {
				if row.Key == key {
					if !safeVariable(row) || row.Value != value {
						return errors.New("rollback runtime variable configuration differs")
					}
					count++
				}
			}
			if count != 1 {
				return errors.New("rollback runtime variable cannot be uniquely verified")
			}
		}
	}
	return c.CheckHealthCheck(ctx, ref, component, false)
}
