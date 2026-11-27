package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/owainlewis/neo/internal/atomicfile"
	"github.com/owainlewis/neo/internal/llm"
)

// MaxReadBytes caps the total bytes ReadFile will return in a single call.
// Files larger than this must be paged via offset/limit.
const MaxReadBytes = MaxOutputBytes

// fileState remembers what a file looked like when the agent last read it.
// edit_file consults it so a write cannot land on content the agent never saw:
// the user saving in their editor, a git checkout, or a concurrent work-mode
// subagent all change a file without the agent being able to observe it.
//
// A nil *fileState tracks nothing, which keeps the zero-value tools usable.
type fileState struct {
	mu   sync.Mutex
	seen map[string]fileStamp
}

// fileStamp is enough to catch a real external write. Hashing would be exact
// but a stat is free and the difference does not come up in practice.
type fileStamp struct {
	modTime time.Time
	size    int64
}

func newFileState() *fileState { return &fileState{seen: map[string]fileStamp{}} }

func stamp(path string) (fileStamp, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}, false
	}
	return fileStamp{modTime: info.ModTime(), size: info.Size()}, true
}

func stateKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// record notes the file's current state as what the agent has seen.
func (s *fileState) record(path string) {
	if s == nil {
		return
	}
	current, ok := stamp(path)
	if !ok {
		return
	}
	s.mu.Lock()
	s.seen[stateKey(path)] = current
	s.mu.Unlock()
}

// changedSinceRead reports whether the file differs from what the agent last
// read. A path the agent has never read is not stale: trusting the model to
// read before it edits is fine, because that is a decision it can make. This
// guard is only for changes it cannot see.
func (s *fileState) changedSinceRead(path string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	previous, tracked := s.seen[stateKey(path)]
	s.mu.Unlock()
	if !tracked {
		return false
	}
	current, ok := stamp(path)
	if !ok {
		return false
	}
	return current != previous
}

type ReadFile struct {
	State *fileState
}

func (ReadFile) Name() string { return "read_file" }

func (ReadFile) ParallelSafe(map[string]any) bool { return true }

func (ReadFile) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "read_file",
		Description: "Read a file from disk. Each line is prefixed with its 1-indexed line number and a tab, matching the line numbers grep reports; raw: true drops that prefix so the text feeds edit_file directly. Use offset/limit to page through large files.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "Absolute or relative path"},
				"offset": map[string]any{"type": "integer", "description": "1-indexed starting line (optional)"},
				"limit":  map[string]any{"type": "integer", "description": "Maximum number of lines to return (optional)"},
				"raw":    map[string]any{"type": "boolean", "description": "Raw text without the line-number prefix (optional)"},
			},
			"required": []string{"path"},
		},
	}
}

func (r ReadFile) Run(ctx context.Context, input map[string]any) (string, error) {
	path, err := mustString(input, "path")
	if err != nil {
		return "", err
	}
	raw := optBool(input, "raw")
	// The continuation marker is charged to the same byte budget as the page.
	// Reserve its widest plausible shape while scanning, then append the exact
	// marker once the first line that did not fit is known.
	reserve := ""
	if !raw {
		reserve = firstPageMarker(MaxReadBytes, MaxReadBytes)
	}
	out, nextOffset, nextLimit, err := readFileWindow(ctx, path, optInt(input, "offset"), optInt(input, "limit"), raw, reserve)
	if err != nil {
		return "", err
	}
	// A partial read still counts: the stamp records that the agent looked at
	// this file, not that it saw all of it.
	r.State.record(path)
	if nextOffset > 0 {
		// The marker supplies its own leading newline, so a page that ends
		// right after a line break does not need to keep it.
		out = strings.TrimSuffix(out, "\n") + firstPageMarker(nextOffset, nextLimit)
	}
	return out, nil
}

// lineNumberPrefix renders the gutter for one line. The tab keeps the content
// column stable and gives the model an unambiguous place to cut when it needs
// the raw text back for edit_file.
func lineNumberPrefix(line int) string {
	return fmt.Sprintf("%6d\t", line)
}

