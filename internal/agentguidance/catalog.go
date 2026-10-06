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
	{Path: Root + "/tasks/resources-and-data.md", Title: "Resources and Data", Purpose: "Configure and bind named Resources; check installed Data support before schemas, queries, or migrations.", ContentTemplate: resourcesAndDataTask},
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

Use this task for Project creation, module identity, the ` + "`" + `--template` + "`" + ` dependency selector, and ordinary Go Module dependencies.

## Supported operations

    plystra new app
    plystra new app --module example.com/acme/app
    plystra new app --template example.com/acme/platform@v1.2.3
    plystra new app --plugin records
    plystra add example.com/acme/email@v1.4.2
    plystra update example.com/acme/email@v1.5.0
    plystra remove example.com/acme/email

` + "`" + `plystra new` + "`" + ` is non-interactive by default. Guidance is generated unless ` + "`" + `--no-agent-guidance` + "`" + ` is set. Git and CI default off and opt in with ` + "`" + `--git` + "`" + ` and ` + "`" + `--github-ci` + "`" + `; prompts require ` + "`" + `--interactive` + "`" + `.

The positional name is one safe child directory; ` + "`" + `--module` + "`" + ` sets its independent Go Module identity. A new Project contains root ` + "`" + `plystra.yaml` + "`" + `, module files, and committed generated source, but no environment overlay, example configuration, or ` + "`" + `go.work` + "`" + `.

` + "`" + `--template` + "`" + ` records the selected Project module as one ordinary direct Go Module dependency. The dependency must expose a regular root ` + "`" + `plystra.yaml` + "`" + ` marker, but its configuration and source are never copied or activated. After creation it is indistinguishable from a dependency added with ` + "`" + `plystra add` + "`" + `.

` + "`" + `--format json` + "`" + ` returns one ` + "`" + `plystra.result/v1` + "`" + ` document. Success nests ` + "`" + `plystra.project-created/v1` + "`" + `; enter ` + "`" + `payload.directory` + "`" + ` and run ` + "`" + `plystra check` + "`" + ` independently.

## Completion checks

1. Confirm ` + "`" + `go.mod` + "`" + ` has the intended module identity and direct dependencies.
2. Confirm root ` + "`" + `plystra.yaml` + "`" + ` is the only automatically created configuration document and contains only current-Project configuration.
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

The contract model treats nil and allocated-empty bytes, repeated values, and maps as the same canonical empty value without changing the pointer field's outer state. Generated proxies check pointer requiredness before constraints and copy normalized requests into isolated snapshots. Adapters give every target execution a fresh copy, and proxies validate and copy successful responses into caller-owned storage. Generated proxy packages export CopyRequest and CopyResponse for direct Implementation tests. Traversal fails closed beyond 64 levels or 65,536 nodes. Use errors.As to inspect a generated ValueError for the Interface, side, field path, and rule without exposing values or map keys. Invalid requests never enter the target; invalid responses return an internal contract error with no result. Ordinary required values may be zero. Caller cancellation returns independently with result_unknown after target entry; late results are discarded. Response validation and copying remain inside the tracked attempt. Generated InterfaceRuntime.Drain closes admission and waits for actual termination; Stop drains before any lifecycle cleanup. Drain and cleanup share the construction/startup cleanup timeout and any earlier caller deadline. Failed drain keeps dependencies live for a fresh bounded Stop retry. Each exact binding admits 64 attempts with no queue; permits remain held through target termination and response copying, even after caller cancellation. Saturation returns resource_exhausted with not_started (Connect/HTTP 429); JavaScript preserves both facts. The frozen model version 19 and Interface provenance v3 record every compiled policy field, including disabled stages and literal compatibility identities. No authored timeout means no added deadline. Authored replay-safe retries use one total timeout budget, fresh request copies, and outermost ownership without overlapping attempts. Authored concurrency, queue, and circuit policies remain unsupported. Generated static calls emit separate Kernel OpenTelemetry caller.duration and target.duration histograms under plystra.invocation; counts distinguish logical calls from entered target attempts, including late termination. Configure the global MeterProvider before bootstrap and shut it down only after successful drain. Labels contain only resolved binding and safe bounded outcome facts, never request values or dynamic identities; no SDK means no-op metrics. Governed spans and broader telemetry remain unfinished. Pointer-bearing Interfaces may remain visible and retain shape and wire history, but this installed CLI does not yet generate the required Protobuf presence wrappers; keep them out of ` + "`" + `http.expose` + "`" + ` until installed capability support changes.

