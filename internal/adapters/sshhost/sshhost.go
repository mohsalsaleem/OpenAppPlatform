// Package sshhost reaches a server over the system OpenSSH client for
// onboarding (docs/architecture/onboarding.md). OAP never reads the server
// password: ssh asks for it on the terminal. Every connection names OAP's
// own key and pinned host key and ignores ~/.ssh/config, so nothing under
// ~/.ssh is read for configuration or written.
package sshhost

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Target is a server and OAP's directory for it on this machine.
type Target struct {
	User, Host string
	Port       int
	// Dir holds the bootstrap key and the pinned host key.
	Dir string
}

// ParseAddress reads user@host[:port], defaulting the user to root and the
// port to 22.
func ParseAddress(s string) (user, host string, port int, err error) {
	user, host = "root", strings.TrimSpace(s)
	if u, h, ok := strings.Cut(host, "@"); ok {
		user, host = u, h
	}
	port = 22
	if h, p, ok := strings.Cut(host, ":"); ok && !strings.Contains(p, ":") {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", "", 0, fmt.Errorf("sshhost: %q isn't a port", p)
		}
		host, port = h, n
	}
	if host == "" || user == "" || strings.ContainsAny(host+user, " \t'\"`$;&|<>\\/") {
		return "", "", 0, fmt.Errorf("sshhost: %q isn't a server address; use user@host or user@host:port", s)
	}
	return user, host, port, nil
}

// Address is user@host, with :port when it isn't 22.
func (t Target) Address() string {
	if t.Port != 0 && t.Port != 22 {
		return fmt.Sprintf("%s@%s:%d", t.User, t.Host, t.Port)
	}
	return t.User + "@" + t.Host
}

// Key is the bootstrap private key's path; its public key is Key()+".pub".
func (t Target) Key() string { return filepath.Join(t.Dir, "bootstrap_ed25519") }

// KnownHosts is the file holding the pinned host key.
func (t Target) KnownHosts() string { return filepath.Join(t.Dir, "known_hosts") }

func (t Target) port() string {
	if t.Port == 0 {
		return "22"
	}
	return strconv.Itoa(t.Port)
}

// ErrNeedsTerminal is returned when a step needs the owner at a terminal.
var ErrNeedsTerminal = errors.New("sshhost: this step needs the owner at a terminal: ssh asks for the server password there, and OAP never handles it")

// Client runs the system OpenSSH tools.
type Client struct {
	// Command builds a command; tests replace it. Default exec.CommandContext.
	Command func(ctx context.Context, name string, args ...string) *exec.Cmd
	// Terminal reports whether the owner is at a terminal. Default: stdin
	// and stdout are both character devices.
	Terminal func() bool
}

func (c Client) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	if c.Command != nil {
		return c.Command(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...)
}

func (c Client) terminal() bool {
	if c.Terminal != nil {
		return c.Terminal()
	}
	return isCharDevice(os.Stdin) && isCharDevice(os.Stdout)
}

func isCharDevice(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// HostKey is a server's public host key as ssh-keyscan reports it.
type HostKey struct {
	Line        string // a known_hosts line
	Fingerprint string // for example "ED25519 SHA256:3f9c…"
}

// ScanHostKey fetches the server's host key, preferring ED25519.
func (c Client) ScanHostKey(ctx context.Context, t Target) (HostKey, error) {
	var out, errb bytes.Buffer
	cmd := c.command(ctx, "ssh-keyscan", "-T", "15", "-p", t.port(), "-t", "ed25519,ecdsa,rsa", t.Host)
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return HostKey{}, fmt.Errorf("sshhost: can't read %s's host key: %v %s", t.Host, err, strings.TrimSpace(errb.String()))
	}
	var lines []string
	for _, l := range strings.Split(out.String(), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return HostKey{}, fmt.Errorf("sshhost: %s offered no host key on port %s", t.Host, t.port())
	}
	line := lines[0]
	for _, l := range lines {
		if strings.Contains(l, " ssh-ed25519 ") {
			line = l
			break
		}
	}
	var fp bytes.Buffer
	cmd = c.command(ctx, "ssh-keygen", "-l", "-E", "sha256", "-f", "-")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = strings.NewReader(line+"\n"), &fp, &errb
	if err := cmd.Run(); err != nil {
		return HostKey{}, fmt.Errorf("sshhost: can't fingerprint the host key: %v", err)
	}
	// "256 SHA256:xxx host (ED25519)"
	f := strings.Fields(fp.String())
	if len(f) < 2 {
		return HostKey{}, fmt.Errorf("sshhost: unexpected fingerprint output %q", fp.String())
	}
	kind := strings.Trim(f[len(f)-1], "()")
	return HostKey{Line: line, Fingerprint: kind + " " + f[1]}, nil
}

// PinHostKey stores the host key the owner confirmed.
func PinHostKey(t Target, k HostKey) error {
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(t.KnownHosts(), []byte(k.Line+"\n"), 0o600)
}

// EnsureKey creates the bootstrap key pair if it doesn't exist and returns
// the public key.
func (c Client) EnsureKey(ctx context.Context, t Target, comment string) (string, error) {
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return "", err
	}
	if _, err := os.Stat(t.Key()); errors.Is(err, os.ErrNotExist) {
		var errb bytes.Buffer
		cmd := c.command(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", comment, "-f", t.Key())
		cmd.Stderr = &errb
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("sshhost: can't create the bootstrap key: %v %s", err, strings.TrimSpace(errb.String()))
		}
	}
	pub, err := os.ReadFile(t.Key() + ".pub")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(pub)), nil
}

