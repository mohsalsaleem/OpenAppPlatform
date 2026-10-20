// Command notes serves a small notes API backed by Postgres.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx := context.Background()
	db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		log.Fatal("SESSION_SECRET is required")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /notes", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(r.Context(), "SELECT id, body FROM notes ORDER BY id")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer rows.Close()
		var out []map[string]any
		for rows.Next() {
			var id int
			var body string
			rows.Scan(&id, &body)
			out = append(out, map[string]any{"id": id, "body": body})
		}
		json.NewEncoder(w).Encode(out)
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("listening on :%s (log level %s)", port, os.Getenv("LOG_LEVEL"))
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
