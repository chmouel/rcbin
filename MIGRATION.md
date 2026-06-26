# Migration notes from `rcold`

This document is for maintainers and users moving from the legacy Bash script
(`rcold`) to the Go `rc` rewrite. It explains what the Go implementation keeps
compatible, what intentionally differs, and what to check before relying on the
new command on a workstation.

## Compatibility goal

The Go rewrite aims to preserve the **host profile model and resulting link
plan** from `rcold`, while keeping the safer Go command structure and error
handling where that was an explicit rewrite goal. It is not a byte-for-byte
replacement for the old Bash CLI.

The practical promise is:

- global configuration is TOML;
- host-specific configuration stays in the legacy YADM host tree;
- active host profiles under `~/.config/yadm/hosts` should validate and produce
  the expected links, binary links, repositories, and hooks;
- the Go CLI uses subcommands instead of the old single-command flag interface.

## What stays compatible

The Go runtime reads the same selected host profile directories as `rcold`:

1. `common`
2. matching multi-host directories such as `ibra,maximus`
3. the exact host directory

Hidden directories such as `.old` and `.claude` are ignored.

The following legacy profile files are supported:

| Host profile path | Go behavior |
| --- | --- |
| `rc` | Produces managed links. `?` marks optional entries. Bare targets go under `~/.config`; slashed relative targets go under `~`; absolute targets are privileged. |
| `chmouzies` | Produces binary links from the `chmouzies` root and discovers adjacent Zsh completions. |
| `repobins` | Produces binary links from local repositories, preserving absolute paths and looking up relative paths through `$GOPATH/src`, GitHub/GitLab GOPATH paths, and `~/git`. |
| `extra-dirs` | Adds repositories and `post_update={...}` / `always={...}` hooks. |
| `emacs/init.el` | Links to `${emacs}/lisp/init-local.el`. First matching profile wins, as in `rcold`. |
| `shell/init.zsh` | Links to `${zsh}/hosts/${HOST}.sh`. First matching profile wins. |
| `shell/post.zsh` | Links to `${zsh}/hosts/${HOST}-post.sh`. First matching profile wins. |
| `shell/functions/*` | Links top-level files to `${zsh}/functions/hosts/${HOST}/<name>`. Later profiles override earlier profiles with the same basename. |
| `bin/*` | Links top-level files to `${desktop_bin}/<name>`. Later profiles override earlier profiles with the same basename. |

The Go runtime also links `${rc}/systemd/*` into `${systemd_user}` when the
systemd user directory exists, matching `rcold` link setup behavior.
During `rc link`, `${zsh}` is linked to `${rc}/zsh` before host shell payloads
are applied. A real existing `${zsh}` directory is refused rather than
overwritten.

## Intentional and current differences

### CLI shape

`rcold` is a single Bash script with flags such as `-u`, `-l`, `-B`, and `-D`.
The Go rewrite uses subcommands:

| `rcold` flag | Go command |
| --- | --- |
| `-J` | `rc status --format waybar` |
| `-u` | `rc sync` |
| `-c` | `rc sync --changed-only` |
| `-l` | `rc link` |
| `-B` | `rc backup` |
| `-y` | `rc update` |
| `-D` | `rc doctor` |

This difference is intentional.

### Global config versus host config

`rcold` hard-coded most global paths and settings in the script. The Go rewrite
keeps global settings in `~/.config/rc/config.toml`, but keeps host-specific
profile data in the old line-based files under `~/.config/yadm/hosts`.

Host `rc.toml` overlays are no longer used, and `rc migrate` has been removed.

### Multi-host ordering

`rcold` uses filesystem `find` order for matching multi-host directories. The Go
rewrite sorts matching multi-host directories lexically for deterministic
behavior.

This only matters when more than one multi-host directory applies to the same
host and they set the same target.

### Duplicate entries

`rcold` effectively allows repeated link commands. The Go loader ignores exact
duplicate legacy entries but rejects conflicting duplicates in the same profile.

This keeps accidental exact repeats working while surfacing real ambiguity.

### Missing sources and real targets

`rcold` generally warns and continues when a required source is missing or when a
target already exists as a real non-symlink file. The Go linker treats those as
operational errors unless the entry is optional.

This is safer, but it is not byte-for-byte compatible with `rcold`.

### Desktop binary cleanup

`rcold` removes and recreates `${desktop_bin}` before linking host binaries,
chmouzies, and repobins. The Go linker currently uses a managed-link manifest
and does not blindly wipe unmanaged files.

If exact `rcold` cleanup is required, implement it deliberately in the linker
and document the data-loss implications.

## Defaults changed for compatibility

The Go defaults include old-script-compatible roots:

```toml
chmouzies = "~/.local/share/chmouzies"
desktop_bin = "~/.local/bin/desktop"
```

You only need to override these in global TOML if your workstation uses
different locations.

## Validation checklist

Run the normal test suite after changing migration or legacy-profile behavior:

```bash
go test ./...
go vet ./...
```

For workstation-specific validation, build a temporary binary and validate the
active host profiles:

```bash
tmpbin=$(mktemp /tmp/rcbin-test.XXXXXX)
go build -o "$tmpbin" ./cmd/rc
for h in civuole fedora fenouille ibra maximus; do
  "$tmpbin" --host "$h" config validate
done
rm -f "$tmpbin"
```

This confirms the live host tree is compatible with the Go loader. It does not
prove full byte-for-byte `rcold` behavior.
