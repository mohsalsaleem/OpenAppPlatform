package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const heartbeat = "/tmp/oap-example-heartbeat"

func main() {
	mode := os.Getenv("MODE")
	if mode == "" {
		mode = "api"
	}
	if len(os.Args) > 1 && os.Args[1] == "check-health" {
		if mode == "worker" {
			v, e := os.Stat(heartbeat)
			if e != nil || time.Since(v.ModTime()) > 10*time.Second {
				os.Exit(1)
			}
			return
		}
		c := http.Client{Timeout: 2 * time.Second}
		r, e := c.Get("http://127.0.0.1:8080/health/ready")
		if e != nil {
			os.Exit(1)
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	var pool *pgxpool.Pool
	if mode != "frontend" {
		config, e := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
		if e != nil {
			log.Fatal("database configuration unavailable")
		}
		config.MaxConns = 2
		pool, e = pgxpool.NewWithConfig(ctx, config)
		if e != nil {
			log.Fatal("database connection unavailable")
		}
		defer pool.Close()
	}
	query := func() (time.Time, error) {
		q, c := context.WithTimeout(ctx, 2*time.Second)
		defer c()
		var now time.Time
		e := pool.QueryRow(q, "SELECT current_timestamp").Scan(&now)
		return now, e
	}
	boot := fmt.Sprint(time.Now().UnixNano())
	if mode == "worker" {
		os.Remove(heartbeat)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if _, e := query(); e == nil {
				if e = os.WriteFile(heartbeat, []byte("ready"), 0600); e != nil {
					log.Fatal("heartbeat unavailable")
				}
				log.Printf("worker database connected boot=%s", boot)
			} else {
				log.Print("worker database unavailable")
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /identity", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"mode": mode, "boot": boot})
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		if mode != "frontend" {
			if _, e := query(); e != nil {
				http.Error(w, "database unavailable", 503)
				return
			}
		}
		w.Write([]byte("ready"))
	})
	mux.HandleFunc("GET /upstream", func(w http.ResponseWriter, r *http.Request) {
		c := http.Client{Timeout: 3 * time.Second}
		response, e := c.Get(os.Getenv("UPSTREAM_URL"))
		if e != nil {
			http.Error(w, "API unavailable", 502)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			http.Error(w, "API database unavailable", 502)
			return
		}
		var data map[string]any
		if json.NewDecoder(response.Body).Decode(&data) != nil {
			http.Error(w, "API response unavailable", 502)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if mode == "frontend" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8"><title>Connected Application</title><h1>Connected Application</h1><p>Frontend, API, worker and an existing PostgreSQL database.</p><button id="check">Check API Database Connection</button><pre id="result"></pre><script>document.querySelector('#check').onclick=async()=>{const r=await fetch('/upstream');document.querySelector('#result').textContent=r.ok?JSON.stringify(await r.json(),null,2):'Connection unavailable'}</script></html>`))
			return
		}
		now, e := query()
		if e != nil {
			http.Error(w, "database unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"connected": true, "databaseTime": now.UTC().Format(time.RFC3339), "boot": boot, "mode": mode})
	})
	server := http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		server.Shutdown(shutdown)
	}()
	log.Printf("%s started boot=%s", mode, boot)
	if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal("HTTP server unavailable")
	}
}
