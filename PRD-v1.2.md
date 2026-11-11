# Midas: Product Requirements Document

**Version:** 1.2 (draft)
**Changes in 1.2:** Backup mode now defaults to `full`. `init` asks for a backup location and recommends a different drive; same-volume warnings are shown on every move. Restore-point policy remains `auto`, but failures are prominent and recorded. Open Question 6 is resolved.
**License:** MIT
**Primary language:** Go
**Status:** Ready for implementation

> **For the AI coding agent reading this:** This PRD is the single source of truth. Build in the order given in Section 17 (Milestones). Finish and test each milestone before starting the next. Never hardcode app-specific paths in Go code; they live in app definition files (Section 7). When something is ambiguous, pick the safest option for user data and note the decision in `docs/DECISIONS.md`. Section 19 lists rules you must follow.

---

## 1. Overview

**Midas** is an open-source, cross-platform command-line tool that relocates the heavy data folders of developer tools (Claude Code, OpenCode, Hermes, and others) from the system drive to another drive or mount point, and keeps those tools working transparently. It also redirects each tool's temporary files to the same destination, so nothing for that app lands on the system drive.

**Tagline:** *Everything you touch turns to storage space.*

### 1.1 The problem

AI coding agents and CLI tools store sessions, transcripts, caches, logs, plugins, and temp files in hidden folders under the user's home directory (for example `~/.claude`, `~/.hermes`, `~/.local/share/opencode`). These routinely grow to 20 GB or more. On machines with a small system drive (commonly the `C:` drive on Windows, or a small internal SSD on a laptop), this fills the disk. Today users manually cut and paste folders, create links by hand, and repeat this for every tool and after every update that breaks the setup.

### 1.2 The solution

One command moves an app's data folders to a chosen destination, leaves a link at the original location so the app never notices, verifies everything, and can undo the whole operation. A second command launches an app with its temp folders redirected to the destination. Support for each app is described by a small declarative file, so the community can add apps without writing Go code.

---

## 2. Goals and non-goals

### 2.1 Goals

1. Move an app's data folders to another drive safely, with no data loss, even if interrupted.
2. Work on Windows, macOS, and Linux from one codebase, shipped as a single static binary per platform.
3. Redirect temp directories per app, per process, without changing system-wide settings.
4. Be fully reversible (`restore`) and always able to report the true state (`status`).
5. Make adding a new app a pure data contribution (one TOML file).
6. Require no administrator or root rights in the normal case.
7. Take a verified safety backup (and, where the OS supports it, a restore point) before anything is changed, so any failure can be undone with one command and no data is lost.
8. Protect that backup: it can only be deleted once the operation it belongs to is finished and healthy.

### 2.2 Non-goals (v1)

- No GUI or tray app.
- No cloud sync or general-purpose backup features. (The pre-move safety backup in Section 9.2 is a temporary safety net tied to one move, not a backup product.)
- No moving of whole applications or installers (data folders only).
- No cleaning or deleting of app data (that may come later as a separate `midas clean`).
- No automatic management of network drives, encrypted volumes, or mounting.
- No full WSL cross-boundary support in v1 (see Open Questions).

---

## 3. Target users

| Persona | Need |
|---|---|
| Developer with a small system SSD | Free up tens of GB without breaking tools |
| Power user running several AI agents | One consistent way to manage all of them |
| Open-source contributor | Add support for a new tool by writing a short TOML file |

---

## 4. Core concepts

- **App:** a tool Midas knows about, defined by an app definition file (Section 7).
- **Vault:** the destination root directory where Midas stores moved data. Example: `D:\Midas` or `/mnt/data/midas`. Set once in config, overridable per command.
- **Target:** one folder belonging to an app (for example `~/.claude`).
- **Link:** what Midas leaves at the original path. A directory junction on Windows, a symlink on macOS and Linux.
- **Manifest:** a JSON file recording every move, so `status` and `restore` always know the truth.
- **Launcher / run:** starting an app as a child process with redirected environment variables.
- **Backup set:** a verified pre-move copy of an app's data, selected by the backup mode (`full`, `essential`, or explicitly confirmed `none`), stored under the configured backup directory. It has a state and can be deleted only when the move is over (Section 9.2.3).
- **Restore point:** an OS-level snapshot taken before a move, on a best-effort basis (Section 9.2.1).
- **Rollback:** returning an app's data to its exact pre-move state after a failure or on request (Section 9.2.4).

Vault layout:

```
<vault>/
  apps/
    claude-code/
      .claude/            <- moved target
      tmp/                <- redirected temp dir
    opencode/
      share/ config/ cache/ state/
      tmp/
  .midas/
    manifest.json
    journal/              <- in-progress move journals (for resume)
    logs/
  backup/                 <- safety-net backup sets (location configurable)
    claude-code/
      20261110-093000/    <- one backup set per move
        BACKUP.json       <- state, file list, hashes, skipped files
        LOCK              <- present while the move is in progress
        files/            <- mirrored copy, same relative paths as the original
```

---

## 5. Functional requirements

Priority key: **P0** = must ship in v1, **P1** = should ship in v1, **P2** = later.

