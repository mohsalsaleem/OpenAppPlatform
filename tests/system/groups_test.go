package system_test

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"testing"
)

func TestApplicationEnvironmentLinkPreservesScopesAndHistory(t *testing.T) {
	f := &fake{resources: map[string]operator.Resource{}}
	c, srv := setup(t, f)
	ctx := context.Background()
	stage := manifest()
	stage.Name = "linked-stage"
	stage.Components[0].Instances = 1
	a, e := c.CreateApplication(ctx, stage)
	if e != nil {
		t.Fatal(e)
	}
	prod := stage
	prod.Name = "linked-prod"
	prod.Environment = "production"
	// The fake target is staging; use the store to model a separate pre-existing
	// environment. No provider work is involved in this metadata transaction.
	b, e := c.Store.CreateApplication(ctx, prod)
	if e != nil {
		t.Fatal(e)
	}
	d, e := c.Enqueue(ctx, a.ID, "before-grouping")
	if e != nil {
		t.Fatal(e)
	}
	before, _ := c.Store.Application(ctx, a.ID)
	code, _ := request(t, srv, http.MethodPost, "/api/v1/application-groups/"+a.GroupID+"/environments", "", map[string]any{"applicationId": b.ID, "expectedGroupVersion": 1, "expectedSourceVersion": 1})
	if code != 200 {
		t.Fatal("link failed", code)
	}
	after, _ := c.Store.Application(ctx, a.ID)
	member, _ := c.Store.Application(ctx, b.ID)
	history, _ := c.Store.Deployment(ctx, d.ID)
	if after.ID != before.ID || after.Version != before.Version || member.GroupID != a.GroupID || history.ApplicationID != a.ID || f.deploys != 0 {
		t.Fatal("metadata linking changed runtime/history")
	}
	groups, e := c.Store.ApplicationGroups(ctx)
	if e != nil || len(groups) != 1 || len(groups[0].Environments) != 2 || groups[0].Environments[0].Manifest.Environment != "staging" {
		t.Fatal("group projection", e)
	}
	// A stale version or a duplicate environment cannot replace an existing member.
	third := stage
	third.Name = "another-stage"
	x, e := c.Store.CreateApplication(ctx, third)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Store.LinkEnvironment(ctx, a.GroupID, x.ID, 1, 1); e != domain.ErrConflict {
		t.Fatal("stale parent accepted", e)
	}
	if e = c.Store.LinkEnvironment(ctx, a.GroupID, x.ID, 2, 1); e != domain.ErrConflict {
		t.Fatal("duplicate environment accepted", e)
	}
	current, _ := c.Store.Application(ctx, x.ID)
	if current.GroupID != x.ID {
		t.Fatal("failed link changed source")
	}
	if e = c.Store.UnlinkEnvironment(ctx, a.GroupID, b.ID, 1); e != domain.ErrConflict {
		t.Fatal("stale unlink accepted", e)
	}
	if e = c.Store.UnlinkEnvironment(ctx, a.GroupID, b.ID, 2); e != nil {
		t.Fatal(e)
	}
	detached, _ := c.Store.Application(ctx, b.ID)
	if detached.ID != b.ID || detached.GroupID == a.GroupID {
		t.Fatal("unlink changed environment identity")
	}
	preserved, _ := c.Store.Deployment(ctx, d.ID)
	if preserved.ID != d.ID || f.deploys != 0 {
		t.Fatal("unlink changed releases/runtime")
	}

}

func TestConcurrentEnvironmentLinksHaveOneVersionWinner(t *testing.T) {
	c, _ := setup(t, &fake{resources: map[string]operator.Resource{}})
	ctx := context.Background()
	m := manifest()
	m.Name = "concurrent-parent"
	parent, e := c.Store.CreateApplication(ctx, m)
	if e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	for _, environment := range []string{"production", "preview"} {
		candidate := m
		candidate.Name = "concurrent-" + environment
		candidate.Environment = environment
		a, e := c.Store.CreateApplication(ctx, candidate)
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, a.ID)
	}
	results := make(chan error, 2)
	for _, id := range ids {
		go func(id string) { results <- c.Store.LinkEnvironment(ctx, parent.GroupID, id, 1, 1) }(id)
	}
	wins, conflicts := 0, 0
	for range ids {
		e := <-results
		if e == nil {
			wins++
		} else if e == domain.ErrConflict {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("version precondition did not serialize links", wins, conflicts)
	}
}
