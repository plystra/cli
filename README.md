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

Reserved `kernel.*` Capabilities are always available outside ordinary provider selection. Ordinary providers are never chosen by priority, official status, discovery order, or filesystem order.

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

The v1 output protocol carries exact generation-derived Capability requirements and structured diagnostics with rule, namespace, and source-Capability provenance. It also defines stable contribution identities at `http.ingress`, `invocation.prepare`, `invocation.complete`, and `http.egress`, with explicit canonical `requires` and `provides` dependency tokens. Contributions contain only the closed CLI-owned node union for typed canonical Capability calls, validated context derivation, conditional failure, bounded non-sensitive scalar metadata attachment, and explicit ordinary-Capability audit events. The CLI validates request bindings against canonical schemas, backward-only node references, timeouts, bounds, sensitive credential flow, and explicit failure behavior; preserves semantic node order; canonically sorts only unordered field bindings; and includes the normalized nodes in output digests.

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

## Generated application invocation

Every ordinary external or cross-plugin call uses generated code:

```text
generated adapter or Capability client
-> selected plugin rule contributions
-> Kernel exact dispatch
-> selected provider
```

For `extensions.authn.authenticated: true`, an AuthN rule adds `authn.session.verify/v1` and generated verification before target dispatch. For `extensions.authz.permission`, an AuthZ rule adds `authz.check/v1`, generates the decision using permission and Space/resource data, and rejects denial. These are static application calls, not Kernel behavior.

## Method-specific login surfaces

Authentication methods use real contracts such as:

```text
authn.login.password/v1
authn.login.passkey/v1
authn.login.oidc.begin/v1
authn.login.oidc.complete/v1
```

When exactly one login method is resolved and explicitly exposed, generated HTTP and JavaScript surfaces may add the application-local `authn.login/v1` alias with that method's exact contract. The alias is not a canonical Capability, Kernel registry entry, provider requirement, or distributed contract. Several methods produce no implicit alias.

## Transaction safety

New project trees are populated and validated in a same-parent staging directory before rename. In-place changes use same-filesystem staged replacements and backups, reject unsafe symbolic traversal, recheck source snapshots, preserve concurrent user edits, and restore original bytes and modes after validation failure or panic.

Commands below a module root use the nearest real enclosing `go.mod`; nested modules do not leak mutations into an outer module. The Module Cache remains read-only.

## Authoring behavior

Plugin-target inference resolves an explicit target, the enclosing plugin, the only local plugin, an interactive choice, or an actionable non-interactive ambiguity error.

Capability identities use `<capability-name>/v<number>`. Names contain at least two dot-separated lower-case segments, may use any logical hierarchy depth, and never imply a fixed namespace/operation split.

Capability creation and implementation update schemas, `plugin.yaml`, generated contracts, providers, clients, application invocation, adapters, assembly, SDKs, docs, and manifests in one transaction. Existing user implementations are never overwritten.

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

## Development

```powershell
go test ./...
go test -race ./...
go vet ./...
go run ./cmd/plystra --help
```
