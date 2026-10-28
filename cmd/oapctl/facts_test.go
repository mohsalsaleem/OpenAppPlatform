package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/extension"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sim"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/model"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"google.golang.org/protobuf/types/known/structpb"
)

const githubFixtures = "../../internal/adapters/extension/testdata/github"

// fixtureCommit is one recorded commit, served as part of acme/shop.
type fixtureCommit struct {
	name, sha string
	dir       string
	pulls     []int
}

// fakeGitHub serves the recorded fixtures as the repository acme/shop,
// behind GitHub App authentication: a JWT signed with the App's key buys
// an installation token, and only that token reads the repository.
func fakeGitHub(t *testing.T, pub *rsa.PublicKey) (*httptest.Server, map[string]fixtureCommit) {
	t.Helper()
	commits := map[string]fixtureCommit{}
	dirs, _ := filepath.Glob(filepath.Join(githubFixtures, "*", "commit.json"))
	for _, p := range dirs {
		var c struct {
			SHA string `json:"sha"`
		}
		b, _ := os.ReadFile(p)
		json.Unmarshal(b, &c)
		var pulls []struct {
			Number int `json:"number"`
		}
		pb, _ := os.ReadFile(filepath.Join(filepath.Dir(p), "pulls.json"))
		json.Unmarshal(pb, &pulls)
		f := fixtureCommit{name: filepath.Base(filepath.Dir(p)), sha: c.SHA, dir: filepath.Dir(p)}
		for _, pr := range pulls {
			f.pulls = append(f.pulls, pr.Number)
		}
		commits[f.name] = f
	}
	if len(commits) != 7 {
		t.Fatalf("found %d recorded fixtures", len(commits))
	}
	const token = "ghs_fixturetoken"
	jwtOK := func(r *http.Request) bool {
		parts := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")
		if len(parts) != 3 {
			return false
		}
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err != nil || rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) != nil {
			return false
		}
		claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var c struct {
			Iss string `json:"iss"`
			Exp int64  `json:"exp"`
		}
		json.Unmarshal(claims, &c)
		return c.Iss == "1234" && c.Exp > time.Now().Unix()
	}
	serve := func(w http.ResponseWriter, path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}
	find := func(ref string) (fixtureCommit, bool) {
		for _, c := range commits {
			if len(ref) >= 7 && strings.HasPrefix(c.sha, ref) {
				return c, true
			}
		}
		return fixtureCommit{}, false
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case p == "/repos/acme/shop/installation" && jwtOK(r):
			fmt.Fprint(w, `{"id": 42}`)
			return
		case p == "/app/installations/42/access_tokens" && r.Method == http.MethodPost && jwtOK(r):
			fmt.Fprintf(w, `{"token": %q, "expires_at": %q}`, token, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
			return
		case !strings.HasPrefix(p, "/repos/acme/shop/") || r.Header.Get("Authorization") != "Bearer "+token:
			http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
			return
		}
		rest := strings.Split(strings.TrimPrefix(p, "/repos/acme/shop/"), "/")
		switch {
		case len(rest) >= 2 && rest[0] == "commits":
			c, ok := find(rest[1])
			if !ok {
				http.Error(w, `{"message":"No commit found"}`, http.StatusUnprocessableEntity)
				return
			}
			file := map[string]string{"": "commit.json", "check-runs": "check-runs.json", "status": "status.json", "pulls": "pulls.json"}
			sub := ""
			if len(rest) == 3 {
				sub = rest[2]
			}
			serve(w, filepath.Join(c.dir, file[sub]))
		case len(rest) == 3 && rest[0] == "pulls" && rest[2] == "reviews":
			for _, c := range commits {
				for _, n := range c.pulls {
					if fmt.Sprint(n) == rest[1] {
						serve(w, filepath.Join(c.dir, "reviews.json"))
						return
					}
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, commits
}

// TestChangeFactsFromRecordedRepository is R4.3's acceptance. A fact
// source (the GitHub extension, signing in as a GitHub App) reads a
// recorded fixture repository, whose commits were recorded from real
// public GitHub commits (testdata/github/sources.tsv), and the facts map to
// the envelope's risk tiers: an agent's deploy of a plain or docs-only
// commit with green CI is allowed, a migration or lockfile change needs
// the owner, and a commit whose CI isn't green (failing, still running, or
// never run) is denied.
func TestChangeFactsFromRecordedRepository(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	gh, commits := fakeGitHub(t, &key.PublicKey)

	bin := filepath.Join(t.TempDir(), "github")
	if out, err := exec.Command("go", "build", "-o", bin, "../oap-ext-github").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ctx := context.Background()
	clock := sim.NewClock(time.Now().UTC())
	rt, edge, journal, desired := sim.NewRuntime(clock, model.HostObservation{Name: "sim"}), sim.NewEdge(), &sim.Journal{}, sim.NewDesired()
	env, _ := spec.FromJSON([]byte(`{
  "agents": {
    "owner": {"scope": {"applications": ["*"]}, "may": ["deploy", "spec-patch", "install-extension", "build"]},
    "claude": {"scope": {"applications": ["shop"]}, "may": ["deploy", "build"]}},
  "change_facts": {"ci-not-green": "deny", "migrations-changed": "sensitive", "lockfile-changed": "sensitive", "docs-only": "routine"}}`))
	desired.SetEnvelope(ctx, env, "owner", "test")
	host := &extension.Host{SecretValue: func(name string) (string, error) {
		if name != "github-app-key" {
			return "", fmt.Errorf("no secret %s", name)
		}
		return keyPEM, nil
	}}
	t.Cleanup(host.Close)
	builder := &recordingBuilder{commit: commits["migrations-green"].sha}
	svc := &service.Service{Desired: desired, Journal: journal, Clock: clock, Extensions: host, Facts: host.FactSource(), Builder: builder,
		Observer: &engine.Observer{Runtime: rt, Clock: clock},
		Executor: &engine.Executor{Runtime: rt, Edge: edge, Journal: journal, Desired: desired, Clock: clock}}
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	await := func(op string) *oapv1.Outcome {
		var o *oapv1.Outcome
		svc.Await(ctx, service.Owner, &oapv1.AwaitRequest{OperationId: op}, func(e *oapv1.Event) error {
			if e.GetOutcome() != nil {
				o = e.GetOutcome()
			}
			return nil
		})
		return o
	}
	ownerApply := func(in *oapv1.Intent) *oapv1.Outcome {
		t.Helper()
		p, err := svc.Propose(ctx, service.Owner, &oapv1.ProposeRequest{Intent: in})
		if err != nil {
			t.Fatal(err)
		}
		a, err := svc.Apply(ctx, service.Owner, &oapv1.ApplyRequest{ProposalId: p.Proposal.Id})
		if err != nil {
			t.Fatal(err)
		}
		return await(a.OperationId)
	}

	digest := fileDigest(t, bin)
	if o := ownerApply(&oapv1.Intent{Change: &oapv1.Intent_InstallExtension{InstallExtension: &oapv1.InstallExtension{
		Name: "github", Path: bin, Sha256: digest, Points: []string{"facts"}, Secrets: []string{"github-app-key"},
		Settings: map[string]string{"app_id": "1234", "api_url": gh.URL}}}}); o.GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED {
		t.Fatalf("install the GitHub extension: %v", o)
	}
	shop, _ := structpb.NewStruct(map[string]any{"environment": "production",
		"components": map[string]any{"web": map[string]any{"kind": "web", "image": "reg/shop:1", "port": 8080}}})
	if o := ownerApply(&oapv1.Intent{Application: "shop", Change: &oapv1.Intent_SpecPatch{SpecPatch: &oapv1.SpecPatch{MergePatch: shop}}}); o.GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED {
		t.Fatalf("create shop: %v", o)
	}

	claude := service.Caller{Agent: "claude", Apps: []string{"shop"}}
	propose := func(name string) *oapv1.Proposal {
		t.Helper()
		sha := commits[name].sha
		p, err := svc.Propose(ctx, claude, &oapv1.ProposeRequest{Intent: &oapv1.Intent{Application: "shop",
			Change: &oapv1.Intent_Deploy{Deploy: &oapv1.Deploy{Images: map[string]string{"web": "reg/shop:" + sha[:12]},
				Source: &oapv1.Source{Repository: "https://github.com/acme/shop", Commit: sha}}}}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return p.Proposal
	}
	for _, c := range []struct {
		fixture string
		verdict oapv1.Verdict
		check   func(*oapv1.ChangeFacts) bool
	}{
		{"plain-green", oapv1.Verdict_VERDICT_ALLOWED, func(f *oapv1.ChangeFacts) bool {
			return f.Ci == "green" && f.Reviewed && f.Signed && !f.DocsOnly && !f.LockfileChanged && !f.MigrationsChanged
		}},
		{"docs-green", oapv1.Verdict_VERDICT_ALLOWED, func(f *oapv1.ChangeFacts) bool { return f.Ci == "green" && f.DocsOnly }},
		{"lockfile-green", oapv1.Verdict_VERDICT_NEEDS_APPROVAL, func(f *oapv1.ChangeFacts) bool { return f.Ci == "green" && f.LockfileChanged }},
		{"migrations-green", oapv1.Verdict_VERDICT_NEEDS_APPROVAL, func(f *oapv1.ChangeFacts) bool {
			return f.Ci == "green" && f.MigrationsChanged && f.Reviewed
		}},
		{"ci-red", oapv1.Verdict_VERDICT_DENIED, func(f *oapv1.ChangeFacts) bool { return f.Ci == "red" && !f.Reviewed }},
		{"ci-pending", oapv1.Verdict_VERDICT_DENIED, func(f *oapv1.ChangeFacts) bool { return f.Ci == "pending" }},
		{"unsigned-no-ci", oapv1.Verdict_VERDICT_DENIED, func(f *oapv1.ChangeFacts) bool { return f.Ci == "unknown" && !f.Signed && f.DocsOnly }},
	} {
		p := propose(c.fixture)
		f := p.GetChangeFacts()
		t.Logf("%-17s %-24s ci=%-8s files=%-3d migrations=%-5v lockfile=%-5v docs-only=%-5v reviewed=%-5v signed=%v",
			c.fixture, p.Verdict, f.Ci, len(f.FilesChanged), f.MigrationsChanged, f.LockfileChanged, f.DocsOnly, f.Reviewed, f.Signed)
		if p.Verdict != c.verdict || !c.check(f) || f.Commit != commits[c.fixture].sha {
			t.Errorf("%s: verdict %v (want %v), facts %v, reasons %s", c.fixture, p.Verdict, c.verdict, f, proposalReasons(p))
		}
	}

	// An image an OAP build made carries its commit: deploying it without a
	// source gets the build's facts (here, a migration).
	if o := ownerApply(&oapv1.Intent{Application: "shop", Change: &oapv1.Intent_Build{Build: &oapv1.Build{
		Component: "web", Repository: "https://github.com/acme/shop", Commit: "main"}}}); o.GetImages()["web"] == "" || o.GetCommits()["web"] != builder.commit {
		t.Fatalf("build outcome: %v", o)
	}
	built, _ := svc.Propose(ctx, claude, &oapv1.ProposeRequest{Intent: &oapv1.Intent{Application: "shop",
		Change: &oapv1.Intent_Deploy{Deploy: &oapv1.Deploy{Images: map[string]string{"web": "oap-build/shop-web:" + builder.commit[:12]}}}}})
	if f := built.GetProposal().GetChangeFacts(); f.GetCommit() != builder.commit || !f.GetMigrationsChanged() ||
		built.GetProposal().GetVerdict() != oapv1.Verdict_VERDICT_NEEDS_APPROVAL {
		t.Fatalf("deploying a built image: %v %v", built.GetProposal().GetVerdict(), f)
	}

	// When GitHub can't be reached, CI is unknown, never green: denied.
	gh.Close()
	if p := propose("plain-green"); p.Verdict != oapv1.Verdict_VERDICT_DENIED || p.GetChangeFacts().GetCi() != "unknown" {
		t.Fatalf("GitHub unreachable: %v %v", p.Verdict, p.GetChangeFacts())
	}
}

// recordingBuilder pretends to build: the image is named after the commit.
type recordingBuilder struct{ commit string }

func (b *recordingBuilder) Build(ctx context.Context, req ops.BuildRequest) (ops.BuildResult, error) {
	return ops.BuildResult{Image: req.Image + ":" + b.commit[:12], Commit: b.commit}, nil
}

func proposalReasons(p *oapv1.Proposal) string {
	var out []string
	for _, r := range p.GetReasons() {
		out = append(out, r.GetWhy()+": "+r.GetMessage())
	}
	return strings.Join(out, "; ")
}
