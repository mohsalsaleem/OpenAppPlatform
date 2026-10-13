package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

type DeploymentPlan struct {
	ID                string            `json:"id"`
	ApplicationID     string            `json:"applicationId"`
	CredentialID      string            `json:"credentialId"`
	DefinitionVersion int64             `json:"definitionVersion"`
	TargetID          string            `json:"targetId"`
	Environment       string            `json:"environment"`
	Components        []string          `json:"components"`
	Images            map[string]string `json:"images"`
	PlanHash          string            `json:"planHash"`
	ExpiresAt         time.Time         `json:"expiresAt"`
	ApprovedAt        *time.Time        `json:"approvedAt,omitempty"`
	DeploymentID      string            `json:"deploymentId,omitempty"`
	Effect            string            `json:"effect"`
}

var immutablePlanImage = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]*@sha256:[a-f0-9]{64}$`)

func definitionHash(m domain.Manifest) string {
	raw, _ := json.Marshal(m)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (c *Controller) CreateDeploymentPlan(ctx context.Context, appID string, version int64, components []string, images map[string]string) (DeploymentPlan, error) {
	p, ok := domain.Identity(ctx)
	a, err := c.Store.Application(ctx, appID)
	if err != nil {
		return DeploymentPlan{}, err
	}
	if !ok || !p.Allows(appID, "deploy", a.Manifest.TargetID, a.Manifest.Environment) {
		return DeploymentPlan{}, access.ErrDenied
	}
	if version < 1 || version != a.Version {
		return DeploymentPlan{}, domain.ErrConflict
	}
	if len(components) == 0 || len(components) > 16 || len(images) > 16 {
		return DeploymentPlan{}, errors.New("select 1–16 managed image components")
	}
	components = slices.Clone(components)
	sort.Strings(components)
	resolved := map[string]string{}
	for i, name := range components {
		if i > 0 && components[i-1] == name {
			return DeploymentPlan{}, errors.New("duplicate component")
		}
		found := false
		for _, comp := range a.Manifest.Components {
			if comp.Name != name {
				continue
			}
			found = true
			if comp.Management == "observe" || comp.Kind == "database" {
				return DeploymentPlan{}, errors.New("observed components retain native authority")
			}
			image := comp.Image
			if v, ok := images[name]; ok {
				image = v
			}
			if len(image) > 512 || !immutablePlanImage.MatchString(image) {
				return DeploymentPlan{}, errors.New("plans require immutable digest images for each selected component")
			}
			resolved[name] = image
		}
		if !found {
			return DeploymentPlan{}, errors.New("unknown component")
		}
	}
	for name := range images {
		if !slices.Contains(components, name) {
			return DeploymentPlan{}, errors.New("image override outside selection")
		}
	}
	v := DeploymentPlan{ID: domain.NewID(), ApplicationID: appID, CredentialID: p.CredentialID, DefinitionVersion: version, TargetID: a.Manifest.TargetID, Environment: a.Manifest.Environment, Components: components, Images: resolved, Effect: "Standard lifecycle may restart selected workloads and cause downtime. No application data restore, migration, paid provisioning or destructive cleanup is authorized."}
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(append(raw, []byte(definitionHash(a.Manifest))...))
	v.PlanHash = hex.EncodeToString(sum[:])
	cs, _ := json.Marshal(components)
	im, _ := json.Marshal(resolved)
	err = c.Store.Pool.QueryRow(ctx, `INSERT INTO oap_deployment_plans(id,application_id,credential_id,definition_version,definition_hash,components,images,plan_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING expires_at`, v.ID, appID, p.CredentialID, version, definitionHash(a.Manifest), cs, im, v.PlanHash).Scan(&v.ExpiresAt)
	return v, err
}
func (c *Controller) DeploymentPlan(ctx context.Context, appID, id string) (DeploymentPlan, error) {
	a, err := c.Store.Application(ctx, appID)
	if err != nil {
		return DeploymentPlan{}, err
	}
	v := DeploymentPlan{ApplicationID: appID, TargetID: a.Manifest.TargetID, Environment: a.Manifest.Environment, Effect: "Standard lifecycle may cause downtime; application data is not restored."}
	var cs, im []byte
	err = c.Store.Pool.QueryRow(ctx, `SELECT id,credential_id,definition_version,components,images,plan_hash,expires_at,approved_at,COALESCE(deployment_id,'') FROM oap_deployment_plans WHERE id=$1 AND application_id=$2`, id, appID).Scan(&v.ID, &v.CredentialID, &v.DefinitionVersion, &cs, &im, &v.PlanHash, &v.ExpiresAt, &v.ApprovedAt, &v.DeploymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, domain.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(cs, &v.Components); err != nil {
		return v, err
	}
	err = json.Unmarshal(im, &v.Images)
	return v, err
}
func (c *Controller) ApproveDeploymentPlan(ctx context.Context, appID, id, hash string) (DeploymentPlan, error) {
	p, ok := domain.Identity(ctx)
	if !ok || p.Kind != "session" || p.Role != "owner" {
		return DeploymentPlan{}, access.ErrDenied
	}
	tx, err := c.Store.Pool.Begin(ctx)
	if err != nil {
		return DeploymentPlan{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", appID); err != nil {
		return DeploymentPlan{}, err
	}
	a, err := c.Store.ApplicationTx(ctx, tx, appID)
	if err != nil {
		return DeploymentPlan{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE oap_deployment_plans SET approved_by=$3,approved_at=now() WHERE id=$1 AND application_id=$2 AND plan_hash=$4 AND expires_at>now() AND definition_version=$5 AND definition_hash=$6 AND deployment_id IS NULL`, id, appID, p.CredentialID, hash, a.Version, definitionHash(a.Manifest))
	if err != nil {
		return DeploymentPlan{}, err
	}
	if result.RowsAffected() != 1 {
		return DeploymentPlan{}, domain.ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return DeploymentPlan{}, err
	}
	return c.DeploymentPlan(ctx, appID, id)
}

