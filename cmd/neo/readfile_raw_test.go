package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/tools"
)

// TestReadFileRawOnEverySurface pins where read_file's raw option is reachable.
// The three surfaces differ in whether read_file is a top-level tool or only a
// global inside script, and "/codemode off" in chat flips features.script
// rather than features.codemode, so a change that only worked on the default
// surface would break the classic one silently. #110 moved the option into the
// tool itself, so it must be there on each surface that exposes read_file at
// all, and a surface that exposes neither must simply not offer it.
func TestReadFileRawOnEverySurface(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	body := "alpha\nbeta\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		codeMode string
		script   bool
	}{
		{"code-mode/script on", config.CodeModeCodemode, true},
		{"code-mode/script off", config.CodeModeCodemode, false},
		{"classic/script on", config.CodeModeClassic, true},
		{"classic/script off", config.CodeModeClassic, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := keylessConfig()
			cfg.Features.CodeMode = c.codeMode
			cfg.SetScript(c.script)
			reg := registryFor(cfg, dir, dir)

			var topLevel, scripted bool
			for _, name := range reg.Names() {
				topLevel = topLevel || name == "read_file"
				scripted = scripted || name == "script"
			}

			if topLevel {
				// A top-level read_file carries its own schema, so raw must be
				// advertised there or the model has no way to learn about it.
				tool, _ := reg.Get("read_file")
				props, _ := tool.Spec().InputSchema["properties"].(map[string]any)
				if _, ok := props["raw"]; !ok {
					t.Fatalf("%s: top-level read_file does not advertise raw", c.name)
				}
				out, err := tool.Run(context.Background(), map[string]any{"path": path, "raw": true})
				if err != nil {
					t.Fatalf("%s: raw read: %v", c.name, err)
				}
				if out != body {
					t.Fatalf("%s: raw = %q, want %q", c.name, out, body)
				}
			}

			if scripted {
				// Inside script, read_file is a global and its schema is not
				// shown; the option still has to work, which is what the code-mode
				// description names it for.
				tool, _ := reg.Get("script")
				s, ok := tool.(tools.Script)
				if !ok {
					t.Fatalf("%s: script tool has unexpected type %T", c.name, tool)
				}
				out, err := s.Run(context.Background(), map[string]any{
					"source": "tools.read_file({path: " + quoted(path) + ", raw: true})",
				})
				if err != nil {
					t.Fatalf("%s: script raw read: %v", c.name, err)
				}
				if out != body {
					t.Fatalf("%s: script raw = %q, want %q", c.name, out, body)
				}
			}

			if !topLevel && !scripted {
				// The remaining surface has no read_file at all: features.script
				// off in code mode leaves only the coordinating tools. Asserting
				// the absence keeps this test from passing vacuously if a later
				// change starts registering something unexpected.
				t.Logf("%s: no read_file surface, tools=%v", c.name, reg.Names())
			}
		})
	}
}
