package coolify

import (
	"context"
	"encoding/json"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDatabaseDiscoveryIsScopedCredentialFreeAndReadOnly(t *testing.T) {
	writes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
			http.Error(w, "write forbidden", 500)
			return
		}
		switch r.URL.Path {
		case "/api/v1/projects/p/staging":
			json.NewEncoder(w).Encode(map[string]any{"postgresqls": []map[string]any{{"uuid": "db", "name": "PostgreSQL", "status": "running:healthy", "image": "postgres:17", "postgres_password": "do-not-expose", "internal_db_url": "postgres://private:do-not-expose@db"}}})
		case "/api/v1/databases/db":
			json.NewEncoder(w).Encode(map[string]any{"uuid": "db", "name": "PostgreSQL", "status": "running:healthy", "image": "postgres:17", "postgres_password": "do-not-expose"})
		case "/api/v1/databases/db/logs":
			json.NewEncoder(w).Encode(map[string]string{"logs": "ready"})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("DB_TOKEN", "fixture-token")
	c, err := New(domain.Target{URL: srv.URL, TokenEnv: "DB_TOKEN", ProjectID: "p", Environment: "staging"}, "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := c.Discover(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rows)
	if strings.Contains(string(raw), "do-not-expose") || rows[0].URL != "" || rows[0].ArtifactKind != "database" {
		t.Fatal("sensitive native fields projected")
	}
	if _, err = c.Inspect(context.Background(), "outside"); err == nil {
		t.Fatal("outside resource exposed")
	}
	if logs, err := c.Logs(context.Background(), "db", 10); err != nil || logs != "ready" {
		t.Fatal("database logs unavailable")
	}
	if _, err = c.Ensure(context.Background(), operator.Spec{Component: domain.Component{Kind: "database"}}); err == nil {
		t.Fatal("database ensure allowed")
	}
	if _, err = c.Deploy(context.Background(), "db"); err == nil {
		t.Fatal("database deploy allowed")
	}
	if _, err = c.Restart(context.Background(), "db"); err == nil {
		t.Fatal("database restart allowed")
	}
	if writes != 0 {
		t.Fatal("native database was mutated")
	}
}
