# Contributing

Thanks for helping Midas grow. The most useful contributions are **app
definitions** (one TOML file) and **bug reports from real Windows junctions,
macOS, and Linux**.

## Ground rules

- The PRD (`PRD-v1.2.md`) is the source of truth.
- Never hardcode app names or paths in Go. They belong in `apps/*.toml`.
- All OS-specific code stays in `internal/platform`, behind the `Platform`
  interface.
- Tests must use `t.TempDir()` only. Never touch a real home folder in a test.
- No telemetry, network calls, or auto-update logic.
- Backup deletion goes only through `internal/backup`'s `Delete`/`CheckDelete`.
  Never call `os.RemoveAll` on a backup path.

## Development

```
go build ./...
go vet ./...
go test ./...        # adds real temp-directory integration tests
gofmt -l ./cmd ./internal ./apps
```

Run the CLI locally without installing:

```
go run ./cmd/midas --help
go run ./cmd/midas --vault <temp-dir> --backup-dir <another-temp-dir> move <app> --dry-run
```

Keep dry-runs and tests away from your real home directory.

## Adding an app

See [docs/ADDING_AN_APP.md](docs/ADDING_AN_APP.md). Validate with:

```
go run ./cmd/midas apps validate apps/my-app.toml
```

## Commit style

Conventional commits are welcome but not required:

```
feat(mover): journal same-volume moves
fix(backup): keep BACKUP.json until last
docs: explain restore-point limits
```

Explain *why* in the body when the change affects data safety. Reference the
PRD section.

## Pull requests

- One logical change per PR.
- Describe how you exercised the change (platform, app versions, commands).
- If you touched the move/backup/rollback state machine, include failure-path
  reasoning: what happens on interruption at each step.
- CI must be green on Windows, Linux and macOS.

## Security

Do not open a public issue for a vulnerability that could lead to data loss or
exposure of backup contents. Contact the maintainers privately first.

## Code of conduct

Be decent. Harassment, discrimination and personal attacks are not welcome. The
maintainers may remove comments, commits or contributors that violate this.
