package tools

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
)

// DefaultRunTimeout bounds one program a script starts. A script that runs
// tests or a build needs longer than an ordinary command and shorter than the
// script's own deadline, so a hung program is reported as itself rather than as
// the script timing out.
const DefaultRunTimeout = 2 * time.Minute

// runResult separates what the program printed from how it ended, because a
// script usually wants both: the output decides the next step, and a non-zero
// exit is the fact that the step failed.
type runResult struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Code   int    `json:"code"`
}

// runBounded executes one program for `run()`. It is deliberately not bash: the
// argv form means a script passes arguments without a shell to re-split them,
// so a path with a space needs no quoting.
//
// Output is capped at MaxOutputBytes by keeping the middle out and both ends in:
// a failing test prints its error last, and a build prints its first error
// first, so a prefix-only cut would drop the one line that matters.
func runBounded(ctx context.Context, dir string, argv []string, timeout time.Duration) (runResult, error) {
	if len(argv) == 0 {
		return runResult{}, errors.New("run needs a program name")
	}
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	parent := ctx
	runCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.Command(argv[0], argv[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	configureProcessGroup(cmd)
	stdout := newBoundedOutput(MaxOutputBytes)
	stderr := newBoundedOutput(MaxOutputBytes)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return runResult{Stderr: err.Error(), Code: -1}, fmt.Errorf("run %s: %w", argv[0], err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-runCtx.Done():
		killProcessGroup(cmd)
		<-done
		if parent.Err() != nil {
			return runResult{Stdout: stdout.String(), Stderr: stderr.String(), Code: -1}, fmt.Errorf("run cancelled: %w", parent.Err())
		}
		return runResult{Stdout: stdout.String(), Stderr: stderr.String(), Code: -1}, fmt.Errorf("run exceeded timeout %s", timeout)
	}

	result := runResult{Stdout: stdout.String(), Stderr: stderr.String(), Code: 0}
	if waitErr != nil {
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			// A non-zero exit is a result, not a Go error: the script decides
			// what to do with code != 0, which is the whole point of running a
			// check from inside one.
			result.Code = exit.ExitCode()
			return result, nil
		}
		return result, fmt.Errorf("run %s: %w", argv[0], waitErr)
	}
	return result, nil
}

// runHost exposes run() to a script. The arguments are read from the realm
// before the program starts, so nothing the script does afterwards changes what
// executes.
func (s Script) runHost(ctx context.Context) func(argv []string, timeout time.Duration) (runResult, error) {
	return func(argv []string, timeout time.Duration) (runResult, error) {
		return runBounded(ctx, s.CWD, argv, timeout)
	}
}

// runSummary renders what a program did as one line for the transcript, which
// is what a script prints when it only needs to know whether a check passed.
func (r runResult) summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "exit %d", r.Code)
	if out := strings.TrimSpace(r.Stdout); out != "" {
		b.WriteString("\nstdout:\n")
		b.WriteString(out)
	}
	if err := strings.TrimSpace(r.Stderr); err != "" {
		b.WriteString("\nstderr:\n")
		b.WriteString(err)
	}
	return b.String()
}

// runTool adapts run() for a script. Its argument is an argv array rather than a
// command line: the script passes a program and its arguments separately, so
// there is no shell to re-split a path and no quoting convention to discover.
//
// A non-zero exit is returned to the script as data, not thrown, because a check
// that fails is usually the information the script branched on to get here.
func (s Script) runTool(ctx context.Context, calls *subCallCounter) moejs.NativeFunc {
	parent, _ := CallMetadataFrom(ctx)
	return func(r *engine.Realm, _ engine.Value, args []engine.Value) (engine.Value, error) {
		argv, timeout, err := runArgs(r, args)
		id := calls.next(parent.ToolUseID)
		report := func(text string, isErr bool) {
			ReportSubCall(ctx, SubCall{
				ID: id, Name: "run", Args: map[string]any{"argv": argv}, Text: text, IsError: isErr, Done: true,
			})
		}
		if err != nil {
			report(err.Error(), true)
			return engine.Undefined(), err
		}

		// The script decides whether it may start programs, and the composition
		// root decides whether the script exists at all. There is deliberately no
		// per-command approval here: Neo does not parse programs, so a matcher
		// that saw them could only look like containment it does not provide.
		result, runErr := runBounded(ctx, s.CWD, argv, timeout)
		if runErr != nil {
			report(runErr.Error(), true)
			return engine.Undefined(), runErr
		}
		// A non-zero exit is the result of the command, not a failure of the call:
		// run() exists so a script can branch on a failing check. Reporting it as
		// an error made every failing `go test` inflate the session's tool-error
		// count, which is what the #91 measurement read as a mode defect.
		text := result.summary()
		report(text, false)

		value, convErr := r.FromGo(map[string]any{
			"stdout": result.Stdout,
			"stderr": result.Stderr,
			"code":   result.Code,
		})
		if convErr != nil {
			return engine.Undefined(), convErr
		}
		return value, nil
	}
}

// runArgs reads run(argv[, timeoutSeconds]) from the realm. Everything is
// converted before the program starts, so the script cannot change what runs.
func runArgs(r *engine.Realm, args []engine.Value) ([]string, time.Duration, error) {
	timeout := DefaultRunTimeout
	if len(args) == 0 {
		return nil, timeout, errors.New(`run needs an argv array, for example run(["go", "test", "./..."])`)
	}
	converted, ok := r.ToGo(args[0]).([]any)
	if !ok {
		return nil, timeout, errors.New("run's first argument must be an array of strings")
	}
	argv := make([]string, 0, len(converted))
	for _, item := range converted {
		text, ok := item.(string)
		if !ok {
			return nil, timeout, fmt.Errorf("run's argv must hold only strings, got %T", item)
		}
		argv = append(argv, text)
	}
	if len(args) > 1 {
		seconds, err := runSeconds(r.ToGo(args[1]))
		if err != nil {
			return nil, timeout, err
		}
		timeout = seconds
	}
	return argv, timeout, nil
}

// runSeconds reads run()'s timeout without demanding one Go type. A script that
// writes 10 and one that writes 10.5 mean the same thing; rejecting the integer
// spelling cost three calls in a measured run, so every numeric shape a JS
// number can arrive in is accepted. The ceiling is the script's own timeout,
// which already bounds the call.
func runSeconds(v any) (time.Duration, error) {
	var seconds float64
	switch n := v.(type) {
	case float64:
		seconds = n
	case int:
		seconds = float64(n)
	case int64:
		seconds = float64(n)
	case int32:
		seconds = float64(n)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0, fmt.Errorf("run's timeout must be a number of seconds, got %q", n)
		}
		seconds = parsed
	default:
		return 0, fmt.Errorf("run's timeout must be a number of seconds, got %T", v)
	}
	if math.IsNaN(seconds) || seconds <= 0 {
		return 0, fmt.Errorf("run's timeout must be a positive number of seconds, got %v", v)
	}
	// Clamp before converting: a Duration counts nanoseconds in an int64, so a
	// large number of seconds would overflow into a deadline that is already past.
	if seconds >= MaxBashTimeout.Seconds() {
		return MaxBashTimeout, nil
	}
	d := time.Duration(seconds * float64(time.Second))
	if d <= 0 {
		return 0, fmt.Errorf("run's timeout is too small to start a program, got %v seconds", v)
	}
	return d, nil
}
