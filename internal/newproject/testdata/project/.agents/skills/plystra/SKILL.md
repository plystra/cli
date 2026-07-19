---
name: plystra
description: Operate and develop Plystra Projects through Go Modules, Plugins, versioned Capabilities, and plystra.yaml. Use when starting from a template or creating, implementing, configuring, exposing, consuming, testing, or debugging a Plugin or Capability.
---

# Plystra Module Development

## Choose the smallest workflow

Start here and use only the workflow that matches the requested task. The
ordinary path uses four public concepts: Go Module, Plugin, Capability, and
plystra.yaml. Do not begin by studying the detailed mechanisms later in this
guide unless the task or a Plystra diagnostic specifically requires them.

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

Stay inside the four-concept model:

- The Go Module is the dependency and import boundary.
- A Plugin is one root-level implementation unit declared by plugin.yaml.
- A Capability is one exact versioned contract declared by capability.yaml.
- plystra.yaml selects the application configuration and public surface.

For a new local behavior, use this sequence:

    plystra plugin create records
    plystra capability create records.read --plugin records --expose
    # Edit the authored contract and Plugin method.
    plystra generate
    go test ./...
    plystra check

When one Plugin needs behavior from another, declare the exact Capability in
the caller's plugin.yaml, regenerate, and call the generated dependency client.
Never import the other concrete Plugin package. If Plystra reports several
compatible implementing Plugins for one required Capability, select the exact
one requested by the application:

    plystra use email.send/v1 acme.email.smtp

The detailed reference below contains complete file shapes and variants. Open
only the section needed for the current command or authored file.

### Select one environment

Keep shared choices in root plystra.yaml. Put only environment-specific
differences in one sparse project-root overlay such as
plystra.production.yaml, then use the same selector for generation and checks:

    plystra generate --env production
    plystra generate --check --env production
    plystra check --env production

No selector means root plystra.yaml only. Use --config only when the task
explicitly requires one complete replacement document; it is an advanced
deployment path, not a second ordinary configuration layer.

## Detailed task reference

Read only the section that matches the current task. Ordinary Project and
business-Plugin work does not require Generation Extensions, fixed-point
resolution, contribution graphs, normalized application models, wire-map
allocation, release evidence, or Kernel assembly internals. Those are
CLI-owned or maintainer mechanisms. They remain documented here only where an
infrastructure task, compatibility change, or specific diagnostic makes them
necessary.

## Start from the module boundary

The current Go Module path is example.com/acme/my-app. Read its go.mod before writing imports.

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
      proto/
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

Author plugin.yaml, capability.yaml, Plugin Go implementation, tests, entry
points, and optional Plugin-owned assets outside generated. Treat every path
under generated as CLI-owned. Never repair generated output by hand; change the
authored declaration or implementation and run plystra generate.

generated/proto/wire-map.json is durable CLI-owned compatibility history for
canonical Capability request and response messages selected for Connect. It
keeps field assignments stable across declaration reordering, allocates new
fields without renumbering existing fields, permanently reserves removed field
names and numbers, and retains inactive canonical history when exposure or
Connect is disabled. Scalar contract enums use a numeric zero UNSPECIFIED
sentinel and stable positive member numbers. Reordering and additions preserve
existing assignments; removed member names and numbers remain permanently
reserved, and enum history becomes inactive when the field stops using it. An
application Alias reuses its canonical target messages and enums and never owns
a separate ledger entry. Never edit or delete the ledger. If it drifts, recover
the exact previously generated content before running plystra generate. The
current ledger does not emit .proto source, descriptor sets, or Connect runtime
bindings; those remain later transport work.

Protobuf-derived names must be unique within each request and response. For
example, foo1 and foo_1 both derive the ProtoJSON name foo1, while enum fields
http_status and h_t_t_p_status both derive one HTTPStatusEnum type. Generation
reports the Capability, request or response, both canonical field names, and the
colliding identity before writing output. Rename one authored capability.yaml
field and regenerate; never repair the collision in generated files.

