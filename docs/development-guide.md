# Plystra Development Guide

This guide describes the implementation that exists after Gate 9. It is for
contributors working on the Kernel or CLI and for developers building a Plystra
Go Module with the current public CLI.

`core-philosophy/` remains the binding architecture specification. This guide
adds operational detail from the working implementation; it does not replace
that specification.

## Current implementation boundary

Plystra Core is exactly the Kernel plus the CLI:

- `github.com/plystra/kernel` is the intrinsic in-process runtime.
- `github.com/plystra/cli` resolves applications and generates their typed Go,
  HTTP, JavaScript, documentation, assembly, bootstrap, and manifest surfaces.
- `github.com/plystra/authn` and `github.com/plystra/authz` are optional official
  Plugin modules. Their implementation is deferred to Gates 10 and 11; at this
  boundary they contain architecture documentation, not usable providers.

The public command surface currently implemented by the `plystra` binary is:

```text
plystra help
plystra version
plystra new <project-name> [--module <go-module-path>] [--template <go-module-query>] [options]
plystra add <go-module-query>
plystra remove <go-module-path>
plystra update <go-module-query>
plystra use <capability-name>/vN <plugin-id> [--env <environment>|--config <yaml-path>]
plystra plugin create <name>
plystra capability create <capability-name> [--plugin <plugin>] [--confirm] [--expose]
plystra capability implement <capability-name>/vN [--plugin <plugin>]
plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]
plystra generate [--check] [--env <environment>|--config <yaml-path>]
```

Commands documented in the roadmap but absent from `plystra --help` are not
implemented. In particular, do not tell users to rely on `dev`, `test`,
`build`, `check`, `fix`, `doctor`, SDK packaging, or
`release` yet. Use the Go, npm, and public generation commands in this guide.

## Workspace and repository layout

The development workspace is a multi-repository container:

```text
core-workspace/
  AGENTS.md                 workspace operating rules
  go.work                   optional local integration convenience
  kernel/                   independent Go Module and Git repository
  cli/                      independent Go Module and Git repository
  authn/                    deferred official Plugin module
  authz/                    deferred official Plugin module
  core-philosophy/          binding specification repository
  core-example/             local disposable CLI acceptance project
  core/                     deprecated, read-only reference
  core-dev-docs/            deprecated, read-only reference
  core-legacy/              deprecated, read-only reference
```

Run Git commands inside the repository that owns the files. The workspace root
is not a Git repository. Never edit the three deprecated reference directories.
`core-example` is test infrastructure, not a product source of truth: fix any
defect it reveals in Kernel, CLI, generators, templates, or an official module.

The CLI implementation is organized by one responsibility per internal
package. Important entry points are:

| Path | Responsibility |
| --- | --- |
| `cmd/plystra/` | installed command entry point |
| `internal/command/` | public argument parsing, help, output, exit codes |
| `internal/newproject/` | transactional project scaffolding |
| `internal/plugincreate/` | transactional Plugin creation |
| `internal/capabilitycreate/` | Capability version creation and implementation |
| `internal/capabilityexpose/` | comment-preserving `plystra.yaml` exposure mutation |
| `internal/applicationinput/` | bounded, immutable application inputs |
| `internal/applicationresolve/` | complete provider and extension fixed point |
| `internal/applicationgenerate/` | complete Plystra Project generation transactions |
| `internal/aliasresolution/` | final application-local Alias map |
| `internal/generation*` | extension activation, execution, lowering, and ordering |
| `internal/*gen/` | typed generated surfaces |
| `internal/generatedfiles/` | manifest ownership and drift reporting |
| `internal/atomicfs/` | transactional install and rollback |
| `internal/gocommand/` | bounded Go subprocess and workspace isolation |
| `generation/v1/` | public extension API imported by advanced Plugins |

Kernel packages are stable runtime boundaries rather than application models:

| Path | Responsibility |
| --- | --- |
| `capability/` | exact typed Capability contracts and IDs |
| `plugin/` | strict Plugin manifest and configuration declarations |
| `assembly/` | immutable already-resolved provider registry boundary |
| `invocation/` | exact dispatch, safe errors, deadlines, and limits |
| `configuration/` | bounded documents, typed decoding, and Secret resolution |
| `lifecycle/` | ordered startup, rollback, and shutdown |
| `intrinsic/` | `kernel.health/v1` and `kernel.info/v1` |

## Prerequisites and setup

Install:

- Go 1.26 or the version declared by the checked-out `go.mod` files.
- Git for repository work and optional project initialization.
- Node.js and npm for the generated JavaScript golden package.
- `staticcheck` for the full local quality pass.
- Docker only when a Plugin or later acceptance scenario genuinely needs
  external infrastructure; Gate 9 itself has no Docker runtime dependency.

Verify the tools from PowerShell:

```powershell
go version
git --version
node --version
npm --version
staticcheck -version
```

The root `go.work` connects `kernel`, `cli`, `authn`, and `authz` during local
integration. It is optional and must never become a build, runtime, install, or
release requirement. From the workspace root:

```powershell
go work sync
```

