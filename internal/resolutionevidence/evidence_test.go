package resolutionevidence_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestBuildConstructsDeterministicNormalizedModelEvidence(t *testing.T) {
	t.Parallel()

	firstContext := selectedContext(t, false, "a", true)
	secondContext := selectedContext(t, true, "a", true)
	first, err := resolutionevidence.Build(resolutionevidence.Input{Context: firstContext, Modules: participatingModules(false)})
	if err != nil {
		t.Fatalf("Build(first): %v", err)
	}
	second, err := resolutionevidence.Build(resolutionevidence.Input{Context: secondContext, Modules: participatingModules(true)})
	if err != nil {
		t.Fatalf("Build(second): %v", err)
	}
	if !first.Valid() || !second.Valid() {
		t.Fatal("Build returned invalid evidence")
	}
	if first.SchemaVersion() != 1 || first.GenerationAPIVersion() != generation.Version {
		t.Fatalf("evidence versions = schema %d generation %q", first.SchemaVersion(), first.GenerationAPIVersion())
	}
	if first.SelectedModelDigest() != firstContext.Digest() || first.BuildModelDigest() != firstContext.BuildModelDigest() {
		t.Fatalf("evidence model digests = selected %q build %q", first.SelectedModelDigest(), first.BuildModelDigest())
	}
	if first.SelectedPluginCount() != 1 || first.CanonicalCapabilityCount() != 2 || first.RequirementCount() != 2 || first.SelectedProviderCount() != 1 || first.CapabilityAliasCount() != 1 {
		t.Fatalf("evidence counts = plugins %d capabilities %d requirements %d providers %d aliases %d", first.SelectedPluginCount(), first.CanonicalCapabilityCount(), first.RequirementCount(), first.SelectedProviderCount(), first.CapabilityAliasCount())
	}
	if first.ParticipatingModuleCount() != 3 {
		t.Fatalf("participating module count = %d", first.ParticipatingModuleCount())
	}
	modules := first.Modules()
	if len(modules) != 3 || modules[0].Path() != "example.com/app" || modules[0].Role() != resolutionevidence.ModuleRoleCurrent || modules[1].Path() != "example.com/shared" || !modules[1].Workspace() || modules[2].Path() != "example.com/smtp" || !modules[2].Direct() || modules[2].RequiredVersion() != "v1.2.0" || modules[2].SelectedVersion() != "v1.3.0" {
		t.Fatalf("participating modules = %#v", modules)
	}
	replacement, exists := modules[2].Replacement()
	if !exists || replacement.Kind() != resolutionevidence.ReplacementModule || replacement.ModulePath() != "corp.example/smtp" || replacement.Version() != "v1.3.0" {
		t.Fatalf("versioned replacement = %#v, %t", replacement, exists)
	}
	if source := modules[2].Source(); source.Module() != "corp.example/smtp" || source.Path() != "plystra.yaml" || source.Kind() != "project-marker" || source.Line() != 1 || source.Column() != 1 {
		t.Fatalf("replacement source = %#v", source)
	}
	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
		t.Fatalf("input permutation changed evidence:\nfirst:  %s %s\nsecond: %s %s", first.CanonicalJSON(), first.Digest(), second.CanonicalJSON(), second.Digest())
	}
	var document struct {
		Version int `json:"version"`
		Modules []struct {
			Path        string `json:"path"`
			Replacement *struct {
				Kind       string `json:"kind"`
				ModulePath string `json:"module_path"`
			} `json:"replacement"`
			Source struct {
				Module string `json:"module"`
				Path   string `json:"path"`
			} `json:"source"`
		} `json:"modules"`
		Counts struct {
			ParticipatingModules int `json:"participating_modules"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(first.CanonicalJSON(), &document); err != nil || document.Version != 1 || len(document.Modules) != 3 || document.Counts.ParticipatingModules != 3 || document.Modules[2].Replacement == nil || document.Modules[2].Replacement.Kind != "module" || document.Modules[2].Source.Module != "corp.example/smtp" || document.Modules[2].Source.Path != "plystra.yaml" {
		t.Fatalf("canonical module evidence = %#v, %v", document, err)
	}
	for _, forbidden := range []string{"example.smtp", "email.send/v1", "capability.yaml", "safe_name"} {
		if bytes.Contains(first.CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("bounded evidence contains detailed model value %q: %s", forbidden, first.CanonicalJSON())
		}
	}

	mutated := first.CanonicalJSON()
	mutated[0] = '['
	modules[0] = resolutionevidence.Module{}
	if first.CanonicalJSON()[0] != '{' || first.Modules()[0].Path() != "example.com/app" || !first.Valid() {
		t.Fatal("CanonicalJSON exposed mutable evidence storage")
	}
}

func TestBuildSeparatesSelectionProvenanceFromBuildState(t *testing.T) {
	t.Parallel()

	baseContext := selectedContext(t, false, "a", true)
	selectionContext := selectedContext(t, false, "c", true)
	buildContext := selectedContext(t, false, "a", false)
	modules := participatingModules(false)
	base, err := resolutionevidence.Build(resolutionevidence.Input{Context: baseContext, Modules: modules})
	if err != nil {
		t.Fatalf("Build(base): %v", err)
	}
	selection, err := resolutionevidence.Build(resolutionevidence.Input{Context: selectionContext, Modules: modules})
	if err != nil {
		t.Fatalf("Build(selection): %v", err)
	}
	build, err := resolutionevidence.Build(resolutionevidence.Input{Context: buildContext, Modules: modules})
	if err != nil {
		t.Fatalf("Build(build): %v", err)
	}
	if base.SelectedModelDigest() == selection.SelectedModelDigest() || base.Digest() == selection.Digest() {
		t.Fatal("selected-configuration provenance did not alter evidence identity")
	}
	if base.BuildModelDigest() != selection.BuildModelDigest() {
		t.Fatal("selected-configuration provenance altered build-model identity")
	}
	if base.BuildModelDigest() == build.BuildModelDigest() || base.Digest() == build.Digest() {
		t.Fatal("build-visible exposure change did not alter evidence identity")
	}
}

func TestBuildRejectsAnAbsentNormalizedModel(t *testing.T) {
	t.Parallel()

	evidence, err := resolutionevidence.Build(resolutionevidence.Input{})
	if !errors.Is(err, resolutionevidence.ErrBuild) || evidence.Valid() {
		t.Fatalf("Build(zero context) = %#v, %v", evidence, err)
	}
}

func TestBuildRejectsInvalidParticipatingProjectEvidence(t *testing.T) {
	t.Parallel()

	context := selectedContext(t, false, "a", true)
	tests := []struct {
		name   string
		mutate func([]resolutionevidence.ModuleInput) []resolutionevidence.ModuleInput
	}{
		{
			name: "no current Project",
			mutate: func(modules []resolutionevidence.ModuleInput) []resolutionevidence.ModuleInput {
				return modules[1:]
			},
		},
		{
			name: "duplicate module path",
			mutate: func(modules []resolutionevidence.ModuleInput) []resolutionevidence.ModuleInput {
				modules[2].Path = modules[1].Path
				return modules
			},
		},
		{
			name: "machine path as local identity",
			mutate: func(modules []resolutionevidence.ModuleInput) []resolutionevidence.ModuleInput {
				modules[1].SourceModulePath = `C:\workspace\smtp`
				modules[1].Replacement = &resolutionevidence.ReplacementInput{Kind: resolutionevidence.ReplacementLocal, ModulePath: `C:\workspace\smtp`}
				return modules
			},
		},
		{
			name: "workspace selected version",
			mutate: func(modules []resolutionevidence.ModuleInput) []resolutionevidence.ModuleInput {
				modules[2].SelectedVersion = "v1.0.0"
				return modules
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evidence, err := resolutionevidence.Build(resolutionevidence.Input{Context: context, Modules: test.mutate(participatingModules(false))})
			if !errors.Is(err, resolutionevidence.ErrBuild) || evidence.Valid() {
				t.Fatalf("Build = %#v, %v", evidence, err)
			}
		})
	}
}

func participatingModules(reverse bool) []resolutionevidence.ModuleInput {
	modules := []resolutionevidence.ModuleInput{
		{
			Path:             "example.com/app",
			Role:             resolutionevidence.ModuleRoleCurrent,
			SourceModulePath: "example.com/app",
		},
		{
			Path:             "example.com/smtp",
			Role:             resolutionevidence.ModuleRoleDependency,
			RequiredVersion:  "v1.2.0",
			SelectedVersion:  "v1.3.0",
			Direct:           true,
			SourceModulePath: "corp.example/smtp",
			Replacement: &resolutionevidence.ReplacementInput{
				Kind:       resolutionevidence.ReplacementModule,
				ModulePath: "corp.example/smtp",
				Version:    "v1.3.0",
			},
		},
		{
			Path:             "example.com/shared",
			Role:             resolutionevidence.ModuleRoleDependency,
			Workspace:        true,
			SourceModulePath: "example.com/shared",
		},
	}
	if reverse {
		modules[0], modules[2] = modules[2], modules[0]
	}
	return modules
}

func selectedContext(t testing.TB, reverse bool, selectedDigestCharacter string, exposed bool) generation.Context {
	t.Helper()
	health := normalizeContract(t, `id: kernel.health/v1
request: {}
response: {}
semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`)
	email := normalizeContract(t, `id: email.send/v1
request:
  idempotency_key: {type: string, required: true}
  partition: {type: integer, required: true}
response: {}
semantics:
  kind: command
  effects: external-write
  idempotency: {mode: keyed, request_field: idempotency_key}
  retry: {safety: requires-idempotency-key}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: per-key, request_field: partition}
  data: {request: confidential, response: restricted}
`)
	exposure := generation.Exposure{Go: true, HTTP: exposed, JavaScript: exposed}
	capabilities := []generation.CapabilityInput{
		{ContractJSON: email, Sources: []string{"plugins/email/capabilities/email.send/v1/capability.yaml"}, Exposure: exposure},
		{ContractJSON: health, Sources: []string{"kernel:kernel.health/v1"}, Intrinsic: true, Exposure: generation.Exposure{Go: true}},
	}
	requirements := []string{"email.send/v1", "kernel.health/v1"}
	if reverse {
		capabilities[0], capabilities[1] = capabilities[1], capabilities[0]
		requirements[0], requirements[1] = requirements[1], requirements[0]
	}
	selectedDigest := "sha256:" + strings.Repeat(selectedDigestCharacter, 64)
	context, err := generation.NewContext(generation.Input{
		ConfigurationProvenance: &generation.ConfigurationProvenanceInput{
			Mode:                        generation.ConfigurationModeDefault,
			RootPath:                    "plystra.yaml",
			RootDigest:                  selectedDigest,
			SelectedPath:                "plystra.yaml",
			SelectedDigest:              selectedDigest,
			DependencyCompositionDigest: "sha256:" + strings.Repeat("b", 64),
		},
		Plugins: []generation.PluginInput{{
			ID:                "example.smtp",
			ModulePath:        "example.com/smtp",
			Provides:          []string{"email.send/v1"},
			Requires:          []string{"kernel.health/v1"},
			BuildMetadataJSON: []byte(`{"safe_name":"smtp"}`),
		}},
		Capabilities: capabilities,
		Requirements: requirements,
		Providers:    []generation.ProviderInput{{Capability: "email.send/v1", Plugin: "example.smtp"}},
		CapabilityAliases: []generation.CapabilityAliasInput{{
			ID:       "mail.send/v1",
			Target:   "email.send/v1",
			Exposure: generation.Exposure{HTTP: exposed, JavaScript: exposed},
			Sources:  []generation.AliasSourceInput{{Kind: generation.AliasSourceApplication, ID: "application"}},
		}},
	})
	if err != nil {
		t.Fatalf("generation.NewContext: %v", err)
	}
	return context
}

func normalizeContract(t testing.TB, source string) []byte {
	t.Helper()
	canonical, err := capabilitymeta.NormalizeSchema([]byte(source))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	return canonical
}
