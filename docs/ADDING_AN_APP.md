# Adding an app

Supporting a new tool is a data contribution: one TOML file, no Go.

## Where definitions live

- Built-ins: [`apps/`](../apps/) — embedded into the binary at build time.
- Yours: the directory printed by `midas apps path`
  (`%APPDATA%\midas\apps` on Windows,
  `~/.config/midas/apps` on Linux, `~/Library/Application Support/midas/apps`
  on macOS).

A local file with the same `id` replaces the built-in.

## Minimum viable definition

```toml
id = "myapp"
name = "My App"
description = "What it is"

[process]
windows = ["myapp.exe"]
linux = ["myapp"]
darwin = ["myapp"]

[launch]
command = "myapp"

[[target]]
name = "data"
windows = "~/.myapp"
linux = "~/.myapp"
darwin = "~/.myapp"
kind = "dir"
```

Validate it:

```
midas apps validate ./myapp.toml
midas apps show myapp
```

## Rules you must follow

1. `id` is lowercase letters, digits and hyphens; it must be unique.
2. `kind` must be `"dir"`. Midas relocates folders, not files. If your tool
   stores one large file (for example `~/.claude.json`), leave it alone and
   mention that in `[notes]`.
3. Targets must expand to an absolute path **inside the user's home directory**
   after `~`, `%VAR%`, `$VAR` and `{token}` expansion. Roots, the home
   directory itself, and system directories are rejected.
4. Do not use shell commands, hooks or anything executable. Definitions are
   data; `validate` enforces the schema strictly and rejects unknown keys.
5. Process detection: list executable names. If the app runs on a shared
   runtime (Node, Python, Electron), add `cmdline_contains` so Midas only
   treats a match as running when the command line also matches:

   ```toml
   [process]
   windows = ["node.exe"]
   linux = ["node"]
   darwin = ["node"]
   cmdline_contains = ["myapp"]
   ```

   If reliable detection is impossible, Midas warns and requires `--yes`.

## Path tokens

| Token | Windows | macOS | Linux |
|---|---|---|---|
| `~` | home | home | home |
| `{home}` | home | home | home |
| `{xdg_config}` | `%APPDATA%` | `~/Library/Application Support` | `$XDG_CONFIG_HOME` or `~/.config` |
| `{xdg_data}` | `%LOCALAPPDATA%` | `~/Library/Application Support` | `$XDG_DATA_HOME` or `~/.local/share` |
| `{xdg_cache}` | `%LOCALAPPDATA%\cache` | `~/Library/Caches` | `$XDG_CACHE_HOME` or `~/.cache` |
| `{xdg_state}` | `%LOCALAPPDATA%` | `~/Library/Application Support` | `$XDG_STATE_HOME` or `~/.local/state` |
| `{appdata}` | `%APPDATA%` | config dir | config dir |
| `{localappdata}` | `%LOCALAPPDATA%` | config dir | config dir |

Environment variables are expanded too: `$VAR`, `${VAR}`, `%VAR%`.

## Optional sections

### Environment for `midas run`

```toml
[env]
MYAPP_CONFIG = "{{vault_app}}/config"
MYAPP_TMP = "{{tmp}}"
```

`{{vault_app}}` is `<vault>/apps/<id>`; `{{tmp}}` is its `tmp` folder. These are
set only for the child process started by `midas run`.

### Essential backup selection

```toml
[backup]
include = ["config*.json", "*.md", "agents/**", "plugins/**"]
exclude = ["cache/**", "logs/**", "tmp/**", "**/node_modules/**", "*.log"]
max_file_mb = 50
```

Globs are relative to the target folder. `**` matches any number of path
segments. These rules apply only to `--backup=essential`; `full` copies
everything. Without a `[backup]` section, essential mode falls back to all files
up to 25 MB except `cache/`, `tmp/`, `logs/` and `node_modules/`.

### Notes

```toml
[notes]
text = "Do not move ~/.myapp.json; it is a file."
```

Shown by `midas apps show`.

## Submitting a built-in definition

1. Copy your file to `apps/<id>.toml`.
2. Add a row to the README's supported-apps table.
3. Run `go build ./...`, `go vet ./...`, and `midas apps validate apps/<id>.toml`.
4. Note the app version you verified against in the pull request. Paths and
   process names change between releases; reviewers will ask.
