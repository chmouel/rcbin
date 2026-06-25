# rc

A workstation orchestrator. `rc` keeps a personal machine in sync: it
synchronizes [YADM](https://yadm.io) and Git repositories, manages dotfile
symlinks and binaries, runs backups and OS/tool updates, reports a Waybar
status, and runs diagnostics.

It is a focused Go rewrite of a large Bash script. Conventions for contributors
and agents are in [`AGENTS.md`](./AGENTS.md).

## Install

```bash
go build -o rc ./cmd/rc
# move rc onto your PATH, e.g.
install -m 0755 rc ~/.local/bin/rc
```

## Usage

```text
rc status [--format text|waybar]
rc sync   [--changed-only] [--repo PATH...] [--skip-yadm | --yadm-only]
rc link
rc backup [TASK...]
rc update [TASK...]
rc doctor [--offline]
rc run
rc config validate
rc migrate --legacy-root PATH --output-root PATH
```

Running `rc` with no arguments prints help and exits successfully without
making any changes.

### Global options

```text
--config PATH        path to the global config file
--host NAME          override the detected hostname
--verbose            enable verbose logging
--no-color           disable colored output
--non-interactive    never launch lazygit, Emacs, or aicommit
--dry-run            show actions without performing them
```

- All mutating commands honor `--dry-run`.
- For a dirty repository the prompt reads a single keypress (no Enter needed):
  `m` Magit, `l` lazygit (default), `s` skip, `a` aicommit, `c` direct commit,
  `q` quit. After an action rc asks `Would you like to continue? ([Y]es/[n]o/[b]ack)`
  before pulling and pushing; `b` returns to the menu, and `n`, `q`, or `Ctrl+C`
  aborts the run cleanly.
- In `--non-interactive` mode, dirty or conflicted repositories become
  actionable errors while clean repositories continue.
- Machine output (such as `status --format waybar`) is written to stdout; logs
  and diagnostics go to stderr.
- Output uses colored section rules, highlighted menu hotkeys, and
  [Nerd Font](https://www.nerdfonts.com/) glyphs for status icons, so a patched
  font is recommended. Color is enabled only when stderr is a terminal; it is
  disabled automatically when output is piped/redirected, when `NO_COLOR` is
  set, or with `--no-color`.
- Exit codes: `0` success, `1` operational/validation failure, `2` invalid CLI
  usage.

### Common workflows

```bash
rc run                       # sync, then link, then backup
rc sync --changed-only       # only touch repositories with local changes
rc status --format waybar    # JSON for a Waybar custom module
rc update                    # run system/tool update tasks
rc doctor                    # check the environment
```

## Configuration

Configuration is layered TOML. Layers merge in this order:

1. Built-in defaults (encoded in the binary).
2. Global file: `~/.config/rc/config.toml` (honors `XDG_CONFIG_HOME`).
3. `common` host overlay.
4. Lexically sorted multi-host overlays (directories named like `hostA,hostB`).
5. The exact-host overlay.

Host overlays live under `~/.config/yadm/hosts/<profile>/rc.toml` by default.
Scalars use the last specified value. Domain lists are keyed — links by target,
binaries by target, repositories by path, tasks by name — so later layers
override earlier ones deterministically, giving the exact host top priority.

The built-in defaults are deliberately neutral: they provide generic roots,
sync/tool/doctor settings, and OS/tool update tasks, but **no** Git provider,
repositories, YADM backup remote, or backup tasks. Put those in your global
file. A complete, ready-to-adapt template lives in
[`examples/config.toml`](./examples/config.toml):

```bash
cp examples/config.toml ~/.config/rc/config.toml
# then edit the provider, repositories, yadm remote, roots, and backups
```

A minimal host overlay:

```toml
version = 1

[[links]]
source_root = "rc"
source = "git"
target = "~/.config/git"

[[bins]]
source_root = "rc"
source = "git/gh-clone"
target = "gh-clone"
discover_completion = true

[[repositories]]
path = "perso/lazyworktree"

[repositories.hooks.post_update]
argv = ["make", "build"]
```

Paths support a leading `~` and `${HOME}`, `${HOST}`, and `${GOPATH}` anywhere.
An unset referenced variable is a validation error. Commands prefer
`argv = [...]`; a task or hook may instead use `shell = "..."`. Exactly one form
is allowed.

Validate a merged configuration with:

```bash
rc config validate
```

## Migrating from the legacy formats

The old script read several bespoke line-based files (`rc`, `chmouzies`,
`repobins`, `extra-dirs`). Convert them to TOML overlays:

```bash
rc migrate --legacy-root ~/.config/yadm/hosts --output-root ~/.config/yadm/hosts
```

This writes a generated `rc.toml` per profile with a header and warning comments.
Review the output before relying on it; the runtime never reads the legacy
formats.

## Development

```bash
make build       # build ./bin/rc
make test        # go test ./...
make race        # go test -race ./...
make lint        # gofmt check + go vet + golangci-lint
make check       # lint + test
make cross       # cross-compile linux/darwin (amd64/arm64)
make help        # list all targets
```

Or use the Go toolchain directly:

```bash
go build ./...
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
golangci-lint run
```

Optional git hooks are managed with [pre-commit](https://pre-commit.com):

```bash
pre-commit install                 # run fast checks on commit
pre-commit install --hook-type pre-push   # run the test suite on push
pre-commit run --all-files
```

Releases are produced by [GoReleaser](https://goreleaser.com) on tag push
(`vX.Y.Z`) via GitHub Actions. Validate the config and dry-run locally with:

```bash
goreleaser check
goreleaser release --snapshot --clean
```

See [`AGENTS.md`](./AGENTS.md) for architecture and conventions.