Build the current CLI:

```powershell
cd cli
go build -o dist/plystra.exe ./cmd/plystra
./dist/plystra.exe --help
```

On macOS or Linux, use `dist/plystra` instead of `dist/plystra.exe`.

Prove each module independently when its declared versions are available:

```powershell
cd kernel
$env:GOWORK = 'off'
go test ./...
go build ./...

cd ../cli
$env:GOWORK = 'off'
go test -p 2 ./...
go build ./cmd/plystra
Remove-Item Env:GOWORK
```

`-p 2` is a bounded Windows setting, not a semantic requirement. It avoids
process exhaustion on machines where the full CLI suite starts many nested Go
commands. A failure to download the current pre-release Kernel pseudo-version
means that version is unavailable through normal module resolution; use the
root workspace for source integration. Do not add a permanent `replace` or make
`go.work` part of a released contract.

## Validate Kernel and CLI changes

Run the narrowest affected package first, then the full module suite.

Kernel:

```powershell
cd kernel
go test ./invocation -count=1
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
staticcheck ./...
go mod verify
go build ./...
```

CLI:

```powershell
cd cli
go test ./internal/aliasresolution -count=1
go test -p 2 ./... -count=1
$env:GOWORK = 'off'
go test -p 2 ./... -count=1
Remove-Item Env:GOWORK
go test -race -p 2 ./... -count=1
go vet ./...
staticcheck ./...
go mod verify
go build -o dist/plystra.exe ./cmd/plystra
```

Check formatting and patch integrity:

```powershell
$files = rg --files -g '*.go'
gofmt -l $files
git diff --check
```

An empty `gofmt -l` result is success. Do not update a golden merely to make a
test pass. When generator output intentionally changes, inspect the semantic
diff before using the package-specific update flag:

```powershell
go test ./internal/newproject -run TestCreate -update
go test ./internal/javascriptgen -run TestRender -update
go test ./internal/apidocgen -run TestRender -update
```

Validate the checked-in JavaScript package:

```powershell
cd cli/internal/javascriptgen/testdata/canonical
npm ci --ignore-scripts --no-audit --no-fund
npm run typecheck
npm run build
npx --no-install tsc -p test/tsconfig.json
node --conditions=browser --test test/runtime.test.mjs
npm pack --dry-run --json
```

Remove ignored `node_modules/` and `dist/` after local validation when they are
no longer needed.

## Create a Plystra Go Module

Interactive creation asks, in order, whether to initialize Git, include GitHub
Actions CI, and include `.agents/skills/plystra/`. Enter accepts yes.

```powershell
plystra new orders
plystra new orders --module example.com/acme/orders
plystra new orders --module example.com/acme/orders --template example.com/acme/platform@v1.2.3
```

The positional value creates exactly `./orders/`. Without `--module`, the
initial `go.mod` directive is `module orders`. Use `--module` when directory
identity and a standard publishable Go Module path differ. Project names must
be one safe lower-case ASCII kebab-case child component; absolute paths,
traversal, separators, `.`, `..`, unsafe names, and existing targets fail
before mutation. The old positional full-module-path syntax is not supported.

`--template` accepts one standard Go Module query. Go resolves the query, the
selected module must contain regular root `plystra.yaml`, and the new Project
retains it as a direct `go.mod` requirement. The CLI composes that dependency
Project's root declarations, regenerates the complete application, and validates
the staged Project before installing the target directory. It does not clone a
source repository, copy Plugin directories, inspect dependency environment
overlays, modify Module Cache source, generate `go.work`, or assign the template
special Provider or configuration precedence after creation.

Automation must answer all three choices explicitly:

```powershell
plystra new orders --module example.com/acme/orders --no-git --no-github-ci --skills
plystra new orders --module example.com/acme/orders --template example.com/acme/platform@v1.2.3 --no-git --no-github-ci --skills
plystra new contracts --module example.com/acme/contracts --no-git --no-github-ci --no-skills
plystra new orders --module example.com/acme/orders --plugin catalog --git --github-ci --skills
```

Success reports the installed module and target. Template creation also names
the selected query:

```text
created example.com/acme/orders in <absolute-path>/orders
created example.com/acme/orders from example.com/acme/platform@v1.2.3 in <absolute-path>/orders
```

The second form is template creation. This implementation does not yet provide
the later qualified-template acceptance suite that also proves build, isolated
startup, intrinsic health, and clean shutdown. Do not describe a template as
qualified until that complete automated suite exists.

Every Plystra Project contains mandatory root `plystra.yaml` and is
independently runnable. A new Project may validly contain zero local Plugins,
primarily distribute reusable Plugins, or obtain selected Providers from
dependency Projects. Project creation also emits `.gitignore`,
`.gitattributes`, a complete generated foundation, and the optional files
selected by the user. It pins the exact Kernel version supported by that CLI
build.

If a non-interactive caller omits any choice, the command fails before creating
the target. Repeated or contradictory choice flags also fail without mutation.

## Understand authored and CLI-owned files

A populated Plystra Project normally looks like:

