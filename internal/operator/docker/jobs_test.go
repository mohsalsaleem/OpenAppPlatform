package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

const testJobRelease = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const testJobRef = "oap-job-" + testJobRelease + "-migrate"

func jobContainer(state string, code int) container {
	v := container{ID: "job-container"}
	v.Config.Labels = map[string]string{prefix + "target": "fixture", prefix + "environment": "staging", prefix + "reference": testJobRef, prefix + "job-release": testJobRelease}
	v.Config.Labels[prefix+"owner"] = "OpenAppPlatform:" + strings.Repeat("b", 32) + ":migrate"
	v.Config.Healthcheck = &nativeHealthCheck{Test: []string{"NONE"}}
	v.HostConfig.RestartPolicy.Name = "no"
	v.State.Status = state
	v.State.Running = state == "running"
	v.State.ExitCode = code
	return v
}

func TestJobCompletionEvidence(t *testing.T) {
	for _, tc := range []struct {
		native   string
		code     int
		expected string
		terminal bool
	}{
		{"created", 0, "pending", false}, {"running", 0, "running", false},
		{"exited", 0, "succeeded", true}, {"exited", 9, "failed", true},
		{"dead", 0, "failed", true},
	} {
		t.Run(tc.native+tc.expected, func(t *testing.T) {
			c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Fatal("observation mutated Docker")
				}
				json.NewEncoder(w).Encode(jobContainer(tc.native, tc.code))
			})
			result, err := c.ObserveJob(context.Background(), testJobRef, testJobRelease, "job-container")
			if err != nil || result.State != tc.expected || (result.ExitCode != nil) != tc.terminal {
				t.Fatal(result, err)
			}
			if result.ExitCode != nil && *result.ExitCode != tc.code {
				t.Fatal("exit code lost")
			}
		})
	}
}

func TestJobReplayDoesNotRestartCompletedOrRunningProcess(t *testing.T) {
	for _, state := range []string{"running", "exited", "dead"} {
		t.Run(state, func(t *testing.T) {
			mutations := 0
			c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					mutations++
					t.Error("replay wrote to Docker")
				}
				if strings.HasSuffix(r.URL.Path, "-next/json") {
					http.NotFound(w, r)
					return
				}
				json.NewEncoder(w).Encode(jobContainer(state, 0))
			})
			for i := 0; i < 2; i++ {
				id, err := c.RunJob(context.Background(), testJobRef, testJobRelease)
				if err != nil || id != "job-container" {
					t.Fatal(id, err)
				}
				// The normal workload helper also must not accidentally restart a job.
				if _, err = c.Deploy(context.Background(), testJobRef); err != nil {
					t.Fatal(err)
				}
			}
			if mutations != 0 {
				t.Fatal("job was restarted")
			}
		})
	}
}

func TestJobObservationRejectsChangedIdentityAndRuntime(t *testing.T) {
	for _, change := range []func(*container){
		func(v *container) { v.ID = "replacement" },
		func(v *container) { v.Config.Labels[prefix+"target"] = "foreign" },
		func(v *container) { v.Config.Labels[prefix+"job-release"] = strings.Repeat("b", 32) },
		func(v *container) { v.HostConfig.RestartPolicy.Name = "always" },
		func(v *container) { v.Config.Healthcheck = nil },
	} {
		v := jobContainer("exited", 0)
		change(&v)
		c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(v) })
		if _, err := c.ObserveJob(context.Background(), testJobRef, testJobRelease, "job-container"); err == nil {
			t.Fatal("changed job accepted")
		}
		if _, err := c.RunJob(context.Background(), testJobRef, testJobRelease); err == nil && v.ID != "replacement" {
			t.Fatal("changed job dispatched")
		}
	}
}

func TestJobPreparationDisablesRestartHealthAndServiceAlias(t *testing.T) {
	var creation map[string]any
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/images/"):
			json.NewEncoder(w).Encode(map[string]string{"Id": "sha256:cached"})
		case strings.Contains(r.URL.Path, "/networks/"):
			json.NewEncoder(w).Encode(map[string]any{"Id": "network", "Labels": map[string]string{prefix + "target": "fixture", prefix + "application": strings.Repeat("b", 32)}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/containers/create"):
			if err := json.NewDecoder(r.Body).Decode(&creation); err != nil {
				t.Fatal(err)
			}
			if r.URL.Query().Get("name") != testJobRef+"-next" {
				t.Fatal("job is not release scoped")
			}
			json.NewEncoder(w).Encode(map[string]string{"Id": "prepared"})
		default:
			http.NotFound(w, r)
		}
	})
	resource, err := c.EnsureJob(context.Background(), operator.Spec{Ownership: "OpenAppPlatform:" + strings.Repeat("b", 32) + ":migrate", Component: domain.Component{Name: "migrate", Kind: "worker", Image: "cached", Instances: 1}}, testJobRelease)
	if err != nil || resource.ID != testJobRef {
		t.Fatal(resource, err)
	}
	host := creation["HostConfig"].(map[string]any)
	if host["RestartPolicy"].(map[string]any)["Name"] != "no" {
		t.Fatal("job restarts automatically")
	}
	if creation["Healthcheck"].(map[string]any)["Test"].([]any)[0] != "NONE" {
		t.Fatal("image health check retained")
	}
	if _, ok := creation["ExposedPorts"]; ok {
		t.Fatal("job exposes ports")
	}
	endpoints := creation["NetworkingConfig"].(map[string]any)["EndpointsConfig"].(map[string]any)
	for _, endpoint := range endpoints {
		if len(endpoint.(map[string]any)) != 0 {
			t.Fatal("job has a service alias")
		}
	}
}

func TestInvalidJobSpecRejectedBeforeDockerCalls(t *testing.T) {
	c := fakeClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid job contacted Docker") })
	for _, component := range []domain.Component{
		{Name: "migrate", Kind: "worker", Instances: 2},
		{Name: "migrate", Kind: "web", Instances: 1, Port: 8080},
		{Name: "migrate", Kind: "worker", Instances: 1, HealthCheck: &domain.HealthCheck{Mode: "image"}},
	} {
		if _, err := c.EnsureJob(context.Background(), operator.Spec{Ownership: "OpenAppPlatform:" + strings.Repeat("b", 32) + ":migrate", Component: component}, testJobRelease); err == nil {
			t.Fatal("invalid job accepted")
		}
	}
}
