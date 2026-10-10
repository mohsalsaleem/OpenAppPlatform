package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/source"
	"github.com/riverqueue/river"
)

type SourceArgs struct {
	ID string `json:"id"`
}

func (SourceArgs) Kind() string { return "oap_source" }

type SourceWorker struct {
	river.WorkerDefaults[SourceArgs]
	Controller *Controller
}

func (w *SourceWorker) Timeout(*river.Job[SourceArgs]) time.Duration { return 12 * time.Minute }
func (w *SourceWorker) Work(ctx context.Context, j *river.Job[SourceArgs]) error {
	return w.Controller.AdvanceSource(ctx, j.Args.ID)
}

type SourceBuild struct {
	Provider *operator.NativeSourceDeployment `json:"provider,omitempty"`
	ID       string                           `json:"id"`
	State    string                           `json:"state"`
	Image    string                           `json:"image,omitempty"`
}
type SourceEvent struct {
	ID            string                 `json:"id"`
	HookID        string                 `json:"hookId"`
	DeliveryID    string                 `json:"deliveryId"`
	ApplicationID string                 `json:"applicationId"`
	CredentialID  string                 `json:"-"`
	Version       int64                  `json:"definitionVersion"`
	Hook          source.Hook            `json:"binding"`
	Commit        string                 `json:"commit"`
	State         string                 `json:"state"`
	Builds        map[string]SourceBuild `json:"builds"`
	DeploymentID  string                 `json:"deploymentId,omitempty"`
	Error         string                 `json:"error,omitempty"`
	UpdatedAt     time.Time              `json:"updatedAt"`
}

const sourceQuery = "SELECT id,hook_id,delivery_id,application_id,credential_id,definition_version,binding,commit_sha,state,builds,COALESCE(deployment_id,''),error,updated_at FROM oap_source_events WHERE id=$1"

