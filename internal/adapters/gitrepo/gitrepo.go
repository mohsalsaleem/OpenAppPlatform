// Package gitrepo keeps desired state in a Git repository of YAML
// (docs/architecture/agent-native-rewrite.md, section 9): one file per
// application under apps/, and envelope.yaml. Every commit carries its
// author and message, and trailers naming the application and the spec's
// content hash, so earlier versions can be found by hash. It runs the
// system git; no Git library is linked.
package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/desiredyaml"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

// ErrNotFound is returned for an application with no desired state.
var ErrNotFound = ops.ErrNoApplication

// DefaultEnvelope is the envelope of a fresh install, until the owner
// commits one: the owner may do anything, no agent may do anything yet,
// and volumes are snapshotted before they change.
var DefaultEnvelope = map[string]any{
	"agents": map[string]any{
		"owner": map[string]any{
			"scope": map[string]any{"applications": []any{"*"}},
			"may": []any{"scale", "deploy", "rollback", "configure", "add-component", "remove-component", "spec-patch",
				"install-extension", "remove-extension", "build"},
		},
	},
	"invariants": []any{
		map[string]any{"snapshot_before_volume_change": true},
		map[string]any{"within_quotas": true},
	},
	"breakers": map[string]any{"failed_applies_per_hour": int64(3)},
}

var appName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Repo is a desired-state repository in a directory.
type Repo struct {
	dir string
	mu  sync.Mutex
}

