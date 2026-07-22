package resolutionevidence

import (
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/providerresolution"
)

func TestPublicExposureEvidenceRejectsInconsistentFinalProvenance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		want   string
		mutate func(*[]PublicExposure, *[]CapabilityRequirement, *[]CapabilityAlias)
	}{
		{
			name: "records unordered",
			want: `not in unique canonical order at "order.create/v1"`,
			mutate: func(exposures *[]PublicExposure, _ *[]CapabilityRequirement, _ *[]CapabilityAlias) {
				(*exposures)[0], (*exposures)[1] = (*exposures)[1], (*exposures)[0]
			},
		},
		{
			name: "kind invalid",
			want: `.kind "other" is invalid`,
			mutate: func(exposures *[]PublicExposure, _ *[]CapabilityRequirement, _ *[]CapabilityAlias) {
				(*exposures)[0].kind = "other"
			},
		},
		{
			name: "canonical target differs",
			want: "canonical identity is inconsistent",
			mutate: func(exposures *[]PublicExposure, _ *[]CapabilityRequirement, _ *[]CapabilityAlias) {
				(*exposures)[0].canonicalTarget = "profile.get/v1"
			},
		},
		{
			name: "contract digest differs",
			want: "canonical identity is inconsistent",
			mutate: func(exposures *[]PublicExposure, _ *[]CapabilityRequirement, _ *[]CapabilityAlias) {
				(*exposures)[0].contractDigest = testDigest("b")
			},
		},
		{
			name: "no public surface",
			want: "has no HTTP or JavaScript surface",
			mutate: func(exposures *[]PublicExposure, _ *[]CapabilityRequirement, _ *[]CapabilityAlias) {
				(*exposures)[0].exposure = generation.Exposure{Go: true}
			},
		},
		{
			name: "sources absent",
			want: ".sources must not be empty",
			mutate: func(exposures *[]PublicExposure, _ *[]CapabilityRequirement, _ *[]CapabilityAlias) {
				(*exposures)[0].sources = nil
			},
		},
		{
			name: "sources duplicated",
			want: "sources are not in unique canonical order",
			mutate: func(exposures *[]PublicExposure, _ *[]CapabilityRequirement, _ *[]CapabilityAlias) {
				(*exposures)[1].sources = append((*exposures)[1].sources, (*exposures)[1].sources[1])
			},
		},
		{
			name: "canonical source differs",
			want: "sources do not match final provenance",
			mutate: func(exposures *[]PublicExposure, _ *[]CapabilityRequirement, _ *[]CapabilityAlias) {
				(*exposures)[0].sources[0].projectModule = "example.com/authn"
			},
		},
		{
			name: "Alias exposure differs",
			want: "Alias identity is inconsistent",
			mutate: func(exposures *[]PublicExposure, _ *[]CapabilityRequirement, _ *[]CapabilityAlias) {
				(*exposures)[1].exposure = generation.Exposure{HTTP: true}
			},
		},
		{
			name: "Alias target exposure differs",
			want: "target exposure evidence is inconsistent",
			mutate: func(_ *[]PublicExposure, _ *[]CapabilityRequirement, aliases *[]CapabilityAlias) {
				(*aliases)[0].targetExposure = generation.Exposure{HTTP: true, JavaScript: true}
			},
		},
		{
			name: "canonical record absent",
			want: "http.expose provenance is absent from public exposure evidence",
			mutate: func(exposures *[]PublicExposure, _ *[]CapabilityRequirement, _ *[]CapabilityAlias) {
				*exposures = (*exposures)[1:]
			},
		},
		{
			name: "Alias record absent",
			want: "public Alias orders.submit/v1 is absent from public exposure evidence",
			mutate: func(exposures *[]PublicExposure, _ *[]CapabilityRequirement, _ *[]CapabilityAlias) {
				*exposures = (*exposures)[:1]
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exposures, requirements, aliases := publicExposureEvidenceFixture()
			test.mutate(&exposures, &requirements, &aliases)
			err := validatePublicExposures(exposures, requirements, aliases)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validatePublicExposures = %v; want %q", err, test.want)
			}
		})
	}
}

func TestPublicExposureEvidenceAcceptsCanonicalAndAliasProvenance(t *testing.T) {
	t.Parallel()

	exposures, requirements, aliases := publicExposureEvidenceFixture()
	if err := validatePublicExposures(exposures, requirements, aliases); err != nil {
		t.Fatalf("validatePublicExposures: %v", err)
	}
}

func publicExposureEvidenceFixture() ([]PublicExposure, []CapabilityRequirement, []CapabilityAlias) {
	aliases, _, requirements, _, _ := capabilityAliasEvidenceFixture()
	exposureSource := RequirementSource{
		kind:          providerresolution.RequirementExposure,
		projectModule: "example.com/app",
		source:        Source{module: "example.com/app", path: "plystra.yaml", kind: "exposure", line: 3, column: 5},
	}
	requirements[0].sources = append(requirements[0].sources, exposureSource)
	exposures := []PublicExposure{
		{
			capability:      "order.create/v1",
			kind:            PublicExposureCanonical,
			canonicalTarget: "order.create/v1",
			contractDigest:  requirements[0].contractDigest,
			exposure:        generation.Exposure{Go: true, HTTP: true, JavaScript: true},
			sources:         publicExposureSourcesFromRequirement(requirements[0]),
		},
		{
			capability:      "orders.submit/v1",
			kind:            PublicExposureAlias,
			canonicalTarget: "order.create/v1",
			contractDigest:  requirements[0].contractDigest,
			exposure:        aliases[0].exposure,
			sources:         publicExposureSourcesFromAlias(aliases[0]),
		},
	}
	return exposures, requirements, aliases
}
