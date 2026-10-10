package system_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"sync"
	"testing"
	"time"
)

func TestIndependentComponentReleasesPersistClaimsAndRecoverTogether(t *testing.T) {
	c, srv := setup(t, &fake{resources: map[string]operator.Resource{}})
	ctx := context.Background()
	m := manifest()
	m.Components[0].Instances = 1
	m.Components = append(m.Components, domain.Component{Name: "api", Image: "nginx:alpine", Port: 80, Instances: 1, Strategy: "standard"})
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	selected := []string{"web", "api"}
	releases := make([]domain.Deployment, 2)
	var wg sync.WaitGroup
	for i, name := range selected {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			code, raw := request(t, srv, "POST", "/api/v1/applications/"+app.ID+"/deployments", "component-release-"+name, map[string]any{"expectedVersion": app.Version, "components": []string{name}})
			if code != 202 {
				t.Errorf("independent admission %s: %d %s", name, code, raw)
				return
			}
			if err := json.Unmarshal(raw, &releases[i]); err != nil {
				t.Error(err)
			}
		}(i, name)
	}
	wg.Wait()
	for i, release := range releases {
		if len(release.Steps) != 1 || release.Steps[0].Component != selected[i] {
			t.Fatal("selection expanded", release)
		}
	}
	duplicate, err := c.EnqueueSelected(ctx, app.ID, "component-release-web", nil, app.Version, []string{"web"})
	if err != nil || duplicate.ID != releases[0].ID {
		t.Fatal("idempotent retry", err)
	}
	if _, err = c.EnqueueSelected(ctx, app.ID, "same-component-overlap", nil, app.Version, []string{"web"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("overlapping writer allowed", err)
	}
	if _, err = c.Enqueue(ctx, app.ID, "whole-application-overlap"); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("whole application overlap allowed", err)
	}
	// Restart the coordinator before either queued operation starts.
	restarted, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	restarted.PollInterval = 10 * time.Millisecond
	if err = restarted.Jobs.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer restarted.Jobs.Stop(ctx)
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		done := true
		for _, release := range releases {
			fresh, err := c.Store.Deployment(ctx, release.ID)
			if err != nil {
				t.Fatal(err)
			}
			if fresh.State != "succeeded" {
				done = false
			}
		}
		if done {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, release := range releases {
		fresh, err := c.Store.Deployment(ctx, release.ID)
		if err != nil || fresh.State != "succeeded" {
			t.Fatal("durable component recovery", fresh.State, err)
		}
	}
	if _, err = c.EnqueueSelected(ctx, app.ID, "after-terminal-release", nil, app.Version, []string{"web"}); err != nil {
		t.Fatal("completed claims still fence", err)
	}
}

func TestDependencyClaimsFenceWritersAndAttentionAfterRestart(t *testing.T) {
	c, _ := setup(t, &fake{resources: map[string]operator.Resource{}})
	ctx := context.Background()
	m := manifest()
	m.Components[0].DependsOn = []string{"api"}
	m.Components = append(m.Components, domain.Component{Name: "api", Image: "nginx:alpine", Port: 80, Instances: 1, Strategy: "standard"}, domain.Component{Name: "side", Image: "nginx:alpine", Port: 80, Instances: 1, Strategy: "standard"})
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	release, err := c.EnqueueSelected(ctx, app.ID, "dependent-reader-release", nil, app.Version, []string{"web"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.EnqueueSelected(ctx, app.ID, "dependency-writer-release", nil, app.Version, []string{"api"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("dependency mutated during reader release", err)
	}
	release.State = "attention"
	if err = c.Store.SaveDeployment(ctx, release); err != nil {
		t.Fatal(err)
	}
	restarted, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.EnqueueSelected(ctx, app.ID, "attention-writer-release", nil, app.Version, []string{"api"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("attention dependency fence lost", err)
	}
	if _, err = restarted.EnqueueSelected(ctx, app.ID, "unrelated-attention-release", nil, app.Version, []string{"side"}); err != nil {
		t.Fatal("unrelated component blocked", err)
	}
}
