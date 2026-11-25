package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/config"
	"github.com/owainlewis/neo/internal/goal"
	"github.com/owainlewis/neo/internal/llm/llmtest"
	"github.com/owainlewis/neo/internal/profile"
	"github.com/owainlewis/neo/internal/session"
)

// These tests cover the chat runtime: the assembly that happens once before the
// TUI starts, and the three switches /model, /provider, and /codemode perform
// between turns. Every switch is defined by what it leaves behind when the
// config write fails, so the rollback branches are the point of the tests, not
// an afterthought. Nothing here reaches the network: custom providers are
// declared inline and, where a credential is needed, the fake stands in.

// keylessConfig returns a config with two OpenAI-compatible providers that need
// no credential, so a switch can construct a backend without any environment.
// Its source is empty, which is what the config package treats as "embedded":
// writes then land in $HOME/.neo/config.yaml rather than in the repository.
func keylessConfig() *config.Config {
	return &config.Config{
		Provider: "local",
		Model:    "qwen3-coder",
		Providers: map[string]config.CustomProvider{
			"local": {Endpoint: "https://llm.internal/v1", DefaultModel: "qwen3-coder"},
			"other": {Endpoint: "https://other.internal/v1", DefaultModel: "other-model"},
		},
	}
}

// tempHome points $HOME at a fresh directory so a persisted switch is readable
// and never touches the developer's real config.
func tempHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("create home: %v", err)
	}
	t.Setenv("HOME", home)
	return home
}

// blockedHome points $HOME at a regular file, so creating ~/.neo/config.yaml
// must fail. It is how a failed persist is produced without a broken filesystem.
func blockedHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	blocked := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	t.Setenv("HOME", blocked)
	return blocked
}

// scriptAvailable reports whether the agent can currently call the script tool.
// RunTool goes through the same registry the model would, so it answers the
// question the switch is about without needing a turn.
func scriptAvailable(t *testing.T, ag *agent.Agent) bool {
	t.Helper()
	out, isErr := ag.RunTool(context.Background(), "script", map[string]any{"source": "1+1"})
	if isErr && strings.Contains(out, "unknown tool") {
		return false
	}
	if isErr {
		t.Fatalf("script call failed: %s", out)
	}
	if out != "2" {
		t.Fatalf("script returned %q, want 2", out)
	}
	return true
}

// chatRuntimeFor builds the runtime the switches mutate. The provider is the
// fake, which answers only the questions the runtime asks of it.
func chatRuntimeFor(cfg *config.Config) *chatRuntime {
	ag := agent.New(agent.Config{
		Model:    cfg.Model,
		Provider: &llmtest.FakeProvider{},
		// Built through the composition root so the fixture's tool surface is the
		// one a session actually gets, which is what /codemode switches.
		Tools: registryFor(cfg, "", ""),
	})
	return &chatRuntime{
		cfg:            cfg,
		ag:             ag,
		provider:       &llmtest.FakeProvider{},
		providerName:   cfg.Provider,
		model:          cfg.Model,
		providerModels: map[string]string{cfg.Provider: cfg.Model},
		cwd:            "",
	}
}

// TestInteractiveSurfaceIsNeverEmpty covers #106. In code mode with
// features.script off, registryFor leaves only the coordinating tools, so the
// surface is exactly as wide as what the caller passes in. headlessAgent checks
// that registry and refuses an empty one; the interactive path instead relied on
// chatExtraTools happening to return workflow and goal. This test states the
// invariant where it can actually fail, and covers both surfaces so a future
// change to the classic branch cannot quietly empty that one instead.
func TestInteractiveSurfaceIsNeverEmpty(t *testing.T) {
	for _, surface := range []string{config.CodeModeCodemode, config.CodeModeClassic} {
		for _, script := range []bool{true, false} {
			t.Run(surface+"/script="+strconv.FormatBool(script), func(t *testing.T) {
				cfg := keylessConfig()
				cfg.Features.CodeMode = surface
				cfg.SetScript(script)
				sess := &session.Session{Metadata: session.Metadata{CWD: t.TempDir()}}
				cwd := t.TempDir()
				var errOut bytes.Buffer

				rt, _ := buildChatRuntime(context.Background(), cfg, &llmtest.FakeProvider{}, "local", "qwen3-coder",
					sess, profile.Profile{}, cwd, cwd, stdio{out: &bytes.Buffer{}, err: &errOut})

				// Rebuild from the runtime the way /codemode does, which is the exact
				// path that would install an empty tool list mid-session.
				names := registryFor(cfg, rt.runtime.cwd, rt.runtime.root, rt.runtime.extra...).Names()
				if len(names) == 0 {
					t.Fatalf("interactive session on %s/script=%v was built with no tools", surface, script)
				}
			})
		}
	}
}

