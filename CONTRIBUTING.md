# Contributing to rc

Thanks for your interest in improving `rc`. This document covers the essentials;
[`AGENTS.md`](./AGENTS.md) has the full architecture and conventions if you or
your agent want to dig deeper.

## Getting started

```bash
git clone https://github.com/chmouel/rc
cd rc
make build      # builds ./bin/rc
make check      # gofmt + go vet + golangci-lint + tests
```

You need Go (see the version in [`go.mod`](./go.mod)) and, for linting,
[`golangci-lint`](https://golangci-lint.run) v2.

## Development workflow

```bash
make build   # build ./bin/rc (embeds version via -ldflags)
make test    # go test ./...
make race    # go test -race ./...
make lint    # gofmt check + go vet + golangci-lint
make check   # lint + test
make cross   # cross-compile linux/darwin amd64+arm64
make help    # list all targets
```

Before opening a pull request, make sure the tree is clean:

- `gofmt -w` on changed files (or `golangci-lint fmt`).
- `go vet ./...` and `golangci-lint run` report no issues.
- `go test ./...` passes; run `make race` when touching concurrency.
- `go mod tidy` after changing imports.

Optional [pre-commit](https://pre-commit.com) hooks run these automatically:

```bash
pre-commit install
pre-commit install --hook-type pre-push
```

## Conventions

These are enforced in review; see `AGENTS.md` for the rationale.

- **External git/yadm.** Git and YADM are always invoked as external commands.
  Never reimplement them in-process.
- **Runner abstraction.** All process execution goes through `runner.Runner`
  (`runner.Exec` in production, `runner.Fake` in tests). Do not call `os/exec`
  directly outside `internal/runner`.
- **Output discipline.** Only machine output (e.g. `status --format waybar`)
  goes to stdout; logs and diagnostics go to stderr via `output.Reporter`. All
  ANSI goes through the `output` styled helpers so a single gate controls color.
- **Config is data.** Built-in defaults live in `internal/config/defaults.go`.
  Whenever you change the config model (types, defaults, validation, expansion,
  or merge behavior), update [`examples/config.toml`](./examples/config.toml) in
  the same change and confirm it still resolves with
  `rc config validate --config examples/config.toml`.
- **Minimal dependencies.** Prefer the standard library. Do not add a dependency
  without a concrete need.
- **Comments.** Comment only what needs clarification.

## Commit messages

Use [Commitizen](https://www.conventionalcommits.org/)-style messages without
scopes: `type: summary` (for example `feat: add completion command` or
`fix: preserve first status column`). Keep the subject concise and wrap the body
near 72 columns. Describe what changed in plain terms; skip low-level detail and
test plans.

## Pull requests

- Keep diffs surgical: change only what the task requires.
- Add or update tests for behavior changes; prefer table-driven tests.
- Update `README.md` and `AGENTS.md` when behavior or conventions change.
- CI (`.github/workflows/ci.yml`) must pass: lint, the test matrix, and the
  GoReleaser config check.

## Releases

Releases are produced by [GoReleaser](https://goreleaser.com) when a `vX.Y.Z`
tag is pushed. Validate locally with:

```bash
goreleaser check
goreleaser release --snapshot --clean
```
