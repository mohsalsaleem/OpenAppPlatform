package system_test

import (
	"context"
	"testing"

	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
)

func TestImageAndConfigurationRecoveryDoNotRewindApplicationDataOrSchema(t *testing.T) {
	c, _, app, old, _ := rollbackHistory(t)
	ctx := context.Background()
	// Separate application namespace, outside the OAP migration/metadata tables.
	schema := "application_data_" + domain.NewID()
	if _, err := c.Store.Pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.Store.Pool.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })
	if _, err := c.Store.Pool.Exec(ctx, "CREATE TABLE "+schema+".customers (id integer PRIMARY KEY, email text); INSERT INTO "+schema+".customers VALUES (1, 'before@example.test'); ALTER TABLE "+schema+".customers DROP COLUMN email; ALTER TABLE "+schema+".customers ADD COLUMN display_name text; INSERT INTO "+schema+".customers VALUES (2, 'after migration')"); err != nil {
		t.Fatal(err)
	}
	assertData := func() {
		t.Helper()
		var rows int
		if err := c.Store.Pool.QueryRow(ctx, "SELECT count(*) FROM "+schema+".customers").Scan(&rows); err != nil || rows != 2 {
			t.Fatal("application writes were changed", err)
		}
		var columnExists bool
		if err := c.Store.Pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name='customers' AND column_name='email')", schema).Scan(&columnExists); err != nil || columnExists {
			t.Fatal("application schema was rewound", err)
		}
	}
	plan, err := c.PlanRollback(ctx, app.ID, old.ID, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	request := controller.RollbackRequest{ReleaseID: old.ID, PlanHash: plan.PlanHash}
	if _, err = c.EnqueueRollback(ctx, app.ID, "no-data-ack", request, app.Version); err == nil {
		t.Fatal("rollback accepted without compatibility acknowledgement")
	}
	assertData()
	request.AcknowledgeDataCompatibility = true
	rolled, err := c.EnqueueRollback(ctx, app.ID, "data-boundary-rollback", request, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		_ = c.Advance(ctx, rolled.ID)
	}
	rolled, err = c.Store.Deployment(ctx, rolled.ID)
	if err != nil || rolled.State != "succeeded" {
		t.Fatal("image release did not finish", err)
	}
	assertData()
	// Acknowledgement is an owner assertion, not automatic schema inspection.
	if _, err = c.Store.Pool.Exec(ctx, "SELECT email FROM "+schema+".customers"); err == nil {
		t.Fatal("old application query unexpectedly compatible")
	}
	changed := app.Manifest
	changed.Components = append([]domain.Component(nil), app.Manifest.Components...)
	changed.Components[0].Port = 8080
	updated, err := c.UpdateApplication(ctx, app.ID, changed, app.Version)
	if err != nil {
		t.Fatal(err)
	}
	configPlan, err := c.PlanConfigurationRestore(ctx, app.ID, old.ID, updated.Version)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := c.RestoreConfiguration(ctx, app.ID, controller.ConfigurationRestoreRequest{ReleaseID: old.ID, PlanHash: configPlan.PlanHash, AcknowledgeEffects: true}, updated.Version)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Manifest.Components[0].Port != app.Manifest.Components[0].Port || restored.Manifest.Components[0].Image != updated.Manifest.Components[0].Image {
		t.Fatal("saved configuration restoration crossed its image boundary")
	}
	assertData()
}
