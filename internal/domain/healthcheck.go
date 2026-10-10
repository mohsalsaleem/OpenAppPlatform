package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// HealthCheck manages the native container check, independently of release deadlines.
// Nil preserves existing operator/image configuration. Image explicitly removes an OAP HTTP override.
type HealthCheck struct {
	Mode               string `json:"mode"`
	Path               string `json:"path,omitempty"`
	IntervalSeconds    int    `json:"intervalSeconds,omitempty"`
	TimeoutSeconds     int    `json:"timeoutSeconds,omitempty"`
	Retries            int    `json:"retries,omitempty"`
	StartPeriodSeconds int    `json:"startPeriodSeconds,omitempty"`
}

var healthPath = regexp.MustCompile(`^/[A-Za-z0-9/_~.\-]*$`)

func (h *HealthCheck) Validate() error {
	if h.Mode == "image" {
		if h.Path != "" || h.IntervalSeconds != 0 || h.TimeoutSeconds != 0 || h.Retries != 0 || h.StartPeriodSeconds != 0 {
			return errors.New("image health checks cannot configure HTTP fields")
		}
		return nil
	}
	if h.Mode != "http" {
		return errors.New("healthCheck mode must be http or image")
	}
	if len(h.Path) > 256 || !healthPath.MatchString(h.Path) || strings.HasPrefix(h.Path, "//") {
		return errors.New("HTTP health path must be a local absolute path without queries, fragments or shell characters")
	}
	if h.IntervalSeconds == 0 {
		h.IntervalSeconds = 10
	}
	if h.TimeoutSeconds == 0 {
		h.TimeoutSeconds = 3
	}
	if h.Retries == 0 {
		h.Retries = 3
	}
	if h.IntervalSeconds < 1 || h.IntervalSeconds > 300 || h.TimeoutSeconds < 1 || h.TimeoutSeconds > 30 || h.Retries < 1 || h.Retries > 20 || h.StartPeriodSeconds < 0 || h.StartPeriodSeconds > 600 {
		return errors.New("health check timings must be bounded: interval 1–300s, timeout 1–30s, retries 1–20, start period 0–600s")
	}
	return nil
}
func HealthCheckSummary(h *HealthCheck) string {
	if h == nil {
		return "Existing operator/image check"
	}
	if h.Mode == "image" {
		return "Image health check"
	}
	return fmt.Sprintf("HTTP GET %s; interval %ds, timeout %ds, retries %d, start period %ds", h.Path, h.IntervalSeconds, h.TimeoutSeconds, h.Retries, h.StartPeriodSeconds)
}
