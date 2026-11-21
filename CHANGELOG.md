# Changelog

Changes that affect behavior, compatibility, data safety, or recovery are
recorded here. Release notes are generated from the changelog and reviewed
before publication.

## 0.1.0

First public release.

### What Midas does

Midas moves the heavy data folders of AI coding tools off a small system
drive onto another drive or mount point, leaving a link behind so the tools
never notice. Every move follows one pipeline: verified safety backup,
verified copy (or same-volume rename), swap the original aside, create the
link, live health check, record the manifest. Any failure rolls back to the
untouched original.

### Coverage

- 19 built-in app definitions: Claude Code, OpenCode, Hermes, Codex CLI,
  Gemini CLI, GitHub Copilot CLI, Qwen Code, iFlow CLI, Aider, Goose, Crush,
  Amp, Factory Droid, Cursor, Windsurf/Devin Desktop, Zed, Cline, Roo Code
  and Continue.
- Defaults researched against vendor docs and path-resolution source code
  on Windows, macOS and Linux; the evidence and popularity data live in
  docs/RESEARCH.md.
- Documented relocation overrides are honoured when set (`CODEX_HOME`,
  `CLAUDE_CONFIG_DIR`, `HERMES_HOME`, `GEMINI_CLI_HOME`, `COPILOT_HOME`,
  `COPILOT_CACHE_HOME`, `QWEN_HOME`, `CRUSH_GLOBAL_CONFIG`,
  `CRUSH_GLOBAL_DATA`, `CRUSH_CACHE_DIR`, `AMP_HOME`, `CURSOR_DATA_DIR`,
  `CLINE_DIR`, `CONTINUE_GLOBAL_DIR`, `XDG_*`) via `${VAR:-default}` path
  expansion. A referenced variable that is unset or empty without a default
  is a hard error, never a silently empty path segment.
- Split stores ship as separate targets (Copilot data+cache, Cursor
  userData+CLI+sync stores, Zed data+config, Cline home+legacy store,
  Windsurf cascade+both userData roots) so a move never silently leaves
  half the data behind.
- Local TOML definitions override built-ins by id; no Go code needed.

### Safety and recovery

- Windows directory junctions (no admin required); macOS/Linux symlinks.
  Links are recreated, never followed; copies and deletions use Lstat
  semantics.
- Backup modes full/essential/none; essential copies only the files a
  definition marks important. Verification by size or SHA-256. Backup sets
  are protected against deletion while creating, verified or in use.
- `midas rollback` undoes a failed or interrupted move; `midas resume`
  restarts a validated interrupted copy; `midas restore` moves data back
  after a successful move; `midas doctor` finds and explicitly repairs
  problems.
- Advisory vault lock serializes mutating commands; operation journals are
  created with O_EXCL and fsynced with their parent directories on Unix.
- App-running checks re-run immediately before the swap and before deleting
  the original; case collisions abort before commit; torn backups are
  discarded instead of marked verified.
- Unreachable vaults fail loudly (exit code 5); `run` never silently falls
  back to the system temp directory without `--allow-fallback`.

### Usability

- `midas list` works before `init` (discovery mode); a configured but
  unreachable vault remains a hard error.
- Read-only `plan` and `analyze`, schema-versioned JSON output, stable exit
  codes, Bash/Zsh/Fish/PowerShell completions, launch shims, bounded opt-in
  copy workers, config schema versioning.

### Quality gates

- Golden registry tests pin every built-in target per OS from any host;
  every override is proven to redirect expansion; host containment is
  validated for all definitions.
- CI matrix on Windows, Ubuntu and both macOS architectures with `-race`,
  staticcheck, govulncheck, six-target cross-compilation and completion
  parsing; tag-triggered release qualification gate blocks publication
  while any item is unmet.
- Build identity is derived from the git tag (GoReleaser ldflags) or
  embedded build info; no release number is hardcoded in the repository.
- LF line endings pinned via `.gitattributes` for identical tooling
  behavior on every platform.
