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
	if len(component.Volumes) > 0 {
		return errors.New("image rollback for persistent components requires operator review of data compatibility")
	}
	if _, err := c.Inspect(ctx, ref); err != nil {
		return errors.New("rollback resource cannot be verified in its scope")
	}
	var current struct {
		Memory   string `json:"limits_memory"`
		CPUs     string `json:"limits_cpus"`
		Ports    string `json:"ports_exposes"`
		Mappings string `json:"ports_mappings"`
	}
	if err := c.request(ctx, "GET", "/applications/"+url.PathEscape(ref), nil, &current); err != nil {
		return errors.New("cannot verify rollback ports")
	}
	if l := component.Limits; l != nil {
		cpu, err := strconv.ParseFloat(current.CPUs, 64)
		if err != nil || cpu != float64(l.CPUMillis)/1000 || current.Memory != strconv.Itoa(l.MemoryMiB)+"m" {
			return errors.New("rollback resource limits differ")
		}
	}
	mappings := ""
	if component.HostPort != 0 {
		mappings = strconv.Itoa(component.HostPort) + ":" + strconv.Itoa(component.Port)
	}
	if strings.TrimSpace(current.Ports) != nativePorts(component) || strings.TrimSpace(current.Mappings) != mappings {
		return errors.New("rollback port configuration differs")
	}
	if component.Kind == "worker" {
		if err := c.checkWorkerConfiguration(ctx, ref); err != nil {
			return err
		}
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
