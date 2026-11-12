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

## How it works

Each move follows the same pipeline: take a verified backup, copy (or same-volume rename) the data into the vault, verify the copy, swap the original aside, leave a link at the original path, health-check the link, then record the move in a manifest. Any failure triggers an automatic rollback to the untouched original.

- Targets land in `<vault>/apps/<app>/<target>`; state lives in `<vault>/.midas/` (manifest, journals, logs).
- Windows gets a **directory junction** (no admin required); macOS and Linux get a **symlink**.
- Links are recreated, never followed; copies and deletions use `Lstat` semantics.
- If the vault is unreachable, commands fail loudly (exit code 5) instead of silently falling back.

See [docs/SAFETY.md](docs/SAFETY.md) for the full guarantee list and [docs/RECOVERY.md](docs/RECOVERY.md) for recovery.

## Install

From source (Go 1.26+):

```
go install github.com/mevarx/Midas/cmd/midas@latest
```

Or download a checksum-verified release binary for Windows/Linux/macOS (amd64 or arm64). See [docs/INSTALL.md](docs/INSTALL.md) for checksum verification, upgrades, and uninstall. No admin rights are needed for normal use.

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

3. **Preview the move without changing anything:**

   ```
   midas plan claude-code
   midas analyze claude-code
   ```

4. **Move an app:**

   ```
   midas move claude-code
   ```

   You get a full plan first (what is copied, what is skipped, space needed, backup location), then a confirmation. Add `--dry-run` to see the plan without changing anything. Use `--target <name>` to move one folder of a multi-target app.

5. **Run the app with temp redirected:**

   ```
   midas run claude-code -- claude
   ```

   Optionally install a wrapper so typing `claude` already does this:

   ```
   midas shim claude-code --dir ~/.local/bin
   ```

6. **Check health at any time:**

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
| `midas resume <app>` | Restart a validated interrupted copy checkpoint |
| `midas plan <app>` | Read-only move plan: estimates, conflicts, transitions |
| `midas analyze <app>` | Read-only target and move-readiness analysis |
| `midas run <app> -- <cmd>` | Launch with temp redirected to the vault |
| `midas status` | Live link/vault health; `--json` for scripts |
| `midas restore <app>` | Move data back after a successful move |
| `midas rollback <app>` | Undo a failed or interrupted move (`--source auto\|bak\|vault\|backup`) |
| `midas backup list/show/verify/delete` | Manage backup sets |
| `midas apps list/show/validate/path` | Inspect app definitions |
| `midas doctor [--repair]` | Health and recovery report; repairs are explicit |
| `midas completion <shell>` | Print Bash, Zsh, Fish, or PowerShell completions |
| `midas shim <app>` | Wrapper that auto-applies `run` |
| `midas version [--commit]` | Print version (and build commit) |

Global flags: `--vault`, `--config`, `--dry-run`, `--yes`, `--json`, `--verbose`.
`move`/`resume`/`restore`/`rollback` accept `--copy-workers 1..16` (default 1) and `--target` (repeatable).
`move` accepts `--backup full|essential|none`, `--backup-dir`, `--restore-point auto|require|skip`, `--force`.
`run` accepts `--allow-fallback` and `--clean-tmp`.

Exit codes: `0` ok, `1` error, `2` usage, `3` app running, `4` insufficient space, `5` vault unreachable, `6` verification failed, `7` unhealthy, `8` backup/restore-point failed, `9` backup is protected. See [docs/COMMANDS.md](docs/COMMANDS.md) and [docs/JSON.md](docs/JSON.md).

## Supported apps

Built-in definitions live in [`apps/`](apps/). Add your own TOML in `midas apps path` — local files override built-ins with the same id.

| App | Targets |
|---|---|
| `claude-code` | `~/.claude` (leaves `~/.claude.json` alone) |
| `opencode` | `data`, `config`, `cache`, `state` XDG folders |
| `hermes` | `~/.hermes` |

Paths and process names vary by version — run `midas apps show <id>` and `midas list` before moving.

## Backups and safety net