func (c *Controller) SourceEvent(ctx context.Context, id string) (SourceEvent, error) {
	return scanSource(c.Store.Pool.QueryRow(ctx, sourceQuery, id))
}
func scanSource(row pgx.Row) (SourceEvent, error) {
	var v SourceEvent
	var binding, builds []byte
	e := row.Scan(&v.ID, &v.HookID, &v.DeliveryID, &v.ApplicationID, &v.CredentialID, &v.Version, &binding, &v.Commit, &v.State, &builds, &v.DeploymentID, &v.Error, &v.UpdatedAt)
	if e != nil {
		return v, domain.ErrNotFound
	}
	if e = json.Unmarshal(binding, &v.Hook); e != nil {
		return v, e
	}
	e = json.Unmarshal(builds, &v.Builds)
	return v, e
}
func (c *Controller) ReceiveSource(ctx context.Context, h source.Hook, delivery string, body []byte, push source.Push) (SourceEvent, error) {
	p, ok := domain.Identity(ctx)
	if !ok || (h.Mode != "coolify-github-app" && !p.Operates(h.ApplicationID)) {
		return SourceEvent{}, access.ErrDenied
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	tx, e := c.Store.Pool.Begin(ctx)
	if e != nil {
		return SourceEvent{}, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))", "source-hook:"+h.ID); e != nil {
		return SourceEvent{}, e
	}
	var existing, oldHash string
	e = tx.QueryRow(ctx, "SELECT id,payload_hash FROM oap_source_events WHERE hook_id=$1 AND delivery_id=$2", h.ID, delivery).Scan(&existing, &oldHash)
	if e == nil {
		if hash != oldHash {
			return SourceEvent{}, domain.ErrConflict
		}
		tx.Rollback(ctx)
		return c.SourceEvent(ctx, existing)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return SourceEvent{}, e
	}
	app, e := c.Store.ApplicationTx(ctx, tx, h.ApplicationID)
	if e != nil {
		return SourceEvent{}, e
	}
	mapped := map[string]bool{}
	for _, comp := range app.Manifest.Components {
		if (h.Mode == "coolify-github-app" && comp.ResourceID != "" && comp.Management == "observe") || (h.Mode != "coolify-github-app" && comp.ResourceID == "" && comp.Management == "") {
			mapped[comp.Name] = true
		}
	}
	builds := map[string]SourceBuild{}
	for _, comp := range h.Components {
		if !mapped[comp.Name] {
			return SourceEvent{}, errors.New("source mappings require an OAP-managed image component")
		}
		builds[comp.Name] = SourceBuild{ID: domain.NewID(), State: "pending"}
	}
	binding, _ := json.Marshal(h)
	encoded, _ := json.Marshal(builds)
	id := domain.NewID()
	if _, e = tx.Exec(ctx, "INSERT INTO oap_source_events(id,hook_id,delivery_id,payload_hash,application_id,credential_id,definition_version,binding,commit_sha,state,builds) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'received',$10)", id, h.ID, delivery, hash, h.ApplicationID, p.CredentialID, app.Version, binding, push.After, encoded); e != nil {
		return SourceEvent{}, e
	}
	if h.Mode != "coolify-github-app" {
		var head string
		lookup := tx.QueryRow(ctx, "SELECT commit_sha FROM oap_source_heads WHERE hook_id=$1 AND application_id=$2 FOR UPDATE", h.ID, h.ApplicationID).Scan(&head)
		if lookup != nil && !errors.Is(lookup, pgx.ErrNoRows) {
			return SourceEvent{}, lookup
		}
		if lookup == nil && push.Before != head {
			if _, e = tx.Exec(ctx, "UPDATE oap_source_events SET state='attention',error='Push is out of order; review the branch head before promoting this commit' WHERE id=$1", id); e != nil {
				return SourceEvent{}, e
			}
		} else {
			if _, e = tx.Exec(ctx, "INSERT INTO oap_source_heads(hook_id,application_id,event_id,commit_sha) VALUES($1,$2,$3,$4) ON CONFLICT(hook_id,application_id) DO UPDATE SET event_id=$3,commit_sha=$4", h.ID, h.ApplicationID, id, push.After); e != nil {
				return SourceEvent{}, e
			}
		}
	}
	if _, e = c.Jobs.InsertTx(ctx, tx, SourceArgs{id}, &river.InsertOpts{MaxAttempts: 3}); e != nil {
		return SourceEvent{}, e
	}
	if _, e = tx.Exec(ctx, "INSERT INTO oap_audit(id,user_id,credential_id,action,status,completed_at) VALUES($1,$2,$3,$4,202,now())", domain.NewID(), p.UserID, p.CredentialID, "github.push "+h.Repository+"@"+push.After); e != nil {
		return SourceEvent{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return SourceEvent{}, e
	}
	return c.SourceEvent(ctx, id)
}
func (c *Controller) saveSource(ctx context.Context, v SourceEvent) error {
	b, _ := json.Marshal(v.Builds)
	_, e := c.Store.Pool.Exec(ctx, "UPDATE oap_source_events SET state=$2,builds=$3,error=$4,deployment_id=NULLIF($5,''),updated_at=now() WHERE id=$1", v.ID, v.State, b, v.Error, v.DeploymentID)
	return e
}
func (c *Controller) AdvanceSource(ctx context.Context, id string) error {
	// Hold one dedicated session across the builder invocation; duplicates never run it twice.
	conn, e := pgx.ConnectConfig(ctx, c.Store.Pool.Config().ConnConfig.Copy())
	if e != nil {
		return e
	}
	defer conn.Close(context.Background())
	var locked bool
	if e = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtext($1))", "source:"+id).Scan(&locked); e != nil {
		return e
	}
	if !locked {
		return river.JobSnooze(time.Second)
	}
	v, e := c.SourceEvent(ctx, id)
	if e != nil {
		return e
	}
	if v.State == "released" || v.State == "observed" || v.State == "attention" {
		return nil
	}
	hold := func(message string) error { v.State = "attention"; v.Error = message; return c.saveSource(ctx, v) }
	p, e := (access.Service{Pool: c.Store.Pool}).ByID(ctx, v.CredentialID)
	if e != nil || (v.Hook.Mode != "coolify-github-app" && !p.Operates(v.ApplicationID)) || (p.ApplicationID != "" && p.ApplicationID != v.ApplicationID) {
		return hold("Source credential expired or was revoked; explicit owner recovery is required")
	}
	if v.Hook.Mode == "coolify-github-app" {
		return c.observeNativeSource(ctx, v)
	}
	var currentEvent string
	if e = c.Store.Pool.QueryRow(ctx, "SELECT event_id FROM oap_source_heads WHERE hook_id=$1 AND application_id=$2", v.HookID, v.ApplicationID).Scan(&currentEvent); e != nil {
		return e
	}
	if currentEvent != v.ID {
		return hold("A newer accepted push superseded this image release; owner review is required")
	}

	for _, comp := range v.Hook.Components {
		build := v.Builds[comp.Name]
		if build.State == "built" {
			continue
		}
		if build.State == "building" {
			return hold("Builder interrupted with an uncertain outcome; inspect its build ID before retrying")
		}
		if c.SourceBuilder == nil {
			return hold("No trusted image builder is configured")
		}
		build.State = "building"
		v.Builds[comp.Name] = build
		v.State = "building"
		if e = c.saveSource(ctx, v); e != nil {
			return e
		}
		result, e := c.SourceBuilder.Build(ctx, source.BuildRequest{BuildID: build.ID, Repository: v.Hook.Repository, RepositoryID: v.Hook.RepositoryID, Commit: v.Commit, Component: comp})
		if e != nil {
			return hold("Image builder did not return a verified result; inspect build " + build.ID)
		}
		if !source.ValidResult(source.BuildRequest{Commit: v.Commit, Component: comp}, result) {
			return hold("Builder result does not match the requested commit and image repository")
		}
		build.State = "built"
		build.Image = result.Image
		v.Builds[comp.Name] = build
		v.State = "received"
		if e = c.saveSource(ctx, v); e != nil {
			return e
		}
		return river.JobSnooze(time.Second)
	}
	images := map[string]string{}
	for name, b := range v.Builds {
		images[name] = b.Image
	}
	v.State = "built"
	if e = c.saveSource(ctx, v); e != nil {
		return e
	}
	d, e := c.EnqueueSource(domain.WithPrincipal(ctx, p), v.ApplicationID, "github:"+v.ID, images, v.Version, &domain.ReleaseSource{EventID: v.ID, Repository: v.Hook.Repository, Commit: v.Commit, Ref: "refs/heads/" + v.Hook.Branch, DeliveryID: v.DeliveryID})
	if e != nil {
		if errors.Is(e, domain.ErrConflict) {
			var active bool
			if queryErr := c.Store.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM oap_deployments WHERE application_id=$1 AND state IN ('queued','running'))", v.ApplicationID).Scan(&active); queryErr != nil {
				return queryErr
			}
			current, loadErr := c.Store.Application(ctx, v.ApplicationID)
			if active && loadErr == nil && current.Version == v.Version {
				return river.JobSnooze(2 * time.Second)
			}
		}
		return hold("Release could not be queued; review definition changes and active releases before retrying")
	}
	v.State = "released"
	v.DeploymentID = d.ID
	v.Error = ""
	return c.saveSource(ctx, v)
}

