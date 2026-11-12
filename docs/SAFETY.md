# Safety model

Midas moves data; a bug could cost work. This document states what the tool
guarantees, what it does not, and how to recover by hand.

## What Midas guarantees

1. **No deletion before verification.** An original folder is renamed aside,
   never deleted, until the vault copy has been verified (file count,
   directory count, total size, per-file size; hashes when requested) and the
   link has passed a live health check.
2. **A verified backup first.** No code path starts moving data without a
   backup set in `verified` state, unless the user typed
   `--backup=none --i-understand-no-backup` and the phrase `no backup`.
3. **Recoverable interruption.** Operational failures attempt rollback. A
   Ctrl+C or termination during a copy leaves the original, owned partial,
   backup, and journal in place; `midas doctor` reports the checkpoint and
   `midas resume` is offered only when its paths and backup validate.
4. **Reversibility.** `midas restore` reverses a completed move. The vault copy
   is removed only after the restored tree is verified.
5. **Protected backups.** A backup set in `creating`, `verified` or `in-use`
   state cannot be deleted. A completed set can be deleted only after its link
   passes a health check right now (or its manifest entry is gone), and the user
   types the app name.
6. **No following links.** Copies and deletions use `Lstat` semantics; links are
   recreated, never traversed while deleting.
7. **No silent fallback.** If the vault is unreachable, commands fail with exit
   code 5. `run --allow-fallback` is the only opt-in.

### The app-running check is point-in-time

Midas asks the OS whether the app's processes are running before a move, again
immediately before it swaps the original aside (just before the link is
created), and again immediately before it deletes `<original>.midas-bak`. That
is all a process check can be: a snapshot. An app can start between the last
check and the rename or unlink that follows, and Midas cannot eliminate that
race.

On Unix this matters most: a process that is already running with open file
descriptors keeps writing to whatever inode those descriptors refer to, even
after the path was renamed to `.midas-bak` or the link was put in place. Such
writes land in the moved-aside tree, not in the vault copy. Midas rechecks at
both destructive points and aborts into rollback if it catches the app, but a
start in the instant after a check is invisible to it.

You must close the app before moving its data. `--force` skips the check
entirely and is not a way to make a running app safe; it only suppresses the
warning.

## What Midas does not do

- **Restore points are not a backup of your profile.** Windows System Restore
  mainly protects system files, the registry and installed programs. It will not
  reliably bring back `~/.claude`. macOS local snapshots can be purged by the
  OS. The verified file backup is the real safety net.
- **Midas cannot stop you from deleting backup files by hand.** Read-only
  filesystem flags and owner-only modes are guardrails, not a security control.
- **Midas does not clean app data** (there is no `midas clean` command), manage drives, or
  support network shares as vaults.
- **Midas makes no network calls**, has no telemetry, and cannot recover data
  from a drive that has physically failed. Keep the backup on a *different*
  drive; `init` asks for this and warns otherwise.
- **Hard links are not preserved by copies**; linked files are duplicated on
  cross-volume moves and restores (same-volume moves use a rename and keep
  them).
- **Cloud-synced folders** with placeholder/hydration behavior are
  unsupported; pausing sync and making files local first is the user's
  responsibility.

## If something goes wrong

Run:

```
midas status
midas doctor
```

Then, depending on the situation:

| Situation | Command |
|---|---|
| Move finished, want the data back on the original drive | `midas restore <app>` |
| Move failed or was interrupted | `midas rollback <app>` |
| Link broken, vault copy present | `midas doctor --repair` |
| An app update replaced the link with a real folder | doctor reports both trees; merge manually, then re-run doctor |
| Backup verification fails | keep the set; copy files out by hand from `<set>/files/` |

### Manual recovery map

During a move these paths may exist:

| Path | Meaning |
|---|---|
| `<original>` | Either the real data (untouched), or a link after success |
| `<original>.midas-bak` | The untouched original, moved aside before linking |
| `<original>.midas-partial` | Incomplete restore/rollback copy; preserve until its journal and ownership are validated |
| `<vault>/apps/<app>/<target>` | Verified vault copy |
| `<vault>/apps/<app>/<target>.midas-partial` | Incomplete move copy; unknown or mismatched ownership is never cleaned automatically |
| `<vault>/.midas/journal/<app>-<target>.json` | In-progress move state |
| `<backup>/<app>/<id>/files/` | Mirrored backup; usable without Midas |

Never delete `<original>.midas-bak` before the link is healthy. Do not delete
partial paths based on their suffix; if ownership cannot be proven, preserve
them and review [RECOVERY.md](RECOVERY.md).

## Reporting a data-loss bug

Include your OS, `midas version`, the app and its version, the last 50 lines of
`<vault>/.midas/logs/<date>.log`, and `midas status --json`. Do not paste file
contents from a backup set: they may contain credentials.
