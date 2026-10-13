package main

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/cli"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout)
	cancel()
	os.Exit(code)
}