The five compatibility records currently emitted by this CLI are replaceable CLI-owned working records for current authored and generated projections, not accepted release baselines. Classification is per record and ownership entry, not directory-wide. ` + "`" + `interface-metadata.json` + "`" + ` uses schema v2 and records ` + "`" + `contract_supplement_digest` + "`" + ` so a new non-required pointer field is an additive candidate only when shape and supplement evidence agree that no other contract input changed. An owned canonical v1 metadata record migrates to v2 in the same generation transaction. Changing an existing field among ` + "`" + `T` + "`" + `, ` + "`" + `*T` + "`" + `, and ` + "`" + `**T` + "`" + ` is breaking compatibility. Pre-stable development may refresh working records in place; once an accepted stable baseline applies, the change requires a new Interface version. The stable-release assessment continues to report a version requirement until every public projection and immutable accepted ancestor also classifies a newly added pointer field as optional. Do not treat refreshed working records as accepted-release evidence.

Effective ` + "`" + `interfaces.require` + "`" + ` and ` + "`" + `http.expose` + "`" + ` entries come from the selected current-Project configuration. Add an internal root or public exposure by editing that document's respective set or mapping. ` + "`" + `interfaces.use` + "`" + ` selects an exact compatible constructor but does not create a root by itself.

When one Implementation needs another Interface, accept the canonical Interface type as a constructor parameter and call its ordinary Go method. Use ` + "`" + `plystra.Optional[T]` + "`" + ` only for an optional Interface dependency. Do not import another concrete Implementation package.

Every required or optional dependency needs an explicit nonblank Go identifier other than _. Only the optional first Config parameter is exempt. Names retain exact case and authored position through discovery, inspection, static assembly, and provenance. Inspection dependency edges carry exact parameter_name and one-based parameter_position; other edges omit them. Repeated dependencies on one Interface remain distinct, with the same name and position in human output. Renaming or reordering a reachable dependency changes application-model identity and requires regeneration without changing its canonical Interface contract. Unnamed and blank parameters fail even on unselected visible candidates, with the owning constructor source and PLYSTRA_IMPLEMENTATION_REQUIRED_INVALID or PLYSTRA_IMPLEMENTATION_OPTIONAL_INVALID before mutation. Name the reported parameter and rerun the command.

Constructors only assemble values; resource acquisition and background work belong in lifecycle Start. Return a concrete pointer plus error. Assembly rejects nil success, redacts errors and panics, and cleans all returned lifecycle values after failure, including partial results and never-started values. Stop must tolerate those states. Use errors.As with interface { RetryCleanup(context.Context) error } to find the outermost construction-cleanup owner and retry pending cleanup under its original timeout; this never restarts construction or publishes a failed runtime. Test constructor failures and startup rollback as well as the success path.

Own lifecycle changes through the generated application's Start and Stop. Overlapping or reentrant application transitions return lifecycle.ErrState without changing either manager or admission, including during rollback and drain. Application copies share this guard; independent applications do not. Retry after the active operation returns. Direct lifecycle calls on the lower-level InterfaceRuntime are outside this application guard.

Construct semantic errors with invocation.NewSemanticError(code, cause) from github.com/plystra/kernel/invocation. Use errors.As with *invocation.SemanticError and Code() locally; structural SemanticErrorCode methods are not recognized. Wrap uncertain effects with invocation.NewResultUnknown(cause) before semantic translation. Generated error projection follows ordinary wrapping and joins up to 64 unwrap levels and 1,024 nodes, rejects conflicting or undeclared codes, and exposes no private cause. Completion remains independent of the primary code through Connect, transitional HTTP, and PlystraError.completion in the SDK.

