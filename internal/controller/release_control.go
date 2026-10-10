package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

type ReleaseControlError struct{ Message string }

func (e *ReleaseControlError) Error() string { return e.Message }
func controlBlocked(message string) error    { return &ReleaseControlError{Message: message} }

type StopReleaseRequest struct {
	Mode                           string    `json:"mode"`
	Reason                         string    `json:"reason"`
	ExpectedUpdatedAt              time.Time `json:"expectedUpdatedAt"`
	AcknowledgePreparedChanges     bool      `json:"acknowledgePreparedChanges"`
	AcknowledgeProviderMayContinue bool      `json:"acknowledgeProviderMayContinue"`
}
type ReconcileReleaseRequest struct {
	ExpectedUpdatedAt         time.Time                       `json:"expectedUpdatedAt"`
	AcknowledgeCurrentRuntime bool                            `json:"acknowledgeCurrentRuntime"`
	Operations                []domain.ProviderReconciliation `json:"operations,omitempty"`
}

func validControlReason(reason string) bool {
	if len(strings.TrimSpace(reason)) == 0 || len(reason) > 512 {
		return false
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func preDispatch(step domain.Step) bool {
	if step.RemoteDeploymentID != "" {
		return false
	}
	phase := step.Phase
	if phase == "attention" {
		phase = step.RecoveryPhase
	}
	return phase == "pending" || phase == "prepared"
}

// A stop uses the same application/release locks as recovery; it cannot race
// provider preparation/dispatch. Prepared changes remain, and no API is canceled.
func (c *Controller) StopRelease(ctx context.Context, id string, request StopReleaseRequest) (domain.Deployment, error) {
	if request.Mode == "abandon" && !c.canCloseUncertain(ctx) {
		return domain.Deployment{}, access.ErrDenied
	}
	if (request.Mode != "cancel" && request.Mode != "abandon") || request.ExpectedUpdatedAt.IsZero() || !validControlReason(request.Reason) {
		return domain.Deployment{}, controlBlocked("mode, a bounded reason and expectedUpdatedAt are required")
	}
	if !request.AcknowledgePreparedChanges {
		return domain.Deployment{}, controlBlocked("acknowledge that prepared resources/configuration may remain")
	}
	if request.Mode == "abandon" && !request.AcknowledgeProviderMayContinue {
		return domain.Deployment{}, controlBlocked("acknowledge that the provider may continue and the application will remain fenced")
	}
	original, err := c.Store.Deployment(ctx, id)
	if err != nil {
		return original, err
	}
	tx, err := c.Store.Pool.Begin(ctx)
	if err != nil {
		return original, err
	}
	defer tx.Rollback(ctx)
	if err = lockReleaseControl(ctx, tx, original.ApplicationID, id); err != nil {
		return original, err
	}
	release, err := c.Store.DeploymentTx(ctx, tx, id)
	if err != nil {
		return release, err
	}
	raw, _ := json.Marshal(request)
	sum := sha256.Sum256(raw)
	requestHash := hex.EncodeToString(sum[:])
	if release.Control != nil {
		if release.Control.RequestHash == requestHash {
			return release, nil
		}
		return release, domain.ErrConflict
	}
	if domain.Terminal(release.State) && release.State != "attention" {
		return release, domain.ErrConflict
	}
	if !release.UpdatedAt.Equal(request.ExpectedUpdatedAt) {
		return release, domain.ErrConflict
	}
	if len(release.Steps) == 0 {
		return release, controlBlocked("this release has no recorded work; allow normal finalization")
	}
	if request.Mode == "cancel" {
		for _, step := range release.Steps {
			if !preDispatch(step) {
				return release, controlBlocked("dispatch may have begun; cancel is unavailable, inspect/recover or abandon tracking")
			}
		}
	} else if release.Operation == "scale-down" {
		return release, controlBlocked("retirement requires its existing recovery/finalization path before closing")
	}
	if err = c.recordCredential(ctx, tx, id, release.ApplicationID); err != nil {
		return release, err
	}
	target, err := c.Store.TargetTx(ctx, tx, release.Manifest.TargetID)
	if err != nil {
		return release, err
	}
	control := &domain.ReleaseControl{Mode: request.Mode, Reason: request.Reason, At: time.Now().UTC(), RequestHash: requestHash, TargetHash: rollbackTargetHash(target)}
	if principal, ok := domain.Identity(ctx); ok {
		control.UserID = principal.UserID
		control.CredentialID = principal.CredentialID
	}
	release.Steps[0].Control = control
	release.Control = control
	if request.Mode == "cancel" {
		release.State = "cancelled"
	} else {
		release.State = "abandoned"
	}
	if err = c.Store.SaveDeploymentTx(ctx, tx, &release); err != nil {
		return release, err
	}
	return release, tx.Commit(ctx)
}
func lockReleaseControl(ctx context.Context, tx pgx.Tx, app, id string) error {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", app); err != nil {
		return err
	}
	var locked bool
	if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtext($1))", id).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return domain.ErrConflict
	}
	return nil
}

