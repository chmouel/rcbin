# `rc` Rewrite Investigation and Go Design

## Executive summary

`rc` is a 1,599-line Bash workstation orchestrator. It is not only a dotfile
linker: it also synchronizes YADM and Git repositories, offers interactive
commit workflows, performs backups and operating-system updates, emits Waybar
JSON, and diagnoses the local environment.

The current implementation has accumulated useful behavior, but its domains
share global state and depend on Bash error semantics. Configuration parsing is
duplicated, Git state is sometimes inferred from human-readable output, and
several observed command paths do not behave as their help text suggests.

The replacement should be a clean-break Go application in a dedicated
`~/git/perso/rc` repository. It should install one binary at
`~/.local/bin/rc`, use explicit subcommands, load layered TOML configuration,
and continue invoking the installed `git`, `yadm`, and interactive tools. It
should not use `go-git` or reproduce accidental shell behavior.

The first release targets Linux and macOS. WSL support is deferred.

## Investigation method

The investigation was repeated in three passes and reconciled before this
design was written.

1. **Behavioral reconstruction** inspected all 47 functions, 876 parsed shell
   command nodes, CLI branches, global variables, traps, and call paths.
2. **Compatibility audit** inspected every active `rc`, `chmouzies`,
   `repobins`, and `extra-dirs` file, current host overlays, shell aliases,
   Waybar callers, installed commands, and recent history.
3. **Rewrite-risk review** exercised read-only status and doctor paths,
   measured relevant operations, identified regressions to preserve as tests,
   and mapped the functionality into Go components.

No conclusion below relies only on function names or comments; the important
flows were checked against their callers and current configuration.

## Current implementation

### Entry points

The CLI is implemented with `getopts` near the end of the script.

| Invocation | Current behavior |
| --- | --- |
| `rc` | Runs repository/YADM sync, linking, then backups. |
| `rc -c` | Scans for dirty repositories and invokes repository update. |
| `rc -J` | Prints Waybar JSON containing the dirty-repository count. |
| `rc -s` | Synchronizes only YADM. |
| `rc -u` | Synchronizes YADM and configured repositories. |
| `rc -l` | Clones missing configuration repositories and creates links. |
| `rc -B` | Runs configured-in-code backup commands. |
| `rc -y` | Runs available system/package update commands. |
| `rc -D` | Runs environment diagnostics. |
| `rc -v` | Enables verbose `ln`/`rm` output for a later action. |

Most action flags execute immediately and call `exit`, so flags do not compose.
The documented `rc -u -l` example only performs `-u`.

Known external callers are:

- Waybar executes `rc -J` every five seconds.
- Waybar's click handler runs `rc -c` interactively.
- Zsh defines `rcy="rc -y"`, `rcl="rc -l"`, and `rcc="rc -c"`.

These callers must be migrated when the clean-break CLI is installed.

### Global state and startup

Startup creates a temporary file, enables `set -euo pipefail`, installs EXIT
and ERR traps, detects a hostname, and initializes hard-coded paths. The main
paths point at:

- `~/.local/share/rc`
- `~/.local/share/chmouzies`
- `~/.config/emacs`
- `~/.local/share/desktop-config`
- `~/.config/yadm`
- `~/.local/share/yadm`
- `~/.local/bin/desktop`
- `~/git`

State such as the selected repositories, verbosity, temporary directory,
parsed hooks, and paths is held in global shell variables. Functions therefore
cannot be tested independently without sourcing the complete script.

### Host overlay resolution

Host configuration lives below `~/.config/yadm/hosts`. Resolution order is:

1. `common`, when present.
2. Every top-level comma-separated directory containing the lowercase host.
3. The exact lowercase hostname directory.

Directories beginning with `.` are ignored. Multi-host directories come from
`find` without an explicit sort, so their order is filesystem-dependent.

The active layers on the investigated machine are:

1. `common`
2. `ibra,maximus`
3. `ibra`

Most link loops let later layers replace earlier links, effectively giving the
exact host highest priority. `_yadm_config_host_link`, however, returns after
the first match, giving the earliest layer priority. This inconsistency must
not be carried into the rewrite.

### Legacy configuration formats

#### `rc`

Each meaningful line describes a source and optional destination:

```text
source
source destination
?source destination
```

- `?` suppresses warnings when the source does not exist.
- A source without `/` is resolved below `~/.local/share/rc`.
- Sources containing `/` are usually resolved below `$HOME`, but an existing
  matching path in the RC repository can take priority.
