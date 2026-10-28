package arch

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strconv"
)

// CheckCalls parses Go source and returns one violation per call to a function
// in ForbiddenCalls, or per dot import, which would hide such calls.
func CheckCalls(filename string, src any) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	names := map[string]string{} // local name -> import path
	var out []string
	for _, imp := range file.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if _, watched := ForbiddenCalls[p]; !watched {
			continue
		}
		name := path.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == "." {
			out = append(out, fmt.Sprintf("%s: dot import of %s is not allowed in core", fset.Position(imp.Pos()), p))
			continue
		}
		names[name] = p
	}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		p, ok := names[id.Name]
		if !ok {
			return true
		}
		for _, f := range ForbiddenCalls[p] {
			if sel.Sel.Name == f {
				out = append(out, fmt.Sprintf("%s: %s.%s is not allowed in core (no clock or console I/O)",
					fset.Position(sel.Pos()), p, f))
			}
		}
		return true
	})
	sort.Strings(out)
	return out, nil
}

// CheckInterfacesOnly parses Go source and returns one violation per
// declaration that is not an interface type. Ports hold interfaces only.
func CheckInterfacesOnly(filename string, src any) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			out = append(out, fmt.Sprintf("%s: func %s; ports declare interfaces only", fset.Position(d.Pos()), d.Name.Name))
		case *ast.GenDecl:
			if d.Tok == token.IMPORT {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					out = append(out, fmt.Sprintf("%s: %s declaration; ports declare interfaces only", fset.Position(spec.Pos()), d.Tok))
					continue
				}
				if _, iface := ts.Type.(*ast.InterfaceType); !iface {
					out = append(out, fmt.Sprintf("%s: type %s is not an interface; put data types in internal/core/ops", fset.Position(ts.Pos()), ts.Name.Name))
				}
			}
		}
	}
	return out, nil
}
