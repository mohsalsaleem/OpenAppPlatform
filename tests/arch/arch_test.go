package arch

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func listPackages(t *testing.T) []Package {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = "../.."
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	var pkgs []Package
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p Package
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

func TestNewTreeImportsFollowLayerRules(t *testing.T) {
	pkgs := listPackages(t)
	seen := map[string]bool{}
	for _, p := range pkgs {
		if l := LayerOf(p.ImportPath); l != "" {
			seen[l] = true
		}
	}
	for _, want := range []string{"core", "ports", "engine", "adapters", "service", "surface", "commands"} {
		if !seen[want] {
			t.Errorf("no packages found for layer %q; the check would pass vacuously", want)
		}
	}
	for _, v := range CheckImports(pkgs) {
		t.Error(v)
	}
}

func TestCoreCallsNoClockOrConsole(t *testing.T) {
	for _, p := range listPackages(t) {
		if LayerOf(p.ImportPath) != "core" {
			continue
		}
		for _, f := range p.GoFiles {
			violations, err := CheckCalls(filepath.Join(p.Dir, f), nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range violations {
				t.Error(v)
			}
		}
	}
}

// The tests below prove the checks reject what they should, so a passing run
// on the real tree means something.

func pkg(path string, imports ...string) Package {
	return Package{ImportPath: local(path), Imports: imports}
}

func TestCheckImportsRejectsViolations(t *testing.T) {
	cases := []struct {
		name string
		pkg  Package
		want string
	}{
		{"core uses os", pkg("internal/core/plan", "os"), "no I/O"},
		{"core uses net/http", pkg("internal/core", "net/http"), "no I/O"},
		{"core uses math/rand/v2", pkg("internal/core", "math/rand/v2"), "no randomness"},
		{"core uses slog", pkg("internal/core", "log/slog"), "no I/O"},
		{"core imports engine", pkg("internal/core", local("internal/engine")), "downward only"},
		{"engine imports adapters", pkg("internal/engine", local("internal/adapters/docker")), "downward only"},
		{"engine imports service", pkg("internal/engine", local("internal/service")), "downward only"},
		{"adapters import engine", pkg("internal/adapters/docker", local("internal/engine")), "downward only"},
		{"adapters import core internals", pkg("internal/adapters/sqlite", local("internal/core/policy")), "downward only"},
		{"surface imports core", pkg("internal/surface/mcp", local("internal/core")), "downward only"},
		{"new tree imports old tree", pkg("internal/engine", local("internal/controller")), "frozen old tree"},
		{"command imports old tree", pkg("cmd/oapd", local("internal/store")), "frozen old tree"},
		{"unlisted dependency", pkg("internal/adapters/sqlite", "github.com/mattn/go-sqlite3"), "allowlist"},
		{"allowlisted but wrong layer", pkg("internal/core", "modernc.org/sqlite"), "not for this layer"},
		{"cgo", pkg("internal/adapters/x", "C"), "cgo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CheckImports([]Package{c.pkg})
			if len(got) != 1 || !strings.Contains(got[0], c.want) {
				t.Fatalf("got %q, want one violation containing %q", got, c.want)
			}
		})
	}
}

func TestCheckImportsAcceptsAllowedImports(t *testing.T) {
	ok := []Package{
		pkg("internal/core/plan", "fmt", "sort", "time", "crypto/sha256", local("internal/core/model"), local("gen/oap/v1")),
		pkg("internal/ports", "context", local("internal/core/model")),
		pkg("internal/engine", local("internal/core"), local("internal/ports")),
		pkg("internal/adapters/sqlite", "database/sql", "modernc.org/sqlite", local("internal/ports"), local("internal/core/model")),
		pkg("internal/adapters/desiredyaml", "gopkg.in/yaml.v3", local("internal/core/spec")),
		pkg("internal/service", local("internal/engine"), local("internal/core/policy")),
		pkg("internal/surface/connect", "net/http", "connectrpc.com/connect", local("internal/service")),
		pkg("cmd/oapd", "os", local("internal/adapters/docker"), local("internal/surface")),
		pkg("internal/controller", "github.com/jackc/pgx/v5"), // old tree: not checked
	}
	if got := CheckImports(ok); len(got) != 0 {
		t.Fatalf("unexpected violations: %q", got)
	}
}

func TestCheckCallsRejectsClockAndConsole(t *testing.T) {
	src := `package plan
import (
	"fmt"
	clock "time"
)
func f() {
	_ = clock.Now()
	fmt.Println("x")
	_ = fmt.Sprintf("ok")
	_ = clock.Duration(1)
}`
	got, err := CheckCalls("plan.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.Contains(got[0], "time.Now") || !strings.Contains(got[1], "fmt.Println") {
		t.Fatalf("got %q", got)
	}
	got, err = CheckCalls("dot.go", "package plan\nimport . \"time\"\nvar _ = Now")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "dot import") {
		t.Fatalf("got %q", got)
	}
}
