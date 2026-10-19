package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sshhost"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/host"
)

var update = flag.Bool("update", false, "rewrite golden files")

const probeFixture = "../../internal/core/host/testdata/probes/"

// fakeSSH answers for ssh-keyscan, ssh-keygen -l and ssh. The probe returns
// the given fixture; failProbe makes it fail, like a dropped connection.
type fakeSSH struct {
	probe     string
	failProbe bool
	calls     []string
}

func (f *fakeSSH) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	last := ""
	if len(args) > 0 {
		last = args[len(args)-1]
	}
	switch {
	case name == "ssh-keyscan":
		return exec.Command("echo", "203.0.113.5 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOAPtestkeyonlyforfixtures")
	case name == "ssh-keygen" && args[0] == "-l":
		return exec.Command("echo", "256 SHA256:testfingerprint 203.0.113.5 (ED25519)")
	case name == "ssh-keygen":
		return exec.CommandContext(ctx, name, args...) // a real key, in the test's directory
	case name == "ssh" && last == "sh -s":
		// The script arrives on stdin: "true" checks the login, anything
		// else is the probe.
		if f.failProbe {
			return exec.Command("sh", "-c", `[ "$(cat)" = true ] || exit 255`)
		}
		return exec.Command("sh", "-c", `[ "$(cat)" = true ] || cat `+f.probe)
	}
	return exec.Command("true")
}

type harness struct {
	a   *app
	out bytes.Buffer
	ssh *fakeSSH
}

func newHarness(t *testing.T, terminal bool, input, probe string) *harness {
	h := &harness{ssh: &fakeSSH{probe: probe}}
	h.a = &app{cli: "oap", in: bufio.NewReader(strings.NewReader(input)), out: &h.out, errOut: &h.out,
		terminal: func() bool { return terminal }, now: func() time.Time { return time.Date(2026, 10, 19, 14, 0, 0, 0, time.UTC) },
		configDir: t.TempDir()}
	h.a.ssh = sshhost.Client{Command: h.ssh.command, Terminal: h.a.terminal}
	return h
}

func (h *harness) run(args ...string) int { return h.a.run(context.Background(), args) }

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		os.WriteFile(p, []byte(got), 0o644)
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Fatalf("%s differs:\n%s", p, got)
	}
}

