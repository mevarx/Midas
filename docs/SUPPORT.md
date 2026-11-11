# Support and maintenance

## Supported use

Support targets and architectures are listed in
[COMPATIBILITY.md](COMPATIBILITY.md). The release is supported on platforms
whose native CI and release qualification jobs pass. Cross-compilation alone
does not qualify a platform. Windows uses junctions; macOS and Linux use
symlinks. Network shares, remote management, cloud backup, automatic elevation,
and non-native WSL paths are outside the support boundary.

## Reports and diagnostics

- For a bug, include Midas version, OS/architecture, filesystem type if known,
  exact command (redacting private paths if desired), exit code, and
  `midas status --json` / `midas doctor --json` output. Reports do not contain
  file contents, but paths can be sensitive; review before posting publicly.
- For suspected data loss, stop writes to all involved paths, preserve original,
  vault, backup, partial, and journal copies, and do not retry cleanup blindly.
  Follow [RECOVERY.md](RECOVERY.md).
- Report security or data-loss vulnerabilities privately as described in
  [SECURITY.md](../SECURITY.md); do not attach backup contents or credentials.

## Maintenance

The latest stable release receives fixes. The immediately preceding stable
minor release receives security and data-loss fixes for 90 days after a new
minor release; older releases are best-effort. Dependency updates are reviewed
at least quarterly and urgently for actionable security advisories. Deprecation
of a supported CLI or metadata behavior is announced in release notes with a
migration path. Export diagnostics by saving the `--json` output after
redacting paths; Midas has no telemetry or diagnostic upload.
