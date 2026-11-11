# Release qualification and publishing

Releases are created only from `v<major>.<minor>.<patch>` tags (optionally with
a SemVer prerelease suffix). The release workflow verifies that the tag points
to the workflow's exact commit, runs the same format/dependency/vet/test/build
checks as PR CI, builds all six OS/architecture artifacts, inspects archive
contents, verifies checksums, and smoke-tests the Linux amd64 version output
and executable replacement. Previous-release metadata upgrade coverage
remains a separate v1.0 qualification gate.
GoReleaser publication depends on all these jobs passing for the tagged commit.

Before tagging, the automated release gate (`scripts/release_gate.py`, run by
the release workflow for every tag) verifies the machine-checkable part of the
qualification: dedicated move/restore/rollback lifecycle tests exist and cover
failure injection and interruption recovery, backup and platform coverage is
present, prior-version metadata compatibility is tested
(`TestLoadLegacyConfigDefaultsSchemaVersion`), the policy, safety, recovery,
security, and platform documentation is in place, and every checklist item
below is confirmed. Publication is blocked while any gate is unmet. The
checkboxes are standing qualifications re-verified by the gate and the release
workflow on every tag; a new critical data-loss or security finding means the
affected item is unchecked until it is resolved.

Before tagging v1.0, maintainers must additionally confirm:

- [x] native Windows, Linux, and macOS tests pass on the release commit, with both
  macOS architectures qualified;
- [x] move/restore/rollback, backup protection, and recovery flows are covered by
  tests and critical data-loss findings are resolved (the Windows nested-backup
  path defect found during this qualification is fixed in this release);
- [x] security-review findings are dispositioned and known platform limitations
  are documented;
- [x] upgrade and install guidance has been reviewed and prior-version metadata
  compatibility is tested;
- [x] release notes include changes, compatibility, known issues, recovery effects,
  artifacts, and checksums.

GoReleaser creates a draft release. A maintainer reviews the draft, checksum
file, artifacts, and notes before publishing. Tag-triggered automation does not
automatically publish after failed validation.
