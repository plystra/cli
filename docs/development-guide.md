# Plystra Development Guide

This guide describes the implementation that exists after the Gate 9 baseline
and the canonical Connect-handler foundation. It is for contributors working
on the Kernel or CLI and for developers building a Plystra Go Module with the
current public CLI.

`core-philosophy/` remains the binding architecture specification. This guide
adds operational detail from the working implementation; it does not replace
that specification.

## Current implementation boundary

Plystra Core is exactly the Kernel plus the CLI:

- `github.com/plystra/kernel` is the intrinsic in-process runtime.
- `github.com/plystra/cli` resolves applications and generates their typed Go,
  HTTP, JavaScript, documentation, assembly, bootstrap, and manifest surfaces.
- `github.com/plystra/authn` and `github.com/plystra/authz` are optional official
  Plugin modules. Their implementation begins at Gates 16 and 23; at this
  boundary they contain architecture documentation, not usable providers.

The public command surface currently implemented by the `plystra` binary is:

```text
plystra help
plystra version
plystra new <project-name> [--module <go-module-path>] [--template <go-module-query>] [options]
plystra add <go-module-query>
plystra remove <go-module-path>
plystra update <go-module-query>
plystra use <interface-id> <constructor-symbol> [--env <environment>|--config <yaml-path>]
plystra plugin create <name>
plystra capability create <capability-name> [--query] [--plugin <plugin>] [--confirm] [--expose]
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
| `internal/projectcheck/` | shared read-only Project drift and Go-test workflow |
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
commands. A failure to download the Kernel version recorded in `cli/go.mod`
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

The template's default application model must resolve without Provider
ambiguity. When several compatible Plugins provide one required Capability,
the template publisher must select one under `capabilities.use` in the
template's root `plystra.yaml` and publish that corrected module version.
Creation otherwise reports every candidate and leaves no target directory for
the consumer to repair.

Template dependencies must not match the effective `GOPRIVATE` setting. The
CLI checks the complete direct and transitive graph, reports every selected
private `path@version`, and rolls back creation. A publisher must publish or
replace a genuinely private dependency before releasing the template. If the
reported module is already public, correct the overbroad Go privacy setting and
retry creation.

Template dependency Projects must not declare relative `replace` directives in
their `go.mod` files. Publish the referenced module version first, replace the
local path with that ordinary requirement, and publish a corrected Project
version. Creation checks the template and every transitive dependency Project,
reports stable `module@version/go.mod` provenance, and leaves no target when a
relative replacement remains.

The staged generated application must also be a fixed point. Creation installs
the generated output and immediately runs the equivalent of
`plystra generate --check`. Dependency-composition drift or any changed,
missing, unexpected, or obsolete generated path rejects the template and
restores the transaction. The publisher must make generation deterministic,
run `plystra generate` followed by `plystra generate --check` in a fresh
Project directory, and publish a corrected module version.

Creation next runs the same read-only workflow as `plystra check`. It verifies
the selected configuration and generated output again, then runs
`go test -mod=readonly ./...` from the staged Project root. A failure restores
the creation transaction; the publisher must make the public check pass in a
fresh Project directory before publishing a corrected version.

The qualification stage then runs `go build -mod=readonly ./...` from the staged
Project root. Every generated and authored Go package must compile without
changing module metadata. The CLI next builds
`./generated/go/application` with `GOWORK=off` into a temporary
`.plystra-smoke` directory, starts the real assembled runtime through its private
`--smoke` path, invokes intrinsic `kernel.health/v1`, and stops lifecycle
providers in generated reverse order. Smoke stdout and stderr are suppressed so
runtime values cannot enter creation diagnostics, and the temporary executable
is removed after success, failure, timeout, or cancellation. Any failure rolls
back the complete target. This is not the later public `plystra build`
executable, `dist/` output, or selector-aware runtime startup contract.

The template's root configuration is also the source of its verified local
operational inputs. Typed values and Secret-reference placeholders declared
there are composed into the new Project's root `plystra.yaml` and validated
against the selected Plugin schemas. Creation never resolves an `env` or
`file` reference, so neither its target nor a value present in the process
environment enters generated source or manifest provenance. The CLI does not
guess a value for an undeclared required field; that omission fails the
creation transaction and the target directory is not installed.

Automation must answer all three choices explicitly:

```powershell
plystra new orders --module example.com/acme/orders --no-git --no-github-ci --skills
plystra new orders --module example.com/acme/orders --template example.com/acme/platform@v1.2.3 --no-git --no-github-ci --skills
plystra new contracts --module example.com/acme/contracts --no-git --no-github-ci --no-skills
plystra new orders --module example.com/acme/orders --plugin catalog --git --github-ci --skills
```

Non-template creation reports the installed module and target:

```text
created example.com/acme/orders in <absolute-path>/orders
```

Template creation instead reports the Project result and immediate next action
without an absolute path or internal resolution detail:

```text
Created orders from example.com/acme/platform@v1.2.3
Configuration scaffolded
Generated, checked, built, and locally verified

