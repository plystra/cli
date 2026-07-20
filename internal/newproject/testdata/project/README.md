# my-app

This is the Plystra Project `example.com/acme/my-app`.

Local plugins belong in direct child directories containing `plugin.yaml`. Do not add a root `plugins/` container.

## Development

```powershell
plystra plugin create records
plystra capability create records.read --query --plugin records --expose
plystra generate
plystra generate --check
plystra check
go test ./...
go build ./...
go vet ./...
```

Mutating Plystra commands regenerate automatically. Add an ordinary Go Module dependency with `plystra add github.com/acme/platform@v1.0.0`, update it with `plystra update github.com/acme/platform@v1.1.0`, and remove it with `plystra remove github.com/acme/platform`. A Project created with `plystra new app --template github.com/acme/platform@v1.0.0` retains the selected template as the same kind of ordinary direct dependency: its root declarations, typed local operational values, and Secret-reference placeholders compose into this Project, but its source is not copied and it receives no resolution priority. Creation validates those values without reading referenced `env` or `file` Secrets; generated source and manifest provenance contain neither reference targets nor resolved values. Run `plystra generate` after manual declaration edits and use `plystra generate --check` as the read-only consistency gate.

A template's default Provider model must be unambiguous. If several compatible Plugins provide one required Capability, the template publisher must record one `capabilities.use` choice in the template's root `plystra.yaml` and publish a corrected version. Creation otherwise reports every candidate and leaves no target Project to repair.

The template's complete effective graph must contain only public Go Modules. Creation rejects every direct or transitive module matched by the effective `GOPRIVATE` setting, reports its selected `path@version`, and leaves no target Project. Publish or replace a genuinely private dependency before publishing the template, or correct an overbroad Go privacy setting before retrying.

Every dependency Plystra Project in the template graph must be portable without a relative Go Module `replace`. Creation reports each remaining directive with stable `module@version/go.mod` provenance and leaves no target Project. Publish the referenced module versions and remove the relative replacements before publishing a corrected template.

The staged generated application must be a fixed point. Creation installs generated output and then runs an immediate `plystra generate --check` equivalent. Dependency-composition drift or any changed, missing, unexpected, or obsolete generated path rejects the template and restores the transaction. The publisher must make generation deterministic, run `plystra generate` followed by `plystra generate --check` in a fresh Project directory, and publish a corrected module version.

Template creation next runs the same read-only workflow as `plystra check`: it rechecks the selected configuration and generated output, then runs `go test -mod=readonly ./...` from the staged Project root. Any failure restores the creation transaction and leaves no target Project. The publisher must make that public check pass in a fresh Project directory before publishing a corrected version.

Template creation then builds every staged Go package with `go build -mod=readonly ./...`. When `generated/sdk/javascript/package.json` exists, it also runs `npm install --ignore-scripts --no-audit --no-fund`, `npm run typecheck`, `npm run build`, and `npm pack --dry-run --json` through npm using that generated package's declared scripts and dependencies. Validation-only `node_modules/` and `dist/` output is removed before installation. It then builds the generated application entrypoint with `GOWORK=off` into isolated temporary output, starts the real assembled runtime, invokes intrinsic `kernel.health/v1`, and stops lifecycle providers cleanly. Child output is suppressed and temporary smoke output is removed on every path. Any failure restores the creation transaction and leaves no target Project. This private qualification executable does not create public distribution output.

Root `plystra.yaml` is the mandatory Project marker and shared default configuration. A sparse project-root `plystra.production.yaml` can be selected with `plystra generate --env production` and checked with the same selector; it is never created or loaded implicitly. To use one complete alternative current-Project document, run `plystra generate --config deploy/customer-a.yaml`. Root configuration is not merged beneath an explicitly selected file. `PLYSTRA_ENV` and `PLYSTRA_CONFIG` supply the corresponding selector for automation; select exactly one mode.

