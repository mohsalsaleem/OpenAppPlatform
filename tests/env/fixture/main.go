package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var version = "dev"
var port = "8080"

func main() {
	if configured := os.Getenv("FIXTURE_PORT"); configured != "" {
		port = configured
	}
	log.Printf("fixture %s listening on %s", version, port)
	boot := fmt.Sprint(time.Now().UnixNano())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /upstream", func(w http.ResponseWriter, r *http.Request) {
		endpoint := os.Getenv("UPSTREAM_URL")
		if endpoint == "" {
			http.Error(w, "upstream not configured", 503)
			return
		}
		req, e := http.NewRequestWithContext(r.Context(), "GET", endpoint, nil)
		if e != nil {
			http.Error(w, "invalid endpoint", 502)
			return
		}
		response, e := (&http.Client{Timeout: 3 * time.Second}).Do(req)
		if e != nil {
			http.Error(w, "upstream unavailable", 502)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		io.Copy(w, io.LimitReader(response.Body, 4096))
	})
	mux.HandleFunc("GET /health/custom", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ready")) })
	mux.HandleFunc("GET /health/fail", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "probe fixture unavailable", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ready": true})
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"version": version, "boot": boot, "message": os.Getenv("OAP_TEST_MESSAGE")})
	})
	server := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			cancel()
		}
	}()
	<-ctx.Done()
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer closeCancel()
	server.Shutdown(closeCtx)
}
