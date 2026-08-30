package newproject

const goModuleTemplate = `module %s

go 1.26

require (
	github.com/plystra/kernel %s
	go.yaml.in/yaml/v3 %s
)
`

const plystraTemplate = `http:
  address: ":8080"
  transports:
    connect: true
    rest: false
  expose: []

timeouts:
  startup: 2m

interfaces:
  require: []
  use: {}
  policies: {}

config: {}
`

const readmeTemplate = `# %s

This is the Plystra Project ` + "`%s`" + `.

Local plugins belong in direct child directories containing ` + "`plugin.yaml`" + `. Do not add a root ` + "`plugins/`" + ` container.

## Development

` + "```powershell" + `
plystra plugin create records
plystra capability create records.read --query --plugin records --expose
plystra interface create records.list
plystra implement records.list/v1 --package ./recordlist
plystra generate
plystra generate --check
plystra inspect
plystra explain capability records.read/v1
plystra explain plugin <plugin-id>
plystra explain config config.<plugin-id>.<field>
plystra explain alias <alias-name>/vN
plystra explain exposure <capability-or-alias-name>/vN
plystra check
go test ./...
go build ./...
go vet ./...
` + "```" + `

Mutating Plystra commands regenerate automatically. Add an ordinary Go Module dependency with ` + "`plystra add github.com/acme/platform@v1.0.0`" + `, update it with ` + "`plystra update github.com/acme/platform@v1.1.0`" + `, and remove it with ` + "`plystra remove github.com/acme/platform`" + `. A Project created with ` + "`plystra new app --template github.com/acme/platform@v1.0.0`" + ` retains the selected template as the same kind of ordinary direct dependency: its root declarations, typed local operational values, and Secret-reference placeholders compose into this Project, but its source is not copied and it receives no resolution priority. Creation validates those values without reading referenced ` + "`env`" + ` or ` + "`file`" + ` Secrets; generated source and manifest provenance contain neither reference targets nor resolved values. Run ` + "`plystra generate`" + ` after manual declaration edits and use ` + "`plystra generate --check`" + ` as the read-only consistency gate. Use ` + "`plystra inspect`" + ` for a concise read-only summary of the same selected model, ` + "`--verbose`" + ` for complete resolution evidence, or ` + "`--format json`" + ` for deterministic automation output. Use ` + "`plystra explain capability <capability-name>/vN`" + ` with the same selector to see a Capability's selected Provider, direct reason and source, and the exact command or configuration field that changes that decision. Use ` + "`plystra explain plugin <plugin-id>`" + ` to see whether a Plugin is selected from the current Project, selected as an exact Capability Provider, or visible but unselected, together with the direct source and selector-matched change. Use ` + "`plystra explain config config.<plugin-id>.<field>`" + ` to see the typed configuration owner, winning source, explicit removal or ancestor suppression, and exact selected document field to edit without exposing configuration values or Secret-reference targets. Use ` + "`plystra explain alias <alias-name>/vN`" + ` to see its direct canonical target, inherited or narrowed exposure, every compatible application or generation-extension source, and the selector-matched field or activation decision that changes the result. Use ` + "`plystra explain exposure <capability-or-alias-name>/vN`" + ` to see why that identity is public or internal and the selected exposure, Alias, or activation decision that changes the surface.

A template's default Provider model must be unambiguous. If several compatible Plugins provide one required Capability, the template publisher must record one ` + "`capabilities.use`" + ` choice in the template's root ` + "`plystra.yaml`" + ` and publish a corrected version. Creation otherwise reports every candidate and leaves no target Project to repair.

The template's complete effective graph must contain only public Go Modules. Creation rejects every direct or transitive module matched by the effective ` + "`GOPRIVATE`" + ` setting, reports its selected ` + "`path@version`" + `, and leaves no target Project. Publish or replace a genuinely private dependency before publishing the template, or correct an overbroad Go privacy setting before retrying.

Every dependency Plystra Project in the template graph must be portable without a relative Go Module ` + "`replace`" + `. Creation reports each remaining directive with stable ` + "`module@version/go.mod`" + ` provenance and leaves no target Project. Publish the referenced module versions and remove the relative replacements before publishing a corrected template.

The staged generated application must be a fixed point. Creation installs generated output and then runs an immediate ` + "`plystra generate --check`" + ` equivalent. Dependency-composition drift or any stale, missing, unexpected, or manually modified generated path rejects the template and restores the transaction. The publisher must make generation deterministic, run ` + "`plystra generate`" + ` followed by ` + "`plystra generate --check`" + ` in a fresh Project directory, and publish a corrected module version.

Template creation next runs the same read-only workflow as ` + "`plystra check`" + `: it rechecks the selected configuration and generated output, then runs ` + "`go test -mod=readonly ./...`" + ` from the staged Project root. Any failure restores the creation transaction and leaves no target Project. The publisher must make that public check pass in a fresh Project directory before publishing a corrected version.

Template creation then builds every staged Go package with ` + "`go build -mod=readonly ./...`" + `. When ` + "`generated/sdk/javascript/package.json`" + ` exists, it also runs ` + "`npm install --ignore-scripts --no-audit --no-fund`" + `, ` + "`npm run typecheck`" + `, ` + "`npm run build`" + `, and ` + "`npm pack --dry-run --json`" + ` through npm using that generated package's declared scripts and dependencies. Validation-only ` + "`node_modules/`" + ` and ` + "`dist/`" + ` output is removed before installation. It then builds the generated application entrypoint with ` + "`GOWORK=off`" + ` into isolated temporary output, starts the real assembled runtime, invokes intrinsic ` + "`kernel.health/v1`" + `, and stops lifecycle providers cleanly. Child output is suppressed and temporary smoke output is removed on every path. Any failure restores the creation transaction and leaves no target Project. This private qualification executable does not create public distribution output.

Root ` + "`plystra.yaml`" + ` is the mandatory Project marker and shared default configuration. A sparse project-root ` + "`plystra.production.yaml`" + ` can be selected with ` + "`plystra generate --env production`" + ` and checked with the same selector; it is never created or loaded implicitly. To use one complete alternative current-Project document, run ` + "`plystra generate --config deploy/customer-a.yaml`" + `. Root configuration is not merged beneath an explicitly selected file. ` + "`PLYSTRA_ENV`" + ` and ` + "`PLYSTRA_CONFIG`" + ` supply the corresponding selector for automation; select exactly one mode.

Start the generated application with the same selector used for generation: ` + "`go run ./generated/go/application --env production`" + ` selects one sparse overlay, while ` + "`go run ./generated/go/application --config deploy/customer-a.yaml`" + ` selects one complete replacement. Generated startup uses root ` + "`plystra.yaml`" + ` when no selector is present and accepts ` + "`PLYSTRA_ENV`" + ` or ` + "`PLYSTRA_CONFIG`" + ` when the corresponding flag is omitted. An explicit selector overrides both ambient variables, and the two modes cannot be combined. Replacement mode still requires a regular root Project marker but does not parse or merge its configuration. The selected replacement must be an existing nonsymbolic regular file inside the runtime Project directory. Unsafe or missing selections and invalid typed changes fail before Implementation construction, and unselected files are ignored. Generated bootstrap records the matching selection provenance plus a bounded projection of transports, CORS, public exposure, requirements, explicit Implementation choices, and Interface timeout policies tied to the complete compiled application-model digest. Startup rejects a different build-affecting projection with rebuild guidance before reading startup settings, resolving Secrets, or constructing Implementations. Runtime-only address, ` + "`timeouts.startup`" + `, ordinary configuration, and Secret-reference changes remain valid when their typed values are valid. Generated source never embeds YAML values, Secret-reference targets, resolved Secrets, or machine paths.

New Projects record ` + "`http.transports.connect: true`" + ` and ` + "`http.transports.rest: false`" + ` explicitly in root configuration. Keep those current-Project transport choices explicit when changing them. A nonempty public exposure requires at least one enabled transport, and JavaScript SDK generation requires Connect. If a selected default, environment, or full-replacement model has JavaScript Capability or Alias surfaces with Connect disabled, generation fails and identifies every affected surface; enable Connect in that selected current-Project configuration or remove those surfaces.

Cross-origin browser access is opt-in. Add ` + "`http.cors.allowed_origins`" + ` to the selected current-Project configuration, and set ` + "`allow_credentials: true`" + ` only for exact origins. Generated Connect handlers require one canonical normalized HTTP/HTTPS request origin of at most 4096 bytes; literal ` + "`null`" + ` is accepted only by a noncredentialed wildcard policy. Preflight accepts only ` + "`POST`" + ` and the fixed ` + "`Authorization`" + `, ` + "`Connect-Protocol-Version`" + `, ` + "`Connect-Timeout-Ms`" + `, and ` + "`Content-Type`" + ` headers, with each name present at most once across no more than four field values totaling at most 4096 bytes. Malformed, noncanonical, duplicate, over-bound, or disallowed input fails before trusted-root creation or Implementation invocation. Without ` + "`http.cors`" + `, generated handlers emit no CORS response headers.

When several compatible Implementations satisfy one required Interface, select one with ` + "`plystra use <interface-id> <constructor-symbol>`" + `. Add ` + "`--env <environment>`" + ` to write only that sparse overlay or ` + "`--config <yaml-path>`" + ` to write only one complete replacement configuration; the command regenerates and validates with the same selection.

Common actionable Plystra CLI failures end with exactly one ` + "`Recovery:`" + ` block followed by one stable ` + "`Diagnostic: PLYSTRA_<AREA>_<CONDITION>`" + ` code. Follow that one command or file edit with the same default, ` + "`--env`" + `, or ` + "`--config`" + ` selection, and use the code rather than human wording as the automation or support identity. Recovery output preserves safe explicit and ambient selectors, but uses ` + "`<environment>`" + ` or ` + "`<yaml-path>`" + ` instead of echoing an unsafe or absolute selector. An unclassified internal error remains unchanged and does not receive guessed advice or a code.

Authored Interface failures distinguish invalid directives, canonical Go contracts, optional interface.yaml metadata, duplicate visible IDs, and packages ordinary Go tooling cannot load. Apply the emitted recovery to the reported module-relative source in its owning Project; never edit a dependency's Module Cache copy.

Authored Implementation failures distinguish invalid directives, Config schemas, required or optional Interface parameters, constructor results, and structural conformance. Apply the emitted recovery to the reported Project-relative Go source. Fix dependency-owned source in its owning Project or select a corrected module version; never edit the Module Cache copy.

` + "`plystra use`" + ` rejects malformed canonical Interface IDs with ` + "`PLYSTRA_USE_INTERFACE_INVALID`" + ` and malformed fully qualified constructor symbols with ` + "`PLYSTRA_USE_CONSTRUCTOR_INVALID`" + ` before Project mutation. Run the emitted corrected command with the same default, environment, or complete-replacement selection.

Interface and Implementation scaffold failures distinguish invalid or missing identities, unsafe package paths, and existing targets before mutation. Follow the emitted corrected command or replacement choice.

Generated source under ` + "`generated/`" + ` is owned by the Plystra CLI. Do not edit it manually; commit it to Git.

` + "`generated/compatibility/interfaces.json`" + ` is the committed, CLI-owned shape baseline for every visible authored Interface, whether or not it is selected or exposed. It records package and method identity, request and response names, reachable messages, stable field numbers, Go and JSON names, requiredness, and canonical Go types while excluding metadata, projections, Implementations, configuration, Secrets, source paths, and module versions. During prerelease development, ` + "`plystra generate`" + ` refreshes it transactionally and ` + "`plystra generate --check`" + ` reports drift without mutation. Never edit it manually.

` + "`generated/compatibility/interface-metadata.json`" + ` is the matching committed, CLI-owned compatibility-class baseline. It stores only exact-contract, documentation, and example digests, never metadata values. The contract class covers Go shape, semantics, semantic-error codes, constraints, and Behavioral Conformance declarations; documentation covers descriptions and deprecation; examples cover validated requests and outcomes. ` + "`plystra generate`" + ` updates it in the same transaction, and ` + "`plystra generate --check`" + ` compares the classes without mutation. Never edit it manually.

` + "`generated/compatibility/interface-transport.json`" + ` is the committed, CLI-owned baseline for every Interface on the selected Connect surface. It stores separate Protobuf-descriptor, Connect-procedure, and active wire-map digests, including the shared safe-error descriptor but no raw contract, Implementation, configuration, Secret, path, or module-version values. ` + "`plystra generate`" + ` updates it transactionally, and ` + "`plystra generate --check`" + ` compares those classes without mutation. Never edit it manually.

` + "`generated/compatibility/interface-javascript.json`" + ` is the committed, CLI-owned caller-visible JavaScript API baseline for every exposed Interface. It stores separate shared package-root, client/factory surface, public TypeScript shape, and semantic-error-union digests without Implementation, configuration, Secret, source-location, or module-version values. ` + "`plystra generate`" + ` updates it transactionally, and ` + "`plystra generate --check`" + ` compares those classes without mutation. Never edit it manually.

` + "`generated/compatibility/interface-documentation.json`" + ` is the committed, CLI-owned baseline for the current ` + "`generated/docs/api.md`" + ` and ` + "`generated/docs/openapi.json`" + ` artifacts. It stores only each closed artifact kind, stable managed path, and exact content digest, including a valid empty record when no documentation surface is selected. It contains no documentation bytes, Implementation, configuration, Secret, source-location, or module-version values. ` + "`plystra generate`" + ` updates it transactionally, and ` + "`plystra generate --check`" + ` reports documentation drift without mutation. Never edit it manually.

The required top-level ` + "`interface_provenance`" + ` record in ` + "`generated/manifest.json`" + ` uses schema ` + "`plystra.interface-provenance/v1`" + `. It joins every visible authored Interface to reachable bindings, explicit or unique-compatible constructor selections, the complete dependency-first constructor graph, configuration ownership, root and exposure sources, effective Interface-policy inputs, and exact generated proxy, adapter, assembly, Protobuf, wire-map, Connect, HTTP-route, and JavaScript mappings and digests. Required intrinsic ` + "`kernel.*`" + ` Interfaces are recorded separately without an ordinary constructor or Implementation adapter. The exact same canonical record is embedded in ` + "`generated/.plystra-manifest.json`" + `. It contains no configuration values, Secret-reference targets, resolved Secrets, process environment, or machine-specific absolute paths. Never edit either manifest; change authored inputs, regenerate, and require a clean ` + "`plystra generate --check`" + `.

Ownership-manifest schema 3 records one immutable entry for every managed artifact: Project-relative path and SHA-256, exact generator/version, closed output kind, normalized non-secret input-record IDs, stable source references, and ` + "`cli-owned`" + ` cleanup authority. The manifest's own provenance is implicit because it cannot contain its own stable digest. An entry remains useful when its target is missing or manually modified. Never edit this evidence; use it to find the authored input, then regenerate.

The required top-level ` + "`transport_toolchain`" + ` record in ` + "`generated/manifest.json`" + ` identifies the exact embedded ` + "`go/format`" + ` runtime; built-in Protobuf-model, descriptor, wire-map, Connect, JavaScript, and API-documentation generator versions; pinned generated Go and npm dependency versions; and its canonical digest. ` + "`plystra generate --check`" + ` reports drift when that identity changes. Plystra generation does not invoke an implicit global ` + "`protoc`" + ` or generator executable and does not use a hosted generation service.

For each Interface exposed through Connect, the generated JavaScript SDK publishes one nested client method, one tree-shakable factory, its declared semantic-error-code union, and deterministic request, response, and reachable nested-message types from the authored Go contract. For ` + "`records.echo/v1`" + `, call ` + "`client.records.echo.v1(request)`" + ` or ` + "`createRecordsEchoV1(options)(request)`" + `. Both forms use the same exact Connect procedure and safe runtime boundary. Effective JSON names and required markers remain exact. ` + "`int32`" + ` and ` + "`uint32`" + ` become JavaScript ` + "`number`" + `; ` + "`int64`" + ` and ` + "`uint64`" + ` become ` + "`bigint`" + `; floating-point fields become ` + "`number`" + `; bytes become ` + "`Uint8Array`" + `; timestamps and durations use their canonical transport strings; repeated values are readonly arrays; and maps are readonly string-keyed records. The unsafe JavaScript object key ` + "`__proto__`" + ` is rejected before dispatch rather than silently changed or dropped.

Import the generated JavaScript SDK only through its package root. Internal runtime, descriptor, codec, operation-binder, and raw Connect surfaces are blocked by the package export map and omitted from public declarations.

Generated client construction requires one explicit ` + "`credentialPolicy`" + `. Select ` + "`{mode: \"anonymous\"}`" + ` for no browser credentials, ` + "`{mode: \"cookie\", fetchCredentials: \"same-origin\"}`" + ` or ` + "`include`" + ` for an exact cookie policy, or ` + "`{mode: \"bearer\", getAccessToken}`" + ` for one bounded raw bearer token. Anonymous and bearer modes use Fetch credentials ` + "`omit`" + `; cookie mode sends no bearer header. Bearer callback rejection and nullish, empty, malformed, prefixed, control-containing, non-string, or oversized results fail before dispatch as ` + "`PlystraError`" + ` code ` + "`credential_error`" + ` without exposing credential data. A selected mode never falls back to another.

Pass an AbortSignal as the generated operation's second argument to cancel before dispatch, while bearer acquisition is pending, or while fetch is in flight. Cancellation rejects with PlystraError code cancelled. Once server invocation has begun, the generated Connect handler preserves that caller cancellation through the trusted Kernel root, canonical application invocation, and Implementation context. The same handler preserves the earlier caller or trusted-root Go context deadline and reports deadline_exceeded without a response when invocation observes it. Cancellation and deadlines are best-effort interruption and do not promise Implementation rollback or compensation.

` + "`generated/proto/wire-map.json`" + ` is committed compatibility history for the request, response, and reachable same-package messages of every visible authored Interface, whether or not the Interface is exposed or Connect is enabled. Exposed Connect Interfaces additionally retain their exact stable service, method, and procedure identity. Authored positive ` + "`plystra`" + ` field numbers are the wire numbers. Generation rejects renumbering and reuse across all visible Interface history, permanently reserves every removed Protobuf field name and number, and carries active reservations into generated source and the descriptor set. Never-exposed Interfaces, removed exposure, and disabled Connect remain inactive and create no schema, descriptor, handler, or SDK output. The ledger temporarily retains separately labelled legacy transport history required by pre-Gate-14 handlers; that bridge is not Interface contract authority. Never edit or delete the ledger. If it drifts, recover the exact previously generated content before running ` + "`plystra generate`" + `.

Generation emits one deterministic ` + "`.proto`" + ` schema with the canonical messages and exactly one unary service from every exposed Interface package, plus a self-contained ` + "`generated/proto/descriptor-set.pb`" + `. The Connect procedure path is derived from the exact Interface ID. An overlapping pre-Gate-14 legacy schema is import-only and owns no competing service. A Project without a selected Connect surface retains a valid empty descriptor set. These files contain no Implementation, configuration, or Secret data, are CLI-owned, and are checked by ` + "`plystra generate --check`" + `.

Every selected Connect application failure carries one shared ` + "`PlystraErrorDetail`" + `. Its ` + "`requested_interface_id`" + ` records the requested canonical Interface or temporary pre-removal Alias, ` + "`canonical_interface_id`" + ` records the canonical Interface target, and exactly one declared semantic code or closed Kernel class is present. Alias handlers preserve the requested Alias while invoking only the canonical target. Implementation text, causes, payloads, panic data, configuration, credentials, Secrets, and internal Kernel detail codes never enter this descriptor. The generated JavaScript wrapper exposes only the validated immutable Plystra detail; a missing, duplicate, malformed, unknown, mismatched, or undeclared detail fails closed to ` + "`internal`" + `.

Binary Protobuf requests are limited to 1 MiB, decoded with a maximum message depth of 64, and validated with a 65,536-node budget. Malformed or truncated wire data, unknown fields at any message depth, and requests that exceed any bound fail before root-context creation or Provider invocation; direct handler calls apply the same recursive validation. Binary Protobuf responses use the same size, depth, and node bounds. Generated conversion preflights canonical content before proportional wire-projection allocation, validates the exact response message, and serializes deterministically. Invalid or oversized responses produce only the safe internal response failure and no partial response on canonical, Alias, or direct handler paths. ProtoJSON requests independently accept at most 1 MiB, 64 nested JSON containers, and 65,536 structural tokens before strict decoding and the same canonical validation. Unknown or duplicate fields, malformed or trailing documents, invalid UTF-8, invalid required nulls, enum sentinels, non-finite numbers, and breached bounds fail before root-context creation or Provider invocation. Optional non-nullable null becomes absence, explicit zero values remain present, and full-range integers remain exact. ProtoJSON responses use the same exact generated message and canonical response validation plus an independent 1 MiB serialized limit, with no partial response. Canonical and Alias binary and ProtoJSON paths agree.
`

