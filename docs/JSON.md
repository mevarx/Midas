# JSON and exit-code contract

Commands that support `--json` emit one JSON object to stdout. Human diagnostics
and progress, when any, go to stderr; JSON mode disables carriage-return
progress. Every object includes `schema_version: 1`. Fields may be added
compatibly within a schema version; a breaking shape or meaning change
increments the schema version and is documented here.

## Current command objects

- `version`: `version`, optionally `commit` with `--commit`.
- `list`: app and target records returned by the list command.
- `status`: `healthy`, `entries`, `in_progress_journals`, `operations`, and
  optional free-space information.
- `doctor`: `ok`, `findings`, `operations`, and `issues`. A finding has stable
  `code`, `severity`, path/target fields, `message`, and `recommended_action`.
- `plan` / `analyze`: `read_only`, app, targets, estimates, state transitions,
  warnings, and the `mutations` list (empty for this command).
- `move`, `restore`, `rollback`, `backup`, and `apps`: command-specific result
  objects. See their `--help` output and the matching command documentation.
- Errors: `error.code` is the process exit code; `error.code_name` is stable;
  `error.message` is human-readable. Messages can improve without changing the
  code. Do not parse prose for control flow.

The exit codes are: 0 success; 1 general error; 2 usage; 3 app running or
uncertain process state; 4 insufficient space; 5 vault unavailable; 6
verification failure; 7 unhealthy/recovery required; 8 backup failure; and 9
protected backup deletion. `doctor` and `status` may emit an unhealthy report
and return 7. Consumers should parse the report before acting.

Stable `error.code_name` values are `ok`, `error`, `usage_error`,
`app_running`, `insufficient_space`, `vault_unreachable`,
`verification_failed`, `unhealthy`, `backup_failed`, and `backup_protected`,
corresponding to the numeric exit codes above.
