# Changelog

All notable changes to Neo will be documented in this file.

## Unreleased

### Added

- The interactive `@` file picker completes paths that leave the workspace. It
  used to match only against paths relative to the working directory, so
  `@../`, `@../../` and `@/etc/` could never match anything and the picker
  stayed hidden. Such a token is now completed from disk one segment per `/`,
  listing directories before files, and the inserted text stays relative to the
  working directory so the tool call resolves it.

- Subagent progress now reaches the visible workflow checklist. The coordinator
  was already told not to mirror child calls because Neo attaches them
  automatically, but the supervisor's event stream was built and then discarded
  at the CLI boundary, so a delegated child's tool calls, completion, and failure
  never appeared. While a parent `agent` call is in flight, its child's activity
  now updates the detail line of the workflow step in progress, matched by the
  parent call's tool-use ID so a slow child cannot relabel a step the coordinator
  has moved past. Nothing is appended per child event: the step keeps the height
  and position it was created with, and stale events from a previous turn are
  ignored by the same generation check workflow events already use.

- Skills are now discovered from `~/.agents/skills/<name>/SKILL.md` as well as
  Neo's own directories, so a skill written for another agent tool is usable
  without copying it. Priority is unchanged elsewhere: built-ins first, then
  the shared directory, then `~/.neo/skills`, then the project, with later
  sources overriding by name. A missing directory is still skipped.

### Changed

- `just build` and `just install` now pass `-s -w`, so a local binary has the
  same shape as the release build: 32.98 MiB down to 23.86 MiB on linux/amd64,
  because the symbol table and DWARF data are gone. `install` matters most,
  since that binary is the one on PATH. The trade is that a panic trace from a
  local build no longer carries file and line numbers; `just build-debug` builds
  an unstripped `neo.debug` for that, and `just clean` removes both.
  `.goreleaser.yaml` already built this way, so the release artifacts are
  unchanged.

### Fixed

- `run()` now accepts the object spelling for its timeout. Every other global in
  a script takes a named-argument object, so `run(["prog", "arg"], {timeout: 10})`
  was the shape a model wrote first, and each attempt died on `run's timeout must
  be a number of seconds, got map[string]interface {}` — an error that named the
  type it received and not the convention it wanted, so the next attempt made the
  same mistake. The object can only mean the timeout, so it is read. When it holds
  no usable `timeout`, the error now names both accepted spellings.

- `grep` and `glob` no longer report a truncated search as a complete one. One
  output line longer than 64 KiB made `bufio.Scanner` stop permanently, and
  `readLines` never checked its error, so every line after the long one was
  dropped and the survivors were returned as the answer — `no matches` when the
  long line held the only match. A minified bundle, a lock file, or bundled JSON
  has exactly that shape. Both tools now fail with `grep: a result line exceeded
  the 64 KiB limit; narrow the pattern`, and the reader reports the scan error
  instead of a partial result.

- A script's `run()` calls are now visible while they happen. The host function
  reported only on completion and always as finished, so the TUI had no start to
  append a row against, and `translate` dropped the completion of any successful
  sub-call before it reached the coordinator. On the code-mode surface `run()` is
  how every test, build and git command happens, so a delegated turn sat on
  `Subagent · script` for the whole command. Both sides now see a start and a
  completion, and the activity line names the command (`$ go test ./...`) and the
  script's first line instead of the bare tool name.

- Sub-call IDs no longer collide between `tools.*` and `run()` in code mode.
  `globals` built a fresh counter for the tool namespace and `codeModeExtras`
  built a second one for `run`, so both started at `#1`. The TUI finds a row by
  that ID, so a failed program settled onto a `read_file` row and marked a
  successful read as failed. One counter now serves the whole script.

- Building a subagent while a custom provider's catalogue fetch was in flight
  could abort the process with `fatal error: concurrent map read and map write`.
  Discovery writes `Config.discovered` from a background goroutine, and the
  subagent path read it through `ContextWindowFor` on the main goroutine, with
  no synchronisation on either side. The table now has its own `sync.RWMutex`;
  every other config field is still set once at load time and remains lock-free.
