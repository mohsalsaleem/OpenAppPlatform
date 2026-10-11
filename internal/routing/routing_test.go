package routing

import (
	"encoding/json"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture() Binding {
	return Binding{ApplicationID: strings.Repeat("a", 32), Component: "api", TargetID: "staging", Name: "oap-test", Domain: "api.example.test", HealthPath: "/health/ready"}
}
func TestRendererRejectsUnsafeOriginsAndPreservesCustomNativeObjects(t *testing.T) {
	b := fixture()
	urls := []string{"https://one.example.test", "https://two.example.test"}
	raw, e := Render(b, urls)
	if e != nil {
		t.Fatal(e)
	}
	allowed := map[string]bool{urls[0]: true, urls[1]: true}
	if _, e = Backends(b, raw, allowed); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"https://@one.example.test", "https://u:p@one.example.test", "https://api.example.test", "https://one.example.test/path", "https://one.example.test?x=y", "https://one.example.test:bad", "https://one.example.test\n"} {
		if _, e = Render(b, []string{bad}); e == nil {
			t.Fatalf("accepted unsafe backend %q", bad)
		}
	}
	var doc map[string]any
	json.Unmarshal(raw, &doc)
	doc["tcp"] = map[string]any{}
	custom, _ := json.Marshal(doc)
	if _, e = Backends(b, custom, allowed); e == nil {
		t.Fatal("custom native objects accepted")
	}
	if _, e = Backends(b, raw, map[string]bool{urls[0]: true}); e == nil {
		t.Fatal("foreign backend accepted")
	}
}
func TestNativeFilePublicationDriftRetryAndSymlinks(t *testing.T) {
	previous := unix.Umask(0077)
	defer unix.Umask(previous)
	b := fixture()
	b.Directory = t.TempDir()
	path := filepath.Join(b.Directory, b.Name+".yaml")
	before, _ := Render(b, []string{"https://one.example.test"})
	desired, _ := Render(b, []string{"https://one.example.test", "https://two.example.test"})
	os.WriteFile(path, before, 0644)
	f, e := Open(b)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	os.WriteFile(path, append(before, '\n'), 0644)
	if _, e = f.Publish(Hash(before), desired); e == nil {
		t.Fatal("drift overwritten")
	}
	current, _ := f.Read()
	if Hash(current) != Hash(append(before, '\n')) {
		t.Fatal("drift not preserved")
	}
	os.WriteFile(path, before, 0644)
	if status, e := f.Publish(Hash(before), desired); e != nil || status != "applied" {
		t.Fatal(status, e)
	}
	stat, _ := os.Stat(path)
	if stat.Mode().Perm() != 0644 {
		t.Fatal("published native route is not readable by the proxy")
	}
	if status, e := f.Publish(Hash(before), desired); e != nil || status != "alreadyApplied" {
		t.Fatal(status, e)
	}
	after, _ := os.Stat(path)
	if !os.SameFile(stat, after) || !stat.ModTime().Equal(after.ModTime()) {
		t.Fatal("retry replaced the native file")
	}
	os.Remove(path)
	os.WriteFile(filepath.Join(b.Directory, "other"), before, 0644)
	os.Symlink("other", path)
	if _, e = f.Read(); e == nil {
		t.Fatal("symlink accepted")
	}
	os.Remove(path)
	os.WriteFile(path, []byte(strings.Repeat("x", 65537)), 0644)
	if _, e = f.Read(); e == nil {
		t.Fatal("oversized file accepted")
	}
}
func TestTrustedBindingConfigurationIsBounded(t *testing.T) {
	b := fixture()
	b.Directory = "/native-routes"
	raw, _ := json.Marshal([]Binding{b})
	if _, e := Parse(raw); e != nil {
		t.Fatal(e)
	}
	for _, bad := range [][]byte{append(raw, []byte(" trailing")...), append(raw, raw...)} {
		if _, e := Parse(bad); e == nil {
			t.Fatal("extra configuration accepted")
		}
	}
	b.Name = "../foreign"
	raw, _ = json.Marshal([]Binding{b})
	if _, e := Parse(raw); e == nil {
		t.Fatal("path traversal accepted")
	}
}
