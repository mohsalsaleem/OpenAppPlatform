package controller

import (
	"context"
	"errors"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/riverqueue/river"
)

// Recovery reattaches observation to one exact provider operation. It never dispatches.
type Recovery struct {
	RetryFinalization  bool      `json:"retryFinalization,omitempty"`
	RetryPreparation   bool      `json:"retryPreparation,omitempty"`
	Component          string    `json:"component"`
	Ordinal            int       `json:"ordinal"`
	RemoteDeploymentID string    `json:"remoteDeploymentId,omitempty"`
	ExpectedUpdatedAt  time.Time `json:"expectedUpdatedAt"`
}

func (c *Controller) Recover(ctx context.Context, id string, request Recovery) (domain.Deployment, error) {
	if ((!request.RetryFinalization) && (request.Component == "" || request.Ordinal < 1)) || request.ExpectedUpdatedAt.IsZero() {
		return domain.Deployment{}, errors.New("component, ordinal and expectedUpdatedAt are required")
	}
	initial, err := c.Store.Deployment(ctx, id)
	if err != nil {
		return initial, err
	}
	adapter, err := c.Adapter(ctx, initial.Manifest.TargetID)
	if err != nil {
		return initial, err
	}
	tx, err := c.Store.Pool.Begin(ctx)
	if err != nil {
		return initial, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", initial.ApplicationID); err != nil {
		return initial, err
	}
	var locked bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtext($1))", id).Scan(&locked); err != nil {
		return initial, err
	}
	if !locked {
		return initial, domain.ErrConflict
	}
	d, err := c.Store.DeploymentTx(ctx, tx, id)
	if err != nil {
		return d, err
	}
	if err = c.recordCredential(ctx, tx, id, d.ApplicationID); err != nil {
		return d, err
	}
	if d.State != "attention" || !d.UpdatedAt.Equal(request.ExpectedUpdatedAt) {
		return d, domain.ErrConflict
	}
	if request.RetryFinalization {
		if d.Operation != "scale-down" || request.Component != "" || request.Ordinal != 0 || request.RetryPreparation || request.RemoteDeploymentID != "" {
			return d, errors.New("finalization retry is only valid for a completed retirement")
		}
		for _, step := range d.Steps {
			if step.Phase != "succeeded" {
				return d, errors.New("all retirements must be confirmed before finalization")
			}
		}
	} else {
		index := -1
		for i, s := range d.Steps {
			if s.Component == request.Component && s.Ordinal == request.Ordinal {
				index = i
				break
			}
		}
		if index < 0 {
			return d, domain.ErrNotFound
		}
		step := &d.Steps[index]
		if step.Phase != "attention" {
			return d, domain.ErrConflict
		}
		if step.RollbackFrom != "" {
			var component domain.Component
			for _, candidate := range d.Manifest.Components {
				if candidate.Name == step.Component {
					component = candidate
					break
				}
			}
			if err = c.verifyRollbackInstance(ctx, adapter, d, *step, component); err != nil {
				return d, err
			}
		}
		if request.RetryPreparation {
			if (step.RecoveryPhase != "pending" && step.RecoveryPhase != "prepared") || step.RemoteDeploymentID != "" || request.RemoteDeploymentID != "" {
				return d, errors.New("only a recorded pre-dispatch phase can be retried")
			}
			step.Phase = step.RecoveryPhase
			step.Error = ""
			step.RecoveryPhase = ""
		} else if step.Action == "retire" {
			if err = c.recoverRetirement(ctx, &d, step, adapter, request); err != nil {
				return d, err
			}
			if err = c.Store.RetireBindingTx(ctx, tx, d.Manifest.TargetID, step.ResourceID, d.ApplicationID, step.Component, step.Ordinal); err != nil {
				return d, err
			}
		} else {
			remote := step.RemoteDeploymentID
			if remote == "" {
				remote = request.RemoteDeploymentID
			} else if request.RemoteDeploymentID != "" && remote != request.RemoteDeploymentID {
				return d, errors.New("the recorded provider deployment cannot be replaced")
			}
			if remote == "" || len(remote) > 256 {
				return d, errors.New("supply the exact provider deployment ID after inspecting the operator; recovery never issues another deployment")
			}
			status, err := adapter.Observe(ctx, remote, step.ResourceID)
			if err != nil {
				return d, err
			}
			switch status.State {
			case "failed":
				step.Phase = "failed"
				step.Error = "provider deployment failed"
			case "succeeded":
				var component domain.Component
				for _, candidate := range d.Manifest.Components {
					if candidate.Name == step.Component {
						component = candidate
						break
					}
				}
				if !releaseReady(component, status.ResourceStatus) {
					return d, errors.New("provider completed but the instance does not satisfy the frozen readiness policy")
				}
				step.Phase = "succeeded"
				step.Error = ""
			case "running":
				step.Phase = "observing"
				step.Error = ""
			default:
				return d, errors.New("provider returned an unknown deployment state")
			}
			now := time.Now().UTC()
			step.ObservationStartedAt = &now
			step.RemoteDeploymentID = remote
			step.Observed = status.ResourceStatus
			step.RecoveryPhase = ""
		}
	}
	d.State = "running"
	for _, s := range d.Steps {
		if s.Phase == "attention" {
			d.State = "attention"
		}
	}
	if err = c.Store.SaveDeploymentTx(ctx, tx, &d); err != nil {
		return d, err
	}
	if d.State == "running" {
		if _, err = c.Jobs.InsertTx(ctx, tx, DeployArgs{d.ID}, &river.InsertOpts{MaxAttempts: 10}); err != nil {
			return d, err
		}
	}
	return d, tx.Commit(ctx)
}
