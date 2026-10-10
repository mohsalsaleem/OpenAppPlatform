package controller

import (
	"context"
	"errors"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

type ScaleDownRequest struct {
	Component string `json:"component"`
	Instances int    `json:"instances"`
}

func (c *Controller) EnqueueScaleDown(ctx context.Context, appID, key string, request ScaleDownRequest, version int64) (domain.Deployment, error) {
	if request.Component == "" || request.Instances < 1 || request.Instances > 4 || version < 1 {
		return domain.Deployment{}, errors.New("component, instances between 1 and 4, and expectedVersion are required")
	}
	return c.enqueueOperation(ctx, appID, key, nil, version, nil, &request, nil, nil)
}
func retirementOwner(d domain.Deployment, step domain.Step) string {
	return "OpenAppPlatform:" + d.ApplicationID + ":" + step.Component
}
func retirementStatus(ctx context.Context, adapter operator.Adapter, d domain.Deployment, step domain.Step, remote string) (operator.DeploymentStatus, error) {
	retirer, ok := adapter.(operator.Retirer)
	if !ok {
		return operator.DeploymentStatus{}, errors.New("target does not implement retirement")
	}
	return retirer.ObserveRetirement(ctx, remote, step.ResourceID, retirementOwner(d, step))
}

// confirmRetirement saves the stopped outcome and the reserved binding together.
func (c *Controller) confirmRetirement(ctx context.Context, d *domain.Deployment, step *domain.Step) error {
	tx, e := c.Store.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = c.Store.RetireBindingTx(ctx, tx, d.Manifest.TargetID, step.ResourceID, d.ApplicationID, step.Component, step.Ordinal); e != nil {
		return e
	}
	if e = c.Store.SaveDeploymentTx(ctx, tx, d); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// finishRetirement commits desired counts only after every stop succeeded.
func (c *Controller) finishRetirement(ctx context.Context, d *domain.Deployment) error {
	tx, e := c.Store.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", d.ApplicationID); e != nil {
		return e
	}
	app, e := c.Store.ApplicationTx(ctx, tx, d.ApplicationID)
	if e != nil {
		return e
	}
	if app.Version != d.DefinitionVersion {
		return domain.ErrConflict
	}
	if _, e = c.Store.UpdateApplicationTx(ctx, tx, app.ID, d.Manifest, app.Version); e != nil {
		return e
	}
	d.State = "succeeded"
	if e = c.Store.SaveDeploymentTx(ctx, tx, d); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

func (c *Controller) recoverRetirement(ctx context.Context, d *domain.Deployment, step *domain.Step, adapter operator.Adapter, request Recovery) error {
	remote := step.RemoteDeploymentID
	if remote == "" {
		remote = request.RemoteDeploymentID
	} else if request.RemoteDeploymentID != "" && remote != request.RemoteDeploymentID {
		return errors.New("the recorded provider operation cannot be replaced")
	}
	if remote == "" || len(remote) > 256 {
		return errors.New("supply the exact retired container ID after inspecting Docker")
	}
	status, e := retirementStatus(ctx, adapter, *d, *step, remote)
	if e != nil {
		return e
	}
	if status.State != "succeeded" {
		return errors.New("instance is not confirmed stopped; retirement remains blocked")
	}
	now := time.Now().UTC()
	step.ObservationStartedAt = &now
	step.RemoteDeploymentID = remote
	step.Observed = status.ResourceStatus
	step.Phase = "succeeded"
	step.Error = ""
	step.RecoveryPhase = ""
	return nil
}
