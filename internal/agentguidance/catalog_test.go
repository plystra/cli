package agentguidance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/version"
)

func TestRenderProducesDeterministicVersionedProjection(t *testing.T) {
	t.Parallel()

	const modulePath = "example.com/acme/application"
	first, err := Render(modulePath)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	second, err := Render(modulePath)
	if err != nil {
		t.Fatalf("Render again: %v", err)
	}
	if !reflect.DeepEqual(first.Files(), second.Files()) || !reflect.DeepEqual(first.Manifest(), second.Manifest()) {
		t.Fatal("repeated guidance rendering differed")
	}

	wantPaths := []string{
		Root + "/SKILL.md",
		Root + "/tasks/project-and-dependencies.md",
		Root + "/tasks/interfaces-and-implementations.md",
		Root + "/tasks/configuration-and-secrets.md",
		Root + "/tasks/resources-and-data.md",
		Root + "/tasks/diagnostics-and-recovery.md",
		Root + "/tasks/verify-build-and-release.md",
		ManifestPath,
	}
	files := first.Files()
	gotPaths := make([]string, len(files))
	byPath := make(map[string][]byte, len(files))
	for index, file := range files {
		gotPaths[index] = file.Path()
		byPath[file.Path()] = file.Data()
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("projection paths = %v, want %v", gotPaths, wantPaths)
	}
	for path, data := range byPath {
		if bytes.Contains(data, []byte("--adopt-export")) {
			t.Fatalf("guidance %s advertises removed creation option", path)
		}
	}
	project := byPath[Root+"/tasks/project-and-dependencies.md"]
	for _, phrase := range []string{
		"records a direct dependency and its exact module path in root plystra.yaml",
		"Creation immediately inherits the supported Interface and named Resource baseline without copying source or configuration",
		"Named Resource instances and bindings are inherited; Data inheritance remains unsupported",
	} {
		if !bytes.Contains(project, []byte(phrase)) {
			t.Fatalf("Project guidance omits %q", phrase)
		}
	}

	skill := byPath[Root+"/SKILL.md"]
	if len(skill) == 0 || len(skill) > 4096 {
		t.Fatalf("SKILL.md size = %d, want 1..4096", len(skill))
	}
	for _, phrase := range []string{
		"name: plystra",
		"Project module: `" + modulePath + "`",
		"CLI `" + version.Current + "`",
		"Kernel `" + version.KernelVersion + "`",
		"specification revision `" + version.SpecificationRevision + "`",
		"tasks/project-and-dependencies.md",
		"tasks/resources-and-data.md",
		"optional `local.md`",
		"`plystra guidance check` compares this projection",
		"`plystra guidance sync --replace-generated`",
		"Missing prior-owned paths",
	} {
		if !bytes.Contains(skill, []byte(phrase)) {
			t.Fatalf("SKILL.md omits %q:\n%s", phrase, skill)
		}
	}
	interfaces := byPath[Root+"/tasks/interfaces-and-implementations.md"]
	for _, phrase := range []string{
		"entries compose from template roots and the selected current-Project delta",
		"Inherited requirements and exposure create roots immediately without repeated current-Project declarations",
		"plystra capability create records.read --query --plugin records",
		"Capability creation and implementation never prompt by default",
		"Add `--interactive` only to request a terminal choice",
		"terminal detection alone never prompts",
		"An ordinary `T` field has no separate presence state",
		"A direct `*T` distinguishes absent from a present value",
		"direct `**T` adds explicit null",
		"maximum depth of two",
		"must not create a message cycle",
		"required `**T` still permits explicit null",
		"Generated proxies check pointer requiredness before constraints",
		"Adapters give every target execution a fresh copy",
		"64 levels or 65,536 nodes",
		"Caller cancellation returns independently with result_unknown after target entry",
		"Response validation and copying remain inside the tracked attempt",
		"Each exact binding admits 64 attempts with no queue",
		"Saturation returns resource_exhausted with not_started",
		"Failed drain keeps dependencies live for a fresh bounded Stop retry",
		"Overlapping or reentrant application transitions return lifecycle.ErrState",
		"Application copies share this guard; independent applications do not",
		"Direct lifecycle calls on the lower-level InterfaceRuntime are outside this application guard",
		"Application Stop initiates both static and legacy dispatcher drains",
		"Catalog publication does not accept public work",
		"Standalone InterfaceRuntime owners call Start and then OpenAdmission",
		"keep them out of `http.expose`",
		"five compatibility records currently emitted by this CLI",
		"Classification is per record and ownership entry, not directory-wide",
		"not accepted release baselines",
		"`contract_supplement_digest`",
		"owned canonical v1 metadata record migrates to v2",
		"new non-required pointer field is an additive candidate",
		"Pre-stable development may refresh working records in place",
		"once an accepted stable baseline applies",
		"stable-release assessment continues to report a version requirement",
		"every public projection and immutable accepted ancestor also classifies a newly added pointer field as optional",
		"Changing an existing field among `T`, `*T`, and `**T` is breaking compatibility",
	} {
		if !bytes.Contains(interfaces, []byte(phrase)) {
			t.Fatalf("Interface guidance omits %q:\n%s", phrase, interfaces)
		}
	}
	configuration := byPath[Root+"/tasks/configuration-and-secrets.md"]
	for _, phrase := range []string{
		"Remove exact interfaces.use, interfaces.policies, and http.expose entries only with {$remove: true}",
		"runtime compatibility version 12",
		"Remove http.cors or either of its fields with {$remove: true}; null is invalid",
		"Selected template roots contribute CORS, while ordinary dependencies remain inactive",
		"Remove a whole config.<constructor-symbol> entry only with {$remove: true}",
		"{} remains configuration, not removal",
		"remove a declared field inside a non-pointer fixed struct with {$remove: true}",
		"Literal null, ~, and blank values are atomic nil only for pointers, slices, and maps",
		"CLI and runtime typed normalization reject malformed scalar payloads",
		"Reserved singleton $remove mappings are invalid inside atomic values",
		"Required fields are checked after template roots and selected layers compose",
		"Requiredness means presence: zero, empty, and schema-permitted nil values count",
		"Generated startup applies these rules and compiled scalar defaults to active constructors",
		"Startup validates effective dormant objects without activating constructors or resolving their Secrets",
		"a supplied non-null pointer field replaces its complete lower value",
		"Only non-pointer fixed structs compose field by field",
		"Generated runtime loading applies the same pointer replacement rules",
		"Template roots need no source tree or Module Cache at startup",
		"every selector validates the live root relationship against the private baseline",
		"dependency module/version membership",
		"Compatible private inherited-value edits require a refreshed baseline",
		"Root template ancestry composes oldest to nearest",
		"overlays and replacements cannot change it",
		"Replacement excludes root application values but retains the root relationship",
		"Named Resource instances and bindings are inherited; Data inheritance remains unsupported",
		"The public runtime contract binds ordered template module/version identities",
		"An interfaces.require sequence replaces the complete lower explicit requirement set; [] clears it",
		"Omission and {} inherit",
		"A later sparse overlay preserves an earlier complete-set boundary",
		"New Projects use require: {} to preserve inherited template requirements",
		"Validated Secret fields exclude reference kind and target from public identity",
		"Later template and current values or tombstones replace lower references",
		"Public provenance never exposes private reference equality",
		"Generation compares private root, selected-document, and dependency snapshots",
		"Validated runtime-only constructor values exclude contents from public identity",
		"A build-visible tag includes the whole value and descendants",
		"nil/empty differences",
		"Build-visible edits cause public generation drift",
		"Current-Project http.address and timeouts.startup values are runtime-only and absent from public hashes",
		"Startup loads the selected timeout without regeneration",
		"Public configuration type descriptions omit raw anonymous-struct tags",
		"Authored Go default changes still require a rebuild",
		"Startup validates effective dormant objects without activating constructors or resolving their Secrets",
	} {
		if !bytes.Contains(configuration, []byte(phrase)) {
			t.Fatalf("configuration guidance omits %q:\n%s", phrase, configuration)
		}
	}
	diagnostics := byPath[Root+"/tasks/diagnostics-and-recovery.md"]
	for _, phrase := range []string{
		"plystra inspect capabilities --format json",
		"all five explanation commands",
		"plystra.explain/v1",
		"plystra.recovery/v1",
		"JSON stderr stays empty after initialization",
		"PLYSTRA_EXPLAIN_FAILED",
		"fully bound `argv`",
		"is separate from Project inspection",
		"exact installed commands and arguments",
		"effect classes",
		"Planned commands are absent",
		"Result, recovery, inspection, and graph schemas are available",
		"standalone diagnostic and continuation schema roles remain explicitly unavailable",
		"independent `specified`, `parsed`, `generated`, `executed`, and `accepted` support stages",
		"plystra guidance check",
		"Ordinary sync changes or removes only unchanged prior-manifest-owned files",
		"PLYSTRA_AGENT_GUIDANCE_DRIFT",
		"PLYSTRA_AGENT_GUIDANCE_MANIFEST_INVALID",
		"PLYSTRA_PROJECT_CONCURRENT_CHANGE",
		"PLYSTRA_PROTOBUF_POINTER_PROJECTION_UNSUPPORTED",
		"declaration-owning `http.expose` document",
		"inherit that declaration from root `plystra.yaml`",
		"leaves authored, generated, module, and compatibility files unchanged",
		"path as an `agent-guidance` source",
	} {
		if !bytes.Contains(diagnostics, []byte(phrase)) {
			t.Fatalf("diagnostics guidance omits %q:\n%s", phrase, diagnostics)
		}
	}
	resources := byPath[Root+"/tasks/resources-and-data.md"]
	if !bytes.Contains(resources, []byte("plystra inspect capabilities --format json")) {
		t.Fatalf("Resource and Data guidance omits installed capability discovery:\n%s", resources)
	}
	for _, phrase := range []string{
		"plystra inspect resources --format json", "contract_digest",
		"64 type-reference levels and 65,536 public shape nodes", "PLYSTRA_RESOURCE_ID_DUPLICATE",
		"resource.provider.discovery", "PLYSTRA_RESOURCE_PROVIDER_DECLARATION_INVALID", "PLYSTRA_RESOURCE_PROVIDER_INVALID",
		"Discovery alone does not select or execute providers", "resource.consumer.discovery",
		"plystra inspect implementations --format json", "declares-dependency edges with reason resource",
		"exact parameter_name and one-based parameter_position", "PLYSTRA_IMPLEMENTATION_REQUIRED_RESOURCE_INVALID",
		"resources.instances.<name>", "1 through 128 ASCII bytes",
		`[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?:\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*)*`,
		"A config-only higher layer may inherit use", "Changing use replaces the whole instance and discards old configuration",
		"resources.bind.implementations.<constructor>.<parameter>", "resources.bind.instances.<consumer-instance>.<parameter>",
		"zero is missing and multiple matches are ambiguous", "including dormant Implementation consumers",
		"PLYSTRA_RESOURCE_METADATA_INVALID", "PLYSTRA_RESOURCE_CONFIGURATION_SCHEMA_INVALID",
		"PLYSTRA_RESOURCE_CONFIGURATION_VALUES_INVALID", "PLYSTRA_RESOURCE_INSTANCE_INVALID",
		"PLYSTRA_RESOURCE_BINDING_MISSING", "PLYSTRA_RESOURCE_BINDING_AMBIGUOUS", "PLYSTRA_RESOURCE_BINDING_INVALID",
		"Every selected instance is active even when unconsumed", "distinct values, Config owners, and lifecycle members",
		"Providers depend only on Resources", "Resources do not create Interface roots, catalog entries, governed proxies",
		"non-nil partial constructor results and never-started lifecycle values",
		"source-independent private runtime baseline", "Runtime compatibility version 12 and frozen model version 19",
		"Resource mutation forms of plystra use and plystra implement are not installed",
		"implemented stages do not establish accepted=yes", "Gate 5 is incomplete",
	} {
		if !bytes.Contains(resources, []byte(phrase)) {
			t.Fatalf("Resource guidance omits %q", phrase)
		}
	}
	for _, forbidden := range []string{"TODO", "create a feature branch", "open a pull request", "push the change", "PLYSTRA_RESOURCE_BINDING_UNSUPPORTED", "Resource instance configuration and runtime binding remain unsupported", "Resource and Data inheritance remain unsupported", "inspect resources remains contract-only"} {
		for name, data := range byPath {
			if strings.Contains(strings.ToLower(string(data)), strings.ToLower(forbidden)) {
				t.Fatalf("%s contains process guidance %q", name, forbidden)
			}
		}
	}

	manifest, err := ParseManifest(byPath[ManifestPath])
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.Schema != Schema || manifest.CLIVersion != version.Current || manifest.KernelVersion != version.KernelVersion || manifest.SpecificationRevision != version.SpecificationRevision || !validDigest(manifest.CatalogDigest) {
		t.Fatalf("manifest release facts = %#v", manifest)
	}
	if len(manifest.Files) != len(files)-1 {
		t.Fatalf("manifest file count = %d, want %d", len(manifest.Files), len(files)-1)
	}
	for _, owned := range manifest.Files {
		data, exists := byPath[owned.Path]
		if !exists || owned.SHA256 != digest(data) {
			t.Fatalf("manifest entry %q = %q, file exists %t", owned.Path, owned.SHA256, exists)
		}
	}
}

