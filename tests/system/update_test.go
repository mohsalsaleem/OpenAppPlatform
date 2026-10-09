package system_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

func TestConfigurationVersionPreventsLostUpdatesAndPreservesRelease(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, srv := setup(t, f)
	ctx := context.Background()
	app, e := c.CreateApplication(ctx, manifest())
	if e != nil {
		t.Fatal(e)
	}
	d, e := c.Enqueue(ctx, app.ID, "before-configuration-edit")
	if e != nil {
		t.Fatal(e)
	}
	changed := app.Manifest
	changed.Components = append([]domain.Component(nil), app.Manifest.Components...)
	changed.Components[0].Image = "nginx:1.28-alpine"
	status, raw := request(t, srv, "PUT", "/api/v1/applications/"+app.ID, "", map[string]any{"manifest": changed, "expectedVersion": app.Version})
	if status != 200 {
		t.Fatalf("update %d %s", status, raw)
	}
	var updated domain.Application
	json.Unmarshal(raw, &updated)
	if updated.Version != app.Version+1 {
		t.Fatal("version did not increment")
	}
	status, _ = request(t, srv, "PUT", "/api/v1/applications/"+app.ID, "", map[string]any{"manifest": app.Manifest, "expectedVersion": app.Version})
	if status != 409 {
		t.Fatal("stale update was accepted")
	}
	frozen, e := c.Store.Deployment(ctx, d.ID)
	if e != nil || frozen.Manifest.Components[0].Image != app.Manifest.Components[0].Image {
		t.Fatal("edit changed an existing release")
	}
}
func TestConcurrentConfigurationEditsHaveOneWinner(t *testing.T) {
	c, _ := setup(t, &fake{resources: map[string]operator.Resource{}})
	ctx := context.Background()
	app, e := c.CreateApplication(ctx, manifest())
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	success, conflict := 0, 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := c.UpdateApplication(ctx, app.ID, app.Manifest, app.Version)
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				success++
			} else if e == domain.ErrConflict {
				conflict++
			} else {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if success != 1 || conflict != 7 {
		t.Fatalf("lost update protection failed: %d %d", success, conflict)
	}
}
func TestConfigurationCannotSilentlyRemoveOrRetireInstances(t *testing.T) {
	c, _ := setup(t, &fake{resources: map[string]operator.Resource{}})
	ctx := context.Background()
	app, e := c.CreateApplication(ctx, manifest())
	if e != nil {
		t.Fatal(e)
	}
	m := app.Manifest
	m.Components = append([]domain.Component(nil), m.Components...)
	m.Components[0].Instances = 1
	if _, e := c.UpdateApplication(ctx, app.ID, m, app.Version); e == nil {
		t.Fatal("scale down accepted without retirement")
	}
	m.TargetID = "another-target"
	if _, e := c.UpdateApplication(ctx, app.ID, m, app.Version); e == nil {
		t.Fatal("target migration accepted")
	}
}
func TestMigrationsAreIdempotentAndChecksummed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	if e := s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := s.Pool.QueryRow(ctx, "SELECT count(*) FROM oap_schema_migrations").Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count < 2 {
		t.Fatal(count)
	}
	if _, e := s.Pool.Exec(ctx, "UPDATE oap_schema_migrations SET checksum='altered' WHERE version=1"); e != nil {
		t.Fatal(e)
	}
	if e := s.Migrate(ctx); e == nil {
		t.Fatal("modified migration accepted")
	}
}

func TestReleasePreconditionAndDuplicateRetryAfterConfigurationChange(t *testing.T) {
	c, _ := setup(t, &fake{resources: map[string]operator.Resource{}})
	ctx := context.Background()
	app, e := c.CreateApplication(ctx, manifest())
	if e != nil {
		t.Fatal(e)
	}
	d, e := c.EnqueueVersion(ctx, app.ID, "versioned-release", nil, app.Version)
	if e != nil {
		t.Fatal(e)
	}
	if d.DefinitionVersion != app.Version {
		t.Fatal("definition version not captured")
	}
	changed := app.Manifest
	changed.Components = append([]domain.Component(nil), changed.Components...)
	changed.Components[0].Image = "nginx:1.28-alpine"
	updated, e := c.UpdateApplication(ctx, app.ID, changed, app.Version)
	if e != nil {
		t.Fatal(e)
	}
	retry, e := c.EnqueueVersion(ctx, app.ID, "versioned-release", nil, app.Version)
	if e != nil || retry.ID != d.ID {
		t.Fatal("duplicate retry lost after edit", e)
	}
	if _, e := c.EnqueueVersion(ctx, app.ID, "stale-new-release", nil, app.Version); e != domain.ErrConflict {
		t.Fatalf("stale plan accepted %v", e)
	}
	if updated.Version != app.Version+1 {
		t.Fatal("definition version unchanged")
	}
}
