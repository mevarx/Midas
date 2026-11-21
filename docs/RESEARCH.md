# AI harness storage research — November 2026

Snapshot: **2026-11-19**. This report records which AI coding harnesses are
most used, where each one stores its data on Windows, macOS and Linux, and
how those findings map to Midas built-in definitions. It is the evidence
base for the registry: every default path shipped in `apps/*.toml` is pinned
by `TestBuiltinGoldenPaths` in `internal/apps/registry_test.go` against the
tables below.

Confidence levels used throughout:

- **verified** — read from vendor docs or the tool's path-resolution source.
- **partial** — vendor docs plus binary analysis or trusted third-party
  corpora; some subdirectories are not vendor-published.
- **inferred** — mapped from a documented layout on another OS.

## 1. Which harnesses are most used

Three surveys with different sampling agree on the shape of the market:

| Harness | SO 2026, past year (n=12,255)[8] | JetBrains AI Pulse, at work (n>10,000)[9] | Pragmatic Engineer, most-loved (n=906)[10] |
|---|---|---|---|
| Claude Code | 65.5% | 18% | #1 most-used; 46% |
| GitHub Copilot | 58.7% | 29% | 9% |
| OpenAI Codex | 29.5% | 3% | — |
| Cursor | 25.5% | 18% | 19% |
| Google Antigravity | 16.0% | 6% | — |
| Gemini Code Assist | 14.6% | — | — |
| OpenCode | 13.6% | — | — |
| JetBrains AI | 12.8% | 11% | — |
| Windsurf | 6.5% | — | — |
| Cline | 4.5% | — | — |
| Continue | 3.8% | — | — |
| Kilo Code | 3.2% | — | — |
| Aider | 2.1% | — | — |

Package-registry volume (week of 2026-11-12..18) shows the same leaders and
Codex's fast rise: `@openai/codex` 27.3M downloads[2],
`@anthropic-ai/claude-code` 20.2M[1], `opencode-ai` 4.0M[4],
`@github/copilot` (CLI) 1.8M[6], `@google/gemini-cli` 0.50M[3],
`@qwen-code/qwen-code` 0.15M[5]; Aider's PyPI package `aider-chat` took
~102k downloads in the last week[7].

Caveats that matter when reading these numbers:

- npm download counts include CI installs, Docker builds and lockfile
  refetches, so they overstate human users and are not comparable across
  packaging models (Cursor and Windsurf are not npm-distributed at all;
  `@github/copilot` measures the standalone CLI, not IDE Copilot)[2][6].
- Stack Overflow's question is "used in the past year" on a self-selected
  web sample — closer to awareness-plus-trial than daily use[8]. JetBrains
  asks about use **at work** among weighted professionals[9]; Pragmatic
  Engineer's newsletter sample is senior-heavy[10].
- 70% of developers run 2–4 AI coding tools simultaneously[10], and 83–90%
  use at least one AI tool[8][9]. A storage-relocation tool therefore has to
  cover many harnesses at once, not just the market leader.

**Compatibility priority derived for Midas:** Cursor (largest single-store
growth), Claude Code, Copilot/Codex/Gemini terminal agents, OpenCode, then
the VS Code extension agents (Cline > Roo Code > Continue) and the remaining
CLI agents. Zed is popular as an editor (95k stars) but its agent panel has
no separate survey row[8].

## 2. Terminal agents

### Claude Code — `claude-code` (verified)

| OS | Default data |
|---|---|
| Windows | `%USERPROFILE%\.claude` |
| macOS | `~/.claude` |
| Linux | `~/.claude` |

`CLAUDE_CONFIG_DIR` replaces the directory; when it is set, `.claude.json`
lives inside it instead of at `~/.claude.json`[11][12]. Midas moves the
directory the variable points at and never touches the loose
`~/.claude.json` file. Growth: `projects/` session transcripts,
`shell-snapshots/`, `todos/`.

### OpenAI Codex CLI — `codex` (verified)