func TestProjectionReturnsDefensiveCopies(t *testing.T) {
	t.Parallel()

	projection, err := Render("application")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	files := projection.Files()
	files[0].data[0] = 'X'
	files[0].path = "changed"
	manifest := projection.Manifest()
	manifest.Files[0].Path = "changed"

	freshFiles := projection.Files()
	freshManifest := projection.Manifest()
	if freshFiles[0].Path() != Root+"/SKILL.md" || freshFiles[0].Data()[0] != '-' || freshManifest.Files[0].Path != Root+"/SKILL.md" {
		t.Fatalf("projection was mutable: %#v %#v", freshFiles[0], freshManifest.Files[0])
	}
}

func TestParseManifestRejectsUnsafeOrNonCanonicalInput(t *testing.T) {
	t.Parallel()

	projection, err := Render("application")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	valid := projection.Manifest()
	owned := func(filePath string) Manifest {
		value := valid
		value.Files = []ManifestFile{{Path: filePath, SHA256: digest(nil)}}
		return value
	}
	encode := func(t *testing.T, manifest Manifest) []byte {
		t.Helper()
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		return data
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty document", data: nil},
		{name: "oversized document", data: bytes.Repeat([]byte("x"), maximumGuidanceFileBytes+1)},
		{name: "unknown schema", data: encode(t, func() Manifest { value := valid; value.Schema = "plystra.agent-guidance/v2"; return value }())},
		{name: "invalid catalog digest", data: encode(t, func() Manifest { value := valid; value.CatalogDigest = "sha256:bad"; return value }())},
		{name: "manifest owns itself", data: encode(t, func() Manifest {
			value := valid
			value.Files = []ManifestFile{{Path: ManifestPath, SHA256: digest(nil)}}
			return value
		}())},
		{name: "manifest owns local", data: encode(t, func() Manifest {
			value := valid
			value.Files = []ManifestFile{{Path: Root + "/local.md", SHA256: digest(nil)}}
			return value
		}())},
		{name: "manifest owns case-variant local", data: encode(t, owned(Root+"/LOCAL.md"))},
		{name: "manifest owns case-variant manifest", data: encode(t, owned(Root+"/MANIFEST.JSON"))},
		{name: "path traversal", data: encode(t, func() Manifest {
			value := valid
			value.Files = []ManifestFile{{Path: Root + "/tasks/../local.md", SHA256: digest(nil)}}
			return value
		}())},
		{name: "backslash", data: encode(t, owned(Root+`\tasks\unsafe.md`))},
		{name: "space", data: encode(t, owned(Root+"/tasks/not portable.md"))},
		{name: "unicode", data: encode(t, owned(Root+"/tasks/caf"+string(rune(0xe9))+".md"))},
		{name: "colon", data: encode(t, owned(Root+"/tasks/not:portable.md"))},
		{name: "trailing dot", data: encode(t, owned(Root+"/tasks/not-portable."))},
		{name: "reserved device", data: encode(t, owned(Root+"/tasks/CON.txt"))},
		{name: "reserved numbered device", data: encode(t, owned(Root+"/tasks/lpt9.md"))},
		{name: "oversized component", data: encode(t, owned(Root+"/tasks/"+strings.Repeat("a", maximumGuidanceComponentBytes+1)))},
		{name: "oversized path", data: encode(t, owned(Root+"/"+strings.Repeat(strings.Repeat("a", 200)+"/", 21)+"task.md"))},
		{name: "unsorted", data: encode(t, func() Manifest {
			value := valid
			value.Files[0], value.Files[1] = value.Files[1], value.Files[0]
			return value
		}())},
		{name: "case-insensitive aliases", data: encode(t, func() Manifest {
			value := valid
			value.Files = []ManifestFile{
				{Path: Root + "/SKILL.md", SHA256: digest(nil)},
				{Path: Root + "/skill.md", SHA256: digest([]byte("other"))},
			}
			return value
		}())},
		{name: "unknown field", data: []byte(`{"schema":"plystra.agent-guidance/v1","unknown":true}`)},
		{name: "trailing", data: append(encode(t, valid), []byte(" {}")...)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseManifest(test.data); err == nil {
				t.Fatalf("ParseManifest(%s) succeeded", test.data)
			}
		})
	}
}