Start the generated application with the same selector used for generation: `go run ./generated/go/application --env production` selects one sparse overlay, while `go run ./generated/go/application --config deploy/customer-a.yaml` selects one complete replacement. Generated startup uses root `plystra.yaml` when no selector is present and accepts `PLYSTRA_ENV` or `PLYSTRA_CONFIG` when the corresponding flag is omitted. An explicit selector overrides both ambient variables, and the two modes cannot be combined. Replacement mode still requires a regular root Project marker but does not parse or merge its configuration. The selected replacement must be an existing nonsymbolic regular file inside the runtime Project directory. Unsafe or missing selections and invalid typed changes fail before Provider construction, and unselected files are ignored. Generated bootstrap records the matching selection provenance plus a bounded projection of transports, CORS, public exposure, requirements, explicit Provider choices, and Aliases tied to the complete compiled application-model digest. Startup rejects a different build-affecting projection with rebuild guidance before reading startup settings, resolving Secrets, or constructing Providers. Runtime-only address, timeout, Plugin configuration, and Secret-reference changes remain valid when their typed values are valid. Generated source never embeds YAML values, Secret-reference targets, resolved Secrets, or machine paths.

New Projects record `http.transports.connect: true` and `http.transports.rest: false` explicitly in root configuration. Keep those current-Project transport choices explicit when changing them. A nonempty public exposure requires at least one enabled transport, and JavaScript SDK generation requires Connect. If a selected default, environment, or full-replacement model has JavaScript Capability or Alias surfaces with Connect disabled, generation fails and identifies every affected surface; enable Connect in that selected current-Project configuration or remove those surfaces.

When several compatible Plugins provide one required Capability, select one with `plystra use <capability-name>/vN <plugin-id>`. Add `--env <environment>` to write only that sparse overlay or `--config <yaml-path>` to write only one complete replacement configuration; the command regenerates and validates with the same selection.

Generated source under `generated/` is owned by the Plystra CLI. Do not edit it manually; commit it to Git.

`generated/proto/wire-map.json` is committed compatibility history for canonical Capability request and response messages selected for Connect. Generation preserves field numbers across declaration reordering, allocates new fields without renumbering existing fields, and permanently reserves removed field names and numbers. Scalar contract enums receive a numeric zero `*_UNSPECIFIED` sentinel and stable positive member numbers; reordering and additions preserve existing assignments, while removed member names and numbers remain permanently reserved. Inactive field and enum history remains when exposure, Connect, or an enum is disabled. Capability Aliases reuse their canonical target messages and enums and have no separate ledger entry. Never edit or delete the ledger; restore its exact last committed content before regenerating. Generation emits deterministic `.proto` schemas for the selected canonical and Alias Connect surfaces plus a self-contained `generated/proto/descriptor-set.pb`; these CLI-owned files contain no Provider, Plugin, Go Module, configuration, or Secret data and must not be edited. A Project without a selected Connect surface retains a valid empty descriptor set. A selected Connect surface also emits a Go handler under `generated/go/adapters/connect/`. Canonical handlers bind one exact procedure to the generated canonical application-invocation handle, while Alias handlers forward through that canonical handler without owning a Provider or Alias dispatch entry. Both accept only Connect POST requests encoded as binary Protobuf or ProtoJSON, require `Connect-Protocol-Version: 1`, and reject gRPC and gRPC-Web before root-context or Provider invocation. Generation installs direct `connectrpc.com/connect` and `google.golang.org/protobuf` requirements at the supported versions inside the existing module transaction. The generated JavaScript package uses the same descriptor graph and declares pinned direct `@bufbuild/protobuf`, `@connectrpc/connect`, and `@connectrpc/connect-web` dependencies; application callers use only the Plystra wrapper rather than raw descriptors, messages, clients, or Connect errors. The generated application entrypoint does not yet mount an HTTP server; server mounting and the remaining protocol projections remain later transport work. Generation also rejects canonical fields in the same request or response when they derive the same ProtoJSON name or generated enum type. The diagnostic identifies both authored field names; rename one field in `capability.yaml` rather than editing generated output.

## Continuous integration

GitHub Actions runs `go test ./...` and `go vet ./...` on Linux, Windows, and macOS, plus the Go race suite on Linux. Keep `.github/workflows/ci.yml` aligned with the local validation commands.

## AI coding agents

Project-specific Plystra development guidance lives in `.agents/skills/plystra/SKILL.md`. Keep it synchronized with this module's commands, generated-code ownership, and architecture.
