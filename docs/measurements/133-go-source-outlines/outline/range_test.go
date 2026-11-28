package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRangesReadBackToTheSameDeclaration is the issue's first acceptance
// criterion: the start-end a caller reads must be usable as
// read_file(offset, limit), so slicing those lines back out and parsing them
// must produce the same declaration. A multi-line signature and a method are
// both covered by the repository's own files.
//
// The check is deliberately structural, not textual: it parses the slice rather
// than comparing bytes, because the question is whether the range is a complete,
// parseable declaration -- which is what the model needs to read it.
func TestRangesReadBackToTheSameDeclaration(t *testing.T) {
	files, err := probeFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no Go files found to probe")
	}
	variants := 0
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(src), "\n")
		decls, err := outline(path, src)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, d := range decls {
			if d.start < 1 || d.stop > len(lines) || d.start > d.stop {
				t.Fatalf("%s %s: range %d-%d outside 1-%d", path, d.name, d.start, d.stop, len(lines))
			}
			window := strings.Join(lines[d.start-1:d.stop], "\n")
			if strings.Contains(d.sig, "{") {
				t.Fatalf("%s %s: signature carries implementation text: %q", path, d.name, d.sig)
			}
			if d.start != d.stop {
				variants++
			}
			// Wrapping in a package clause lets a declaration that is legal
			// only inside a package parse on its own.
			wrapped := "package probe\n\n" + window + "\n"
			got, err := parseNames(wrapped)
			if err != nil {
				t.Fatalf("%s %s: range %d-%d does not parse back: %v\n%s", path, d.name, d.start, d.stop, err, window)
			}
			if !contains(got, d.name) {
				t.Fatalf("%s %s: range %d-%d parses as %v, which does not contain the declaration", path, d.name, d.start, d.stop, got)
			}
		}
	}
	if variants == 0 {
		t.Fatal("probe saw only single-line declarations; multi-line signatures were not exercised")
	}
}

// TestSkipsNonDeclarations keeps the outline free of implementation bodies: a
// statement or a local variable inside a function is not a declaration, and
// listing one would mislead a caller narrowing into the file.
func TestSkipsNonDeclarations(t *testing.T) {
	const src = `package p

// Package-level, listed.
const Top = 1

func Outer() {
	inner := 2
	_ = inner
}

type T struct{ A int }
`
	decls, err := outline("x.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range decls {
		names = append(names, d.name)
	}
	want := []string{"Top", "Outer", "T"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("names = %v, want %v", names, want)
	}
}

// TestParseFailureIsAnError covers the issue's "no partial index" rule: a file
// that does not parse must fail, not return the declarations read so far.
func TestParseFailureIsAnError(t *testing.T) {
	if _, err := outline("broken.go", []byte("package p\n\nfunc (")); err == nil {
		t.Fatal("a broken file parsed without error")
	}
}

// outline parses src and returns the declarations the command would print, so
// the test exercises the same ranges rather than a copy of the logic.
func outline(path string, src []byte) ([]decl, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	return collect(fset, file, src), nil
}

func parseNames(src string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, d := range collect(fset, file, []byte(src)) {
		names = append(names, d.name)
	}
	return names, nil
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// probeFiles are the repository's own Go sources: they are already reviewed,
// already contain multi-line signatures and methods, and need no fixture.
func probeFiles() ([]string, error) {
	var out []string
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out, err
}
