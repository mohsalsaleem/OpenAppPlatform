package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"strconv"
)

type ConfigurationRestorePlan struct {
	ApplicationID     string                 `json:"applicationId"`
	ReleaseID         string                 `json:"releaseId"`
	DefinitionVersion int64                  `json:"definitionVersion"`
	PlanHash          string                 `json:"planHash"`
	Changes           []domain.ReleaseChange `json:"changes"`
}
type ConfigurationRestoreRequest struct {
	ReleaseID          string `json:"releaseId"`
	PlanHash           string `json:"planHash"`
	AcknowledgeEffects bool   `json:"acknowledgeEffects"`
}

func (c *Controller) PlanConfigurationRestore(ctx context.Context, appID, releaseID string, version int64) (ConfigurationRestorePlan, error) {
	tx, err := c.Store.Pool.Begin(ctx)
	if err != nil {
		return ConfigurationRestorePlan{}, err
	}
	defer tx.Rollback(ctx)
	plan, _, err := c.configurationRestorePlan(ctx, tx, appID, releaseID, version)
	return plan, err
}

// Restore changes only the desired definition. Deployment remains a separate,
// reviewed operation; data, schema, source cursors and runtime are untouched.
func (c *Controller) RestoreConfiguration(ctx context.Context, appID string, request ConfigurationRestoreRequest, version int64) (domain.Application, error) {
	if !request.AcknowledgeEffects || len(request.PlanHash) != 64 {
		return domain.Application{}, rollbackInvalid("review the configuration restore plan and acknowledge its effects")
	}
	if c.RequireIdentity {
		p, ok := domain.Identity(ctx)
		if !ok {
			return domain.Application{}, access.ErrDenied
		}
		current, err := (access.Service{Pool: c.Store.Pool}).ByID(ctx, p.CredentialID)
		if err != nil || !current.Operates(appID) {
			return domain.Application{}, access.ErrDenied
		}
	}
	tx, err := c.Store.Pool.Begin(ctx)
	if err != nil {
		return domain.Application{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", appID); err != nil {
		return domain.Application{}, err
	}
	plan, desired, err := c.configurationRestorePlan(ctx, tx, appID, request.ReleaseID, version)
	if err != nil {
		return domain.Application{}, err
	}
	if request.PlanHash != plan.PlanHash {
		return domain.Application{}, domain.ErrConflict
	}
	updated, err := c.updateApplicationTx(ctx, tx, appID, desired, version)
	if err != nil {
		return domain.Application{}, err
	}
	return updated, tx.Commit(ctx)
}

func (c *Controller) configurationRestorePlan(ctx context.Context, tx pgx.Tx, appID, releaseID string, version int64) (ConfigurationRestorePlan, domain.Manifest, error) {
	plan := ConfigurationRestorePlan{ApplicationID: appID, ReleaseID: releaseID, DefinitionVersion: version, Changes: []domain.ReleaseChange{}}
	app, err := c.Store.ApplicationTx(ctx, tx, appID)
	if err != nil {
		return plan, domain.Manifest{}, err
	}
	if version < 1 || app.Version != version {
		return plan, domain.Manifest{}, domain.ErrConflict
	}
	original, err := c.Store.DeploymentTx(ctx, tx, releaseID)
	if err != nil {
		return plan, domain.Manifest{}, err
	}
	if original.ApplicationID != appID {
		return plan, domain.Manifest{}, domain.ErrNotFound
	}
	if original.State != "succeeded" || original.Operation != "deploy" {
		return plan, domain.Manifest{}, rollbackInvalid("configuration recovery requires a successful deployment snapshot")
	}
	var active bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oap_deployments WHERE application_id=$1 AND state IN ('queued','running','attention'))", appID).Scan(&active); err != nil {
		return plan, domain.Manifest{}, err
	}
	if active {
		return plan, domain.Manifest{}, domain.ErrConflict
	}
	desired, err := restoreConfigurationDefinition(app.Manifest, original.Manifest, original.Steps)
	if err != nil {
		return plan, desired, err
	}
	target, err := c.Store.TargetTx(ctx, tx, app.Manifest.TargetID)
	if err != nil {
		return plan, desired, err
	}
	adapter, err := c.Factory(target)
	if err != nil {
		return plan, desired, err
	}
	if err = operator.ValidateRuntime(desired, adapter.Capabilities()); err != nil {
		return plan, desired, rollbackInvalid("snapshot configuration is unsupported by this target")
	}
	plan.Changes = domain.CompareReleases(domain.Deployment{Manifest: app.Manifest}, domain.Deployment{Manifest: desired}).Changes
	if len(plan.Changes) == 0 {
		return plan, desired, rollbackInvalid("snapshot configuration already matches the saved definition")
	}
	raw, _ := json.Marshal(struct {
		Plan     ConfigurationRestorePlan
		Current  domain.Application
		Snapshot domain.Deployment
		Desired  domain.Manifest
		Target   string
	}{plan, app, original, desired, rollbackTargetHash(target)})
	sum := sha256.Sum256(raw)
	plan.PlanHash = hex.EncodeToString(sum[:])
	return plan, desired, nil
}

func restoreConfigurationDefinition(current, source domain.Manifest, steps []domain.Step) (domain.Manifest, error) {
	raw, _ := json.Marshal(source)
	var desired domain.Manifest
	json.Unmarshal(raw, &desired)
	if current.Name != source.Name || current.Environment != source.Environment || current.TargetID != source.TargetID || len(current.Components) != len(source.Components) {
		return desired, rollbackInvalid("configuration recovery cannot change application identity, target or component topology")
	}
	verified := map[string]bool{}
	for _, step := range steps {
		key := step.Component + ":" + strconv.Itoa(step.Ordinal)
		if step.Phase != "succeeded" || step.Action != "" || step.Observed != "running:healthy" || step.ResourceID == "" || verified[key] {
			return desired, rollbackInvalid("snapshot lacks complete healthy replica verification")
		}
		verified[key] = true
	}
	expected := 0
	for i, previous := range desired.Components {
		var now domain.Component
		for _, candidate := range current.Components {
			if candidate.Name == previous.Name {
				now = candidate
				break
			}
		}
		if now.Name == "" || now.Kind != previous.Kind || now.Instances != previous.Instances || now.ResourceID != "" || previous.ResourceID != "" || now.Management != "" || previous.Management != "" {
			return desired, rollbackInvalid("configuration recovery requires unchanged OAP-owned component identities and replica counts")
		}
		for ordinal := 1; ordinal <= previous.Instances; ordinal++ {
			expected++
			if !verified[previous.Name+":"+strconv.Itoa(ordinal)] {
				return desired, rollbackInvalid("snapshot lacks complete healthy replica verification")
			}
		}
		desired.Components[i].Image = now.Image
		if now.HealthCheck != nil && previous.HealthCheck == nil {
			desired.Components[i].HealthCheck = &domain.HealthCheck{Mode: "image"}
		}
	}
	if len(verified) != expected {
		return desired, rollbackInvalid("snapshot contains unexpected instance verification")
	}
	if err := desired.Validate(); err != nil {
		return desired, rollbackInvalid("snapshot configuration is invalid")
	}
	return desired, nil
}
