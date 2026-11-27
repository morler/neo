package agent

import (
	"context"
	"fmt"
	"runtime/debug"

	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/tools"
)

// toolPanicError reports a tool whose implementation panicked. It is an error
// result rather than a process exit, and its text says so plainly: the model
// can do nothing about a bug in Neo, and retrying the same call unchanged would
// hit the same panic.
type toolPanicError struct {
	name  string
	value string
}

func (e *toolPanicError) Error() string {
	return fmt.Sprintf("%s tool panicked: %s (this is an internal Neo error, not a problem with this call; retrying it unchanged usually fails the same way, and the stack trace is in the debug log)", e.name, e.value)
}

// runToolSafely is the one place a tool's Run is called, so one tool bug can
// only fail that call. It matters most on the parallel path: runGroup starts a
// goroutine per call, and a panic there has nothing above it to recover, so the
// whole process would exit and take the unsaved session with it. The serial
// path has the same defect with a shorter fuse.
//
// The stack goes to the log only. In the tool result it would spend transcript
// context and show the model local paths it cannot use.
func runToolSafely(ctx context.Context, t tools.Tool, name string, input map[string]any) (out string, err error) {
	// GODEBUG=panicnil=1 makes panic(nil) recover as nil, so the flag rather
	// than the recovered value decides whether the call panicked.
	panicked := true
	defer func() {
		if !panicked {
			return
		}
		r := recover()
		// The panic value is redacted and bounded the same way a tool's own
		// arguments are: a panic while echoing input can carry a credential, and
		// a struct dump costs context the model cannot act on.
		value := logx.SafeString(fmt.Sprint(r), 0)
		logx.Debug("tool panic", "name", name, "panic", value, "stack", string(debug.Stack()))
		out = ""
		err = &toolPanicError{name: name, value: value}
	}()
	out, err = t.Run(ctx, input)
	panicked = false
	return out, err
}
