// Package agentguidance owns the installed release's Project guidance catalog
// and deterministic Project projection.
package agentguidance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/version"
)

const (
	// Schema identifies the Project guidance ownership manifest.
	Schema = "plystra.agent-guidance/v1"
	// Root is the Project-relative root owned by the Plystra guidance catalog.
	Root = ".agents/skills/plystra"
	// ManifestPath is the Project-relative ownership manifest path.
	ManifestPath = Root + "/manifest.json"

	catalogSchema                 = "plystra.agent-guidance-catalog/v1"
	maximumGuidanceFileBytes      = 64 << 10
	maximumGuidanceManifestFiles  = 1024
	maximumGuidanceComponentBytes = 255
	maximumGuidancePathBytes      = 1024
	moduleToken                   = "{{MODULE_PATH}}"
	cliToken                      = "{{CLI_VERSION}}"
	kernelToken                   = "{{KERNEL_VERSION}}"
	specToken                     = "{{SPECIFICATION_REVISION}}"
)

// File is one deterministic Project-relative guidance projection.
type File struct {
	path string
	data []byte
}

// Path returns the slash-separated Project-relative path.
func (f File) Path() string { return f.path }

// Data returns a defensive copy of the projected bytes.
func (f File) Data() []byte { return append([]byte(nil), f.data...) }

// Manifest records the installed release and every manifest-owned projection.
type Manifest struct {
	Schema                string         `json:"schema"`
	CLIVersion            string         `json:"cli_version"`
	KernelVersion         string         `json:"kernel_version"`
	SpecificationRevision string         `json:"specification_revision"`
	CatalogDigest         string         `json:"catalog_digest"`
	Files                 []ManifestFile `json:"files"`
}

// ManifestFile records one manifest-owned Project-relative projection.
type ManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Projection is one complete deterministic rendering of the installed catalog.
type Projection struct {
	modulePath string
	files      []File
	manifest   Manifest
}

// Files returns defensive copies of all projected files, including the
// ownership manifest as the final entry.
func (p Projection) Files() []File {
	files := make([]File, len(p.files))
	for index, file := range p.files {
		files[index] = File{path: file.path, data: file.Data()}
	}
	return files
}

// Manifest returns a defensive copy of the projection manifest.
func (p Projection) Manifest() Manifest {
	manifest := p.manifest
	manifest.Files = append([]ManifestFile(nil), p.manifest.Files...)
	return manifest
}

type catalogDocument struct {
	Schema                string        `json:"schema"`
	CLIVersion            string        `json:"cli_version"`
	KernelVersion         string        `json:"kernel_version"`
	SpecificationRevision string        `json:"specification_revision"`
	RouterTemplate        string        `json:"router_template"`
	Tasks                 []catalogTask `json:"tasks"`
}

type catalogTask struct {
	Path            string `json:"path"`
	Title           string `json:"title"`
	Purpose         string `json:"purpose"`
	ContentTemplate string `json:"content_template"`
}

var tasks = []catalogTask{
	{Path: Root + "/tasks/project-and-dependencies.md", Title: "Project and dependencies", Purpose: "Create a Project, inspect its layout, and change ordinary Go Module dependencies.", ContentTemplate: projectAndDependenciesTask},
	{Path: Root + "/tasks/interfaces-and-implementations.md", Title: "Interfaces and Implementations", Purpose: "Author one-method Interfaces, implement them in ordinary Go, and select exact constructors.", ContentTemplate: interfacesAndImplementationsTask},
	{Path: Root + "/tasks/configuration-and-secrets.md", Title: "Configuration and Secrets", Purpose: "Choose one configuration mode, keep Secret values outside authored files, and regenerate consistently.", ContentTemplate: configurationAndSecretsTask},
	{Path: Root + "/tasks/resources-and-data.md", Title: "Resources and Data", Purpose: "Check installed Data support before declaring Resources, schemas, queries, or migrations.", ContentTemplate: resourcesAndDataTask},
	{Path: Root + "/tasks/diagnostics-and-recovery.md", Title: "Diagnostics and recovery", Purpose: "Inspect the selected model and apply stable source-bearing recovery without exposing private values.", ContentTemplate: diagnosticsAndRecoveryTask},
	{Path: Root + "/tasks/verify-build-and-release.md", Title: "Verify, build, and release", Purpose: "Verify authored and generated state through the public CLI and ordinary Go Module tooling.", ContentTemplate: verifyBuildAndReleaseTask},
}