```text
orders/
  go.mod
  go.sum
  plystra.yaml
  cmd/app/main.go                         user-authored, when needed
  catalog/
    plugin.yaml                           authored declaration
    plugin.go                             authored implementation
    plugin_test.go                        authored tests
    capability_catalog.item.get_v1.go     authored provider method
    capabilities/
      catalog.item.get/
        v1/capability.yaml                authored canonical contract
    migrations/                           optional Plugin-owned assets
  generated/
    .plystra-manifest.json                CLI ownership manifest
    manifest.json                         resolved application manifest
    docs/
    go/
      adapters/
      assembly/
      bootstrap/
      clients/
      configuration/
      contracts/
      dependencies/
      internal/
      invocation/
      providers/
    sdk/javascript/
```

Edit declarations, Plugin Go code, tests, entry points, and Plugin-owned assets
outside `generated/`. Every path under `generated/` is CLI-owned. Fix its
authored input and regenerate; never patch generated output by hand.

`.agents/skills/plystra/` is a creation-time project guide that the project may
maintain as its authored workflows evolve. It is outside `generated/` and is not
part of `plystra generate --check` ownership.

The current CLI does not scaffold or run database migrations. A Plugin that
owns migrations keeps them under that Plugin, and its lifecycle or provider
logic applies them explicitly. Migration commands and reusable database Plugin
workflows remain deferred; do not place migration assets under `generated/`.

## Compose dependency Project configuration

Add one ordinary Go Module query through the public transaction:

```powershell
plystra add github.com/acme/email@v1.4.2
```

Remove a selected module by exact path without a version query:

```powershell
plystra remove github.com/acme/email
```

Update exactly one selected module to an explicit version:

```powershell
plystra update github.com/acme/email@v1.5.0
```

Omitting `@version` asks ordinary Go tooling for its normal upgrade selection
for that module. The command never turns an omitted argument into a whole-graph
upgrade.

All three commands may start at the Project root or inside a Plugin. Add resolves
the query through ordinary Go tooling and retains the module as a direct
`go.mod` requirement even when its declarations do not create a Go import.
Remove requires the exact module path to be selected in `go.mod`, removes it
through ordinary Go tooling, and verifies that regeneration plus tidy did not
select it again. Update also requires an existing selection, preserves a direct
requirement as direct, and verifies that the module remains selected. It
targets one query; ordinary Go resolution may still adjust transitive versions
required by the selected graph. Each command recomposes the dependency-derived
root `plystra.yaml` baseline, regenerates, tidies, and runs
`go test -mod=readonly ./...`. The current dependency surfaces use the default
root configuration; environment and full-replacement validation for dependency
mutations remains incomplete. The commands never rewrite an unselected overlay or
alternative YAML file. Any later failure restores `go.mod`, `go.sum`, root
configuration, generated output, and every other transaction-owned file.

Every direct or transitive module in the effective Go Module graph whose root
contains regular `plystra.yaml` is a dependency Plystra Project. The CLI scans
its root-level Plugins and composes only that root configuration. It ignores a
dependency's `plystra.production.yaml`, `plystra.test.yaml`, and every other
environment-specific sibling. A markerless Go module remains an ordinary
dependency even when it contains a file named `plugin.yaml` below its root.

Composition is typed and independent of dependency order:

- `http.expose` and `capabilities.require` form canonical-ID unions. Use their
  sparse `{add: [...], remove: [...]}` form for an exact inherited set edit.
- Identical additions, removals, Provider selections, and Alias declarations
  deduplicate.
- Plugin configuration merges by fields declared in that Plugin's
  `plugin.yaml`. Declared objects merge recursively; scalar and array fields
  replace as complete values. A keyed `null` removes one inherited field, and
  `config.<plugin-id>: null` removes that Plugin's inherited object. Unknown
  fields and invalid or changing types fail.
- Dependency `http.address`, `http.transports`, `http.cors`,
  `timeouts.startup`, and other process settings do not enter the current
  Project.
- Incompatible inherited Providers, Aliases, or Plugin fields fail with every
  contributing `module@version/plystra.yaml` source.

Resolve an inherited Provider conflict in the current Project at the exact
canonical key:

```yaml
capabilities:
  use:
    email.send/v1: acme.email.smtp
```

This is an explicit current-Project replacement. It does not grant priority to
the dependency that supplies `acme.email.smtp`, and normal provider and exact
contract validation still runs. Use the same exact-field principle for an
Alias or Plugin configuration conflict; do not add dependency priority,
reorder `go.mod`, or copy a dependency's Plugin locally.

Record an exact inherited removal without copying the rest of the dependency
configuration:

```yaml
http:
  expose:
    remove:
      - diagnostics.internal/v1

capabilities:
  require:
    remove:
      - audit.legacy/v1
  use:
    email.send/v1: null
  aliases:
    mail.send/v1: null

config:
  acme.email.smtp:
    legacy_host: null
```