- Inspect subagents now actually receive `read_file`, `grep`, and `glob` under
  the default code-mode surface. `toolsFor` asked for those three names while
  the code-mode registry held only `script`, and `Registry.Filter` silently
  drops a requested name the registry does not have, so the child got an empty
  tool list — and still answered, which made it read like a report produced by
  inspecting the repository. Work-mode children are unchanged: they ask for
  `script` alone and the same filter still leaves them with just that. A child
  that ends up with no tools is now an error naming the tools it asked for and
  the surface, matching the refusal the headless path already had.
- A skill directory that is a symlink is now discovered. `loadDir` skipped any
  entry whose `DirEntry.IsDir()` was false, and that check uses lstat
  semantics, so an entry symlinked into `~/.agents/skills` from another
  checkout was dropped even though `<name>/SKILL.md` resolved fine. Skills are
  commonly shared this way, so real ones never reached the catalog or the slash
  picker. Links that dangle or point at a non-directory are still skipped
  without failing the load, and precedence is unchanged. Note that one link
  must point at a single skill directory: a link to a directory that merely
  contains several skills has no `SKILL.md` of its own and is not a skill.
- `glob` no longer lists `.git/` internals. Ripgrep lets a later `--glob`
  override an earlier one, and the shared argument builder put `--glob "!.git/"`
  before the user's pattern, so any pattern broad enough to match directories
  replaced the exclusion: `glob("*")`, `glob("**")`, `glob("**/*")`,
  `glob("*.*")` and `glob("**/**")` returned `.git/HEAD`, `.git/config`, and
  files under `.git/objects` — including the contents of committed objects,
  which the model could then read with `read_file`. The exclusion is now
  appended after the pattern, and `grep` appends the same one explicitly.
  Narrow patterns such as `**/*.go` never leaked, which is why the existing
  recursive test did not catch it. A `--glob` still overrides `.gitignore`, as
  it did before; `docs/developer/tools.md` now records both rules.

## [v0.7.0-dev.20261126] - 2026-11-26

### Added

- The Chat Completions adapter now logs each request's resolved
  `reasoning_effort` alongside the body hash, so a pass-through backend that
  silently ignores the field can be told apart from one that honours it. Every
  other adapter already logged its request; this one did not, which left the
  provider family most likely to ignore an unknown effort as the only one with
  no way to check.

