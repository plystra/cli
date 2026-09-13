# Interfaces and Implementations

Use authored Go as the contract and implementation source. A Plystra Interface is a versioned one-method Go interface; an Implementation is an ordinary constructor and concrete type.

## Author and select

    plystra interface create records.read
    plystra implement records.read/v1 --package ./records
    plystra use email.send/v1 example.com/acme/email/smtp.New
    plystra inspect interfaces
    plystra inspect implementations

After scaffolding, edit the authored Interface and Implementation, add package tests, then run `plystra generate`. Interface IDs are provider-independent and use the exact `/vN` suffix. Constructors declare `//plystra:implements <interface-id>` and return one compatible value.

Require an internal root by editing the selected document's `interfaces.require`. Public exposure belongs to the selected current-Project `http.expose` mapping. `interfaces.use` selects an exact compatible constructor but does not create a root by itself.

When one Implementation needs another Interface, accept the canonical Interface type as a constructor parameter and call its ordinary Go method. Use `plystra.Optional[T]` only for an optional Interface dependency. Do not import another concrete Implementation package.

## Completion checks

1. Run the authored package tests.
2. Run `plystra generate` and inspect ordinary generated diffs.
3. Run `plystra inspect interfaces` and `plystra inspect implementations` to confirm roots, selections, dependencies, and assembly membership.
4. Run `plystra check`.

See the [Project README](../../../../README.md), `plystra interface create --help`, and `plystra implement --help`.
