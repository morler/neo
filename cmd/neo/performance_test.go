package main

import (
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/goal"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/profile"
	"github.com/owainlewis/neo/internal/skills"
	"github.com/owainlewis/neo/internal/subagent"
	"github.com/owainlewis/neo/internal/tools"
	"github.com/owainlewis/neo/internal/workflow"
)

// Two budgets, because the prompt is no longer one size. The core is sent on
// every request in every mode and is the number that matters most; the chat
// budget covers it plus the workflow and delegation sections, which only exist
// where those tools do.
//
// These cover the system prompt alone. fixedFloorByteBudget below covers the
// other half of a request: the tool schemas, which nothing gated before.
const (
	// 1400 was the ceiling before the script capability; the core prompt is
	// 1,282 with it, so the script section fits without moving this one.
	corePromptByteBudget = 1400
	// 2,600 before the script capability, 2,985 with it. Rounded up to the next
	// 500, the same rule the fixed floor uses.
	chatPromptByteBudget = 3000
)

// fixedFloorByteBudget caps what every chat turn re-sends before the
// conversation starts: the tool schemas plus the system prompt as a chat
// session actually builds it, with the built-in skill catalog. Both are resent
// on every request, so growth here is paid per turn, which is what rule 3 of
// docs/design/token-budget.md asks a test to prevent.
//
// It covers the default chat surface, which has no script tool. That tool is
// off unless the config or --script turns it on, because a measurement found no
// task where the model chose it over bash; keeping it out of the default is what
// keeps this ceiling where it was. Re-measure with
// `go test ./cmd/neo/ -run TestFixedPromptFloorBudget -v` after adding a tool.
const fixedFloorByteBudget = 8_500

// scriptFloorByteBudget covers the same surface with `features.script` on:
// measured 2026-11-24 at 9,499 bytes (5,873 tool definitions + 3,626 prompt),
// then 9,555 on 2026-11-25 when the judgement moved from file count to
// dependency (5,873 + 3,682), then 9,653 when bash's description gained one
// sentence naming the script decision point (5,971 + 3,682, #87), then 9,796
// when the batching rule was qualified against a script being one serial call
// (5,971 + 3,825, #89). The script schema is 851 and the script tool's own
// prompt section is 620; the rest of the prompt delta is that tie-break. The
// ceiling was 9,500 before the prompt was rewritten around task shapes with a
// runnable example; that left one byte of room, which would have made the next
// edit a failing test rather than a decision. Rounded up to the next 500.
const scriptFloorByteBudget = 10_000

func TestChatSystemPromptSizeBudgets(t *testing.T) {
	headless, _ := chatSystem(&config.Config{}, "", nil, profile.Profile{}, newRegistry("", ""), io.Discard)
	if len(headless) > corePromptByteBudget {
		t.Fatalf("always-loaded prompt = %d bytes, budget = %d", len(headless), corePromptByteBudget)
	}
	chat, _ := chatSystem(&config.Config{}, "", nil, profile.Profile{}, chatRegistry(), io.Discard)
	if len(chat) > chatPromptByteBudget {
		t.Fatalf("chat prompt = %d bytes, budget = %d", len(chat), chatPromptByteBudget)
	}
}

// fixedFloor is the part of a request that never depends on the conversation.
type fixedFloor struct {
	toolsBytes  int
	promptBytes int
	blocks      []llm.SystemBlock
}

func (f fixedFloor) total() int { return f.toolsBytes + f.promptBytes }

// measureFixedFloor renders the chat registry's schemas and the system prompt a
// chat session gets, with the built-in skill catalog but no project context, so
// the result depends only on the code.
func measureFixedFloor(t *testing.T) fixedFloor {
	t.Helper()
	reg := chatRegistry()
	schemas, err := json.Marshal(reg.Specs())
	if err != nil {
		t.Fatalf("marshal tool specs: %v", err)
	}
	prompt, blocks := chatSystem(&config.Config{}, "", skills.Defaults(), profile.Profile{}, reg, io.Discard)
	return fixedFloor{toolsBytes: len(schemas), promptBytes: len(prompt), blocks: blocks}
}

