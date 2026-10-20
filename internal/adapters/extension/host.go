package extension

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
	extensionv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/extension/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Defaults sized for a small server.
const (
	DefaultMemoryMiB    = 64
	DefaultCallTimeout  = 10 * time.Second
	DefaultBuildTimeout = 20 * time.Minute
	startTimeout        = 10 * time.Second
	memoryPoll          = 100 * time.Millisecond
)

// Config is one installed extension: its binary and what the owner granted.
type Config struct {
	Name string
	// Path is the extension's binary; SHA256 its expected digest (hex).
	Path, SHA256 string
	// Points, Settings and Secrets are the grants: the points the core may
	// use it for, the settings it gets, and the secrets it may receive.
	Points   []extensionv1.Point
	Settings map[string]string
	Secrets  []string
	// MemoryMiB caps its resident memory; CallTimeout bounds each call,
	// and BuildTimeout each build.
	MemoryMiB    int
	CallTimeout  time.Duration
	BuildTimeout time.Duration
}

// Error is an extension failure, as the core sees it.
type Error struct {
	Extension, Op string
	Err           error
}

func (e *Error) Error() string { return fmt.Sprintf("extension %s: %s: %v", e.Extension, e.Op, e.Err) }
func (e *Error) Unwrap() error { return e.Err }

// Errors the host reports.
var (
	ErrMemoryCap  = errors.New("killed: over its memory cap")
	ErrTimeout    = errors.New("didn't answer in time; stopped")
	ErrNotGranted = errors.New("not granted")
	ErrDigest     = errors.New("binary doesn't match its recorded digest")
)

// Host runs installed extensions.
type Host struct {
	// SecretValue resolves a granted secret's value, at configure time.
	SecretValue func(name string) (string, error)
	// Logger receives extensions' own log lines; nil discards them.
	Logger hclog.Logger

	mu   sync.Mutex
	exts map[string]*running
}

type running struct {
	cfg    Config
	mu     sync.Mutex // one start at a time
	client *plugin.Client
	conn   *grpc.ClientConn
	pid    int
	killed error // why the watchdog killed it, if it did
	stop   chan struct{}
}

// Add registers an extension; it starts on first use.
func (h *Host) Add(cfg Config) {
	if cfg.MemoryMiB <= 0 {
		cfg.MemoryMiB = DefaultMemoryMiB
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = DefaultCallTimeout
	}
	if cfg.BuildTimeout <= 0 {
		cfg.BuildTimeout = DefaultBuildTimeout
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.exts == nil {
		h.exts = map[string]*running{}
	}
	if old, ok := h.exts[cfg.Name]; ok {
		old.kill(nil)
	}
	h.exts[cfg.Name] = &running{cfg: cfg}
}

// Install registers an installed extension from its desired state and,
// with verify, starts it now and checks its manifest against its grants.
// It implements ports.Extensions.
func (h *Host) Install(ctx context.Context, e spec.Extension, verify bool) error {
	cfg := Config{Name: e.Name, Path: e.Path, SHA256: e.SHA256, Settings: e.Settings, Secrets: e.Secrets, MemoryMiB: e.MemoryMiB}
	for _, p := range e.Points {
		pt, ok := points[p]
		if !ok {
			return &Error{Extension: e.Name, Op: "install", Err: fmt.Errorf("unknown point %q", p)}
		}
		cfg.Points = append(cfg.Points, pt)
	}
	h.Add(cfg)
	if !verify {
		return nil
	}
	r, err := h.get(e.Name)
	if err != nil {
		return err
	}
	if _, err := h.start(ctx, r); err != nil {
		return &Error{Extension: e.Name, Op: "start", Err: err}
	}
	return nil
}

// points maps desired-state point names to the contract's.
var points = map[string]extensionv1.Point{"notifier": extensionv1.Point_POINT_NOTIFIER, "builder": extensionv1.Point_POINT_BUILDER}

// Remove stops and forgets an extension.
func (h *Host) Remove(name string) {
	h.mu.Lock()
	r := h.exts[name]
	delete(h.exts, name)
	h.mu.Unlock()
	if r != nil {
		r.kill(nil)
	}
}

// Close stops every extension.
func (h *Host) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.exts {
		r.kill(nil)
	}
}

