# M6 data-safety review record

**Review scope:** Midas repository at the M4–M6 qualification PR head.
**Review type:** Source-level safety review plus automated tests and static
checks; not an independent penetration test or a certification.

| Area | Finding / disposition |
|---|---|
| Path traversal | Target components, backup-relative paths, manifest destinations, and journal paths are validated. Unsafe metadata is rejected and preserved. |
| Links and cleanup | Copy/verification do not follow links. Cleanup requires operation/path ownership metadata; unknown partials are preserved. Link removal is tested not to remove the target. |
| Destructive actions | Move, restore, rollback and backup deletion have guarded flows; doctor is report-only unless `--repair` is explicit. Repairs recheck preconditions. |
| Permissions and temp files | Config, journals, and backup metadata use restrictive modes where supported; atomic writes use same-directory temporary files. Platform semantics still vary. |
| Metadata corruption/tampering | Schemas and paths are validated; unknown recovery fields and unsupported versions are rejected without rewriting source metadata. |
| Command execution | Only the explicit `run` command launches a user-selected process. App definitions are declarative; definitions do not supply executable commands to the migration engine. |
| Diagnostics | JSON reports include paths and operation IDs needed for recovery but do not include file contents. Users must review paths before public posting. |
| Dependencies | `go mod verify`, `go vet`, tests, and release dependency checks run in CI. These are not a full dependency vulnerability audit. |
| Artifact integrity | GoReleaser emits checksums; tag validation, checksum verification, archive inspection, and a Linux amd64 version smoke test gate publishing. |

No actionable critical issue was identified in this scoped source review.
Known residual risks include filesystem TOCTOU windows, hardware failure,
external volume loss, untested third-party filesystems, and native CI results
that have not yet completed for the release commit. Resolve those qualification
gates before publishing v1.0; do not treat this record as release sign-off by
itself.
