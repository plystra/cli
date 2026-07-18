# Plystra CLI

`github.com/plystra/cli` builds the user-installed `plystra` command. It is the developer, resolution, generation, assembly, validation, build, test, and delivery half of Plystra Core.

The CLI is a separate Go Module from `github.com/plystra/kernel`. It completes build-time work and emits deterministic Go and JavaScript source targeting the Kernel's versioned assembly API; it is not a second runtime.

## Ownership

The CLI owns:

- Go Module, Plugin, and Capability creation.
- Root-level plugin and intended dependency-module scanning.
- The complete normalized application model.
- Official and intrinsic Capability discovery.
- Exact requirement closure and ordinary provider resolution.
- Plugin-provided build-time rule discovery and validation.
- Generation-derived Capability requirements.
- Deterministic structured contribution validation and merging.
- Contracts, clients, providers, configuration, application invocation, adapters, assembly, bootstrap, JavaScript SDKs, documentation, and manifests.
- Transactional mutation, generated consistency, development, testing, building, and release preparation.

The CLI is the sole writer of final `generated/` source.

Capability inspection strictly parses the optional `extensions` mapping within the 1 MiB declaration boundary. The CLI preserves every valid lower-kebab namespace, including unknown namespaces, as immutable namespace-sorted canonical JSON-compatible metadata: object key order is normalized, scalar types and array order are preserved, and omitted and empty metadata are equivalent. Normalized extension metadata participates in exact contract equality, so providers cannot add, remove, or change generation-affecting behavior under one Capability ID; conflicts report the differing metadata paths and require a new version. Namespace interpretation remains a selected plugin generation-extension responsibility.

## Resolution and generation fixed point

The CLI derives one statically resolved application from local plugins, explicit public exposure, generated client use, non-inferable declared requirements, namespaced build-time metadata, selected plugin rules, and explicit provider choices only when several ordinary providers exist.

Reserved `kernel.*` Capabilities are always available outside ordinary provider selection. Their exact schemas and digests come from the versioned `github.com/plystra/kernel/capability/catalog` API and are revalidated against the CLI contract model at startup; the CLI does not maintain a second intrinsic schema copy. A runnable application directly requires `github.com/plystra/kernel` in `go.mod`, and generation retains that selected module version or a deterministic local-workspace build identity for intrinsic runtime provenance. Ordinary providers are never chosen by priority, official status, discovery order, or filesystem order.

The CLI strictly normalizes `plystra.yaml` `http.address` and the unordered `http.expose` list. Every exposed exact canonical ID becomes a root requirement with stable configuration provenance and enters the generation model with HTTP and JavaScript exposure; duplicates, malformed IDs, unknown configuration keys, and IDs absent from the visible canonical catalog fail before extension execution or generated output. Internal availability alone never creates a public surface, while intrinsic `kernel.*` targets still require explicit exposure.

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

The CLI indexes each plugin's strict Kernel configuration declaration and preserves each `plystra.yaml` object only in a separate private resolution path. It also validates `timeouts.startup` as an optional positive Go duration, using `2m` when omitted; generated bootstrap reads the setting again from the bounded runtime document rather than embedding an application value. After the provider and generation fixed point stabilizes, the CLI validates exactly one object for every selected Plugin ID with the Kernel's non-resolving validator. Omitted objects normalize to `{}` so optional fields and defaults remain usable; missing required fields, unknown fields, invalid values or Secret-reference syntax, and configuration for an unselected plugin fail before rendering. Environment variables and files are never read during generation.

Private values and Secret reference targets do not enter generation-extension context, generated source, manifests, SDKs, documentation, or diagnostics. A private digest covers the validated selected plugin manifests and values only for concurrent-input detection during the generation transaction.

For every selected local plugin, generation derives its module-owned type and decoder under `generated/go/configuration/` from the validated `plugin.yaml` schema alone. Required fields and fields with defaults use direct Go values; omitted optional scalars use pointers, while optional objects and arrays preserve nil-versus-configured-empty behavior. The generated decoder calls Kernel `configuration.Decode` at runtime, constructs one typed object for the Plugin ID, and redacts formatting and serialization. Application values and Secret reference targets are never embedded in this source. Selected dependency plugins ship the same generated configuration boundary in their own Go Modules.

The application-owned `generated/go/bootstrap` package is the runtime construction boundary. Its `New` function accepts the runtime document path, loads it through the Kernel's bounded regular-file API, validates `timeouts.startup`, resolves Secrets, constructs each selected ordinary provider exactly once, adds the Kernel's intrinsic bindings, publishes the complete canonical invocation runtime, and returns a private redacted `Application`. `Application.Invocations` exposes the immutable typed application handles after successful construction. `Application.Start` applies the configured startup deadline and bounded failed-start rollback; `Application.Stop` shuts active lifecycle providers down in reverse selected Plugin ID order. Callers retain control of the document location and lifecycle contexts, and no runtime value or Secret reference target is embedded in generated source.