// TestFixedPromptFloorBudget gates two rules with one measurement: the fixed
// floor stays under a checked ceiling (rule 3), and rendering the same state
// twice yields a byte-identical prefix with a cacheable base block (rule 1).
// They read the same Specs() and chatSystem output, so splitting them would
// only mean collecting it twice.
func TestFixedPromptFloorBudget(t *testing.T) {
	floor := measureFixedFloor(t)
	t.Logf("fixed floor: tools %d + prompt %d = %d bytes (budget %d)",
		floor.toolsBytes, floor.promptBytes, floor.total(), fixedFloorByteBudget)
	if floor.total() > fixedFloorByteBudget {
		t.Fatalf("fixed floor = %d bytes (tools %d + prompt %d), budget = %d, over by %d",
			floor.total(), floor.toolsBytes, floor.promptBytes, fixedFloorByteBudget,
			floor.total()-fixedFloorByteBudget)
	}

	if len(floor.blocks) == 0 {
		t.Fatal("system prompt has no blocks")
	}
	if !floor.blocks[0].Cache {
		t.Fatal("the base system block must be marked cacheable")
	}

	again := measureFixedFloor(t)
	if len(again.blocks) != len(floor.blocks) {
		t.Fatalf("block count changed between renders: %d then %d", len(floor.blocks), len(again.blocks))
	}
	for i := range floor.blocks {
		if floor.blocks[i] != again.blocks[i] {
			t.Fatalf("system block %d changed between renders with identical inputs", i)
		}
	}
	if again.toolsBytes != floor.toolsBytes || again.promptBytes != floor.promptBytes {
		t.Fatalf("floor changed between renders: %+v then %+v", floor, again)
	}
}

// TestFixedPromptFloorRespondsToToolChanges is the reverse assertion: it proves
// the measurement tracks the tool surface, so the budget test can actually fail
// when a tool grows.
func TestFixedPromptFloorRespondsToToolChanges(t *testing.T) {
	full := measureFixedFloor(t)
	filtered, err := json.Marshal(chatRegistry().Filter([]string{"bash", "read_file"}).Specs())
	if err != nil {
		t.Fatalf("marshal filtered specs: %v", err)
	}
	if len(filtered) >= full.toolsBytes {
		t.Fatalf("filtered tool schemas = %d bytes, want fewer than the full %d",
			len(filtered), full.toolsBytes)
	}

	// The prompt also follows the registry: capability sections disappear with
	// the tools that justify them.
	narrow, _ := chatSystem(&config.Config{}, "", skills.Defaults(), profile.Profile{},
		chatRegistry().Filter([]string{"bash", "read_file"}), io.Discard)
	if len(narrow) >= full.promptBytes {
		t.Fatalf("prompt for two tools = %d bytes, want fewer than the full %d",
			len(narrow), full.promptBytes)
	}
}

// TestFixedPromptFloorKeepsTheTailUncached pins where the clock can leak in.
// environmentSection takes the current time, so it must land after the cache
// breakpoint; if it ever moved into the base block, every session would miss
// the cache on its first turn.
func TestFixedPromptFloorKeepsTheTailUncached(t *testing.T) {
	base := measureFixedFloor(t)
	if len(base.blocks) != 1 {
		t.Fatalf("blocks without a cwd = %d, want the single cacheable base block", len(base.blocks))
	}

	_, blocks := chatSystem(&config.Config{}, "/tmp/neo", skills.Defaults(), profile.Profile{}, chatRegistry(), io.Discard)
	if len(blocks) < 2 {
		t.Fatalf("blocks with a cwd = %d, want a dynamic tail after the base", len(blocks))
	}
	if !blocks[0].Cache || blocks[1].Cache {
		t.Fatalf("cache flags = %v, %v; want the base cached and the tail not",
			blocks[0].Cache, blocks[1].Cache)
	}
	if blocks[0].Text != base.blocks[0].Text {
		t.Fatal("adding a working directory changed the cacheable base block")
	}
}

// TestFixedFloorCoversTheWholeChatSurface guards the measurement: a registry
// that quietly lost a tool would make the budget easier to pass without anyone
// noticing the prompt no longer describes the real tool surface.
func TestFixedFloorCoversTheWholeChatSurface(t *testing.T) {
	want := []string{"agent", "bash", "edit_file", "glob", "goal", "grep", "read_file", "workflow", "write_file"}
	names := chatRegistry().Names()
	if len(names) != len(want) {
		t.Fatalf("chat registry has %d tools %v, want %d", len(names), names, len(want))
	}
	for _, name := range want {
		found := false
		for _, got := range names {
			if got == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("chat registry is missing %q; the floor no longer covers the real tool surface", name)
		}
	}
}

// BenchmarkChatSystem tracks the stable base prompt construction path. Project
// context is intentionally excluded because its size and I/O depend on the
// checkout running the benchmark.
func BenchmarkChatSystem(b *testing.B) {
	cfg := &config.Config{}
	prompt, _ := chatSystem(cfg, "", nil, profile.Profile{}, chatRegistry(), io.Discard)
	b.ReportAllocs()

	for b.Loop() {
		prompt, blocks := chatSystem(cfg, "", nil, profile.Profile{}, chatRegistry(), io.Discard)
		if len(prompt) == 0 || len(blocks) != 1 {
			b.Fatal("unexpected base prompt")
		}
	}
	b.ReportMetric(float64(len(prompt)), "prompt_bytes")
}

