package controller

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/runtimeinputs"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
)

type ActionRequest struct {
	Action          string                           `json:"action"`
	ApplicationID   string                           `json:"applicationId,omitempty"`
	ExpectedVersion int64                            `json:"expectedVersion,omitempty"`
	Specification   *domain.ApplicationSpecification `json:"specification,omitempty"`
	Components      []string                         `json:"components,omitempty"`
	Images          map[string]string                `json:"images,omitempty"`
	Component       string                           `json:"component,omitempty"`
	Ordinal         int                              `json:"ordinal,omitempty"`
	DeploymentID    string                           `json:"deploymentId,omitempty"`
	Recovery        *Recovery                        `json:"recovery,omitempty"`
	RequireReview   bool                             `json:"requireReview,omitempty"`
}

func (c *Controller) ValidateSpecification(ctx context.Context, spec domain.ApplicationSpecification, appID string) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	p, ok := domain.Identity(ctx)
	if !ok {
		return access.ErrDenied
	}
	if appID != "" {
		app, err := c.Store.Application(ctx, appID)
		if err != nil {
			return err
		}
		if !p.Reads(appID, app.Manifest.TargetID, app.Manifest.Environment) || app.Manifest.Name != spec.Spec.Name || app.Manifest.TargetID != spec.Spec.TargetID || app.Manifest.Environment != spec.Spec.Environment {
			return access.ErrDenied
		}
	} else if !(p.Creates(spec.Spec.TargetID, spec.Spec.Environment) || (p.Kind == "session" && p.Operates(""))) {
		return access.ErrDenied
	}
	target, err := c.Store.Target(ctx, spec.Spec.TargetID)
	if err != nil {
		return err
	}
	if target.Environment != spec.Spec.Environment {
		return errors.New("specification environment must match target")
	}
	adapter, err := c.Factory(target)
	if err != nil {
		return err
	}
	return operator.ValidateTargetManifest(spec.Spec, adapter)
}
func (c *Controller) prepareAction(ctx context.Context, request *ActionRequest) (domain.Application, domain.Target, error) {
	p, ok := domain.Identity(ctx)
	if !ok {
		return domain.Application{}, domain.Target{}, access.ErrDenied
	}
	var app domain.Application
	allowed := false
	switch request.Action {
	case "create":
		if request.ApplicationID != "" || request.ExpectedVersion != 0 || request.Specification == nil {
			return app, domain.Target{}, errors.New("create requires a new specification without app/version")
		}
		if err := request.Specification.Validate(); err != nil {
			return app, domain.Target{}, err
		}
		app.Manifest = request.Specification.Spec
		allowed = p.Creates(app.Manifest.TargetID, app.Manifest.Environment) || (p.Kind == "session" && p.Operates(""))
		if p.TargetGrant() {
			for _, comp := range app.Manifest.Components {
				if comp.ResourceID != "" || comp.Management == "observe" || comp.Kind == "database" {
					return app, domain.Target{}, access.ErrDenied
				}
			}
		}
	case "configure", "deploy", "restart", "recover", "scale-down":
		var err error
		app, err = c.Store.Application(ctx, request.ApplicationID)
		if err != nil {
			return app, domain.Target{}, err
		}
		if request.ExpectedVersion < 1 || request.ExpectedVersion != app.Version {
			return app, domain.Target{}, domain.ErrConflict
		}
		allowed = p.Allows(app.ID, request.Action, app.Manifest.TargetID, app.Manifest.Environment)
	default:
		return app, domain.Target{}, errors.New("unsupported action; no destructive executor is available")
	}
	if !allowed {
		return app, domain.Target{}, access.ErrDenied
	}
	if request.Action != "create" && request.Action != "configure" && request.Action != "scale-down" && request.Specification != nil {
		return app, domain.Target{}, errors.New("specification is only valid for create/configure")
	}
	if request.Action != "deploy" && (len(request.Components) > 0 || len(request.Images) > 0) {
		return app, domain.Target{}, errors.New("release selection is only valid for deploy")
	}
	if request.Action != "restart" && (request.Component != "" || request.Ordinal != 0) {
		return app, domain.Target{}, errors.New("component/ordinal are only valid for restart")
	}
	if request.Action != "recover" && (request.Recovery != nil || request.DeploymentID != "") {
		return app, domain.Target{}, errors.New("recovery fields are only valid for recover")
	}
	target, err := c.Store.Target(ctx, app.Manifest.TargetID)
	if err != nil {
		return app, target, err
	}
	adapter, err := c.Factory(target)
	if err != nil {
		return app, target, err
	}
	switch request.Action {
	case "create":
		if err = c.authorizeSecurity(ctx, app.Manifest); err != nil {
			return app, target, err
		}
		for _, comp := range app.Manifest.Components {
			if comp.Kind == "database" && (p.Kind != "session" || p.Role != "owner") {
				return app, target, access.ErrDenied
			}
		}
		err = operator.ValidateTargetManifest(app.Manifest, adapter)
	case "configure":
		if request.Specification == nil {
			return app, target, errors.New("configure requires a specification")
		}
		if err = request.Specification.Validate(); err != nil {
			return app, target, err
		}
		tx, e := c.Store.Pool.Begin(ctx)
		if e != nil {
			return app, target, e
		}
		_, err = c.updateApplicationTx(ctx, tx, app.ID, request.Specification.Spec, request.ExpectedVersion)
		tx.Rollback(ctx)
	case "scale-down":
		if request.Specification == nil {
			return app, target, errors.New("scale-down requires the desired specification")
		}
		if err = request.Specification.Validate(); err != nil {
			return app, target, err
		}
		if _, err = desiredScaleDown(app.Manifest, request.Specification.Spec); err != nil {
			return app, target, err
		}
		if !adapter.Capabilities().Retirement {
			return app, target, errors.New("target does not support retirement")
		}
	case "deploy":
		if len(request.Components) == 0 {
			for _, comp := range app.Manifest.Components {
				if comp.Management != "observe" && comp.Kind != "database" {
					request.Components = append(request.Components, comp.Name)
				}
			}
		}
		seen := map[string]bool{}
		images := map[string]string{}
		for _, name := range request.Components {
			if seen[name] {
				return app, target, errors.New("duplicate deployment component")
			}
			seen[name] = true
			found := false
			for _, comp := range app.Manifest.Components {
				if comp.Name != name {
					continue
				}
				if comp.Management == "observe" || comp.Kind == "database" {
					return app, target, access.ErrDenied
				}
				image := comp.Image
				if override, ok := request.Images[name]; ok {
					image = override
				}
				if !(immutablePlanImage.MatchString(image) || (target.Operator == "docker" && dockerContentID.MatchString(image))) {
					return app, target, errors.New("action plans require immutable registry digests or Docker cached content IDs")
				}
				images[name] = image
				found = true
			}
			if !found {
				return app, target, errors.New("unknown deployment component")
			}
		}
		for name := range request.Images {
			if !seen[name] {
				return app, target, errors.New("image override outside selection")
			}
		}
		if len(images) == 0 {
			return app, target, errors.New("select a managed component")
		}
		request.Images = images
	case "restart":
		if !adapter.Capabilities().Restart || request.Ordinal < 1 {
			return app, target, errors.New("restart requires a supported instance")
		}
		found := false
		for _, comp := range app.Manifest.Components {
			if comp.Name == request.Component && request.Ordinal <= comp.Instances && comp.Management != "observe" && comp.Kind != "database" {
				found = true
			}
		}
		if !found {
			return app, target, errors.New("unknown managed restart instance")
		}
	case "recover":
		if request.Recovery == nil || request.Recovery.ExpectedUpdatedAt.IsZero() {
			return app, target, errors.New("recovery requires exact existing recovery parameters")
		}
		d, e := c.Store.Deployment(ctx, request.DeploymentID)
		if e != nil {
			return app, target, e
		}
		if d.ApplicationID != app.ID || d.State != "attention" || !d.UpdatedAt.Equal(request.Recovery.ExpectedUpdatedAt) {
			return app, target, domain.ErrConflict
		}
		if !p.Allows(app.ID, releaseAuthorizationAction(d), app.Manifest.TargetID, app.Manifest.Environment) {
			return app, target, access.ErrDenied
		}
	}
	return app, target, err
}
func (c *Controller) actionInputs(ctx context.Context, app domain.Application, req ActionRequest, id string) (string, error) {
	pins := map[string]string{}
	if req.Action == "deploy" || req.Action == "restart" {
		selection := req.Components
		if req.Action == "restart" {
			selection = []string{req.Component}
		}
		for _, name := range selection {
			comp, err := domain.RuntimeComponent(app.Manifest, name)
			if err != nil {
				return "", err
			}
			if image, ok := req.Images[name]; ok {
				comp.Image = image
			}
			resolved, err := c.RuntimeInputs.Resolve(app.Manifest.TargetID, app.ID, comp)
			if err != nil {
				return "", err
			}
			pins[name] = runtimeinputs.Fingerprint(resolved, id)
		}
	}
	if req.Action == "restart" || req.Action == "scale-down" {
		bindings, err := c.Store.Bindings(ctx, app.ID)
		if err != nil {
			return "", err
		}
		pins["bindings"] = store.HashJSON(bindings)
	}
	return store.HashJSON(pins), nil
}
func (c *Controller) CreateActionPlan(ctx context.Context, req ActionRequest) (store.ActionPlan, error) {
	app, target, err := c.prepareAction(ctx, &req)
	if err != nil {
		return store.ActionPlan{}, err
	}
	p, _ := domain.Identity(ctx)
	plan := store.ActionPlan{ID: domain.NewID(), Action: req.Action, CredentialID: p.CredentialID, ApplicationID: app.ID, TargetID: target.ID, Environment: target.Environment, ExpectedVersion: req.ExpectedVersion, DefinitionHash: store.HashJSON(app.Manifest), TargetHash: store.HashJSON(target), RequiresReview: req.RequireReview, ExpiresAt: time.Now().UTC().Add(30 * time.Minute)}
	plan.InputFingerprint, err = c.actionInputs(ctx, app, req, plan.ID)
	if err != nil {
		return plan, err
	}
	plan.Payload, _ = json.Marshal(req)
	effect := "Save metadata only; no deployment or runtime mutation."
	if req.Action == "deploy" || req.Action == "restart" {
		effect = "Standard runtime lifecycle; interruption/downtime is possible. No application data restore."
	}
	if req.Action == "scale-down" {
		effect = "Withdraw routing, wait bounded drain, stop exact excess instances, then save desired count. Volumes are retained; exact owner review is enforced when requested."
	}
	if req.Action == "recover" {
		effect = "Existing constrained recovery; no arbitrary redispatch, SQL or data restore."
	}
	summary := map[string]any{"untrustedEvidence": true, "effect": effect, "components": req.Components, "images": req.Images, "component": req.Component, "ordinal": req.Ordinal, "deploymentId": req.DeploymentID, "policy": "owner-issued action grant; requested exact owner review is enforced", "missingEvidence": []string{"application behavior", "final admission conflicts"}}
	if req.Action == "deploy" {
		summary["desiredIngress"] = app.Manifest.Ingress
		summary["ingressPublication"] = "Replaces this application's last published routes; nil desired ingress withdraws its routes without deleting containers, volumes or certificates. Sibling applications retain their last published routes. Domain changes withdraw the old hostname for new requests. DNS and certificate readiness require separate verification."
	}
	if req.Specification != nil {
		summary["specification"] = domain.ExportSpecification(req.Specification.Spec)
		if app.ID != "" {
			summary["diff"] = domain.SpecificationDiff(app, req.Specification.Spec)
			if req.Action == "configure" && !reflect.DeepEqual(app.Manifest.Ingress, req.Specification.Spec.Ingress) {
				summary["ingressChange"] = map[string]any{"previous": app.Manifest.Ingress, "desired": req.Specification.Spec.Ingress, "publication": "A separate deploy publishes the desired routes and withdraws the previously published routes. Saving leaves live routes unchanged."}
			}
		}
	}
	plan.Summary, _ = json.Marshal(summary)
	// Private random key makes the public commitment opaque to literal-value guessing.
	mac := hmac.New(sha256.New, []byte(access.Secret()))
	mac.Write(plan.Payload)
	mac.Write([]byte(plan.DefinitionHash + plan.TargetHash + plan.InputFingerprint + plan.ID))
	plan.Hash = hex.EncodeToString(mac.Sum(nil))
	return c.Store.SaveActionPlan(ctx, plan)
}
func (c *Controller) ActionPlan(ctx context.Context, id string) (store.ActionPlan, error) {
	plan, err := c.Store.ActionPlan(ctx, id)
	if err != nil {
		return plan, err
	}
	p, ok := domain.Identity(ctx)
	if !ok || !(p.Kind == "session" && p.Role == "owner" || p.CredentialID == plan.CredentialID || plan.ApplicationID != "" && p.Reads(plan.ApplicationID, plan.TargetID, plan.Environment)) {
		return store.ActionPlan{}, access.ErrDenied
	}
	return plan, nil
}
func (c *Controller) ApproveActionPlan(ctx context.Context, id, hash string) (store.ActionPlan, error) {
	p, ok := domain.Identity(ctx)
	if !ok || p.Kind != "session" || p.Role != "owner" {
		return store.ActionPlan{}, access.ErrDenied
	}
	plan, err := c.ActionPlan(ctx, id)
	if err != nil {
		return plan, err
	}
	if plan.Hash != hash || plan.Result != nil || !plan.ExpiresAt.After(time.Now()) {
		return plan, domain.ErrConflict
	}
	var req ActionRequest
	if json.Unmarshal(plan.Payload, &req) != nil {
		return plan, domain.ErrConflict
	}
	// Recheck the creator's current scope, not the owner's broader authority.
	creator, err := (access.Service{Pool: c.Store.Pool}).ByID(ctx, plan.CredentialID)
	if err != nil {
		return plan, access.ErrDenied
	}
	creatorContext := domain.WithPrincipal(ctx, creator)
	app, target, err := c.prepareAction(creatorContext, &req)
	if err != nil {
		return plan, err
	}
	inputs, err := c.actionInputs(ctx, app, req, id)
	if err != nil || inputs != plan.InputFingerprint || store.HashJSON(target) != plan.TargetHash || store.HashJSON(app.Manifest) != plan.DefinitionHash {
		return plan, domain.ErrConflict
	}
	result, err := c.Store.Pool.Exec(ctx, "UPDATE oap_action_plans SET approved_by=$2,approved_at=now() WHERE id=$1 AND plan_hash=$3 AND result IS NULL AND expires_at>now()", id, p.CredentialID, hash)
	if err != nil {
		return plan, err
	}
	if result.RowsAffected() != 1 {
		return plan, domain.ErrConflict
	}
	return c.ActionPlan(ctx, id)
}
func (c *Controller) ExecuteActionPlan(ctx context.Context, id string) (store.ActionPlan, error) {
	plan, err := c.ActionPlan(ctx, id)
	if err != nil {
		return plan, err
	}
	p, _ := domain.Identity(ctx)
	if p.CredentialID != plan.CredentialID {
		return plan, access.ErrDenied
	}
	allowed := p.Allows(plan.ApplicationID, plan.Action, plan.TargetID, plan.Environment)
	if plan.Action == "create" {
		allowed = p.Creates(plan.TargetID, plan.Environment) || (p.Kind == "session" && p.Operates(""))
	}
	if !allowed {
		return plan, access.ErrDenied
	}
	if plan.Result != nil {
		return plan, nil
	}
	var req ActionRequest
	if json.Unmarshal(plan.Payload, &req) != nil {
		return plan, domain.ErrConflict
	}
	app, _, err := c.prepareAction(ctx, &req)
	if err != nil {
		return plan, err
	}
	inputs, err := c.actionInputs(ctx, app, req, id)
	if err != nil {
		return plan, domain.ErrConflict
	}
	ctx = domain.WithAction(store.WithActionPlan(ctx, id, req.Action, inputs), req.Action)
	switch req.Action {
	case "create":
		_, err = c.CreateApplication(domain.WithCreationKey(ctx, "action-plan:"+id), req.Specification.Spec)
	case "configure":
		_, err = c.UpdateApplication(ctx, req.ApplicationID, req.Specification.Spec, req.ExpectedVersion)
	case "deploy":
		_, err = c.EnqueueSelected(ctx, req.ApplicationID, "action-plan:"+id, req.Images, req.ExpectedVersion, req.Components)
	case "scale-down":
		down, e := desiredScaleDown(app.Manifest, req.Specification.Spec)
		if e != nil {
			return plan, e
		}
		_, err = c.EnqueueScaleDown(ctx, req.ApplicationID, "action-plan:"+id, down, req.ExpectedVersion)
	case "restart":
		_, err = c.EnqueueRestart(ctx, req.ApplicationID, "action-plan:"+id, RestartRequest{Component: req.Component, Ordinal: req.Ordinal}, req.ExpectedVersion)
	case "recover":
		_, err = c.Recover(ctx, req.DeploymentID, *req.Recovery)
	default:
		err = errors.New("unsupported action")
	}
	if err != nil {
		return plan, err
	}
	return c.ActionPlan(ctx, id)
}

// The first spec-driven retirement changes exactly one replica count. Other
// topology/runtime edits stay separate and do not get applied by retirement.
func desiredScaleDown(current, proposed domain.Manifest) (ScaleDownRequest, error) {
	var result ScaleDownRequest
	if len(current.Components) != len(proposed.Components) {
		return result, errors.New("scale-down cannot add or remove components")
	}
	clone := proposed
	clone.Components = append([]domain.Component(nil), proposed.Components...)
	for i, comp := range current.Components {
		other := clone.Components[i]
		if comp.Name != other.Name {
			return result, errors.New("scale-down preserves component order and identity")
		}
		if other.Instances < comp.Instances {
			if result.Component != "" || other.Instances < 1 || comp.Management != "" || comp.ResourceID != "" || comp.Kind == "database" {
				return result, errors.New("scale-down changes one owned component count")
			}
			result = ScaleDownRequest{Component: comp.Name, Instances: other.Instances}
			clone.Components[i].Instances = comp.Instances
		}
	}
	if result.Component == "" || !reflect.DeepEqual(current, clone) {
		return result, errors.New("scale-down only lowers one instance count; save other changes separately")
	}
	return result, nil
}
