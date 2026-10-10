package store

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func TestGroupMigrationPreservesLegacyEnvironmentAndReleaseIDs(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	base, e := pgxpool.New(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	schema := "oap_group_test_" + domain.NewID()
	if _, e = base.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	s, e := Open(ctx, url+sep+"search_path="+schema)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Pool.Close(); base.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); base.Close() })
	old := fstest.MapFS{}
	names, _ := fs.Glob(migrations, "migrations/*.sql")
	for _, name := range names {
		version, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(name, "migrations/"), "_", 2)[0])
		if version >= 10 {
			continue
		}
		data, _ := migrations.ReadFile(name)
		old[name] = &fstest.MapFile{Data: data}
	}
	if e = s.migrateFS(ctx, old); e != nil {
		t.Fatal(e)
	}
	target := domain.Target{ID: "fixture", Name: "Legacy", Operator: "coolify", URL: "https://operator.invalid", Environment: "staging"}
	if e = s.SaveTarget(ctx, target); e != nil {
		t.Fatal(e)
	}
	ids := []string{domain.NewID(), domain.NewID()}
	release := domain.NewID()
	for i, id := range ids {
		environment := "staging"
		if i == 1 {
			environment = "production"
		}
		m := domain.Manifest{Name: "legacy", Environment: environment, TargetID: target.ID, Components: []domain.Component{{Name: "web", Image: "nginx:alpine", Port: 80, Instances: 1}}}
		raw, _ := json.Marshal(m)
		if _, e = s.Pool.Exec(ctx, "INSERT INTO oap_applications(id,name,environment,spec,version) VALUES($1,'legacy',$2,$3,7)", id, environment, raw); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.Pool.Exec(ctx, "INSERT INTO oap_bindings(target_id,resource_id,application_id,component,ordinal) VALUES('fixture','legacy-resource',$1,'web',1)", ids[0]); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Pool.Exec(ctx, "INSERT INTO oap_deployments(id,application_id,state,spec,steps,idempotency_key,request_hash) VALUES($1,$2,'succeeded','{}','[]','legacy','legacy')", release, ids[0]); e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	groups, e := s.ApplicationGroups(ctx)
	if e != nil || len(groups) != 2 {
		t.Fatal("legacy names auto-merged", e)
	}
	for _, id := range ids {
		a, e := s.Application(ctx, id)
		if e != nil || a.ID != id || a.GroupID != id || a.Version != 7 {
			t.Fatal("legacy environment changed", e)
		}
	}
	var preserved string
	if e = s.Pool.QueryRow(ctx, "SELECT application_id FROM oap_deployments WHERE id=$1", release).Scan(&preserved); e != nil || preserved != ids[0] {
		t.Fatal("release reference changed", e)
	}
	if e = s.Pool.QueryRow(ctx, "SELECT application_id FROM oap_bindings WHERE resource_id='legacy-resource'").Scan(&preserved); e != nil || preserved != ids[0] {
		t.Fatal("binding changed", e)
	}
	legacyInsert := domain.NewID()
	if _, e = s.Pool.Exec(ctx, "INSERT INTO oap_applications(id,name,environment,spec) VALUES($1,'legacy-insert','staging','{}')", legacyInsert); e != nil {
		t.Fatal("old controller insert is incompatible", e)
	}
	var groupID string
	if e = s.Pool.QueryRow(ctx, "SELECT group_id FROM oap_applications WHERE id=$1", legacyInsert).Scan(&groupID); e != nil || groupID != legacyInsert {
		t.Fatal("old controller insert lacked a group", e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal("migration not idempotent", e)
	}
}
