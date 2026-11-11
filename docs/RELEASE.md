# Release qualification and publishing

Releases are created only from `v<major>.<minor>.<patch>` tags (optionally with
a SemVer prerelease suffix). The release workflow verifies that the tag points
to the workflow's exact commit, runs the same format/dependency/vet/test/build
checks as PR CI, builds all six OS/architecture artifacts, inspects archive
contents, verifies checksums, and smoke-tests the Linux amd64 version output
and executable replacement. Previous-release metadata upgrade coverage
remains a separate v1.0 qualification gate.
GoReleaser publication depends on all these jobs passing for the tagged commit.

Before tagging v1.0, maintainers must additionally confirm:

- native Windows, Linux, and macOS tests pass on the release commit, with both
  macOS architectures qualified;
- M1–M5 acceptance gates are complete and critical data-loss findings are
  resolved;
- security-review findings are dispositioned and known platform limitations
  are documented;
- upgrade and install guidance has been reviewed and prior-version metadata
  compatibility is tested;
- release notes include changes, compatibility, known issues, recovery effects,
  artifacts, and checksums.

GoReleaser creates a draft release. A maintainer reviews the draft, checksum
file, artifacts, and notes before publishing. Tag-triggered automation does not
automatically publish after failed validation.
