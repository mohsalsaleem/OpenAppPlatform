package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1/oapv1connect"
)

// A build whose one step holds 600 MiB: two at once don't fit in 1 GiB.
const hungryDockerfile = `FROM busybox:1.37
RUN dd if=/dev/zero of=/dev/null bs=600M count=1
CMD ["sleep", "3600"]
`

// TestBuildsOneAtATimeOn1GB is R4.2's acceptance: on a host with 1 GiB of
// memory (swap included) running the real oapd and the on-host builder
// extension, two build requests made at once, each needing 600 MiB, run
// one after another and both succeed, and nothing on the host is killed
// for want of memory. The same two builds run at once on that host do run
// it out of memory, so the check can fail. Gated on OAP_TEST_DOCKER=1.
func TestBuildsOneAtATimeOn1GB(t *testing.T) {
	if os.Getenv("OAP_TEST_DOCKER") != "1" {
		t.Skip("needs Docker; set OAP_TEST_DOCKER=1")
	}
	h := newJourneyHost(t, "1g")
	const oapd = "oap-journey1-oapd"
	sh := func(script string) string {
		t.Helper()
		out, err := exec.Command("docker", "exec", "-i", h.name, "sh", "-c", script).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", script, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	ooms := func() string { return sh(`grep oom_kill /sys/fs/cgroup/memory.events | grep -v oom_kill_disable`) }

	// A repository inside oapd's container, where the builder clones it.
	sh(`docker exec -i ` + oapd + ` sh -c 'mkdir -p /tmp/repo && cd /tmp/repo && git init -q -b main && cat > Dockerfile && git -c user.name=t -c user.email=t@t.test add -A && git -c user.name=t -c user.email=t@t.test commit -qm init' <<'EOF'
` + hungryDockerfile + `EOF`)

	dir := t.TempDir()
	owner := map[string]string{"OAP_AGENT_URL": h.api}
	digest := strings.Fields(sh(`docker exec ` + oapd + ` sha256sum /usr/local/lib/oap/extensions/builder`))[0]
	if code, out := cli(t, dir, owner, "ext", "install", "builder", "--path", "/usr/local/lib/oap/extensions/builder", "--sha256", digest,
		"--point", "builder", "--setting", "install_id=journey1", "--yes"); code != 0 {
		t.Fatalf("install the builder: %s", out)
	}
	spec := journeySpec("built")
	spec["api_version"], spec["kind"], spec["name"] = "oap.dev/v1", "Application", "built"
	b, _ := json.Marshal(spec)
	file := filepath.Join(t.TempDir(), "oap.yaml")
	os.WriteFile(file, b, 0o600)
	if code, out := cli(t, dir, owner, "app", "apply", file, "--yes"); code != 0 {
		t.Fatalf("create the application: %s", out)
	}
	before := ooms()

	// Two builds asked for at once.
	client := oapv1connect.NewAgentServiceClient(&http.Client{Timeout: 15 * time.Minute}, h.api)
	ctx := context.Background()
	type result struct {
		op      string
		outcome *oapv1.Outcome
		err     error
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := client.Propose(ctx, connect.NewRequest(&oapv1.ProposeRequest{Intent: &oapv1.Intent{Application: "built",
				Change: &oapv1.Intent_Build{Build: &oapv1.Build{Component: "web", Repository: "file:///tmp/repo", Commit: "main", MemoryMib: int32(700 + 4*i)}}}}))
			if err != nil {
				results[i].err = err
				return
			}
			a, err := client.Apply(ctx, connect.NewRequest(&oapv1.ApplyRequest{ProposalId: p.Msg.Proposal.Id}))
			if err != nil {
				results[i].err = err
				return
			}
			results[i].op = a.Msg.OperationId
			s, err := client.Await(ctx, connect.NewRequest(&oapv1.AwaitRequest{OperationId: a.Msg.OperationId, TimeoutSeconds: 900}))
			if err != nil {
				results[i].err = err
				return
			}
			for s.Receive() {
				if o := s.Msg().Event.GetOutcome(); o != nil {
					results[i].outcome = o
				}
			}
			results[i].err = s.Err()
		}()
	}
	wg.Wait()

	// (Identical requests would be one proposal, built once; these differ
	// in their memory, so they are two builds.)
	if results[0].op == results[1].op {
		t.Fatal("the two builds are one operation")
	}
	for i, r := range results {
		if r.err != nil || r.outcome.GetStatus() != oapv1.OutcomeStatus_OUTCOME_STATUS_ACHIEVED || r.outcome.GetImages()["web"] == "" {
			t.Fatalf("build %d: %v %v", i, r.outcome, r.err)
		}
	}
	if after := ooms(); after != before {
		t.Fatalf("the host ran out of memory during the builds: %s, then %s", before, after)
	}
	for _, c := range []string{oapd, "oap-journey1-caddy"} {
		if st := sh(`docker inspect -f '{{.State.Running}} {{.State.OOMKilled}}' ` + c); st != "true false" {
			t.Fatalf("%s after the builds: %s", c, st)
		}
	}
	if img := results[0].outcome.GetImages()["web"]; !strings.HasPrefix(img, "oap-build/built-web:") ||
		sh(`docker image inspect -f '{{index .Config.Labels "io.openappplatform.install"}}' `+img) != "journey1" {
		t.Fatalf("built image %s isn't labelled as this installation's", img)
	}

	// One after another: the later build started only after the earlier one
	// finished, by journal revision.
	{
		spans := buildSpans(t, client, results[0].op, results[1].op)
		a, b := spans[results[0].op], spans[results[1].op]
		if a[0] > b[0] {
			a, b = b, a
		}
		if b[0] < a[1] {
			t.Fatalf("the builds overlapped: started at revisions %d and %d, the first finished at %d", a[0], b[0], a[1])
		}
		t.Logf("first build: revisions %d to %d; second: %d to %d", a[0], a[1], b[0], b[1])
	}

	// Registry-only: the image a registry already has for the commit.
	commit := sh(`docker exec ` + oapd + ` git -C /tmp/repo rev-parse HEAD`)
	sh(`docker run -d -p 5000:5000 --name reg registry:2 >/dev/null && sleep 2 && docker pull -q busybox:1.37 >/dev/null &&
docker tag busybox:1.37 localhost:5000/built-web:` + commit[:12] + ` && docker push -q localhost:5000/built-web:` + commit[:12] + ` >/dev/null`)
	regDigest := strings.Fields(sh(`docker exec ` + oapd + ` sha256sum /usr/local/lib/oap/extensions/registry`))[0]
	if code, out := cli(t, dir, owner, "ext", "install", "registry", "--path", "/usr/local/lib/oap/extensions/registry", "--sha256", regDigest,
		"--point", "builder", "--setting", "image_template=localhost:5000/built-web:{short}", "--yes"); code != 0 {
		t.Fatalf("install the registry builder: %s", out)
	}
	if code, out := cli(t, dir, owner, "app", "build", "built", "web", "--repo", "file:///tmp/repo", "--commit", "main", "--yes"); code == 0 || !strings.Contains(out, "several builders") {
		t.Fatalf("a build naming no builder with two installed: %d\n%s", code, out)
	}
	code, out := cli(t, dir, owner, "app", "build", "built", "web", "--repo", "file:///tmp/repo", "--commit", "main", "--builder", "registry", "--yes")
	if want := "localhost:5000/built-web:" + commit[:12]; code != 0 || !strings.Contains(out, "Image: "+want) {
		t.Fatalf("registry-only build: %d\n%s", code, out)
	}

	// The control: the same two builds at once, outside OAP's queue, run
	// this host out of memory.
	sh(`mkdir -p /tmp/ctl && cd /tmp/ctl && cat > Dockerfile <<'EOF'
` + strings.Replace(hungryDockerfile, "count=1", "count=1 && echo control", 1) + `EOF`)
	exec.Command("docker", "exec", h.name, "sh", "-c",
		`cd /tmp/ctl && (DOCKER_BUILDKIT=0 docker build -q --no-cache -m 700m --memory-swap 700m -t ctl1 . & DOCKER_BUILDKIT=0 docker build -q --no-cache -m 700m --memory-swap 700m -t ctl2 . & wait)`).Run()
	if ctl := ooms(); ctl == before {
		t.Fatalf("the control (two builds at once) didn't run the host out of memory (%s); the check proves nothing", ctl)
	} else {
		t.Logf("control: %s became %s", before, ctl)
	}
}

// buildSpans returns, for each operation, the journal revisions at which its
// build step started and succeeded.
func buildSpans(t *testing.T, c oapv1connect.AgentServiceClient, ops ...string) map[string][2]uint64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := c.Watch(ctx, connect.NewRequest(&oapv1.WatchRequest{SinceRevision: 1}))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][2]uint64{}
	for s.Receive() {
		e := s.Msg().Event
		p := e.GetOperationProgress()
		if p == nil || p.GetStep().GetKind() != oapv1.StepKind_STEP_KIND_BUILD {
			continue
		}
		span := out[p.OperationId]
		switch p.State {
		case oapv1.StepState_STEP_STATE_STARTED:
			span[0] = e.Revision
		case oapv1.StepState_STEP_STATE_SUCCEEDED:
			span[1] = e.Revision
		}
		out[p.OperationId] = span
	}
	for _, op := range ops {
		if s := out[op]; s[0] == 0 || s[1] == 0 {
			b, _ := json.Marshal(out)
			t.Fatalf("no build span for %s: %s", op, bytes.TrimSpace(b))
		}
	}
	return out
}
