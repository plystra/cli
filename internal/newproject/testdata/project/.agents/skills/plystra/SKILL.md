---
name: plystra
description: Develop this Plystra Project through installed CLI commands, authored Go, versioned Interfaces, Implementations, and plystra.yaml.
---

# Plystra Project Guidance

Project module: `example.com/acme/my-app`

Installed support: CLI `0.1.0`, Kernel `v0.0.0-20260724160327-26ece9a0df89`, specification revision `c98210bdaa626511a7f6c45a43402354bdb6b331`.

Start with `plystra help` and the exact subcommand help. Read only the task references needed for the current change.

## Route the task

- [Project and dependencies](tasks/project-and-dependencies.md): create a Project, inspect its layout, or change Go Module dependencies.
- [Interfaces and Implementations](tasks/interfaces-and-implementations.md): define, implement, require, expose, or select an Interface.
- [Configuration and Secrets](tasks/configuration-and-secrets.md): edit root, environment, or complete-replacement configuration safely.
- [Resources and Data](tasks/resources-and-data.md): check whether this installed release supports the required Resource or Data workflow.
- [Diagnostics and recovery](tasks/diagnostics-and-recovery.md): inspect selected state and follow source-bearing diagnostics.
- [Verify, build, and release](tasks/verify-build-and-release.md): prove generated state, tests, builds, and installed release support.

## Ownership

The CLI owns this `SKILL.md`, `manifest.json`, and the files listed by that manifest. Do not edit those projections. Project-specific guidance belongs in optional `local.md`; the CLI never creates, edits, deletes, or claims it.

The CLI also never claims unlisted files, sibling skills, or repository-wide Agent instructions.

`plystra guidance check` compares this projection with the installed catalog without mutation. Ordinary `plystra guidance sync` installs an absent projection only when desired paths are free, then refreshes or removes only unchanged prior-manifest-owned files. A desired path absent from previous ownership blocks sync whether missing or occupied. Any blocking drift leaves every Project file unchanged.

`plystra guidance sync --replace-generated` may discard edits only in existing bounded regular prior-manifest-owned files. Missing prior-owned paths and desired paths absent from previous ownership remain blocked. Move Project-specific content to `local.md`, restore one complete matching generated projection or move an occupied conflict, and check again before synchronizing.
