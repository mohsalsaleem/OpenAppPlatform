package coolify

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStoragePreparationRetriesAndPreservesNativeMounts(t *testing.T) {
	rows := []map[string]any{}
	creates := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/applications/native/storages" {
			t.Errorf("wrong storage scope %s", r.URL.Path)
		}
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"persistent_storages": rows, "file_storages": []any{}})
			return
		}
		if r.Method != "POST" {
			t.Errorf("unexpected storage mutation %s", r.Method)
			w.WriteHeader(500)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if len(body) != 3 || body["type"] != "persistent" {
			t.Error("unsafe storage fields")
		}
		creates++
		rows = append(rows, map[string]any{"name": "native-" + body["name"].(string), "mount_path": body["mount_path"], "host_path": nil})
		w.WriteHeader(201)
	}))
	defer srv.Close()
	client, err := New(domain.Target{URL: srv.URL}, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	spec := operator.Spec{Component: domain.Component{Volumes: []domain.VolumeMount{{Name: "data", MountPath: "/data"}}}}
	for i := 0; i < 2; i++ {
		if err := client.syncStorage(context.Background(), "native", spec); err != nil {
			t.Fatal(err)
		}
	}
	if creates != 1 {
		t.Fatal("duplicate volume")
	}
	rows[0]["mount_path"] = "/unexpected"
	if err := client.syncStorage(context.Background(), "native", spec); err == nil || creates != 1 {
		t.Fatal("native mount silently replaced")
	}
	rows = []map[string]any{{"name": "unrelated", "mount_path": "/other"}}
	if err := client.syncStorage(context.Background(), "native", spec); err == nil {
		t.Fatal("foreign mount taken over")
	}
	data := map[string]any{"limits_memory": "native"}
	applyLimits(data, domain.Component{})
	if data["limits_memory"] != "native" {
		t.Fatal("unspecified limits overwritten")
	}
	applyLimits(data, domain.Component{Limits: &domain.ResourceLimits{MemoryMiB: 256, CPUMillis: 500}})
	if data["limits_memory"] != "256m" || data["limits_cpus"] != "0.500" {
		t.Fatal(data)
	}
}
