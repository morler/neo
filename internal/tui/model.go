package tui

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/owainlewis/neo/internal/agent"
	"github.com/owainlewis/neo/internal/goal"
	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/logx"
	"github.com/owainlewis/neo/internal/skills"
	"github.com/owainlewis/neo/internal/subagent"
	"github.com/owainlewis/neo/internal/tools"
	"github.com/owainlewis/neo/internal/workflow"
)

type Options struct {
	AfterSend          func() error
	GoalManager        *goal.Manager
	GoalPersist        func() error
	ModelChoices       []ModelChoice
	ModelChoicesLoader func(context.Context) ([]ModelChoice, error)
	Provider           string
	ModelSwitcher      func(string) error
	ProviderChoices    []ProviderChoice
	ProviderSwitcher   ProviderSwitcher
	// ScriptSwitcher turns the JavaScript `script` tool on or off for this
	// session and persists the choice. The TUI never builds a registry: it asks
	// the CLI boundary, which owns tool composition and the config write.
	ScriptSwitcher ScriptSwitcher
	// ScriptEnabled reports the current state for `/codemode status`.
	ScriptEnabled func() bool
	// EffortSwitcher changes the reasoning effort for this session and persists
	// it. The TUI does not write config itself; a failure leaves the previous
	// setting in place and is shown to the user.
	EffortSwitcher func(string) error
	// Effort reports the effort currently in effect, for bare `/effort`.
	Effort         func() string
	SessionID      string
	SessionLister  func(context.Context) ([]SessionChoice, error)
	SessionOpener  func(string) error
	WorkflowEvents <-chan workflow.Event
	// SubagentEvents carries the supervisor's stream for chat-spawned
	// children. The TUI attaches it to the live workflow item instead of
	// rendering a row per event: a delegated child is one step of the plan,
	// and its progress is what that step is doing.
	SubagentEvents <-chan subagent.Event
	Verbose        bool
	Input          io.Reader
	Output         io.Writer
}

// ScriptSwitcher enables or disables the script tool and reports the state it
// ended in. A failure leaves the previous state in place and is shown to the
// user, so a write error cannot look like a successful switch.
type ScriptSwitcher func(on bool) error

type Option func(*Options)

func WithAfterSend(fn func() error) Option {
	return func(opts *Options) { opts.AfterSend = fn }
}

// WithScriptSwitcher enables /codemode. The callback owns both the config write
// and the agent's tool/prompt swap, because the TUI has neither.
func WithScriptSwitcher(switcher ScriptSwitcher, current func() bool) Option {
	return func(opts *Options) {
		opts.ScriptSwitcher = switcher
		opts.ScriptEnabled = current
	}
}

// WithEffortSwitcher enables /effort. fn applies the effort to the running
// agent and persists it; current reports what is in effect. Both are optional
// in the sense that a nil fn disables the command — a session built without
// them says so instead of pretending the switch worked.
func WithEffortSwitcher(fn func(string) error, current func() string) Option {
	return func(opts *Options) {
		opts.EffortSwitcher = fn
		opts.Effort = current
	}
}

// WithGoalManager gives the TUI the session's goal manager. The TUI never
// mutates goal state directly: it asks this manager, which serializes
// transitions and enforces the ownership rules.
func WithGoalManager(mgr *goal.Manager) Option {
	return func(opts *Options) { opts.GoalManager = mgr }
}

// WithGoalPersist supplies the save used after a goal transition that happens
// inside the TUI. It runs synchronously before the first turn of a new goal is
// sent, so a goal always reaches disk before the model sees it. A nil callback
// means the TUI cannot save goals on its own; callers that enable goal commands
// must provide one.
func WithGoalPersist(fn func() error) Option {
	return func(opts *Options) { opts.GoalPersist = fn }
}

// WithModelSwitcher enables model selection for the active provider. The
// callback must finish the model switch before returning.
func WithModelSwitcher(provider string, choices []ModelChoice, fn func(string) error) Option {
	return func(opts *Options) {
		opts.Provider = provider
		opts.ModelChoices = choices
		opts.ModelSwitcher = fn
	}
}

// WithProviderSwitcher enables /provider. choices lists every backend the
// switcher accepts; fn builds the backend, persists the choice, and returns the
// resulting model and catalogue. An error leaves the running backend as it was.
func WithProviderSwitcher(choices []ProviderChoice, fn ProviderSwitcher) Option {
	return func(opts *Options) {
		opts.ProviderChoices = choices
		opts.ProviderSwitcher = fn
	}
}

// WithSessions enables /sessions: id names the running session, list returns
// the saved ones, and open checks that a chosen session can still be loaded.
//
// The TUI does not switch sessions itself. Choosing one ends this program and
// Run returns the target ID, so the caller resumes it exactly like `neo
// resume <id>`. That keeps every runtime handle (agent, goal manager, backend,
// cwd, skills) built in one place instead of being swapped in a half-applied
// state. open runs before the switch so a missing or unreadable session is
// reported in the open browser rather than after the terminal is restored.
func WithSessions(id string, list func(context.Context) ([]SessionChoice, error), open func(string) error) Option {
	return func(opts *Options) {
		opts.SessionID = id
		opts.SessionLister = list
		opts.SessionOpener = open
	}
}

// WithModelChoicesLoader supplies the live model catalogue for providers that
// do not ship a static list. The loader runs only when the model browser is
// opened, so a slow catalogue fetch never delays the first frame.
func WithModelChoicesLoader(loader func(context.Context) ([]ModelChoice, error)) Option {
	return func(opts *Options) { opts.ModelChoicesLoader = loader }
}

func WithWorkflowEvents(ch <-chan workflow.Event) Option {
	return func(opts *Options) { opts.WorkflowEvents = ch }
}

// WithSubagentEvents supplies the supervisor's child-agent stream. Events are
// correlated to the parent agent call by CallID, so a child's activity lands on
// the workflow item the coordinator is currently working through.
func WithSubagentEvents(ch <-chan subagent.Event) Option {
	return func(opts *Options) { opts.SubagentEvents = ch }
}

// WithVerbose controls tool activity rendering: false (the default) shows live
// activity and concise completed receipts while hiding successful tool result
// bodies; true restores full tool call/result cards. Errors always render in
// full regardless of this setting.
func WithVerbose(verbose bool) Option {
	return func(opts *Options) { opts.Verbose = verbose }
}

// WithIO configures the terminal streams used by Bubble Tea. When omitted,
// Run preserves the standard stdin/stdout behavior.
func WithIO(input io.Reader, output io.Writer) Option {
	return func(opts *Options) {
		opts.Input = input
		opts.Output = output
	}
}

