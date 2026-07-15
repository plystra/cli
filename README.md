# Plystra CLI

`github.com/plystra/cli` builds the user-installed `plystra` command. The CLI owns project creation, plugin and capability authoring, deterministic generation, static assembly, validation, development, testing, building, and release preparation.

The CLI is a separate Go Module from `github.com/plystra/kernel`. It completes build-time work and emits source targeting the Kernel's versioned assembly API; it is not a second runtime.

Generated assembly source imports `github.com/plystra/kernel/assembly`, declares `assembly.V1`, and validates that exact contract before runtime registration. This compile-time import also keeps the Kernel dependency visible to ordinary Go tooling.

New project trees are populated and validated in a same-parent staging directory before a final rename. Failed population and validation remove the staged tree, and an existing or concurrently created target is preserved.

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
