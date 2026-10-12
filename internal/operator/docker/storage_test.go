package docker

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"strings"
	"testing"
)

func TestVolumeRetryAndOwnershipConflict(t *testing.T) {
	spec := operator.Spec{Ownership: "OpenAppPlatform:" + strings.Repeat("a", 32) + ":web", Component: domain.Component{Volumes: []domain.VolumeMount{{Name: "data", MountPath: "/data"}}}}
	var stored *volume
	creates := 0
	client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if stored == nil {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(stored)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1.45/volumes/create" {
			t.Errorf("unexpected storage mutation %s", r.Method)
			w.WriteHeader(500)
			return
		}
		creates++
		stored = &volume{}
		json.NewDecoder(r.Body).Decode(stored)
		json.NewEncoder(w).Encode(stored)
	})
	for i := 0; i < 2; i++ {
		mounts, err := client.mounts(context.Background(), spec)
		if err != nil || len(mounts) != 1 || mounts[0]["Target"] != "/data" {
			t.Fatal(mounts, err)
		}
	}
	if creates != 1 {
		t.Fatal("retry created duplicate volume")
	}
	stored.Labels[prefix+"owner"] = "foreign"
	if _, err := client.mounts(context.Background(), spec); err == nil || creates != 1 {
		t.Fatal("foreign volume adopted")
	}
	stored.Labels[prefix+"owner"] = spec.Ownership
	stored.Options = map[string]string{"device": "/host/data"}
	if _, err := client.mounts(context.Background(), spec); err == nil {
		t.Fatal("host-backed driver accepted")
	}
	other := spec
	other.Ownership = "OpenAppPlatform:" + strings.Repeat("b", 32) + ":web"
	if client.volumeName(other, "data") == client.volumeName(spec, "data") {
		t.Fatal("applications share a volume")
	}
}

func TestMissingReferencedVolumeHoldsWithoutRecreation(t *testing.T) {
	spec := operator.Spec{Name: "owned", Ownership: "OpenAppPlatform:" + strings.Repeat("a", 32) + ":web", Component: domain.Component{Volumes: []domain.VolumeMount{{Name: "data", MountPath: "/data"}}}}
	writes := 0
	client := fakeClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
			w.WriteHeader(500)
			return
		}
		if strings.Contains(r.URL.Path, "/volumes/") {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"Id": "owned", "Name": "/owned", "Config": map[string]any{"Labels": map[string]string{prefix + "target": "fixture", prefix + "environment": "staging", prefix + "owner": spec.Ownership, prefix + "reference": "owned"}}, "Mounts": []any{map[string]any{"Type": "volume", "Name": "missing", "Destination": "/data", "RW": true}}})
	})
	if _, err := client.mounts(context.Background(), spec); err == nil || writes != 0 {
		t.Fatal("missing referenced data silently recreated")
	}
}
