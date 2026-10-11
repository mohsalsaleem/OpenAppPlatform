package system_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
)

// Lose only the response: the real Docker operation has already completed.
type lostDockerResponse struct {
	*docker.Client
	operation  string
	dispatches int
	marker     string
}

func (a *lostDockerResponse) Deploy(ctx context.Context, id string) (string, error) {
	a.dispatches++
	remote, err := a.Client.Deploy(ctx, id)
	if err != nil {
		return remote, err
	}
	a.operation = remote
	if a.marker != "" {
		// Publish only after the native operation completes, while Advance still
		// owns its PostgreSQL session lock and has not saved the operation ID.
		if err = os.WriteFile(a.marker+".tmp", []byte(remote), 0600); err != nil {
			return "", err
		}
		if err = os.Rename(a.marker+".tmp", a.marker); err != nil {
			return "", err
		}
		<-ctx.Done()
		return "", ctx.Err()
	}
	return "", errors.New("injected response loss after Docker dispatch")
}

func TestNativeDockerLostResponseRecoveryDoesNotRestartRuntime(t *testing.T) {
	nativeDockerResponseRecovery(t, false)
}

func TestNativeDockerProcessKillRecoveryDoesNotRestartRuntime(t *testing.T) {
	nativeDockerResponseRecovery(t, true)
}

// This helper runs only in a child test process. The parent kills it while the
// actual controller is blocked inside a completed native dispatch.
func TestDockerRecoveryProcessHelper(t *testing.T) {
	if os.Getenv("OAP_RECOVERY_CHILD") != "1" {
		return // exercised through TestNativeDockerProcessKillRecoveryDoesNotRestartRuntime
	}
	ctx := context.Background()
	s, err := store.Open(ctx, os.Getenv("OAP_RECOVERY_DATABASE"))
	if err != nil {
		t.Fatal("open child store failed")
	}
	defer s.Pool.Close()
	c, err := controller.New(ctx, s, func(target domain.Target) (operator.Adapter, error) {
		client, err := docker.New(target)
		if err != nil {
			return nil, err
		}
		return &lostDockerResponse{Client: client, marker: os.Getenv("OAP_RECOVERY_MARKER")}, nil
	})
	if err != nil {
		t.Fatal("create child controller failed")
	}
	for i := 0; i < 4; i++ {
		_ = c.Advance(ctx, os.Getenv("OAP_RECOVERY_RELEASE"))
	}
	t.Fatal("child did not reach dispatch barrier")
}

