# rc

A workstation orchestrator. `rc` keeps a personal machine in sync: it
synchronizes [YADM](https://yadm.io) and Git repositories, manages dotfile
symlinks and binaries, runs backups and OS/tool updates, reports a Waybar
status, and runs diagnostics.

It's very specific to my way of configuring my hosts, This used to be a large (hand written) Bash script and this has been vibe rewritted in Go. 

Conventions for contributors and agents are in [`AGENTS.md`](./AGENTS.md).

## Install

With [Homebrew](https://brew.sh):

```bash
brew install chmouel/tap/rc
```

Or build from source:

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
rc completion [bash|zsh|fish|powershell]
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

### Shell completion

`rc` ships shell completion via Cobra. Load it for your shell, for example:

```bash
# bash (current session)
source <(rc completion bash)
# zsh (current session)
source <(rc completion zsh)
# fish
rc completion fish | source
```

Run `rc completion <shell> --help` for instructions on installing it
permanently. The Homebrew formula installs completions automatically.

## Configuration

Configuration is layered. Global configuration is TOML, while host/profile
configuration uses the line-based host files under the YADM host root. Layers
merge in this order:

1. Built-in defaults (encoded in the binary).
2. Global file: `~/.config/rc/config.toml` (honors `XDG_CONFIG_HOME`).
3. `common` host profile.
4. Lexically sorted multi-host profiles (directories named like `hostA,hostB`).
5. The exact-host profile.

Host profiles live under `~/.config/yadm/hosts/<profile>` by default.
Scalars use the last specified value. Domain lists are keyed — links by target,
binaries by target, repositories by path, tasks by name — so later layers
override earlier ones deterministically, giving the exact host top priority.
See [`MIGRATION.md`](./MIGRATION.md) for the precise compatibility notes and
known differences from `rcold`.

The built-in defaults are deliberately neutral: they provide generic roots,
sync/tool/doctor settings, and OS/tool update tasks, but **no** Git provider,
repositories, YADM backup remote, or backup tasks. Put those in your global
file. A complete, ready-to-adapt template lives in
[`examples/config.toml`](./examples/config.toml):

```bash
cp examples/config.toml ~/.config/rc/config.toml
# then edit the provider, repositories, yadm remote, roots, and backups
```

A minimal host profile can use these host files:

```text
# ~/.config/yadm/hosts/common/rc
git
readline/inputrc ~/.inputrc
.local/share/desktop-config/krb5/krb5.conf /etc/krb5.conf
?$GOPATH/bin/goimports .local/bin/goimports
```

```text
# ~/.config/yadm/hosts/common/chmouzies
git/gh-clone
graphical/copy-path :: rf
perso/x :: .config/zsh/funcs/$HOST/x
```

```text
# ~/.config/yadm/hosts/common/repobins
perso/myrepo/bin/tool
```

```text
# ~/.config/yadm/hosts/common/extra-dirs
perso/lazyworktree post_update={ make build }
perso/x always={ echo hi | cat }
```

Host profiles may also carry payload files that are linked directly:

| Host path | Linked target |
| --- | --- |
| `emacs/init.el` | `${emacs}/lisp/init-local.el` |
| `shell/init.zsh` | `${zsh}/hosts/${HOST}.sh` |
| `shell/post.zsh` | `${zsh}/hosts/${HOST}-post.sh` |
| `shell/functions/*` | `${zsh}/functions/hosts/${HOST}/<name>` |
| `bin/*` | `${desktop_bin}/<name>` |

For singleton files (`emacs/init.el`, `shell/init.zsh`, `shell/post.zsh`), the
first matching profile wins, matching the old script. For directories such as
`shell/functions` and `bin`, later matching profiles override earlier files with
the same basename. During `rc link`, files under `${rc}/systemd` are also linked
into `${systemd_user}` when that target directory exists.

`rc link` recreates `${desktop_bin}` before linking binaries. Any unmanaged
files or directories inside `${desktop_bin}` are deleted, so reserve that
directory for rc-managed desktop scripts.

Paths support a leading `~` and `${HOME}`, `${HOST}`, and `${GOPATH}` anywhere.
An unset referenced variable is a validation error. Commands prefer
`argv = [...]`; a task or hook may instead use `shell = "..."`. Exactly one form
is allowed.

Validate a merged configuration with:

```bash
rc config validate
```

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

## License

Released under the [MIT License](./LICENSE). © 2026 Chmouel Boudjnah.