// Run starts the Bubble Tea chat TUI. It returns when the user quits. cwd is
// the effective working directory for display and file references. sk is the
// loaded skill set used for $name expansion (nil when the feature is off).
//
// The returned ID is the session the user asked to switch to with /sessions,
// or "" when the program ended normally. The caller decides what resuming it
// means, because this package does not open sessions itself.
func Run(ctx context.Context, ag *agent.Agent, modelTag, version, cwd string, sk []skills.Skill, options ...Option) (string, error) {
	logx.Debug("tui start", "model", modelTag, "skills", len(sk))
	opts := Options{}
	for _, option := range options {
		option(&opts)
	}
	m := newModel(ctx, ag, modelTag, version, cwd, sk, opts)
	// AltScreen + MouseMode are properties of the View in v2 (see View()).
	p := tea.NewProgram(m, programOptions(opts)...)
	// Pipe agent events directly into the Bubble Tea program. This avoids a
	// hand-rolled channel pump and the back-pressure that came with it.
	ag.SetEventHandler(func(e agent.Event) { p.Send(agentEventMsg{ev: e}) })
	if opts.WorkflowEvents != nil {
		go func() {
			for ev := range opts.WorkflowEvents {
				p.Send(workflowEventMsg{ev: ev})
			}
		}()
	}
	if opts.SubagentEvents != nil {
		go func() {
			for ev := range opts.SubagentEvents {
				p.Send(subagentEventMsg{ev: ev})
			}
		}()
	}
	ag.SetApprover(func(ctx context.Context, req agent.ApprovalRequest) (bool, error) {
		reply := make(chan bool, 1)
		p.Send(approvalRequestMsg{req: req, reply: reply})
		select {
		case ok := <-reply:
			return ok, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	})
	final, err := p.Run()
	if err != nil {
		logx.Debug("tui exit", "error", err.Error())
		return "", err
	}
	logx.Debug("tui exit", "error", "")
	// A session switch is reported through the final model, not the program
	// result: quitting this way is an ordinary exit to Bubble Tea.
	if fm, ok := final.(*model); ok {
		return fm.resumeSessionID, nil
	}
	return "", nil
}

func programOptions(opts Options) []tea.ProgramOption {
	var options []tea.ProgramOption
	// Let Bubble Tea resolve the controlling terminal when the process stdin is
	// redirected. Explicit input is reserved for genuinely injected readers.
	input, isFile := opts.Input.(*os.File)
	if opts.Input != nil && (!isFile || input != os.Stdin) {
		options = append(options, tea.WithInput(opts.Input))
	}
	if opts.Output != nil {
		options = append(options, tea.WithOutput(opts.Output))
	}
	return options
}

type sendResultMsg struct {
	err           error
	saveErr       error
	saveAttempted bool
}
type persistenceRetryResultMsg struct{ err error }
type agentEventMsg struct{ ev agent.Event }
type workflowEventMsg struct{ ev workflow.Event }
type subagentEventMsg struct{ ev subagent.Event }
type branchMsg struct{ branch string }
type modelChoicesMsg struct {
	choices []ModelChoice
	err     error
}
type approvalRequestMsg struct {
	req   agent.ApprovalRequest
	reply chan bool
}

type approvalState struct {
	req      agent.ApprovalRequest
	reply    chan bool
	selected rune
}

type turnStats struct {
	tools    int
	errors   int
	workflow bool
	direct   bool
	// label names the slash-invoked skill driving this turn, if any.
	label string
}

type queuedTurn struct {
	displayText string
	agentText   string
	images      []string
}

type model struct {
	ctx         context.Context
	ag          *agent.Agent
	modelTag    string
	providerTag string

	cwd    string
	branch string

	width, height int

	viewport  viewport.Model
	input     textarea.Model
	spin      spinner.Model
	picker    commandPicker
	files     filePicker
	models    modelBrowser
	providers providerBrowser

	// scriptSwitcher and scriptEnabled back /codemode. They are nil when the
	// caller did not offer the switch, and the command says so rather than
	// pretending to work.
	scriptSwitcher ScriptSwitcher
	scriptEnabled  func() bool
	// effortSwitcher and effort back /effort. Nil when the caller offered no
	// switch, in which case the command reports that rather than no-opping.
	effortSwitcher func(string) error
	effort         func() string
	// scriptCalls counts the top-level `script` calls this session asked for,
	// so /codemode status can weigh the prompt cost against its use.
	scriptCalls int
	sessions    sessionBrowser

	// lastInputHeight is the textarea height the current layout was computed
	// for. When the textarea grows/shrinks (DynamicHeight), this lets us
	// detect the change and re-layout the viewport around it.
	lastInputHeight int

	blocks []block
	md     *glamour.TermRenderer
	// mdWidth is the wrap width m.md was built for, so layout only rebuilds
	// the renderer (style parse plus chroma setup) when the width changes.
	mdWidth int
	// rendered caches each block's output at renderedWidth, index-aligned with
	// blocks. Rendering a block means a glamour pass for markdown, so redoing
	// the whole transcript on every event made each event cost O(transcript).
	// Entries are dropped when the width changes, when blocks are reset, and
	// from the first mutated index onward; pointer blocks that mutate in place
	// are never cached.
	rendered      []string
	renderedWidth int

	busy      bool
	busySince time.Time
	spinning  bool // a spinner tick is scheduled
	// inflight holds the tool calls the agent has started and not yet
	// finished, keyed by tool-use ID. Serial calls hold one entry; a parallel
	// group holds several. The status line reads it; receipts are appended
	// as each result arrives.
	inflight map[string]*toolCallBlock
	// subCalls maps a script sub-call's ID to the index of its row in blocks,
	// and subScripts maps a script's tool-use ID to everything its sub-calls
	// produced, so the rows can be settled or summarized when it ends.
	subCalls   map[string]int
	subScripts map[string]*scriptRun
	// workflow is the checklist block for the current turn, if the workflow
	// tool created one. It lives in the transcript like any other block and
	// is updated in place until the next turn starts.
	workflow       *workflowBlock
	turn           turnStats
	approval       *approvalState
	quitting       bool
	quitPending    bool
	persistenceErr error

	// cancel for the currently in-flight Send, if any.
	sendCancel      context.CancelFunc
	steer           func(string) bool
	pendingSteering []string
	queued          *queuedTurn
	// conversationGeneration tags the work started by the current turn. It
	// advances on every send and on /clear, so buffered workflow events from
	// an earlier turn are recognised as stale and ignored.
	conversationGeneration uint64

	// mdStyleName is the glamour style currently in use. It starts at dark and
	// switches when Bubble Tea reports a light terminal background, so startup
	// never blocks on a terminal colour query.
	mdStyleName string

	// skills drives $name expansion of the user's input and /name skill
	// invocations before a turn is sent. A slash-invoked skill also labels the
	// turn in the status line and receipt.
	skills []skills.Skill

	afterSend           func() error
	goal                *goal.Manager
	goalPersist         func() error
	modelChoices        []ModelChoice
	modelChoicesLoader  func(context.Context) ([]ModelChoice, error)
	modelChoicesLoaded  bool
	modelChoicesLoading bool
	modelSwitcher       func(string) error
	providerChoices     []ProviderChoice
	providerSwitcher    ProviderSwitcher
	sessionID           string
	sessionLister       func(context.Context) ([]SessionChoice, error)
	sessionOpener       func(string) error
	// resumeSessionID is set when the user asked to switch sessions. Run
	// returns it after the program exits so the caller can resume it.
	resumeSessionID string
	verbose         bool
}

func newModel(ctx context.Context, ag *agent.Agent, modelTag, version, workingDir string, sk []skills.Skill, opts Options) *model {
	// Start dark and let Bubble Tea report the terminal background
	// asynchronously (see Init). Querying it here with lipgloss would block
	// the first frame for up to four seconds on terminals that do not answer
	// DA1.
	const styleName = "dark"
	absCWD := resolveWorkingDir(workingDir)
	branch := gitBranch()
	if version == "" {
		version = "dev"
	}
	m := &model{
		ctx:                ctx,
		ag:                 ag,
		modelTag:           modelTag,
		providerTag:        strings.TrimSpace(opts.Provider),
		mdStyleName:        styleName,
		md:                 newMarkdownRenderer(styleName),
		cwd:                displayCWD(absCWD),
		branch:             branch,
		viewport:           newTranscriptViewport(),
		input:              newComposer(),
		spin:               newStatusSpinner(),
		files:              newFilePicker(absCWD),
		skills:             sk,
		afterSend:          opts.AfterSend,
		goal:               opts.GoalManager,
		goalPersist:        opts.GoalPersist,
		modelChoices:       normalizeModelChoices(modelTag, opts.ModelChoices),
		modelChoicesLoader: opts.ModelChoicesLoader,
		modelSwitcher:      opts.ModelSwitcher,
		providerChoices:    normalizeProviderChoices(opts.ProviderChoices),
		providerSwitcher:   opts.ProviderSwitcher,
		scriptSwitcher:     opts.ScriptSwitcher,
		scriptEnabled:      opts.ScriptEnabled,
		effortSwitcher:     opts.EffortSwitcher,
		effort:             opts.Effort,
		sessionID:          strings.TrimSpace(opts.SessionID),
		sessionLister:      opts.SessionLister,
		sessionOpener:      opts.SessionOpener,
		verbose:            opts.Verbose,
		steer:              ag.Steer,
	}
	// Welcome banner shown once at the top of scrollback.
	m.blocks = append(m.blocks, splashBlock{version: version})
	m.appendTranscript(ag.Transcript())
	return m
}

// newMarkdownRenderer builds the transcript renderer. A failure degrades to no
// markdown rather than refusing to start: the composer must still work.
func newMarkdownRenderer(styleName string) *glamour.TermRenderer {
	md, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(styleName),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		return nil
	}
	return md
}