Next:
  cd orders
  plystra check
```

The template form proves read-only Go package tests and builds, isolated runtime
startup, intrinsic health, and clean shutdown. When a generated JavaScript SDK
is present, creation also runs `npm install --ignore-scripts --no-audit --no-fund`,
`npm run typecheck`, `npm run build`, and `npm pack --dry-run --json`, then
removes validation-only `node_modules/` and `dist/` output before installation.
The generated package declares pinned Buf and Connect runtime dependencies, so
this checks the real descriptor-backed transport rather than an unused package
entry.
The complete qualified-template acceptance suite still needs public `plystra dev`
and `plystra build` workflows. Do not describe a template as qualified until
that complete automated suite exists.

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
    proto/
      descriptor-set.pb                   self-contained descriptor evidence
      plystra/generated/.../capability.proto
                                          canonical and Alias schemas
      wire-map.json                       committed Protobuf wire history
    docs/
    go/
      adapters/
      application/                         CLI-owned process entrypoint
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

`generated/proto/wire-map.json` is durable compatibility history, not a
disposable cache. For each canonical Capability on the selected Connect
surface, it preserves request and response field numbers across declaration
reordering and allocates new fields without renumbering existing ones. Removing
a field permanently reserves both its generated name and number, and removing
exposure or disabling Connect retains the canonical history as inactive.
Every scalar contract enum receives a numeric zero `*_UNSPECIFIED` sentinel.
Its canonical members receive stable positive numbers: reordering does not
change them, additions use unused positive values without renumbering existing
members, and removals permanently reserve both generated member names and
numbers. If a field stops using an enum, its history remains inactive for later
compatible reactivation. An application Alias reuses the canonical target
messages and enums and therefore has no separate ledger entry. Commit this
CLI-owned file with the rest of generated output, but never edit or delete it.
If generation reports ledger drift, restore the exact last committed copy
before rerunning `plystra generate`.

For every canonical Capability on the selected Connect surface, generation
emits one deterministic
`generated/proto/plystra/generated/.../capability.proto` schema. An Alias emits
a service-only schema that imports and reuses its canonical target messages.
`generated/proto/descriptor-set.pb` is the deterministic self-contained binary
descriptor graph, including required well-known descriptors. When no Connect
surface is selected, the descriptor set remains present as a valid empty set.
The schemas and descriptor set contain no Provider, Plugin, Go Module,
configuration, or Secret data. They are CLI-owned evidence: never edit them,
and use `plystra generate --check` to detect missing or modified files. A
selected Connect surface also emits a Go handler under
`generated/go/adapters/connect/`. Canonical handlers bind one exact procedure
to the generated canonical application-invocation handle; Alias handlers
forward through that canonical handler and never own a Provider or Alias
dispatch entry. At the current transport gate, the canonical contract must
declare `semantics.kind: query` or `semantics.kind: command`; generation
projects either operation as one unary procedure, and every Alias reuses the
same canonical target. Selecting an `event` or `stream` for Connect fails
before output with the exact Capability, typed kind, supported unary kinds,
and instruction to remove the Capability from `http.expose` until that
operation kind is supported. Do not relabel an event or stream to bypass this
validation.
The application supplies one `RootContext` function for each generated
canonical handler. It receives the live external request context plus a cloned
header map and returns the trusted Kernel root. When the returned root
intentionally uses `context.WithoutCancel` or otherwise detaches from the
request, the generated handler reattaches explicit caller cancellation and
derives the earlier caller or trusted-root deadline. Canonical, Alias, HTTP,
and direct paths therefore deliver `context.Canceled` or
`context.DeadlineExceeded` through generated application invocation to the
Provider and return no response when invocation observes either. Providers
remain responsible for their own transaction, compensation, and rollback
behavior; best-effort interruption does not undo work already performed.
Both handlers accept only Connect POST requests encoded as binary
Protobuf or ProtoJSON, require `Connect-Protocol-Version: 1`, and reject gRPC
and gRPC-Web with `415 Unsupported Media Type` before root-context or Provider
invocation. Their `Accept-Post` response advertises only the two supported
Connect media types. Binary Protobuf requests are limited to 1 MiB, decoded
with a maximum message depth of 64, and validated with a 65,536-node budget.
Malformed or truncated wire data, unknown fields at any message depth, and
requests that exceed any bound fail before root-context creation or Provider
invocation. Direct calls to the generated handler apply the same recursive
message validation. Binary Protobuf responses use the same 1 MiB, depth-64,
and 65,536-node bounds. Generated conversion preflights canonical fields,
collections, object graphs, and content bytes before proportional
wire-projection allocation, validates the exact response message, and
deterministically serializes it. Invalid or oversized responses produce the
safe internal response failure without partial bytes on canonical, Alias, and
direct handler paths. ProtoJSON requests independently accept at most 1 MiB,
64 nested JSON containers, and 65,536 structural tokens before strict decoding;
unknown or duplicate fields, malformed or trailing documents, invalid UTF-8,
excessive work, the enum zero sentinel, and non-finite canonical numbers fail before root
creation or Provider invocation. Required `null` fails requiredness, optional
non-nullable `null` becomes absence, explicit zero values remain present, and
full-range integers remain exact. ProtoJSON responses run the same exact
message and canonical response validation, then enforce their own 1 MiB
serialized limit without writing a partial response. Canonical and Alias
binary and ProtoJSON calls therefore reach the same canonical request and
response model.
Generation installs direct `connectrpc.com/connect` and
`google.golang.org/protobuf` requirements at the supported versions inside the
existing module transaction. The generated application entrypoint still does
not mount an HTTP server; server mounting and the remaining protocol
projections are later transport work.

Protobuf-derived names must also be unique within each request and response.
For example, `foo1` and `foo_1` both become the ProtoJSON name `foo1`, while
enum fields `http_status` and `h_t_t_p_status` both become a generated
`HTTPStatusEnum` type. Generation reports the Capability, request or response,
both canonical field names, and the conflicting generated identity before it
changes generated output. Rename one authored `capability.yaml` field and
regenerate; request and response names are checked independently.

`.agents/skills/plystra/` is a creation-time project guide that the project may
maintain as its authored workflows evolve. It is outside `generated/` and is not
part of `plystra generate --check` ownership. The generated guide begins with
the currently supported template-create-and-check path and an ordinary
business-development sequence using only Go Module, Plugin, Capability, and
`plystra.yaml`. It teaches one selected environment on that path, marks complete
`--config` replacement as advanced, and puts resolver, generation,
wire-history, and maintainer mechanics behind a detailed-reference boundary.
It does not advertise a template as qualified while the complete qualification
suite remains unfinished.

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
`connect` and `rest` fields. A new Project records the default selection
explicitly in root `plystra.yaml`:

```yaml
http:
  transports:
    connect: true
    rest: false
