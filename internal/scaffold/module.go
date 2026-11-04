package scaffold

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
)

// rewriteModule renames the template's module to newPath in go.mod, in the imports of every .go file
// (parsed, so only real import paths change), and in the imports of every .templ file.
func rewriteModule(p Project, newPath string) error {
	mf, err := modfile.Parse("go.mod", p["go.mod"], nil)
	if err != nil {
		return err
	}
	if mf.Module == nil {
		return fmt.Errorf("go.mod has no module line")
	}
	oldPath := mf.Module.Mod.Path
	if oldPath == newPath {
		return nil
	}
	if err := mf.AddModuleStmt(newPath); err != nil {
		return err
	}
	b, err := mf.Format()
	if err != nil {
		return err
	}
	p["go.mod"] = b

	for _, name := range p.Paths() {
		switch path.Ext(name) {
		case ".go":
			b, err := rewriteGoImports(name, p[name], oldPath, newPath)
			if err != nil {
				return err
			}
			p[name] = b
		case ".templ":
			p[name] = rewriteTemplImports(p[name], oldPath, newPath)
		}
	}

	// AGENTS.md tells readers to rename the module; that is done now.
	return p.replaceOnce("AGENTS.md",
		"`"+oldPath+"` (rename it in `go.mod` and the imports)",
		"`"+newPath+"`")
}

// rewriteGoImports changes import paths under oldPath to newPath. Files without such an import are
// returned unchanged; changed files are gofmt-formatted (which also re-sorts the import block).
func rewriteGoImports(name string, src []byte, oldPath, newPath string) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	changed := false
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if np, ok := renameImport(p, oldPath, newPath); ok {
			imp.Path.Value = strconv.Quote(np)
			changed = true
		}
	}
	if !changed {
		return src, nil
	}

	// sort imports again: the new path may belong in a different place within its block
	ast.SortImports(fset, f)
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return buf.Bytes(), nil
}

func renameImport(importPath, oldPath, newPath string) (string, bool) {
	if importPath == oldPath {
		return newPath, true
	}
	if rest, ok := strings.CutPrefix(importPath, oldPath+"/"); ok {
		return newPath + "/" + rest, true
	}
	return "", false
}

// rewriteTemplImports changes `import "oldPath/..."` lines in a .templ file. templ files cannot be
// parsed as Go, so this works line by line and only touches import lines.
func rewriteTemplImports(src []byte, oldPath, newPath string) []byte {
	lines := strings.SplitAfter(string(src), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "import ") && !strings.HasPrefix(trimmed, `"`) {
			continue
		}
		lines[i] = strings.ReplaceAll(line, `"`+oldPath+`"`, `"`+newPath+`"`)
		lines[i] = strings.ReplaceAll(lines[i], `"`+oldPath+`/`, `"`+newPath+`/`)
	}
	return []byte(strings.Join(lines, ""))
}