// newComposer builds the input box: it grows and shrinks with its content and
// shares the wrapping bar's background so the composer reads as one block.
func newComposer() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = defaultPlaceholder
	ta.Prompt = "› "
	ta.CharLimit = 0
	// Let the textarea grow and shrink to fit its content. DynamicHeight
	// accounts for soft-wrapped lines (a long unwrapped paragraph still
	// expands), which a manual LineCount() height never could. The box stays
	// between one row and inputMaxRows; past that it scrolls internally.
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = inputMaxRows
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	// Draw a real terminal cursor rather than a styled block: only a real one
	// tells the terminal (and therefore the input method) where the insertion
	// point is, which is what anchors a candidate window. See composerAnchor.
	ta.SetVirtualCursor(false)
	// Give the textarea the same solid background as its wrapping bar so the
	// composer reads as one continuous block (Codex-style), with no seams
	// between the textarea cells and the surrounding padding.
	taStyles := ta.Styles()
	for _, st := range []*textarea.StyleState{&taStyles.Focused, &taStyles.Blurred} {
		st.Base = st.Base.Background(colInputBg)
		st.Text = st.Text.Background(colInputBg)
		st.Prompt = st.Prompt.Background(colInputBg)
		st.Placeholder = st.Placeholder.Background(colInputBg)
		st.CursorLine = st.CursorLine.Background(colInputBg)
		st.EndOfBuffer = st.EndOfBuffer.Background(colInputBg)
	}
	ta.SetStyles(taStyles)
	ta.Focus()
	return ta
}

// newTranscriptViewport builds the scrollback pane. Horizontal scrolling stays
// off: the content is word-wrapped, and a horizontal trackpad swipe would
// otherwise slide the whole transcript.
//
// SoftWrap is deliberately off, and worth stating because it looks like the fix
// for a clipped wide line. It is not: every block already breaks its own text
// with lipgloss, which breaks CJK and long paths at the pane width, so the
// viewport never receives a row wider than the pane. Measured by turning it on
// and off, an unbreakable 92-column path in a 40-column pane reaches its tail
// either way. An over-wide row is a block's own width bug — the one thinkingBlock
// had — not the viewport's, and enabling soft wrap would hide such a bug behind
// the viewport instead of failing a test.
func newTranscriptViewport() viewport.Model {
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.MouseWheelEnabled = true
	vp.SetHorizontalStep(0)
	return vp
}

func newStatusSpinner() spinner.Model {
	sp := spinner.New()
	sp.Spinner = statusSpinner
	sp.Style = lipgloss.NewStyle().Foreground(colDotThinking)
	return sp
}

// resolveWorkingDir falls back to the process working directory, which is what
// relative tool paths are resolved against.
func resolveWorkingDir(workingDir string) string {
	if workingDir != "" {
		return workingDir
	}
	cwd, _ := os.Getwd()
	return cwd
}

// displayCWD shortens a path under the home directory to "~/…" for the status
// line. Anything outside home is shown in full.
func displayCWD(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return dir
	}
	rel, err := filepath.Rel(home, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return dir
	}
	return "~/" + rel
}

// Init asks Bubble Tea for the terminal background without blocking the first
// frame. The reply arrives as tea.BackgroundColorMsg; the spinner ticks only
// while a turn is running (see the end of Update).
func (m *model) Init() tea.Cmd {
	return tea.RequestBackgroundColor
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	wasBusy := m.busy
	res := m.handleMsg(msg)
	if res.early {
		if res.cmd != nil {
			return m, res.cmd
		}
		return m, nil
	}

	// The spinner drives elapsed-time displays, so it runs only while a turn
	// is active: it starts when a message makes the model busy and a tick
	// that arrives after the turn ends is not rescheduled.
	if m.busy && !wasBusy && !m.spinning {
		m.spinning = true
		res.cmds = append(res.cmds, m.spin.Tick)
	}
	return m, tea.Batch(res.cmds...)
}

func (m *model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	if m.width == 0 {
		return makeView("loading…")
	}
	if m.providers.visible {
		return makeView(m.providerBrowserView())
	}
	if m.models.visible {
		return makeView(m.modelBrowserView())
	}
	if m.sessions.visible {
		return makeView(m.sessionBrowserView())
	}
	status := m.statusLine()
	footer := m.footerLine()
	picker := m.inlinePickerView()
	// While an approval is pending the input is inert (all keys route to the
	// y/a/n handler), so the composer is replaced by a focused action bar.
	bottom := styInputBar.Width(m.width).Render(m.input.View())
	if m.approval != nil {
		bottom = m.approvalBarView()
	}

	// Everything above the composer, in the order JoinVertical stacks it. Its
	// height is what offsets the composer's cursor into frame coordinates.
	above := []string{m.viewport.View(), "", status, ""}
	parts := append(append([]string{}, above...), bottom)
	if picker != "" {
		parts = append(parts, picker)
	}
	parts = append(parts, "", footer)

	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	view := makeView(content)
	// The terminal cursor has to sit at the composer's insertion point, or an
	// input method has nowhere to anchor its candidate window and falls back to
	// a screen corner. See composerAnchor.
	view.Cursor = m.composerAnchor(lipgloss.Height(strings.Join(above, "\n")))
	return view
}

// composerAnchor places the terminal cursor on the composer's insertion point.
//
// bubbletea v2 moved cursor positioning out of the program: the View declares
// where the cursor belongs and the framework moves the terminal there. A nil
// Cursor leaves it wherever the last frame finished drawing, which is the
// bottom-right corner of the screen -- and that is where fcitx5 and other input
// methods then put the candidate window, far from the text being typed.
//
// above is the rendered height of everything stacked over the composer, in rows.
//
// A nil result is deliberate in the states where the composer is not what the
// user is typing into: an approval bar replaces it, a browser or picker covers
// the screen, the width is not known yet, or the textarea is not focused (its
// Cursor is then nil). Pointing at the composer in those states would be worse
// than not pointing anywhere.
func (m *model) composerAnchor(above int) *tea.Cursor {
	if m.approval != nil || m.width == 0 || m.quitting {
		return nil
	}
	if m.providers.visible || m.models.visible || m.sessions.visible {
		return nil
	}
	cur := m.input.Cursor()
	if cur == nil {
		return nil
	}
	// The composer's own padding sits between the textarea's origin and the
	// block the terminal sees.
	cur.X += styInputBar.GetPaddingLeft()
	cur.Y += above + styInputBar.GetPaddingTop()
	return cur
}

// makeView wraps a rendered string with the v2 View settings we want for
// every frame: alt screen, mouse reporting and a request for keyboard
// enhancements. The alt screen hides the terminal's own scrollback, so the
// transcript is only reachable through the viewport: without mouse reporting
// the wheel does nothing and history is unreadable. MouseModeCellMotion
// delivers wheel events (plus clicks) to Update, where the viewport consumes
// them. Terminals still allow drag selection with shift held down.
// ReportAlternateKeys asks terminals that speak the Kitty
// keyboard protocol (Kitty, Ghostty, WezTerm, recent iTerm2) to disambiguate
// shift+enter from a bare enter, which is what lets shift+enter insert a
// newline there. On terminals without it, alt+enter / ctrl+j remain the
// portable fallbacks.
func makeView(content string) tea.View {
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.KeyboardEnhancements.ReportAlternateKeys = true
	return v
}

