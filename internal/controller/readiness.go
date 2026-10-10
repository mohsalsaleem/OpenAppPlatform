package controller

import (
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"strings"
	"time"
)

func observationTimeout(component domain.Component) time.Duration {
	if component.Readiness != nil && component.Readiness.TimeoutSeconds != 0 {
		return time.Duration(component.Readiness.TimeoutSeconds) * time.Second
	}
	return 15 * time.Minute
}

func releaseReady(component domain.Component, status string) bool {
	if (component.HealthCheck != nil && component.HealthCheck.Mode == "http") || (component.Readiness != nil && component.Readiness.RequireHealthy) {
		return status == "running:healthy" || strings.HasPrefix(status, "running:healthy:")
	}
	return Healthy(status)
}
