package server

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// serverPinningVars type-checks this package (without its tests) and returns the package-level
// variables that can hold a *Server: their type reaches *Server through maps, slices, pointers or
// this package's structs, or they are a sync.Map (also as a field) that some code stores a value
// reaching *Server in, as key or value. Func values are not followed (closures cannot be seen).
func serverPinningVars(t *testing.T) []string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("no go command: %v", err)
	}
	cmd := exec.Command(goBin, "list", "-export", "-deps", "-json", ".")
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	type listed struct {
		ImportPath, Export, Dir string
		GoFiles                 []string
	}
	exports := map[string]string{}
	var self listed
	dec := json.NewDecoder(&out)
	for {
		var p listed
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		exports[p.ImportPath] = p.Export
		self = p // the package itself comes last
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range self.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(self.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		return os.Open(exports[path])
	})
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{}}
	conf := types.Config{Importer: imp}
	pkg, err := conf.Check(self.ImportPath, fset, files, info)
	if err != nil {
		t.Fatalf("type-check: %v", err)
	}
	server := pkg.Scope().Lookup("Server").Type()

	var reaches func(types.Type, map[types.Type]bool) bool
	reaches = func(typ types.Type, seen map[types.Type]bool) bool {
		if seen[typ] {
			return false
		}
		seen[typ] = true
		switch u := typ.(type) {
		case *types.Pointer:
			if types.Identical(u.Elem(), server) {
				return true
			}
			return reaches(u.Elem(), seen)
		case *types.Named:
			if types.Identical(u, server) {
				return true
			}
			if u.Obj().Pkg() != pkg {
				return false
			}
			return reaches(u.Underlying(), seen)
		case *types.Alias:
			return reaches(types.Unalias(u), seen)
		case *types.Map:
			return reaches(u.Key(), seen) || reaches(u.Elem(), seen)
		case *types.Slice:
			return reaches(u.Elem(), seen)
		case *types.Array:
			return reaches(u.Elem(), seen)
		case *types.Chan:
			return reaches(u.Elem(), seen)
		case *types.Struct:
			for i := range u.NumFields() {
				if reaches(u.Field(i).Type(), seen) {
					return true
				}
			}
		}
		return false
	}

	found := map[string]bool{}
	for _, name := range pkg.Scope().Names() {
		if v, ok := pkg.Scope().Lookup(name).(*types.Var); ok && reaches(v.Type(), map[types.Type]bool{}) {
			found[name] = true
		}
	}
	// sync.Map stores: the root package-level variable of the receiver.
	stores := map[string]bool{"Store": true, "LoadOrStore": true, "Swap": true, "CompareAndSwap": true}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !stores[sel.Sel.Name] {
				return true
			}
			fn, ok := info.Uses[sel.Sel].(*types.Func)
			if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "sync" {
				return true
			}
			recv := fn.Signature().Recv()
			if recv == nil || !types.Identical(recv.Type(), types.NewPointer(syncMapType(fn))) {
				return true
			}
			root := sel.X
			for {
				if s, ok := root.(*ast.SelectorExpr); ok {
					root = s.X
					continue
				}
				break
			}
			id, ok := root.(*ast.Ident)
			if !ok {
				return true
			}
			v, ok := info.Uses[id].(*types.Var)
			if !ok || v.Parent() != pkg.Scope() {
				return true
			}
			for _, a := range call.Args {
				if tv, ok := info.Types[a]; ok && reaches(tv.Type, map[types.Type]bool{}) {
					found[v.Name()] = true
				}
			}
			return true
		})
	}
	names := make([]string, 0, len(found))
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// syncMapType is sync.Map, from one of its methods.
func syncMapType(fn *types.Func) types.Type {
	return fn.Pkg().Scope().Lookup("Map").Type()
}

// TestForgetProcessStateCoversEveryServerMap fails when a package-level variable can hold a *Server
// but forgetProcessState does not clear it: a theme-preview sandbox would stay in memory after it
// is closed. Register the variable in processStateForgets (server_forget.go).
func TestForgetProcessStateCoversEveryServerMap(t *testing.T) {
	registered := map[string]bool{}
	for _, f := range processStateForgets {
		if registered[f.name] {
			t.Errorf("%s is registered twice", f.name)
		}
		registered[f.name] = true
	}
	found := serverPinningVars(t)
	if len(found) == 0 {
		t.Fatal("the scan found nothing: it is broken")
	}
	for _, name := range found {
		if !registered[name] {
			t.Errorf("package-level %s can hold a *Server but is not in processStateForgets (server_forget.go)", name)
		}
		delete(registered, name)
	}
	for name := range registered {
		if !processStateForgetUnscanned[name] {
			t.Errorf("processStateForgets has %s, which the scan does not find: remove it or list it in processStateForgetUnscanned", name)
		}
	}
}
