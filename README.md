# Plystra CLI

`github.com/plystra/cli` builds the user-installed `plystra` command. It is the developer, resolution, generation, assembly, validation, build, test, and delivery half of Plystra Core.

The CLI is a separate Go Module from `github.com/plystra/kernel`. It completes build-time work and emits deterministic Go and JavaScript source targeting the Kernel's versioned assembly API; it is not a second runtime.

## License

Plystra CLI is licensed under the [Apache License, Version 2.0](LICENSE).

Using the CLI does not require your application to be open source. Any CLI template or runtime code included in generated output remains subject to Apache-2.0, including applicable license and notice requirements when redistributed. Third-party dependencies and material retain their own licenses.

## Ownership

The CLI owns:

- Go Module, Plugin, and Capability creation.
- Root-level Plugin scanning, effective-graph Project discovery, and typed current-Project configuration composition.
- The complete normalized application model.
- Official and intrinsic Capability discovery.
- Exact requirement closure and ordinary provider resolution.
- Plugin-provided build-time rule discovery and validation.
- Generation-derived Capability requirements.
- Deterministic structured contribution validation and merging.
- Contracts, clients, providers, configuration, application invocation, adapters, assembly, bootstrap, JavaScript SDKs, documentation, and manifests.
- Transactional mutation, generated consistency, development, testing, building, and release preparation.

The CLI is the sole writer of final `generated/` source.

## Interface field presence

An ordinary Interface message field `T` has no business-observable presence
state: omission and the Go zero value normalize identically. A direct `*T`
distinguishes absent from a present value, including zero or empty, and a direct
`**T` adds explicit null. Pointers are accepted only as one or two direct layers
at a message-field boundary; they are invalid inside repeated elements, map
keys or values, or another supported type, and a pointer-to-message edge cannot
create a recursive cycle.

A `required` ordinary field is meaningful only at representations that retain
occurrence, such as validated examples or transport input. A required `*T`
rejects absence. A required `**T` also rejects absence but still accepts explicit
null because requiredness checks the outer pointer state. The contract model
applies constraints through pointer layers to a present, non-null innermost
value and treats nil and allocated-empty bytes, repeated values, and maps as the
same canonical empty value without changing a pointer field's outer absent,
null, or present state.

Current support implements that model for Interface declaration parsing,
`interface.yaml` constraint and example validation, and compatibility
classification. Generated governed proxies check represented pointer requiredness
before field constraints, normalize empty collections, and deep-copy requests
into isolated call snapshots. Each Implementation adapter gives its target a
fresh independent copy; successful responses are validated and copied into
caller-owned storage. The generated proxy package exports `CopyRequest` and
`CopyResponse` for applying the same checks in direct Implementation tests.
Traversal fails closed beyond 64 levels or 65,536 nodes. A generated `ValueError`
reports the Interface, side, field path, and rule, never the submitted value or
map key; map entries use indices in canonical-key lexical order. Invalid requests
never enter the target, and invalid responses return an internal contract error
without a result. Restored request and response `ValueError` details retain the returned
Kernel boundary through `Unwrap`, without depending on its pointer identity or
replacing cancellation and unknown completion. Ordinary required values may
still be zero. Pointer-bearing
Connect exposure remains unsupported until transport projections preserve those
states. Caller cancellation returns independently with result_unknown after
target entry; late results are discarded. Response validation and copying
remain inside the tracked attempt. Generated InterfaceRuntime.Drain closes
admission and waits for actual termination; Stop drains before any lifecycle
cleanup. Drain and cleanup share the construction/startup cleanup timeout and
any earlier caller deadline. Failed drain keeps dependencies live for a fresh
bounded Stop retry. Application Stop initiates both static Interface and
transitional legacy Capability drains before waiting, and cleans neither
lifecycle set unless both drains succeed.

Construction publishes catalogs without accepting public work. Application
Start opens both dispatchers only after all dependency-ordered hooks succeed.
Hooks use their bounded context for governed calls to already-ready injected
dependencies, including during Stop after public admission closes. Never retain
a hook context for later work. Standalone InterfaceRuntime owners must call
Start and then OpenAdmission explicitly; Application owners use Start alone.

Generated application `Start` and `Stop` share one transition guard across
both lifecycle managers, including rollback and bounded drain. Overlapping or
reentrant calls fail immediately with `lifecycle.ErrState` without changing
either manager or closing admission. Copies of an application share the guard;
independent applications do not. A later call can retry after the active
transition returns. This guards application transitions, not direct lifecycle
operations on the lower-level `InterfaceRuntime` returned by `Interfaces()`.

Generated static calls emit separate Kernel OpenTelemetry histograms for caller
completion and entered-target termination: `plystra.invocation.caller.duration`
and `plystra.invocation.target.duration`, in seconds. Counts distinguish logical
calls from retry attempts; late targets remain tracked through response
processing and never rewrite the caller outcome. Labels contain only the exact
binding, safe outcome/code, completion classification, and target lateness.
Request values, opaque context data, arbitrary errors, and dynamic identities
never become labels. Configure the standard global MeterProvider before
bootstrap and shut it down only after successful application drain. Without an
installed SDK, metrics are no-ops; generated code configures no exporter or
extra telemetry queue. Nested calls remain independent, and Connect adds no
invocation or retry telemetry layer. Governed spans and broader telemetry remain
incomplete.

Each exact binding admits 64 target attempts with no queue. Capacity remains
held through actual target termination and response copying, including after
caller cancellation or timeout. Saturation returns `resource_exhausted` with
`not_started`; Connect and transitional HTTP use status 429, and JavaScript
preserves both facts without automatic retries. Generated assembly, the frozen
model, and Interface provenance v3 record the limit. Installed capability JSON
reports `defaults.invocation_concurrency` (`default_limit: 64`, `queue: 0`) and
`limits.invocation_concurrency_limit` (65,536). Authored concurrency, queue,
and circuit forms remain unsupported. Complete compiled defaults and timeout
and replay-safe retry policies include literal schema, compiler, and defaults
identities.
The default timeout is zero (no added deadline), retries are disabled with one
attempt, and circuits are disabled. Installed capability facts include those
identities and positive timeout bounds of 1ns through 2562047h47m16.854775807s,
with at most 64 bytes per authored duration. An authored retry requires
`eligibility: replay_safe` and `timeout`; `max_attempts` counts the first attempt
and permits 2 through 16 (default 2), while `backoff` defaults to 0s and permits
0s through 2562047h47m16.854775807s. The outermost retry-enabled binding owns
replay, with fresh request copies and no overlapping attempts.

`generated/compatibility/interfaces.json` is the committed, CLI-owned
replaceable shape working record for every visible authored Interface, whether
or not it is selected or exposed. It records each Interface package and method,
request and response names, reachable messages, stable field numbers, Go and
JSON names, requiredness, direct pointer depth, and canonical Go types. It
deliberately excludes
Interface metadata, generated projections, Implementations, configuration,
Secrets, source paths, and module versions. `plystra generate` refreshes this
working record transactionally and
`plystra generate --check` reports its drift without mutation. Never edit it
manually.

`generated/compatibility/interface-metadata.json` is the matching committed,
CLI-owned compatibility-class working record for every visible authored
Interface. Schema `plystra.interface-metadata-baseline/v2` stores the normalized
exact-contract, `contract_supplement_digest`, documentation, and example
digests, never the metadata values themselves. The supplement covers semantics,
semantic-error codes, constraints, and Behavioral Conformance independently of
Go shape. Generation migrates an owned canonical v1 record to v2 in the same
transaction as its ownership manifest and restores both on rollback;
`generate --check` reports the migration as stale without mutation.

Changing an existing field among `T`, `*T`, and `**T`, changing its innermost
type, or otherwise adding or removing an observable state is a breaking
compatibility classification. Pre-stable development may refresh the
replaceable working records in place; once an accepted stable baseline applies,
the change requires a new Interface version. A newly numbered non-required
pointer field is retained as an additive candidate only when the shape working
record and v2 supplement prove that the edit is shape-only. The stable-release
assessment still reports a version requirement until every applicable public
projection and immutable accepted ancestor can also classify it as optional.

`generated/compatibility/interface-transport.json` is the committed,
CLI-owned transport working record for each Interface on the selected Connect
surface. It stores only separate digests for the exact Protobuf descriptor,
Connect procedure, and active wire-map projection. The descriptor digest also
covers the shared safe-error descriptor. `plystra generate` refreshes this
record in the same transaction as its source projections, and
`plystra generate --check` reports the exact changed transport classes without
modifying generated output.

`generated/compatibility/interface-javascript.json` is the committed,
CLI-owned caller-visible JavaScript API working record for every successfully
projected exposed Interface.
It compares the shared package-root exports and public runtime types separately
from each Interface's client path and factory, request/response/reachable
TypeScript shapes, requiredness and exact scalar mappings, and semantic-error
union. The record stores only classified digests and exact Interface IDs; it
contains no Implementation, configuration, Secret, source-location, or
module-version data. Generation refreshes it in the same transaction, and
`plystra generate --check` reports API drift without modifying the Project.

`generated/compatibility/interface-documentation.json` is the committed,
CLI-owned working record for the documentation artifacts currently emitted under
`generated/docs/`. It classifies the Interface reference and OpenAPI document
by stable managed path and stores only their exact content digests, not the
documentation bytes, Implementations, configuration, Secrets, source
locations, or module versions. Generation refreshes it transactionally,
including a valid empty working record when the selected model has no documentation
surface. `plystra generate --check` reports documentation drift without
modifying the Project.

The five compatibility working records listed above are replaceable
current-source state whose ownership-manifest output kind is
`compatibility-working-record`. Classification is per record and ownership entry;
another file is not replaceable merely because it is stored under
`generated/compatibility/`. Accepted baselines are distinct immutable release
evidence keyed to an exact released artifact and lineage. Ordinary generation,
check, cleanup, recovery, or relabeling must not create, overwrite, or delete
that evidence.

`generated/proto/wire-map.json` is committed, CLI-owned compatibility history
for the request, response, and reachable same-package messages of every visible
authored Interface, whether or not that Interface is exposed or Connect is
enabled. Exposed Connect Interfaces additionally retain their exact stable
service, method, and procedure identity. Authored positive `plystra` field
numbers are the wire numbers. Generation rejects renumbering and reuse across
all visible Interface history, permanently reserves both the Protobuf name and
number of every removed field, and carries active reservations into generated
source and the descriptor set. Never-exposed Interfaces, removed exposure, and
disabled Connect remain inactive and create no schema, descriptor, handler, or
SDK output, while their field names and numbers remain in wire history and their
pointer shape remains in the authored-shape working record. The same ledger temporarily retains separately labelled legacy
transport history required by pre-Gate-14 handlers; that bridge is not
Interface contract authority. The ownership manifest records the exact ledger
digest, and generation rejects a missing, manually changed, corrupt, reused,
or projection-inconsistent prior ledger instead of guessing. Never edit or
delete this file; restore its exact last committed content before
regenerating. Generation also emits one deterministic schema containing the
canonical messages and one unary service for each supported pointer-free exposed Interface, plus a self-contained
`generated/proto/descriptor-set.pb` containing any required well-known
descriptors. A Project without a selected Connect surface retains a valid
empty descriptor set. These schema and descriptor files are CLI-owned, contain
no Implementation, configuration, or Secret data, and are checked for drift
with the rest of `generated/`; never edit them manually.
For every selected Connect surface, generation also emits a Go handler under
`generated/go/adapters/connect/`. Canonical handlers bind one exact procedure
to the generated canonical application-invocation handle; Alias handlers are
thin forwards to that canonical handler and never create a Provider or Alias
dispatch entry. The current Connect boundary accepts canonical contracts whose
explicit `semantics.kind` is `query` or `command` and projects each as one unary
procedure; an Alias reuses that canonical target. Selecting an `event` or
`stream` for Connect fails before generated output and names the Capability,
declared kind, supported unary kinds, and `http.expose` remediation. The
`PLYSTRA_PROTOBUF_OPERATION_KIND_UNSUPPORTED` diagnostic reports the effective
module-relative configuration document at `1:1` as an `exposure` source before
selector-aware recovery. Do not relabel an event or stream to bypass this
validation.
The current CLI retains unexposed pointer-bearing Interfaces but does not yet
emit their required Protobuf presence wrappers. Selecting one in `http.expose`
fails closed with `PLYSTRA_PROTOBUF_POINTER_PROJECTION_UNSUPPORTED`, reports the
effective declaration's owning configuration document at `1:1` as an
`exposure` source,
and directs the developer to remove that exposure and rerun generation with the
same selector. The diagnostic exposes no absolute path or pointer value, and
the failed command changes no authored, generated, module, compatibility, or
transaction file.
The configured `RootContext` receives the live external request context and
returns the trusted Kernel root used by the canonical invocation. Generated
handlers preserve explicit caller cancellation and the earlier caller or
trusted-root deadline even when that trusted root deliberately detaches from
the external context. Canonical, Alias, HTTP, and direct paths deliver
`context.Canceled` or `context.DeadlineExceeded` through the application
invocation to the Provider and return no response when invocation observes it.
These signals are best-effort interruption only; neither cancellation nor a
deadline rolls back or compensates Provider work that already occurred.
Both handlers accept only Connect POST requests encoded as binary
Protobuf or ProtoJSON, require `Connect-Protocol-Version: 1`, and reject gRPC
and gRPC-Web with `415 Unsupported Media Type` before root-context or Provider
invocation. Their `Accept-Post` response advertises only the two supported
Connect media types. Binary Protobuf requests are limited to 1 MiB, decoded
with a maximum message depth of 64, and validated with a 65,536-node budget.
Malformed or truncated wire data, unknown fields at any message depth, and
requests that exceed any bound fail before root-context creation or Provider
invocation; the same validation applies when a generated handler is called
directly. Binary Protobuf responses use the same 1 MiB, depth-64, and
65,536-node bounds. Generated conversion preflights canonical fields,
collections, object graphs, and content bytes before proportional
wire-projection allocation, validates the exact response message, and
deterministically serializes it. An invalid or
oversized response produces only the safe internal response failure and no
partial response; canonical, Alias, and direct handler paths agree. ProtoJSON
requests have their own 1 MiB, depth-64, and 65,536-token preflight before
strict decoding with unknown or duplicate fields and invalid UTF-8 rejected,
followed by the same generated message and canonical request validation. A required `null` fails requiredness,
and an optional non-nullable `null` becomes absence. For non-required
non-pointer scalar and value-message Interface fields, omission and an explicit
Go zero value normalize to the same ordinary Go value; wire presence is not
business-observable, and full-range integers remain exact. Non-finite canonical numbers
fail before root-context creation or Provider invocation. ProtoJSON responses
are validated against the same exact generated message and canonical response,
then limited independently to 1 MiB with no partial response. Canonical and
Alias binary and ProtoJSON paths therefore produce the same canonical values
and safe failures.
The CLI transaction installs direct `connectrpc.com/connect`
and `google.golang.org/protobuf` requirements at the supported versions when
those handlers are present. The generated application entrypoint does not yet
mount an HTTP server; server mounting and the remaining protocol projections
remain later transport work. Before
wire-map reconciliation, the normalized Protobuf model
rejects two canonical fields in the same request or response when they derive
the same ProtoJSON name or generated enum type. The
`PLYSTRA_PROTOBUF_IDENTITY_COLLISION` diagnostic names the Interface, Go
message, both authored field names, and colliding identity, then reports the
owning module-relative Go file at the trusted Interface declaration position as
an `interface-contract` source. Ordinary generation and
`generate --check` fail without changing the Project.

