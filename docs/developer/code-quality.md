# Code Quality Tooling

Two optional tools sit outside `go test` and `golangci-lint`:

- **qlty** is the local, offline linter aggregator. It runs the plugins
  configured in `.qlty/qlty.toml` (gofmt, golangci-lint, actionlint, shellcheck,
  trufflehog, osv-scanner, zizmor) and reports whatever they find.
- **supercov** asks Jev, a model-based reviewer, for per-file judgments
  (long methods, magic values, duplication) and for coverage of a test command.
  It needs network access and an API key.

Neither runs in CI. `.github/workflows/ci.yml` stays on `go build`, `go test
-race`, `go vet`, `gofmt -l`, and golangci-lint; qlty and supercov are for
local review before a commit or for a deeper look at one area.

## qlty

```sh
qlty check --all           # every plugin over the whole repository
qlty check --filter gofmt  # one plugin
```

`exclude_patterns` in `.qlty/qlty.toml` decides what is linted. Keep it narrow:
an entry such as `"**/config/**"` also hides a real Go package named `config`,
so the file is checked only by the plugins CI pins.

## supercov

### Credentials

supercov sends its requests to `<TYPESAFE_BASE_URL>/v1/systemone`. Three
variables matter:

| Variable | Meaning |
| --- | --- |
| `TYPESAFE_API_KEY` | The key. Required for anything that scores code; reading a saved snapshot never needs it. |
| `TYPESAFE_BASE_URL` | Host serving the same API. Defaults to `https://api.typesafe.ai`. |
| `TYPESAFE_DEFAULT_MODEL` | Model id requested through that gateway. |

Because the path `v1/systemone` is appended, the base must not already end in
`/v1` — `https://openrouter.ai/api/v1` becomes `/api/v1/v1/systemone` and fails.
To route through OpenRouter, the base is exactly `https://openrouter.ai/api`:

```sh
export TYPESAFE_API_KEY=<your-key>
export TYPESAFE_BASE_URL=https://openrouter.ai/api
export TYPESAFE_DEFAULT_MODEL=<model-id>
```

A wrong base URL is reported as an upstream HTTP error and the summary reads
`Quality not scored: no file was assessed.`, which is easy to mistake for a
scoring problem rather than a configuration one. If a run cached those failures,
`supercov quality clean` clears them before retrying.

### Smoke test

Score one file before spending a key on the whole repository:

```sh
supercov quality cmd/neo/doctor.go
```

A working setup prints the score, the checks, and the lines they fired on, for
example `Quality fair (7.8/10) over 1 file.` `just quality` runs the same
command and fails early with a clear message when `TYPESAFE_API_KEY` is unset.
Pass a different path to `just quality <path>`.

### The useful queries

| Command | Answers |
| --- | --- |
| `supercov quality` | Score every source file; saves a snapshot. |
| `supercov quality gaps` | Only the files something fired on. |
| `supercov quality file <path>` | Every check for one file, from the newest saved snapshot. |
| `supercov quality patch --base origin/main` | What the change introduced, not what predates it. |
| `supercov security` | Security findings over the same files. |
| `supercov -- go test ./...` | Line, branch, and MC/DC coverage of a test command. |
| `supercov runs latest gaps` | Files the tests leave under-exercised. |

Model judgments are opinions to check against the file, not CI gates. Read the
line each one points at before changing code for it.

### Coverage has one known blind spot here

`internal/tools` mixes `package tools` and `package tools_test` test files in the
same directory, and Supercov's Go instrumenter cannot handle that shape: it
generates the `__supercovTest` shim into the internal package only, so the
external test files fail to compile and the whole package drops out of the run.

```
$ go test ./...                      # fine: 862 tests pass
$ supercov -- go test ./...          # FAIL _test [build failed]
                                     # undefined: __supercovTest
[supercov] 1 test package(s) wrote no evidence and are absent from this run:
           internal/tools
```

The run still exits non-zero and its summary marks the coverage diagnostic, but
the missing package is easy to miss, so `internal/tools` counts as unmeasured
in a Supercov run. Use `go test -cover ./internal/tools/` for that package until
the instrumenter handles mixed test packages, and do not read a Supercov total
as covering it.

`.supercov/` holds instrumented copies and snapshots, and ignores itself
through its own `.gitignore`. Do not add it to the repository's `.gitignore`.
