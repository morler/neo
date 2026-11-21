package main

import (
	"os"
	"path/filepath"
	"regexp"
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
