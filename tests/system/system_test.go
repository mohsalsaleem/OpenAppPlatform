package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/coolify"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
)

const platformToken = "platform-system-test-access-token"

type fake struct {
	mu        sync.Mutex
	resources map[string]operator.Resource
	deploys   int
	fail      bool
	failedID  string
}

func (f *fake) Capabilities() operator.Capabilities {
	return operator.Capabilities{Standard: true, Discovery: true}
}
func (f *fake) Discover(context.Context) ([]operator.Resource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []operator.Resource{}
	for _, r := range f.resources {
		out = append(out, r)
	}
	return out, nil
}
func (f *fake) Ensure(_ context.Context, s operator.Spec) (operator.Resource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.resources {
		if r.Name == s.Name {
			return r, nil
		}
	}
	r := operator.Resource{ID: domain.NewID(), Name: s.Name, Description: s.Ownership, Image: s.Component.Image, ArtifactKind: "image"}
	f.resources[r.ID] = r
	if s.Component.Name == "api" {
		f.failedID = r.ID
	}
	return r, nil
}
func (f *fake) Inspect(_ context.Context, id string) (operator.Resource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.resources[id]
	if !ok {
		return r, domain.ErrNotFound
	}
	return r, nil
}
func (f *fake) Deploy(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deploys++
	return fmt.Sprintf("remote-%d", f.deploys), nil
}
func (f *fake) Observe(_ context.Context, _ string, resourceID string) (operator.DeploymentStatus, error) {
	if f.fail && resourceID == f.failedID {
		return operator.DeploymentStatus{State: "failed", ResourceStatus: "exited"}, nil
	}
	return operator.DeploymentStatus{State: "succeeded", ResourceStatus: "running:healthy"}, nil
}
func (f *fake) Logs(context.Context, string, int) (string, error) { return "hello from fixture", nil }
func testStore(t *testing.T) *store.Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL system tests")
	}
	ctx := context.Background()
	base, e := pgxpool.New(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	schema := "oap_test_" + domain.NewID()
	if _, e = base.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	s, e := store.Open(ctx, url+sep+"search_path="+schema+"&pool_max_conns=4")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Pool.Close(); base.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); base.Close() })
	return s
}
func setup(t *testing.T, f operator.Adapter) (*controller.Controller, *httptest.Server) {
	t.Helper()
	s := testStore(t)
	ctx := context.Background()
	if e := s.SaveTarget(ctx, domain.Target{ID: "fixture", Name: "Fixture", Operator: "coolify", Environment: "staging"}); e != nil {
		t.Fatal(e)
	}
	c, e := controller.New(ctx, s, func(domain.Target) (operator.Adapter, error) { return f, nil })
	if e != nil {
		t.Fatal(e)
	}
	c.PollInterval = 20 * time.Millisecond
	srv := httptest.NewServer((&httpapi.Server{Controller: c, Token: platformToken}).Handler())
	t.Cleanup(srv.Close)
	return c, srv
}
func request(t *testing.T, srv *httptest.Server, method, path, key string, body any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	r, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+platformToken)
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	res, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	var raw json.RawMessage
	if e = json.NewDecoder(res.Body).Decode(&raw); e != nil {
		t.Fatal(e)
	}
	return res.StatusCode, raw
}
func manifest() domain.Manifest {
	return domain.Manifest{Name: "system-fixture", Environment: "staging", TargetID: "fixture", Components: []domain.Component{{Name: "web", Image: "nginx:1.27-alpine", Port: 80, Instances: 2, Strategy: "standard"}}}
}
func TestApplicationLifecycleThroughHTTPAndDurableQueue(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, srv := setup(t, f)
	ctx := context.Background()
	res, e := http.Get(srv.URL + "/api/v1/applications")
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("API must require authentication")
	}
	status, raw := request(t, srv, "POST", "/api/v1/applications", "", manifest())
	if status != 201 {
		t.Fatalf("create: %d %s", status, raw)
	}
	var app domain.Application
	json.Unmarshal(raw, &app)
	status, raw = request(t, srv, "POST", "/api/v1/applications/"+app.ID+"/deployments", "duplicate-delivery", nil)
	if status != 202 {
		t.Fatalf("deploy: %d %s", status, raw)
	}
	var d domain.Deployment
	json.Unmarshal(raw, &d)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dup, e := c.Enqueue(ctx, app.ID, "duplicate-delivery")
			if e != nil || dup.ID != d.ID {
				t.Errorf("dedup failed %v", e)
			}
		}()
	}
	wg.Wait()
	status, _ = request(t, srv, "POST", "/api/v1/applications/"+app.ID+"/deployments", "another-release", nil)
	if status != 409 {
		t.Fatal("overlapping deployment accepted")
	}
	// Reconstruct the controller before starting the persisted job: process restart does not lose it.
	c2, e := controller.New(ctx, c.Store, c.Factory)
	if e != nil {
		t.Fatal(e)
	}
	c2.PollInterval = 20 * time.Millisecond
	if e = c2.Jobs.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer c2.Jobs.Stop(ctx)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		observed, e := c.Store.Deployment(ctx, d.ID)
		if e != nil {
			t.Fatal(e)
		}
		if domain.Terminal(observed.State) {
			d = observed
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if d.State != "succeeded" || len(d.Steps) != 2 {
		t.Fatalf("release did not finish: %+v", d)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deploys != 2 || len(f.resources) != 2 {
		t.Fatalf("duplicate operations: %d %d", f.deploys, len(f.resources))
	}
}
func TestInterruptedDispatchDoesNotRedeploy(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, _ := setup(t, f)
	ctx := context.Background()
	m := manifest()
	m.Components[0].Instances = 1
	a, e := c.CreateApplication(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	d, e := c.Enqueue(ctx, a.ID, "interrupted-dispatch")
	if e != nil {
		t.Fatal(e)
	}
	d.Steps[0].Phase = "dispatching"
	d.State = "running"
	if e = c.Store.SaveDeployment(ctx, d); e != nil {
		t.Fatal(e)
	}
	if e = c.Advance(ctx, d.ID); e != nil {
		t.Fatal(e)
	}
	d, _ = c.Store.Deployment(ctx, d.ID)
	if d.State != "attention" || f.deploys != 0 {
		t.Fatal("uncertain dispatch must require attention")
	}
	if _, err := c.Enqueue(ctx, a.ID, "new-after-uncertain"); err != domain.ErrConflict {
		t.Fatal("unresolved attention must block a new dispatch")
	}

}
func TestPartialFailureRetainsComponentOutcomes(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}, fail: true}
	c, _ := setup(t, f)
	ctx := context.Background()
	m := manifest()
	m.Components[0].Instances = 1
	m.Components = append(m.Components, domain.Component{Name: "api", Kind: "web", Image: "nginx:1.27-alpine", Port: 80, Instances: 1, Strategy: "standard"})
	a, e := c.CreateApplication(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	d, e := c.Enqueue(ctx, a.ID, "failure-release")
	if e != nil {
		t.Fatal(e)
	}
	for range 12 {
		_ = c.Advance(ctx, d.ID)
	}
	d, _ = c.Store.Deployment(ctx, d.ID)
	if d.State != "failed" || d.Steps[0].Phase != "succeeded" || d.Steps[1].Phase != "failed" {
		t.Fatalf("failure lost: %+v", d)
	}
}
func TestLiveCoolifyLifecycle(t *testing.T) {
	if os.Getenv("OAP_LIVE_COOLIFY") != "1" {
		t.Skip("set OAP_LIVE_COOLIFY=1 for opt-in staging deployment")
	}
	target := domain.Target{ID: "fixture", Name: "Coolify staging", Operator: "coolify", URL: os.Getenv("COOLIFY_URL"), ProjectID: os.Getenv("COOLIFY_PROJECT_ID"), ServerID: os.Getenv("COOLIFY_SERVER_ID"), Environment: os.Getenv("COOLIFY_ENVIRONMENT")}
	if target.Environment != "staging" || target.ProjectID == "" {
		t.Fatal("live tests require an explicit staging project")
	}
	a, e := coolify.New(target, os.Getenv("COOLIFY_TOKEN"))
	if e != nil {
		t.Fatal(e)
	}
	c, srv := setup(t, a)
	if e = c.Store.SaveTarget(context.Background(), target); e != nil {
		t.Fatal(e)
	}
	m := manifest()
	m.Name = "live-" + domain.NewID()[:8]
	m.Components[0].Instances = 1
	m.Components[0].Image = os.Getenv("COOLIFY_TEST_IMAGE")
	m.Components[0].ResourceID = os.Getenv("COOLIFY_TEST_RESOURCE_ID")
	if port, err := strconv.Atoi(os.Getenv("COOLIFY_TEST_PORT")); err == nil {
		m.Components[0].Port = port
	}
	if m.Components[0].Image == "" {
		t.Fatal("COOLIFY_TEST_IMAGE is required; use an immutable image digest")
	}
	status, raw := request(t, srv, "POST", "/api/v1/applications", "", m)
	if status != 201 {
		t.Fatalf("create: %d %s", status, raw)
	}
	var app domain.Application
	json.Unmarshal(raw, &app)
	status, raw = request(t, srv, "POST", "/api/v1/applications/"+app.ID+"/deployments", "live-first-release", nil)
	if status != 202 {
		t.Fatalf("deploy: %d %s", status, raw)
	}
	var d domain.Deployment
	json.Unmarshal(raw, &d)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if e = c.Jobs.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer c.Jobs.Stop(context.Background())
	for ctx.Err() == nil {
		d, e = c.Store.Deployment(ctx, d.ID)
		if e != nil {
			t.Fatal(e)
		}
		if domain.Terminal(d.State) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("retained staging fixture resource=%s deployment=%s", d.Steps[0].ResourceID, d.Steps[0].RemoteDeploymentID)
	if d.State != "succeeded" {
		t.Fatalf("real Coolify release: %+v", d)
	}
	r, e := a.Inspect(ctx, d.Steps[0].ResourceID)
	if e != nil {
		t.Fatal(e)
	}
	if !controller.Healthy(r.Status) {
		t.Fatalf("not running: %s", r.Status)
	}
	if r.Image != m.Components[0].Image {
		t.Fatalf("image identity differs: %s", r.Image)
	}
	if r.URL == "" {
		t.Fatal("fixture needs a reachable URL for HTTP verification")
	}
	httpDeadline := time.Now().Add(90 * time.Second)
	var lastHTTPError error
	verified := false
	for time.Now().Before(httpDeadline) && ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, "GET", r.URL, nil)
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				verified = true
				break
			}
			lastHTTPError = fmt.Errorf("HTTP %d", resp.StatusCode)
		} else {
			lastHTTPError = err
		}
		time.Sleep(2 * time.Second)
	}
	if !verified {
		t.Fatalf("fixture HTTPS readiness failed: %v", lastHTTPError)
	}

	t.Logf("verified fixture URL=%s image digest matched", r.URL)

	status, raw = request(t, srv, "GET", "/api/v1/applications/"+app.ID+"/logs/web", "", nil)
	if status != 200 {
		t.Fatalf("logs: %d %s", status, raw)
	}
	// Fixture retention avoids destructive cleanup. Its ID is recorded for manual inspection.
	restart, e := c.EnqueueRestart(ctx, app.ID, "live-fixture-restart", controller.RestartRequest{Component: "web", Ordinal: 1}, app.Version)
	if e != nil {
		t.Fatal(e)
	}
	restartDeadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(restartDeadline) {
		restart, e = c.Store.Deployment(ctx, restart.ID)
		if e != nil {
			t.Fatal(e)
		}
		if domain.Terminal(restart.State) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if restart.State != "succeeded" {
		t.Fatalf("Coolify restart failed %+v", restart)
	}
	t.Logf("verified retained staging restart deployment=%s", restart.Steps[0].RemoteDeploymentID)

}

