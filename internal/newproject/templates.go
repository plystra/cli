package newproject

const goModuleTemplate = `module %s

go 1.26

require github.com/plystra/kernel %s
`

const plystraTemplate = `http:
  address: ":8080"
  expose: []

timeouts:
  startup: 2m

capabilities:
  require: []
  use: {}
  aliases: {}

config: {}
`

const readmeTemplate = `# %s

This is the Plystra Project ` + "`%s`" + `.

Local plugins belong in direct child directories containing ` + "`plugin.yaml`" + `. Do not add a root ` + "`plugins/`" + ` container.

## Development

` + "```powershell" + `
plystra plugin create records
plystra capability create records.read --plugin records --expose
plystra generate
plystra generate --check
go test ./...
go vet ./...
` + "```" + `

Mutating Plystra commands regenerate automatically. Run ` + "`plystra generate`" + ` after manual declaration edits and use ` + "`plystra generate --check`" + ` as the read-only consistency gate.

Generated source under ` + "`generated/`" + ` is owned by the Plystra CLI. Do not edit it manually; commit it to Git.
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
description: Develop, structure, configure, debug, and validate Plystra Go Modules, Plugins, and versioned Capabilities. Use when creating or modifying plugin.yaml, capability.yaml, plystra.yaml, dependency Project composition, generated contracts or clients, provider selection, cross-Plugin calls, Capability Aliases, HTTP exposure, JavaScript SDK output, runtime bootstrap, or generation diagnostics.
---

# Plystra Module Development

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
      .plystra-manifest.json
      manifest.json
      docs/
      go/
        adapters/
        assembly/
        bootstrap/
        clients/
        configuration/
        contracts/
        dependencies/
        invocation/
        providers/
      sdk/javascript/

Author plugin.yaml, capability.yaml, Plugin Go implementation, tests, entry
points, and optional Plugin-owned assets outside generated. Treat every path
under generated as CLI-owned. Never repair generated output by hand; change the
authored declaration or implementation and run plystra generate.

The CLI currently does not create or execute database migrations. When a Plugin
owns migrations, keep them inside that Plugin and make its runtime lifecycle or
provider implementation apply them deliberately. Do not place migrations under
generated.

## Compose dependency Project configuration

The CLI asks Go for the effective module graph. Every direct or transitive
module with regular root plystra.yaml is a dependency Plystra Project; its
root-level Plugins and root configuration become visible. A module without the
root marker is an ordinary dependency and is not scanned. Dependency files such
as plystra.production.yaml and plystra.test.yaml are never inherited.

Composition uses field-specific rules:

- http.expose and capabilities.require form deterministic canonical-ID unions.
  Use their sparse add/remove mapping for exact inherited set edits.
- Identical additions, removals, Provider selections, and Alias declarations
  deduplicate.
- Plugin configuration merges only by fields declared in plugin.yaml.
- Dependency http.address and timeouts.startup never replace this Project's
  process settings.
- Incompatible Provider, Alias, or Plugin-field values fail with every
  contributing module@version/plystra.yaml source.

Resolve an inherited Provider conflict with one exact current-Project choice:

    capabilities:
      use:
        email.send/v1: acme.email.smtp

The current entry replaces inherited choices for email.send/v1, then normal
Provider and exact contract validation still runs. Do not reorder dependencies,
make one direct, invent priority, or copy a dependency Plugin to choose a
winner. After go.mod, replace, or dependency-version changes, run plystra
generate and plystra generate --check. Inspect generated/manifest.json for the
non-secret dependency composition digest and path/digest/removal/source
baseline. An explicit tombstone has removed: true; the manifest never contains
raw Plugin configuration or Secret reference targets.

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

The same Capability cannot appear in both add and remove. A null entry removes
only that keyed Provider or Alias decision. An inherited add/remove conflict
must be resolved by the current Project at that exact key; dependency ordering
never resolves it.

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
  same exact ID only when their request, response, errors, behavioral metadata,
  and normalized extension metadata are exactly compatible.
- An application-local Capability Alias has the same ID grammar but is never a
  provider, canonical contract, reusable requirement, or Kernel registration.

## Create a module and a Plugin

From the desired parent directory, create a Plystra Project interactively:

    plystra new example.com/acme/app

Inside an existing module, create a root-level Plugin:

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

Create and expose a first version in one transaction:

    plystra capability create records.read --plugin records --expose

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

Use the unversioned create workflow for an ordinary first or next version:

    plystra capability create records.archive --plugin records

Use an explicit version only for a deliberate unusual version and confirm it:

    plystra capability create records.archive/v3 --plugin records --confirm

Implement an exact canonical Capability already visible from an official or
dependency module with:

    plystra capability implement email.send/v1 --plugin mailer

That workflow materializes the visible exact contract and adds the local
provider. It does not create a similar private contract. Never recreate an
already visible exact version. Before v0.0.1, rewrite unreleased contracts and
regenerate local fixtures directly instead of adding a compatibility wrapper,
decoder, fallback, or parallel old version. After a public release, never
change a released contract in place; create a new /vN for an incompatible
request, response, semantic-error, or extension-metadata change.

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

Runtime values and application choices belong in root plystra.yaml, not in
plugin.yaml or generated source:

    http:
      address: ":8080"
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

Or expose during creation with --expose. Exposure is application-owned and
updates plystra.yaml http.expose. The CLI generates a strict POST handler at:

    /api/v1/capabilities/records.read/v1/invoke

Generated handlers enforce the exact route, application/json, bounded bodies,
required and unknown fields, enums, response validation, safe errors, and
no-store headers. They require a trusted RootContext function and the generated
application invocation handle. They do not start an HTTP server automatically.

A user-authored entry point typically constructs the runtime with:

    application, err := bootstrap.New(ctx, "plystra.yaml")
    if err != nil { /* handle safe startup error */ }
    if err := application.Start(ctx); err != nil { /* handle safe startup error */ }
    defer application.Stop(shutdownContext)

Bind each generated handler to its matching handle from
application.Invocations(). Use the generated RoutePattern or RoutePath constant
instead of spelling a route again. Test the real generated handler with
httptest, including success, every semantic error, malformed JSON, unknown
fields, wrong media type, and oversized input where relevant.

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
When configuring getAccessToken, return only the raw token. The generated
transport adds the Bearer authorization scheme and rejects a callback value
that already includes the Bearer scheme before sending a request.
Provider packages, runtime configuration, verified internal context, and Secret
values must never appear in the browser package.

## Validate every change

Run the narrowest relevant test first, then the complete module checks:

    plystra generate --check
    go test ./...
    go test -race ./...
    go vet ./...
    go build ./...
    go mod verify

plystra generate --check is read-only. It recomputes the complete resolution and
generation fixed point and fails on changed, missing, unexpected, or obsolete
managed paths. If it reports drift:

1. Identify the authored plugin.yaml, capability.yaml, plystra.yaml, go.mod, or
   generation-extension input that should produce the desired output.
2. Move any handwritten file out of generated.
3. Run plystra generate.
4. Rerun checks and inspect the generated contract, manifest, docs, and SDK
   surfaces affected by the change.

Keep go.work optional. Standard Go Module dependency resolution remains the
build and distribution boundary for every Plystra module.

## Diagnose common failures

- Missing provider: require or expose the intended canonical ID and make a
  compatible provider visible through a local Plugin or a dependency Plystra
  Project in the effective Go Module graph. Markerless dependencies are not
  scanned for Plugins.
- Ambiguous provider: set plystra.yaml capabilities.use for the canonical ID.
  Do not add priorities or rely on discovery order.
- Incompatible contract: compare exact request, response, semantic errors,
  behavioral metadata, and extension metadata. Implement the visible contract
  or create a new version instead of weakening validation.
- Constructor signature mismatch after adding requires: regenerate, import the
  Plugin's generated dependencies package, and use
  New(Config, dependencies.Dependencies) *Plugin.
- Unavailable generated client: confirm assembly completed and avoid invoking
  clients during Plugin construction.
- Invalid configuration: compare the concrete selected Plugin ID and its
  plugin.yaml config schema with the object in plystra.yaml. Keep Secret values
  behind valid env or file references.
- Alias error: point directly to a resolved canonical target with the same
  version and exposure no broader than the target.
- Unclaimed extension namespace: add a compatible selected Plugin whose
  generation declaration activates that namespace; do not make the Kernel or
  application interpret extension metadata.
- Unexpected generated path: remove handwritten content from generated and
  regenerate. Do not overwrite the reported path manually.
- Stale output after removal: run plystra generate so the managed-file manifest
  can remove obsolete contracts, clients, adapters, Alias surfaces, docs, and
  SDK operations transactionally.
`

const skillAgentTemplate = `interface:
  display_name: "Plystra Development"
  short_description: "Develop and validate Plystra applications"
  default_prompt: "Use $plystra to implement and validate this Plystra module, Plugin, or Capability change."
`
