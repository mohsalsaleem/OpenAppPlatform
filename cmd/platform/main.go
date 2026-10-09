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

	"github.com/mohsalsaleem/OpenAppPlatform/internal/config"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/httpapi"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/operators"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/store"
)

func main() {
	if e := run(); e != nil {
		slog.Error("platform failed", "error", e)
		os.Exit(1)
	}
}
func run() error {
	targetFile := flag.String("targets", "", "JSON file containing configured deployment targets")
	web := flag.String("web", "web/dist", "compiled dashboard directory")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if len(os.Getenv("OAP_API_TOKEN")) < 24 {
		return errors.New("OAP_API_TOKEN must contain at least 24 characters")
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
	if e = c.Jobs.Start(ctx); e != nil {
		return e
	}
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c.Jobs.Stop(stop)
	}()
	addr := os.Getenv("OAP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8787"
	}
	server := &http.Server{Addr: addr, Handler: (&httpapi.Server{Controller: c, Token: os.Getenv("OAP_API_TOKEN"), WebDir: *web}).Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second}
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