```

When omitted from another selected document, Connect still defaults to enabled
and REST to disabled. In an environment overlay the two fields replace
independently: an omitted field inherits the root choice, while `null` restores
that field's schema default. A full-replacement file does not inherit root
transport choices; omitted transport fields use the same defaults. Dependency
Project transport settings never participate in composition.

The selected transport values are build-affecting and participate in the
generated application-model digest. A nonempty `http.expose` set requires at
least one enabled transport. JavaScript SDK generation requires Connect, so a
selected default, environment, or full-replacement model with JavaScript
Capability or Alias surfaces and `connect: false` fails before output. The
diagnostic identifies the selected configuration and every affected surface;
enable `connect: true` in that current-Project selection or remove those
surfaces. Connect handlers are generated for selected surfaces, while server
mounting and the optional REST projection remain in later transport gates;
setting `rest: true` does not yet create a REST adapter.

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

The normalized selected CORS policy is build-affecting and participates in the
generated application-model digest. Changing its origins or credential choice
therefore produces deterministic generation drift, while reordered,
deduplicated equivalent origins retain one static model identity. Generated
Connect handlers enforce that policy before protocol dispatch. Each request
origin must be one canonical normalized HTTP/HTTPS origin serialization of at
most 4096 bytes. Literal `null` is accepted only through a noncredentialed
wildcard policy; `Origin: *` is never valid request input. A valid preflight
must request `POST` and may request only `Authorization`,
`Connect-Protocol-Version`, `Connect-Timeout-Ms`, or `Content-Type`. Those
case-insensitive names must be unique across at most four
`Access-Control-Request-Headers` field values totaling at most 4096 bytes;
success returns `204` with deterministic allow headers. Allowed actual requests
receive `Access-Control-Allow-Origin` on successful and safe error responses,
plus `Access-Control-Allow-Credentials: true` only when configured. Malformed,
noncanonical, duplicate, over-bound, or disallowed origins, methods, and
requested headers return `403` before trusted-root creation or Implementation
invocation. Exact-origin responses and preflights emit the required `Vary`
fields. A noncredentialed wildcard emits `*`. Without `http.cors`, handlers
emit no CORS response headers and do not accept a cross-origin preflight.
Server mounting and the optional REST projection remain later transport work.

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

`generated/manifest.json` records a versioned canonical constraint projection
with every resolved canonical Capability ID, its exact contract and constraint
digests, and each constrained request or response field's path, type, and
normalized constraint object. Unconstrained Capabilities retain empty field
lists, while the aggregate projection digest changes for added, removed, or
changed constraints. Configuration schema v4 records `default`,
`environment`, or `explicit-config` mode; the environment name and overlay
reference when applicable; project-relative paths; normalized document
digests; dependency baseline history; the committed Protobuf wire-map digest;
and the final build-affecting application-model digest. Environment mode reuses
the root dependency baseline because overlays do not own dependency
maintenance. The required top-level `transport_toolchain` record identifies
the exact embedded `go/format` runtime; built-in Protobuf-model, descriptor,
wire-map, Connect, and JavaScript generator versions; pinned generated Go and
npm dependencies; and its canonical SHA-256 digest. The CLI does not invoke an
implicit global `protoc` or generator and does not use a hosted generation
service. A CLI or supported-toolchain change therefore changes the generated
manifest and `plystra generate --check` reports drift without modifying the
Project. The manifest excludes raw configuration, Secret reference targets,
resolved Secrets, environment selectors outside their declared provenance,
VCS state, timestamps, and machine-specific absolute paths. Use the same
selection for generation and its check; selecting another build-affecting
model correctly reports generated drift.

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

Create and expose a first Query Capability version in one transaction:

```powershell
plystra capability create catalog.item.get --query --plugin catalog --expose
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
    constraints:
      min_length: 1
      max_length: 128
      pattern: '^[a-z0-9][a-z0-9_-]{0,127}$'

