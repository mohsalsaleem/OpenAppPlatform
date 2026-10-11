// A small container-ready API for experimenting with application components.
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

func main() {
	instance, _ := os.Hostname()
	boot := fmt.Sprint(time.Now().UnixNano())
	version := os.Getenv("VERSION")
	if version == "" {
		version = "dev"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /demo", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Open App Platform · Connected Preview</title><style>body{font:16px system-ui;margin:0;background:#f4f6fa;color:#17243c}main{max-width:760px;margin:60px auto;padding:24px}small{color:#546580}h1{font-size:32px}section{background:white;border:1px solid #dce3ed;border-radius:12px;padding:24px;margin:24px 0}button{background:#2854df;color:white;border:0;border-radius:6px;padding:12px 18px;font:inherit;cursor:pointer}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#f4f6fa;padding:16px;border-radius:6px}#state{font-weight:600}p{line-height:1.6}</style><main><small>OPEN APP PLATFORM · STAGING DEMO</small><h1>One application. Connected services.</h1><p>This frontend calls a stable API endpoint backed by two native Coolify instances. Open App Platform groups and manages the components; the native proxy carries application traffic.</p><section><p id="state" role="status">Ready to request the API</p><button id="request">Call the API</button><pre id="result">Responses show the backend instance, version and process identity.</pre><small>Each click makes a new request. Compare instance IDs to see traffic distribution.</small></section><p>Use OAP to inspect health and logs, review a selected-component update, and recover a verified image release. This is synthetic demo data.</p></main><script>document.getElementById('request').onclick=async()=>{const state=document.getElementById('state');state.textContent='Requesting…';try{const response=await fetch('/upstream',{cache:'no-store'});if(!response.ok)throw Error('API returned '+response.status);const result=await response.json();document.getElementById('result').textContent=JSON.stringify(result,null,2);state.textContent='API request succeeded'}catch(error){state.textContent=error.message}}</script></html>`))
	})
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
		json.NewEncoder(w).Encode(map[string]string{"message": "Hello from Open App Platform", "version": version, "instance": instance, "boot": boot})
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
