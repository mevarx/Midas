# Command reference

Global options may precede or follow a command: `--vault`, `--config`,
`--dry-run`, `--yes`, `--json`, and `--verbose`. JSON output uses schema version
1; see [JSON.md](JSON.md).

| Command | Reads | Possible changes |
|---|---|---|
| `init` | Config and requested paths | Writes config and vault metadata |
| `list` | App definitions, manifest, target paths | None |
| `apps list/show/validate/path` | App definitions and host paths | None |
| `plan <app>` / `analyze <app>` | Definitions, source trees, available-space metadata | None; estimates only |
| `move <app>` | Source, config, backup and vault state | Backup, journal, verified copy/rename, link, manifest |
| `resume <app>` | Journal, source, destination, backup | Restarts only a validated owned move-copy checkpoint |
| `status` | Manifest, links, journals, available space | None |
| `doctor` | Manifest, journals, backups, links, partial/orphan state | None unless `--repair` is explicit |
| `restore <app>` | Manifest, vault copy, source state | Restore journal, verified original copy/link, manifest |
| `rollback <app>` | Journal, original, vault, backup | Recovery journal and verified restoration |
| `backup list/show/verify/delete` | Backup metadata and files | Delete only via guarded explicit deletion |
| `run <app> -- <command...>` | App configuration and vault | Child process; optional temp cleanup only when requested |
| `shim <app>` | App definition | Writes a launch shim, not application data |
| `completion <shell>` | Embedded static script | Writes script to stdout only |
| `version` | Build metadata | None |

`--copy-workers` is accepted by move/resume/restore/rollback, with a bounded
range of 1–16 and a default of 1. The worker option does not weaken hashing,
verification, backup, or recovery requirements.

Exit codes: 0 success; 1 general error; 2 usage; 3 running/unknown process state;
4 insufficient space; 5 vault unavailable; 6 verification failure; 7
unhealthy/recovery required; 8 backup failure; 9 protected backup. Prefer JSON
error `code`/`code_name` over matching error message text.
