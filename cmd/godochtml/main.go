package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// godochtml generates static HTML documentation from Go source packages.
// Usage: godochtml -module <module-path> <dir1> [dir2 ...]
// Outputs HTML to stdout.

type pkgDoc struct {
	ImportPath string
	Name       string
	Doc        string
	Types      []typeDoc
	Funcs      []funcDoc
	Consts     []valueDoc
	Vars       []valueDoc
}

type typeDoc struct {
	Name    string
	Doc     string
	Decl    string
	Methods []funcDoc
	Funcs   []funcDoc // associated constructors
}

type funcDoc struct {
	Name string
	Doc  string
	Decl string
}

type valueDoc struct {
	Names []string
	Doc   string
	Decl  string
}

func main() {
	modulePath := flag.String("module", "", "Go module path (e.g., github.com/accretional/anyserver)")
	flag.Parse()

	if *modulePath == "" || flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "usage: godochtml -module <module-path> <dir1> [dir2 ...]\n")
		os.Exit(1)
	}

	dirs := flag.Args()
	var packages []pkgDoc

	for _, dir := range dirs {
		pkgs, err := parseDir(dir, *modulePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "parse %s: %v\n", dir, err)
			continue
		}
		packages = append(packages, pkgs...)
	}

	sort.Slice(packages, func(i, j int) bool {
		return packages[i].ImportPath < packages[j].ImportPath
	})

	tmpl := template.Must(template.New("docs").Funcs(template.FuncMap{
		"join":     strings.Join,
		"synopsis": doc.Synopsis,
	}).Parse(docsTemplate))
	if err := tmpl.Execute(os.Stdout, packages); err != nil {
		fmt.Fprintf(os.Stderr, "template: %v\n", err)
		os.Exit(1)
	}
}

func parseDir(root string, modulePath string) ([]pkgDoc, error) {
	var results []pkgDoc

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if !info.IsDir() {
			return nil
		}
		// Skip hidden dirs, vendor, testdata (but not the root "." itself)
		base := filepath.Base(path)
		if path != root && (strings.HasPrefix(base, ".") || base == "vendor" || base == "testdata") {
			return filepath.SkipDir
		}

		// Skip directories that are clearly not Go source packages
		rel, _ := filepath.Rel(root, path)
		if strings.Contains(rel, "cmd/anyserver/source") {
			return filepath.SkipDir
		}
		// Skip proto generated packages (too noisy, not useful as docs)
		if strings.HasPrefix(rel, "proto/") || strings.HasPrefix(rel, "proto\\") {
			return filepath.SkipDir
		}

		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, path, func(fi os.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, parser.ParseComments)
		if err != nil {
			return nil // skip unparseable
		}

		for _, pkg := range pkgs {
			if pkg.Name == "main" {
				continue // skip main packages
			}

			d := doc.New(pkg, "", doc.AllDecls)

			// Compute import path
			rel, _ := filepath.Rel(root, path)
			importPath := modulePath
			if rel != "." && rel != "" {
				importPath = modulePath + "/" + filepath.ToSlash(rel)
			}

			pd := pkgDoc{
				ImportPath: importPath,
				Name:       d.Name,
				Doc:        d.Doc,
			}

			// Types
			for _, t := range d.Types {
				if !ast.IsExported(t.Name) {
					continue
				}
				td := typeDoc{
					Name: t.Name,
					Doc:  t.Doc,
					Decl: formatNode(fset, t.Decl),
				}
				for _, m := range t.Methods {
					if !ast.IsExported(m.Name) {
						continue
					}
					td.Methods = append(td.Methods, funcDoc{
						Name: m.Name,
						Doc:  m.Doc,
						Decl: formatNode(fset, m.Decl),
					})
				}
				for _, f := range t.Funcs {
					if !ast.IsExported(f.Name) {
						continue
					}
					td.Funcs = append(td.Funcs, funcDoc{
						Name: f.Name,
						Doc:  f.Doc,
						Decl: formatNode(fset, f.Decl),
					})
				}
				pd.Types = append(pd.Types, td)
			}

			// Package-level functions
			for _, f := range d.Funcs {
				if !ast.IsExported(f.Name) {
					continue
				}
				pd.Funcs = append(pd.Funcs, funcDoc{
					Name: f.Name,
					Doc:  f.Doc,
					Decl: formatNode(fset, f.Decl),
				})
			}

			// Constants
			for _, v := range d.Consts {
				names := exportedNames(v.Names)
				if len(names) == 0 {
					continue
				}
				pd.Consts = append(pd.Consts, valueDoc{
					Names: names,
					Doc:   v.Doc,
					Decl:  formatNode(fset, v.Decl),
				})
			}

			// Variables
			for _, v := range d.Vars {
				names := exportedNames(v.Names)
				if len(names) == 0 {
					continue
				}
				pd.Vars = append(pd.Vars, valueDoc{
					Names: names,
					Doc:   v.Doc,
					Decl:  formatNode(fset, v.Decl),
				})
			}

			// Only include packages that have exported content
			if pd.Doc != "" || len(pd.Types) > 0 || len(pd.Funcs) > 0 || len(pd.Consts) > 0 || len(pd.Vars) > 0 {
				results = append(results, pd)
			}
		}
		return nil
	})

	return results, err
}

