package system_test

import (
	"context"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/controller"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/operator"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/secretstore"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptedInputsRestartMissingKeyAndIsolatedRecovery(t *testing.T) {
	ctx := context.Background()
	adapter := &inputAdapter{fake: &fake{resources: map[string]operator.Resource{}}}
	c, _ := setup(t, adapter)
	m := manifest()
	m.Components[0].Instances = 1
	m.Components[0].SecretRefs = map[string]string{"TOKEN": "token-v1"}
	app, err := c.CreateApplication(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	path, key := filepath.Join(dir, "store.json"), filepath.Join(dir, "key")
	if err = secretstore.Init(path, key); err != nil {
		t.Fatal(err)
	}
	if err = secretstore.Put(path, key, secretstore.Record{Reference: "token-v1", TargetID: "fixture", ApplicationID: app.ID, Component: "web", Variable: "TOKEN", Value: "encrypted-private-value"}); err != nil {
		t.Fatal(err)
	}
	records, status := secretstore.Snapshot(path, key)
	c.SecretStoreStatus = status
	c.RuntimeInputs, err = secretstore.Inputs(nil, records)
	if err != nil {
		t.Fatal(err)
	}
	d, err := c.Enqueue(ctx, app.ID, "encrypted-durable-operation")
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := os.ReadFile(path)
	keyBytes, _ := os.ReadFile(key)
	recovery := t.TempDir()
	copyStore, copyKey := filepath.Join(recovery, "store.json"), filepath.Join(recovery, "key")
	os.WriteFile(copyStore, cipher, 0600)
	os.WriteFile(copyKey, keyBytes, 0600)
	restarted, err := controller.New(ctx, c.Store, c.Factory)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(copyKey)
	records, status = secretstore.Snapshot(copyStore, copyKey)
	if status.State != "unavailable" || len(records) != 0 {
		t.Fatal("missing key returned data")
	}
	if err = restarted.Advance(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	held, err := c.Store.Deployment(ctx, d.ID)
	if err != nil || held.State != "attention" || adapter.ensures != 0 || adapter.deploys != 0 {
		t.Fatal("missing key dispatched")
	}
	os.WriteFile(copyKey, keyBytes, 0600)
	records, status = secretstore.Snapshot(copyStore, copyKey)
	restarted.SecretStoreStatus = status
	restarted.RuntimeInputs, err = secretstore.Inputs(nil, records)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.Recover(ctx, d.ID, controller.Recovery{Component: "web", Ordinal: 1, RetryPreparation: true, ExpectedUpdatedAt: held.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 5; n++ {
		restarted.Advance(ctx, d.ID)
	}
	completed, err := c.Store.Deployment(ctx, d.ID)
	if err != nil || completed.State != "succeeded" || adapter.ensures != 1 || adapter.deploys != 1 {
		t.Fatal("recovery duplicated execution", err)
	}
	if strings.Contains(restarted.RuntimeInputs.Redact(adapter.value), adapter.value) {
		t.Fatal("recovered value not redacted")
	}
}
