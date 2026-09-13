# Project and dependencies

Use this task for Project creation, module identity, templates, and ordinary Go Module dependencies.

## Supported operations

    plystra new app
    plystra new app --module example.com/acme/app
    plystra new app --template example.com/acme/platform@v1.2.3
    plystra new app --plugin records
    plystra add example.com/acme/email@v1.4.2
    plystra update example.com/acme/email@v1.5.0
    plystra remove example.com/acme/email

Project creation is non-interactive by default. Agent guidance is generated unless `--no-agent-guidance` is supplied. Git initialization and GitHub CI default off; `--git` and `--github-ci` opt in. Only `--interactive` permits prompts for omitted tool choices.

The positional Project name is one safe child directory. `--module` sets an independent Go Module identity. A new Project contains root `plystra.yaml`, ordinary module files, and committed CLI-owned generated source; it does not create an environment overlay, example configuration, or `go.work`.

This installed CLI accepts one `--template` query but does not yet expose `--adopt-export`. Treat template workflows as transitional and rely on the installed help and resulting authored Project state rather than planned export-adoption behavior.

## Completion checks

1. Confirm `go.mod` has the intended module identity and direct dependencies.
2. Confirm root `plystra.yaml` is the only automatically created configuration document.
3. Run `plystra generate --check`, `plystra check`, and the relevant Go tests.
4. Follow any emitted `Recovery:` action before retrying.

See the [Project README](../../../../README.md) and `plystra new --help` for the installed command contract.
