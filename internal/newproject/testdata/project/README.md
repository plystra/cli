# my-app

This is the runnable Plystra Go Module `example.com/acme/my-app`.

Local plugins belong in direct child directories containing `plugin.yaml`. Do not add a root `plugins/` container.

## Development

```powershell
plystra plugin create records
plystra capability create records.read --plugin records --expose
plystra generate
plystra generate --check
go test ./...
go vet ./...
```

Mutating Plystra commands regenerate automatically. Run `plystra generate` after manual declaration edits and use `plystra generate --check` as the read-only consistency gate.

Generated source under `generated/` is owned by the Plystra CLI. Do not edit it manually; commit it to Git.

## Continuous integration

GitHub Actions runs `go test ./...` and `go vet ./...` on Linux, Windows, and macOS, plus the Go race suite on Linux. Keep `.github/workflows/ci.yml` aligned with the local validation commands.

## AI coding agents

Project-specific Plystra development guidance lives in `.agents/skills/plystra/SKILL.md`. Keep it synchronized with this module's commands, generated-code ownership, and architecture.
