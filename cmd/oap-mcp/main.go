package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/mcp"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	base := os.Getenv("OAP_URL")
	if base == "" {
		base = "http://127.0.0.1:8787"
	}
	endpoint := flag.String("url", base, "platform URL")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() { <-ctx.Done(); os.Stdin.Close() }()
	s := mcp.Server{URL: *endpoint, Token: os.Getenv("OAP_API_TOKEN")}
	if e := s.Serve(ctx, os.Stdin, os.Stdout); e != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
