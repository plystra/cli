# Diagnostics and recovery

Use read-only inspection before changing authored inputs:

    plystra inspect
    plystra inspect modules
    plystra inspect interfaces
    plystra inspect implementations
    plystra inspect configuration
    plystra explain capability <capability-name>/vN
    plystra explain plugin <plugin-id>
    plystra explain config config.<constructor-symbol>.<field>
    plystra generate --check
    plystra check

Reuse the same `--env` or `--config` selector. `--format json` returns the installed versioned inspect or explain schema; this release does not yet wrap every command in the planned shared result envelope.

Actionable human failures end with one `Recovery:` block and one stable `Diagnostic: PLYSTRA_<AREA>_<CONDITION>` code. Source-bearing failures add deterministic module-relative `Source:` lines. Use the code as the automation identity, apply the recovery to the reported authored source, and rerun the same selected command.

Never print or persist resolved Secrets, unrestricted configuration values, avoidable absolute paths, or Module Cache paths while diagnosing a Project. Do not edit dependency source in the Module Cache or CLI-owned files under `generated/`.

See the [Project README](../../../../README.md) and the exact command help for diagnostic-specific recovery.
