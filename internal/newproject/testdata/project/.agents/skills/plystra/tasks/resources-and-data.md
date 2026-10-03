# Resources and Data

The installed CLI `0.1.0` discovers consumer Resource contracts in current and dependency Projects. Use `plystra inspect resources --format json` to inspect exact IDs, packages, owning sources, and contract_digest values without mutation. Declare one non-generic defined Go interface Resource with exactly one //plystra:resource <resource-id> directive. Do not attach Interface directives or interface.yaml projection metadata, and keep lifecycle control on providers. Duplicate IDs and malformed declarations or bounded public type shapes fail with source-bearing PLYSTRA_RESOURCE_ID_DUPLICATE, PLYSTRA_RESOURCE_DECLARATION_INVALID, or PLYSTRA_RESOURCE_CONTRACT_INVALID diagnostics.

Contract digests retain transitive public fields and methods, normalized aliases, instantiated generic arguments, and recursive references, with limits of 64 type-reference levels and 65,536 public shape nodes. They exclude comments, source locations, private implementation details, and unrelated provider lifecycle hooks. Installed support reports resource.contract separately; Resource instance configuration, provider construction, binding, Data schema/query generation, and `data migration plan|apply|status` remain unsupported.

Do not simulate those operations with handwritten files under `generated/`, legacy Plugin conventions, or an unversioned migration script. Check `plystra help` after upgrading and run `plystra inspect capabilities --format json` before adopting any Resource contract, provider, backend, Data compiler, or migration workflow.

Until those commands are present, keep persistence behavior inside ordinary authored Implementation code and its tests without claiming Plystra-managed Data generation or migration support.

See the [Project README](../../../../README.md) for the currently supported application surface.
