# Plystra CLI

`github.com/plystra/cli` builds the user-installed `plystra` command. It is the developer, resolution, generation, assembly, validation, build, test, and delivery half of Plystra Core.

The CLI is a separate Go Module from `github.com/plystra/kernel`. It completes build-time work and emits deterministic Go and JavaScript source targeting the Kernel's versioned assembly API; it is not a second runtime.

## Ownership

The CLI owns:

- Go Module, Plugin, and Capability creation.
- Root-level Plugin scanning, effective-graph dependency-Project discovery, and typed dependency configuration composition.
- The complete normalized application model.
- Official and intrinsic Capability discovery.
- Exact requirement closure and ordinary provider resolution.
- Plugin-provided build-time rule discovery and validation.
- Generation-derived Capability requirements.
- Deterministic structured contribution validation and merging.
- Contracts, clients, providers, configuration, application invocation, adapters, assembly, bootstrap, JavaScript SDKs, documentation, and manifests.
- Transactional mutation, generated consistency, development, testing, building, and release preparation.

The CLI is the sole writer of final `generated/` source.

`generated/proto/wire-map.json` is committed, CLI-owned compatibility history
for canonical Capability request and response messages selected for Connect.
Generation assigns the lowest permitted positive field number, preserves that
assignment across declaration reordering, allocates new fields without
renumbering existing fields, and permanently reserves both the name and number
of every removed field. Scalar contract enums additionally receive a numeric
zero `*_UNSPECIFIED` sentinel and deterministic positive member numbers.
Existing members keep their assignments across declaration reordering and
additions; removed member names and numbers remain permanently reserved. The
ledger retains inactive canonical field and enum histories when Connect,
exposure, or an enum is later disabled. Capability Aliases reuse their
canonical target's messages and enums and never receive separate ledger
entries. The ownership manifest records the exact ledger digest, and
generation rejects a missing, manually changed, corrupt, or inconsistent prior
ledger instead of guessing. Never edit or delete this file; restore its exact
last committed content before regenerating. Generation also emits one
deterministic `.proto` schema for each selected canonical Connect Capability,
one service-only Alias schema that imports the canonical messages, and a
self-contained `generated/proto/descriptor-set.pb` containing any required
well-known descriptors. A Project without a selected Connect surface retains a
valid empty descriptor set. These schema and descriptor files are CLI-owned,
exclude Provider, Plugin, Go Module, configuration, and Secret data, and are
checked for drift with the rest of `generated/`; never edit them manually.
For every selected Connect surface, generation also emits a Go handler under
`generated/go/adapters/connect/`. Canonical handlers bind one exact procedure
to the generated canonical application-invocation handle; Alias handlers are
thin forwards to that canonical handler and never create a Provider or Alias
dispatch entry. The current Connect boundary accepts canonical contracts whose
explicit `semantics.kind` is `query` or `command` and projects each as one unary
procedure; an Alias reuses that canonical target. Selecting an `event` or
`stream` for Connect fails before generated output and names the Capability,
declared kind, supported unary kinds, and `http.expose` remediation. Do not
relabel an event or stream to bypass this validation.
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
an optional non-nullable `null` becomes absence, explicit zero values retain
presence, full-range integers remain exact, and non-finite canonical numbers
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
the same ProtoJSON name or generated enum type. The diagnostic names the
Capability, message direction, both authored field names, and the colliding
identity; ordinary generation and `generate --check` fail without changing the
Project.

Capability inspection strictly parses the optional `extensions` mapping within the 1 MiB declaration boundary. The CLI preserves every valid lower-kebab namespace, including unknown namespaces, as immutable namespace-sorted canonical JSON-compatible metadata: object key order is normalized, scalar types and array order are preserved, and omitted and empty metadata are equivalent. Normalized extension metadata participates in exact contract equality, so providers cannot add, remove, or change generation-affecting behavior under one Capability ID; conflicts report the differing metadata paths and require a new version. Namespace interpretation remains a selected plugin generation-extension responsibility.

## Resolution and generation fixed point

The CLI derives one statically resolved application from local plugins, explicit public exposure, generated client use, non-inferable declared requirements, namespaced build-time metadata, selected plugin rules, and explicit provider choices only when several ordinary providers exist.

