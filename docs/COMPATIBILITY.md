# Compatibility policy

This policy defines the supported platforms, formats, and versioning rules.
It does not claim that a release has passed qualification before its CI and
release checks complete.

## App coverage

19 built-in app definitions ship. Every default path is
researched against vendor docs or path-resolution source code and pinned by
golden tests (`TestBuiltinGoldenPaths`) that expand each target for
Windows, macOS and Linux from any host. The evidence, popularity data and
per-app caveats live in [RESEARCH.md](RESEARCH.md).

| App | Windows | macOS | Linux | Evidence level |
|---|---|---|---|---|
| aider | ✓ | ✓ | ✓ | verified |
| amp | ✓ | ✓ | ✓ | partial (closed source) |
| claude-code | ✓ | ✓ | ✓ | verified |
| cline | ✓ | ✓ | ✓ | verified |
| codex | ✓ | ✓ | ✓ | verified |
| continue | ✓ | ✓ | ✓ | verified (archived upstream) |
| copilot-cli | ✓ | ✓ | ✓ | verified |
| crush | ✓ | ✓ | ✓ | verified |
| cursor | ✓ | ✓ | ✓ | verified |
| droid | ✓ | ✓ | ✓ | partial (closed source) |
| gemini-cli | ✓ | ✓ | ✓ | verified (sunset upstream) |
| goose | ✓ | ✓ | ✓ | verified |
| hermes | ✓ | ✓ | ✓ | verified |
| iflow | ✓ | ✓ | ✓ | partial (Windows inferred) |
| opencode | ✓ | ✓ | ✓ | verified |
| qwen-code | ✓ | ✓ | ✓ | verified |
| roo-code | ✓ | ✓ | ✓ | verified (idle upstream) |
| windsurf | ✓ | ✓ | ✓ | partial (mid-rename) |
| zed | ✓ | ✓ | ✓ | verified |

Coverage rules:

- Only vendor-documented relocation overrides are honoured in path
  templates; undocumented knobs are recorded in each definition's notes
  and can be handled with a local override definition.
- A target that does not exist on an OS is omitted for that OS (for
  example Goose's state dir on Windows, Cursor's sync stores on macOS).
- "partial" means some subdirectories rest on binary analysis or trusted
  third-party corpora rather than vendor publication; `midas list` and
  `midas plan` show what is actually present before anything moves.
- App layouts change between versions. Definitions name the verified
  layout as of the research snapshot; `midas doctor` detects drift after
  app updates.

## Platform and build targets

- Windows 10 22H2 and Windows 11, amd64; directory junctions are used.
- Ubuntu LTS, amd64 and arm64; directory symlinks are used.
- macOS 15, amd64 and arm64; directory symlinks are used. Native CI runs on
  both architectures before the release job may publish.
- Go 1.26.5 or newer is required to build from source: that patch release
  fixes GO-2026-4602 (`os.ReadDir` FileInfo escaping a Root), which
  govulncheck reports as reachable through link health checks. Release
  binaries target
  Windows, Linux, and macOS on amd64 and arm64.
- Network filesystems and non-native WSL paths are unsupported. Filesystem,
  privacy, permission, and volume behavior may limit individual operations.

## Persistent and machine-readable formats

Configuration, manifest, backup metadata, and journals each have explicit
schema versions (currently 1). Missing config schema is accepted as legacy v1;
unknown config keys, unsupported schema versions, invalid paths, and unknown
recovery metadata fields are rejected without rewriting the source file.
Journal schema 1 records operation ownership; old transient journals without an
operation ID are report-only and are not eligible for automatic resume.

JSON objects carry `schema_version`. Additive fields are compatible within a
version; incompatible field removal/renaming or changed meaning requires a
version increment and a documented migration path.

BACKUP.json schema 1 has an optional additive `dirs` list recording directory
paths, permission bits, and modification times, including empty directories.
Sets written before the field existed remain readable: they restore their files
normally, directory modes and timestamps are not restored, and the rollback
report states that they were not recorded. Because the field is additive, no
schema bump is required and older sets keep loading unchanged.

Before changing a persistent schema, preserve a recoverable copy, implement an
explicit migration, and test upgrade from the prior supported release. Do not
silently discard fields that may be relevant to recovery. Downgrades are not
supported after a format migration unless a release note explicitly says so.

## Versioning and upgrades

Midas follows Semantic Versioning: breaking CLI, config, manifest,
backup, journal, or JSON changes require a major version; compatible additions
are minor; compatible fixes are patch releases. Release notes identify upgrade
and recovery effects. While the major version is 0, format and CLI changes may
still occur; preserve the vault and backups before upgrading.
