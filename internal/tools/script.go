package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"

	"github.com/owainlewis/neo/internal/llm"
)

// scriptSourceKey is the JS binding the model's source is loaded into, so the
// wrapper can evaluate it without the model's text becoming part of the
// wrapper's own body.
const scriptSourceKey = "__neo_source"

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
	// Read is the only tool a script can call today. Search, writes, and the
	// approval gate that comes with them land in later steps; each one adds an
	// explicit line to globals.
	Read ReadFile
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

	rt := moejs.NewRuntime(moejs.Options{})
	if err := rt.SetGlobal("tools", s.globals(ctx)); err != nil {
		return "", fmt.Errorf("script setup: %w", err)
	}
	if err := rt.SetGlobal(scriptSourceKey, source); err != nil {
		return "", fmt.Errorf("script setup: %w", err)
	}

	result, err := rt.RunScript(wrapper)
	if err != nil {
		return "", describeScriptError(err)
	}
	return scriptResult(rt, result)
}

// globals builds the tool namespace. Every entry is an explicit edit here.
func (s Script) globals(ctx context.Context) map[string]any {
	return map[string]any{
		"read_file": s.hostTool(ctx, s.Read),
	}
}

// hostTool adapts one Go tool into a synchronous JS function.
//
// The arguments are converted to Go before the tool runs, so nothing the script
// does to its own object afterwards can change what executes. An error thrown
// here surfaces as a JS exception, which is how a script sees a failed tool.
func (s Script) hostTool(ctx context.Context, t Tool) moejs.NativeFunc {
	return func(r *engine.Realm, _ engine.Value, args []engine.Value) (engine.Value, error) {
		input := map[string]any{}
		if len(args) > 0 {
			if converted, ok := r.ToGo(args[0]).(map[string]any); ok {
				input = converted
			}
		}
		out, err := t.Run(ctx, input)
		if err != nil {
			return engine.Undefined(), err
		}
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