The sparse set form may contain `add`, `remove`, or both, but the same
Capability cannot occur in both lists. A keyed `null` removes only that exact
Provider, Alias, Plugin object, or declared Plugin-field decision. Nested
object keys merge recursively, while arrays are one replaceable value rather
than an appendable list. When dependencies disagree between an addition and
removal, the current Project must make that same exact decision. Removing a
required Plugin field still fails final configuration validation unless a
valid default supplies it.

After manually changing a replacement or dependency version, run:

```powershell
plystra generate
plystra generate --check
```

`plystra generate` compares the previous dependency baseline, the authored root
configuration, and the newly resolved dependency baseline. The typed update
keeps comments, explicit current-Project values, and exact removal tombstones;
adds newly inherited declarations; and removes disappeared inherited values
that were not retained locally. Deleting an inherited value by hand is
ambiguous, so record the field-specific sparse removal or `null` tombstone
instead. Configuration maintenance and generated output share one rollback
boundary. `plystra generate --check` reports `changed plystra.yaml (dependency
composition)` without modifying the configuration or generated tree.

Inspect `generated/manifest.json` for the non-secret dependency composition
digest and path/digest/removal/source baseline. An explicit tombstone has
`"removed": true`; the manifest records provenance, not raw Plugin
configuration or Secret reference targets.

## Select an environment or complete alternative configuration

Root `plystra.yaml` is mandatory and is the default current-Project
configuration. A named environment adds one sparse project-root overlay above
that shared root. For example, create only the differences needed by production
in `plystra.production.yaml`:

```yaml
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
```

Generate and check with the same selector:

```powershell
plystra generate --env production
plystra generate --check --env production
```

The file must exist; the CLI never creates common environment files or loads
an unselected `plystra.*.yaml`. The effective declarative order is dependency
Project composition, root `plystra.yaml`, then the selected sparse overlay.
Omitted fields inherit. Scalars and declared arrays replace at their typed
field, keyed objects merge by declared field path, set fields use their sparse
`add` and `remove` form, and `null` keeps its existing exact tombstone meaning.
Unknown fields and type mismatches remain errors. A dependency Project's own
environment files are never inherited.

`http.transports` is a closed current-Project object. It accepts only boolean
`connect` and `rest` fields. When omitted, Connect defaults to enabled and REST
defaults to disabled. In an environment overlay the two fields replace
independently: an omitted field inherits the root choice, while `null` restores
that field's schema default. A full-replacement file does not inherit root
transport choices; omitted transport fields use the same defaults. Dependency
Project transport settings never participate in composition.

At this implementation boundary, resolution validates and composes the closed
selection. Connecting it to the generated application model, Connect output,
and optional REST projection remains in the later transport gates; setting
`rest: true` does not yet create a REST adapter.

`http.cors` is an optional closed current-Project object. When present it
requires one nonempty `allowed_origins` list and accepts only an optional
boolean `allow_credentials`, which defaults to `false`. The CLI lowercases
schemes and hosts, removes default ports, sorts, and deduplicates HTTP/HTTPS
origins; `*` is allowed only without credentials. In an environment overlay,
the origin list replaces as one deterministic value when present and may be
omitted to inherit root origins, while credentials replace independently. The
effective result must still contain origins. Use `http: {cors: null}` to disable
root CORS for that selected environment. A full-replacement file does not
inherit root CORS, and dependency Project CORS never participates in
composition.

At this implementation boundary, resolution validates and composes CORS but
the generated HTTP handler does not yet emit CORS response behavior. That
projection remains in the later HTTP transport gate.

For automation, select the same environment name with:

```powershell
$env:PLYSTRA_ENV = "production"
plystra generate --check
```

To generate against one complete alternative document instead, select it
explicitly:

```powershell
plystra generate --config deploy/customer-a.yaml
plystra generate --check --config deploy/customer-a.yaml
```

The effective declarative order is dependency Project composition followed by
the selected complete document. Root `plystra.yaml` remains the Project marker
but is not merged beneath `deploy/customer-a.yaml`. Put every current-Project
Provider choice, exposure, Alias, process setting, and Plugin value needed by
that application model in the selected document.

Relative paths are resolved from the detected Project root, including when the
command starts in a nested Plugin directory. An absolute path is accepted only
when it resolves inside that root. For automation, set the selector variable:

```powershell
$env:PLYSTRA_CONFIG = "deploy/customer-a.yaml"
plystra generate --check
```

`--env` and `--config` cannot be combined. Setting both `PLYSTRA_ENV` and
`PLYSTRA_CONFIG` is also an error. Either explicit CLI selector overrides both
ambient variables, so a stale variable cannot change an explicit invocation.
Environment generation maintains dependency-derived changes in root
`plystra.yaml` and preserves the selected overlay as a sparse user-authored
document. Explicit-config generation maintains only the selected complete
document; it never copies the same edit into root or another alternative file.
Dependency baseline history is retained independently for each maintained
selection, so switching modes does not transfer ownership decisions.
Configuration maintenance and generated output share one transaction. Check
mode reports the maintained path, such as
`changed deploy/customer-a.yaml (dependency composition)`, and does not write
either surface.

