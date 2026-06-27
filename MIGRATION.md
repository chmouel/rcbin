# Migration notes from `rcold`

This file is for users moving from the old Bash script, `rcold`, to the Go
command. It lists what stayed compatible, what changed, and what to check on a
workstation.

## Compatibility target

The Go command keeps the host profile model and resulting link plan compatible
with `rcold` where practical. It is not a byte-for-byte replacement.

Expected compatibility:

- global settings move to TOML;
- host-specific data stays under `~/.config/yadm/hosts`;
- active host profiles produce the expected links, binary links, repositories,
  and hooks;
- the CLI uses subcommands instead of the old flag-only interface.

## Host profiles

Profiles are read in this order:

1. `common`
2. matching multi-host directories such as `ibra,maximus`
3. the exact host directory

Hidden directories such as `.old` and `.claude` are ignored.

Supported host profile files:

| Host profile path | Go behavior |
| --- | --- |
| `rc` | Creates managed links. `?` marks optional entries. Bare targets go under `~/.config`; slashed relative targets go under `~`; absolute targets are privileged. |
| `chmouzies` | Links binaries from the `chmouzies` root and discovers adjacent Zsh completions. |
| `repobins` | Links binaries from local repositories, preserving absolute paths and resolving relative paths through `$GOPATH/src`, GitHub/GitLab GOPATH paths, and `~/git`. |
| `extra-dirs` | Adds repositories. Supports `post_update={...}` after a successful sync that changes `HEAD`, and `always={...}` after every attempted sync. |
| `emacs/init.el` | Links to `${emacs}/lisp/init-local.el`. First matching profile wins. |
| `shell/init.zsh` | Links to `${zsh}/hosts/${HOST}.sh`. First matching profile wins. |
| `shell/post.zsh` | Links to `${zsh}/hosts/${HOST}-post.sh`. First matching profile wins. |
| `shell/functions/*` | Links files to `${zsh}/functions/hosts/${HOST}/<name>`. Later profiles override the same basename. |
| `bin/*` | Links files to `${desktop_bin}/<name>`. Later profiles override the same basename. |

During `rc link`, `${desktop_bin}` is removed and recreated before binary links
are applied. Unmanaged files in that directory are deleted.

If `${systemd_user}` exists, rc links `${rc}/systemd/*` into it. rc also links
`${zsh}` to `${rc}/zsh` before applying host shell payloads. A real existing
`${zsh}` directory is refused instead of overwritten.

## Current differences

### CLI shape

`rcold` used one command with flags. The Go command uses subcommands:

| `rcold` flag | Go command |
| --- | --- |
| `-J` | `rc status --format waybar` |
| `-u` | `rc sync` |
| `-c` | `rc sync --changed-only` |
| `-l` | `rc link` |
| `-B` | `rc backup` |
| `-y` | `rc update` |
| `-D` | `rc doctor` |

### Global config

`rcold` kept many paths and settings in the script. The Go command keeps global
settings in `~/.config/rc/config.toml` and keeps host-profile data under
`~/.config/yadm/hosts`.

Host `rc.toml` overlays are no longer used. `rc migrate` has been removed.

### Multi-host ordering

`rcold` used filesystem `find` order for matching multi-host directories. The Go
loader sorts matching multi-host directories lexically. This only matters when
more than one matching profile sets the same target.

### Duplicate entries

The Go loader ignores exact duplicate host entries and rejects conflicting
duplicates in the same profile.

### Missing sources and real targets

`rcold` often warned and continued when a source was missing or a target already
existed as a real file. The Go linker reports these as operational errors unless
the entry is optional.

## Compatibility defaults

The Go defaults include old-script-compatible roots:

```toml
chmouzies = "~/.local/share/chmouzies"
desktop_bin = "~/.local/bin/desktop"
```

Override them in global TOML only if your workstation uses different paths.

## Validation

Run the normal checks after changing migration or host-profile behavior:

```bash
go test ./...
go vet ./...
```

To validate live host profiles with a temporary binary:

```bash
tmpbin=$(mktemp /tmp/rcbin-test.XXXXXX)
go build -o "$tmpbin" ./cmd/rc
for h in civuole fedora fenouille ibra maximus; do
  "$tmpbin" --host "$h" config validate
done
rm -f "$tmpbin"
```

This validates the live host tree against the Go loader. It does not prove
byte-for-byte `rcold` behavior.