func TestBuildChatRuntimeAssemblesToolsPromptAndAgent(t *testing.T) {
	cfg := keylessConfig()
	sess := &session.Session{Metadata: session.Metadata{CWD: t.TempDir()}}
	cwd := t.TempDir()
	var errOut bytes.Buffer

	rt, wf := buildChatRuntime(context.Background(), cfg, &llmtest.FakeProvider{}, "local", "qwen3-coder",
		sess, profile.Profile{}, cwd, cwd, stdio{out: &bytes.Buffer{}, err: &errOut})

	if wf == nil {
		t.Fatal("workflow events channel is nil")
	}
	if rt.runtime == nil || rt.runtime.ag == nil {
		t.Fatal("runtime or agent is nil")
	}
	if rt.runtime.providerName != "local" || rt.runtime.model != "qwen3-coder" {
		t.Fatalf("runtime backend = %s/%s", rt.runtime.providerName, rt.runtime.model)
	}
	if got := rt.runtime.providerModels["local"]; got != "qwen3-coder" {
		t.Fatalf("providerModels[local] = %q", got)
	}
	if rt.runtime.cwd != cwd || rt.runtime.root != cwd {
		t.Fatalf("runtime paths = %q/%q", rt.runtime.cwd, rt.runtime.root)
	}
	// workflow, goal, plus the agent tool the readable working directory earns.
	if len(rt.runtime.extra) != 3 {
		t.Fatalf("extra tools = %d, want 3", len(rt.runtime.extra))
	}
	if rt.runtime.runner == nil || !rt.runtime.runnerFollowsCoordinator {
		t.Fatal("a readable cwd with no configured worker backend still yields a runner that follows")
	}
	if len(rt.sk) == 0 {
		t.Fatal("skills were not loaded")
	}
	if rt.runtime.goal == nil {
		t.Fatal("goal manager is nil")
	}
}

func TestBuildChatRuntimeSkipsTheAgentToolWithoutAWorkingDirectory(t *testing.T) {
	cfg := keylessConfig()
	sess := &session.Session{}
	var out, errOut bytes.Buffer

	rt, _ := buildChatRuntime(context.Background(), cfg, &llmtest.FakeProvider{}, "local", "qwen3-coder",
		sess, profile.Profile{}, "", "", stdio{out: &out, err: &errOut})

	if len(rt.runtime.extra) != 2 {
		t.Fatalf("extra tools = %d, want workflow and goal only", len(rt.runtime.extra))
	}
	if rt.runtime.runner != nil {
		t.Fatal("no working directory must not produce a subagent runner")
	}
}

func TestBuildChatRuntimeWarnsAboutAnUnreadableSavedGoal(t *testing.T) {
	cfg := keylessConfig()
	// A saved goal without an objective is rejected, and an unreadable goal must
	// not make an otherwise valid session unopenable.
	sess := &session.Session{Goal: &goal.State{ID: "g1", Status: goal.Active}}
	var out, errOut bytes.Buffer

	rt, _ := buildChatRuntime(context.Background(), cfg, &llmtest.FakeProvider{}, "local", "qwen3-coder",
		sess, profile.Profile{}, "", "", stdio{out: &out, err: &errOut})

	if !strings.Contains(errOut.String(), "ignoring the saved goal") {
		t.Fatalf("error output = %q, want a dropped-goal warning", errOut.String())
	}
	if rt.runtime.goal == nil {
		t.Fatal("a rejected goal must still leave an empty manager")
	}
}