## Generated application invocation

Every ordinary external or cross-plugin call uses generated code:

```text
generated adapter or Capability client
-> selected plugin rule contributions
-> Kernel exact dispatch
-> selected provider
```

The CLI emits `generated/go/assembly/invocations_gen.go` with one typed endpoint adapter per selected ordinary canonical Capability, the complete Kernel-owned intrinsic binding set, one immutable canonical catalog, one shared dispatcher, and dependency-ordered application handles. Assembly prepares every typed handle while the dispatcher is unpublished, constructs every selected provider, and atomically publishes the canonical catalog only after all constructors succeed. The catalog records exact contract digests and provider provenance: ordinary entries carry Plugin ID, package, Go Module build, and sole-provider or explicit-selection reason; intrinsic entries carry Kernel module/build provenance, an empty Plugin ID, and the intrinsic selection reason. Cross-module adapters copy fields directly and perform explicit conversions only for generated named enum types; they do not use JSON as an in-process type bridge. Raw dispatch applies a 30-second default only when the caller and generated path supply no earlier deadline. Alias IDs never enter the catalog or dispatcher.

Required or explicitly exposed intrinsic Capabilities receive normal typed application handles and clients. Their generated request, response, and enum declarations are aliases to `github.com/plystra/kernel/intrinsic`, and assembly binds those handles with `intrinsic.HealthContract`, `intrinsic.InfoContract`, and the Kernel's own binding constructors so callers and endpoints share the same typed contract tokens. Generated application handles are constructed in topological dependency order from the canonical clients required by their lowered contributions; an ordinary invocation may depend on an intrinsic client. A cycle, missing or repeated dependency, accessor collision, invalid provenance value, or inconsistent intrinsic/ordinary selection fails generation. Applications with no ordinary providers still publish the two intrinsic canonical bindings, while HTTP and JavaScript surfaces remain absent unless explicitly exposed.

For each local plugin that declares exact `requires`, generation emits an immutable client set at `generated/go/dependencies/<plugin-directory>/dependencies_gen.go`. Its authored constructor receives `func New(Config, dependencies.Dependencies) *Plugin`; a plugin with no requirements keeps `func New(Config) *Plugin`. Dependency-module plugins use the equivalent generated package from their own module. Constructors may validate and retain these clients but cannot invoke them successfully until construction completes and assembly publishes the catalog. Every later cross-plugin call therefore follows the same generated application invocation path and contributions as an external adapter. A missing contract, unbound client, constructor panic or nil result, cross-module type mismatch, or publication failure leaves the runtime unpublished.

Each application-local Capability Alias exposed to Go generates only a thin Alias-named client package. It reuses the canonical target's request, response, and errors and forwards to the target's generated client, so every target contribution runs before the Kernel receives the canonical ID. Alias clients create no Alias contract, invocation handle, provider, or Kernel registration. Several Alias clients may forward to one canonical target, and native Go deprecation comments carry application-local compatibility guidance.

For `extensions.authn.authenticated: true`, an AuthN rule adds `authn.session.verify/v1` and generated verification before target dispatch. For `extensions.authz.permission`, an AuthZ rule adds `authz.check/v1`, generates the decision using permission and Space/resource data, and rejects denial. These are static application calls, not Kernel behavior.

## Canonical HTTP transport

Each explicitly HTTP-exposed canonical Capability can generate one `net/http` adapter at `POST /api/v1/capabilities/<capability-name>/vN/invoke`. The adapter accepts a trusted root-context factory and the concrete generated application-invocation handle; it never constructs a raw Kernel handle, registers a provider, or dispatches a route identity itself. Generated contract error codes implement `capability.SemanticError`, and adapters accept both those generated values and sanitized Kernel `invocation.SemanticError` values only when the code belongs to the canonical target contract.

The generated transport accepts one `application/json` object with no content encoding other than identity and bounds request and response JSON to 1 MiB. It rejects alternate paths, query parameters, duplicate media headers, duplicate or unknown fields, missing or `null` required fields, incompatible JSON types, invalid enum values, trailing JSON, and oversized bodies before application invocation. Responses are validated and fully encoded before headers are committed. Error bodies contain only stable transport, semantic Capability, or Kernel invocation codes; provider messages, panic values, and root-context failures are normalized to `internal`. Every response uses `application/json`, `Cache-Control: no-store`, and `X-Content-Type-Options: nosniff`.

