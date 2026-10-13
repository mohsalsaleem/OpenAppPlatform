package system_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
)

type targetStateFake struct {
	*fake
	discoveryErr error
	block        bool
}

func (f *targetStateFake) Discover(ctx context.Context) ([]operator.Resource, error) {
	if f.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.discoveryErr != nil {
		return nil, f.discoveryErr
	}
	return f.fake.Discover(ctx)
}
func TestTargetObservationOutageRestartStalenessAndSafeRecheck(t *testing.T) {
	ctx := context.Background()
	f := &targetStateFake{fake: &fake{resources: map[string]operator.Resource{}}}
	c, srv := setup(t, f.fake)
	c.Factory = func(domain.Target) (operator.Adapter, error) { return f, nil }
	state, err := c.TargetConnection(ctx, "fixture")
	if err != nil || state.State != "unknown" {
		t.Fatal("unchecked state", state, err)
	}
	code, _ := request(t, srv, "POST", "/api/v1/targets/fixture/connection-checks", "", map[string]any{})
	if code != 200 {
		t.Fatal("check API failed", code)
	}
	state, err = c.TargetConnection(ctx, "fixture")
	if err != nil || state.State != "connected" || state.ResourceCount == nil || *state.ResourceCount != 0 {
		t.Fatal("empty connected scope lost", state, err)
	}
	// An already-held operation is never redispatched by a connection recheck.
	app, err := c.CreateApplication(ctx, manifest())
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Enqueue(ctx, app.ID, "target-held-fixture")
	if err != nil {
		t.Fatal(err)
	}
	d.State = "attention"
	d.Steps[0].Phase = "attention"
	if err = c.Store.SaveDeployment(ctx, d); err != nil {
		t.Fatal(err)
	}
	d, err = c.Store.Deployment(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.discoveryErr = operator.ErrUnreachable
	state, err = c.CheckTarget(ctx, "fixture")
	if err != nil || state.State != "unreachable" || len(state.HeldOperations) != 1 || state.HeldOperations[0].ID != d.ID {
		t.Fatal("outage/recovery link lost", state, err)
	}
	c2, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	state, err = c2.TargetConnection(ctx, "fixture")
	if err != nil || state.State != "unreachable" {
		t.Fatal("restart lost outage evidence", err)
	}
	f.discoveryErr = errors.New("private upstream error with secret fixture-value")
	state, err = c2.CheckTarget(ctx, "fixture")
	raw, _ := json.Marshal(state)
	if err != nil || state.State != "failed" || string(raw) == "" {
		t.Fatal("reachable rejection classification", state, err)
	}
	if bytes.Contains(raw, []byte("fixture-value")) {
		t.Fatal("upstream detail leaked")
	}
	f.discoveryErr = nil
	state, err = c2.CheckTarget(ctx, "fixture")
	if err != nil || state.State != "connected" || f.deploys != 0 || len(f.resources) != 0 {
		t.Fatal("recheck dispatched provider work", err)
	}
	held, err := c.Store.Deployment(ctx, d.ID)
	if err != nil || held.State != "attention" || !held.UpdatedAt.Equal(d.UpdatedAt) {
		t.Fatal("recheck altered held operation", err)
	}
	past := time.Now().Add(-time.Minute)
	state.ExpiresAt = &past
	raw, _ = json.Marshal(state)
	if _, err = c.Store.Pool.Exec(ctx, "UPDATE oap_target_observations SET summary=$1 WHERE target_id='fixture'", raw); err != nil {
		t.Fatal(err)
	}
	state, err = c2.TargetConnection(ctx, "fixture")
	if err != nil || state.State != "stale" || state.ObservedState != "connected" {
		t.Fatal("stale result looked live", state, err)
	}
	target, err := c.Store.Target(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	target.Environment = "other"
	if err = c.Store.SaveTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	state, err = c2.TargetConnection(ctx, "fixture")
	if err != nil || state.State != "unknown" || state.CheckedAt != nil {
		t.Fatal("changed scope reused old evidence", state, err)
	}
	// Respect a caller's tighter deadline without waiting the ten-second probe bound.
	f.block = true
	deadline, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = c2.CheckTarget(deadline, "fixture")
	if err == nil || time.Since(started) > time.Second {
		t.Fatal("cancelled caller exceeded its deadline", err)
	}
}