func TestBuildChatRuntimeInstallsTheApprovalMatcher(t *testing.T) {
	cfg := keylessConfig()
	// bash only exists on the classic surface, and a prefix rule is what this
	// test is about.
	cfg.Features.CodeMode = config.CodeModeClassic
	cwd := t.TempDir()
	cfg.ToolApprovals = []string{"bash"}
	var out, errOut bytes.Buffer

	rt, _ := buildChatRuntime(context.Background(), cfg, &llmtest.FakeProvider{}, "local", "qwen3-coder",
		&session.Session{}, profile.Profile{}, cwd, cwd, stdio{out: &out, err: &errOut})

	// The runtime installs the matcher and keeps the agent's approver unset, so
	// a listed tool asks instead of running: no approver means no approval.
	denied, isErr := rt.runtime.ag.RunTool(context.Background(), "bash", map[string]any{"command": "true"})
	if !isErr {
		t.Fatal("a tool listed in tool_approvals ran without an approver")
	}
	if !strings.Contains(denied, "no approver is configured") {
		t.Fatalf("refusal = %q, want the missing-approver explanation", denied)
	}

	// An unlisted tool still runs, which is what proves the matcher is wired to
	// the list rather than to everything.
	cfg.ToolApprovals = nil
	rt, _ = buildChatRuntime(context.Background(), cfg, &llmtest.FakeProvider{}, "local", "qwen3-coder",
		&session.Session{}, profile.Profile{}, cwd, cwd, stdio{out: &out, err: &errOut})
	if _, isErr := rt.runtime.ag.RunTool(context.Background(), "bash", map[string]any{"command": "true"}); isErr {
		t.Fatal("a tool absent from tool_approvals must run without asking")
	}
}

func TestChatExtraToolsRegistersTheAgentToolOnlyWithACWD(t *testing.T) {
	cfg := keylessConfig()
	mgr, warning := chatGoalManager(nil, cfg)
	if warning != "" {
		t.Fatalf("goal manager warning: %s", warning)
	}
	cwd := t.TempDir()

	wf, extra, runner, follows := chatExtraTools(context.Background(), cfg, &llmtest.FakeProvider{}, "qwen3-coder", cwd, cwd, mgr)
	if wf == nil || len(extra) != 3 || runner == nil || !follows {
		t.Fatalf("with cwd: wf=%v extra=%d runner=%v follows=%v", wf != nil, len(extra), runner != nil, follows)
	}

	wf, extra, runner, follows = chatExtraTools(context.Background(), cfg, &llmtest.FakeProvider{}, "qwen3-coder", "", "", mgr)
	if wf == nil || len(extra) != 2 || runner != nil || follows {
		t.Fatalf("without cwd: wf=%v extra=%d runner=%v follows=%v", wf != nil, len(extra), runner != nil, follows)
	}
}

// TestChatExtraToolsKeepsAConfiguredWorkerBackendIndependent covers the branch
// where subagents are pinned to another provider: the runner must then not
// follow the coordinator, and an unavailable worker must not abort the chat.
func TestChatExtraToolsKeepsAConfiguredWorkerBackendIndependent(t *testing.T) {
	cfg := keylessConfig()
	cfg.Subagents = config.Backend{Provider: "missing", Model: "worker-model"}
	mgr, _ := chatGoalManager(nil, cfg)
	cwd := t.TempDir()

	_, extra, runner, follows := chatExtraTools(context.Background(), cfg, &llmtest.FakeProvider{}, "qwen3-coder", cwd, cwd, mgr)
	if len(extra) != 3 || runner == nil {
		t.Fatalf("extra=%d runner=%v", len(extra), runner != nil)
	}
	if follows {
		t.Fatal("a pinned worker backend must not follow the coordinator")
	}
	if runner.Provider.Name() != "missing" {
		t.Fatalf("runner provider = %q, want the unavailable stand-in named for the pin", runner.Provider.Name())
	}
}

