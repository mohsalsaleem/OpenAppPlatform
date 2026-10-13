package secretstore

import (
	"bytes"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/domain"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptedStoreRecoveryAndScope(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	path, keyPath := filepath.Join(dir, "store.json"), filepath.Join(dir, "key")
	if err := Init(path, keyPath); err != nil {
		t.Fatal(err)
	}
	record := Record{Reference: "token-v1", TargetID: "docker", ApplicationID: strings.Repeat("a", 32), Component: "web", Variable: "TOKEN", Value: "private-stored-value"}
	if err := Put(path, keyPath, record); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte(record.Value)) || bytes.Contains(raw, []byte(record.ApplicationID)) {
		t.Fatal("plaintext stored")
	}
	records, status := Snapshot(path, keyPath)
	if status.State != "ready" || len(status.Grants) != 1 {
		t.Fatal("status unavailable")
	}
	inputs, err := Inputs(nil, records)
	if err != nil {
		t.Fatal(err)
	}
	component := domain.Component{Name: "web", SecretRefs: map[string]string{"TOKEN": "token-v1"}}
	resolved, err := inputs.Resolve("docker", record.ApplicationID, component)
	if err != nil || resolved.Env["TOKEN"] != record.Value {
		t.Fatal("scoped resolution failed")
	}
	if _, err = inputs.Resolve("docker", strings.Repeat("b", 32), component); err == nil {
		t.Fatal("wrong app received value")
	}
	if !strings.Contains(inputs.Redact(record.Value), "[REDACTED]") {
		t.Fatal("value not redacted")
	}
	keyBytes, _ := os.ReadFile(keyPath)
	copyDir := t.TempDir()
	copyStore, copyKey := filepath.Join(copyDir, "store.json"), filepath.Join(copyDir, "key")
	os.WriteFile(copyStore, raw, 0600)
	os.WriteFile(copyKey, keyBytes, 0600)
	if _, err = Load(copyStore, copyKey); err != nil {
		t.Fatal("isolated recovery failed", err)
	}
	os.WriteFile(copyKey, bytes.Repeat([]byte{1}, 32), 0600)
	if _, err = Load(copyStore, copyKey); err == nil {
		t.Fatal("wrong key accepted")
	}
	os.Remove(copyKey)
	if _, status = Snapshot(copyStore, copyKey); status.State != "unavailable" || len(status.Grants) != 0 {
		t.Fatal("missing key exposed status/grants")
	}
	os.WriteFile(copyKey, keyBytes, 0644)
	if _, err = Load(copyStore, copyKey); err == nil {
		t.Fatal("public key permissions accepted")
	}
	os.Chmod(copyKey, 0600)
	raw[len(raw)/2] ^= 1
	os.WriteFile(copyStore, raw, 0600)
	if _, err = Load(copyStore, copyKey); err == nil {
		t.Fatal("tampered store accepted")
	}
	if err = Init(path, keyPath); err == nil {
		t.Fatal("existing store overwritten")
	}
}
func TestOwnerSuppliedKeyAndStaleWriterLock(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	path, keyPath := filepath.Join(dir, "store.json"), filepath.Join(dir, "key")
	supplied := bytes.Repeat([]byte{2}, 32)
	os.WriteFile(keyPath, supplied, 0600)
	if err := Init(path, keyPath); err != nil {
		t.Fatal(err)
	}
	current, _ := os.ReadFile(keyPath)
	if !bytes.Equal(supplied, current) {
		t.Fatal("owner key changed")
	}
	os.WriteFile(path+".lock", nil, 0600)
	if err := Put(path, keyPath, Record{}); err == nil {
		t.Fatal("stale lock bypassed")
	}
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatal("stale lock deleted automatically")
	}
}