func (h *Host) get(name string) (*running, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.exts[name]
	if !ok {
		return nil, &Error{Extension: name, Op: "find", Err: errors.New("not installed")}
	}
	return r, nil
}

// PID returns a running extension's process id, or 0.
func (h *Host) PID(name string) int {
	r, err := h.get(name)
	if err != nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil || r.client.Exited() {
		return 0
	}
	return r.pid
}

// call runs one method on an extension, starting it if it isn't running.
// A call that times out stops the extension, so a hung one is replaced on
// the next use.
func (h *Host) call(ctx context.Context, name string, point extensionv1.Point, method string, req, res any) error {
	return h.callWithin(ctx, name, point, method, req, res, 0)
}

// callWithin is call with its own timeout; zero uses the extension's.
func (h *Host) callWithin(ctx context.Context, name string, point extensionv1.Point, method string, req, res any, timeout time.Duration) error {
	r, err := h.get(name)
	if err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = r.cfg.CallTimeout
	}
	if point != extensionv1.Point_POINT_UNSPECIFIED && !slices.Contains(r.cfg.Points, point) {
		return &Error{Extension: name, Op: method, Err: fmt.Errorf("%w: point %s", ErrNotGranted, point)}
	}
	conn, err := h.start(ctx, r)
	if err != nil {
		return &Error{Extension: name, Op: "start", Err: err}
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err = conn.Invoke(cctx, method, req, res)
	if err == nil {
		return nil
	}
	r.mu.Lock()
	killed := r.killed
	r.mu.Unlock()
	switch {
	case killed != nil:
		err = killed
	case status.Code(err) == codes.DeadlineExceeded && ctx.Err() == nil:
		r.kill(ErrTimeout)
		err = ErrTimeout
	case status.Code(err) == codes.Unavailable:
		// The process died or dropped the connection: make sure it is gone,
		// so the next call starts it afresh instead of reusing the conn.
		r.kill(nil)
	}
	return &Error{Extension: name, Op: method, Err: err}
}

// start runs the extension if it isn't running, checks its manifest against
// the grants, and configures it with exactly what was granted.
func (h *Host) start(ctx context.Context, r *running) (*grpc.ClientConn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil && !r.client.Exited() {
		return r.conn, nil
	}
	cfg := r.cfg
	if err := checkDigest(cfg.Path, cfg.SHA256); err != nil {
		return nil, err
	}
	logger := h.Logger
	if logger == nil {
		logger = hclog.NewNullLogger()
	}
	cmd := exec.Command(cfg.Path)
	// Nothing of oapd's environment: only what a process needs to run.
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "GOMEMLIMIT=" + strconv.Itoa(cfg.MemoryMiB*3/4) + "MiB"}
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  Handshake,
		Plugins:          plugin.PluginSet{pluginName: &grpcPlugin{}},
		Cmd:              cmd,
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		SkipHostEnv:      true, // go-plugin would otherwise pass on oapd's environment, secrets included
		StartTimeout:     startTimeout,
		Logger:           logger.Named(cfg.Name),
		SyncStdout:       io.Discard,
		SyncStderr:       io.Discard,
	})
	rpc, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, err
	}
	raw, err := rpc.Dispense(pluginName)
	if err != nil {
		client.Kill()
		return nil, err
	}
	conn := raw.(*grpc.ClientConn)
	r.client, r.conn, r.killed, r.stop = client, conn, nil, make(chan struct{})
	if rc := client.ReattachConfig(); rc != nil {
		r.pid = rc.Pid
	}
	go r.watch(r.client, r.pid, r.stop)

	ictx, cancel := context.WithTimeout(ctx, cfg.CallTimeout)
	defer cancel()
	var d extensionv1.DescribeResponse
	if err := conn.Invoke(ictx, "/oap.extension.v1.ExtensionService/Describe", &extensionv1.DescribeRequest{ContractVersion: ContractVersion}, &d); err != nil {
		r.killLocked(nil)
		return nil, fmt.Errorf("describe: %w", err)
	}
	if err := checkManifest(cfg, d.GetManifest()); err != nil {
		r.killLocked(nil)
		return nil, err
	}
	secrets := map[string]string{}
	for _, name := range cfg.Secrets {
		if h.SecretValue == nil {
			r.killLocked(nil)
			return nil, fmt.Errorf("secret %s is granted but the host has no secret store", name)
		}
		v, err := h.SecretValue(name)
		if err != nil {
			r.killLocked(nil)
			return nil, fmt.Errorf("secret %s: %w", name, err)
		}
		secrets[name] = v
	}
	if err := conn.Invoke(ictx, "/oap.extension.v1.ExtensionService/Configure",
		&extensionv1.ConfigureRequest{Settings: cfg.Settings, Secrets: secrets}, &extensionv1.ConfigureResponse{}); err != nil {
		r.killLocked(nil)
		return nil, fmt.Errorf("configure: %w", err)
	}
	return conn, nil
}