func TestAdoptionCannotBindResourceToTwoApplications(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{"existing": {ID: "existing", Image: "nginx:1.27-alpine", ArtifactKind: "image"}}}
	c, _ := setup(t, f)
	m := manifest()
	m.Components[0].Instances = 1
	m.Components[0].ResourceID = "existing"
	if _, e := c.CreateApplication(context.Background(), m); e != nil {
		t.Fatal(e)
	}
	m.Name = "another-owner"
	if _, e := c.CreateApplication(context.Background(), m); e != domain.ErrConflict {
		t.Fatalf("duplicate adoption accepted: %v", e)
	}
}

func TestReleaseSnapshotsDigestAndRejectsIdempotencyPayloadMismatch(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, _ := setup(t, f)
	ctx := context.Background()
	m := manifest()
	app, e := c.CreateApplication(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	digest := "nginx@sha256:" + strings.Repeat("a", 64)
	d, e := c.EnqueueImages(ctx, app.ID, "artifact-release", map[string]string{"web": digest})
	if e != nil {
		t.Fatal(e)
	}
	if d.Manifest.Components[0].Image != digest {
		t.Fatal("release did not freeze artifact")
	}
	saved, e := c.Store.Application(ctx, app.ID)
	if e != nil || saved.Manifest.Components[0].Image != m.Components[0].Image {
		t.Fatal("release changed base manifest")
	}
	if _, e = c.EnqueueImages(ctx, app.ID, "artifact-release", map[string]string{"web": "nginx@sha256:" + strings.Repeat("b", 64)}); e != domain.ErrConflict {
		t.Fatalf("key reuse accepted: %v", e)
	}
}

func TestExampleManifestsValidate(t *testing.T) {
	for _, name := range []string{"hello-web", "two-components", "docker-hello", "docker-connected"} {
		t.Run(name, func(t *testing.T) {
			b, e := os.ReadFile("../../examples/" + name + "/application.json")
			if e != nil {
				t.Fatal(e)
			}
			var m domain.Manifest
			if e = json.Unmarshal(b, &m); e != nil {
				t.Fatal(e)
			}
			if e = m.Validate(); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestConcurrentApplicationsProgressWithSmallConnectionPool(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, _ := setup(t, f)
	ctx := context.Background()
	ids := []string{}
	for n := range 4 {
		m := manifest()
		m.Name = fmt.Sprintf("parallel-%d", n)
		m.Components[0].Instances = 1
		a, e := c.CreateApplication(ctx, m)
		if e != nil {
			t.Fatal(e)
		}
		d, e := c.Enqueue(ctx, a.ID, "parallel-release")
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, d.ID)
	}
	if e := c.Jobs.Start(ctx); e != nil {
		t.Fatal(e)
	}
	defer c.Jobs.Stop(ctx)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		done := true
		for _, id := range ids {
			d, e := c.Store.Deployment(ctx, id)
			if e != nil {
				t.Fatal(e)
			}
			if d.State != "succeeded" {
				done = false
			}
		}
		if done {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("parallel releases did not progress with a four-connection pool")
}
