package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

var dockerContentID = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var registryContentRef = regexp.MustCompile(`@sha256:[a-f0-9]{64}$`)

type RollbackValidationError struct{ Message string }

func (e *RollbackValidationError) Error() string { return e.Message }
func rollbackInvalid(message string) error       { return &RollbackValidationError{Message: message} }

type RollbackRequest struct {
	ReleaseID                    string `json:"releaseId"`
	PlanHash                     string `json:"planHash"`
	AcknowledgeDataCompatibility bool   `json:"acknowledgeDataCompatibility"`
}
type RollbackInstance struct {
	Component    string `json:"component"`
	Ordinal      int    `json:"ordinal"`
	ResourceID   string `json:"resourceId"`
	CurrentImage string `json:"currentImage"`
	RestoreImage string `json:"restoreImage"`
}
type RollbackPlan struct {
	ApplicationID        string             `json:"applicationId"`
	ReleaseID            string             `json:"releaseId"`
	LatestReleaseID      string             `json:"latestReleaseId"`
	DefinitionVersion    int64              `json:"definitionVersion"`
	PlanHash             string             `json:"planHash"`
	Instances            []RollbackInstance `json:"instances"`
	TargetHash           string             `json:"targetHash"`
	ArtifactAvailability string             `json:"artifactAvailability"`
}

// Only images change. Runtime configuration and topology must already match the
// recorded snapshot; this is not configuration/data/schema rollback.
func sameRollbackConfiguration(a, b domain.Manifest) bool {
	normalize := func(m domain.Manifest) domain.Manifest {
		m.Components = append([]domain.Component(nil), m.Components...)
		for i := range m.Components {
			c := &m.Components[i]
			c.Image = ""
			c.DependsOn = append([]string(nil), c.DependsOn...)
			sort.Strings(c.DependsOn)
			healthy := c.Readiness != nil && c.Readiness.RequireHealthy
			seconds := int(observationTimeout(*c).Seconds())
			c.Readiness = &domain.ReadinessPolicy{RequireHealthy: healthy, TimeoutSeconds: seconds}
		}
		sort.Slice(m.Components, func(i, j int) bool { return m.Components[i].Name < m.Components[j].Name })
		return m
	}
	av, _ := json.Marshal(normalize(a))
	bv, _ := json.Marshal(normalize(b))
	return string(av) == string(bv)
}