const defaultPlaceholder = "Describe the outcome…   ↩ send"

func (m *model) submitUserTurn(displayText, agentText string, images []string) tea.Cmd {
	return m.submitUserTurnWithSkillExpansion(displayText, agentText, images, true)
}

func (m *model) steerActiveTurn(displayText, agentText string) {
	sent, used := skills.Expand(agentText, m.skills)
	if m.steer == nil || !m.steer(sent) {
		m.appendBlock(errorBlock{err: fmt.Errorf("this operation cannot be steered; use ctrl+enter to queue a follow-up")})
		return
	}
	m.resetInput()
	m.appendBlock(userBlock{text: displayText})
	m.pendingSteering = append(m.pendingSteering, displayText)
	if len(used) > 0 {
		m.appendBlock(noticeBlock{text: "applied skill: " + strings.Join(used, ", ")})
	}
	m.appendBlock(noticeBlock{text: "steering current turn"})
}

func (m *model) queueFollowUp(displayText string) {
	if m.queued != nil {
		m.appendBlock(errorBlock{err: fmt.Errorf("a follow-up is already queued")})
		return
	}
	if strings.HasPrefix(displayText, "/") || strings.HasPrefix(displayText, "!") {
		m.appendBlock(errorBlock{err: fmt.Errorf("commands cannot be queued; wait for the turn to finish or use $skill in a chat message")})
		return
	}
	agentText, images := extractImagePaths(displayText)
	m.queued = &queuedTurn{displayText: displayText, agentText: agentText, images: images}
	m.resetInput()
	m.appendBlock(noticeBlock{text: "queued next: " + oneLine(displayText)})
}

func (m *model) restoreInput(texts ...string) {
	current := strings.TrimSpace(m.input.Value())
	if current != "" {
		texts = append(texts, current)
	}
	if len(texts) == 0 {
		return
	}
	m.input.SetValue(strings.Join(texts, "\n"))
	m.syncInputHeight()
	m.layout()
}

func (m *model) submitUserTurnWithSkillExpansion(displayText, agentText string, images []string, expandSkillRefs bool) tea.Cmd {
	// The previous turn's checklist stays in the transcript but stops being
	// the live one, so a new plan never inherits stale running items. Workflow
	// events travel on a buffered channel, so the generation advances per
	// turn: a late event from the previous turn is then ignored rather than
	// restored as the live plan.
	m.workflow = nil
	m.clearInflight()
	m.conversationGeneration++
	m.appendBlock(userBlock{text: displayText})
	if len(images) > 0 {
		m.appendBlock(noticeBlock{text: "attached image: " + strings.Join(shortPaths(images), ", ")})
	}
	sent := agentText
	if expandSkillRefs {
		var used []string
		sent, used = skills.Expand(agentText, m.skills)
		if len(used) > 0 {
			m.appendBlock(noticeBlock{text: "applied skill: " + strings.Join(used, ", ")})
		}
	}
	m.busy = true
	m.busySince = time.Now()
	m.turn = turnStats{}
	m.setDotColor(colDotThinking)
	return m.startSend(displayText, sent, images)
}

// handleSlashCommand parses slash commands. Called only when input begins
// with '/'.
func (m *model) handleSlashCommand(line string) tea.Cmd {
	parts := strings.Fields(line)
	cmd := parts[0]
	if m.busy && slashCommandRequiresIdle(cmd, strings.TrimSpace(strings.TrimPrefix(line, cmd))) {
		m.appendBlock(errorBlock{err: fmt.Errorf("%s is unavailable while a turn is running", cmd)})
		return nil
	}
	switch cmd {
	case "/help":
		m.appendBlock(helpBlock{commands: m.slashCommands()})
	case "/codemode":
		return m.handleCodeModeCommand(strings.TrimSpace(strings.TrimPrefix(line, cmd)))
	case "/goal":
		return m.handleGoalCommand(strings.TrimSpace(strings.TrimPrefix(line, cmd)))
	case "/model":
		return m.openModelBrowser()
	case "/effort":
		m.handleEffortCommand(strings.TrimSpace(strings.TrimPrefix(line, cmd)))
		return nil
	case "/provider":
		return m.openProviderBrowser()
	case "/sessions":
		return m.openSessionBrowser()
	case "/usage":
		m.appendBlock(usageBlock{usage: m.ag.Usage()})
	case "/clear":
		cleared := m.resetConversation()
		if cleared != nil {
			m.appendBlock(noticeBlock{text: fmt.Sprintf(
				"goal cleared (%s: %s) · /goal pause keeps a goal without continuing it",
				cleared.ID, goal.Summarize(cleared.Objective))})
		}
		if err := m.persistSession(); err != nil {
			m.appendBlock(errorBlock{err: err})
		}
	case "/quit", "/exit":
		return m.quitNow()
	default:
		if sk, ok := m.slashSkill(cmd); ok {
			if m.busy {
				m.appendBlock(errorBlock{err: fmt.Errorf("%s is unavailable while a turn is running", cmd)})
				return nil
			}
			args := strings.TrimSpace(strings.TrimPrefix(line, cmd))
			expanded := skills.ExpandInvocation(sk, args)
			send := m.submitUserTurnWithSkillExpansion(line, expanded, nil, false)
			m.turn.label = skills.DisplayName(sk.Name)
			m.appendBlock(noticeBlock{text: "applied skill: " + sk.Name})
			return send
		}
		m.appendBlock(errorBlock{err: fmt.Errorf("unknown command: %s — try /help", cmd)})
	}
	return nil
}

// quitNow leaves immediately, whether or not a turn is running. It is the
// escape hatch for a turn that will not unwind: ctrl+c asks the turn to cancel
// and then waits for it, so a tool that ignores its context can leave the UI
// waiting. This does not wait.
//
// The session is saved on the way out, but a failed save does not block the
// exit — the user asked to leave.
func (m *model) quitNow() tea.Cmd {
	logx.Debug("tui quit command", "busy", m.busy)
	if m.sendCancel != nil {
		m.sendCancel()
		m.sendCancel = nil
	}
	if err := m.persistSession(); err != nil {
		m.appendBlock(errorBlock{err: fmt.Errorf("save session: %w", err)})
	}
	m.quitting = true
	return tea.Quit
}

// slashCommandRequiresIdle reports whether a command starts new work or
// replaces conversation state. Control commands that only change the session's
// goal status stay available mid-turn, so /goal is decided by its subcommand
// rather than by its name.
//
// /usage is read-only but still idle-only: the agent writes usage from the turn
// goroutine, so reading it mid-turn would race with that write.
func slashCommandRequiresIdle(cmd, rest string) bool {
	switch cmd {
	case "/clear", "/model", "/provider", "/sessions", "/usage":
		return true
	case "/goal":
		sub, _ := splitGoalArgs(rest)
		return sub == ""
	default:
		return false
	}
}

func (m *model) updateInlinePickers() {
	m.updateSlashPicker()
	if m.picker.visible {
		m.hideFilePicker()
		return
	}
	m.updateFilePicker()
}

func (m *model) inlinePickerView() string {
	if out := m.slashPickerView(); out != "" {
		return out
	}
	return m.filePickerView()
}