Catalog publication does not accept public work. Application Start opens both dispatchers only after complete dependency-ordered readiness. Lifecycle hooks pass their bounded context to already-ready injected dependencies, including during Stop after public admission closes; never retain that context for later work. Application Stop initiates both static and legacy dispatcher drains before waiting and retains both lifecycle sets if either drain fails. Standalone InterfaceRuntime owners call Start and then OpenAdmission; Application owners use Start alone.

## Completion checks

1. Run the authored package tests.
2. Run ` + "`" + `plystra generate` + "`" + ` and inspect ordinary generated diffs.
3. Run ` + "`" + `plystra inspect interfaces` + "`" + ` and ` + "`" + `plystra inspect implementations` + "`" + ` to confirm roots, selections, dependencies, and assembly membership.
4. Run ` + "`" + `plystra check` + "`" + `.

See the [Project README](../../../../README.md), ` + "`" + `plystra interface create --help` + "`" + `, and ` + "`" + `plystra implement --help` + "`" + `.
`

const configurationAndSecretsTask = `# Configuration and Secrets

Use one selected current-Project configuration mode consistently.

Root ` + "`" + `plystra.yaml` + "`" + ` is the Project marker. A selected environment overlay composes over that root, while a complete replacement document is used by itself and must carry its own Project marker. Dependencies contribute discoverable declarations only; their configuration is inactive.

Remove exact interfaces.use, interfaces.policies, and http.expose entries only with {$remove: true} in an environment overlay. Null, empty values, false or string-valued markers, and sibling fields are invalid. Keep the authored tombstone even if no lower root value exists. Root and complete replacement documents reject tombstones. Regenerate and rebuild for runtime compatibility version 12. Remove http.cors or either of its fields with {$remove: true} in an overlay; null is invalid. Removing credentials restores false; an effective CORS object still requires origins.

Remove a whole config.<constructor-symbol> entry only with {$remove: true}. Null, sequences, malformed markers, and sibling fields are invalid at that boundary; {} remains configuration, not removal. The constructor needs a discovered compiled Config schema. Keep exclusions even without lower configuration; inspection retains removal ownership and suppressed sources. Generated runtime loading strips whole-entry markers before construction, but removing required configuration still fails before any constructor runs.

During typed CLI composition, remove a declared field inside a non-pointer fixed struct with {$remove: true}. Exclusions persist without lower values; an omitted overlay field keeps the root value. Literal null, ~, and blank values are atomic nil only for pointers, slices, and maps, distinct from removal and empty collections. Other compiled types reject null. Reserved singleton $remove mappings are invalid inside atomic values, including pointed-to struct members, list elements, and dynamic-map entries. A dynamic map with $remove and other keys remains an ordinary typed value.

CLI and runtime typed normalization reject malformed scalar payloads before a nullable pointer, slice, or map can discard them. This includes selected root, environment, and replacement values and lower values hidden by later removals. Genuine null remains valid for nullable Go types.

Required fields are checked after the selected root and, when present, environment overlay compose. Effective dormant objects and all active configurable constructors must be complete; dormant absent or removed objects need no values yet. Requiredness means presence: zero, empty, and schema-permitted nil values count. Omitted fixed structs and fixed-array elements still need nested required fields; absent or nil pointers and empty slices/maps have no child values to validate. PLYSTRA_CONSTRUCTOR_CONFIGURATION_VALUES_INVALID identifies a safe declared path and the selected current document before mutation. Supply the missing field there or correct its overlay tombstone. Generated startup applies these rules and compiled scalar defaults after the selected layers compose. Startup validates effective dormant objects without activating constructors or resolving their Secrets.

During typed CLI composition, a supplied non-null pointer field replaces its complete lower value, including pointers to structs and multiple pointer layers. Omission keeps the lower root value in an environment overlay; {} replaces a pointed-to struct without keeping omitted fields. Only non-pointer fixed structs compose field by field. Inspection retains one redacted atomic value and its sources. Generated runtime loading applies the same pointer replacement rules to selected current-Project layers.