Reserved `kernel.*` Capabilities are always available outside ordinary provider selection. Their exact schemas and digests come from the versioned `github.com/plystra/kernel/capability/catalog` API and are revalidated against the CLI contract model at startup; the CLI does not maintain a second intrinsic schema copy. A Plystra Project directly requires `github.com/plystra/kernel` in `go.mod`, and generation retains that selected module version or a deterministic local-workspace build identity for intrinsic runtime provenance. Ordinary providers are never chosen by priority, official status, discovery order, or filesystem order.

The CLI strictly normalizes `plystra.yaml` `http.address`, the closed `http.transports` and optional `http.cors` objects, and the unordered `http.expose` list. `http.transports` accepts only boolean `connect` and `rest` fields, defaults to Connect enabled and REST disabled, and permits `null` only as the typed instruction to restore that field's schema default. New Project scaffolds emit both choices explicitly as `connect: true` and `rest: false`; an omitted field in another selected document still uses the same schema default. When present, `http.cors` requires one nonempty normalized `allowed_origins` list, accepts only optional boolean `allow_credentials` beyond that list, defaults credentials to disabled, and rejects malformed origins plus credentialed wildcards. Every exposed exact canonical ID becomes a root requirement with stable configuration provenance and enters the generation model with HTTP and JavaScript exposure; duplicates, malformed IDs, unknown configuration keys, and IDs absent from the visible canonical catalog fail before extension execution or generated output. Internal availability alone never creates a public surface, while intrinsic `kernel.*` targets still require explicit exposure. A nonempty exposed set requires at least one selected transport. JavaScript SDK generation requires Connect, so a selected model with canonical or Alias JavaScript surfaces and `connect: false` fails before output with the selected configuration path, every affected surface, and the `connect: true` remediation.

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

Filesystem-backed contexts also expose immutable configuration provenance: selection mode, selected environment when applicable, stable Project-relative root and selected-document paths, normalized root and selected-document digests, and the dependency-composition digest. They never expose YAML values, resolved Secrets, absolute paths, the process environment, or generated-output locations. `Digest` covers this complete extension input and survives the helper-process round trip. `BuildModelDigest` excludes document provenance so a runtime-only configuration change does not alter static assembly unless an extension actually changes its normalized output. Before built-in transport or bootstrap generation begins, the CLI cross-checks that bounded identity against the typed dependency composition and generated-manifest provenance and ties it to the final application-model digest. Bootstrap records the exact canonical non-secret view and its digest as immutable compiled constants. Connect, REST/JSON, JavaScript, and API-document renderers require the same validated view but do not serialize selector-only identity into transport source, so equal effective build models retain byte-stable transport output while `generated/manifest.json` and bootstrap still record selection drift.

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

Rule inputs, outputs, dependency graphs, contribution digests, and final results enter the generated manifest without Secret values. `plystra generate --check` recomputes them, and removing a plugin or metadata match removes obsolete output.

## Runtime configuration resolution

Before provider resolution, the CLI loads the regular root `plystra.yaml` from every direct and transitive dependency Plystra Project in the effective Go Module graph. It never loads a dependency environment overlay, and a markerless Go dependency remains unscanned. Dependency `http.expose` and `capabilities.require` values form deterministic canonical-ID unions. Their sparse `{add: [...], remove: [...]}` form records exact set decisions, while `null` under `capabilities.use`, `capabilities.aliases`, or `config` removes that exact inherited keyed declaration or Plugin field. Declared object configuration fields merge recursively; declared scalars and arrays replace as complete values. Identical additions, removals, Provider selections, Alias declarations, and typed Plugin configuration paths deduplicate with all-source provenance; incompatible inherited add/remove, value/removal, or typed decisions fail with the exact field plus every contributing `module@version/plystra.yaml` location unless root `plystra.yaml` explicitly resolves that exact decision. Directness, graph depth, version, discovery order, filesystem order, and Plugin ID sorting never choose a dependency winner. Dependency process settings such as `http.address`, `http.transports`, `http.cors`, and `timeouts.startup` are ignored because they remain current-Project-owned.