// handleBangCommand parses !shell aliases. Called only when input begins with
// '!'. The command is intentionally not appended as a user chat message.
func (m *model) handleBangCommand(line string) tea.Cmd {
	command := strings.TrimSpace(strings.TrimPrefix(line, "!"))
	if command == "" {
		m.appendBlock(errorBlock{err: fmt.Errorf("type a shell command after !, for example !git status")})
		return nil
	}
	if m.busy {
		m.appendBlock(errorBlock{err: fmt.Errorf("! is unavailable while a turn is running")})
		return nil
	}
	m.busy = true
	m.busySince = time.Now()
	m.turn = turnStats{direct: true}
	m.setDotColor(colDotThinking)
	return m.startTool("bash", map[string]any{"command": command})
}

func (m *model) finishApproval(ok bool) {
	if m.approval == nil {
		return
	}
	m.approval.reply <- ok
	if ok {
		logx.Debug("tui approval answered", "tool", m.approval.req.ToolName, "approved", true)
		m.appendBlock(noticeBlock{text: "approved " + m.approval.req.ToolName})
	} else {
		logx.Debug("tui approval answered", "tool", m.approval.req.ToolName, "approved", false)
		m.appendBlock(noticeBlock{text: "denied " + m.approval.req.ToolName})
	}
	m.approval = nil
}

func (m *model) resultSummary(err error, elapsed time.Duration) (resultSummaryBlock, bool) {
	if !m.turn.direct && m.turn.tools == 0 && !m.turn.workflow && m.turn.label == "" {
		return resultSummaryBlock{}, false
	}
	maxTurns := errors.Is(err, agent.ErrMaxTurns)
	done, failedCount, skipped, total := m.workflowCounts()
	// Tool failures are intermediate warnings when the agent recovers and
	// completes the turn. Direct commands and failed workflow items are final
	// outcomes, so their receipts stay visibly incomplete.
	failed := !maxTurns && (err != nil || (m.turn.direct && m.turn.errors > 0) || failedCount > 0)
	label := m.turnSummaryLabel(maxTurns, failed)
	detail := m.turnSummaryDetail(done, failedCount, skipped, total, failed, maxTurns)
	return resultSummaryBlock{label: label, detail: detail, elapsed: elapsed, failed: failed}, true
}

// workflowCounts reports the checklist totals, all zero when there is none.
func (m *model) workflowCounts() (done, failed, skipped, total int) {
	if m.workflow == nil {
		return 0, 0, 0, 0
	}
	done, failed, skipped = workflowCounts(m.workflow.items)
	return done, failed, skipped, len(m.workflow.items)
}

// turnSummaryLabel picks the receipt headline: the skill or plan name when
// there is one, and otherwise a phrase that says whether the turn succeeded.
func (m *model) turnSummaryLabel(maxTurns, failed bool) string {
	skillLabel := strings.TrimSpace(m.turn.label)
	label := skillLabel
	if label == "" {
		label = "Done"
	}
	switch {
	case maxTurns:
		if skillLabel != "" {
			return skillLabel + " paused"
		}
		return "Paused"
	case m.turn.label == "" && m.workflow != nil && strings.TrimSpace(m.workflow.title) != "":
		return strings.TrimSpace(m.workflow.title)
	case m.turn.direct:
		if failed {
			return "Command finished with issues"
		}
		return "Command complete"
	case failed:
		if skillLabel != "" {
			return skillLabel + " finished with issues"
		}
		return "Finished with issues"
	}
	return label
}

// turnSummaryDetail lists what the turn did: checklist progress, tool count,
// recovered errors, and whether a max-turns stop expects a reply.
func (m *model) turnSummaryDetail(done, failedCount, skipped, total int, failed, maxTurns bool) string {
	var parts []string
	if total > 0 {
		outcome := fmt.Sprintf("%d/%d complete", done, total)
		if done == total {
			outcome = fmt.Sprintf("%d/%d verified", done, total)
		}
		parts = append(parts, outcome)
		if failedCount > 0 {
			parts = append(parts, fmt.Sprintf("%d failed", failedCount))
		}
		if skipped > 0 {
			parts = append(parts, fmt.Sprintf("%d skipped", skipped))
		}
	} else if m.turn.tools > 0 && !m.turn.direct {
		parts = append(parts, fmt.Sprintf("%d %s", m.turn.tools, plural("tool", m.turn.tools)))
	}
	if m.turn.errors > 0 && !failed {
		parts = append(parts, fmt.Sprintf("recovered from %d tool %s", m.turn.errors, plural("error", m.turn.errors)))
	}
	if maxTurns {
		parts = append(parts, "reply to continue")
	}
	return strings.Join(parts, " · ")
}

func plural(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// setDotColor swaps the spinner's foreground so the pulsing dot reflects
// the current state (thinking vs. tool-active). Called from Update on the
// transitions that actually change state.
func (m *model) setDotColor(c color.Color) {
	m.spin.Style = lipgloss.NewStyle().Foreground(c)
}

func (m *model) statusLine() string {
	if !m.busy {
		// Idle is intentionally quiet; input discovery lives in the splash.
		dot := lipgloss.NewStyle().Foreground(colDotReady).Render("●")
		return truncate(" "+dot+" "+styMuted.Render("ready"), max(m.width, 1))
	}

	elapsed := time.Since(m.busySince)

	fullHint := "↩ steer · ctrl+↩ queue · esc interrupt"
	compactHint := "esc interrupt"
	if m.approval != nil {
		fullHint = "esc to deny"
		compactHint = "esc deny"
	} else if m.queued != nil && m.turn.direct {
		fullHint = "next queued · esc interrupt"
		compactHint = "queued · esc interrupt"
	} else if m.queued != nil {
		fullHint = "next queued · ↩ steer · esc interrupt"
		compactHint = "queued · esc interrupt"
	} else if m.turn.direct {
		fullHint = "ctrl+↩ queue · esc interrupt"
	}
	activity := m.statusActivity()
	prefix := " " + m.spin.View() + " "
	timing := " · " + formatElapsedCompact(elapsed)
	minimumActivityWidth := 28
	if m.approval != nil {
		minimumActivityWidth = 16
	}
	hint := statusHintForWidth(m.width-lipgloss.Width(prefix)-lipgloss.Width(timing), fullHint, compactHint, minimumActivityWidth)
	suffix := timing
	if hint != "" {
		suffix += "   " + hint
	}
	activityWidth := max(m.width-lipgloss.Width(prefix)-lipgloss.Width(suffix), 1)
	line := prefix + styLabel.Render(truncate(activity, activityWidth)) + styDim.Render(suffix)
	return truncate(line, max(m.width, 1))
}

func statusHintForWidth(available int, full, compact string, minimumActivityWidth int) string {
	// Keep enough room for primary states such as "3 subagents in parallel".
	// Controls degrade from full to compact before useful activity disappears.
	if lipgloss.Width(full)+3+minimumActivityWidth <= available {
		return full
	}
	if lipgloss.Width(compact)+3+minimumActivityWidth <= available {
		return compact
	}
	return ""
}

func (m *model) statusActivity() string {
	parts := []string{}
	if m.turn.label != "" {
		parts = append(parts, m.turn.label)
	}
	if m.approval != nil {
		parts = append(parts, "Waiting for approval")
		return strings.Join(parts, " · ")
	}
	if workflow := m.workflowProgress(); workflow != "" {
		parts = append(parts, workflow)
	}
	if running := m.inflightTools(); len(running) > 1 {
		noun := "tools"
		if allAgents(running) {
			noun = "subagents"
		}
		parts = append(parts, fmt.Sprintf("%d %s in parallel", len(running), noun))
	} else if len(running) == 1 {
		parts = append(parts, capitalize(toolVerb(running[0].name, running[0].args)))
	}
	if len(parts) == 0 {
		if m.turn.tools > 0 {
			return "Reviewing results"
		}
		return "Understanding request"
	}
	return strings.Join(parts, " · ")
}

func allAgents(calls []*toolCallBlock) bool {
	for _, call := range calls {
		if call.name != "agent" {
			return false
		}
	}
	return len(calls) > 0
}

// trackToolCall records a started tool call so the status line can describe
// it and its receipt can carry an elapsed time.
func (m *model) trackToolCall(id string, tc *toolCallBlock) {
	if m.inflight == nil {
		m.inflight = map[string]*toolCallBlock{}
	}
	m.inflight[id] = tc
}

// finishToolCall removes and returns the tracked call, or nil if unknown.
func (m *model) finishToolCall(id string) *toolCallBlock {
	tc := m.inflight[id]
	delete(m.inflight, id)
	return tc
}

func (m *model) clearInflight() { m.inflight = nil }

// inflightTools lists running calls oldest first, so a single running call
// is described deterministically.
func (m *model) inflightTools() []*toolCallBlock {
	out := make([]*toolCallBlock, 0, len(m.inflight))
	for _, tc := range m.inflight {
		out = append(out, tc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].startAt.Before(out[j].startAt) })
	return out
}

