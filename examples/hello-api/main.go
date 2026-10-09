// A small container-ready API for experimenting with application components.
package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	version := os.Getenv("VERSION")
	if version == "" {
		version = "dev"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /upstream", func(w http.ResponseWriter, r *http.Request) {
		endpoint := os.Getenv("UPSTREAM_URL")
		if endpoint == "" {
			http.Error(w, "upstream is not configured", 503)
			return
		}
		request, e := http.NewRequestWithContext(r.Context(), "GET", endpoint, nil)
		if e != nil {
			http.Error(w, "invalid upstream", 502)
			return
		}
		response, e := (&http.Client{Timeout: 3 * time.Second}).Do(request)
		if e != nil {
			http.Error(w, "upstream unavailable", 502)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		io.Copy(w, io.LimitReader(response.Body, 4096))
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ready": true})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "Hello from OpenAppPlatform", "version": version})
	})
	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			log.Print(e)
			cancel()
		}
	}()
	<-ctx.Done()
	stop, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	server.Shutdown(stop)
}