response:
  item_id: {type: string, required: true}
  name: {type: string, required: true}
  price_cents: {type: integer, required: true}

errors:
  - invalid_item_id
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
```

`--query` writes that complete semantic profile into the authoritative
`capability.yaml`; the Capability name never implies behavior. A genuinely new
identity requires one supported intent profile before the command mutates the
Project.

Field constraints use one closed type-specific vocabulary: strings accept
`min_length`, `max_length`, and bounded Go regular-expression `pattern`;
integers and numbers accept `minimum` and `maximum`; arrays accept `min_items`
and `max_items`. Contract loading rejects unknown or type-incompatible keys,
reversed or excessive bounds, invalid expressions, non-finite numbers, and
normalized numeric values that would lose their declared value. Constraints
participate in exact equality and the contract digest, so every Provider copy
must match the canonical declaration. Generated Go application invocation now
enforces every closed constraint before contributions and Provider dispatch and
validates the Provider response after completion contributions. String bounds
count Unicode scalar values rather than bytes or grapheme clusters. Generated
Connect and optional REST adapters run the same request validator before
trusted-root creation. Generated JavaScript request and response declarations
retain the exact normalized constraint object in a `@plystraConstraints`
annotation. Browser request preflight and decoded-response validation enforce
Unicode scalar-value length, numeric bounds, and array item counts. Canonical
`pattern` remains declared and server-authoritative because JavaScript
`RegExp` is not a compatible substitute for bounded Go regular-expression
semantics.

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

An omitted version creates `v1` for a new identity. When an identity is already
visible, the same unversioned command creates the next version by copying the
highest exact contract, including its semantics; omit profile flags for that
later-version workflow. An unusual explicit new identity and version requires
both an intent profile and deliberate confirmation:

```powershell
plystra capability create catalog.item.search/v3 --query --plugin catalog --confirm
```

Implement an exact visible dependency or official contract instead of creating
a similar private one:

```powershell
plystra capability implement email.send/v1 --plugin mailer
```

Before a contract appears in any published tag, rewrite it directly when needed
for the clean initial API and regenerate every affected fixture. A published
`v0.0.1-rc.N` tag and its artifacts are immutable, but a newer RC may revise the
same exact `/vN` after recording compatibility differences and re-pinning,
regenerating, rebuilding, and revalidating every affected downstream Project.
Never move or reuse a published tag, and do not add a compatibility wrapper
solely for an obsolete RC. After stable `v0.0.1`, an incompatible exact contract
change requires a new `/vN`.

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

If several compatible Implementations satisfy the same required Interface,
generation fails until the selected current-Project configuration chooses one
fully qualified constructor:

```yaml
interfaces:
  require:
    - email.send/v1
  use:
    email.send/v1: example.com/acme/email/smtp.New