// Reconciliation only observes exact operations and releases the metadata fence.
// It does not resume pending steps, redeploy, stop containers or restore data.
func (c *Controller) ReconcileAbandoned(ctx context.Context, id string, request ReconcileReleaseRequest) (domain.Deployment, error) {
	if !c.canCloseUncertain(ctx) {
		return domain.Deployment{}, access.ErrDenied
	}
	if request.ExpectedUpdatedAt.IsZero() || !request.AcknowledgeCurrentRuntime {
		return domain.Deployment{}, controlBlocked("inspect current runtime and acknowledge reconciliation")
	}
	original, err := c.Store.Deployment(ctx, id)
	if err != nil {
		return original, err
	}
	tx, err := c.Store.Pool.Begin(ctx)
	if err != nil {
		return original, err
	}
	defer tx.Rollback(ctx)
	if err = lockReleaseControl(ctx, tx, original.ApplicationID, id); err != nil {
		return original, err
	}
	release, err := c.Store.DeploymentTx(ctx, tx, id)
	if err != nil {
		return release, err
	}
	if release.Control == nil || release.Control.Mode != "abandon" {
		return release, domain.ErrConflict
	}
	if release.Control.ResolvedAt != nil {
		return release, nil
	}
	if !release.UpdatedAt.Equal(request.ExpectedUpdatedAt) {
		return release, domain.ErrConflict
	}
	target, err := c.Store.TargetTx(ctx, tx, release.Manifest.TargetID)
	if err != nil {
		return release, err
	}
	if rollbackTargetHash(target) != release.Control.TargetHash {
		return release, controlBlocked("target authority changed; do not reconcile against a different operator")
	}
	adapter, err := c.Factory(target)
	if err != nil {
		return release, err
	}
	supplied := map[string]domain.ProviderReconciliation{}
	for _, operation := range request.Operations {
		key := operation.Component + ":" + strconv.Itoa(operation.Ordinal)
		if _, exists := supplied[key]; exists || operation.Ordinal < 1 || operation.RemoteDeploymentID == "" || len(operation.RemoteDeploymentID) > 256 {
			return release, controlBlocked("provide unique exact provider IDs for unresolved instances")
		}
		supplied[key] = operation
	}
	resolved := []domain.ProviderReconciliation{}
	for _, step := range release.Steps {
		key := step.Component + ":" + strconv.Itoa(step.Ordinal)
		operation, hasSupplied := supplied[key]
		delete(supplied, key)
		if preDispatch(step) {
			if hasSupplied {
				return release, controlBlocked("no provider ID is allowed for work recorded before dispatch")
			}
			continue
		}
		remote := step.RemoteDeploymentID
		if remote != "" && hasSupplied && remote != operation.RemoteDeploymentID {
			return release, controlBlocked("recorded provider IDs cannot be replaced")
		}
		if remote == "" {
			remote = operation.RemoteDeploymentID
		}
		if remote == "" || step.ResourceID == "" {
			return release, controlBlocked("supply an exact provider operation ID after inspecting the operator")
		}
		resource, err := adapter.Inspect(ctx, step.ResourceID)
		if err != nil {
			return release, controlBlocked("recorded instance cannot be inspected")
		}
		var component domain.Component
		for _, comp := range release.Manifest.Components {
			if comp.Name == step.Component {
				component = comp
				break
			}
		}
		if component.Name == "" || (component.ResourceID == "" && resource.Description != "OpenAppPlatform:"+release.ApplicationID+":"+component.Name) || (component.ResourceID != "" && component.ResourceID != step.ResourceID) {
			return release, controlBlocked("instance ownership changed; explicit reconciliation cannot proceed")
		}
		status, err := adapter.Observe(ctx, remote, step.ResourceID)
		if err != nil {
			return release, controlBlocked("exact provider operation cannot be observed")
		}
		if status.State != "succeeded" && status.State != "failed" {
			return release, controlBlocked("provider operation is not terminal; the application fence stays active")
		}
		resolved = append(resolved, domain.ProviderReconciliation{Component: step.Component, Ordinal: step.Ordinal, RemoteDeploymentID: remote, State: status.State})
	}
	if len(supplied) != 0 {
		return release, controlBlocked("provider ID refers to an instance outside this release")
	}
	if err = c.recordCredential(ctx, tx, id, release.ApplicationID); err != nil {
		return release, err
	}
	now := time.Now().UTC()
	release.Control.ResolvedAt = &now
	release.Control.Operations = resolved
	release.Steps[0].Control = release.Control
	if err = c.Store.SaveDeploymentTx(ctx, tx, &release); err != nil {
		return release, err
	}
	return release, tx.Commit(ctx)
}

// Retry exhaustion also holds the release lock, so it cannot resurrect closed
// work using an earlier snapshot after cancellation/abandonment commits.
func (c *Controller) markRetriesExhausted(ctx context.Context, id string) error {
	tx, err := c.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", id); err != nil {
		return err
	}
	release, err := c.Store.DeploymentTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if domain.Terminal(release.State) {
		return nil
	}
	release.State = "attention"
	for i := range release.Steps {
		step := &release.Steps[i]
		if step.Phase != "succeeded" && step.Phase != "failed" {
			step.RecoveryPhase = step.Phase
			step.Phase = "attention"
			step.Error = "Adapter operation retries exhausted; inspect provider before retrying"
			break
		}
	}
	if err = c.Store.SaveDeploymentTx(ctx, tx, &release); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (c *Controller) canCloseUncertain(ctx context.Context) bool {
	if !c.RequireIdentity {
		return true
	}
	p, ok := domain.Identity(ctx)
	return ok && p.Role == "owner" && p.Kind == "session"
}