// Render projects the installed catalog for one validated Project module path.
func Render(modulePath string) (Projection, error) {
	if !validModuleLabel(modulePath) {
		return Projection{}, fmt.Errorf("render Plystra Agent guidance: invalid module path label %q", modulePath)
	}
	catalogJSON, err := json.Marshal(catalogDocument{
		Schema:                catalogSchema,
		CLIVersion:            version.Current,
		KernelVersion:         version.KernelVersion,
		SpecificationRevision: version.SpecificationRevision,
		RouterTemplate:        routerTemplate,
		Tasks:                 tasks,
	})
	if err != nil {
		return Projection{}, fmt.Errorf("render Plystra Agent guidance catalog: %w", err)
	}

	files := make([]File, 0, len(tasks)+2)
	files = append(files, File{path: Root + "/SKILL.md", data: renderTemplate(routerTemplate, modulePath)})
	for _, task := range tasks {
		files = append(files, File{path: task.Path, data: renderTemplate(task.ContentTemplate, modulePath)})
	}

	manifest := Manifest{
		Schema:                Schema,
		CLIVersion:            version.Current,
		KernelVersion:         version.KernelVersion,
		SpecificationRevision: version.SpecificationRevision,
		CatalogDigest:         digest(catalogJSON),
		Files:                 make([]ManifestFile, 0, len(files)),
	}
	for _, file := range files {
		manifest.Files = append(manifest.Files, ManifestFile{Path: file.path, SHA256: digest(file.data)})
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Projection{}, fmt.Errorf("render Plystra Agent guidance manifest: %w", err)
	}
	manifestJSON = append(manifestJSON, '\n')
	files = append(files, File{path: ManifestPath, data: manifestJSON})
	return Projection{modulePath: modulePath, files: files, manifest: manifest}, nil
}

// ParseManifest decodes and validates one v1 ownership manifest. It accepts
// older release facts while keeping every owned path confined to the Plystra
// skill root so a later synchronizer can reason about it safely.
func ParseManifest(data []byte) (Manifest, error) {
	if len(data) == 0 || len(data) > maximumGuidanceFileBytes {
		return Manifest{}, fmt.Errorf("invalid Plystra Agent guidance manifest: document must contain 1..%d bytes", maximumGuidanceFileBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode Plystra Agent guidance manifest: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected trailing JSON value")
		}
		return Manifest{}, fmt.Errorf("decode Plystra Agent guidance manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	manifest.Files = append([]ManifestFile(nil), manifest.Files...)
	return manifest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Schema != Schema {
		return fmt.Errorf("invalid Plystra Agent guidance manifest: schema must equal %q", Schema)
	}
	for name, value := range map[string]string{
		"cli_version":            manifest.CLIVersion,
		"kernel_version":         manifest.KernelVersion,
		"specification_revision": manifest.SpecificationRevision,
	} {
		if !validFact(value) {
			return fmt.Errorf("invalid Plystra Agent guidance manifest: %s is invalid", name)
		}
	}
	if !validDigest(manifest.CatalogDigest) {
		return errors.New("invalid Plystra Agent guidance manifest: catalog_digest is invalid")
	}
	if len(manifest.Files) == 0 || len(manifest.Files) > maximumGuidanceManifestFiles {
		return fmt.Errorf("invalid Plystra Agent guidance manifest: files must contain 1..%d entries", maximumGuidanceManifestFiles)
	}
	previous := ""
	aliases := make(map[string]string, len(manifest.Files))
	for index, file := range manifest.Files {
		if !validOwnedPath(file.Path) {
			return fmt.Errorf("invalid Plystra Agent guidance manifest: files[%d].path is invalid", index)
		}
		if previous != "" && file.Path <= previous {
			return errors.New("invalid Plystra Agent guidance manifest: files must be unique and sorted by path")
		}
		alias := guidancePathAlias(file.Path)
		if existing, found := aliases[alias]; found && existing != file.Path {
			return errors.New("invalid Plystra Agent guidance manifest: files must not contain case-insensitive path aliases")
		}
		aliases[alias] = file.Path
		if !validDigest(file.SHA256) {
			return fmt.Errorf("invalid Plystra Agent guidance manifest: files[%d].sha256 is invalid", index)
		}
		previous = file.Path
	}
	return nil
}

func validOwnedPath(value string) bool {
	if len(value) == 0 || len(value) > maximumGuidancePathBytes || !fs.ValidPath(value) || !strings.HasPrefix(value, Root+"/") || strings.ContainsRune(value, '\\') || path.Clean(value) != value {
		return false
	}
	if strings.EqualFold(value, ManifestPath) || strings.EqualFold(value, Root+"/local.md") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if !validGuidancePathComponent(component) {
			return false
		}
	}
	return true
}

func validGuidancePathComponent(value string) bool {
	if value == "" || len(value) > maximumGuidanceComponentBytes || strings.HasSuffix(value, ".") {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '.' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	base := value
	if index := strings.IndexByte(base, '.'); index >= 0 {
		base = base[:index]
	}
	upper := strings.ToUpper(base)
	switch upper {
	case "CON", "PRN", "AUX", "NUL":
		return false
	}
	if len(upper) == 4 && (upper[:3] == "COM" || upper[:3] == "LPT") && upper[3] >= '1' && upper[3] <= '9' {
		return false
	}
	return true
}

func guidancePathAlias(value string) string { return strings.ToLower(value) }

func validModuleLabel(value string) bool {
	return validFact(value) && !strings.ContainsRune(value, '`') && len(value) <= 1024
}

func validFact(value string) bool {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return false
			}
		}
	}
	return true
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func renderTemplate(template, modulePath string) []byte {
	return []byte(strings.NewReplacer(moduleToken, modulePath, cliToken, version.Current, kernelToken, version.KernelVersion, specToken, version.SpecificationRevision).Replace(template))
}

