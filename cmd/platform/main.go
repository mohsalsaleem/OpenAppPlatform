package main

import (
	"context"

	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/config"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/operators"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/routing"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/runtimeinputs"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/secretstore"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/source"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
)

func main() {
	if e := run(); e != nil {
		slog.Error("platform failed", "error", e)
		os.Exit(1)
	}
}
func run() error {
	targetFile := flag.String("targets", os.Getenv("OAP_TARGETS_FILE"), "JSON file containing configured deployment targets")
	sourceFile := flag.String("github-hooks", os.Getenv("OAP_GITHUB_HOOKS_FILE"), "trusted GitHub bindings and image-builder command JSON")
	web := flag.String("web", "web/dist", "compiled dashboard directory")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	preview := os.Getenv("OAP_AUTH_MODE") == "preview"
	if mode := os.Getenv("OAP_AUTH_MODE"); mode != "" && mode != "owner" && mode != "preview" {
		return errors.New("OAP_AUTH_MODE must be owner or preview")
	}
	if preview && len(os.Getenv("OAP_API_TOKEN")) < 24 {
		return errors.New("OAP_API_TOKEN must contain at least 24 characters")
	}
	addr := os.Getenv("OAP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8787"
	}
	if e := validateHTTPBinding(addr, preview, os.Getenv("OAP_COOKIE_SECURE") != "false", os.Getenv("OAP_LOCAL_HTTP_PUBLISHED_IP")); e != nil {
		return e
	}
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	s, e := store.Open(ctx, url)
	if e != nil {
		return errors.New("cannot connect to PostgreSQL")
	}
	defer s.Pool.Close()
	if e = s.Migrate(ctx); e != nil {
		return e
	}
	ownerExists, e := (access.Service{Pool: s.Pool}).HasOwner(ctx)
	if e != nil {
		return e
	}
	if preview && ownerExists {
		return errors.New("preview mode is disabled once an owner account exists")
	}
	if !preview && !ownerExists && len(os.Getenv("OAP_SETUP_TOKEN")) < 24 {
		return errors.New("first-run owner setup requires OAP_SETUP_TOKEN with at least 24 characters")
	}
	if *targetFile != "" {
		b, e := os.ReadFile(*targetFile)
		if e != nil {
			return e
		}
		targets, e := config.Targets(b)
		if e != nil {
			return e
		}
		for _, t := range targets {
			if e = t.Validate(); e != nil {
				return e
			}
			if _, e = operators.New(t); e != nil {
				return e
			}
			if e = s.SaveTarget(ctx, t); e != nil {
				return e
			}
		}
	}

	c, e := controller.New(ctx, s, operators.New)
	if e != nil {
		return e
	}
	if raw := os.Getenv("OAP_RUNTIME_INPUTS"); raw != "" {
		if preview {
			return errors.New("runtime secret bindings require owner mode")
		}
		c.RuntimeInputs, e = runtimeinputs.Parse([]byte(raw), os.Getenv)
		if e != nil {
			return e
		}
	}
	if raw := os.Getenv("OAP_REGISTRY_INPUTS"); raw != "" {
		if preview {
			return errors.New("registry credential bindings require owner mode")
		}
		c.RuntimeInputs, e = runtimeinputs.AddRegistries(c.RuntimeInputs, []byte(raw), os.Getenv)
		if e != nil {
			return e
		}
	}
	stored, status := secretstore.Snapshot(os.Getenv("OAP_SECRET_STORE_FILE"), os.Getenv("OAP_SECRET_KEY_FILE"))
	c.SecretStoreStatus = status
	if status.Enabled && preview {
		return errors.New("encrypted secret store requires owner mode")
	}
	if status.State == "ready" {
		c.RuntimeInputs, e = secretstore.Inputs(c.RuntimeInputs, stored)
		if e != nil {
			return errors.New("runtime input grants conflict across providers")
		}
	}
	if *sourceFile != "" {
		if preview {
			return errors.New("GitHub release bindings require owner mode")
		}
		raw, e := os.ReadFile(*sourceFile)
		if e != nil {
			return e
		}
		config, e := source.Parse(raw)
		if e != nil {
			return e
		}
		if len(config.Command) > 0 {
			c.SourceBuilder = source.CommandBuilder{Command: config.Command, Env: config.BuilderEnv}
		}
		c.SourceHooks = map[string]source.Hook{}
		for _, hook := range config.Hooks {
			c.SourceHooks[hook.ID] = hook
		}
	}
	if raw := os.Getenv("OAP_NATIVE_ROUTES"); raw != "" {
		if preview {
			return errors.New("native route publication requires owner mode")
		}
		c.RouteBindings, e = routing.Parse([]byte(raw))
		if e != nil {
			return e
		}
	}
	c.RequireIdentity = !preview
	if e = c.Jobs.Start(ctx); e != nil {
		return e
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c.Jobs.Stop(stop)
	}()
	server := platformServer(ctx, addr, (&httpapi.Server{Preview: preview, SetupToken: os.Getenv("OAP_SETUP_TOKEN"), SecureCookie: os.Getenv("OAP_COOKIE_SECURE") != "false", Controller: c, Token: os.Getenv("OAP_API_TOKEN"), WebDir: *web}).Handler())
	listener, e := net.Listen("tcp", addr)
	if e != nil {
		return e
	}
	slog.Info("OpenAppPlatform listening", "address", listener.Addr().String())
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case e = <-done:
		if !errors.Is(e, http.ErrServerClosed) {
			return e
		}
	case <-ctx.Done():
	}
	stop, cancelStop := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStop()
	return server.Shutdown(stop)
}