// baseArgs are the options every connection uses: no user or system
// config, only OAP's pinned host key, and strict checking.
func baseArgs(t Target) []string {
	return []string{"-F", "/dev/null",
		"-o", "UserKnownHostsFile=" + t.KnownHosts(),
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "ConnectTimeout=15",
		"-p", t.port()}
}

// installScript appends pub to authorized_keys unless it is already there.
func installScript(pub string) (string, error) {
	if strings.ContainsAny(pub, "'\n\r") || !strings.HasPrefix(pub, "ssh-") {
		return "", fmt.Errorf("sshhost: refusing to install an unexpected public key")
	}
	return fmt.Sprintf(`umask 077; mkdir -p ~/.ssh && touch ~/.ssh/authorized_keys && `+
		`(grep -qxF '%[1]s' ~/.ssh/authorized_keys || printf '%%s\n' '%[1]s' >> ~/.ssh/authorized_keys)`, pub), nil
}

// InstallKeyWithPassword installs pub with one password login. ssh asks the
// owner for the password on the terminal; without a terminal it refuses
// before connecting.
func (c Client) InstallKeyWithPassword(ctx context.Context, t Target, pub string) error {
	if !c.terminal() {
		return ErrNeedsTerminal
	}
	script, err := installScript(pub)
	if err != nil {
		return err
	}
	args := append(baseArgs(t),
		"-o", "PubkeyAuthentication=no",
		"-o", "PreferredAuthentications=keyboard-interactive,password",
		"-o", "NumberOfPasswordPrompts=3",
		t.User+"@"+t.Host, script)
	cmd := c.command(ctx, "ssh", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sshhost: couldn't install the bootstrap key: %v", err)
	}
	return nil
}

// InstallKeyWithIdentity installs pub using a key the owner already has,
// for servers the provider set up with a key rather than a password.
func (c Client) InstallKeyWithIdentity(ctx context.Context, t Target, pub, identity string) error {
	script, err := installScript(pub)
	if err != nil {
		return err
	}
	args := append(baseArgs(t), "-i", identity, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", t.User+"@"+t.Host, script)
	var errb bytes.Buffer
	cmd := c.command(ctx, "ssh", args...)
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sshhost: couldn't install the bootstrap key with %s: %v %s", identity, err, lastLine(errb.String()))
	}
	return nil
}

// Run runs a shell script on the server with the bootstrap key, without
// prompting, and returns its standard output.
func (c Client) Run(ctx context.Context, t Target, script string) (string, error) {
	return c.RunStream(ctx, t, script, nil)
}

// RunStream is Run that also hands each line of output to line as it
// arrives, so slow work can show progress. line may be nil.
func (c Client) RunStream(ctx context.Context, t Target, script string, line func(string)) (string, error) {
	return c.Remote(ctx, t, "sh -s", strings.NewReader(script), line)
}

// Remote runs a remote command with the bootstrap key, feeding it stdin,
// and returns its output, handing each line to line as it arrives.
func (c Client) Remote(ctx context.Context, t Target, remote string, stdin io.Reader, line func(string)) (string, error) {
	args := append(baseArgs(t), "-i", t.Key(), "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", t.User+"@"+t.Host, remote)
	var out, errb bytes.Buffer
	cmd := c.command(ctx, "ssh", args...)
	cmd.Stdin, cmd.Stderr = stdin, &errb
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("sshhost: running on %s failed: %v", t.Address(), err)
	}
	sc := bufio.NewScanner(pipe)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		out.Write(sc.Bytes())
		out.WriteByte('\n')
		if line != nil {
			line(sc.Text())
		}
	}
	if err := cmd.Wait(); err != nil {
		return out.String(), fmt.Errorf("sshhost: running on %s failed: %v %s", t.Address(), err, lastLine(errb.String()))
	}
	return out.String(), nil
}

// Forward forwards a local unix socket to one on the server until ctx
// ends. It returns once the forward is ready.
func (c Client) Forward(ctx context.Context, t Target, local, remote string) (*exec.Cmd, error) {
	os.Remove(local)
	args := append(baseArgs(t), "-i", t.Key(), "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes",
		"-o", "ExitOnForwardFailure=yes", "-o", "StreamLocalBindUnlink=yes", "-N", "-L", local+":"+remote, t.User+"@"+t.Host)
	cmd := c.command(ctx, "ssh", args...)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	for i := 0; i < 150; i++ {
		if st, err := os.Stat(local); err == nil && st.Mode()&os.ModeSocket != 0 {
			return cmd, nil
		}
		if cmd.ProcessState != nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	cmd.Process.Kill()
	cmd.Wait()
	return nil, fmt.Errorf("sshhost: couldn't forward %s to %s on %s: %s", local, remote, t.Address(), lastLine(errb.String()))
}

// lastLine keeps an error short and drops OpenSSH's advisory warnings.
func lastLine(s string) string {
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "**") {
			keep = append(keep, l)
		}
	}
	if len(keep) == 0 {
		return ""
	}
	return keep[len(keep)-1]
}
