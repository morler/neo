package tools_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestToolsDoesNotImportAgent guards the dependency direction the sub-call sink
// exists to preserve: internal/tools reports events without knowing the agent.
func TestToolsDoesNotImportAgent(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "github.com/owainlewis/neo/internal/agent" {
			t.Fatal("internal/tools imports internal/agent")
		}
	}
}