func (c *Controller) PlanRollback(ctx context.Context, appID, releaseID string, version int64) (RollbackPlan, error) {
	tx, err := c.Store.Pool.Begin(ctx)
	if err != nil {
		return RollbackPlan{}, err
	}
	defer tx.Rollback(ctx)
	app, err := c.Store.ApplicationTx(ctx, tx, appID)
	if err != nil {
		return RollbackPlan{}, err
	}
	if version < 1 || app.Version != version {
		return RollbackPlan{}, domain.ErrConflict
	}
	plan, _, err := c.rollbackPlan(ctx, tx, app, releaseID)
	return plan, err
}
func (c *Controller) EnqueueRollback(ctx context.Context, appID, key string, request RollbackRequest, version int64) (domain.Deployment, error) {
	if version < 1 || request.ReleaseID == "" || len(request.PlanHash) != 64 || !request.AcknowledgeDataCompatibility {
		return domain.Deployment{}, rollbackInvalid("review a rollback plan and acknowledge application data compatibility")
	}
	return c.enqueueOperation(ctx, appID, key, nil, version, nil, nil, nil, &request)
}
func (c *Controller) rollbackPlan(ctx context.Context, tx pgx.Tx, app domain.Application, releaseID string) (RollbackPlan, domain.Deployment, error) {
	plan := RollbackPlan{ApplicationID: app.ID, ReleaseID: releaseID, DefinitionVersion: app.Version, Instances: []RollbackInstance{}, ArtifactAvailability: "Not preflight-verified; the operator must fetch the exact content reference during preparation/deployment"}
	original, err := c.Store.DeploymentTx(ctx, tx, releaseID)
	if err != nil {
		return plan, original, err
	}
	if original.ApplicationID != app.ID {
		return plan, original, domain.ErrNotFound
	}
	if original.State != "succeeded" || original.Operation != "deploy" {
		return plan, original, rollbackInvalid("rollback requires a successful image deployment snapshot")
	}
	if !sameRollbackConfiguration(app.Manifest, original.Manifest) {
		return plan, original, rollbackInvalid("snapshot configuration/topology differs; image-only rollback cannot restore it")
	}
	for _, comp := range app.Manifest.Components {
		if comp.Management == "observe" || comp.ResourceID != "" {
			return plan, original, rollbackInvalid("rollback currently requires OAP-owned managed image components")
		}
	}
	var active bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oap_deployments WHERE application_id=$1 AND state IN ('queued','running','attention'))", app.ID).Scan(&active); err != nil {
		return plan, original, err
	}
	if active {
		return plan, original, domain.ErrConflict
	}
	if err = tx.QueryRow(ctx, "SELECT id FROM oap_deployments WHERE application_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1", app.ID).Scan(&plan.LatestReleaseID); err != nil {
		return plan, original, err
	}
	if plan.LatestReleaseID == original.ID {
		return plan, original, rollbackInvalid("choose a preceding successful release; use redeploy for the latest snapshot")
	}
	target, err := c.Store.TargetTx(ctx, tx, app.Manifest.TargetID)
	if err != nil {
		return plan, original, err
	}
	plan.TargetHash = rollbackTargetHash(target)
	adapter, err := c.Factory(target)
	if err != nil {
		return plan, original, err
	}
	if !adapter.Capabilities().Standard || !adapter.Capabilities().ImmutableImages || !adapter.Capabilities().ImageRollback {
		return plan, original, rollbackInvalid("target does not support immutable image releases")
	}
	selected := map[string]int{}
	uniqueInstances := map[string]bool{}
	changedImage := false
	for _, step := range original.Steps {
		if step.Phase != "succeeded" || step.Action != "" || step.ResourceID == "" || !Healthy(step.Observed) {
			return plan, original, rollbackInvalid("source snapshot lacks completed healthy instance verification")
		}
		var comp domain.Component
		for _, candidate := range original.Manifest.Components {
			if candidate.Name == step.Component {
				comp = candidate
				break
			}
		}
		if comp.Name == "" || (!registryContentRef.MatchString(comp.Image) && !(target.Operator == "docker" && dockerContentID.MatchString(comp.Image))) {
			return plan, original, rollbackInvalid("rollback image must be a recorded registry digest or Docker content ID")
		}
		instanceKey := step.Component + ":" + strconv.Itoa(step.Ordinal)
		if step.Ordinal < 1 || step.Ordinal > comp.Instances || uniqueInstances[instanceKey] {
			return plan, original, rollbackInvalid("source snapshot has invalid or duplicate instance verification")
		}
		uniqueInstances[instanceKey] = true
		var binding string
		err = tx.QueryRow(ctx, "SELECT resource_id FROM oap_bindings WHERE target_id=$1 AND application_id=$2 AND component=$3 AND ordinal=$4 AND retired_at IS NULL", target.ID, app.ID, step.Component, step.Ordinal).Scan(&binding)
		if err != nil || binding != step.ResourceID {
			return plan, original, rollbackInvalid("source instance binding changed or was retired")
		}
		resource, err := adapter.Inspect(ctx, binding)
		if err != nil {
			return plan, original, rollbackInvalid("rollback instance cannot be inspected")
		}
		if resource.ArtifactKind != "image" || resource.Description != "OpenAppPlatform:"+app.ID+":"+comp.Name || resource.Port != comp.Port {
			return plan, original, rollbackInvalid("rollback instance ownership or port configuration changed")
		}
		var imageReleaseID string
		if err = tx.QueryRow(ctx, "SELECT id FROM oap_deployments WHERE application_id=$1 AND operation='deploy' AND steps @> jsonb_build_array(jsonb_build_object('component',$2::text)) ORDER BY created_at DESC,id DESC LIMIT 1", app.ID, comp.Name).Scan(&imageReleaseID); err != nil {
			return plan, original, err
		}
		imageRelease, err := c.Store.DeploymentTx(ctx, tx, imageReleaseID)
		if err != nil {
			return plan, original, err
		}
		expected := ""
		for _, current := range imageRelease.Manifest.Components {
			if current.Name == comp.Name {
				expected = current.Image
			}
		}
		if expected == "" || resource.Image != expected {
			return plan, original, rollbackInvalid("operator image configuration differs from the latest release; reconcile drift first")
		}
		if resource.Image != comp.Image {
			changedImage = true
		}
		checker, ok := adapter.(operator.RollbackSafetyChecker)
		if !ok {
			return plan, original, rollbackInvalid("target cannot verify current rollback configuration")
		}
		runtime, err := domain.RuntimeComponent(app.Manifest, comp.Name)
		if err != nil {
			return plan, original, err
		}
		if err = checker.CheckRollbackConfiguration(ctx, binding, runtime); err != nil {
			return plan, original, rollbackInvalid("native runtime configuration differs or cannot be verified for rollback")
		}
		selected[comp.Name]++
		plan.Instances = append(plan.Instances, RollbackInstance{Component: comp.Name, Ordinal: step.Ordinal, ResourceID: binding, CurrentImage: resource.Image, RestoreImage: comp.Image})
	}
	if !changedImage {
		return plan, original, rollbackInvalid("snapshot images already match the operator; use redeploy instead")
	}
	if len(plan.Instances) == 0 {
		return plan, original, rollbackInvalid("source snapshot has no instances")
	}
	for _, comp := range original.Manifest.Components {
		if count := selected[comp.Name]; count != 0 && count != comp.Instances {
			return plan, original, rollbackInvalid("source snapshot has incomplete replica verification")
		}
	}
	raw, _ := json.Marshal(struct {
		Plan                RollbackPlan
		Target              domain.Target
		CredentialReference string
	}{plan, target, target.TokenEnv})
	sum := sha256.Sum256(raw)
	plan.PlanHash = hex.EncodeToString(sum[:])
	return plan, original, nil
}