The CLI currently does not create or execute database migrations. When a Plugin
owns migrations, keep them inside that Plugin and make its runtime lifecycle or
provider implementation apply them deliberately. Do not place migrations under
generated.

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
Dependency-composition drift or any changed, missing, unexpected, or obsolete
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

Resolve an inherited Provider conflict with one exact current-Project choice:

    capabilities:
      use:
        email.send/v1: acme.email.smtp

Prefer the targeted public workflow for ordinary Provider selection:

    plystra use email.send/v1 acme.email.smtp
    plystra use email.send/v1 acme.email.production --env production
    plystra use email.send/v1 acme.email.customer --config deploy/customer-a.yaml

The default form writes root plystra.yaml. Environment mode writes only the
selected sparse overlay, and full-replacement mode writes only the selected
complete document. PLYSTRA_ENV and PLYSTRA_CONFIG select the same targets when
no flag is present; an explicit selector overrides both ambient variables. The
command preserves comments and unrelated values, regenerates with the same
selection, and restores configuration, generated output, go.mod, and go.sum
after any later failure. It rejects intrinsic Capabilities, Aliases, unknown or
unrequired Capabilities, unknown Plugins, and Plugins that do not provide the
exact contract.

The current entry replaces inherited choices for email.send/v1, then normal
Provider and exact contract validation still runs. Do not reorder dependencies,
make one direct, invent priority, or copy a dependency Plugin to choose a
winner. After manual replace or dependency-version changes, run plystra
generate and plystra generate --check. Inspect generated/manifest.json for the
non-secret dependency composition digest and path/digest/removal/source
baseline. An explicit tombstone has removed: true; the manifest never contains
raw Plugin configuration or Secret reference targets.

Plystra generate maintains the selected current-Project document with a typed
three-way update from that selection's previous dependency baseline, the
authored current file, and the newly resolved dependency baseline. Default mode
selects root plystra.yaml. It preserves comments, explicit current-Project
values, and exact tombstones; introduces new inherited declarations; and
removes inherited declarations that disappeared. A hand-deleted inherited
value is ambiguous, so express that decision with the field's sparse removal or
null tombstone. Configuration and generated output share one rollback boundary.
Plystra generate --check reports dependency-composition drift against the
selected path without writing either surface.

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

The selected overlay must exist. The CLI does not create common environment
files or load unselected overlays. The effective order is dependency Project
composition, root plystra.yaml, then the selected overlay. Omitted fields
inherit. Scalars and arrays replace at their declared typed field, keyed
objects merge by declared field path, set fields use their sparse add/remove
form, and null keeps its exact tombstone meaning. Unknown fields and type
mismatches remain errors. Dependency Project environment overlays are never
inherited.

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
selection or remove those surfaces. Real Connect/REST projection remains a
later roadmap feature, so rest: true does not yet create a REST adapter.

http.cors is an optional closed current-Project object. When present it
requires one nonempty allowed_origins list and accepts only optional boolean
allow_credentials, which defaults to false. The CLI normalizes, sorts, and
deduplicates HTTP/HTTPS origins; * cannot be combined with credentials. An
environment overlay replaces the complete origin list when present and may omit
it to inherit root origins, while credentials compose independently. The
effective result must still contain origins. Set http.cors to null to disable
root CORS for that environment. A complete --config document does not inherit
root CORS, and dependency Project CORS settings are ignored.

The current CLI validates and composes CORS. Generated CORS response behavior
remains a later HTTP transport feature.

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

Inspect generated/manifest.json configuration schema v4 for default,
environment, or explicit-config mode; the environment and overlay reference
when applicable; Project-relative paths; normalized document digests;
dependency baseline history; the Protobuf wire-map digest; and final
application-model digest. Environment mode retains the root dependency baseline
because the overlay does not own dependency maintenance. The manifest never
records raw configuration, Secret reference targets, resolved Secrets, or
machine-specific absolute paths. Switching a build-affecting selection
correctly creates generated drift.

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

## Create a Project and a Plugin