```

Use the targeted command for the same explicit current-Project decision:

```powershell
plystra use email.send/v1 example.com/acme/email/smtp.New
plystra use email.send/v1 example.com/acme/email/production.New --env production
plystra use email.send/v1 example.com/acme/email/customer.New --config deploy/customer-a.yaml
```

The default form writes root `plystra.yaml`; `--env` writes only the selected
sparse project-root overlay; and `--config` writes only the selected complete
replacement document. `PLYSTRA_ENV` and `PLYSTRA_CONFIG` provide the same
selection when no flag is present, while an explicit flag overrides both
variables. The command may start inside a nested package, preserves comments and
unrelated values, regenerates and validates with the same selection, and
restores the selected YAML, generated output, `go.mod`, and `go.sum` if any
later step fails. It rejects intrinsic Interfaces, unknown Interfaces, unknown
constructors, and constructors that do not implement the exact canonical
Interface.

There is no constructor priority, discovery-order winner, or runtime selection
fallback.

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

The CLI generates both strict JSON HTTP handlers and Connect handlers, but the
current CLI-owned `generated/go/application/main_gen.go` entrypoint does not
yet mount an HTTP server. It delegates default root `plystra.yaml` selection to
`generated/go/bootstrap`, then owns signal-driven shutdown and the private
template-qualification health smoke. Do not edit either generated boundary or
add a competing application startup workaround.

Start the generated application from the Project root with the same environment
selection used for generation:

```powershell
go run ./generated/go/application
go run ./generated/go/application --env production
go run ./generated/go/application --config deploy/customer-a.yaml
```

The first command loads only root `plystra.yaml`. The second requires
`plystra.production.yaml` and applies it as one typed sparse overlay above the
root document. The third requires root `plystra.yaml` only as the regular
Project marker, then loads and normalizes the selected complete document
without parsing or merging root configuration. `PLYSTRA_ENV=production` and
`PLYSTRA_CONFIG=deploy/customer-a.yaml` are the ambient equivalents when no
explicit selector is present. An explicit selector overrides both variables;
the environment and replacement modes cannot be combined. Relative and
supported absolute replacement paths must identify an existing nonsymbolic
regular file inside the runtime Project directory. Unsafe selectors, missing
files, unknown fields, invalid typed values, and YAML anchors or aliases fail
before Provider construction; unselected overlays and replacement files are
not read. Generate, check, and start with the same selector. Compiled selection
provenance and the versioned application-model compatibility projection are
visible in `generated/go/bootstrap/bootstrap_gen.go` as canonical non-secret
JSON plus digests. The projection covers selected transports, CORS, public
exposure, requirements, explicit Provider choices, and Alias declarations and
is cryptographically associated with the complete generated application-model
digest. Startup derives the same projection from the normalized selected
runtime document and rejects a mismatch with rebuild guidance before reading
startup settings, creating a Secret resolver, or constructing a Provider.
Runtime-only address, timeout, Plugin configuration, and Secret-reference
changes do not enter the projection. Neither compiled record contains YAML
values, Secret-reference targets, resolved Secrets, or machine paths.

Validate the generated Connect handler directly with `httptest` until server
mounting lands in the later transport gate. Exercise both binary Protobuf and
ProtoJSON Connect clients. Requests using gRPC or gRPC-Web media types must
receive `415 Unsupported Media Type` and must not enter root-context creation
or Provider invocation. For binary Protobuf, include malformed and truncated
wire data, nested unknown fields, the enum zero sentinel, more than 64 nested
messages, more than 65,536 decoded validation nodes, and a request over 1 MiB.
Each rejection must occur before root-context creation or Provider invocation.
For binary responses, include deterministic repeat encoding, wrong message
types, unknown nested fields, cyclic object input, output over 1 MiB, more than
64 nested messages, more than 65,536 encoded nodes, canonical and Alias HTTP
paths, and direct handler invocation. A rejection must return no partial
response. For ProtoJSON, include malformed and trailing documents, invalid
UTF-8, top-level and nested unknown fields, duplicate fields, required and optional `null`,
explicit zero values, full-range integers, enum sentinels, non-finite values,
more than 64 nested containers, more than 65,536 structural tokens, request and
response payloads over 1 MiB, and canonical/Alias parity. A ProtoJSON rejection
must likewise return no partial canonical response and must not enter the
Provider for invalid input.

The handler's root-context function is a trusted adapter boundary. Do not
convert raw headers into verified internal AuthN state there. Official AuthN
behavior is deferred until Gate 10.

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
  src/descriptors.ts
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
  credentialPolicy: {
    mode: "bearer",
    getAccessToken: async () => rawAccessToken,
  },
});

const item = await client.catalog.item.get.v1({item_id: "coffee"});
```

Canonical `integer` fields are signed 64-bit values and use JavaScript
`bigint` in generated requests, responses, arrays, and enum literals. Write
values such as `42n`; passing a JavaScript `number` is rejected even when it is
currently within the safe-number range, so the same API remains exact across
the complete contract range.

`credentialPolicy` is required. Use `{mode: "anonymous"}` for no browser
credentials, `{mode: "cookie", fetchCredentials: "same-origin"}` or
`{mode: "cookie", fetchCredentials: "include"}` for the exact cookie policy,
or the bearer policy above. Anonymous and bearer modes set Fetch credentials to
`omit`; cookie mode sends no bearer header. `getAccessToken` returns one raw
token only. The generated transport adds the `Bearer` authorization scheme.
Rejected, nullish, empty, malformed, already-prefixed, control-containing,
non-string, or larger-than-64-KiB results fail before dispatch as
`PlystraError` code `credential_error` without exposing the credential. A mode
never falls back to another mode.

The SDK validates Plystra request and response values, resolves the exact unary
method from the generated self-contained Protobuf descriptor graph, and sends
binary Connect requests. Its generated `package.json` pins
`@bufbuild/protobuf`, `@connectrpc/connect`, and `@connectrpc/connect-web` as
direct runtime dependencies. Callers use only the Plystra wrapper: do not
export raw descriptors, Protobuf message objects, Connect clients, or
`ConnectError` as application contracts. Network, credential, cancellation,
malformed-response, and schema failures are normalized to stable Plystra error
fields. Provider packages, runtime configuration, verified internal context,
and Secrets must not appear in the package.

