package docker

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"net/url"
	"reflect"
	"strconv"
	"time"
)

type nativeHealthCheck struct {
	Test          []string `json:"Test"`
	Interval      int64    `json:"Interval"`
	Timeout       int64    `json:"Timeout"`
	Retries       int      `json:"Retries"`
	StartPeriod   int64    `json:"StartPeriod"`
	StartInterval int64    `json:"StartInterval,omitempty"`
}

func httpHealthCheck(component domain.Component) *nativeHealthCheck {
	h := component.HealthCheck
	if h == nil || h.Mode != "http" {
		return nil
	}
	return &nativeHealthCheck{Test: []string{"CMD", "wget", "-q", "-T", strconv.Itoa(h.TimeoutSeconds), "-O", "/dev/null", "http://127.0.0.1:" + strconv.Itoa(component.Port) + h.Path}, Interval: int64(time.Duration(h.IntervalSeconds) * time.Second), Timeout: int64(time.Duration(h.TimeoutSeconds) * time.Second), Retries: h.Retries, StartPeriod: int64(time.Duration(h.StartPeriodSeconds) * time.Second)}
}
func (c *Client) CheckHealthCheck(ctx context.Context, ref string, component domain.Component, prepared bool) error {
	if component.HealthCheck == nil {
		return nil
	}
	name := ref
	if prepared {
		if candidate, err := c.inspectContainer(ctx, ref+"-next"); err == nil {
			if !c.owned(candidate) || candidate.Config.Labels[prefix+"reference"] != ref {
				return errors.New("native health candidate is outside target")
			}
			name = ref + "-next"
		} else if !isStatus(err, 404) {
			return err
		}
	}
	container, err := c.inspectContainer(ctx, name)
	if err != nil || !c.owned(container) {
		return errors.New("native health instance cannot be verified")
	}
	expected := httpHealthCheck(component)
	if component.HealthCheck.Mode == "image" {
		var img struct {
			Config struct {
				Healthcheck *nativeHealthCheck `json:"Healthcheck"`
			}
		}
		if err = c.request(ctx, "GET", "/images/"+url.PathEscape(container.Config.Image)+"/json", nil, &img); err != nil {
			return errors.New("image health configuration cannot be verified")
		}
		expected = img.Config.Healthcheck
	}
	if !reflect.DeepEqual(expected, container.Config.Healthcheck) {
		return errors.New("native health configuration differs from frozen release")
	}
	return nil
}
