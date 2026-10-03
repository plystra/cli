# Diagnostics and recovery

Use read-only inspection before changing authored inputs:

    plystra inspect capabilities --format json
    plystra inspect
    plystra inspect modules
    plystra inspect interfaces
    plystra inspect implementations
    plystra inspect resources
    plystra inspect configuration
    plystra explain capability <capability-name>/vN
    plystra explain plugin <plugin-id>
    plystra explain config config.<constructor-symbol>.<field>
    plystra generate --check
    plystra check

`plystra inspect capabilities` is separate from Project inspection. It ignores the working directory, invalid Project state, `PLYSTRA_ENV`, and `PLYSTRA_CONFIG` while reporting exact installed commands and arguments, selectors, stable defaults, interaction and output modes, effect classes, schemas, bounds, toolchain identity, and independent `specified`, `parsed`, `generated`, `executed`, and `accepted` support stages. Planned commands are absent. Result, recovery, inspection, and graph schemas are available; standalone diagnostic and continuation schema roles remain explicitly unavailable. Its only option is `--format human|json`.

Inspect versioned Agent guidance before refreshing it:

    plystra guidance check
    plystra guidance sync
    plystra guidance sync --replace-generated

`guidance check` is always non-mutating. Ordinary sync changes or removes only unchanged prior-manifest-owned files, and any drift blocks the complete transaction. `--replace-generated` can replace only an existing bounded regular prior-owned file; missing prior-owned paths and desired paths absent from previous ownership remain blocked whether missing or occupied. Neither sync mode touches optional `local.md`, another unlisted file, a sibling skill, or repository-wide Agent instructions.

Reuse the same `--env` or `--config` selector for Project-bound inspection and explanation. `--format json` returns `plystra.result/v1` for creation, installed capability discovery, and all five explanation commands. Explanation nests `plystra.explain/v1` with diagnostics and `plystra.recovery/v1` actions; JSON stderr stays empty after initialization. Project inspect retains its current top-level schemas.

Explanation exits 2 for invalid invocation or subject, 3 for invalid Project state or missing targets, 4 for required decisions or missing prerequisites, and 8 for internal failures. Unknown failures use redacted `PLYSTRA_EXPLAIN_FAILED`. Recovery preserves the selector and names the exact source edit or finite choices. Provider choices edit `capabilities.use`; installed `plystra use` accepts only Interface Implementation constructors. Only executable actions and supported choice options carry a working directory and fully bound `argv`. Never execute unresolved placeholders or parse human display text as a shell command; run the action's independent verification afterward.

Actionable human failures end with one `Recovery:` block and one stable `Diagnostic: PLYSTRA_<AREA>_<CONDITION>` code. Source-bearing failures add deterministic module-relative `Source:` lines. Use the code as the automation identity, apply the recovery to the reported authored source, and rerun the same selected command.

Agent-guidance drift uses `PLYSTRA_AGENT_GUIDANCE_DRIFT` and reports every affected path as an `agent-guidance` source. An invalid ownership manifest uses `PLYSTRA_AGENT_GUIDANCE_MANIFEST_INVALID`. A manifest or transaction path that changes after inspection uses `PLYSTRA_PROJECT_CONCURRENT_CHANGE` with every deterministically known affected guidance path.

`PLYSTRA_PROTOBUF_POINTER_PROJECTION_UNSUPPORTED` reports the declaration-owning `http.expose` document at `1:1` as an `exposure` source when an active Interface needs unavailable pointer-presence wrappers. A sparse environment overlay may inherit that declaration from root `plystra.yaml`, so remove the exposure from the reported source and rerun generation with the same selector. The failed command leaves authored, generated, module, and compatibility files unchanged and does not expose an absolute path or pointer value.

Never print or persist resolved Secrets, unrestricted configuration values, avoidable absolute paths, or Module Cache paths while diagnosing a Project. Do not edit dependency source in the Module Cache or CLI-owned files under `generated/`.

See the [Project README](../../../../README.md) and the exact command help for diagnostic-specific recovery.