Root `plystra.yaml` is the mandatory Project marker, shared current-Project layer, and default configuration for every invocation. `plystra generate --env production` adds exactly one sparse project-root `plystra.production.yaml` overlay above that root; the overlay must exist, omitted fields inherit, and typed scalar, keyed-object, set, and tombstone semantics determine each field rather than a generic YAML deep merge. Within the overlay, `http.transports.connect` and `http.transports.rest` replace independently, an omitted field inherits, and `null` restores its default. A supplied `http.cors.allowed_origins` list replaces the complete normalized root list while omitted origins inherit and credentials replace independently; `http.cors: null` disables inherited current-project CORS. The selected environment applies only to the current Project: dependency environment files are never loaded, unselected overlays are ignored, and dependency-baseline maintenance still updates root `plystra.yaml` without materializing inherited values into the overlay.

`plystra generate --config deploy/customer-a.yaml` instead uses that one complete document as the current-Project layer above dependency composition; root `plystra.yaml` is not merged beneath it. `PLYSTRA_ENV` and `PLYSTRA_CONFIG` supply their corresponding selector for automation. Setting both variables is an error, `--env` and `--config` cannot be combined, and either explicit selector overrides both ambient variables. Relative configuration paths are resolved from the detected Project root even when the command starts inside a Plugin; an absolute path is accepted only when it resolves within that root. Selecting an environment or configuration never changes Project, Plugin, or dependency discovery.

The CLI indexes each visible plugin's strict Kernel configuration declaration and composes Plugin values only at declared typed field boundaries. It validates `timeouts.startup` as an optional positive Go duration, using `2m` when omitted; generated bootstrap reads the setting again from the bounded runtime document rather than embedding an application value. After the provider and generation fixed point stabilizes, the CLI validates exactly one object for every selected Plugin ID with the Kernel's non-resolving validator. Omitted objects normalize to `{}` so optional fields and defaults remain usable; missing required fields, unknown fields, invalid values or Secret-reference syntax, and configuration for an unselected plugin fail before rendering. Environment variables and files are never read during generation.

Private values and Secret reference targets do not enter generation-extension context, generated source, SDKs, documentation, or diagnostics. `generated/manifest.json` configuration schema v4 records `default`, `environment`, or `explicit-config` mode; the selected environment and overlay reference when applicable; stable Project-relative paths; normalized semantic document digests; dependency-composition baseline history; the committed Protobuf wire-map digest; and the final build-affecting application-model digest. `generated/go/bootstrap/bootstrap_gen.go` records the matching bounded selection-provenance JSON and its digest as constants. It also records a versioned compatibility projection for the selected HTTP transports and CORS policy, public exposure, Capability requirements, explicit Provider choices, and Alias declarations; that projection and its digest include the complete application-model digest, so the bounded runtime check is cryptographically associated with the exact generated assembly. Process address and timeout values, ordinary Plugin configuration, Secret references, resolved Secrets, source paths, and machine-specific absolute paths are excluded. Changing selected CORS origins or credential handling creates deterministic generation drift while equivalent normalized policies retain one static model identity. Environment mode retains the root dependency baseline because the sparse overlay never owns dependency maintenance. Baseline records contain only deterministic path/digest/removal/source provenance. A separate private digest covers the validated selected plugin manifests and values only for concurrent-input detection during the generation transaction.

For every selected local plugin, generation derives its module-owned type and decoder under `generated/go/configuration/` from the validated `plugin.yaml` schema alone. Required fields and fields with defaults use direct Go values; omitted optional scalars use pointers, while optional objects and arrays preserve nil-versus-configured-empty behavior. The generated decoder calls Kernel `configuration.Decode` at runtime, constructs one typed object for the Plugin ID, and redacts formatting and serialization. Application values and Secret reference targets are never embedded in this source. Selected dependency plugins ship the same generated configuration boundary in their own Go Modules.