`generated/manifest.json` configuration schema v3 records `default`,
`environment`, or `explicit-config` mode; the environment name and overlay
reference when applicable; project-relative paths; normalized document
digests; dependency baseline history; and the final build-affecting
application-model digest. Environment mode reuses the root dependency baseline
because overlays do not own dependency maintenance. The manifest excludes raw
configuration, Secret reference targets, resolved Secrets, and machine-specific
absolute paths. Use the same selection for generation and its check; selecting
another build-affecting model correctly reports generated drift.

## Create and configure a Plugin

From a module root:

```powershell
plystra plugin create catalog
```

The command emits a root-level `catalog/` directory. Do not add a root
`plugins/` container. Success resembles:

```text
created plugin acme.orders.catalog in <absolute-path>/catalog
```

Read the exact generated Plugin ID from `catalog/plugin.yaml`; do not derive it
again in application code. A Plugin with no requirements starts with:

```go
func New(config Config) *Plugin
```

Declare configuration in `plugin.yaml`:

```yaml
id: acme.orders.catalog
provides: []
requires: []
config:
  endpoint: {type: url, required: true}
  timeout: {type: duration, default: 2s}
  mode: {type: string, default: strict, enum: [strict, relaxed]}
  token: {type: secret, required: true}
```

Generation emits the typed configuration adapter under
`generated/go/configuration/`. Runtime values belong in the selected
current-Project document, which is root `plystra.yaml` by default:

```yaml
config:
  acme.orders.catalog:
    endpoint: https://catalog.example.test
    timeout: 2s
    mode: strict
    token:
      env: CATALOG_TOKEN
```

A Secret field accepts an `env` or absolute `file` reference, never plaintext.
Generation validates only the reference structure. Generated bootstrap resolves
the value at runtime and injects it only into the selected Plugin's typed
configuration. Secret values and reference targets must not enter generated
source, manifests, SDKs, docs, logs, errors, or extension input.

## Create, version, and implement a Capability

Canonical IDs use two or more lower-case dotted name segments plus one positive
major version:

```text
catalog.item.get/v1
authn.login.oidc.complete/v1
```

The ID identifies a provider-independent contract. It never contains a Plugin
ID or module path.

Create and expose a first version in one transaction:

```powershell
plystra capability create catalog.item.get --plugin catalog --expose
```

This creates `catalog/capabilities/catalog.item.get/v1/capability.yaml`, adds
the exact ID to `catalog/plugin.yaml`, emits a compile-safe provider method
scaffold, adds the canonical ID to `plystra.yaml` HTTP exposure, regenerates,
tidies, and tests. The initial method deliberately returns
`implementation.unavailable`; replace it before treating the Capability as
implemented.

Define the provider-independent contract:

```yaml
id: catalog.item.get/v1
description: Returns one catalog item.

request:
  item_id:
    type: string
    required: true

response:
  item_id: {type: string, required: true}
  name: {type: string, required: true}
  price_cents: {type: integer, required: true}

errors:
  - invalid_item_id
  - not_found
```

Regenerate before implementing against the typed contract:

```powershell
plystra generate
```

Inspect:

```text
generated/go/contracts/catalog/item/get/v1/contract_gen.go
generated/go/providers/catalog/item/get/v1/provider_gen.go
```

Then implement the generated interface in the Plugin-owned scaffold:

```go
package catalog

import (
    "context"
    "strings"

    contract "example.com/acme/orders/generated/go/contracts/catalog/item/get/v1"
)

func (*Plugin) Get(_ context.Context, request contract.Request) (contract.Response, error) {
    if request.ItemID == "" || strings.TrimSpace(request.ItemID) != request.ItemID {
        return contract.Response{}, contract.ErrInvalidItemID
    }
    if request.ItemID != "coffee" {
        return contract.Response{}, contract.ErrNotFound
    }
    return contract.Response{
        ItemID: request.ItemID,
        Name: "Plystra Coffee",
        PriceCents: 1800,
    }, nil
}
```

Return only semantic errors declared by the exact contract. Provider messages,
undeclared errors, and panics are normalized before they cross the Kernel or
HTTP boundary.

An omitted version creates `v1`, or the next version above the highest visible
version. An unusual explicit new version requires deliberate confirmation:

```powershell
plystra capability create catalog.item.search/v3 --plugin catalog --confirm
```

Implement an exact visible dependency or official contract instead of creating
a similar private one:

```powershell
plystra capability implement email.send/v1 --plugin mailer
```

Before `v0.0.1`, rewrite unreleased contracts directly when needed for the clean
initial API. After a version is publicly released, incompatible request,
response, error, guarantee, serialization, or extension metadata requires a
new `/vN`.

## Resolve providers and consume cross-Plugin Capabilities

`plugin.yaml` `provides` declares exact provider interfaces. `requires`
declares exact runtime dependencies that cannot be inferred from exposure,
aliases, generated clients, or extensions.

For an `orders` Plugin that calls `catalog.item.get/v1`:

```yaml
id: acme.orders.checkout
provides:
  - order.place/v1
requires:
  - catalog.item.get/v1
config: {}
```