| ID | Requirement | Priority |
|---|---|---|
| FR-1 | `midas init` sets the vault path, collects the backup directory, and writes the user config | P0 |
| FR-2 | `midas list` shows known apps, which targets exist, their sizes, and moved/not-moved state | P0 |
| FR-3 | `midas move <app>` relocates all of an app's targets and leaves links | P0 |
| FR-4 | `midas run <app> -- <cmd...>` launches a command with temp and optional app env vars redirected | P0 |
| FR-5 | `midas status` shows every moved target, link health, vault reachability, and disk space | P0 |
| FR-6 | `midas restore <app>` reverses a move completely | P0 |
| FR-7 | `--dry-run` on every state-changing command prints the exact plan and changes nothing | P0 |
| FR-8 | Refuse to move while the app is running (override with `--force`) | P0 |
| FR-9 | Resumable and interruptible copy with progress output | P1 |
| FR-10 | `midas shim <app>` creates a wrapper script so typing the normal command name auto-applies `run` | P1 |
| FR-11 | Local user app definitions override or extend built-in ones | P1 |
| FR-12 | `midas doctor` checks for broken links, missing vault, replaced links after app updates, and offers repair | P1 |
| FR-13 | `midas apps validate <file>` validates a contributor's app definition | P1 |
| FR-14 | Checksum verification mode (`--verify=hash`) in addition to default size and count verification | P2 |
| FR-15 | Shell completions (bash, zsh, fish, PowerShell) | P2 |
| FR-16 | Before any move, create and verify a backup set according to the selected backup mode; refuse to move if this fails, unless `--backup=none` was explicitly confirmed | P0 |
| FR-17 | Before any move, attempt an OS restore point or snapshot where the OS supports it (best effort; mandatory only if configured) | P0 |
| FR-18 | `midas rollback <app>` restores the pre-move state from the best available source; the same logic runs automatically on any failure or interruption | P0 |
| FR-19 | A backup set cannot be deleted while its move is in progress, unverified, or unhealthy; deletion needs explicit confirmation | P0 |
| FR-20 | `midas backup list / show / verify / delete` manage backup sets | P0 |

---

## 6. Command specification

Global flags: `--vault <path>`, `--dry-run`, `--yes` (skip confirmation prompts), `--json` (machine-readable output), `--verbose`, `--config <path>`.

### 6.1 `midas init [--vault <path>] [--backup-dir <path>]`

- Prompts for (or accepts `--vault`) the vault path. Creates it if needed.
- Validates the vault path is writable and not on the same volume as the user's home folder (warn, do not block, since the user may want a same-volume move for organization).
- Asks where backup sets should be stored, accepting `--backup-dir <path>` to provide the location non-interactively. Recommend a different drive or volume from the vault.
- If the selected backup directory is on the same volume as the vault, print a clear warning that one drive failure could destroy both the moved data and its backup. The user may accept this arrangement; save the choice in config.
- If the backup location is left empty, use `<vault>/backup`. If this fallback is on the same volume as the vault, retain that location but warn on every move.
- Validate that the selected or default backup location can be created and written to. Save a custom selection in config; an empty `backup_dir` in config means `<vault>/backup`.
- Writes config to the OS-appropriate config dir (`%APPDATA%\midas\config.toml`, `~/.config/midas/config.toml`, `~/Library/Application Support/midas/config.toml`).

### 6.2 `midas list`

Table columns: app, target path, exists (yes/no), size, state (`local`, `moved`, `broken-link`, `partial`). Size calculation should be fast and cancellable; show a spinner.

### 6.3 `midas move <app> [--target <name>...]`

Moves all targets of the app (or only the named ones). Follows the algorithm in Section 9, which always begins with the safety net (Section 9.2). Prints the plan first, including exactly what will be backed up and what will not, and asks for confirmation unless `--yes`.

Extra flags: `--backup=full|essential|none` (default `full`), `--backup-dir <path>` (overrides the configured backup directory for this move), and `--restore-point=auto|require|skip` (default `auto`). `--backup=none` additionally needs `--i-understand-no-backup` and a typed confirmation; `--yes` does not satisfy it.

### 6.4 `midas run <app> [flags] -- <command> [args...]`

- Creates `<vault>/apps/<app>/tmp/` if missing.
- Builds the child environment: copies the current environment, sets `TEMP`, `TMP`, `TMPDIR` to the vault tmp dir, then applies the app definition's `[env]` entries (with template substitution, Section 7.2).
- Starts the child with stdin, stdout, stderr passed straight through (including TTY behavior), forwards signals (SIGINT, SIGTERM; Ctrl+C on Windows), and exits with the child's exit code.
- If the vault is unreachable, print a clear error and exit non-zero before launching anything. Do not silently fall back to the system temp dir (`--allow-fallback` may enable this explicitly).
- Optional `--clean-tmp` removes the app's tmp dir contents after the child exits.

### 6.5 `midas status`

Reads the manifest and checks each entry live: does the link exist, does it point to the recorded destination, does the destination exist, is it readable. Also prints vault free space. Exit code is non-zero if any entry is unhealthy (useful for scripts).

### 6.6 `midas restore <app>`

Reverse of move: refuse if app is running, check free space on the original drive, copy back, verify, remove the link, put the data in place, then remove the vault copy only after verification. Updates the manifest. Supports `--keep-vault-copy`.

### 6.7 `midas shim <app>`