func TestResolveChatProviderUsesTheConfiguredBackend(t *testing.T) {
	cfg := keylessConfig()
	var errOut bytes.Buffer

	name, model, prov, ok := resolveChatProvider(context.Background(), cfg, nil, stdio{out: &bytes.Buffer{}, err: &errOut})
	if !ok {
		t.Fatalf("resolve failed: %s", errOut.String())
	}
	if name != "local" || model != "qwen3-coder" || prov == nil {
		t.Fatalf("resolved %s/%s prov=%v", name, model, prov != nil)
	}
}

// TestResolveChatProviderFallsBackWhenTheSavedBackendCannotBeBuilt covers the
// warning path: the session remembers a provider whose credential has gone
// missing, so the chat continues on the configured default instead of failing.
func TestResolveChatProviderFallsBackWhenTheSavedBackendCannotBeBuilt(t *testing.T) {
	tempHome(t)
	cfg := keylessConfig()
	cfg.Provider = "other"
	cfg.Model = "other-model"
	// A declared custom provider whose key variable is unset fails to build,
	// which is exactly what a stale session looks like.
	cfg.Providers["stale"] = config.CustomProvider{
		Endpoint:     "https://stale.internal/v1",
		APIKeyEnv:    "NEO_TEST_STALE_KEY_UNSET",
		DefaultModel: "stale-model",
	}
	t.Setenv("NEO_TEST_STALE_KEY_UNSET", "")
	sess := &session.Session{Metadata: session.Metadata{Provider: "stale", Model: "stale-model"}}
	var errOut bytes.Buffer

	name, model, prov, ok := resolveChatProvider(context.Background(), cfg, sess, stdio{out: &bytes.Buffer{}, err: &errOut})
	if !ok {
		t.Fatalf("resolve failed: %s", errOut.String())
	}
	if name != "other" || model != "other-model" {
		t.Fatalf("resolved %s/%s, want the configured other/other-model", name, model)
	}
	if prov == nil || !strings.Contains(errOut.String(), "cannot resume with stale") {
		t.Fatalf("expected a resume warning, got %q", errOut.String())
	}
}

func TestResolveChatProviderFailsWhenTheFallbackCannotBeBuiltEither(t *testing.T) {
	tempHome(t)
	cfg := keylessConfig()
	// The configured default needs an OpenAI subscription login that does not
	// exist in this HOME, so neither the saved backend nor the fallback builds.
	cfg.Provider = "openai"
	cfg.Model = "gpt-6.1-sol"
	cfg.OpenAIAuth = config.OpenAIAuthSubscription
	cfg.Providers["stale"] = config.CustomProvider{
		Endpoint:     "https://stale.internal/v1",
		APIKeyEnv:    "NEO_TEST_STALE_KEY_UNSET_2",
		DefaultModel: "stale-model",
	}
	t.Setenv("NEO_TEST_STALE_KEY_UNSET_2", "")
	sess := &session.Session{Metadata: session.Metadata{Provider: "stale", Model: "stale-model"}}
	var errOut bytes.Buffer

	if _, _, _, ok := resolveChatProvider(context.Background(), cfg, sess, stdio{out: &bytes.Buffer{}, err: &errOut}); ok {
		t.Fatal("resolve must fail when the configured provider cannot be built")
	}
	if !strings.Contains(errOut.String(), "not logged in") {
		t.Fatalf("error output = %q, want the login guidance", errOut.String())
	}
}

func TestResolveChatProviderFailsWhenTheSessionBackendIsTheDefaultOne(t *testing.T) {
	tempHome(t)
	cfg := keylessConfig()
	cfg.Provider = "openai"
	cfg.Model = "gpt-6.1-sol"
	cfg.OpenAIAuth = config.OpenAIAuthSubscription
	// The session was recorded on the same provider now configured, so there is
	// no other backend to fall back to.
	sess := &session.Session{Metadata: session.Metadata{Provider: "openai", Model: "gpt-6.1-sol"}}
	var errOut bytes.Buffer

	if _, _, _, ok := resolveChatProvider(context.Background(), cfg, sess, stdio{out: &bytes.Buffer{}, err: &errOut}); ok {
		t.Fatal("resolve must fail when the only candidate backend cannot be built")
	}
	if !strings.Contains(errOut.String(), "not logged in") {
		t.Fatalf("error output = %q", errOut.String())
	}
}

