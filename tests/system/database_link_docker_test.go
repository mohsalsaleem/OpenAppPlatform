package system_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/cli"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/mcp"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/runtimeinputs"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
	"github.com/riverqueue/river"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The database is created by test setup, before OAP sees it. OAP only links it.
func TestNativeDockerFourComponentApplicationWithExistingDatabase(t *testing.T) {
	socket, image, postgres := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_APPLICATION_IMAGE"), os.Getenv("OAP_TEST_DATABASE_IMAGE")
	if socket == "" || image == "" || postgres == "" {
		t.Skip("isolated database/application fixture required")
	}
	ctx := context.Background()
	engine := http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	call := func(method, path string, body any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, "http://docker/v1.45"+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		r, e := engine.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		if r.StatusCode < 200 || r.StatusCode >= 300 {
			t.Fatalf("fixture engine %s returned %d", path, r.StatusCode)
		}
		v := map[string]any{}
		if r.StatusCode != 204 {
			json.NewDecoder(r.Body).Decode(&v)
		}
		return v
	}
	adminPassword := "isolated-admin-fixture-only"
	readerPassword := "isolated-reader-fixture-only"
	pgListener, pgListenErr := net.Listen("tcp", "127.0.0.1:0")
	if pgListenErr != nil {
		t.Fatal(pgListenErr)
	}
	fixedPgPort := strconv.Itoa(pgListener.Addr().(*net.TCPAddr).Port)
	pgListener.Close()
	created := call("POST", "/containers/create?name=existing-db-"+domain.NewID()[:8], map[string]any{"Image": postgres, "Env": []string{"POSTGRES_USER=fixture_admin", "POSTGRES_PASSWORD=" + adminPassword, "POSTGRES_DB=oap_fixture"}, "ExposedPorts": map[string]any{"5432/tcp": map[string]any{}}, "Healthcheck": map[string]any{"Test": []string{"CMD-SHELL", "pg_isready -U fixture_admin -d oap_fixture"}, "Interval": int64(time.Second), "Timeout": int64(2 * time.Second), "Retries": 20}, "HostConfig": map[string]any{"PortBindings": map[string]any{"5432/tcp": []map[string]string{{"HostIp": "0.0.0.0", "HostPort": fixedPgPort}}}}})
	dbID := created["Id"].(string)
	defer call("DELETE", "/containers/"+dbID+"?force=true&v=true", nil)
	call("POST", "/containers/"+dbID+"/start", nil)
	waitDB := func() {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			v := call("GET", "/containers/"+dbID+"/json", nil)
			state := v["State"].(map[string]any)
			if h, ok := state["Health"].(map[string]any); ok && h["Status"] == "healthy" {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("fixture database health timed out")
	}
	waitDB()
	info := call("GET", "/containers/"+dbID+"/json", nil)
	ports := info["NetworkSettings"].(map[string]any)["Ports"].(map[string]any)["5432/tcp"].([]any)
	port := ports[0].(map[string]any)["HostPort"].(string)
	adminURL := "postgres://fixture_admin:" + adminPassword + "@127.0.0.1:" + port + "/oap_fixture?sslmode=disable"
	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatal("fixture admin unavailable")
	}
	defer admin.Close(ctx)
	if _, err = admin.Exec(ctx, "CREATE TABLE example_marker(value text); INSERT INTO example_marker VALUES('existing-data-unchanged'); REVOKE CREATE ON SCHEMA public FROM PUBLIC; CREATE ROLE fixture_reader LOGIN PASSWORD '"+readerPassword+"'; GRANT CONNECT ON DATABASE oap_fixture TO fixture_reader; GRANT USAGE ON SCHEMA public TO fixture_reader; GRANT SELECT ON example_marker TO fixture_reader; ALTER ROLE fixture_reader SET default_transaction_read_only=on"); err != nil {
		t.Fatal("fixture setup failed")
	}
	fingerprint := func() [32]byte {
		v := call("GET", "/containers/"+dbID+"/json", nil)
		networks := map[string]any{}
		for name, value := range v["NetworkSettings"].(map[string]any)["Networks"].(map[string]any) {
			n := value.(map[string]any)
			networks[name] = map[string]any{"networkId": n["NetworkID"], "aliases": n["Aliases"], "ipamConfig": n["IPAMConfig"]}
		}
		raw, _ := json.Marshal(map[string]any{"config": v["Config"], "hostConfig": v["HostConfig"], "mounts": v["Mounts"], "networks": networks})
		return sha256.Sum256(raw)
	}
	before := fingerprint()
	network := call("GET", "/networks/bridge", nil)
	configs := network["IPAM"].(map[string]any)["Config"].([]any)
	gateway := configs[0].(map[string]any)["Gateway"].(string)
	endpoint := "postgresql://" + net.JoinHostPort(gateway, port) + "/oap_fixture"
	dsn := "postgres://fixture_reader:" + readerPassword + "@" + net.JoinHostPort(gateway, port) + "/oap_fixture?sslmode=disable"
	availablePort := func() int {
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		p := l.Addr().(*net.TCPAddr).Port
		l.Close()
		return p
	}
	target := domain.Target{ID: "database-docker", Name: "Existing Database Docker", Operator: "docker", URL: "unix://" + socket, Environment: "staging"}
	target.Settings, _ = json.Marshal(map[string]any{"pullPolicy": "never", "hostBindIP": "127.0.0.1", "observeContainers": []string{dbID}})
	adapter, err := docker.New(target)
	if err != nil {
		t.Fatal(err)
	}
	c, srv := setup(t, adapter)
	if err = c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	m := domain.Manifest{Name: "breadth-" + domain.NewID()[:8], Environment: "staging", TargetID: target.ID, Components: []domain.Component{
		{Name: "frontend", Kind: "web", Image: image, Port: 8080, HostPort: availablePort(), Instances: 1, Env: map[string]string{"MODE": "frontend"}, Services: map[string]string{"UPSTREAM_URL": "api"}, DependsOn: []string{"api"}, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 120}},
		{Name: "api", Kind: "web", Image: image, Port: 8080, HostPort: availablePort(), Instances: 1, Env: map[string]string{"MODE": "api"}, SecretRefs: map[string]string{"DATABASE_URL": "database-v1"}, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 120}},
		{Name: "worker", Kind: "worker", Image: image, Instances: 1, Env: map[string]string{"MODE": "worker"}, SecretRefs: map[string]string{"DATABASE_URL": "database-v1"}, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 120}},
	}}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	app, err = c.LinkDatabase(ctx, app.ID, controller.DatabaseLinkRequest{Name: "database", ResourceID: dbID, Endpoint: endpoint, ExpectedVersion: app.Version, Acknowledge: true})
	if err != nil {
		t.Fatal(err)
	}
	for i := range app.Manifest.Components {
		if app.Manifest.Components[i].Name == "api" || app.Manifest.Components[i].Name == "worker" {
			app.Manifest.Components[i].DependsOn = []string{"database"}
		}
	}
	app, err = c.UpdateApplication(ctx, app.ID, app.Manifest, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	grants := []runtimeinputs.Binding{}
	for _, name := range []string{"api", "worker"} {
		grants = append(grants, runtimeinputs.Binding{Reference: "database-v1", TargetID: target.ID, ApplicationID: app.ID, Component: name, Variable: "DATABASE_URL", ValueEnv: "OAP_SECRET_TEST_DATABASE"})
	}
	raw, _ := json.Marshal(grants)
	c.RuntimeInputs, err = runtimeinputs.Parse(raw, func(string) string { return dsn })
	if err != nil {
		t.Fatal(err)
	}
	// Existing database linking remains an owner operation. The external agent
	// receives only this app's permitted lifecycle actions after explicit linking.
	c.RequireIdentity = true
	svc := access.Service{Pool: c.Store.Pool}
	owner, err := svc.Register(ctx, "portable-native@example.invalid", "Owner", "portable-native-owner-password", "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := svc.IssueRestricted(ctx, owner, "Portable lifecycle agent", "operate", app.ID, time.Hour, domain.AgentPermissions{Actions: []string{"configure", "deploy", "restart", "recover"}, TargetID: target.ID, Environment: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := svc.Principal(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	agentCtx := domain.WithPrincipal(ctx, actor)
	api := httptest.NewServer((&httpapi.Server{Controller: c}).Handler())
	defer api.Close()
	invoke := func(command string, flags []string, body any) cli.Result {
		t.Helper()
		var in, out bytes.Buffer
		if body != nil {
			json.NewEncoder(&in).Encode(body)
		}
		args := append([]string{"--url", api.URL}, flags...)
		if body != nil {
			args = append(args, "--file", "-")
		}
		args = append(args, command)
		code := cli.Run(ctx, args, func(key string) string {
			if key == "OAP_API_TOKEN" {
				return token
			}
			return ""
		}, &in, &out)
		var result cli.Result
		if json.Unmarshal(out.Bytes(), &result) != nil || code != 0 {
			t.Fatalf("portable CLI %s failed: %d", command, code)
		}
		return result
	}
	planned := func(request controller.ActionRequest) store.ActionPlan {
		t.Helper()
		created := invoke("action-plan", nil, request)
		raw, _ := json.Marshal(created.Data)
		var plan store.ActionPlan
		if json.Unmarshal(raw, &plan) != nil || plan.ID == "" {
			t.Fatal("invalid plan")
		}
		// MCP and CLI submit the identical durable plan to the same REST/controller.
		rpc, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "execute_action_plan", "arguments": map[string]string{"planId": plan.ID}}})
		var out bytes.Buffer
		server := mcp.Server{URL: api.URL, Token: token, Operate: true}
		if err := server.Serve(ctx, bytes.NewReader(append(rpc, '\n')), &out); err != nil {
			t.Fatal(err)
		}
		var reply struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if json.Unmarshal(out.Bytes(), &reply) != nil || reply.Result.IsError || len(reply.Result.Content) != 1 {
			t.Fatal("MCP action failed")
		}
		var executed store.ActionPlan
		if json.Unmarshal([]byte(reply.Result.Content[0].Text), &executed) != nil || executed.Result == nil {
			t.Fatal("MCP receipt missing")
		}
		replay := invoke("execute-action", []string{"--plan", plan.ID}, map[string]any{})
		raw, _ = json.Marshal(replay.Data)
		var repeated store.ActionPlan
		json.Unmarshal(raw, &repeated)
		if repeated.Result == nil || *repeated.Result != *executed.Result {
			t.Fatal("CLI/MCP receipt divergence")
		}
		return executed
	}
	spec := domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: app.Manifest}
	invoke("validate-spec", nil, map[string]any{"applicationId": app.ID, "specification": spec})
	invoke("diff-spec", []string{"--app", app.ID}, map[string]any{"expectedVersion": app.Version, "specification": spec})
	saved := planned(controller.ActionRequest{Action: "configure", ApplicationID: app.ID, ExpectedVersion: app.Version, Specification: &spec})
	if saved.Result.OperationID != "" {
		t.Fatal("save deployed")
	}
	app, err = c.Store.Application(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	immutable := call("GET", "/images/"+image+"/json", nil)["Id"].(string)
	images := map[string]string{"frontend": immutable, "api": immutable, "worker": immutable}
	release := planned(controller.ActionRequest{Action: "deploy", ApplicationID: app.ID, ExpectedVersion: app.Version, Images: images})
	d, err := c.Store.Deployment(ctx, release.Result.OperationID)
	if err != nil || len(d.Steps) != 3 {
		t.Fatal("database entered release", err)
	}
	for _, name := range []string{"frontend", "api", "worker"} {
		defer adapter.Stop(ctx, domain.ResourceName(app.ID, name, 1))
	}
	if err = c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer c.Jobs.Stop(ctx)
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		observed, e := c.Store.Deployment(ctx, d.ID)
		if e != nil {
			t.Fatal(e)
		}
		if domain.Terminal(observed.State) {
			d = observed
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if d.State != "succeeded" {
		observed, _ := c.Store.Deployment(ctx, d.ID)
		t.Fatalf("three-workload release failed or timed out: %s %+v", observed.State, observed.Steps)
	}
	c.Jobs.Stop(ctx)
	instances, err := c.Instances(ctx, app.ID)
	if err != nil || len(instances) != 4 {
		t.Fatal(err)
	}
	for _, v := range instances {
		if v.Status != "running:healthy" {
			t.Fatal("component is not healthy", v.Component, v.Status)
		}
		if v.Component == "database" && (v.Resource.URL != "" || v.Resource.ArtifactKind != "database") {
			t.Fatal("database exposed an HTTP service")
		}
	}
	fetch := func(url string) map[string]any {
		t.Helper()
		r, e := http.Get(url)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatal("example connection unavailable")
		}
		v := map[string]any{}
		if json.NewDecoder(r.Body).Decode(&v) != nil {
			t.Fatal("example JSON unavailable")
		}
		return v
	}
	front, _ := adapter.Inspect(ctx, domain.ResourceName(app.ID, "frontend", 1))
	apiResource, _ := adapter.Inspect(ctx, domain.ResourceName(app.ID, "api", 1))
	connected := fetch(front.URL + "/upstream")
	if connected["connected"] != true || connected["databaseTime"] == "" {
		t.Fatal("frontend API database connection failed")
	}
	code, rawLogs := request(t, srv, http.MethodGet, "/api/v1/applications/"+app.ID+"/logs/worker", "", nil)
	var logResult struct {
		Logs string `json:"logs"`
	}
	json.Unmarshal(rawLogs, &logResult)
	if code != 200 || !strings.Contains(logResult.Logs, "worker database connected") {
		t.Fatal("worker did not connect to database")
	}
	code, rawLogs = request(t, srv, http.MethodGet, "/api/v1/applications/"+app.ID+"/logs/database", "", nil)
	json.Unmarshal(rawLogs, &logResult)
	if code != 200 || !strings.Contains(logResult.Logs, "ready to accept connections") {
		t.Fatal("database logs unavailable")
	}
	savedDefinition, _ := json.Marshal(app)
	if bytes.Contains(savedDefinition, []byte(dsn)) || bytes.Contains(savedDefinition, []byte(readerPassword)) {
		t.Fatal("SQL credentials saved in definition")
	}
	if fingerprint() != before {
		t.Fatal("OAP changed native database configuration")
	}
	var marker string
	if admin.QueryRow(ctx, "SELECT value FROM example_marker").Scan(&marker) != nil || marker != "existing-data-unchanged" {
		t.Fatal("database data changed")
	}
	reader, err := pgx.Connect(ctx, strings.Replace(dsn, gateway, "127.0.0.1", 1))
	if err != nil {
		t.Fatal("reader verification unavailable")
	}
	if _, err = reader.Exec(ctx, "INSERT INTO example_marker VALUES('forbidden')"); err == nil {
		t.Fatal("application credential can write owner data")
	}
	reader.Close(ctx)
	// Restart one managed worker through the same plan path; preserve its peers.
	apiBootBefore := fetch(apiResource.URL + "/identity")["boot"]
	restartPlan := planned(controller.ActionRequest{Action: "restart", ApplicationID: app.ID, ExpectedVersion: app.Version, Component: "worker", Ordinal: 1})
	for i := 0; i < 40; i++ {
		var snooze *river.JobSnoozeError
		if e := c.Advance(ctx, restartPlan.Result.OperationID); e != nil && !errors.As(e, &snooze) {
			t.Fatal(e)
		}
		observed, e := c.Store.Deployment(ctx, restartPlan.Result.OperationID)
		if e != nil {
			t.Fatal(e)
		}
		if observed.State == "succeeded" {
			break
		}
		if domain.Terminal(observed.State) || i == 39 {
			t.Fatal("worker restart failed", observed.State)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if fetch(apiResource.URL + "/identity")["boot"] != apiBootBefore {
		t.Fatal("worker restart changed API")
	}
	boot := fetch(apiResource.URL + "/identity")["boot"]
	call("POST", "/containers/"+dbID+"/stop?t=3", nil)
	failedResponse, failedErr := http.Get(apiResource.URL)
	if failedErr != nil {
		t.Fatal(failedErr)
	}
	failedResponse.Body.Close()
	if failedResponse.StatusCode != 503 {
		t.Fatal("API ignored unavailable database")
	}

	instances, _ = c.Instances(ctx, app.ID)
	if instances[3].Status == "running:healthy" {
		t.Fatal("database failure hidden")
	}
	heldPlan := planned(controller.ActionRequest{Action: "deploy", ApplicationID: app.ID, ExpectedVersion: app.Version, Components: []string{"api"}, Images: map[string]string{"api": immutable}})
	held, err := c.Store.Deployment(ctx, heldPlan.Result.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Advance(ctx, held.ID); err != nil {
		t.Fatal(err)
	}
	held, _ = c.Store.Deployment(ctx, held.ID)
	if held.State != "attention" {
		t.Fatal("database failure did not hold release")
	}
	if fetch(apiResource.URL + "/identity")["boot"] != boot {
		t.Fatal("dependency failure restarted API")
	}
	call("POST", "/containers/"+dbID+"/start", nil)
	waitDB()
	recovered := planned(controller.ActionRequest{Action: "recover", ApplicationID: app.ID, ExpectedVersion: app.Version, DeploymentID: held.ID, Recovery: &controller.Recovery{Component: "api", Ordinal: 1, RetryPreparation: true, ExpectedUpdatedAt: held.UpdatedAt}})
	held, err = c.Store.Deployment(agentCtx, recovered.Result.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		var snooze *river.JobSnoozeError
		if e := c.Advance(ctx, held.ID); e != nil && !errors.As(e, &snooze) {
			t.Fatal(e)
		}
		held, _ = c.Store.Deployment(ctx, held.ID)
		if held.State == "succeeded" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if held.State != "succeeded" {
		t.Fatal("dependency hold failed to recover", held.State)
	}
	fresh, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("restored fixture unavailable: %v", err)
	}
	defer fresh.Close(ctx)
	if fresh.QueryRow(ctx, "SELECT value FROM example_marker").Scan(&marker) != nil || marker != "existing-data-unchanged" {
		t.Fatal("existing data changed across dependency recovery")
	}
	if fingerprint() != before {
		t.Fatal("native database configuration changed")
	}
	if fetch(front.URL + "/upstream")["connected"] != true {
		t.Fatal("consumer connection did not recover")
	}

}