// readFileWindow returns the requested line window. raw omits the line-number
// gutter, which is what edit_file matches: a script can then pass the result
// back as old_string without stripping the prefix by hand. A non-empty reserve
// is held back from the byte cap for a continuation marker.
func readFileWindow(ctx context.Context, path string, offset, limit int, raw bool, markerReserve string) (page string, nextOffset, nextLimit int, err error) {
	// Check before opening: a cancelled turn should report cancellation rather
	// than a filesystem error, and opening a FIFO with no writer blocks.
	if err := ctx.Err(); err != nil {
		return "", 0, 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer f.Close()

	startLine := 1
	if offset > 0 {
		startLine = offset
	}
	fileEmpty := false
	if info, err := f.Stat(); err == nil {
		fileEmpty = info.Size() == 0
	}

	var out strings.Builder
	w := &windowScanner{out: &out, startLine: startLine, limit: limit, raw: raw, lineNo: 1, reserve: markerReserve}
	if err := w.scan(ctx, bufio.NewReader(f)); err != nil {
		// A cancelled turn still returns what it read, since the caller may
		// report it; a filesystem failure does not.
		if w.canceled {
			return out.String(), 0, 0, err
		}
		if errors.Is(err, errReadSelectionTooLarge) {
			page := out.String()
			// A line that did not fit may have left a fragment in the builder.
			// Drop it so the page holds only complete lines: a half line, or a
			// split UTF-8 sequence, must never be delivered as content.
			if !w.lastEndedWithNewline {
				if i := strings.LastIndexByte(page, '\n'); i >= 0 {
					page = page[:i]
				} else {
					page = ""
				}
			}
			// With no complete line to deliver the marker alone cannot help, so
			// fail instead. raw keeps its original error contract.
			if raw || page == "" || !utf8.ValidString(page) {
				return "", 0, 0, tooLargeAdvice(startLine, limit, w.lineNo, w.selected)
			}
			// The buffered lines are usable evidence: hand them back with an
			// inline marker naming the first line that did not fit. A plain
			// error would discard them and let the model page past content it
			// never saw.
			return page, nextPageOffset(startLine, w.lineNo), nextPageLimit(limit, w.selected), nil
		}
		return "", 0, 0, err
	}
	if offset > 0 && !w.wroteLine && (!fileEmpty || offset != 1) {
		return "", 0, 0, fmt.Errorf("read_file: offset %d is past end of file", offset)
	}
	return out.String(), 0, 0, nil
}

// windowScanner tracks the line window one read_file call asked for while the
// reader hands it a line at a time.
type windowScanner struct {
	out       *strings.Builder
	startLine int
	limit     int
	// raw omits the line-number gutter, so the output matches the file's own
	// text and can be fed straight to edit_file.
	raw bool

	// reserve is held back from the byte cap for an inline continuation marker.
	reserve string

	lineNo   int
	selected int

	wroteLine  bool
	inSelected bool
	canceled   bool

	// lastEndedWithNewline distinguishes a file that ends with a newline from
	// one that stops mid-line: only the former owes a trailing blank line.
	lastEndedWithNewline bool
}

// more reports whether the window still has room for another line.
func (w *windowScanner) more() bool { return w.limit <= 0 || w.selected < w.limit }

// scan reads until the window is full or the input ends.
func (w *windowScanner) scan(ctx context.Context, r *bufio.Reader) error {
	for w.more() {
		if err := ctx.Err(); err != nil {
			w.canceled = true
			return err
		}
		part, err := r.ReadSlice('\n')
		if len(part) > 0 {
			endsLine := part[len(part)-1] == '\n'
			w.lastEndedWithNewline = endsLine
			if endsLine {
				part = part[:len(part)-1]
			}
			if err := w.consume(part, endsLine); err != nil {
				return err
			}
		}
		switch err {
		case nil, bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(part) == 0 && w.lastEndedWithNewline && w.more() && w.lineNo >= w.startLine {
				return w.breakLine()
			}
			return nil
		default:
			return err
		}
	}
	return nil
}

// consume appends one line fragment, adding the gutter when the fragment opens
// a newly selected line.
func (w *windowScanner) consume(part []byte, endsLine bool) error {
	if w.lineNo >= w.startLine {
		if !w.inSelected {
			if err := w.breakLine(); err != nil {
				return err
			}
			if !w.raw {
				if err := appendReadFileChunk(w.out, lineNumberPrefix(w.lineNo), w.reserve); err != nil {
					return err
				}
			}
			w.inSelected = true
		}
		if err := appendReadFileChunk(w.out, string(part), w.reserve); err != nil {
			return err
		}
	}
	if endsLine {
		if w.lineNo >= w.startLine {
			w.selected++
			w.inSelected = false
		}
		w.lineNo++
	}
	return nil
}

// breakLine separates two selected lines, and records the first with no
// leading newline.
func (w *windowScanner) breakLine() error {
	if !w.wroteLine {
		w.wroteLine = true
		return nil
	}
	return appendReadFileChunk(w.out, "\n", w.reserve)
}

func appendReadFileChunk(out *strings.Builder, chunk string, reserve string) error {
	if out.Len()+len(chunk)+len(reserve) > MaxReadBytes {
		return errReadSelectionTooLarge
	}
	_, _ = out.WriteString(chunk)
	return nil
}

// errReadSelectionTooLarge reports a window that does not fit the byte cap. The
// message is a sentinel so the caller can turn it into a concrete next call
// once the scanner knows where it stopped; an error alone tells the model to
// "narrow with offset/limit" without saying how.
var errReadSelectionTooLarge = errors.New("read_file: selection exceeds the byte cap")

// firstPageMarker is appended inside the result so the model can continue
// reading from the first line that did not fit. It is deliberately terse: the
// marker is charged against the same byte cap as the page itself.
func firstPageMarker(offset, limit int) string {
	return fmt.Sprintf("\n[read_file: first page only; retry with offset=%d, limit=%d]", offset, limit)
}

// tooLargeAdvice turns a failed window into the next call the model can make:
// resume at the first line that did not fit. When the call set a limit, that
// limit is reused unchanged, because it was the caller's own choice and the
// lines after the cut are still unread. Without a limit, it suggests half the
// lines that overflowed, which fits by construction for lines of similar length
// and needs no second copy of the byte budget.
func tooLargeAdvice(startLine, limit, stoppedAt, lines int) error {
	return fmt.Errorf("%w; retry with offset=%d, limit=%d", errReadSelectionTooLarge,
		nextPageOffset(startLine, stoppedAt), nextPageLimit(limit, lines))
}

// nextPageOffset is the first line whose text was not delivered. A line that
// does not fit on a page has no visible text at all, so it belongs to the next
// page rather than being skipped.
func nextPageOffset(startLine, stoppedAt int) int { return max(stoppedAt, startLine) }

// nextPageLimit reuses an explicit caller limit; without one it halves the
// lines that already overflowed, which leaves headroom for the same average
// line length and needs no second copy of the byte budget.
func nextPageLimit(limit, lines int) int {
	if limit > 0 {
		return limit
	}
	return max(lines/2, 1)
}

type WriteFile struct {
	State *fileState
}

func (WriteFile) Name() string { return "write_file" }

func (WriteFile) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "write_file",
		Description: "Write content to a file, creating parent directories. Overwrites if exists.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
	}
}