func exportedNames(names []string) []string {
	var out []string
	for _, n := range names {
		if ast.IsExported(n) {
			out = append(out, n)
		}
	}
	return out
}

func formatNode(fset *token.FileSet, node ast.Node) string {
	if node == nil {
		return ""
	}
	// For GenDecl (type, const, var), extract just the signature
	switch n := node.(type) {
	case *ast.FuncDecl:
		return formatFuncDecl(n)
	case *ast.GenDecl:
		return formatGenDecl(fset, n)
	}
	return ""
}

func formatFuncDecl(f *ast.FuncDecl) string {
	var b strings.Builder
	b.WriteString("func ")
	if f.Recv != nil && len(f.Recv.List) > 0 {
		b.WriteString("(")
		b.WriteString(formatFieldList(f.Recv))
		b.WriteString(") ")
	}
	b.WriteString(f.Name.Name)
	b.WriteString("(")
	if f.Type.Params != nil {
		b.WriteString(formatFieldList(f.Type.Params))
	}
	b.WriteString(")")
	if f.Type.Results != nil && len(f.Type.Results.List) > 0 {
		results := formatFieldList(f.Type.Results)
		if len(f.Type.Results.List) > 1 || len(f.Type.Results.List[0].Names) > 0 {
			b.WriteString(" (" + results + ")")
		} else {
			b.WriteString(" " + results)
		}
	}
	return b.String()
}

func formatFieldList(fl *ast.FieldList) string {
	var parts []string
	for _, f := range fl.List {
		typ := formatExpr(f.Type)
		if len(f.Names) > 0 {
			var names []string
			for _, n := range f.Names {
				names = append(names, n.Name)
			}
			parts = append(parts, strings.Join(names, ", ")+" "+typ)
		} else {
			parts = append(parts, typ)
		}
	}
	return strings.Join(parts, ", ")
}

func formatExpr(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return formatExpr(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + formatExpr(t.X)
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + formatExpr(t.Elt)
		}
		return "[...]" + formatExpr(t.Elt)
	case *ast.MapType:
		return "map[" + formatExpr(t.Key) + "]" + formatExpr(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		var b strings.Builder
		b.WriteString("func(")
		if t.Params != nil {
			b.WriteString(formatFieldList(t.Params))
		}
		b.WriteString(")")
		if t.Results != nil && len(t.Results.List) > 0 {
			results := formatFieldList(t.Results)
			if len(t.Results.List) > 1 {
				b.WriteString(" (" + results + ")")
			} else {
				b.WriteString(" " + results)
			}
		}
		return b.String()
	case *ast.Ellipsis:
		return "..." + formatExpr(t.Elt)
	case *ast.ChanType:
		switch t.Dir {
		case ast.SEND:
			return "chan<- " + formatExpr(t.Value)
		case ast.RECV:
			return "<-chan " + formatExpr(t.Value)
		default:
			return "chan " + formatExpr(t.Value)
		}
	case *ast.StructType:
		return "struct{...}"
	case *ast.ParenExpr:
		return "(" + formatExpr(t.X) + ")"
	case *ast.IndexExpr:
		return formatExpr(t.X) + "[" + formatExpr(t.Index) + "]"
	}
	return "?"
}