Run `plystra generate`. The CLI emits:

```text
generated/go/clients/catalog/item/get/v1/client_gen.go
generated/go/dependencies/checkout/dependencies_gen.go
```

Accept and retain the generated immutable dependency set:

```go
import dependencies "example.com/acme/orders/generated/go/dependencies/checkout"

type Plugin struct {
    clients dependencies.Dependencies
}

func New(_ Config, clients dependencies.Dependencies) *Plugin {
    return &Plugin{clients: clients}
}
```

Call the generated client from a Capability method, not during `New`:

```go
item, err := p.clients.CatalogItemGetV1().Get(ctx, catalogcontract.Request{
    ItemID: request.ItemID,
})
```

Never import the concrete catalog Plugin. Generated clients preserve provider
replacement and run the same application contributions as external calls.
Dispatch is intentionally unavailable during constructors until every selected
provider is built and the canonical catalog publishes atomically.

If several compatible Plugins provide the same required ID, generation fails
until `plystra.yaml` selects one canonical provider:

```yaml
capabilities:
  require:
    - email.send/v1
  use:
    email.send/v1: acme.email.smtp
  aliases: {}
```

Use the targeted command for the same explicit current-Project decision:

```powershell
plystra use email.send/v1 acme.email.smtp
plystra use email.send/v1 acme.email.production --env production
plystra use email.send/v1 acme.email.customer --config deploy/customer-a.yaml
```

The default form writes root `plystra.yaml`; `--env` writes only the selected
sparse project-root overlay; and `--config` writes only the selected complete
replacement document. `PLYSTRA_ENV` and `PLYSTRA_CONFIG` provide the same
selection when no flag is present, while an explicit flag overrides both
variables. The command may start inside a Plugin, preserves comments and
unrelated values, regenerates and validates with the same selection, and
restores the selected YAML, generated output, `go.mod`, and `go.sum` if any
later step fails. It rejects intrinsic Capabilities, application Aliases,
unknown or unrequired Capabilities, unknown Plugins, and Plugins that do not
provide the exact contract.

There is no provider priority, discovery-order winner, enabled-Plugin file, or
runtime selection fallback.

## Declare Capability Aliases

Aliases are application-local alternate names for one direct, same-version
canonical target. Edit root `plystra.yaml` and regenerate:

```yaml
capabilities:
  require: []
  use: {}
  aliases:
    catalog.item.fetch/v1:
      target: catalog.item.get/v1
    catalog.item.lookup/v1:
      target: catalog.item.get/v1
      expose:
        go: true
        http: false
        javascript: false
      deprecated:
        message: Use catalog.item.get/v1 instead.
```

An Alias has no provider or independent contract. It cannot target another
Alias, transform data, add defaults, choose a provider, broaden target
exposure, or enter the Kernel registry. Generated Go clients, HTTP wrappers,
JavaScript operations, OpenAPI, and application docs reuse the target's exact
contract and application invocation path.

Use an Alias only as an intentional application API name. Do not preserve an
unreleased or deprecated implementation with compatibility aliases or shims.

## Generate and test HTTP exposure

Expose an existing exact canonical Capability:

```powershell
plystra capability expose catalog.item.get/v1
plystra capability expose catalog.item.get/v1 --env production
```

The default command writes root `plystra.yaml`. The environment form writes
only the sparse project-root `plystra.production.yaml` overlay, preserving its
comments, unrelated values, and explicit add/remove tombstones. For an
advanced complete replacement, use:

```powershell
plystra capability expose catalog.item.get/v1 --config deploy/customer-a.yaml
```

`PLYSTRA_ENV` and `PLYSTRA_CONFIG` select the same targets when no explicit
selector is present. An explicit `--env` or `--config` overrides both ambient
variables; the two explicit flags and the two ambient variables cannot be
combined. Relative `--config` paths resolve from the Project root even when the
command starts inside a Plugin. Success reports the selected document path,
regenerates with the same selection, never synchronizes an unselected YAML
file, and is byte-idempotent when generated output is current. A selection,
generation, module, or validation failure restores the selected document and
all other files in the transaction.

The generated strict handler is under:

```text
generated/go/adapters/http/catalog/item/get/v1/handler_gen.go
```

Its route is:

```text
POST /api/v1/capabilities/catalog.item.get/v1/invoke
```

The CLI generates handlers, not an HTTP server. A user-authored entry point
constructs the runtime and binds each generated handler:

```go
application, err := bootstrap.New(ctx, "plystra.yaml")
if err != nil {
    return err
}
if err := application.Start(ctx); err != nil {
    return err
}
defer application.Stop(shutdownContext)

handler, err := httpcatalogitemgetv1.New(
    func(request *http.Request) (context.Context, error) {
        return request.Context(), nil
    },
    application.Invocations().CatalogItemGetV1(),
)
if err != nil {
    return err
}
mux := http.NewServeMux()
mux.Handle(httpcatalogitemgetv1.RoutePattern, handler)
```

The root-context function is a trusted adapter boundary. Do not convert raw
headers into verified internal AuthN state there. Official AuthN behavior is
deferred until Gate 10.

