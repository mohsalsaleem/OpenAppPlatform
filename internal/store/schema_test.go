package store

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSchemaPreflightAndStartupRejectUnknownDowngrade(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	base, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Pool.Close()
	schema := "oap_schema_test_" + domain.NewID()
	if _, err := base.Pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.Pool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	s, err := Open(ctx, url+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Pool.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	old := fstest.MapFS{}
	files, _ := fs.Glob(migrations, "migrations/*.sql")
	for _, file := range files[:len(files)-1] {
		raw, _ := fs.ReadFile(migrations, file)
		old[file] = &fstest.MapFile{Data: raw}
	}
	if err := s.checkSchemaFS(ctx, old); err == nil {
		t.Fatal("unknown migration allowed during preflight")
	}
	if err := s.migrateFS(ctx, old); err == nil {
		t.Fatal("older image allowed to start")
	}
	if err := s.CheckSchema(ctx); err != nil {
		t.Fatal("rejected downgrade changed metadata", err)
	}
}