Capability inspection strictly parses the optional `extensions` mapping within the 1 MiB declaration boundary. The CLI preserves every valid lower-kebab namespace, including unknown namespaces, as immutable namespace-sorted canonical JSON-compatible metadata: object key order is normalized, scalar types and array order are preserved, and omitted and empty metadata are equivalent. Normalized extension metadata participates in exact contract equality, so providers cannot add, remove, or change generation-affecting behavior under one Capability ID; conflicts report the differing metadata paths and require a new version. Namespace interpretation remains a selected plugin generation-extension responsibility.

## Resolution and generation fixed point

Every required Interface or exact `plystra.Optional[T]` constructor dependency
needs an explicit nonblank Go parameter identifier other than `_`, even when
the constructor is only a visible candidate. The optional first `Config`
parameter is exempt. Discovery, static assembly, inspection, and provenance
preserve dependency names with exact case and authored position. Renaming or
reordering a reachable dependency changes application-model identity and
requires regeneration, without changing its canonical Interface contract.
Invalid required and optional names use `PLYSTRA_IMPLEMENTATION_REQUIRED_INVALID`
and `PLYSTRA_IMPLEMENTATION_OPTIONAL_INVALID`, respectively, with the owning
constructor source and a recovery action before any Project mutation.

The Interface-resolution path derives one static constructor graph from effective `interfaces.require` entries and external exposure composed from selected current-Project layers and the selected current-Project delta, plus transitive required constructor parameters. Selected requirements and exposure create roots immediately without repeated current-Project declarations. Every discovered `//plystra:implements` declaration is only a compatible candidate, whether it belongs to the current Project or a dependency Project; discovery alone never creates an application root, binding, constructor membership, or generated output. An exact compatible `interfaces.use` choice is validated even before its Interface is required, but remains dormant and creates no root, binding, reachable constructor, lifecycle membership, or generated Interface runtime until that Interface enters the requirement closure. Invalid dormant choices fail before generation. Unreachable Implementation candidates and optional-only dependencies remain outside assembly.

An effective `config.<constructor-symbol>` object is valid only when that exact constructor is named by an effective explicit `interfaces.use` choice or is already selected into the reachable constructor graph. The CLI immediately validates the object against the constructor's exported same-package `Config` schema, including Secret-reference syntax, without reading an environment variable or Secret file. While the owning choice remains dormant, the object remains authored configuration only: it creates no runtime delivery, generated assembly or bootstrap membership, Secret lookup, or Kernel state. Configuration for any other constructor fails without exposing its values under `PLYSTRA_CONSTRUCTOR_CONFIGURATION_UNSELECTED`.

A Project with zero non-intrinsic roots is valid. With no selected Resource instances, generation emits the intrinsic Kernel catalog, an empty ordinary constructor and lifecycle plan, static assembly, and bootstrap. The generated application starts, invokes `kernel.health/v1` through its smoke path, and shuts down cleanly without creating ordinary bindings, runtime-configuration membership, or public transport surfaces.

Generation emits one managed typed proxy for every reachable authored Interface under `generated/go/proxies/<interface-id>/vN/proxy_gen.go`. Each proxy has a compile-time assignment to the authored `Interface`, preserves its exact request and response method signature, and delegates the call to a typed `github.com/plystra/kernel/invocation.Handle`; it does not copy or redefine the canonical contract. Generation also emits one managed endpoint adapter for every reachable selected Interface binding under `generated/go/adapters/implementations/<interface-id>/vN/adapter_gen.go`. The adapter owns the exact opaque Kernel contract token for that binding, including the Interface's declared semantic-error codes, accepts the authored `Interface` rather than naming the constructor's concrete pointer type, and delegates to the exact authored method. It records the selected constructor symbol and inferred concrete pointer type as deterministic provenance, so a constructor returning an unexported type remains usable and one constructor selected for several Interfaces receives one adapter per binding. Unreachable dependency Interfaces produce neither proxy nor adapter.

Each supported `http.expose` entry backed by a canonical pointer-free Interface package emits one deterministic schema at `generated/proto/plystra/generated/<interface-id>/interface.proto` and contributes it to the existing self-contained descriptor set. Authored non-intrinsic packages come from the visible Project graph; intrinsic `kernel.*` packages are loaded only by their exact Kernel-owned inventory paths from the selected Kernel module. The canonical Go request, response, and nested message graph supplies the exact scalar widths, collection and map shapes, well-known types, effective JSON names, required markers, and authored `plystra` field numbers. The same Interface schema owns exactly one deterministic unary service and `Invoke` method whose Connect path is derived from the exact Interface ID and retained in wire history. Generated handlers map declared Interface semantic failures to `failed_precondition`, map every closed Kernel runtime class to its fixed Connect code, and normalize unknown failures, panics, malformed requests, and invalid responses without copying private error text. During the pre-Gate-14 transport transition, an overlapping legacy schema becomes an import-only bridge and declares no competing message, enum, service, or procedure.

Generation also owns `generated/go/assembly/interfaces_gen.go`. Its `NewInterfaceRuntime` creates each selected Implementation constructor exactly once in dependency-first order, injects required proxies plus available or unavailable `plystra.Optional[T]` values, shares one concrete instance across every Interface declared by that constructor, creates exact Implementation bindings with constructor, module, selection-reason, and contract-digest provenance, and publishes one complete immutable Kernel catalog before returning typed root Interface accessors. Exposed intrinsic Kernel Interfaces receive typed root accessors backed by that same catalog without entering ordinary Implementation selection. It constructs named Resources before Implementations, binding Resource lifecycle by exact instance name and Implementation lifecycle by exact constructor symbol; startup is bounded, a failed or panicking start rolls back every constructed lifecycle value in reverse order, including never-started values, and normal shutdown also runs in reverse order. Internal calls remain ordinary typed in-process method calls through the governed proxies. Proxy, adapter, static-assembly, lifecycle, normalized Interface timeout-policy, canonical Interface Protobuf projection identity and source digests, stable unary procedure identity, governed Connect adapter mapping, exposed intrinsic proxy mapping, and active Interface wire history participate in application-model schema version 19; `plystra generate --check` reports a missing or modified file without changing the Project. Generated bootstrap constructs this `InterfaceRuntime`, includes it in application validity, exposes it through `Application.Interfaces`, and coordinates startup and shutdown with the temporary legacy lifecycle boundary.

Constructors only assemble values; acquisition and background work belong in
lifecycle `Start`. Assembly rejects nil success values, redacts constructor
errors and panics, and cleans earlier lifecycle values plus any non-nil partial
result in reverse dependency order before returning failure. No failed runtime
is published. `Stop` must tolerate never-started and partially started values.
If construction cleanup fails, use `errors.As` with
`interface { RetryCleanup(context.Context) error }` to recover the outermost
cleanup owner. Bootstrap retains both static and legacy cleanup when needed;
standalone failures use `InterfaceAssemblyError` or `ProviderAssemblyError`.
`RetryCleanup(ctx)` retries only pending stops under the original timeout and
any earlier caller deadline, without repeating successful cleanup or restarting
construction. Versioned Kernel dependencies older than the installed supported
Kernel fail static assembly generation; upgrade the Project dependency before
regenerating. Explicit local replacements remain developer-owned source builds.

Reserved `kernel.*` Interfaces are always collected as intrinsic requirements outside ordinary Implementation selection. The versioned `github.com/plystra/kernel/intrinsic.InterfaceDefinitions` inventory names their canonical `github.com/plystra/kernel/interfaces/kernel/...` packages; the CLI adapts that Kernel-owned inventory and maintains no second intrinsic Interface list. Explicit `interfaces.require` and `http.expose` declarations add deterministic requirement provenance without creating an Implementation choice. An unknown reserved ID, an application-authored `kernel.*` Interface declaration, or an explicit `interfaces.use` choice for an intrinsic Interface fails before generation. A Plystra Project directly requires `github.com/plystra/kernel` in `go.mod`, and generation retains that selected module version or a deterministic local-workspace build identity for intrinsic runtime provenance. Ordinary Implementations are never chosen by priority, official status, discovery order, or filesystem order.

The CLI strictly normalizes `plystra.yaml` `http.address`, optional `http.cors`, and the Interface-keyed `http.expose` mapping. Each exact Interface key requires `transport: connect`. A supported exposure makes the Interface an application root and generates its Connect and JavaScript surfaces; a pointer-bearing Interface fails closed at the projection boundary described above. Internal availability alone creates no public surface, including for intrinsic `kernel.*` targets. New Projects write `http.expose: {}` as an empty delta that preserves selected root exposure. No external transport is selected only when the effective exposure is empty. Duplicate or malformed IDs, missing transports, unsupported fields, exposure lists, `add`/`remove` exposure sets, and global `http.transports` switches fail before output. REST route configuration remains deferred. When present, `http.cors` requires one nonempty normalized `allowed_origins` list, accepts only optional boolean `allow_credentials`, defaults credentials to disabled, and rejects malformed origins and credentialed wildcards.

Generated Connect handlers enforce a selected CORS policy before protocol dispatch. A request origin must be one canonical normalized HTTP/HTTPS origin serialization, except that literal `null` is accepted only by a noncredentialed wildcard policy; origin input is bounded to 4096 bytes. Preflight accepts only `POST` and the fixed `Authorization`, `Connect-Protocol-Version`, `Connect-Timeout-Ms`, and `Content-Type` request headers, with each name present at most once across no more than four field values totaling at most 4096 bytes. Malformed, noncanonical, duplicate, over-bound, or disallowed cross-origin input fails before trusted-root creation or Implementation invocation; allowed responses carry deterministic origin, credential, and `Vary` headers. Without `http.cors`, generation adds no implicit CORS behavior.

Rule-derived requirements participate in the same transitive fixed point. A missing provider, unclaimed metadata namespace, ambiguous rule owner, rule cycle, incompatible contribution, or provider ambiguity fails before the CLI writes a runnable artifact.

Visible generation declarations are first indexed by extension namespace. Several candidate providers may associate one namespace with the same exact activation Capability, but different activation Capabilities for one namespace fail with every declaring Plugin ID, API, package, and source. After ordinary provider resolution selects that Capability's provider, only the matching extension owned by that selected plugin is eligible to run; every unselected provider extension is excluded.

## Plugin-provided build-time rules

Advanced infrastructure plugins may declare:

```yaml
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: authn
      capability: authn.session.verify/v1
```

The CLI accepts only a supported generation API, a canonical plugin-relative package path that resolves to an existing directory through non-symbolic components, and unique lower-kebab namespace activations naming Capabilities provided by the same plugin. It loads the confined package only during resolution or generation. It supplies a filtered read-only normalized model containing public declarations, exact schemas, extension metadata, requirements, provider mappings, exposure, and only explicitly build-visible structure. Secret values, the unrestricted environment, private runtime configuration, writable user source, and final generated paths are excluded.

The public v1 input contract is `github.com/plystra/cli/generation/v1`. It validates complete resolved state, exposes only defensive immutable views, canonically orders every collection and JSON-compatible metadata value, and provides stable SHA-256 input and contract digests. Its empty context is valid for applications with no plugins or extensions.

Filesystem-backed contexts also expose immutable configuration provenance:
selection mode, selected environment when applicable, stable Project-relative
root and selected-document paths, normalized root and selected-document
digests, and the current-Project composition digest. They never expose YAML
values, resolved Secrets, absolute paths, the process environment, or
generated-output locations. `Digest` covers this complete extension input and
survives the helper-process round trip. `BuildModelDigest` excludes document
provenance so a runtime-only configuration change does not alter static
assembly unless an extension actually changes its normalized output. Before
built-in transport or bootstrap generation begins, the CLI cross-checks that
bounded identity against the typed current-Project composition and
generated-manifest provenance and ties it to the final application-model
digest. Bootstrap and the Connect, REST/JSON, JavaScript, and API-document
renderers require the validated identity but do not serialize selector-only
paths or document digests into executable or public output. Equal effective
build models therefore retain byte-stable executable/public artifacts while
`generated/manifest.json` and its ownership manifest retain selection and
composition provenance.

Each compatible generation package exports exactly:

```go
func Generate(context generation.GenerationContext) (generation.Output, error)
```

The v1 output protocol carries exact generation-derived Capability requirements, structured diagnostics, and application-local Capability Alias contributions with rule, namespace, and source-Capability provenance. It also defines stable contribution identities at `http.ingress`, `invocation.prepare`, `invocation.complete`, and `http.egress`, with explicit canonical `requires` and `provides` dependency tokens. Contributions contain only the closed CLI-owned node union for typed canonical Capability calls, validated context derivation, conditional failure, bounded non-sensitive scalar metadata attachment, and explicit ordinary-Capability audit events. The CLI validates Alias identity, direct resolved canonical targets, same-version semantics, exposure narrowing, deprecation bounds, request bindings against canonical schemas, backward-only node references, timeouts, bounds, sensitive credential flow, and explicit failure behavior; preserves semantic node order; canonically sorts only unordered fields and Alias proposals; and includes every normalized result in output digests.

For reliability isolation, the CLI compiles each selected package into a transient helper against the application's own Go Module graph. The helper enforces the exact `Generate` signature, receives a bounded strict-JSON context envelope, runs from an empty temporary working directory with a minimal environment and deadline, and returns a bounded strict-JSON result. Compile errors, extension errors, panics, abnormal exits, timeouts, oversized output, malformed envelopes, and invalid normalized output remain distinct diagnostics that name the Plugin ID, API, package, and activation namespaces. Cancellation terminates the helper process tree, and closing the helper removes its temporary source and executable. This process boundary is crash and timeout containment, not a security sandbox for malicious trusted code.

Rules return protocol-defined exact requirements, diagnostics, structured operations, and dependency edges at:

```text
http.ingress
invocation.prepare
invocation.complete
http.egress
```

They cannot patch arbitrary text, write source, choose providers, mutate another plugin, or own final generated files. The CLI validates duplicate IDs, missing dependencies, cycles, incompatible outputs, and contradictory failure behavior. Semantic order comes from declared dependencies; stable sorting affects bytes only when operations are already order-independent.

