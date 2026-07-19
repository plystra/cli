# my-app

This is the Plystra Project `example.com/acme/my-app`.

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

Mutating Plystra commands regenerate automatically. Add an ordinary Go Module dependency with `plystra add github.com/acme/platform@v1.0.0`, update it with `plystra update github.com/acme/platform@v1.1.0`, and remove it with `plystra remove github.com/acme/platform`. Run `plystra generate` after manual declaration edits and use `plystra generate --check` as the read-only consistency gate.

Root `plystra.yaml` is the mandatory Project marker and shared default configuration. A sparse project-root `plystra.production.yaml` can be selected with `plystra generate --env production` and checked with the same selector; it is never created or loaded implicitly. To use one complete alternative current-Project document, run `plystra generate --config deploy/customer-a.yaml`. Root configuration is not merged beneath an explicitly selected file. `PLYSTRA_ENV` and `PLYSTRA_CONFIG` supply the corresponding selector for automation; select exactly one mode.

Generated source under `generated/` is owned by the Plystra CLI. Do not edit it manually; commit it to Git.

## Continuous integration

GitHub Actions runs `go test ./...` and `go vet ./...` on Linux, Windows, and macOS, plus the Go race suite on Linux. Keep `.github/workflows/ci.yml` aligned with the local validation commands.

## AI coding agents

Project-specific Plystra development guidance lives in `.agents/skills/plystra/SKILL.md`. Keep it synchronized with this module's commands, generated-code ownership, and architecture.
