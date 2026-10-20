package intent

import (
	"fmt"
	"sort"

	oapv1 "github.com/mohsalsaleem/OpenAppPlatform/gen/oap/v1"
	"github.com/mohsalsaleem/OpenAppPlatform/internal/core/spec"
)

// Extension intents (R4.1) change the server's extensions, not an
// application.
const (
	KindInstallExtension Kind = "install-extension"
	KindRemoveExtension  Kind = "remove-extension"
)

// IsExtension reports whether an intent changes extensions.
func IsExtension(in *oapv1.Intent) bool {
	switch in.GetChange().(type) {
	case *oapv1.Intent_InstallExtension, *oapv1.Intent_RemoveExtension:
		return true
	}
	return false
}

// ExtensionResult is a compiled extension intent.
type ExtensionResult struct {
	Kind Kind
	Name string
	// Tree is the new extensions.yaml; Old the extension's previous spec,
	// nil when it is new; New its new spec, nil when it is removed.
	Tree     map[string]any
	Hash     string
	Old, New *spec.Extension
	Changes  []spec.Change
}

// CompileExtension applies an extension intent to the current extensions.
func CompileExtension(current any, in *oapv1.Intent) (*ExtensionResult, *Error) {
	if in.GetApplication() != "" {
		return nil, fail(WhyWrongApplication, "application", "extensions are server-wide; leave the application empty")
	}
	tree, _ := spec.Clone(current).(map[string]any)
	if tree == nil {
		tree = map[string]any{}
	}
	existing, _ := spec.ParseExtensions(tree)
	res := &ExtensionResult{Kind: KindOf(in)}
	switch c := in.GetChange().(type) {
	case *oapv1.Intent_InstallExtension:
		ie := c.InstallExtension
		e := spec.Extension{Name: ie.GetName(), Path: ie.GetPath(), SHA256: ie.GetSha256(), Points: ie.GetPoints(),
			Settings: ie.GetSettings(), Secrets: ie.GetSecrets(), MemoryMiB: int(ie.GetMemoryMib())}
		if e.MemoryMiB == 0 {
			e.MemoryMiB = 64
		}
		sort.Strings(e.Points)
		sort.Strings(e.Secrets)
		if probs := e.Check(); len(probs) > 0 {
			return nil, &Error{Why: WhyInvalidResult, Message: probs[0].Message, Remedy: fix(probs...), Problems: probs}
		}
		res.Name, res.New = e.Name, &e
		op := spec.OpAdd
		if old, ok := existing[e.Name]; ok {
			res.Old, op = &old, spec.OpReplace
		}
		tree[e.Name] = e.Tree()
		res.Changes = []spec.Change{{Path: "extensions." + e.Name, Op: op}}
	case *oapv1.Intent_RemoveExtension:
		name := c.RemoveExtension.GetName()
		old, ok := existing[name]
		if !ok {
			return nil, fail(WhyUnknownComponent, "change.remove_extension.name", "no extension %q is installed", name)
		}
		res.Name, res.Old = name, &old
		delete(tree, name)
		res.Changes = []spec.Change{{Path: "extensions." + name, Op: spec.OpRemove}}
	default:
		return nil, fail(WhyMissingChange, "change", "not an extension intent")
	}
	if res.Old != nil && res.New != nil && spec.Equal(res.Old.Tree(), res.New.Tree()) {
		return nil, &Error{Why: WhyNoChange, Message: fmt.Sprintf("extension %s is already installed as asked", res.Name)}
	}
	res.Tree = tree
	h, err := spec.Hash(tree)
	if err != nil {
		return nil, fail(WhyBadValue, "", "%v", err)
	}
	res.Hash = h
	return res, nil
}
