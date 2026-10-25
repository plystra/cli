# Project and dependencies

Use this task for Project creation, module identity, the `--template` dependency selector, and ordinary Go Module dependencies.

## Supported operations

    plystra new app
    plystra new app --module example.com/acme/app
    plystra new app --template example.com/acme/platform@v1.2.3
    plystra new app --plugin records
    plystra add example.com/acme/email@v1.4.2
    plystra update example.com/acme/email@v1.5.0
    plystra remove example.com/acme/email

`plystra new` is non-interactive by default. Guidance is generated unless `--no-agent-guidance` is set. Git and CI default off and opt in with `--git` and `--github-ci`; prompts require `--interactive`.

The positional name is one safe child directory; `--module` sets its independent Go Module identity. A new Project contains root `plystra.yaml`, module files, and committed generated source, but no environment overlay, example configuration, or `go.work`.

Creation generates and builds the complete staged Project with `go build -mod=readonly ./...` before installation, without running application tests. A build failure leaves no target Project.

`--template` records the selected Project module as one ordinary direct Go Module dependency. The dependency must expose a regular root `plystra.yaml` marker, but its configuration and source are never copied or activated. After creation it is indistinguishable from a dependency added with `plystra add`.

`--format json` returns one `plystra.result/v1` document. Success nests `plystra.project-created/v1`; enter `payload.directory` and run `plystra check` independently.

Dependency commands compare captured old and final declarations. They remove only configuration whose last proven owner disappears and binding parameters proven obsolete on that consumer. Still-owned configuration, Resource instances, surviving bindings, and unselected documents remain unchanged. Unknown old declarations prove no automatic ownership or parameter cleanup; a never-declared binding remains an error. Cleanup, generation, module metadata, and the private runtime baseline share rollback. Ambient PLYSTRA_ENV or PLYSTRA_CONFIG selects one environment or replacement document; dependency commands have no explicit selector flags yet. Public change plans remain non_exact and cannot authorize installation.

## Completion checks

1. Confirm `go.mod` has the intended module identity and direct dependencies.
2. Confirm root `plystra.yaml` is the only automatically created configuration document and contains only current-Project configuration.
3. Run `plystra generate --check`, `plystra check`, and the relevant Go tests.
4. Follow any emitted `Recovery:` action before retrying.

See the [Project README](../../../../README.md) and `plystra new --help` for the installed command contract.
