package resolutionevidence

import (
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/providerresolution"
)

func TestCapabilityAliasEvidenceRejectsInconsistentFinalProvenance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		want   string
		mutate func(*[]CapabilityAlias, *[]Module, *[]CapabilityRequirement, *[]PluginCandidate, *[]GenerationActivation)
	}{
		{
			name: "reserved Alias ID",
			want: `aliases[0].id "kernel.health/v1" is invalid`,
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].id = "kernel.health/v1"
			},
		},
		{
			name: "target major differs",
			want: `aliases[0].target "order.create/v2" is invalid`,
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].target = "order.create/v2"
			},
		},
		{
			name: "target contract differs",
			want: "target order.create/v1 contract identity is inconsistent",
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].targetContractDigest = testDigest("b")
			},
		},
		{
			name: "sources absent",
			want: "aliases[0].sources must not be empty",
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, requirements *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].sources = nil
				(*requirements)[0].sources = nil
			},
		},
		{
			name: "sources duplicated",
			want: "sources are not in unique canonical order",
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].sources = append((*aliases)[0].sources, (*aliases)[0].sources[1])
			},
		},
		{
			name: "source Project absent",
			want: "Project provenance is inconsistent",
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].sources[0].projectModule = "example.com/missing"
			},
		},
		{
			name: "source location invalid",
			want: "has invalid stable location",
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].sources[0].source.path = ""
			},
		},
		{
			name: "application fields invalid",
			want: "application provenance is invalid",
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].sources[0].pluginID = "example.authn"
			},
		},
		{
			name: "application declaration absent",
			want: "has no matching application declaration",
			mutate: func(_ *[]CapabilityAlias, _ *[]Module, requirements *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*requirements)[0].sources = nil
			},
		},
		{
			name: "generation Plugin absent",
			want: "generation contribution is invalid",
			mutate: func(_ *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, plugins *[]PluginCandidate, _ *[]GenerationActivation) {
				*plugins = nil
			},
		},
		{
			name: "generation Plugin Project differs",
			want: "generation contribution is invalid",
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].sources[1].projectModule = "example.com/app"
				(*aliases)[0].sources[1].source.module = "example.com/app"
			},
		},
		{
			name: "contribution ID invalid",
			want: "generation contribution is invalid",
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].sources[1].contributionID = "AuthN.order-submit"
			},
		},
		{
			name: "namespace invalid",
			want: "generation contribution is invalid",
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].sources[1].namespace = "AuthN"
			},
		},
		{
			name: "source Capability not required",
			want: "source Capability profile.get/v1 is not required",
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].sources[1].sourceCapability = "profile.get/v1"
			},
		},
		{
			name: "selected activation absent",
			want: "has no matching selected activation",
			mutate: func(_ *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, activations *[]GenerationActivation) {
				*activations = nil
			},
		},
		{
			name: "activation Capability differs",
			want: "activation Capability is inconsistent",
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].sources[1].activationCapability = "authn.other/v1"
			},
		},
		{
			name: "generation location differs",
			want: "generation source location is inconsistent",
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].sources[1].source.path = "authn/generation/generate.go"
			},
		},
		{
			name: "source kind invalid",
			want: `.kind "other" is invalid`,
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].sources[1].kind = "other"
			},
		},
		{
			name: "orphan application declaration",
			want: "an application Alias declaration is absent from final Alias evidence",
			mutate: func(aliases *[]CapabilityAlias, _ *[]Module, _ *[]CapabilityRequirement, _ *[]PluginCandidate, _ *[]GenerationActivation) {
				(*aliases)[0].sources = (*aliases)[0].sources[1:]
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			aliases, modules, requirements, plugins, activations := capabilityAliasEvidenceFixture()
			test.mutate(&aliases, &modules, &requirements, &plugins, &activations)
			err := validateCapabilityAliases(aliases, modules, requirements, plugins, activations)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateCapabilityAliases = %v; want %q", err, test.want)
			}
		})
	}
}

func TestCapabilityAliasEvidenceAcceptsCompleteProvenance(t *testing.T) {
	t.Parallel()

	aliases, modules, requirements, plugins, activations := capabilityAliasEvidenceFixture()
	if err := validateCapabilityAliases(aliases, modules, requirements, plugins, activations); err != nil {
		t.Fatalf("validateCapabilityAliases: %v", err)
	}
}

func capabilityAliasEvidenceFixture() ([]CapabilityAlias, []Module, []CapabilityRequirement, []PluginCandidate, []GenerationActivation) {
	contractDigest := testDigest("a")
	applicationRequirementSource := RequirementSource{
		kind:          providerresolution.RequirementAliasTarget,
		projectModule: "example.com/app",
		source:        Source{module: "example.com/app", path: "plystra.yaml", kind: "alias-target", line: 7, column: 5},
		alias:         "orders.submit/v1",
	}
	pluginSource := Source{module: "corp.example/authn", path: "authn/plugin.yaml", kind: "plugin-declaration", line: 1, column: 1}
	aliases := []CapabilityAlias{{
		id:                   "orders.submit/v1",
		target:               "order.create/v1",
		targetContractDigest: contractDigest,
		sources: []CapabilityAliasSource{
			{kind: generation.AliasSourceApplication, projectModule: "example.com/app", source: applicationRequirementSource.source},
			{kind: generation.AliasSourceGenerationExtension, projectModule: "example.com/authn", pluginID: "example.authn", contributionID: "authn.order-submit", namespace: "authn", sourceCapability: "order.create/v1", activationCapability: "authn.session.verify/v1", source: Source{module: pluginSource.module, path: pluginSource.path, kind: "generation-alias-contribution", line: pluginSource.line, column: pluginSource.column}},
		},
	}}
	modules := []Module{
		{path: "example.com/app", source: Source{module: "example.com/app"}},
		{path: "example.com/authn", source: Source{module: "corp.example/authn"}},
	}
	requirements := []CapabilityRequirement{{capability: "order.create/v1", contractDigest: contractDigest, sources: []RequirementSource{applicationRequirementSource}}}
	plugins := []PluginCandidate{{id: "example.authn", modulePath: "example.com/authn", source: pluginSource}}
	activations := []GenerationActivation{{namespace: "authn", sourceCapability: "order.create/v1", activationCapability: "authn.session.verify/v1", pluginID: "example.authn", projectModule: "example.com/authn"}}
	return aliases, modules, requirements, plugins, activations
}

func testDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