func (m *model) workflowProgress() string {
	if m.workflow == nil || len(m.workflow.items) == 0 {
		return ""
	}
	for i, item := range m.workflow.items {
		if item.Status == workflow.Running {
			return fmt.Sprintf("%d/%d %s", i+1, len(m.workflow.items), oneLine(item.Text))
		}
	}
	done, failed, skipped := workflowCounts(m.workflow.items)
	finished := done + failed + skipped
	if finished == len(m.workflow.items) {
		if failed > 0 {
			return fmt.Sprintf("%d/%d Plan finished · %d failed", finished, len(m.workflow.items), failed)
		}
		if skipped > 0 {
			return fmt.Sprintf("%d/%d Plan complete · %d skipped", finished, len(m.workflow.items), skipped)
		}
		return fmt.Sprintf("%d/%d Plan complete", finished, len(m.workflow.items))
	}
	title := strings.TrimSpace(m.workflow.title)
	if title == "" {
		title = "Plan"
	}
	return fmt.Sprintf("%d/%d %s", finished, len(m.workflow.items), oneLine(title))
}

func formatElapsedCompact(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	return fmt.Sprintf("%dm %02ds", seconds/60, seconds%60)
}

func capitalize(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func formatElapsed(d time.Duration) string {
	if d > 0 && d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d / time.Minute)
	s := int(d/time.Second) % 60
	return fmt.Sprintf("%dm%02ds", m, s)
}

func (m *model) footerLine() string {
	left := m.cwd
	if m.branch != "" && m.branch != "no-git" {
		left += " · " + m.branch
	}
	right := backendLabel(m.providerTag, m.modelTag)
	line := left
	if right != "" {
		gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap > 0 {
			line += strings.Repeat(" ", gap) + right
		} else {
			line += "  " + right
		}
	}
	return styFooter.Render(truncate(line, max(m.width, 1)))
}

// inputMaxRows caps how tall the input grows; beyond that the textarea
// scrolls internally.
const inputMaxRows = 8

// transcriptProgressGapHeight reserves breathing room between scrollable
// output and the fixed workflow/status area below it.
const transcriptProgressGapHeight = 1

const minimumTranscriptHeight = 1

// syncInputHeight re-runs layout when the textarea's (self-managed, soft-wrap
// aware) height has changed since the last frame, so the viewport resizes to
// make room. The textarea recalculates its own height on every edit because
// DynamicHeight is enabled; we only react to the result.
func (m *model) syncInputHeight() {
	if m.input.Height() != m.lastInputHeight {
		m.lastInputHeight = m.input.Height()
		m.layout()
	}
}

// setMarkdownStyle switches the glamour style and re-renders the transcript.
// It runs after the first frame, when Bubble Tea delivers the terminal
// background colour, so it must not assume a window size is known yet.
func (m *model) setMarkdownStyle(name string) {
	if name == "" || name == m.mdStyleName {
		return
	}
	m.mdStyleName = name
	m.mdWidth = 0
	m.rendered = m.rendered[:0]
	m.renderedWidth = 0
	if m.width > 2 {
		if r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle(name),
			glamour.WithWordWrap(m.width-2),
		); err == nil {
			m.md = r
			m.mdWidth = m.width - 2
		}
	}
	if m.width > 0 {
		m.refreshViewport()
	}
}

func (m *model) layout() {
	followOutput := m.viewport.AtBottom()
	previousOffset := m.viewport.YOffset()
	vpH := m.height - m.fixedChromeHeight()
	if vpH < minimumTranscriptHeight {
		vpH = minimumTranscriptHeight
	}
	m.viewport.SetWidth(m.width)
	m.viewport.SetHeight(vpH)
	// A shorter viewport otherwise looks manually scrolled even when it was
	// following the bottom before surrounding UI grew.
	if followOutput {
		m.viewport.GotoBottom()
	} else {
		m.viewport.SetYOffset(previousOffset)
	}
	m.input.SetWidth(m.width - 2)
	if m.md != nil && m.mdWidth != m.width-2 {
		// Re-create renderer at the new width so code blocks wrap nicely.
		// Use the cached style name — no re-probing the terminal here.
		if r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle(m.mdStyleName),
			glamour.WithWordWrap(m.width-2),
		); err == nil {
			m.md = r
			m.mdWidth = m.width - 2
		}
	}
}

func (m *model) statusLineHeight() int {
	return 1
}

func (m *model) fixedChromeHeight() int {
	return m.baseChromeHeight() + m.pickerPanelHeight()
}

func (m *model) baseChromeHeight() int {
	inputHeight := m.input.Height() + 2 // textarea body + top/bottom padding
	return inputHeight + transcriptProgressGapHeight + 3 + m.statusLineHeight()
}

func (m *model) pickerPanelHeight() int {
	return lipgloss.Height(m.inlinePickerView())
}

func (m *model) maxInlinePickerRows() int {
	return max(m.height-m.baseChromeHeight()-minimumTranscriptHeight, 0)
}

func (m *model) appendBlock(b block) {
	m.blocks = append(m.blocks, b)
	m.refreshViewport()
}

func (m *model) toggleLatestToolResultExpansion() bool {
	for i := len(m.blocks) - 1; i >= 0; i-- {
		b, ok := m.blocks[i].(toolResultBlock)
		if !ok || !b.isTruncated() {
			continue
		}
		b.expanded = !b.expanded
		m.blocks[i] = b
		m.invalidateRenderedFrom(i)
		m.refreshViewport()
		return true
	}
	return false
}

func (m *model) refreshViewport() {
	if m.width == 0 {
		return
	}
	followOutput := m.viewport.AtBottom()
	previousOffset := m.viewport.YOffset()
	if m.renderedWidth != m.width {
		m.rendered = m.rendered[:0]
		m.renderedWidth = m.width
	}
	if len(m.rendered) > len(m.blocks) {
		m.rendered = m.rendered[:len(m.blocks)]
	}
	var sb strings.Builder
	for i, b := range m.blocks {
		if i > 0 {
			sb.WriteString(blockSeparator(m.blocks[i-1], b))
		}
		if i < len(m.rendered) && !mutableBlock(b) {
			sb.WriteString(m.rendered[i])
		} else {
			out := b.render(m.width, m.md)
			if i < len(m.rendered) {
				m.rendered[i] = out
			} else {
				m.rendered = append(m.rendered, out)
			}
			sb.WriteString(out)
		}
		sb.WriteString("\n")
	}
	m.viewport.SetContent(sb.String())
	if followOutput {
		m.viewport.GotoBottom()
	} else {
		m.viewport.SetYOffset(previousOffset)
	}
}

// mutableBlock reports whether a block is updated in place after it is
// appended, which makes its cached rendering stale.
func mutableBlock(b block) bool {
	_, ok := b.(*workflowBlock)
	return ok
}

