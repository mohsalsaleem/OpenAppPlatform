package docker

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
)

const probeImage = "oap-hardening-probe:test"

// probe builds the probe image, starts it with the given security, and
// returns what it reported and how Docker created it.
func probe(t *testing.T, r *Runtime, component string, sec ops.Security, memoryMiB int) (map[string]string, inspected) {
	t.Helper()
	if out, err := exec.Command("docker", "build", "-q", "-t", probeImage, "testdata/hardening").CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v\n%s", err, out)
	}
	ref := ops.InstanceRef{Application: "probe", Component: component, Ordinal: 1, Release: "r-1"}
	id, err := r.Start(ctx, ops.InstanceSpec{Ref: ref, Image: probeImage, MemoryMiB: memoryMiB,
		Volumes: map[string]string{"data": "/data"}, Security: sec})
	if err != nil {
		t.Fatal(err)
	}
	var report map[string]string
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		if report = parseReport(logs(t, r, id)); report["probe"] == "done" {
			break
		}
	}
	if report["probe"] != "done" {
		t.Fatalf("probe didn't finish: %v", report)
	}
	var c inspected
	if err := r.c.do(ctx, http.MethodGet, "/containers/"+id+"/json", nil, nil, &c); err != nil {
		t.Fatal(err)
	}
	return report, c
}

type inspected struct {
	ID         string
	HostConfig struct {
		CapDrop, CapAdd, SecurityOpt []string
		ReadonlyRootfs, Privileged   bool
		Tmpfs                        map[string]string
		PidsLimit, Memory            int64
		NetworkMode, PidMode         string
		IpcMode                      string
		Devices                      []any
		Binds                        []string
	}
	Mounts []struct{ Type, Destination string }
}