const routerTemplate = `---
name: plystra
description: Develop this Plystra Project through installed CLI commands, authored Go, versioned Interfaces, Implementations, and plystra.yaml.
---

# Plystra Project Guidance

Project module: ` + "`" + moduleToken + "`" + `

Installed support: CLI ` + "`" + cliToken + "`" + `, Kernel ` + "`" + kernelToken + "`" + `, specification revision ` + "`" + specToken + "`" + `.

Start with ` + "`" + `plystra help` + "`" + ` and the exact subcommand help. Read only the task references needed for the current change.

## Route the task

- [Project and dependencies](tasks/project-and-dependencies.md): create a Project, inspect its layout, or change Go Module dependencies.
- [Interfaces and Implementations](tasks/interfaces-and-implementations.md): define, implement, require, expose, or select an Interface.
- [Configuration and Secrets](tasks/configuration-and-secrets.md): edit root, environment, or complete-replacement configuration safely.
- [Resources and Data](tasks/resources-and-data.md): check whether this installed release supports the required Resource or Data workflow.
- [Diagnostics and recovery](tasks/diagnostics-and-recovery.md): inspect selected state and follow source-bearing diagnostics.
- [Verify, build, and release](tasks/verify-build-and-release.md): prove generated state, tests, builds, and installed release support.

## Ownership

The CLI owns this ` + "`" + `SKILL.md` + "`" + `, ` + "`" + `manifest.json` + "`" + `, and the files listed by that manifest. Do not edit those projections. Project-specific guidance belongs in optional ` + "`" + `local.md` + "`" + `; the CLI never creates, edits, deletes, or claims it.

The CLI also never claims unlisted files, sibling skills, or repository-wide Agent instructions.

` + "`" + `plystra guidance check` + "`" + ` compares this projection with the installed catalog without mutation. Ordinary ` + "`" + `plystra guidance sync` + "`" + ` installs an absent projection only when desired paths are free, then refreshes or removes only unchanged prior-manifest-owned files. A desired path absent from previous ownership blocks sync whether missing or occupied. Any blocking drift leaves every Project file unchanged.

` + "`" + `plystra guidance sync --replace-generated` + "`" + ` may discard edits only in existing bounded regular prior-manifest-owned files. Missing prior-owned paths and desired paths absent from previous ownership remain blocked. Move Project-specific content to ` + "`" + `local.md` + "`" + `, restore one complete matching generated projection or move an occupied conflict, and check again before synchronizing.
`