The application-owned `generated/go/bootstrap` package is the runtime construction and configuration-selection boundary. Its `New` function selects root `plystra.yaml` by default, loads it through the Kernel's bounded regular-file API, and projects the normalized runtime document onto the build-affecting declarations compiled into the binary. A projection mismatch fails with rebuild guidance before startup settings are read, a Secret resolver is created, or any Provider constructor runs. Runtime-only changes to `http.address`, `timeouts.startup`, ordinary Plugin configuration, and Secret references remain outside this comparison. After compatibility succeeds, bootstrap validates the startup timeout, resolves Secrets, constructs each selected ordinary Provider exactly once, adds the Kernel's intrinsic bindings, publishes the complete canonical invocation runtime, and returns a private redacted `Application`. Passing `--env <environment>` to the generated binary, or setting `PLYSTRA_ENV` when no explicit selector is present, loads root plus exactly one required sparse `plystra.<environment>.yaml` through the same typed field rules used during generation. Passing `--config <yaml-path>`, or setting `PLYSTRA_CONFIG` when no explicit selector is present, instead loads and normalizes that one complete current-Project document without parsing or merging root configuration; a regular root `plystra.yaml` marker remains mandatory. An explicit selector overrides both ambient variables, the two modes cannot be combined, and a selected configuration must be an existing nonsymbolic regular file within the runtime Project directory. Unsafe names or paths, missing files, unknown fields, invalid types, prohibited YAML references, and incompatible build-affecting declarations fail before Provider construction, while unselected overlays and replacement files remain unread. Generate and start the application with the same selector; after editing selected Providers, requirements, public exposure, transports, CORS, or Aliases, regenerate and rebuild with that selector before starting the binary.

`Application.Invocations` exposes the immutable typed application handles after successful construction. `Application.Start` applies the configured startup deadline and bounded failed-start rollback; `Application.Stop` shuts active lifecycle providers down in reverse selected Plugin ID order. The CLI-owned `generated/go/application` process entrypoint delegates configuration selection to bootstrap, waits for `SIGINT` or `SIGTERM` during normal execution, and owns bounded shutdown. Its private smoke path starts the same runtime, invokes intrinsic health through the shared dispatcher, and stops immediately. No runtime value or Secret reference target is embedded in generated source.

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

The CLI emits `generated/go/assembly/invocations_gen.go` with one typed endpoint adapter per selected ordinary canonical Capability, the complete Kernel-owned intrinsic binding set, one immutable canonical catalog, one shared dispatcher, and dependency-ordered application handles. Assembly prepares every typed handle while the dispatcher is unpublished, constructs every selected provider, and atomically publishes the canonical catalog only after all constructors succeed. The catalog records exact contract digests and provider provenance: ordinary entries carry Plugin ID, package, Go Module build, and sole-provider or explicit-selection reason; intrinsic entries carry Kernel module/build provenance, an empty Plugin ID, and the intrinsic selection reason. Cross-module adapters copy fields directly and perform explicit conversions only for generated named enum types; they do not use JSON as an in-process type bridge. Raw dispatch applies a 30-second default only when the caller and generated path supply no earlier deadline. Alias IDs never enter the catalog or dispatcher.

Every generated canonical invocation validates the selected Provider's response after completion contributions and before an external adapter can serialize it. Invalid enum values, non-finite numbers, malformed UTF-8, absent required collections or objects, and unsafe object graphs are discarded; the caller receives the zero response plus one data-free internal contract-defect error. Provider errors likewise discard any accompanying response. The invocation package projects any failure into one immutable `TransportErrorInput` containing only one declared semantic code or one closed Kernel class with an optional bounded detail code. It never retains error text, causes, payloads, or Provider data, and generated adapters consume that projection instead of reclassifying raw errors. Internal clients, canonical adapters, and Alias forwards therefore share the same response and error boundary rather than relying on transport-specific serializers as the first validator.

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

Explicitly JavaScript-exposed canonical Capabilities lower into one immutable provider-independent SDK model and a deterministic ESM TypeScript package under `generated/sdk/javascript/`. The package contains nested exact-version methods such as `client.email.send.v1`, tree-shakable operation factories, strict request and response types, semantic error-code types, generated Connect transport descriptors, declarations, and package documentation. Contract digests include extension metadata, while provider identities, runtime plugin configuration, verified internal context, and Secret values never enter the SDK model or source.