One root on every OS: `~/.codex` (`%USERPROFILE%\.codex` on Windows); no XDG
behaviour on Linux[14]. `CODEX_HOME` replaces the root but **must already
exist** — a missing directory is a hard startup error, and the value is
canonicalized, so symlinks resolve to their target[13][14]. Heavy children:
`sessions/` and `archived_sessions/` (rollout JSONL), `log/`,
`shell_snapshots/`, `history.jsonl`, `.tmp/`, and SQLite state/log databases
(`state_*.sqlite`, `logs_*.sqlite`)[13]. `config.toml` can pin `log_dir` and
`sqlite_home` outside the root, so a relocation must read it first[13].
macOS Seatbelt refuses a symlinked `CODEX_HOME` unless
`allow_symlinked_codex_home = true` is set in the user config[13].

### Gemini CLI — `gemini-cli` (verified, product in sunset)

Global dir is `<home>/.gemini` on every OS[15][16]. **`GEMINI_CLI_HOME` is
the parent directory**, not the `.gemini` dir itself — the CLI appends
`.gemini`[16]. Heavy children: `tmp/<projectId>/chats` (session JSONL),
`checkpoints/` (shadow git), `tool-outputs/`, `logs/`, `history/`,
`extensions/`, `bin/litert` models[15]. Under macOS Seatbelt sandbox mode
the runtime half moves to `~/.cache/.gemini`[15]. Machine-wide settings live
in `/etc/gemini-cli`, `C:\ProgramData\gemini-cli` or
`/Library/Application Support/GeminiCli` and are never relocated[15].
Google announced on 2026-05-19 that Gemini CLI is folding into Antigravity
CLI; unpaid tiers were cut over on 2026-06-18, so this layout is frozen[17].
Runs on Node: process detection must match `node` argv, not an image name.

### GitHub Copilot CLI — `copilot-cli` (verified)

Config root `~/.copilot` on every OS, replaced wholesale by
`COPILOT_HOME`[18]. The cache is **separate**: `%LOCALAPPDATA%\copilot`,
`~/Library/Caches/copilot`, `$XDG_CACHE_HOME/copilot`, overridable with
`COPILOT_CACHE_HOME`; `COPILOT_HOME` does not touch it[18]. Heavy children:
`session-state/`, `logs/`, `session-store.db` (SQLite, live writers),
`command-history-state/`, `installed-plugins/`, `plugin-data/`[18]. On Linux
the CLI migrates legacy XDG config into `~/.copilot` at startup when
`COPILOT_HOME` is unset — removing the dir instead of linking it triggers a
re-migration[18]. Managed settings under `%ProgramFiles%`, `/etc` or
`/Library/Application Support` are admin-owned and never moved[18]. Ships
native per-OS binaries (`copilot.exe`/`copilot`), not a Node shim[18].

### Qwen Code — `qwen-code` (verified)