Rule inputs, outputs, dependency graphs, contribution digests, and final results enter the generated manifest without Secret values. `plystra generate --check` recomputes them, and removing a plugin or metadata match removes stale output.

## Runtime configuration resolution

`--template` resolves one Project module through the current Project's effective Go Module graph and records it as an ordinary direct dependency. Missing modules, non-Project targets, and invalid queries fail before mutation; `go.mod` owns the selected version. The dependency contributes no configuration, source, runtime baseline, Resource instance, or Data declaration.

Configuration has at most two current-Project layers: root `plystra.yaml` and one selected environment overlay. A complete replacement document is one complete current-Project layer with no lower application layer. Later exact-key replacements and overlay removals have precedence; root and replacement documents reject sparse forms and tombstones. Ordinary dependencies remain discoverable, but their application configuration stays inactive.

Malformed template queries or selected dependencies report `PLYSTRA_PROJECT_CREATE_TEMPLATE_INVALID` with safe module and marker diagnostics. Correct the owning Project or Go Module declaration, never a Module Cache copy.

CLI and generated runtime typed normalization reject malformed scalar payloads before replacements, removals, or nullable values can hide them. Genuine null remains valid for nullable Go types; diagnostics omit decoder text and private values.

Exact entries in `interfaces.use`, `interfaces.policies`, and `http.expose`
are removed only by `{$remove: true}`. Null, empty values, false or string-valued
markers, and markers with sibling fields are invalid. Exclusions remain authored
intent even when no lower entry exists and survive later root edits.
Generated runtime compatibility version 12 applies the same rule before
construction; regenerate and rebuild older Projects. Remove `http.cors` or either
of its fields with `{$remove: true}`; null is invalid. Removing credentials uses
the default `false`; an effective CORS object must still contain origins.

Remove a whole `config.<constructor-symbol>` entry only with `{$remove: true}`.
Null, sequences, malformed markers, and markers with sibling fields are invalid
at that boundary. An empty mapping remains configuration, not removal. The
constructor still needs a discovered compiled Config schema, and its exclusion
survives absent or changed lower configuration without activating it. Inspection
retains the removal owner and suppressed field sources. Generated runtime
loading strips whole-entry markers before construction; removing required
configuration can still fail validation before any constructor runs.

Typed CLI composition uses the same exact marker to remove a declared field
inside a non-pointer fixed struct. Exclusions survive absent or changed lower
values; omission inherits. Literal `null` (including `~` or a blank value) is
an atomic nil value only for pointers, slices, and maps, and remains distinct
from removal or an empty collection. Other compiled types reject null. The
reserved singleton `$remove` mapping is invalid inside atomic values, including
list elements, dynamic-map entries, and pointed-to struct members. A dynamic map
with `$remove` and other keys is an ordinary typed map, not a tombstone.

Required fields are checked after the selected current-Project layers compose.
Partial fragments may supply them together. Every effective authored object is
checked, including dormant constructor configuration; an active configurable
constructor is checked even when its whole object is absent or removed. Dormant
constructors with no effective object need no required values yet. Requiredness
means presence, so zero, empty, and schema-permitted nil values count. Omitted
fixed structs and fixed-array elements still need their nested required fields;
absent or nil pointers and empty slices/maps have no child values to validate.
Failures use `PLYSTRA_CONSTRUCTOR_CONFIGURATION_VALUES_INVALID` with a safe
declared field path and the selected current document, without modifying files.
Generated startup applies these rules and compiled scalar defaults after
composing active constructor values from the selected current-Project layers.

During typed CLI composition, a supplied non-null pointer field replaces its
complete lower value, including pointers to structs and multiple pointer layers.
Omitted pointer fields inherit; an explicit `{}` replaces a pointed-to struct
with an empty object instead of inheriting its omitted fields. Only non-pointer
fixed structs compose field by field. A supplied overlay pointer value replaces
the root value atomically, even when their fields are disjoint. Inspection records
one redacted atomic value with its contributing
sources. Generated runtime loading applies the same pointer replacement rules
to the selected current-Project layers.

An `interfaces.require` sequence declares the complete explicit requirement set
at that layer: `[email.send/v1]` replaces lower requirements and `[]` clears them.
Root and complete-replacement documents require that sequence. An environment
overlay may omit it, inherit it with `{}`, or use `{add: [...], remove: [...]}`
to change only the listed members. Root configuration precedes one selected
environment overlay. A sparse overlay preserves the root complete-set boundary.
This does not remove requirements from exposure, constructor parameters, or
intrinsic Kernel entries. Inspection retains suppressed sources under the
`interfaces.require` complete-set boundary. New Projects use `require: []`.
Generated runtime compatibility version 12 applies these set semantics to the
selected current-Project layers reconstructed from
the private baseline, preserving complete-set boundaries and sparse removals.

The public runtime contract binds the selected current-Project model and its exact generated identity. Dependency module membership is ordinary Go state; no dependency source tree or Module Cache is a runtime input at startup.

The public inspect and graph envelopes remain version 1; embedded resolution evidence is schema 3, generated manifest configuration is schema 8, and runtime compatibility is version 12. Current-Project contributions retain exact source paths, normalized digests, layer ownership, and removal history. Equal private values retain separate layer histories. Older schemas fail strict decoding; regenerate with the matching CLI, without migration or compatibility aliases.

Root `plystra.yaml` is the mandatory Project marker, shared current-Project layer, and default configuration for every invocation. `plystra generate --env production` adds exactly one sparse project-root `plystra.production.yaml` overlay above that root; the overlay must exist, omitted fields inherit, and typed scalar, keyed-object, set, and tombstone semantics determine each field rather than a generic YAML deep merge. Within `http.expose`, omitted Interface entries inherit, a supplied complete entry replaces that exact choice, and `{$remove: true}` removes that entry. An empty mapping preserves inherited entries; the complete exposure field cannot be null. A supplied `http.cors.allowed_origins` list replaces the complete normalized root list while omitted origins inherit and credentials replace independently; `http.cors: {$remove: true}` disables inherited CORS. Null is invalid at the object and field boundaries. `template` is an unknown configuration field. Dependency environment files are never loaded, unselected overlays are ignored, and generation preserves both authored current-Project documents byte-for-byte.

`plystra generate --config deploy/customer-a.yaml` instead uses that one complete
document as the selected current-Project layer, with no lower application layer.
Root `plystra.yaml` remains the mandatory Project marker, but its top-level
application declarations are not merged beneath the selected file. `PLYSTRA_ENV` and `PLYSTRA_CONFIG` supply their
corresponding selector for automation. Setting both variables is an error,
`--env` and `--config` cannot be combined, and either explicit selector
overrides both ambient variables. Relative configuration paths are resolved
from the detected Project root even when the command starts inside a Plugin;
an absolute path is accepted only when it resolves within that root. Selecting
an environment or configuration never changes Project, Plugin, or dependency
discovery.

The CLI indexes each visible plugin's strict Kernel configuration declaration and composes Plugin values only at declared typed field boundaries. It validates `timeouts.startup` as an optional positive Go duration, using `2m` when omitted; generated bootstrap reads that runtime-only application lifecycle bound again from the bounded runtime document rather than embedding it. The closed `interfaces.policies` schema separately accepts a positive Go-duration `timeout` and an optional closed `retry` mapping for each canonical non-intrinsic Interface ID. Policies compose and overlay by exact Interface key and are not aliases for `timeouts.startup`. Authored static Interface timeout and replay-safe retry policies execute through the selected binding. Retry requires timeout and the explicit eligibility: replay_safe assertion about the binding and its downstream effects; safety is never inferred. max_attempts counts the first attempt, defaults to 2, and permits 2 through 16; backoff defaults to 0s and accepts nonnegative Go durations. One total budget starts before request validation and copying, is capped by an earlier caller deadline, and includes all attempts, backoff, and response processing. Each attempt receives a fresh copy of the original request snapshot and starts only after the previous target terminates. The outermost retry-enabled binding owns replay; nested bindings suppress their own retries. Only not_started resource exhaustion and result_known unavailable or resource exhaustion can retry. Semantic errors, cancellation, deadlines, internal or validation failures, and result_unknown never replay; exhaustion retains the final safe category, completion, and bounded attempt count. Without retry there is one attempt; without timeout there is no added deadline. Complete compiled policy values and literal schema/compiler/defaults versions are frozen before runtime; mismatches fail closed. Dormant policies remain intent outside executable identity until activation. Inspect capabilities reports support stages, exact defaults, and duration bounds. Authored concurrency, queue, and circuit forms remain unsupported. Transitional legacy Capability wrappers do not include preparation and completion in the Kernel budget; active authored policies on that path still fail with PLYSTRA_POLICY_NOT_ENFORCED. Capability discovery reports that exception as legacy.capability-timeout with executed=no and accepted=no. After the provider and generation fixed point stabilizes, the CLI validates exactly one object for every selected Plugin ID with the Kernel's non-resolving validator. Omitted objects normalize to `{}` so optional fields and defaults remain usable; missing required fields, unknown fields, invalid values or Secret-reference syntax, and configuration for an unselected plugin fail before rendering. Environment variables and files are never read during generation.

Private values and Secret reference targets do not enter generation-extension context, generated source, SDKs, documentation, or diagnostics. `generated/manifest.json` records one versioned canonical constraint projection containing every resolved canonical Capability ID, its exact contract and constraint digests, and each constrained request or response field's path, type, and normalized constraint object. Unconstrained Capabilities retain empty field lists, and an aggregate constraint-projection digest makes addition, removal, or semantic constraint changes deterministic drift. Configuration schema v8 records `default`, `environment`, or `explicit-config` mode; the selected environment and overlay reference when applicable; stable Project-relative paths; normalized semantic document digests; current-Project composition provenance and redacted source provenance; `current_project_paths` ownership; the committed Protobuf wire-map digest; and the final build-affecting application-model digest. It requires canonically ordered `dormant_implementation_selections` and constructor-keyed `dormant_constructor_configurations` arrays, each with an aggregate digest. A dormant selection retains the exact Interface and constructor, constructor module/version/source, canonical `interfaces.use` path and normalized decision digest, effective configuration owner, and ordered replacement/removal contributions with module-relative sources. A dormant constructor-configuration record appears once even when the same constructor has several dormant selections; it retains the exact constructor identity, canonical `config["<constructor>"]` path, every normalized field digest and redacted summary, effective/removal state, owner, suppressed descendants, and complete ordered contribution history. A constructor active through any reachable binding is excluded from this dormant configuration class. Empty Projects record explicit empty arrays and canonical digests. Neither record contains a raw configuration value, Secret-reference target, resolved Secret, or machine path, and both remain disjoint from executable `interface_provenance`; activation removes the dormant records and places the same choice and configuration ownership in ordinary reachable binding and constructor provenance instead.

The required top-level `transport_toolchain` record contains the exact embedded `go/format` runtime, built-in Protobuf-model, descriptor, wire-map, Connect, JavaScript, and API-documentation generator versions, pinned generated Go and npm dependency versions, and a canonical digest. Generation never consults an implicit global `protoc`, another generator executable, or a hosted generation service; changing this embedded identity changes the manifest and is detected by `plystra generate --check`. `generated/go/bootstrap/bootstrap_gen.go` records only the bounded executable compatibility projection: selected public Interface exposure entries with their transports, CORS policy, explicit Interface requirements, exact executable Interface-to-Implementation constructor choices, normalized Interface timeout and retry policies, and the complete application-model digest. Dormant choices and dormant constructor-configuration records remain only in manifest configuration/composition provenance until activation, so a dormant-only edit changes manifest provenance without changing bootstrap source or artifact provenance. The projection digest keeps the runtime check cryptographically associated with the exact generated assembly. Process address, `timeouts.startup`, runtime configuration, Secret references, resolved Secrets, source paths, selector-only document identity, and machine-specific absolute paths are excluded. Changing selected CORS origins, credential handling, selected current-Project model, or an Interface timeout or retry policy creates deterministic generation drift when it changes the normalized selected model; equivalent normalized values retain one static model identity. Current-Project records contain deterministic path, digest, removal, and source provenance. A separate private digest covers validated runtime configuration only for concurrent-input detection during the generation transaction.

Ordinary dependency configuration stays inactive and its values do not enter public identities. Selected current-Project configuration is validated against discovered compiled Config schemas. Secret references and runtime-only values remain private, including their hashes; build-visible fields contribute only their public projection. Generation separately snapshots root and selected-document inputs to detect concurrent edits without overwriting them.

The current-Project process settings `http.address` and `timeouts.startup`
also exclude their values from public hashes. Valid edits leave generated
output, inspection, and explanation unchanged; declared presence, type, source,
and removal intent remain visible. Private snapshots still detect concurrent
edits. Startup reads the selected timeout without regeneration. CORS, exposure,
and invocation policies remain build-affecting, not runtime-only process values.

Public configuration type descriptions omit raw anonymous-struct tags, including
inside containers and generic arguments, so nested defaults cannot enter logs,
diagnostics, or public value hashes. Compiled field names, policy, default
presence, and private default access remain intact. These descriptions are not
complete schema or Go assignment identities. Changing an authored Go default
still requires rebuilding. Generated startup reads scalar defaults from the
compiled Config type after current-Project composition,
without copying private default literals into generated source.

The required top-level `interface_provenance` record in
`generated/manifest.json` uses schema `plystra.interface-provenance/v3`. It
identifies every visible authored Interface, every reachable ordinary binding,
the selected constructor and selection reason, the complete dependency-first
constructor graph, configuration ownership and source paths, effective
Interface-policy inputs, root and exposure sources, and the exact generated
proxy, adapter, assembly, Protobuf, wire-map, Connect, HTTP-route, and
JavaScript mappings and digests. Required intrinsic `kernel.*` Interfaces are
recorded separately and never acquire an ordinary constructor or
Implementation adapter. The same canonical record is embedded in
`generated/.plystra-manifest.json` for recovery. It contains no YAML
configuration values, Secret-reference targets, resolved Secrets, process
environment, or machine-specific absolute paths. `plystra generate --check`
validates its strict schema, ordering, completeness, and digest and reports
drift without modifying either manifest.

Ownership-manifest schema 3 also records immutable provenance for every
managed artifact: its Project-relative path and SHA-256, exact owning
generator/version, closed output kind, normalized input-record IDs, stable
source references, and `cli-owned` cleanup authority. The ownership manifest
itself has an implicit provenance record derived from the complete artifact
set, avoiding a self-digest. This evidence remains available when an owned
target is missing or manually modified, and it contains no configuration
values, Secret-reference targets, resolved Secrets, or machine-specific
absolute paths.

For every selected local plugin, generation derives its module-owned type and decoder under `generated/go/configuration/` from the validated `plugin.yaml` schema alone. Required fields and fields with defaults use direct Go values; omitted optional scalars use pointers, while optional objects and arrays preserve nil-versus-configured-empty behavior. The generated decoder calls Kernel `configuration.Decode` at runtime, constructs one typed object for the Plugin ID, and redacts formatting and serialization. Application values and Secret reference targets are never embedded in this source. Selected dependency plugins ship the same generated configuration boundary in their own Go Modules.

