// Command outline prints a Go file's declarations with the line range each one
// occupies. It is the experiment for #133: a read-only skeleton the model can
// use to locate code before reading it, built from the standard library only so
// it stays CGO_ENABLED=0 and adds no dependency to Neo.
//
// It is deliberately not wired into Neo as a tool. The question #133 asks is
// whether an outline plus a narrow read beats grep plus a narrow read; that is
// answerable with a helper in the fixture workspace, and answering it does not
// justify a new tool on the default surface.
//
// Usage:
//
//	outline <file.go>...
//
// One line per declaration:
//
//	path:start-end  kind  name  signature
//
// The signature is the declaration header with runs of whitespace collapsed, so
// a multi-line signature stays one line and still shows its parameters.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: outline <file.go>...")
		os.Exit(2)
	}
	var failed bool
	for _, path := range os.Args[1:] {
		if err := outlineFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "outline: %v\n", err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

// outlineFile prints one file's declarations. A file that does not parse is an
// error rather than a partial listing: a caller that gets half an outline would
// conclude a symbol is absent when it was never read.
func outlineFile(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	for _, d := range collect(fset, file, src) {
		fmt.Printf("%s:%d-%d\t%s\t%s\t%s\n", path, d.start, d.stop, d.kind, d.name, d.sig)
	}
	return nil
}

// collect is the one implementation of what an outline is, shared by the command
// and the tests so a range they disagree about cannot pass.
func collect(fset *token.FileSet, file *ast.File, src []byte) []decl {
	var out []decl
	for _, d := range file.Decls {
		switch v := d.(type) {
		case *ast.FuncDecl:
			kind := "func"
			if v.Recv != nil {
				kind = "method"
			}
			out = append(out, makeDecl(fset, v.Pos(), v.End(), kind, v.Name.Name, src, v.Type.Pos(), v.Type.End()))
		case *ast.GenDecl:
			for _, spec := range v.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					out = append(out, makeDecl(fset, v.Pos(), s.End(), "type", s.Name.Name, src, v.Pos(), s.End()))
				case *ast.ValueSpec:
					kind := "var"
					if v.Tok == token.CONST {
						kind = "const"
					}
					for _, name := range s.Names {
						out = append(out, makeDecl(fset, v.Pos(), s.End(), kind, name.Name, src, v.Pos(), s.End()))
					}
				}
			}
		}
	}
	return out
}

type decl struct {
	start, stop int
	kind, name  string
	sig         string
}

func makeDecl(fset *token.FileSet, pos, end token.Pos, kind, name string, src []byte, sigPos, sigEnd token.Pos) decl {
	return decl{
		start: fset.PositionFor(pos, false).Line,
		stop:  fset.PositionFor(end, false).Line,
		kind:  kind,
		name:  name,
		sig:   signature(src, fset, sigPos, sigEnd),
	}
}

// signature renders the declaration header on one line. It stops at the body so
// the outline never carries implementation text.
func signature(src []byte, fset *token.FileSet, pos, end token.Pos) string {
	start := fset.PositionFor(pos, false).Offset
	stop := fset.PositionFor(end, false).Offset
	if start < 0 || stop > len(src) || start >= stop {
		return ""
	}
	header := string(src[start:stop])
	if i := strings.Index(header, "{"); i >= 0 {
		header = header[:i]
	}
	header = strings.Join(strings.Fields(header), " ")
	const limit = 160
	if len(header) > limit {
		header = header[:limit] + " ..."
	}
	return header
}
