# Compatibility policy

This policy defines the supported platforms, formats, and versioning rules.
It does not claim that a release has passed qualification before its CI and
release checks complete.

## Platform and build targets

- Windows 10 22H2 and Windows 11, amd64; directory junctions are used.
- Ubuntu LTS, amd64 and arm64; directory symlinks are used.
- macOS 15, amd64 and arm64; directory symlinks are used. Native CI runs on
  both architectures before the release job may publish.
- Go 1.26 or newer is required to build from source. Release binaries target
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

After v1.0, Midas follows Semantic Versioning: breaking CLI, config, manifest,
backup, journal, or JSON changes require a major version; compatible additions
are minor; compatible fixes are patch releases. Release notes identify upgrade
and recovery effects. Until v1.0 is tagged, format and CLI changes may still
occur; preserve the vault and backups before upgrading.