- A destination without `/` is placed below `~/.config`.
- A relative destination containing `/` is placed below `$HOME`.
- Absolute destinations are used directly and may invoke `sudo`.
- `~`, `$HOME`, `$HOST`, and `$GOPATH` are expanded inconsistently.
- Existing non-symlink destinations are not overwritten.
- Later host files can replace symlinks created by earlier layers.

The active layers contain 89 entries. They include repeated sources targeting
different destinations, so source alone is not a valid merge key.

#### `chmouzies`

This format links scripts from `~/.local/share/chmouzies`:

```text
path/to/script
path/to/script :: command-name
path/to/script :: relative/target
?path/to/script
```

A bare target is placed in `~/.local/bin/desktop`; a target containing `/` is
placed below `$HOME`. Adjacent `_command` files are automatically linked as Zsh
completions. The active layers contain 106 entries.

#### `repobins`

This format resembles `chmouzies`, but sources are searched in several local
repository roots:

1. The path as written.
2. `$GOPATH/src`.
3. `$GOPATH/src/github.com`.
4. `$GOPATH/src/gitlab.com`.
5. `~/git`.

It accepts optional entries, ` :: ` target aliases, inline comments, `$HOST`,
and adjacent completion files. The active layers contain 23 entries.

#### `extra-dirs`

Each entry adds a Git repository to synchronization:

```text
repository/path
repository/path post_update={ make build }
repository/path always={ command }
```

Relative paths resolve below `~/git`. Hook commands are evaluated through the
shell in the repository directory. Paths cannot contain spaces and hook bodies
cannot contain `}`. The parser accepts arbitrary hook names, although only
`post_update` and `always` execute. The active host has four entries, including
two build hooks.

### Linking workflow

`rc -l` currently:

1. Requires `yadm` and an SSH directory.
2. Clones the YADM repository when its local state directory is absent.
3. Clones missing RC, chmouzies, desktop-config, and Emacs repositories.
4. Reads all matching host `rc` files and creates relative symlinks.
5. Links systemd user units.
6. Replaces `~/.config/zsh` with a link to the RC repository.
7. Links host shell initialization and function files.
8. Deletes and recreates the complete desktop binary directory.
9. Links chmouzies, repository binaries, and completions.
10. Links the host-specific Emacs initialization file.

Deleting the entire binary directory makes ownership ambiguous: the script
cannot distinguish its links from files another tool or the user placed there.

### YADM synchronization

YADM synchronization stages the YADM config directory, password store, and
desktop application directory. It then parses English `yadm status` output to
decide whether to launch lazygit, pull, or push.

When the branch is ahead, the code performs pull, push, and then another
unconditional pull. A clean network round-trip was measured at roughly 1.6
seconds, while local status took roughly 0.09 seconds. The duplicated pull is
therefore visible in normal use.

Pull failures launch lazygit and fall back to `yadm sync`. Final cleanliness is
again determined by matching English status phrases. The displayed remote URL
is constructed by prepending `https://` to an SSH-style URL and is cosmetic,
not a valid URL.

### Git repository synchronization

Default repositories are RC, chmouzies, Emacs, desktop-config, and optionally
ZMK. Extra repositories come from host configuration.

The current sync algorithm:

1. Synchronizes YADM unless a targeted dirty-repository scan was requested.
2. Validates directories and deduplicates identical path strings.
3. Detects local changes with Git porcelain output.
4. Starts one background operation for every clean repository.
5. Waits for all clean operations.
6. Replays buffered results in configured order.
7. Handles dirty repositories or failed clean pulls interactively.

Clean repositories push first, suppress push failures, then pull. If a push is
rejected and the pull succeeds, the resulting local commit may never be pushed,
yet the repository is reported as synchronized. Concurrency is unbounded.

Dirty repositories display status and diffs, then offer Magit/Emacs, lazygit,
`aicommit`, direct signed commit, or skip. Pull errors are ignored before a
final push. Hooks execute after successful clean processing or interactive
processing, but not consistently after skips and failures.

### Backups and updates

Backups are hard-coded for Dconf, requested Homebrew packages, Homebrew casks,
and Pacman packages. Output is written into desktop-config and committed with a
signoff only when Git sees a change. Failures are intentionally suppressed, and
backup commits are not pushed until a later sync.

System updates are capability-based and may invoke LazyVim, Homebrew, Winget,
Atuin, Pacman/Yay, Nix, Home Manager, Doom Emacs, Apt, GitHub CLI, and Zsh plugin
pulls. Platform policy, command order, and flags are embedded in code. Some
branches use `A && B || C` constructs whose behavior differs when `B` fails.

### Doctor and status

