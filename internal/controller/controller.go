// Package controller owns releases; adapters own provider-specific operations.
package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/source"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type DeployArgs struct {
	DeploymentID string `json:"deploymentId"`
}

func (DeployArgs) Kind() string { return "oap_deploy" }

type Controller struct {
	SourceHooks     map[string]source.Hook
	SourceBuilder   source.Builder
	RequireIdentity bool
	Store           *store.Store
	Factory         operator.Factory
	Jobs            *river.Client[pgx.Tx]
	PollInterval    time.Duration
}
type Worker struct {
	river.WorkerDefaults[DeployArgs]
	Controller *Controller
}

func New(ctx context.Context, s *store.Store, f operator.Factory) (*Controller, error) {
	c := &Controller{Store: s, Factory: f, PollInterval: 2 * time.Second}
	driver := riverpgxv5.New(s.Pool)
	migrator, e := rivermigrate.New(driver, nil)
	if e != nil {
		return nil, e
	}
	if _, e = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); e != nil {
		return nil, e
	}
	workers := river.NewWorkers()
	river.AddWorker(workers, &Worker{Controller: c})
	river.AddWorker(workers, &SourceWorker{Controller: c})
	c.Jobs, e = river.NewClient(driver, &river.Config{Queues: map[string]river.QueueConfig{"default": {MaxWorkers: 4}}, Workers: workers, JobTimeout: time.Minute})
	return c, e
}
func (c *Controller) Adapter(ctx context.Context, id string) (operator.Adapter, error) {
	t, e := c.Store.Target(ctx, id)
	if e != nil {
		return nil, e
	}
	return c.Factory(t)
}
func (c *Controller) CreateApplication(ctx context.Context, m domain.Manifest) (domain.Application, error) {
	for i := range m.Components {
		if m.Components[i].ResourceID != "" && m.Components[i].Management == "" {
			m.Components[i].Management = "observe"
		}
	}
	if e := m.Validate(); e != nil {
		return domain.Application{}, e
	}
	t, e := c.Store.Target(ctx, m.TargetID)
	if e != nil {
		return domain.Application{}, e
	}
	if t.Environment != m.Environment {
		return domain.Application{}, errors.New("application environment must match target environment")
	}
	a, e := c.Factory(t)
	if e != nil {
		return domain.Application{}, e
	}
	if e = operator.ValidateRuntime(m, a.Capabilities()); e != nil {
		return domain.Application{}, e
	}
	for _, comp := range m.Components {
		if comp.ResourceID != "" {
			r, e := a.Inspect(ctx, comp.ResourceID)
			if e != nil {
				return domain.Application{}, e
			}
			if r.ArtifactKind != "image" && r.ArtifactKind != "source" {
				return domain.Application{}, errors.New("unsupported resource type for observation")
			}
			if r.Image != comp.Image || r.Port != comp.Port {
				return domain.Application{}, errors.New("resource configuration changed; refresh discovery")
			}
		}
	}
	return c.Store.CreateApplication(ctx, m)
}

// Enqueue snapshots the manifest and inserts the release and job atomically.
func (c *Controller) Enqueue(ctx context.Context, appID, key string) (domain.Deployment, error) {
	return c.EnqueueImages(ctx, appID, key, nil)
}
func (c *Controller) EnqueueImages(ctx context.Context, appID, key string, images map[string]string) (domain.Deployment, error) {
	return c.EnqueueVersion(ctx, appID, key, images, 0)
}
func (c *Controller) EnqueueVersion(ctx context.Context, appID, key string, images map[string]string, expectedVersion int64) (domain.Deployment, error) {
	return c.enqueueOperation(ctx, appID, key, images, expectedVersion, nil, nil, nil, nil)
}

type RestartRequest struct {
	Component string `json:"component"`
	Ordinal   int    `json:"ordinal"`
}