An interfaces.require sequence replaces the complete lower explicit requirement set; [] clears it. In an environment overlay, omission and {} keep the root set, while {add: [...], remove: [...]} changes only named members. Root and complete replacement documents reject sparse requirement forms. Exposure, constructor dependencies, and intrinsic Kernel requirements remain independent. Inspection and explanation retain suppressed sources. Generated runtime compatibility version 12 compares effective current-Project requirements, reachable selections, and policies after overlay replacements and removals.

The public runtime contract binds the selected current-Project model and its exact generated identity. Private values and their hashes never enter public output.

Validated Secret fields exclude reference kind and target from public identity. Changing an environment or file reference leaves generated output and inspection unchanged, including nested and dormant configuration. Later overlay values or tombstones replace lower references. Public provenance never exposes private reference equality. Generation compares private root, selected-document, and dependency snapshots to detect concurrent edits and preserve their source.

Validated runtime-only constructor values exclude contents from public identity. A build-visible tag includes the whole value and descendants; otherwise only tagged descendants contribute. Entirely private atomic pointers, lists, arrays, and maps hide keys, contents, cardinality, and nil/empty differences. Mixed containers retain positions, keys, order, and nullness needed to locate public descendants, excluding private siblings. Declared field presence, sources, and tombstones remain provenance. Layer order determines ownership without publishing private equality. Build-visible edits cause public generation drift; startup checks effective build-visible values after selected current-project layers and defaults compose.

Current-Project http.address and timeouts.startup values are runtime-only and absent from public hashes. Valid value edits preserve generated output, inspection, and explanation; presence, type, source, and removal intent remain provenance. Private snapshots detect concurrent edits. Startup loads the selected timeout without regeneration. CORS, exposure, and invocation policies remain build-affecting and still require regeneration when changed.

Public configuration type descriptions omit raw anonymous-struct tags, including containers and generic arguments. Nested defaults stay out of logs, diagnostics, and public value hashes. Compiled field names, policy, default presence, and private default access remain intact. A public type description is not a complete schema or Go assignment identity. Authored Go default changes still require a rebuild; current-project startup applies defaults from the compiled Go type without copying private literals into generated source.

## Select the document

- No selector: root ` + "`" + `plystra.yaml` + "`" + ` only.
- ` + "`" + `--env production` + "`" + `: root plus one sparse project-root ` + "`" + `plystra.production.yaml` + "`" + ` overlay.
- ` + "`" + `--config deploy/customer-a.yaml` + "`" + `: one complete replacement document; root remains the Project marker.

Use the same selector for mutation, generation, inspection, checking, and startup:

    plystra generate --env production
    plystra generate --check --env production
    plystra inspect configuration --env production
    plystra check --env production
    go run ./generated/go/application --configuration-root . --runtime-baseline dist/runtime-baseline.json --env production

Do not combine ` + "`" + `--env` + "`" + ` and ` + "`" + `--config` + "`" + `. ` + "`" + `PLYSTRA_ENV` + "`" + ` and ` + "`" + `PLYSTRA_CONFIG` + "`" + ` supply the same selectors when explicit flags are absent.

The generated binary requires --configuration-root <directory> and --runtime-baseline <path>. Generation and scaffolding create ignored dist/runtime-baseline.json with native owner-only permissions. Deploy it with the matching binary and preserve those permissions. Relative baseline and replacement paths resolve from the configuration root; an absolute baseline path may point to a separately deployed private file. Startup checks the public runtime contract, dependency module/version membership, and compiled constructor defaults before Secrets or constructors. Missing, malformed, publicly readable, or mismatched baselines fail with redacted recovery. Refresh the baseline with plystra generate after private-default edits and rebuild authored Go. Compatible private value edits require a refreshed baseline, without rebuilding the existing binary. Baseline bytes and private hashes never enter public generated artifacts. generate --check checks public output without creating private output. Root, overlay, and replacement documents use bounded confined reads and reject symbolic components. These runtime flags do not apply to the Plystra CLI. Named Resource instances receive typed configuration and Secrets through the same private baseline; complete gate acceptance remains unfinished.

