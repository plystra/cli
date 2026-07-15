# Plystra CLI

`github.com/plystra/cli` builds the user-installed `plystra` command. The CLI owns project creation, plugin and capability authoring, deterministic generation, static assembly, validation, development, testing, building, and release preparation.

The CLI is a separate Go Module from `github.com/plystra/kernel`. It completes build-time work and emits source targeting the Kernel's versioned assembly API; it is not a second runtime.

## Current commands

```text
plystra help
plystra version
```

## Development

```powershell
go test ./...
go test -race ./...
go vet ./...
go run ./cmd/plystra --help
```
