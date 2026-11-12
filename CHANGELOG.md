# Changelog

Changes that affect behavior, compatibility, data safety, or recovery are
recorded here. Release notes are generated from the changelog and reviewed
before publication.

## 1.0.1

- Fix Windows backup creation rejecting sources with subdirectories: rel
  paths are stored slash-separated on every platform, so nested app data is
  backed up and verified instead of aborting the move.
- Fix rollback from a backup set failing on Linux and macOS: staging now
  copies with writable modes and applies the original per-file modes
  recorded in BACKUP.json, so protected read-only sets can restore data
  after total loss instead of aborting mid-copy.
- Add dedicated move/restore/rollback lifecycle tests with failure injection
  and interruption recovery; supersede the combined ownership-file coverage.
- Add native Windows tests for paths beyond the 260-character boundary:
  junction create/read/resolve/remove plus end-to-end move, restore, and
  rollback; docs/DECISIONS.md records the verified state.
- Add an automated release qualification gate (`scripts/release_gate.py`) to
  the release workflow; GoReleaser publication is blocked while any
  qualification item is unmet (docs/RELEASE.md).
- Repair the published v1.0.0 release: the six platform archives and
  checksums.txt are attached and their public download links verified.

## Unreleased

- Rename the Go module to `github.com/mevarx/Midas` so
  `go install github.com/mevarx/Midas/cmd/midas@latest` resolves; the previous
  path returned 404. Imports, linker flags, and install docs are updated.
- Fix Unix moves, restores, and rollbacks of trees containing read-only
  directories: staged copies are created writable, verified, and then given
  their original modes before the commit rename. Owned-path cleanup clears
  read-only modes bottom-up without following links, so `.midas-bak` and
  partial trees can be removed.
- Add a regression test proving `directChild` rejects nested paths on every
  platform (Windows `filepath.Rel` returns backslashes).
- Add bounded opt-in copy workers, reproducible copy/backup benchmarks, and
  separate verification progress.
- Add read-only `midas plan` / `midas analyze`, four shell completion scripts,
  and schema-versioned JSON and error output.
- Add configuration schema versioning, compatibility/security/support policies,
  and release artifact verification.
- Keep sequential copying as the default; benchmark results are hardware and
  filesystem specific.
