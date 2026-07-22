package resolutionevidence_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/providerresolution"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestBuildConstructsDeterministicNormalizedModelEvidence(t *testing.T) {
	t.Parallel()

	firstContext := selectedContext(t, false, "a", true)
	secondContext := selectedContext(t, true, "a", true)
	first, err := resolutionevidence.Build(resolutionEvidenceInput(t, firstContext, participatingModules(false), participatingPluginCandidates(false)))
	if err != nil {
		t.Fatalf("Build(first): %v", err)
	}
	second, err := resolutionevidence.Build(resolutionEvidenceInput(t, secondContext, participatingModules(true), participatingPluginCandidates(true)))
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
	if first.SelectedPluginCount() != 1 || first.CanonicalCapabilityCount() != 2 || first.RequirementCount() != 2 || first.ProviderCandidateCount() != 1 || first.RejectedProviderCount() != 0 || first.SelectedProviderCount() != 2 || first.CapabilityAliasCount() != 1 {
		t.Fatalf("evidence counts = plugins %d capabilities %d requirements %d candidates %d rejected %d providers %d aliases %d", first.SelectedPluginCount(), first.CanonicalCapabilityCount(), first.RequirementCount(), first.ProviderCandidateCount(), first.RejectedProviderCount(), first.SelectedProviderCount(), first.CapabilityAliasCount())
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
	providerCandidates := first.ProviderCandidates()
	if len(providerCandidates) != 1 || providerCandidates[0].Capability() != "email.send/v1" || providerCandidates[0].PluginID() != "example.smtp" || providerCandidates[0].ProjectModule() != "example.com/smtp" || providerCandidates[0].ContractDigest() == "" || providerCandidates[0].Rejected() || providerCandidates[0].RejectionReason() != "" || providerCandidates[0].Source().Module() != "corp.example/smtp" || providerCandidates[0].Source().Path() != "smtp/capabilities/email.send/v1/capability.yaml" || providerCandidates[0].Source().Kind() != "provider-declaration" {
		t.Fatalf("Provider candidates = %#v", providerCandidates)
	}
	selectedProviders := first.SelectedProviders()
	if len(selectedProviders) != 2 || selectedProviders[0].Capability() != "email.send/v1" || selectedProviders[0].PluginID() != "example.smtp" || selectedProviders[0].ProjectModule() != "example.com/smtp" || selectedProviders[0].ContractDigest() != providerCandidates[0].ContractDigest() || selectedProviders[0].ProviderSource() != providerCandidates[0].Source() || selectedProviders[0].SelectionReason() != resolutionevidence.ProviderSelectionSoleProvider || selectedProviders[0].Intrinsic() || len(selectedProviders[0].SelectionSources()) != 0 {
		t.Fatalf("ordinary selected Provider = %#v", selectedProviders)
	}
	if selectedProviders[1].Capability() != "kernel.health/v1" || selectedProviders[1].PluginID() != "" || selectedProviders[1].ProjectModule() != "" || selectedProviders[1].SelectionReason() != resolutionevidence.ProviderSelectionIntrinsic || !selectedProviders[1].Intrinsic() || selectedProviders[1].ProviderSource().Module() != "github.com/plystra/kernel" || selectedProviders[1].ProviderSource().Path() != "capability/catalog/definitions/kernel.health/v1/capability.yaml" || selectedProviders[1].ProviderSource().Kind() != "intrinsic-provider" || len(selectedProviders[1].SelectionSources()) != 0 {
		t.Fatalf("intrinsic selected Provider = %#v", selectedProviders[1])
	}
	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
		t.Fatalf("input permutation changed evidence:\nfirst:  %s %s\nsecond: %s %s", first.CanonicalJSON(), first.Digest(), second.CanonicalJSON(), second.Digest())
	}
	withoutUnselected, err := resolutionevidence.Build(resolutionEvidenceInput(t, firstContext, participatingModules(false), participatingPluginCandidates(false)[:1]))
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
		ProviderCandidates []struct {
			Capability      string `json:"capability"`
			PluginID        string `json:"plugin_id"`
			ProjectModule   string `json:"project_module"`
			ContractDigest  string `json:"contract_digest"`
			RejectionReason string `json:"rejection_reason"`
			Source          struct {
				Module string `json:"module"`
				Path   string `json:"path"`
				Kind   string `json:"kind"`
			} `json:"source"`
		} `json:"provider_candidates"`
		SelectedProviders []struct {
			Capability      string `json:"capability"`
			PluginID        string `json:"plugin_id"`
			ProjectModule   string `json:"project_module"`
			SelectionReason string `json:"selection_reason"`
			ProviderSource  struct {
				Module string `json:"module"`
				Path   string `json:"path"`
				Kind   string `json:"kind"`
			} `json:"provider_source"`
			SelectionSources []struct {
				ProjectModule string `json:"project_module"`
			} `json:"selection_sources"`
		} `json:"selected_providers"`
		Counts struct {
			ParticipatingModules int `json:"participating_modules"`
			DiscoveredPlugins    int `json:"discovered_plugins"`
			ProviderCandidates   int `json:"provider_candidates"`
			RejectedProviders    int `json:"rejected_providers"`
			SelectedProviders    int `json:"selected_providers"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(first.CanonicalJSON(), &document); err != nil || document.Version != 1 || len(document.Modules) != 3 || document.Counts.ParticipatingModules != 3 || document.Counts.DiscoveredPlugins != 2 || document.Counts.ProviderCandidates != 1 || document.Counts.RejectedProviders != 0 || document.Counts.SelectedProviders != 2 || document.Modules[2].Replacement == nil || document.Modules[2].Replacement.Kind != "module" || document.Modules[2].Source.Module != "corp.example/smtp" || document.Modules[2].Source.Path != "plystra.yaml" || len(document.PluginCandidates) != 2 || document.PluginCandidates[1].ID != "example.smtp" || document.PluginCandidates[1].ModulePath != "example.com/smtp" || document.PluginCandidates[1].Source.Module != "corp.example/smtp" || document.PluginCandidates[1].Source.Path != "smtp/plugin.yaml" || len(document.SelectedPlugins) != 1 || document.SelectedPlugins[0].ID != "example.smtp" || document.SelectedPlugins[0].ModulePath != "example.com/smtp" || document.SelectedPlugins[0].ModuleVersion != "v1.3.0" || len(document.SelectedPlugins[0].Reasons) != 1 || document.SelectedPlugins[0].Reasons[0].Kind != "provider" || document.SelectedPlugins[0].Reasons[0].Capability != "email.send/v1" || len(document.ProviderCandidates) != 1 || document.ProviderCandidates[0].Capability != "email.send/v1" || document.ProviderCandidates[0].PluginID != "example.smtp" || document.ProviderCandidates[0].ProjectModule != "example.com/smtp" || document.ProviderCandidates[0].RejectionReason != "" || document.ProviderCandidates[0].Source.Module != "corp.example/smtp" || document.ProviderCandidates[0].Source.Path != "smtp/capabilities/email.send/v1/capability.yaml" || document.ProviderCandidates[0].Source.Kind != "provider-declaration" || len(document.SelectedProviders) != 2 || document.SelectedProviders[0].Capability != "email.send/v1" || document.SelectedProviders[0].SelectionReason != "sole-provider" || document.SelectedProviders[0].ProviderSource.Module != "corp.example/smtp" || document.SelectedProviders[1].Capability != "kernel.health/v1" || document.SelectedProviders[1].SelectionReason != "intrinsic-kernel" || document.SelectedProviders[1].ProviderSource.Module != "github.com/plystra/kernel" {
		t.Fatalf("canonical module evidence = %#v, %v", document, err)
	}
	for _, forbidden := range []string{"idempotency_key", "safe_name", "Provider-independent audit"} {
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
	providerCandidates[0] = resolutionevidence.ProviderCandidate{}
	selectedProviders[0] = resolutionevidence.SelectedProvider{}
	if first.CanonicalJSON()[0] != '{' || first.Modules()[0].Path() != "example.com/app" || first.PluginCandidates()[0].ID() != "example.shared" || first.SelectedPlugins()[0].ID() != "example.smtp" || first.SelectedPlugins()[0].Reasons()[0].Capability() != "email.send/v1" || first.ProviderCandidates()[0].PluginID() != "example.smtp" || first.SelectedProviders()[0].PluginID() != "example.smtp" || !first.Valid() {
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
	base, err := resolutionevidence.Build(resolutionEvidenceInput(t, baseContext, modules, candidates))
	if err != nil {
		t.Fatalf("Build(base): %v", err)
	}
	selection, err := resolutionevidence.Build(resolutionEvidenceInput(t, selectionContext, modules, candidates))
	if err != nil {
		t.Fatalf("Build(selection): %v", err)
	}
	build, err := resolutionevidence.Build(resolutionEvidenceInput(t, buildContext, modules, candidates))
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
	first, err := resolutionevidence.Build(resolutionEvidenceInput(t, firstContext, participatingModules(false), participatingPluginCandidates(false)))
	if err != nil {
		t.Fatalf("Build(first): %v", err)
	}
	second, err := resolutionevidence.Build(resolutionEvidenceInput(t, secondContext, participatingModules(true), participatingPluginCandidates(true)))
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

func TestBuildRecordsEveryProviderCandidateAndStableRejectionReason(t *testing.T) {
	t.Parallel()

	email := queryContract(t, "email.send/v1")
	queue := queryContract(t, "queue.push/v1")
	context, err := generation.NewContext(generation.Input{
		Plugins: []generation.PluginInput{{
			ID:                "example.smtp",
			ModulePath:        "example.com/smtp",
			ModuleVersion:     "v1.3.0",
			Provides:          []string{"email.send/v1"},
			BuildMetadataJSON: []byte("{}"),
		}},
		Capabilities: []generation.CapabilityInput{{ContractJSON: queue}, {ContractJSON: email}},
		Requirements: []string{"email.send/v1"},
		Providers:    []generation.ProviderInput{{Capability: "email.send/v1", Plugin: "example.smtp"}},
	})
	if err != nil {
		t.Fatalf("generation.NewContext: %v", err)
	}
	plugins := []resolutionevidence.PluginCandidateInput{
		{ID: "example.queue", ModulePath: "example.com/shared", Path: "queue"},
		{ID: "example.smtp", ModulePath: "example.com/smtp", Path: "smtp"},
		{ID: "example.mailgun", ModulePath: "example.com/shared", Path: "mailgun"},
	}
	providerInputs := []providerresolution.Candidate{
		{PluginID: "example.queue", Contract: queue, Source: "queue diagnostic that must not enter evidence"},
		{PluginID: "example.smtp", Contract: email, Source: "smtp diagnostic that must not enter evidence"},
		{PluginID: "example.mailgun", Contract: email, Source: "mailgun diagnostic that must not enter evidence"},
	}
	requirement := providerresolution.Requirement{
		Contract: email,
		Source: providerresolution.RequirementSource{
			Kind:       providerresolution.RequirementDeclaration,
			Reference:  `plystra.yaml capabilities.require["email.send/v1"]`,
			ModulePath: "example.com/app",
			Path:       "plystra.yaml",
			Line:       1,
			Column:     1,
		},
	}
	choice := providerresolution.Choice{
		Capability: "email.send/v1",
		PluginID:   "example.smtp",
		Sources: []providerresolution.ChoiceSource{{
			Kind:       providerresolution.ChoiceSourceCurrentProject,
			Reference:  "plystra.yaml capabilities.use.email.send/v1",
			ModulePath: "example.com/app",
			Path:       "plystra.yaml",
			Line:       1,
			Column:     1,
		}},
	}

	build := func(reverse bool) resolutionevidence.Evidence {
		t.Helper()
		candidates := append([]providerresolution.Candidate(nil), providerInputs...)
		discovered := append([]resolutionevidence.PluginCandidateInput(nil), plugins...)
		if reverse {
			slices.Reverse(candidates)
			slices.Reverse(discovered)
		}
		resolved, err := providerresolution.Resolve(providerresolution.Input{
			Requirements: []providerresolution.Requirement{requirement},
			Candidates:   candidates,
			Choices:      []providerresolution.Choice{choice},
		})
		if err != nil {
			t.Fatalf("providerresolution.Resolve: %v", err)
		}
		evidence, err := resolutionevidence.Build(resolutionevidence.Input{
			Context:            context,
			ProviderResolution: resolved,
			Modules:            participatingModules(reverse),
			PluginCandidates:   discovered,
		})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return evidence
	}

	first := build(false)
	second := build(true)
	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
		t.Fatalf("Provider candidate input permutation changed evidence:\nfirst:  %s\nsecond: %s", first.CanonicalJSON(), second.CanonicalJSON())
	}
	candidates := first.ProviderCandidates()
	if first.ProviderCandidateCount() != 3 || first.RejectedProviderCount() != 2 || len(candidates) != 3 {
		t.Fatalf("Provider candidate counts = %d candidates, %d rejected; values %#v", first.ProviderCandidateCount(), first.RejectedProviderCount(), candidates)
	}
	if candidates[0].Capability() != "email.send/v1" || candidates[0].PluginID() != "example.mailgun" || candidates[0].ProjectModule() != "example.com/shared" || candidates[0].RejectionReason() != resolutionevidence.ProviderRejectionAnotherProviderSelected || candidates[0].Source().Module() != "example.com/shared" || candidates[0].Source().Path() != "mailgun/capabilities/email.send/v1/capability.yaml" {
		t.Fatalf("replaced email candidate = %#v", candidates[0])
	}
	if candidates[1].Capability() != "email.send/v1" || candidates[1].PluginID() != "example.smtp" || candidates[1].Rejected() || candidates[1].RejectionReason() != "" || candidates[1].Source().Module() != "corp.example/smtp" || candidates[1].Source().Path() != "smtp/capabilities/email.send/v1/capability.yaml" {
		t.Fatalf("selected email candidate = %#v", candidates[1])
	}
	if candidates[2].Capability() != "queue.push/v1" || candidates[2].PluginID() != "example.queue" || candidates[2].RejectionReason() != resolutionevidence.ProviderRejectionCapabilityNotRequired || candidates[2].Source().Path() != "queue/capabilities/queue.push/v1/capability.yaml" {
		t.Fatalf("unrequired queue candidate = %#v", candidates[2])
	}
	for _, forbidden := range []string{"queue diagnostic", "smtp diagnostic", "mailgun diagnostic"} {
		if bytes.Contains(first.CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("Provider evidence contains resolver diagnostic source %q: %s", forbidden, first.CanonicalJSON())
		}
	}
	candidates[0] = resolutionevidence.ProviderCandidate{}
	if first.ProviderCandidates()[0].PluginID() != "example.mailgun" || !first.Valid() {
		t.Fatal("ProviderCandidates exposed mutable evidence storage")
	}
}

func TestBuildRecordsEverySelectedProviderReasonAndChoiceSource(t *testing.T) {
	t.Parallel()

	contracts := map[string][]byte{
		"audit.write/v1":   queryContract(t, "audit.write/v1"),
		"email.send/v1":    queryContract(t, "email.send/v1"),
		"kernel.health/v1": queryContract(t, "kernel.health/v1"),
		"queue.push/v1":    queryContract(t, "queue.push/v1"),
	}
	build := func(reverse bool) resolutionevidence.Evidence {
		t.Helper()
		capabilities := []generation.CapabilityInput{
			{ContractJSON: contracts["audit.write/v1"]},
			{ContractJSON: contracts["email.send/v1"]},
			{ContractJSON: contracts["kernel.health/v1"], Intrinsic: true},
			{ContractJSON: contracts["queue.push/v1"]},
		}
		requirementIDs := []string{"audit.write/v1", "email.send/v1", "kernel.health/v1", "queue.push/v1"}
		providers := []generation.ProviderInput{
			{Capability: "audit.write/v1", Plugin: "example.app-audit"},
			{Capability: "email.send/v1", Plugin: "example.smtp"},
			{Capability: "queue.push/v1", Plugin: "example.smtp"},
		}
		plugins := []generation.PluginInput{
			{ID: "example.app-audit", ModulePath: "example.com/app", Provides: []string{"audit.write/v1"}, BuildMetadataJSON: []byte("{}")},
			{ID: "example.smtp", ModulePath: "example.com/smtp", ModuleVersion: "v1.3.0", Provides: []string{"email.send/v1", "queue.push/v1"}, BuildMetadataJSON: []byte("{}")},
		}
		if reverse {
			slices.Reverse(capabilities)
			slices.Reverse(requirementIDs)
			slices.Reverse(providers)
			slices.Reverse(plugins)
		}
		context, err := generation.NewContext(generation.Input{Plugins: plugins, Capabilities: capabilities, Requirements: requirementIDs, Providers: providers})
		if err != nil {
			t.Fatalf("generation.NewContext: %v", err)
		}
		requirements := make([]providerresolution.Requirement, 0, len(requirementIDs))
		for _, capability := range requirementIDs {
			requirements = append(requirements, providerresolution.Requirement{
				Contract: contracts[capability],
				Source: providerresolution.RequirementSource{
					Kind: providerresolution.RequirementDeclaration, Reference: "require " + capability,
					ModulePath: "example.com/app", Path: "plystra.yaml", Line: 1, Column: 1,
				},
			})
		}
		candidates := []providerresolution.Candidate{
			{PluginID: "example.app-audit", Contract: contracts["audit.write/v1"], Source: "current audit diagnostic"},
			{PluginID: "example.shared", Contract: contracts["audit.write/v1"], Source: "shared audit diagnostic"},
			{PluginID: "example.smtp", Contract: contracts["email.send/v1"], Source: "smtp email diagnostic"},
			{PluginID: "example.shared", Contract: contracts["queue.push/v1"], Source: "shared queue diagnostic"},
			{PluginID: "example.smtp", Contract: contracts["queue.push/v1"], Source: "smtp queue diagnostic"},
		}
		choices := []providerresolution.Choice{
			{
				Capability: "audit.write/v1", PluginID: "example.app-audit",
				Sources: []providerresolution.ChoiceSource{{Kind: providerresolution.ChoiceSourceCurrentProject, Reference: "private current diagnostic", ModulePath: "example.com/app", Path: "plystra.production.yaml", Line: 9, Column: 5}},
			},
			{
				Capability: "queue.push/v1", PluginID: "example.smtp",
				Sources: []providerresolution.ChoiceSource{
					{Kind: providerresolution.ChoiceSourceDependencyProject, Reference: "private smtp diagnostic", ModulePath: "example.com/smtp", Path: "plystra.yaml", Line: 3, Column: 7},
					{Kind: providerresolution.ChoiceSourceDependencyProject, Reference: "private shared diagnostic", ModulePath: "example.com/shared", Path: "plystra.yaml", Line: 4, Column: 2},
				},
			},
		}
		candidateInputs := []resolutionevidence.PluginCandidateInput{
			{ID: "example.app-audit", ModulePath: "example.com/app", Path: "audit"},
			{ID: "example.shared", ModulePath: "example.com/shared", Path: "shared"},
			{ID: "example.smtp", ModulePath: "example.com/smtp", Path: "smtp"},
		}
		modules := participatingModules(reverse)
		if reverse {
			slices.Reverse(candidates)
			slices.Reverse(choices)
			slices.Reverse(candidateInputs)
		}
		resolved, err := providerresolution.Resolve(providerresolution.Input{Requirements: requirements, Candidates: candidates, Choices: choices})
		if err != nil {
			t.Fatalf("providerresolution.Resolve: %v", err)
		}
		evidence, err := resolutionevidence.Build(resolutionevidence.Input{Context: context, ProviderResolution: resolved, Modules: modules, PluginCandidates: candidateInputs})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return evidence
	}

	first := build(false)
	second := build(true)
	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
		t.Fatalf("selected Provider permutation changed evidence:\nfirst:  %s\nsecond: %s", first.CanonicalJSON(), second.CanonicalJSON())
	}
	providers := first.SelectedProviders()
	if first.SelectedProviderCount() != 4 || len(providers) != 4 {
		t.Fatalf("selected Providers = %d %#v", first.SelectedProviderCount(), providers)
	}
	want := []struct {
		capability string
		reason     resolutionevidence.ProviderSelectionReason
		plugin     string
		sources    int
	}{
		{"audit.write/v1", resolutionevidence.ProviderSelectionCurrentProject, "example.app-audit", 1},
		{"email.send/v1", resolutionevidence.ProviderSelectionSoleProvider, "example.smtp", 0},
		{"kernel.health/v1", resolutionevidence.ProviderSelectionIntrinsic, "", 0},
		{"queue.push/v1", resolutionevidence.ProviderSelectionInherited, "example.smtp", 2},
	}
	for index, expected := range want {
		if providers[index].Capability() != expected.capability || providers[index].SelectionReason() != expected.reason || providers[index].PluginID() != expected.plugin || len(providers[index].SelectionSources()) != expected.sources {
			t.Fatalf("selected Providers[%d] = %#v", index, providers[index])
		}
	}
	currentSource := providers[0].SelectionSources()[0]
	if currentSource.ProjectModule() != "example.com/app" || currentSource.Source().Module() != "example.com/app" || currentSource.Source().Path() != "plystra.production.yaml" || currentSource.Source().Kind() != "provider-selection" || currentSource.Source().Line() != 9 || currentSource.Source().Column() != 5 {
		t.Fatalf("current selection source = %#v", currentSource)
	}
	inherited := providers[3].SelectionSources()
	if inherited[0].ProjectModule() != "example.com/shared" || inherited[0].Source().Module() != "example.com/shared" || inherited[1].ProjectModule() != "example.com/smtp" || inherited[1].Source().Module() != "corp.example/smtp" {
		t.Fatalf("inherited selection sources = %#v", inherited)
	}
	for _, forbidden := range []string{"private current diagnostic", "private smtp diagnostic", "private shared diagnostic", "current audit diagnostic", "smtp queue diagnostic"} {
		if bytes.Contains(first.CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("selected Provider evidence contains diagnostic text %q: %s", forbidden, first.CanonicalJSON())
		}
	}
	providers[0] = resolutionevidence.SelectedProvider{}
	inherited[0] = resolutionevidence.ProviderSelectionSource{}
	if first.SelectedProviders()[0].Capability() != "audit.write/v1" || first.SelectedProviders()[3].SelectionSources()[0].ProjectModule() != "example.com/shared" || !first.Valid() {
		t.Fatal("SelectedProviders exposed mutable evidence storage")
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
			evidence, err := resolutionevidence.Build(resolutionEvidenceInput(t, context, test.mutate(participatingModules(false)), participatingPluginCandidates(false)))
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
			evidence, err := resolutionevidence.Build(resolutionEvidenceInput(t, context, participatingModules(false), candidates))
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
	evidence, err := resolutionevidence.Build(resolutionEvidenceInput(t, context, participatingModules(false), participatingPluginCandidates(false)))
	if !errors.Is(err, resolutionevidence.ErrBuild) || !strings.Contains(err.Error(), "at least one selection reason is required") || evidence.Valid() {
		t.Fatalf("Build = %#v, %v", evidence, err)
	}
}

func TestBuildRecordsCanonicalCapabilityRequirementSources(t *testing.T) {
	t.Parallel()

	context := selectedContext(t, false, "a", true)
	contracts := make(map[string][]byte)
	digests := make(map[string]string)
	for _, capability := range context.Capabilities() {
		contracts[capability.ID().String()] = capability.ContractJSON()
		digests[capability.ID().String()] = capability.ContractDigest()
	}
	emailSources := []providerresolution.RequirementSource{
		{
			Kind:             providerresolution.RequirementGenerationRule,
			Reference:        "generation plugin example.smtp rule authn.require-email",
			ModulePath:       "example.com/smtp",
			Path:             "smtp/plugin.yaml",
			Line:             1,
			Column:           1,
			PluginID:         "example.smtp",
			Namespace:        "authn",
			SourceCapability: "order.create/v1",
			RuleID:           "authn.require-email",
		},
		{
			Kind:       providerresolution.RequirementExposure,
			Reference:  `plystra.yaml http.expose["email.send/v1"]`,
			ModulePath: "example.com/app",
			Path:       "plystra.yaml",
			Line:       8,
			Column:     5,
		},
		{
			Kind:       providerresolution.RequirementPlugin,
			Reference:  "plugin example.smtp requires email.send/v1",
			ModulePath: "example.com/smtp",
			Path:       "smtp/plugin.yaml",
			Line:       1,
			Column:     1,
			PluginID:   "example.smtp",
		},
		{
			Kind:       providerresolution.RequirementDeclaration,
			Reference:  `example.com/smtp@v1.3.0/plystra.yaml capabilities.require["email.send/v1"]`,
			ModulePath: "example.com/smtp",
			Path:       "plystra.yaml",
			Line:       4,
			Column:     7,
		},
		{
			Kind:       providerresolution.RequirementGeneratedClient,
			Reference:  "generated client import in example.smtp",
			ModulePath: "example.com/smtp",
			Path:       "smtp/internal/client.go",
			Line:       12,
			Column:     3,
			PluginID:   "example.smtp",
		},
		{
			Kind:             providerresolution.RequirementActivation,
			Reference:        "extensions.authn on order.create/v1",
			ModulePath:       "example.com/app",
			Path:             "plystra.yaml",
			Line:             3,
			Column:           5,
			Namespace:        "authn",
			SourceCapability: "order.create/v1",
		},
		{
			Kind:       providerresolution.RequirementAliasTarget,
			Reference:  `plystra.yaml capabilities.aliases["mail.send/v1"] target`,
			ModulePath: "example.com/app",
			Path:       "plystra.yaml",
			Line:       11,
			Column:     5,
			Alias:      "mail.send/v1",
		},
	}
	duplicateDeclaration := emailSources[3]
	duplicateDeclaration.Reference = "same declaration through a second diagnostic label"
	emailSources = append(emailSources, duplicateDeclaration)
	healthSource := providerresolution.RequirementSource{
		Kind:       providerresolution.RequirementDeclaration,
		Reference:  `plystra.yaml capabilities.require["kernel.health/v1"]`,
		ModulePath: "example.com/app",
		Path:       "plystra.yaml",
		Line:       5,
		Column:     7,
	}

	build := func(reverse bool) resolutionevidence.Evidence {
		t.Helper()
		sources := append([]providerresolution.RequirementSource(nil), emailSources...)
		if reverse {
			slices.Reverse(sources)
		}
		requirements := make([]providerresolution.Requirement, 0, len(sources)+1)
		for _, source := range sources {
			requirements = append(requirements, providerresolution.Requirement{Contract: contracts["email.send/v1"], Source: source})
		}
		requirements = append(requirements, providerresolution.Requirement{Contract: contracts["kernel.health/v1"], Source: healthSource})
		if reverse {
			slices.Reverse(requirements)
		}
		resolved, err := providerresolution.Resolve(providerresolution.Input{
			Requirements: requirements,
			Candidates: []providerresolution.Candidate{{
				PluginID: "example.smtp",
				Contract: contracts["email.send/v1"],
				Source:   "smtp/capability.yaml",
			}},
		})
		if err != nil {
			t.Fatalf("providerresolution.Resolve: %v", err)
		}
		evidence, err := resolutionevidence.Build(resolutionevidence.Input{
			Context:            context,
			ProviderResolution: resolved,
			Modules:            participatingModules(reverse),
			PluginCandidates:   participatingPluginCandidates(reverse),
		})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return evidence
	}

	first := build(false)
	second := build(true)
	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
		t.Fatalf("requirement input permutation changed evidence:\nfirst:  %s\nsecond: %s", first.CanonicalJSON(), second.CanonicalJSON())
	}
	requirements := first.Requirements()
	if len(requirements) != 2 || requirements[0].Capability() != "email.send/v1" || requirements[1].Capability() != "kernel.health/v1" {
		t.Fatalf("requirements = %#v", requirements)
	}
	if requirements[0].ContractDigest() != digests["email.send/v1"] || requirements[0].Intrinsic() {
		t.Fatalf("email requirement = %#v", requirements[0])
	}
	wantKinds := []providerresolution.RequirementSourceKind{
		providerresolution.RequirementActivation,
		providerresolution.RequirementAliasTarget,
		providerresolution.RequirementDeclaration,
		providerresolution.RequirementExposure,
		providerresolution.RequirementGeneratedClient,
		providerresolution.RequirementGenerationRule,
		providerresolution.RequirementPlugin,
	}
	sources := requirements[0].Sources()
	if len(sources) != len(wantKinds) {
		t.Fatalf("email requirement sources = %#v", sources)
	}
	for index, want := range wantKinds {
		if sources[index].Kind() != want || sources[index].Source().Kind() != string(want) {
			t.Fatalf("source[%d] = %#v, want kind %q", index, sources[index], want)
		}
	}
	if source := sources[0]; source.Namespace() != "authn" || source.SourceCapability() != "order.create/v1" || source.ProjectModule() != "example.com/app" {
		t.Fatalf("activation source = %#v", source)
	}
	if source := sources[1]; source.Alias() != "mail.send/v1" || source.ProjectModule() != "example.com/app" {
		t.Fatalf("Alias-target source = %#v", source)
	}
	if source := sources[2]; source.ProjectModule() != "example.com/smtp" || source.Source().Module() != "corp.example/smtp" || source.Source().Path() != "plystra.yaml" || source.Source().Line() != 4 || source.Source().Column() != 7 {
		t.Fatalf("replacement-safe declaration source = %#v", source)
	}
	if source := sources[4]; source.PluginID() != "example.smtp" || source.Source().Module() != "corp.example/smtp" || source.Source().Path() != "smtp/internal/client.go" {
		t.Fatalf("generated-client source = %#v", source)
	}
	if source := sources[5]; source.PluginID() != "example.smtp" || source.Namespace() != "authn" || source.SourceCapability() != "order.create/v1" || source.RuleID() != "authn.require-email" || source.Source().Module() != "corp.example/smtp" {
		t.Fatalf("generation-rule source = %#v", source)
	}
	if source := sources[6]; source.PluginID() != "example.smtp" || source.Source().Module() != "corp.example/smtp" || source.Source().Path() != "smtp/plugin.yaml" {
		t.Fatalf("Plugin source = %#v", source)
	}
	if requirements[1].ContractDigest() != digests["kernel.health/v1"] || !requirements[1].Intrinsic() || len(requirements[1].Sources()) != 1 || requirements[1].Sources()[0].ProjectModule() != "example.com/app" {
		t.Fatalf("intrinsic requirement = %#v", requirements[1])
	}
	for _, forbidden := range []string{"same declaration through a second diagnostic label", "generation plugin example.smtp", `capabilities.require[\"kernel.health/v1\"]`} {
		if bytes.Contains(first.CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("canonical evidence contains diagnostic reference %q: %s", forbidden, first.CanonicalJSON())
		}
	}

	requirements[0] = resolutionevidence.CapabilityRequirement{}
	sources[0] = resolutionevidence.RequirementSource{}
	if got := first.Requirements(); got[0].Capability() != "email.send/v1" || got[0].Sources()[0].Kind() != providerresolution.RequirementActivation {
		t.Fatal("Requirements or Requirement.Sources exposed mutable evidence storage")
	}
}