// TestScriptFloorBudget measures the classic surface with the script tool on,
// which is the largest floor the code produces. It exists because an earlier
// version of this file measured only the default surface and would have kept
// passing after the tool's cost grew.
func TestScriptFloorBudget(t *testing.T) {
	on := true
	cfg := &config.Config{}
	cfg.Features.Script = &on
	cfg.Features.CodeMode = config.CodeModeClassic
	reg := registryFor(cfg, "", "", workflow.Tool{}, subagent.AgentTool{}, goal.Tool{})
	schemas, err := json.Marshal(reg.Specs())
	if err != nil {
		t.Fatalf("marshal tool specs: %v", err)
	}
	prompt, _ := chatSystem(cfg, "", skills.Defaults(), profile.Profile{}, reg, io.Discard)
	total := len(schemas) + len(prompt)
	t.Logf("script floor: tools %d + prompt %d = %d bytes (budget %d)",
		len(schemas), len(prompt), total, scriptFloorByteBudget)
	if total > scriptFloorByteBudget {
		t.Fatalf("script floor = %d bytes, budget = %d, over by %d",
			total, scriptFloorByteBudget, total-scriptFloorByteBudget)
	}
}

// TestScriptToolIsOnByDefault is the decision itself, as a test: code execution
// is what the surface is built around, so a session gets it without asking.
func TestScriptToolIsOnByDefault(t *testing.T) {
	if !(&config.Config{}).ScriptEnabled() {
		t.Fatal("script tool is disabled by default")
	}
	if _, ok := registryFor(&config.Config{}, "", "").Get("script"); !ok {
		t.Fatal("default registry has no script tool")
	}
}

// TestBashHintFollowsTheScriptTool is the composition contract for the hint in
// bash's description, which only the classic surface has. registryFor is the one
// place that decides, so the text about bash and the availability of script
// cannot disagree.
func TestBashHintFollowsTheScriptTool(t *testing.T) {
	withoutScript := &config.Config{Features: config.Features{CodeMode: config.CodeModeClassic, Script: new(bool)}}
	withScript := &config.Config{Features: config.Features{CodeMode: config.CodeModeClassic}}

	plainBash, ok := registryFor(withoutScript, "", "").Get("bash")
	if !ok {
		t.Fatal("classic registry without script has no bash")
	}
	scriptedBash, ok := registryFor(withScript, "", "").Get("bash")
	if !ok {
		t.Fatal("classic registry with script has no bash")
	}

	// Compared against the tool itself rather than a literal: this test is about
	// which description the registry selects, and the literal is pinned in
	// internal/tools.
	if got, want := plainBash.Spec().Description, (tools.Bash{}).Spec().Description; got != want {
		t.Fatalf("description without script = %q, want the plain one %q", got, want)
	}
	if got, want := scriptedBash.Spec().Description, (tools.Bash{ScriptAvailable: true}).Spec().Description; got != want {
		t.Fatalf("description with script = %q, want the hinted one %q", got, want)
	}
}

// TestScriptTieBreakReachesTheClassicPromptOnly covers the prompt half of the
// batching conflict on the surface that still batches: classic with bash and the
// file tools. Code mode has no second top-level call, so it drops the rule
// instead, which TestCodeModePromptDropsTheBatchingRule pins.
func TestScriptTieBreakReachesTheClassicPromptOnly(t *testing.T) {
	if !strings.HasSuffix(chatSystemPrompt, chatPromptTail) {
		t.Fatal("chatSystemPrompt does not end with chatPromptTail, so the scripted rewrite cannot replace it")
	}
	if strings.Contains(chatSystemPrompt, "one script call") {
		t.Fatal("the base prompt mentions script; a session without the tool pays for it")
	}

	classicOff := &config.Config{Features: config.Features{CodeMode: config.CodeModeClassic, Script: new(bool)}}
	classicOn := &config.Config{Features: config.Features{CodeMode: config.CodeModeClassic}}
	plain, _ := chatSystem(classicOff, "", nil, profile.Profile{}, registryFor(classicOff, "", ""), io.Discard)
	scripted, _ := chatSystem(classicOn, "", nil, profile.Profile{}, registryFor(classicOn, "", ""), io.Discard)

	// chatSystem appends environment and project sections after the
	// instructions, so the tail is found in the body rather than compared to the
	// end of the whole prompt.
	if !strings.Contains(plain, chatPromptTail) {
		t.Fatal("the classic prompt without script does not carry the plain tail")
	}
	if !strings.Contains(scripted, chatPromptTailScripted) {
		t.Fatal("the classic prompt with script does not carry the scripted tail")
	}
	if strings.Contains(plain, "one script call") {
		t.Fatal("the plain prompt resolves a conflict it cannot have")
	}
}