// Open opens the repository in dir, creating it if needed.
func Open(ctx context.Context, dir string) (*Repo, error) {
	if err := os.MkdirAll(filepath.Join(dir, "apps"), 0o700); err != nil {
		return nil, err
	}
	r := &Repo{dir: dir}
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if _, err := r.git(ctx, nil, "init", "-q", "-b", "main"); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Dir is the repository's directory.
func (r *Repo) Dir() string { return r.dir }

func (r *Repo) git(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.dir, "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_COMMITTER_NAME=OAP", "GIT_COMMITTER_EMAIL=oap@localhost"), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("gitrepo: git %s: %v %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func appPath(app string) string { return filepath.Join("apps", app+".yaml") }

func (r *Repo) read(app string) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(r.dir, appPath(app)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return b, err
}

func parse(b []byte) (any, string, error) {
	tree, probs := desiredyaml.Parse(b)
	if len(probs) > 0 {
		return nil, "", fmt.Errorf("gitrepo: stored desired state is invalid: %s", probs[0].Message)
	}
	h, err := spec.Hash(tree)
	return tree, h, err
}

// Get returns an application's current desired state and its hash.
func (r *Repo) Get(ctx context.Context, app string) (any, string, error) {
	if !appName.MatchString(app) {
		return nil, "", ErrNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := r.read(app)
	if err != nil {
		return nil, "", err
	}
	return parse(b)
}

// Applications lists the applications with desired state, sorted.
func (r *Repo) Applications(ctx context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	matches, err := filepath.Glob(filepath.Join(r.dir, "apps", "*.yaml"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range matches {
		out = append(out, strings.TrimSuffix(filepath.Base(m), ".yaml"))
	}
	return out, nil
}

// Commit stores a tree as an application's desired state and returns its
// hash. Committing the tree already there makes no commit.
func (r *Repo) Commit(ctx context.Context, app string, tree any, author, message string) (string, error) {
	if !appName.MatchString(app) {
		return "", fmt.Errorf("gitrepo: %q isn't an application name", app)
	}
	b, err := desiredyaml.Emit(tree)
	if err != nil {
		return "", err
	}
	h, err := spec.Hash(tree)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, err := r.read(app); err == nil && bytes.Equal(cur, b) {
		return h, nil
	}
	if err := os.WriteFile(filepath.Join(r.dir, appPath(app)), b, 0o600); err != nil {
		return "", err
	}
	return h, r.commit(ctx, appPath(app), author, message, "Application: "+app, "Spec-Hash: "+h)
}

func (r *Repo) commit(ctx context.Context, path, author, message string, trailers ...string) error {
	if _, err := r.git(ctx, nil, "add", "-A", "--", path); err != nil {
		return err
	}
	if strings.TrimSpace(message) == "" {
		message = "Update " + path
	}
	msg := strings.TrimSpace(message) + "\n\n" + strings.Join(trailers, "\n") + "\n"
	name := strings.NewReplacer("<", "", ">", "", "\n", " ").Replace(author)
	if name == "" {
		name = "unknown"
	}
	_, err := r.git(ctx, []string{"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + name + "@oap.invalid"},
		"commit", "-q", "--no-verify", "-m", msg, "--", path)
	return err
}

// ByHash returns an earlier desired state of any application by its hash.
func (r *Repo) ByHash(ctx context.Context, hash string) (any, bool, error) {
	if !strings.HasPrefix(hash, "sha256:") || strings.ContainsAny(hash, " \n") {
		return nil, false, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out, err := r.git(ctx, nil, "log", "--all", "-F", "--grep=Spec-Hash: "+hash, "--format=%H%x00%(trailers:key=Application,valueonly,separator=)")
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return nil, false, nil
		}
		return nil, false, err
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		commit, app, ok := strings.Cut(line, "\x00")
		if !ok || !appName.MatchString(strings.TrimSpace(app)) {
			continue
		}
		b, err := r.git(ctx, nil, "show", commit+":"+appPath(strings.TrimSpace(app)))
		if err != nil {
			continue
		}
		tree, h, err := parse([]byte(b))
		if err == nil && h == hash {
			return tree, true, nil
		}
	}
	return nil, false, nil
}

// Envelope returns the envelope and its version: the hash of its canonical
// form. Without a committed envelope it is DefaultEnvelope.
func (r *Repo) Envelope(ctx context.Context) (any, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(r.dir, "envelope.yaml"))
	tree := any(DefaultEnvelope)
	if err == nil {
		t, probs := desiredyaml.Parse(b)
		if len(probs) > 0 {
			return nil, "", fmt.Errorf("gitrepo: envelope.yaml is invalid: %s", probs[0].Message)
		}
		tree = t
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	h, err := spec.Hash(tree)
	return tree, h, err
}

// SetEnvelope commits a new envelope.
func (r *Repo) SetEnvelope(ctx context.Context, tree any, author, message string) (string, error) {
	b, err := desiredyaml.Emit(tree)
	if err != nil {
		return "", err
	}
	h, err := spec.Hash(tree)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.WriteFile(filepath.Join(r.dir, "envelope.yaml"), b, 0o600); err != nil {
		return "", err
	}
	return h, r.commit(ctx, "envelope.yaml", author, message, "Envelope-Version: "+h)
}

// Extensions returns extensions.yaml and its hash; empty when absent.
func (r *Repo) Extensions(ctx context.Context) (any, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	tree := any(map[string]any{})
	b, err := os.ReadFile(filepath.Join(r.dir, "extensions.yaml"))
	if err == nil {
		t, probs := desiredyaml.Parse(b)
		if len(probs) > 0 {
			return nil, "", fmt.Errorf("gitrepo: extensions.yaml is invalid: %s", probs[0].Message)
		}
		if t != nil {
			tree = t
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", err
	}
	h, err := spec.Hash(tree)
	return tree, h, err
}

// SetExtensions commits extensions.yaml.
func (r *Repo) SetExtensions(ctx context.Context, tree any, author, message string) (string, error) {
	b, err := desiredyaml.Emit(tree)
	if err != nil {
		return "", err
	}
	h, err := spec.Hash(tree)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := os.WriteFile(filepath.Join(r.dir, "extensions.yaml"), b, 0o600); err != nil {
		return "", err
	}
	return h, r.commit(ctx, "extensions.yaml", author, message, "Extensions-Version: "+h)
}

// Version is one commit of an application's desired state.
type Version struct {
	Commit  string
	Hash    string
	Author  string
	Message string
	At      time.Time
}

// History lists an application's versions, newest first.
func (r *Repo) History(ctx context.Context, app string) ([]Version, error) {
	if !appName.MatchString(app) {
		return nil, ErrNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out, err := r.git(ctx, nil, "log", "--format=%H%x1f%an%x1f%aI%x1f%s%x1f%(trailers:key=Spec-Hash,valueonly,separator=)%x1e", "--", appPath(app))
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var vs []Version
	for _, rec := range strings.Split(out, "\x1e") {
		f := strings.Split(strings.TrimSpace(rec), "\x1f")
		if len(f) != 5 {
			continue
		}
		at, _ := time.Parse(time.RFC3339, f[2])
		vs = append(vs, Version{Commit: f[0], Author: f[1], At: at, Message: f[3], Hash: strings.TrimSpace(f[4])})
	}
	if len(vs) == 0 {
		return nil, ErrNotFound
	}
	return vs, nil
}

// Diff returns the changes from one version of desired state to another.
func (r *Repo) Diff(ctx context.Context, fromHash, toHash string) ([]spec.Change, error) {
	from, ok, err := r.ByHash(ctx, fromHash)
	if err != nil || !ok {
		return nil, fmt.Errorf("gitrepo: version %s not found %v", fromHash, err)
	}
	to, ok, err := r.ByHash(ctx, toHash)
	if err != nil || !ok {
		return nil, fmt.Errorf("gitrepo: version %s not found %v", toHash, err)
	}
	return spec.Diff(from, to), nil
}

// Revert makes an earlier version of an application current again, as a
// new commit; history is never rewritten.
func (r *Repo) Revert(ctx context.Context, app, toHash, author, message string) (string, error) {
	tree, ok, err := r.ByHash(ctx, toHash)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("gitrepo: version %s not found", toHash)
	}
	if name, _ := tree.(map[string]any)["name"].(string); name != app {
		return "", fmt.Errorf("gitrepo: version %s belongs to %q, not %q", toHash, name, app)
	}
	return r.Commit(ctx, app, tree, author, message)
}

// Export pushes the repository to a mirror, overwriting it. It is one-way:
// nothing is ever fetched from the mirror.
func (r *Repo) Export(ctx context.Context, remote string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.git(ctx, nil, "push", "-q", "--force", remote, "main:main")
	return err
}

// MaxBriefing is the largest briefing.md OAP stores.
const MaxBriefing = 16 << 10

func briefingPath(app string) string {
	if app == "" {
		return "briefing.md"
	}
	return filepath.Join("apps", app+".briefing.md")
}

// Briefing returns the owner's briefing.md for an application, or for the
// server when app is empty.
func (r *Repo) Briefing(ctx context.Context, app string) (string, error) {
	if app != "" && !appName.MatchString(app) {
		return "", nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := os.ReadFile(filepath.Join(r.dir, briefingPath(app)))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// SetBriefing commits the owner's briefing.md; empty removes it.
func (r *Repo) SetBriefing(ctx context.Context, app, markdown, author string) error {
	if app != "" && !appName.MatchString(app) {
		return fmt.Errorf("gitrepo: %q isn't an application name", app)
	}
	if len(markdown) > MaxBriefing {
		return fmt.Errorf("gitrepo: briefing.md is %d bytes; the limit is %d", len(markdown), MaxBriefing)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	path := briefingPath(app)
	full := filepath.Join(r.dir, path)
	if markdown == "" {
		if _, err := os.Stat(full); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err := os.Remove(full); err != nil {
			return err
		}
		return r.commit(ctx, path, author, "Remove "+path, "Briefing: "+or(app, "server"))
	}
	if cur, err := os.ReadFile(full); err == nil && string(cur) == markdown {
		return nil
	}
	if err := os.WriteFile(full, []byte(markdown), 0o600); err != nil {
		return err
	}
	return r.commit(ctx, path, author, "Update "+path, "Briefing: "+or(app, "server"))
}

func or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