Pass an `AbortSignal` as the operation's second argument for caller-controlled
cancellation:

```ts
const controller = new AbortController();
const pending = client.catalog.item.get.v1(
  {item_id: "coffee"},
  {signal: controller.signal},
);
controller.abort();
await pending; // rejects with PlystraError code "cancelled"
```

The same signal cancels pending bearer-token acquisition and a request already
in `fetch`; once server invocation has started, the generated Connect boundary
propagates that cancellation to the canonical invocation and Implementation
context. Treat it as interruption, not as an Implementation rollback
guarantee.

Generated Connect application failures carry one closed
`plystra.generated.transport.v1.PlystraErrorDetail`. The detail identifies the
requested canonical or Alias Capability, its canonical target, and exactly one
declared semantic error code or closed Kernel error class. It never contains a
Provider ID or message, cause, payload, panic value, stack, internal Kernel
detail code, configuration, credential, or Secret. Alias calls therefore keep
the Alias in `requestedCapabilityID` while `canonicalCapabilityID` remains the
target. The generated JavaScript wrapper exposes the validated result as an
immutable Plystra-owned detail rather than a raw `ConnectError`:

```ts
import { PlystraError } from "@acme/orders-sdk";

try {
  await client.catalog.item.get.v1({item_id: "coffee"});
} catch (error) {
  if (
    error instanceof PlystraError &&
    error.detail?.semanticErrorCode === "item_missing"
  ) {
    // Handle only the error declared by catalog.item.get/v1.
  }
}
```

Do not parse `error.message` or depend on Connect internals. A missing,
duplicate, malformed, unknown, identity-mismatched, outer-code-mismatched, or
undeclared detail is deliberately reduced to the generic `internal` Plystra
error. When testing a generated handler, assert the exact requested and
canonical IDs, the semantic-code-or-Kernel-class exclusivity, and absence of
unsafe Provider text for both canonical and Alias procedures.

Import the SDK only through its generated package root. Its export map blocks
runtime, descriptor, and generated-operation implementation subpaths, and its
declarations omit transport, codec, descriptor, and binder internals even
though those modules remain packaged for the wrapper's own execution.

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
exposure, aliases, and digests. Filesystem-backed contexts additionally expose
the selected configuration's non-secret identity:

```go
func selectedEnvironment(context plystragen.GenerationContext) (string, bool) {
    provenance, ok := context.ConfigurationProvenance()
    if !ok || provenance.Mode() != plystragen.ConfigurationModeEnvironment {
        return "", false
    }
    return provenance.Environment(), true
}
```

The view provides `RootPath`, `RootDigest`, and
`DependencyCompositionDigest` alongside the selected-document accessors.
Paths are stable Project-relative slash paths. Digests are normalized
lowercase SHA-256 identities. No accessor returns YAML content, runtime
configuration, Secret values, absolute paths, unrestricted environment state,
another Plugin's raw files, writable source, or final generated paths.
Synthetic unit-test contexts may omit the view.

Use `context.Digest()` when caching or comparing the complete extension input;
it includes configuration provenance and is verified across the helper-process
round trip. `context.BuildModelDigest()` excludes document provenance. The CLI
uses that second identity for static assembly so runtime-only YAML changes do
not force a different compiled model. If an extension deliberately changes its
normalized output from a provenance digest, the extension output digest still
changes the final application model.

