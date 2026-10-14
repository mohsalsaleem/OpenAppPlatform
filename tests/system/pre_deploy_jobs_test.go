package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/cli"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testAgentPreDeployJobs(t *testing.T, ctx context.Context, target domain.Target, adapter *docker.Client, jobImage, webImage string) {
	var err error
	s := testStore(t)
	if err = s.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	c, err := controller.New(ctx, s, func(domain.Target) (operator.Adapter, error) { return adapter, nil })
	if err != nil {
		t.Fatal(err)
	}
	c.RequireIdentity = true
	c.PollInterval = 100 * time.Millisecond
	service := access.Service{Pool: s.Pool}
	owner, err := service.Register(ctx, "jobs-owner@example.invalid", "Owner", "isolated-jobs-owner-password", "")
	if err != nil {
		t.Fatal(err)
	}
	ownerSession, err := service.Issue(ctx, owner, "session", "Owner", "operate", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer((&httpapi.Server{Controller: c}).Handler())
	defer api.Close()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	ownerFile := filepath.Join(directory, "owner.json")
	agentFile := filepath.Join(directory, "agent.json")
	raw, _ := json.Marshal(map[string]any{"schemaVersion": 1, "origin": api.URL, "session": ownerSession})
	if err = os.WriteFile(ownerFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	token := ""
	invoke := func(owner bool, command string, flags []string, body any, want int) cli.Result {
		t.Helper()
		var in, out bytes.Buffer
		if body != nil {
			json.NewEncoder(&in).Encode(body)
		}
		args := []string{"--url", api.URL}
		if owner {
			args = append(args, "--credential-file", ownerFile)
		}
		args = append(args, flags...)
		if body != nil {
			args = append(args, "--file", "-")
		}
		args = append(args, command)
		code := cli.Run(ctx, args, func(key string) string {
			if key == "OAP_API_TOKEN" && !owner {
				return token
			}
			return ""
		}, &in, &out)
		var r cli.Result
		if json.Unmarshal(out.Bytes(), &r) != nil {
			t.Fatal("invalid CLI fixture result")
		}
		if code != want {
			t.Fatalf("jobs %s exit %d expected %d: %s", command, code, want, out.String())
		}
		return r
	}
	invoke(true, "grant", []string{"--out", agentFile}, map[string]any{"name": "Jobs agent", "targetId": target.ID, "environment": "staging", "maxApplications": 1, "actions": []string{"configure", "deploy", "recover", "remove-component", "rollback"}}, 0)
	raw, err = os.ReadFile(agentFile)
	if err != nil {
		t.Fatal(err)
	}
	var issued struct {
		Token string `json:"token"`
	}
	json.Unmarshal(raw, &issued)
	token = issued.Token
	if token == "" {
		t.Fatal("missing agent grant")
	}
	plan := func(req controller.ActionRequest) store.ActionPlan {
		t.Helper()
		r := invoke(false, "action-plan", nil, req, 0)
		raw, _ := json.Marshal(r.Data)
		var p store.ActionPlan
		json.Unmarshal(raw, &p)
		return p
	}
	execute := func(p store.ActionPlan) store.ActionPlan {
		t.Helper()
		r := invoke(false, "execute-action", []string{"--plan", p.ID}, map[string]any{}, 0)
		raw, _ := json.Marshal(r.Data)
		var done store.ActionPlan
		json.Unmarshal(raw, &done)
		return done
	}
	wait := func(p store.ActionPlan, want int) {
		t.Helper()
		invoke(false, "wait", []string{"--operation", p.Result.OperationID, "--timeout", "90s"}, nil, want)
	}

	wrap := func(m domain.Manifest) *domain.ApplicationSpecification {
		return &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: m}
	}

	port := func() int {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := l.Addr().(*net.TCPAddr).Port
		l.Close()
		return p
	}
	workerImage, err := adapter.CachedImageID(ctx, os.Getenv("OAP_TEST_WORKER_IMAGE"))
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := adapter.CachedImageID(ctx, os.Getenv("OAP_TEST_DOCKER_REPLACEMENT_IMAGE"))
	if err != nil {
		t.Fatal(err)
	}
	apiPort := port()
	manifest := domain.Manifest{Name: "job-app", TargetID: target.ID, Environment: "staging", Components: []domain.Component{
		{Name: "api", Kind: "web", Image: webImage, Port: 8080, HostPort: apiPort, Instances: 1, Env: map[string]string{"FIXTURE_VOLUME": "enabled", "FIXTURE_PORT": "8080"}, Volumes: []domain.VolumeMount{{Name: "data", MountPath: "/data"}}, DependsOn: []string{"migrate"}},
		{Name: "migrate", Kind: "job", Image: jobImage, Instances: 1, Readiness: &domain.ReadinessPolicy{TimeoutSeconds: 30}},
		{Name: "frontend", Kind: "web", Image: webImage, Env: map[string]string{"FIXTURE_PORT": "8080"}, Port: 8080, HostPort: port(), Instances: 1},
		{Name: "worker", Kind: "worker", Image: workerImage, Instances: 1},
	}}
	created := execute(plan(controller.ActionRequest{Action: "create", Specification: wrap(manifest)}))
	id := created.Result.ApplicationID
	if err = c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Jobs.Stop(context.Background())
	current := func() domain.Application {
		r := invoke(false, "get", []string{"--app", id}, nil, 0)
		raw, _ := json.Marshal(r.Data)
		var app domain.Application
		json.Unmarshal(raw, &app)
		return app
	}
	operation := func(id string) domain.Deployment {
		r := invoke(false, "operation", []string{"--operation", id}, nil, 0)
		raw, _ := json.Marshal(r.Data)
		var d domain.Deployment
		json.Unmarshal(raw, &d)
		return d
	}
	firstDeploy := true
	deploy := func(want int) domain.Deployment {
		selection := []string{"api"}
		if firstDeploy {
			selection = nil
			firstDeploy = false
		}
		p := plan(controller.ActionRequest{Action: "deploy", ApplicationID: id, ExpectedVersion: current().Version, Components: selection})
		if !bytes.Contains(p.Summary, []byte("preDeployJobs")) || !bytes.Contains(p.Summary, []byte("migrate")) {
			t.Fatal("plan omitted migration effects")
		}
		done := execute(p)
		wait(done, want)
		return operation(done.Result.OperationID)
	}
	first := deploy(0)
	if len(first.Steps) != 4 || first.Steps[0].Component != "migrate" || first.Steps[0].ExitCode == nil || *first.Steps[0].ExitCode != 0 || first.Steps[1].Phase != "succeeded" {
		t.Fatal("ordered migration release", first)
	}
	apiRef := domain.ResourceName(id, "api", 1)
	initial, err := adapter.Inspect(ctx, apiRef)
	if err != nil {
		t.Fatal(err)
	}
	invoke(false, "incident", []string{"--app", id, "--component", "migrate"}, nil, 0)
	invoke(false, "verify", []string{"--app", id}, nil, 0)
	save := func() {
		execute(plan(controller.ActionRequest{Action: "configure", ApplicationID: id, ExpectedVersion: current().Version, Specification: wrap(manifest)}))
	}
	manifest.Components[1].Env = map[string]string{"JOB_EXIT_CODE": "7"}
	save()
	failed := deploy(6)
	if failed.State != "failed" || failed.Steps[0].ExitCode == nil || *failed.Steps[0].ExitCode != 7 || failed.Steps[1].ResourceID != "" {
		t.Fatal("failed job dispatched dependent", failed)
	}
	unchanged, err := adapter.Inspect(ctx, apiRef)
	if err != nil || unchanged.RuntimeID != initial.RuntimeID {
		t.Fatal("failed migration changed existing API")
	}
	manifest.Components[1].Env = nil
	save()
	// Application traffic writes fixture data; this is not a second management interface.
	public := &http.Client{Timeout: 5 * time.Second}
	defer public.CloseIdleConnections()
	volumeURL := fmt.Sprintf("http://127.0.0.1:%d/volume", apiPort)
	response, err := public.Post(volumeURL, "text/plain", bytes.NewBufferString("rollback-retained-data"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal("fixture volume write", response.StatusCode)
	}
	upgraded := execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: id, ExpectedVersion: current().Version, Components: []string{"api", "frontend", "worker"}, Images: map[string]string{"api": replacement, "frontend": replacement}}))
	wait(upgraded, 0)
	beforeRollback := operation(upgraded.Result.OperationID)
	if beforeRollback.Steps[0].ResourceID == first.Steps[0].ResourceID {
		t.Fatal("new release reused migration completion")
	}
	jobRef := beforeRollback.Steps[0].ResourceID
	rollbackRead := invoke(false, "rollback-plan", []string{"--app", id}, map[string]any{"releaseId": first.ID, "expectedVersion": current().Version}, 0)
	raw, _ = json.Marshal(rollbackRead.Data)
	var rollback controller.RollbackPlan
	if json.Unmarshal(raw, &rollback) != nil || len(rollback.Instances) != 3 || len(rollback.SkippedJobs) != 1 {
		t.Fatal("app-wide rollback plan", rollbackRead)
	}
	payload := map[string]any{"releaseId": first.ID, "expectedVersion": current().Version, "planHash": rollback.PlanHash, "acknowledgeDataCompatibility": true}
	admitted := invoke(false, "rollback", []string{"--app", id, "--key", "job-app-rollback"}, payload, 0)
	raw, _ = json.Marshal(admitted.Data)
	var restoring domain.Deployment
	json.Unmarshal(raw, &restoring)
	invoke(false, "wait", []string{"--operation", restoring.ID, "--timeout", "90s"}, nil, 0)
	repeated := invoke(false, "rollback", []string{"--app", id, "--key", "job-app-rollback"}, payload, 0)
	raw, _ = json.Marshal(repeated.Data)
	var replay domain.Deployment
	json.Unmarshal(raw, &replay)
	if replay.ID != restoring.ID {
		t.Fatal("rollback replay duplicated operation")
	}
	restored := operation(restoring.ID)
	for _, step := range restored.Steps {
		if step.Component == "migrate" {
			t.Fatal("rollback reran migration")
		}
	}
	for _, name := range []string{"api", "frontend"} {
		resource, err := adapter.Inspect(ctx, domain.ResourceName(id, name, 1))
		if err != nil || resource.Image != webImage {
			t.Fatal("rollback did not restore app images", name, err)
		}
	}
	response, err = public.Get(volumeURL)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(data) != "rollback-retained-data" {
		t.Fatal("rollback changed volume data")
	}
	jobLogs, err := adapter.Logs(ctx, jobRef, 10)
	if err != nil || bytes.Count([]byte(jobLogs), []byte("job-executed")) != 1 {
		t.Fatal("rollback reran job", err)
	}
	invoke(false, "verify", []string{"--app", id}, nil, 0)
	// Controller-crash fixture: the process completed but dispatch response was
	// lost. All recovery operations below remain named-agent CLI operations.
	if err = c.Jobs.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	held := execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: id, ExpectedVersion: current().Version, Components: []string{"api"}}))
	for i := 0; i < 4; i++ {
		_ = c.Advance(ctx, held.Result.OperationID)
	}
	lost := operation(held.Result.OperationID)
	if lost.Steps[0].Phase != "succeeded" {
		t.Fatal("crash fixture job not completed", lost)
	}
	lost.State = "running"
	lost.Steps[0].Phase = "dispatching"
	lost.Steps[0].RemoteDeploymentID = ""
	lost.Steps[0].ExitCode = nil
	if err = s.SaveDeployment(ctx, lost); err != nil {
		t.Fatal(err)
	}
	_ = c.Advance(ctx, lost.ID)
	attention := operation(lost.ID)
	if attention.State != "attention" {
		t.Fatal("interrupted dispatch retried")
	}
	r := invoke(false, "instances", []string{"--app", id}, nil, 0)
	raw, _ = json.Marshal(r.Data)
	var instances []controller.Instance
	json.Unmarshal(raw, &instances)
	nativeID := ""
	for _, instance := range instances {
		if instance.Component == "migrate" && instance.Resource != nil {
			nativeID = instance.Resource.RuntimeID
		}
	}
	if nativeID == "" {
		t.Fatal("public instance interface cannot recover exact job identity")
	}
	recovered := execute(plan(controller.ActionRequest{Action: "recover", ApplicationID: id, ExpectedVersion: current().Version, DeploymentID: attention.ID, Recovery: &controller.Recovery{Component: "migrate", Ordinal: 1, RemoteDeploymentID: nativeID, ExpectedUpdatedAt: attention.UpdatedAt}}))
	_ = recovered
	for i := 0; i < 100; i++ {
		_ = c.Advance(ctx, lost.ID)
		if domain.Terminal(operation(lost.ID).State) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	result := operation(lost.ID)
	if result.State != "succeeded" || result.Steps[0].ExitCode == nil || *result.Steps[0].ExitCode != 0 {
		t.Fatal("observation-only migration recovery", result)
	}
	logs, err := adapter.Logs(ctx, result.Steps[0].ResourceID, 10)
	if err != nil || bytes.Count([]byte(logs), []byte("job-executed")) != 1 {
		t.Fatal("recovery reran migration", err)
	}
	manifest.Components[1].Env = map[string]string{"JOB_SLEEP_SECONDS": "60"}
	save()
	timed := execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: id, ExpectedVersion: current().Version, Components: []string{"api"}}))
	for i := 0; i < 2; i++ {
		_ = c.Advance(ctx, timed.Result.OperationID)
	}
	expired := operation(timed.Result.OperationID)
	if expired.Steps[0].Phase != "observing" {
		t.Fatal("timeout fixture was not observing")
	}
	// Host fault drill: move only the durable observation clock beyond the
	// configured deadline; do not make the operating agent write control data.
	started := time.Now().Add(-31 * time.Second)
	expired.Steps[0].ObservationStartedAt = &started
	if err = s.SaveDeployment(ctx, expired); err != nil {
		t.Fatal(err)
	}
	_ = c.Advance(ctx, expired.ID)
	heldTimeout := operation(expired.ID)
	if heldTimeout.State != "attention" || heldTimeout.Steps[1].ResourceID != "" || heldTimeout.Steps[0].ExitCode != nil {
		t.Fatal("timeout claimed completion or dispatched dependent", heldTimeout)
	}
	if err = adapter.Stop(ctx, heldTimeout.Steps[0].ResourceID); err != nil {
		t.Fatal("fixture cleanup", err)
	}
	if directory := os.Getenv("OAP_TEST_REPORT_DIR"); directory != "" {
		receipt, _ := json.MarshalIndent(map[string]any{"status": "passed", "interface": "cli", "namedAgent": true, "preDeployJobSuccess": true, "nonzeroExitBlocksRollout": true, "newReleaseRunsNewJob": true, "publicRuntimeIdentity": true, "interruptedDispatchObservedWithoutRedispatch": true, "timeoutHeldWithoutCompletion": true, "appWideWorkloadImageRollback": true, "migrationSkippedOnRollback": true, "volumeDataRetained": true, "rollbackReplay": true, "applicationDataRestore": false}, "", "  ")
		if err := os.WriteFile(filepath.Join(directory, "pre-deploy-jobs.json"), append(receipt, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