func formatGenDecl(fset *token.FileSet, g *ast.GenDecl) string {
	if len(g.Specs) == 0 {
		return ""
	}

	var lines []string
	for _, spec := range g.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			typ := formatExpr(s.Type)
			lines = append(lines, "type "+s.Name.Name+" "+typ)
		case *ast.ValueSpec:
			var names []string
			for _, n := range s.Names {
				names = append(names, n.Name)
			}
			prefix := "var"
			if g.Tok == token.CONST {
				prefix = "const"
			}
			typ := ""
			if s.Type != nil {
				typ = " " + formatExpr(s.Type)
			}
			lines = append(lines, prefix+" "+strings.Join(names, ", ")+typ)
		}
	}
	return strings.Join(lines, "\n")
}

const docsTemplate = `<section class="index-section">
  <h2>Package Documentation</h2>
  <p>Generated from Go source using <code>go/doc</code> + <code>go/parser</code>.</p>
  <table class="file-list">
    <thead><tr><th>Package</th><th>Synopsis</th></tr></thead>
    <tbody>
    {{range .}}
    <tr>
      <td><a href="#pkg-{{.Name}}"><code>{{.ImportPath}}</code></a></td>
      <td>{{synopsis .Doc}}</td>
    </tr>
    {{end}}
    </tbody>
  </table>
</section>

{{range .}}
<section class="index-section" id="pkg-{{.Name}}">
  <h2>package {{.Name}}</h2>
  <p class="file-meta"><code>import "{{.ImportPath}}"</code></p>
  {{if .Doc}}<div class="pkg-doc"><pre>{{.Doc}}</pre></div>{{end}}

  {{if .Consts}}
  <h3>Constants</h3>
  {{range .Consts}}
  <div class="doc-entry">
    <div class="code-block"><pre><code>` + "{{.Decl}}" + `</code></pre></div>
    {{if .Doc}}<p>` + "{{.Doc}}" + `</p>{{end}}
  </div>
  {{end}}
  {{end}}

  {{if .Vars}}
  <h3>Variables</h3>
  {{range .Vars}}
  <div class="doc-entry">
    <div class="code-block"><pre><code>` + "{{.Decl}}" + `</code></pre></div>
    {{if .Doc}}<p>` + "{{.Doc}}" + `</p>{{end}}
  </div>
  {{end}}
  {{end}}

  {{if .Funcs}}
  <h3>Functions</h3>
  {{range .Funcs}}
  <div class="doc-entry">
    <h4><code>` + "{{.Name}}" + `</code></h4>
    <div class="code-block"><pre><code>` + "{{.Decl}}" + `</code></pre></div>
    {{if .Doc}}<p>` + "{{.Doc}}" + `</p>{{end}}
  </div>
  {{end}}
  {{end}}

  {{if .Types}}
  <h3>Types</h3>
  {{range .Types}}
  <div class="doc-entry">
    <h4><code>` + "{{.Name}}" + `</code></h4>
    <div class="code-block"><pre><code>` + "{{.Decl}}" + `</code></pre></div>
    {{if .Doc}}<p>` + "{{.Doc}}" + `</p>{{end}}
    {{if .Funcs}}
    <div class="doc-sub">
    {{range .Funcs}}
      <h5><code>` + "{{.Name}}" + `</code></h5>
      <div class="code-block"><pre><code>` + "{{.Decl}}" + `</code></pre></div>
      {{if .Doc}}<p>` + "{{.Doc}}" + `</p>{{end}}
    {{end}}
    </div>
    {{end}}
    {{if .Methods}}
    <div class="doc-sub">
    {{range .Methods}}
      <h5><code>` + "{{.Name}}" + `</code></h5>
      <div class="code-block"><pre><code>` + "{{.Decl}}" + `</code></pre></div>
      {{if .Doc}}<p>` + "{{.Doc}}" + `</p>{{end}}
    {{end}}
    </div>
    {{end}}
  </div>
  {{end}}
  {{end}}
</section>
{{end}}
`
