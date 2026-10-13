package backupreports

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReportFeedIsBoundedRedactedAndDoesNotFollowSymlinks(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()
	report := Report{Version: 1, ID: strings.Repeat("a", 32), Action: "backup", Status: "succeeded", Source: "host-helper", Coverage: "metadata-only", StartedAt: now, UpdatedAt: now, SHA256: strings.Repeat("b", 64), SizeBytes: 1024}
	raw, _ := json.Marshal(report)
	if err := os.WriteFile(filepath.Join(dir, report.ID+".json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(private, []byte(`{"password":"do-not-expose"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, filepath.Join(dir, strings.Repeat("c", 32)+".json")); err != nil {
		t.Fatal(err)
	}
	bad := strings.Repeat("d", 32) + ".json"
	if err := os.WriteFile(filepath.Join(dir, bad), []byte(`{"password":"do-not-expose"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, strings.Repeat("e", 32)+".json"), []byte(strings.Repeat("x", 20000)), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := Read(dir)
	if err != nil || !out.Configured || len(out.Reports) != 1 || out.InvalidReports != 2 {
		t.Fatal("report validation failed", out, err)
	}
	body, _ := json.Marshal(out)
	if strings.Contains(string(body), "do-not-expose") || strings.Contains(string(body), private) {
		t.Fatal("private report input leaked")
	}
	out, err = Read("")
	if err != nil || out.Configured || len(out.Reports) != 0 {
		t.Fatal("unconfigured feed misreported")
	}
	if _, err = Read("relative"); err == nil {
		t.Fatal("relative trusted path accepted")
	}
}
func TestPendingAndFailedReportsNeverClaimVerifiedArchive(t *testing.T) {
	now := time.Now().UTC()
	r := Report{Version: 1, ID: strings.Repeat("a", 32), Action: "restore-check", Status: "running", Source: "host-helper", Coverage: "metadata-only", StartedAt: now, UpdatedAt: now}
	if !r.valid() {
		t.Fatal("pending intent rejected")
	}
	r.SHA256 = strings.Repeat("b", 64)
	if r.valid() {
		t.Fatal("pending intent claimed archive")
	}
	r.SHA256 = ""
	r.Status = "failed"
	r.ErrorCode = "checksum-mismatch"
	if !r.valid() {
		t.Fatal("known safe failure rejected")
	}
	r.ErrorCode = "private raw error"
	if r.valid() {
		t.Fatal("arbitrary failure detail accepted")
	}
}
