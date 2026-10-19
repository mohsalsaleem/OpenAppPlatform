package gitrepo

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/contract"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
)

var ctx = context.Background()

func app(image string) any {
	return map[string]any{"api_version": "oap.dev/v1", "kind": "Application", "name": "site", "environment": "production",
		"components": map[string]any{"web": map[string]any{"kind": "web", "image": image, "port": int64(8080)}}}
}

func open(t *testing.T) *Repo {
	t.Helper()
	r, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestContract(t *testing.T) {
	contract.Desired(t, func(t *testing.T) ports.DesiredRepo { return open(t) }, func(image string) any {
		a := app(image).(map[string]any)
		a["name"] = "contract"
		return a
	})
}

func TestCommitsCarryAuthorAndMessage(t *testing.T) {
	r := open(t)
	h1, _ := r.Commit(ctx, "site", app("web:1"), "owner", "Create site")
	if again, _ := r.Commit(ctx, "site", app("web:1"), "agent:claude", "No change"); again != h1 {
		t.Fatal("same tree, different hash")
	}
	h2, _ := r.Commit(ctx, "site", app("web:2"), "agent:claude", "Deploy web:2\n\nBecause the owner asked.")
	vs, err := r.History(ctx, "site")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 || vs[0].Hash != h2 || vs[1].Hash != h1 || vs[0].Author != "agent:claude" || vs[0].Message != "Deploy web:2" ||
		vs[1].Author != "owner" || vs[1].At.IsZero() || len(vs[0].Commit) != 40 {
		t.Fatalf("history %+v", vs)
	}
	full, _ := exec.Command("git", "-C", r.Dir(), "log", "-1", "--format=%B").Output()
	if !strings.Contains(string(full), "Because the owner asked.") || !strings.Contains(string(full), "Spec-Hash: "+h2) {
		t.Fatalf("message %q", full)
	}
}

func TestDiffAndRevert(t *testing.T) {
	r := open(t)
	h1, _ := r.Commit(ctx, "site", app("web:1"), "owner", "Create")
	h2, _ := r.Commit(ctx, "site", app("web:2"), "owner", "Deploy")
	changes, err := r.Diff(ctx, h1, h2)
	if err != nil || len(changes) != 1 || changes[0].Path != "components.web.image" || changes[0].Before != "web:1" || changes[0].After != "web:2" {
		t.Fatalf("diff %+v %v", changes, err)
	}
	h3, err := r.Revert(ctx, "site", h1, "owner", "Roll back")
	if err != nil || h3 != h1 {
		t.Fatalf("revert %s %v", h3, err)
	}
	if _, cur, _ := r.Get(ctx, "site"); cur != h1 {
		t.Fatalf("current %s, want %s", cur, h1)
	}
	vs, _ := r.History(ctx, "site")
	if len(vs) != 3 || vs[0].Message != "Roll back" {
		t.Fatalf("history after revert %+v", vs)
	}
	other := app("x:1").(map[string]any)
	other["name"] = "other"
	ho, _ := r.Commit(ctx, "other", other, "owner", "Create other")
	if _, err := r.Revert(ctx, "site", ho, "owner", "wrong app"); err == nil {
		t.Fatal("reverting to another application's version must fail")
	}
}

// TestExportIsOneWay: the mirror gets every commit; changes made in the
// mirror never come back and are overwritten by the next export.
func TestExportIsOneWay(t *testing.T) {
	r := open(t)
	r.Commit(ctx, "site", app("web:1"), "owner", "Create")
	mirror := filepath.Join(t.TempDir(), "mirror.git")
	if b, err := exec.Command("git", "init", "-q", "--bare", "-b", "main", mirror).CombinedOutput(); err != nil {
		t.Fatal(err, string(b))
	}
	if err := r.Export(ctx, mirror); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	run := func(dir string, args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=x", "-c", "user.email=x@x"}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(err, string(b))
		}
		return string(b)
	}
	run(".", "clone", "-q", mirror, clone)
	run(clone, "commit", "-q", "--allow-empty", "-m", "edited in the mirror")
	run(clone, "push", "-q", "origin", "main")
	r.Commit(ctx, "site", app("web:2"), "owner", "Deploy")
	if vs, _ := r.History(ctx, "site"); len(vs) != 2 {
		t.Fatal("the mirror's commit must not reach the repository")
	}
	if err := r.Export(ctx, mirror); err != nil {
		t.Fatal(err)
	}
	log := run(mirror, "log", "--format=%s", "main")
	if strings.Contains(log, "edited in the mirror") || !strings.HasPrefix(log, "Deploy") {
		t.Fatalf("mirror log after export:\n%s", log)
	}
}

func TestEnvelopeDefaultsAndVersions(t *testing.T) {
	r := open(t)
	_, v1, err := r.Envelope(ctx)
	if err != nil || !strings.HasPrefix(v1, "sha256:") {
		t.Fatal(v1, err)
	}
	_, again, _ := r.Envelope(ctx)
	if again != v1 {
		t.Fatal("default envelope version isn't stable")
	}
	env := map[string]any{"agents": map[string]any{}}
	v2, err := r.SetEnvelope(ctx, env, "owner", "Lock down agents")
	if err != nil || v2 == v1 {
		t.Fatal(v2, err)
	}
	if _, cur, _ := r.Envelope(ctx); cur != v2 {
		t.Fatal("committed envelope not current")
	}
}

func TestReopenKeepsState(t *testing.T) {
	dir := t.TempDir()
	r, _ := Open(ctx, dir)
	h, _ := r.Commit(ctx, "site", app("web:1"), "owner", "Create")
	r2, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, cur, _ := r2.Get(ctx, "site"); cur != h {
		t.Fatal("state lost on reopen")
	}
	if apps, _ := r2.Applications(ctx); len(apps) != 1 || apps[0] != "site" {
		t.Fatalf("applications %v", apps)
	}
}