The application-owned `generated/go/bootstrap` package is the runtime construction and configuration-selection boundary. Its `New` function requires `--configuration-root <directory>`, selects `plystra.yaml` within that root by default, loads it through a confined directory handle with bounded regular-file reads, and projects the normalized runtime document onto the build-affecting declarations compiled into the binary. The runtime schema accepts `interfaces.require`, `interfaces.use`, and `interfaces.policies`, validates canonical Interface IDs, fully qualified constructor symbols, and the closed timeout-and-retry policy shape, and rejects the superseded `capabilities` section. A projection mismatch fails with rebuild guidance before startup settings are read, Secrets are resolved, or an Implementation constructor runs. Runtime-only changes to `http.address`, `timeouts.startup`, configuration values, and Secret references remain outside this comparison; an Interface timeout or retry change does not. After compatibility succeeds, bootstrap constructs the frozen generated `InterfaceRuntime`, verifies that its immutable catalog is published, and returns it from the private redacted `Application` through `Application.Interfaces`. Passing `--env <environment>` to the generated binary, or setting `PLYSTRA_ENV` when no explicit selector is present, loads root plus exactly one required sparse `plystra.<environment>.yaml` through the same typed field rules used during generation. Passing `--config <yaml-path>`, or setting `PLYSTRA_CONFIG` when no explicit selector is present, instead loads and normalizes that one complete current-Project document without merging root application declarations; `template` is not a configuration field and a regular root `plystra.yaml` marker remains mandatory. An explicit selector overrides both ambient variables, the two modes cannot be combined, and a selected configuration must be an existing nonsymbolic regular file within the explicit configuration root. Unsafe names or paths, missing files, unknown fields, invalid types, prohibited YAML references, and incompatible build-affecting declarations fail before Implementation construction, while unselected overlays and replacement files remain unread. Generate and start the application with the same selector; after editing selected Implementations, Interface requirements, Interface timeout and retry policies, public exposure, transports, or CORS, regenerate and rebuild with that selector before starting the binary.

Generated bootstrap delivers composed constructor configuration through
the exact typed `Config` fields in `ConstructorConfiguration`. Its generated
internal validator supports the compiled scalar, struct, pointer, slice, array,
map, and Secret types. Fixed structs compose by field; atomic values replace;
nil, empty values, and tombstones remain distinct. Requiredness and scalar
defaults apply after composition, including implicit fixed structs and arrays.
All active objects and build-visible values are checked before any Secret is
resolved or constructor runs. Binding invokes no custom YAML unmarshalling.
Defaults come from the compiled Go type; private values, defaults, and reference
targets stay out of generated source. Dormant objects create no runtime binding.
The private baseline carries a discovery-derived inventory of every visible
constructor's supported Interfaces and configuration schema. Startup checks
dormant selection ownership, typed composition, defaults, required fields, and
Secret-reference syntax against that inventory, then discards dormant objects
without resolving their Secrets or importing or calling their constructors.
An absent dormant object needs no required values. Dormant-only value or choice
edits preserve executable artifacts; schema-inventory changes require a matching
regenerated baseline and rebuilt binary. Regenerate older baseline/binary pairs
to include this validation inventory.
Startup rejects invalid recompiled field metadata, including duplicate tags,
metadata on ignored or unexported fields, and invalid defaults hidden by runtime
overrides. Regenerate and rebuild after correcting the authored Go declaration.
The selected root and one environment overlay compose before generation. Later exact-key choices and values replace or remove earlier declarations. Equivalent private values do not collapse distinct layer ownership.
Compatibility version 12 compares effective requirements, selections, and policies
including inherited declarations. Regenerate and rebuild older generated output.

Every generated-binary invocation requires `--configuration-root <directory>`
and `--runtime-baseline <path>`.
For source-tree development, use `go run ./generated/go/application --configuration-root . --runtime-baseline dist/runtime-baseline.json`.
An absolute root permits startup from an unrelated working directory; a relative
root is resolved once at startup. Both explicit and ambient replacement paths
resolve from this root. Root, overlay, and replacement documents must be
nonsymbolic regular files of at most 1 MiB; every path component stays within
the opened directory, and observable file replacement or modification during
loading fails before construction. Missing or invalid roots produce redacted
selector errors without exposing the supplied directory. Regenerate and rebuild
older generated entrypoints for these required arguments.

Generation and scaffolding write `dist/runtime-baseline.json` with native
owner-only permissions and an ignore rule. Deploy that private file with the
matching binary. Linux checks ownership and mode; macOS also rejects extended
ACLs, and Windows requires a protected owner-only DACL. Relative baseline paths
resolve from the configuration root; an absolute path may identify a separately
deployed private file. Startup checks
the public runtime-contract identity and compiled constructor defaults before
Secret resolution or construction. A private default edit requires rebuilding
and refreshing the baseline with `plystra generate`, without publishing private
defaults or their hashes in generated source. Missing, malformed, mismatched,
or publicly readable baselines fail with redacted recovery. `generate --check`
checks public generated output without creating or refreshing private output.
Selected current-Project layers are reconstructed solely from the owner-private baseline. Their selected root, environment, or replacement identity and compiled defaults must match the public runtime contract. Missing, malformed, cyclic, or mismatched selected configuration fails before Secret lookup or constructor entry.

No dependency source tree or Module Cache is read at startup. Named Resource
instances receive their own typed configuration and Secrets through this same
private-baseline boundary. Runtime compatibility version 12 binds instance
names, providers, contracts, and resolved bindings; frozen model version 19
records their assembly identity. Regenerate and rebuild older output. This
support does not establish complete Gate 9 acceptance.

`Application.Interfaces` exposes the frozen governed typed Interface runtime after successful construction, while `Application.Invocations` retains the existing canonical invocation handles during the migration. `Application.Start` starts lifecycle-aware named Resources and static Implementations in dependency order within `timeouts.startup`; failure is redacted and rolls back the full constructed lifecycle set, including never-started instances. `Application.Stop` coordinates reverse-order shutdown, remains retryable after a bounded failure, and reports the combined application state. The CLI-owned `generated/go/application` process entrypoint delegates configuration selection to bootstrap, waits for `SIGINT` or `SIGTERM` during normal execution, and owns bounded shutdown. No runtime value or Secret reference target is embedded in generated source.

## Generated application invocation

Every ordinary external or cross-plugin call uses generated code:

```text
generated adapter or Capability client
-> selected plugin rule contributions
-> Kernel exact dispatch
-> selected provider
-> completion contributions
-> canonical Provider response validation
-> adapter egress and serialization when external
```

The CLI emits `generated/go/assembly/invocations_gen.go` with one typed endpoint adapter per selected ordinary canonical Capability, the complete Kernel-owned intrinsic binding set, one immutable canonical catalog, one shared dispatcher, and dependency-ordered application handles. Assembly prepares every typed handle while the dispatcher is unpublished, constructs every selected provider, and atomically publishes the canonical catalog only after all constructors succeed. The catalog records exact contract digests and provider provenance: ordinary entries carry Plugin ID, package, Go Module build, and sole-provider or explicit-selection reason; intrinsic entries carry Kernel module/build provenance, an empty Plugin ID, and the intrinsic selection reason. Cross-module adapters copy fields directly and perform explicit conversions only for generated named enum types; they do not use JSON as an in-process type bridge. Raw dispatch adds no implicit deadline; the complete compiled absence policy uses a zero timeout. Alias IDs never enter the catalog or dispatcher.

Every generated canonical invocation validates closed field constraints before any selected rule contribution or Provider call. String bounds count Unicode scalar values, patterns use the validated Go regular expression, numeric bounds retain the canonical integer or finite-number model, and array bounds count items. Invalid requests return a data-free `invalid_argument` failure with the stable `contract.invalid_request` detail. Generated Connect and optional REST adapters apply that same validator before creating a trusted root context, while the invocation handle repeats it so internal clients cannot bypass the boundary.

The invocation validates the selected Provider's response after completion contributions and before an external adapter can serialize it. Constraint failures, invalid enum values, non-finite numbers, malformed UTF-8, absent required collections or objects, and unsafe object graphs are discarded; the caller receives the zero response plus one data-free internal contract-defect error. Provider errors likewise discard any accompanying response. The invocation package projects any failure into one immutable `TransportErrorInput` containing only one declared semantic code or one closed Kernel class with an optional bounded detail code. It never retains error text, causes, payloads, or Provider data, and generated adapters consume that projection instead of reclassifying raw errors. Internal clients, canonical adapters, and Alias forwards therefore share the same response and error boundary rather than relying on transport-specific serializers as the first validator.

Required or explicitly exposed intrinsic Capabilities receive normal typed application handles and clients. Their generated request, response, and enum declarations are aliases to `github.com/plystra/kernel/intrinsic`, and assembly binds those handles with `intrinsic.HealthContract`, `intrinsic.InfoContract`, and the Kernel's own binding constructors so callers and endpoints share the same typed contract tokens. `Invocations.IntrinsicHealth` is always available after assembly publication and creates a typed health handle against the same shared dispatcher, even when health is not an application requirement. Generated application handles are constructed in topological dependency order from the canonical clients required by their lowered contributions; an ordinary invocation may depend on an intrinsic client. A cycle, missing or repeated dependency, accessor collision, invalid provenance value, or inconsistent intrinsic/ordinary selection fails generation. Applications with no ordinary providers still publish the two intrinsic canonical bindings, while HTTP and JavaScript surfaces remain absent unless explicitly exposed.

For each local plugin that declares exact `requires`, generation emits an immutable client set at `generated/go/dependencies/<plugin-directory>/dependencies_gen.go`. Its authored constructor receives `func New(Config, dependencies.Dependencies) *Plugin`; a plugin with no requirements keeps `func New(Config) *Plugin`. Dependency-module plugins use the equivalent generated package from their own module. Constructors may validate and retain these clients but cannot invoke them successfully until construction completes and assembly publishes the catalog. Every later cross-plugin call therefore follows the same generated application invocation path and contributions as an external adapter. A missing contract, unbound client, constructor panic or nil result, cross-module type mismatch, or publication failure leaves the runtime unpublished.

Each application-local Capability Alias exposed to Go generates only a thin Alias-named client package. It reuses the canonical target's request, response, and errors and forwards to the target's generated client, so every target contribution runs before the Kernel receives the canonical ID. Alias clients create no Alias contract, invocation handle, provider, or Kernel registration. Several Alias clients may forward to one canonical target, and native Go deprecation comments carry application-local replacement guidance.

For `extensions.authn.authenticated: true`, an AuthN rule adds `authn.session.verify/v1` and generated verification before target dispatch. For `extensions.authz.permission`, an AuthZ rule adds `authz.check/v1`, generates the decision using permission and Space/resource data, and rejects denial. These are static application calls, not Kernel behavior.

## Canonical HTTP transport

Each explicitly HTTP-exposed canonical Capability can generate one `net/http` adapter at `POST /api/v1/capabilities/<capability-name>/vN/invoke`. The adapter accepts a trusted root-context factory and the concrete generated application-invocation handle; it never constructs a raw Kernel handle, registers a provider, or dispatches a route identity itself. Generated contract error codes implement `capability.SemanticError`, and adapters accept both those generated values and sanitized Kernel `invocation.SemanticError` values only when the code belongs to the canonical target contract.

The generated transport accepts one `application/json` object with no content encoding other than identity and bounds request and response JSON to 1 MiB. It rejects alternate paths, query parameters, duplicate media headers, duplicate or unknown fields, missing or `null` required fields, incompatible JSON types, invalid enum values, trailing JSON, and oversized bodies before application invocation. Responses are validated and fully encoded before headers are committed. Error bodies contain only stable transport, semantic Capability, or Kernel invocation codes; provider messages, panic values, and root-context failures are normalized to `internal`. Every response uses `application/json`, `Cache-Control: no-store`, and `X-Content-Type-Options: nosniff`.

When a lowered plan contains `http.ingress`, `http.egress`, or an adapter-credential input, the generated handler selects the matching external invocation path. It runs ingress before shared invocation preparation, preserves the generated invocation context through completion, then runs egress before serialization; internal `Invoke` calls run only the shared preparation/completion path and receive no adapter credentials. Canonical lower-snake credential names map deterministically to HTTP headers (`authorization` to `Authorization`, `x_api_key` to `X-Api-Key`). Missing, empty, duplicate, control-containing, or larger-than-64-KiB credential values are treated as absent, and only the downstream generated verification contribution can turn raw credential text into trusted state.

Each validated HTTP-exposed Alias generates only its own route identity and a thin wrapper around the already-bound canonical handler. Several Alias handlers can share that one canonical transport instance; they do not copy request validation, own an invocation handle, register a provider, or dispatch an Alias ID. The canonical handler validates the Alias path and runs the same planned ingress, invocation, completion, egress, response, and safe-error logic. Alias generation revalidates same-version direct targeting, target contract digest, exposure narrowing, and bounded deprecation metadata; deprecated wrappers carry native Go `Deprecated:` markers without changing runtime behavior or deprecating the target.

## Generated JavaScript SDK

Each supported pointer-free authored or intrinsic Interface successfully projected through Connect generates exactly one nested exact-version method such as `client.records.echo.v1` and one tree-shakable factory such as `createRecordsEchoV1`. Both forms use the same descriptor-resolved unary Connect procedure, typed request and response, declared semantic-error-code union, safe error mapping, explicit credential policy, and cancellation behavior. Request, response, and reachable same-package message types preserve exact authored JSON names, required markers, scalar widths, bytes, timestamps, durations, repeated values, maps, and recursive messages. `int32` and `uint32` use JavaScript `number`; `int64` and `uint64` use `bigint`; bytes use `Uint8Array`. The wrapper validates shapes and ranges before dispatch, uses bounded cycle-safe traversal, and rejects the unsafe JavaScript object map key `__proto__` rather than allowing the pinned Protobuf object representation to change or drop it. An exact-ID transitional operation cannot compete with the canonical Interface-owned module, method, or factory.

Transitional explicitly JavaScript-exposed canonical Capabilities continue to lower into the same deterministic ESM TypeScript package under `generated/sdk/javascript/` until their Gate 14 removal. Their immutable SDK model and emitted field declarations retain each exact normalized constraint object. Generated request preflight and decoded-response validation enforce Unicode scalar-value length, exact `bigint` or finite-number bounds, and array item counts. Canonical `pattern` remains declared but is not reinterpreted through JavaScript `RegExp`; generated server validation uses the authoritative bounded Go regular-expression semantics. Contract digests include closed field constraints and extension metadata, while Implementation identities, runtime configuration, verified internal context, and Secret values never enter the SDK model or source.

