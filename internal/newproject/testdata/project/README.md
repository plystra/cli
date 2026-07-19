# my-app

This is the Plystra Project `example.com/acme/my-app`.

Local plugins belong in direct child directories containing `plugin.yaml`. Do not add a root `plugins/` container.

## Development

```powershell
plystra plugin create records
plystra capability create records.read --plugin records --expose
plystra generate
plystra generate --check
go test ./...
go vet ./...
```

Mutating Plystra commands regenerate automatically. Add an ordinary Go Module dependency with `plystra add github.com/acme/platform@v1.0.0`, update it with `plystra update github.com/acme/platform@v1.1.0`, and remove it with `plystra remove github.com/acme/platform`. A Project created with `plystra new app --template github.com/acme/platform@v1.0.0` retains the selected template as the same kind of ordinary direct dependency: its root declarations, typed local operational values, and Secret-reference placeholders compose into this Project, but its source is not copied and it receives no resolution priority. Creation validates those values without reading referenced `env` or `file` Secrets; generated source and manifest provenance contain neither reference targets nor resolved values. Run `plystra generate` after manual declaration edits and use `plystra generate --check` as the read-only consistency gate.

A template's default Provider model must be unambiguous. If several compatible Plugins provide one required Capability, the template publisher must record one `capabilities.use` choice in the template's root `plystra.yaml` and publish a corrected version. Creation otherwise reports every candidate and leaves no target Project to repair.

The template's complete effective graph must contain only public Go Modules. Creation rejects every direct or transitive module matched by the effective `GOPRIVATE` setting, reports its selected `path@version`, and leaves no target Project. Publish or replace a genuinely private dependency before publishing the template, or correct an overbroad Go privacy setting before retrying.

Every dependency Plystra Project in the template graph must be portable without a relative Go Module `replace`. Creation reports each remaining directive with stable `module@version/go.mod` provenance and leaves no target Project. Publish the referenced module versions and remove the relative replacements before publishing a corrected template.

The staged generated application must be a fixed point. Creation installs generated output and then runs an immediate `plystra generate --check` equivalent. Dependency-composition drift or any changed, missing, unexpected, or obsolete generated path rejects the template and restores the transaction. The publisher must make generation deterministic, run `plystra generate` followed by `plystra generate --check` in a fresh Project directory, and publish a corrected module version.

Root `plystra.yaml` is the mandatory Project marker and shared default configuration. A sparse project-root `plystra.production.yaml` can be selected with `plystra generate --env production` and checked with the same selector; it is never created or loaded implicitly. To use one complete alternative current-Project document, run `plystra generate --config deploy/customer-a.yaml`. Root configuration is not merged beneath an explicitly selected file. `PLYSTRA_ENV` and `PLYSTRA_CONFIG` supply the corresponding selector for automation; select exactly one mode.

When several compatible Plugins provide one required Capability, select one with `plystra use <capability-name>/vN <plugin-id>`. Add `--env <environment>` to write only that sparse overlay or `--config <yaml-path>` to write only one complete replacement configuration; the command regenerates and validates with the same selection.

Generated source under `generated/` is owned by the Plystra CLI. Do not edit it manually; commit it to Git.

## Continuous integration

GitHub Actions runs `go test ./...` and `go vet ./...` on Linux, Windows, and macOS, plus the Go race suite on Linux. Keep `.github/workflows/ci.yml` aligned with the local validation commands.

## AI coding agents

Project-specific Plystra development guidance lives in `.agents/skills/plystra/SKILL.md`. Keep it synchronized with this module's commands, generated-code ownership, and architecture.