- Backup mode defaults to `full`; `essential` copies only the files an app definition marks as important. `none` requires `--backup=none --i-understand-no-backup` and typing `no backup`.
- Verification defaults to per-file size; `--verify=hash` (or `verify = "hash"` in config) compares SHA-256.
- Backup sets are **protected**: one in `creating`, `verified`, or `in-use` state cannot be deleted. A completed set can only be deleted after its link passes a live health check (or the move was already restored).
- **System Restore points do not reliably restore personal folders** like `~/.claude`; Windows System Restore mainly covers system files, the registry and installed programs. The verified file backup is the real safety net. Midas records the restore point it attempted and tells you how to open it.
- Restore points need an elevated shell and System Protection enabled, and Windows allows roughly one automatic point per 24 hours. Midas never elevates itself and never edits the registry to bypass that.

## Configuration

`midas init` writes `config.toml` (`%APPDATA%\midas\` on Windows, `~/.config/midas/` on Linux, `~/Library/Application Support/midas/` on macOS; override with `MIDAS_CONFIG`, `--config`, or `MIDAS_VAULT` for the vault). Keys: `vault`, `verify` (`size`/`hash`), `confirm`, `backup` (`full`/`essential`/`none`), `backup_dir`, `backup_retention` (`keep`/`delete-on-success`/`30d`), `restore_point` (`auto`/`require`/`skip`), `allow_fallback`, `shim_dir`. See [docs/CONFIGURATION.md](docs/CONFIGURATION.md).

## Adding an app

Write one TOML file; no Go code. See [docs/ADDING_AN_APP.md](docs/ADDING_AN_APP.md) and validate it:

```
midas apps validate ./my-app.toml
```

## FAQ

**My vault drive is disconnected.** `status`, `run` and `move` fail loudly with exit code 5. Midas never silently falls back to the system temp directory. `run --allow-fallback` opts in explicitly.

**An app update replaced the link with a real folder.** `midas doctor` detects this and reports both trees; merge deliberately, then re-run doctor. Automatic merging is not implemented.

**Can I keep the vault on the same drive?** Yes — for organization, not to free space. `init` and every `move` warn when the vault and backup share a volume.

**WSL?** Out of scope. Moving Linux paths used inside WSL is not supported.

**Network drives?** Not supported. Junctions to network shares are detected and refused.

**What about hard links?** Copies duplicate each linked name; same-volume moves keep them because the tree is renamed.

**Can I move a OneDrive/Dropbox/iCloud folder?** Not supported. Pause sync and make files fully local first; placeholder hydration and sync writes can fail or change files mid-copy.

**Does Midas call home?** No telemetry, no network calls, no auto-update.

## Documentation

- [docs/COMMANDS.md](docs/COMMANDS.md) — command, flag, and exit-code reference
- [docs/SAFETY.md](docs/SAFETY.md) — guarantees, limits, and manual recovery
- [docs/RECOVERY.md](docs/RECOVERY.md) — diagnosis, resume eligibility, recovery steps
- [docs/CONFIGURATION.md](docs/CONFIGURATION.md) — config keys and schema version
- [docs/ADDING_AN_APP.md](docs/ADDING_AN_APP.md) — app definition format
- [docs/JSON.md](docs/JSON.md) — machine-readable output contract
- [docs/PLATFORMS.md](docs/PLATFORMS.md) — CI coverage and platform limitations
- [docs/COMPATIBILITY.md](docs/COMPATIBILITY.md) — format and platform policy
- [docs/PERFORMANCE.md](docs/PERFORMANCE.md) — copy benchmarks and methodology
- [docs/INSTALL.md](docs/INSTALL.md) — checksum verification, upgrade, uninstall
- [docs/DECISIONS.md](docs/DECISIONS.md) — implementation choices
- [docs/SECURITY_REVIEW.md](docs/SECURITY_REVIEW.md) — safety review record
- [docs/RELEASE.md](docs/RELEASE.md) — release gates and qualification checklist
- [docs/SUPPORT.md](docs/SUPPORT.md) — support and maintenance boundaries
- [SECURITY.md](SECURITY.md) — private vulnerability reporting
- [CONTRIBUTING.md](CONTRIBUTING.md) — development workflow

## License

MIT — see [LICENSE](LICENSE).
