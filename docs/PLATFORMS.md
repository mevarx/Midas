# Platform validation

The CI workflow runs the native test, vet, and build suites on Windows, Ubuntu,
and macOS. Tests exercise the host link implementation (Windows junctions or
Unix directory symlinks), link removal without deleting the target, temp
environment redirection, child exit-code propagation, Unix signal forwarding,
migration, restore, rollback, and recovery ownership. Windows Ctrl+C delivery
uses the shared console and is interactive-terminal dependent; headless CI
cannot reproduce every console-host variation. Go builds are also cross-compiled for amd64
and arm64 on Windows, Linux, and macOS.

The release workflow repeats native validation and all six cross-builds before
running GoReleaser. Cross-compilation is not treated as native filesystem
validation.

CI does not claim manual coverage of every filesystem or hardware condition.
Long-path limits, locked files, external/removable volumes, privacy-protected
macOS directories, Windows case-insensitive collisions, and network filesystems
remain environment-dependent; network shares are unsupported. Midas does not
bypass access controls or silently skip unreadable files. See
[SAFETY.md](SAFETY.md) for the data-preservation guarantees.

Local checks:

```sh
gofmt -w ./cmd ./internal ./apps
go mod verify
go vet ./...
go test ./...
go build ./...
```
