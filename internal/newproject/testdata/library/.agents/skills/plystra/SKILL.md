---
name: plystra
description: Develop, debug, test, and regenerate this Plystra Go Module. Use when changing plugins, Capability contracts or implementations, provider selection, plystra.yaml configuration, generated clients or assembly, HTTP or JavaScript surfaces, or project validation.
---

# Plystra Development

## Establish the module boundary

- Read `README.md`, the nearest `plugin.yaml`, relevant `capability.yaml` files, and root `plystra.yaml` when present.
- Treat direct child directories containing `plugin.yaml` as plugins. Do not add a root `plugins/` container or infer a provider from a Capability name.
- Use exact provider-independent Capability IDs such as `records.read/v1`. Do not silently change a released versioned contract.

## Preserve authored and generated ownership

- Edit plugin implementation and declarations outside `generated/`.
- Treat every file under `generated/` as Plystra CLI-owned. Never patch generated drift manually.
- Keep generated source committed when this project uses Git; ignore only build output under `dist/` and generated JavaScript build directories.
- Keep one runtime configuration object per selected Plugin ID. Put Secret references, never plaintext Secret values, in `plystra.yaml`.

## Implement through public workflows

1. Inspect `git status` when a Git repository is present and preserve unrelated changes.
2. Create plugins with `plystra plugin create <name>`.
3. Create or implement Capabilities with `plystra capability create <name>` or `plystra capability implement <name>/vN`.
4. Expose only intended canonical Capabilities with `plystra capability expose <name>/vN` or creation's `--expose` flag.
5. Run `plystra generate` after manual declaration edits. Let mutating Plystra commands regenerate automatically.
6. Implement provider methods in plugin-owned Go files; do not add handwritten provider registration.

When a plugin declares exact `requires`, use its generated immutable `dependencies.Dependencies` constructor parameter and generated Capability clients. A plugin without requirements keeps `New(Config) *Plugin`. Cross-plugin calls must use those clients so they follow the application invocation path.

## Validate and diagnose

- Run `plystra generate --check`, `go test ./...`, and `go vet ./...` before a feature boundary.
- If generated checks report changed, missing, unexpected, or obsolete paths, fix declarations or move unexpected authored files, then run `plystra generate`. Do not edit the reported generated files.
- Treat missing providers, ambiguous providers, incompatible contracts, constructor failures, and configuration errors as application assembly defects. Fix the declared source instead of bypassing resolution.
- When `.github/workflows/ci.yml` exists, keep it consistent with commands that pass locally.

If the project uses Git, commit each coherent validated feature with `type(scope): description` using the most specific scope, then push immediately.