Creates a small wrapper in a user-chosen bin directory (`--dir`), named after the app's launch command, that calls `midas run <app> -- <real command> "$@"`. Must handle the Windows `.cmd` form and the Unix shell script form. Must locate the real executable to avoid infinite recursion (the shim must never call itself).

### 6.8 `midas doctor`

Detects and offers fixes for: missing vault (drive not mounted), dangling links, links replaced by a real folder by an app update (offer to merge into the vault copy), manifest entries with no link, orphaned partial copies, stale backup locks, and incomplete or unverified backup sets.

### 6.9 `midas apps`

Subcommands: `apps list`, `apps show <app>`, `apps validate <file>`, `apps path` (prints the local definitions directory).

### 6.10 `midas backup`

- `backup list [<app>]`: shows each set's id, app, mode, state, size, and date.
- `backup show <id>`: shows the file list, skipped files, restore point info, and whether the set is deletable (and if not, why).
- `backup verify <id>`: re-hashes the set and reports any mismatch.
- `backup delete <id>`: deletes a set, subject to the rules in Section 9.2.3. `backup delete --all-completed` deletes every set that is currently deletable.

### 6.11 `midas rollback <app>`

Returns the app's data to its exact pre-move state. Flags: `--backup <id>`, `--source auto|bak|vault|backup`, `--dry-run`, `--force`. Follows Section 9.2.4. Use `rollback` when something went wrong; use `restore` (Section 9.5) when a move succeeded but you want the data back on the original drive.

### 6.12 Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | General error |
| 2 | Usage / invalid arguments |
| 3 | App is running |
| 4 | Insufficient space |
| 5 | Vault unreachable |
| 6 | Verification failed (original left untouched) |
| 7 | Unhealthy state found by `status` / `doctor` |
| 8 | Backup or required restore point could not be created or verified (nothing was changed) |
| 9 | Backup is protected; deletion refused |

---

## 7. App definition format

Each app is a TOML file. Built-in definitions are embedded into the binary with `go:embed` from `/apps/*.toml`. User definitions live in the local definitions directory and take precedence over built-ins with the same `id`.

### 7.1 Schema

```toml
id = "claude-code"                 # unique, lowercase, [a-z0-9-]
name = "Claude Code"
homepage = "https://..."
description = "Anthropic's agentic coding CLI"

# Process names used to detect whether the app is running.
# Per-OS tables, matched case-insensitively against process executable names.
[process]
windows = ["claude.exe", "node.exe"]   # see note on shared runtimes in 7.3
linux   = ["claude"]
darwin  = ["claude"]

# Launch command for shim creation and docs
[launch]
command = "claude"

# Folders to relocate. Paths support ~, %VAR%, $VAR, and {xdg_*} tokens.
[[target]]
name = "data"
windows = "~/.claude"
linux   = "~/.claude"
darwin  = "~/.claude"
kind    = "dir"                    # only "dir" is allowed; files are rejected

# Environment variables applied by `midas run`.
# Templates: {{vault_app}} = <vault>/apps/<id>, {{tmp}} = <vault_app>/tmp
[env]
# CLAUDE_CONFIG_DIR = "{{vault_app}}/.claude"   # optional alternative to linking

# What counts as "important" for the pre-move backup (mode = essential).
# Globs are relative to the target folder. Illustrative: verify against a real install.
[backup]
include = ["settings*.json", "*.md", "commands/**", "agents/**", "plugins/**", ".credentials*"]
exclude = ["cache/**", "logs/**", "tmp/**", "**/node_modules/**", "*.log"]
max_file_mb = 50

[notes]
text = "Do not move ~/.claude.json; it is a file, not a folder."
```

### 7.2 Template and path rules

- Expand `~`, environment variables, and tokens: `{home}`, `{xdg_config}`, `{xdg_data}`, `{xdg_cache}`, `{xdg_state}`, `{appdata}`, `{localappdata}`.
- If a target has no entry for the current OS, it is skipped on that OS (not an error).
- After expansion, every path must be absolute, must be inside the user's home directory (or an explicitly allowed root), and must not be a filesystem root, the home directory itself, or a well-known system directory. Definitions violating this fail validation.

### 7.3 Process detection caveat

Some apps run on generic runtimes (for example `node`). Matching only on `node` would produce false positives. Support an optional `cmdline_contains = ["@anthropic-ai/claude-code"]` field in `[process]`, and treat a match as running only if both the name and (when provided) the command-line substring match. Where reliable detection is impossible, warn the user and require `--yes`.

### 7.4 Built-in definitions for v1

These are starting points. **Paths and process names must be verified against each tool's current documentation and a real install before release**, since they vary by version.

| App id | Expected targets |
|---|---|
| `claude-code` | `~/.claude` (leave `~/.claude.json` untouched) |
| `opencode` | `{xdg_data}/opencode`, `{xdg_config}/opencode`, `{xdg_cache}/opencode`, `{xdg_state}/opencode` (on Windows these resolve under the user profile; verify) |
| `hermes` | `~/.hermes` |

---

## 8. Platform layer

All OS-specific behavior lives behind one Go interface in `internal/platform/`, with build-tagged files (`*_windows.go`, `*_unix.go`, `*_darwin.go`).

