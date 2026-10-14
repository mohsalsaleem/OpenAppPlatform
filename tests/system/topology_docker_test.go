package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/cli"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
)

func TestAgentAddsRemovesAndRestoresStandaloneComponents(t *testing.T) {
	socket, image, workerImage := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_DOCKER_IMAGE"), os.Getenv("OAP_TEST_WORKER_IMAGE")
	if socket == "" || image == "" || workerImage == "" {
		t.Skip("isolated Docker topology fixtures required")
	}
	if socket != "/run/oap-test/docker.sock" {
		t.Fatal("isolated Engine required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	engine := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	defer engine.CloseIdleConnections()
	native := func(path string) map[string]any {
		t.Helper()
		r, err := engine.Get("http://docker/v1.45" + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatal("native fixture observation", r.StatusCode)
		}
		v := map[string]any{}
		if json.NewDecoder(r.Body).Decode(&v) != nil {
			t.Fatal("native fixture response")
		}
		return v
	}
	pinned := native("/images/" + image + "/json")["Id"].(string)
	workerPinned := native("/images/" + workerImage + "/json")["Id"].(string)
	port := func() int {
		t.Helper()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		p := l.Addr().(*net.TCPAddr).Port
		l.Close()
		return p
	}
	target := domain.Target{ID: "topology-" + domain.NewID()[:8], Name: "Topology fixture", Operator: "docker", Environment: "staging", URL: "unix://" + socket, Settings: json.RawMessage(`{"pullPolicy":"never","hostBindIP":"127.0.0.1"}`)}
	adapter, err := docker.New(target)
	if err != nil {
		t.Fatal(err)
	}
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
	owner, err := service.Register(ctx, "topology-owner@example.invalid", "Owner", "isolated-topology-owner-password", "")
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
		if json.Unmarshal(out.Bytes(), &r) != nil || code != want {
			t.Fatalf("topology %s exit %d expected %d: %s", command, code, want, out.String())
		}
		return r
	}
	invoke(true, "grant", []string{"--out", agentFile}, map[string]any{"name": "Topology agent", "targetId": target.ID, "environment": "staging", "maxApplications": 1, "actions": []string{"configure", "deploy", "recover", "remove-component"}}, 0)
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
	wait := func(p store.ActionPlan) {
		t.Helper()
		invoke(false, "wait", []string{"--operation", p.Result.OperationID, "--timeout", "90s"}, nil, 0)
	}
	spec := domain.Manifest{Name: "topology", TargetID: target.ID, Environment: "staging", Components: []domain.Component{{Name: "base", Kind: "web", Image: pinned, Port: 8080, HostPort: port(), Instances: 1, Env: map[string]string{"OAP_TEST_MESSAGE": "base"}, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 60}}}}
	wrap := func(m domain.Manifest) *domain.ApplicationSpecification {
		return &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: m}
	}
	created := execute(plan(controller.ActionRequest{Action: "create", Specification: wrap(spec)}))
	id := created.Result.ApplicationID
	t.Cleanup(func() {
		bindings, _ := s.Bindings(context.Background(), id)
		for _, b := range bindings {
			adapter.Stop(context.Background(), b.ResourceID)
		}
	})
	if err = c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Jobs.Stop(context.Background())
	current := func() domain.Application {
		t.Helper()
		r := invoke(false, "get", []string{"--app", id}, nil, 0)
		raw, _ := json.Marshal(r.Data)
		var a domain.Application
		if json.Unmarshal(raw, &a) != nil || a.Version < 1 {
			t.Fatal("application observation")
		}
		return a
	}
	deploy := func(names []string) {
		t.Helper()
		a := current()
		wait(execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: id, ExpectedVersion: a.Version, Components: names})))
	}
	save := func(m domain.Manifest) {
		t.Helper()
		a := current()
		execute(plan(controller.ActionRequest{Action: "configure", ApplicationID: id, ExpectedVersion: a.Version, Specification: wrap(m)}))
	}
	deploy(nil)
	baseRef := domain.ResourceName(id, "base", 1)
	baseBoot := native("/containers/" + baseRef + "/json")["State"].(map[string]any)["StartedAt"]
	extra := domain.Component{Name: "extra", Kind: "web", Image: pinned, Port: 8080, HostPort: port(), Instances: 1, Env: map[string]string{"FIXTURE_VOLUME": "enabled"}, Volumes: []domain.VolumeMount{{Name: "data", MountPath: "/data"}}, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 60}}
	worker := domain.Component{Name: "worker", Kind: "worker", Image: workerPinned, Instances: 1, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 60}}
	spec.Components = append(append([]domain.Component(nil), spec.Components...), extra, worker)
	save(spec)
	bindings, _ := s.Bindings(ctx, id)
	if len(bindings) != 1 {
		t.Fatal("save deployed added components")
	}
	deploy([]string{"extra", "worker"})
	if native("/containers/" + baseRef + "/json")["State"].(map[string]any)["StartedAt"] != baseBoot {
		t.Fatal("addition restarted existing component")
	}
	public := &http.Client{Timeout: 5 * time.Second}
	defer public.CloseIdleConnections()
	dataURL := fmt.Sprintf("http://127.0.0.1:%d/volume", extra.HostPort)
	response, err := public.Post(dataURL, "text/plain", bytes.NewBufferString("retained-topology-marker"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal("volume fixture write")
	}
	extraRef := domain.ResourceName(id, "extra", 1)
	mounted := native("/containers/" + extraRef + "/json")["Mounts"].([]any)
	volume := mounted[0].(map[string]any)["Name"].(string)
	desired := spec
	desired.Components = []domain.Component{spec.Components[0], worker}
	invoke(false, "update", []string{"--app", id}, map[string]any{"manifest": desired, "expectedVersion": current().Version}, 4)
	removal := plan(controller.ActionRequest{Action: "remove-component", ApplicationID: id, ExpectedVersion: current().Version, Specification: wrap(desired)})
	if !removal.RequiresReview {
		t.Fatal("component removal omitted required review")
	}
	invoke(false, "execute-action", []string{"--plan", removal.ID}, map[string]any{}, 4)
	invoke(false, "approve-action", []string{"--plan", removal.ID}, map[string]any{"planHash": removal.Hash}, 3)
	invoke(true, "approve-action", []string{"--plan", removal.ID}, map[string]any{"planHash": removal.Hash}, 0)
	retired := execute(removal)
	wait(retired)
	if native("/containers/" + extraRef + "/json")["State"].(map[string]any)["Running"] != false {
		t.Fatal("removed instance still running")
	}
	native("/volumes/" + volume)
	if len(current().Manifest.Components) != 2 {
		t.Fatal("retirement did not commit desired topology")
	}
	if execute(removal).Result.OperationID != retired.Result.OperationID {
		t.Fatal("removal replay created another operation")
	}
	// Re-add the same owned component through the source spec. The retained volume
	// and reserved binding remain under the original application ownership.
	spec = desired
	spec.Components = append(append([]domain.Component(nil), spec.Components...), extra)
	save(spec)
	deploy([]string{"extra"})
	response, err = public.Get(dataURL)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || string(data) != "retained-topology-marker" {
		t.Fatal("topology re-add lost retained data")
	}
	desired = spec
	desired.Components = []domain.Component{spec.Components[0], extra}
	stale := plan(controller.ActionRequest{Action: "remove-component", ApplicationID: id, ExpectedVersion: current().Version, Specification: wrap(desired)})
	save(spec)
	invoke(true, "approve-action", []string{"--plan", stale.ID}, map[string]any{"planHash": stale.Hash}, 4)
	removal = plan(controller.ActionRequest{Action: "remove-component", ApplicationID: id, ExpectedVersion: current().Version, Specification: wrap(desired)})
	invoke(true, "approve-action", []string{"--plan", removal.ID}, map[string]any{"planHash": removal.Hash}, 0)
	wait(execute(removal))
	if native("/containers/" + domain.ResourceName(id, "worker", 1) + "/json")["State"].(map[string]any)["Running"] != false {
		t.Fatal("removed worker still running")
	}
	if native("/containers/" + baseRef + "/json")["State"].(map[string]any)["StartedAt"] != baseBoot {
		t.Fatal("topology retirement restarted sibling")
	}
	if dir := os.Getenv("OAP_TEST_REPORT_DIR"); dir != "" {
		raw, _ := json.MarshalIndent(map[string]any{"status": "passed", "interface": "owner grant and named-agent CLI", "savedAdditionDoesNotDeploy": true, "webAndWorkerAddition": true, "mandatoryOwnerReview": true, "selfApprovalDenied": true, "staleApprovalDenied": true, "removalReplay": true, "volumeRetainedAcrossReadd": true, "siblingNotRestarted": true}, "", "  ")
		os.WriteFile(filepath.Join(dir, "topology.json"), append(raw, '\n'), 0600)
	}
}
