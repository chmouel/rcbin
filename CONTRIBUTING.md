# Contributing to rc

This file covers the contributor workflow. Architecture and project rules live
in [`AGENTS.md`](./AGENTS.md).

## Setup

```bash
git clone https://github.com/chmouel/rcbin
cd rcbin
make build
make check
```

You need Go, using the version in [`go.mod`](./go.mod). Linting uses
[`golangci-lint`](https://golangci-lint.run) v2.

## Build and test

Use the Makefile for normal work:

```bash
make build   # build ./bin/rc
make test    # go test ./...
make race    # go test -race ./...
make lint    # gofmt check + go vet + golangci-lint
make check   # lint + test
make cross   # linux/darwin, amd64/arm64
make help    # list targets
```

Raw commands, when needed:

```bash
go build ./...
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
golangci-lint run
```

Run `go mod tidy` after changing imports.

## Pre-commit

```bash
pre-commit install
pre-commit install --hook-type pre-push
pre-commit run --all-files
```

The hooks run gofmt, vet, golangci-lint, `go mod tidy`, and the test suite on
pre-push.

## Pull requests

- Keep diffs tight.
- Add or update tests for behavior changes.
- Prefer table-driven tests.
- Update `README.md`, `AGENTS.md`, or `examples/config.toml` when behavior,
  conventions, or configuration schema change.
- CI must pass: lint, the test matrix, race/coverage jobs, and GoReleaser config
  checks.

## Commit messages

Use Commitizen-style subjects without scopes:

```text
feat: add completion command
fix: preserve status column
```

Keep the subject short. Use a body only when it helps explain the user-visible
change.

## Releases

Releases are built by GoReleaser when a `vX.Y.Z` tag is pushed. Check the config
locally before cutting a release:

```bash
goreleaser check
goreleaser release --snapshot --clean
```
