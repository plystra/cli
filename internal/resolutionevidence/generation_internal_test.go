package resolutionevidence

import (
	"strings"
	"testing"

	"github.com/plystra/cli/internal/providerresolution"
)

func TestGenerationEvidenceRejectsInconsistentFinalProvenance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		want   string
		mutate func(*[]CapabilityRequirement, *[]SelectedProvider, *[]PluginCandidate)
	}{
		{
			name: "generated rule without matching activation",
			want: "has no matching selected activation",
			mutate: func(requirements *[]CapabilityRequirement, _ *[]SelectedProvider, _ *[]PluginCandidate) {
				(*requirements)[1].sources = nil
			},
		},
		{
			name: "non-required originating Capability",
			want: "names non-required source Capability order.create/v1",
			mutate: func(requirements *[]CapabilityRequirement, _ *[]SelectedProvider, _ *[]PluginCandidate) {
				*requirements = (*requirements)[:2]
			},
		},
		{
			name: "rule Plugin differs from selected activation Plugin",
			want: "differs from selected activation Plugin \"example.authn\"",
			mutate: func(requirements *[]CapabilityRequirement, _ *[]SelectedProvider, _ *[]PluginCandidate) {
				source := &(*requirements)[0].sources[0]
				source.pluginID = "example.other"
				source.projectModule = "example.com/other"
				source.source.module = "example.com/other"
				source.source.path = "other/plugin.yaml"
			},
		},
		{
			name: "invalid activation namespace",
			want: "activation for authn.session.verify/v1 has invalid namespace \"AuthN\"",
			mutate: func(requirements *[]CapabilityRequirement, _ *[]SelectedProvider, _ *[]PluginCandidate) {
				(*requirements)[1].sources[0].namespace = "AuthN"
			},
		},
		{
			name: "invalid generated namespace",
			want: "generated requirement audit.write/v1 has invalid namespace \"AuthN\"",
			mutate: func(requirements *[]CapabilityRequirement, _ *[]SelectedProvider, _ *[]PluginCandidate) {
				(*requirements)[0].sources[0].namespace = "AuthN"
			},
		},
		{
			name: "invalid generated rule ID",
			want: "generated requirement audit.write/v1 has invalid rule ID \"AuthN.rule\"",
			mutate: func(requirements *[]CapabilityRequirement, _ *[]SelectedProvider, _ *[]PluginCandidate) {
				(*requirements)[0].sources[0].ruleID = "AuthN.rule"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requirements, providers, plugins := generationEvidenceFixture()
			test.mutate(&requirements, &providers, &plugins)
			activations, generated, err := generationEvidenceFromRequirements(requirements, providers, plugins)
			if err == nil || !strings.Contains(err.Error(), test.want) || activations != nil || generated != nil {
				t.Fatalf("generationEvidenceFromRequirements = %#v, %#v, %v; want %q", activations, generated, err, test.want)
			}
		})
	}
}

func TestGenerationEvidenceCanonicalizesDuplicateSources(t *testing.T) {
	t.Parallel()

	requirements, providers, plugins := generationEvidenceFixture()
	activation := requirements[1].sources[0]
	secondCause := activation
	secondCause.source.line = 9
	requirements[1].sources = []RequirementSource{secondCause, activation, activation}
	rule := requirements[0].sources[0]
	requirements[0].sources = []RequirementSource{rule, rule}

	activations, generated, err := generationEvidenceFromRequirements(requirements, providers, plugins)
	if err != nil {
		t.Fatalf("generationEvidenceFromRequirements: %v", err)
	}
	if len(activations) != 1 || len(activations[0].causes) != 2 || activations[0].causes[0].source.line != 4 || activations[0].causes[1].source.line != 9 {
		t.Fatalf("canonical activations = %#v", activations)
	}
	if len(generated) != 1 || generated[0].ruleID != "authn.require-audit" {
		t.Fatalf("canonical generated requirements = %#v", generated)
	}
}

func generationEvidenceFixture() ([]CapabilityRequirement, []SelectedProvider, []PluginCandidate) {
	activation := RequirementSource{
		kind:             providerresolution.RequirementActivation,
		projectModule:    "example.com/app",
		source:           Source{module: "example.com/app", path: "plystra.yaml", kind: "activation", line: 4, column: 3},
		namespace:        "authn",
		sourceCapability: "order.create/v1",
	}
	rule := RequirementSource{
		kind:             providerresolution.RequirementGenerationRule,
		projectModule:    "example.com/authn",
		source:           Source{module: "example.com/authn", path: "authn/plugin.yaml", kind: "generation-rule", line: 1, column: 1},
		pluginID:         "example.authn",
		namespace:        "authn",
		sourceCapability: "order.create/v1",
		ruleID:           "authn.require-audit",
	}
	return []CapabilityRequirement{
			{capability: "audit.write/v1", sources: []RequirementSource{rule}},
			{capability: "authn.session.verify/v1", sources: []RequirementSource{activation}},
			{capability: "order.create/v1"},
		}, []SelectedProvider{
			{capability: "authn.session.verify/v1", pluginID: "example.authn", projectModule: "example.com/authn"},
		}, []PluginCandidate{
			{id: "example.authn", modulePath: "example.com/authn"},
			{id: "example.other", modulePath: "example.com/other"},
		}
}