func findings(t *testing.T, fixture string) []host.Finding {
	b, err := os.ReadFile(probeFixture + fixture)
	if err != nil {
		t.Fatal(err)
	}
	f, err := host.ParseProbe(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return host.Check(f)
}

// TestFrontDoorGolden pins `oap` and `oap --json` for every journey state.
func TestFrontDoorGolden(t *testing.T) {
	states := []struct {
		name   string
		server *host.Server
		exit   int
	}{
		{"none", nil, host.ExitOwner},
		{"new", &host.Server{Address: "root@203.0.113.5", Stage: host.StageNew}, host.ExitOwner},
		{"connected", &host.Server{Address: "root@203.0.113.5", Stage: host.StageConnected}, host.ExitOK},
		{"checked-stopped", &host.Server{Address: "root@203.0.113.5", Stage: host.StageChecked, Summary: "Ubuntu 18.04.2 LTS, x86_64, 1 CPU, 985 MiB memory, 12.1 GiB disk free",
			Findings: findings(t, "vultr-oap-2026-10-19.txt")}, host.ExitStopped},
		{"checked-fixes", &host.Server{Address: "root@203.0.113.5", Stage: host.StageChecked, Summary: "Ubuntu 24.04.5 LTS, aarch64, 12 CPUs, 7836 MiB memory, 46.6 GiB disk free",
			Findings: findings(t, "container-ubuntu-24-04.txt")}, host.ExitNotBuiltYet},
		{"ready", &host.Server{Address: "root@203.0.113.5", Stage: host.StageReady, Summary: "Debian GNU/Linux 12 (bookworm), aarch64, 2 CPUs, 3931 MiB memory, 28.6 GiB disk free"}, host.ExitNotBuiltYet},
		{"installed", &host.Server{Address: "root@203.0.113.5", Stage: host.StageInstalled}, host.ExitNotBuiltYet},
		{"owned", &host.Server{Address: "root@203.0.113.5", Stage: host.StageOwned}, host.ExitNotBuiltYet},
		{"handed-off", &host.Server{Address: "root@203.0.113.5", Stage: host.StageHandedOff}, host.ExitOK},
	}
	for _, s := range states {
		t.Run(s.name, func(t *testing.T) {
			for _, mode := range []string{"json", "txt"} {
				h := newHarness(t, mode == "txt", "", "")
				if s.server != nil {
					if err := h.a.update(*s.server); err != nil {
						t.Fatal(err)
					}
				}
				var code int
				if mode == "json" {
					code = h.run("--json")
				} else if s.server != nil && s.server.Stage != host.StageNew && s.server.Stage != host.StageConnected {
					code = h.run()
				} else {
					continue // on a terminal these start connecting or checking: TestFirstRunOnTerminal
				}
				if code != s.exit {
					t.Fatalf("%s: exit %d, want %d\n%s", mode, code, s.exit, h.out.String())
				}
				got := h.out.String()
				if mode == "json" {
					var st host.Status
					if err := json.Unmarshal([]byte(got), &st); err != nil {
						t.Fatal(err)
					}
					if st.Next.By == host.ByOwner && (st.Next.Why == "" || (st.Next.AskOwner == "" && st.Next.Task == "")) {
						t.Fatalf("an owner step must say why and what to ask: %+v", st.Next)
					}
				}
				golden(t, fmt.Sprintf("frontdoor-%s.%s", s.name, mode), fmt.Sprintf("exit %d\n%s", code, got))
			}
		})
	}
}

// TestFirstRunOnTerminal walks the owner's first launch: address, default
// user, host key confirmation, connect, then the read-only check.
func TestFirstRunOnTerminal(t *testing.T) {
	h := newHarness(t, true, "203.0.113.5\n\ny\n", probeFixture+"vultr-oap-2026-10-19.txt")
	code := h.run()
	if code != host.ExitStopped {
		t.Fatalf("exit %d\n%s", code, h.out.String())
	}
	golden(t, "first-run.txt", h.out.String())
	j, _ := h.a.loadJourney()
	if s := j.first(); s == nil || s.Stage != host.StageChecked || s.Address != "root@203.0.113.5" {
		t.Fatalf("journey %+v", j)
	}
	pinned, _ := os.ReadFile(filepath.Join(h.a.configDir, "hosts/203.0.113.5/known_hosts"))
	if !strings.Contains(string(pinned), "ssh-ed25519") {
		t.Fatalf("host key not pinned: %q", pinned)
	}
}

func TestHostKeyMustBeConfirmed(t *testing.T) {
	h := newHarness(t, true, "203.0.113.5\n\nn\n", probeFixture+"vultr-oap-2026-10-19.txt")
	if code := h.run(); code != 1 || !strings.Contains(h.out.String(), "host key not confirmed") {
		t.Fatalf("exit %d\n%s", code, h.out.String())
	}
	if j, _ := h.a.loadJourney(); j.first() != nil {
		t.Fatal("an unconfirmed host was recorded")
	}
}

// TestPasswordConnectNeedsTerminal: an agent can't run a password connect.
func TestPasswordConnectNeedsTerminal(t *testing.T) {
	h := newHarness(t, false, "", "")
	if code := h.run("host", "connect", "root@203.0.113.5"); code != 1 || !strings.Contains(h.out.String(), "needs the owner at a terminal") {
		t.Fatalf("exit %d\n%s", code, h.out.String())
	}
	for _, c := range h.ssh.calls {
		if strings.HasPrefix(c, "ssh ") {
			t.Fatalf("connected anyway: %s", c)
		}
	}
}

func TestFingerprintFlagMustMatch(t *testing.T) {
	h := newHarness(t, false, "", "")
	code := h.run("host", "connect", "root@203.0.113.5", "--identity", "/keys/mine", "--fingerprint", "SHA256:other")
	if code != 1 || !strings.Contains(h.out.String(), "refusing to connect") {
		t.Fatalf("exit %d\n%s", code, h.out.String())
	}
	h = newHarness(t, false, "", "")
	if code := h.run("host", "connect", "root@203.0.113.5", "--identity", "/keys/mine", "--fingerprint", "SHA256:testfingerprint"); code != 0 {
		t.Fatalf("exit %d\n%s", code, h.out.String())
	}
}

// TestInterruptedRunResumes: a check that fails after connecting leaves the
// journey at "connected", and the next run picks up from the check.
func TestInterruptedRunResumes(t *testing.T) {
	h := newHarness(t, true, "203.0.113.5\n\ny\n", probeFixture+"vultr-oap-2026-10-19.txt")
	h.ssh.failProbe = true
	if code := h.run(); code != 1 {
		t.Fatalf("exit %d\n%s", code, h.out.String())
	}
	j, _ := h.a.loadJourney()
	if s := j.first(); s == nil || s.Stage != host.StageConnected {
		t.Fatalf("journey after interruption %+v", j)
	}
	h.ssh.failProbe = false
	h.out.Reset()
	calls := len(h.ssh.calls)
	if code := h.run(); code != host.ExitStopped {
		t.Fatalf("exit %d\n%s", code, h.out.String())
	}
	for _, c := range h.ssh.calls[calls:] {
		if strings.HasPrefix(c, "ssh-keyscan") {
			t.Fatal("resumed run connected again instead of resuming at the check")
		}
	}
}

func TestJourneyFileIsPrivate(t *testing.T) {
	h := newHarness(t, false, "", "")
	h.a.update(host.Server{Address: "root@203.0.113.5", Stage: host.StageNew})
	st, _ := os.Stat(filepath.Join(h.a.configDir, "journey.json"))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("journey.json mode %v", st.Mode())
	}
}