Global dir `~/.qwen` on every OS; `QWEN_HOME` is the **final directory**
(unlike Gemini's parent semantics), empty treated as unset, relative values
resolve against cwd, and Qwen aborts if it resolves inside the project
repo[19]. `QWEN_RUNTIME_DIR` splits the runtime half (`projects/`, `tmp/`,
`debug/`) from credentials — often the better relocation lever[19]. Heavy
children: `projects/<cwd>/chats`, `tmp/<hash>/checkpoints`,
`tool-results/`, `file-history/`, `debug/`[19]. A PR to move Linux installs
to `$XDG_CONFIG_HOME/qwen` was closed and is not in main[19]. Node shim:
match `node` argv containing `qwen`.

### iFlow CLI — `iflow` (partial — closed source, service shut down)

Docs place everything in `~/.iflow` (settings, `tmp/<hash>/shell_history`,
`snapshots/<hash>` shadow git repos, `cache/<hash>/checkpoints`)[20]. No
home-directory env override is documented[20]. The Windows mapping to
`%USERPROFILE%\.iflow` is inferred from Node's `os.homedir()` semantics.
The hosted service shut down on 2026-04-17 and the npm package is frozen at
0.5.19, so the layout is legacy[44][45]. iFlow is a gemini-cli fork, which
is why the layout mirrors `~/.gemini`[20].

### Aider — `aider` (verified)

The only per-user directory is `~/.aider` (analytics, `oauth-keys.env`,
`caches/`) — a few MB[21][22]. **The bulk of Aider's growth is
per-repository**: `.aider.tags.cache.v4/` (repo-map diskcache),
`.aider.chat.history.md`, `.aider.input.history` at the repo root[21].
Relocating `~/.aider` reclaims little; repo dotfiles need per-project local
definitions. The global config `~/.aider.conf.yml` is a file, not a
folder[21]. Every CLI option has an `AIDER_*` env var, but none relocates
the home directory[21].

### Goose — `goose` (verified)

The CLI uses etcetera's app strategy: XDG on macOS/Linux, Windows strategy
on Windows only[23].

| OS | config | data | state | cache |
|---|---|---|---|---|
| Windows | `%APPDATA%\Block\goose\config` | `%APPDATA%\Block\goose\data` | = data | `%LOCALAPPDATA%\Block\goose\cache` |
| macOS | `~/.config/goose` | `~/.local/share/goose` | `~/.local/state/goose` | `~/.cache/goose` |
| Linux | `$XDG_CONFIG_HOME/goose` | `$XDG_DATA_HOME/goose` | `$XDG_STATE_HOME/goose` | `$XDG_CACHE_HOME/goose` |

`~/Library/Application Support/Goose` belongs to the **desktop app**, not
the CLI[23]. Sessions are one SQLite DB (`sessions/sessions.db`, WAL) since
v1.10[23]. `GOOSE_PATH_ROOT` is a first-class relocation knob, but it
changes the layout *shape* (`<root>/config|data|state`), which a single
default-path template cannot express — Midas documents it and defers to a
local override definition. Secrets normally live in the OS keyring[23].

### Crush — `crush` (verified)

Crush deliberately uses `~/.config/crush` on **every OS including Windows**
(no `%APPDATA%` branch)[24]. Global data: `%LOCALAPPDATA%\crush` on Windows,
`$XDG_DATA_HOME/crush` elsewhere; cache: `%LOCALAPPDATA%\crush\cache` /
`$XDG_CACHE_HOME/crush`[24]. Overrides: `CRUSH_GLOBAL_CONFIG`,
`CRUSH_GLOBAL_DATA` (directories — the code appends `crush.json`),
`CRUSH_CACHE_DIR`[24]. Most growth is per-project: `<project>/.crush` holds
`crush.db` (SQLite WAL) and logs, so moving the global dirs reclaims little
unless `CRUSH_DATA_DIR`/`--data-dir` also points elsewhere[24].

### Amp — `amp` (partial — closed source)

The security page states credentials live in `~/.local/share/amp/secrets.json`
on macOS/Linux and `%USERPROFILE%\.local\share\amp\secrets.json` on
Windows; settings in `~/.config/amp/settings.json` (raw XDG on Windows
too)[25]. The installer home is `~/.amp` (`AMP_HOME`), holding `bin/` and
versioned binaries; `install.sh` symlinks `~/.local/bin/amp` into it[26].
`threads/` is a local history cache — authoritative threads are
server-side, so it may lag or shrink (layout reported by third-party thread
readers, not vendor docs)[25]. Windows is WSL-oriented; native paths are
documented but should be verified per host[25].

### Factory Droid — `droid` (partial — closed source)

Everything under `~/.factory` on every OS; binary analysis found no
`%APPDATA%` or Library paths[27]. Official docs verify
`settings.json`/`settings.local.json`[27]; sessions (JSONL per project),
`logs/`, `cache/session-index/index.db` (SQLite), `snapshots/`,
`worktrees/`, `updates/` come from binary string analysis of droid
0.233.0[27]. `auth.v2.file` is encrypted with a key in the OS keyring —
moving is safe, copying to another machine is not[27].
`FACTORY_HOME_OVERRIDE` exists in the binary but is undocumented; Midas
notes it rather than shipping it. A background daemon (`droid daemon`)
writes `factoryd.log`[27].

### Hermes Agent — `hermes` (verified)

`HERMES_HOME` selects the home: default `~/.hermes` on POSIX,
**`%LOCALAPPDATA%\hermes` on Windows**[28][29]. The root mixes config,
secrets, `state.db` (SQLite WAL), sessions, memories, logs, cache, and a
full `hermes-agent/` source checkout with Python environments — the
checkout plus `tools/` and `installs/` are the bulk of the footprint
(multi-GB observed on a real Windows install)[29]. Lock files
(`gateway.lock`, `*.lock`) live in the root: stop the agent and gateway
before moving[28].

### OpenCode — `opencode` (verified)

Raw XDG semantics with **no platform branching**, Windows included[30]:

| Role | All OSes |
|---|---|
| data | `$XDG_DATA_HOME/opencode` or `~/.local/share/opencode` |
| config | `$XDG_CONFIG_HOME/opencode` or `~/.config/opencode` |
| cache | `$XDG_CACHE_HOME/opencode` or `~/.cache/opencode` |
| state | `$XDG_STATE_HOME/opencode` or `~/.local/state/opencode` |

On Windows that is `%USERPROFILE%\.local\share\opencode` — **not**
`%LOCALAPPDATA%`; on macOS `~/.local/share/opencode` — **not**
`~/Library/Application Support`[30][31]. Sessions live in one SQLite DB
(`opencode.db` + WAL sidecars); `snapshot/` and `repos/` hold git trees that
can grow to many GB; advisory locks live under the state dir, so data and
state must move together[30][31]. (Earlier Midas definitions shipped the
wrong Windows/macOS defaults for this app; the current registry corrects
them.)

## 3. IDE harnesses

### Cursor — `cursor` (verified)

VS Code fork, so userData follows Code's rules[34]:

| OS | userData | CLI data | sync stores |
|---|---|---|---|
| Windows | `%APPDATA%\Cursor` | `%USERPROFILE%\.cursor` | `%LOCALAPPDATA%\Cursor\AgentStores` |
| macOS | `~/Library/Application Support/Cursor` | `~/.cursor` | inside userData |
| Linux | `~/.config/Cursor` | `~/.cursor` | `~/.local/state/cursor/agent-stores` |

The chat store is `User/globalStorage/state.vscdb` (ItemTable +
cursorDiskKV): 30–125 GB reported by heavy users, individual cases of 38,
42.6, 49.8 and 52 GB, ~1 GB/day growth, plus a stale `state.vscdb.backup`
that can double the footprint[32][34]. `conversation-search.db`,
`anysphere.cursor-commits/` shadow copies and the embedded
`anysphere.cursor-agent-worker` (~608 MB CLI) live beside it[34]. Chats
exist **twice**: `~/.cursor/projects/<cwd>/agent-transcripts/*.jsonl` is a
second copy[34]. `code.lock` and the single-instance socket mean every
Cursor process (including agent workers) must be exited before a move[34].
Cursor staff call `--user-data-dir` the supported relocation and a
junction on `User\globalStorage` "a working option, even if unofficial"[33].
CLI overrides: `CURSOR_DATA_DIR`, `CURSOR_CONFIG_DIR`,
`CURSOR_WORKTREES_ROOT`[34].

### Windsurf / Devin Desktop — `windsurf` (partial — mid-rename)

Dual identity: `%APPDATA%\Windsurf` and `%APPDATA%\Devin` (and the macOS/Linux
equivalents) can both exist with different content; the app reads both and
writes to the new one[35]. **Chat history is not in userData** — it is
`~/.codeium/windsurf/cascade/*.pb` (schema-less protobuf), with `memories/`,
`global_workflows/`, `database/`, `code_tracker/`, `codemaps/` beside it[35].
`~/.codeium` is shared with the Codeium JetBrains/Eclipse/Neovim plugins, so
only the `windsurf` subfolder is a safe target[35]. Typical footprint ~5 GB,
10–15 GB routine on monorepos; extensions survive app deletion (~1.75 GB
observed)[35]. No vendor relocation flag exists; an update may recreate the
legacy folder[35].

### Zed — `zed` (verified)

Config and data are split on every OS[36][37]:

| OS | data | config |
|---|---|---|
| Windows | `%LOCALAPPDATA%\Zed` | `%APPDATA%\Zed` |
| macOS | `~/Library/Application Support/Zed` | `~/.config/zed` |
| Linux | `$XDG_DATA_HOME/zed` | `$XDG_CONFIG_HOME/zed` |

`threads/threads.db` holds every agent thread as a zstd-compressed blob,
rewritten whole on each save: 472 MB observed with 200+ threads[37]. It is
opened **without WAL and without busy_timeout**, and a background worker
logs and drops failed writes — repointing it under a running Zed can
silently lose state[37]. macOS ignores XDG variables[37]. The instance
socket `zed-<channel>.sock` lives in the data dir[37]. Zed's
`--user-data-dir` moves data and config together and is the supported
alternative to a link[36].

## 4. VS Code extension agents

### Cline — `cline` (verified)

4.x writes to `~/.cline` on every OS (shared by the IDE extension, the CLI
and the JetBrains plugin): `data/tasks/<id>/` transcripts + `checkpoints/`
shadow git repos, `data/db/*.sqlite`, `worktrees/`[38][39]. Checkpoints are
committed after **every tool use** and are the biggest storage sink found
in this research: 120 GB reported in one issue, 4 GB+ per task
repeatedly[39]. Legacy installs keep tasks under
`<userData>/User/globalStorage/saoudrizwan.claude-dev/tasks` — both trees
must move or half the space stays[39]. The same extension in Cursor,
Windsurf or VSCodium has separate globalStorage roots[39]. Native
overrides: `CLINE_DIR` (whole home), `CLINE_DATA_DIR`,
`CLINE_SESSION_DATA_DIR`, `CLINE_DB_DATA_DIR` and per-file variants[38].
Remote/SSH sessions write globalStorage on the server, not the laptop[39].

### Roo Code — `roo-code` (verified, project idle)

Primary store is
`<userData>/User/globalStorage/rooveterinaryinc.roo-cline` (lowercased id;
wrong case on Linux silently creates a second empty store): `tasks/<id>/`
JSON transcripts, `checkpoints/` shadow git, `settings/`, provider model
caches[40][41]. Shared config in `~/.roo` (rules, worktrees); the `roo` CLI
keeps a parallel tree in `~/.vscode-mock`[41]. Roo ships
`roo-cline.customStoragePath` — check it before junctioning, or Midas moves
a path Roo no longer reads[41]. Other hosts (Cursor, Windsurf, VSCodium,
Insiders) and `~/.vscode-server` have separate roots[41]. Last release
3.54.0 (2026-05-15), no commits since — stable but declining[41].

### Continue — `continue` (verified, archived)

Everything in `~/.continue` on every OS — it ignores XDG and platform
app-data conventions entirely[42]. `index/` holds `index.sqlite`,
`lancedb/` vectors, `autocompleteCache.sqlite`, `docs.sqlite`; whole-dir
footprints of ~30 GB are reported[43]. `sessions/` is shared with the
JetBrains plugin and `cn` CLI; `dev_data/` grows silently; `.utils/` holds
a downloaded Chromium[42][43]. `CONTINUE_GLOBAL_DIR` relocates the whole
dir (relative values resolve against cwd — a footgun)[42]. The VS Code
`globalStorage/continue.continue` folder is only a small state stub[42].
Archived: 2.0.0/2.1.0 was the final release (June 2026) and the repo is
read-only[46].

## 5. What Midas ships from this research

19 built-ins: the 16 above plus the three pre-existing definitions
(`claude-code`, `opencode`, `hermes`), whose Windows/macOS defaults were
corrected against the sources cited here.

Policy decisions:

1. **Only documented overrides ship in path templates.** Undocumented or
   shape-changing knobs (`GOOSE_PATH_ROOT`, `FACTORY_HOME_OVERRIDE`,
   `QWEN_RUNTIME_DIR`, `CLINE_DATA_DIR`, Roo's `customStoragePath`) are
   recorded in each definition's `[notes]`; users who set them can write a
   local override TOML (`midas apps path`).
2. **Every default is golden-tested on all three OSes from any host**
   (`TestBuiltinGoldenPaths`), and every `${VAR:-default}` override is
   proven to redirect expansion (`TestBuiltinEnvOverrides`). A path change
   must update the golden table and this report together.
3. **Split stores ship as separate targets** (Copilot data+cache, Cursor
   userData+cli+agent-stores, Zed data+config, Cline home+legacy store,
   Windsurf cascade+both userData roots) so a move never silently leaves
   half the data behind.
4. **Process detection favours false positives over data loss.** Node-shim
   agents match `node` + argv substring; extension agents treat any open
   VS Code-family editor as a potential writer.
5. **Live databases are called out in notes** (SQLite WAL sidecars, Zed's
   no-WAL threads.db, shadow git checkpoints): close the app before moving.

## 6. Maintaining this report

When a harness changes its layout: re-read its path-resolution code or
docs, update the table here with a citation, update `goldenPaths` in
`internal/apps/registry_test.go`, update `apps/<id>.toml`, and run
`go test ./internal/apps/`. Paths verified against a specific version
should name that version in the pull request (see
[ADDING_AN_APP.md](ADDING_AN_APP.md)).

## Sources

[1] https://api.npmjs.org/downloads/point/last-week/@anthropic-ai/claude-code
[2] https://api.npmjs.org/downloads/point/last-week/@openai/codex
[3] https://api.npmjs.org/downloads/point/last-week/@google/gemini-cli
[4] https://api.npmjs.org/downloads/point/last-week/opencode-ai
[5] https://api.npmjs.org/downloads/point/last-week/@qwen-code/qwen-code
[6] https://api.npmjs.org/downloads/point/last-week/@github/copilot
[7] https://pypistats.org/api/packages/aider-chat/recent
[8] https://survey.stackoverflow.co/2026/ai/data/ai-code-agent
[9] https://blog.jetbrains.com/research/2026/04/which-ai-coding-tools-do-developers-actually-use-at-work
[10] https://newsletter.pragmaticengineer.com/p/ai-tooling-2026
[11] https://code.claude.com/docs/en/env-vars
[12] https://code.claude.com/docs/en/claude-directory
[13] https://github.com/openai/codex/blob/main/docs/config.md
[14] https://raw.githubusercontent.com/openai/codex/main/codex-rs/utils/home-dir/src/lib.rs
[15] https://raw.githubusercontent.com/google-gemini/gemini-cli/main/packages/core/src/config/storage.ts
[16] https://raw.githubusercontent.com/google-gemini/gemini-cli/main/packages/core/src/utils/paths.ts
[17] https://developers.googleblog.com/en/an-important-update-transitioning-gemini-cli-to-antigravity-cli
[18] https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference
[19] https://raw.githubusercontent.com/QwenLM/qwen-code/main/packages/core/src/config/storage.ts
[20] https://github.com/iflow-ai/iflow-cli/blob/main/docs_en/configuration/settings.md
[21] https://aider.chat/docs/config/aider_conf.html
[22] https://raw.githubusercontent.com/Aider-AI/aider/main/aider/analytics.py
[23] https://raw.githubusercontent.com/block/goose/main/crates/goose/src/config/paths.rs
[24] https://raw.githubusercontent.com/charmbracelet/crush/main/internal/config/load.go
[25] https://ampcode.com/security
[26] https://ampcode.com/install.sh
[27] https://docs.factory.com/droid-cli/settings
[28] https://hermes-agent.nousresearch.com/docs/reference/environment-variables
[29] https://hermes-agent.nousresearch.com/docs/user-guide/windows-native
[30] https://raw.githubusercontent.com/anomalyco/opencode/dev/packages/util/src/global-roots.ts
[31] https://dev.opencode.ai/v1/docs/troubleshooting
[32] https://forum.cursor.com/t/cursor-database-too-large-state-vscdb/168926
[33] https://forum.cursor.com/t/the-problem-of-the-c-drive-being-full/164958/5
[34] https://seanthegeek.net/doubleagent/research/cursor.html
[35] https://seanthegeek.net/doubleagent/research/windsurf.html
[36] https://zed.dev/docs/reference/cli
[37] https://seanthegeek.net/doubleagent/research/zed.html
[38] https://docs.cline.bot/getting-started/config
[39] https://github.com/cline/cline/blob/39ff2359f7e08231281539696e48a166ce49270c/sdk/packages/shared/src/storage/paths.ts
[40] https://github.com/RooCodeInc/Roo-Code/blob/b867ec9145750d0ae1ff7f02d35406e9bf2a0b16/src/utils/storage.ts
[41] https://seanthegeek.net/doubleagent/research/roo-code.html
[42] https://github.com/continuedev/continue/blob/5522c6f44ca0ac3528b37244818fbfa39b5af470/core/util/paths.ts
[43] https://github.com/continuedev/continue/issues/4309
[44] https://github.com/iflow-ai/iflow-cli/blob/main/README.md
[45] https://registry.npmjs.org/@iflow-ai/iflow-cli/latest
[46] https://github.com/continuedev/continue/discussions/6993
