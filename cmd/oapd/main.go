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
	"syscall"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/caddy"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/docker"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/gitrepo"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/secretstore"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/adapters/sqlite"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/ops"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/engine"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/service"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/surface/agentapi"
)

// Version is set at build time.
var Version = "dev"

type config struct {
	data, socket, dockerSocket, install, caddyAdmin string
	tls                                             bool
	observeEvery                                    time.Duration
}

func main() {
	var c config
	flag.StringVar(&c.data, "data", env("OAPD_DATA", "/var/lib/oapd"), "data directory: SQLite, desired state, secrets")
	flag.StringVar(&c.socket, "socket", env("OAPD_SOCKET", "/run/oapd/oapd.sock"), "unix socket for the agent API")
	flag.StringVar(&c.dockerSocket, "docker", env("OAPD_DOCKER", "/var/run/docker.sock"), "Docker Engine socket")
	flag.StringVar(&c.install, "install", env("OAPD_INSTALL", ""), "installation id, on every resource OAP creates")
	flag.StringVar(&c.caddyAdmin, "caddy-admin", env("OAPD_CADDY_ADMIN", "/run/oap-caddy/admin.sock"), "Caddy admin API socket")
	flag.BoolVar(&c.tls, "tls", env("OAPD_TLS", "true") == "true", "serve routed hosts over HTTPS with automatic certificates")
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
	edge := caddy.Unix(c.caddyAdmin, func(r ops.InstanceRef) string { return docker.ContainerName(c.install, r) })
	edge.TLS = c.tls
	if c.tls {
		edge.Listen = []string{":443"}
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
	svc := &service.Service{Desired: desired, Journal: store, Clock: clock, Observer: observer,
		Executor: &engine.Executor{Runtime: rt, Edge: edge, Journal: store, Desired: desired, Clock: clock}}
	if err := svc.Start(ctx); err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle((&agentapi.Server{Service: svc, Caller: service.Owner}).Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
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
	log.Info("oapd serving", "version", Version, "socket", c.socket, "install", c.install, "tls", c.tls)
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return ctx.Err()
}