Generated bootstrap delivers selected current-Project constructor configuration through exact typed Config fields. It validates the complete scalar, struct, pointer, slice, array, map, and Secret value model, composes fixed structs by field, replaces atomic values, and preserves nil, empty, and removal distinctions. Requiredness and scalar defaults apply after composition. All active objects and effective build-visible values are checked before any Secret resolution or constructor entry. No custom YAML unmarshalling runs. Private defaults come from compiled Go tags and never enter generated source. Startup rejects invalid recompiled tag syntax, duplicate policy options, metadata on ignored or unexported fields, and invalid defaults even when runtime values override them. Correct the Go declaration, regenerate, and rebuild. Dormant objects create no runtime binding. Every selector validates the selected document against the private baseline.

The private baseline carries every visible constructor's validated Interface membership and Config schema. Startup validates effective dormant objects without activating constructors or resolving their Secrets. Requiredness applies only when a dormant object exists. Dormant-only choice and value changes preserve executable output. Schema-inventory changes require a regenerated baseline and rebuilt binary; regenerate older pairs to include this validation inventory.

Configuration values belong under the exact constructor-owned ` + "`" + `config.<constructor-symbol>` + "`" + ` object. Keep Secret values out of YAML, generated source, diagnostics, SDKs, and tests. A Secret field contains only a valid ` + "`" + `env` + "`" + ` or absolute ` + "`" + `file` + "`" + ` reference, and generation validates the reference without resolving its value.

Authored static Interface timeout and replay-safe retry policies execute through the selected binding. Retry requires timeout and the explicit eligibility: replay_safe assertion about the binding and its downstream effects; safety is never inferred. max_attempts counts the first attempt, defaults to 2, and permits 2 through 16; backoff defaults to 0s and accepts nonnegative Go durations. One total budget starts before request validation and copying, is capped by an earlier caller deadline, and includes all attempts, backoff, and response processing. Each attempt receives a fresh copy of the original request snapshot and starts only after the previous target terminates. The outermost retry-enabled binding owns replay; nested bindings suppress their own retries. Only not_started resource exhaustion and result_known unavailable or resource exhaustion can retry. Semantic errors, cancellation, deadlines, internal or validation failures, and result_unknown never replay; exhaustion retains the final safe category, completion, and bounded attempt count. Without retry there is one attempt; without timeout there is no added deadline. Complete compiled policy values and literal schema/compiler/defaults versions are frozen before runtime; mismatches fail closed. Dormant policies remain intent outside executable identity until activation. Inspect capabilities reports support stages, exact defaults, and duration bounds. Authored concurrency, queue, and circuit forms remain unsupported. Transitional legacy Capability wrappers do not include preparation and completion in the Kernel budget; active authored policies on that path still fail with PLYSTRA_POLICY_NOT_ENFORCED. Capability discovery reports that exception as legacy.capability-timeout with executed=no and accepted=no.

## Completion checks

1. Inspect the selected layer and ownership with ` + "`" + `plystra inspect configuration` + "`" + `.
2. Regenerate with the same selector.
3. Run ` + "`" + `plystra generate --check` + "`" + ` and ` + "`" + `plystra check` + "`" + ` with that selector.
4. Confirm generated records contain no configuration values, Secret targets, resolved Secrets, or machine paths.

See the [Project README](../../../../README.md) and ` + "`" + `plystra generate --help` + "`" + `.
`

const resourcesAndDataTask = `# Resources and Data

The installed CLI ` + "`" + cliToken + "`" + ` discovers consumer Resource contracts in current and dependency Projects. Use ` + "`" + `plystra inspect resources --format json` + "`" + ` to inspect exact IDs, packages, owning sources, and contract_digest values without mutation. Declare one non-generic defined Go interface Resource with exactly one //plystra:resource <resource-id> directive. Do not attach Interface directives or interface.yaml projection metadata, and keep lifecycle control on providers. Duplicate IDs and malformed declarations or bounded public type shapes fail with source-bearing PLYSTRA_RESOURCE_ID_DUPLICATE, PLYSTRA_RESOURCE_DECLARATION_INVALID, or PLYSTRA_RESOURCE_CONTRACT_INVALID diagnostics.