const projectAndDependenciesTask = `# Project and dependencies

Use this task for Project creation, module identity, templates, and ordinary Go Module dependencies.

## Supported operations

    plystra new app
    plystra new app --module example.com/acme/app
    plystra new app --template example.com/acme/platform@v1.2.3 --adopt-export application
    plystra new app --plugin records
    plystra add example.com/acme/email@v1.4.2
    plystra update example.com/acme/email@v1.5.0
    plystra remove example.com/acme/email

` + "`" + `plystra new` + "`" + ` is non-interactive by default. Guidance is generated unless ` + "`" + `--no-agent-guidance` + "`" + ` is set. Git and CI default off and opt in with ` + "`" + `--git` + "`" + ` and ` + "`" + `--github-ci` + "`" + `; prompts require ` + "`" + `--interactive` + "`" + `.

The positional name is one safe child directory; ` + "`" + `--module` + "`" + ` sets its independent Go Module identity. A new Project contains root ` + "`" + `plystra.yaml` + "`" + `, module files, and committed generated source, but no environment overlay, example configuration, or ` + "`" + `go.work` + "`" + `.

` + "`" + `--template` + "`" + ` records one direct dependency. Its configuration stays inert unless repeatable ` + "`" + `--adopt-export <name>` + "`" + ` selects an exact root export; source is never copied and template origin grants no priority.

` + "`" + `--format json` + "`" + ` returns one ` + "`" + `plystra.result/v1` + "`" + ` document. Success nests ` + "`" + `plystra.project-created/v1` + "`" + `; enter ` + "`" + `payload.directory` + "`" + ` and run ` + "`" + `plystra check` + "`" + ` independently.

## Completion checks

1. Confirm ` + "`" + `go.mod` + "`" + ` has the intended module identity and direct dependencies.
2. Confirm root ` + "`" + `plystra.yaml` + "`" + ` is the only automatically created configuration document and contains only intended explicit adoptions.
3. Run ` + "`" + `plystra generate --check` + "`" + `, ` + "`" + `plystra check` + "`" + `, and the relevant Go tests.
4. Follow any emitted ` + "`" + `Recovery:` + "`" + ` action before retrying.

See the [Project README](../../../../README.md) and ` + "`" + `plystra new --help` + "`" + ` for the installed command contract.
`