func (c *Controller) verifyRollbackInstance(ctx context.Context, adapter operator.Adapter, d domain.Deployment, step domain.Step, comp domain.Component) error {
	if step.RollbackFrom == "" {
		return nil
	}
	target, err := c.Store.Target(ctx, d.Manifest.TargetID)
	if err != nil || rollbackTargetHash(target) != step.RollbackTargetHash {
		return rollbackInvalid("rollback target authority changed before execution")
	}
	var ref string
	err = c.Store.Pool.QueryRow(ctx, "SELECT resource_id FROM oap_bindings WHERE target_id=$1 AND application_id=$2 AND component=$3 AND ordinal=$4 AND retired_at IS NULL", d.Manifest.TargetID, d.ApplicationID, comp.Name, step.Ordinal).Scan(&ref)
	if err != nil || ref != step.RollbackResourceID {
		return rollbackInvalid("rollback instance binding changed before execution")
	}
	resource, err := adapter.Inspect(ctx, ref)
	if err != nil {
		return rollbackInvalid("rollback instance cannot be inspected before execution")
	}
	if resource.ArtifactKind != "image" || resource.Description != "OpenAppPlatform:"+d.ApplicationID+":"+comp.Name || resource.Port != comp.Port {
		return rollbackInvalid("rollback ownership/configuration drift requires review")
	}
	if resource.Image != step.RollbackExpectedImage && resource.Image != comp.Image {
		return rollbackInvalid("rollback image drift requires review")
	}
	checker, ok := adapter.(operator.RollbackSafetyChecker)
	if !ok {
		return rollbackInvalid("rollback configuration verification is unavailable")
	}
	runtime, err := domain.RuntimeComponent(d.Manifest, comp.Name)
	if err != nil {
		return err
	}
	if err = checker.CheckRollbackConfiguration(ctx, ref, runtime); err != nil {
		return rollbackInvalid("native rollback configuration drift requires review")
	}
	return nil
}

func rollbackTargetHash(target domain.Target) string {
	raw, _ := json.Marshal(struct {
		Target              domain.Target
		CredentialReference string
	}{target, target.TokenEnv})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
