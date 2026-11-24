package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"

	"github.com/owainlewis/neo/internal/llm"
)

// scriptSourceKey is the JS binding the model's source is loaded into, so the
// wrapper can evaluate it without the model's text becoming part of the
// wrapper's own body.
const scriptSourceKey = "__neo_source"

// Defaults for one script. Both are sized so an ordinary script never notices
// them and a runaway one ends quickly: 5 minutes is longer than any read-only
// batch on a large repository, and 64 MiB is four times what a script can put
// into the transcript (MaxOutputBytes).
//
// These are not config knobs. Nothing has measured a case that needs them
// changed, and docs/design/token-budget.md sets the precedent: an unmeasured
// switch is a second behaviour nobody tests.
const (
	DefaultScriptTimeout     = 5 * time.Minute
	DefaultScriptMemoryLimit = 64 << 20
)

// scriptWrapper evaluates the model's source and normalizes its result.
//
// `eval` rather than `new Function` because the two disagree on what the value
// is: a classic script's value is its last expression, while a function body
// returns undefined unless it has an explicit `return`. Verified against the
// engine — `eval("1+41")` is 42 and `(new Function("1+41"))()` is undefined.
//
// The Promise check exists because a classic script may not use top-level
// `await` but can still produce a Promise, and stringifying one yields `{}`.
// Silent empty results are worse than a refusal, so this throws instead.
const scriptWrapper = `(function (source) {
	var value = eval(source);
	if (value !== null && typeof value === "object" && typeof value.then === "function") {
		throw new Error("script returned a Promise; write a synchronous script, async and top-level await are not supported");
	}
	if (value === undefined) {
		return null;
	}
	if (typeof value === "string") {
		return value;
	}
	return JSON.stringify(value);
})(` + scriptSourceKey + `)`

// Script runs model-written JavaScript with a closed set of tools.
//
// The whitelist is built here as a list of values, never as a filtered registry:
// adding a tool has to be an edit to this file. A tool that appears by accident
// is a tool whose approvals and output limits nobody reviewed.
type Script struct {
	// Read, Grep, Glob, Write, and Edit are the whole surface a script has.
	// Each one is an explicit line in globals and every one passes through the
	// approval gate, so a capability cannot appear here by accident.
	//
	// bash is absent on purpose: Neo does not parse shell commands, so it
	// cannot tell a read from a write, and a tool it cannot classify makes the
	// approvals above decorative.
	Read  ReadFile
	Grep  Grep
	Glob  Glob
	Write WriteFile
	Edit  EditFile

	// Timeout bounds one script. Zero means DefaultScriptTimeout. A script that
	// has not finished by then is interrupted, which is the only way to stop a
	// loop the engine cannot otherwise see the end of.
	Timeout time.Duration
	// MemoryLimit bounds what the script's heap may retain, in bytes. Zero means
	// DefaultScriptMemoryLimit. It is the engine's own limit, so a script that
	// allocates past it stops with a typed error rather than being killed by the
	// host after the machine is already under pressure.
	MemoryLimit int64
}

func (Script) Name() string { return "script" }

// ParallelSafe reports false, always. A script runs arbitrary logic over
// whatever the whitelist allows, so running two together would be a concurrency
// decision nobody made. Overriding this needs an isolation argument, not a
// preference.
func (Script) ParallelSafe(map[string]any) bool { return false }

func (Script) Spec() llm.ToolSpec {
	return llm.ToolSpec{
		Name: "script",
		Description: `Run JavaScript that can call tools, returning its last expression's value.
Use it when a later step depends on an earlier result: filter, count, or branch over tool output inside the script instead of sending that output back and forth.
Available inside the script: tools.read_file({path, offset?, limit?}) -> string. Nothing else is defined; there is no bash, no write, and no import.
Write synchronous code. The last expression is the result; strings come back as-is and other values as JSON.`,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source": map[string]any{
					"type":        "string",
					"description": "JavaScript to run. The last expression is the result.",
				},
			},
			"required": []string{"source"},
		},
	}
}

func (s Script) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return DefaultScriptTimeout
}