const interfacesAndImplementationsTask = `# Interfaces and Implementations

Use authored Go as the contract and implementation source. A Plystra Interface is a versioned one-method Go interface; an Implementation is an ordinary constructor and concrete type.

## Author and select

    plystra interface create records.read
    plystra implement records.read/v1 --package ./records
    plystra capability create records.read --query --plugin records
    plystra capability implement records.read/v1 --plugin records
    plystra use email.send/v1 example.com/acme/email/smtp.New
    plystra inspect interfaces
    plystra inspect implementations

After scaffolding, edit the authored Interface and Implementation, add package tests, then run ` + "`" + `plystra generate` + "`" + `. Interface IDs are provider-independent and use the exact ` + "`" + `/vN` + "`" + ` suffix. Constructors declare ` + "`" + `//plystra:implements <interface-id>` + "`" + ` and return one compatible value.

Capability creation and implementation never prompt by default. Pass ` + "`" + `--plugin <directory-or-plugin-id>` + "`" + ` as complete non-interactive input. Add ` + "`" + `--interactive` + "`" + ` only to request a terminal choice after enclosing and sole-Plugin inference remain ambiguous; terminal detection alone never prompts, and unavailable requested interaction fails before mutation.

An ordinary ` + "`" + `T` + "`" + ` field has no separate presence state: omission and its Go zero value normalize identically. A direct ` + "`" + `*T` + "`" + ` distinguishes absent from a present value, including zero or empty, while direct ` + "`" + `**T` + "`" + ` adds explicit null. Pointers are allowed only directly on message fields, with a maximum depth of two, and must not create a message cycle. A required ordinary field must occur in representations that retain occurrence; required pointer fields reject absence, while required ` + "`" + `**T` + "`" + ` still permits explicit null. Constraints apply to a present non-null innermost value.

The contract model treats nil and allocated-empty bytes, repeated values, and maps as the same canonical empty value without changing the pointer field's outer state. Generated proxies check pointer requiredness before constraints and copy normalized requests into isolated snapshots. Adapters give every target execution a fresh copy, and proxies validate and copy successful responses into caller-owned storage. Generated proxy packages export CopyRequest and CopyResponse for direct Implementation tests. Traversal fails closed beyond 64 levels or 65,536 nodes. Use errors.As to inspect a generated ValueError for the Interface, side, field path, and rule without exposing values or map keys. Invalid requests never enter the target; invalid responses return an internal contract error with no result. Ordinary required values may be zero. Caller cancellation returns independently with result_unknown after target entry; late results are discarded. Response validation and copying remain inside the tracked attempt. Generated InterfaceRuntime.Drain closes admission and waits for actual termination; Stop drains before any lifecycle cleanup. Drain and cleanup share the construction/startup cleanup timeout and any earlier caller deadline. Failed drain keeps dependencies live for a fresh bounded Stop retry. Readiness, lifecycle-hook dependency access, permits, retries, and separate telemetry remain unfinished. Pointer-bearing Interfaces may remain visible and retain shape and wire history, but this installed CLI does not yet generate the required Protobuf presence wrappers; keep them out of ` + "`" + `http.expose` + "`" + ` until installed capability support changes.

The five compatibility records currently emitted by this CLI are replaceable CLI-owned working records for current authored and generated projections, not accepted release baselines. Classification is per record and ownership entry, not directory-wide. ` + "`" + `interface-metadata.json` + "`" + ` uses schema v2 and records ` + "`" + `contract_supplement_digest` + "`" + ` so a new non-required pointer field is an additive candidate only when shape and supplement evidence agree that no other contract input changed. An owned canonical v1 metadata record migrates to v2 in the same generation transaction. Changing an existing field among ` + "`" + `T` + "`" + `, ` + "`" + `*T` + "`" + `, and ` + "`" + `**T` + "`" + ` is breaking compatibility. Pre-stable development may refresh working records in place; once an accepted stable baseline applies, the change requires a new Interface version. The stable-release assessment continues to report a version requirement until every public projection and immutable accepted ancestor also classifies a newly added pointer field as optional. Do not treat refreshed working records as accepted-release evidence.

Require an internal root by editing the selected document's ` + "`" + `interfaces.require` + "`" + `. Public exposure belongs to the selected current-Project ` + "`" + `http.expose` + "`" + ` mapping. ` + "`" + `interfaces.use` + "`" + ` selects an exact compatible constructor but does not create a root by itself.

When one Implementation needs another Interface, accept the canonical Interface type as a constructor parameter and call its ordinary Go method. Use ` + "`" + `plystra.Optional[T]` + "`" + ` only for an optional Interface dependency. Do not import another concrete Implementation package.

Constructors only assemble values; resource acquisition and background work belong in lifecycle Start. Return a concrete pointer plus error. Assembly rejects nil success, redacts errors and panics, and cleans all returned lifecycle values after failure, including partial results and never-started values. Stop must tolerate those states. Use errors.As to find a generated assembly.InterfaceAssemblyError and RetryCleanup(ctx) to retry failed construction cleanup under its original timeout; this never restarts construction or publishes a failed runtime. Test constructor failures and startup rollback as well as the success path.

Construct semantic errors with invocation.NewSemanticError(code, cause) from github.com/plystra/kernel/invocation. Use errors.As with *invocation.SemanticError and Code() locally; structural SemanticErrorCode methods are not recognized. Wrap uncertain effects with invocation.NewResultUnknown(cause) before semantic translation. Generated error projection follows ordinary wrapping and joins up to 64 unwrap levels and 1,024 nodes, rejects conflicting or undeclared codes, and exposes no private cause. Completion remains independent of the primary code through Connect, transitional HTTP, and PlystraError.completion in the SDK.

Static Interface drain does not cover the transitional legacy Capability dispatcher; bootstrap does not yet drain that dispatcher.

## Completion checks

1. Run the authored package tests.
2. Run ` + "`" + `plystra generate` + "`" + ` and inspect ordinary generated diffs.
3. Run ` + "`" + `plystra inspect interfaces` + "`" + ` and ` + "`" + `plystra inspect implementations` + "`" + ` to confirm roots, selections, dependencies, and assembly membership.
4. Run ` + "`" + `plystra check` + "`" + `.

See the [Project README](../../../../README.md), ` + "`" + `plystra interface create --help` + "`" + `, and ` + "`" + `plystra implement --help` + "`" + `.
`

