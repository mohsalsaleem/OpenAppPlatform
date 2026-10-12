package system_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator/docker"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/runtimeinputs"
)

// An in-memory Distribution v2 fixture serves one Linux image to the isolated
// Engine. The runner and Engine share only their disposable network namespace.
func TestNativePrivateImagePull(t *testing.T) {
	socket := os.Getenv("OAP_TEST_DOCKER_SOCKET")
	binary, err := os.ReadFile("/opt/oap/fixture-v1")
	if socket == "" || err != nil {
		t.Skip("isolated Docker and Linux fixture required")
	}
	digest := func(raw []byte) string { sum := sha256.Sum256(raw); return "sha256:" + hex.EncodeToString(sum[:]) }
	var layer bytes.Buffer
	archive := tar.NewWriter(&layer)
	if err := archive.WriteHeader(&tar.Header{Name: "fixture", Mode: 0755, Size: int64(len(binary))}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(binary); err != nil {
		t.Fatal(err)
	}
	archive.Close()
	diffID := digest(layer.Bytes())
	var compressed bytes.Buffer
	zip := gzip.NewWriter(&compressed)
	zip.Write(layer.Bytes())
	zip.Close()
	config, _ := json.Marshal(map[string]any{"architecture": runtime.GOARCH, "os": "linux", "config": map[string]any{"Entrypoint": []string{"/fixture"}, "User": "10001"}, "rootfs": map[string]any{"type": "layers", "diff_ids": []string{diffID}}, "history": []map[string]any{{"created_by": "isolated private registry fixture"}}})
	descriptor := func(media string, raw []byte) map[string]any {
		return map[string]any{"mediaType": media, "size": len(raw), "digest": digest(raw)}
	}
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.docker.distribution.manifest.v2+json", "config": descriptor("application/vnd.docker.container.image.v1+json", config), "layers": []map[string]any{descriptor("application/vnd.docker.image.rootfs.diff.tar.gzip", compressed.Bytes())}})
	var accepted, rejected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Distribution-Api-Version", "registry/2.0")
		user, password, ok := r.BasicAuth()
		if !ok || user != "reader" || password != "isolated-private-password" {
			rejected.Add(1)
			w.Header().Set("WWW-Authenticate", `Basic realm="isolated fixture"`)
			w.WriteHeader(401)
			return
		}
		accepted.Add(1)
		var raw []byte
		switch {
		case r.URL.Path == "/v2/":
			raw = []byte("{}")
		case strings.Contains(r.URL.Path, "/manifests/"):
			raw = manifest
			w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
		case strings.HasSuffix(r.URL.Path, digest(config)):
			raw = config
		case strings.HasSuffix(r.URL.Path, digest(compressed.Bytes())):
			raw = compressed.Bytes()
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Docker-Content-Digest", digest(raw))
		w.Header().Set("Content-Length", fmt.Sprint(len(raw)))
		if r.Method != "HEAD" {
			w.Write(raw)
		}
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	target := domain.Target{ID: "private-pull", Name: "Private fixture", Operator: "docker", URL: "unix://" + socket, Environment: "test", Settings: json.RawMessage(`{"pullPolicy":"if-missing"}`)}
	adapter, err := docker.New(target)
	if err != nil {
		t.Fatal(err)
	}
	appID := strings.Repeat("f", 32)
	component := domain.Component{Name: "web", Kind: "web", Image: host + "/team/private:v1", RegistryCredentialRef: "private-v1", Port: 8080, Instances: 1, Strategy: "standard"}
	spec := operator.Spec{Name: domain.ResourceName(appID, "web", 0), Ownership: "OpenAppPlatform:" + appID + ":web", Component: component}
	grant := runtimeinputs.RegistryBinding{Reference: "private-v1", TargetID: target.ID, ApplicationID: appID, Component: "web", Registry: host, UsernameEnv: "OAP_SECRET_REGISTRY_USER", PasswordEnv: "OAP_SECRET_REGISTRY_PASSWORD"}
	raw, _ := json.Marshal([]runtimeinputs.RegistryBinding{grant})
	missing, _ := runtimeinputs.AddRegistries(nil, raw, func(string) string { return "" })
	if _, err := missing.Resolve(target.ID, appID, component); err == nil {
		t.Fatal("missing accepted")
	}
	if accepted.Load() != 0 || rejected.Load() != 0 {
		t.Fatal("missing credentials dispatched")
	}
	invalid, _ := runtimeinputs.AddRegistries(nil, raw, func(key string) string {
		if key == grant.UsernameEnv {
			return "reader"
		}
		return "incorrect-password"
	})
	spec.Component, err = invalid.Resolve(target.ID, appID, component)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Ensure(context.Background(), spec); err == nil || strings.Contains(err.Error(), "incorrect-password") {
		t.Fatal("invalid credentials accepted or leaked", err)
	}
	if rejected.Load() == 0 || accepted.Load() != 0 {
		t.Fatal("fixture not rejecting invalid authentication")
	}
	valid, _ := runtimeinputs.AddRegistries(nil, raw, func(key string) string {
		if key == grant.UsernameEnv {
			return "reader"
		}
		return "isolated-private-password"
	})
	spec.Component, err = valid.Resolve(target.ID, appID, component)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := adapter.Ensure(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Load() == 0 {
		t.Fatal("private image not pulled")
	}
	if _, err := adapter.Deploy(context.Background(), resource.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		v, err := adapter.Inspect(context.Background(), resource.ID)
		if err == nil && strings.Contains(v.Status, "running") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	engine := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	inspect := func() string {
		response, err := engine.Get("http://docker/v1.45/containers/" + resource.ID + "/json")
		if err != nil {
			t.Fatal("container inspection failed")
		}
		defer response.Body.Close()
		var native struct {
			ID     string `json:"Id"`
			Config struct {
				Env    []string
				Labels map[string]string
			}
			State struct{ Running bool }
		}
		if json.NewDecoder(response.Body).Decode(&native) != nil || !native.State.Running {
			t.Fatal("private image did not run")
		}
		serialized, _ := json.Marshal(native.Config)
		if strings.Contains(string(serialized), "isolated-private-password") || strings.Contains(string(serialized), "X-Registry-Auth") {
			t.Fatal("pull credentials reached container configuration")
		}
		return native.ID
	}
	before := inspect()
	spec.Component.RegistryCredentialRef = "other-read-v1"
	auth := *spec.Component.RegistryAuth
	auth.Password = "another-private-password"
	spec.Component.RegistryAuth = &auth
	if _, err := adapter.Ensure(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if inspect() != before {
		t.Fatal("credential-only update replaced the unchanged runtime")
	}

}
