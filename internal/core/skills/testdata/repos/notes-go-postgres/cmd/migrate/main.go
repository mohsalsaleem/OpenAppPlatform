// Command migrate applies the SQL files in migrations/ in order, once.
package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5"
)

func main() {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	files, _ := filepath.Glob("/app/migrations/*.sql")
	sort.Strings(files)
	conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (name text PRIMARY KEY)")
	for _, f := range files {
		var done bool
		conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)", f).Scan(&done)
		if done {
			continue
		}
		sql, _ := os.ReadFile(f)
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			log.Fatalf("%s: %v", f, err)
		}
		conn.Exec(ctx, "INSERT INTO schema_migrations (name) VALUES ($1)", f)
		log.Printf("applied %s", f)
	}
}