func TestEnsureChatSessionReusesTheGivenSession(t *testing.T) {
	sess := &session.Session{Metadata: session.Metadata{ID: "kept"}}
	got, ok := ensureChatSession(context.Background(), session.NewStore(t.TempDir()), sess, chatSessionMeta{}, stdio{})
	if !ok || got != sess {
		t.Fatalf("ensureChatSession did not return the caller's session")
	}
}

func TestEnsureChatSessionCreatesOneAndRecordsItsMetadata(t *testing.T) {
	store := session.NewStore(t.TempDir())
	meta := chatSessionMeta{cwd: "/work", agent: "reviewer", model: "qwen3-coder", providerID: "local", openAIAuth: "api_key"}

	got, ok := ensureChatSession(context.Background(), store, nil, meta, stdio{})
	if !ok {
		t.Fatal("ensureChatSession failed")
	}
	if got.Metadata.ID == "" || got.Metadata.CWD != "/work" || got.Metadata.Agent != "reviewer" ||
		got.Metadata.Model != "qwen3-coder" || got.Metadata.Provider != "local" || got.Metadata.OpenAIAuth != "api_key" {
		t.Fatalf("created session metadata = %+v", got.Metadata)
	}
	if _, err := os.Stat(filepath.Join(store.Dir(), got.Metadata.ID+".json")); err != nil {
		t.Fatalf("session file was not written: %v", err)
	}
}

func TestEnsureChatSessionReportsACreateFailure(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer

	if _, ok := ensureChatSession(context.Background(), session.NewStore(filepath.Join(blocked, "sessions")), nil,
		chatSessionMeta{}, stdio{out: &bytes.Buffer{}, err: &errOut}); ok {
		t.Fatal("ensureChatSession must fail when the store cannot be written")
	}
	if !strings.Contains(errOut.String(), "create session") {
		t.Fatalf("error output = %q", errOut.String())
	}
}

func TestSwitchCodeModeNeedsAnAgent(t *testing.T) {
	rt := &chatRuntime{cfg: keylessConfig()}
	if err := rt.switchCodeMode(true); err == nil {
		t.Fatal("switchCodeMode must refuse without an agent")
	}
}

func TestSwitchCodeModeIsANoopWhenTheSettingAlreadyMatches(t *testing.T) {
	cfg := keylessConfig()
	off := false
	cfg.Features.Script = &off
	rt := chatRuntimeFor(cfg)

	if err := rt.switchCodeMode(false); err != nil {
		t.Fatalf("switchCodeMode: %v", err)
	}
	if scriptAvailable(t, rt.ag) {
		t.Fatal("script must stay unregistered when the setting did not change")
	}
}

// TestSwitchCodeModeRegistersTheToolPromptAndSetting covers the success path:
// the registry, the prompt, and the persisted file must all agree, and the
// transcript is untouched because the agent is the same object.
func TestSwitchCodeModeRegistersTheToolPromptAndSetting(t *testing.T) {
	home := tempHome(t)
	cfg := keylessConfig()
	rt := chatRuntimeFor(cfg)
	// Code execution is the default surface, so the switch starts on.
	if !scriptAvailable(t, rt.ag) {
		t.Fatal("script must start registered by default")
	}

	if err := rt.switchCodeMode(false); err != nil {
		t.Fatalf("switchCodeMode(off): %v", err)
	}
	if rt.cfg.ScriptEnabled() {
		t.Fatal("the running config must record the switch")
	}
	if scriptAvailable(t, rt.ag) {
		t.Fatal("the script tool is still registered after turning code mode off")
	}
	b, err := os.ReadFile(filepath.Join(home, ".neo", "config.yaml"))
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	if !strings.Contains(string(b), "script: false") {
		t.Fatalf("persisted config does not record the feature:\n%s", b)
	}

	// Turning it back on must restore the tool on the same agent.
	if err := rt.switchCodeMode(true); err != nil {
		t.Fatalf("switchCodeMode(on): %v", err)
	}
	if !scriptAvailable(t, rt.ag) {
		t.Fatal("the script tool was not restored")
	}
}