func TestBuildRejectsRequirementResolutionInconsistentWithSelectedContext(t *testing.T) {
	t.Parallel()

	context := selectedContext(t, false, "a", true)
	contracts := make(map[string][]byte)
	for _, capability := range context.Capabilities() {
		contracts[capability.ID().String()] = capability.ContractJSON()
	}
	resolve := func(t *testing.T, requirements []providerresolution.Requirement, emailContract []byte) providerresolution.Result {
		t.Helper()
		resolved, err := providerresolution.Resolve(providerresolution.Input{
			Requirements: requirements,
			Candidates: []providerresolution.Candidate{{
				PluginID: "example.smtp",
				Contract: emailContract,
				Source:   "smtp/capability.yaml",
			}},
		})
		if err != nil {
			t.Fatalf("providerresolution.Resolve: %v", err)
		}
		return resolved
	}
	declaration := func(modulePath string) providerresolution.RequirementSource {
		return providerresolution.RequirementSource{
			Kind:       providerresolution.RequirementDeclaration,
			Reference:  "configuration declaration",
			ModulePath: modulePath,
			Path:       "plystra.yaml",
			Line:       1,
			Column:     1,
		}
	}
	build := func(result providerresolution.Result) (resolutionevidence.Evidence, error) {
		return resolutionevidence.Build(resolutionevidence.Input{
			Context:            context,
			ProviderResolution: result,
			Modules:            participatingModules(false),
			PluginCandidates:   participatingPluginCandidates(false),
		})
	}

	t.Run("missing selected-model requirement", func(t *testing.T) {
		result := resolve(t, []providerresolution.Requirement{{Contract: contracts["email.send/v1"], Source: declaration("example.com/app")}}, contracts["email.send/v1"])
		evidence, err := build(result)
		if !errors.Is(err, resolutionevidence.ErrBuild) || !strings.Contains(err.Error(), "1 requirements while the selected model has 2") || evidence.Valid() {
			t.Fatalf("Build = %#v, %v", evidence, err)
		}
	})

	t.Run("contract differs", func(t *testing.T) {
		changed := normalizeContract(t, `id: email.send/v1
request: {}
response: {changed: {type: boolean, required: true}}
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
		result := resolve(t, []providerresolution.Requirement{
			{Contract: changed, Source: declaration("example.com/app")},
			{Contract: contracts["kernel.health/v1"], Source: declaration("example.com/app")},
		}, changed)
		evidence, err := build(result)
		if !errors.Is(err, resolutionevidence.ErrBuild) || !strings.Contains(err.Error(), "does not match the selected canonical contract") || evidence.Valid() {
			t.Fatalf("Build = %#v, %v", evidence, err)
		}
	})

	t.Run("source Project does not participate", func(t *testing.T) {
		result := resolve(t, []providerresolution.Requirement{
			{Contract: contracts["email.send/v1"], Source: declaration("example.com/unrelated")},
			{Contract: contracts["kernel.health/v1"], Source: declaration("example.com/app")},
		}, contracts["email.send/v1"])
		evidence, err := build(result)
		if !errors.Is(err, resolutionevidence.ErrBuild) || !strings.Contains(err.Error(), "is not a participating Project") || evidence.Valid() {
			t.Fatalf("Build = %#v, %v", evidence, err)
		}
	})

	t.Run("selected Provider differs", func(t *testing.T) {
		result, err := providerresolution.Resolve(providerresolution.Input{
			Requirements: []providerresolution.Requirement{
				{Contract: contracts["email.send/v1"], Source: declaration("example.com/app")},
				{Contract: contracts["kernel.health/v1"], Source: declaration("example.com/app")},
			},
			Candidates: []providerresolution.Candidate{{PluginID: "example.shared", Contract: contracts["email.send/v1"], Source: "shared/email"}},
		})
		if err != nil {
			t.Fatalf("providerresolution.Resolve: %v", err)
		}
		evidence, err := build(result)
		if !errors.Is(err, resolutionevidence.ErrBuild) || !strings.Contains(err.Error(), "does not match provider resolution") || evidence.Valid() {
			t.Fatalf("Build = %#v, %v", evidence, err)
		}
	})

	t.Run("selection source Project does not participate", func(t *testing.T) {
		result, err := providerresolution.Resolve(providerresolution.Input{
			Requirements: []providerresolution.Requirement{
				{Contract: contracts["email.send/v1"], Source: declaration("example.com/app")},
				{Contract: contracts["kernel.health/v1"], Source: declaration("example.com/app")},
			},
			Candidates: []providerresolution.Candidate{{PluginID: "example.smtp", Contract: contracts["email.send/v1"], Source: "smtp/email"}},
			Choices: []providerresolution.Choice{{
				Capability: "email.send/v1",
				PluginID:   "example.smtp",
				Sources: []providerresolution.ChoiceSource{{
					Kind: providerresolution.ChoiceSourceCurrentProject, Reference: "outside choice",
					ModulePath: "example.com/unrelated", Path: "plystra.yaml", Line: 1, Column: 1,
				}},
			}},
		})
		if err != nil {
			t.Fatalf("providerresolution.Resolve: %v", err)
		}
		evidence, err := build(result)
		if !errors.Is(err, resolutionevidence.ErrBuild) || !strings.Contains(err.Error(), "nonparticipating Project") || evidence.Valid() {
			t.Fatalf("Build = %#v, %v", evidence, err)
		}
	})

	t.Run("candidate Capability is absent from selected catalog", func(t *testing.T) {
		result, err := providerresolution.Resolve(providerresolution.Input{
			Requirements: []providerresolution.Requirement{
				{Contract: contracts["email.send/v1"], Source: declaration("example.com/app")},
				{Contract: contracts["kernel.health/v1"], Source: declaration("example.com/app")},
			},
			Candidates: []providerresolution.Candidate{
				{PluginID: "example.smtp", Contract: contracts["email.send/v1"], Source: "smtp/email"},
				{PluginID: "example.shared", Contract: queryContract(t, "queue.push/v1"), Source: "shared/queue"},
			},
		})
		if err != nil {
			t.Fatalf("providerresolution.Resolve: %v", err)
		}
		evidence, err := build(result)
		if !errors.Is(err, resolutionevidence.ErrBuild) || !strings.Contains(err.Error(), "queue.push/v1 is absent from the selected canonical catalog") || evidence.Valid() {
			t.Fatalf("Build = %#v, %v", evidence, err)
		}
	})
}

func resolutionEvidenceInput(t testing.TB, context generation.Context, modules []resolutionevidence.ModuleInput, candidates []resolutionevidence.PluginCandidateInput) resolutionevidence.Input {
	t.Helper()
	capabilities := make(map[string]generation.CapabilityView)
	for _, capability := range context.Capabilities() {
		capabilities[capability.ID().String()] = capability
	}
	requirements := make([]providerresolution.Requirement, 0, len(context.Requirements()))
	for _, id := range context.Requirements() {
		capability := capabilities[id.String()]
		requirements = append(requirements, providerresolution.Requirement{
			Contract: capability.ContractJSON(),
			Source: providerresolution.RequirementSource{
				Kind:       providerresolution.RequirementDeclaration,
				Reference:  `plystra.yaml capabilities.require["` + id.String() + `"]`,
				ModulePath: "example.com/app",
				Path:       "plystra.yaml",
				Line:       1,
				Column:     1,
			},
		})
	}
	providerCandidates := make([]providerresolution.Candidate, 0, len(context.Providers()))
	for _, provider := range context.Providers() {
		capability := capabilities[provider.Capability().String()]
		providerCandidates = append(providerCandidates, providerresolution.Candidate{
			PluginID: provider.Plugin().String(),
			Contract: capability.ContractJSON(),
			Source:   provider.Plugin().String() + "/capability.yaml",
		})
	}
	providerResult, err := providerresolution.Resolve(providerresolution.Input{Requirements: requirements, Candidates: providerCandidates})
	if err != nil {
		t.Fatalf("providerresolution.Resolve: %v", err)
	}
	return resolutionevidence.Input{
		Context:            context,
		ProviderResolution: providerResult,
		Modules:            modules,
		PluginCandidates:   candidates,
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
