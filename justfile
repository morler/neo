# Neo justfile

# Version stamped into the binary at build time. Uses the nearest git tag
# (with dirty suffix if there are uncommitted changes), falling back to the
# short commit SHA, falling back to "dev" outside a git checkout.
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`

# List available recipes
default:
    @just --list

# Build the neo binary (stamps Version via -ldflags).
# -s -w drop the symbol table and DWARF data, which is ~9.6 MB on linux/amd64.
# The release build has always done this (.goreleaser.yaml), so a local binary
# now has the same size shape as the one users download. The cost is that a
# local panic trace has no line numbers; use build-debug when that matters.
[doc('build neo (stripped, release-shaped)')]
build:
    go build -ldflags "-s -w -X main.Version={{version}}" -o neo ./cmd/neo

# Unstripped build for debugging: keeps DWARF, so panics and Delve report
# file:line. Same source, same version stamp, far larger binary.
[doc('build neo unstripped (DWARF kept for debugging)')]
build-debug:
    go build -ldflags "-X main.Version={{version}}" -o neo.debug ./cmd/neo

# Run neo (dev mode)
dev *args:
    go run -ldflags "-X main.Version={{version}}" ./cmd/neo {{ args }}

# Run all tests
test:
    go test ./...

# Run tests with verbose output
test-verbose:
    go test -v ./...

# Build the release-shaped binary and run stable local microbenchmarks
performance:
    ./scripts/performance.sh

# Run codex in a Docker Sandbox for this repo, with host skills mounted
sandbox *args:
    ./scripts/sandbox.sh {{ args }}

# Install neo into a runnable bin directory (stamps Version).
# Strips symbols like build and the release artefacts do, so the installed
# binary is the release shape rather than 9.6 MB larger.
install:
    #!/usr/bin/env bash
    set -euo pipefail

    on_path() {
      case ":$PATH:" in
        *":$1:"*) return 0 ;;
        *) return 1 ;;
      esac
    }

    can_use_dir() {
      local dir="$1"
      mkdir -p "$dir" 2>/dev/null && [ -w "$dir" ]
    }

    bin_dir="${GOBIN:-$(go env GOBIN)}"
    if [ -z "$bin_dir" ]; then
      for candidate in "$HOME/.local/bin" "$HOME/bin" "$(go env GOPATH)/bin" "/usr/local/bin"; do
        if on_path "$candidate" && can_use_dir "$candidate"; then
          bin_dir="$candidate"
          break
        fi
      done
    fi
    if [ -z "$bin_dir" ]; then
      for candidate in "$HOME/.local/bin" "$HOME/bin" "$(go env GOPATH)/bin" "/usr/local/bin"; do
        if can_use_dir "$candidate"; then
          bin_dir="$candidate"
          break
        fi
      done
    fi
    if [ -z "$bin_dir" ]; then
      echo "error: could not find a writable install directory" >&2
      exit 1
    fi

    GOBIN="$bin_dir" go install -ldflags "-s -w -X main.Version={{version}}" ./cmd/neo
    echo "installed neo to $bin_dir/neo"
    if ! on_path "$bin_dir"; then
      echo "warning: $bin_dir is not on your PATH" >&2
      echo "add this to your shell rc: export PATH=\"$bin_dir:\$PATH\"" >&2
    fi

# Validate the install script with bash -n (syntax check)
check-install-script:
    bash -n install.sh && echo "install.sh syntax OK"

# Remove the built binaries
clean:
    rm -f neo neo.debug

# Format all Go source files
fmt:
    gofmt -w .

# Run static checks
lint:
    go vet ./...
    golangci-lint run

# Score one file with supercov as a smoke test (needs TYPESAFE_API_KEY)
quality file="cmd/neo/doctor.go":
    #!/usr/bin/env bash
    set -euo pipefail

    if [ -z "${TYPESAFE_API_KEY:-}" ]; then
      echo "error: TYPESAFE_API_KEY is not set" >&2
      echo "see docs/developer/code-quality.md for the required environment" >&2
      exit 1
    fi
    case "${TYPESAFE_BASE_URL:-}" in
      */v1|*/v1/) echo "warning: TYPESAFE_BASE_URL must not end in /v1 (supercov appends /v1/systemone)" >&2 ;;
    esac
    exec supercov quality "{{file}}"
# Print the version that would be stamped (for debugging)
print-version:
    @echo {{version}}