```go
type Platform interface {
    // Create a link at `link` pointing to directory `target`.
    CreateLink(link, target string) error
    // Report whether path is a Midas-style link and where it points.
    ReadLink(path string) (target string, isLink bool, err error)
    // Remove a link without touching its target's contents.
    RemoveLink(path string) error
    // Free bytes on the volume containing path.
    FreeSpace(path string) (uint64, error)
    // Volume identity, to detect same-volume moves (rename instead of copy).
    VolumeID(path string) (string, error)
    // Running processes matching names / cmdline substrings.
    FindProcesses(names []string, cmdlineContains []string) ([]ProcInfo, error)
    // Env var names that control temp dirs on this OS.
    TempEnvVars() []string
    // Default config directory and default app data locations.
    ConfigDir() string
}
```

| Concern | Windows | macOS | Linux |
|---|---|---|---|
| Link type | Directory junction (no admin) | Symlink | Symlink |
| Temp vars set by `run` | `TEMP`, `TMP`, `TMPDIR` | `TMPDIR` (+ `TMP`, `TEMP`) | `TMPDIR` (+ `TMP`, `TEMP`) |
| Link detection | Reparse point with junction tag | `os.Lstat` symlink mode | `os.Lstat` symlink mode |
| Long paths | Use `\\?\` prefixed paths internally | n/a | n/a |
| Free space | `GetDiskFreeSpaceEx` | `statfs` | `statfs` |

Implementation notes:

- On Windows, a junction can be created with `mklink /J` through `cmd /c`, or directly through the reparse point APIs. Either is acceptable; prefer the direct API if reliable, and fall back to `cmd`. Do not use file symlinks on Windows because they need elevation or developer mode.
- Go's `os.Readlink` and `os.Lstat` treat junctions inconsistently across versions. Wrap detection in the platform layer and test it on real Windows CI.
- On macOS, reading some folders (Documents, Desktop, external volumes) may trigger privacy (TCC) prompts or failures. Detect permission errors and print a clear message explaining how to grant the terminal Full Disk Access.

---

## 9. Move algorithm (critical path)

The move must be safe under failure, interruption, and power loss. Implement as a journaled state machine. Each step records progress in `<vault>/.midas/journal/<app>-<target>.json` so `move` can resume and `doctor` can recover.

### 9.1 Pre-flight (no changes made)

1. Load and validate the app definition.
2. Expand and validate target paths (Section 7.2).
3. Skip targets that do not exist, are already Midas links, or whose destination already exists (report clearly).
4. Verify the vault is reachable and writable.
5. If the app is running and `--force` is not set: abort with exit code 3.
6. Compute source size and the estimated backup size for the selected mode. The vault needs at least source size plus a 10% margin, and the backup location needs the estimated backup size plus 10%. If the vault and backup directory are on the same volume, the two requirements add up. Abort with exit code 4 if space is insufficient. Midas must never silently downgrade the backup mode to make the operation fit: the error must state how much space is missing and suggest `--backup-dir` on another drive or, as a deliberate choice, `--backup=essential`.
7. Print the full plan: what will be backed up (file count, size), which files are skipped and why, where the backup goes, whether the backup directory shares a volume with the vault, any resulting space/risk warning, whether a restore point will be attempted, and the backup retention policy. If `--dry-run`, stop here. Otherwise confirm unless `--yes`.

### 9.2 Safety net: restore point and backup

Nothing is moved, renamed, or linked until this phase succeeds. It has two parts: an OS restore point (best effort) and a verified file backup (mandatory).

#### 9.2.1 OS restore point or snapshot (best effort)

| OS | Mechanism | Notes |
|---|---|---|
| Windows | System Restore point via PowerShell `Checkpoint-Computer` | Needs an elevated shell and System Protection enabled for the system drive. Windows normally allows only one automatic restore point per 24 hours, so creation can be skipped. Midas must not edit the registry to change that and must not auto-elevate. |
| macOS | APFS local snapshot via `tmutil localsnapshot` | Best effort. The OS may purge local snapshots automatically. |
| Linux | Detect Timeshift, btrfs, ZFS, or LVM snapshots | v1 runs Timeshift only if it is installed and Midas is already root; otherwise it prints a recommendation. |

Policy (`restore_point` in config or `--restore-point`):

- `auto` (default): try; if it cannot be created, print a prominent warning with the reason and how to fix it (for example, run an elevated shell or enable System Protection), record the failure in the manifest, then continue because the verified file backup is mandatory. `auto` is the default rather than `require` because `require` could make Midas unusable from a normal non-elevated shell, conflicting with Goal 6 (no administrator rights). Users who want stricter behavior can set `restore_point = "require"` or pass `--restore-point=require`.
- `require`: abort with exit code 8 if the restore point cannot be created.
- `skip`: do not attempt.

**Limits, which Midas must state in its output and in the README:** System Restore points mainly protect system files, the registry, and installed programs. They generally do not restore personal files inside the user profile, such as `~/.claude`. The file backup below is therefore the real safety net, and the restore point is an extra layer. Midas cannot roll back an OS restore point by itself; it records the restore point's name and sequence number in the manifest and prints how to open it (System Restore on Windows, Time Machine on macOS).

#### 9.2.2 File backup (mandatory)

Location: `<backup_dir>/<app>/<YYYYMMDD-HHMMSS>/`, where `backup_dir` defaults to `<vault>/backup`. One backup set is created per move. The `files/` folder mirrors the original folder structure, so a person can browse it and copy files back by hand even without Midas.

| Mode | What is copied | Space cost |
|---|---|---|
| `full` (default) | The entire target folder | Same size as the data, on top of the vault copy; pre-flight checks that it fits |
| `essential` | Files matching the app definition's `[backup].include` globs, not matching `exclude`, and no larger than `max_file_mb` | Small; cannot restore everything, so rollback lists what is not covered |
| `none` | Nothing | Needs `--i-understand-no-backup` and a typed confirmation. Not recommended |

When `essential` mode is selected and an app definition has no `[backup]` section, the default is all files up to 25 MB, excluding obviously regenerable folders (`cache/`, `tmp/`, `logs/`, `node_modules/`). This fallback does not affect the `full` default.

Procedure:

1. Create the set directory. Write `BACKUP.json` with state `creating`. Create `LOCK` containing PID, hostname, and start time.
2. Copy files, preserving structure, symlinks (as symlinks), permissions, timestamps, and Windows file attributes. Compute each file's SHA-256 while reading the source.
3. Record every skipped file (excluded, or over the size cap) under `skipped` in `BACKUP.json`, and show a summary in the plan so the user knows what is not covered.
4. Verify by re-reading the copies and comparing each hash. Hash verification is always used in `essential` mode; in `full` mode it is used unless `--verify=size` is given.
5. Restrict access to the owner (backups can contain credentials) and mark files read-only where the OS supports it, to prevent accidental modification.
6. Set state to `verified`. If any step fails: delete the incomplete set, change nothing else, exit with code 8.

If the backup directory is on the same volume as the vault, warn on every move that one drive failure would take out both the moved data and the backup, and recommend `--backup-dir` on a different drive. In `full` mode, the plan must also show the combined/doubled space requirement for the vault copy and backup when they share a volume.

`BACKUP.json` example:

```json
{
  "schema": 1,
  "id": "20261110-093000",
  "app": "claude-code",
  "target": "data",
  "original_path": "C:\\Users\\me\\.claude",
  "mode": "full",
  "state": "verified",
  "created_at": "2026-11-10T09:30:00Z",
  "files": [
    { "path": "settings.json", "size": 812, "mtime": "2026-11-09T18:02:11Z", "sha256": "..." }
  ],
  "skipped": [],
  "restore_point": { "attempted": true, "created": false, "reason": "shell not elevated" }
}
```

#### 9.2.3 Backup states and deletion rules

| State | Meaning | Deletable |
|---|---|---|
| `creating` | Being written | No |
| `verified` | Complete and verified; move not finished | No |
| `in-use` | Move in progress (LOCK held) | No |
| `complete` | Move finished and the link passed its health check | Yes, if the rules below pass |
| `rolled-back` | Used to restore the original state | Yes |
| `abandoned` | Move was cancelled before anything changed | Yes |

All deletion goes through **one function** (`backup.Delete`). It enforces, in order:

1. The state must be `complete`, `rolled-back`, or `abandoned`.
2. No journal exists for that app and target, and no live process holds `LOCK`.
3. For `complete`: the link must pass a live health check right now (or the manifest entry no longer exists because the user ran `restore`).
4. The user confirms by typing the app name (skipped only with `--yes`).
5. Any violation returns exit code 9 with the failed condition and what to do next. There is no `--force` for `creating`, `verified`, or `in-use` sets.
6. Deletion uses `Lstat`, clears read-only flags, never follows symlinks, and removes `BACKUP.json` last so a half-deleted set is detectable.

Retention (`backup_retention`):

- `keep` (default): at the end of a successful move Midas asks "Delete the backup now? [y/N]". The default is No.
- `delete-on-success`: delete automatically after step 12, only if all rules above pass.
- `<N>d` (for example `30d`): checked on later Midas runs; delete after N days if all rules pass.

Midas never deletes a backup in any other circumstance. Midas cannot stop a person from deleting backup files by hand; the read-only flags and owner-only permissions are guardrails, and the README must say so.

#### 9.2.4 Rollback

Runs automatically on any failure or interruption after the safety net completes, and manually via `midas rollback <app>`.

Restore sources, tried in this order, using the first that passes verification:

1. `<original>.midas-bak`: the untouched original (exists until step 11 of Section 9.3).
2. The verified vault copy (a full copy, if it exists and passes verification).
3. The backup set. In `full` mode this restores everything. In `essential` mode it restores only the backed-up files, and Midas must list exactly which files are not covered.

Steps:

1. Refuse if the app is running, unless `--force`.
2. Print the plan: which source will be used and what cannot be restored. `--dry-run` stops here.
3. If the original path is a Midas link, remove the link (as a link). If it is a real folder (partial state), rename it to `<original>.midas-rollback-<timestamp>`; never delete it.
4. Rebuild the original folder from the chosen source, then check every file recorded in the backup set against its hash and restore any missing or mismatched file from the set.
5. Verify, then remove the manifest entry, set the backup state to `rolled-back`, and delete the journal.
6. Do not delete the vault copy or the backup set. Print where they are and how to remove them.
7. Print a plain summary: what was restored, from where, and where the backup is kept. If rollback itself fails, stop and print the manual recovery steps; never leave the user guessing.

### 9.3 Execution (per target)

**Precondition:** the safety net (Section 9.2) has finished and the backup set is `verified`. No step below may run without this, unless `--backup=none` was explicitly confirmed.

| Step | Action | Failure handling |
|---|---|---|
| 1 | Journal: `state=copying` | |
| 2 | Same volume? Skip to step 5 using rename. Otherwise copy source to `<dest>.midas-partial/`, preserving symlinks (do not follow), permissions, and timestamps, with progress | Delete partial copy, leave source untouched |
| 3 | Journal: `state=verifying` | |
| 4 | Verify: file count, directory count, total size, and per-file size always; hashes if `--verify=hash`. Mismatch aborts with exit code 6 | Delete partial copy, source untouched |
| 5 | Atomically rename `<dest>.midas-partial` to `<dest>` | |
| 6 | Journal: `state=swapping` | |
| 7 | Rename the original to `<original>.midas-bak` | If this fails (files in use), roll back: remove nothing from the source, delete the vault copy, report |
| 8 | Create the link at the original path pointing to `<dest>` | If this fails, rename the backup back to the original and report |
| 9 | Health check: list the link, read one file through it | If this fails, remove link, restore backup |
| 10 | Write manifest entry (including backup id and restore point info); journal: `state=cleanup`; backup state `in-use` | |
| 11 | Delete `<original>.midas-bak` | If this fails, warn and leave it; `doctor` can clean up |
| 12 | Journal: `state=done` (then delete journal); backup state becomes `complete`; release `LOCK`; apply the retention policy (Section 9.2.3) | |

**Failure policy:** operational failures attempt automatic rollback (Section 9.2.4). Ctrl+C or a supported termination during a copy preserves the source, operation-owned partial, backup, and journal for validated resume or rollback; Midas does not mark a canceled migration complete.

### 9.4 Resume

If a journal exists, inspect it with `midas doctor` before acting. `midas resume <app>` is limited to validated `copying` or `verifying` checkpoints with a matching operation-owned partial path, unchanged source metadata, safe destination, and a verified backup (or an explicitly recorded no-backup choice). It restarts that copy from the source rather than trusting partially copied files. Ambiguous or later states require review and rollback; unknown partial directories are preserved.

### 9.5 Restore (planned undo of a completed move)

Use `restore` when a move finished fine but you want the data back on the original drive. Use `rollback` (Section 9.2.4) when something went wrong.

Mirror of the above in reverse (copy back to `<original>.midas-partial`, verify, remove link, rename into place, then delete the vault copy unless `--keep-vault-copy`).

### 9.6 Special cases

- **Same-volume move:** use a plain rename and skip the copy phase.
- **Symlinks inside the data folder:** recreate them as symlinks, never follow them. Warn if any point outside the folder.
- **Files that vanish or change during copy:** tolerate vanished temp files; re-check the size at the end and warn if the app was modified while running (only possible with `--force`).
- **Sparse and locked files (Windows):** report which files could not be read and abort rather than skipping silently.
- **Case-insensitive file systems:** detect collisions that would occur when moving between case-sensitive and case-insensitive volumes, and abort with a list.

---

## 10. Manifest format

`<vault>/.midas/manifest.json`, written atomically (write to temp file in the same directory, fsync, rename).

```json
{
  "schema": 1,
  "midas_version": "1.0.0",
  "entries": [
    {
      "app": "claude-code",
      "target": "data",
      "original_path": "C:\\Users\\me\\.claude",
      "vault_path": "D:\\Midas\\apps\\claude-code\\.claude",
      "link_type": "junction",
      "moved_at": "2026-11-10T09:30:00Z",
      "bytes": 21474836480,
      "files": 183422,
      "verified": "size",
      "backup": {
        "id": "20261110-093000",
        "mode": "full",
        "state": "complete",
        "path": "D:\\Midas\\backup\\claude-code\\20261110-093000",
        "restore_point": { "attempted": true, "created": false, "reason": "shell not elevated" }
      }
    }
  ]
}
```

Always keep a copy of the previous manifest as `manifest.json.bak`. If the manifest is corrupt, `doctor` must be able to rebuild it by scanning for links that point into the vault.

---

## 11. Configuration

`config.toml`:

```toml
vault = "D:\\Midas"
verify = "size"          # size | hash
confirm = true
shim_dir = ""
allow_fallback = false
backup = "full"               # full | essential | none
backup_dir = ""               # default: <vault>/backup; custom path may be on another drive
restore_point = "auto"        # auto | require | skip
backup_retention = "keep"     # keep | delete-on-success | 30d
```

Precedence: CLI flags, then environment variables (`MIDAS_VAULT`, `MIDAS_CONFIG`), then config file, then defaults. `--backup-dir` on `midas move` overrides `backup_dir` in config for that move. `backup_dir = ""` resolves to `<vault>/backup`; a custom location selected during `init` is stored in config.

---

## 12. Non-functional requirements

| Area | Requirement |
|---|---|
| Safety | Never delete user data until the copy has been verified and the link health-checked |
| Performance | Copy uses a worker pool (default 4–8 goroutines); progress shown with bytes/sec and ETA; 20 GB with 200k files must not exhaust memory |
| Portability | Builds for windows/amd64, windows/arm64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 |
| Dependencies | Keep minimal. Suggested: `spf13/cobra` (CLI), `BurntSushi/toml` (config), `shirou/gopsutil` (processes), `schollz/progressbar` or similar. Avoid cgo |
| Privileges | No admin/root in the normal path. If elevation is ever required, explain why and do not auto-elevate |
| Output | Human-friendly by default, stable JSON with `--json`, honor `NO_COLOR` |
| Logging | Per-run log file in `<vault>/.midas/logs/` |
| Privacy | No telemetry, no network calls |
| Accessibility | Clear plain-text messages; no information conveyed by color alone |

---

## 13. Security and safety rules

1. Validate all paths after expansion and resolution. Reject path traversal and any target outside the allowed roots.
2. Refuse to operate on: filesystem roots, the home directory itself, system directories (`C:\Windows`, `C:\Program Files*`, `/usr`, `/etc`, `/System`, etc.), and the vault itself.
3. Refuse a vault located inside any target (infinite recursion).
4. Never follow symlinks while deleting; deletion functions must use `Lstat` and remove links as links.
5. Treat app definition files as untrusted input: no shell execution from definitions, no arbitrary commands, only declarative data. `validate` enforces the schema strictly.
6. `run` must pass arguments to the child process as an argv array, not through a shell string.
7. Backup sets may contain credentials and tokens. Create them with owner-only permissions, never upload or transmit them, and never print file contents from them in logs.

---

## 14. Testing strategy

- **Unit tests:** path expansion and validation, app definition parsing and validation, manifest read/write, template substitution.
- **Integration tests (temp directories only):** full move, run, status, restore cycles using fake apps and two temp directories standing in for two drives. Never touch real user folders.
- **Failure injection:** simulate errors at every step of Section 9.3 (copy failure, verification mismatch, rename failure, link failure, interruption) and assert the original data is intact and state is recoverable.
- **Backup tests:** a move must never start without a `verified` backup set; corrupting one backup file must be detected by `backup verify` and block the move; `essential` mode must list skipped files accurately.
- **Deletion-protection tests:** attempt `backup delete` on sets in `creating`, `verified`, and `in-use` states, with a held `LOCK`, with an existing journal, and with an unhealthy link; each must fail with exit code 9 and leave every file intact.
- **Crash-and-rollback tests:** kill the process at random points, then run `midas rollback` and assert the restored tree matches the original by hash (or, in `essential` mode, that all backed-up files match and the uncovered files are reported correctly).
- **Large-tree test:** generate a tree of 100k small files plus a few large files and assert correctness and bounded memory.
- **Platform matrix in CI:** GitHub Actions on `windows-latest`, `ubuntu-latest`, `macos-latest`. Windows junction behavior must be tested on real Windows runners.
- **Golden tests** for `--json` output.

---

## 15. Repository structure

```
midas/
  cmd/midas/main.go
  internal/
    cli/               # cobra commands
    apps/              # definition loading, validation, built-ins (embed)
    mover/             # journaled move/restore state machine, copy, verify
    backup/            # safety-net backup sets, protected deletion, rollback
    snapshot/          # OS restore point / snapshot adapters (best effort)
    manifest/
    platform/          # interface + *_windows.go, *_unix.go, *_darwin.go
    runner/            # env building, child process, signal forwarding
    config/
    ui/                # progress, tables, prompts, json output
  apps/                # built-in app definitions (*.toml)
    claude-code.toml
    opencode.toml
    hermes.toml
  docs/
    DECISIONS.md
    ADDING_AN_APP.md
    SAFETY.md
  .github/
    workflows/ci.yml
    workflows/release.yml
    ISSUE_TEMPLATE/
  .goreleaser.yaml
  README.md
  CONTRIBUTING.md
  CODE_OF_CONDUCT.md
  LICENSE
  go.mod
