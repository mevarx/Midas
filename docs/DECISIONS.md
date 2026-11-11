# Implementation decisions

Design choices behind the current implementation, in the order they were
taken. Behavior guarantees live in `SAFETY.md`; compatibility policy in
`COMPATIBILITY.md`.

## Dependencies

- **BurntSushi/toml** is kept. App definitions and config are untrusted input;
  a strict, well-tested TOML parser is safer than a hand-rolled subset. Unknown
  keys are rejected so contributor typos fail validation instead of being
  ignored.
- **shirou/gopsutil/v4** is kept for process enumeration (name + command line).
  Shelling out to `wmic`/`tasklist`/`ps` is fragile across Windows versions and
  cannot reliably give command lines. gopsutil is pure Go (no cgo) on the three
  target OSes.
- **cobra is not used.** The CLI is built on the standard `flag` package with a
  small dispatcher. This removes a dependency and keeps global-flag handling
  explicit; global flags may appear before or after the subcommand.
- **No progress-bar library.** `internal/ui` renders a one-line progress with
  rate and ETA, honouring `NO_COLOR`.

## Vault layout

- A target is stored at `<vault>/apps/<app-id>/<target-name>`, where
  `target-name` comes from the definition's `[[target]].name`, **not** the
  basename of the original path. The basename would collide for OpenCode (all
  four XDG folders are named `opencode`).
- `.midas/`, `backup/`, `apps/` are reserved names inside the vault.

## Move algorithm

- **Same-volume moves** use a single `rename(original, vault-path)` and skip the
  copy and `.midas-bak` stages. The link is created afterwards; if that fails,
  rollback renames the vault path back to the original.
- **Cross-volume moves** copy to `<dest>.midas-partial`, verify, rename it into
  place, rename the original to `<original>.midas-bak`, create the link, health
  check, write the manifest, then remove `.midas-bak`.
- Files that vanish or cannot be read during a copy are treated as errors:
  Midas reports which files could not be read and aborts rather than
  skipping silently. Symlinks are recreated, never followed. Windows
  junctions inside a target are detected through the platform layer and are not
  descended into.
- **Case collisions** between paths that differ only by case abort the copy
  before anything is removed.
- Entry `bytes`/`files` in the manifest record the **source** size measured in
  pre-flight. For move targets they are not the backup size (essential backups
  are smaller).
- A `--backup=none` move calls the same state machine with
  `NoBackupConfirmed`; it refuses unless the explicit flag and typed phrase
  (`no backup`) were supplied. `--yes` does not satisfy the typed phrase.

## Backup sets

- One set per target per move: `<backup-dir>/<app>/<YYYYMMDD-HHMMSS>/`.
- Set IDs have second resolution; a suffix (`-2`) is added on collision.
- `BACKUP.json` stores per-file SHA-256 even in size-verification mode; the hash
  is simply not re-checked unless `--verify=hash` or the mode is `essential`.
- `full` mode treats an unreadable file as fatal (the set is deleted and exit 8
  is returned). A symlink that cannot be recreated is recorded under `skipped`
  with the reason, because Windows without developer mode cannot always create
  file symlinks; it is never silently dropped.
- After verification the tree is chmod'ed read-only. Windows ACL owner-only
  enforcement is not implemented beyond mode flags; the README and
  `docs/SAFETY.md` say guardrails are not a security boundary against a user
  deleting files by hand.
- Automatic rollback marks the set `rolled-back` rather than `verified` or
  `in-use`; it stays on disk until the user (or the retention policy) deletes
  it.
- **Retention.** `keep` asks after the move ([y/N], default No).
  `delete-on-success` deletes through the same protected path. `<N>d` is swept
  on later runs (currently `midas status`); a configured age policy is treated
  as the user's standing confirmation, so no prompt is shown. Every deletion
  still passes the journal, lock, and link-health rules.
- Deletion is implemented **only** in `internal/backup` (`Delete` /
  `CheckDelete`). It walks with `Lstat` semantics, clears read-only flags, does
  not follow links, and removes `BACKUP.json` last. `os.RemoveAll` is never used
  on a backup path.

## Restore points

- Policy default is `auto`. A failure is recorded in `BACKUP.json` and the
  manifest, and printed prominently; the move continues because the file backup
  is mandatory. `require` returns exit 8.
- Windows uses PowerShell `Checkpoint-Computer`; macOS uses
  `tmutil localsnapshot`; Linux uses Timeshift only when already root and
  installed. No elevation, no registry edits, no snapshot provisioning.

## Rollback

- Source order: `.midas-bak`, vault copy, backup set. `--source` can force one.
- A real folder found at the original path is renamed to
  `<original>.midas-rollback-<timestamp>`; it is never deleted.
- Rollback never deletes the vault copy or the backup set. It prints where they
  are.
- When restoring from an `essential` set, every skipped file is listed as not
  covered (`NotCovered` in the report).

## Doctor

- Current repairs: recreate a missing link when the vault copy exists, remove stale
  LOCKs whose PID is dead, remove orphaned `.midas-partial` directories, and
  rebuild a corrupt manifest by scanning known targets for links into the
  vault.
- Automatic merge when an app update replaces a link with a real folder is
  **not** implemented; doctor reports both trees and leaves the decision to the
  user. This is intentionally conservative with user data.
- Repairs prompt unless `--repair` or `--yes` is given. `--dry-run` never
  changes anything.

## Known limitations

- **Long paths on Windows**: verified with native tests
  (`internal/mover/longpath_windows_test.go`,
  `internal/platform/longpath_windows_test.go`): junction creation, move,
  restore, and rollback all work with full paths beyond the 260-character
  boundary on NTFS. Other filesystems and non-standard volumes remain
  untested; users on those report via `midas doctor`.
- **Resume** restarts the copy from scratch after validating the journal,
  ownership, and backup; already-copied files are not skipped.
- **Shims**: Windows `.cmd` wrappers forward `%*`; unusual quoting (embedded
  quotes in arguments) can still be mangled by `cmd.exe`. Unix wrappers
  forward `"$@"` exactly.
- **macOS TCC**: permission errors surface as clear copy errors; dedicated
  Full Disk Access guidance is not yet added to output.
- **Link replacement**: when an app update replaces a link with a real
  folder, doctor reports both trees and leaves the merge decision to the
  user; automatic merging is not implemented.