Contract digests retain transitive public fields and methods, normalized aliases, instantiated generic arguments, and recursive references, with limits of 64 type-reference levels and 65,536 public shape nodes. They exclude comments, source locations, private implementation details, and unrelated provider lifecycle hooks. Installed support reports resource.contract separately from named-instance configuration and execution. Data schema/query generation and ` + "`" + `data migration plan|apply|status` + "`" + ` remain unsupported.

Resource provider discovery validates exactly one //plystra:implements-resource <resource-id> directive on an exported non-generic package-level constructor. Use an optional first same-package Config value, then explicitly named canonical Resource parameters, and a concrete pointer plus error result assignable to the exact visible Resource. Do not mix ordinary Interface directives on that constructor. Invalid declarations and typed signatures report PLYSTRA_RESOURCE_PROVIDER_DECLARATION_INVALID or PLYSTRA_RESOURCE_PROVIDER_INVALID with owning sources. Installed resource.provider.discovery reports validation separately. Discovery alone does not select or execute providers; resources.instances supplies the named selection.

Ordinary Implementation constructors may also declare explicitly named canonical Resource parameters alongside required and optional Interface dependencies. Use plystra inspect implementations --format json to inspect resource-contract nodes with resource_id and contract_digest, and declares-dependency edges with reason resource, exact parameter_name and one-based parameter_position. Repeated parameters for the same Resource remain distinct. These declarations are not Interface requirements or runtime activation. Selected instances and resolved bindings appear separately in inspect resources. Installed resource.consumer.discovery covers discovery and inspection of dormant explicit and unselected candidates. Malformed Resource-shaped parameters fail with source-bearing PLYSTRA_IMPLEMENTATION_REQUIRED_RESOURCE_INVALID. Reachable consumers require named Resource bindings; dormant choices remain inactive.

Named instances are authored under resources.instances.<name> with one exact use provider constructor and typed instance-owned config, not top-level config. Names are exact, 1 through 128 ASCII bytes, matching [a-z][a-z0-9]*(?:-[a-z0-9]+)*(?:\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*)*. A config-only environment overlay may keep the lower provider. With the same provider, non-pointer fixed Config structs compose by field and pointers and collections replace atomically. Changing use replaces the whole instance and discards old configuration even when field names match. Requiredness and defaults apply after composition. Dependency Projects remain inactive configuration sources.

Bind exact Go parameters with resources.bind.implementations.<constructor>.<parameter> or resources.bind.instances.<consumer-instance>.<parameter>. Each leaf names one exact instance; the old flat binding form is invalid. Explicit targets must exist and conform to the required Resource. Otherwise exactly one compatible selected instance binds implicitly; zero is missing and multiple matches are ambiguous. Provider identity and declaration order never break ties. All explicit addresses are validated, including dormant Implementation consumers. Remove an instance or binding leaf only with {$remove: true}; null and empty mappings are not removal. Removal never silently retargets an explicit binding; dangling addresses and dependency cycles fail before generation.

Malformed Resource configuration reports PLYSTRA_RESOURCE_METADATA_INVALID, PLYSTRA_RESOURCE_CONFIGURATION_SCHEMA_INVALID, or PLYSTRA_RESOURCE_CONFIGURATION_VALUES_INVALID without private values. Invalid instance selections report PLYSTRA_RESOURCE_INSTANCE_INVALID. Missing, ambiguous, or invalid dependency targets report PLYSTRA_RESOURCE_BINDING_MISSING, PLYSTRA_RESOURCE_BINDING_AMBIGUOUS, or PLYSTRA_RESOURCE_BINDING_INVALID with owning sources. Correct the selected instance, exact consumer/parameter address, or required typed field, then rerun the same selector; do not remove a still-needed dependency to bypass validation.

Every selected instance is active even when unconsumed. The same provider under different names produces distinct values, Config owners, and lifecycle members. Each named instance is constructed once and shared across its consumers. Providers depend only on Resources; Implementations may mix Resource and Interface dependencies. Resources do not create Interface roots, catalog entries, governed proxies, exposure, or transport output. Dependencies construct and start first; consumers stop first. Constructors only assemble values, with acquisition in Start. Failure cleanup includes non-nil partial constructor results and never-started lifecycle values; Stop must tolerate those states. Nil/nil and panics fail safely, and bounded cleanup failures remain retryable.

