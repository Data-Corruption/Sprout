// --- FILE template ---

package cut

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// Module renaming is part of the cutter's final preflight rather than a
// separate text-replacement step. Ownership markers can appear inside Go
// import blocks, where an early gofmt could move imports across marker comments
// and change the apparent ownership. Run first plans every cut and marker
// removal; this code then parses the resulting Go source and rewrites import
// literals before any file is touched.
//
// prepareModuleRename parses go.mod and validates the destination with x/mod.
// rewriteGoImports changes only imports equal to the old module or beneath its
// path. An old module prefix in an application string or a non-documentation,
// non-Go file aborts the complete preflight instead of guessing. References in
// binary files and all symlinks are rejected as well. The setup machinery is
// exempt from the string check because its tests intentionally contain
// module-path fixtures and the machinery is removed after setup.
//
// Build scripts derive their -ldflags package from `go list -m`, so this
// transformation has no separate shell or PowerShell module path to synchronize.
type moduleRename struct {
	oldPath string
	newPath string
	modFile []byte
}

func prepareModuleRename(root, newPath string) (*moduleRename, error) {
	if newPath == "" {
		return nil, nil
	}
	if err := module.CheckPath(newPath); err != nil {
		return nil, fmt.Errorf("invalid module path %q: %w", newPath, err)
	}

	path := filepath.Join(root, "go.mod")
	original, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read go.mod: %w", err)
	}
	parsed, err := modfile.Parse("go.mod", original, nil)
	if err != nil {
		return nil, fmt.Errorf("parse go.mod: %w", err)
	}
	if parsed.Module == nil || parsed.Module.Mod.Path == "" {
		return nil, fmt.Errorf("go.mod has no module directive")
	}
	oldPath := parsed.Module.Mod.Path
	if oldPath == newPath {
		return nil, nil
	}
	if err := parsed.AddModuleStmt(newPath); err != nil {
		return nil, fmt.Errorf("set module path: %w", err)
	}
	rewritten, err := parsed.Format()
	if err != nil {
		return nil, fmt.Errorf("format go.mod: %w", err)
	}
	rewritten = preserveLineEndings(rewritten, original)

	return &moduleRename{
		oldPath: oldPath,
		newPath: newPath,
		modFile: rewritten,
	}, nil
}

func (rename *moduleRename) rewriteGoImports(relative string, source, original []byte) ([]byte, int, error) {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, relative, source, parser.ParseComments)
	if err != nil {
		return nil, 0, err
	}

	imports := make(map[*ast.BasicLit]struct{}, len(parsed.Imports))
	replacements := make([]byteReplacement, 0, len(parsed.Imports))
	for _, spec := range parsed.Imports {
		imports[spec.Path] = struct{}{}
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, 0, fmt.Errorf("read import path %s: %w", spec.Path.Value, err)
		}
		if path != rename.oldPath && !strings.HasPrefix(path, rename.oldPath+"/") {
			continue
		}
		replacement := rename.newPath + strings.TrimPrefix(path, rename.oldPath)
		replacements = append(replacements, byteReplacement{
			start: files.Position(spec.Path.Pos()).Offset,
			end:   files.Position(spec.Path.End()).Offset,
			data:  []byte(strconv.Quote(replacement)),
		})
	}

	var unresolved string
	ast.Inspect(parsed, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		if _, ok := imports[literal]; ok {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && strings.HasPrefix(value, rename.oldPath+"/") {
			unresolved = value
			return false
		}
		return true
	})
	if unresolved != "" {
		return nil, 0, fmt.Errorf(
			"module path %q occurs outside an import (%q); update the renamer before proceeding",
			rename.oldPath,
			unresolved,
		)
	}
	if len(replacements) == 0 {
		return source, 0, nil
	}

	rewritten := append([]byte(nil), source...)
	for index := len(replacements) - 1; index >= 0; index-- {
		replacement := replacements[index]
		rewritten = bytes.Join([][]byte{
			rewritten[:replacement.start],
			replacement.data,
			rewritten[replacement.end:],
		}, nil)
	}
	formatted, err := formatGo(rewritten, original)
	if err != nil {
		return nil, 0, err
	}
	return formatted, len(replacements), nil
}

func (rename *moduleRename) validateOtherFile(relative string, data []byte) error {
	if strings.HasPrefix(filepath.ToSlash(relative), "docs/") || !hasModuleReference(data, rename.oldPath) {
		return nil
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return fmt.Errorf(
			"module path %q occurs in a binary file; refusing to rewrite it",
			rename.oldPath,
		)
	}
	return fmt.Errorf(
		"module path %q occurs outside go.mod or a Go import; update the renamer before proceeding",
		rename.oldPath,
	)
}

type byteReplacement struct {
	start int
	end   int
	data  []byte
}

func hasModuleReference(data []byte, modulePath string) bool {
	needle := []byte(modulePath + "/")
	for offset := 0; ; {
		index := bytes.Index(data[offset:], needle)
		if index < 0 {
			return false
		}
		index += offset
		if index == 0 || !modulePathByte(data[index-1]) {
			return true
		}
		offset = index + len(needle)
	}
}

func modulePathByte(value byte) bool {
	switch {
	case value >= 'a' && value <= 'z':
		return true
	case value >= 'A' && value <= 'Z':
		return true
	case value >= '0' && value <= '9':
		return true
	}
	return strings.ContainsRune("./_~-+", rune(value))
}

func preserveLineEndings(rewritten, original []byte) []byte {
	if firstLF := bytes.IndexByte(original, '\n'); firstLF > 0 && original[firstLF-1] == '\r' {
		return bytes.ReplaceAll(rewritten, []byte{'\n'}, []byte{'\r', '\n'})
	}
	return rewritten
}