// invalidateRenderedFrom drops cached renderings from index i onward. Callers
// that replace a value block in m.blocks use it before refreshing.
func (m *model) invalidateRenderedFrom(i int) {
	if i < len(m.rendered) {
		m.rendered = m.rendered[:i]
	}
}

func blockSeparator(previous, next block) string {
	previousTool, previousOK := previous.(toolCallBlock)
	nextTool, nextOK := next.(toolCallBlock)
	if previousOK && nextOK && !previousTool.verbose && !nextTool.verbose {
		return ""
	}
	return "\n"
}

func (m *model) handleEvent(e agent.Event) {
	switch e.Kind {
	case agent.EventParallelStart:
		// Each call in the group also arrives as its own tool_call event, so
		// the group announcement carries nothing the transcript needs.
	case agent.EventAssistantText:
		if strings.TrimSpace(e.Text) != "" {
			m.appendBlock(textBlock{text: e.Text})
		}
	case agent.EventAssistantCommentary:
		if strings.TrimSpace(e.Text) != "" {
			m.appendBlock(thinkingBlock{text: e.Text})
		}
	case agent.EventSteeringApplied:
		if len(m.pendingSteering) > 0 {
			m.pendingSteering = m.pendingSteering[1:]
		}
	case agent.EventToolCall:
		m.handleToolCallEvent(e)
	case agent.EventToolResult:
		m.handleToolResultEvent(e)
	case agent.EventError:
		// The same error comes back from Send and is rendered once from
		// sendResultMsg, which also knows whether it was a cancellation.
	case agent.EventMaxTurnsReached:
		m.appendBlock(maxTurnsBlock{limit: e.MaxTurns, label: m.turn.label})
	case agent.EventDone:
		m.clearInflight()
	}
}

// handleToolCallEvent tracks a started call so its receipt can report elapsed
// time later. The workflow tool is skipped: it mutates the checklist through
// workflowEventMsg, and a generic card beside that would duplicate it.
func (m *model) handleToolCallEvent(e agent.Event) {
	// A sub-call names its parent through GroupID. GroupSize is zero because a
	// script's calls arrive one at a time and their number is unknown up front,
	// which is exactly how a parallel group differs from this.
	if e.GroupID != "" && e.GroupSize == 0 {
		m.startScriptSubCall(e)
		return
	}
	if e.Name == "workflow" {
		m.turn.workflow = true
		return
	}
	if e.Name == "script" {
		m.scriptCalls++
	}
	m.turn.tools++
	m.noteWorkflowActivity(capitalize(toolVerb(e.Name, e.Args)))
	tc := &toolCallBlock{
		name: e.Name, args: e.Args, startAt: time.Now(), verbose: m.verbose,
		toolUseID: e.ToolUseID, groupID: e.GroupID,
	}
	m.trackToolCall(e.ToolUseID, tc)
	if m.verbose {
		m.appendBlock(*tc)
	}
}

// handleToolResultEvent renders a finished call and counts it as recovered or
// final. A successful workflow call is represented by the checklist, but a
// failure may not produce a workflow event and must stay visible.
func (m *model) handleToolResultEvent(e agent.Event) {
	if e.GroupID != "" && e.GroupSize == 0 {
		m.finishScriptSubCall(e)
		return
	}
	if e.Name == "workflow" {
		if e.IsError {
			m.turn.errors++
			m.appendBlock(toolResultBlock{name: e.Name, text: e.Text, isError: true})
		}
		return
	}
	elapsed := time.Duration(0)
	completed := m.finishToolCall(e.ToolUseID)
	if completed != nil {
		elapsed = time.Since(completed.startAt)
	}
	// The agent tool reports a failed child inside a successful result.
	failed := e.IsError || (e.Name == "agent" && !runStepOK(e.Text))
	if failed {
		m.turn.errors++
	}
	if !m.verbose && !failed && completed != nil {
		completed.elapsed = elapsed
		m.appendBlock(*completed)
	}
	// In concise mode, routine successful result bodies add no scannable
	// information beyond the compact receipt, so only failures render.
	// Direct ! commands are user-requested output, not intermediate agent
	// activity, and must remain visible in either mode.
	if m.verbose || failed || m.turn.direct {
		m.appendBlock(toolResultBlock{name: e.Name, text: e.Text, isError: failed, elapsed: elapsed})
	}
}

func (m *model) appendTranscript(messages []llm.Message) {
	failedToolUses := failedTranscriptToolUseOccurrences(messages)
	for messageIndex, msg := range messages {
		switch msg.Role {
		case llm.RoleUser:
			var toolResults []llm.ContentBlock
			var textParts []string
			if strings.TrimSpace(msg.DisplayText) != "" {
				textParts = append(textParts, msg.DisplayText)
			}
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					if strings.TrimSpace(msg.DisplayText) != "" {
						continue
					}
					if strings.TrimSpace(block.Text) != "" {
						textParts = append(textParts, block.Text)
					}
				case "image":
					textParts = append(textParts, "[image]")
				case "tool_result":
					toolResults = append(toolResults, block)
				}
			}
			if len(textParts) > 0 {
				m.blocks = append(m.blocks, userBlock{text: strings.Join(textParts, "\n")})
			}
			for _, block := range toolResults {
				if m.verbose || block.IsError {
					m.blocks = append(m.blocks, toolResultBlock{text: block.Content, isError: block.IsError})
				}
			}
		case llm.RoleAssistant:
			hasTools := hasTranscriptToolUse(msg.Content)
			for blockIndex, block := range msg.Content {
				switch block.Type {
				case "text":
					if strings.TrimSpace(block.Text) != "" {
						if hasTools {
							m.blocks = append(m.blocks, thinkingBlock{text: block.Text})
						} else {
							m.blocks = append(m.blocks, textBlock{text: block.Text})
						}
					}
				case "tool_use":
					occurrence := transcriptToolUseOccurrence{message: messageIndex, block: blockIndex}
					if !m.verbose && failedToolUses[occurrence] {
						continue
					}
					m.blocks = append(m.blocks, toolCallBlock{name: block.Name, args: block.Input, verbose: m.verbose})
				}
			}
		}
	}
	m.refreshViewport()
}

type transcriptToolUseOccurrence struct {
	message int
	block   int
}

func failedTranscriptToolUseOccurrences(messages []llm.Message) map[transcriptToolUseOccurrence]bool {
	failed := make(map[transcriptToolUseOccurrence]bool)
	pending := make(map[string][]transcriptToolUseOccurrence)
	for messageIndex, msg := range messages {
		for blockIndex, block := range msg.Content {
			switch block.Type {
			case "tool_use":
				occurrence := transcriptToolUseOccurrence{message: messageIndex, block: blockIndex}
				pending[block.ID] = append(pending[block.ID], occurrence)
			case "tool_result":
				queue := pending[block.ToolUseID]
				if len(queue) == 0 {
					continue
				}
				occurrence := queue[0]
				pending[block.ToolUseID] = queue[1:]
				if block.IsError {
					failed[occurrence] = true
				}
			}
		}
	}
	return failed
}

func hasTranscriptToolUse(content []llm.ContentBlock) bool {
	for _, block := range content {
		if block.Type == "tool_use" {
			return true
		}
	}
	return false
}

func (m *model) startSend(displayText, text string, images []string) tea.Cmd {
	ctx := tools.WithConversationGeneration(m.ctx, m.conversationGeneration)
	ctx, cancel := context.WithCancel(ctx)
	m.sendCancel = cancel
	logx.Debug("tui send start", "images", len(images), "text", logx.SafeString(text, 240))
	return func() tea.Msg {
		_, err := m.ag.SendWithDisplay(ctx, text, displayText, images)
		result := sendResultMsg{err: err}
		result.saveAttempted, result.saveErr = runPersistence(m.afterSend)
		return result
	}
}

func (m *model) persistSession() error {
	attempted, err := runPersistence(m.afterSend)
	if !attempted {
		return nil
	}
	m.recordPersistenceResult(err)
	return err
}