Use `httptest` against the generated handler. Cover success, every declared
semantic error, unknown and missing fields, invalid JSON, wrong media type,
oversized input when relevant, and safe response headers. The generated
transport enforces exact routes, `application/json`, 1 MiB JSON bounds,
`Cache-Control: no-store`, and `X-Content-Type-Options: nosniff`.

## Validate the generated JavaScript SDK

Every explicitly HTTP-exposed canonical Capability is also available in the
generated browser package unless Alias exposure narrows it:

```text
generated/sdk/javascript/
  .npmrc
  package.json
  README.md
  src/index.ts
  src/runtime.ts
  src/operations/catalog/item/get/v1.ts
```

Validate a generated project. The CLI-owned `.npmrc` prevents `npm install`
from creating an unmanaged lockfile:

```powershell
cd generated/sdk/javascript
npm install --ignore-scripts --no-audit --no-fund
npm run typecheck
npm run build
npm pack --dry-run --json
```

Use the generated client from frontend-style code:

```ts
import { createPlystraClient } from "@acme/orders-sdk";

const client = createPlystraClient({
  baseUrl: "http://localhost:8080",
  getAccessToken: async () => rawAccessToken,
});

const item = await client.catalog.item.get.v1({item_id: "coffee"});
```

`getAccessToken` returns the raw token only. The generated transport adds the
`Bearer` authorization scheme; do not include `Bearer` in the callback result.
An already-prefixed value fails locally before any request is sent.

The SDK validates requests and responses, uses the exact generated HTTP route,
normalizes network and credential failures, and exposes only stable error
status, code, and detail code. Provider packages, runtime configuration,
verified internal context, and Secrets must not appear in the package.

## Develop a generation extension

Generation extensions are advanced trusted build dependencies for Plugins that
own cross-cutting build-time integration. Ordinary business Plugins do not need
one.

Declare the API, confined package, and activation Capability in `plugin.yaml`:

```yaml
id: acme.routing.default
provides:
  - routing.aliases/v1
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: routing
      capability: routing.aliases/v1
```

The package imports `github.com/plystra/cli/generation/v1` and exports exactly:

```go
package generation

import plystragen "github.com/plystra/cli/generation/v1"

func Generate(context plystragen.GenerationContext) (plystragen.Output, error) {
    return plystragen.Output{}, nil
}
```

The selected provider of the activation Capability owns the only extension
allowed to interpret that namespace. The input exposes immutable normalized
Plugins, canonical contracts and metadata, requirements, selected providers,
exposure, aliases, and digests. It excludes runtime configuration, Secret
values, unrestricted environment state, another Plugin's raw files, writable
source, and final generated paths.

Output is limited to exact generated requirements, structured diagnostics,
typed contributions at `http.ingress`, `invocation.prepare`,
`invocation.complete`, or `http.egress`, and direct application-local Alias
contributions. Use `generation.ParseCapabilityID` and the concrete exported
types in `generation/v1/`; use its tests as executable protocol examples.

Extensions cannot return source text, patch generated files, mutate the model,
choose providers, add priority, or create private dispatchers. They run in a
bounded helper process for crash and timeout containment, not as a security
sandbox. Any change must remain deterministic across time, paths, discovery
order, and platforms.

## Regenerate and diagnose drift

After a manual `plugin.yaml`, `capability.yaml`, selected configuration,
`go.mod`, or generation-package edit, regenerate and check with the same
selection:

```powershell
plystra generate
plystra generate --check
plystra generate --env production
plystra generate --check --env production
plystra generate --config deploy/customer-a.yaml
plystra generate --check --config deploy/customer-a.yaml
```

Clean check output resembles:

```text
generated output is current for example.com/acme/orders in <absolute-path>/orders
```

Check mode is read-only. Drift reports one or more categories:

- `changed`: a managed path differs from deterministic output.
- `missing`: a required managed path is absent.
- `unexpected`: a path exists under managed space but is not owned; the CLI
  preserves it rather than overwriting it.
- `obsolete`: the ownership manifest still records output that should disappear.

Fix the authored input, move handwritten files out of `generated/`, and run
normal generation. Never delete or overwrite an unexpected file blindly.

The CLI protects an enclosing Go workspace boundary. If a valid parent
`go.work` does not list the nearest module, nested Go subprocesses run with
`GOWORK=off`; a malformed workspace remains visible so Go can report the real
error. An explicit `GOWORK` value is preserved.

## Common failures

### No provider or ambiguous provider

Confirm the exact canonical ID is visible and provided by a local Plugin or a
dependency Plystra Project anywhere in the effective Go Module graph. A
markerless Go dependency is intentionally not scanned. If several compatible
Providers remain, run `plystra use <capability-name>/vN <plugin-id>` with the
same `--env` or `--config` selection used to generate the application. Do not
add a priority or fallback.

### Inherited configuration conflict

Read the exact `capabilities.use`, `capabilities.aliases`, or `config` field and
every contributing module named by the diagnostic. Add one explicit decision
for that exact key or declared Plugin field in root `plystra.yaml`, then
regenerate. Changing dependency order, making a module direct, or sorting
Plugin IDs cannot resolve the conflict.