Doctor checks commands, paths, SSH, connectivity, symlinks, all configuration
formats, Git repositories, YADM, host overlays, disk space, GitHub auth, Atuin,
LazyVim, and password-store.

The validation logic independently reimplements the legacy parsers, and the
implementations have drifted. For example, hook-bearing `extra-dirs` lines are
validated as if the entire line were a path.

Waybar JSON is hand-escaped and generated from the dirty repository scan.

## Confirmed defects and rewrite decisions

| Current problem | Go behavior |
| --- | --- |
| Empty scans print a newline, causing `rc -J` to report one empty repository. | Empty collections remain empty; JSON reports `0` with an empty tooltip. |
| Doctor aborts in config validation because post-increment returns failure under `set -e`. | Checks run independently and always reach a summary unless cancelled. |
| Action flags exit immediately and cannot compose. | Explicit subcommands replace action flags. |
| Human Git/YADM output is parsed. | Use `--porcelain=v2`, explicit ref queries, and command exit status. |
| YADM can pull twice. | Pull at most once per sync invocation. |
| Clean repositories push before pull and suppress rejected pushes. | Pull first, then push when ahead; report every failure. |
| Repository concurrency is unbounded. | Use a configurable bounded worker pool, defaulting to four. |
| Duplicate paths can refer to the same worktree through aliases. | Canonicalize existing paths before deduplication. |
| Host multi-profile ordering is filesystem-dependent. | Sort matching multi-host profile names lexically. |
| Overlay precedence varies by subsystem. | Apply one documented merge order everywhere. |
| Parser and doctor validation logic differ. | Parse once into typed values used by execution and validation. |
| The whole binary directory is deleted. | Track managed links in a manifest and remove only stale managed links. |
| Missing directories and optional failures are often silently ignored. | Return structured warnings or errors with task and path context. |
| Shell pipelines are implicit strings. | Prefer argument arrays; require an explicit shell field for pipelines. |
| No automated tests cover behavior. | Build unit, integration, golden, and race tests before cutover. |

## Target CLI

Running `rc` with no arguments prints help and exits successfully. It performs
no mutation.

### Commands

```text
rc status [--format text|waybar]
rc sync [--changed-only] [--repo PATH...] [--skip-yadm | --yadm-only]
rc link
rc backup [TASK...]
rc update [TASK...]
rc doctor [--offline]
rc run
rc config validate
rc migrate --legacy-root PATH --output-root PATH
```

`rc run` is the explicit replacement for the old no-argument workflow. It runs
sync, link, and backup in that order, stopping only when continuing would be
unsafe. Independent failures are aggregated where possible.

### Global options

```text
--config PATH
--host NAME
--verbose
--no-color
--non-interactive
--dry-run
```

- All mutating commands honor `--dry-run`.
- `--non-interactive` never launches lazygit, Emacs, or aicommit. Dirty or
  conflicted repositories become actionable errors while clean repositories
  continue.
- Machine output is written only to stdout; logs and diagnostics use stderr.
- Exit `0` means success, exit `1` means an operational or validation failure,
  and exit `2` means invalid CLI usage.

The Waybar caller becomes `rc status --format waybar`. The shell aliases become
`rc update`, `rc link`, and `rc sync --changed-only`.

## Configuration design

Use `github.com/spf13/cobra` for the CLI and
`github.com/pelletier/go-toml/v2` for TOML. Pin both in `go.mod` and avoid other
dependencies unless a concrete need appears.

### Files and layers

- Global file: `~/.config/rc/config.toml`.
- Host root: `~/.config/yadm/hosts` by default.
- Host overlays: `rc.toml` inside `common`, matching multi-host directories,
  and the exact host directory.

Merge order is global, common, lexically sorted multi-host overlays, then exact
host. Scalars use the last specified value. Lists append unless the entry has a
domain key:

- Links key by canonical destination.
- Binaries key by canonical target.
- Repositories key by canonical path.
- Backup and update tasks key by task name.

Later entries replace or extend earlier entries with the same key. This gives
the exact host deterministic priority.

### Global configuration responsibilities

The global file defines:

- Named roots such as home, RC assets, chmouzies, repository base, desktop
  binary directory, YADM config, and YADM state.
- Default Git repositories and optional existence conditions.
- YADM remote/track paths.
- Sync concurrency and interactive tool preferences.
- Backup task commands, outputs, repositories, and commit settings.
- Linux and macOS update tasks with command/platform predicates.
- Doctor connectivity endpoints and timeouts.

### Host overlay responsibilities