- The effort scale gained `none`, `xhigh`, and `max` alongside low/medium/
  high. `none` turns thinking off: Gemini gets an explicit `thinkingBudget: 0`
  (sent even though it is the zero value, since omitting it would restore the
  model's dynamic default), and Anthropic drops the `thinking` field, which is
  the only "off" that API has. `xhigh` and `max` extend the budget tables
  (Anthropic 24576/32000, Gemini 32768/65536). On OpenAI-compatible backends all
  three names travel unchanged, so a model without those tiers answers with its
  own API error rather than Neo guessing.

- Reasoning effort can be set for the whole session with `reasoning_effort` in
  `neo.yaml`, per invocation with `--effort <value>`, or mid-session with
  `/effort <value>` (`/effort` alone reports the current value). Before this,
  Neo sent no reasoning field at all and the strength was whatever each backend
  defaulted to, so there was no way to spend less thinking or ask for more.
  Leaving the key unset keeps the request byte-identical to before. What is sent
  depends on the backend, because the APIs disagree: Anthropic maps names onto
  `thinking.budget_tokens` (raising `max_tokens` so a budget still leaves room
  to answer), Gemini onto `thinkingConfig.thinkingBudget`, and OpenAI/OpenRouter/
  custom providers take `reasoning.effort` or `reasoning_effort` as written. The
  value is not checked against a list — the accepted vocabulary belongs to the
  backend and keeps growing — so an unfamiliar word is passed through and the
  API's error explains it. Anthropic and Gemini cannot carry a word at all, so a
  value that cannot be mapped to a budget fails the request with guidance
  instead of being silently dropped. `--effort` never writes the config, so a
  one-off run cannot change the saved default; `/effort` persists. Children
  spawned by the `agent` tool inherit the coordinator's effort, and compaction
  summaries are sent without one.

- `read_file` takes `raw: true`, which returns the file's own text without the
  `%6d\t` line-number gutter. The gutter is for the model's eyes — it matches
  what `grep` reports — but `edit_file` matches the file's raw text, so a script
  that read a file and passed that text back as `old_string` failed with
  "old_string not found" and then had to strip the prefix by hand
  (`lines.map((l) => l.replace(/^\s*\d+\t/, ""))`), which is the step that kept
  read-decide-write from collapsing into one call. Measured over 85 local
  sessions, 218 script calls with sub-calls merged a read and a write 45 times
  (20.6%), and 12 `old_string not found` results and 6 "found N times" results
  traced back to the same mismatch. The default output is byte-for-byte
  unchanged; only the opt-in flag differs, and the fixed floor grew by 158 bytes
  for the description that names it.

- A custom provider's context window can be discovered from its catalogue
  instead of being typed into the config. Neo already fetched
  `<endpoint>/models` for the `/model` picker; it now also reads the window that
  response carries, under `context_length`, `max_model_len`,
  `context_window`/`max_context_length`, a numeric string, or llama.cpp's
  `meta.n_ctx`. Since #108 a window can be set per model, but it still had to be
  set by hand, and a local server that reports `meta.n_ctx: 40192` had its
  compaction trigger derived from a guessed 200,000. Discovery sits below an
  explicit `context_window_tokens` and above `compaction.context_window_tokens`,
  because the embedded defaults set that key, and it is never persisted. The
  fetch is asynchronous: a chat session rebuilds its summarizer when the answer
  arrives, `neo run` waits for it within its deadline, and a backend that lists
  no catalogue, or answers with an unreadable shape, keeps the configured window
  with no error.

- A custom provider's model can carry its own `context_window_tokens`, which
  overrides `compaction.context_window_tokens` for that model. A window belongs
  to a model, and one config serving a 40K local model and a 200K hosted one
  could not share a single trigger: at the default the local model was refused
  long before compaction ran, and lowered to fit it every hosted model would
  have been compacted five times sooner than needed. The window now follows a
  `/model` switch, and a subagent running on a different model resolves that
  model's window rather than inheriting the coordinator's.

- Added the single-tool surface that this release then made the default. It was
  introduced as an opt-in mode under the name `solo`; that name no longer
  exists, and the behaviour it described is now what `codemode` does: one
  `script` tool carrying `run(argv, timeoutSeconds)`, a `console`/`print`, and
  one interpreter per session so `globalThis` survives between calls. Measured
  over five runs of one debugging task: the suite passed in both arms and input
  fell about 20%. A first measurement showed more tool errors; that was the
  instrumentation, not the mode — a program's non-zero exit was counted as a
  tool error and `glob("*")` had no binding for a bare string. After both were
  fixed, the same five runs showed 0–2 errors against the default's 1–2, 6
  requests against 8 and 17,135 input tokens against 23,406. #91 records both
  measurements and why the conclusion was "add capability" rather than "keep":
  the merge rarely happens and the fixture is too easy to prove it would help.

- Added the JavaScript `script` tool: it runs model-written code that can call
  `read_file`, `grep`, `glob`, `write_file`, and `edit_file`, returning its last
  expression's value. Each scripted call passes the same approval matcher a
  top-level call would, so `tool_approvals` keeps its meaning one level down,
  and the prompt names `script` as the requester. Scripts are bounded by a
  deadline, the engine's memory limit, and the 64 KiB tool-output cap, and their
  sub-calls appear as rows under the script's own receipt. It is off by default:
  a measurement found no task where the model chose it over `bash`, and it costs
  1,712 bytes per request (949 schema, 763 prompt), including one sentence in
  `bash`'s description that points at the tool and a tie-break in the base
  prompt's batching rule. Turn it on with `--script`,
  `features.script: true`, or `/codemode on` in chat. `/codemode status` reports
  how many times this session chose the tool, so the cost can be weighed against
  its use.

- `/sessions` lists saved sessions inside the TUI and switches to one without
  leaving the terminal. The running session is saved first and the chosen one
  must still load, so a failed save or a deleted session reports the error in
  the picker and keeps you where you were. The switch re-enters the TUI on the
  target the same way `neo resume <id>` does, so the saved cwd, provider, and
  model rules are unchanged.

- `/goal` gives a session one standing objective and keeps working toward it
  while the session is idle: `/goal <objective>` starts the first turn,
  `/goal status` shows the objective, status, attempt budget, blocked audit, and
  last reason, and `/goal pause`, `/goal resume`, and `/goal clear` control it.
  The model can only mark a goal complete (with evidence) or blocked (after the
  same blocker recurs), so it can never resume, clear, or extend its own run.
  Goal state is saved with the session and restored on `neo resume`. `esc` and
  `ctrl+c` pause a running goal, `/clear` drops it, and `goals.max_attempts`
  (default 20) stops the run with a handoff that names the last recorded reason.

- Declared custom OpenAI-compatible providers under a top-level `providers:`
  map and select them with `provider: <name>` or `subagents.provider`. This
  covers local runtimes (Ollama, vLLM), LiteLLM, and internal gateways without
  an adapter per vendor. Endpoints with no `api_key_env` send no authorization
  header; configured keys are validated at startup and redacted in logs.
  Custom backends appear in `/model`, `neo doctor`, and resumed sessions.

- `/model` now loads the live model catalogue of a custom OpenAI-compatible
  backend from `GET <endpoint>/models` when the picker opens, instead of only
  listing `providers.<name>.models`. The static list stays as the immediate
  fallback and supplies display names; a failed or slow fetch keeps it visible
  and reports the error in the picker.

- Added `/provider` to switch the running chat to another built-in or custom
  backend without restarting. Neo validates credentials first, moves the chat
  agent and any coordinator-following subagent runner to the new provider's
  last-used or default model, refreshes the footer and `/model` list, and
  rewrites only `provider:` and `model:` in the config file it loaded
  (`~/.neo/config.yaml` when the session runs on embedded defaults). A failed
  switch leaves the running backend and the file unchanged.

- `/model` now persists the chosen model to the config file it loaded, rewriting
  only `model:` so the next launch starts on the same model. A write failure
  restores the previous model and reports the error.

- `neo run --json` now reports `requests`, the provider calls the run made,
  and `usage`, the token totals broken into input, cache-write, cache-read, and
  output. Both include the compaction summary call, so a scripted consumer sees
  the full cost of a run rather than only the turns the agent loop asked for.

- Added `/usage` to show what the session spent in provider tokens: the prompt
  total with its input, cache-write, and cache-read parts, the output total,
  and the share of the prompt served from the cache. The parts are the same
  figures saved with the session, so the cache hit rate has an unambiguous
  denominator. An empty session says so instead of printing zeros, and `/clear`
  resets the totals with the transcript.

### Changed

- An unrecognised `features.codemode` value is now an error instead of the
  default surface. The CLI flag already refused one, but the config file did
  not: `classic` misspelled, or `solo`, a mode removed in this release, both
  started a code-mode session with nothing to notice, which is how one
  measurement came to compare a surface against itself.

- `neo run` now refuses to start when `features.script: false` is combined with
  the default code mode. That combination left the headless session with no
  tools at all: the run answered normally and changed nothing. The error names
  both ways out, `features.script: true` or `features.codemode: classic`.
  Interactive sessions keep starting, since they still have `agent`, `goal`
  and `workflow`.

- Code mode is now the default surface. The model acts by writing JavaScript in
  the `script` tool, which carries `run(argv, timeoutSeconds)` for programs, a
  `console`/`print`, and one interpreter per session, so `globalThis` state
  survives between calls while local declarations stay per call. `agent`, `goal`
  and `workflow` remain: they coordinate the work, and writing code does not mean
  giving up delegation, goals or the visible checklist. Absolute paths keep
  `grep`/`glob` output usable inside a call where there is no `cd`, and relative
  paths resolve against the session directory so a script and `run()` agree.

- `features.codemode: classic` or `--codemode=classic` keeps the separate `bash`,
  `read_file`, `write_file`, `edit_file`, `grep` and `glob` tools. The fixed floor
  is 3,379 bytes in code mode against 8,084 in classic.

- `features.script` now defaults to `true`, since code execution is what the
  surface is built around. `/codemode on|off` still toggles it and persists the
  choice.

- `docs/performance.md` now records a token baseline — the 8,084-byte fixed
  overhead and how to measure a real run from `neo run --json` — so token work
  can be compared before and after instead of argued about.

- Truncated tool output now says what to do next. `read_file` reports the first
  line that did not fit and a limit that will fit (`retry with offset=N,
  limit=M`), and `bash` suggests narrowing the command itself (`| tail -n 200`,
  `| grep pattern`) alongside the byte counts it already reported.

### Fixed

- The terminal cursor now sits at the composer's insertion point. bubbletea v2
  keeps cursor positioning out of the program: the view declares where the
  cursor belongs, and with nothing declared the terminal left it wherever the
  last frame stopped drawing. fcitx5 and other input methods anchor their
  candidate window to that position, so Chinese input put the candidate list in
  the screen's bottom-right corner instead of beside the text.

- Editing an existing line in the composer no longer drops characters or
  crashes the TUI. `bubbles` v2.1.0 indexed a soft-wrapped row with an offset
  computed from a different wrap, so a row holding wide runes could be cut short,
  and a backspace could shift the cursor past the end of the rune slice and panic
  with `index out of range`. The bug was not Chinese-specific — a plain ASCII
  line failed the same way — but wide runes fill a row with fewer characters, so
  Chinese text reached it after a couple of keystrokes. Upgraded to `bubbles`
  v2.2.2, which fixes it; `TestComposerRendersEveryCharacterAfterInLineEdit`
  fails on the old version and passes on the new one.

- A model stuck reissuing the same tool call with the same arguments now gets a
  short reminder on the 3rd, 5th, and 8th attempt and has that call refused on
  the 12th, ending the turn with a readable error instead of spending every
  remaining turn on identical requests. The reminder is appended at the end of
  the conversation, so the cached prefix is untouched.

- Compaction no longer re-fires on the next turn right after summarizing: the
  transcript must grow by a further 20% before it is compacted again.

- Context-size estimation now counts an inline image as a fixed 1,500-token
  reserve instead of its base64 payload. A single screenshot used to estimate
  as ~100k tokens of text, past the default compaction trigger, which started a
  summarisation loop the moment a user attached one.

### Performance

- Removed two startup stalls: the OpenRouter model catalogue now loads when
  `/model` opens, and the terminal background is detected asynchronously
  instead of blocking the first frame for up to four seconds.

### Security

- Cleared every reported dependency advisory by upgrading `golang.org/x/net`
  to v0.63.0, `golang.org/x/sys` to v0.50.0, and `golang.org/x/text` to v0.44.0,
  and by moving the module to Go 1.26.11. Building Neo now requires Go 1.26.

- Hardened the workflows: every third-party action is pinned to a commit SHA,
  checkouts no longer persist credentials into the workspace, `pages: write`
  and `id-token: write` are limited to the deploy job, the nightly workflow
  passes the default branch through `env` instead of expanding it into a shell
  script, and the release job builds without a module cache.

## [v0.6.2] - 2026-08-04

### Security

- Redacted Google and OpenRouter API keys everywhere Neo sanitizes provider
  credentials, including verbose logs and nested payloads.
- Rejected project `AGENTS.md` symlinks that resolve outside the workspace while
  continuing to support symlinks whose targets remain inside it.

### Fixed

- Updated the embedded Anthropic default model to match the documented default.
- Preserved the OpenAI provider identity in saved sessions so resumed chats use
  the correct authentication mode.
- Centralized `/clear` conversation reset so it clears the full conversation,
  workflow, tool, image, and usage state consistently.

## [v0.6.1] - 2026-08-04

### Fixed

- Kept interactive `@` file references rooted at Neo's effective startup
  working directory, including resumed sessions, instead of walking up to an
  ancestor Git repository.

## [v0.6.0] - 2026-08-01

### Added

- Added visible, configurable named phases with built-in `/design`, `/plan`,
  `/build`, and `/review` prompts. Phases reuse the existing workflow UI and can
  be extended or overridden through `neo.yaml`. They activate only through
  explicit slash commands.

### Fixed

- Rejected unsupported workflow actions instead of reporting success for an
  update the UI could not apply.

## [v0.5.1] - 2026-07-31

### Changed

- Polished the workflow experience in the TUI: simpler splash and welcome,
  clearer activity and workflow hints, and tidier command and file pickers.

## [v0.5.0] - 2026-07-29

### Changed

- Replaced permission modes and command-risk heuristics with default-empty,
  interactive-only `tool_approvals`. Neo now treats its VM or sandbox as the
  security boundary. Existing `permissions:` config and `--permission` fail
  with migration guidance.

## [v0.4.1] - 2026-07-25

### Changed

- Default models updated to current generations: Anthropic now defaults to Claude Opus 5, OpenAI to GPT-5.6 Sol, and the OpenRouter fallback to Claude Sonnet 5. The `/model` picker lists the GPT-5.6 and Claude 5 families.

### Fixed

- Transcript scrolling in the TUI. The alt screen hides the terminal's own scrollback and mouse reporting was disabled, so the mouse wheel did nothing and history was unreachable. Wheel scrolling now works, with `shift+↑/↓` and `shift+home/end` as keyboard alternatives. Drag selection still works with shift held.

## [v0.4.0] - 2026-07-23

### Added

- Headless mode for running Neo non-interactively from the CLI.
- Parallel execution for safe tool calls and factory inspect subagents, with visible parallel execution groups in the TUI.

### Changed

- Trusted mode now skips approval prompts for dangerous bash commands too, so it auto-allows all bash; path-shaped file tools still cannot escape the workspace root.
- Simplified the release process to use the verified installer only.
- Removed low-value product complexity and unused tools/permissions commands from the TUI.

### Fixed

- Restored chat history scrolling in the TUI.

## [v0.3.0] - 2026-07-19

### Highlights

- Added Google Gemini as a first-class provider and made provider and model switching available from the running TUI.
- Added active steering and queued follow-up instructions so users can redirect ongoing work without breaking tool-call transcripts.
- Expanded the coordinator experience with live subagent activity, configurable subagent backends, slash-invoked skills, and clearer workflow progress.
- Reworked the terminal UI around concise tool receipts, visible task status, native text selection, and less noisy output.

### Added

- Google Gemini provider support.
- Runtime provider and model switching from the model picker.
- Active-turn steering and queued follow-up messages.
- Live subagent activity in the terminal UI.
- Configurable provider and model selection for subagents.
- Skill invocation through slash commands.
- Lightweight performance regression budgets for workflows, tools, and the TUI.
- A public Astro and Starlight website for product guidance and generated developer documentation.

### Changed

- Made workflow checklists model-driven and separated compact progress from normal output.
- Defaulted routine tool activity to concise receipts, with verbose output remaining configurable.
- Simplified provider, CLI, composer, agent-loop, and landing-page internals.
- Replaced `.neo/commands` prompt templates and `features.prompt_commands` with slash-invoked `.neo/skills`; existing custom prompt commands must be migrated to skills.
- Removed project memory and its bundled repository artifacts to keep Neo focused on explicit project instructions, skills, and local sessions.
- Moved website deployment from Cloudflare Pages to GitHub Pages while keeping DNS independent.

### Fixed

- Hardened built-in tools, permission checks, and provider-native tool argument decoding.
- Restored native terminal text selection and kept selection working across TUI updates.
- Reported recovered tool failures correctly and kept progress separate from assistant output.
- Ran independent `neo doctor` checks even when configuration loading fails.
- Removed redundant tool output, duplicate max-turn summaries, and dead retry wrappers.
- Installed Neo onto a runnable path more reliably.

## [v0.2.2] - 2026-07-07

### Changed

- Published a patch release to verify the configured Homebrew tap token updates `owainlewis/homebrew-tap` during release automation.

## [v0.2.1] - 2026-07-07

### Fixed

- Skipped Homebrew cask publishing when the `HOMEBREW_TAP_GITHUB_TOKEN` secret is not configured, allowing GitHub release publishing to complete successfully.

## [v0.2.0] - 2026-07-07

### Highlights

- Added persistent chat sessions with resume support, a session browser, transcript search, and persisted token usage.
- Added OpenAI and OpenRouter provider support, including OpenAI subscription/device-code login and live OpenRouter model catalogue loading.
- Added robust tool permissions, approval inspection improvements, workspace path sandboxing, and safer atomic file writes.
- Added project memory, git-context injection, prompt-file slash commands, slash command autocomplete, and a coordinator-worker/subagent workflow experience.
- Added transcript compaction controls, capped tool-result transcript content, expanded truncated tool results, and better cancellation handling.
- Added update tooling for stable and nightly channels, plus release/nightly automation and generated developer documentation.

### Added

- `neo doctor` command for environment diagnostics.
- Saved-session transcript search.
- TUI model picker, permissions picker, file reference picker, and bang-shell alias.
- Structured `slog` tracing.
- Manual project memory support and README guidance for memory configuration.
- Repository skills for coordinator-worker and backlog-manager workflows.
- Nightly build workflow and generated developer docs checks in CI.

### Changed

- Improved TUI workflow status feedback, paste handling, display-width-aware truncation, and approval previews.
- Reused shared atomic file-write helpers across tools.
- Refreshed README onboarding, provider authentication documentation, robust-core usage documentation, roadmap, and developer docs.

### Fixed

- Cleaned up installer temporary directories safely.
- Preserved file modes on atomic writes.
- Preserved transcript invariants when turns are cancelled.
- Stopped bash child processes on cancellation.
- Honored `Retry-After` headers with jitter.
- Validated `openai_auth` configuration modes.
- Blocked `/memory` while a turn is active.
- Capped and streamed large tool results safely.

## [v0.1.0] - 2026-05-30

- Initial public release.

[v0.7.0-dev.20261126]: https://github.com/morler/neo/compare/v0.6.2...v0.7.0-dev.20261126
[v0.6.2]: https://github.com/owainlewis/neo/compare/v0.6.1...v0.6.2
[v0.6.1]: https://github.com/owainlewis/neo/compare/v0.6.0...v0.6.1
[v0.6.0]: https://github.com/owainlewis/neo/compare/v0.5.1...v0.6.0
[v0.5.1]: https://github.com/owainlewis/neo/compare/v0.5.0...v0.5.1
[v0.5.0]: https://github.com/owainlewis/neo/compare/v0.4.1...v0.5.0
[v0.4.1]: https://github.com/owainlewis/neo/compare/v0.4.0...v0.4.1
[v0.4.0]: https://github.com/owainlewis/neo/compare/v0.3.0...v0.4.0
[v0.3.0]: https://github.com/owainlewis/neo/compare/v0.2.2...v0.3.0
[v0.2.2]: https://github.com/owainlewis/neo/compare/v0.2.1...v0.2.2
[v0.2.1]: https://github.com/owainlewis/neo/compare/v0.2.0...v0.2.1
[v0.2.0]: https://github.com/owainlewis/neo/compare/v0.1.0...v0.2.0
[v0.1.0]: https://github.com/owainlewis/neo/releases/tag/v0.1.0
