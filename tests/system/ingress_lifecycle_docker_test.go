package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/cli"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ingress"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentStandaloneIngressReleaseScaleAndControllerRecovery(t *testing.T) {
	socket, image, caddyImage := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_DOCKER_IMAGE"), os.Getenv("OAP_TEST_CADDY_IMAGE")
	if socket == "" || image == "" || caddyImage == "" {
		t.Skip("isolated Docker Caddy fixtures required")
	}
	if socket != "/run/oap-test/docker.sock" {
		t.Fatal("isolated Engine required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 330*time.Second)
	defer cancel()
	engine := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	defer engine.CloseIdleConnections()
	call := func(method, path string, body any) map[string]any {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		request, _ := http.NewRequestWithContext(context.Background(), method, "http://docker/v1.45"+path, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		response, err := engine.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			t.Fatalf("ingress fixture %s %s %d", method, path, response.StatusCode)
		}
		value := map[string]any{}
		if response.StatusCode != 204 {
			json.NewDecoder(response.Body).Decode(&value)
		}
		return value
	}
	suffix := domain.NewID()[:8]
	targetID := "ingress-" + suffix
	directory := "/run/oap-test/ingress-" + suffix
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	var proxyID string
	var adapter *docker.Client
	t.Cleanup(func() {
		if proxyID != "" {
			call("DELETE", "/containers/"+proxyID+"?force=true&v=true", nil)
		}
		os.RemoveAll(directory)
	})
	// Reserve a stable loopback test port, not a public ingress listener.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	proxySocket := "/run/oap-ingress/admin.sock"
	bootstrap, _ := json.Marshal(map[string]any{"admin": map[string]any{"listen": "unix/" + proxySocket + "|0600", "config": map[string]any{"persist": true}}})
	proxyID = call("POST", "/containers/create?name=ingress-owner-"+suffix, map[string]any{"Image": caddyImage, "Labels": map[string]string{"io.openappplatform.ingress-target": targetID}, "Entrypoint": []string{"/bin/sh", "-c"}, "Cmd": []string{`printf '%s' "$1" >/config/bootstrap.json; exec caddy run --config /config/bootstrap.json --resume`, "fixture", string(bootstrap)}, "Env": []string{"XDG_CONFIG_HOME=/config", "XDG_DATA_HOME=/data"}, "ExposedPorts": map[string]any{"8080/tcp": map[string]any{}}, "HostConfig": map[string]any{"Binds": []string{directory + ":/run/oap-ingress"}, "PortBindings": map[string]any{"8080/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": fmt.Sprint(port)}}}, "SecurityOpt": []string{"no-new-privileges:true"}}})["Id"].(string)
	call("POST", "/containers/"+proxyID+"/start", nil)
	nativeClient, err := ingress.New(directory + "/admin.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer nativeClient.Close()
	var snapshot ingress.Snapshot
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		snapshot, err = nativeClient.Read(ctx)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}

	// Test-only transport fault: Caddy applies the write, then the response is lost.
	var loseResponse atomic.Bool
	var nativeWrites atomic.Int64
	forward := &httputil.ReverseProxy{Director: func(r *http.Request) { r.URL.Scheme = "http"; r.URL.Host = "localhost"; r.Host = "localhost" }, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", directory+"/admin.sock")
	}}, ModifyResponse: func(r *http.Response) error {
		if r.Request.Method == "POST" && loseResponse.Swap(false) {
			return errors.New("fixture response lost")
		}
		return nil
	}, ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		connection, _, e := w.(http.Hijacker).Hijack()
		if e == nil {
			connection.Close()
		}
	}}
	bridgeListener, e := net.Listen("unix", directory+"/publisher.sock")
	if e != nil {
		t.Fatal(e)
	}
	bridge := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config/" || r.Method != "GET" && r.Method != "POST" {
			w.WriteHeader(404)
			return
		}
		if r.Method == "POST" {
			nativeWrites.Add(1)
		}
		forward.ServeHTTP(w, r)
	}), ReadHeaderTimeout: time.Second}
	go bridge.Serve(bridgeListener)
	defer bridge.Close()
	pinned := call("GET", "/images/"+image+"/json", nil)["Id"].(string)
	settings, _ := json.Marshal(docker.Settings{PullPolicy: "never", HostBindIP: "127.0.0.1", Ingress: &docker.IngressSettings{ContainerID: proxyID, AdminSocket: directory + "/publisher.sock", ProxySocket: proxySocket, Domain: "app.example.test", Domains: []string{"sibling.example.test", "moved.example.test"}, BootstrapHash: snapshot.Hash, HTTPPort: 8080, HTTPSPort: 8443, InternalCA: true}})
	target := domain.Target{ID: targetID, Name: "Ingress fixture", Operator: "docker", URL: "unix://" + socket, Environment: "staging", Settings: settings}
	adapter, err = docker.New(target)
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
	c.PollInterval = 100 * time.Millisecond
	m := domain.Manifest{Name: "ingress-app", TargetID: targetID, Environment: "staging", Components: []domain.Component{{Name: "frontend", Kind: "web", Image: pinned, Port: 8080, Instances: 1, Env: map[string]string{"OAP_TEST_MESSAGE": "frontend", "FIXTURE_PORT": "8080"}, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 60}}, {Name: "api", Kind: "web", Image: pinned, Port: 8080, Instances: 1, Env: map[string]string{"OAP_TEST_MESSAGE": "api", "FIXTURE_PORT": "8080"}, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 60}}}, Ingress: &domain.IngressDefinition{Domain: "app.example.test", Routes: []domain.IngressRoute{{Path: "/", Component: "frontend"}, {Path: "/api", Component: "api", StripPrefix: true}}}}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bindings, _ := s.Bindings(context.Background(), app.ID)
		for _, b := range bindings {
			adapter.Stop(context.Background(), b.ResourceID)
		}
	})
	c.RequireIdentity = true
	service := access.Service{Pool: s.Pool}
	owner, err := service.Register(ctx, "ingress-owner@example.invalid", "Owner", "isolated-ingress-owner-password", "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := service.IssueRestricted(ctx, owner, "Ingress agent", "operate", app.ID, time.Hour, domain.AgentPermissions{Actions: []string{"configure", "deploy", "scale-down", "recover", "remove-component", "rollback"}, TargetID: targetID, Environment: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	var handler atomic.Value
	handler.Store((&httpapi.Server{Controller: c}).Handler())
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.Load().(http.Handler).ServeHTTP(w, r) }))
	defer api.Close()
	invoke := func(command string, flags []string, body any, want int) cli.Result {
		t.Helper()
		for attempt := 0; attempt < 2; attempt++ {
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
				if key == "OAP_API_TOKEN" && command != "grant" && command != "approve-action" {
					return token
				}
				return ""
			}, &in, &out)
			var result cli.Result
			if json.Unmarshal(out.Bytes(), &result) != nil {
				t.Fatal("invalid CLI envelope")
			}
			if attempt == 0 && want == 0 && command == "action-plan" && code == 5 && result.Error != nil && result.Error.Code == "rate_limited" {
				t.Log("Action planning budget exhausted; respecting the one-minute product limit before retrying rejected creation")
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(61 * time.Second):
				}
				continue
			}
			if code != want {
				t.Fatalf("ingress agent %s exit %d expected %d: %s", command, code, want, out.String())
			}
			return result
		}
		t.Fatal("action planning retry exhausted")
		return cli.Result{}
	}
	plan := func(request any) store.ActionPlan {
		t.Helper()
		result := invoke("action-plan", nil, request, 0)
		raw, _ := json.Marshal(result.Data)
		var p store.ActionPlan
		json.Unmarshal(raw, &p)
		return p
	}
	execute := func(p store.ActionPlan) store.ActionPlan {
		t.Helper()
		result := invoke("execute-action", []string{"--plan", p.ID}, map[string]any{}, 0)
		raw, _ := json.Marshal(result.Data)
		var done store.ActionPlan
		json.Unmarshal(raw, &done)
		return done
	}
	wait := func(done store.ActionPlan) domain.Deployment {
		t.Helper()
		if done.Result == nil {
			t.Fatal("missing action receipt")
		}
		result := invoke("wait", []string{"--operation", done.Result.OperationID, "--timeout", "90s"}, nil, 0)
		_ = result
		d, err := s.Deployment(ctx, done.Result.OperationID)
		if err != nil || d.State != "succeeded" {
			t.Fatal("routed release failed", d.State, err)
		}
		return d
	}
	if err = c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { c.Jobs.Stop(context.Background()) }()
	first := execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: app.ID, ExpectedVersion: app.Version}))
	wait(first)
	// A separate owner-issued onboarding grant creates and deploys the sibling
	// through the same CLI. The original agent never receives sibling authority.
	firstToken := token
	grantOwnerSession, e := service.Issue(ctx, owner, "session", "Owner", "operate", "", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	ownerFile := filepath.Join(directory, "owner.json")
	ownerRaw, _ := json.Marshal(map[string]any{"schemaVersion": 1, "origin": api.URL, "session": grantOwnerSession})
	if e = os.WriteFile(ownerFile, ownerRaw, 0600); e != nil {
		t.Fatal(e)
	}
	agentFile := filepath.Join(directory, "sibling-agent.json")
	invoke("grant", []string{"--credential-file", ownerFile, "--out", agentFile}, map[string]any{"name": "Sibling agent", "targetId": targetID, "environment": "staging", "maxApplications": 1, "actions": []string{"configure", "deploy", "recover"}}, 0)
	agentRaw, e := os.ReadFile(agentFile)
	if e != nil {
		t.Fatal(e)
	}
	var issued struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(agentRaw, &issued) != nil || issued.Token == "" {
		t.Fatal("missing scoped sibling credential")
	}
	token = issued.Token
	siblingSpec := domain.Manifest{Name: "ingress-sibling", TargetID: targetID, Environment: "staging", Components: []domain.Component{{Name: "web", Kind: "web", Image: pinned, Port: 8080, Instances: 1, Env: map[string]string{"OAP_TEST_MESSAGE": "sibling"}, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 60}}}, Ingress: &domain.IngressDefinition{Domain: "sibling.example.test", Routes: []domain.IngressRoute{{Path: "/", Component: "web"}}}}
	created := execute(plan(controller.ActionRequest{Action: "create", Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: siblingSpec}}))
	siblingID := created.Result.ApplicationID
	t.Cleanup(func() {
		bindings, _ := s.Bindings(context.Background(), siblingID)
		for _, b := range bindings {
			adapter.Stop(context.Background(), b.ResourceID)
		}
	})
	siblingApp, _ := s.Application(ctx, siblingID)
	wait(execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: siblingID, ExpectedVersion: siblingApp.Version})))
	// Saved desired sibling changes must not enter another app's publication.
	siblingApp, _ = s.Application(ctx, siblingID)
	unshipped := siblingApp.Manifest
	unshipped.Components = append([]domain.Component(nil), unshipped.Components...)
	unshipped.Components[0].Env = map[string]string{"OAP_TEST_MESSAGE": "not-deployed"}
	execute(plan(controller.ActionRequest{Action: "configure", ApplicationID: siblingID, ExpectedVersion: siblingApp.Version, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: unshipped}}))
	oldPublication, e := s.IngressPublication(ctx, first.Result.OperationID)
	if e != nil || !errors.Is(s.ConfirmIngress(ctx, oldPublication), domain.ErrConflict) {
		t.Fatal("old application publication could rewind shared authority", e)
	}
	token = firstToken
	// Scope isolation remains enforced even though the controller aggregates routes.
	invoke("get", []string{"--app", siblingID}, nil, 3)
	checkSibling := func() {
		t.Helper()
		request, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
		request.Host = "sibling.example.test"
		response, e := (&http.Client{Timeout: 3 * time.Second}).Do(request)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		var value map[string]string
		if response.StatusCode != 200 || json.NewDecoder(response.Body).Decode(&value) != nil || value["message"] != "sibling" {
			t.Fatal("sibling route was lost")
		}
	}
	checkSibling()
	public := &http.Client{Timeout: 3 * time.Second}
	defer public.CloseIdleConnections()
	get := func(path string) (map[string]string, error) {
		request, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
		request.Host = m.Ingress.Domain
		response, err := public.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return nil, fmt.Errorf("public status %d", response.StatusCode)
		}
		var value map[string]string
		err = json.NewDecoder(response.Body).Decode(&value)
		return value, err
	}
	value, err := get("/")
	if err != nil || value["message"] != "frontend" {
		t.Fatal("frontend route", err)
	}
	value, err = get("/api")
	if err != nil || value["message"] != "api" {
		t.Fatal("API route", err)
	}

	oldVersion := value["version"]
	rollbackImage := call("GET", "/images/"+os.Getenv("OAP_TEST_DOCKER_REPLACEMENT_IMAGE")+"/json", nil)["Id"].(string)
	changedImages := execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: app.ID, ExpectedVersion: app.Version, Components: []string{"frontend", "api"}, Images: map[string]string{"frontend": rollbackImage, "api": rollbackImage}}))
	wait(changedImages)
	value, err = get("/api")
	if err != nil || value["version"] == oldVersion {
		t.Fatal("routed image upgrade", err)
	}
	rollbackRead := invoke("rollback-plan", []string{"--app", app.ID}, map[string]any{"releaseId": first.Result.OperationID, "expectedVersion": app.Version}, 0)
	rawRollback, _ := json.Marshal(rollbackRead.Data)
	var rollback controller.RollbackPlan
	json.Unmarshal(rawRollback, &rollback)
	if len(rollback.Instances) != 2 {
		t.Fatal("routed app rollback did not select both components")
	}
	rollbackResult := invoke("rollback", []string{"--app", app.ID, "--key", "routed-app-rollback"}, map[string]any{"releaseId": first.Result.OperationID, "expectedVersion": app.Version, "planHash": rollback.PlanHash, "acknowledgeDataCompatibility": true}, 0)
	invoke("wait", []string{"--operation", rollbackResult.OperationID, "--timeout", "90s"}, nil, 0)
	for _, path := range []string{"/", "/api"} {
		value, err = get(path)
		if err != nil || value["version"] != oldVersion {
			t.Fatal("routed app image restore", path, err)
		}
	}
	checkSibling()
	current, _ := s.Application(ctx, app.ID)
	spec := current.Manifest
	spec.Components = append([]domain.Component(nil), spec.Components...)
	spec.Components[1].Instances = 3
	save := execute(plan(controller.ActionRequest{Action: "configure", ApplicationID: app.ID, ExpectedVersion: current.Version, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: spec}}))
	if save.Result.State != "saved" {
		t.Fatal("spec not saved")
	}
	before, _ := nativeClient.Read(ctx)
	current, _ = s.Application(ctx, app.ID)
	up := execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: app.ID, ExpectedVersion: current.Version, Components: []string{"api"}}))
	wait(up)
	checkSibling()
	after, _ := nativeClient.Read(ctx)
	if after.Hash == before.Hash {
		t.Fatal("scale-up did not publish membership")
	}
	boots := map[string]bool{}
	for i := 0; i < 30; i++ {
		v, err := get("/api")
		if err != nil {
			t.Fatal(err)
		}
		boots[v["boot"]] = true
	}
	if len(boots) != 3 {
		t.Fatal("three replicas not serving", len(boots))
	}
	// Recreating only the controller leaves proxy/container traffic intact.
	if err = c.Jobs.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	value, err = get("/api")
	if err != nil || value["message"] != "api" {
		t.Fatal("controller-down route failed", err)
	}
	replacement, err := controller.New(ctx, s, func(domain.Target) (operator.Adapter, error) { return adapter, nil })
	if err != nil {
		t.Fatal(err)
	}
	replacement.RequireIdentity = true
	replacement.PollInterval = 100 * time.Millisecond
	c = replacement
	handler.Store((&httpapi.Server{Controller: c}).Handler())
	if err = c.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	current, _ = s.Application(ctx, app.ID)
	downSpec := current.Manifest
	downSpec.Components = append([]domain.Component(nil), downSpec.Components...)
	downSpec.Components[1].Instances = 1
	down := plan(controller.ActionRequest{Action: "scale-down", ApplicationID: app.ID, ExpectedVersion: current.Version, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: downSpec}, RequireReview: true})
	invoke("execute-action", []string{"--plan", down.ID}, map[string]any{}, 4)
	ownerSession, err := service.Issue(ctx, owner, "session", "Owner", "operate", "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ownerPrincipal, err := service.Principal(ctx, ownerSession)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.ApproveActionPlan(domain.WithPrincipal(ctx, ownerPrincipal), down.ID, down.Hash); err != nil {
		t.Fatal(err)
	}
	var failures, requests atomic.Int64
	trafficCtx, stopTraffic := context.WithCancel(ctx)
	defer stopTraffic()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for trafficCtx.Err() == nil {
			requests.Add(1)
			if _, err := get("/api"); err != nil {
				failures.Add(1)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	retired := execute(down)
	wait(retired)
	checkSibling()
	stopTraffic()
	<-finished
	if failures.Load() != 0 || requests.Load() < 10 {
		t.Fatal("scale-down public failures", failures.Load(), requests.Load())
	}
	final, _ := s.Application(ctx, app.ID)
	if final.Manifest.Components[1].Instances != 1 {
		t.Fatal("desired count not committed")
	}
	surviving := map[string]bool{}
	for i := 0; i < 12; i++ {
		v, err := get("/api")
		if err != nil {
			t.Fatal(err)
		}
		surviving[v["boot"]] = true
	}
	if len(surviving) != 1 {
		t.Fatal("retired replicas still routed")
	}
	replay := execute(down)
	if replay.Result.OperationID != retired.Result.OperationID {
		t.Fatal("retirement replay duplicated operation")
	}
	snapshot, _ = nativeClient.Read(ctx)
	publication, err := s.IngressPublication(ctx, retired.Result.OperationID)
	if err != nil || publication.Phase != "applied" || publication.CandidateHash != snapshot.Hash {
		t.Fatal("durable publication receipt", err)
	}

	// An uncertain dispatch cannot be sent again. Recovery observes its committed
	// candidate and completes the existing operation under the same named agent.
	current, _ = s.Application(ctx, app.ID)
	changed := current.Manifest
	ingressCopy := *changed.Ingress
	ingressCopy.Routes = append([]domain.IngressRoute(nil), ingressCopy.Routes...)
	ingressCopy.Routes[1].StripPrefix = false
	ingressCopy.Domain = "moved.example.test"
	changed.Ingress = &ingressCopy
	// Domain authority remains owner-configured; save never changes live routes.
	unauthorized := changed
	unauthorizedIngress := *changed.Ingress
	unauthorizedIngress.Domain = "unapproved.example.test"
	unauthorized.Ingress = &unauthorizedIngress
	invoke("action-plan", nil, controller.ActionRequest{Action: "configure", ApplicationID: app.ID, ExpectedVersion: current.Version, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: unauthorized}}, 4)
	handoff := plan(controller.ActionRequest{Action: "configure", ApplicationID: app.ID, ExpectedVersion: current.Version, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: changed}})
	var summary map[string]any
	if json.Unmarshal(handoff.Summary, &summary) != nil || summary["ingressChange"] == nil {
		t.Fatal("route transition missing from reviewed plan")
	}
	execute(handoff)
	if _, err := get("/"); err != nil {
		t.Fatal("saving domain handoff changed live routes", err)
	}
	current, _ = s.Application(ctx, app.ID)
	writeCount := nativeWrites.Load()
	loseResponse.Store(true)
	handoffRelease := plan(controller.ActionRequest{Action: "deploy", ApplicationID: app.ID, ExpectedVersion: current.Version, Components: []string{"api"}, RequireReview: true})
	invoke("execute-action", []string{"--plan", handoffRelease.ID}, map[string]any{}, 4)
	// Approval uses the owner CLI identity, execution remains the scoped agent.
	invoke("approve-action", []string{"--credential-file", ownerFile, "--plan", handoffRelease.ID}, map[string]any{"planHash": handoffRelease.Hash}, 0)
	uncertain := execute(handoffRelease)
	invoke("wait", []string{"--operation", uncertain.Result.OperationID, "--timeout", "90s"}, nil, 6)
	stopped, _ := s.Deployment(ctx, uncertain.Result.OperationID)
	if stopped.State != "attention" || nativeWrites.Load() != writeCount+1 {
		t.Fatal("lost response dispatch was retried")
	}
	if _, e := s.IngressSiblings(ctx, targetID, siblingID); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("uncertain sibling publication did not fence proxy", e)
	}
	recovery := execute(plan(controller.ActionRequest{Action: "recover", ApplicationID: app.ID, ExpectedVersion: current.Version, DeploymentID: stopped.ID, Recovery: &controller.Recovery{Component: "@ingress", Ordinal: 1, ExpectedUpdatedAt: stopped.UpdatedAt}}))
	wait(recovery)
	checkSibling()
	if nativeWrites.Load() != writeCount+1 || recovery.Result.OperationID != stopped.ID {
		t.Fatal("ingress observation recovery issued another write")
	}
	checkOldDomain := func() {
		t.Helper()
		request, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
		request.Host = "app.example.test"
		response, err := public.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatal("old domain still routed", response.StatusCode)
		}
	}
	checkOldDomain()
	m.Ingress.Domain = "moved.example.test"
	value, err = get("/")
	if err != nil || value["message"] != "frontend" {
		t.Fatal("new domain not serving", err)
	}
	// A sibling's later publication must retain the handoff, not resurrect the
	// application's former hostname from an older release.
	token = issued.Token
	siblingApp, _ = s.Application(ctx, siblingID)
	siblingSpec = siblingApp.Manifest
	siblingSpec.Components = append([]domain.Component(nil), siblingSpec.Components...)
	siblingSpec.Components[0].Env = map[string]string{"OAP_TEST_MESSAGE": "sibling"}
	execute(plan(controller.ActionRequest{Action: "configure", ApplicationID: siblingID, ExpectedVersion: siblingApp.Version, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: siblingSpec}}))
	siblingApp, _ = s.Application(ctx, siblingID)
	wait(execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: siblingID, ExpectedVersion: siblingApp.Version})))
	checkSibling()
	checkOldDomain()
	if value, err = get("/"); err != nil || value["message"] != "frontend" {
		t.Fatal("sibling publication lost domain handoff", err)
	}
	// Withdrawal is a durable publication with nil ingress, not container
	// deletion or forgetting the app. Approval remains the owner's choice.
	checkAbsentDomain := func(host string) {
		t.Helper()
		request, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
		request.Host = host
		response, err := public.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatal("withdrawn domain still routed", host, response.StatusCode)
		}
	}
	token = firstToken
	current, _ = s.Application(ctx, app.ID)
	originalIngress := current.Manifest.Ingress
	bindingsBefore, err := s.Bindings(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	started := map[string]any{}
	for _, binding := range bindingsBefore {
		if !binding.Retired {
			started[binding.ResourceID] = call("GET", "/containers/"+binding.ResourceID+"/json", nil)["State"].(map[string]any)["StartedAt"]
		}
	}
	withdrawn := current.Manifest
	withdrawn.Ingress = nil
	withdrawRaw, _ := json.Marshal(controller.ActionRequest{Action: "configure", ApplicationID: app.ID, ExpectedVersion: current.Version, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: withdrawn}, RequireReview: true})
	var withdrawJSON map[string]any
	json.Unmarshal(withdrawRaw, &withdrawJSON)
	// Exercise explicit JSON null as well as the omitted-field representation
	// used for the final sibling withdrawal below.
	withdrawJSON["specification"].(map[string]any)["spec"].(map[string]any)["ingress"] = nil
	withdrawSave := plan(withdrawJSON)
	invoke("execute-action", []string{"--plan", withdrawSave.ID}, map[string]any{}, 4)
	invoke("approve-action", []string{"--credential-file", ownerFile, "--plan", withdrawSave.ID}, map[string]any{"planHash": withdrawSave.Hash}, 0)
	execute(withdrawSave)
	if _, err = get("/"); err != nil {
		t.Fatal("withdrawal save changed live routes", err)
	}
	current, _ = s.Application(ctx, app.ID)
	writesBeforeWithdrawal := nativeWrites.Load()
	loseResponse.Store(true)
	withdrawRelease := execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: app.ID, ExpectedVersion: current.Version}))
	invoke("wait", []string{"--operation", withdrawRelease.Result.OperationID, "--timeout", "90s"}, nil, 6)
	withdrawAttention, _ := s.Deployment(ctx, withdrawRelease.Result.OperationID)
	if withdrawAttention.State != "attention" || nativeWrites.Load() != writesBeforeWithdrawal+1 {
		t.Fatal("withdrawal lost response was retried")
	}
	checkAbsentDomain("moved.example.test")
	checkSibling()
	withdrawRecovery := execute(plan(controller.ActionRequest{Action: "recover", ApplicationID: app.ID, ExpectedVersion: current.Version, DeploymentID: withdrawAttention.ID, Recovery: &controller.Recovery{Component: "@ingress", Ordinal: 1, ExpectedUpdatedAt: withdrawAttention.UpdatedAt}}))
	wait(withdrawRecovery)
	if nativeWrites.Load() != writesBeforeWithdrawal+1 {
		t.Fatal("withdrawal recovery dispatched another write")
	}
	for id, startedAt := range started {
		state := call("GET", "/containers/"+id+"/json", nil)["State"].(map[string]any)
		if state["Running"] != true || state["StartedAt"] != startedAt {
			t.Fatal("route withdrawal restarted or stopped workload")
		}
	}
	// Redeployment without ingress does not publish routes again.
	wait(execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: app.ID, ExpectedVersion: current.Version})))
	if nativeWrites.Load() != writesBeforeWithdrawal+1 {
		t.Fatal("non-routed redeployment republished ingress")
	}
	token = issued.Token
	siblingApp, _ = s.Application(ctx, siblingID)
	wait(execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: siblingID, ExpectedVersion: siblingApp.Version})))
	checkSibling()
	checkAbsentDomain("moved.example.test")
	// Removing the final routed app leaves a persistent deny-only proxy.
	siblingSpec = siblingApp.Manifest
	siblingSpec.Ingress = nil
	execute(plan(controller.ActionRequest{Action: "configure", ApplicationID: siblingID, ExpectedVersion: siblingApp.Version, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: siblingSpec}}))
	siblingApp, _ = s.Application(ctx, siblingID)
	wait(execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: siblingID, ExpectedVersion: siblingApp.Version})))
	checkAbsentDomain("sibling.example.test")
	checkAbsentDomain("moved.example.test")
	checkOldDomain()
	// Re-enabling ingress is an explicit new spec/deploy, not resurrecting it
	// from the history while compiling another app's publication.
	token = firstToken
	current, _ = s.Application(ctx, app.ID)
	reenabled := current.Manifest
	reenabled.Ingress = originalIngress
	execute(plan(controller.ActionRequest{Action: "configure", ApplicationID: app.ID, ExpectedVersion: current.Version, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: reenabled}}))
	current, _ = s.Application(ctx, app.ID)
	wait(execute(plan(controller.ActionRequest{Action: "deploy", ApplicationID: app.ID, ExpectedVersion: current.Version})))
	if value, err = get("/"); err != nil || value["message"] != "frontend" {
		t.Fatal("explicit ingress re-enable failed", err)
	}
	checkAbsentDomain("sibling.example.test")
	// M3.2 removal reuses the shipped routing/drain primitive. Clean up the
	// desired routes first, then retire the exact API through the reviewed plan.
	current, _ = s.Application(ctx, app.ID)
	routeOnly := current.Manifest
	trimmed := *routeOnly.Ingress
	trimmed.Routes = nil
	for _, route := range routeOnly.Ingress.Routes {
		if route.Component != "api" {
			trimmed.Routes = append(trimmed.Routes, route)
		}
	}
	routeOnly.Ingress = &trimmed
	execute(plan(controller.ActionRequest{Action: "configure", ApplicationID: app.ID, ExpectedVersion: current.Version, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: routeOnly}}))
	if value, err = get("/api"); err != nil || value["message"] != "api" {
		t.Fatal("route cleanup save changed live API", err)
	}
	current, _ = s.Application(ctx, app.ID)
	withoutAPI := current.Manifest
	withoutAPI.Components = append([]domain.Component(nil), current.Manifest.Components[:1]...)
	removeAPI := plan(controller.ActionRequest{Action: "remove-component", ApplicationID: app.ID, ExpectedVersion: current.Version, Specification: &domain.ApplicationSpecification{APIVersion: "oap.dev/v1alpha1", Kind: "Application", Spec: withoutAPI}})
	if !removeAPI.RequiresReview {
		t.Fatal("routed component retirement omitted owner review")
	}
	invoke("execute-action", []string{"--plan", removeAPI.ID}, map[string]any{}, 4)
	invoke("approve-action", []string{"--credential-file", ownerFile, "--plan", removeAPI.ID}, map[string]any{"planHash": removeAPI.Hash}, 0)
	wait(execute(removeAPI))
	if native := call("GET", "/containers/"+domain.ResourceName(app.ID, "api", 1)+"/json", nil); native["State"].(map[string]any)["Running"] != false {
		t.Fatal("removed routed API still running")
	}
	if value, err = get("/api"); err != nil || value["message"] != "frontend" {
		t.Fatal("retired component remained in routing", err)
	}
	checkAbsentDomain("sibling.example.test")
	if dir := os.Getenv("OAP_TEST_REPORT_DIR"); dir != "" {
		raw, _ := json.MarshalIndent(map[string]any{"status": "passed", "scope": "owner-provisioned dedicated Caddy; local HTTP", "namedAgent": true, "sharedProxySiblingPreserved": true, "authorizedDomainHandoff": true, "savePreservesLiveDomain": true, "retiredDomainNotResurrected": true, "durableIngressWithdrawal": true, "withdrawalPreservesRunningContainers": true, "withdrawalLostResponseRecovered": true, "finalApplicationWithdrawal": true, "explicitIngressReenable": true, "routedComponentRetirement": true, "routedAppImageRollback": true, "scale": "1 -> 3 -> 1", "controllerDownTraffic": true, "scaleDownRequests": requests.Load(), "scaleDownFailures": failures.Load(), "publicationJournal": true, "retirementReplay": true, "lostResponseObservedWithoutRedispatch": true, "publicACME": false, "automaticProxyProvisioning": false}, "", "  ")
		os.WriteFile(filepath.Join(dir, "ingress-lifecycle.json"), append(raw, '\n'), 0600)
	}
}