// RecoverSource never automatically retries an uncertain build. An owner must
// explicitly confirm retryBuild after inspecting its recorded build ID.
func (c *Controller) RecoverSource(ctx context.Context, id string, updated time.Time, version int64, retryBuild bool, promoteCommit bool) (SourceEvent, error) {
	p, ok := domain.Identity(ctx)
	if !ok || p.Kind != "session" || p.Role != "owner" {
		return SourceEvent{}, access.ErrDenied
	}
	tx, e := c.Store.Pool.Begin(ctx)
	if e != nil {
		return SourceEvent{}, e
	}
	defer tx.Rollback(ctx)
	var locked bool
	if e = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtext($1))", "source:"+id).Scan(&locked); e != nil {
		return SourceEvent{}, e
	}
	if !locked {
		return SourceEvent{}, domain.ErrConflict
	}
	v, e := scanSource(tx.QueryRow(ctx, sourceQuery, id))
	if e != nil {
		return v, e
	}
	if v.State != "attention" || !v.UpdatedAt.Equal(updated) {
		return v, domain.ErrConflict
	}
	app, e := c.Store.ApplicationTx(ctx, tx, v.ApplicationID)
	if e != nil {
		return v, e
	}
	if version < 1 || app.Version != version {
		return v, domain.ErrConflict
	}
	// Restore a lost enqueue acknowledgement without rebasing an already-frozen release.
	var linked string
	lookup := tx.QueryRow(ctx, "SELECT id FROM oap_deployments WHERE application_id=$1 AND idempotency_key=$2 AND source->>'commit'=$3 AND source->>'repository'=$4 AND source->>'deliveryId'=$5", v.ApplicationID, "github:"+v.ID, v.Commit, v.Hook.Repository, v.DeliveryID).Scan(&linked)
	if lookup == nil {
		if _, e = tx.Exec(ctx, "UPDATE oap_source_events SET state='released',deployment_id=$2,error='',updated_at=now() WHERE id=$1", id, linked); e != nil {
			return v, e
		}
		if e = tx.Commit(ctx); e != nil {
			return v, e
		}
		return c.SourceEvent(ctx, id)
	}
	if !errors.Is(lookup, pgx.ErrNoRows) {
		return v, lookup
	}
	if v.Hook.Mode != "coolify-github-app" {
		var current string
		lookup := tx.QueryRow(ctx, "SELECT event_id FROM oap_source_heads WHERE hook_id=$1 AND application_id=$2 FOR UPDATE", v.HookID, v.ApplicationID).Scan(&current)
		if lookup != nil && !errors.Is(lookup, pgx.ErrNoRows) {
			return v, lookup
		}
		if current != v.ID {
			if !promoteCommit {
				return v, errors.New("review the repository branch head and explicitly approveSourceCommit")
			}
			if _, e = tx.Exec(ctx, "INSERT INTO oap_source_heads(hook_id,application_id,event_id,commit_sha) VALUES($1,$2,$3,$4) ON CONFLICT(hook_id,application_id) DO UPDATE SET event_id=$3,commit_sha=$4", v.HookID, v.ApplicationID, v.ID, v.Commit); e != nil {
				return v, e
			}
		}
	}
	for name, build := range v.Builds {
		if build.State == "building" {
			if !retryBuild {
				return v, errors.New("inspect the uncertain build and explicitly confirm retryBuild")
			}
			build.State = "pending"
			v.Builds[name] = build
		}
	}
	builds, _ := json.Marshal(v.Builds)
	if _, e = tx.Exec(ctx, "UPDATE oap_source_events SET state='received',builds=$2,definition_version=$3,credential_id=$4,error='',observation_started_at=now(),updated_at=now() WHERE id=$1", id, builds, version, p.CredentialID); e != nil {
		return v, e
	}
	if _, e = c.Jobs.InsertTx(ctx, tx, SourceArgs{id}, &river.InsertOpts{MaxAttempts: 3}); e != nil {
		return v, e
	}
	if e = tx.Commit(ctx); e != nil {
		return v, e
	}
	return c.SourceEvent(ctx, id)
}

