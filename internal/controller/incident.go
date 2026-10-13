package controller

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
)

const incidentInstanceLimit = 32
const incidentFreshness = 15 * time.Second

type IncidentObservation struct {
	Component  string    `json:"component"`
	Ordinal    int       `json:"ordinal"`
	Status     string    `json:"status"`
	Retired    bool      `json:"retired"`
	ResourceID string    `json:"resourceId,omitempty"`
	Image      string    `json:"image,omitempty"`
	CheckedAt  time.Time `json:"checkedAt"`
	ErrorCode  string    `json:"errorCode,omitempty"`
}
type IncidentStep struct {
	Component         string `json:"component"`
	Ordinal           int    `json:"ordinal"`
	Phase             string `json:"phase"`
	RecoveryPhase     string `json:"recoveryPhase,omitempty"`
	RemoteOperationID string `json:"remoteOperationId,omitempty"`
	ErrorCode         string `json:"errorCode,omitempty"`
}
type IncidentOperation struct {
	ID                string         `json:"id"`
	State             string         `json:"state"`
	Action            string         `json:"action"`
	DefinitionVersion int64          `json:"definitionVersion"`
	CreatedAt         time.Time      `json:"createdAt"`
	UpdatedAt         time.Time      `json:"updatedAt"`
	Steps             []IncidentStep `json:"steps"`
}
type IncidentEvent struct {
	OperationID string    `json:"operationId"`
	State       string    `json:"state"`
	Action      string    `json:"action"`
	Commit      string    `json:"commit,omitempty"`
	RecordedAt  time.Time `json:"recordedAt"`
}
type IncidentLog struct {
	Component     string     `json:"component,omitempty"`
	Ordinal       int        `json:"ordinal,omitempty"`
	Status        string     `json:"status"`
	Text          string     `json:"text,omitempty"`
	SampledAt     *time.Time `json:"sampledAt,omitempty"`
	Truncated     bool       `json:"truncated"`
	MissingReason string     `json:"missingReason,omitempty"`
}
type IncidentNextAction struct {
	Action  string         `json:"action"`
	Allowed bool           `json:"allowed"`
	Reason  string         `json:"reason"`
	Request *ActionRequest `json:"request,omitempty"`
}
type VerificationCriterion struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type Incident struct {
	SchemaVersion               int                       `json:"schemaVersion"`
	ApplicationID               string                    `json:"applicationId"`
	DefinitionVersion           int64                     `json:"definitionVersion"`
	TargetID                    string                    `json:"targetId"`
	Environment                 string                    `json:"environment"`
	GeneratedAt                 time.Time                 `json:"generatedAt"`
	ValidUntil                  time.Time                 `json:"validUntil"`
	SnapshotConsistent          bool                      `json:"snapshotConsistent"`
	UntrustedEvidence           bool                      `json:"untrustedEvidence"`
	State                       string                    `json:"state"`
	Limits                      map[string]int            `json:"limits"`
	Observations                []IncidentObservation     `json:"observations"`
	Operations                  []IncidentOperation       `json:"operations"`
	Timeline                    []IncidentEvent           `json:"timeline"`
	Comparison                  *domain.ReleaseComparison `json:"comparison,omitempty"`
	Logs                        IncidentLog               `json:"logs"`
	NextActions                 []IncidentNextAction      `json:"nextActions"`
	Verification                []VerificationCriterion   `json:"verification"`
	ApplicationBehaviorVerified bool                      `json:"applicationBehaviorVerified"`
	MissingEvidence             []string                  `json:"missingEvidence"`
}