Host overlays define links, binaries, completions, extra repositories, hooks,
and host-specific task overrides. Entries use explicit source roots rather than
the legacy parsers' path heuristics.

Representative structure:

```toml
version = 1

[[links]]
source_root = "rc"
source = "git"
target = "~/.config/git"
optional = false

[[bins]]
source_root = "chmouzies"
source = "git/gh-clone"
target = "gh-clone"
discover_completion = true

[[repositories]]
path = "perso/lazyworktree"

[repositories.hooks.post_update]
argv = ["make", "build"]
```

Paths support `~` only at the beginning and `${HOME}`, `${HOST}`, and
`${GOPATH}` anywhere. An unset referenced variable is a validation error.
Paths with spaces are supported naturally by TOML strings.

Commands prefer `argv = [...]`. A task or hook may instead specify
`shell = "..."`, which explicitly runs through the configured shell. Exactly
one form is allowed.

## Go architecture

The dedicated repository should use this initial package layout:

```text
cmd/rc/                 process startup and exit codes
internal/app/           command orchestration
internal/config/        TOML types, loading, merging, validation, migration
internal/host/          hostname detection and profile selection
internal/runner/        external command execution and cancellation
internal/output/        terminal, JSON, color, and deterministic result output
internal/linker/        links, managed manifest, binaries, completions, clones
internal/repo/          Git state, discovery, synchronization, hooks
internal/yadm/          YADM status and synchronization
internal/commitui/      lazygit, Emacs, aicommit, direct commit, prompt adapters
internal/maintenance/   backup and update task engines
internal/doctor/        structured diagnostic checks and summaries
```

Keep interfaces narrow:

- `Runner` executes a command with context, directory, environment, and
  explicit stdio/capture mode.
- `FileSystem` wraps only operations needed for deterministic linker tests.
- `Prompter` handles choices and confirmations.
- `Reporter` accepts typed events and renders human or machine output.

Production implementations use `os/exec` and the standard library. Tests use
fakes. Git and YADM remain external commands so the tool respects existing SSH
agents, credential helpers, hooks, signing, aliases, and user Git config.

### Repository flow

1. Load and validate configuration.
2. Resolve, canonicalize, and deduplicate repository paths.
3. Inspect every repository using porcelain/ref commands without network I/O.
4. Send clean repositories to a worker pool of at most four workers.
5. Process dirty repositories through the interactive adapter sequentially.
6. For a clean repository, pull once, inspect ahead state, then push if needed.
7. Run `post_update` only after a successful operation that changed HEAD.
8. Run `always` after each completed attempt, including a reported failure.
9. Buffer per-repository output and emit it in configured order.
10. Return nonzero when any repository fails.

Cancellation must propagate SIGINT/SIGTERM to child process groups and stop
starting new work.

### YADM flow

1. Verify YADM initialization and configured track paths.
2. Stage only configured paths.
3. Read machine status and ahead/behind state.
4. If dirty, use the interactive adapter or fail in non-interactive mode.
5. Pull once when clean.
6. Push only when the resulting branch is ahead.
7. Re-read machine status and report the actual configured remote URL.

### Linking flow

- Resolve the complete desired link set before mutation.
- Reject conflicting destinations during validation.
- Never replace a real file or directory without an explicit future force
  option; version one has no force option.
- Create parent directories with normal filesystem operations inside writable
  user paths.
- Use `sudo mkdir`/`sudo ln` only for explicitly configured privileged targets.
- Store a manifest of links managed by `rc` and remove only stale entries from
  that manifest.
- Keep relative links where possible.
- Clone missing configured repositories before resolving their assets.

### Backup and update flow

Backup and update behavior becomes data-driven. Each task declares its name,
platforms, required executable, command form, working directory, and failure
policy.

A backup task writes to a temporary file, compares it with the destination,
atomically replaces the destination only when changed, and commits that path
with configured signoff behavior. It does not push implicitly.

Update tasks run sequentially by default because package managers and user
prompts are interactive. Missing optional executables produce a skip event,
not a failure.

### Doctor and status flow

Doctor consumes the same parsed configuration and path resolver as execution.
Checks produce typed pass, warning, failure, or informational results. Network
checks use bounded timeouts and are omitted by `--offline`. Every scheduled
check runs and the command always prints a summary.

Status is read-only. The Waybar renderer uses `encoding/json`, reports zero
correctly, and never emits logs on stdout.

## Migration strategy

### Phase 1: foundation

1. Create `~/git/perso/rc` with `go.mod`, Cobra root command, shared runner,
   output model, and CI/test targets.