Generated typed configuration and Secrets are loaded per instance from the selected documents and source-independent private runtime baseline. Validate every selected object before resolving Secrets or entering constructors. Runtime compatibility version 12 and frozen model version 19 cover named Resource structure. Instance identity, providers, bindings, and build-visible edits require regeneration and rebuilding. Valid runtime-only values and Secret references do not enter public generated identity; public artifacts exclude private values, reference targets, and private hashes.

Use plystra use <target> <constructor-symbol> for a canonical Interface ID including /vN or an existing named Resource instance, with no kind flag. It never creates an instance. An exact provider change discards only that instance's old Config; the same provider and other instances retain their configuration. Selection removes configuration whose last explicit or reachable owner disappears and only provably obsolete consumer binding parameters. Cleanup writes selected-layer tombstones or sparse deltas and never edits a dependency or copies a baseline. Still-owned configuration remains intact. Surviving dependencies and new required values must be valid; the CLI never guesses a binding or retargets unrelated consumers. The command regenerates and validates the repaired final state in its rollback boundary, even when the starting graph is invalid.

` + "`" + `plystra implement <contract> --package ./<package>` + "`" + ` creates an unfinished, inactive ordinary Resource provider scaffold. It does not create a named instance, select a provider, infer configuration, or provide runtime behavior. Author ` + "`" + `New` + "`" + `, provider methods, configuration, dependencies, and lifecycle hooks before generation; no guessed Config, dependency, or hook is added. Instance creation remains unsupported. After manual Go or YAML edits, run ` + "`" + `plystra generate` + "`" + `, ` + "`" + `plystra generate --check` + "`" + `, and ` + "`" + `plystra check` + "`" + ` with the same selector. Never hand-edit generated assembly. Installed ` + "`" + `resource.provider.scaffold` + "`" + ` reports scaffold stages separately from ` + "`" + `resource.provider.selection` + "`" + ` and full Resource acceptance; implemented stages do not establish ` + "`" + `accepted=yes` + "`" + `. Compound change plans, plan digests, and ` + "`" + `--dry-run` + "`" + ` are not installed. Data schema/query generation and data migration plan|apply|status remain unsupported; Gates 5 and 13 remain incomplete.

Do not simulate those operations with handwritten files under ` + "`" + `generated/` + "`" + `, legacy Plugin conventions, or an unversioned migration script. Check ` + "`" + `plystra help` + "`" + ` after upgrading and run ` + "`" + `plystra inspect capabilities --format json` + "`" + ` before adopting any Resource contract, provider, backend, Data compiler, or migration workflow.

Until those commands are present, keep persistence behavior inside ordinary authored Implementation code and its tests without claiming Plystra-managed Data generation or migration support.

See the [Project README](../../../../README.md) for the currently supported application surface.
`

const diagnosticsAndRecoveryTask = `# Diagnostics and recovery

Unified plystra use input failures report PLYSTRA_USE_TARGET_INVALID for malformed Interface or instance targets and PLYSTRA_USE_CONSTRUCTOR_INVALID for malformed constructor symbols before Project discovery. PLYSTRA_USE_TARGET_NOT_FOUND means the selected model has no such Interface or named instance; inspect interfaces or resources with the same selector. PLYSTRA_USE_PROVIDER_INCOMPATIBLE means the Resource provider choice is incompatible. Recovery retains the selector, uses safe placeholders, and exposes no private configuration or Secret-reference targets. Resource selection never creates an instance or guesses a surviving dependency binding.

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