const configurationAndSecretsTask = `# Configuration and Secrets

Use one selected current-Project configuration mode consistently.

## Select the document

- No selector: root ` + "`" + `plystra.yaml` + "`" + ` only, including its explicit ` + "`" + `composition.adopt` + "`" + ` set.
- ` + "`" + `--env production` + "`" + `: root plus one sparse project-root ` + "`" + `plystra.production.yaml` + "`" + ` overlay.
- ` + "`" + `--config deploy/customer-a.yaml` + "`" + `: one complete replacement document; root remains only the Project marker.

Use the same selector for mutation, generation, inspection, checking, and startup:

    plystra generate --env production
    plystra generate --check --env production
    plystra inspect configuration --env production
    plystra check --env production
    go run ./generated/go/application --env production

Do not combine ` + "`" + `--env` + "`" + ` and ` + "`" + `--config` + "`" + `. ` + "`" + `PLYSTRA_ENV` + "`" + ` and ` + "`" + `PLYSTRA_CONFIG` + "`" + ` supply the same selectors when explicit flags are absent.

Configuration values belong under the exact constructor-owned ` + "`" + `config.<constructor-symbol>` + "`" + ` object. Keep Secret values out of YAML, generated source, diagnostics, SDKs, and tests. A Secret field contains only a valid ` + "`" + `env` + "`" + ` or absolute ` + "`" + `file` + "`" + ` reference, and generation validates the reference without resolving its value.

Active Interface timeout policies currently fail generation and check with PLYSTRA_POLICY_NOT_ENFORCED because Kernel execution is unsupported. Remove the policy from the selected layer or install a compatible CLI/Kernel pair. Inspect capabilities for support stages; read-only Project inspection remains available. Dormant policies are intent, not enforced guarantees.

## Completion checks

1. Inspect the selected layer and ownership with ` + "`" + `plystra inspect configuration` + "`" + `.
2. Regenerate with the same selector.
3. Run ` + "`" + `plystra generate --check` + "`" + ` and ` + "`" + `plystra check` + "`" + ` with that selector.
4. Confirm generated records contain no configuration values, Secret targets, resolved Secrets, or machine paths.

See the [Project README](../../../../README.md) and ` + "`" + `plystra generate --help` + "`" + `.
`

const resourcesAndDataTask = `# Resources and Data

The installed CLI ` + "`" + cliToken + "`" + ` does not expose public Resource declaration, Data schema/query generation, or ` + "`" + `data migration plan|apply|status` + "`" + ` operations.

Do not simulate those operations with handwritten files under ` + "`" + `generated/` + "`" + `, legacy Plugin conventions, or an unversioned migration script. Check ` + "`" + `plystra help` + "`" + ` after upgrading and run ` + "`" + `plystra inspect capabilities --format json` + "`" + ` before adopting any Resource contract, provider, backend, Data compiler, or migration workflow.

Until those commands are present, keep persistence behavior inside ordinary authored Implementation code and its tests without claiming Plystra-managed Data generation or migration support.

See the [Project README](../../../../README.md) for the currently supported application surface.
`