// TestSwitchCodeModeRestoresEverythingWhenTheWriteFails is the reason the
// switch exists in two halves: a session that cannot persist the choice must
// not claim it, or the next start would disagree with what just ran.
func TestSwitchCodeModeRestoresEverythingWhenTheWriteFails(t *testing.T) {
	cfg := keylessConfig()
	rt := chatRuntimeFor(cfg)
	blockedHome(t)

	if err := rt.switchCodeMode(false); err == nil {
		t.Fatal("expected the config write to fail")
	}
	if !rt.cfg.ScriptEnabled() {
		t.Fatal("the failed write left the running config claiming code mode is off")
	}
	if !scriptAvailable(t, rt.ag) {
		t.Fatal("the failed write removed the script tool")
	}
}

func TestSwitchModelMovesTheChatAndPersistsTheChoice(t *testing.T) {
	home := tempHome(t)
	cfg := keylessConfig()
	rt := chatRuntimeFor(cfg)

	if err := rt.switchModel("qwen3-coder-small"); err != nil {
		t.Fatalf("switchModel: %v", err)
	}
	if _, model := rt.ag.Backend(); model != "qwen3-coder-small" {
		t.Fatalf("agent model = %q", model)
	}
	if rt.model != "qwen3-coder-small" || rt.cfg.Model != "qwen3-coder-small" {
		t.Fatalf("runtime model = %q, config model = %q", rt.model, rt.cfg.Model)
	}
	if got := rt.providerModels["local"]; got != "qwen3-coder-small" {
		t.Fatalf("providerModels[local] = %q", got)
	}
	b, err := os.ReadFile(filepath.Join(home, ".neo", "config.yaml"))
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	if !strings.Contains(string(b), "model: qwen3-coder-small") {
		t.Fatalf("persisted config does not record the model:\n%s", b)
	}
}

func TestSwitchModelRejectsAnEmptyModel(t *testing.T) {
	tempHome(t)
	cfg := keylessConfig()
	rt := chatRuntimeFor(cfg)
	if err := rt.switchModel(""); err == nil {
		t.Fatal("switchModel must reject an empty model")
	}
	if _, model := rt.ag.Backend(); model != "qwen3-coder" {
		t.Fatalf("a rejected switch changed the model to %q", model)
	}
}

// TestSwitchModelReadsThePreviousModelFromTheAgentWhenUntracked covers the
// rollback for a runtime whose providerModels map was never filled: the model
// to restore has to come from the running agent instead.
func TestSwitchModelRestoresTheAgentModelWhenTheWriteFails(t *testing.T) {
	tempHome(t)
	cfg := keylessConfig()
	rt := chatRuntimeFor(cfg)
	rt.providerModels = map[string]string{}
	blockedHome(t)

	if err := rt.switchModel("qwen3-coder-small"); err == nil {
		t.Fatal("expected the config write to fail")
	}
	if _, model := rt.ag.Backend(); model != "qwen3-coder" {
		t.Fatalf("agent model = %q after a failed switch, want the previous one", model)
	}
	if rt.model != "qwen3-coder" {
		t.Fatalf("runtime model = %q after a failed switch", rt.model)
	}
}

func TestSwitchProviderRejectsAnUnknownName(t *testing.T) {
	cfg := keylessConfig()
	rt := chatRuntimeFor(cfg)
	if _, err := rt.switchProvider(context.Background(), "nope"); err == nil {
		t.Fatal("switchProvider must reject a name that is neither built in nor declared")
	}
}

func TestSwitchProviderRejectsAProviderWithNoDefaultModel(t *testing.T) {
	cfg := keylessConfig()
	cfg.Providers["bare"] = config.CustomProvider{Endpoint: "https://bare.internal/v1"}
	rt := chatRuntimeFor(cfg)
	if _, err := rt.switchProvider(context.Background(), "bare"); err == nil {
		t.Fatal("switchProvider must ask for a model when the provider declares none")
	}
	if rt.providerName != "local" {
		t.Fatalf("a rejected switch changed the provider to %q", rt.providerName)
	}
}

