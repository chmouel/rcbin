# AGENTS.md

Guidance for agents and contributors working in this repository.

## Project

`rc` is a workstation orchestrator. It syncs YADM and Git repositories, manages
dotfile links and binary links, runs backups and update tasks, emits Waybar
status, and runs diagnostics. User-facing behavior belongs in `README.md`.

This repository is named `chmouel/rcbin`. The Go module and command remain
`github.com/chmouel/rc` and `rc`.

## Module and dependencies

- Go version: see `go.mod`.
- Module path: `github.com/chmouel/rc`.
- Dependencies stay minimal:
  - `github.com/spf13/cobra` for the CLI;
  - `github.com/pelletier/go-toml/v2` for TOML;
  - `golang.org/x/term` for terminal detection and raw-mode prompts.
- Prefer the standard library. Do not add dependencies without a concrete need.

## Layout

```text
cmd/rc/                 startup, signal handling, exit codes
internal/app/           cobra command tree and orchestration
internal/config/        TOML, host profiles, merge, validation, path expansion
internal/host/          hostname detection and profile selection
internal/runner/        external command execution and cancellation
internal/output/        stderr logs, stdout results, color, Waybar JSON
internal/linker/        links, manifests, binaries, completions, clones
internal/repo/          git state, discovery, sync, hooks
internal/yadm/          YADM status and sync
internal/commitui/      lazygit, Emacs, aicommit, direct commit, prompt
internal/maintenance/   backups and update tasks
internal/doctor/        diagnostics
```

## Core rules

### External commands

Git and YADM must run as external commands so rc respects SSH agents, credential
helpers, hooks, signing, aliases, and user Git config. Do not reimplement Git
in-process.

All process execution goes through `runner.Runner`. Production uses
`runner.Exec`; tests use `runner.Fake`. Do not call `os/exec` directly outside
`internal/runner`.

### Output

Machine output, such as `status --format waybar`, is the only output written to
stdout. Logs and diagnostics go to stderr through `output.Reporter`.

All ANSI styling must go through `Reporter.paint` or the styled helpers:
`Bold`, `Dim`, `Accent`, `Key`, `Good`, `Bad`, `Caution`, `Rule`,
`SuccessLine`, and `Arrow`. `output.ColorFor` disables color when `--no-color`
is set, `NO_COLOR` is present, or stderr is not a TTY. Nerd Font glyphs belong
only on the human stderr surface, never in machine stdout.

### Exit codes

- `0`: success
- `1`: operational or validation failure
- `2`: invalid CLI usage

In `internal/app`, wrap operational failures returned from `RunE` with `op(err)`.
Cobra flag and argument errors map to `2`.

### Configuration

Built-in defaults live as structured data in `internal/config/defaults.go`.
Global configuration is TOML. Host profiles use the line-based files `rc`,
`chmouzies`, `repobins`, and `extra-dirs`, plus payloads under `emacs/`,
`shell/`, and `bin/`. Keep host-profile targets compatible with `rcold`.

Keep `examples/config.toml` in sync with the config model. Update it whenever
you change config types, defaults, validation, expansion, merge behavior, fields,
roots, or task types. After editing it, confirm it resolves with:

```bash
rc config validate --config examples/config.toml
```

Only path fields expand variables. Supported expansion is a leading `~` plus
`${HOME}`, `${HOST}`, and `${GOPATH}` anywhere in the path. Command bodies
(`argv` and `shell`) are never expanded, so shell variables survive. Referencing
an unset variable is a validation error.

### Determinism and comments

Merge and resolve should produce config-ordered, keyed collections. Prefer
deterministic output and table-driven tests.

Comment only when code needs clarification. Do not narrate obvious code.

## Configuration merge model

Layers merge in this order:

1. Built-in defaults.
2. Global TOML file (`~/.config/rc/config.toml`).
3. `common` host profile.
4. Lexically sorted multi-host profiles.
5. Exact-host profile.

Scalars are last-wins. Domain lists are keyed:

- links by target;
- bins by target;
- repositories by path;
- tasks by name.

Later layers override earlier keyed entries. Duplicate keys within one layer are
conflict errors.

Singleton payloads are the exception: `emacs/init.el`, `shell/init.zsh`, and
`shell/post.zsh` use the first matching profile, matching `rcold`. Directory
payloads such as `shell/functions/*` and `bin/*` keep normal later-profile
override behavior.

## Build, test, lint

Use the Makefile:

```bash
make build   # build ./bin/rc
make test    # go test ./...
make race    # go test -race ./...
make lint    # gofmt check + go vet + golangci-lint
make check   # lint + test
make cross   # linux/darwin, amd64/arm64
```

Raw equivalents:

```bash
go build ./...
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
golangci-lint run
GOOS=linux  go build ./cmd/rc
GOOS=darwin go build ./cmd/rc
```

Run `gofmt -w` on changed Go files. Run `make lint` after writing or modifying
Go code. Run `go mod tidy` after changing imports.

Documentation-only changes do not need build or test runs unless the edited docs
have a dedicated check.

## Tooling and CI

- golangci-lint uses `.golangci.yml` with the v2 schema. `gosec` excludes
  G204, G304, G301, and G306 because rc intentionally runs external commands via
  `runner.Runner` and reads or writes user-specified paths with conventional
  dotfile permissions. Do not silence other linters without justification.
- pre-commit uses `.pre-commit-config.yaml` for gofmt, vet, golangci-lint,
  `go mod tidy`, and tests on pre-push.
- GoReleaser uses `.goreleaser.yaml` with the v2 schema. Validate with
  `goreleaser check`; dry-run with `goreleaser release --snapshot --clean`.
- GitHub Actions runs CI in `.github/workflows/ci.yml` and releases from
  `.github/workflows/release.yml`.

## Testing notes

- Use `runner.NewFake()` to stub command output and assert recorded calls. It is
  concurrency-safe for `-race`.
- `internal/app` tests run the full command tree against a temporary `HOME` and
  explicit `--host`.
- Host-profile tests should create synthetic host files under a temporary
  `~/.config/yadm/hosts` root and validate through `config.Load`. Do not depend
  on live machine-specific host files.

## Git

Do not commit, amend, or rewrite history unless explicitly asked. The old
`~/.local/bin/rc` and the user's dotfiles are tracked by YADM (`yadm diff` and
`yadm status`).
