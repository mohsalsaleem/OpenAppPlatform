package sshhost

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var ctx = context.Background()

func TestParseAddress(t *testing.T) {
	cases := []struct {
		in, user, host string
		port           int
		bad            bool
	}{
		{in: "203.0.113.5", user: "root", host: "203.0.113.5", port: 22},
		{in: "deploy@203.0.113.5", user: "deploy", host: "203.0.113.5", port: 22},
		{in: "root@example.com:2222", user: "root", host: "example.com", port: 2222},
		{in: "root@2001:db8::1", user: "root", host: "2001:db8::1", port: 22},
		{in: "root@x:99999", bad: true},
		{in: "", bad: true},
		{in: "root@host;rm", bad: true},
		{in: "'root'@host", bad: true},
	}
	for _, c := range cases {
		u, h, p, err := ParseAddress(c.in)
		if (err != nil) != c.bad || (!c.bad && (u != c.user || h != c.host || p != c.port)) {
			t.Errorf("%q: %q %q %d %v", c.in, u, h, p, err)
		}
	}
}

// recorder captures the commands a Client would run, and runs `true`.
type recorder struct{ calls [][]string }

func (r *recorder) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	r.calls = append(r.calls, append([]string{name}, args...))
	return exec.CommandContext(ctx, "true")
}

func TestPasswordLoginRefusedWithoutTerminal(t *testing.T) {
	r := &recorder{}
	c := Client{Command: r.command, Terminal: func() bool { return false }}
	err := c.InstallKeyWithPassword(ctx, Target{User: "root", Host: "203.0.113.5", Dir: t.TempDir()}, "ssh-ed25519 AAAA oap-bootstrap")
	if !errors.Is(err, ErrNeedsTerminal) || len(r.calls) != 0 {
		t.Fatalf("err %v, %d commands run", err, len(r.calls))
	}
}

func TestConnectionsUseOnlyOAPsKeyAndPinnedHost(t *testing.T) {
	r := &recorder{}
	c := Client{Command: r.command, Terminal: func() bool { return true }}
	tg := Target{User: "root", Host: "203.0.113.5", Port: 2222, Dir: t.TempDir()}
	c.Run(ctx, tg, "true")
	c.InstallKeyWithIdentity(ctx, tg, "ssh-ed25519 AAAA oap-bootstrap", "/keys/mine")
	c.InstallKeyWithPassword(ctx, tg, "ssh-ed25519 AAAA oap-bootstrap")
	if len(r.calls) != 3 {
		t.Fatalf("%d calls", len(r.calls))
	}
	for _, call := range r.calls {
		args := strings.Join(call, " ")
		for _, want := range []string{"ssh -F /dev/null", "UserKnownHostsFile=" + tg.KnownHosts(), "GlobalKnownHostsFile=/dev/null",
			"StrictHostKeyChecking=yes", "-p 2222", "root@203.0.113.5"} {
			if !strings.Contains(args, want) {
				t.Errorf("%q lacks %q", args, want)
			}
		}
		if strings.Contains(args, ".ssh/known_hosts") || strings.Contains(args, "StrictHostKeyChecking=no") {
			t.Errorf("%q trusts hosts it shouldn't", args)
		}
	}
	run := strings.Join(r.calls[0], " ")
	if !strings.Contains(run, "-i "+tg.Key()) || !strings.Contains(run, "BatchMode=yes") || !strings.Contains(run, "IdentitiesOnly=yes") {
		t.Errorf("run: %q", run)
	}
	pw := strings.Join(r.calls[2], " ")
	if !strings.Contains(pw, "PubkeyAuthentication=no") || strings.Contains(pw, "BatchMode") {
		t.Errorf("password install: %q", pw)
	}
}

func TestInstallScriptRefusesOddKeys(t *testing.T) {
	for _, pub := range []string{"ssh-ed25519 AAAA x'; rm -rf / #", "ecdsa AAAA", "ssh-ed25519 AAAA\nssh-rsa BBBB"} {
		if _, err := installScript(pub); err == nil {
			t.Errorf("accepted %q", pub)
		}
	}
	s, err := installScript("ssh-ed25519 AAAA oap-bootstrap 2026-10-19")
	if err != nil || !strings.Contains(s, "grep -qxF") || !strings.Contains(s, "umask 077") {
		t.Fatalf("%q %v", s, err)
	}
}

// TestEnsureKeyLeavesSSHDirAlone creates a real key with ssh-keygen and
// checks nothing appears under ~/.ssh.
func TestEnsureKeyLeavesSSHDirAlone(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Fatal("ssh-keygen is required: OAP uses the system OpenSSH tools")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	tg := Target{User: "root", Host: "203.0.113.5", Dir: filepath.Join(home, ".config/oap/hosts/203.0.113.5")}
	pub, err := Client{}.EnsureKey(ctx, tg, "oap-bootstrap 2026-10-19")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Client{}.EnsureKey(ctx, tg, "oap-bootstrap 2026-10-19")
	if !strings.HasPrefix(pub, "ssh-ed25519 ") || !strings.HasSuffix(pub, "oap-bootstrap 2026-10-19") || again != pub {
		t.Fatalf("pub %q, again %q", pub, again)
	}
	st, _ := os.Stat(tg.Key())
	dir, _ := os.Stat(tg.Dir)
	if st.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
		t.Fatalf("modes %v %v", st.Mode(), dir.Mode())
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("~/.ssh touched: %v", err)
	}
}
