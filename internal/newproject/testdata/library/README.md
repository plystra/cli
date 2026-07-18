# email

This is the non-runnable Plystra plugin Go Module `example.com/acme/email`.

Local plugins belong in direct child directories containing `plugin.yaml`. Do not add a root `plugins/` container.

## Development

```powershell
plystra plugin create records
plystra capability create records.read --plugin records
plystra generate
plystra generate --check
go test ./...
go vet ./...
```

Mutating Plystra commands regenerate automatically. Run `plystra generate` after manual declaration edits and use `plystra generate --check` as the read-only consistency gate.

Generated source under `generated/` is owned by the Plystra CLI. Do not edit it manually; commit it to Git.
