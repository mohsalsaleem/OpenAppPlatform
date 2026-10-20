// Command oapd is OAP's server: one process that keeps desired state, runs
// operations against Docker and Caddy, and serves the agent API. Until
// device login (R3.6) it serves only a local unix socket, which the owner
// reaches over SSH.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/caddy"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/docker"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/extension"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/gitrepo"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/notify"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/secretstore"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sqlite"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/ports"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/listeners"
)

// Version is set at build time.
var Version = "dev"

type config struct {
	data, socket, dockerSocket, install, caddyAdmin string
	edgeMode, apiHost, publicListen                 string
	tls                                             bool
	buildConcurrency                                int
	observeEvery                                    time.Duration
}

func main() {
	var c config
	flag.StringVar(&c.data, "data", env("OAPD_DATA", "/var/lib/oapd"), "data directory: SQLite, desired state, secrets")
	flag.StringVar(&c.socket, "socket", env("OAPD_SOCKET", "/run/oapd/oapd.sock"), "unix socket for the agent API")
	flag.StringVar(&c.dockerSocket, "docker", env("OAPD_DOCKER", "/var/run/docker.sock"), "Docker Engine socket")
	flag.StringVar(&c.install, "install", env("OAPD_INSTALL", ""), "installation id, on every resource OAP creates")
	flag.StringVar(&c.caddyAdmin, "caddy-admin", env("OAPD_CADDY_ADMIN", "/run/oap-caddy/admin.sock"), "Caddy admin API socket")
	flag.StringVar(&c.edgeMode, "edge", env("OAPD_EDGE", "dns"), `"dns": membership through oapd's DNS, no Caddy reload (ADR 0001); "static": reload Caddy on every change`)
	flag.StringVar(&c.apiHost, "api-host", env("OAPD_API_HOST", ""), "public host name for the agent API, routed through Caddy; empty keeps it on the local socket only")
	flag.StringVar(&c.publicListen, "public-listen", env("OAPD_PUBLIC_LISTEN", ":8080"), "address of the token-only public listener, used with --api-host")
	flag.BoolVar(&c.tls, "tls", env("OAPD_TLS", "true") == "true", "serve routed hosts over HTTPS with automatic certificates")
	flag.IntVar(&c.buildConcurrency, "build-concurrency", atoi(env("OAPD_BUILD_CONCURRENCY", "1")), "how many builds run at once")
	flag.DurationVar(&c.observeEvery, "observe-every", 10*time.Second, "how often to re-observe the runtime in full")
	version := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *version {
		fmt.Println(Version)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, c, log); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("oapd stopped", "err", err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// systemClock is the real clock.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

func (systemClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func run(ctx context.Context, c config, log *slog.Logger) error {
	if c.install == "" {
		return errors.New("an installation id is required (--install or OAPD_INSTALL)")
	}
	if err := os.MkdirAll(c.data, 0o700); err != nil {
		return err
	}
	store, err := sqlite.Open(ctx, filepath.Join(c.data, "oap.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	desired, err := gitrepo.Open(ctx, filepath.Join(c.data, "desired"))
	if err != nil {
		return err
	}
	keyPath := filepath.Join(c.data, "secret.key")
	if _, err := os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
		if err := secretstore.CreateKey(keyPath); err != nil {
			return err
		}
		log.Info("created the secret store key; back it up", "path", keyPath)
	}
	secrets, err := secretstore.Open(filepath.Join(c.data, "secrets.json"), keyPath)
	if err != nil {
		return err
	}
	rt, err := docker.New(c.dockerSocket, c.install)
	if err != nil {
		return err
	}
	rt.Secrets = secrets
	admin := caddy.Unix(c.caddyAdmin, func(r ops.InstanceRef) string { return docker.ContainerName(c.install, r) })
	admin.TLS = c.tls
	if c.tls {
		admin.Listen = []string{":443"}
	}
	var edge ports.Edge = admin
	if c.edgeMode == "dns" {
		dns := &caddy.DNSEdge{Admin: admin, Resolver: "udp/oap-" + c.install + "-oapd:53", Refresh: time.Second,
			StatePath: filepath.Join(c.data, "edge.json")}
		if c.apiHost != "" {
			_, port, _ := net.SplitHostPort(c.publicListen)
			dns.Extra = []caddy.StaticRoute{{Host: c.apiHost, Dial: "oap-" + c.install + "-oapd:" + port}}
			if err := dns.EnsureShape(ctx); err != nil {
				return fmt.Errorf("routing the API host: %w", err)
			}
		}
		conn, err := net.ListenPacket("udp", ":53")
		if err != nil {
			return fmt.Errorf("edge DNS: %w", err)
		}
		go func() {
			if err := dns.ServeDNS(ctx, conn); err != nil && ctx.Err() == nil {
				log.Error("edge DNS stopped", "err", err)
			}
		}()
		edge = dns
	}
	clock := systemClock{}
	observer := &engine.Observer{Runtime: rt, Clock: clock, Interval: c.observeEvery}
	if err := observer.Start(ctx); err != nil {
		return fmt.Errorf("first observation: %w", err)
	}
	go func() {
		for ctx.Err() == nil {
			if err := observer.Run(ctx, time.Second); err != nil && ctx.Err() == nil {
				log.Warn("observer", "err", err)
				clock.Sleep(ctx, 5*time.Second)
				observer.Start(ctx)
			}
		}
	}()
	var notifier ports.Notifier = notify.Log{Logger: log}
	if u := os.Getenv("OAPD_WEBHOOK_URL"); u != "" {
		notifier = notify.Webhook{URL: u, Secret: os.Getenv("OAPD_WEBHOOK_SECRET")}
	}
	// Extensions run out of process; a granted secret is resolved only
	// when its extension is configured.
	extensions := &extension.Host{SecretValue: func(name string) (string, error) {
		v, err := secrets.Resolve(ctx, map[string]string{name: name})
		return v[name], err
	}}
	defer extensions.Close()
	notifier = extension.Fanout{Host: extensions, Base: notifier,
		Errors: func(err error) { log.Warn("extension notifier", "err", err) }}
	// Builds go through the queue: one at a time unless the owner says
	// otherwise, each only when the host has its memory.
	builds := &engine.BuildQueue{Builder: extensions.Builder(), Clock: clock, Concurrency: c.buildConcurrency,
		Available: func() (int, bool) {
			if m := observer.Snapshot().Host.MemoryAvailableMiB; m != nil {
				return int(*m), true
			}
			return 0, false
		}}
	svc := &service.Service{Desired: desired, Journal: store, Clock: clock, Observer: observer, Notifier: notifier, SecretStore: secrets,
		Extensions: extensions, Builder: builds,
		Executor: &engine.Executor{Runtime: rt, Edge: edge, Journal: store, Desired: desired, Clock: clock}}
	if err := svc.Start(ctx); err != nil {
		return err
	}

	mux := listeners.Socket(svc)
	if err := os.MkdirAll(filepath.Dir(c.socket), 0o700); err != nil {
		return err
	}
	os.Remove(c.socket)
	ln, err := net.Listen("unix", c.socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(c.socket, 0o600); err != nil {
		return err
	}
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{Handler: mux, Protocols: &protocols, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	if c.apiHost != "" {
		pub := listeners.Public(svc)
		psrv := &http.Server{Addr: c.publicListen, Handler: pub, Protocols: &protocols, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := psrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("public listener", "err", err)
			}
		}()
		go func() {
			<-ctx.Done()
			shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			psrv.Shutdown(shut)
		}()
		log.Info("public agent API", "host", c.apiHost, "listen", c.publicListen)
	}
	log.Info("oapd serving", "version", Version, "socket", c.socket, "install", c.install, "tls", c.tls, "edge", c.edgeMode)
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return ctx.Err()
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
