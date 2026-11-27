package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var relLink = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)

// TestDeveloperDocsLinksResolve keeps docs/developer usable from the docs site
// and from a terminal: a rename that misses a link is a broken page.
func TestDeveloperDocsLinksResolve(t *testing.T) {
	root := filepath.Join("..", "..", "docs", "developer")
	files, err := filepath.Glob(filepath.Join(root, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	guides, err := filepath.Glob(filepath.Join(root, "guides", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, guides...)
	if len(files) == 0 {
		t.Fatal("no developer docs found")
	}
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range relLink.FindAllStringSubmatch(string(body), -1) {
			target := m[1]
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") ||
				strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			target = strings.SplitN(target, "#", 2)[0]
			if target == "" {
				continue
			}
			resolved := filepath.Join(filepath.Dir(path), target)
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s: link %q does not resolve", path, m[1])
			}
		}
	}
}

var docPackage = regexp.MustCompile("(?:^|[\\s`(])((?:internal|cmd|website)/[A-Za-z0-9_/<>-]*)")

// TestArchitecturePackageReferencesResolve keeps ARCHITECTURE.md honest about
// where code lives. It is headed "Current implementation" and is the first
// document repository instructions point at, so a package rename that misses it
// sends the next reader looking for a directory that no longer exists: that is
// exactly how internal/factory outlived its own rename by three months.
//
// Backticks and the dependency diagram are both checked, because the diagram was
// where the stale name survived longest. A reference followed by a dot names a
// file or a symbol rather than a package, so it is left to the compiler. The
// check is one-directional on purpose: the document may leave a package
// unmentioned without becoming wrong, but it may not name one that is not there.
func TestArchitecturePackageReferencesResolve(t *testing.T) {
	root := filepath.Join("..", "..")
	body, err := os.ReadFile(filepath.Join(root, "ARCHITECTURE.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	seen := map[string]bool{}
	var missing []string
	for _, m := range docPackage.FindAllStringSubmatchIndex(text, -1) {
		ref := text[m[2]:m[3]]
		if next := text[m[3]:]; next != "" && next[0] == '.' {
			continue
		}
		ref = strings.TrimSuffix(ref, "/")
		// A placeholder such as internal/llm/<provider>/ names a family of
		// packages rather than one directory, so only its root is checked.
		if i := strings.IndexByte(ref, '<'); i >= 0 {
			ref = strings.TrimSuffix(ref[:i], "/")
		}
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(ref))); err != nil {
			missing = append(missing, ref)
		}
	}
	if len(seen) == 0 {
		t.Fatal("no package references found in ARCHITECTURE.md")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("ARCHITECTURE.md names packages that do not exist: %s", strings.Join(missing, ", "))
	}
}

var goTestLine = regexp.MustCompile("(?m)^go test (?:-race )?(.*)$")

// TestDesignDocTestCommandsRun checks that the check commands recorded in the
// design docs name packages that exist. A design doc that records "go test
// ./internal/factory" cannot be followed at all: the failure is a setup error,
// not a failing test, so a reader learns nothing about the code.
func TestDesignDocTestCommandsRun(t *testing.T) {
	root := filepath.Join("..", "..")
	docs, err := filepath.Glob(filepath.Join(root, "docs", "*", "design.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) == 0 {
		t.Fatal("no design docs found")
	}
	checked := 0
	for _, path := range docs {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range goTestLine.FindAllStringSubmatch(string(body), -1) {
			for _, arg := range strings.Fields(m[1]) {
				pkg := strings.TrimPrefix(arg, "./")
				if !strings.HasPrefix(arg, "./") || pkg == "..." || strings.ContainsAny(pkg, "*{}$") {
					continue
				}
				checked++
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(pkg))); err != nil {
					t.Errorf("%s: go test names %q, which is not a directory", path, arg)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no go test package arguments found in the design docs")
	}
}
