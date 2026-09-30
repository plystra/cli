# Interfaces and Implementations

Use authored Go as the contract and implementation source. A Plystra Interface is a versioned one-method Go interface; an Implementation is an ordinary constructor and concrete type.

## Author and select

    plystra interface create records.read
    plystra implement records.read/v1 --package ./records
    plystra capability create records.read --query --plugin records
    plystra capability implement records.read/v1 --plugin records
    plystra use email.send/v1 example.com/acme/email/smtp.New
    plystra inspect interfaces
    plystra inspect implementations

After scaffolding, edit the authored Interface and Implementation, add package tests, then run `plystra generate`. Interface IDs are provider-independent and use the exact `/vN` suffix. Constructors declare `//plystra:implements <interface-id>` and return one compatible value.

Capability creation and implementation never prompt by default. Pass `--plugin <directory-or-plugin-id>` as complete non-interactive input. Add `--interactive` only to request a terminal choice after enclosing and sole-Plugin inference remain ambiguous; terminal detection alone never prompts, and unavailable requested interaction fails before mutation.

An ordinary `T` field has no separate presence state: omission and its Go zero value normalize identically. A direct `*T` distinguishes absent from a present value, including zero or empty, while direct `**T` adds explicit null. Pointers are allowed only directly on message fields, with a maximum depth of two, and must not create a message cycle. A required ordinary field must occur in representations that retain occurrence; required pointer fields reject absence, while required `**T` still permits explicit null. Constraints apply to a present non-null innermost value.

The contract model treats nil and allocated-empty bytes, repeated values, and maps as the same canonical empty value without changing the pointer field's outer state. Generated proxies check pointer requiredness before constraints and copy normalized requests into isolated snapshots. Adapters give every target execution a fresh copy, and proxies validate and copy successful responses into caller-owned storage. Generated proxy packages export CopyRequest and CopyResponse for direct Implementation tests. Traversal fails closed beyond 64 levels or 65,536 nodes. Use errors.As to inspect a generated ValueError for the Interface, side, field path, and rule without exposing values or map keys. Invalid requests never enter the target; invalid responses return an internal contract error with no result. Ordinary required values may be zero. Caller cancellation returns independently with result_unknown after target entry; late results are discarded. Response validation and copying remain inside the tracked attempt. Generated InterfaceRuntime.Drain closes admission and waits for actual termination; Stop drains before any lifecycle cleanup. Drain and cleanup share the construction/startup cleanup timeout and any earlier caller deadline. Failed drain keeps dependencies live for a fresh bounded Stop retry. Each exact binding admits 64 attempts with no queue; permits remain held through target termination and response copying, even after caller cancellation. Saturation returns resource_exhausted with not_started (Connect/HTTP 429); JavaScript preserves both facts. The frozen model version 18 and Interface provenance v3 record every compiled policy field, including disabled stages and literal compatibility identities. No authored timeout means no added deadline. Authored concurrency, queue, retry, and circuit policies remain unsupported. Readiness, lifecycle-hook dependency access, retries, and separate telemetry remain unfinished. Pointer-bearing Interfaces may remain visible and retain shape and wire history, but this installed CLI does not yet generate the required Protobuf presence wrappers; keep them out of `http.expose` until installed capability support changes.

The five compatibility records currently emitted by this CLI are replaceable CLI-owned working records for current authored and generated projections, not accepted release baselines. Classification is per record and ownership entry, not directory-wide. `interface-metadata.json` uses schema v2 and records `contract_supplement_digest` so a new non-required pointer field is an additive candidate only when shape and supplement evidence agree that no other contract input changed. An owned canonical v1 metadata record migrates to v2 in the same generation transaction. Changing an existing field among `T`, `*T`, and `**T` is breaking compatibility. Pre-stable development may refresh working records in place; once an accepted stable baseline applies, the change requires a new Interface version. The stable-release assessment continues to report a version requirement until every public projection and immutable accepted ancestor also classifies a newly added pointer field as optional. Do not treat refreshed working records as accepted-release evidence.

Require an internal root by editing the selected document's `interfaces.require`. Public exposure belongs to the selected current-Project `http.expose` mapping. `interfaces.use` selects an exact compatible constructor but does not create a root by itself.

When one Implementation needs another Interface, accept the canonical Interface type as a constructor parameter and call its ordinary Go method. Use `plystra.Optional[T]` only for an optional Interface dependency. Do not import another concrete Implementation package.

Constructors only assemble values; resource acquisition and background work belong in lifecycle Start. Return a concrete pointer plus error. Assembly rejects nil success, redacts errors and panics, and cleans all returned lifecycle values after failure, including partial results and never-started values. Stop must tolerate those states. Use errors.As to find a generated assembly.InterfaceAssemblyError and RetryCleanup(ctx) to retry failed construction cleanup under its original timeout; this never restarts construction or publishes a failed runtime. Test constructor failures and startup rollback as well as the success path.

Construct semantic errors with invocation.NewSemanticError(code, cause) from github.com/plystra/kernel/invocation. Use errors.As with *invocation.SemanticError and Code() locally; structural SemanticErrorCode methods are not recognized. Wrap uncertain effects with invocation.NewResultUnknown(cause) before semantic translation. Generated error projection follows ordinary wrapping and joins up to 64 unwrap levels and 1,024 nodes, rejects conflicting or undeclared codes, and exposes no private cause. Completion remains independent of the primary code through Connect, transitional HTTP, and PlystraError.completion in the SDK.

Static Interface drain does not cover the transitional legacy Capability dispatcher; bootstrap does not yet drain that dispatcher.

## Completion checks

1. Run the authored package tests.
2. Run `plystra generate` and inspect ordinary generated diffs.
3. Run `plystra inspect interfaces` and `plystra inspect implementations` to confirm roots, selections, dependencies, and assembly membership.
4. Run `plystra check`.

See the [Project README](../../../../README.md), `plystra interface create --help`, and `plystra implement --help`.
