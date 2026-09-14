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

Inspect versioned Agent guidance before refreshing it:

    plystra guidance check
    plystra guidance sync
    plystra guidance sync --replace-generated

`guidance check` is always non-mutating. Ordinary sync changes or removes only unchanged prior-manifest-owned files, and any drift blocks the complete transaction. `--replace-generated` can replace only an existing bounded regular prior-owned file; missing prior-owned paths and desired paths absent from previous ownership remain blocked whether missing or occupied. Neither sync mode touches optional `local.md`, another unlisted file, a sibling skill, or repository-wide Agent instructions.

Reuse the same `--env` or `--config` selector. `--format json` returns the installed versioned inspect or explain schema; this release does not yet wrap every command in the planned shared result envelope.

Actionable human failures end with one `Recovery:` block and one stable `Diagnostic: PLYSTRA_<AREA>_<CONDITION>` code. Source-bearing failures add deterministic module-relative `Source:` lines. Use the code as the automation identity, apply the recovery to the reported authored source, and rerun the same selected command.

Agent-guidance drift uses `PLYSTRA_AGENT_GUIDANCE_DRIFT` and reports every affected path as an `agent-guidance` source. An invalid ownership manifest uses `PLYSTRA_AGENT_GUIDANCE_MANIFEST_INVALID`. A manifest or transaction path that changes after inspection uses `PLYSTRA_PROJECT_CONCURRENT_CHANGE` with every deterministically known affected guidance path.

Never print or persist resolved Secrets, unrestricted configuration values, avoidable absolute paths, or Module Cache paths while diagnosing a Project. Do not edit dependency source in the Module Cache or CLI-owned files under `generated/`.

See the [Project README](../../../../README.md) and the exact command help for diagnostic-specific recovery.
