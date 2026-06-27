# rc

`rc` keeps my workstations in sync. It syncs YADM and Git repositories, manages
dotfile and binary links, runs backups and update tasks, prints Waybar status,
and checks the local environment.

The tool is tailored to my host setup. It started as a Bash script and now lives
as a Go command.

## Install

Nightly binary:

```bash
curl -fsSL https://raw.githubusercontent.com/chmouel/rcbin/main/install.sh | sh
```

From source:

```bash
go build -o rc ./cmd/rc
install -m 0755 rc ~/.local/bin/rc
```

## Usage

```text
rc
rc help
rc status [--format text|waybar]
rc sync   [--changed-only] [--repo PATH...] [--skip-yadm | --yadm-only]
rc link
rc backup [TASK...]
rc update [TASK...]
rc self-update
rc doctor [--offline]
rc run
rc config validate
rc completion [bash|zsh|fish|powershell]
```

Running `rc` without arguments runs the default `rc run` workflow. Use `rc help`
or `rc --help` to print help without making changes.

### Global options

```text
--config PATH        path to the global config file
--host NAME          override the detected hostname
--verbose            enable verbose logging
--no-color           disable colored output
--non-interactive    never launch lazygit, Emacs, or aicommit
--dry-run            show actions without performing them
```

- Mutating commands honor `--dry-run`.
- Machine output, such as `status --format waybar`, goes to stdout. Logs and
  diagnostics go to stderr.
- Color is enabled only when stderr is a terminal. It is disabled by `--no-color`,
  `NO_COLOR`, or redirected output.
- Exit codes: `0` success, `1` operational or validation failure, `2` invalid
  CLI usage.

### Common workflows

```bash
rc                           # sync, link, then backup
rc help                      # print help
rc sync --changed-only       # only touch repositories with local changes
rc status --format waybar    # JSON for a Waybar custom module
rc update                    # run system/tool update tasks
rc self-update               # update ~/.local/bin/rc and zsh completion
rc doctor                    # check the environment
```

When a repository is dirty, the interactive menu accepts one key: `m` Magit, `l`
lazygit, `d` diff, `s` skip, `a` aicommit, `c` direct commit, and `q` quit. In
`--non-interactive` mode, dirty or conflicted repositories become errors while
clean repositories continue.

### Self-update and completion

`rc self-update` updates `~/.local/bin/rc`.

- If the binary is a symlink to a `github.com/chmouel/rcbin` checkout, rc refuses
  a dirty checkout, then runs `git pull --ff-only` and `make build`.
- If it is a regular binary, rc downloads the `nightly` archive, verifies
  `checksums.txt`, and replaces the binary atomically.

After a successful update, rc regenerates Zsh completion at
`${zsh}/functions/hosts/${HOST}/_rc`, usually
`~/.config/zsh/functions/hosts/<host>/_rc`.

For shell completion in the current session:

```bash
source <(rc completion bash)
source <(rc completion zsh)
rc completion fish | source
```

Run `rc completion <shell> --help` for install instructions.

## Configuration

Configuration has two parts:

- global TOML at `~/.config/rc/config.toml`, honoring `XDG_CONFIG_HOME`;
- line-based host profiles under `~/.config/yadm/hosts/<profile>`.

Layers merge in this order:

1. Built-in defaults.
2. Global TOML.
3. `common` host profile.
4. Lexically sorted multi-host profiles, for example `hostA,hostB`.
5. Exact-host profile.

Scalars use the last value. Lists are keyed by domain: links by target, binaries
by target, repositories by path, tasks by name.

The built-in defaults include generic roots and built-in update tasks, but no Git
provider, repositories, YADM backup remote, or backup tasks. Start from the
example:

```bash
cp examples/config.toml ~/.config/rc/config.toml
rc config validate
```

### Host profiles

A small host profile can use these files:

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

`extra-dirs` lines start with a repository path. Relative paths resolve under
`${repo_base}` unless they expand to absolute paths. Add hooks after the path:
`post_update={ command }` runs after a successful sync that changes `HEAD`;
`always={ command }` runs after every attempted sync. Simple commands such as
`make build` run as argv commands. Pipes, redirects, variables, globs, and other
shell syntax run through the configured shell.

Host profiles can also carry files that rc links directly:

| Host path | Linked target |
| --- | --- |
| `emacs/init.el` | `${emacs}/lisp/init-local.el` |
| `shell/init.zsh` | `${zsh}/hosts/${HOST}.sh` |
| `shell/post.zsh` | `${zsh}/hosts/${HOST}-post.sh` |
| `shell/functions/*` | `${zsh}/functions/hosts/${HOST}/<name>` |
| `bin/*` | `${desktop_bin}/<name>` |

For `emacs/init.el`, `shell/init.zsh`, and `shell/post.zsh`, the first matching
profile wins. For `shell/functions/*` and `bin/*`, later matching profiles
override files with the same basename.

`rc link` recreates `${desktop_bin}` before linking binaries and deletes
unmanaged files there. Keep that directory for rc-managed desktop scripts.

Paths support a leading `~` plus `${HOME}`, `${HOST}`, and `${GOPATH}`. Unset
variables fail validation. Commands use either `argv = [...]` or `shell = "..."`,
never both.

See [`MIGRATION.md`](./MIGRATION.md) for `rcold` compatibility notes.

## Development

See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for build, test, lint, pull request,
and release workflow. See [`AGENTS.md`](./AGENTS.md) for architecture and agent
rules.

## License

Released under the [MIT License](./LICENSE). © 2026 Chmouel Boudjnah.