When a lowered plan contains `http.ingress`, `http.egress`, or an adapter-credential input, the generated handler selects the matching external invocation path. It runs ingress before shared invocation preparation, preserves the generated invocation context through completion, then runs egress before serialization; internal `Invoke` calls run only the shared preparation/completion path and receive no adapter credentials. Canonical lower-snake credential names map deterministically to HTTP headers (`authorization` to `Authorization`, `x_api_key` to `X-Api-Key`). Missing, empty, duplicate, control-containing, or larger-than-64-KiB credential values are treated as absent, and only the downstream generated verification contribution can turn raw credential text into trusted state.

Each validated HTTP-exposed Alias generates only its own route identity and a thin wrapper around the already-bound canonical handler. Several Alias handlers can share that one canonical transport instance; they do not copy request validation, own an invocation handle, register a provider, or dispatch an Alias ID. The canonical handler validates the Alias path and runs the same planned ingress, invocation, completion, egress, response, and safe-error logic. Alias generation revalidates same-version direct targeting, target contract digest, exposure narrowing, and bounded deprecation metadata; deprecated wrappers carry native Go `Deprecated:` markers without changing runtime behavior or deprecating the target.

## Generated JavaScript SDK

Explicitly JavaScript-exposed canonical Capabilities lower into one immutable provider-independent SDK model and a deterministic ESM TypeScript package under `generated/sdk/javascript/`. The package contains nested exact-version methods such as `client.email.send.v1`, tree-shakable operation factories, strict request and response types, semantic error-code types, native `fetch` transport, declarations, and package documentation. Contract digests include extension metadata, while provider identities, runtime plugin configuration, verified internal context, and Secret values never enter the SDK model or source.

The generated browser transport uses the matching strict HTTP route, preserves an application base-path prefix, bounds JSON to 1 MiB, accepts an optional access-token callback and `AbortSignal`, and exposes only stable error status/code/detail fields. It validates exact fields, enums, finite numbers, safe integers, plain JSON objects, response media types, and unexpected response data. Credentials are attached only as one bounded `Authorization` header and callback, network, cancellation, malformed-response, and schema failures are normalized without copying provider text.

Every final Alias whose normalized exposure includes JavaScript generates a nested method and tree-shakable factory under its Alias ID. The Alias module imports the canonical target's exact request, response, semantic errors, validators, and contract digest, then changes only the HTTP route identity so the generated Alias handler forwards into the canonical application invocation path. Several aliases may reuse one target without copying its schema or provider details. Alias exposure cannot broaden the target, and compatibility aliases emit native TypeScript `@deprecated` declarations without deprecating the target.

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
plystra new github.com/acme/my-app
```

Each prompt defaults to yes and accepts `yes`/`y`, `no`/`n`, or Enter. Scripts
and other non-interactive callers must choose every option explicitly:

```powershell
plystra new github.com/acme/my-app --git --github-ci --skills
plystra new github.com/acme/email --library --no-git --no-github-ci --no-skills
```

The independent flag pairs are `--git`/`--no-git`,
`--github-ci`/`--no-github-ci`, and `--skills`/`--no-skills`. This permits, for
example, generating GitHub CI inside a project directory already governed by a
parent repository without initializing a nested repository. Requested Git
initialization creates an empty repository on branch `main`; requested CI emits
`.github/workflows/ci.yml`; requested skills emit a complete, validated
Plystra-specific `SKILL.md` and agent metadata rather than generic advice or
TODO placeholders. `plystra new --help` documents the complete contract.

## Transaction safety

New project trees, optional CI and skill files, and requested Git initialization are populated and validated in a same-parent staging directory before rename. A Git initialization failure leaves no target project. In-place changes use same-filesystem staged replacements and backups, reject unsafe symbolic traversal, recheck source snapshots, preserve concurrent user edits, and restore original bytes and modes after validation failure or panic.

Commands below a module root use the nearest real enclosing `go.mod`; nested modules do not leak mutations into an outer module. The Module Cache remains read-only.

## Authoring behavior

Plugin-target inference resolves an explicit target, the enclosing plugin, the only local plugin, or an interactive choice when several local plugins remain and a terminal is available. Non-interactive ambiguity fails with every candidate and requires `--plugin <directory-or-plugin-id>`.

Capability identities use `<capability-name>/v<number>`. Names contain at least two dot-separated lower-case segments, may use any logical hierarchy depth, and never imply a fixed namespace/operation split.

Capability creation and implementation update schemas, `plugin.yaml`, generated contracts, providers, clients, application invocation, adapters, assembly, SDKs, docs, and manifests in one transaction. Existing user implementations are never overwritten.

Plugin and Capability mutations reject unowned or modified-obsolete files under `generated/`. They report the conflicting paths, preserve those files, and roll back every CLI-owned declaration, source, module-metadata, and generated-output change instead of returning success beside immediate generation drift.

Create a first or next version from inside the target plugin, from a single-plugin module, or with an explicit target:

```powershell
plystra capability create records.create
plystra capability create records.archive --plugin records
plystra capability create records.read --plugin records --expose
```

An omitted version selects `v1` when none is visible and otherwise selects one above the highest visible version, copying that highest exact schema as an editing base. An explicit older or skipped new version is rejected without mutation until it is deliberately repeated with `--confirm`. An existing exact version is never recreated; implement it instead:

```powershell
plystra capability implement email.send/v1 --plugin mailer
```

For a genuinely new name, creation reports conservative typo-like visible exact Capabilities as advisory recommendations. It never redirects or blocks the requested custom identity based only on similar spelling.

Implementation searches local and explicit Go Module dependency contracts, requires exact provider-independent equality including normalized extension metadata, copies the canonical schema when the target plugin does not yet provide it, adds a compile-safe user-owned method only when absent, regenerates all affected module surfaces, tidies module metadata, and validates with `go test -mod=readonly ./...`. Repeating the command preserves an existing method byte-for-byte.

In a runnable application, expose an existing exact canonical Capability or create and expose a new one in the same transaction:

```powershell
plystra capability expose records.create/v1
plystra capability create records.update --plugin records --expose
```

`capability expose` requires an exact `<capability-name>/vN` and updates the root `plystra.yaml` `http.expose` list before regenerating every affected Go, HTTP, JavaScript, documentation, assembly, and manifest surface. Repeating it is byte-idempotent when generated output is current. A library module without `plystra.yaml`, an absent visible contract or provider, unsafe or concurrently changed configuration, unexpected generated output, generation failure, untidy module state, or validation failure leaves the configuration and every generated or module-owned file unchanged. `capability create --expose` uses the same rollback boundary for the new schema, plugin declaration, implementation scaffold, application exposure, module metadata, and generated output.

Generation always emits the contract and provider interface for every Capability provided by a local plugin, even before the application requires that Capability. This keeps user-owned provider implementations buildable while they are being authored. Clients, invocation paths, HTTP adapters, SDK operations, documentation, provider selection, and Kernel registration remain requirement- and exposure-driven, so an unrequired local Capability does not enter the runnable application surface.

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
plystra doctor
plystra sdk link
plystra sdk pack
plystra sdk publish
plystra release
```

