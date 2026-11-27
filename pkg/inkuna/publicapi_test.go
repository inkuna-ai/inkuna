package inkuna

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// Another module cannot import internal/, so a public identifier that names
// an internal type is one it can see but never write down or implement. This
// checks every package under pkg/, as the compiler type-checked it, for one.
func TestPublicAPINamesNoInternalType(t *testing.T) {
	cmd := exec.Command("go", "list", "-export", "-deps", "-json=ImportPath,Export,DepOnly", "./pkg/...")
	cmd.Dir = "../.."
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list pkg/ with export data: %v\n%s", err, stderr.String())
	}
	exports := map[string]string{}
	var public []string
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var listed struct {
			ImportPath, Export string
			DepOnly            bool
		}
		if err := decoder.Decode(&listed); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		exports[listed.ImportPath] = listed.Export
		if !listed.DepOnly {
			public = append(public, listed.ImportPath)
		}
	}
	if !slices.Contains(public, "github.com/inkuna-ai/inkuna/pkg/extension") || !slices.Contains(public, "github.com/inkuna-ai/inkuna/pkg/inkuna") {
		t.Fatalf("go list found %v, want pkg/extension and pkg/inkuna among them", public)
	}
	imports := importer.ForCompiler(token.NewFileSet(), "gc", func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok || file == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file)
	})
	for _, path := range public {
		pkg, err := imports.Import(path)
		if err != nil {
			t.Fatalf("import %s: %v", path, err)
		}
		for _, problem := range internalReferences(pkg) {
			t.Error(problem)
		}
	}
}

// The check itself must catch each way a type can leak, or the test above
// would pass for the wrong reason.
func TestInternalReferencesFindsEveryExposure(t *testing.T) {
	fset := token.NewFileSet()
	secret := typeCheck(t, fset, "example.com/m/internal/secret", `package secret
type Key struct{}
func (Key) Open() {}`, nil)
	public := typeCheck(t, fset, "example.com/m/pkg/public", `package public
import "example.com/m/internal/secret"
type Alias = secret.Key
type Holder struct {
	Exposed secret.Key
	hidden  secret.Key
}
type Embeds struct{ secret.Key }
type Source interface{ Get() map[string][]*secret.Key }
type quiet interface{ get() secret.Key }
type Options struct{ Fn func(chan secret.Key) }
func Make() (secret.Key, error) { return secret.Key{}, nil }
func (Holder) Swap(secret.Key) {}
func (Holder) swap(secret.Key) {}
var Default secret.Key
func helper(secret.Key) {}
type Clean struct{ Name string }
`, map[string]*types.Package{"example.com/m/internal/secret": secret})
	got := internalReferences(public)
	want := []string{
		"example.com/m/pkg/public.Alias",
		"example.com/m/pkg/public.Default",
		"example.com/m/pkg/public.Embeds.Key",
		"example.com/m/pkg/public.Holder.Exposed",
		"example.com/m/pkg/public.Holder.Swap",
		"example.com/m/pkg/public.Make",
		"example.com/m/pkg/public.Options.Fn",
		"example.com/m/pkg/public.Source.Get",
	}
	if len(got) != len(want) {
		t.Fatalf("internalReferences found %d:\n%s\nwant %d: %v", len(got), strings.Join(got, "\n"), len(want), want)
	}
	for index, where := range want {
		if !strings.HasPrefix(got[index], where+" ") {
			t.Errorf("finding %d = %q, want it at %s", index, got[index], where)
		}
	}
}

func typeCheck(t *testing.T, fset *token.FileSet, path, source string, deps map[string]*types.Package) *types.Package {
	t.Helper()
	file, err := parser.ParseFile(fset, path+".go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	config := types.Config{Importer: importerFunc(func(path string) (*types.Package, error) {
		if pkg, ok := deps[path]; ok {
			return pkg, nil
		}
		return nil, fmt.Errorf("unknown package %s", path)
	})}
	pkg, err := config.Check(path, fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

type importerFunc func(string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

// internalReferences lists, sorted, each exported identifier in pkg whose type
// another module could see but not name: one that refers to a type declared in
// an internal package.
func internalReferences(pkg *types.Package) []string {
	var found []string
	report := func(where string, typ types.Type) {
		if named := internalType(typ, map[types.Type]bool{}); named != "" {
			found = append(found, fmt.Sprintf("%s.%s names %s", pkg.Path(), where, named))
		}
	}
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		object := scope.Lookup(name)
		if !object.Exported() {
			continue
		}
		typeName, isType := object.(*types.TypeName)
		if !isType {
			report(name, object.Type())
			continue
		}
		if typeName.IsAlias() {
			report(name, object.Type())
			continue
		}
		named, ok := object.Type().(*types.Named)
		if !ok {
			continue
		}
		switch underlying := named.Underlying().(type) {
		case *types.Struct:
			for index := range underlying.NumFields() {
				if field := underlying.Field(index); field.Exported() {
					report(name+"."+field.Name(), field.Type())
				}
			}
		case *types.Interface:
			for index := range underlying.NumMethods() {
				if method := underlying.Method(index); method.Exported() {
					report(name+"."+method.Name(), method.Type())
				}
			}
		default:
			report(name, underlying)
		}
		for index := range named.NumMethods() {
			if method := named.Method(index); method.Exported() {
				report(name+"."+method.Name(), method.Type())
			}
		}
	}
	slices.Sort(found)
	return found
}

// internalType returns the first type declared in an internal package that
// typ refers to, or "". A named type declared outside internal/ is not
// followed: its own package's check covers it, or it belongs to another
// module, which owns what it exposes.
func internalType(typ types.Type, seen map[types.Type]bool) string {
	typ = types.Unalias(typ)
	if seen[typ] {
		return ""
	}
	seen[typ] = true
	switch typ := typ.(type) {
	case *types.Named:
		if pkg := typ.Obj().Pkg(); pkg != nil && isInternal(pkg.Path()) {
			return typ.String()
		}
		for argument := range typ.TypeArgs().Types() {
			if found := internalType(argument, seen); found != "" {
				return found
			}
		}
	case *types.Pointer:
		return internalType(typ.Elem(), seen)
	case *types.Slice:
		return internalType(typ.Elem(), seen)
	case *types.Array:
		return internalType(typ.Elem(), seen)
	case *types.Chan:
		return internalType(typ.Elem(), seen)
	case *types.Map:
		if found := internalType(typ.Key(), seen); found != "" {
			return found
		}
		return internalType(typ.Elem(), seen)
	case *types.Signature:
		for _, tuple := range []*types.Tuple{typ.Params(), typ.Results()} {
			for variable := range tuple.Variables() {
				if found := internalType(variable.Type(), seen); found != "" {
					return found
				}
			}
		}
	case *types.Struct:
		for index := range typ.NumFields() {
			if field := typ.Field(index); field.Exported() {
				if found := internalType(field.Type(), seen); found != "" {
					return found
				}
			}
		}
	case *types.Interface:
		for index := range typ.NumMethods() {
			if method := typ.Method(index); method.Exported() {
				if found := internalType(method.Type(), seen); found != "" {
					return found
				}
			}
		}
	}
	return ""
}

func isInternal(path string) bool {
	return path == "internal" || strings.HasPrefix(path, "internal/") || strings.HasSuffix(path, "/internal") || strings.Contains(path, "/internal/")
}
