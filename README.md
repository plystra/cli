# Plystra CLI

`github.com/plystra/cli` builds the user-installed `plystra` command. The CLI owns project creation, plugin and capability authoring, deterministic generation, static assembly, validation, development, testing, building, and release preparation.

The CLI is a separate Go Module from `github.com/plystra/kernel`. It completes build-time work and emits source targeting the Kernel's versioned assembly API; it is not a second runtime.

Generated assembly source imports `github.com/plystra/kernel/assembly`, declares `assembly.V1`, and validates that exact contract before runtime registration. This compile-time import also keeps the Kernel dependency visible to ordinary Go tooling.

New project trees are populated and validated in a same-parent staging directory before a final rename. Failed population and validation remove the staged tree, and an existing or concurrently created target is preserved.

In-place CLI mutations use sorted same-root staged replacements and backups. Paths that traverse symbolic links are rejected, existing files are checked again for concurrent edits, and validation failures or panics restore original bytes and modes before temporary state is removed. If a file changes again during validation, the user edit is preserved and the original backup location is reported for recovery.

Commands invoked below a module root resolve the real working directory and use the nearest enclosing regular `go.mod`; nested modules do not leak mutations into an outer module.

Plugin-target inference indexes strict bounded metadata envelopes from root-level `plugin.yaml` files, including deterministic exact provided-capability identities. Full manifest and schema validation remains owned by the Kernel parser. Targeting resolves, in order, an explicit directory or Plugin ID, the enclosing plugin, the only local plugin, or an interactive numeric selection. Multiple plugins fail with an actionable `--plugin` diagnostic whenever no terminal selector is available.

Capability-reference parsing accepts only canonical lower-case dotted names such as `account.register`, optionally followed by an exact positive major version such as `/v2`. Inputs are rejected instead of normalized, including leading-zero versions and identifiers outside the unsigned 64-bit range.

Capability-version planning creates `v1` when no matching capability is visible and otherwise chooses one above the highest visible major without filling gaps. Exact existing versions become implementation plans; explicit older or skipped versions require confirmation; and exhausting the unsigned 64-bit major range fails instead of wrapping.

Local capability-creation planning uses one immutable module plugin snapshot to combine target inference, visible declared versions, and the version decision. It retains every local provider of the source version in deterministic directory order so schema equality can be enforced before any later mutation chooses bytes to copy.

Capability-source identity parsing reads bounded single-document `capability.yaml` envelopes, rejects references, duplicate or unknown top-level keys, and non-canonical exact IDs, and leaves full request, response, error, and compatibility validation to the Kernel contract parser.

Local capability sources load only from the exact conventional path below a plugin. Symbolic or non-regular path components, oversized files, replacement during the read, and a declared ID that differs from the expected capability all fail before source bytes are returned.

Creation-plan source resolution loads every local provider of the selected source version in deterministic plugin order, normalizes every schema, and returns no partial set. Non-semantic source differences are accepted; meaningful conflicts fail with both plugin and source locations, deterministic schema paths and values, and corrective guidance.

Capability-source schema normalization produces the deterministic Kernel-compatible wire projection used for provider comparison. It ignores descriptions, formatting, mapping order, enum order, error order, and explicit false defaults while preserving identity, field, type, required, item, enum, and error semantics; full contract ownership remains with Kernel.

Validated schemas can be retargeted when a new capability version copies the highest visible version. Existing-version copies preserve exact bytes; new-version copies update only the top-level identity through the YAML syntax tree, normalize line endings deterministically, and preserve comments and human descriptions.

Capability schema-write rendering is non-mutating and produces one guarded, module-relative `capability.yaml` write. First versions receive a complete empty wire schema; later versions use the validated deterministic source snapshot, and existing target files or version directories are never eligible for replacement.

Plugin capability declarations are updated idempotently through the YAML syntax tree. New `provides` entries are canonical and sorted while plugin identity, requirements, opaque configuration, and comments are preserved; an already declared capability returns the exact original bytes.

Plugin indexing retains the exact validated `plugin.yaml` bytes as an immutable snapshot. Target inference carries that snapshot into compound authoring plans, so later render steps use one coherent scan rather than rereading mutable manifest state.

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