func TestValidateManifestRejectsTooManyOwnedPaths(t *testing.T) {
	t.Parallel()

	projection, err := Render("application")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	manifest := projection.Manifest()
	manifest.Files = make([]ManifestFile, maximumGuidanceManifestFiles+1)
	for index := range manifest.Files {
		manifest.Files[index] = ManifestFile{
			Path:   fmt.Sprintf("%s/tasks/%04d.md", Root, index),
			SHA256: digest(nil),
		}
	}
	if err := validateManifest(manifest); err == nil {
		t.Fatal("validateManifest accepted too many owned paths")
	}
}

func TestOwnedPathLimitFitsDiagnosticSources(t *testing.T) {
	t.Parallel()

	accepted := guidancePathWithLength(maximumGuidancePathBytes)
	if !validOwnedPath(accepted) {
		t.Fatalf("%d-byte guidance path is invalid", len(accepted))
	}
	if _, err := diagnosticjson.CanonicalizeSources([]diagnosticjson.Source{{
		Module: "application",
		Path:   accepted,
		Kind:   "agent-guidance",
	}}); err != nil {
		t.Fatalf("maximum guidance path is not a diagnostic source: %v", err)
	}
	if oversized := guidancePathWithLength(maximumGuidancePathBytes + 1); validOwnedPath(oversized) {
		t.Fatalf("%d-byte guidance path is valid", len(oversized))
	}
}

func TestRenderRejectsUnsafeModuleLabel(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", " application", "application\nsecret", "application`escape"} {
		if _, err := Render(value); err == nil {
			t.Fatalf("Render(%q) succeeded", value)
		}
	}
}

func guidancePathWithLength(length int) string {
	result := Root
	for len(result) < length {
		remaining := length - len(result) - 1
		componentLength := min(remaining, maximumGuidanceComponentBytes)
		result += "/" + strings.Repeat("a", componentLength)
	}
	return result
}