func (c *Controller) EnqueueRestart(ctx context.Context, appID, key string, request RestartRequest, version int64) (domain.Deployment, error) {
	if request.Component == "" || request.Ordinal < 1 || version < 1 {
		return domain.Deployment{}, errors.New("component, ordinal and expectedVersion are required")
	}
	return c.enqueueOperation(ctx, appID, key, nil, version, &request, nil, nil, nil)
}
func (c *Controller) enqueueOperation(ctx context.Context, appID, key string, images map[string]string, expectedVersion int64, restart *RestartRequest, scaleDown *ScaleDownRequest, provenance *domain.ReleaseSource, rollback *RollbackRequest) (domain.Deployment, error) {
	if len(key) < 8 || len(key) > 128 {
		return domain.Deployment{}, errors.New("Idempotency-Key must contain 8 to 128 characters")
	}
	tx, e := c.Store.Pool.Begin(ctx)
	if e != nil {
		return domain.Deployment{}, e
	}
	defer tx.Rollback(ctx)
	// Serialize enqueue and give duplicate deliveries the original deployment.
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", appID); e != nil {
		return domain.Deployment{}, e
	}
	a, e := c.Store.ApplicationTx(ctx, tx, appID)
	if e != nil {
		return domain.Deployment{}, e
	}
	if expectedVersion < 0 {
		return domain.Deployment{}, errors.New("expectedVersion cannot be negative")
	}
	if len(images) == 0 {
		images = nil
	}
	requestBody, _ := json.Marshal(struct {
		Images    map[string]string     `json:"images"`
		Version   int64                 `json:"expectedVersion"`
		Restart   *RestartRequest       `json:"restart,omitempty"`
		ScaleDown *ScaleDownRequest     `json:"scaleDown,omitempty"`
		Source    *domain.ReleaseSource `json:"source,omitempty"`
		Rollback  *RollbackRequest      `json:"rollback,omitempty"`
	}{images, expectedVersion, restart, scaleDown, provenance, rollback})
	requestSum := sha256.Sum256(requestBody)
	requestHash := hex.EncodeToString(requestSum[:])
	var existingID, oldHash string
	var hashVersion int
	existingErr := tx.QueryRow(ctx, "SELECT id,request_hash,request_hash_version FROM oap_deployments WHERE application_id=$1 AND idempotency_key=$2", appID, key).Scan(&existingID, &oldHash, &hashVersion)
	if existingErr == nil && hashVersion == 2 {
		if oldHash != requestHash {
			return domain.Deployment{}, domain.ErrConflict
		}
		return c.Store.DeploymentTx(ctx, tx, existingID)
	}
	if existingErr != nil && !errors.Is(existingErr, pgx.ErrNoRows) {
		return domain.Deployment{}, existingErr
	}
	if expectedVersion > 0 && a.Version != expectedVersion {
		return domain.Deployment{}, domain.ErrConflict
	}
	if provenance != nil {
		var current string
		e = tx.QueryRow(ctx, "SELECT h.event_id FROM oap_source_heads h JOIN oap_source_events e ON e.hook_id=h.hook_id AND e.application_id=h.application_id WHERE e.id=$1 FOR UPDATE OF h", provenance.EventID).Scan(&current)
		if e != nil || current != provenance.EventID {
			return domain.Deployment{}, domain.ErrConflict
		}
	}
	var rollbackPlan RollbackPlan
	var rollbackSource domain.Deployment
	if rollback != nil {
		rollbackPlan, rollbackSource, e = c.rollbackPlan(ctx, tx, a, rollback.ReleaseID)
		if e != nil {
			return domain.Deployment{}, e
		}
		if rollbackPlan.PlanHash != rollback.PlanHash {
			return domain.Deployment{}, domain.ErrConflict
		}
		images = map[string]string{}
		for _, instance := range rollbackPlan.Instances {
			images[instance.Component] = instance.RestoreImage
		}
	}
	for _, comp := range a.Manifest.Components {
		if comp.Management == "observe" && (provenance == nil || images[comp.Name] != "") && ((restart == nil && scaleDown == nil) || (restart != nil && restart.Component == comp.Name) || (scaleDown != nil && scaleDown.Component == comp.Name)) {
			return domain.Deployment{}, errors.New("observe-only components require an explicit management handoff before lifecycle operations")
		}
	}
	for name, image := range images {
		found := false
		for i := range a.Manifest.Components {
			comp := &a.Manifest.Components[i]
			if comp.Name == name {
				found = true
				if comp.ResourceID != "" && comp.Image != image {
					return domain.Deployment{}, errors.New("adopted resource image changes are not supported")
				}
				if !strings.Contains(image, "@sha256:") && !(rollback != nil && dockerContentID.MatchString(image)) {
					return domain.Deployment{}, errors.New("release image overrides must use an immutable sha256 digest")
				}
				comp.Image = image
			}
		}
		if !found {
			return domain.Deployment{}, errors.New("release image override names an unknown component")
		}
	}
	if e = a.Manifest.Validate(); e != nil {
		return domain.Deployment{}, e
	}
	spec, e := json.Marshal(a.Manifest)
	if e != nil {
		return domain.Deployment{}, e
	}
	sum := sha256.Sum256(spec)
	hash := hex.EncodeToString(sum[:])
	if existingErr == nil {
		if restart != nil || scaleDown != nil || oldHash != hash {
			return domain.Deployment{}, domain.ErrConflict
		}
		return c.Store.DeploymentTx(ctx, tx, existingID)
	}
	var active bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oap_deployments WHERE application_id=$1 AND state IN ('queued','running','attention'))", appID).Scan(&active); e != nil {
		return domain.Deployment{}, e
	}
	if active {
		return domain.Deployment{}, domain.ErrConflict
	}
	d := domain.Deployment{Operation: "deploy", ID: domain.NewID(), ApplicationID: appID, Manifest: a.Manifest, DefinitionVersion: a.Version, State: "queued", Steps: domain.InitialSteps(a.Manifest)}
	d.Source = provenance
	if provenance != nil || rollback != nil {
		selected := []domain.Step{}
		for _, step := range d.Steps {
			if images[step.Component] != "" {
				selected = append(selected, step)
			}
		}
		if len(selected) == 0 {
			return d, errors.New("source release requires at least one selected image")
		}
		d.Steps = selected
		if rollback != nil {
			d.Source = rollbackSource.Source
			for i := range d.Steps {
				for _, instance := range rollbackPlan.Instances {
					if instance.Component == d.Steps[i].Component && instance.Ordinal == d.Steps[i].Ordinal {
						d.Steps[i].RollbackFrom = rollback.ReleaseID
						d.Steps[i].RollbackTargetHash = rollbackPlan.TargetHash
						d.Steps[i].RollbackResourceID = instance.ResourceID
						d.Steps[i].RollbackExpectedImage = instance.CurrentImage
					}
				}
			}
		}
	}
	target, e := c.Store.TargetTx(ctx, tx, a.Manifest.TargetID)
	if e != nil {
		return d, e
	}
	adapter, e := c.Factory(target)
	if e != nil {
		return d, e
	}
	if restart == nil && scaleDown == nil {
		if e = operator.ValidateRuntime(a.Manifest, adapter.Capabilities()); e != nil {
			return d, e
		}
	} else if scaleDown != nil {
		if !adapter.Capabilities().Retirement {
			return d, errors.New("target does not support instance retirement")
		}
		if _, ok := adapter.(operator.Retirer); !ok {
			return d, errors.New("target does not implement retirement")
		}
		found := false
		for i := range d.Manifest.Components {
			comp := &d.Manifest.Components[i]
			if comp.Name != scaleDown.Component {
				continue
			}
			found = true
			if comp.ResourceID != "" || scaleDown.Instances >= comp.Instances {
				return d, errors.New("scale-down requires a managed component and a lower positive instance count")
			}
			d.Steps = []domain.Step{}
			for ordinal := comp.Instances; ordinal > scaleDown.Instances; ordinal-- {
				var ref string
				err := tx.QueryRow(ctx, "SELECT resource_id FROM oap_bindings WHERE application_id=$1 AND component=$2 AND ordinal=$3", appID, comp.Name, ordinal).Scan(&ref)
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				if err != nil {
					return d, err
				}
				d.Steps = append(d.Steps, domain.Step{Component: comp.Name, Ordinal: ordinal, ResourceID: ref, Phase: "pending", Action: "retire"})
			}
			comp.Instances = scaleDown.Instances
			break
		}
		if !found {
			return d, errors.New("unknown component")
		}
		d.Operation = "scale-down"
	} else {
		d.Operation = "restart"
		if !adapter.Capabilities().Restart {
			return d, errors.New("target does not support restart")
		}
		if _, ok := adapter.(operator.Restarter); !ok {
			return d, errors.New("target does not implement restart")
		}
		found := false
		for _, comp := range a.Manifest.Components {
			if comp.Name == restart.Component && restart.Ordinal <= comp.Instances {
				found = true
			}
		}
		if !found {
			return d, errors.New("restart selects an unknown component or instance")
		}
		ref, e := c.Store.BindingTx(ctx, tx, appID, restart.Component, restart.Ordinal)
		if e != nil {
			return d, e
		}
		d.Steps = []domain.Step{{Component: restart.Component, Ordinal: restart.Ordinal, ResourceID: ref, Phase: "pending", Action: "restart"}}
	}
	spec, e = json.Marshal(d.Manifest)
	if e != nil {
		return d, e
	}
	steps, _ := json.Marshal(d.Steps)
	e = tx.QueryRow(ctx, "INSERT INTO oap_deployments(id,application_id,state,spec,steps,idempotency_key,request_hash,definition_version,request_hash_version,operation) VALUES($1,$2,$3,$4,$5,$6,$7,$8,2,$9) RETURNING created_at,updated_at", d.ID, appID, d.State, spec, steps, key, requestHash, a.Version, d.Operation).Scan(&d.CreatedAt, &d.UpdatedAt)
	if e != nil {
		return d, e
	}
	if d.Source != nil {
		raw, _ := json.Marshal(d.Source)
		if _, e = tx.Exec(ctx, "UPDATE oap_deployments SET source=$2 WHERE id=$1", d.ID, raw); e != nil {
			return d, e
		}
	}
	if e = c.recordCredential(ctx, tx, d.ID, appID); e != nil {
		return d, e
	}
	if _, e = c.Jobs.InsertTx(ctx, tx, DeployArgs{d.ID}, &river.InsertOpts{MaxAttempts: 10}); e != nil {
		return d, e
	}
	return d, tx.Commit(ctx)
}
func (w *Worker) NextRetry(job *river.Job[DeployArgs]) time.Time {
	delay := time.Duration(job.Attempt*2) * time.Second
	if delay > 20*time.Second {
		delay = 20 * time.Second
	}
	return time.Now().Add(delay)
}
func (w *Worker) Work(ctx context.Context, job *river.Job[DeployArgs]) error {
	err := w.Controller.Advance(ctx, job.Args.DeploymentID)
	if err != nil && job.Attempt >= job.MaxAttempts {
		var snooze *river.JobSnoozeError
		if !errors.As(err, &snooze) {
			if saveErr := w.Controller.markRetriesExhausted(ctx, job.Args.DeploymentID); saveErr != nil {
				return saveErr
			}
		}
	}
	return err
}
func (c *Controller) Advance(ctx context.Context, id string) error {
	// A session lock fences concurrent workers and is released if the connection dies.
	// Session locks use a dedicated connection so concurrent workers cannot drain
	// the query pool while waiting for their own state reads.
	conn, e := pgx.ConnectConfig(ctx, c.Store.Pool.Config().ConnConfig.Copy())
	if e != nil {
		return errors.New("cannot open deployment lock connection")
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn.Close(closeCtx)
	}()
	var locked bool
	if e = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtext($1))", id).Scan(&locked); e != nil {
		return e
	}
	if !locked {
		return river.JobSnooze(c.PollInterval)
	}
	// Closing the dedicated session releases the advisory lock.
	d, e := c.Store.Deployment(ctx, id)
	if e != nil {
		return e
	}
	if domain.Terminal(d.State) {
		return nil
	}
	a, e := c.Adapter(ctx, d.Manifest.TargetID)
	if e != nil {
		return e
	}
	d.State = "running"
	fail := func(step *domain.Step, state string, err error) error {
		step.RecoveryPhase = step.Phase
		step.Phase = state
		step.Error = err.Error()
		d.State = state
		return c.Store.SaveDeployment(ctx, d)
	}
	for i := range d.Steps {
		step := &d.Steps[i]
		if step.Phase == "succeeded" || step.Phase == "failed" || step.Phase == "attention" {
			continue
		}
		if (step.Action == "retire" && d.Operation != "scale-down") || (step.Action != "" && step.Action != "restart" && step.Action != "retire") {
			return fail(step, "attention", errors.New("unknown operation action"))
		}
		var comp domain.Component
		for _, v := range d.Manifest.Components {
			if v.Name == step.Component {
				comp = v
			}
		}
		if comp.Management == "observe" {
			return fail(step, "attention", errors.New("observe-only component cannot execute lifecycle operations"))
		}
		if e = c.authorizeRelease(ctx, d); e != nil {
			return fail(step, "attention", e)
		}
		if step.RollbackFrom != "" {
			if e = c.verifyRollbackInstance(ctx, a, d, *step, comp); e != nil {
				return fail(step, "attention", e)
			}
		}
		if step.Action != "retire" && (step.Phase == "pending" || step.Phase == "prepared") {
			ready, err := c.dependenciesReady(ctx, a, d, comp)
			if err != nil {
				state := "attention"
				if errors.Is(err, errDependencyFailed) {
					state = "failed"
				}
				return fail(step, state, err)
			}
			if !ready {
				return river.JobSnooze(c.PollInterval)
			}
		}
		switch step.Phase {
		case "pending":
			if step.Action == "restart" || step.Action == "retire" {
				resource, e := a.Inspect(ctx, step.ResourceID)
				if e != nil {
					return fail(step, "failed", e)
				}
				if comp.ResourceID == "" && resource.Description != "OpenAppPlatform:"+d.ApplicationID+":"+comp.Name {
					return fail(step, "attention", errors.New("managed resource ownership changed; inspect provider before this operation"))
				}
			} else if comp.ResourceID != "" {
				resource, inspectErr := a.Inspect(ctx, comp.ResourceID)
				if inspectErr != nil {
					return fail(step, "failed", inspectErr)
				}
				if resource.ArtifactKind != "image" || resource.Image != comp.Image {
					return fail(step, "attention", errors.New("adopted resource artifact configuration changed; inspect provider before deploying"))
				}
				step.ResourceID = comp.ResourceID
			} else {
				runtime, e := domain.RuntimeComponent(d.Manifest, comp.Name)
				if e != nil {
					return fail(step, "failed", e)
				}
				if e = c.authorizeRelease(ctx, d); e != nil {
					return fail(step, "attention", e)
				}
				r, e := a.Ensure(ctx, operator.Spec{Name: domain.ResourceName(d.ApplicationID, comp.Name, step.Ordinal), Ownership: "OpenAppPlatform:" + d.ApplicationID + ":" + comp.Name, Component: runtime, Variables: c.Store.VariableJournal(d.Manifest.TargetID, d.ApplicationID, comp.Name, step.Ordinal)})
				if e != nil {
					return e
				}
				if step.RollbackFrom != "" && r.ID != step.RollbackResourceID {
					return fail(step, "attention", errors.New("rollback preparation returned a different resource"))
				}
				step.ResourceID = r.ID
			}
			if step.Action != "retire" {
				if e = c.Store.Bind(ctx, d.Manifest.TargetID, step.ResourceID, d.ApplicationID, comp.Name, step.Ordinal); e != nil {
					return fail(step, "attention", errors.New("resource is already bound to another component"))
				}
			}
			step.Phase = "prepared"
			if e = c.Store.SaveDeployment(ctx, d); e != nil {
				return e
			}
			return river.JobSnooze(c.PollInterval)
		case "prepared":
			if e = c.authorizeRelease(ctx, d); e != nil {
				return fail(step, "attention", e)
			}
			// Durable dispatch intent prevents automatic reissue after an uncertain response.
			step.Phase = "dispatching"
			if e = c.Store.SaveDeployment(ctx, d); e != nil {
				return e
			}
			var remote string
			if step.Action == "retire" {
				retirer, ok := a.(operator.Retirer)
				if !ok {
					return fail(step, "attention", errors.New("retirement capability unavailable"))
				}
				remote, e = retirer.Retire(ctx, step.ResourceID, retirementOwner(d, *step))
			} else if step.Action == "restart" {
				restarter, ok := a.(operator.Restarter)
				if !ok {
					return fail(step, "attention", errors.New("restart capability is unavailable"))
				}
				remote, e = restarter.Restart(ctx, step.ResourceID)
			} else {
				remote, e = a.Deploy(ctx, step.ResourceID)
			}
			if e != nil {
				return fail(step, "attention", fmt.Errorf("deployment dispatch outcome is uncertain; inspect provider before retrying: %w", e))
			}
			step.RemoteDeploymentID = remote
			step.Phase = "observing"
			now := time.Now().UTC()
			step.ObservationStartedAt = &now
			if e = c.Store.SaveDeployment(ctx, d); e != nil {
				return e
			}
			return river.JobSnooze(c.PollInterval)
		case "dispatching":
			return fail(step, "attention", errors.New("controller interrupted during dispatch; inspect provider before retrying"))
		case "observing":
			started := d.CreatedAt
			if step.ObservationStartedAt != nil {
				started = *step.ObservationStartedAt
			}
			if time.Since(started) > observationTimeout(comp) {
				return fail(step, "attention", fmt.Errorf("deployment observation exceeded %s; provider may still be running or waiting for readiness", observationTimeout(comp)))
			}
			var status operator.DeploymentStatus
			if step.Action == "retire" {
				status, e = retirementStatus(ctx, a, d, *step, step.RemoteDeploymentID)
			} else {
				status, e = a.Observe(ctx, step.RemoteDeploymentID, step.ResourceID)
			}
			if e != nil {
				return e
			}
			step.Observed = status.ResourceStatus
			if status.State == "failed" {
				step.Phase = "failed"
				step.Error = "provider deployment failed"
			} else if status.State == "succeeded" && (step.Action == "retire" || releaseReady(comp, status.ResourceStatus)) {
				step.Phase = "succeeded"
			}
			if step.Action == "retire" && step.Phase == "succeeded" {
				e = c.confirmRetirement(ctx, &d, step)
			} else {
				e = c.Store.SaveDeployment(ctx, d)
			}
			if e != nil {
				return e
			}
			return river.JobSnooze(c.PollInterval)
		default:
			return fail(step, "attention", errors.New("unknown persisted deployment phase"))
		}
	}
	d.State = "succeeded"
	for _, s := range d.Steps {
		if s.Phase == "attention" {
			d.State = "attention"
			break
		}
		if s.Phase == "failed" {
			d.State = "failed"
			break
		}
	}
	if d.Operation == "scale-down" && d.State == "succeeded" {
		return c.finishRetirement(ctx, &d)
	}
	return c.Store.SaveDeployment(ctx, d)
}
func Healthy(status string) bool {
	return status == "running" || status == "running:healthy" || strings.HasPrefix(status, "running:healthy:")
}

func (c *Controller) EnqueueSource(ctx context.Context, app, key string, images map[string]string, version int64, provenance *domain.ReleaseSource) (domain.Deployment, error) {
	return c.enqueueOperation(ctx, app, key, images, version, nil, nil, provenance, nil)
}