type planKey struct{}

func (c *Controller) ExecuteDeploymentPlan(ctx context.Context, appID, id string) (domain.Deployment, error) {
	v, err := c.DeploymentPlan(ctx, appID, id)
	if err != nil {
		return domain.Deployment{}, err
	}
	p, ok := domain.Identity(ctx)
	if !ok || p.CredentialID != v.CredentialID || !p.Allows(appID, "deploy", v.TargetID, v.Environment) {
		return domain.Deployment{}, access.ErrDenied
	}
	return c.EnqueueSelected(context.WithValue(ctx, planKey{}, id), appID, "plan:"+id, v.Images, v.DefinitionVersion, v.Components)
}

// Runs inside the same transaction and application lock as release admission.
func (c *Controller) validateDeploymentPlan(ctx context.Context, tx pgx.Tx, a domain.Application) error {
	id, _ := ctx.Value(planKey{}).(string)
	if id == "" {
		return nil
	}
	p, _ := domain.Identity(ctx)
	var valid bool
	err := tx.QueryRow(ctx, `SELECT definition_version=$3 AND definition_hash=$4 AND credential_id=$5 AND approved_at IS NOT NULL AND expires_at>now() AND deployment_id IS NULL AND EXISTS(SELECT 1 FROM oap_credentials c JOIN oap_users u ON u.id=c.user_id WHERE c.id=approved_by AND c.kind='session' AND c.revoked_at IS NULL AND c.expires_at>now() AND u.active AND u.role='owner') FROM oap_deployment_plans WHERE id=$1 AND application_id=$2 FOR UPDATE`, id, a.ID, a.Version, definitionHash(a.Manifest), p.CredentialID).Scan(&valid)
	if err != nil || !valid {
		return domain.ErrConflict
	}
	return nil
}
func (c *Controller) linkDeploymentPlan(ctx context.Context, tx pgx.Tx, id string) error {
	plan, _ := ctx.Value(planKey{}).(string)
	if plan == "" {
		return nil
	}
	_, err := tx.Exec(ctx, "UPDATE oap_deployment_plans SET deployment_id=$2 WHERE id=$1", plan, id)
	return err
}