The generated browser transport resolves each unary method from the same self-contained Protobuf descriptor graph used by the generated Connect handlers, translates Plystra request and response values at the wrapper boundary, and sends binary Connect requests through `@bufbuild/protobuf`, `@connectrpc/connect`, and `@connectrpc/connect-web`. Those packages are pinned direct npm dependencies of the generated package; callers never construct Protobuf messages, import descriptors, create raw Connect clients, or receive `ConnectError` as the public error model. The transport preserves an application base-path prefix, bounds encoded requests to 1 MiB, accepts an optional raw-access-token callback and `AbortSignal`, and exposes only stable Plystra error fields. It adds the `Bearer` scheme itself and rejects already-prefixed callback values before sending a request. Exact fields, enums, finite numbers, safe integers, plain JSON objects, and decoded responses remain validated. Credentials are attached only as one bounded `Authorization` header, and callback, network, cancellation, malformed-response, and schema failures are normalized without copying Connect or provider text.

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

Interactive creation asks whether to initialize Git, include GitHub Actions CI,
and include the Plystra-specific development skill under
`.agents/skills/plystra/`:

```powershell
plystra new my-app
plystra new my-app --module github.com/acme/my-app
plystra new my-app --module github.com/acme/my-app --template github.com/acme/platform@v1.2.3
```

The positional value is one lower-case ASCII kebab-case child-directory name.
The first command creates `./my-app/` with `module my-app`; `--module` changes
the `go.mod` identity and generated imports without changing that directory.
Unsafe names, paths, traversal, separators, and an existing target fail before
filesystem mutation. An explicit module path must satisfy standard Go Module
rules. The removed positional full-module-path form is not accepted.

`--template` resolves one standard Go Module query, requires that selected
module to contain regular root `plystra.yaml`, and retains it as an ordinary
direct dependency. The CLI composes the dependency Project's root declarations
into the new Project and regenerates the application; it does not clone or copy
the dependency source, inspect dependency environment overlays, modify the Go
Module Cache, create `go.work`, or grant the template any Provider or
configuration priority. A module without root `plystra.yaml` is rejected and
the target directory is not installed.

The template's default application model must resolve without Provider
ambiguity. If several compatible Plugins provide one required Capability and
the template root does not select one under `capabilities.use`, creation rejects
the template before installing the target. The diagnostic names every candidate
and requires the template publisher to record the explicit default choice in
root `plystra.yaml` and publish a corrected module version.

The complete effective template graph must also use public Go Modules. Creation
rejects every direct or transitive module matched by the effective `GOPRIVATE`
setting, reports each selected `path@version`, and leaves no target directory.
Publish or replace a genuinely private dependency before publishing the
template, or correct an overbroad Go privacy setting before retrying.

Every dependency Plystra Project in the selected template graph must also be
portable without a relative Go Module `replace`. Creation inspects the bounded
validated `go.mod` snapshots, reports each `module@version/go.mod` directive,
and rolls back. Publish the referenced module versions and remove the relative
replacements before publishing a corrected template.

Creation then requires the generated application to be a fixed point. After
installing generated output, the CLI immediately performs the equivalent of
`plystra generate --check`. Dependency-composition drift or any changed,
missing, unexpected, or obsolete generated path rejects the template and rolls
back the target. The template publisher must make generation deterministic,
run `plystra generate` followed by `plystra generate --check` in a fresh
Project directory, and publish a corrected module version.

Template creation then runs the same read-only validation workflow as
`plystra check`: it rechecks the selected configuration and generated output,
then runs `go test -mod=readonly ./...`. A failure remains inside the creation
transaction and leaves no target Project. The publisher must make that public
check pass in a fresh Project directory before publishing a corrected version.

The staged Project must also build every Go package with
`go build -mod=readonly ./...`. Build failure rejects the template inside the
same transaction and reports publisher-owned remediation. The CLI then builds
the generated application entrypoint with `GOWORK=off` into isolated temporary
output, starts the real assembled runtime, invokes intrinsic
`kernel.health/v1`, and stops lifecycle providers cleanly. Child output is not
surfaced and the temporary executable is removed on every path. Failure remains
inside the same creation transaction. This private qualification executable is
not the later public `plystra build` and `dist/` contract.