2. Build the new executable as `rc-go`; do not replace the Bash command.
3. Add `status`, config loading, and config validation first because they are
   read-only and exercise the foundational types.

### Phase 2: configuration conversion

1. Implement a one-time legacy reader only in the migration package.
2. Convert every legacy host file into TOML under a staging directory.
3. Preserve comments where practical and emit warnings for ambiguous entries.
4. Validate staged TOML and compare the resolved desired links/repositories
   against the legacy configuration.
5. Review the generated files before copying them into YADM host directories.

The runtime must not support legacy formats after migration; only the migration
command understands them.

### Phase 3: subsystem rollout

Implement and exercise, in order:

1. Linker with dry-run and managed manifest.
2. Git status and clean repository sync.
3. YADM sync.
4. Dirty repository interactive adapters.
5. Backups and updates.
6. Doctor and explicit `rc run` workflow.

### Phase 4: cutover

1. Preserve the Bash script as `~/.local/bin/rc-legacy`.
2. Run Go status/doctor alongside the legacy read-only commands.
3. Run `rc-go link --dry-run`, sync against disposable repositories, and each
   maintenance task explicitly.
4. Update Waybar and Zsh callers to the new CLI.
5. Atomically install the Go binary as `~/.local/bin/rc`.
6. Keep `rc-legacy` until the Go version has completed normal workflows on
   both Linux and macOS.

## Test strategy

### Configuration tests

- Global/common/multi/exact merge ordering.
- Lexical ordering of multiple matching multi-host profiles.
- Links with spaces, absolute paths, optional sources, and variables.
- Duplicate destinations, binary targets, repository aliases, and hooks.
- Missing environment variables and invalid command definitions.
- Golden migration tests using every existing host configuration file.

### Repository and YADM tests

- Clean, dirty, staged, untracked, conflicted, detached, and missing-upstream
  repositories.
- Local ahead, remote behind, both diverged, rejected push, failed pull, and
  authentication failure.
- Canonical deduplication of repeated and symlinked repository paths.
- Worker-pool limit and deterministic replay order under `go test -race`.
- `post_update` and `always` hook conditions.
- Exactly one YADM pull and no push when not ahead.
- Non-interactive dirty-state failure without launching external UI.

Tests should use real temporary Git repositories plus fake remotes for Git
semantics, and fake executables for YADM and interactive tools.

### Linker tests

- New, existing valid, broken, stale managed, and unmanaged destinations.
- Refusal to overwrite real files/directories.
- Relative-link construction and paths containing spaces.
- Host overrides and destination conflict detection.
- Dry-run causing no filesystem changes.
- Privileged command planning without invoking real `sudo`.

### Maintenance and diagnostic tests

- Linux/macOS task selection and missing optional commands.
- Backup unchanged/changed/failure behavior and commit arguments.
- Doctor runs every check, counts warnings/errors correctly, and honors offline
  mode.
- Waybar zero, one, and multiple repository JSON snapshots.
- Signal cancellation and child cleanup.

### Required checks

```text
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
GOOS=linux go build ./cmd/rc
GOOS=darwin go build ./cmd/rc
```

## Acceptance criteria

- `rc` with no arguments is non-mutating and shows help.
- All active legacy entries have validated TOML equivalents.
- Linux and macOS builds succeed from the same source.
- Waybar reports zero correctly and receives valid JSON only.
- No repository is synchronized concurrently more than once.
- YADM performs no redundant network operation.
- Dirty repositories retain lazygit, Magit/Emacs, aicommit, direct commit, and
  skip choices.
- The linker never deletes an unmanaged file.
- Doctor always reaches a summary.
- Failures include the subsystem, target, command, and underlying cause.
- The Bash implementation remains available for rollback during rollout.

## Non-goals for version one

- Preserving the old short flags or no-argument mutation.
- Runtime support for legacy line-based configuration.
- Reimplementing Git or YADM protocols in Go.
- WSL/Winget support.
- A graphical interface.
- Automatic overwrite of unmanaged files.
- Remote execution or fleet management.

## Decision record

- **Migration style:** clean break.
- **Scope:** all current feature domains.
- **Source location:** dedicated `~/git/perso/rc` repository.
- **Configuration:** global plus host-layered TOML.
- **CLI:** Cobra; no-argument invocation shows help.
- **Dependencies:** Cobra and a maintained TOML parser only initially.
- **Git integration:** external Git/YADM commands through an injectable runner.
- **Dirty repositories:** retain configurable interactive adapters.
- **Platforms:** Linux and macOS.
- **Rollout:** side-by-side binary, dry-run validation, then atomic cutover.
