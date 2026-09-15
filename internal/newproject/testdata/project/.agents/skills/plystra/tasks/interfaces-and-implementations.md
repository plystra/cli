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

The contract model treats nil and allocated-empty bytes, repeated values, and maps as the same canonical empty value without changing the pointer field's outer state. Current support implements that model for declaration parsing, `interface.yaml` constraint and example validation, and compatibility classification. Generated proxies and adapters do not yet apply pointer-aware requiredness, constraint checks, or empty-collection normalization to internal calls. Pointer-bearing Interfaces may remain visible and retain shape and wire history, but this installed CLI does not yet generate the required Protobuf presence wrappers; keep them out of `http.expose` until installed capability support changes.

The five compatibility records currently emitted by this CLI are replaceable CLI-owned working records for current authored and generated projections, not accepted release baselines. Classification is per record and ownership entry, not directory-wide. `interface-metadata.json` uses schema v2 and records `contract_supplement_digest` so a new non-required pointer field is an additive candidate only when shape and supplement evidence agree that no other contract input changed. An owned canonical v1 metadata record migrates to v2 in the same generation transaction. Changing an existing field among `T`, `*T`, and `**T` is breaking compatibility. Pre-stable development may refresh working records in place; once an accepted stable baseline applies, the change requires a new Interface version. The stable-release assessment continues to report a version requirement until every public projection and immutable accepted ancestor also classifies a newly added pointer field as optional. Do not treat refreshed working records as accepted-release evidence.

Require an internal root by editing the selected document's `interfaces.require`. Public exposure belongs to the selected current-Project `http.expose` mapping. `interfaces.use` selects an exact compatible constructor but does not create a root by itself.

When one Implementation needs another Interface, accept the canonical Interface type as a constructor parameter and call its ordinary Go method. Use `plystra.Optional[T]` only for an optional Interface dependency. Do not import another concrete Implementation package.

## Completion checks

1. Run the authored package tests.
2. Run `plystra generate` and inspect ordinary generated diffs.
3. Run `plystra inspect interfaces` and `plystra inspect implementations` to confirm roots, selections, dependencies, and assembly membership.
4. Run `plystra check`.

See the [Project README](../../../../README.md), `plystra interface create --help`, and `plystra implement --help`.