// logs reads a container's output, removing Docker's stream headers.
func logs(t *testing.T, r *Runtime, id string) string {
	t.Helper()
	resp, err := r.c.request(ctx, http.MethodGet, "/containers/"+id+"/logs", url.Values{"stdout": {"1"}, "stderr": {"1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out bytes.Buffer
	for len(raw) >= 8 {
		n := int(binary.BigEndian.Uint32(raw[4:8]))
		if len(raw) < 8+n {
			break
		}
		out.Write(raw[8 : 8+n])
		raw = raw[8+n:]
	}
	return out.String()
}

func parseReport(s string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			out[k] = v
		}
	}
	return out
}

// TestHardening runs a probe in a default container: it can't write outside
// /tmp and its volumes, has no capabilities (so chown and mknod fail
// even as root), can't gain privileges (no_new_privs, which also defeats
// setuid binaries) and can't fork past the process limit. Docker shows the
// same settings, and none of the forbidden ones.
func TestHardening(t *testing.T) {
	r := newRuntime(t)
	// The probe runs as root on purpose: it shows root without capabilities
	// can't do what root normally can.
	report, c := probe(t, r, "default", ops.Security{RunAsRoot: true}, 0)
	want := map[string]string{"root_write": "denied", "tmp_write": "ok", "volume_write": "ok",
		"chown": "denied", "mknod": "denied", "no_new_privs": "1", "cap_eff": "0000000000000000"}
	for k, v := range want {
		if report[k] != v {
			t.Errorf("%s = %q, want %q", k, report[k], v)
		}
	}
	if n, _ := strconv.Atoi(report["processes"]); n == 0 || n > DefaultPids {
		t.Errorf("processes = %q: the fork bomb must stop at %d", report["processes"], DefaultPids)
	}
	h := c.HostConfig
	if strings.Join(h.CapDrop, ",") != "ALL" || len(h.CapAdd) != 0 || !h.ReadonlyRootfs || h.PidsLimit != DefaultPids ||
		h.Memory != DefaultMemoryMiB<<20 || len(h.SecurityOpt) != 1 || !strings.HasPrefix(h.SecurityOpt[0], "no-new-privileges") {
		t.Errorf("host config: %+v", h)
	}
	if _, ok := h.Tmpfs["/tmp"]; !ok {
		t.Errorf("no tmpfs /tmp: %v", h.Tmpfs)
	}
	for _, opt := range h.SecurityOpt {
		if strings.Contains(opt, "unconfined") {
			t.Errorf("seccomp or apparmor disabled: %s", opt)
		}
	}
	if h.Privileged || h.PidMode == "host" || h.IpcMode == "host" || h.NetworkMode == "host" || len(h.Devices) > 0 || len(h.Binds) > 0 {
		t.Errorf("forbidden setting: %+v", h)
	}
	for _, m := range c.Mounts {
		if m.Type == "bind" {
			t.Errorf("host path mounted at %s", m.Destination)
		}
	}
}

// TestHardeningExceptions turns each exception on: the spec can keep a
// capability from Docker's default set, a writable root, privilege
// escalation and a different process limit.
func TestHardeningExceptions(t *testing.T) {
	r := newRuntime(t)
	report, c := probe(t, r, "exceptions", ops.Security{RunAsRoot: true, Capabilities: []string{"CHOWN"}, WritableRoot: true,
		AllowPrivilegeEscalation: true, Pids: 64}, 128)
	want := map[string]string{"root_write": "ok", "chown": "ok", "mknod": "denied", "no_new_privs": "0"}
	for k, v := range want {
		if report[k] != v {
			t.Errorf("%s = %q, want %q", k, report[k], v)
		}
	}
	if n, _ := strconv.Atoi(report["processes"]); n == 0 || n > 64 {
		t.Errorf("processes = %q, want at most 64", report["processes"])
	}
	h := c.HostConfig
	if strings.Join(h.CapAdd, ",") != "CHOWN" || h.ReadonlyRootfs || len(h.SecurityOpt) != 0 || h.PidsLimit != 64 || h.Memory != 128<<20 {
		t.Errorf("host config: %+v", h)
	}
}

// Jobs get the same defaults.
func TestJobHardening(t *testing.T) {
	r := newRuntime(t)
	ref := ops.InstanceRef{Application: "probe", Component: "migrate", Ordinal: 1, Release: "r-1"}
	if err := r.RunJob(ctx, ops.JobSpec{Ref: ref, Image: images.Job, Timeout: time.Minute, Security: ops.Security{User: "65532:65532"}}); err != nil {
		t.Fatal(err)
	}
	cs, err := r.containers(ctx, RoleJob, &ref)
	if err != nil || len(cs) != 1 {
		t.Fatalf("job containers: %v %v", cs, err)
	}
	var c inspected
	if err := r.c.do(ctx, http.MethodGet, "/containers/"+cs[0].ID+"/json", nil, nil, &c); err != nil {
		t.Fatal(err)
	}
	h := c.HostConfig
	if strings.Join(h.CapDrop, ",") != "ALL" || !h.ReadonlyRootfs || h.PidsLimit != DefaultPids || h.Memory != DefaultMemoryMiB<<20 || len(h.SecurityOpt) != 1 {
		t.Errorf("job host config: %+v", h)
	}
}

// An image that runs as root is refused unless the spec sets a user or
// allows root, and the refusal says how to fix it.
func TestRootImageRefused(t *testing.T) {
	r := newRuntime(t)
	if out, err := exec.Command("docker", "build", "-q", "-t", probeImage, "testdata/hardening").CombinedOutput(); err != nil {
		t.Fatalf("build probe: %v\n%s", err, out)
	}
	ref := ops.InstanceRef{Application: "probe", Component: "root", Ordinal: 1, Release: "r-1"}
	_, err := r.Start(ctx, ops.InstanceSpec{Ref: ref, Image: probeImage})
	var refused *ops.Refusal
	if !errors.As(err, &refused) || refused.Why != "runtime.runs_as_root" || !strings.Contains(refused.Reason, "run_as_root") {
		t.Fatalf("want a runs_as_root refusal, got %v", err)
	}
	job := ops.InstanceRef{Application: "probe", Component: "migrate", Ordinal: 1, Release: "r-1"}
	if err := r.RunJob(ctx, ops.JobSpec{Ref: job, Image: probeImage, Timeout: time.Minute}); !errors.As(err, &refused) {
		t.Fatalf("jobs are refused too, got %v", err)
	}
	if cs, _ := r.containers(ctx, RoleInstance, &ref); len(cs) != 0 {
		t.Fatal("a refused instance must not be created")
	}
}

// With a user, the process runs as that uid and its new volume belongs to
// it, so it can write its data without any capability.
func TestRunsAsUserAndOwnsNewVolume(t *testing.T) {
	r := newRuntime(t)
	report, c := probe(t, r, "user", ops.Security{User: "65532:65532"}, 0)
	if report["uid"] != "65532" || report["volume_write"] != "ok" || report["tmp_write"] != "ok" || report["cap_eff"] != "0000000000000000" {
		t.Fatalf("report: %v", report)
	}
	var cfg struct{ Config struct{ User string } }
	if err := r.c.do(ctx, http.MethodGet, "/containers/"+c.ID+"/json", nil, nil, &cfg); err != nil || cfg.Config.User != "65532:65532" {
		t.Fatalf("container user %q %v", cfg.Config.User, err)
	}
}
