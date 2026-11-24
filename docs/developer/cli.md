# CLI

## Commands

| Command | Description |
| --- | --- |
| `neo` | Open interactive chat mode. |
| `neo chat` | Open interactive chat mode explicitly. |
| `neo run [options] <prompt>` | Run one headless prompt and exit without session persistence. |
| `neo agents` | List discovered agent prompts and where each came from. |
| `neo sessions` | List saved chat sessions. |
| `neo doctor` | Check local config, credentials, sessions, git, and workspace readiness. |
| `neo sessions search <query>` | Search saved session transcripts locally. |
| `neo resume <id>` | Resume a saved chat session. |
| `neo login` | Log in to an OpenAI ChatGPT/Codex subscription with device-code auth. |
| `neo logout` | Remove stored OpenAI subscription credentials. |
| `neo version` | Print the build-time Neo version. Also available as `neo -v` and `neo --version`. |
| `neo help` | Print usage. |

## Global flags

| Flag | Description |
| --- | --- |
| `--agent <name>` | Replace the built-in system prompt with `<name>.md` from `~/.neo/agents/` or `.neo/agents/`. Accepted anywhere in the arguments, before or after a subcommand, and in `--agent=name` or `--agent name` form. |

`--agent` is extracted before subcommand dispatch, so it works with `neo`,
`neo chat`, `neo run`, and `neo resume`. An unknown name exits non-zero and
lists what exists rather than falling back to the coding prompt. Sessions record
the agent they were started with, so `neo resume` keeps it unless a different
`--agent` is given.

## Environment

- `ANTHROPIC_API_KEY` is required when `provider: anthropic`.
- `OPENAI_API_KEY` is required when `provider: openai` uses `openai_auth: api_key`.
- `OPENROUTER_API_KEY` is required when `provider: openrouter`.
- `GOOGLE_API_KEY` is required when `provider: google`.
- A custom provider declared under `providers:` reads the variable named by its `api_key_env`, or none when that key is omitted.
- `openai_auth: subscription` uses stored ChatGPT/Codex device-code credentials created by `neo login` instead of an API key.

## Runtime Notes

- `neo` with no subcommand defaults to chat.
- `neo run` executes one prompt without opening the TUI, prints the final answer, and exits. It is intended for scripts and eval harnesses.
- `neo run` applies a `10m` timeout, does not create or update sessions, and supports `--json` for a machine-readable summary containing elapsed time and tool counts. The summary also carries `requests`, the provider calls the run actually made, and `usage` with the same `input_tokens`, `cache_creation_tokens`, `cache_read_tokens`, and `output_tokens` figures a saved session records. `requests` counts what crossed the provider boundary, so a compaction summary call is included even though the agent loop never asked for it, and `usage` sums every response including that one. Both fields are always present, including on a failed run.
- Headless runs receive the standard tool registry and do not use interactive `tool_approvals`. Run Neo inside a VM or sandbox that provides the required filesystem, process, network, and credential boundaries.
- The removed `--permission` option returns migration guidance instead of being silently accepted.
- `neo run` accepts prompt text as arguments. Piped stdin is the prompt when no argument is given (`cat prompt.md | neo run --json`), and is prepended to the arguments when one of them is `-` (`git diff | neo run "Review this diff" -`). stdin is not read otherwise, so a harness that holds an idle pipe open cannot block the run.
- `neo doctor` is local-first: it checks config, required credential presence, session store access, git availability, and whether the current directory is a git workspace without calling providers or printing secrets. A custom provider reports `pass` when its endpoint needs no key, and names the missing variable otherwise.
- The interactive `@` file picker indexes files under Neo's effective startup working directory and inserts paths relative to that directory.
- The OpenRouter and custom-provider model catalogues are fetched lazily when `/model` is opened (`GET <endpoint>/models` for a custom backend). Startup uses the provider default or static list, so a slow or offline catalogue request cannot delay the first frame.
- `/goal <objective>` sets the session's standing goal and starts the first turn; `/goal status`, `/goal pause`, `/goal resume`, and `/goal clear` manage it. See [Goals](goals.md).
- `/usage` prints the session's token accounting: the prompt total with its input, cache-write, and cache-read parts, the output total, and the share of the prompt served from the cache. The parts come from the same `llm.Usage` that is saved with the session, so they never overlap and the prompt total is their sum. A session with no completed response reports that instead of a row of zeros, and `/clear` resets the totals along with the transcript. Like `/clear`, it is idle-only.
- `/provider` switches backends mid-session. `/model` switches the model within the current backend. Both rewrite the config file Neo loaded (`~/.neo/config.yaml` when running on embedded defaults) so the next launch starts on the chosen backend and model: `/provider` sets `provider:` and `model:`, `/model` sets only `model:`. Credential errors and write failures abort the switch before the running backend changes.
- The TUI starts with the dark markdown style and asks Bubble Tea for the terminal background asynchronously. A terminal that does not answer the query leaves the dark style in place instead of blocking startup.
- `neo login` prints the OpenAI Codex device-code URL and one-time code, then stores refreshable subscription credentials in `~/.neo/auth.json` with file permissions intended to protect secrets.
- `neo logout` deletes the stored OpenAI subscription credential entry.
- Resuming a session attempts to change into the saved session cwd. If unavailable, Neo warns and stays in the current directory.
- Session saves happen after each user turn through the TUI `WithAfterSend` callback. When an interactive quit interrupts an active turn, Neo cancels the turn and waits for this save before exiting. If the save fails, Neo keeps the TUI open and lets the user retry by quitting again. A second interrupt while cancellation or a retry is pending forces an immediate exit.

## Process Boundary

`cmd/neo.run(args, streams)` is the command dispatcher. It installs signal
handling, writes through the supplied stdin, stdout, and stderr streams, and
returns the command's exit code. Command helpers return status codes rather
than terminating the process.

The top-level `main` function contains the only `os.Exit` call. Before reaching
that boundary, `execute` initializes logging, defers logging cleanup, and calls
`run`. This guarantees cleanup completes before both successful and failed
commands terminate.