From the desired parent directory, create `./app/` with the project name as
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

        contract "example.com/acme/my-app/generated/go/contracts/records/read/v1"
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

        lookupcontract "example.com/acme/my-app/generated/go/contracts/catalog/lookup/v1"
        ordercontract "example.com/acme/my-app/generated/go/contracts/order/place/v1"
        dependencies "example.com/acme/my-app/generated/go/dependencies/orders"
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
The current generated handler remains the implemented HTTP surface until the
later Connect and optional REST projection gates consume this selection.

Cross-origin configuration belongs in the selected current-Project document.
http.cors accepts only required nonempty allowed_origins and optional boolean
allow_credentials. Origins must be * or origin-only HTTP/HTTPS URLs; a
credentialed wildcard is invalid. Environment overlays may inherit the root
origin list or replace it completely; http.cors: null disables the root
declaration, and dependency Project CORS never applies. Generated CORS response
behavior remains deferred to the later HTTP transport gate.

Generated handlers enforce the exact route, application/json, bounded bodies,
required and unknown fields, enums, response validation, safe errors, and
no-store headers. They require a trusted RootContext function and the generated
application invocation handle. The CLI-owned generated/go/application entrypoint
owns default lifecycle startup, signal-driven shutdown, and template health
smoke, but it does not yet mount an HTTP server. Do not edit that generated main
or add a competing startup workaround. Connect serving and generated handler
binding remain deferred to the HTTP transport gate. Test the real generated
handler with httptest, including success, every semantic error, malformed JSON,
unknown fields, wrong media type, and oversized input where relevant.

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

Plystra check verifies the selected configuration and generated fixed point,
then runs go test -mod=readonly ./... from the Project root. Use the same --env
or --config selector used for generation. The command is read-only and never
repairs YAML, generated output, or module metadata.

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
- Ambiguous provider: run plystra use <capability-name>/vN <plugin-id> with the
  same --env or --config selector used for the application. Do not add
  priorities or rely on discovery order.
- Incompatible contract: compare exact request, response, semantic errors,
  behavioral metadata, and extension metadata. Implement the visible contract
  or create a new version instead of weakening validation.
- Constructor signature mismatch after adding requires: regenerate, import the
  Plugin's generated dependencies package, and use
  New(Config, dependencies.Dependencies) *Plugin.
- Unavailable generated client: confirm assembly completed and avoid invoking
  clients during Plugin construction.
- Invalid configuration: compare the concrete selected Plugin ID and its
  plugin.yaml config schema with the object in the selected current-Project
  document. Keep Secret values behind valid env or file references.
- Wrong configuration selection: inspect generated/manifest.json mode,
  environment, and document references, then run generate and generate --check
  with the same --env or --config. For automation, set exactly one of
  PLYSTRA_ENV or PLYSTRA_CONFIG. An environment is a sparse overlay above root;
  an explicit file is complete and root plystra.yaml is not merged beneath it.
- Alias error: point directly to a resolved canonical target with the same
  version and exposure no broader than the target.
- Unclaimed extension namespace: add a compatible selected Plugin whose
  generation declaration activates that namespace; do not make the Kernel or
  application interpret extension metadata.
- Unexpected generated path: remove handwritten content from generated and
  regenerate. Do not overwrite the reported path manually.
- Protobuf wire-history drift: recover the exact previously generated
  generated/proto/wire-map.json. Never edit or delete it to force new field
  or enum-member numbers; generation rejects missing, modified, corrupt, reused,
  or inconsistent history instead of guessing.
- Protobuf naming collision: rename one of the two canonical fields named by
  the diagnostic in the authored capability.yaml. ProtoJSON collapses names
  such as foo1 and foo_1, and generated enum initialisms can collapse names such
  as http_status and h_t_t_p_status. Do not patch generated names or the wire
  map; ordinary generation and generate --check leave the Project unchanged.
- Stale output after removal: run plystra generate so the managed-file manifest
  can remove obsolete contracts, clients, adapters, Alias surfaces, docs, and
  SDK operations transactionally.
