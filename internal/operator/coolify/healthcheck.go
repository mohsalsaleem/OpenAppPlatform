package coolify

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/url"
	"strconv"
)

type nativeHealthFields struct {
	Enabled     bool   `json:"health_check_enabled"`
	Type        string `json:"health_check_type"`
	Path        string `json:"health_check_path"`
	Port        string `json:"health_check_port"`
	Host        string `json:"health_check_host"`
	Method      string `json:"health_check_method"`
	Scheme      string `json:"health_check_scheme"`
	Interval    int    `json:"health_check_interval"`
	Timeout     int    `json:"health_check_timeout"`
	Retries     int    `json:"health_check_retries"`
	StartPeriod int    `json:"health_check_start_period"`
	CustomFound bool   `json:"custom_healthcheck_found"`
}

func applyHealthCheck(data map[string]any, component domain.Component) {
	h := component.HealthCheck
	if h == nil {
		return
	}
	data["health_check_enabled"] = h.Mode == "http"
	if h.Mode == "image" {
		return
	}
	for key, value := range map[string]any{"health_check_type": "http", "health_check_path": h.Path, "health_check_port": strconv.Itoa(component.Port), "health_check_host": "127.0.0.1", "health_check_method": "GET", "health_check_scheme": "http", "health_check_return_code": 200, "health_check_response_text": "", "health_check_interval": h.IntervalSeconds, "health_check_timeout": h.TimeoutSeconds, "health_check_retries": h.Retries, "health_check_start_period": h.StartPeriodSeconds} {
		data[key] = value
	}
}
func (c *Client) CheckHealthCheck(ctx context.Context, ref string, component domain.Component, _ bool) error {
	if component.HealthCheck == nil {
		return nil
	}
	r, err := c.Inspect(ctx, ref)
	if err != nil || r.ArtifactKind != "image" {
		return errors.New("native health instance cannot be verified in image scope")
	}
	var current nativeHealthFields
	if err = c.request(ctx, "GET", "/applications/"+url.PathEscape(ref), nil, &current); err != nil {
		return errors.New("native health configuration cannot be read")
	}
	h := component.HealthCheck
	if h.Mode == "image" {
		if current.Enabled {
			return errors.New("native HTTP override remains enabled")
		}
		return nil
	}
	if !current.Enabled || current.CustomFound || current.Type != "http" || current.Path != h.Path || current.Port != strconv.Itoa(component.Port) || current.Host != "127.0.0.1" || current.Method != "GET" || current.Scheme != "http" || current.Interval != h.IntervalSeconds || current.Timeout != h.TimeoutSeconds || current.Retries != h.Retries || current.StartPeriod != h.StartPeriodSeconds {
		return errors.New("native health configuration differs from frozen release")
	}
	return nil
}