func (w WriteFile) Run(ctx context.Context, input map[string]any) (string, error) {
	path, err := mustString(input, "path")
	if err != nil {
		return "", err
	}
	content, err := mustString(input, "content")
	if err != nil {
		return "", err
	}
	if err := atomicWrite(path, []byte(content)); err != nil {
		return "", err
	}
	// Re-stamp so this write does not read as an external change later.
	w.State.record(path)
	return fmt.Sprintf("wrote %d bytes to %s", len(content), path), nil
}

type EditFile struct {
	State *fileState
}

func (EditFile) Name() string { return "edit_file" }

func (EditFile) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name:        "edit_file",
		Description: "Replace exactly one occurrence of old_string with new_string in a file. Fails if old_string is missing or appears more than once. Strip the line-number prefix read_file adds before matching; old_string must be the file's raw text.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string"},
				"old_string": map[string]any{"type": "string"},
				"new_string": map[string]any{"type": "string"},
			},
			"required": []string{"path", "old_string", "new_string"},
		},
	}
}

func (e EditFile) Run(ctx context.Context, input map[string]any) (string, error) {
	path, err := mustString(input, "path")
	if err != nil {
		return "", err
	}
	oldStr, err := mustString(input, "old_string")
	if err != nil {
		return "", err
	}
	if oldStr == "" {
		return "", fmt.Errorf("edit_file: old_string must not be empty; provide exact unique text to replace")
	}
	newStr, err := mustString(input, "new_string")
	if err != nil {
		return "", err
	}
	if e.State.changedSinceRead(path) {
		return "", fmt.Errorf("edit_file: %s changed since you read it; read it again and redo the edit against its current contents", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("edit_file: read %s: %w", path, err)
	}
	s := string(b)
	n := strings.Count(s, oldStr)
	if n == 0 {
		return "", fmt.Errorf("edit_file: old_string not found in %s; read the file and retry with exact text", path)
	}
	if n > 1 {
		return "", fmt.Errorf("edit_file: old_string found %d times in %s; include more surrounding text so it is unique", n, path)
	}
	out := strings.Replace(s, oldStr, newStr, 1)
	if err := atomicWrite(path, []byte(out)); err != nil {
		return "", err
	}
	// The agent has seen the result of its own edit, so it is not stale.
	e.State.record(path)
	return fmt.Sprintf("edited %s", path), nil
}

func atomicWrite(path string, content []byte) error {
	return atomicfile.WritePreserveMode(path, content, 0o644, 0o755)
}
