# Plystra CLI

`github.com/plystra/cli` builds the user-installed `plystra` command. The CLI owns project creation, plugin and capability authoring, deterministic generation, static assembly, validation, development, testing, building, and release preparation.

The CLI is a separate Go Module from `github.com/plystra/kernel`. It completes build-time work and emits source targeting the Kernel's versioned assembly API; it is not a second runtime.

Generated assembly source imports `github.com/plystra/kernel/assembly`, declares `assembly.V1`, and validates that exact contract before runtime registration. This compile-time import also keeps the Kernel dependency visible to ordinary Go tooling.

New project trees are populated and validated in a same-parent staging directory before a final rename. Failed population and validation remove the staged tree, and an existing or concurrently created target is preserved.

In-place CLI mutations use sorted same-root staged replacements and backups. Paths that traverse symbolic links are rejected, existing files are checked again for concurrent edits, and validation failures or panics restore original bytes and modes before temporary state is removed. If a file changes again during validation, the user edit is preserved and the original backup location is reported for recovery.

Commands invoked below a module root resolve the real working directory and use the nearest enclosing regular `go.mod`; nested modules do not leak mutations into an outer module.

Plugin-target inference indexes strict bounded identity envelopes from root-level `plugin.yaml` files and resolves, in order, an explicit directory or Plugin ID, the enclosing plugin, the only local plugin, or an interactive numeric selection. Multiple plugins fail with an actionable `--plugin` diagnostic whenever no terminal selector is available.

## Current commands

```text
plystra help
plystra version
plystra new <module-path> [--library] [--plugin <name>]
plystra plugin create <name>
```

`plystra new github.com/acme/my-app` creates a zero-local-plugin runnable module in `my-app/`. It pins the compatible Kernel, resolves checksums with standard Go tooling, emits the assembly API handshake and project policy files, and runs `go test ./...` before the staged directory is committed.

`plystra new github.com/acme/email --library` creates the same validated Go Module foundation without `plystra.yaml`. The resulting module is distributable and testable but non-runnable; development commands can provide a temporary host when runtime behavior is needed.

Add `--plugin account` to either form to create and validate the initial root-level plugin inside the same outer project transaction. `plystra new github.com/acme/email --library --plugin smtp` therefore leaves either the complete library-plus-plugin module or no target directory at all.

`plystra plugin create account` finds the nearest enclosing Go Module, derives a host- and major-version-independent Plugin ID such as `acme.my-app.account`, and transactionally creates the root-level plugin, manifest, constructor test, configuration type, assembly binding, and plugin documentation. It runs the module tests with read-only module metadata and restores every created path if validation fails.

## Development

```powershell
go test ./...
go test -race ./...
go vet ./...
go run ./cmd/plystra --help
```