Typed operational values and Secret-reference placeholders declared by the
template's root configuration are materialized in the new root `plystra.yaml`
through that same dependency composition. Creation validates them against the
selected Plugin schemas but never reads an `env` or `file` reference, even when
the referenced value exists in the creation environment. Secret-reference
targets and resolved Secret values are excluded from generated source and
manifest provenance. A required Plugin field omitted by the template is not
invented; generation fails transactionally until the template declares a valid
local value or reference.

Each prompt defaults to yes and accepts `yes`/`y`, `no`/`n`, or Enter. Scripts
and other non-interactive callers must choose every option explicitly:

```powershell
plystra new my-app --module github.com/acme/my-app --git --github-ci --skills
plystra new my-app --module github.com/acme/my-app --template github.com/acme/platform@v1.2.3 --git --github-ci --skills
plystra new email --module github.com/acme/email --no-git --no-github-ci --no-skills
```

The independent flag pairs are `--git`/`--no-git`,
`--github-ci`/`--no-github-ci`, and `--skills`/`--no-skills`. This permits, for
example, generating GitHub CI inside a project directory already governed by a
parent repository without initializing a nested repository. Requested Git
initialization creates an empty repository on branch `main`; requested CI emits
`.github/workflows/ci.yml`; requested skills emit a complete, validated
Plystra-specific `SKILL.md` and agent metadata rather than generic advice or
TODO placeholders. The skill embeds the created module path and provides
an immediate task-oriented route for operating a template-created Project and
another for ordinary development through only Go Module, Plugin, Capability,
and `plystra.yaml`. It presents environment selection on that ordinary path,
identifies complete `--config` replacement as advanced, and places resolution,
generation, wire-history, and other mechanism-heavy guidance after an explicit
detailed-reference boundary. The complete reference still covers operational
module layout, Plugin and Capability authoring, configuration, provider
selection, cross-Plugin generated-client use, Alias, HTTP, JavaScript, runtime,
validation, and troubleshooting workflows. It contains no Git, branch, commit,
or push instructions. `plystra new --help` documents the complete creation
contract.

Successful template creation reports the selected query:

```text
Created my-app from github.com/acme/platform@v1.2.3
Configuration scaffolded
Generated, checked, built, and locally verified

Next:
  cd my-app
  plystra check
```

The concise output names the result and next action without exposing absolute
paths, internal resolution detail, or an unavailable command. This ordinary
template-dependency workflow includes automatic read-only Go package tests and
builds plus isolated startup, intrinsic health verification, and clean shutdown.
When the generated Project has a JavaScript SDK, creation also runs the package
manager workflow `npm install --ignore-scripts --no-audit --no-fund`,
`npm run typecheck`, `npm run build`, and `npm pack --dry-run --json`. The
install resolves the generated package's pinned Buf and Connect runtime
dependencies, so type checking covers the actual descriptor-backed transport
rather than an unused dependency declaration. The
validation-only `node_modules/` and `dist/` directories are removed before the
target is installed. The complete qualified-template contract still needs
public `plystra dev` and `plystra build` workflows. No template is advertised
as qualified by this CLI version.

## Transaction safety

New project trees, template dependency metadata and composition, optional CI and skill files, and requested Git initialization are populated and validated in a same-parent staging directory before rename. A template resolution, composition, generation, validation, or Git initialization failure leaves no target project. In-place changes use same-filesystem staged replacements and backups, reject unsafe symbolic traversal, recheck source snapshots, preserve concurrent user edits, and restore original bytes and modes after validation failure or panic.

Commands below a module root use the nearest real enclosing `go.mod`; nested modules do not leak mutations into an outer module. The Module Cache remains read-only.

## Authoring behavior

Plugin-target inference resolves an explicit target, the enclosing plugin, the only local plugin, or an interactive choice when several local plugins remain and a terminal is available. Non-interactive ambiguity fails with every candidate and requires `--plugin <directory-or-plugin-id>`.

Capability identities use `<capability-name>/v<number>`. Names contain at least two dot-separated lower-case segments, may use any logical hierarchy depth, and never imply a fixed namespace/operation split.

Capability creation and implementation update schemas, `plugin.yaml`, generated contracts, providers, clients, application invocation, adapters, assembly, SDKs, docs, and manifests in one transaction. Existing user implementations are never overwritten.

