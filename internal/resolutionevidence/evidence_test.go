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
	first, err := resolutionevidence.Build(resolutionevidence.Input{Context: firstContext, Modules: participatingModules(false), PluginCandidates: participatingPluginCandidates(false)})
	if err != nil {
		t.Fatalf("Build(first): %v", err)
	}
	second, err := resolutionevidence.Build(resolutionevidence.Input{Context: secondContext, Modules: participatingModules(true), PluginCandidates: participatingPluginCandidates(true)})
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
	if first.DiscoveredPluginCount() != 2 {
		t.Fatalf("discovered Plugin count = %d", first.DiscoveredPluginCount())
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
	candidates := first.PluginCandidates()
	if len(candidates) != 2 || candidates[0].ID() != "example.shared" || candidates[0].ModulePath() != "example.com/shared" || candidates[0].ModuleRole() != resolutionevidence.ModuleRoleDependency || candidates[0].Path() != "shared" || candidates[0].Local() || candidates[1].ID() != "example.smtp" || candidates[1].ModulePath() != "example.com/smtp" || candidates[1].Path() != "smtp" {
		t.Fatalf("Plugin candidates = %#v", candidates)
	}
	if source := candidates[1].Source(); source.Module() != "corp.example/smtp" || source.Path() != "smtp/plugin.yaml" || source.Kind() != "plugin-declaration" || source.Line() != 1 || source.Column() != 1 {
		t.Fatalf("replacement Plugin source = %#v", source)
	}
	selectedPlugins := first.SelectedPlugins()
	if len(selectedPlugins) != 1 || selectedPlugins[0].ID() != "example.smtp" || selectedPlugins[0].ModulePath() != "example.com/smtp" || selectedPlugins[0].ModuleVersion() != "v1.3.0" || selectedPlugins[0].ModuleRole() != resolutionevidence.ModuleRoleDependency || selectedPlugins[0].Path() != "smtp" || selectedPlugins[0].Local() || selectedPlugins[0].Source() != candidates[1].Source() {
		t.Fatalf("selected Plugins = %#v", selectedPlugins)
	}
	reasons := selectedPlugins[0].Reasons()
	if len(reasons) != 1 || reasons[0].Kind() != resolutionevidence.PluginSelectionProvider || reasons[0].Capability() != "email.send/v1" {
		t.Fatalf("selected Plugin reasons = %#v", reasons)
	}
	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
		t.Fatalf("input permutation changed evidence:\nfirst:  %s %s\nsecond: %s %s", first.CanonicalJSON(), first.Digest(), second.CanonicalJSON(), second.Digest())
	}
	withoutUnselected, err := resolutionevidence.Build(resolutionevidence.Input{
		Context:          firstContext,
		Modules:          participatingModules(false),
		PluginCandidates: participatingPluginCandidates(false)[:1],
	})
	if err != nil {
		t.Fatalf("Build(without unselected candidate): %v", err)
	}
	if first.SelectedModelDigest() != withoutUnselected.SelectedModelDigest() || first.BuildModelDigest() != withoutUnselected.BuildModelDigest() || first.Digest() == withoutUnselected.Digest() {
		t.Fatal("unselected candidate did not change only the complete evidence identity")
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
		PluginCandidates []struct {
			ID         string `json:"id"`
			ModulePath string `json:"module_path"`
			Source     struct {
				Module string `json:"module"`
				Path   string `json:"path"`
			} `json:"source"`
		} `json:"plugin_candidates"`
		SelectedPlugins []struct {
			ID            string `json:"id"`
			ModulePath    string `json:"module_path"`
			ModuleVersion string `json:"module_version"`
			Reasons       []struct {
				Kind       string `json:"kind"`
				Capability string `json:"capability"`
			} `json:"reasons"`
		} `json:"selected_plugins"`
		Counts struct {
			ParticipatingModules int `json:"participating_modules"`
			DiscoveredPlugins    int `json:"discovered_plugins"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(first.CanonicalJSON(), &document); err != nil || document.Version != 1 || len(document.Modules) != 3 || document.Counts.ParticipatingModules != 3 || document.Counts.DiscoveredPlugins != 2 || document.Modules[2].Replacement == nil || document.Modules[2].Replacement.Kind != "module" || document.Modules[2].Source.Module != "corp.example/smtp" || document.Modules[2].Source.Path != "plystra.yaml" || len(document.PluginCandidates) != 2 || document.PluginCandidates[1].ID != "example.smtp" || document.PluginCandidates[1].ModulePath != "example.com/smtp" || document.PluginCandidates[1].Source.Module != "corp.example/smtp" || document.PluginCandidates[1].Source.Path != "smtp/plugin.yaml" || len(document.SelectedPlugins) != 1 || document.SelectedPlugins[0].ID != "example.smtp" || document.SelectedPlugins[0].ModulePath != "example.com/smtp" || document.SelectedPlugins[0].ModuleVersion != "v1.3.0" || len(document.SelectedPlugins[0].Reasons) != 1 || document.SelectedPlugins[0].Reasons[0].Kind != "provider" || document.SelectedPlugins[0].Reasons[0].Capability != "email.send/v1" {
		t.Fatalf("canonical module evidence = %#v, %v", document, err)
	}
	for _, forbidden := range []string{"idempotency_key", "capability.yaml", "safe_name"} {
		if bytes.Contains(first.CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("bounded evidence contains detailed model value %q: %s", forbidden, first.CanonicalJSON())
		}
	}

	mutated := first.CanonicalJSON()
	mutated[0] = '['
	modules[0] = resolutionevidence.Module{}
	candidates[0] = resolutionevidence.PluginCandidate{}
	selectedPlugins[0] = resolutionevidence.SelectedPlugin{}
	reasons[0] = resolutionevidence.PluginSelectionReason{}
	if first.CanonicalJSON()[0] != '{' || first.Modules()[0].Path() != "example.com/app" || first.PluginCandidates()[0].ID() != "example.shared" || first.SelectedPlugins()[0].ID() != "example.smtp" || first.SelectedPlugins()[0].Reasons()[0].Capability() != "email.send/v1" || !first.Valid() {
		t.Fatal("CanonicalJSON exposed mutable evidence storage")
	}
}

func TestBuildSeparatesSelectionProvenanceFromBuildState(t *testing.T) {
	t.Parallel()

	baseContext := selectedContext(t, false, "a", true)
	selectionContext := selectedContext(t, false, "c", true)
	buildContext := selectedContext(t, false, "a", false)
	modules := participatingModules(false)
	candidates := participatingPluginCandidates(false)
	base, err := resolutionevidence.Build(resolutionevidence.Input{Context: baseContext, Modules: modules, PluginCandidates: candidates})
	if err != nil {
		t.Fatalf("Build(base): %v", err)
	}
	selection, err := resolutionevidence.Build(resolutionevidence.Input{Context: selectionContext, Modules: modules, PluginCandidates: candidates})
	if err != nil {
		t.Fatalf("Build(selection): %v", err)
	}
	build, err := resolutionevidence.Build(resolutionevidence.Input{Context: buildContext, Modules: modules, PluginCandidates: candidates})
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

func TestBuildOrdersEverySelectedPluginReasonDeterministically(t *testing.T) {
	t.Parallel()

	firstContext := multiProviderContext(t, false)
	secondContext := multiProviderContext(t, true)
	first, err := resolutionevidence.Build(resolutionevidence.Input{Context: firstContext, Modules: participatingModules(false), PluginCandidates: participatingPluginCandidates(false)})
	if err != nil {
		t.Fatalf("Build(first): %v", err)
	}
	second, err := resolutionevidence.Build(resolutionevidence.Input{Context: secondContext, Modules: participatingModules(true), PluginCandidates: participatingPluginCandidates(true)})
	if err != nil {
		t.Fatalf("Build(second): %v", err)
	}
	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
		t.Fatalf("provider permutation changed selected Plugin evidence:\nfirst:  %s\nsecond: %s", first.CanonicalJSON(), second.CanonicalJSON())
	}
	selected := first.SelectedPlugins()
	if len(selected) != 1 {
		t.Fatalf("selected Plugins = %#v", selected)
	}
	reasons := selected[0].Reasons()
	if len(reasons) != 2 || reasons[0].Kind() != resolutionevidence.PluginSelectionProvider || reasons[0].Capability() != "audit.write/v1" || reasons[1].Kind() != resolutionevidence.PluginSelectionProvider || reasons[1].Capability() != "email.send/v1" {
		t.Fatalf("selected Plugin reasons = %#v", reasons)
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
			evidence, err := resolutionevidence.Build(resolutionevidence.Input{Context: context, Modules: test.mutate(participatingModules(false)), PluginCandidates: participatingPluginCandidates(false)})
			if !errors.Is(err, resolutionevidence.ErrBuild) || evidence.Valid() {
				t.Fatalf("Build = %#v, %v", evidence, err)
			}
		})
	}
}

func TestBuildRejectsInvalidDiscoveredPluginCandidateEvidence(t *testing.T) {
	t.Parallel()

	context := selectedContext(t, false, "a", true)
	tests := []struct {
		name   string
		want   string
		mutate func([]resolutionevidence.PluginCandidateInput) []resolutionevidence.PluginCandidateInput
	}{
		{
			name: "missing selected candidate",
			want: "has no discovered candidate",
			mutate: func(candidates []resolutionevidence.PluginCandidateInput) []resolutionevidence.PluginCandidateInput {
				return candidates[1:]
			},
		},
		{
			name: "duplicate Plugin ID",
			want: "duplicates \"example.smtp\"",
			mutate: func(candidates []resolutionevidence.PluginCandidateInput) []resolutionevidence.PluginCandidateInput {
				return append(candidates, resolutionevidence.PluginCandidateInput{ID: "example.smtp", ModulePath: "example.com/shared", Path: "other"})
			},
		},
		{
			name: "duplicate source",
			want: "duplicates source",
			mutate: func(candidates []resolutionevidence.PluginCandidateInput) []resolutionevidence.PluginCandidateInput {
				return append(candidates, resolutionevidence.PluginCandidateInput{ID: "example.smtp-two", ModulePath: "example.com/smtp", Path: "smtp"})
			},
		},
		{
			name: "nonparticipating module",
			want: "is not a participating Project",
			mutate: func(candidates []resolutionevidence.PluginCandidateInput) []resolutionevidence.PluginCandidateInput {
				candidates[0].ModulePath = "example.com/ordinary"
				return candidates
			},
		},
		{
			name: "unsafe path",
			want: "one safe non-reserved root child",
			mutate: func(candidates []resolutionevidence.PluginCandidateInput) []resolutionevidence.PluginCandidateInput {
				candidates[0].Path = "../smtp"
				return candidates
			},
		},
		{
			name: "current candidate not selected",
			want: "current-Project Plugin candidate \"example.app\" is not selected",
			mutate: func(candidates []resolutionevidence.PluginCandidateInput) []resolutionevidence.PluginCandidateInput {
				return append(candidates, resolutionevidence.PluginCandidateInput{ID: "example.app", ModulePath: "example.com/app", Path: "application"})
			},
		},
		{
			name: "selected module mismatch",
			want: "does not match candidate module",
			mutate: func(candidates []resolutionevidence.PluginCandidateInput) []resolutionevidence.PluginCandidateInput {
				candidates[0].ModulePath = "example.com/shared"
				candidates[0].Path = "smtp"
				return candidates
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidates := test.mutate(participatingPluginCandidates(false))
			evidence, err := resolutionevidence.Build(resolutionevidence.Input{Context: context, Modules: participatingModules(false), PluginCandidates: candidates})
			if !errors.Is(err, resolutionevidence.ErrBuild) || !strings.Contains(err.Error(), test.want) || evidence.Valid() {
				t.Fatalf("Build = %#v, %v; want %q", evidence, err, test.want)
			}
		})
	}
}

func TestBuildRejectsSelectedDependencyPluginWithoutASelectionReason(t *testing.T) {
	t.Parallel()

	context, err := generation.NewContext(generation.Input{Plugins: []generation.PluginInput{{
		ID:                "example.smtp",
		ModulePath:        "example.com/smtp",
		ModuleVersion:     "v1.3.0",
		BuildMetadataJSON: []byte("{}"),
	}}})
	if err != nil {
		t.Fatalf("generation.NewContext: %v", err)
	}
	evidence, err := resolutionevidence.Build(resolutionevidence.Input{Context: context, Modules: participatingModules(false), PluginCandidates: participatingPluginCandidates(false)})
	if !errors.Is(err, resolutionevidence.ErrBuild) || !strings.Contains(err.Error(), "at least one selection reason is required") || evidence.Valid() {
		t.Fatalf("Build = %#v, %v", evidence, err)
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

func participatingPluginCandidates(reverse bool) []resolutionevidence.PluginCandidateInput {
	candidates := []resolutionevidence.PluginCandidateInput{
		{ID: "example.smtp", ModulePath: "example.com/smtp", Path: "smtp"},
		{ID: "example.shared", ModulePath: "example.com/shared", Path: "shared"},
	}
	if reverse {
		candidates[0], candidates[1] = candidates[1], candidates[0]
	}
	return candidates
}

func multiProviderContext(t testing.TB, reverse bool) generation.Context {
	t.Helper()
	audit := queryContract(t, "audit.write/v1")
	email := queryContract(t, "email.send/v1")
	capabilities := []generation.CapabilityInput{{ContractJSON: audit}, {ContractJSON: email}}
	requirements := []string{"audit.write/v1", "email.send/v1"}
	providers := []generation.ProviderInput{
		{Capability: "audit.write/v1", Plugin: "example.smtp"},
		{Capability: "email.send/v1", Plugin: "example.smtp"},
	}
	provides := []string{"audit.write/v1", "email.send/v1"}
	if reverse {
		capabilities[0], capabilities[1] = capabilities[1], capabilities[0]
		requirements[0], requirements[1] = requirements[1], requirements[0]
		providers[0], providers[1] = providers[1], providers[0]
		provides[0], provides[1] = provides[1], provides[0]
	}
	context, err := generation.NewContext(generation.Input{
		Plugins: []generation.PluginInput{{
			ID:                "example.smtp",
			ModulePath:        "example.com/smtp",
			ModuleVersion:     "v1.3.0",
			Provides:          provides,
			BuildMetadataJSON: []byte("{}"),
		}},
		Capabilities: capabilities,
		Requirements: requirements,
		Providers:    providers,
	})
	if err != nil {
		t.Fatalf("generation.NewContext: %v", err)
	}
	return context
}

func queryContract(t testing.TB, id string) []byte {
	t.Helper()
	return normalizeContract(t, "id: "+id+"\n"+`request: {}
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
			ModuleVersion:     "v1.3.0",
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
