package main

import (
	"context"
	"net"
	"net/http"
	"time"
)

// All request contexts, including long-lived event streams, follow the process
// lifetime. Shutdown must not wait for a browser to close its live connection.
func platformServer(ctx context.Context, addr string, handler http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second}
}