The generated browser transport resolves each unary method from the same self-contained Protobuf descriptor graph used by the generated Connect handlers, translates Plystra request and response values at the wrapper boundary, and sends binary Connect requests through `@bufbuild/protobuf`, `@connectrpc/connect`, and `@connectrpc/connect-web`. Those packages are pinned direct npm dependencies of the generated package; callers never construct Protobuf messages, import descriptors, create raw Connect clients, or receive `ConnectError` as the public error model. The package export map exposes only the root Plystra API, and declaration generation strips transport, descriptor, codec, and binder internals. The transport preserves an application base-path prefix, bounds encoded requests to 1 MiB and canonical Interface traversal to 64 levels and 65,536 nodes, requires exactly one `credentialPolicy`, accepts an `AbortSignal`, and exposes only stable Plystra error fields. Anonymous mode uses Fetch credentials `omit` and sends no authorization header. Cookie mode sends no bearer header and uses exactly the declared `same-origin` or `include` Fetch policy. Bearer mode also uses `omit`, calls `getAccessToken` for one bounded raw token, adds exactly one `Authorization: Bearer ...` header, and fails closed with `PlystraError` code `credential_error` for rejected, nullish, empty, malformed, prefixed, control-containing, non-string, or oversized results without exposing credential data. No mode silently falls back to another. Aborting before dispatch, while bearer acquisition is pending, or while `fetch` is in flight rejects with `PlystraError` code `cancelled`; once server invocation has begun, the same cancellation reaches the generated Connect handler, canonical invocation, and Implementation context. Cancellation remains best-effort and does not promise Implementation rollback. Exact fields, enums, finite floating-point numbers, full-width `bigint` values, byte sequences, plain objects, and decoded responses remain validated. Network, cancellation, malformed-response, and schema failures are normalized without copying Connect or Implementation text.

When Connect surfaces exist, descriptor generation also emits the shared `plystra/generated/transport/v1/error.proto` schema. Every generated application failure attaches exactly one `PlystraErrorDetail`; `requested_interface_id` records the requested canonical Interface or temporary pre-removal Alias, `canonical_interface_id` records the canonical Interface target, and exactly one of `semantic_error_code` or `kernel_error_class` is present. Trace identity remains absent until a safe source exists. Alias handlers enter the same canonical invocation while retaining the Alias as the requested identity. Implementation text, causes, payloads, panic data, configuration, credentials, Secrets, and internal Kernel detail codes are excluded. The JavaScript wrapper validates the outer Connect code, exact operation identities, declared semantic-code set, closed Kernel class, detail count, fields, and unknown wire data before exposing an immutable Plystra-owned `error.detail`. A missing, malformed, duplicate, unknown, mismatched, or undeclared detail fails closed to the generic `internal` error without leaking the raw Connect error.

Construct semantic errors with invocation.NewSemanticError(code, cause) from
github.com/plystra/kernel/invocation. Use errors.As with
*invocation.SemanticError and Code() locally; structural SemanticErrorCode
methods are not recognized. Wrap uncertain effects with
invocation.NewResultUnknown(cause) before semantic translation. Generated
error projection follows ordinary wrapping and joins up to 64 unwrap levels
and 1,024 nodes, rejects conflicting or undeclared codes, and exposes no
private cause. Completion remains independent of the primary code through
Connect, transitional HTTP, and PlystraError.completion in the SDK.

Completion is independent of the primary error code: not_started,
result_known, or result_unknown. The wire completion field is required, and
the SDK exposes PlystraError.completion as well as detail.completion.
Predispatch cancellation is not_started; in-flight interruption or an
untrusted failure is result_unknown. An uncertain semantic error does not
prove rollback or permit automatic resubmission.

Every final Alias whose normalized exposure includes JavaScript generates a nested method and tree-shakable factory under its Alias ID. The Alias module imports the canonical target's exact request, response, semantic errors, validators, codecs, and contract digest, then resolves the Alias service descriptor while reusing the canonical request and response messages. Several aliases may reuse one target without copying its schema or provider details. Alias exposure cannot broaden the target, and deprecated aliases emit native TypeScript `@deprecated` declarations without deprecating the target.

The generated package includes a CLI-owned `.npmrc` that prevents ordinary `npm install` from creating `package-lock.json`; a lockfile below `generated/sdk/javascript/` is unexpected generated drift rather than authored project state.

## Generated application API documentation

The CLI renders `generated/docs/api.md` and OpenAPI 3.1 JSON at `generated/docs/openapi.json` from the same provider-independent canonical contracts and final Alias map. Both outputs list exact HTTP routes, strict request and response schemas, semantic errors, target contract digests, and direct Alias targets. HTTP-only narrowed aliases remain documented even when excluded from the browser SDK; deprecated aliases are marked without changing or deprecating the canonical target. Operation IDs remain deterministic when distinct Capability names normalize to the same identifier, and provider identities, runtime configuration, verified internal context, and Secret values are excluded.

## Method-specific login surfaces

Authentication methods use real contracts such as:

```text
authn.login.password/v1
authn.login.passkey/v1
authn.login.oidc.begin/v1
authn.login.oidc.complete/v1
```

When exactly one login method is resolved and explicitly exposed, generated Go, HTTP, and JavaScript surfaces may add the application-local `authn.login/v1` Alias with that method's exact contract. The Alias is not a canonical Capability, Kernel registry entry, provider requirement, or distributed contract. Several methods produce no implicit Alias.

## Project creation

Project creation is non-interactive by default. It generates version-matched
Plystra Agent guidance under `.agents/skills/plystra/`, while Git initialization
and GitHub Actions CI default off:

```powershell
plystra new my-app
plystra new my-app --module github.com/acme/my-app
plystra new my-app --module github.com/acme/my-app --template github.com/acme/platform@v1.2.3
plystra new my-app --module github.com/acme/my-app --format json
```

The positional value is one lower-case ASCII kebab-case child-directory name.
The first command creates `./my-app/` with `module my-app`; `--module` changes
the `go.mod` identity and generated imports without changing that directory.
Unsafe names, paths, traversal, separators, and an existing target fail before
filesystem mutation. An explicit module path must satisfy standard Go Module
rules. Invalid Project names and module identities emit
`PLYSTRA_PROJECT_CREATE_NAME_INVALID` and
`PLYSTRA_PROJECT_CREATE_MODULE_INVALID`. The removed positional
full-module-path form is not accepted. An existing Project file, directory, or
symbolic target emits `PLYSTRA_PROJECT_CREATE_TARGET_EXISTS`; the CLI preserves
it and directs recovery to a different Project name or parent directory.

An initial `--plugin` value is validated before target staging. Invalid or
reserved names emit `PLYSTRA_PROJECT_CREATE_PLUGIN_NAME_INVALID`; inputs that
cannot derive one canonical Plugin ID emit
`PLYSTRA_PROJECT_CREATE_PLUGIN_ID_INVALID`. Both recoveries rerun
`plystra new` with placeholders rather than switching to the post-creation
Plugin command or echoing rejected input.

`--template` rejects a malformed query before staging with
`PLYSTRA_PROJECT_CREATE_TEMPLATE_INVALID`. It resolves one standard Go Module
query, requires the selected module to expose a regular root `plystra.yaml`
Project marker, and records that module as one ordinary direct dependency.
Dependency source and configuration remain inactive; no relationship, ancestry,
special template state is persisted.
The `template` configuration field is unknown and is rejected like any other
unknown root field. A markerless selected module leaves no target Project.

Use `--git` and `--github-ci` to opt into those independent tools. Use
`--no-agent-guidance` to omit the default guidance. Only `--interactive` permits
prompts for omitted Git and GitHub CI choices; terminal detection never prompts.
Each requested prompt defaults to yes and accepts `yes`/`y`, `no`/`n`, or Enter.

Human output is the default. `--format json` writes exactly one canonical
`plystra.result/v1` document to stdout. Successful creation returns `changed`
with exit class `0`, one observed `project_write`, and a nested
`plystra.project-created/v1` payload containing the module path and safe
relative `directory`. Enter that directory and run `plystra check` through the
public CLI before using the Project. Structured failures keep diagnostics,
typed recovery, and all four effect-disposition arrays in the same document;
invalid invocations use exit class `2`, validation failures `3`, missing
prerequisites `4`, cancellation `5`, and execution failures `8`.

```powershell
plystra new my-app --module github.com/acme/my-app
plystra new my-app --module github.com/acme/my-app --git --github-ci
plystra new my-app --module github.com/acme/my-app --interactive
plystra new email --module github.com/acme/email --no-agent-guidance
```

This permits, for example, generating GitHub CI inside a project directory
already governed by a parent repository without initializing a nested
repository. Requested Git initialization creates an empty repository on branch
`main`; requested CI emits `.github/workflows/ci.yml`.

The guidance projection contains a small `SKILL.md`, six task-scoped references,
and `manifest.json` with schema `plystra.agent-guidance/v1`. The manifest records
the installed CLI version, supported Kernel version, specification revision,
catalog digest, and exact digest of every CLI-owned guidance file. The CLI does
not own optional `.agents/skills/plystra/local.md`, unlisted files, sibling
skills, or repository-wide instructions. Generated guidance contains no Git,
branch, commit, review, release, or team workflow rules. `plystra new --help`
documents the complete creation contract.

Manage the projection through the public lifecycle commands:

```powershell
plystra guidance check
plystra guidance sync
plystra guidance sync --replace-generated
```

`plystra guidance check` compares the installed catalog, prior manifest, and
owned paths without changing the Project. An initial `plystra guidance sync`
installs the catalog only when every desired path is unoccupied. With an
existing manifest, ordinary sync refreshes or removes only prior-owned files
that still match their recorded digests. A desired catalog path absent from the
previous manifest blocks sync whether that path is missing or already occupied.
Any blocking drift leaves every Project file unchanged.

`--replace-generated` may discard edits only in existing bounded regular files
owned by the prior manifest. Missing prior-owned paths and desired paths absent
from prior manifest ownership block both sync modes. Restore one complete
matching generated projection or move an occupied conflict first. Neither mode
creates, edits, deletes, or claims `local.md`, other unlisted files, sibling
skills, or repository-wide Agent instructions.

Drift reports `PLYSTRA_AGENT_GUIDANCE_DRIFT` with each affected path as an
`agent-guidance` source. An invalid or unsafe ownership manifest reports
`PLYSTRA_AGENT_GUIDANCE_MANIFEST_INVALID`. If a transaction path changes after
inspection, the command reports `PLYSTRA_PROJECT_CONCURRENT_CHANGE` with every
deterministically known affected guidance path and does not claim a successful
refresh.

If Git cannot be started, creation emits
`PLYSTRA_PROJECT_CREATE_GIT_UNAVAILABLE` with exit class `4`. If Git starts but
`git init` fails, it emits `PLYSTRA_PROJECT_CREATE_GIT_INITIALIZATION_FAILED`
with exit class `8`. Both remove the staged tree, leave no target Project, and
direct the caller to correct Git and retry with `--git` or omit `--git` when no
repository is intended.

Successful creation with `--template` reports the selected query:

```text
Created my-app with dependency github.com/acme/platform@v1.2.3
Configuration scaffolded
Generated and tested

Next:
  cd my-app
  plystra check
```

The concise output names the result and next action without exposing absolute
paths or internal resolution detail. Creation generates the selected Project
and runs its Go package tests before installation. It does not claim a separate
build, startup health check, or JavaScript SDK qualification. Those checks can
be run independently where applicable. Complete public `plystra dev` and
`plystra build` workflows remain future acceptance work; `--template` itself
does not qualify or activate the selected dependency.

## Transaction safety

New project trees, template dependency metadata, optional CI and skill files, and requested Git initialization are populated and validated in a same-parent staging directory before rename. A template resolution, generation, validation, or Git initialization failure leaves no target project. In-place changes use same-filesystem staged replacements and backups, reject unsafe symbolic traversal, recheck source snapshots, preserve concurrent user edits, and restore original bytes and modes after validation failure or panic.

Commands below a module root use the nearest real enclosing `go.mod`; nested modules do not leak mutations into an outer module. The Module Cache remains read-only.

The internal `changerequest` package parses `plystra.change/v1` intent for
Interface and Implementation creation, dependencies, configuration, Interface
roots, and Implementation and Resource selections. Parsing is independent of
Project I/O: it validates closed fields and target identities, normalizes exact
decimal values and operation order, and returns private canonical JSON and a
SHA-256 request digest. Bounds are 1 MiB for input and normalized JSON, 1,024
operations, 64 JSON levels, 65,536 value nodes, 4,096 decoded bytes per string or
key, and 1,024 bytes per Implementation package path. Duplicate keys and
conflicting targets fail without exposing submitted configuration values.
This parser does not establish typed final-state validation or install changes;
public `change plan`, `change apply`, and primitive-command lowering remain
unavailable.

## Authoring behavior

`PLYSTRA_INTERFACE_CREATE_TARGET_EXISTS` reports an occupied target as a
module-relative `authored-package` path without a fabricated span. When another
visible package defines the requested ID, it reports that owning current or
dependency Project's `interface-declaration` and exact directive span. Choose
a different unversioned Interface name; the command never changes the existing
source or a dependency cache copy.

Create the initial `/v1` package for one canonical unversioned Interface name:

```powershell
plystra interface create email.send
```

The name has at least two lower-case dot-separated segments. Creation fails
without mutation when the name is invalid or its local package or visible ID
already exists.

Scaffold an ordinary Go package for one visible canonical Interface from the
Project root or any nested authored package:

```powershell
plystra implement email.send/v1 --package ./smtp
```

The package path is a canonical Project-relative path beginning with `./`, and
the target package must not already exist. The command accepts either a visible
Interface or Resource ID and infers the contract kind. Interface scaffolds
import the canonical contract, create `Service` and `New`, add the exact
`//plystra:implements` directive, scaffold the operation method, and add a
compile-time assertion. Resource scaffolds use a concrete `Provider`; `New`
returns `errNotImplemented`, and every provider method panics with that error
until authored. Resource scaffolds contain no guessed Config, dependencies, or
lifecycle hooks, create no selected instance, and do not activate the provider.
Neither kind copies the contract, writes configuration or registration, or
creates generated output. Plystra rediscovers and type-checks the new contract
before committing; any failure removes the complete scaffold.

Creation diagnostics distinguish invalid, missing, ambiguous, or inaccessible
Interface and Resource IDs, unsafe Implementation package paths, and existing
targets. Each failure emits one recovery command or replacement choice and
leaves the Project unchanged. A successful scaffold is explicitly unfinished
and inactive; author behavior before using it in a generation workflow.

Plugin-target inference resolves an explicit target, the enclosing plugin, or
the only local plugin without prompting. Only an explicit `--interactive`
permits a terminal choice when several local plugins remain; terminal detection
alone never activates interaction. Non-interactive ambiguity fails with every
candidate and accepts `--plugin <directory-or-plugin-id>` as the complete
explicit form. Requested interaction without a terminal fails deterministically
with `PLYSTRA_PLUGIN_TARGET_INVALID` before mutation.

