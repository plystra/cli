# Configuration and Secrets

Use one selected current-Project configuration mode consistently.

Remove exact interfaces.use, interfaces.policies, and http.expose entries only with {$remove: true}. Null, empty values, false or string-valued markers, and sibling fields are invalid. Keep the authored tombstone even if no lower entry exists; it prevents later dependency additions from restoring the entry. Regenerate and rebuild for runtime compatibility version 7. Nested constructor fields and CORS retain transitional null handling; this is not full typed-removal support.

Remove a whole config.<constructor-symbol> entry only with {$remove: true}. Null, sequences, malformed markers, and sibling fields are invalid at that boundary; {} remains configuration, not removal. The constructor needs a discovered compiled Config schema. Keep exclusions even without lower configuration; inspection retains removal ownership and suppressed sources. Generated runtime loading strips whole-entry markers before construction, but removing required configuration still fails before any constructor runs. Nested field removal and nullable typed-value composition remain unfinished.

During typed CLI composition, a supplied non-null pointer field replaces its complete lower value, including pointers to structs and multiple pointer layers. Omission inherits; {} replaces a pointed-to struct without inheriting omitted fields. Only non-pointer fixed structs compose field by field. Different whole-pointer values in adopted exports conflict even when their fields are disjoint; identical normalized values deduplicate. Inspection retains one redacted atomic value and its sources. Nullable-value handling and standalone typed runtime loading remain incomplete.

An interfaces.require sequence replaces the complete lower explicit requirement set; [] clears it. Omission and {} inherit, while {add: [...], remove: [...]} changes only named members. Adopted exports form one unordered lower layer, including self-adopted exports. A later sparse overlay preserves an earlier complete-set boundary. Exposure, constructor dependencies, and intrinsic Kernel requirements remain independent. Inspection and explanation retain suppressed sources. New Projects use require: {} to preserve explicit template adoptions. Generated runtime compatibility version 7 uses these set rules for selected current-Project documents; source-independent adopted-export runtime baselines remain unfinished.

A composition.adopt sequence replaces the complete lower adoption set; [] clears it. Omission and {} inherit, while sparse add/remove entries modify exact module/export identities. Complete and sparse declarations have distinct layer identities even when their members match. Inspection and explanation retain excluded lower members under the composition.adopt complete-set boundary and name its owning document. A replacement document never inherits root adoptions. Source-independent adopted-export runtime composition remains unfinished.

Reusable exports have no lower layer and cannot contain the reserved one-entry $remove mapping, even inside nested configuration, collections, or an unadopted Resource fragment. This does not turn ordinary null, empty, or zero values into removals; adopted values still require compiled-type validation. Correct the export in the owning Project marker reported by PLYSTRA_PROJECT_MANIFEST_INVALID, then rerun the same generation or check command.

Resource export syntax is checked even without adoption: instances contain only use and config; bind contains only implementations and instances. Instance names use dot-separated lower-kebab segments within 128 ASCII bytes, constructors use exact symbols, and binding leaves map nonblank Go parameter identifiers to instance names. Structural and configuration mappings need unique string keys. Resource adoption remains unsupported; syntax validation does not resolve provider types, required fields, or binding targets.

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

Authored static Interface timeout and replay-safe retry policies execute through the selected binding. Retry requires timeout and the explicit eligibility: replay_safe assertion about the binding and its downstream effects; safety is never inferred. max_attempts counts the first attempt, defaults to 2, and permits 2 through 16; backoff defaults to 0s and accepts nonnegative Go durations. One total budget starts before request validation and copying, is capped by an earlier caller deadline, and includes all attempts, backoff, and response processing. Each attempt receives a fresh copy of the original request snapshot and starts only after the previous target terminates. The outermost retry-enabled binding owns replay; nested bindings suppress their own retries. Only not_started resource exhaustion and result_known unavailable or resource exhaustion can retry. Semantic errors, cancellation, deadlines, internal or validation failures, and result_unknown never replay; exhaustion retains the final safe category, completion, and bounded attempt count. Without retry there is one attempt; without timeout there is no added deadline. Complete compiled policy values and literal schema/compiler/defaults versions are frozen before runtime; mismatches fail closed. Dormant policies remain intent outside executable identity until activation. Inspect capabilities reports support stages, exact defaults, and duration bounds. Authored concurrency, queue, and circuit forms remain unsupported. Transitional legacy Capability wrappers do not include preparation and completion in the Kernel budget; active authored policies on that path still fail with PLYSTRA_POLICY_NOT_ENFORCED. Capability discovery reports that exception as legacy.capability-timeout with executed=no and accepted=no.

## Completion checks

1. Inspect the selected layer and ownership with `plystra inspect configuration`.
2. Regenerate with the same selector.
3. Run `plystra generate --check` and `plystra check` with that selector.
4. Confirm generated records contain no configuration values, Secret targets, resolved Secrets, or machine paths.

See the [Project README](../../../../README.md) and `plystra generate --help`.