Plugin and Capability mutations reject unowned or modified-obsolete files under `generated/`. They report the conflicting paths, preserve those files, and roll back every CLI-owned declaration, source, module-metadata, and generated-output change instead of returning success beside immediate generation drift.

Create a genuinely new Capability identity from inside the target plugin, from a single-plugin module, or with an explicit target by choosing an intent profile:

```powershell
plystra capability create records.create --query
plystra capability create records.archive --query --plugin records
plystra capability create records.read --query --plugin records --expose
```

`--query` expands into complete explicit read-only, inherently idempotent, safely retryable, best-effort-cancellable, completed-before-return semantics with public request and response data. Names never imply semantics. A new Capability identity requires one supported profile before any mutation.

An omitted version selects `v1` when none is visible. When the identity is already visible, it selects one above the highest visible version and copies that exact contract, including its semantics, as an editing base; omit profile flags for that later-version workflow. An explicit older or skipped new version is rejected without mutation until it is deliberately repeated with `--confirm`. An existing exact version is never recreated; implement it instead:

```powershell
plystra capability implement email.send/v1 --plugin mailer
```

For a genuinely new name, creation reports conservative typo-like visible exact Capabilities as advisory recommendations. It never redirects or blocks the requested custom identity based only on similar spelling.

Implementation searches local and effective-graph dependency Project contracts, requires exact provider-independent equality including normalized extension metadata, copies the canonical schema when the target plugin does not yet provide it, adds a compile-safe user-owned method only when absent, regenerates all affected module surfaces, tidies module metadata, and validates with `go test -mod=readonly ./...`. Repeating the command preserves an existing method byte-for-byte.

In a Plystra Project, expose an existing exact canonical Capability or create and expose a new one in the same transaction:

```powershell
plystra capability expose records.create/v1
plystra capability expose records.create/v1 --env production
plystra capability expose records.create/v1 --config deploy/customer-a.yaml
plystra capability create records.update --query --plugin records --expose
```

`capability expose` requires an exact `<capability-name>/vN`. With no selector it updates root `plystra.yaml`; `--env production` updates only the sparse `plystra.production.yaml` overlay; and `--config deploy/customer-a.yaml` updates only that complete replacement document. `PLYSTRA_ENV` and `PLYSTRA_CONFIG` provide the same two selector modes when neither flag is present, while either explicit flag overrides both ambient variables. The command preserves comments, unrelated values, and exact add/remove tombstones, then regenerates every affected Go, HTTP, JavaScript, documentation, assembly, and manifest surface with the same selected configuration. Invocation from a nested Plugin still resolves relative configuration paths from the Project root. Repeating the command is byte-idempotent when generated output is current, and no unselected configuration file is synchronized.

When several compatible Plugins provide one required canonical Capability, select the intended Provider through the targeted public workflow:

```powershell
plystra use email.send/v1 acme.email.smtp
plystra use email.send/v1 acme.email.production --env production
plystra use email.send/v1 acme.email.customer --config deploy/customer-a.yaml
```

The default form writes an explicit current-Project replacement under root `plystra.yaml` `capabilities.use`; `--env` writes only the selected sparse overlay; and `--config` writes only the selected complete replacement document. `PLYSTRA_ENV` and `PLYSTRA_CONFIG` select the same targets when no explicit flag is present, while an explicit selector overrides both variables. The command requires an exact canonical `<capability-name>/vN` and a visible Plugin that provides that exact contract; intrinsic Capabilities, application Aliases, absent or unrequired Capabilities, unknown Plugins, and non-providers fail without mutation. It preserves comments and unrelated YAML, regenerates with the same selection, validates the complete Project, is byte-idempotent when already selected, and restores the selected YAML, generated tree, `go.mod`, and `go.sum` after any later failure.

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

