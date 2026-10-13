package store

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/access"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAgentPermissionMigrationPreservesLegacyCredentialAndAudit(t *testing.T) {
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
	schema := "oap_agent_migration_" + domain.NewID()
	if _, err = base.Pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
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
	old := fstest.MapFS{}
	files, _ := fs.Glob(migrations, "migrations/*.sql")
	for _, file := range files {
		version, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(file, "migrations/"), "_", 2)[0])
		if version >= 17 {
			continue
		}
		raw, _ := fs.ReadFile(migrations, file)
		old[file] = &fstest.MapFile{Data: raw}
	}
	if err = s.migrateFS(ctx, old); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"INSERT INTO oap_targets(id,spec,token_env) VALUES('target','{}','')",
		"INSERT INTO oap_application_groups(id,name) VALUES('app','Legacy')",
		"INSERT INTO oap_applications(id,name,environment,spec,group_id) VALUES('app','Legacy','staging','{}','app')",
		"INSERT INTO oap_users(id,email,name,password_hash,role) VALUES('owner','legacy@example.invalid','Owner','fixture','owner')",
		"INSERT INTO oap_credentials(id,user_id,token_hash,kind,name,scope,application_id,expires_at) VALUES('credential','owner','fixture','agent','Legacy','operate','app',now()+interval '1 day')",
		"INSERT INTO oap_audit(id,user_id,credential_id,action,status) VALUES('audit','owner','credential','fixture',202)",
	} {
		if _, err = s.Pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := (access.Service{Pool: s.Pool}).ByID(ctx, "credential")
	if err != nil || p.CredentialID != "credential" || p.UserID != "owner" || p.Permissions != nil || !p.Operates("app") {
		t.Fatal("legacy credential changed", err)
	}
	var audit string
	var status int
	if err = s.Pool.QueryRow(ctx, "SELECT id,status FROM oap_audit WHERE id='audit'").Scan(&audit, &status); err != nil || audit != "audit" || status != 202 {
		t.Fatal("legacy audit changed", err)
	}
}