func nativeDockerResponseRecovery(t *testing.T, killProcess bool) {
	t.Helper()
	socket, image := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_DOCKER_IMAGE")
	if socket == "" || image == "" {
		t.Skip("isolated Docker fixtures required")
	}
	ctx := context.Background()
	target := domain.Target{ID: "recovery-docker", Name: "Recovery Docker", Operator: "docker", Environment: "staging", URL: "unix://" + socket, Settings: json.RawMessage(`{"pullPolicy":"never","hostBindIP":"127.0.0.1"}`)}
	adapter, err := docker.New(target)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	lost := &lostDockerResponse{Client: adapter}
	c, _ := setup(t, lost)
	if err = c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	app, err := c.CreateApplication(ctx, domain.Manifest{Name: "recovery-native-" + domain.NewID()[:8], TargetID: target.ID, Environment: "staging", Components: []domain.Component{{Name: "web", Image: image, Port: 8080, HostPort: port, Instances: 1, Env: map[string]string{"FIXTURE_PORT": "8080"}, Readiness: &domain.ReadinessPolicy{RequireHealthy: true, TimeoutSeconds: 120}}}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Enqueue(ctx, app.ID, "lost-response")
	if err != nil {
		t.Fatal(err)
	}
	if killProcess {
		marker := filepath.Join(t.TempDir(), "operation")
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		child := exec.Command(binary, "-test.run=^TestDockerRecoveryProcessHelper$", "-test.timeout=45s")
		child.Env = append(os.Environ(), "OAP_RECOVERY_CHILD=1", "OAP_RECOVERY_DATABASE="+c.Store.Pool.Config().ConnString(), "OAP_RECOVERY_RELEASE="+d.ID, "OAP_RECOVERY_MARKER="+marker)
		if err = child.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = child.Process.Kill() })
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			remote, e := os.ReadFile(marker)
			if e == nil {
				lost.operation = string(remote)
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if lost.operation == "" {
			_ = child.Process.Kill()
			_ = child.Wait()
			t.Fatal("child never reached completed dispatch")
		}
		d, err = c.Store.Deployment(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer adapter.Stop(ctx, d.Steps[0].ResourceID)
		if d.Steps[0].Phase != "dispatching" || d.Steps[0].RemoteDeploymentID != "" {
			t.Fatal("kill barrier is outside durable dispatch uncertainty")
		}
		// A competing worker must respect the live child's session lock.
		_ = c.Advance(ctx, d.ID)
		locked, err := c.Store.Deployment(ctx, d.ID)
		if err != nil || !locked.UpdatedAt.Equal(d.UpdatedAt) {
			t.Fatal("competing worker changed locked release")
		}
		if err = child.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err = child.Wait(); err == nil {
			t.Fatal("child exited successfully instead of being killed")
		}
	} else {
		for i := 0; i < 4; i++ {
			_ = c.Advance(ctx, d.ID)
			d, err = c.Store.Deployment(ctx, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			if d.State == "attention" {
				break
			}
		}
	}
	// SIGKILL must release the worker's database session lock. Advancing the
	// persisted intent should now produce attention without another dispatch.
	if killProcess {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			_ = c.Advance(ctx, d.ID)
			d, err = c.Store.Deployment(ctx, d.ID)
			if err != nil {
				t.Fatal(err)
			}
			if d.State == "attention" {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	expectedDispatches := 1
	if killProcess {
		expectedDispatches = 0
	} // one real dispatch occurred in the child
	if d.State != "attention" || lost.dispatches != expectedDispatches || lost.operation == "" || d.Steps[0].RemoteDeploymentID != "" {
		t.Fatalf("response loss not durably fenced: %+v dispatches=%d", d, lost.dispatches)
	}
	defer adapter.Stop(ctx, d.Steps[0].ResourceID)
	deadline := time.Now().Add(30 * time.Second)
	var endpoint string
	for time.Now().Before(deadline) {
		r, e := adapter.Inspect(ctx, d.Steps[0].ResourceID)
		if e == nil && r.Status == "running:healthy" {
			endpoint = r.URL
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if endpoint == "" {
		t.Fatal("native fixture did not become healthy")
	}
	before := fixtureJSON(t, endpoint)
	c2, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	if err = c2.Advance(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = c2.Enqueue(ctx, app.ID, "must-stay-fenced"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("uncertain work lost its fence: %v", err)
	}
	request := controller.Recovery{Component: "web", Ordinal: 1, ExpectedUpdatedAt: d.UpdatedAt, RetryPreparation: true}
	if _, err = c2.Recover(ctx, d.ID, request); err == nil {
		t.Fatal("uncertain dispatch accepted as preparation retry")
	}
	request.RetryPreparation = false
	request.RemoteDeploymentID = lost.operation
	recovered, err := c2.Recover(ctx, d.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c2.Recover(ctx, d.ID, request); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale recovery accepted: %v", err)
	}
	if err = c2.Advance(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	finished, err := c.Store.Deployment(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	after := fixtureJSON(t, endpoint)
	if finished.State != "succeeded" || recovered.Steps[0].ResourceID != d.Steps[0].ResourceID || lost.dispatches != expectedDispatches || before["boot"] == "" || before["boot"] != after["boot"] {
		t.Fatalf("recovery replaced or restarted runtime: state=%s dispatches=%d before=%v after=%v", finished.State, lost.dispatches, before, after)
	}
}