`plystra add` validates one module query, resolves it through ordinary Go tooling, and records the selected module as a direct requirement. `plystra remove` requires a module already selected in `go.mod`, uses ordinary Go tooling to remove it, and fails if regeneration or tidy would select it again. `plystra update` also requires an existing selection, preserves a direct requirement as direct, and verifies that the module remains selected. All three commands recompose the dependency-derived root `plystra.yaml` baseline, regenerate, tidy, and validate the complete Project. These initial dependency workflows use the default root configuration and never scan for or rewrite environment overlays or alternative YAML files. A failed Go command, resolution, composition, generation, tidy, dependency postcondition, or validation step restores `go.mod`, `go.sum`, root configuration, generated artifacts, and every other transaction-owned file without overwriting a concurrent user edit. The Go Module proxy and cache remain ordinary Go-tool boundaries; the CLI never copies or modifies dependency source.

## Public command surface

The intended command set includes:

```text
plystra new
plystra add
plystra remove
plystra update
plystra plugin create
plystra capability create
plystra capability implement
plystra capability expose
plystra capability require
plystra use
plystra dev
plystra test
plystra build
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

The current `plystra check` implementation is read-only. It verifies the
selected dependency composition and generated fixed point, then runs
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

The command resolves the mandatory root `plystra.yaml`, the effective Go Module graph, every dependency Project and composed root declaration in that graph, canonical providers, selected generation extensions, generation-derived requirements, contributions, and Capability Aliases. Only module roots containing `plystra.yaml` are scanned for dependency Plugins or declarations; markerless modules remain ordinary Go dependencies, and dependency environment overlays are ignored. It renders the complete Project-owned Go, HTTP, JavaScript, documentation, assembly-compatibility, configuration-provenance manifest, invocation, and runtime-bootstrap surfaces. A Go Module without root `plystra.yaml` is an ordinary dependency and is rejected as a generation target.

Generation maintains the current Project from the dependency-derived baseline recorded in the prior ownership manifest. Default and environment modes update root `plystra.yaml`; environment mode then applies the selected sparse overlay without rewriting it. Explicit mode updates only the selected complete file and never synchronizes the same change into root or another alternative. Each maintained selection retains independent baseline history. The typed three-way update preserves comments, explicit current-Project values, and exact removal tombstones while introducing new inherited declarations and removing inherited declarations that disappeared. An inherited value deleted without an explicit typed removal is an ownership error instead of an instruction to overwrite the file. The maintained configuration and generated tree install in one transaction, run `go test -mod=readonly ./...`, and re-resolve the complete application before commit. Validation failure, changed inputs, concurrent configuration edits, or nondeterministic output roll back every safe CLI-owned change while preserving a concurrent user edit and retaining recovery data when that edit prevents a safe restore.

Go subprocesses preserve an explicit `GOWORK` selection. An automatically discovered enclosing `go.work` remains active when it validly includes the nearest module; when it is valid but does not list that module, the CLI runs the subprocess with `GOWORK=off` so an unrelated parent workspace cannot redirect generation or validation. Malformed workspaces, missing `use` directories, and invalid used modules remain active so the Go tool reports the original workspace error instead of having it hidden.

Use the read-only consistency gate in local checks and CI:

```powershell
plystra generate --check
```

Check mode never writes module files or configuration. It reports dependency-composition drift against the selected path, such as `changed plystra.yaml (dependency composition)` or `changed deploy/customer-a.yaml (dependency composition)`, alongside deterministic `changed`, `missing`, `unexpected`, and `obsolete` generated paths. Switching selections or changing a build-affecting selected value changes generated provenance and output. The command returns a failing exit status while any drift remains. Installation preserves an unexpected unowned file rather than overwriting or deleting it.

## Development

The complete contributor and Plystra-module workflow is in
[`docs/development-guide.md`](docs/development-guide.md). It records the actual
end-of-Gate-9 command surface, generated ownership rules, operational examples,
troubleshooting, and intentionally deferred roadmap work.

```powershell
go test ./...
go test -race ./...
go vet ./...
go run ./cmd/plystra --help
go test ./internal/generationresolution -run '^$' -bench '^BenchmarkGenerationFixedPoint$' -benchmem
go test ./internal/clientgen -run '^$' -bench 'BenchmarkGenerated(CanonicalInvocation|AliasForwarding)$' -benchmem
go test ./internal/httpgen -run '^$' -bench '^BenchmarkGeneratedHTTPInvocation$' -benchmem
```

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