Capability identities use `<capability-name>/v<number>`. Names contain at least two dot-separated lower-case segments, may use any logical hierarchy depth, and never imply a fixed namespace/operation split.

Capability creation and implementation update schemas, `plugin.yaml`, generated contracts, providers, clients, application invocation, adapters, assembly, SDKs, docs, and manifests in one transaction. Existing user implementations are never overwritten.

Plugin and Capability mutations reject desired-path ownership conflicts, unexpected unowned files, and manually modified prior-owned files under `generated/`. They report the conflicting paths as module-relative `generated-artifact` sources, preserve those entries, and roll back every CLI-owned declaration, source, module-metadata, and generated-output change instead of returning success beside immediate generation drift.

Create a genuinely new Capability identity from inside the target plugin, from a single-plugin module, or with an explicit target by choosing an intent profile:

```powershell
plystra capability create records.create --query
plystra capability create records.archive --query --plugin records
plystra capability create records.read --query --plugin records --expose
```

`--query` expands into complete explicit read-only, inherently idempotent, safely retryable, best-effort-cancellable, completed-before-return semantics with public request and response data. Names never imply semantics. A new Capability identity requires one supported profile before any mutation.

An omitted version selects `v1` when none is visible. When the identity is already visible, it selects one above the highest visible version and copies that exact contract, including its semantics, as an editing base; omit profile flags for that later-version workflow. An explicit older or skipped new version is rejected without mutation under `PLYSTRA_CAPABILITY_CREATE_CONFIRMATION_REQUIRED` until the same create command is deliberately repeated with `--confirm`. If the highest visible major is already `18446744073709551615`, omitted-version creation emits `PLYSTRA_CAPABILITY_CREATE_VERSION_EXHAUSTED` before mutation; use a new canonical Capability identity because no higher major exists. An existing exact version is never recreated; implement it instead:

```powershell
plystra capability implement email.send/v1 --plugin mailer
```

For a genuinely new name, creation reports conservative typo-like visible exact Capabilities as advisory recommendations. It never redirects or blocks the requested custom identity based only on similar spelling.

Implementation searches local and effective-graph dependency Project contracts, requires exact provider-independent equality including closed field constraints, typed semantics, and normalized extension metadata, copies the canonical schema when the target plugin does not yet provide it, adds a compile-safe user-owned method only when absent, regenerates all affected module surfaces, tidies module metadata, and validates with `go test -mod=readonly ./...`. Repeating the command preserves an existing method byte-for-byte.

If visible Providers disagree on the source exact contract, both creation of a
later version and implementation of that exact version stop before mutation
with `PLYSTRA_CAPABILITY_SCHEMA_CONFLICT`. The diagnostic reports every
conflicting `capability.yaml` as an owning-module, module-relative source and
never exposes the local checkout or Module Cache path.

In a Plystra Project, expose an existing exact canonical Capability or create and expose a new one in the same transaction:

```powershell
plystra capability expose records.create/v1
plystra capability expose records.create/v1 --env production
plystra capability expose records.create/v1 --config deploy/customer-a.yaml
plystra capability create records.update --query --plugin records --expose
```

`capability expose` requires an exact `<capability-name>/vN`. With no selector it updates root `plystra.yaml`; `--env production` updates only the sparse `plystra.production.yaml` overlay; and `--config deploy/customer-a.yaml` updates only that complete replacement document. `PLYSTRA_ENV` and `PLYSTRA_CONFIG` provide the same two selector modes when neither flag is present, while either explicit flag overrides both ambient variables. The command preserves comments, unrelated values, and exact add/remove tombstones, then regenerates every affected Go, HTTP, JavaScript, documentation, assembly, and manifest surface with the same selected configuration. Invocation from a nested Plugin still resolves relative configuration paths from the Project root. Repeating the command is byte-idempotent when generated output is current, and no unselected configuration file is synchronized.

Malformed `capability create`, `capability implement`, and `capability expose`
references emit `PLYSTRA_CAPABILITY_CREATE_REFERENCE_INVALID`,
`PLYSTRA_CAPABILITY_IMPLEMENT_REFERENCE_INVALID`, and
`PLYSTRA_CAPABILITY_EXPOSE_REFERENCE_INVALID` respectively. Each is rejected
before Project discovery or mutation. Recovery uses canonical placeholders
instead of copying the rejected reference, and exposure recovery retains only a
safe default, environment, or complete-replacement selector.

A well-formed exact `capability expose` target must already be present in the
selected visible canonical catalog. An absent target emits
`PLYSTRA_CAPABILITY_EXPOSE_NOT_VISIBLE` before write planning or mutation;
recovery keeps a safe selector, substitutes the Capability placeholder, and
leaves every Project byte unchanged.

A well-formed exact version sent to the wrong authoring action is also
classified. `PLYSTRA_CAPABILITY_CREATE_ALREADY_VISIBLE` switches creation of an
existing exact contract to `capability implement`, while
`PLYSTRA_CAPABILITY_IMPLEMENT_NOT_VISIBLE` switches implementation of a missing
exact contract to `capability create`. Both failures leave the Project
unchanged and preserve the original problem wording above one placeholder-based
recovery command.

An explicit older or skipped new version emits
`PLYSTRA_CAPABILITY_CREATE_CONFIRMATION_REQUIRED`. Review the visible version
history and repeat the same `capability create` command with `--confirm`; the
unconfirmed request leaves every Project byte unchanged.

An omitted version cannot advance an identity whose highest visible major is
already `18446744073709551615`. That failure emits
`PLYSTRA_CAPABILITY_CREATE_VERSION_EXHAUSTED`, retains the existing inference
problem, and directs creation to a new canonical Capability identity without
changing the Project.

Intent-profile mistakes are distinct. A new identity without a profile emits
`PLYSTRA_CAPABILITY_CREATE_INTENT_PROFILE_REQUIRED` and recovers by adding
`--query`; a later version supplied with a profile emits
`PLYSTRA_CAPABILITY_CREATE_INTENT_PROFILE_NOT_ALLOWED` and recovers by omitting
`--query`. Both fail before mutation.

Select a constructor for a visible Interface or an existing named Resource instance through the same public workflow:

```powershell
plystra use email.send/v1 example.com/acme/email/smtp.New
plystra use email.send/v1 example.com/acme/email/production.New --env production
plystra use email.send/v1 example.com/acme/email/customer.New --config deploy/customer-a.yaml
plystra use database.primary example.com/acme/postgres.New --env production
```

`plystra use <target> <constructor-symbol>` infers the target kind without a flag. A canonical Interface ID includes `/vN`; a Resource target is an existing exact instance name, not a Resource contract ID or a request to create an instance. The default form writes root `plystra.yaml`; `--env` writes only the selected sparse overlay; and `--config` writes only the selected complete replacement document. `PLYSTRA_ENV` and `PLYSTRA_CONFIG` select the same targets when no explicit flag is present, while an explicit selector overrides both variables.

An Interface choice writes `interfaces.use` and requires a visible compatible fully qualified Implementation constructor. Intrinsic Interfaces, unknown targets or constructors, and incompatible constructors fail without mutation. A valid choice may be recorded before the Interface is required; it remains dormant without activating the constructor or emitting its generated runtime. Its exact `config.<constructor-symbol>` object is type-validated immediately, but Secret references remain unresolved and enter no runtime, bootstrap, or Kernel state.

A Resource choice writes `resources.instances.<name>.use`. Replacing a nonempty previous provider discards only that instance's old Config; selecting the same provider preserves it. An existing instance without a provider can receive its first selection, retaining unbound Config authored in the selected document. The command removes configuration whose last explicit or reachable owner disappears and only provably obsolete consumer binding parameters. It preserves other instances and still-owned configuration, revalidates surviving dependencies, and never guesses a replacement binding or newly required value. Cleanup writes only the required tombstones or sparse deltas in the selected current-Project layer; lower-layer sources and unselected documents remain unchanged.

The command validates the repaired final state, not a required valid starting graph. It preserves comments and unrelated YAML, regenerates with the same selection, is byte-idempotent when already selected, and restores the selected YAML, generated tree, `go.mod`, and `go.sum` after a later failure. This installed transaction slice does not provide compound change plans, plan digests, `--dry-run`, or complete Gate 13 acceptance.

An ordinary Go Module without root `plystra.yaml`, an absent visible contract or provider, a missing or unsafe selected file, conflicting selectors, concurrently changed configuration, unexpected generated output, generation failure, untidy module state, or validation failure leaves the selected configuration and every generated or module-owned file unchanged. `capability create --expose` remains the default-configuration authoring shortcut and uses the same rollback boundary for the new schema, Plugin declaration, implementation scaffold, root application exposure, module metadata, and generated output.

Generation always emits the contract and provider interface for every Capability provided by a local plugin, even before the application requires that Capability. This keeps user-owned provider implementations buildable while they are being authored. Clients, invocation paths, HTTP adapters, SDK operations, documentation, provider selection, and Kernel registration remain requirement- and exposure-driven, so an unrequired local Capability does not enter the runnable application surface.

Add one ordinary Go Module dependency from the Project root or any nested Plugin directory:

```powershell
plystra add github.com/acme/email@v1.4.2
```

Remove a selected dependency with its exact module path and no version query:

```powershell
plystra remove github.com/acme/email
```

Update exactly one selected dependency to the query resolved by Go:

```powershell
plystra update github.com/acme/email@v1.5.0
```

Omit the version query to request Go's normal upgrade selection for that module. `plystra update` never performs an implicit whole-graph upgrade.

`plystra add` validates one module query, resolves it through ordinary Go tooling, and records the selected module as a direct requirement. `plystra remove` requires a module already selected in `go.mod`, uses ordinary Go tooling to remove it, and fails if regeneration or tidy would select it again. `plystra update` also requires an existing selection, preserves a direct requirement as direct, and verifies that the module remains selected. All three commands recompute discovery and the root selected current-Project model, regenerate, tidy, and validate the complete Project. They do not activate ordinary dependencies or rewrite authored YAML except for deterministic ownership cleanup: when a changed dependency removes a Resource constructor parameter, the selected layer removes only that obsolete binding leaf. Instances, surviving bindings, Interface selections, and unrelated YAML remain unchanged; overlay leaves receive sparse tombstones, local leaves are deleted, and the CLI never guesses a replacement provider or retargets an incompatible binding. A dependency change that invalidates the current Project's selected model fails and rolls the transaction back. A failed Go command, resolution, composition, generation, tidy, dependency postcondition, or validation step restores `go.mod`, `go.sum`, generated artifacts, and every other transaction-owned file without overwriting a concurrent user edit. The Go Module proxy and cache remain ordinary Go-tool boundaries; the CLI never copies or modifies dependency source.

Malformed `add` and `update` queries emit
`PLYSTRA_DEPENDENCY_ADD_QUERY_INVALID` and
`PLYSTRA_DEPENDENCY_UPDATE_QUERY_INVALID`; a malformed exact `remove` path emits
`PLYSTRA_DEPENDENCY_REMOVE_PATH_INVALID`. Each failure occurs before Project
discovery or mutation and supplies one placeholder-based corrected command.
A valid `remove` path or `update` query whose module path is absent from
`go.mod` emits `PLYSTRA_DEPENDENCY_REMOVE_NOT_SELECTED` or
`PLYSTRA_DEPENDENCY_UPDATE_NOT_SELECTED` before mutation. Recovery identifies
the required selected path or query without copying the supplied value.

## Public command surface

The intended command set includes:

```text
plystra new
plystra add
plystra remove
plystra update
plystra plugin create
plystra interface create
plystra implement
plystra capability create
plystra capability implement
plystra capability expose
plystra capability require
plystra use
plystra dev
plystra test
plystra build
plystra inspect capabilities
plystra inspect
plystra inspect modules
plystra inspect interfaces
plystra inspect resources
plystra inspect implementations
plystra inspect configuration
plystra inspect --verbose
plystra inspect --format json
plystra explain capability <capability-name>/vN
plystra explain capability <capability-name>/vN --verbose
plystra explain capability <capability-name>/vN --format json
plystra explain plugin <plugin-id>
plystra explain plugin <plugin-id> --verbose
plystra explain plugin <plugin-id> --format json
plystra explain config <field-path>
plystra explain config <field-path> --verbose
plystra explain config <field-path> --format json
plystra explain alias <alias-name>/vN
plystra explain alias <alias-name>/vN --verbose
plystra explain alias <alias-name>/vN --format json
plystra explain exposure <capability-or-alias-name>/vN
plystra explain exposure <capability-or-alias-name>/vN --verbose
plystra explain exposure <capability-or-alias-name>/vN --format json
plystra check
plystra fix
plystra generate
plystra generate --check
plystra generate --env <environment>
plystra generate --check --env <environment>
plystra generate --config <yaml-path>
plystra generate --check --config <yaml-path>
plystra doctor
plystra sdk link
plystra sdk pack
plystra sdk publish
plystra release
```

Mutating commands perform all derivable generation automatically. Build and generation never publish or release as a side effect.

`plystra inspect capabilities` is the Project-independent view of the installed
CLI distribution. Human output reports the CLI, supported Kernel, implemented
specification revision, Go requirement, running platform, global interaction and
output defaults, stable document and timeout bounds, command, selector, and
effect-class counts, transport-toolchain digest, and five-stage support summary.
It identifies the command argument, selector, effect-class, and transport
component details omitted from that filtered view.
`--format json` writes one canonical `plystra.result/v1` document whose payload
is `plystra.capabilities/v1`, including the exact 28 installed leaf commands and
their arguments, selectors, stable defaults, interaction modes, output formats,
all nine effect classes, and the complete 13-component transport toolchain
identity. Planned commands are absent. Each supported feature reports
independent `specified`, `parsed`, `generated`, `executed`, and `accepted`
states. The command ignores
`PLYSTRA_ENV`, `PLYSTRA_CONFIG`, the working directory, and invalid Project
state. Explicit `--verbose`, `--env`, or `--config` is invalid and reports
`PLYSTRA_INSPECT_CAPABILITIES_INVOCATION_INVALID`.

The payload's closed schema inventory reports `plystra.result/v1`,
`plystra.recovery/v1`, `plystra.inspect` version 1, and `plystra.graph` version
1 as available. The standalone diagnostic and continuation schema roles are
reported explicitly as unavailable; the CLI does not infer or publish an
identity for either unsupported role.

