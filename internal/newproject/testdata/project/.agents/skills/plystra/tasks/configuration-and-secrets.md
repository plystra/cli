# Configuration and Secrets

Use one selected current-Project configuration mode consistently.

## Select the document

- No selector: root `plystra.yaml` only, including its explicit `composition.adopt` set.
- `--env production`: root plus one sparse project-root `plystra.production.yaml` overlay.
- `--config deploy/customer-a.yaml`: one complete replacement document; root remains only the Project marker.

Use the same selector for mutation, generation, inspection, checking, and startup:

    plystra generate --env production
    plystra generate --check --env production
    plystra inspect configuration --env production
    plystra check --env production
    go run ./generated/go/application --env production

Do not combine `--env` and `--config`. `PLYSTRA_ENV` and `PLYSTRA_CONFIG` supply the same selectors when explicit flags are absent.

Configuration values belong under the exact constructor-owned `config.<constructor-symbol>` object. Keep Secret values out of YAML, generated source, diagnostics, SDKs, and tests. A Secret field contains only a valid `env` or absolute `file` reference, and generation validates the reference without resolving its value.

Authored static Interface timeout policies execute through the selected binding. One total budget starts before request validation and copying, is capped by an earlier caller deadline, and includes target and response processing. Absence adds no deadline. Complete compiled policy values and literal schema/compiler/defaults versions are frozen before runtime; mismatches fail closed. Dormant policies remain intent outside executable identity until activation. Inspect capabilities reports support stages, exact defaults, and duration bounds. Authored concurrency, queue, retry, and circuit forms remain unsupported. Transitional legacy Capability wrappers do not include preparation and completion in the Kernel budget; active authored timeouts on that path still fail with PLYSTRA_POLICY_NOT_ENFORCED. Capability discovery reports that exception as legacy.capability-timeout with executed=no and accepted=no.

## Completion checks

1. Inspect the selected layer and ownership with `plystra inspect configuration`.
2. Regenerate with the same selector.
3. Run `plystra generate --check` and `plystra check` with that selector.
4. Confirm generated records contain no configuration values, Secret targets, resolved Secrets, or machine paths.

See the [Project README](../../../../README.md) and `plystra generate --help`.