Application generation also converts that same bounded identity into one
internal transport-provenance value. It must agree with the selected
configuration record in `generated/manifest.json`, the typed dependency
composition digest, and the final build-affecting application-model digest
before bootstrap, Connect, REST/JSON, JavaScript, or API-document rendering can
start. Bootstrap embeds the exact canonical non-secret provenance JSON plus its
digest in `compiledConfigurationSelectionProvenanceJSON` and
`compiledConfigurationSelectionProvenanceDigest`; do not edit those generated
constants. Transport renderers receive no YAML values or Secret targets and do
not embed selector-only paths or document digests in their source. Changing
from the default file to an environment overlay or full replacement therefore
changes manifest and bootstrap provenance, while equal effective build models
retain byte-identical transport output.

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
plystra inspect
plystra inspect --env production
plystra inspect --config deploy/customer-a.yaml
plystra explain capability email.send/v1
plystra explain capability email.send/v1 --env production
plystra explain capability email.send/v1 --config deploy/customer-a.yaml
plystra explain plugin acme.email.smtp
plystra explain plugin acme.email.smtp --env production
plystra explain plugin acme.email.smtp --config deploy/customer-a.yaml
plystra explain config config.acme.email.smtp.host
plystra explain config config.acme.email.smtp.host --env production
plystra explain config config.acme.email.smtp.host --config deploy/customer-a.yaml
plystra explain alias mail.send/v1
plystra explain alias mail.send/v1 --env production
plystra explain alias mail.send/v1 --config deploy/customer-a.yaml
plystra explain exposure email.send/v1
plystra explain exposure mail.send/v1 --env production
plystra explain exposure mail.send/v1 --config deploy/customer-a.yaml
plystra generate
plystra generate --check
plystra generate --env production
plystra generate --check --env production
plystra generate --config deploy/customer-a.yaml
plystra generate --check --config deploy/customer-a.yaml
```

`plystra inspect` first resolves that exact selected model without modifying the
Project. The default view stays concise: Project and configuration identity,
Plugin and Capability counts, AuthN/AuthZ activation, transports, readiness,
and the selector-matched `plystra check` action. Use `--verbose` for complete
indented resolution evidence or `--format json` for one deterministic
`plystra.inspect` v1 document on stdout; JSON progress and diagnostics use
stderr. The JSON form is suitable for automation and contains stable
module-relative provenance rather than unrestricted configuration, Secrets, or
machine-specific Project paths.

Use `plystra explain capability <capability-name>/vN` when a particular
Capability's selection is unexpected. A required Capability reports the
selected Plugin Provider or Kernel intrinsic, the direct selection reason and
source, and either a concrete `plystra use` command for an available alternative
or the selected configuration field that owns the decision. A visible but
unrequired Capability points to the selected document's
`capabilities.require["<capability-name>/vN"]` field. Keep the same `--env` or
`--config` selector throughout diagnosis, generation, checking, testing, and
startup. Add `--verbose` for the complete candidate, rejection, requirement,
generation, configuration, and assembly evidence, or `--format json` for one
deterministic `plystra.explain` v1 document. The command is read-only and emits
neither Secret values nor machine-specific Project paths.

Use `plystra explain plugin <plugin-id>` when a Plugin's inclusion is
unexpected. A current-Project Plugin is selected by its root-level declaration.
A selected dependency Plugin lists every exact Capability for which it is the
chosen Provider and the direct Provider-decision sources. A visible unselected
dependency Plugin distinguishes an alternate Provider winning from none of its
provided Capabilities being required. The concise result identifies one
selector-matched `plystra use` command or selected-configuration field that
changes the decision. Keep the same selector for follow-up generation and
validation; `--verbose` and `--format json` use the same complete redacted
evidence boundary as Capability explanations.

Use `plystra explain config <field-path>` when the owner or selected source of a
typed configuration decision is unexpected. Plugin fields use the dotted form
`config.<plugin-id>.<field>`; the result reports the canonical typed path, the
effective dependency, root, environment, or full-replacement owner, every
winning source, and the exact selected current-Project document and field to
edit. Explicit removals and descendants suppressed by an ancestor removal are
reported as decisions rather than being mistaken for missing fields. Values,
Secret-reference targets, and resolved Secrets remain outside concise, verbose,
and JSON output. Keep the same selector for the follow-up edit, generation, and
validation.

Use `plystra explain alias <alias-name>/vN` when an application-local Alias has
an unexpected target, exposure, or origin. The concise result names the direct
canonical target, distinguishes inherited target exposure from an explicit
narrowing, lists every compatible application and generation-extension source,
and identifies the selected configuration field or activation-Provider decision
that changes the result. Keep the same selector for the follow-up edit,
generation, and validation. Use `--verbose` or `--format json` to inspect the
target contract digest, generation contribution identity, activation Capability,
and complete redacted resolution evidence.

Use `plystra explain exposure <capability-or-alias-name>/vN` when a generated
HTTP or JavaScript surface is unexpected or absent. A public canonical
Capability reports every effective `http.expose` declaration; a public Alias
reports its direct target and compatible application or generation sources. An
internal canonical Capability points to the selected `http.expose` field. An
internal Alias distinguishes an explicit Alias narrowing from a target that is
not public and points to the selected Alias, activation Provider, or target
exposure field that controls the result. Keep the same selector for the edit,
generation, check, and startup workflows.

Clean check output resembles:

```text
generated output is current for example.com/acme/orders in <absolute-path>/orders
```

Run the initial Project-wide read-only check with the same selection:

```powershell
plystra check
plystra check --env production
plystra check --config deploy/customer-a.yaml
```

It verifies dependency-composition and generated-output currency before
running `go test -mod=readonly ./...` from the Project root. The check never
repairs YAML, generated output, or module metadata. Transport, JavaScript SDK,
formatting, race, and release-era validation remain deferred to their later
roadmap gates.

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

A common typed failure is rendered as one concise problem followed by exactly
one primary action:

```text
<problem>

Recovery:
<one command or file edit>