The current `plystra inspect` implementation is a read-only view over the same
selected application model used by generation and validation. Its default human
output reports the Project and selected configuration, Plugin and Capability
counts, AuthN/AuthZ activation, transports, readiness, and the matching
`plystra check` action. `--verbose` appends the complete deterministic resolution
evidence. `--format json` writes exactly one `plystra.inspect` v1 schema document
to stdout while progress and diagnostics remain on stderr. `plystra inspect
modules` shows the participating current and dependency Project module graph.
`plystra inspect interfaces` shows every visible authored and intrinsic Interface,
its owning module and stable source, active root requirements, selected
constructors and selection reasons, and required or available/unavailable
optional constructor dependencies; visible unconnected Interfaces remain
explicitly inactive. `plystra inspect implementations` shows every visible
constructor candidate, its active, dormant-explicit, or unselected state,
implemented Interfaces, declared and resolved dependencies, constructor-owned
configuration provenance, and reachable assembly membership. `plystra inspect
configuration` shows selected layers, redacted field summaries, ownership and
precedence, effective and overridden contributions, explicit removals, and
suppressed descendants. `plystra inspect resources` shows every visible consumer
Resource contract, its defining package and sources, exact `resource_id` and `contract_digest`
on `resource-contract` nodes, plus selected named instances and resolved bindings.
Inspection is read-only: it neither changes selection nor constructs values.
All five graph views use the versioned
`plystra.graph` v1 schema with project-relative source references and omit
resolved Secrets and unrestricted configuration values. The command accepts the same `--env`, `--config`,
`PLYSTRA_ENV`, and `PLYSTRA_CONFIG` selectors as generation and check.

Constructor dependency edges in both Interface and Implementation inspection
retain `parameter_name` and one-based `parameter_position`. These fields are
absent on other edges. Repeated dependencies on one Interface remain distinct;
human output names each parameter alongside its position. Implementation
inspection also includes `resource-contract` nodes with exact `resource_id`,
`contract_digest`, and declaration sources. Its `declares-dependency` edges use
reason `resource` for canonical Resource parameters, preserving each name and
position even when several parameters consume the same contract. These are
declarations, not Interface requirements, active bindings, or runtime instances.

Ordinary Implementation constructors may declare explicitly named canonical
Resource parameters alongside required and optional Interface dependencies.
Discovery validates unselected and dormant candidates, and inspection retains
their Resource dependencies without activating them. Malformed Resource-shaped
parameters fail with `PLYSTRA_IMPLEMENTATION_REQUIRED_RESOURCE_INVALID` and the
owning constructor source. Each reachable consumer needs an explicit compatible
named instance or exactly one compatible selected instance for implicit binding.
A dormant Implementation is not constructed, but its explicit binding addresses
must still name valid consumers, parameters, and targets.

Resource-only packages are discovered in the current and dependency Projects
through the same Go-selected source boundary as Interfaces. Declare one
non-generic defined Go interface named `Resource` with exactly one
`//plystra:resource data.database/v1` directive. IDs use the Interface identity
grammar without case folding. Duplicate identities retain every defining source.
Completed method sets cannot expose `Start`, `Stop`, `Shutdown`, or `Close`;
equivalent lifecycle controls also belong on providers, not consumer contracts.
Ordinary infrastructure types are supported without Interface projection rules
or `interface.yaml`. Digests include reachable public Go shapes, methods,
generic arguments and recursive references, but exclude locations, comments,
private implementation details and unrelated provider hooks. Graphs are bounded
to 64 type-reference levels and 65,536 public shape nodes.
Malformed declarations, contracts and duplicate IDs report
`PLYSTRA_RESOURCE_DECLARATION_INVALID`, `PLYSTRA_RESOURCE_CONTRACT_INVALID` and
`PLYSTRA_RESOURCE_ID_DUPLICATE`, respectively, with owning module-relative sources.
Provider discovery accepts one `//plystra:implements-resource <resource-id>`
directive on an exported non-generic package-level constructor. Its optional
first parameter is a same-package `Config` value; remaining parameters are
exact canonical Resource types with explicit nonblank, case-sensitive Go names.
It returns a concrete pointer plus `error`, and that pointer must be assignable
to the declared visible Resource. Multiple provider candidates remain inert:
discovery does not infer selection, create instances, or execute constructors.
Contracts and constructors use the current Project's effective Go graph,
including its selected versions and replacements, without rewriting module
files. Invalid configuration defaults retain field context but never their value.
Mixed Interface/Resource-provider directives, invalid signatures and failed
assignability produce `PLYSTRA_RESOURCE_PROVIDER_DECLARATION_INVALID` or
`PLYSTRA_RESOURCE_PROVIDER_INVALID` with owning source locations.
Installed capability facts distinguish contract and constructor discovery from
named-instance configuration, binding, generation, and execution. Read each
support stage independently; implemented stages do not imply `accepted: yes`.
Data schema/query generation and migration commands remain unsupported.

The CLI parses current-Project `data.members` assignments as exact member-ID
entries. Each entry requires one valid named `resource` and may name an `access`
instance. An environment overlay replaces an entire entry or removes it with
`{$remove: true}`; root and complete replacement documents cannot remove it.
This is configuration composition only. An effective active member makes
`plystra generate` and `plystra generate --check` fail with
`PLYSTRA_DATA_COMPILER_UNAVAILABLE` before changing the Project. Invalid entries
report `PLYSTRA_DATA_MEMBER_METADATA_INVALID` with their source location.
Compiler acquisition, assignment validation, generated access, and migrations
are not installed yet.

### Named Resource instances

The named-Resource lifecycle integration requires the CLI's exact Kernel pin,
which remains local rather than published. Local integration checks do not
establish ordinary remote module resolution or release acceptance; the final
pin and integrated validation must be checked before using this slice as a
published CLI/Kernel pair.

Author `resources.instances.<name>` in the selected configuration document.
Names are exact, case-sensitive, 1 through 128 ASCII bytes, matching
`[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?:\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*)*`.
Each entry selects one exact provider constructor with `use` and owns its typed
`config`. Resource provider Config belongs here, not under top-level `config`.
The same provider under two names creates two values, configuration owners, and
lifecycle members; every selected instance is active even when unconsumed.

Root configuration composes before one selected environment overlay; a complete
replacement is a single complete layer. An unchanged provider composes non-pointer fixed Config structs by field;
pointers and collections replace atomically. A config-only higher layer may
inherit `use`. Changing `use` replaces the whole instance and discards the old
configuration, even when field names match. Requiredness and defaults apply
after composition. Use the exact `{$remove: true}` tombstone to remove an
instance or binding leaf; null and empty mappings are not removal.

Bind exact case-sensitive Go parameter names through
`resources.bind.implementations.<constructor>.<parameter>` or
`resources.bind.instances.<consumer-instance>.<parameter>`. Each leaf names one
exact instance. Explicit targets must exist and implement the required Resource
contract. Without a leaf, exactly one compatible selected instance binds
implicitly; zero or multiple matches fail. Providers depend only on Resources;
Implementations may mix Resource and Interface dependencies. Removal never
silently retargets an explicit binding, and dangling addresses and cycles fail
before generation.

Generated assembly constructs each named instance once, shares it across bound
consumers, and starts dependencies before consumers. Shutdown and failure cleanup
reverse that order, including non-nil partial constructor results and values
whose Start never ran. Constructors only assemble values; acquisition belongs in
Start, and Stop must tolerate partial and never-started states. Resource calls
are ordinary typed Go calls, outside Interface roots, catalogs, governed proxies,
and transports. Runtime-only Config and Secrets stay out of public artifacts;
structural or build-visible edits require regeneration and rebuilding.

Use `plystra use <instance-name> <provider-constructor>` to replace a compatible
provider on an existing named instance. It applies deterministic ownership
cleanup, regenerates, and validates with the same selector. Resource
`plystra implement <contract> --package ./<package>` creates an unfinished,
inactive ordinary Resource provider scaffold. It does not create a named
instance, select a provider, infer configuration, or provide runtime behavior:
implement `New`, provider methods, configuration, dependencies, and lifecycle
hooks as appropriate before generation. Instance creation remains unsupported.
Installed `resource.provider.scaffold` reports the scaffold stages separately
from provider selection and full Resource acceptance; generated and executed
scaffold support does not establish `accepted: yes`. After manual declaration or binding edits, run `plystra generate`,
`plystra generate --check`, and `plystra check` with the same selector. Do not
hand-edit generated assembly. Installed `resource.provider.selection` support
does not establish Resource or Data acceptance; Gate 5 remains incomplete under the
current creation-only template contract.

### Explain resolution

`plystra explain capability <capability-name>/vN` is the corresponding causal
read-only view. For a required Capability it reports the selected ordinary
Provider or Kernel intrinsic, the exact `sole-provider`,
`current-project-replacement`, or `intrinsic-kernel` selection reason, its direct
module-relative source, and one
selector-matched configuration field that changes the Provider decision. A
visible but unrequired Capability reports that state and the selected
configuration's `capabilities.require` field. `--verbose` appends the complete
deterministic evidence; `--format json` writes one `plystra.result/v1` document
with a `plystra.explain/v1` payload, structured diagnostics, selector snapshot,
and `plystra.recovery/v1` actions. JSON stderr is empty after initialization;
human progress and diagnostics use stderr. The same explicit and ambient
configuration selectors apply, and unknown canonical Capability IDs fail
without modifying the Project.

`plystra explain plugin <plugin-id>` applies the same read-only evidence boundary
to Plugin selection. A root-level current-Project Plugin reports that membership
as its direct selection reason. A selected dependency Plugin reports every exact
Capability for which it is the Provider and the Provider-decision sources that
caused its inclusion. A visible unselected dependency Plugin reports whether an
alternate Provider won or none of its provided Capabilities is required, plus a
selector-matched configuration field that changes the decision. Unknown
canonical Plugin IDs fail without modifying the Project.

`plystra explain config <field-path>` traces one typed field through dependency
Project composition and the selected current-Project layer. Plugin fields accept
the operational dotted form `config.<plugin-id>.<field>` and are reported with
their canonical typed path. The result distinguishes effective values, explicit
removals, and descendants suppressed by an ancestor replacement or removal;
identifies the owning Plugin when applicable; reports every winning source; and
points to the exact selected current-Project document and field that changes the
decision. Configuration values and Secret-reference targets remain redacted in
concise, verbose, and JSON output.

`plystra explain alias <alias-name>/vN` traces one final application-local Alias
to its direct canonical target. It reports whether exposure is inherited or
narrowed, every compatible application declaration and selected
generation-extension contribution, and one selector-matched configuration field
or activation-Provider decision that changes the result. Unknown canonical Alias
IDs fail without modifying the Project; concise output omits contract digests and
complete generation evidence unless `--verbose` or `--format json` is selected.

`plystra explain exposure <capability-or-alias-name>/vN` reports why one visible
identity is public through HTTP or JavaScript, or why it remains internal. A
canonical Capability reports every effective `http.expose` source; an Alias
reports its direct target and every compatible application or generation source.
An internal Alias distinguishes its own narrowing from an internal canonical
target. The result identifies the selected `http.expose`, Alias, or
activation-Provider decision that changes the surface without exposing contract
digests in concise output.

All five explanation commands return exit `2` for invalid invocation or subject,
`3` for invalid Project state or a missing target, `4` for a required decision
or missing prerequisite, and `8` for an internal failure. The explanation codes
are `PLYSTRA_EXPLAIN_INVOCATION_INVALID`, `PLYSTRA_EXPLAIN_SUBJECT_INVALID`,
`PLYSTRA_EXPLAIN_TARGET_NOT_FOUND`, and redacted `PLYSTRA_EXPLAIN_FAILED`;
known resolution failures retain their source-bearing diagnostic codes.
Recovery identifies an exact source edit, finite choice, missing prerequisite,
or manual correction. Executable recovery includes a Project-relative working
directory and exact `argv`, never a shell string or unresolved placeholder.
Provider choices edit the selected `capabilities.use` field; the installed
`plystra use` command accepts Interface Implementations and named Resource
providers, not legacy Capability Plugin IDs. Implementation
ambiguity lists sorted constructors with selector-matched commands and an
independent verification command. No recovery action runs automatically.

Common actionable human CLI failures append exactly one `Recovery:` block after
the concise problem. Follow that one command or file edit before rerunning the
operation. The block is followed by one stable
`Diagnostic: PLYSTRA_<AREA>_<CONDITION>` code;
use that code as the machine-stable failure identity instead of matching the
human wording. A recovery command preserves the selected default, `--env`, or
`--config` mode, including a safe selector supplied through `PLYSTRA_ENV` or
`PLYSTRA_CONFIG`. Unsafe and absolute selector values are replaced by
`<environment>` or `<yaml-path>` placeholders instead of being copied into the
advice in commands that retain the older human recovery format. Explanation JSON
never marks placeholders executable. Unknown explanation failures are redacted
as `PLYSTRA_EXPLAIN_FAILED`; other command families retain their current error
protocols.

Classified failures with typed provenance insert one or more canonical source
lines between the problem and recovery:

```text
Source: <module>:<module-relative-path>[:<line>:<column>] (<kind>)
```

Multiple sources use the shared diagnostic-envelope order. The CLI prints the
owning Project module, never an absolute path or Module Cache path, and omits
rather than fabricates a line or column when structured provenance does not
provide one. Authored Interface and Implementation failures report their
declaration or package source. `PLYSTRA_CAPABILITY_REQUIREMENT_CONFLICT`
reports every source that requires one of the incompatible exact contracts.
`PLYSTRA_CAPABILITY_CONTRACT_CONFLICT` reports every visible Provider's
`capability.yaml` declaration carrying one of the conflicting exact contracts.
`PLYSTRA_CAPABILITY_SCHEMA_CONFLICT` reports both Provider declarations whose
source contract blocks Capability creation or implementation.
`PLYSTRA_CAPABILITY_MANIFEST_INVALID` reports the invalid authored Provider
`capability.yaml` at `1:1` as a `provider-declaration` source.
`PLYSTRA_PROVIDER_MISSING` reports every typed requirement source that made the
exact Capability necessary.
`PLYSTRA_PROVIDER_AMBIGUOUS` reports those requirement sources together with
every compatible Provider's `capability.yaml` declaration.
`PLYSTRA_PROVIDER_SELECTION_INVALID` reports every effective
`capabilities.use` declaration that created the rejected choice.
`PLYSTRA_PROVIDER_CONTRACT_CONFLICT` reports every reference-only requirement
source together with each conflicting Provider's `capability.yaml`
declaration.
`PLYSTRA_PROVIDER_CONTRACT_MISMATCH` reports every exact-contract requirement
source together with each incompatible Provider's `capability.yaml`
declaration.
`PLYSTRA_RESOLVE_MISSING_IMPLEMENTATION` reports every declaration or exposure
source that introduced the root Interface plus every requiring constructor on
the complete path to the missing binding. Constructor declarations use the
`implementation-constructor` kind.
`PLYSTRA_RESOLVE_MULTIPLE_IMPLEMENTATIONS` reports every compatible
Implementation constructor declaration as an `implementation-constructor`
source before selection recovery.
`PLYSTRA_RESOLVE_CONSTRUCTOR_CYCLE` reports every requiring constructor
declaration in the complete cycle as an `implementation-constructor` source.
The problem retains the ordered Interface edges and selection reasons.
`PLYSTRA_PROJECT_MANIFEST_INVALID` reports exactly one current or dependency
Project `plystra.yaml` as a `project-marker` source. A malformed readable
document uses the conservative `1:1` span; an unsafe or unreadable marker omits
the unavailable line and column. Correct the owning Project, or select a
corrected dependency version, rather than editing a Module Cache copy.
`PLYSTRA_RESOLVE_UNKNOWN_INTERFACE` reports every effective
`interfaces.require` or `http.expose` declaration as a `declaration` or
`exposure` source, or every effective `interfaces.use` declaration as an
`implementation-selection` source. A selected current-Project reference names
only the root, environment, or complete-replacement document. Equal values retain
distinct layer ownership rather than creating peer contributors.
`PLYSTRA_RESOLVE_RESERVED_INTERFACE` reports the application-authored
`kernel.*` Interface declaration as an `interface-declaration` source. The
source retains the owning current or dependency Project module and
module-relative Go path; remove that declaration in its owning Project and
import the canonical Kernel Interface package instead of editing a Module Cache
copy.
`PLYSTRA_RESOLVE_UNKNOWN_IMPLEMENTATION`,
`PLYSTRA_RESOLVE_INCOMPATIBLE_IMPLEMENTATION`, and
`PLYSTRA_RESOLVE_INTRINSIC_INTERFACE_SELECTION` report every effective
`interfaces.use` declaration as an `implementation-selection` source. A
current-Project choice identifies only the selected root, environment, or
complete-replacement document. For an intrinsic Interface, correct the owning document
to remove the local or lower-layer effective selection; Kernel supplies that
Interface intrinsically.
`PLYSTRA_PROJECT_CREATE_TEMPLATE_INVALID` reports an invalid template query or selected dependency with owning module-relative declarations. Correct the query or Go Module graph and rerun the same command.