const githubCIReadmeTemplate = `
## Continuous integration

GitHub Actions runs ` + "`go test ./...`" + ` and ` + "`go vet ./...`" + ` on Linux, Windows, and macOS, plus the Go race suite on Linux. Keep ` + "`.github/workflows/ci.yml`" + ` aligned with the local validation commands.
`

const skillsReadmeTemplate = `
## AI coding agents

Project-specific Plystra development guidance lives in ` + "`.agents/skills/plystra/SKILL.md`" + `. Keep it synchronized with this module's commands, generated-code ownership, and architecture.
`

const gitignoreTemplate = `/dist/
/generated/sdk/javascript/node_modules/
/generated/sdk/javascript/dist/
.env
.env.local
go.work
go.work.sum
`

const gitattributesTemplate = `* text=auto eol=lf
/generated/** linguist-generated=true
`

const ciTemplate = `name: CI

on:
  push:
  pull_request:

permissions:
  contents: read

jobs:
  test:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, windows-latest, macos-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v6
        with:
          go-version: "1.26.x"
          cache: true
      - run: go test ./...
      - run: go vet ./...

  race:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v6
        with:
          go-version: "1.26.x"
          cache: true
      - run: go test -race ./...
`

const skillTemplate = `---
name: plystra
description: Develop Plystra Projects through ordinary Go Modules, versioned Interfaces, Implementations, and plystra.yaml. Use when creating, configuring, consuming, testing, or debugging an Interface or Implementation.
---

# Plystra Project Development

## Choose the smallest workflow

Use the workflow matching the task. Plystra adds two public concepts: a Plystra
Project is a Go Module with root plystra.yaml; an Interface is a versioned,
single-operation Go interface. Implementations are ordinary Go. Read details
only when the task or a diagnostic requires them.

### Operate a Project created from a template

Create from a published template Go Module query in the desired parent
directory:

    plystra new app --module github.com/acme/app --template github.com/acme/platform@v1.2.3

On success the command reports:

    Created app from github.com/acme/platform@v1.2.3
    Configuration scaffolded
    Generated, checked, built, and locally verified

    Next:
      cd app
      plystra check

Follow that next action. Read root plystra.yaml only when the Project needs a
documented local operational value or the check gives a configuration recovery
action. Do not inspect or alter implementation internals merely because the
Project came from a template. Creation is atomic, so a failure leaves no target
Project to repair; address the reported cause and retry the command.

Use only a template version that its publisher explicitly identifies as
qualified. The current CLI does not advertise any template as qualified. A
successful creation proves the lifecycle shown above, but it does not by itself
grant that label.

### Change ordinary business behavior

Start with ordinary Go:

- Go Module remains the dependency and import boundary.
- Define the one-operation Interface in an authored Go package.
- Implement it with a ` + "`//plystra:implements`" + ` constructor.
- Keep shared application choices in root plystra.yaml.

For a new local behavior, use this sequence:

    plystra interface create records.read
    plystra implement records.read/v1 --package ./records
    # Edit the authored Interface and Implementation.
    plystra generate
    go test ./...
    plystra check

When one Implementation needs another Interface, accept the canonical Interface
type as a constructor parameter and call its ordinary Go method. Never import
the other concrete Implementation package. If Plystra reports several
compatible Implementations for one required Interface, select the exact
constructor requested by the application:

    plystra use email.send/v1 example.com/acme/email/smtp.New

The detailed reference below contains complete file shapes and variants. Open
only the section needed for the current command or authored file.

### Select one environment

Keep shared choices in root plystra.yaml. Put only environment-specific
differences in one sparse project-root overlay such as
plystra.production.yaml, then use the same selector for generation and checks:

    plystra generate --env production
    plystra generate --check --env production
    plystra check --env production
    go run ./generated/go/application --env production

No selector means root plystra.yaml only. Use --config only when the task
explicitly requires one complete replacement document; it is an advanced
deployment path, not a second ordinary configuration layer. Generate, check,
and start that document with one consistent selector:

    plystra generate --config deploy/customer-a.yaml
    plystra generate --check --config deploy/customer-a.yaml
    go run ./generated/go/application --config deploy/customer-a.yaml

Generated startup accepts the same --env selector or PLYSTRA_ENV and the same
--config selector or PLYSTRA_CONFIG. An explicit selector overrides both
ambient variables, and the two modes cannot be combined. An overlay or
replacement must exist and pass typed validation before application
construction; unselected documents are not read. Replacement mode keeps root
plystra.yaml as the mandatory Project marker but does not merge it beneath the
selected file. Generated bootstrap records the matching selection mode,
selected environment when applicable, stable relative paths, normalized
document and dependency-composition digests, and final application-model digest
as non-secret provenance. It also embeds a bounded compatibility projection for
the build-affecting declarations tied to that full model digest. Startup derives
the same projection from the selected runtime document and rejects a mismatch
with rebuild guidance before startup settings, Secret resolution, or application
construction. Runtime-only address, timeouts.startup, ordinary configuration, and Secret-
reference changes stay outside that comparison. Neither compiled record contains
YAML values, Secret-reference targets, resolved Secrets, or machine paths.

## Detailed task reference

Read only the section that matches the current task. Start with authored Go and
plystra.yaml; inspect generated transport or assembly internals only when a
specific diagnostic or infrastructure task requires them.

## Start from the module boundary

The current Go Module path is %[1]s. Read its go.mod before writing imports.

Inspect these authored inputs first:

- go.mod and go.sum define module dependencies and versions.
- Root plystra.yaml is mandatory and identifies this Go Module as a Plystra Project.
- Each direct child directory containing plugin.yaml is one local Plugin.
- Each provided Capability declaration lives below its implementing Plugin.
- README.md and Plugin README files may add module-specific instructions.

Do not add a root plugins directory. Do not infer a Plugin from a Capability
name. Every Plystra Project is independently runnable and may contain zero
local Plugins, primarily distribute reusable Plugins, or obtain selected
Providers from dependency Projects.

## Module and file ownership

A typical Plystra Project evolves into this layout:

    go.mod
    go.sum
    plystra.yaml
    records/
      plugin.yaml
      plugin.go
      plugin_test.go
      capability_records.read_v1.go
      capabilities/
        records.read/
          v1/
            capability.yaml
      migrations/                 # optional Plugin-owned database assets
    generated/
      .plystra-manifest.json        # per-file generator/input/source provenance
      manifest.json
      proto/
        descriptor-set.pb
        plystra/generated/.../capability.proto
        wire-map.json
      docs/
      go/
        adapters/
        application/
        assembly/
        bootstrap/
        clients/
        configuration/
        contracts/
        dependencies/
        invocation/
        providers/
      sdk/javascript/
        package.json
        src/descriptors.ts
        src/interfaces/<interface-id>.ts
        src/runtime.ts

CLI owns generated/compatibility/{interfaces,interface-metadata,interface-transport,interface-javascript,interface-documentation}.json;
never edit them. interface-documentation.json records doc kind, path, digest,
or an empty state. Refresh with plystra generate; check with
plystra generate --check.

generated/proto/wire-map.json is durable CLI-owned compatibility history for
every visible authored Interface message, exposed or not and even with Connect
disabled. Authored positive plystra numbers are wire numbers. Generation
rejects renumbering or reuse and permanently reserves removed Protobuf names
and numbers. Only exposed Connect Interfaces become active and emit schemas,
descriptors, handlers, or SDK output. Never edit or delete the ledger; recover
its exact previous generated content before running plystra generate.

Generation emits one deterministic .proto schema with canonical messages and
exactly one unary service from every exposed Interface package. The Connect
procedure path is derived from the exact Interface ID. The temporary legacy
schema imports that file and owns no competing messages, service, or procedure.
generated/proto/descriptor-set.pb is the self-contained deterministic binary
descriptor graph, including required well-known descriptors. With no selected
Connect surface it remains present as a valid empty descriptor set. These files
contain no Implementation, configuration, or Secret data. They are CLI-owned;
never edit them, and use plystra generate --check to detect drift.

Connect exposure generates one nested JavaScript method, one tree-shakable
factory, the declared semantic-error-code union, and Interface request,
response, and nested-message types under generated/sdk/javascript. Import only
the package root. For records.echo/v1, the representative calls are
client.records.echo.v1(request) and createRecordsEchoV1(options)(request);
substitute the actual generated symbols exported by
generated/sdk/javascript/src/index.ts. Both forms use the same exact Connect
procedure and safe runtime boundary. Properties preserve JSON names and
required markers. int32/uint32 use number, int64/uint64 use bigint, floats use
number, bytes use Uint8Array, timestamp and duration use transport strings,
repeated values use readonly arrays, and maps use readonly string-keyed
records. The unsafe JavaScript object key __proto__ is rejected before dispatch
rather than silently changed or dropped. Do not edit these CLI-owned sources.
A selected Connect surface also emits a Go handler under
generated/go/adapters/connect/. Canonical handlers bind one exact procedure to
the generated canonical application-invocation handle, while Alias handlers
forward through that canonical handler without owning a Provider or Alias
dispatch entry. The current Connect boundary accepts canonical contracts with
explicit semantics.kind: query or command and projects each as one unary
procedure; an Alias reuses that canonical target. Selecting an event or stream
for Connect fails before output and identifies the Capability, typed kind,
supported unary kinds, and http.expose remediation. Do not relabel an event or
stream to bypass this check. Both accept only Connect
POST requests encoded as binary
Protobuf or ProtoJSON, require Connect-Protocol-Version: 1, and reject gRPC and
gRPC-Web before root-context or Provider invocation. Binary Protobuf requests
are limited to 1 MiB, decoded with a maximum message depth of 64, and validated
with a 65,536-node budget. Malformed or truncated wire data, unknown fields at
any message depth, and requests that exceed any bound fail before root-context
creation or Provider invocation; direct handler calls apply the same recursive
validation. Binary Protobuf responses use the same size, depth, and node
bounds. Generated conversion preflights canonical content before proportional
wire-projection allocation, validates the exact response message, and
serializes deterministically. Invalid or oversized responses produce only the
safe internal response failure and no partial response on canonical, Alias, or
direct handler paths. ProtoJSON requests independently accept at most 1 MiB,
64 nested JSON containers, and 65,536 structural tokens before strict decoding
and the same canonical validation. Unknown or duplicate fields, malformed or
trailing documents, invalid UTF-8, invalid required nulls, enum sentinels, non-finite numbers,
and breached bounds fail before root-context creation or Provider invocation.
Optional non-nullable null becomes absence, explicit zero values remain
present, and full-range integers remain exact. ProtoJSON responses use the
same exact generated message and canonical response validation plus an
independent 1 MiB serialized limit, with no partial response. Canonical and
Alias binary and ProtoJSON paths agree.
Generation installs direct
connectrpc.com/connect and google.golang.org/protobuf requirements at the
supported versions inside the existing module transaction. The generated
JavaScript wrapper loads that same descriptor graph and declares pinned direct
@bufbuild/protobuf, @connectrpc/connect, and @connectrpc/connect-web runtime
dependencies. Callers never construct raw descriptors, Protobuf messages, or
Connect clients and never receive ConnectError as the public error model. The
shared plystra.generated.transport.v1.PlystraErrorDetail carries
requested_interface_id for the requested canonical Interface or temporary
pre-removal Alias, canonical_interface_id for the canonical Interface target,
and exactly one declared semantic code or closed Kernel class. Alias handlers
preserve the requested Alias while entering only the canonical target. In JavaScript, catch PlystraError and
inspect its immutable detail; do not parse messages or Connect internals. A
missing, duplicate, malformed, unknown, identity-mismatched, outer-code-
mismatched, or undeclared detail fails closed to internal. Implementation text,
causes, payloads, panic data, configuration, credentials, Secrets, and internal
Kernel detail codes never enter the safe detail.

## Compose dependency Project configuration

Start a new Project from one ordinary Plystra Project dependency:

    plystra new app --module github.com/acme/app --template github.com/acme/platform@v1.2.3

The template value is a standard Go Module query. The selected module must have
regular root plystra.yaml, remains a direct go.mod requirement, and has no
special status after creation. The CLI composes only its root declarations and
regenerates the staged application. It does not copy dependency files, mutate
Module Cache source, create go.work, inherit dependency environment overlays,
or give template origin Provider or configuration priority. A failure leaves no
target Project.

Template-declared operational values and Secret-reference placeholders are
composed into the new root plystra.yaml through the same typed field rules. For
example, a template may declare:

    config:
      acme.platform.mailer:
        host: smtp.localhost
        password:
          env: PLATFORM_SMTP_PASSWORD

Creation validates this object against acme.platform.mailer's plugin.yaml but
does not read PLATFORM_SMTP_PASSWORD. Generated source and manifest provenance
contain neither that reference target nor its resolved value. The CLI does not
invent values for required fields omitted by the template; an incomplete
declaration fails the creation transaction.

Template creation requires an unambiguous default Provider model. If several
visible Plugins provide one required Capability, the template publisher must
record one explicit choice in the template root plystra.yaml before publishing
that version:

    capabilities:
      use:
        email.send/v1: acme.platform.mailer

Creation otherwise names every candidate and leaves no target Project for the
consumer to repair.

Template dependencies must not match the effective GOPRIVATE setting. The CLI
checks the complete direct and transitive graph, reports every selected private
path@version, and leaves no target Project. Publish or replace a genuinely
private dependency before publishing the template. If a reported module is
already public, correct the overbroad Go privacy setting before retrying.

Template dependency Projects must not declare relative replace directives in
their go.mod files. Publish the referenced module version first, replace the
local path with that ordinary requirement, and publish a corrected Project
version. Creation checks the template and every transitive dependency Project,
reports stable module@version/go.mod provenance, and leaves no target when a
relative replacement remains.

The staged generated application must be a fixed point. Creation installs the
generated output and then runs an immediate plystra generate --check equivalent.
Dependency-composition drift or any stale, missing, unexpected, or manually modified
generated path rejects the template and restores the transaction. The
publisher must make generation deterministic, run plystra generate followed by
plystra generate --check in a fresh Project directory, and publish a corrected
module version.

Template creation next runs the same read-only workflow as plystra check. It
rechecks the selected configuration and generated output, then runs Go package
tests with -mod=readonly from the staged Project root. Any failure restores the
creation transaction and leaves no target Project. The publisher must make that
public check pass in a fresh Project directory before publishing a corrected
version.

Template creation then builds every staged Go package with -mod=readonly. It
next builds generated/go/application with GOWORK=off into isolated temporary
output, starts the real assembled runtime, invokes intrinsic kernel.health/v1,
and stops lifecycle providers cleanly. Child output is suppressed and temporary
smoke output is removed after success, failure, timeout, or cancellation. Any
failure restores the creation transaction and leaves no target Project. This
private qualification executable does not create public distribution output.

Add one ordinary Go Module dependency through the public transaction:

    plystra add github.com/acme/email@v1.4.2

Remove a selected dependency by exact module path without a version query:

    plystra remove github.com/acme/email

Update exactly one selected dependency through a standard module query:

    plystra update github.com/acme/email@v1.5.0

All three commands may start at the Project root or inside a Plugin. Add
resolves the query through ordinary Go tooling and
retains the selected module as a direct go.mod requirement. Remove requires the
module to be selected in go.mod and
verifies that regeneration plus tidy did not select it again. Update requires
an existing selection and preserves an existing direct requirement. It targets
only that module query; Go may adjust transitive versions required by the selected
graph. An omitted
version query uses Go's normal upgrade selection rather than requesting an
upgrade of every selected module. Each command
recomposes root plystra.yaml, regenerates, tidies, and validates the complete
Project. The current dependency surfaces use the default root configuration and
never rewrite unselected environment overlays or alternative YAML files. A
failed Go command, resolution, composition, generation, tidy, removal
postcondition, or validation step restores every transaction-owned module,
root-configuration, and generated file.

The CLI asks Go for the effective module graph. Every direct or transitive
module with regular root plystra.yaml is a dependency Plystra Project; its
root-level Plugins and root configuration become visible. A module without the
root marker is an ordinary dependency and is not scanned. Dependency files such
as plystra.production.yaml and plystra.test.yaml are never inherited.

Composition uses field-specific rules:

- interfaces.require is a set; interfaces.use and interfaces.policies replace
  or remove exact keys.
- http.expose and capabilities.require form deterministic canonical-ID unions.
  Use their sparse add/remove mapping for exact inherited set edits.
- Identical additions, removals, Provider selections, and Alias declarations
  deduplicate.
- Plugin configuration merges only by fields declared in plugin.yaml.
  Declared objects merge recursively; scalar and array fields replace as one
  value. Null removes one inherited field or a complete Plugin config entry.
- Dependency http.address, http.transports, http.cors, and timeouts.startup
  never replace this Project's process settings.
- Incompatible Provider, Alias, or Plugin-field values fail with every
  contributing module@version/plystra.yaml source.

Resolve an inherited Implementation conflict with one exact current-Project choice:

    interfaces:
      use:
        email.send/v1: example.com/acme/email/smtp.New

Prefer the targeted public workflow for ordinary Implementation selection:

    plystra use email.send/v1 example.com/acme/email/smtp.New
    plystra use email.send/v1 example.com/acme/email/production.New --env production
    plystra use email.send/v1 example.com/acme/email/customer.New --config deploy/customer-a.yaml

The default form writes root plystra.yaml; --env writes only that sparse
overlay; --config writes only that complete document. Ambient selectors choose
the same targets unless a flag overrides them. The command preserves unrelated
content, regenerates with the same selection, rolls back every owned file after
failure, and rejects an invalid or incompatible target.

The current entry replaces the inherited choice; Interface and conformance
validation still runs. Never reorder dependencies or invent priority.
Regenerate and check after selection or dependency changes.
Both generated manifests share non-secret composition plus strict
plystra.interface-provenance/v1 for Interfaces, bindings, constructors,
intrinsics, and generated mappings. Inspect; never edit.

Configure one exact non-intrinsic Interface policy:

    interfaces:
      policies:
        email.send/v1:
          timeout: 5s

Only positive timeout is accepted. Values normalize and replace the exact key;
null removes it. Changes cause drift. Enforcement is deferred.

Plystra generate applies a typed three-way update from the selection's previous
dependency baseline, authored file, and new dependency baseline. Default mode
selects root plystra.yaml. It preserves comments, explicit values, and exact
tombstones; adds new inherited declarations; and removes disappeared inherited
values. Hand deletion is ambiguous; use the field's sparse removal or null.
Configuration and generated output share one rollback boundary. The generated
manifest retains non-secret current_project_paths so an identical local choice
stays locally owned; edit YAML, never ownership data. Plystra generate --check
reports dependency-composition drift without writing either surface.

Remove only exact inherited declarations with sparse edits and null
tombstones:

    http:
      expose:
        remove: [diagnostics.internal/v1]
    capabilities:
      require:
        remove: [audit.legacy/v1]
      use:
        email.send/v1: null
      aliases:
        mail.send/v1: null
    interfaces:
      policies:
        email.send/v1: null
    config:
      acme.email.smtp:
        legacy_host: null

The same Capability cannot appear in both add and remove. A null entry removes
only that keyed Provider, Alias, Plugin object, or declared Plugin field.
Nested object keys merge recursively, while arrays replace as a complete value.
Removing a required field still fails final validation unless it has a valid
default. An inherited add/remove conflict must be resolved by the current
Project at that exact key; dependency ordering never resolves it.

## Select an environment or one complete current-Project configuration

Root plystra.yaml is the mandatory Project marker, shared current-Project base,
and default configuration. Add only environment-specific differences to one
optional sparse project-root overlay, for example plystra.production.yaml:

    http:
      transports:
        rest: true
      cors:
        allowed_origins:
          - https://app.example.com
    capabilities:
      use:
        email.send/v1: acme.email.smtp
    config:
      acme.email.smtp:
        endpoint: https://smtp.production.example
        token:
          env: SMTP_PRODUCTION_TOKEN

Generate and check that exact environment consistently:

    plystra generate --env production
    plystra generate --check --env production
    go run ./generated/go/application --env production

The selected overlay must exist. The CLI does not create common environment
files or load unselected overlays. The effective order is dependency Project
composition, root plystra.yaml, then the selected overlay. Omitted fields
inherit. Scalars and arrays replace at their declared typed field, keyed
objects merge by declared field path, set fields use their sparse add/remove
form, and null keeps its exact tombstone meaning. Unknown fields and type
mismatches remain errors. Dependency Project environment overlays are never
inherited.

Generated startup defaults to root plystra.yaml. It accepts --env or
PLYSTRA_ENV for one sparse overlay and --config or PLYSTRA_CONFIG for one
complete replacement. An explicit selector overrides both ambient variables,
and the two modes cannot be combined. Environment mode loads root plus the one
selected overlay. Replacement mode still requires a regular root Project
marker but does not parse or merge root configuration; its selected path must
be an existing nonsymbolic regular file within the runtime Project directory.
Both modes apply typed validation and reject unsafe or missing selections
before Provider construction. Generate, check, and start with the same
selector. Generated startup compares the selected document's normalized
transport, CORS, public-exposure, requirement, explicit Implementation-choice,
and Interface-policy projection with the projection compiled for the full application-model
digest. A mismatch fails before startup settings, Secret resolution, or
Provider construction and instructs the operator to rebuild with the same
selector. Runtime-only address, timeouts.startup, Plugin configuration, and Secret-
reference differences remain valid when they pass typed validation.

http.transports is a closed current-Project object. It accepts only boolean
connect and rest fields. New Project scaffolds write both fields explicitly as
connect: true and rest: false. When omitted from another selected document,
the same schema defaults apply. In an environment overlay, those fields replace
independently: omission inherits the root value and null restores that field's
schema default. A complete --config document does not inherit root transport
choices; omitted fields use the same defaults. Dependency Project transport
settings are ignored.

The selected transport values participate in the generated application-model
digest. A nonempty http.expose set requires at least one enabled transport.
The official generated JavaScript SDK requires connect: true whenever the
selected model contains JavaScript Capability or Alias surfaces. Generation
fails before output with the selected configuration path and every affected
surface when Connect is disabled. Enable Connect in that current-Project
selection or remove those surfaces. Connect handlers are generated for
selected surfaces; server mounting and the optional REST projection remain
later transport work, so rest: true does not yet create a REST adapter.

http.cors is an optional closed current-Project object. When present it
requires one nonempty allowed_origins list and accepts only optional boolean
allow_credentials, which defaults to false. The CLI normalizes, sorts, and
deduplicates HTTP/HTTPS origins; * cannot be combined with credentials. An
environment overlay replaces the complete origin list when present and may omit
it to inherit root origins, while credentials compose independently. The
effective result must still contain origins. Set http.cors to null to disable
root CORS for that environment. A complete --config document does not inherit
root CORS, and dependency Project CORS settings are ignored.

The normalized selected CORS policy participates in the generated
application-model digest. Origin or credential changes create generation drift,
while reordered or duplicate equivalent origins retain one static model
identity. Generated Connect handlers enforce the policy before protocol
dispatch. Request Origin is limited to 4096 bytes and must equal its canonical
normalized HTTP/HTTPS serialization. Literal null is accepted only through a
noncredentialed wildcard; request Origin: * is invalid. Preflight requests POST.
Across at most four Access-Control-Request-Headers values totaling at most 4096
bytes, each of Authorization, Connect-Protocol-Version, Connect-Timeout-Ms, and
Content-Type may appear once, case-insensitively. A valid preflight returns 204.
Malformed, noncanonical, duplicate, over-bound, or disallowed input returns 403
before Implementation invocation. Allowed responses carry deterministic
origin, credential, and Vary headers. Without http.cors, generated handlers
emit no CORS response headers and reject cross-origin preflight.

PLYSTRA_ENV supplies the same environment name for automation when --env is
omitted. To generate from a complete alternative document instead, use the same
full-replacement selection for the write and read-only check:

    plystra generate --config deploy/customer-a.yaml
    plystra generate --check --config deploy/customer-a.yaml

The effective order is dependency Project composition followed by the selected
complete document. Root plystra.yaml remains the Project marker but is not
merged beneath deploy/customer-a.yaml. Put every current-Project process
setting, requirement, exposure, Provider replacement, Alias, and Plugin value
needed by that application model in the selected document.

Relative paths are resolved from the detected Project root even when the
command starts inside a Plugin. An absolute path must still resolve inside that
root. PLYSTRA_CONFIG supplies the same path for automation when --config is
omitted. Do not combine --env and --config or set PLYSTRA_ENV and
PLYSTRA_CONFIG together. Either explicit CLI selector overrides both ambient
variables. Environment generation maintains dependency-derived changes in root
plystra.yaml and preserves the sparse overlay. Full-replacement generation
maintains only the selected file, and independent maintained selections retain
independent dependency baselines.

Inspect generated/manifest.json for the versioned canonical constraint
projection: every resolved canonical Capability has exact contract and
constraint digests plus its ordered constrained request and response fields;
an unconstrained Capability has an empty field list. The configuration schema v5
records default, environment, or explicit-config mode; the environment and
overlay reference when applicable; Project-relative paths; normalized document
digests; dependency baseline history with non-secret current_project_paths; the
Protobuf wire-map digest; and final application-model digest. Environment mode
retains the root dependency baseline because overlays do not own maintenance.
The manifest never
records raw configuration, Secret reference targets, resolved Secrets, or
machine-specific absolute paths. Its top-level transport_toolchain record identifies
embedded go/format; Protobuf-model, descriptor, wire-map, Connect, JavaScript,
and API-documentation generator versions; pinned generated Go/npm dependencies;
and its digest. Generation invokes neither global protoc nor a hosted generator.
A changed build-affecting selection or toolchain identity creates drift.

## Naming and identity rules

- Plugin directory names use lower-case ASCII kebab-case, such as records or
  postgres-store.
- Plugin IDs are exact stable dotted identities written in plugin.yaml. Read the
  ID produced by the CLI instead of deriving it in application code.
- Canonical Capability IDs use at least two lower-case dotted name segments and
  one positive version: records.read/v1 or authn.login.password/v2.
- The version suffix is exactly /vN. Do not use v0, a leading zero, or an
  unversioned ID in plugin.yaml, plystra.yaml, generated imports, or runtime
  calls.
- Capability IDs are provider-independent. Several Plugins may provide the
  same exact ID only when their request, response, closed field constraints,
  errors, typed semantics, and normalized extension metadata are exactly
  compatible.
- An application-local Capability Alias has the same ID grammar but is never a
  provider, canonical contract, reusable requirement, or Kernel registration.

## Create a Project and a Plugin

From the desired parent directory, create ` + "`./app/`" + ` with the project name as
its initial Go Module path:

    plystra new app

Choose an independent standard Go Module path without changing the directory:

    plystra new app --module github.com/acme/app

Start from an existing Plystra Project distributed as a Go Module dependency:

    plystra new app --module github.com/acme/app --template github.com/acme/platform@v1.2.3

Inside an existing Project, create a root-level Plugin:

    plystra plugin create records

Expected effects:

- records/plugin.yaml receives the generated exact Plugin ID.
- records/plugin.go receives Config, Plugin, and New(Config) declarations.
- records/plugin_test.go and records/README.md are created.
- generated/go/configuration/records_gen.go is created.
- Complete application assembly is regenerated for the Project.
- The command formats, tidies, tests, and rolls back its own changes on failure.

Do not expose a Plugin. Applications expose exact Capabilities. All local
root-level Plugins in a Plystra Project participate in application resolution;
dependency-module Plugins are selected only when exact requirements need them.

## Create and implement a new Capability

Create and expose a first Query Capability version in one transaction:

    plystra capability create records.read --query --plugin records --expose

The command creates
records/capabilities/records.read/v1/capability.yaml, adds
records.read/v1 to records/plugin.yaml provides, creates a Plugin-owned method
scaffold, adds the canonical ID to plystra.yaml http.expose, and regenerates all
derived surfaces. The initial method deliberately returns
implementation.unavailable; replace that scaffold before treating the
Capability as implemented.

Edit the authored capability.yaml into a complete provider-independent contract:

    id: records.read/v1
    description: Returns one record.

    request:
      record_id:
        type: string
        required: true
        constraints:
          min_length: 1
          max_length: 128
          pattern: '^[a-z0-9][a-z0-9_-]{0,127}$'

    response:
      record_id:
        type: string
        required: true
      title:
        type: string
        required: true
      archived:
        type: boolean
        required: true

    errors:
      - invalid_record_id
      - not_found

    semantics:
      kind: query
      effects: none
      idempotency:
        mode: inherent
      retry:
        safety: safe
      cancellation:
        mode: best-effort
      completion:
        mode: completed-before-return
      ordering:
        mode: none
      data:
        request: public
        response: public

The --query profile writes those complete semantics into the authoritative
capability.yaml. A Capability name never implies behavior, and a genuinely new
identity requires one supported intent profile before the command mutates the
Project.

Constraints use one closed type-specific vocabulary: string fields accept
min_length, max_length, and bounded Go regular-expression pattern; integer and
number fields accept minimum and maximum; array fields accept min_items and
max_items. Contract loading rejects unknown or type-incompatible keys, invalid
bounds or expressions, and inexact normalized numbers. Constraints participate
in exact equality and the contract digest, so every Provider copy must match.
Generated Go application invocation enforces every closed constraint before
contributions and Provider dispatch and validates Provider responses after
completion contributions. String bounds count Unicode scalar values rather
than bytes or grapheme clusters. Generated Connect and optional REST adapters
run the same request validator before trusted-root creation. Generated
JavaScript request and response declarations retain each exact normalized
constraint object in a @plystraConstraints annotation. Browser request
preflight and decoded-response validation enforce Unicode scalar-value length,
numeric bounds, and array item counts. Canonical pattern remains declared and
server-authoritative because JavaScript RegExp is not a compatible substitute
for bounded Go regular-expression semantics.

Then regenerate before implementing against the typed contract:

    plystra generate

Inspect generated/go/contracts/records/read/v1/contract_gen.go and
generated/go/providers/records/read/v1/provider_gen.go. Implement the generated
provider interface in the Plugin-owned scaffold:

    package records

    import (
        "context"
        "strings"

        contract "%[1]s/generated/go/contracts/records/read/v1"
    )

    func (*Plugin) Read(_ context.Context, request contract.Request) (contract.Response, error) {
        if request.RecordID == "" || strings.TrimSpace(request.RecordID) != request.RecordID {
            return contract.Response{}, contract.ErrInvalidRecordID
        }
        if request.RecordID != "demo" {
            return contract.Response{}, contract.ErrNotFound
        }
        return contract.Response{
            RecordID: request.RecordID,
            Title:    "Demonstration record",
            Archived: false,
        }, nil
    }

Return only semantic errors declared by that contract. Generated HTTP and
Kernel invocation paths sanitize undeclared errors, provider messages, and
panics. Add Plugin tests for success, every declared error, cancellation or
deadline behavior when relevant, and any state transition owned by the Plugin.

## Version and implement canonical contracts

Use an explicit intent profile for the first version of a new identity:

    plystra capability create records.archive --query --plugin records

When the identity is already visible, the same unversioned command creates the
next version by copying the highest exact contract, including its semantics;
omit profile flags for that later-version workflow. Use an explicit version for
a deliberate unusual new identity and version, select its profile, and confirm
it:

    plystra capability create records.archive/v3 --query --plugin records --confirm

Implement a visible exact Interface as ordinary Go:

    plystra implement email.send/v1 --package ./mailer

--package must be one new canonical Project-relative path beginning with ./.
The scaffold imports the canonical Interface package and adds Service, New, the
//plystra:implements directive, the operation stub, and a compile-time Interface
assertion. It creates no copied contract, configuration, generated output, or
registration. Edit the method, add package tests, and run go test ./.... The CLI
rediscovers and type-checks the constructor; failure removes the whole scaffold.

Never recreate an already visible exact version.
Before a contract appears in any published tag, rewrite it and regenerate local
fixtures directly instead of adding a compatibility wrapper, decoder, fallback,
or parallel old version.
A published v0.0.1-rc.N tag and its artifacts are immutable.
A newer RC may revise the same exact /vN before stable v0.0.1 only by publishing
a new immutable RC, recording compatibility differences, and re-pinning,
regenerating, rebuilding, and revalidating every affected downstream Project.
Never move, delete, overwrite, or reuse a published tag. Do not add a
compatibility wrapper solely for an obsolete RC.
After stable v0.0.1, an incompatible exact contract change requires a new /vN.

## Declare and register providers

plugin.yaml is the authored Plugin declaration. A representative declaration is:

    id: acme.app.records
    provides:
      - records.read/v1
    requires:
      - audit.write/v1
    config:
      endpoint: {type: url, required: true}
      timeout: {type: duration, default: 2s}
      mode: {type: string, default: strict, enum: [strict, relaxed]}
      token: {type: secret, required: true}

Supported configuration field kinds include string, integer, number, boolean,
duration, url, secret, object, and arrays with a supported items kind. Use
required, default, enum, and supported format validation only when the Plugin
actually enforces that contract.

The provides list declares exact canonical provider interfaces. The requires
list declares exact non-inferable runtime dependencies. plystra generate emits
generated provider interfaces, adapters, selected bindings, and the immutable
canonical catalog. There is no handwritten provider registration, enabled
Plugin list, registration function, provider priority, or runtime directory
scan. Do not add one.

## Configure the selected application

Runtime values and application choices belong in the selected current-Project
document, which is root plystra.yaml by default, not in plugin.yaml or
generated source:

    http:
      address: ":8080"
      transports:
        connect: true
        rest: false
      cors:
        allowed_origins:
          - https://app.example.com
        allow_credentials: true
      expose:
        - records.read/v1

    timeouts:
      startup: 2m

    capabilities:
      require:
        - email.send/v1
      use:
        email.send/v1: acme.email.smtp
      aliases: {}

    config:
      acme.app.records:
        endpoint: https://records.example.test
        timeout: 2s
        mode: strict
        token:
          env: RECORDS_TOKEN

Each selected Plugin ID receives exactly one configuration object. The CLI
validates the object against plugin.yaml and generates an immutable typed Config
value. Plugin code consumes that injected Config; it does not parse the shared
YAML file or another Plugin's configuration.

A secret field accepts only a reference such as env: RECORDS_TOKEN or
file: /run/secrets/records-token. Never place plaintext Secret values in
plystra.yaml, plugin.yaml, capability.yaml, diagnostics, generated source, SDK
output, or tests. Generation validates reference structure without resolving
the Secret value; generated bootstrap resolves it at runtime.

Use capabilities.require only for an exact root requirement that exposure,
generated-client use, Plugin requires, Alias targets, or generation extensions
cannot infer. When several compatible Plugins provide one required canonical
ID, set capabilities.use from that canonical ID to the selected Plugin ID.
Never use an Alias ID as a requirement or provider-selection key.

After any manual plugin.yaml, capability.yaml, plystra.yaml, or go.mod change,
run:

    plystra generate

## Consume another Plugin through a Capability

Plugins depend on contracts, never concrete Plugin packages. For example, an
orders Plugin requiring catalog.lookup/v1 declares:

    id: acme.app.orders
    provides:
      - order.place/v1
    requires:
      - catalog.lookup/v1
    config: {}

The example assumes these two authored contracts:

    # catalog.lookup/v1 capability.yaml
    id: catalog.lookup/v1
    request:
      key: {type: string, required: true}
    response:
      value: {type: string, required: true}
    errors: [not_found]

    # order.place/v1 capability.yaml
    id: order.place/v1
    request:
      item_key: {type: string, required: true}
    response:
      order_id: {type: string, required: true}
    errors: [not_found]

Run plystra generate. The CLI emits:

    generated/go/contracts/catalog/lookup/v1/contract_gen.go
    generated/go/clients/catalog/lookup/v1/client_gen.go
    generated/go/dependencies/orders/dependencies_gen.go

Change the authored constructor to accept the generated immutable dependency
set and retain the generated client:

    package orders

    import (
        "context"

        lookupcontract "%[1]s/generated/go/contracts/catalog/lookup/v1"
        ordercontract "%[1]s/generated/go/contracts/order/place/v1"
        dependencies "%[1]s/generated/go/dependencies/orders"
    )

    type Plugin struct {
        clients dependencies.Dependencies
    }

    func New(_ Config, clients dependencies.Dependencies) *Plugin {
        return &Plugin{clients: clients}
    }

    func (p *Plugin) Place(ctx context.Context, request ordercontract.Request) (ordercontract.Response, error) {
        item, err := p.clients.CatalogLookupV1().Lookup(ctx, lookupcontract.Request{Key: request.ItemKey})
        if err != nil {
            return ordercontract.Response{}, err
        }
        return ordercontract.Response{OrderID: "order-" + item.Value}, nil
    }

Use the accessor names generated in dependencies_gen.go; do not guess them.
Do not call a generated client from New: assembly intentionally keeps dispatch
unavailable until every selected provider constructs successfully and the
canonical catalog publishes atomically. Invoke dependencies only after runtime
construction, normally from Capability methods or lifecycle work after Start.

Never import the concrete catalog Plugin from orders. The generated client
preserves provider replacement, explicit selection, application contributions,
deadlines, cancellation, semantic errors, and the same invocation path used by
external adapters.

## Add Capability Aliases

Aliases are application-local direct alternate names for one resolved canonical
target. Add them under plystra.yaml capabilities.aliases:

    capabilities:
      aliases:
        records.fetch/v1:
          target: records.read/v1
        records.lookup/v1:
          target: records.read/v1
          expose:
            go: true
            http: false
            javascript: false
          deprecated:
            message: Use records.read/v1 instead.

An Alias must use the target version, point directly to a canonical target, and
reuse the target request, response, errors, digest, provider, and generated
invocation contributions. It cannot transform data, add defaults, chain through
another Alias, select a provider, or broaden target exposure. Use a real
Capability when behavior or schema differs.

Run plystra generate after Alias edits. Inspect generated/manifest.json,
generated Go Alias clients, HTTP routes, JavaScript operations, API docs, and
deprecation markers. Multiple Aliases may target one canonical Capability
without adding providers or Kernel registrations.

## Expose HTTP and JavaScript surfaces

Expose an existing exact canonical Capability with:

    plystra capability expose records.read/v1
    plystra capability expose records.read/v1 --env production

The default form updates root plystra.yaml. The environment form updates only
the sparse project-root plystra.production.yaml overlay while preserving
comments, unrelated values, and explicit add/remove tombstones. For an
advanced complete replacement, use:

    plystra capability expose records.read/v1 --config deploy/customer-a.yaml

PLYSTRA_ENV and PLYSTRA_CONFIG select the same targets when neither explicit
flag is present. An explicit --env or --config overrides both variables, and
the selector modes cannot be combined. Relative replacement paths resolve from
the Project root even when the command starts inside a Plugin. The command
regenerates with the same selection, reports the selected document path, never
synchronizes an unselected YAML file, and restores the selected document on
failure.

Or expose during creation with --expose. That shortcut uses the default root
configuration. Exposure is application-owned and updates the selected
document's http.expose declaration. The CLI generates a strict POST handler at:

    /api/v1/capabilities/records.read/v1/invoke

Keep transport selection in the selected current-Project document. Only
connect and rest are valid keys. New Project scaffolds record connect: true and
rest: false; omitted values in another selected document use those same schema
defaults. Environment overlays replace the two booleans independently, and
dependency Project transport choices never override the current Project.
JavaScript SDK generation requires Connect. A REST-only selected model with
JavaScript Capability or Alias surfaces fails before output and names every
affected surface; enable connect: true in the selected current-Project
configuration or remove those surfaces.
The generated strict JSON handler remains the implemented HTTP surface, and a
selected Connect surface also receives a generated canonical handler plus any
Alias forwards. Those handlers accept only Connect POST requests encoded as
binary Protobuf or ProtoJSON, require Connect-Protocol-Version: 1, and reject
gRPC and gRPC-Web before root-context or Provider invocation. Their binary
decoder accepts at most 1 MiB, at most 64 nested messages, and at most 65,536
decoded validation nodes. It rejects malformed or truncated data and unknown
fields at every message depth before root-context or Provider invocation.
Direct handler calls apply the same recursive validation. Binary Protobuf
responses use the same 1 MiB, depth-64, and 65,536-node bounds. Generated
conversion preflights canonical content before proportional wire-projection
allocation, validates the exact response message, and serializes
deterministically. Invalid or oversized responses yield the safe internal
response failure without partial bytes on canonical, Alias, and direct paths.
ProtoJSON requests independently accept at most 1 MiB, 64 nested JSON
containers, and 65,536 structural tokens before strict decoding and the same
canonical validation. Unknown or duplicate fields, malformed or trailing
documents, invalid UTF-8, invalid required nulls, enum sentinels, non-finite numbers, and
breached bounds fail before root-context creation or Provider invocation.
Optional non-nullable null becomes absence, explicit zero values remain
present, and full-range integers remain exact. ProtoJSON responses use the
same exact generated message and canonical response validation plus an
independent 1 MiB serialized limit, with no partial response. Canonical and
Alias binary and ProtoJSON paths agree. Server mounting and the optional REST
projection remain in the later transport gates.

Cross-origin configuration belongs in the selected current-Project document.
http.cors accepts only required nonempty allowed_origins and optional boolean
allow_credentials. Origins must be * or origin-only HTTP/HTTPS URLs; a
credentialed wildcard is invalid. Environment overlays may inherit the root
origin list or replace it completely; http.cors: null disables the root
declaration, and dependency Project CORS never applies. The normalized selected
CORS configuration participates in the build-affecting application-model
digest; origin or credential changes create generation drift. Test the
generated Connect handler with canonical and malformed Origin values, literal
null under a noncredentialed wildcard, duplicate requested names, more than
four requested-header values, and more than 4096 bytes of origin or
requested-header input. The boundary rules above must return 403 before trusted
RootContext creation or Implementation invocation. Exact-origin responses and
preflights include Vary; a noncredentialed wildcard emits *. Without http.cors,
handlers emit no CORS response headers and reject cross-origin preflight.

Generated handlers enforce the exact route, application/json, bounded bodies,
required and unknown fields, enums, response validation, safe errors, and
no-store headers. They require a trusted RootContext function and the generated
application invocation handle. The CLI-owned generated/go/application entrypoint
owns default lifecycle startup, signal-driven shutdown, and template health
smoke, but it does not yet mount an HTTP server. Do not edit that generated main
or add a competing startup workaround. The generated Connect handler is
available for direct httptest validation; server mounting remains in the later
HTTP transport gate. Test the real generated handler with httptest, including
binary Protobuf and ProtoJSON success, gRPC and gRPC-Web rejection without
Provider invocation, every semantic error, malformed JSON, nested unknown
binary fields, malformed and truncated binary wire data, the enum zero
sentinel, excessive binary nesting and decoded nodes, wrong media type, and
oversized input. Binary rejections must not create a root context or invoke a
Provider. Also test deterministic binary responses, wrong or unknown response
messages, cyclic object values, oversized output, excessive response nesting
and nodes, canonical and Alias HTTP paths, and direct invocation; response
rejection returns no partial response. For ProtoJSON, also test malformed and
trailing documents, invalid UTF-8, top-level and nested unknown fields, duplicate fields,
required and optional null, explicit zero values, full-range integers, enum
sentinels, non-finite values, more than 64 nested containers, more than 65,536
structural tokens, independently oversized request and response payloads, and
canonical/Alias parity. Invalid input must not create a root context or invoke
a Provider, and response rejection returns no partial canonical response.
RootContext receives the live external request context and may return a trusted
root detached with context.WithoutCancel. The generated boundary reattaches
explicit caller cancellation and derives the earlier caller or trusted-root
deadline without discarding trusted root values. Test a pre-cancelled direct
call and in-flight canonical plus Alias HTTP cancellations; each must reach
ctx.Done in the canonical invocation and Provider context and return no
response. Also test a pre-expired direct deadline, canonical and Alias HTTP
Connect-Timeout-Ms deadlines, and an earlier trusted-root deadline. Each must
retain context.DeadlineExceeded through the Provider and safe Connect error.
Cancellation and deadlines are best-effort and never guarantee that Provider
work already performed is rolled back or compensated.

The provider-independent TypeScript package is under
generated/sdk/javascript. Validate it with:

    cd generated/sdk/javascript
    npm install --ignore-scripts --no-audit --no-fund
    npm run typecheck
    npm run build
    npm pack --dry-run --json

The generated .npmrc disables lockfile creation. A package-lock.json below
generated/sdk/javascript is unexpected generated drift; remove it and rerun
the documented install command.

Use createPlystraClient from the generated package and call the nested exact
version method, for example client.records.read.v1({record_id: "demo"}). Only
explicitly exposed canonical Capabilities and valid Alias surfaces appear.
The wrapper resolves the matching unary method from src/descriptors.ts and
sends binary Connect requests through the pinned direct @bufbuild/protobuf,
@connectrpc/connect, and @connectrpc/connect-web dependencies. Application
callers do not construct raw Protobuf messages or Connect clients and do not
receive raw ConnectError values.
Import only the generated package root. Internal runtime, descriptor, codec,
and binder modules are not public package subpaths or declaration exports.
Canonical integer fields, integer array items, and integer enum members are
signed 64-bit bigint values in this public API. Use literals such as 42n and
never coerce them to JavaScript number values.
ClientOptions requires credentialPolicy. Anonymous is Fetch omit/no
Authorization. Cookie uses fetchCredentials same-origin or include and no
bearer. Bearer is Fetch omit plus getAccessToken for one bounded raw token.
Rejected or invalid tokens fail before dispatch as PlystraError credential_error
without token data; modes never fall back.
AbortSignal in the second argument cancels pre-dispatch, bearer acquisition, or
fetch as PlystraError cancelled; in-flight cancellation reaches fetch/server.
Cancellation is not an Implementation rollback guarantee.
Implementation packages, runtime configuration, verified internal context, and Secret
values must never appear in the browser package.

## Validate every change

Run the narrowest relevant test first, then the complete module checks:

    plystra inspect
    plystra inspect --env production
    plystra inspect --config deploy/customer-a.yaml
    plystra inspect --format json
    plystra explain capability email.send/v1
    plystra explain capability email.send/v1 --env production
    plystra explain capability email.send/v1 --config deploy/customer-a.yaml
    plystra explain capability email.send/v1 --format json
    plystra explain plugin acme.email.smtp
    plystra explain plugin acme.email.smtp --env production
    plystra explain plugin acme.email.smtp --config deploy/customer-a.yaml
    plystra explain plugin acme.email.smtp --format json
    plystra explain config config.acme.email.smtp.host
    plystra explain config config.acme.email.smtp.host --env production
    plystra explain config config.acme.email.smtp.host --config deploy/customer-a.yaml
    plystra explain config config.acme.email.smtp.host --format json
    plystra explain alias mail.send/v1
    plystra explain alias mail.send/v1 --env production
    plystra explain alias mail.send/v1 --config deploy/customer-a.yaml
    plystra explain alias mail.send/v1 --format json
    plystra explain exposure email.send/v1
    plystra explain exposure mail.send/v1 --env production
    plystra explain exposure mail.send/v1 --config deploy/customer-a.yaml
    plystra explain exposure mail.send/v1 --format json
    plystra generate --check
    plystra generate --check --env production
    plystra generate --check --config deploy/customer-a.yaml
    plystra check
    plystra check --env production
    plystra check --config deploy/customer-a.yaml
    go test ./...
    go test -race ./...
    go vet ./...
    go build ./...
    go mod verify

Plystra inspect resolves the same selected model without modifying the Project.
Its default output gives the Project and configuration, Plugin and Capability
counts, AuthN/AuthZ activation, transports, readiness, and the matching plystra
check action. Add --verbose for complete deterministic resolution evidence or
--format json for one plystra.inspect v1 document on stdout; JSON progress and
diagnostics stay on stderr. Use the same --env or --config selector across
inspect, generate, check, and generated application startup.

Plystra check verifies the selected configuration and generated fixed point,
then runs go test -mod=readonly ./... from the Project root. Use the same --env
or --config selector used for generation. The command is read-only and never
repairs YAML, generated output, or module metadata.

plystra generate --check is read-only. It recomputes the complete resolution and
generation fixed point and fails on stale, missing, unexpected, or manually modified
managed paths. If it reports drift:

1. Read that path's generated/.plystra-manifest.json entry for its exact
   generator, normalized input IDs, source references, output kind, and cleanup owner.
2. Change the named authored input; move handwritten files out of generated.
3. Run plystra generate, then plystra generate --check with the same selector.

Keep go.work optional. Standard Go Module dependency resolution remains the
build and distribution boundary for every Plystra module.

## Diagnose common failures

Common actionable CLI failures end with exactly one Recovery: block followed by
one stable Diagnostic: PLYSTRA_<AREA>_<CONDITION> code. Follow that one command or file edit with
the same default, --env, or --config selection, and use the code as the stable
automation or support identity. Recovery output preserves safe explicit and
ambient selectors, but substitutes <environment> or <yaml-path> for unsafe or
absolute selector input. An unclassified internal error receives no guessed
recovery action or code.

- Missing Implementation: require or expose the Interface and make a compatible
  constructor visible in the effective Plystra Project graph. Markerless
  dependencies are not broadly scanned for Plystra declarations.
- Ambiguous Implementation: run plystra use <interface-id> <constructor-symbol>
  with the same --env or --config selector used for the application. Do not add
  priorities or rely on discovery order.
- Invalid plystra use input: PLYSTRA_USE_INTERFACE_INVALID identifies a
  malformed canonical versioned Interface ID, while
  PLYSTRA_USE_CONSTRUCTOR_INVALID identifies a malformed fully qualified
  exported constructor symbol. Run the corrected command emitted by Recovery;
  it preserves the active selector and fails before Project mutation.
- Incompatible contract: compare exact request, response, closed field
  constraints, semantic errors, typed semantics, and extension metadata.
  Implement the visible contract or create a new version instead of weakening
  validation.
- Interface authoring codes:
  PLYSTRA_INTERFACE_DECLARATION_INVALID,
  PLYSTRA_INTERFACE_CONTRACT_INVALID,
  PLYSTRA_INTERFACE_METADATA_INVALID,
  PLYSTRA_INTERFACE_ID_DUPLICATE, and PLYSTRA_AUTHORING_PACKAGE_INVALID.
  They identify the directive, Go contract, optional interface.yaml, visible ID,
  and loadable-package boundaries. Apply Recovery in the owning Project, never
  to a dependency's Module Cache copy.
- Scaffold-command codes:
  PLYSTRA_INTERFACE_CREATE_NAME_INVALID,
  PLYSTRA_INTERFACE_CREATE_TARGET_EXISTS,
  PLYSTRA_IMPLEMENTATION_CREATE_INTERFACE_INVALID,
  PLYSTRA_IMPLEMENTATION_CREATE_INTERFACE_NOT_FOUND,
  PLYSTRA_IMPLEMENTATION_CREATE_PACKAGE_INVALID, and
  PLYSTRA_IMPLEMENTATION_CREATE_TARGET_EXISTS. They distinguish invalid or
  missing identities, unsafe package paths, and existing targets before mutation.
- Implementation authoring codes, in validation order:
  PLYSTRA_IMPLEMENTATION_DECLARATION_INVALID,
  PLYSTRA_IMPLEMENTATION_CONFIG_INVALID,
  PLYSTRA_IMPLEMENTATION_REQUIRED_INTERFACE_INVALID,
  PLYSTRA_IMPLEMENTATION_OPTIONAL_INTERFACE_INVALID,
  PLYSTRA_IMPLEMENTATION_RESULT_INVALID, and
  PLYSTRA_IMPLEMENTATION_CONFORMANCE_INVALID. They identify the directive,
  Config, required parameter, exact plystra.Optional[T] parameter, result, and
  conformance boundary. Apply Recovery to the reported source; fix dependency
  source in its owning Project or select a corrected version, never Module Cache.
- Unavailable generated client: confirm assembly completed and avoid invoking
  application clients while constructors are still running before publication.
- Invalid runtime configuration: compare the selected constructor symbol and
  its compiled Config schema with the object in the selected current-Project
  document. Keep Secret values behind valid env or file references.
- Wrong configuration selection: run plystra inspect with the intended --env or
  --config, add --verbose or --format json when complete provenance is needed,
  then run generate and generate --check with the same selector. For
  automation, set exactly one of
  PLYSTRA_ENV or PLYSTRA_CONFIG. An environment is a sparse overlay above root;
  an explicit file is complete and root plystra.yaml is not merged beneath it.
- Alias error: point directly to one resolved canonical Interface target with
  the same version and exposure no broader than that target.
- Unexpected generated path: remove handwritten content from generated and
  regenerate. Do not overwrite the reported path manually.
- Protobuf wire-history drift: recover the exact previously generated
  generated/proto/wire-map.json. Never edit or delete it to force new field
  or enum-member numbers. Every visible authored Interface enters the ledger
  after successful generation, even before exposure. Use an unused field number
  for an additive field; define a new Interface version for a wire change.
  Generation rejects missing, modified, corrupt, renumbered, reused, or
  projection-inconsistent history instead of guessing.
- Protobuf schema or descriptor drift: never patch generated .proto files or
  generated/proto/descriptor-set.pb. Restore or regenerate the complete
  CLI-owned output, then rerun plystra generate --check.
- Protobuf naming collision: Protobuf-derived names must be unique within each request and response.
  For example, foo1 and foo_1 both derive the ProtoJSON name foo1.
  Likewise, http_status and h_t_t_p_status both derive one HTTPStatusEnum type.
  Rename one of the two canonical fields named by the diagnostic in the
  authored Interface Go package. Do not patch generated names or the wire map;
  ordinary generation and generate --check leave the Project unchanged.
- Unsupported Connect operation kind: the current unary boundary accepts a
  canonical contract with semantics.kind: query or command. Remove the named
  event or stream from http.expose until its operation kind is supported; do
  not relabel the contract to bypass this check.
- Stale output after removal: run plystra generate so the managed-file manifest
  can remove obsolete contracts, clients, adapters, Alias surfaces, docs, and
  SDK operations transactionally.
`

const skillAgentTemplate = `interface:
  display_name: "Plystra Development"
  short_description: "Operate and develop Plystra Projects"
  default_prompt: "Use $plystra for this Go Module, Plugin, Capability, or plystra.yaml task."
`