func (c *Controller) observeNativeSource(ctx context.Context, v SourceEvent) error {
	app, e := c.Store.Application(ctx, v.ApplicationID)
	if e != nil {
		return e
	}
	adapter, e := c.Adapter(ctx, app.Manifest.TargetID)
	if e != nil {
		return e
	}
	observer, ok := adapter.(operator.NativeSourceObserver)
	if !ok {
		v.State = "attention"
		v.Error = "Target does not support native GitHub App observation"
		return c.saveSource(ctx, v)
	}
	var created, started time.Time
	if e = c.Store.Pool.QueryRow(ctx, "SELECT created_at,observation_started_at FROM oap_source_events WHERE id=$1", v.ID).Scan(&created, &started); e != nil {
		return e
	}
	if time.Since(started) > 15*time.Minute {
		v.State = "attention"
		v.Error = "No completed matching provider deployment was observed before the deadline"
		return c.saveSource(ctx, v)
	}
	complete := true
	for _, mapping := range v.Hook.Components {
		ref := ""
		for _, component := range app.Manifest.Components {
			if component.Name == mapping.Name && component.Management == "observe" {
				ref = component.ResourceID
			}
		}
		if ref == "" {
			v.State = "attention"
			v.Error = "Observed component identity changed"
			return c.saveSource(ctx, v)
		}
		result, e := observer.FindSourceDeployment(ctx, ref, v.Hook.Repository, v.Hook.Branch, v.Commit, created)
		if e != nil {
			v.State = "attention"
			v.Error = "Native source contract could not be verified; inspect repository, branch, and provider history"
			return c.saveSource(ctx, v)
		}
		build := v.Builds[mapping.Name]
		build.Provider = &result
		build.State = result.State
		v.Builds[mapping.Name] = build
		if result.State == "failed" {
			v.State = "attention"
			v.Error = "The native GitHub App deployment failed"
			return c.saveSource(ctx, v)
		}
		if result.State != "succeeded" || !Healthy(result.ResourceStatus) {
			complete = false
		}
	}
	v.State = "observing"
	if complete {
		v.State = "observed"
	}
	if e = c.saveSource(ctx, v); e != nil {
		return e
	}
	if !complete {
		return river.JobSnooze(5 * time.Second)
	}
	return nil
}