// checkManifest refuses an extension whose manifest doesn't match its
// install: another name, a point it doesn't serve, or a grant it never
// asked for.
func checkManifest(cfg Config, m *extensionv1.Manifest) error {
	if m.GetName() != cfg.Name {
		return fmt.Errorf("the binary is extension %q, not %q", m.GetName(), cfg.Name)
	}
	for _, p := range cfg.Points {
		if !slices.Contains(m.GetPoints(), p) {
			return fmt.Errorf("it doesn't serve the granted point %s", p)
		}
	}
	asked := map[string]bool{}
	for _, c := range m.GetCapabilities() {
		switch k := c.GetKind().(type) {
		case *extensionv1.Capability_Setting:
			asked["setting:"+k.Setting] = true
		case *extensionv1.Capability_Secret:
			asked["secret:"+k.Secret] = true
		}
	}
	for s := range cfg.Settings {
		if !asked["setting:"+s] {
			return fmt.Errorf("setting %s is granted but the extension doesn't ask for it", s)
		}
	}
	for _, s := range cfg.Secrets {
		if !asked["secret:"+s] {
			return fmt.Errorf("secret %s is granted but the extension doesn't ask for it", s)
		}
	}
	return nil
}

func checkDigest(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("%w (sha256 %s, recorded %s)", ErrDigest, got, want)
	}
	return nil
}

// watch kills the extension when its resident memory exceeds the cap.
func (r *running) watch(client *plugin.Client, pid int, stop chan struct{}) {
	t := time.NewTicker(memoryPoll)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		if client.Exited() {
			return
		}
		if rss, ok := residentMiB(pid); ok && rss > r.cfg.MemoryMiB {
			r.kill(ErrMemoryCap)
			return
		}
	}
}

func (r *running) kill(why error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.killLocked(why)
}

func (r *running) killLocked(why error) {
	if r.client == nil {
		return
	}
	if why != nil {
		r.killed = why
	}
	if r.stop != nil {
		close(r.stop)
		r.stop = nil
	}
	r.client.Kill()
	r.client = nil
}

// residentMiB reads a process's resident memory: from /proc on Linux, else
// from ps.
func residentMiB(pid int) (int, bool) {
	if f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "VmRSS:"); ok {
				kb, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "kB")))
				return kb / 1024, err == nil
			}
		}
		return 0, false
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, false
	}
	kb, err := strconv.Atoi(strings.TrimSpace(string(out)))
	return kb / 1024, err == nil
}

// Notifier returns an extension as the core's notifier.
func (h *Host) Notifier(name string) Notifier { return Notifier{h: h, name: name} }

// Notifier delivers notifications through an extension.
type Notifier struct {
	h    *Host
	name string
}

