package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/owainlewis/neo/internal/llm"
)

// Search tools shell out to ripgrep rather than walking the tree themselves.
// rg respects .gitignore, skips binaries, and is an order of magnitude faster
// than anything worth hand-rolling here.
//
// They remain distinct tools rather than folding into bash for two reasons:
// they are classified parallel-safe, which bash cannot be without interpreting
// shell commands, and they give inspect-mode subagents a read-only search
// capability that does not require handing them a shell.
const (
	// SearchBinary is exported so `neo doctor` can report whether search works.
	SearchBinary     = "rg"
	defaultSearchMax = 200
)

// errNoRipgrep names the missing dependency without prescribing a workaround.
// An agent holding bash will reach for it; an inspect subagent, whose registry
// is read_file/grep/glob with no shell, cannot, and telling it to would just
// waste a turn.
var errNoRipgrep = fmt.Errorf(
	"grep and glob require ripgrep (%s), which is not installed on this machine", SearchBinary)

// baseArgs are shared by both tools. --no-require-git makes .gitignore apply
// even when the workspace is not a git checkout, so discovery does not silently
// change behaviour depending on whether the directory happens to be a repo.
func baseArgs() []string {
	return []string{
		"--color", "never",
		// Apply .gitignore even outside a git checkout, so discovery does not
		// change behaviour based on whether the directory happens to be a repo.
		"--no-require-git",
		// Dotfiles are ordinary project files: .github/workflows and
		// .devcontainer are exactly the sort of thing an agent is asked about.
		// --no-require-git disables ripgrep's own .git handling, so the caller
		// excludes .git explicitly rather than walking the object store.
		"--hidden",
	}
}

// gitDirExclusion keeps ripgrep's object store out of search results.
//
// Order matters: ripgrep lets a later --glob override an earlier one, so a user
// pattern as broad as "*" replaced this exclusion and glob("*") listed .git/HEAD
// and the object files. Both tools therefore append this after their own globs.
// grep passes no pattern glob, so for it this is only the exclusion it always
// had; glob needs the ordering for the exclusion to survive.
func gitDirExclusion() []string {
	return []string{"--glob", "!.git/"}
}

type Grep struct {
	Root string
	// Absolute makes the result paths absolute. It is set in the single-tool
	// mode, where grep's output feeds read_file and edit_file inside the same
	// script: a relative path would resolve against the process's directory
	// instead of Root, and the script has no cd to fix it.
	Absolute bool
}

func (Grep) Name() string { return "grep" }

func (Grep) ParallelSafe(map[string]any) bool { return true }

func (Grep) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "grep",
		Description: "Search the workspace with a regular expression, honouring .gitignore. Returns matching lines as path:line:text.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":       map[string]any{"type": "string", "description": "Regular expression to search for"},
				"path":          map[string]any{"type": "string", "description": "File or directory to search (optional, defaults to the workspace root)"},
				"context_lines": map[string]any{"type": "integer", "description": "Lines of context before and after each match (optional)"},
				"max_matches":   map[string]any{"type": "integer", "description": "Maximum output lines to return; context lines count toward it (optional, default 200)"},
			},
			"required": []string{"pattern"},
		},
	}
}

func (g Grep) Run(ctx context.Context, input map[string]any) (string, error) {
	pattern, err := mustString(input, "pattern")
	if err != nil {
		return "", err
	}
	args := append(baseArgs(), "--line-number", "--no-heading")
	args = append(args, gitDirExclusion()...)
	if n := optInt(input, "context_lines"); n > 0 {
		args = append(args, "--context", fmt.Sprint(n))
	}
	args = append(args, "--regexp", pattern)
	if path := optString(input, "path"); path != "" {
		args = append(args, path)
	}
	return runSearch(ctx, g.Root, args, optInt(input, "max_matches"), "matching lines", searchLinesLabelled, g.Absolute)
}

type Glob struct {
	Root string
	// Absolute makes the result paths absolute, for the same reason Grep has it.
	Absolute bool
}

func (Glob) Name() string { return "glob" }

func (Glob) ParallelSafe(map[string]any) bool { return true }

func (Glob) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "glob",
		Description: "List workspace files matching a glob pattern, honouring .gitignore. Supports ** for recursive matches. Returns one path per line.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern":     map[string]any{"type": "string", "description": "Glob pattern such as **/*.go"},
				"path":        map[string]any{"type": "string", "description": "Directory to search from (optional, defaults to the workspace root)"},
				"max_matches": map[string]any{"type": "integer", "description": "Maximum paths to return (optional, default 200)"},
			},
			"required": []string{"pattern"},
		},
	}
}

