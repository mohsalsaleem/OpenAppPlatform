package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// A boot counter demonstrates retained local data without a database or secrets.
func main() {
	raw, err := os.ReadFile("/data/boots")
	if err != nil && !os.IsNotExist(err) {
		log.Fatal("cannot read persistent boot counter")
	}
	count := 0
	if len(raw) > 0 {
		count, err = strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			log.Fatal("invalid persistent boot counter")
		}
	}
	count++
	if err = os.WriteFile("/data/boots.next", []byte(strconv.Itoa(count)), 0600); err != nil {
		log.Fatal("persistent directory is not writable")
	}
	if err = os.Rename("/data/boots.next", "/data/boots"); err != nil {
		log.Fatal("cannot persist boot counter")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ready")) })
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{"boots": count})
	})
	log.Fatal((&http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}).ListenAndServe())
}
