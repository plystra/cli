# Resources and Data

The installed CLI `0.1.0` does not expose public Resource declaration, Data schema/query generation, or `data migration plan|apply|status` operations.

Do not simulate those operations with handwritten files under `generated/`, legacy Plugin conventions, or an unversioned migration script. Check `plystra help` after upgrading and use the installed capability facts before adopting any Resource contract, provider, backend, Data compiler, or migration workflow.

Until those commands are present, keep persistence behavior inside ordinary authored Implementation code and its tests without claiming Plystra-managed Data generation or migration support.

See the [Project README](../../../../README.md) for the currently supported application surface.
