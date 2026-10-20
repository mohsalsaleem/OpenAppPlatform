// Package arch enforces the rewrite's layer rules (docs/architecture/rewrite-plan.md,
// guardrail 8) and its dependency allowlist (guardrail 9).
package arch

import (
	"fmt"
	"sort"
	"strings"
)

// Module is the import path of this repository's Go module.
const Module = "github.com/mohsalsaleem/OpenAppPlatform"

// Package is the slice of `go list -json` output the rules need.
type Package struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Imports    []string
}

// ThirdParty lists the module prefixes the new tree may import.
var ThirdParty = []string{
	"connectrpc.com/connect",
	"google.golang.org/protobuf",
	"github.com/hashicorp/go-plugin",
	// go-plugin's API uses two of its own dependencies: its gRPC mode hands
	// plugins *grpc.Server and *grpc.ClientConn, and its client logs through
	// an hclog.Logger. Only the extension host imports them.
	"google.golang.org/grpc",
	"github.com/hashicorp/go-hclog",
	"modernc.org/sqlite",
	"gopkg.in/yaml.v3",
}

// CLIOnly lists modules only the CLI may import: its terminal interface
// (owner decision, 2026-10-20). oapd and every internal layer stay without them.
var CLIOnly = []string{
	"charm.land/bubbletea/v2",
	"charm.land/lipgloss/v2",
	"charm.land/bubbles/v2",
}

// forbiddenInCore lists standard library packages that perform I/O or bring in
// nondeterminism. Core may not import them, or anything beneath them.
var forbiddenInCore = []string{
	"os", "net", "io/fs", "io/ioutil", "database/sql", "syscall", "log",
	"math/rand", "crypto/rand", "runtime", "plugin", "unsafe",
}

// ForbiddenCalls lists package functions core may not call, keyed by import path.
var ForbiddenCalls = map[string][]string{
	"time": {"Now", "Since", "Until", "Sleep", "After", "AfterFunc", "Tick", "NewTimer", "NewTicker"},
	"fmt":  {"Print", "Printf", "Println", "Scan", "Scanf", "Scanln"},
}

// layer names a part of the new tree and what it may import from this module.
type layer struct {
	name       string
	prefixes   []string // packages in this layer
	mayImport  []string // module-local prefixes it may import (its own layer is implied)
	thirdParty []string // allowed third-party prefixes
}

func local(rel string) string { return Module + "/" + rel }

var layers = []layer{
	{"core", []string{local("internal/core")},
		[]string{local("gen")},
		[]string{"google.golang.org/protobuf"}},
	{"ports", []string{local("internal/ports")},
		[]string{local("internal/core"), local("gen")},
		[]string{"google.golang.org/protobuf"}},
	{"engine", []string{local("internal/engine")},
		[]string{local("internal/core"), local("internal/ports"), local("gen")},
		[]string{"google.golang.org/protobuf"}},
	{"adapters", []string{local("internal/adapters")},
		[]string{local("internal/ports"), local("internal/core/model"), local("internal/core/spec"), local("internal/core/ops"), local("gen")},
		ThirdParty},
	{"service", []string{local("internal/service")},
		[]string{local("internal/core"), local("internal/engine"), local("internal/ports"), local("gen")},
		[]string{"google.golang.org/protobuf", "connectrpc.com/connect"}},
	{"surface", []string{local("internal/surface")},
		[]string{local("internal/service"), local("gen")},
		[]string{"google.golang.org/protobuf", "connectrpc.com/connect"}},
	{"gen", []string{local("gen")},
		nil,
		[]string{"google.golang.org/protobuf", "connectrpc.com/connect"}},
	{"cli", []string{local("cmd/oapctl")},
		[]string{local("internal/core"), local("internal/engine"), local("internal/ports"),
			local("internal/adapters"), local("internal/service"), local("internal/surface"), local("gen")},
		append(append([]string{}, ThirdParty...), CLIOnly...)},
	{"commands", []string{local("cmd/oapd")},
		[]string{local("internal/core"), local("internal/engine"), local("internal/ports"),
			local("internal/adapters"), local("internal/service"), local("internal/surface"), local("gen")},
		ThirdParty},
}

func under(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func underAny(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if under(path, p) {
			return true
		}
	}
	return false
}

// IsStdlib reports whether an import path belongs to the standard library.
func IsStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// LayerOf returns the layer name of a package, or "" when it is not in the new tree.
func LayerOf(path string) string {
	for _, l := range layers {
		if underAny(path, l.prefixes) {
			return l.name
		}
	}
	return ""
}

// CheckImports returns one violation per forbidden import in the new tree.
func CheckImports(pkgs []Package) []string {
	var out []string
	for _, pkg := range pkgs {
		var l *layer
		for i := range layers {
			if underAny(pkg.ImportPath, layers[i].prefixes) {
				l = &layers[i]
				break
			}
		}
		if l == nil {
			continue
		}
		for _, imp := range pkg.Imports {
			if reason := importViolation(*l, imp); reason != "" {
				out = append(out, fmt.Sprintf("%s (%s) imports %s: %s", pkg.ImportPath, l.name, imp, reason))
			}
		}
	}
	sort.Strings(out)
	return out
}

func importViolation(l layer, imp string) string {
	switch {
	case imp == "C":
		return "cgo is not allowed in the new tree"
	case IsStdlib(imp):
		if l.name == "core" {
			for _, f := range forbiddenInCore {
				if under(imp, f) {
					return "core performs no I/O and uses no randomness"
				}
			}
		}
		return ""
	case under(imp, Module):
		if underAny(imp, l.prefixes) || underAny(imp, l.mayImport) {
			return ""
		}
		if LayerOf(imp) == "" {
			return "the new tree must not import the frozen old tree"
		}
		return "dependencies point downward only (rewrite doc section 12)"
	default:
		if underAny(imp, l.thirdParty) {
			return ""
		}
		if underAny(imp, ThirdParty) || underAny(imp, CLIOnly) {
			return "allowlisted dependency, but not for this layer"
		}
		return "not on the dependency allowlist (rewrite plan, guardrail 9)"
	}
}
