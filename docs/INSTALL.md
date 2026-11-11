# Install, upgrade, and uninstall

Midas does not install or update itself. Download only from the repository's
GitHub Releases page or build it with Go 1.26 or newer.

## Release binary

1. Choose the archive matching OS and architecture (`windows`, `linux`, or
   `darwin`; `amd64` or `arm64`).
2. Download the archive and `checksums.txt` from the same release.
3. Verify the archive checksum before extracting it. On Unix:

   ```sh
   grep -F 'midas_<version>_<os>_<arch>.' checksums.txt | sha256sum --check
   ```

   On PowerShell, compare `Get-FileHash -Algorithm SHA256 <archive>` with the
   matching checksum line. Do not run an artifact whose checksum does not
   match.
4. Extract the `midas` executable and place it on `PATH`. No installer script,
   elevation, or remote shell pipeline is required.

## Build from source

```sh
go install github.com/midas-cli/midas/cmd/midas@latest
```

Use a versioned module path instead of `@latest` when you need a reproducible
build. `go install` places the executable in the Go bin directory; make sure it
is on `PATH`.

## Upgrade

1. Close affected applications and finish or record any recovery operation.
2. Keep the existing vault, backup sets, manifest, configuration, and journals
   intact; take an additional copy before changing Midas versions.
3. Replace only the executable after verifying the new artifact. Do not delete
   the prior executable until the new one starts and `midas status` works.
4. Read the version's release notes and compatibility policy before upgrading.
   Never downgrade after a metadata schema change without the documented
   recovery procedure.

Config files without `schema_version` are read as schema 1. Saving writes the
version field. No data-directory migration is currently performed during
installation or upgrade.

## Uninstall

Remove only the executable and, if desired, shell-completion registration.
`midas uninstall` is not a command. Keep your app data, vault, backups,
configuration, shims, and links unless you have separately restored the data
and verified every copy. Uninstall never implies data deletion.
