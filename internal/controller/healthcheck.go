package controller

import (
	"context"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

func verifyHealthCheck(ctx context.Context, adapter operator.Adapter, resource string, component domain.Component, prepared bool) error {
	if component.HealthCheck == nil {
		return nil
	}
	checker, ok := adapter.(operator.HealthCheckVerifier)
	if !ok {
		return errors.New("native health configuration verification is unavailable")
	}
	return checker.CheckHealthCheck(ctx, resource, component, prepared)
}