func (n Notifier) Notify(ctx context.Context, msg ops.Notification) error {
	var res extensionv1.NotifyResponse
	err := n.h.call(ctx, n.name, extensionv1.Point_POINT_NOTIFIER, "/oap.extension.v1.NotifierService/Notify",
		&extensionv1.NotifyRequest{Kind: msg.Kind, Subject: msg.Subject, Body: msg.Body}, &res)
	if err != nil {
		return err
	}
	if !res.Delivered {
		return &Error{Extension: n.name, Op: "notify", Err: fmt.Errorf("not delivered: %s", res.Detail)}
	}
	return nil
}

// NotifyDetail is Notify that also returns the extension's detail, for
// tests and diagnostics.
func (n Notifier) NotifyDetail(ctx context.Context, msg ops.Notification) (string, error) {
	var res extensionv1.NotifyResponse
	err := n.h.call(ctx, n.name, extensionv1.Point_POINT_NOTIFIER, "/oap.extension.v1.NotifierService/Notify",
		&extensionv1.NotifyRequest{Kind: msg.Kind, Subject: msg.Subject, Body: msg.Body}, &res)
	return res.Detail, err
}

// Fanout sends each notification to the base notifier and, in the
// background, to every installed extension granted the notifier point, so
// a slow, hung or failing extension never holds up or fails the caller.
// Errors returns extensions' failures as they happen, when set.
type Fanout struct {
	Host *Host
	Base interface {
		Notify(context.Context, ops.Notification) error
	}
	Errors func(error)
}

func (f Fanout) Notify(ctx context.Context, n ops.Notification) error {
	var err error
	if f.Base != nil {
		err = f.Base.Notify(ctx, n)
	}
	f.Host.mu.Lock()
	var names []string
	for name, r := range f.Host.exts {
		if slices.Contains(r.cfg.Points, extensionv1.Point_POINT_NOTIFIER) {
			names = append(names, name)
		}
	}
	f.Host.mu.Unlock()
	for _, name := range names {
		go func() {
			if err := f.Host.Notifier(name).Notify(context.WithoutCancel(ctx), n); err != nil && f.Errors != nil {
				f.Errors(err)
			}
		}()
	}
	return err
}

// Builder returns the host as the core's builder: each build goes to the
// builder extension the request names, or to the only one installed.
func (h *Host) Builder() Builder { return Builder{h: h} }

// Builder builds images through builder extensions.
type Builder struct{ h *Host }

func (b Builder) Build(ctx context.Context, req ops.BuildRequest) (ops.BuildResult, error) {
	name := req.Builder
	if name == "" {
		b.h.mu.Lock()
		var found []string
		for n, r := range b.h.exts {
			if slices.Contains(r.cfg.Points, extensionv1.Point_POINT_BUILDER) {
				found = append(found, n)
			}
		}
		b.h.mu.Unlock()
		slices.Sort(found)
		switch len(found) {
		case 0:
			return ops.BuildResult{}, errors.New("no builder extension is installed; install one with oapctl ext install NAME --point builder")
		case 1:
			name = found[0]
		default:
			return ops.BuildResult{}, fmt.Errorf("several builders are installed (%s); name one", strings.Join(found, ", "))
		}
	}
	r, err := b.h.get(name)
	if err != nil {
		return ops.BuildResult{}, err
	}
	var res extensionv1.BuildResponse
	err = b.h.callWithin(ctx, name, extensionv1.Point_POINT_BUILDER, "/oap.extension.v1.BuilderService/Build", &extensionv1.BuildRequest{
		Application: req.Application, Component: req.Component, Repository: req.Repository, Commit: req.Commit,
		Context: req.Context, Dockerfile: req.Dockerfile, Image: req.Image, MemoryMib: int32(req.MemoryMiB)}, &res, r.cfg.BuildTimeout)
	if err != nil {
		return ops.BuildResult{}, err
	}
	if res.Image == "" {
		return ops.BuildResult{}, &Error{Extension: name, Op: "build", Err: errors.New("it returned no image")}
	}
	return ops.BuildResult{Image: res.Image, Commit: res.Commit, LogTail: res.LogTail}, nil
}
