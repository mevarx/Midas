# Midas

**Everything you touch turns to storage space.**

Midas is a cross-platform command-line tool that moves the heavy data folders of developer tools (Claude Code, OpenCode, Hermes, and others) off a small system drive onto another drive or mount point — and leaves a link behind so the tools never notice. It also redirects each tool's temporary files to the same destination.

```
midas init --vault D:\Midas
midas move claude-code
midas run claude-code -- claude
```

## ⚠️ Back up before first use

Midas takes its own verified safety backup before it changes anything, but this is beta software. Keep an independent copy of anything irreplaceable before your first move. Midas never deletes user data until a copy has been verified and the link health-checked, and every move can be undone with `midas rollback` — but no tool is a substitute for a real backup.

## What it does

- Moves an app's data folders into a vault (`D:\Midas`, `/mnt/data/midas`, …).
- Leaves a **directory junction** on Windows (no admin required) or a **symlink** on macOS and Linux.
- Takes a **verified file backup** first (mode `full` by default), and, where the OS allows, a best-effort restore point.
- Tracks every move in a manifest, so `status` always tells the truth and `restore` reverses a move.
- Rolls back automatically if anything fails mid-move.
- Launches apps with `TEMP`, `TMP` and `TMPDIR` pointed at the vault.

## Install

From source (Go 1.26+):

```
go install github.com/midas-cli/midas/cmd/midas@latest
```

Or download a release binary for windows/linux/darwin (amd64 or arm64). No admin rights are needed for normal use.

## Quickstart

1. **Choose a vault on another drive** (recommended):

   ```
   midas init --vault D:\Midas --backup-dir E:\MidasBackup
   ```

   `init` asks for a backup location and warns when the vault and backup share a volume.

2. **See what is known and how big it is:**

   ```
   midas list
   ```

3. **Move an app:**

   ```
   midas move claude-code
   ```

   You get a full plan first (what is copied, what is skipped, space needed, backup location), then a confirmation. Add `--dry-run` to see the plan without changing anything.

4. **Run the app with temp redirected:**

   ```
   midas run claude-code -- claude
   ```

   Optionally install a wrapper so typing `claude` already does this:

   ```
   midas shim claude-code --dir ~/.local/bin
   ```

5. **Check health at any time:**

   ```
   midas status        # exits non-zero if anything is unhealthy
   midas doctor        # finds and offers to repair problems
   ```

## Commands

| Command | Purpose |
|---|---|
| `midas init` | Set the vault and backup directory; write config |
| `midas list` | Apps, targets, sizes, moved/not-moved state |
| `midas move <app>` | Move targets into the vault and create links |
| `midas run <app> -- <cmd>` | Launch with temp redirected to the vault |
| `midas status` | Live link/vault health; `--json` for scripts |
| `midas restore <app>` | Move data back after a successful move |
| `midas rollback <app>` | Undo a failed or interrupted move |
| `midas backup list/show/verify/delete` | Manage backup sets |
| `midas apps list/show/validate/path` | Inspect app definitions |
| `midas doctor` | Detect broken links, stale locks, partial copies |
| `midas shim <app>` | Wrapper that auto-applies `run` |

Global flags: `--vault`, `--config`, `--dry-run`, `--yes`, `--json`, `--verbose`.

Exit codes: `0` ok, `1` error, `2` usage, `3` app running, `4` insufficient space, `5` vault unreachable, `6` verification failed, `7` unhealthy, `8` backup/restore-point failed, `9` backup is protected.

## Supported apps

Built-in definitions live in [`apps/`](apps/). Add your own TOML in `midas apps path` — local files override built-ins with the same id.

| App | Targets |
|---|---|
| `claude-code` | `~/.claude` (leaves `~/.claude.json` alone) |
| `opencode` | XDG data/config/cache/state folders for OpenCode |
| `hermes` | `~/.hermes` |

Paths and process names vary by version — run `midas apps show <id>` and `midas list` before moving.

## Backups and restore points

- Backup mode defaults to `full`; `essential` copies only the files an app definition marks as important. `none` requires `--i-understand-no-backup` and a typed confirmation.
- Backup sets are **protected**: one in `creating`, `verified`, or `in-use` state cannot be deleted. A completed set can only be deleted after its link passes a live health check (or the move was already restored).
- **System Restore points do not reliably restore personal folders** like `~/.claude`; Windows System Restore mainly covers system files, the registry and installed programs. The verified file backup is the real safety net. Midas records the restore point it attempted and tells you how to open it.
- Restore points need an elevated shell and System Protection enabled, and Windows allows roughly one automatic point per 24 hours. Midas never elevates itself and never edits the registry to bypass that.

## FAQ

**My vault drive is disconnected.** `status`, `run` and `move` fail loudly with exit code 5. Midas never silently falls back to the system temp directory. `run --allow-fallback` opts in explicitly.

**An app update replaced the link with a real folder.** `midas doctor` detects this. In v1 it reports the vault copy so you can merge deliberately; automated merging is planned.

**Can I keep the vault on the same drive?** Yes — for organization, not to free space. `init` and every `move` warn when the vault and backup share a volume.

**WSL?** Out of scope for v1. Moving Linux paths used inside WSL is tracked as an open question.

**Network drives?** Not supported in v1. Junctions to network shares are detected and refused.

**Does Midas call home?** No telemetry, no network calls, no auto-update.

## Adding an app

Write one TOML file; no Go code. See [docs/ADDING_AN_APP.md](docs/ADDING_AN_APP.md) and validate it:

```
midas apps validate ./my-app.toml
```

## Safety and design documents

- [docs/DECISIONS.md](docs/DECISIONS.md) — implementation choices
- [docs/SAFETY.md](docs/SAFETY.md) — what is guaranteed and manual recovery
- [docs/ADDING_AN_APP.md](docs/ADDING_AN_APP.md) — definition format
- [CONTRIBUTING.md](CONTRIBUTING.md)

## License

MIT — see [LICENSE](LICENSE).