Mutating commands perform all derivable generation automatically. Build and generation never publish or release as a side effect.

`plystra plugin create` keeps the new scaffold, generated module surfaces, and Go module metadata in one rollback boundary. It runs `go mod tidy` after generated imports exist, retains explicit pre-existing requirements and checksum entries, validates with `go test -mod=readonly ./...`, and rolls back its own `go.mod`, `go.sum`, generated-file, and scaffold changes if any later check fails. Concurrent user edits remain protected.

### Module generation

From any directory inside a Plystra Go Module, install its complete current managed tree with:

```powershell
plystra generate
```

For a runnable module, the command resolves the root `plystra.yaml`, explicit Go Module dependencies, canonical providers, selected generation extensions, generation-derived requirements, contributions, and Capability Aliases. It renders the application-owned Go, HTTP, JavaScript, documentation, assembly-compatibility, and manifest surfaces. For a library module without `plystra.yaml`, it renders module-owned Kernel compatibility, local plugin configuration, contracts and provider interfaces for locally provided Capabilities, plus contracts, clients, and immutable dependency sets for exact requirements visible through the module or its direct Go Module dependencies. Library generation does not emit application assembly, invocation paths, HTTP, SDK, or runtime bootstrap output.

Both paths install output transactionally and run `go test -mod=readonly ./...` before commit. Runnable generation re-resolves the complete application, while library generation reindexes its declarations and rendered ownership snapshot; changed inputs or nondeterministic output roll back the transaction.

Go subprocesses preserve an explicit `GOWORK` selection. An automatically discovered enclosing `go.work` remains active when it validly includes the nearest module; when it is valid but does not list that module, the CLI runs the subprocess with `GOWORK=off` so an unrelated parent workspace cannot redirect generation or validation. Malformed workspaces, missing `use` directories, and invalid used modules remain active so the Go tool reports the original workspace error instead of having it hidden.

Use the read-only consistency gate in local checks and CI:

```powershell
plystra generate --check
```

Check mode never writes module files. Both modes report deterministic `changed`, `missing`, `unexpected`, and `obsolete` paths and return a failing exit status while any drift remains. Installation preserves an unexpected unowned file rather than overwriting or deleting it.

## Development

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
