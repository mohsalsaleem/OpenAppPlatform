package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/cli"
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
	operate := flag.Bool("operate", false, "enable scoped lifecycle tools on stdio only")
	credential := flag.String("credential-file", os.Getenv("OAP_CREDENTIAL_FILE"), "origin-bound private agent credential")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() { <-ctx.Done(); os.Stdin.Close() }()
	token := os.Getenv("OAP_API_TOKEN")
	if *credential != "" {
		if token != "" {
			fmt.Fprintln(os.Stderr, "choose one agent credential source")
			os.Exit(1)
		}
		var err error
		token, err = cli.AgentCredential(*credential, *endpoint)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	s := mcp.Server{URL: *endpoint, Token: token, Operate: *operate}
	if e := s.Serve(ctx, os.Stdin, os.Stdout); e != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