func (s Script) memoryLimit() int64 {
	if s.MemoryLimit > 0 {
		return s.MemoryLimit
	}
	return DefaultScriptMemoryLimit
}

func (s Script) Run(ctx context.Context, input map[string]any) (string, error) {
	source, err := mustString(input, "source")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(source) == "" {
		return "", fmt.Errorf("input source must not be empty")
	}
	// A cancelled turn reports cancellation rather than a confusing engine error.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	wrapper, err := moejs.CompileScript("script", scriptWrapper)
	if err != nil {
		// The wrapper is a constant in this file, so this means the engine
		// changed under us, not that the model wrote bad code.
		return "", fmt.Errorf("script wrapper does not compile: %w", err)
	}

	rt := moejs.NewRuntime(moejs.Options{MemoryLimit: s.memoryLimit()})
	if err := rt.SetGlobal("tools", s.globals(ctx)); err != nil {
		return "", fmt.Errorf("script setup: %w", err)
	}
	if err := rt.SetGlobal(scriptSourceKey, source); err != nil {
		return "", fmt.Errorf("script setup: %w", err)
	}

	// One deadline covers both the script's own budget and the caller's
	// cancellation, so an interrupted turn reports its real cause instead of
	// waiting out the script's timeout. AfterFunc is what lets the engine stop:
	// RunScript is a single blocking call and cannot select on ctx itself.
	runCtx, cancel := context.WithTimeout(ctx, s.timeout())
	defer cancel()
	stop := context.AfterFunc(runCtx, func() { rt.Interrupt(context.Cause(runCtx)) })
	defer stop()

	result, err := rt.RunScript(wrapper)
	if err != nil {
		return "", describeScriptError(err)
	}
	return s.limitOutput(scriptResult(rt, result))
}

// limitOutput caps what one script contributes to the transcript. The agent
// applies the same cap as a backstop for every tool, but capping here keeps a
// large script result from being built into a string first.
//
// The marker matches the agent's wording so a reader cannot tell which layer
// truncated, and the suggestion is the same one that layer gives.
func (s Script) limitOutput(result string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	if len(result) <= MaxOutputBytes {
		return result, nil
	}
	originalBytes := len(result)
	originalLines := strings.Count(result, "\n") + 1
	marker := fmt.Sprintf("\n\n[tool output truncated: original %d bytes across %d lines; showing first %d bytes. Re-run the tool with narrower output to inspect the rest.]",
		originalBytes, originalLines, MaxOutputBytes)
	keep := MaxOutputBytes - len(marker)
	if keep <= 0 {
		return trimToBytes(marker, MaxOutputBytes), nil
	}
	return trimToBytes(result, keep) + marker, nil
}

// trimToBytes cuts at a rune boundary so the result stays valid UTF-8.
func trimToBytes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// globals builds the tool namespace. Every entry is an explicit edit here.
func (s Script) globals(ctx context.Context) map[string]any {
	// The counter numbers sub-calls within this one script execution, so the
	// agent can tell two read_file calls apart even when the arguments match.
	calls := &subCallCounter{}
	return map[string]any{
		"read_file":  s.hostTool(ctx, s.Read, calls),
		"grep":       s.hostTool(ctx, s.Grep, calls),
		"glob":       s.hostTool(ctx, s.Glob, calls),
		"write_file": s.hostTool(ctx, s.Write, calls),
		"edit_file":  s.hostTool(ctx, s.Edit, calls),
	}
}

// subCallCounter numbers the calls one script makes. It is not shared and not
// concurrent: a script runs on one goroutine, which is why the tool is serial.
type subCallCounter struct{ n int }

func (c *subCallCounter) next(parent string) string {
	c.n++
	return fmt.Sprintf("%s#%d", parent, c.n)
}

