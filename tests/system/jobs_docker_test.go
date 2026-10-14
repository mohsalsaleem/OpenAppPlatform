package system_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
)

// Adapter evidence and the named-agent CLI release journey share one pinned,
// offline job fixture on the disposable Engine.
func TestStandaloneDockerRunToCompletionPrimitive(t *testing.T) {
	socket, base := os.Getenv("OAP_TEST_DOCKER_SOCKET"), os.Getenv("OAP_TEST_DOCKER_IMAGE")
	if socket == "" || base == "" {
		t.Skip("isolated Docker fixtures required")
	}
	if socket != "/run/oap-test/docker.sock" {
		t.Fatal("isolated Docker Engine required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	engine := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	defer engine.CloseIdleConnections()
	request := func(method, path string, body io.Reader) map[string]any {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, "http://docker/v1.45"+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/x-tar")
		}
		response, err := engine.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			t.Fatal("fixture Engine request", response.StatusCode)
		}
		out := map[string]any{}
		if err = json.NewDecoder(response.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	baseID := request("GET", "/images/"+base+"/json", nil)["Id"].(string)
	// The fixture inherits the pinned, already loaded Alpine-based image. There
	// is no package installation, image pull or application command override.
	dockerfile := []byte("FROM " + baseID + "\nENTRYPOINT [\"/bin/sh\", \"-c\", \"sleep ${JOB_SLEEP_SECONDS:-0}; echo job-executed; exit ${JOB_EXIT_CODE:-0}\"]\n")
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0600, Size: int64(len(dockerfile))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(dockerfile); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	image := "oap-job-fixture:" + domain.NewID()
	req, err := http.NewRequestWithContext(ctx, "POST", "http://docker/v1.45/build?t="+image+"&pull=0", &archive)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-tar")
	response, err := engine.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(response.Body)
	for {
		var event map[string]any
		err = decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil || event["error"] != nil {
			response.Body.Close()
			t.Fatal("job fixture build failed", err)
		}
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("job fixture build", response.StatusCode)
	}
	imageID := request("GET", "/images/"+image+"/json", nil)["Id"].(string)
	target := domain.Target{ID: "jobs-" + domain.NewID()[:8], Operator: "docker", Environment: "staging", URL: "unix://" + socket, Settings: json.RawMessage(`{"pullPolicy":"never"}`)}
	adapter, err := docker.New(target)
	if err != nil {
		t.Fatal(err)
	}
	app := domain.NewID()
	for _, code := range []string{"0", "7"} {
		t.Run("exit-"+code, func(t *testing.T) {
			release := domain.NewID()
			spec := operator.Spec{Ownership: "OpenAppPlatform:" + app + ":migrate", Component: domain.Component{Name: "migrate", Kind: "worker", Image: imageID, Instances: 1, Env: map[string]string{"JOB_EXIT_CODE": code}}}
			resource, err := adapter.EnsureJob(ctx, spec, release)
			if err != nil {
				t.Fatal(err)
			}
			id, err := adapter.RunJob(ctx, resource.ID, release)
			if err != nil {
				t.Fatal(err)
			}
			var result docker.JobResult
			for {
				result, err = adapter.ObserveJob(ctx, resource.ID, release, id)
				if err != nil {
					t.Fatal(err)
				}
				if result.ExitCode != nil {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(100 * time.Millisecond):
				}
			}
			expected := "succeeded"
			if code != "0" {
				expected = "failed"
			}
			if result.State != expected || result.ExitCode == nil {
				t.Fatal(result)
			}
			before := request("GET", "/containers/"+id+"/json", nil)["State"].(map[string]any)
			prepared, err := adapter.EnsureJob(ctx, spec, release)
			if err != nil || prepared.ID != resource.ID {
				t.Fatal("job preparation replay", prepared, err)
			}
			changed := spec
			changed.Component.Env = map[string]string{"JOB_EXIT_CODE": "3"}
			if _, err := adapter.EnsureJob(ctx, changed, release); err == nil {
				t.Fatal("changed inputs replaced a completed job within its release")
			}
			replayed, err := adapter.RunJob(ctx, resource.ID, release)
			if err != nil || replayed != id {
				t.Fatal("job dispatch replay", replayed, err)
			}
			if _, err = adapter.Restart(ctx, resource.ID); err == nil {
				t.Fatal("job restart accepted")
			}
			after := request("GET", "/containers/"+id+"/json", nil)["State"].(map[string]any)
			if before["StartedAt"] != after["StartedAt"] || before["FinishedAt"] != after["FinishedAt"] {
				t.Fatal("completed job ran again")
			}
			logs, err := adapter.Logs(ctx, resource.ID, 10)
			if err != nil || strings.Count(logs, "job-executed") != 1 {
				t.Fatal("job execution evidence", logs, err)
			}
			next, err := adapter.EnsureJob(ctx, spec, domain.NewID())
			if err != nil || next.ID == resource.ID {
				t.Fatal("new release reused completed job", next, err)
			}
			if _, err = adapter.ObserveJob(ctx, resource.ID, release, "replacement"); err == nil {
				t.Fatal("replacement identity accepted")
			}
		})
	}
	t.Run("named-agent-release", func(t *testing.T) { testAgentPreDeployJobs(t, ctx, target, adapter, imageID, baseID) })
}
