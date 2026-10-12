package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// All request contexts, including long-lived event streams, follow the process
// lifetime. Shutdown must not wait for a browser to close its live connection.
func platformServer(ctx context.Context, addr string, handler http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: handler, BaseContext: func(net.Listener) context.Context { return ctx }, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second}
}

// PublishedIP is a trusted deployment declaration, not request input. The owner
// Compose profile derives it from the same IP used for its host port binding.
// Preview mode never accepts a container-wide insecure listener.
func validateHTTPBinding(addr string, preview, secure bool, publishedIP string) error {
	if !preview && secure {
		return nil
	}
	host, _, err := net.SplitHostPort(addr)
	if err == nil {
		ip := net.ParseIP(host)
		if ip != nil && ip.IsLoopback() {
			return nil
		}
		published := net.ParseIP(publishedIP)
		if !preview && ip != nil && ip.IsUnspecified() && published != nil && published.IsLoopback() {
			return nil
		}
	}
	return errors.New("preview requires a loopback listener; insecure owner cookies require a loopback listener or an explicitly declared loopback host publication")
}
