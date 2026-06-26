# AGENTS.md

Guidance for AI agents and contributors working in this repository.

## What this is

`rc` is a workstation orchestrator: a Go rewrite of a large Bash script that
synchronizes YADM and Git repositories, manages dotfile symlinks, runs backups
and OS/tool updates, emits Waybar status, and runs diagnostics. User-facing
behavior is documented in `README.md`; read it before making non-trivial
changes.

## Module and dependencies

- Module path: `github.com/chmouel/rc`.
- Go: see `go.mod` (`go 1.26`).
- Dependencies are intentionally minimal:
  - `github.com/spf13/cobra` — CLI.
  - `github.com/pelletier/go-toml/v2` — TOML config.
- Do not add dependencies without a concrete need. Prefer the standard library.

## Layout

```
cmd/rc/                 process startup, signal handling, exit codes
internal/app/           cobra command tree and orchestration
internal/config/        TOML types, layered load, merge, validate, path expansion
internal/host/          hostname detection and profile selection
internal/runner/        external command execution + cancellation (+ Fake)
internal/output/        Reporter: text/stderr logs, stdout results, color, Waybar JSON
internal/linker/        links, managed manifest, binaries, completions, clones
internal/repo/          git state, discovery, synchronization, hooks
internal/yadm/          YADM status and synchronization
internal/commitui/      lazygit / Emacs / aicommit / direct commit / prompt
internal/maintenance/   backup and update task engines
internal/doctor/        diagnostic checks and summaries
```

## Core conventions

- **External git/yadm.** Git and YADM are always invoked as external commands so
  the tool respects the user's SSH agent, credential helpers, hooks, signing,
  aliases, and Git config. Never reimplement Git in-process.
- **Runner abstraction.** All process execution goes through
  `runner.Runner`. Production uses `runner.Exec`; tests use `runner.Fake`. Do not
  call `os/exec` directly outside `internal/runner`.
- **Output discipline.** Machine output (e.g. `status --format waybar`) is the
  only thing written to stdout. Logs and diagnostics go to stderr via
  `output.Reporter`. Keep stdout clean for commands that have machine output.
- **Color and styling.** All ANSI must go through `Reporter.paint`/the styled
  helpers (`Bold`/`Dim`/`Accent`/`Key`/`Good`/`Bad`/`Caution`/`Rule`/
  `SuccessLine`/`Arrow`) so a single gate controls it. Color is computed by
  `output.ColorFor`: off when `--no-color` is set, when `NO_COLOR` is present, or
  when stderr is not a TTY. Status icons use Nerd Font glyphs on the human
  (stderr) surface only; machine output on stdout stays plain.
- **Exit codes.** `0` success, `1` operational/validation failure, `2` invalid
  CLI usage. In `internal/app`, wrap operational failures returned from a
  command's `RunE` with `op(err)`; flag-parsing and argument-validation errors
  are produced by cobra and map to `2` automatically.
- **Config is data.** Built-in defaults live in `internal/config/defaults.go` as
  structured data. Global configuration is TOML, while host profiles use the
  line-based host files (`rc`, `chmouzies`, `repobins`, `extra-dirs`) parsed by
  `internal/config`. Host profiles may also carry host payloads under
  `emacs/`, `shell/`, and `bin/`; keep their targets compatible with `rcold`.
- **Keep `examples/config.toml` in sync.** It is the user-facing reference for the
  full config schema. Whenever you change the config model (`internal/config`
  types, defaults, validation, expansion, or merge behavior) or add/rename a
  field, root, or task type, update `examples/config.toml` in the same change so
  it stays valid and demonstrates the new shape. After editing it, confirm it
  still resolves with `rc config validate --config examples/config.toml`.
- **Path expansion.** Only `~` at the start and `${HOME}`, `${HOST}`, `${GOPATH}`
  (any position) are expanded, and only in path fields. Command bodies
  (`argv`/`shell`) are never expanded so shell `${VAR}` survives. An unset
  referenced variable is a validation error.
- **Determinism.** Merge/resolve produce config-ordered, keyed collections.
  Prefer deterministic output and table-driven tests.
- **Comments.** Comment only what needs clarification (non-obvious decisions,
  invariants). Avoid narrating obvious code.

## Configuration model (summary)

Layers merge in this order: built-in defaults → global TOML file
(`~/.config/rc/config.toml`) → `common` host profile → lexically sorted
multi-host profiles → exact-host profile. Scalars are last-wins. Domain lists
are keyed and later layers override earlier ones by key:

- links by target, bins by target, repositories by path, tasks by name.

Duplicate keys *within a single layer* are a conflict error. The singleton host
payloads `emacs/init.el`, `shell/init.zsh`, and `shell/post.zsh` are an
exception to normal layer override semantics: the first matching profile wins to
match `rcold`. Directory payloads (`shell/functions/*`, `bin/*`) keep normal
later-profile override behavior.

## Build, test, lint

A `Makefile` wraps the common workflows; prefer it for consistency:

```bash
make build   # build ./bin/rc (embeds version via -ldflags -X main.version)
make test    # go test ./...
make race    # go test -race ./...
make lint    # gofmt check + go vet + golangci-lint
make check   # lint + test
make cross   # cross-compile linux/darwin amd64+arm64
```

Equivalent raw commands:

```bash
go build ./...
go test ./...
go test -race ./...        # race detector (worker pool, fakes)
go vet ./...
gofmt -l .                 # list unformatted files (should be empty)
golangci-lint run          # config in .golangci.yml (golangci-lint v2)
GOOS=linux  go build ./cmd/rc
GOOS=darwin go build ./cmd/rc
```

Always run `gofmt -w` on changed files (or `golangci-lint fmt`). Keep the tree
`gofmt`-, `go vet`-, and `golangci-lint`-clean. Run `go mod tidy` after changing
imports.

### Tooling and CI

- **golangci-lint** (`.golangci.yml`, v2 schema). `gosec` excludes G204/G304/
  G301/G306 because the tool intentionally runs external commands via the Runner
  and reads/writes user-specified paths with conventional dotfile permissions.
  Do not silence other linters without justification.
- **pre-commit** (`.pre-commit-config.yaml`): fast checks on commit (gofmt, vet,
  golangci-lint, go mod tidy) and the test suite on pre-push.
- **GoReleaser** (`.goreleaser.yaml`, v2): builds linux/darwin amd64+arm64,
  archives, checksums, and publishes a Homebrew cask to `chmouel/homebrew-tap`
  (needs the `HOMEBREW_TAP_GITHUB_TOKEN` secret at release time). Validate with
  `goreleaser check`; dry-run with `goreleaser release --snapshot --clean`.
- **GitHub Actions**: `.github/workflows/ci.yml` (lint, test matrix on
  ubuntu+macos with race/coverage, GoReleaser config check) runs on push/PR;
  `.github/workflows/release.yml` runs GoReleaser on `v*` tags.


## Testing notes

- Use `runner.NewFake()` to stub command output and assert recorded calls;
  it is concurrency-safe for `-race`.
- `internal/app` tests run the full command tree against a temporary `HOME` and
  an explicit `--host` so they are hermetic.
- Host-profile tests should use synthetic host files under a temporary
  `~/.config/yadm/hosts` root and validate through `config.Load`; do not depend
  on live machine-specific host files.

## Git

This rewrite lives in its own repository. Do not commit, amend, or rewrite
history unless explicitly asked. The old `~/.local/bin/rc` and the user's
dotfiles are tracked by `yadm` (`yadm diff` / `yadm status`).