func (g Glob) Run(ctx context.Context, input map[string]any) (string, error) {
	pattern, err := mustString(input, "pattern")
	if err != nil {
		return "", err
	}
	// The user's pattern comes first and the .git exclusion last, because a
	// later --glob overrides an earlier one: with the exclusion first, glob("*")
	// listed .git/HEAD and the object files.
	args := append(baseArgs(), "--files", "--glob", pattern)
	args = append(args, gitDirExclusion()...)
	if path := optString(input, "path"); path != "" {
		args = append(args, path)
	}
	return runSearch(ctx, g.Root, args, optInt(input, "max_matches"), "paths", searchLinesPaths, g.Absolute)
}

// runSearch executes ripgrep and returns at most maxMatches lines. Output is
// read incrementally and the process is stopped once the limit is reached: a
// broad pattern over a large workspace can produce far more than we will ever
// return, and buffering all of it first would let a model-chosen pattern decide
// how much memory Neo uses.
//
// rg exits 1 when nothing matched, which is a result rather than a failure.
//
// shape says what each output line holds, which decides how it is rebased: a
// glob line is a bare path, a grep line is "path:line:text". Sniffing for a
// colon instead would misread a bare path containing one (Windows drive
// letters, odd filenames) and re-join it with the root twice.
func runSearch(ctx context.Context, root string, args []string, maxMatches int, unit string, shape searchLineShape, absolute bool) (string, error) {
	if maxMatches <= 0 {
		maxMatches = defaultSearchMax
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, SearchBinary, args...)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", errNoRipgrep
		}
		return "", err
	}

	lines, total := readLines(stdout, maxMatches)
	if absolute && root != "" {
		for i, line := range lines {
			lines[i] = rebaseSearchPath(line, root, shape)
		}
	}
	if total > maxMatches {
		// Stop ripgrep rather than draining output we have already decided to
		// discard. The wait below then reports a signal, which is expected.
		cancel()
	}
	waitErr := cmd.Wait()

	if total <= maxMatches {
		if err := searchExitError(ctx, waitErr, stderr.String()); err != nil {
			return "", err
		}
	}
	if len(lines) == 0 {
		return "no matches", nil
	}
	out := strings.Join(lines, "\n")
	if total > maxMatches {
		out += fmt.Sprintf("\n\n[showing the first %d %s; narrow the pattern or raise max_matches]", maxMatches, unit)
	}
	return out, nil
}

// readLines reads up to limit lines, plus one more to detect that there were
// others. It returns the kept lines and how many were seen.
func readLines(r io.Reader, limit int) ([]string, int) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), MaxOutputBytes)
	var lines []string
	total := 0
	for scanner.Scan() {
		total++
		if total > limit {
			break
		}
		lines = append(lines, scanner.Text())
	}
	return lines, total
}

// searchExitError maps ripgrep's exit status. Status 1 means no matches, which
// is a legitimate answer, not an error.
func searchExitError(ctx context.Context, err error, stderr string) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, exec.ErrNotFound) {
		return errNoRipgrep
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		// 1 is "nothing matched". 2 is an error, except for one case that is not
		// the model's fault: with no path argument ripgrep searches the working
		// directory, and when that holds no files at all it exits 2 with an
		// explanation instead of 1. Passing "." asks the same question and gets
		// the "nothing matched" answer, so the two spellings disagreed.
		if exit.ExitCode() == 1 || exit.ExitCode() == 2 && isNoFilesSearched(stderr) {
			return nil
		}
		return fmt.Errorf("%s: %s", SearchBinary, strings.TrimSpace(stderr))
	}
	return err
}

// isNoFilesSearched reports ripgrep's "there was nothing to search" message,
// which is a result rather than a failure: an empty directory has no matches.
func isNoFilesSearched(stderr string) bool {
	return strings.Contains(stderr, "No files were searched")
}

// searchLineShape says what each ripgrep output line holds.
type searchLineShape int

const (
	// searchLinesPaths is glob's output: one bare path per line.
	searchLinesPaths searchLineShape = iota
	// searchLinesLabelled is grep's output: "path:line:text". Only the leading
	// path is rewritten, so line numbers and match text stay untouched.
	searchLinesLabelled
)

// rebaseSearchPath makes one search result path absolute. The shape decides
// where the path ends: for paths the whole line is the path, for labelled
// lines it is everything before the first colon.
func rebaseSearchPath(line, root string, shape searchLineShape) string {
	if line == "" || filepath.IsAbs(line) {
		return line
	}
	head, rest := line, ""
	if shape == searchLinesLabelled {
		if cut, tail, ok := strings.Cut(line, ":"); ok {
			head, rest = cut, ":"+tail
		}
	}
	if filepath.IsAbs(head) {
		return line
	}
	return filepath.Join(root, head) + rest
}