// promptConfig builds a config whose script setting is the given one.
func promptConfig(on bool) *config.Config {
	cfg := &config.Config{}
	cfg.Features.Script = &on
	return cfg
}

// TestDefaultPromptKeepsThePlainBatchingRule pins the default prompt byte for
// byte. The batching rule moved into a constant so a session that can script
// can qualify it; a session that cannot must read exactly the text it read
// before, or the fixed floor grows for a capability it does not have.
func TestDefaultPromptKeepsThePlainBatchingRule(t *testing.T) {
	const want = "You are neo, a coding agent working in the user's current working directory.\n" +
		"\n" +
		"Inspect the project before changing it, and prefer the smallest change that does\n" +
		"the job. After changing code, run the checks that cover it and say what you ran;\n" +
		"if something could not be verified, say that instead of implying it passed.\n" +
		"\n" +
		"Answer the request that was made. An inspection, explanation, review, or design\n" +
		"request is answered, not implemented, unless changes were asked for. Leave\n" +
		"unrelated work alone, and do not commit, push, or deploy unless asked.\n" +
		"\n" +
		"Before a tool call, write one short sentence explaining what you are checking or\n" +
		"changing and why. Do not narrate obvious individual calls or expose private\n" +
		"reasoning. Issue independent reads, searches, or inspections together in one\n" +
		"response. When you finish a task, briefly summarize what changed."
	if chatSystemPrompt != want {
		t.Fatalf("chatSystemPrompt = %q, want %q", chatSystemPrompt, want)
	}
}

// codeModeFloorByteBudget covers the default surface: one code tool plus the
// prompt a session that acts by writing code builds. Measured 2026-11-25 at
// 2,970 bytes (856 schema + 2,114 prompt) before the coordinating tools and
// their sections are added, so this is the floor of the mode itself.
const codeModeFloorByteBudget = 3_500

// TestCodeModeFloorBudget measures the surface the default registry builds. It
// is separate from the classic floor because the tool list and prompt sections
// are both different, so folding it into one number would hide which moved.
func TestCodeModeFloorBudget(t *testing.T) {
	cfg := &config.Config{}
	reg := registryFor(cfg, "", "")
	schemas, err := json.Marshal(reg.Specs())
	if err != nil {
		t.Fatalf("marshal tool specs: %v", err)
	}
	prompt, _ := chatSystem(cfg, "", nil, profile.Profile{}, reg, io.Discard)
	total := len(schemas) + len(prompt)
	t.Logf("code-mode floor: tools %d + prompt %d = %d bytes (budget %d)",
		len(schemas), len(prompt), total, codeModeFloorByteBudget)
	if total > codeModeFloorByteBudget {
		t.Fatalf("code-mode floor = %d bytes, budget = %d", total, codeModeFloorByteBudget)
	}
}

// TestCodeModeKeepsTheCoordinatingTools is the decision that separates this mode
// from a single-tool session: writing code does not mean giving up delegation,
// goals or the checklist, so the tools the caller passes in must survive.
func TestCodeModeKeepsTheCoordinatingTools(t *testing.T) {
	names := registryFor(&config.Config{}, "", "", workflow.Tool{}, subagent.AgentTool{}, goal.Tool{}).Names()
	want := []string{"agent", "goal", "script", "workflow"}
	if !slices.Equal(names, want) {
		t.Fatalf("code-mode tools = %v, want %v", names, want)
	}
}

// TestClassicModeKeepsThePerActionTools pins the other shape, which is what a
// session asks for when it does not want its actions written as code.
func TestClassicModeKeepsThePerActionTools(t *testing.T) {
	cfg := &config.Config{}
	cfg.Features.CodeMode = config.CodeModeClassic
	names := registryFor(cfg, "", "").Names()
	for _, want := range []string{"bash", "read_file", "write_file", "edit_file", "grep", "glob", "script"} {
		if !slices.Contains(names, want) {
			t.Fatalf("classic tools = %v, want %q", names, want)
		}
	}
}

// TestCodeModePromptDropsTheBatchingRule keeps the two prompts from
// contradicting themselves. Acting through one code tool leaves no second
// top-level call to batch, so asking for it would describe a decision the model
// cannot make.
func TestCodeModePromptDropsTheBatchingRule(t *testing.T) {
	cfg := &config.Config{}
	prompt, _ := chatSystem(cfg, "", nil, profile.Profile{}, registryFor(cfg, "", ""), io.Discard)

	if strings.Contains(prompt, "Issue independent reads") {
		t.Fatal("the code-mode prompt still asks for independent top-level calls")
	}
	for _, want := range []string{"run(", "globalThis"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("code-mode prompt lacks %q", want)
		}
	}
}