### Wrong configuration selection

If drift or a Provider choice does not match the intended deployment, inspect
the configuration mode, environment, and project-relative document references
in `generated/manifest.json`. Run generation and check with the same `--env` or
`--config`; for automation, set exactly one of `PLYSTRA_ENV` or
`PLYSTRA_CONFIG`. An environment is a sparse overlay above root, while an
explicit file is complete and root `plystra.yaml` is not merged beneath it. A
missing selected overlay or root Project marker is an error.

### Incompatible contract

Compare request, response, semantic errors, behavioral metadata, and normalized
extension metadata. Implement the visible exact contract or create a new
version. Do not weaken equality or add a compatibility decoder.

### Plugin target is ambiguous

Run from inside the target Plugin or pass its directory or exact Plugin ID:

```powershell
plystra capability create order.cancel --plugin checkout
```

### Constructor signature no longer compiles

After adding `requires`, regenerate and accept the Plugin's generated
`dependencies.Dependencies` argument. Do not handwrite registration or bypass
the generated client.

### Generated client is unavailable

Do not call clients during Plugin construction. Ensure the requirement is
selected, all constructors succeed, and bootstrap has published the complete
catalog before the Capability method runs.

### Invalid configuration or Secret

Match the exact selected Plugin ID and its `plugin.yaml` schema. Remove unknown
fields, supply required values, and use an `env` or absolute `file` reference
for Secrets. Generation does not read the referenced value; bootstrap does.

### Unclaimed extension namespace

Make a compatible activation provider and its declared generation package
visible and selected. Do not make the Kernel or application source interpret
the metadata directly.

### Extension helper fails

Read the diagnostic category: compile error, signature mismatch, returned
error, panic, timeout, abnormal exit, oversized output, malformed protocol, or
invalid normalized contribution. Reproduce with the application's actual module
graph; do not depend on absolute paths, full environment state, time, random
data, or filesystem ordering.

### Windows resource exhaustion

Rerun the full Go suite sequentially or with `-p 2`. Do not classify process
exhaustion as a product failure, and do not hide an actual package failure by
reducing coverage.

## Repository workflow

For each smallest coherent feature:

1. Inspect status, recent history, governing documentation, implementation,
   tests, and generated artifacts.
2. Change implementation, tests, generated output, help, examples, and docs
   together.
3. Run targeted checks and the strongest applicable full validation.
4. Confirm no superseded pre-release path remains.
5. Review and stage only that feature.
6. Commit with `type(scope): description` using the most specific subsystem,
   such as `feat(invocation): ...` or `fix(generation): ...`.
7. Push immediately to the configured upstream branch.
8. Resolve configured CI failures before the next feature in the normal
   repository workflow.

Before `v0.0.1`, replace conflicting unreleased APIs directly. Do not retain
compatibility wrappers, deprecated active APIs, migration shims, legacy
configuration readers, old command aliases, transitional abstractions,
fallbacks, or obsolete paths. Regenerate local fixtures as needed.

Generated project `SKILL.md` files deliberately contain no Git workflow rules;
repository process belongs in contributor documentation such as this guide.

## Architectural boundaries that must not be crossed

- Go Modules are distribution and dependency boundaries; `go.work` is optional.
- Plugins are root-level module directories; there is no root `plugins/` tree.
- Capabilities are exact, provider-independent, versioned contracts.
- Ordinary Plugins depend on generated Capability clients, not concrete Plugins.
- The CLI resolves providers and writes all final generated source.
- The Kernel receives one immutable already-resolved registry and never scans,
  selects, or activates Plugins dynamically.
- The Kernel has no User, AuthN, AuthZ, Space, business audit, or application
  identity model.
- No ordinary Plugin provides a Kernel function.
- There is no Registry, marketplace, downloader, `enabled.yaml`, Plystra lock
  file, provider priority, per-Space assembly, remote provider subprocess, or
  cross-language runtime dispatch.
- Secret values never enter build-time extension input or generated surfaces.
- Aliases resolve before Kernel dispatch and never enter the canonical registry.
- External and internal calls share the same generated application invocation
  requirements.

## Intentionally deferred after Gate 9

The following remain roadmap work and must not be represented as complete:

- Gate 10 official AuthN Plugins, verified-state establishment and reuse, and
  automatic single-login-method Alias contribution.
- Gate 11 official AuthZ models, providers, explicit Space/resource bindings,
  and generated authorization decisions.
- Complete CLI dependency-management, development, test, build, check, fix,
  doctor, SDK packaging/publication, and release commands.
- CLI-managed database migration workflows and an official persistence Plugin.
- Final Gate 12 migration closure, full cross-platform acceptance, packaging,
  release metadata, and removal audit.
- Public Kernel and CLI `v0.0.1` releases and independent release verification.

Do not fill a deferred boundary with a placeholder, fake adapter, compatibility
layer, skipped test, or undocumented manual edit to CLI-owned files.