const diagnosticsAndRecoveryTask = `# Diagnostics and recovery

Use read-only inspection before changing authored inputs:

    plystra inspect capabilities --format json
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

` + "`" + `plystra inspect capabilities` + "`" + ` is separate from Project inspection. It ignores the working directory, invalid Project state, ` + "`" + `PLYSTRA_ENV` + "`" + `, and ` + "`" + `PLYSTRA_CONFIG` + "`" + ` while reporting exact installed commands and arguments, selectors, stable defaults, interaction and output modes, effect classes, schemas, bounds, toolchain identity, and independent ` + "`" + `specified` + "`" + `, ` + "`" + `parsed` + "`" + `, ` + "`" + `generated` + "`" + `, ` + "`" + `executed` + "`" + `, and ` + "`" + `accepted` + "`" + ` support stages. Planned commands are absent. Result, recovery, inspection, and graph schemas are available; standalone diagnostic and continuation schema roles remain explicitly unavailable. Its only option is ` + "`" + `--format human|json` + "`" + `.

Inspect versioned Agent guidance before refreshing it:

    plystra guidance check
    plystra guidance sync
    plystra guidance sync --replace-generated

` + "`" + `guidance check` + "`" + ` is always non-mutating. Ordinary sync changes or removes only unchanged prior-manifest-owned files, and any drift blocks the complete transaction. ` + "`" + `--replace-generated` + "`" + ` can replace only an existing bounded regular prior-owned file; missing prior-owned paths and desired paths absent from previous ownership remain blocked whether missing or occupied. Neither sync mode touches optional ` + "`" + `local.md` + "`" + `, another unlisted file, a sibling skill, or repository-wide Agent instructions.

Reuse the same ` + "`" + `--env` + "`" + ` or ` + "`" + `--config` + "`" + ` selector for Project-bound inspection and explanation. ` + "`" + `--format json` + "`" + ` returns ` + "`" + `plystra.result/v1` + "`" + ` for creation, installed capability discovery, and all five explanation commands. Explanation nests ` + "`" + `plystra.explain/v1` + "`" + ` with diagnostics and ` + "`" + `plystra.recovery/v1` + "`" + ` actions; JSON stderr stays empty after initialization. Project inspect retains its current top-level schemas.

Explanation exits 2 for invalid invocation or subject, 3 for invalid Project state or missing targets, 4 for required decisions or missing prerequisites, and 8 for internal failures. Unknown failures use redacted ` + "`" + `PLYSTRA_EXPLAIN_FAILED` + "`" + `. Recovery preserves the selector and names the exact source edit or finite choices. Provider choices edit ` + "`" + `capabilities.use` + "`" + `; installed ` + "`" + `plystra use` + "`" + ` accepts only Interface Implementation constructors. Only executable actions and supported choice options carry a working directory and fully bound ` + "`" + `argv` + "`" + `. Never execute unresolved placeholders or parse human display text as a shell command; run the action's independent verification afterward.

Actionable human failures end with one ` + "`" + `Recovery:` + "`" + ` block and one stable ` + "`" + `Diagnostic: PLYSTRA_<AREA>_<CONDITION>` + "`" + ` code. Source-bearing failures add deterministic module-relative ` + "`" + `Source:` + "`" + ` lines. Use the code as the automation identity, apply the recovery to the reported authored source, and rerun the same selected command.

Agent-guidance drift uses ` + "`" + `PLYSTRA_AGENT_GUIDANCE_DRIFT` + "`" + ` and reports every affected path as an ` + "`" + `agent-guidance` + "`" + ` source. An invalid ownership manifest uses ` + "`" + `PLYSTRA_AGENT_GUIDANCE_MANIFEST_INVALID` + "`" + `. A manifest or transaction path that changes after inspection uses ` + "`" + `PLYSTRA_PROJECT_CONCURRENT_CHANGE` + "`" + ` with every deterministically known affected guidance path.

` + "`" + `PLYSTRA_PROTOBUF_POINTER_PROJECTION_UNSUPPORTED` + "`" + ` reports the declaration-owning ` + "`" + `http.expose` + "`" + ` document at ` + "`" + `1:1` + "`" + ` as an ` + "`" + `exposure` + "`" + ` source when an active Interface needs unavailable pointer-presence wrappers. A sparse environment overlay may inherit that declaration from root ` + "`" + `plystra.yaml` + "`" + `, so remove the exposure from the reported source and rerun generation with the same selector. The failed command leaves authored, generated, module, and compatibility files unchanged and does not expose an absolute path or pointer value.

Never print or persist resolved Secrets, unrestricted configuration values, avoidable absolute paths, or Module Cache paths while diagnosing a Project. Do not edit dependency source in the Module Cache or CLI-owned files under ` + "`" + `generated/` + "`" + `.

See the [Project README](../../../../README.md) and the exact command help for diagnostic-specific recovery.
`

const verifyBuildAndReleaseTask = `# Verify, build, and release

Use the narrowest relevant authored test first, then verify the complete selected Project state:

    plystra generate --check
    plystra check
    go test ./...
    go build ./...
    go vet ./...
    go mod verify

Use ` + "`" + `GOWORK=off` + "`" + ` when proving that the module resolves and builds independently of a local workspace. When a generated JavaScript SDK exists, run its declared install, typecheck, build, runtime-test, declaration, and dry-run package commands from ` + "`" + `generated/sdk/javascript` + "`" + `.

Generated source and working compatibility records are reviewable outputs, but they are never edited manually. Change authored Go, YAML, or module inputs and rerun ` + "`" + `plystra generate` + "`" + `. Accepted release baselines are separate immutable evidence and are not created by ordinary generation.

The installed CLI ` + "`" + cliToken + "`" + ` does not expose the planned public ` + "`" + `plystra dev` + "`" + `, ` + "`" + `plystra build` + "`" + `, release-preparation, or publication operations. Do not present local checks as proof that an unavailable release workflow ran.

See the [Project README](../../../../README.md) for the exact generated application and JavaScript checks supported by this Project.
`
