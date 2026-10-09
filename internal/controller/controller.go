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
	Store        *store.Store
	Factory      operator.Factory
	Jobs         *river.Client[pgx.Tx]
	PollInterval time.Duration
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
			if r.ArtifactKind != "image" {
				return domain.Application{}, errors.New("only Docker-image resources can be adopted in this milestone")
			}
			if r.Image != comp.Image {
				return domain.Application{}, errors.New("adoption requires the existing image reference; this milestone does not mutate adopted source or image configuration")
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
	return c.enqueueOperation(ctx, appID, key, images, expectedVersion, nil)
}

type RestartRequest struct {
	Component string `json:"component"`
	Ordinal   int    `json:"ordinal"`
}

func (c *Controller) EnqueueRestart(ctx context.Context, appID, key string, request RestartRequest, version int64) (domain.Deployment, error) {
	if request.Component == "" || request.Ordinal < 1 || version < 1 {
		return domain.Deployment{}, errors.New("component, ordinal and expectedVersion are required")
	}
	return c.enqueueOperation(ctx, appID, key, nil, version, &request)
}
func (c *Controller) enqueueOperation(ctx context.Context, appID, key string, images map[string]string, expectedVersion int64, restart *RestartRequest) (domain.Deployment, error) {
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
		Images  map[string]string `json:"images"`
		Version int64             `json:"expectedVersion"`
		Restart *RestartRequest   `json:"restart,omitempty"`
	}{images, expectedVersion, restart})
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
	for name, image := range images {
		found := false
		for i := range a.Manifest.Components {
			comp := &a.Manifest.Components[i]
			if comp.Name == name {
				found = true
				if comp.ResourceID != "" && comp.Image != image {
					return domain.Deployment{}, errors.New("adopted resource image changes are not supported")
				}
				if !strings.Contains(image, "@sha256:") {
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
		if restart != nil || oldHash != hash {
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
	d := domain.Deployment{ID: domain.NewID(), ApplicationID: appID, Manifest: a.Manifest, DefinitionVersion: a.Version, State: "queued", Steps: domain.InitialSteps(a.Manifest)}
	target, e := c.Store.TargetTx(ctx, tx, a.Manifest.TargetID)
	if e != nil {
		return d, e
	}
	adapter, e := c.Factory(target)
	if e != nil {
		return d, e
	}
	if restart == nil {
		if e = operator.ValidateRuntime(a.Manifest, adapter.Capabilities()); e != nil {
			return d, e
		}
	} else {
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
	steps, _ := json.Marshal(d.Steps)
	e = tx.QueryRow(ctx, "INSERT INTO oap_deployments(id,application_id,state,spec,steps,idempotency_key,request_hash,definition_version,request_hash_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,2) RETURNING created_at,updated_at", d.ID, appID, d.State, spec, steps, key, requestHash, a.Version).Scan(&d.CreatedAt, &d.UpdatedAt)
	if e != nil {
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
			d, loadErr := w.Controller.Store.Deployment(ctx, job.Args.DeploymentID)
			if loadErr == nil && !domain.Terminal(d.State) {
				d.State = "attention"
				for i := range d.Steps {
					if d.Steps[i].Phase != "succeeded" && d.Steps[i].Phase != "failed" {
						d.Steps[i].RecoveryPhase = d.Steps[i].Phase
						d.Steps[i].Phase = "attention"
						d.Steps[i].Error = "Adapter operation retries exhausted; inspect provider before retrying"
						break
					}
				}
				if saveErr := w.Controller.Store.SaveDeployment(ctx, d); saveErr != nil {
					return saveErr
				}
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
		if step.Action != "" && step.Action != "restart" {
			return fail(step, "attention", errors.New("unknown operation action"))
		}
		var comp domain.Component
		for _, v := range d.Manifest.Components {
			if v.Name == step.Component {
				comp = v
			}
		}
		switch step.Phase {
		case "pending":
			if step.Action == "restart" {
				resource, e := a.Inspect(ctx, step.ResourceID)
				if e != nil {
					return fail(step, "failed", e)
				}
				if comp.ResourceID == "" && resource.Description != "OpenAppPlatform:"+d.ApplicationID+":"+comp.Name {
					return fail(step, "attention", errors.New("managed resource ownership changed; inspect provider before restarting"))
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
				r, e := a.Ensure(ctx, operator.Spec{Name: domain.ResourceName(d.ApplicationID, comp.Name, step.Ordinal), Ownership: "OpenAppPlatform:" + d.ApplicationID + ":" + comp.Name, Component: runtime})
				if e != nil {
					return e
				}
				step.ResourceID = r.ID
			}
			if e = c.Store.Bind(ctx, d.Manifest.TargetID, step.ResourceID, d.ApplicationID, comp.Name, step.Ordinal); e != nil {
				return fail(step, "attention", errors.New("resource is already bound to another component"))
			}
			step.Phase = "prepared"
			if e = c.Store.SaveDeployment(ctx, d); e != nil {
				return e
			}
			return river.JobSnooze(c.PollInterval)
		case "prepared":
			// Durable dispatch intent prevents automatic reissue after an uncertain response.
			step.Phase = "dispatching"
			if e = c.Store.SaveDeployment(ctx, d); e != nil {
				return e
			}
			var remote string
			if step.Action == "restart" {
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
			if time.Since(started) > 15*time.Minute {
				return fail(step, "attention", errors.New("deployment observation exceeded 15 minutes; provider may still be running"))
			}
			status, e := a.Observe(ctx, step.RemoteDeploymentID, step.ResourceID)
			if e != nil {
				return e
			}
			step.Observed = status.ResourceStatus
			if status.State == "failed" {
				step.Phase = "failed"
				step.Error = "provider deployment failed"
			} else if status.State == "succeeded" && Healthy(status.ResourceStatus) {
				step.Phase = "succeeded"
			}
			if e = c.Store.SaveDeployment(ctx, d); e != nil {
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
	return c.Store.SaveDeployment(ctx, d)
}
func Healthy(status string) bool {
	return status == "running" || status == "running:healthy" || strings.HasPrefix(status, "running:healthy:")
}
