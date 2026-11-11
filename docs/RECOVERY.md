# Recovery and operation reports

`midas status` and `midas doctor` inspect current filesystem state. Doctor is
read-only by default and emits stable `code`, `severity`, affected paths, and a
recommended next action in both human and `--json` output. Use `midas doctor
--repair` only after reviewing the proposed action; `--dry-run` shows a repair
plan without applying it. `--yes` alone does not enable repairs.

## Interrupted operations

An operation journal records its ID, type, source, destination, checkpoint,
backup reference, and partial path. `midas status --json` lists those details;
`midas doctor --json` adds filesystem checks, link and manifest state, backup
verification, uncertainties, and safe next actions.

`midas resume <app>` currently supports only a move interrupted in `copying` or
`verifying`. Before restarting the copy it verifies that:

- the journal is valid and matches the current app definition;
- the original source is still a readable real directory and the destination
  has not appeared;
- an existing partial is owned by the same operation ID and expected path;
- the referenced backup still verifies, or the journal records the explicit
  no-backup confirmation.

The resume restarts the copy; it does not trust or skip files in a partial.
Other states, corrupt journals, missing sources, changed paths, and invalid
backups are preserved for manual recovery. Use `midas rollback <app>` only
after reviewing the recovery report and confirming the chosen source.

## Interruption and unknown data

Ctrl+C and supported termination signals cancel a copy at a file boundary.
Midas keeps the original, the operation-owned partial, any backup, and journal;
doctor can then offer the validated resume path. Progress output is plain text
in non-interactive environments and does not determine operation state.

An unknown `.midas-partial` directory is not evidence of Midas ownership.
Doctor reports it and never deletes it. Missing, corrupt, or mismatched
ownership metadata likewise blocks cleanup. Preserve all plausible copies if
the report contains an uncertainty or `critical` finding.

Reports never include file contents. Paths and operation identifiers are
included because they are needed to identify recovery locations.