func (c *Controller) incidentReleases(ctx context.Context, appID string) ([]domain.Deployment, error) {
	rows, err := c.Store.Pool.Query(ctx, "SELECT id FROM oap_deployments WHERE application_id=$1 ORDER BY created_at DESC,id DESC LIMIT 2", appID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []domain.Deployment{}
	for _, id := range ids {
		d, e := c.Store.Deployment(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, nil
}

// Incident is a bounded read model, never a repair executor or SQL/shell tool.
// Sampling clocks are evidence times, not a promise of continued health.
func (c *Controller) Incident(ctx context.Context, appID, component string, ordinal int) (Incident, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	app, err := c.Store.Application(ctx, appID)
	if err != nil {
		return Incident{}, err
	}
	principal, identified := domain.Identity(ctx)
	if c.RequireIdentity && (!identified || !principal.Reads(app.ID, app.Manifest.TargetID, app.Manifest.Environment)) {
		return Incident{}, access.ErrDenied
	}
	if ordinal < 0 || ordinal > 4 || (component == "" && ordinal != 0) {
		return Incident{}, errors.New("select a valid component and ordinal")
	}
	if component != "" {
		found := false
		for _, comp := range app.Manifest.Components {
			if comp.Name == component && (ordinal == 0 || ordinal <= comp.Instances) {
				found = true
			}
		}
		if !found {
			return Incident{}, domain.ErrNotFound
		}
		if ordinal == 0 {
			ordinal = 1
		}
	}
	bindings, err := c.Store.Bindings(ctx, appID)
	if err != nil {
		return Incident{}, err
	}
	target, err := c.Store.Target(ctx, app.Manifest.TargetID)
	if err != nil {
		return Incident{}, err
	}
	releases, err := c.incidentReleases(ctx, appID)
	if err != nil {
		return Incident{}, err
	}
	out := Incident{SchemaVersion: 1, ApplicationID: app.ID, DefinitionVersion: app.Version, TargetID: app.Manifest.TargetID, Environment: app.Manifest.Environment, SnapshotConsistent: true, UntrustedEvidence: true, State: "inconclusive", Limits: map[string]int{"instances": 32, "releases": 2, "events": 20, "logLines": 50, "logBytes": 16384, "historyForRedaction": 20, "secretPins": 128, "requestSeconds": 8, "freshnessSeconds": 15}, Observations: []IncidentObservation{}, Operations: []IncidentOperation{}, Timeline: []IncidentEvent{}, NextActions: []IncidentNextAction{}, MissingEvidence: []string{"application behavior", "repository patches", "metrics and indexed logs", "application-data backup safety"}, Logs: IncidentLog{Status: "not_requested"}}
	for _, d := range releases {
		op := IncidentOperation{ID: d.ID, State: d.State, Action: releaseAuthorizationAction(d), DefinitionVersion: d.DefinitionVersion, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, Steps: []IncidentStep{}}
		for _, step := range d.Steps {
			code := ""
			if step.Error != "" {
				code = "operation_step_failed_or_held"
			}
			op.Steps = append(op.Steps, IncidentStep{Component: step.Component, Ordinal: step.Ordinal, Phase: step.Phase, RecoveryPhase: step.RecoveryPhase, RemoteOperationID: step.RemoteDeploymentID, ErrorCode: code})
		}
		out.Operations = append(out.Operations, op)
	}
	if len(releases) > 0 {
		before := releases[0]
		if len(releases) > 1 {
			before = releases[1]
		}
		comparison := domain.CompareReleases(before, releases[0])
		out.Comparison = &comparison
	} else {
		out.MissingEvidence = append(out.MissingEvidence, "recorded release")
	}
	rows, err := c.Store.Pool.Query(ctx, "SELECT deployment_id,state,operation,commit_sha,created_at FROM oap_notification_outbox WHERE application_id=$1 ORDER BY created_at DESC LIMIT 20", appID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var event IncidentEvent
		if err = rows.Scan(&event.OperationID, &event.State, &event.Action, &event.Commit, &event.RecordedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Timeline = append(out.Timeline, event)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	runtimeCtx, runtimeCancel := context.WithTimeout(ctx, 3*time.Second)
	instances, truncated, runtimeErr := c.instancesBounded(runtimeCtx, appID, incidentInstanceLimit)
	runtimeCancel()
	for _, instance := range instances {
		item := IncidentObservation{Component: instance.Component, Ordinal: instance.Ordinal, Status: instance.Status, Retired: instance.Retired, ResourceID: instance.ResourceID, CheckedAt: instance.CheckedAt}
		if instance.Resource != nil {
			item.Image = instance.Resource.Image
		}
		if instance.Error != "" {
			item.ErrorCode = "runtime_observation_unavailable_or_drifted"
		}
		out.Observations = append(out.Observations, item)
	}
	if truncated || runtimeErr != nil {
		out.MissingEvidence = append(out.MissingEvidence, "complete current runtime observations")
	}
	if component != "" {
		logCtx, logCancel := context.WithTimeout(ctx, 2*time.Second)
		out.Logs = c.incidentLogs(logCtx, app, bindings, component, ordinal)
		logCancel()
		if out.Logs.Status != "sampled" {
			out.MissingEvidence = append(out.MissingEvidence, "selected redacted logs")
		}
	}
	// Detect changes across the sampling interval. Plans still revalidate at admission.
	latest, checkErr := c.Store.Application(ctx, appID)
	endBindings, bindingErr := c.Store.Bindings(ctx, appID)
	endTarget, targetErr := c.Store.Target(ctx, app.Manifest.TargetID)
	endReleases, releaseErr := c.incidentReleases(ctx, appID)
	if checkErr != nil || bindingErr != nil || targetErr != nil || releaseErr != nil || latest.Version != app.Version || store.HashJSON(endBindings) != store.HashJSON(bindings) || store.HashJSON(endTarget) != store.HashJSON(target) || store.HashJSON(releaseClocks(endReleases)) != store.HashJSON(releaseClocks(releases)) {
		out.SnapshotConsistent = false
		out.MissingEvidence = append(out.MissingEvidence, "consistent snapshot; refresh incident")
	}
	if !out.SnapshotConsistent && component != "" {
		out.Logs = IncidentLog{Component: component, Ordinal: ordinal, Status: "unavailable", MissingReason: "snapshot_changed_or_unavailable"}
		out.MissingEvidence = append(out.MissingEvidence, "selected redacted logs")
	}
	out.GeneratedAt = time.Now().UTC()
	out.ValidUntil = out.GeneratedAt.Add(incidentFreshness)
	for _, item := range out.Observations {
		until := item.CheckedAt.Add(incidentFreshness)
		if until.Before(out.ValidUntil) {
			out.ValidUntil = until
		}
	}
	out.State, out.Verification = incidentVerification(out, truncated || runtimeErr != nil)
	owned := false
	for _, component := range app.Manifest.Components {
		if component.Management != "observe" && component.Kind != "database" {
			owned = true
		}
	}
	adapter, adapterErr := c.Adapter(ctx, app.Manifest.TargetID)
	for _, action := range []string{"configure", "deploy", "restart"} {
		supported := owned && adapterErr == nil
		if supported && action == "deploy" {
			supported = adapter.Capabilities().Standard
		}
		if supported && action == "restart" {
			supported = adapter.Capabilities().Restart
		}
		out.NextActions = append(out.NextActions, IncidentNextAction{Action: action, Allowed: supported && out.SnapshotConsistent && out.ValidUntil.After(out.GeneratedAt) && principal.Allows(app.ID, action, app.Manifest.TargetID, app.Manifest.Environment), Reason: "Use a fresh exact action plan; admission, ownership and runtime capability checks remain required"})
	}

	if len(releases) > 0 && releases[0].State == "attention" {
		d := releases[0]
		allowed := out.SnapshotConsistent && out.ValidUntil.After(out.GeneratedAt) && principal.Allows(appID, "recover", app.Manifest.TargetID, app.Manifest.Environment) && principal.Allows(appID, releaseAuthorizationAction(d), app.Manifest.TargetID, app.Manifest.Environment)
		for _, step := range d.Steps {
			if step.Phase != "attention" || step.Action == "retire" || step.RollbackFrom != "" {
				continue
			}
			recovery := Recovery{Component: step.Component, Ordinal: step.Ordinal, ExpectedUpdatedAt: d.UpdatedAt}
			reason := "Inspect the exact recorded provider operation; never redispatch an uncertain operation"
			if step.RemoteDeploymentID != "" {
				recovery.RemoteDeploymentID = step.RemoteDeploymentID
			} else if step.RecoveryPhase == "pending" || step.RecoveryPhase == "prepared" {
				recovery.RetryPreparation = true
				reason = "Recorded pre-dispatch phase; retry only after correcting its cause"
			} else {
				out.NextActions = append(out.NextActions, IncidentNextAction{Action: "inspect_operator", Allowed: false, Reason: reason})
				continue
			}
			req := ActionRequest{Action: "recover", ApplicationID: appID, ExpectedVersion: app.Version, DeploymentID: d.ID, Recovery: &recovery}
			out.NextActions = append(out.NextActions, IncidentNextAction{Action: "recover", Allowed: allowed, Reason: reason, Request: &req})
		}
	}
	return out, nil
}
func releaseClocks(releases []domain.Deployment) any {
	clocks := []string{}
	for _, d := range releases {
		clocks = append(clocks, d.ID+":"+d.UpdatedAt.UTC().Format(time.RFC3339Nano))
	}
	return clocks
}
func incidentVerification(out Incident, incomplete bool) (string, []VerificationCriterion) {
	state := "observed_healthy"
	runtime := "passed"
	active := 0
	for _, item := range out.Observations {
		if item.Retired {
			if item.Status == "retired:running" {
				state = "issue"
				runtime = "failed"
			}
			continue
		}
		active++
		switch item.Status {
		case "running", "healthy", "running:healthy":
		case "unavailable", "":
			if state != "issue" {
				state = "inconclusive"
			}
			if runtime != "failed" {
				runtime = "missing"
			}
		default:
			state = "issue"
			runtime = "failed"
		}
	}
	if active == 0 || incomplete {
		if state != "issue" {
			state = "inconclusive"
		}
		if runtime != "failed" {
			runtime = "missing"
		}
	}
	snapshot := "passed"
	if !out.SnapshotConsistent || !out.ValidUntil.After(out.GeneratedAt) {
		snapshot = "missing"
		state = "inconclusive"
	}
	release := "missing"
	if len(out.Operations) > 0 {
		switch out.Operations[0].State {
		case "succeeded":
			release = "passed"
		case "failed", "attention", "cancelled", "abandoned":
			release = "failed"
			if snapshot == "passed" {
				state = "issue"
			}
		default:
			release = "pending"
			if state == "observed_healthy" {
				state = "inconclusive"
			}
		}
	} else if state == "observed_healthy" {
		state = "inconclusive"
	}
	return state, []VerificationCriterion{{ID: "current_runtime_health", Status: runtime, Detail: "Current bounded instance observations; requested images and queue admission are not health evidence"}, {ID: "fresh_consistent_snapshot", Status: snapshot, Detail: "Refresh after validUntil or any definition/operation change"}, {ID: "latest_operation", Status: release, Detail: "Recorded outcome is separate from current observations"}, {ID: "application_behavior", Status: "missing", Detail: "External agent must verify declared HTTP/worker behavior; OAP does not fetch arbitrary URLs or query application databases"}}
}
func (c *Controller) incidentLogs(ctx context.Context, app domain.Application, bindings []store.Binding, component string, ordinal int) IncidentLog {
	out := IncidentLog{Component: component, Ordinal: ordinal, Status: "unavailable"}
	for _, comp := range app.Manifest.Components {
		if comp.Name == component && (comp.Management == "observe" || comp.Kind == "database" || comp.ResourceID != "") {
			out.MissingReason = "native_log_redaction_not_owned"
			return out
		}
	}
	resource := ""
	for _, b := range bindings {
		if b.Component == component && b.Ordinal == ordinal && !b.Retired {
			resource = b.ResourceID
		}
	}
	if resource == "" {
		out.MissingReason = "no_current_binding"
		return out
	}
	// Bound historical literal redaction. Omit logs if complete coverage cannot be established.
	rows, err := c.Store.Pool.Query(ctx, "SELECT spec FROM oap_deployments WHERE application_id=$1 ORDER BY created_at DESC LIMIT 21", app.ID)
	if err != nil {
		out.MissingReason = "redaction_unavailable"
		return out
	}
	manifests := []domain.Manifest{app.Manifest}
	count := 0
	for rows.Next() {
		count++
		var m domain.Manifest
		if err = rows.Scan(&m); err != nil {
			break
		}
		manifests = append(manifests, m)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil || count > 20 {
		out.MissingReason = "historical_redaction_coverage_unavailable"
		return out
	}
	adapter, err := c.Adapter(ctx, app.Manifest.TargetID)
	if err != nil {
		out.MissingReason = "target_unavailable"
		return out
	}
	logs, err := adapter.Logs(ctx, resource, 50)
	if err != nil {
		out.MissingReason = "provider_logs_unavailable"
		return out
	}
	logs, err = c.redactRuntimeLogs(ctx, app.ID, logs, 128)
	if err != nil {
		out.MissingReason = "historical_secret_binding_unavailable"
		return out
	}
	values := []string{}
	for _, m := range manifests {
		for _, comp := range m.Components {
			for _, value := range comp.Env {
				if value != "" {
					values = append(values, value)
				}
			}
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, value := range values {
		logs = strings.ReplaceAll(logs, value, "[REDACTED]")
	}
	lines := strings.Split(logs, "\n")
	if len(lines) > 50 {
		logs = strings.Join(lines[len(lines)-50:], "\n")
		out.Truncated = true
	}
	if len(logs) > 16384 {
		logs = logs[:16384]
		out.Truncated = true
	}
	logs = strings.ToValidUTF8(logs, "")
	at := time.Now().UTC()
	out.SampledAt = &at
	out.Text = logs
	out.Status = "sampled"
	return out
}
