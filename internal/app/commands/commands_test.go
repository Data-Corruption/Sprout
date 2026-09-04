package commands

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestAllCommandConstructorsAreListed(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate commands package")
	}
	sourceDir := filepath.Dir(testFile)

	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		t.Fatalf("read commands package: %v", err)
	}

	declared := make(map[string]bool)
	registered := make(map[string]bool)
	var duplicates []string
	foundList := false
	fset := token.NewFileSet()

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(fset, filepath.Join(sourceDir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok {
				if function.Recv == nil && returnsCLICommand(function.Type) {
					declared[function.Name.Name] = true
				}
				continue
			}

			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.VAR {
				continue
			}
			for _, specification := range general.Specs {
				value, ok := specification.(*ast.ValueSpec)
				if !ok || len(value.Names) != 1 || value.Names[0].Name != "constructors" {
					continue
				}
				if foundList {
					t.Fatal("constructors is declared more than once")
				}
				foundList = true
				if len(value.Values) != 1 {
					t.Fatal("constructors must have one composite literal value")
				}
				list, ok := value.Values[0].(*ast.CompositeLit)
				if !ok {
					t.Fatal("constructors must be a composite literal")
				}
				for _, element := range list.Elts {
					identifier, ok := element.(*ast.Ident)
					if !ok {
						t.Fatalf("constructors entry at %s must be a function identifier", fset.Position(element.Pos()))
					}
					if registered[identifier.Name] {
						duplicates = append(duplicates, identifier.Name)
					}
					registered[identifier.Name] = true
				}
			}
		}
	}

	if !foundList {
		t.Fatal("constructors list not found")
	}

	var missing, unexpected []string
	for name := range declared {
		if !registered[name] {
			missing = append(missing, name)
		}
	}
	for name := range registered {
		if !declared[name] {
			unexpected = append(unexpected, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	sort.Strings(duplicates)

	if len(missing) != 0 {
		t.Errorf("constructors missing command functions: %s", strings.Join(missing, ", "))
	}
	if len(unexpected) != 0 {
		t.Errorf("constructors contains non-command functions: %s", strings.Join(unexpected, ", "))
	}
	if len(duplicates) != 0 {
		t.Errorf("constructors contains duplicate functions: %s", strings.Join(duplicates, ", "))
	}
}

func returnsCLICommand(function *ast.FuncType) bool {
	if function.Results == nil || len(function.Results.List) != 1 {
		return false
	}

	result := function.Results.List[0].Type
	if parenthesized, ok := result.(*ast.ParenExpr); ok {
		result = parenthesized.X
	}
	pointer, ok := result.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Command" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == "cli"
}