` + "`" + `plystra inspect capabilities` + "`" + ` is separate from Project inspection. It ignores the working directory, invalid Project state, ` + "`" + `PLYSTRA_ENV` + "`" + `, and ` + "`" + `PLYSTRA_CONFIG` + "`" + ` while reporting exact installed commands and arguments, selectors, stable defaults, interaction and output modes, effect classes, schemas, bounds, toolchain identity, and independent ` + "`" + `specified` + "`" + `, ` + "`" + `parsed` + "`" + `, ` + "`" + `generated` + "`" + `, ` + "`" + `executed` + "`" + `, and ` + "`" + `accepted` + "`" + ` support stages. Planned commands are absent. Result, recovery, inspection, and graph schemas are available; standalone diagnostic and continuation schema roles remain explicitly unavailable. Its only option is ` + "`" + `--format human|json` + "`" + `.

Inspect versioned Agent guidance before refreshing it:

    plystra guidance check
    plystra guidance sync
    plystra guidance sync --replace-generated

` + "`" + `guidance check` + "`" + ` is always non-mutating. Ordinary sync changes or removes only unchanged prior-manifest-owned files, and any drift blocks the complete transaction. ` + "`" + `--replace-generated` + "`" + ` can replace only an existing bounded regular prior-owned file; missing prior-owned paths and desired paths absent from previous ownership remain blocked whether missing or occupied. Neither sync mode touches optional ` + "`" + `local.md` + "`" + `, another unlisted file, a sibling skill, or repository-wide Agent instructions.

Reuse the same ` + "`" + `--env` + "`" + ` or ` + "`" + `--config` + "`" + ` selector for Project-bound inspection and explanation. ` + "`" + `--format json` + "`" + ` returns ` + "`" + `plystra.result/v1` + "`" + ` for creation, installed capability discovery, and all five explanation commands. Explanation nests ` + "`" + `plystra.explain/v1` + "`" + ` with diagnostics and ` + "`" + `plystra.recovery/v1` + "`" + ` actions; JSON stderr stays empty after initialization. Project inspect retains its current top-level schemas.

Explanation exits 2 for invalid invocation or subject, 3 for invalid Project state or missing targets, 4 for required decisions or missing prerequisites, and 8 for internal failures. Unknown failures use redacted ` + "`" + `PLYSTRA_EXPLAIN_FAILED` + "`" + `. Recovery preserves the selector and names the exact source edit or finite choices. Provider choices edit ` + "`" + `capabilities.use` + "`" + `; installed ` + "`" + `plystra use` + "`" + ` accepts Interface Implementations and named Resource providers, not legacy Capability Plugin IDs. Only executable actions and supported choice options carry a working directory and fully bound ` + "`" + `argv` + "`" + `. Never execute unresolved placeholders or parse human display text as a shell command; run the action's independent verification afterward.

Actionable human failures end with one ` + "`" + `Recovery:` + "`" + ` block and one stable ` + "`" + `Diagnostic: PLYSTRA_<AREA>_<CONDITION>` + "`" + ` code. Source-bearing failures add deterministic module-relative ` + "`" + `Source:` + "`" + ` lines. Use the code as the automation identity, apply the recovery to the reported authored source, and rerun the same selected command.

Agent-guidance drift uses ` + "`" + `PLYSTRA_AGENT_GUIDANCE_DRIFT` + "`" + ` and reports every affected path as an ` + "`" + `agent-guidance` + "`" + ` source. An invalid ownership manifest uses ` + "`" + `PLYSTRA_AGENT_GUIDANCE_MANIFEST_INVALID` + "`" + `. A manifest or transaction path that changes after inspection uses ` + "`" + `PLYSTRA_PROJECT_CONCURRENT_CHANGE` + "`" + ` with every deterministically known affected guidance path.

` + "`" + `PLYSTRA_PROTOBUF_POINTER_PROJECTION_UNSUPPORTED` + "`" + ` reports the declaration-owning ` + "`" + `http.expose` + "`" + ` document at ` + "`" + `1:1` + "`" + ` as an ` + "`" + `exposure` + "`" + ` source when an active Interface needs unavailable pointer-presence wrappers. A sparse environment overlay may keep that declaration from root ` + "`" + `plystra.yaml` + "`" + `, so remove the exposure from the reported source and rerun generation with the same selector. The failed command leaves authored, generated, module, and compatibility files unchanged and does not expose an absolute path or pointer value.

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