Diagnostic: PLYSTRA-<STABLE-CODE>
```

Run that action with the same selected application model. Recovery commands
retain default, environment, or complete-replacement mode, including selectors
supplied through `PLYSTRA_ENV` or `PLYSTRA_CONFIG`. Unsafe or absolute selector
input is replaced by `<environment>` or `<yaml-path>`; provide the intended safe
Project-relative selection when rerunning. Treat the uppercase `PLYSTRA-*` code
as the stable automation and support identity; the concise problem and recovery
wording may improve without changing that identity. The CLI does not invent
advice or a code for an unclassified internal error.

### No Implementation or ambiguous Implementation

Confirm the exact Interface is visible and that a local or dependency package
contains a compatible `//plystra:implements` constructor in a Plystra Project
from the effective Go Module graph. A markerless Go dependency is intentionally
not broadly scanned. If several compatible Implementations remain, run
`plystra use <interface-id> <constructor-symbol>` with the same `--env` or
`--config` selection used to generate the application. Do not add a priority or
fallback.

### Inherited configuration conflict

Read the exact `capabilities.use`, `capabilities.aliases`, or `config` field and
every contributing module named by the diagnostic. Add one explicit decision
for that exact key or declared Plugin field in root `plystra.yaml`, then
regenerate. Changing dependency order, making a module direct, or sorting
Plugin IDs cannot resolve the conflict.

### Wrong configuration selection

If drift or a Provider choice does not match the intended deployment, inspect
the active model with `plystra inspect` and the intended `--env` or `--config`.
Use `--verbose` or `--format json` when complete resolution provenance is
needed, then run generation and check with the same selector; for automation,
set exactly one of `PLYSTRA_ENV` or
`PLYSTRA_CONFIG`. An environment is a sparse overlay above root, while an
explicit file is complete and root `plystra.yaml` is not merged beneath it. A
missing selected overlay or root Project marker is an error.

### Runtime configuration requires a different compiled model

The generated binary rejects a selected document that changes HTTP transports,
CORS, public exposure, Capability requirements, an explicit Provider choice,
or an Alias declaration from the model used to build it. Regenerate and rebuild
with the same `--env` or `--config` selector, then start the replacement binary
with that selector. A change limited to `http.address`, `timeouts.startup`,
ordinary Plugin configuration, or Secret references does not require a new
static model, but it must still pass typed runtime validation.

### Incompatible contract

Compare request, response, closed field constraints, semantic errors, typed
semantics, and normalized extension metadata. Implement the visible exact
contract or create a new version. Do not weaken equality or add a compatibility
decoder.

### Protobuf wire-history drift

Do not repair `generated/proto/wire-map.json` by hand or delete it to force new
numbers. Restore the exact last committed file, then regenerate from the
authored Capability contracts. A missing ownership baseline, changed digest,
noncanonical JSON, reused removed field or enum-member name or number,
inconsistent message or enum identity, or invalid zero sentinel is a
compatibility error that generation intentionally refuses to guess through.

### Protobuf naming collision

If generation reports that two canonical request or response fields produce
the same ProtoJSON name or generated enum identity, rename one field in the
authored `capability.yaml`. Do not patch the generated wire map or generated Go
types. The diagnostic is lexical and stable, and both ordinary generation and
`plystra generate --check` leave the Project unchanged on this failure.

### Plugin target is ambiguous

Run from inside the target Plugin or pass its directory or exact Plugin ID:

```powershell
plystra capability create order.cancel --query --plugin checkout
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
4. Confirm no superseded active path remains.
5. Review and stage only that feature.
6. Commit with `type(scope): description` using the most specific subsystem,
   such as `feat(invocation): ...` or `fix(generation): ...`.
7. Push immediately to the configured upstream branch.
8. Resolve configured CI failures before the next feature in the normal
   repository workflow.

Apply the contract lifecycle above: untagged development may replace a
conflicting API directly; a published RC remains immutable while a newer RC may
replace its contract only with complete downstream revalidation; a stable exact
contract changes only through a new `/vN`. Do not retain compatibility wrappers,
deprecated active APIs, migration shims, legacy configuration readers, old
command aliases, transitional abstractions, fallbacks, or obsolete paths solely
for an untagged snapshot or obsolete RC. Regenerate local fixtures as needed.

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

## Intentionally deferred after the current Connect-handler foundation

The following remain roadmap work and must not be represented as complete:

- Gate 15 Core foundation closure and the real Kernel and CLI prerelease pair.
- Gates 16 through 22 official AuthN implementation, complete product
  acceptance, and the accepted AuthN prerelease.
- Gates 23 through 25 official AuthZ implementation, layered acceptance, the
  accepted AuthZ prerelease, and three-layer development-Goal closure.
- Complete CLI dependency-management, development, test, build, check, fix,
  doctor, SDK packaging/publication, and release commands.
- Generated documentation, compatibility, Behavioral Conformance, manifest,
  and release-evidence constraint projection.
- CLI-managed database migration workflows and an official persistence Plugin.
- Gate 26 final cross-platform acceptance, packaging, release metadata,
  public-source readiness, and coordinated stable Kernel, CLI, AuthN, and AuthZ
  `v0.0.1` publication.

Do not fill a deferred boundary with a placeholder, fake adapter, compatibility
layer, skipped test, or undocumented manual edit to CLI-owned files.
