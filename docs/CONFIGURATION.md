# Configuration

`midas init` writes a TOML configuration with `schema_version = 1`. The file
contains no credentials. Legacy files without this key are read as version 1;
unsupported future versions and unknown fields are rejected rather than
silently discarded.

| Key | Values / meaning |
|---|---|
| `schema_version` | `1` |
| `vault` | Required vault directory; `MIDAS_VAULT` overrides this value |
| `verify` | `size` or `hash` |
| `confirm` | Prompt before destructive changes |
| `backup` | `full`, `essential`, or `none` (none always requires explicit confirmation) |
| `backup_dir` | Backup root; defaults to `<vault>/backup` |
| `backup_retention` | `keep`, `delete-on-success`, or a day count such as `30d` |
| `restore_point` | `auto`, `require`, or `skip` where supported |
| `allow_fallback` | Opt in to temp-directory fallback for `run` |
| `shim_dir` | Optional directory for generated launch shims |

Use `midas --config <path>` to select another config and `midas --vault <path>`
to override the vault for one command. Config writes are atomic and use
owner-only permissions where the platform supports them. App definitions are
separate declarative TOML files; see [ADDING_AN_APP.md](ADDING_AN_APP.md).

Midas deletes `<vault>/.midas/logs/*.log` files older than 30 days on a
best-effort basis once per run (on the first logged action); failures are
ignored.
