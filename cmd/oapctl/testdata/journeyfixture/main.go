// Command journeyfixture is every component of the release journey's
// application (R5.2), chosen by ROLE: web, api, notes (counts its boots in
// a volume), worker, kv (the data component) and migrate (a job that exits
// with JOB_EXIT_CODE). Its version is set at build time; the "bad" version
// of the api never listens.
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var version = "v1"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "health" {
		os.Exit(health())
	}
	role := os.Getenv("ROLE")
	host, _ := os.Hostname()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	switch role {
	case "migrate":
		code, _ := strconv.Atoi(os.Getenv("JOB_EXIT_CODE"))
		fmt.Printf("migrate %s: exit %d\n", version, code)
		os.Exit(code)
	case "worker":
		for {
			time.Sleep(time.Hour)
		}
	case "api":
		if version == "bad" {
			fmt.Println("api bad: exiting")
			os.Exit(1)
		}
		mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]string{"version": version, "instance": host})
		})
	case "notes":
		boots := 1
		if b, err := os.ReadFile("/data/boots"); err == nil {
			n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			boots = n + 1
		}
		if err := os.WriteFile("/data/boots", []byte(strconv.Itoa(boots)), 0o644); err != nil {
			fmt.Println("notes:", err)
			os.Exit(1)
		}
		mux.HandleFunc("/notes", func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"boots": boots, "version": version})
		})
	case "web", "kv":
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "journey %s %s %s\n", role, version, os.Getenv("APP"))
		})
	default:
		fmt.Println("journeyfixture: set ROLE")
		os.Exit(2)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "80"
	}
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// health is the image's HEALTHCHECK: workers are healthy while they run;
// the others answer /health.
func health() int {
	switch os.Getenv("ROLE") {
	case "worker", "migrate":
		return 0
	}
	res, err := http.Get("http://127.0.0.1:80/health")
	if err != nil || res.StatusCode != 200 {
		return 1
	}
	return 0
}