// hostTool adapts one Go tool into a synchronous JS function.
//
// The arguments are converted to Go before the tool runs, so nothing the script
// does to its own object afterwards can change what executes. An error thrown
// here surfaces as a JS exception, which is how a script sees a failed tool.
func (s Script) hostTool(ctx context.Context, t Tool, calls *subCallCounter) moejs.NativeFunc {
	// The parent's tool-use ID is what the agent already knows: reusing
	// CallMetadata means this file needs no second context key and no import of
	// the agent package.
	parent, _ := CallMetadataFrom(ctx)
	report := func(id, name string, input map[string]any, text string, isError, done bool) {
		ReportSubCall(ctx, SubCall{
			ID: id, Name: name, Args: input, Text: text, IsError: isError, Done: done,
		})
	}
	return func(r *engine.Realm, _ engine.Value, args []engine.Value) (engine.Value, error) {
		input := map[string]any{}
		if len(args) > 0 {
			if converted, ok := r.ToGo(args[0]).(map[string]any); ok {
				input = converted
			}
		}
		id := calls.next(parent.ToolUseID)
		name := t.Name()
		report(id, name, input, "", false, false)

		// Approval is enforced here, at the tool boundary, not at the script
		// boundary. The outer `script` call is one rule away from every tool the
		// whitelist exposes, so a matcher that only sees "script" cannot express
		// "ask before write_file". The argument is the Go map built above, not
		// the JS object, so a script cannot change what was approved afterwards.
		if err := requireApproval(ctx, name, input, "script"); err != nil {
			report(id, name, input, err.Error(), true, true)
			return engine.Undefined(), err
		}

		out, err := t.Run(ctx, input)
		if err != nil {
			report(id, name, input, err.Error(), true, true)
			return engine.Undefined(), err
		}
		report(id, name, input, out, false, true)
		return engine.StringValue(engine.FromGoString(out)), nil
	}
}

// scriptResult turns the wrapper's value into the tool result. The wrapper
// already stringifies everything except strings and null, so the common case is
// a Go string.
func scriptResult(rt *moejs.Runtime, result engine.Value) (string, error) {
	if result.IsUndefined() || result.IsNullish() {
		return "", nil
	}
	converted, err := rt.ToGo(result)
	if err != nil {
		return "", describeScriptError(err)
	}
	if text, ok := converted.(string); ok {
		return text, nil
	}
	if converted == nil {
		return "", nil
	}
	return fmt.Sprintf("%v", converted), nil
}

// describeScriptError names the failure class, because the model reacts
// differently to "your source did not compile" and "your source threw".
//
// A syntax error inside `eval` arrives as a thrown SyntaxError rather than the
// compile-time *SyntaxError, since the source is only parsed when the script
// runs. Both are reported as syntax errors so the model does not go looking for
// a runtime bug it cannot find.
func describeScriptError(err error) error {
	// A memory limit and a deadline both surface as an interrupt. Telling them
	// apart matters: one means "allocate less", the other "finish sooner", and a
	// model given the wrong one retries the wrong thing.
	var limit *moejs.MemoryLimitError
	if errors.As(err, &limit) {
		return fmt.Errorf("script exceeded its memory limit: %w", err)
	}
	var interrupted *moejs.InterruptedError
	if errors.As(err, &interrupted) {
		// The interrupt's value is already in the message, including whether it
		// was a deadline or a cancellation, so no second wording is needed.
		return fmt.Errorf("script stopped: %w", err)
	}
	var syntax *moejs.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Errorf("script has a syntax error: %w", err)
	}
	var exception *moejs.Exception
	if errors.As(err, &exception) {
		if exception.Name() == "SyntaxError" {
			return fmt.Errorf("script has a syntax error: %w", err)
		}
		return fmt.Errorf("script threw: %w", err)
	}
	return fmt.Errorf("script failed: %w", err)
}

// NewScript builds the script tool over the file tools the coordinator already
// holds, so a file a script reads is a file the next edit knows about. Passing
// the same values rather than new ones is what keeps the stale-edit guard
// meaningful across the script boundary.
//
// Only the tools named here are reachable from JavaScript. Adding one means
// adding a struct field and a line in globals; there is no registry lookup that
// could grow the surface without an edit.
func NewScript(files []Tool) Script {
	s := Script{}
	for _, t := range files {
		switch v := t.(type) {
		case ReadFile:
			s.Read = v
		case Grep:
			s.Grep = v
		case Glob:
			s.Glob = v
		case WriteFile:
			s.Write = v
		case EditFile:
			s.Edit = v
		}
	}
	return s
}