Configuration-selection failures use
`PLYSTRA_CONFIGURATION_SELECTION_INVALID`. An explicit `--env` plus `--config`
pair is rejected before Project discovery or mutation; conflicting or duplicate
ambient selector variables, unsafe values, and missing selected documents use
the same code at the shared selection boundary. A normalized selected path that
stays within the Project but cannot be loaded emits exactly one path-only
`configuration-selection` source for the current Project, without a fabricated
span. Conflicting, duplicate, or unsafe selectors emit no source because no
selected document is trustworthy. The recovery action names no selector value
and directs the command to exactly one intended selection.

`plystra plugin create` distinguishes an invalid or reserved root-level name,
a Project module namespace that cannot form a canonical Plugin ID, and an
existing Plugin directory with `PLYSTRA_PLUGIN_CREATE_NAME_INVALID`,
`PLYSTRA_PLUGIN_CREATE_ID_INVALID`, and
`PLYSTRA_PLUGIN_CREATE_TARGET_EXISTS`. Each recovery uses placeholders rather
than echoing rejected input, and each failure leaves the Project unchanged.

Authored Interface failures distinguish an invalid `//plystra:interface`
declaration, canonical Go contract, optional `interface.yaml`, duplicate visible
ID, and an authored package that ordinary Go tooling cannot load. Correct the
reported `Source:` in its owning Project.

Authored Implementation failures distinguish an invalid
`//plystra:implements` declaration, unsupported `Config` shape, invalid required
or `plystra.Optional[T]` parameter, invalid constructor result, and structural
conformance failure. Follow the code-specific recovery against the reported
`Source:`; never edit a dependency's Module Cache copy.

Malformed `plystra capability create`, `plystra capability implement`, and
`plystra capability expose` references use
`PLYSTRA_CAPABILITY_CREATE_REFERENCE_INVALID`,
`PLYSTRA_CAPABILITY_IMPLEMENT_REFERENCE_INVALID`, and
`PLYSTRA_CAPABILITY_EXPOSE_REFERENCE_INVALID`. They fail before Project
discovery or mutation; recovery uses canonical placeholders and preserves a
safe exposure selector.

Non-interactive `capability create` and `capability implement` with several
valid local Plugins and no exact target emit `PLYSTRA_PLUGIN_TARGET_AMBIGUOUS`.
The diagnostic reports every candidate as a sorted current-Project
`plugin-declaration` source at its module-relative `plugin.yaml:1:1`, never an
absolute checkout path. Pass `--plugin <directory-or-plugin-id>` as complete
non-interactive input, or add `--interactive` to request a terminal choice.
Requested interaction without a terminal and failed selections emit
`PLYSTRA_PLUGIN_TARGET_INVALID`; absent explicit targets and failed selections
remain source-less because no existing declaration owns those failures.

`PLYSTRA_CAPABILITY_EXPOSE_NOT_VISIBLE` identifies a well-formed exact exposure
target absent from the selected visible canonical catalog. Classification
requires the owning exposure boundary and the preserved unknown-Interface
condition; recovery reruns `capability expose` with a visible placeholder and
the safe selected configuration, while the failed request changes no files.

Valid exact Capability IDs use `PLYSTRA_CAPABILITY_CREATE_ALREADY_VISIBLE` when
`capability create` must become `capability implement`, and
`PLYSTRA_CAPABILITY_IMPLEMENT_NOT_VISIBLE` when `capability implement` must
become `capability create`. Neither action mismatch mutates the Project.

`PLYSTRA_CAPABILITY_SCHEMA_CONFLICT` identifies visible Providers that carry
different exact source contracts during Capability creation or implementation.
It reports both owning-module `capability.yaml` declarations before recovery,
leaks no absolute or Module Cache path, and leaves the Project unchanged.

`PLYSTRA_CAPABILITY_MANIFEST_INVALID` identifies an invalid authored visible
Provider contract while resolving the application or preparing Capability
creation, implementation, or exposure. It reports the owning module-relative
`capability.yaml` at `1:1` as a `provider-declaration` source, preserves the
typed manifest failure, exposes no checkout or Module Cache path, and fails
before mutation.

`PLYSTRA_CAPABILITY_CREATE_CONFIRMATION_REQUIRED` identifies an explicit older
or skipped new version that must be reviewed and repeated with `--confirm`.
Classification requires the owning create-operation boundary and occurs before
mutation.

`PLYSTRA_CAPABILITY_CREATE_VERSION_EXHAUSTED` identifies an omitted-version
create request whose visible identity already uses the maximum unsigned 64-bit
major. Classification requires the create, authoring-exhaustion, and low-level
overflow conditions together; recovery creates a new canonical Capability
identity, and the failed request does not mutate the Project.

`PLYSTRA_CAPABILITY_CREATE_INTENT_PROFILE_REQUIRED` identifies a new identity
without `--query`; `PLYSTRA_CAPABILITY_CREATE_INTENT_PROFILE_NOT_ALLOWED`
identifies a copied later version that must omit `--query`. Both failures are
classified before mutation.

`plystra use` rejects malformed targets with `PLYSTRA_USE_TARGET_INVALID` and
malformed fully qualified constructor symbols with `PLYSTRA_USE_CONSTRUCTOR_INVALID`
before Project discovery. A well-formed target absent from the selected model
reports `PLYSTRA_USE_TARGET_NOT_FOUND`; an incompatible Resource provider reports
`PLYSTRA_USE_PROVIDER_INCOMPATIBLE`. Recovery uses safe placeholders and retains
the selected default, environment, or complete-replacement mode. Failed
selection leaves the Project unchanged and exposes no configuration values or
Secret-reference targets.

Constructor-keyed configuration whose constructor has no discovered compiled
same-package `Config` schema fails with
`PLYSTRA_CONSTRUCTOR_CONFIGURATION_SCHEMA_INVALID`. Select a discovered
constructor with a compiled schema or remove the configuration entry in the
reported owning Project document. The diagnostic emits exactly one
`configuration-declaration` source at `1:1` and never prints configured values,
Secret-reference targets, absolute paths, or Module Cache paths.

Constructor-keyed configuration whose value does not match its discovered
compiled same-package `Config` schema fails with
`PLYSTRA_CONSTRUCTOR_CONFIGURATION_VALUES_INVALID`. Correct the reported safe
field path in the owning current or dependency Project document. The diagnostic
emits exactly one `configuration-declaration` source at `1:1` and never prints
configured values, unknown authored keys, Secret-reference targets, absolute
paths, or Module Cache paths.

A malformed selected environment or complete-replacement document fails with
`PLYSTRA_CONFIGURATION_INVALID`. Correct the emitted `Source:` in the selected
current-Project document. The diagnostic reports it at `1:1` as a
`configuration-declaration` source without exposing configured values or an
absolute path.

A selected sparse environment overlay whose typed application over root
configuration is invalid fails with `PLYSTRA_ENVIRONMENT_OVERLAY_INVALID`.
Correct the reported relationship in the selected `plystra.<environment>.yaml`.
The diagnostic emits that current-Project document at `1:1` as a
`configuration-declaration` source without exposing values or an absolute path.

Constructor-keyed configuration whose constructor is neither named by an
effective `interfaces.use` choice nor reachable from an active Interface fails
with `PLYSTRA_CONSTRUCTOR_CONFIGURATION_UNSELECTED`. Name that constructor in an
effective choice, make it reachable through an Interface requirement, or remove
the configuration object from the selected document. The diagnostic reports
every effective contributing Project document at `1:1` as a sorted
`configuration-declaration` source; it never prints configured values or
Secret-reference targets.

The current `plystra check` implementation is read-only. It validates the
selected application model and generated fixed point, then runs
`go test -mod=readonly ./...` from the Project root. Later roadmap gates add
the remaining transport, JavaScript SDK, formatting, race, and release-era
checks without changing this command or its configuration selectors.

`plystra plugin create` keeps the new scaffold, generated module surfaces, and Go module metadata in one rollback boundary. It runs `go mod tidy` after generated imports exist, retains explicit pre-existing requirements and checksum entries, validates with `go test -mod=readonly ./...`, and rolls back its own `go.mod`, `go.sum`, generated-file, and scaffold changes if any later check fails. Concurrent user edits remain protected.

### Module generation

From any directory inside a Plystra Project, install its complete current managed tree with:

```powershell
plystra generate
```

Select one sparse environment overlay above root `plystra.yaml` with:

```powershell
plystra generate --env production
plystra generate --check --env production
```

The selector requires project-root `plystra.production.yaml`. `PLYSTRA_ENV=production` is its automation equivalent; no overlay is loaded when the selector is absent.

Select one complete alternative current-Project document with:

```powershell
plystra generate --config deploy/customer-a.yaml
plystra generate --check --config deploy/customer-a.yaml
```

`PLYSTRA_CONFIG=deploy/customer-a.yaml` is the automation equivalent when the option is omitted. Setting `PLYSTRA_ENV` and `PLYSTRA_CONFIG` together is an error. An explicit `--env` or `--config` overrides both variables, and the two options cannot be combined.

The command resolves mandatory root metadata, the effective Go Module graph, and the selected current-Project model. Ordinary dependency roots contribute discovery, not application intent. Only the root, one selected environment overlay, or one complete replacement document contributes configuration; dependency overlays and deployment settings are never read.

Generation preserves authored YAML bytes and never materializes lower-layer values. It installs generated output and required module metadata in one transaction. Invalid selected configuration, concurrent input changes, validation failure, or nondeterministic output rolls back CLI-owned changes while preserving concurrent user edits.

Go subprocesses preserve an explicit `GOWORK` selection. An automatically discovered enclosing `go.work` remains active when it validly includes the nearest module; when it is valid but does not list that module, the CLI runs the subprocess with `GOWORK=off` so an unrelated parent workspace cannot redirect generation or validation. Malformed workspaces, missing `use` directories, and invalid used modules remain active so the Go tool reports the original workspace error instead of having it hidden.

Use the read-only consistency gate in local checks and CI:

```powershell
plystra generate --check
```

Check mode never writes module files or configuration. It reports deterministic
`stale`, `missing`, `unexpected`, and `manually-modified` generated paths for
the exact selected current-Project composition and layer.
Switching selections, changing selected current-Project model, or changing a build-affecting
selected value changes generated provenance and output. The command returns a
failing exit status while any drift remains. Installation preserves an
unexpected unowned file rather than overwriting or deleting it.

## Development

The complete contributor and Plystra-module workflow is in
[`docs/development-guide.md`](docs/development-guide.md). It records the actual
end-of-Gate-9 command surface, generated ownership rules, operational examples,
troubleshooting, and intentionally deferred roadmap work.

```powershell
$env:GOWORK = "off"
go mod download all
go mod download github.com/go-logr/logr@v1.2.2 go.opentelemetry.io/otel/metric/x@v0.68.0
go test -timeout=40m ./...
go test -race -timeout=40m ./...
go vet ./...
go run ./cmd/plystra --help
go test ./internal/generationresolution -run '^$' -bench '^BenchmarkGenerationFixedPoint$' -benchmem
go test ./internal/clientgen -run '^$' -bench 'BenchmarkGenerated(CanonicalInvocation|AliasForwarding)$' -benchmem
go test ./internal/httpgen -run '^$' -bench '^BenchmarkGeneratedHTTPInvocation$' -benchmem
```

CI prepares dependencies before offline generated-Project tests, including the
older `logr` graph metadata and the telemetry SDK's test-only metric module.
These fixtures also prepare their dependencies when run independently. Scaffold
tests retain isolated module caches and file proxies while reusing Go's
content-addressed build cache. CI package timeouts are 60 minutes on Windows
and 20 minutes on Linux, macOS, and the Linux race job; these are cumulative
package limits, not per-test limits.

Lifecycle smoke tests compare the configuration root with the child working
directory by filesystem identity, including symbolic directory paths and a
stale inherited `PWD`. Equivalent macOS temporary-directory spellings do not
indicate a wrong root; all smoke and baseline arguments remain checked.

The checked-in JavaScript golden package is validated with:

```powershell
cd internal/javascriptgen/testdata/canonical
npm ci --ignore-scripts --no-audit --no-fund
npm run typecheck
npm run build
npx --no-install tsc -p test/tsconfig.json
node --conditions=browser --test test/runtime.test.mjs
npm pack --dry-run --json
```

`BenchmarkGenerationFixedPoint` measures a three-pass selected-extension closure that activates AuthN and derives one ordinary audit requirement through the real resolver with an in-process test extension helper. The two generated-client benchmarks use identical no-op canonical target work. `BenchmarkGeneratedCanonicalInvocation` measures the canonical generated client and invocation path; `BenchmarkGeneratedAliasForwarding` adds exactly the application-local Alias client layer. `BenchmarkGeneratedHTTPInvocation` measures the generated strict JSON transport, root context, canonical application invocation, and response serialization around a no-op in-process provider. Raw Kernel canonical dispatch remains a separate `kernel` benchmark and is not folded into these CLI results.