```

---

## 16. Open-source and distribution

- **License:** MIT.
- **README must include:** one-paragraph pitch, a prominent **"Back up before first use"** warning, install instructions, a 3-command quickstart (`init`, `move`, `run`), supported apps table, FAQ (external drives, app updates, WSL), and how to add an app.
- **CONTRIBUTING:** how to add an app definition, how to run tests, how to run `apps validate`, commit style.
- **Issue templates:** bug report (asks for OS, Midas version, app and app version, `midas status --json` output) and "new app request".
- **Releases:** GoReleaser builds all six binaries and checksums on a version tag, publishes a GitHub Release.
- **Package managers (after v1.0):** Homebrew tap, Scoop bucket, winget manifest; optional `npm`/`pip` wrapper packages that download the binary.
- **Naming check:** before publishing, confirm "midas" is available on GitHub and the target package managers; if not, use a suffix such as `midas-cli` for the package name while keeping the `midas` command.

---

## 17. Milestones and acceptance criteria

### M1: Skeleton and Windows MVP
- Project scaffold, cobra CLI, config, `init`, `list`, `apps validate`.
- App definition loader with embedded built-ins and schema validation.
- Windows platform layer (junction create/detect/remove, free space).
- `move` for a single app with the full Section 9 algorithm (without resume), `--dry-run`.
- Safety net: verified backup set (`essential` and `full` modes), backup states, the single protected `backup.Delete` function, and automatic rollback on failure. `move` must not work without it.
- **Accept when:** moving a test folder between two temp dirs on Windows leaves a working junction, data intact, and a correct manifest; a move never starts unless the backup set is verified; all failure-injection tests end with the original data restored automatically.

### M2: Run and restore
- `run` with temp redirection, env templating, signal forwarding, exit code passthrough.
- `status`, `restore`.
- `backup list / show / verify / delete`, manual `rollback`, and best-effort restore point (Windows) and snapshot (macOS).
- Running-process detection with `cmdline_contains`.
- **Accept when:** `midas run` launches a child that writes to its temp dir and the files appear in the vault; `restore` returns the exact original tree (verified by hashing in tests); after a simulated crash `midas rollback` restores the original; `backup delete` on an in-progress set is refused with exit code 9.

### M3: Linux and macOS
- Unix platform layer, build tags, XDG handling, macOS TCC error messaging, Linux snapshot detection (Timeshift, btrfs hints).
- CI matrix green on all three operating systems.
- **Accept when:** the full integration suite passes on Windows, Linux, and macOS runners.

### M4: Resilience
- Journaling and resume, partial-copy cleanup, `doctor`, progress bars and ETA, worker-pool copy, `--verify=hash`.
- **Accept when:** killing the process at random points during a move, then re-running, always ends in a consistent state with no data loss.

### M5: Polish and release
- `shim`, shell completions, `--json` stabilization, docs, issue templates, GoReleaser, first tagged release.
- **Accept when:** a fresh user can install a release binary and complete `init`, `move`, `run` for Claude Code following only the README.

---

## 18. Risks and mitigations

| Risk | Mitigation |
|---|---|
| Data loss during move | Verify-before-delete, backup rename, journaling, failure-injection tests |
| App update replaces the link with a real folder | `doctor` detects and merges; document in FAQ |
| Vault drive disconnected | `run` and `status` fail loudly with a clear message; never silently fall back |
| App definitions go stale as tools change | Definitions are data, easy for the community to update; include a `tested_version` field in future versions |
| Junction/symlink quirks across Go versions | All link logic isolated in the platform layer and tested on real CI runners |
| Antivirus flags a tool that moves many files | Sign Windows binaries when possible; document known false positives |
| Running-process detection is unreliable | `cmdline_contains` matching plus explicit warning and confirmation fallback |
| Backup uses extra disk space | `full` is the default; space is estimated and checked in pre-flight, and the move aborts with exit code 4 rather than silently downgrading. `essential` is a deliberate opt-out; `--backup-dir` allows a third drive |
| Backup shares a drive with the vault | Warn on every move when they share a volume; recommend a different drive and show the combined space requirement in `full` mode |
| Backup contains credentials | Owner-only permissions, read-only flags, never transmitted or logged |
| User deletes backup by hand | Cannot be prevented; guardrails plus clear README wording |
| OS restore point unavailable or throttled | Best effort only; the file backup is the real safety net and the tool says so |
| `essential` backup cannot restore everything | Original `.midas-bak` and the vault copy are tried first; Midas lists exactly what a backup-only rollback cannot restore |

---

## 19. Rules for the implementing agent

1. Follow the milestones in order. Do not start M(n+1) until M(n) acceptance criteria pass.
2. Never hardcode app names or paths in Go code. They belong in `/apps/*.toml`.
3. All OS-specific code goes through `internal/platform`. No `runtime.GOOS` checks outside that package, except in tests.
4. Tests must only touch `t.TempDir()` or explicitly created temp directories. Never read, write, or delete real home folders in tests.
5. Every destructive operation must be reachable only after verification and must have a dry-run representation.
6. Prefer the standard library. Justify each new dependency in `docs/DECISIONS.md`.
7. Write clear error messages that say what happened, what was left untouched, and what the user can do next.
8. Keep functions small and the mover state machine explicit (named states, table-driven transitions) so failure injection is easy.
9. Run `go vet`, `staticcheck`, and `go test ./...` before considering any milestone done.
10. Do not add telemetry, network calls, or auto-update logic.
11. No code path may start moving data without a `verified` backup set, unless `--backup=none` was explicitly confirmed. Backup deletion goes only through `backup.Delete`; never call `os.RemoveAll` (or equivalent) on a backup path anywhere else.
12. When a rollback cannot restore everything, say exactly what was and was not restored.

---

## 20. Open questions

1. **WSL:** should Midas support moving data used by tools running inside WSL (Linux paths on a Windows drive), or document it as out of scope for v1?
2. **Per-app vault override:** is a single global vault enough, or should each app be able to target a different drive?
3. **Using env vars instead of links:** for tools that support a config-dir variable (for example `CLAUDE_CONFIG_DIR`), should Midas offer a `--mode env` that avoids links entirely?
4. **Windows ReFS/Dev Drive and network paths:** are junctions to network shares in scope? (Default: not supported; detect and refuse.)
5. **Update-proofing:** should `run` automatically re-check and repair links each launch (cheap health check) before starting the app?
6. **Backup location default:** *Resolved in 1.2.* `init` asks for a backup directory and recommends a different drive. The fallback default remains `<vault>/backup` because Midas cannot reliably choose another drive automatically. If the backup directory shares a volume with the vault, Midas warns on every move.
7. **Grace period:** should Midas offer to keep the original as `.midas-bak` for a chosen number of days (frees no space until then) before step 11 deletes it?