func (m *model) recordPersistenceResult(err error) {
	m.persistenceErr = err
}

func runPersistence(afterSend func() error) (bool, error) {
	if afterSend == nil {
		return false, nil
	}
	return true, afterSend()
}

func (m *model) startTool(name string, input map[string]any) tea.Cmd {
	ctx := tools.WithConversationGeneration(m.ctx, m.conversationGeneration)
	ctx, cancel := context.WithCancel(ctx)
	m.sendCancel = cancel
	logx.Debug("tui tool start", "name", name, "args", logx.SafeAny(input))
	return func() tea.Msg {
		m.ag.RunTool(ctx, name, input)
		return sendResultMsg{}
	}
}

// shortPaths renders attachment paths as just their base names for the inline
// notice, so a long absolute path doesn't blow out the line.
func shortPaths(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

func refreshBranch() tea.Cmd {
	return func() tea.Msg {
		return branchMsg{branch: gitBranch()}
	}
}

func gitBranch() string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "no-git"
	}
	return strings.TrimSpace(string(out))
}

// scriptRun is the bookkeeping for one script's sub-calls: which rows belong to
// it and how many failed. Rows stay on screen after the script ends, because
// removing them would move everything rendered below, and the cheap alternative
// — blanking them — leaves gaps.
type scriptRun struct {
	rows     []int
	calls    int
	failures int
}

// startScriptSubCall appends one row beneath the script's receipt.
//
// Rows are appended at the end of the transcript rather than inserted after the
// parent. That is deliberate: inserting would renumber every block below it,
// and the rule this UI keeps is that work below the composer never moves.
// Because a script's calls arrive one at a time and their count is unknown,
// the parallel-group approach of reserving every row up front cannot apply —
// there is no number to reserve.
func (m *model) startScriptSubCall(e agent.Event) {
	run := m.subScripts[e.GroupID]
	if run == nil {
		run = &scriptRun{}
		if m.subScripts == nil {
			m.subScripts = map[string]*scriptRun{}
		}
		m.subScripts[e.GroupID] = run
	}
	if m.subCalls == nil {
		m.subCalls = map[string]int{}
	}
	row := subCallBlock{name: e.Name, args: e.Args, startAt: time.Now()}
	m.blocks = append(m.blocks, row)
	m.subCalls[e.ToolUseID] = len(m.blocks) - 1
	run.rows = append(run.rows, len(m.blocks)-1)
	run.calls++
	m.invalidateRenderedFrom(len(m.blocks) - 1)
	m.refreshViewport()
}

// finishScriptSubCall settles one row in place. It never appends, so a
// completed call does not move anything that was already rendered.
func (m *model) finishScriptSubCall(e agent.Event) {
	idx, ok := m.subCalls[e.ToolUseID]
	if !ok || idx >= len(m.blocks) {
		return
	}
	row, ok := m.blocks[idx].(subCallBlock)
	if !ok {
		return
	}
	row.done = true
	row.isError = e.IsError
	row.elapsed = time.Since(row.startAt)
	m.blocks[idx] = row
	m.invalidateRenderedFrom(idx)
	m.refreshViewport()
	if e.IsError {
		if run := m.subScripts[e.GroupID]; run != nil {
			run.failures++
		}
		m.turn.errors++
	}
}

// handleCodeModeCommand implements /codemode. The tool is off by default
// because it costs prompt bytes on every request, so this command is how a
// session opts in without editing the config by hand.
//
// It is idle-only: switching changes the model's tool list and system prompt,
// and doing that mid-turn would leave a request in flight describing tools the
// agent no longer has.
func (m *model) handleCodeModeCommand(rest string) tea.Cmd {
	arg := strings.ToLower(strings.TrimSpace(rest))
	if m.scriptSwitcher == nil || m.scriptEnabled == nil {
		m.appendBlock(errorBlock{err: fmt.Errorf("/codemode is unavailable in this session")})
		return nil
	}
	if m.busy {
		m.appendBlock(errorBlock{err: fmt.Errorf("/codemode is unavailable while a turn is running")})
		return nil
	}

	switch arg {
	case "":
		m.appendBlock(errorBlock{err: fmt.Errorf("usage: /codemode on|off|status")})
		return nil
	case "status":
		m.appendBlock(noticeBlock{text: m.codeModeStatusLine()})
		return nil
	case "on", "off":
		want := arg == "on"
		if current := m.scriptEnabled(); current == want {
			m.appendBlock(noticeBlock{text: m.codeModeStatusLine()})
			return nil
		}
		if err := m.scriptSwitcher(want); err != nil {
			m.appendBlock(errorBlock{err: fmt.Errorf("codemode: %w", err)})
			return nil
		}
		m.appendBlock(noticeBlock{text: m.codeModeStatusLine()})
		return nil
	default:
		m.appendBlock(errorBlock{err: fmt.Errorf("unknown /codemode argument %q — use on, off, or status", arg)})
		return nil
	}
}

// handleEffortCommand implements /effort. Bare form reports the setting; a
// value applies and persists it.
//
// The value is deliberately not checked against a list. Neo's own vocabulary is
// low/medium/high, but the backend owns the real set and it keeps growing, so
// an unfamiliar word is sent anyway and the API's error is what the user sees.
// Refusing it here would break a session the server would have accepted.
//
// It works mid-turn: the effort for a turn already in flight is fixed, and the
// switch applies to the next request, which is the same trade /model makes.
func (m *model) handleEffortCommand(rest string) {
	arg := strings.ToLower(strings.TrimSpace(rest))
	if m.effortSwitcher == nil || m.effort == nil {
		m.appendBlock(errorBlock{err: fmt.Errorf("/effort is unavailable in this session")})
		return
	}
	if arg == "" {
		m.appendBlock(noticeBlock{text: m.effortStatusLine()})
		return
	}
	if err := m.effortSwitcher(arg); err != nil {
		m.appendBlock(errorBlock{err: fmt.Errorf("effort: %w", err)})
		return
	}
	m.appendBlock(noticeBlock{text: m.effortStatusLine()})
}

// effortStatusLine names the effort in effect and what it means for requests,
// because "medium" alone does not say whether anything is being sent at all.
func (m *model) effortStatusLine() string {
	effort := ""
	if m.effort != nil {
		effort = strings.TrimSpace(m.effort())
	}
	if effort == "" {
		return "effort unset: no reasoning field is sent; the backend's default applies (try /effort medium)"
	}
	if effort == "none" {
		return "effort none: extended thinking is turned off where the backend supports it"
	}
	// A plain statement of what is now in effect. An earlier wording appended
	// "the backend rejects an unsupported value", which described a design rule
	// but read as if this very switch had just been rejected.
	return fmt.Sprintf("effort %s: sent as the reasoning strength on every request", effort)
}

// codeModeStatusLine states the state, what it costs, and whether that cost has
// bought anything yet. The last part is the one a user cannot otherwise see:
// "on" alone does not say whether this session's requests carried a tool that
// was never called.
func (m *model) codeModeStatusLine() string {
	if m.scriptEnabled != nil && m.scriptEnabled() {
		line := "codemode on: actions are written as code (+~1.7 KB prompt and schema per request)"
		return line + m.scriptUsageLine()
	}
	return "codemode off: the script tool is not registered"
}

// scriptUsageLine reports how many top-level `script` calls this session made.
// A script's own sub-calls arrive with a GroupID and are not counted: the
// question here is whether the tool was chosen at all, not how busy it was.
// Nothing is said while the count is zero, because "used 0 times" says what the
// absence already says.
func (m *model) scriptUsageLine() string {
	switch m.scriptCalls {
	case 0:
		return ""
	case 1:
		return "; used once this session"
	default:
		return fmt.Sprintf("; used %d times this session", m.scriptCalls)
	}
}
