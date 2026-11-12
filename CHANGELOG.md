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

## 1.1.0

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
- On Windows, query reparse points only for irregular entries, avoiding two
  filesystem calls for every regular file and directory during tree walks.
- Remove provably unused code (`copyPool.addFile`, `jsonMarshal`, `u16ptr`,
  and unused force option fields/parameters) and use `maps.Equal` for tree
  snapshots.
- Restore the documented `<N>d` backup-retention sweep in `midas status`, now
  performed under the vault lock; the status row in COMMANDS.md says so.
- Pool the 1 MiB copy and hash buffers and verify file hashes in parallel
  with the existing bounded copy-worker count, keeping the problem list
  deterministic. Add a hash-verification benchmark.
- Update BurntSushi/toml to v1.6.0, gopsutil/v4 to v4.26.10, and
  golang.org/x/sys to v0.50.0; tidy the module graph.
- CI runs `go test -race` on Ubuntu and macOS, adds pinned staticcheck and
  govulncheck steps, and enables weekly Dependabot updates for Go modules and
  GitHub Actions.
- Treat post-commit ownership-marker cleanup failures as warnings instead of
  rolling back committed, verified data; `doctor --repair` removes stale
  markers for known targets after revalidation.
- Delete `<vault>/.midas/logs/*.log` files older than 30 days on a best-effort
  basis once per run; documented in CONFIGURATION.md.
- fsync the parent directory after atomic journal, manifest, and BACKUP.json
  renames on Unix so metadata survives a crash immediately after the write.
  The step is a documented no-op on Windows.
- Document known limitations: hard links are duplicated by copies, cloud-synced
  placeholder folders are unsupported/risky, and legacy backup sets lack
  directory metadata.
- Let read-only commands (`status`, `list`, `plan`, `analyze`, and `doctor`
  without `--repair`) inspect a read-only vault. Mutating commands keep the
  writability check and exit 5.
- Fix `HealthCheck` to check the byte read through the link, descend when no
  top-level file exists, and reuse a single directory listing. An unreadable
  file through the link now reports it unhealthy instead of silently passing.
- Record directory paths, modes, and timestamps in BACKUP.json (additive
  `dirs` within schema 1, including empty directories) and restore them
  bottom-up. Older sets still restore and report that directory metadata was
  not recorded.
- Re-run the app-running check immediately before swapping the original
  aside and immediately before deleting `.midas-bak`; a detection aborts into
  the existing rollback path. `docs/SAFETY.md` documents the residual
  point-in-time race window.
- Serialize mutating commands with an advisory vault lock
  (`<vault>/.midas/LOCK`, flock on Unix and LockFileEx on Windows) and create
  new operation journals with O_EXCL, so concurrent Midas processes fail
  cleanly instead of racing on the same paths. Doctor reports a live lock
  holder.
- Preserve directory modification times on copies by applying them bottom-up
  after all children are written; the previous early timestamp call was
  always overwritten by child writes.
- Detect case collisions for directories and links as well as regular files,
  so trees that would silently merge on case-insensitive filesystems abort
  before commit. The copy error now states that the original is untouched and
  any partial copy is preserved.
- Verify each backup source file did not change during its copy (same file,
  size, modification time, and byte count). A torn backup set is discarded
  instead of being marked verified.
- Add bounded opt-in copy workers, reproducible copy/backup benchmarks, and
  separate verification progress.
- Add read-only `midas plan` / `midas analyze`, four shell completion scripts,
  and schema-versioned JSON and error output.
- Add configuration schema versioning, compatibility/security/support policies,
  and release artifact verification.
- Keep sequential copying as the default; benchmark results are hardware and
  filesystem specific.