func TestSwitchProviderReportsAMissingCredentialBeforeChangingAnything(t *testing.T) {
	cfg := keylessConfig()
	cfg.Providers["gated"] = config.CustomProvider{
		Endpoint:     "https://gated.internal/v1",
		APIKeyEnv:    "NEO_TEST_GATED_KEY_UNSET",
		DefaultModel: "gated-model",
	}
	t.Setenv("NEO_TEST_GATED_KEY_UNSET", "")
	rt := chatRuntimeFor(cfg)

	if _, err := rt.switchProvider(context.Background(), "gated"); err == nil {
		t.Fatal("switchProvider must fail when the credential is missing")
	}
	if rt.providerName != "local" || rt.model != "qwen3-coder" {
		t.Fatalf("a failed switch changed the backend to %s/%s", rt.providerName, rt.model)
	}
}

func TestSwitchProviderMovesTheChatAndPersistsTheChoice(t *testing.T) {
	home := tempHome(t)
	cfg := keylessConfig()
	rt := chatRuntimeFor(cfg)

	state, err := rt.switchProvider(context.Background(), "other")
	if err != nil {
		t.Fatalf("switchProvider: %v", err)
	}
	if state.Provider != "other" || state.Model != "other-model" {
		t.Fatalf("state = %s/%s", state.Provider, state.Model)
	}
	if rt.providerName != "other" || rt.model != "other-model" || rt.cfg.Provider != "other" || rt.cfg.Model != "other-model" {
		t.Fatalf("runtime = %s/%s, config = %s/%s", rt.providerName, rt.model, rt.cfg.Provider, rt.cfg.Model)
	}
	if got := rt.providerModels["other"]; got != "other-model" {
		t.Fatalf("providerModels[other] = %q", got)
	}
	if provider, _ := rt.ag.Backend(); provider != "other" {
		t.Fatalf("agent provider = %q", provider)
	}
	b, err := os.ReadFile(filepath.Join(home, ".neo", "config.yaml"))
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	if !strings.Contains(string(b), "provider: other") || !strings.Contains(string(b), "model: other-model") {
		t.Fatalf("persisted config does not record the switch:\n%s", b)
	}
}

// TestSwitchProviderRestoresTheBackendWhenTheWriteFails checks the second half
// of the two-phase switch: the backend is applied first so it can be validated,
// so a failed persist has to put the previous one back.
func TestSwitchProviderRestoresTheBackendWhenTheWriteFails(t *testing.T) {
	tempHome(t)
	cfg := keylessConfig()
	rt := chatRuntimeFor(cfg)
	blockedHome(t)

	if _, err := rt.switchProvider(context.Background(), "other"); err == nil {
		t.Fatal("expected the config write to fail")
	}
	if rt.providerName != "local" || rt.model != "qwen3-coder" {
		t.Fatalf("runtime = %s/%s after a failed switch", rt.providerName, rt.model)
	}
	if _, model := rt.ag.Backend(); model != "qwen3-coder" {
		t.Fatalf("agent model = %q after a failed switch", model)
	}
}

// TestSwitchProviderFallsBackToTheRunningModelForTheRollback covers a runtime
// that never recorded the current provider's model: the model to restore then
// has to come from the runtime itself.
func TestSwitchProviderFallsBackToTheRunningModelForTheRollback(t *testing.T) {
	tempHome(t)
	cfg := keylessConfig()
	rt := chatRuntimeFor(cfg)
	rt.providerModels = map[string]string{}
	blockedHome(t)

	if _, err := rt.switchProvider(context.Background(), "other"); err == nil {
		t.Fatal("expected the config write to fail")
	}
	if _, model := rt.ag.Backend(); model != "qwen3-coder" {
		t.Fatalf("agent model = %q after a failed switch", model)
	}
}

func TestRunLoginReportsAMissingHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	var out, errOut bytes.Buffer
	if code := runLogin(context.Background(), stdio{out: &out, err: &errOut}); code != 1 {
		t.Fatalf("runLogin exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "login:") {
		t.Fatalf("error output = %q", errOut.String())
	}
}
