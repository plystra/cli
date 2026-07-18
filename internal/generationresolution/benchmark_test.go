package generationresolution

import (
	"testing"

	generation "github.com/plystra/cli/generation/v1"
)

func BenchmarkGenerationFixedPoint(b *testing.B) {
	order := extensionTestContract(b, "order.create/v1", "extensions:\n  authn: {authenticated: true}\n")
	verify := extensionTestContract(b, "authn.session.verify/v1", "")
	audit := extensionTestContract(b, "audit.write/v1", "")
	input := extensionTestInput(b, order, verify, audit)
	source := extensionTestCapabilityID(b, "order.create/v1")
	requirement := extensionTestCapabilityID(b, "audit.write/v1")
	output := func(_ int, _ generation.Context) (generation.Output, error) {
		return generation.Output{Requirements: []generation.Requirement{{
			RuleID:     "authn.require-audit",
			Namespace:  "authn",
			Source:     source,
			Capability: requirement,
		}}}, nil
	}
	resolve := func() ExtensionResult {
		builder := newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
			"example.authn": {output: output},
		})
		result, err := resolveExtensions(b.Context(), input, builder.Build)
		if err != nil {
			b.Fatalf("resolveExtensions: %v", err)
		}
		return result
	}
	if result := resolve(); result.Passes() != 3 || len(result.GeneratedRequirements()) != 1 {
		b.Fatalf("benchmark fixture = passes %d, generated requirements %d", result.Passes(), len(result.GeneratedRequirements()))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if result := resolve(); result.Passes() != 3 {
			b.Fatalf("Passes = %d, want 3", result.Passes())
		}
	}
}
