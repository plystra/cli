package generationresolution

import (
	"errors"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
)

func TestResolveContributionGraphUsesPointAndDependencyOrder(t *testing.T) {
	generationContext := contributionTestContext(t)
	verifyContribution := contributionTestValue(t,
		"z-verify", "authn", generation.GenerationPointInvocationPrepare, nil, []generation.ContributionToken{"verified-authn-context"},
	)
	verifyContribution.Nodes = []generation.GeneratedNode{{
		ID: "attach-verification",
		MetadataAttachment: &generation.GeneratedMetadataAttachment{
			Key: "authn.verification", Value: generation.StringValue("verified"), MaximumBytes: 32,
		},
	}}
	outputs := []ExtensionOutput{
		contributionTestOutput(t, generationContext, "example.z-ingress", contributionTestValue(t,
			"z-ingress", "authn", generation.GenerationPointHTTPIngress, nil, []generation.ContributionToken{"request-traced"},
		)),
		contributionTestOutput(t, generationContext, "example.z-authn", verifyContribution),
		contributionTestOutput(t, generationContext, "example.a-authz", contributionTestValue(t,
			"a-authorize", "authz", generation.GenerationPointInvocationPrepare, []generation.ContributionToken{"verified-authn-context"}, []generation.ContributionToken{"authorization-approved"},
		)),
		contributionTestOutput(t, generationContext, "example.m-audit", contributionTestValue(t,
			"m-record", "audit", generation.GenerationPointInvocationComplete, []generation.ContributionToken{"authorization-approved"}, []generation.ContributionToken{"outcome-recorded"},
		)),
		contributionTestOutput(t, generationContext, "example.a-egress", contributionTestValue(t,
			"a-egress", "audit", generation.GenerationPointHTTPEgress, []generation.ContributionToken{"authorization-approved", "outcome-recorded", "request-traced"}, nil,
		)),
	}
	wantIDs := []string{"z-ingress", "z-verify", "a-authorize", "m-record", "a-egress"}
	wantPlugins := []string{"example.z-ingress", "example.z-authn", "example.a-authz", "example.m-audit", "example.a-egress"}
	plugins := contributionTestPlugins(outputs)

	permutations := 0
	forEachExtensionOutputPermutation(outputs, func(permutation []ExtensionOutput) {
		permutations++
		resolved, err := resolveContributionGraph(permutation, plugins)
		if err != nil {
			t.Fatalf("resolveContributionGraph permutation %d: %v", permutations, err)
		}
		if got := resolvedContributionIDs(resolved); !slices.Equal(got, wantIDs) {
			t.Fatalf("permutation %d contribution IDs = %v, want %v", permutations, got, wantIDs)
		}
		if got := resolvedContributionPluginIDs(resolved); !slices.Equal(got, wantPlugins) {
			t.Fatalf("permutation %d plugin IDs = %v, want %v", permutations, got, wantPlugins)
		}
		nodes := resolved[1].Nodes()
		if len(nodes) != 1 || nodes[0].ID() != "attach-verification" || nodes[0].Kind() != generation.GeneratedNodeKindMetadataAttachment {
			t.Fatalf("permutation %d resolved nodes = %#v", permutations, nodes)
		}
		attachment, ok := nodes[0].MetadataAttachment()
		if !ok || attachment.Key != "authn.verification" || attachment.Value.Literal == nil || attachment.Value.Literal.String == nil || *attachment.Value.Literal.String != "verified" {
			t.Fatalf("permutation %d resolved attachment = %#v, %v", permutations, attachment, ok)
		}
		nodes[0] = generation.NormalizedGeneratedNode{}
		*attachment.Value.Literal.String = "changed"
		fresh, _ := resolved[1].Nodes()[0].MetadataAttachment()
		if fresh.Key != "authn.verification" || *fresh.Value.Literal.String != "verified" {
			t.Fatalf("permutation %d resolved nodes exposed mutable storage", permutations)
		}
	})
	if permutations != 120 {
		t.Fatalf("permutations = %d, want 120", permutations)
	}
}

func TestResolveContributionGraphRejectsInvalidGraphsDeterministically(t *testing.T) {
	generationContext := contributionTestContext(t)
	tests := []struct {
		name    string
		outputs []ExtensionOutput
		target  error
		details []string
	}{
		{
			name: "duplicate global ID",
			outputs: []ExtensionOutput{
				contributionTestOutput(t, generationContext, "example.authn", contributionTestValue(t, "shared.prepare", "authn", generation.GenerationPointInvocationPrepare, nil, nil)),
				contributionTestOutput(t, generationContext, "example.authz", contributionTestValue(t, "shared.prepare", "authz", generation.GenerationPointInvocationPrepare, nil, nil)),
			},
			target:  ErrDuplicateContributionID,
			details: []string{"shared.prepare", "example.authn", "example.authz", "global"},
		},
		{
			name: "missing token provider",
			outputs: []ExtensionOutput{
				contributionTestOutput(t, generationContext, "example.authz", contributionTestValue(t, "authz.authorize", "authz", generation.GenerationPointInvocationPrepare, []generation.ContributionToken{"verified-authn-context"}, nil)),
			},
			target:  ErrContributionTokenProvider,
			details: []string{"authz.authorize", "example.authz", "verified-authn-context", "no selected contribution"},
		},
		{
			name: "multiple token providers",
			outputs: []ExtensionOutput{
				contributionTestOutput(t, generationContext, "example.authn", contributionTestValue(t, "authn.verify", "authn", generation.GenerationPointInvocationPrepare, nil, []generation.ContributionToken{"verified-authn-context"})),
				contributionTestOutput(t, generationContext, "example.authz", contributionTestValue(t, "authz.verify", "authz", generation.GenerationPointInvocationPrepare, nil, []generation.ContributionToken{"verified-authn-context"})),
			},
			target:  ErrContributionTokenProvider,
			details: []string{"verified-authn-context", "authn.verify", "authz.verify", "exactly one provider"},
		},
		{
			name: "backward point dependency",
			outputs: []ExtensionOutput{
				contributionTestOutput(t, generationContext, "example.audit", contributionTestValue(t, "audit.complete", "audit", generation.GenerationPointInvocationComplete, nil, []generation.ContributionToken{"outcome-recorded"})),
				contributionTestOutput(t, generationContext, "example.authz", contributionTestValue(t, "authz.prepare", "authz", generation.GenerationPointInvocationPrepare, []generation.ContributionToken{"outcome-recorded"}, nil)),
			},
			target:  ErrContributionPointDependency,
			details: []string{"authz.prepare", "audit.complete", "outcome-recorded", "later", "invocation.prepare", "invocation.complete"},
		},
		{
			name: "HTTP-only dependency at common invocation point",
			outputs: []ExtensionOutput{
				contributionTestOutput(t, generationContext, "example.authn", contributionTestValue(t, "authn.ingress", "authn", generation.GenerationPointHTTPIngress, nil, []generation.ContributionToken{"credentials-read"})),
				contributionTestOutput(t, generationContext, "example.authn", contributionTestValue(t, "authn.prepare", "authn", generation.GenerationPointInvocationPrepare, []generation.ContributionToken{"credentials-read"}, nil)),
			},
			target:  ErrContributionPointDependency,
			details: []string{"authn.ingress", "authn.prepare", "credentials-read", "HTTP-only", "internal calls"},
		},
		{
			name: "unordered work at ordered point",
			outputs: []ExtensionOutput{
				contributionTestOutput(t, generationContext, "example.authn", contributionTestValue(t, "authn.prepare", "authn", generation.GenerationPointInvocationPrepare, nil, nil)),
				contributionTestOutput(t, generationContext, "example.authz", contributionTestValue(t, "authz.prepare", "authz", generation.GenerationPointInvocationPrepare, nil, nil)),
			},
			target:  ErrUnorderedContributions,
			details: []string{"authn.prepare", "authz.prepare", "invocation.prepare", "simultaneously ready", "requires/provides"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plugins := contributionTestPlugins(test.outputs)
			_, firstErr := resolveContributionGraph(test.outputs, plugins)
			if !errors.Is(firstErr, ErrContributionGraph) || !errors.Is(firstErr, test.target) {
				t.Fatalf("error = %v, want ErrContributionGraph and %v", firstErr, test.target)
			}
			for _, detail := range test.details {
				if !strings.Contains(firstErr.Error(), detail) {
					t.Fatalf("error omits %q: %v", detail, firstErr)
				}
			}

			reversed := append([]ExtensionOutput(nil), test.outputs...)
			slices.Reverse(reversed)
			_, reversedErr := resolveContributionGraph(reversed, plugins)
			if reversedErr == nil || reversedErr.Error() != firstErr.Error() {
				t.Fatalf("reversed error = %v, want %v", reversedErr, firstErr)
			}
		})
	}
}

func TestResolveContributionGraphReportsCompleteCycle(t *testing.T) {
	generationContext := contributionTestContext(t)
	outputs := []ExtensionOutput{
		contributionTestOutput(t, generationContext, "example.authn", contributionTestValue(t,
			"authn.a", "authn", generation.GenerationPointInvocationPrepare, []generation.ContributionToken{"token-c"}, []generation.ContributionToken{"token-a"},
		)),
		contributionTestOutput(t, generationContext, "example.authz", contributionTestValue(t,
			"authz.b", "authz", generation.GenerationPointInvocationPrepare, []generation.ContributionToken{"token-a"}, []generation.ContributionToken{"token-b"},
		)),
		contributionTestOutput(t, generationContext, "example.audit", contributionTestValue(t,
			"audit.c", "audit", generation.GenerationPointInvocationPrepare, []generation.ContributionToken{"token-b"}, []generation.ContributionToken{"token-c"},
		)),
	}

	plugins := contributionTestPlugins(outputs)
	_, err := resolveContributionGraph(outputs, plugins)
	if !errors.Is(err, ErrContributionGraph) || !errors.Is(err, ErrContributionCycle) {
		t.Fatalf("cycle error = %v", err)
	}
	var cycle *ContributionCycleError
	if !errors.As(err, &cycle) {
		t.Fatalf("cycle error type = %T", err)
	}
	dependencies := cycle.Dependencies()
	if got := contributionDependencyStrings(dependencies); !slices.Equal(got, []string{
		"audit.c --token-c--> authn.a",
		"authn.a --token-a--> authz.b",
		"authz.b --token-b--> audit.c",
	}) {
		t.Fatalf("cycle dependencies = %v", got)
	}
	for _, detail := range []string{"audit.c", "authn.a", "authz.b", "token-a", "token-b", "token-c", "example.authn", "example.authz", "example.audit", "correction:"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("cycle error omits %q: %v", detail, err)
		}
	}
	dependencies[0] = ContributionDependency{}
	if cycle.Dependencies()[0].Provider().ID() != "audit.c" {
		t.Fatal("ContributionCycleError exposed mutable dependency storage")
	}
	providerSources := cycle.Dependencies()[0].Provider().RequirementSourceDetails()
	if len(providerSources) != 1 || providerSources[0].Kind != "generation-rule" || providerSources[0].ModulePath != "example.com/application" || providerSources[0].Path != "audit/plugin.yaml" || providerSources[0].Line != 1 || providerSources[0].Column != 1 || providerSources[0].PluginID != "example.audit" || providerSources[0].Namespace != "audit" || providerSources[0].SourceCapability != "order.create/v1" || providerSources[0].RuleID != "audit.c" {
		t.Fatalf("cycle provider sources = %#v", providerSources)
	}
	consumerSources := cycle.Dependencies()[0].Consumer().RequirementSourceDetails()
	if len(consumerSources) != 1 || consumerSources[0].Path != "authn/plugin.yaml" || consumerSources[0].PluginID != "example.authn" || consumerSources[0].RuleID != "authn.a" {
		t.Fatalf("cycle consumer sources = %#v", consumerSources)
	}
	providerSources[0] = consumerSources[0]
	if fresh := cycle.Dependencies()[0].Provider().RequirementSourceDetails(); len(fresh) != 1 || fresh[0].Path != "audit/plugin.yaml" {
		t.Fatal("ResolvedContribution exposed mutable requirement-source storage")
	}

	reversed := append([]ExtensionOutput(nil), outputs...)
	slices.Reverse(reversed)
	_, reversedErr := resolveContributionGraph(reversed, plugins)
	if reversedErr == nil || reversedErr.Error() != err.Error() {
		t.Fatalf("reversed cycle error = %v, want %v", reversedErr, err)
	}
}

func TestResolveContributionGraphReportsUnorderedContributions(t *testing.T) {
	generationContext := contributionTestContext(t)
	outputs := []ExtensionOutput{
		contributionTestOutput(t, generationContext, "example.authn",
			contributionTestValue(t, "authn.verify", "authn", generation.GenerationPointInvocationPrepare, nil, nil),
			contributionTestValue(t, "authn.attach", "authn", generation.GenerationPointInvocationPrepare, nil, nil),
		),
		contributionTestOutput(t, generationContext, "example.audit",
			contributionTestValue(t, "audit.record", "audit", generation.GenerationPointInvocationPrepare, nil, nil),
		),
	}

	plugins := contributionTestPlugins(outputs)
	_, err := resolveContributionGraph(outputs, plugins)
	if !errors.Is(err, ErrContributionGraph) || !errors.Is(err, ErrUnorderedContributions) {
		t.Fatalf("unordered error = %v", err)
	}
	var unordered *UnorderedContributionsError
	if !errors.As(err, &unordered) {
		t.Fatalf("unordered error type = %T", err)
	}
	if unordered.Point() != generation.GenerationPointInvocationPrepare {
		t.Fatalf("unordered point = %q", unordered.Point())
	}
	contributions := unordered.Contributions()
	if got := resolvedContributionIDs(contributions); !slices.Equal(got, []string{"audit.record", "authn.attach", "authn.verify"}) {
		t.Fatalf("unordered contributions = %v", got)
	}
	wantSources := []struct {
		path     string
		pluginID string
		ruleID   string
	}{
		{path: "audit/plugin.yaml", pluginID: "example.audit", ruleID: "audit.record"},
		{path: "authn/plugin.yaml", pluginID: "example.authn", ruleID: "authn.attach"},
		{path: "authn/plugin.yaml", pluginID: "example.authn", ruleID: "authn.verify"},
	}
	for index, contribution := range contributions {
		sources := contribution.RequirementSourceDetails()
		want := wantSources[index]
		if len(sources) != 1 || sources[0].Kind != "generation-rule" || sources[0].ModulePath != "example.com/application" || sources[0].Path != want.path || sources[0].Line != 1 || sources[0].Column != 1 || sources[0].PluginID != want.pluginID || sources[0].RuleID != want.ruleID {
			t.Fatalf("unordered contribution %d sources = %#v", index, sources)
		}
	}
	firstSources := contributions[0].RequirementSourceDetails()
	firstSources[0] = contributions[1].RequirementSourceDetails()[0]
	contributions[0] = ResolvedContribution{}
	if fresh := unordered.Contributions(); fresh[0].ID() != "audit.record" || fresh[0].RequirementSourceDetails()[0].Path != "audit/plugin.yaml" {
		t.Fatal("UnorderedContributionsError exposed mutable contribution storage")
	}

	reversed := append([]ExtensionOutput(nil), outputs...)
	slices.Reverse(reversed)
	_, reversedErr := resolveContributionGraph(reversed, plugins)
	if reversedErr == nil || reversedErr.Error() != err.Error() {
		t.Fatalf("reversed unordered error = %v, want %v", reversedErr, err)
	}
}

func TestResolveExtensionsRejectsInvalidFinalContributionGraph(t *testing.T) {
	order := extensionTestContract(t, "order.create/v1", "extensions:\n  authn: {authenticated: true}\n")
	verify := extensionTestContract(t, "authn.session.verify/v1", "")
	audit := extensionTestContract(t, "audit.write/v1", "")
	input := extensionTestInput(t, order, verify, audit)
	builder := newFakeExtensionBuilder(map[string]*fakeExtensionHelper{
		"example.authn": {
			output: func(_ int, _ generation.Context) (generation.Output, error) {
				return generation.Output{Contributions: []generation.Contribution{contributionTestValue(t,
					"authn.verify", "authn", generation.GenerationPointInvocationPrepare, []generation.ContributionToken{"missing-context"}, nil,
				)}}, nil
			},
		},
	})

	result, err := resolveExtensions(t.Context(), input, builder.Build)
	if result.Passes() != 0 || !errors.Is(err, ErrResolveExtensions) || !errors.Is(err, ErrContributionGraph) || !errors.Is(err, ErrContributionTokenProvider) {
		t.Fatalf("invalid graph result = %#v, %v", result, err)
	}
	for _, detail := range []string{"pass 2", "authn.verify", "example.authn", "missing-context"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("invalid graph error omits %q: %v", detail, err)
		}
	}
	if !builder.helpers["example.authn"].closed {
		t.Fatal("invalid final graph did not close the selected helper")
	}
}

func contributionTestContext(t *testing.T) generation.Context {
	t.Helper()
	contract := extensionTestContract(t, "order.create/v1", "extensions:\n  audit: {record: true}\n  authn: {authenticated: true}\n  authz: {permission: order.create}\n")
	context, err := generation.NewContext(generation.Input{
		Plugins: []generation.PluginInput{{
			ID:                "example.business",
			ModulePath:        "example.com/application",
			Provides:          []string{"order.create/v1"},
			BuildMetadataJSON: []byte("{}"),
		}},
		Capabilities: []generation.CapabilityInput{{ContractJSON: contract}},
		Requirements: []string{"order.create/v1"},
		Providers: []generation.ProviderInput{{
			Capability: "order.create/v1",
			Plugin:     "example.business",
		}},
	})
	if err != nil {
		t.Fatalf("generation.NewContext: %v", err)
	}
	return context
}

func contributionTestOutput(t *testing.T, context generation.Context, pluginID string, contributions ...generation.Contribution) ExtensionOutput {
	t.Helper()
	output, err := generation.NormalizeOutput(context, generation.Output{Contributions: contributions})
	if err != nil {
		t.Fatalf("generation.NormalizeOutput(%s): %v", pluginID, err)
	}
	return ExtensionOutput{
		pluginID:    pluginID,
		api:         generation.Version,
		packagePath: "./generation",
		output:      output,
	}
}

func contributionTestValue(t *testing.T, id, namespace string, point generation.GenerationPoint, requires, provides []generation.ContributionToken) generation.Contribution {
	t.Helper()
	return generation.Contribution{
		ID:        id,
		Namespace: namespace,
		Source:    extensionTestCapabilityID(t, "order.create/v1"),
		Point:     point,
		Requires:  append([]generation.ContributionToken(nil), requires...),
		Provides:  append([]generation.ContributionToken(nil), provides...),
	}
}

func contributionTestPlugins(outputs []ExtensionOutput) map[string]Plugin {
	plugins := make(map[string]Plugin)
	for _, output := range outputs {
		if _, exists := plugins[output.pluginID]; exists {
			continue
		}
		pluginPath := output.pluginID
		if separator := strings.LastIndex(pluginPath, "."); separator >= 0 {
			pluginPath = pluginPath[separator+1:]
		}
		plugins[output.pluginID] = Plugin{
			Context: generation.PluginInput{
				ID:         output.pluginID,
				ModulePath: "example.com/application",
			},
			PluginPath: pluginPath,
		}
	}
	return plugins
}

func forEachExtensionOutputPermutation(values []ExtensionOutput, visit func([]ExtensionOutput)) {
	working := append([]ExtensionOutput(nil), values...)
	var permute func(int)
	permute = func(index int) {
		if index == len(working) {
			visit(append([]ExtensionOutput(nil), working...))
			return
		}
		for candidate := index; candidate < len(working); candidate++ {
			working[index], working[candidate] = working[candidate], working[index]
			permute(index + 1)
			working[index], working[candidate] = working[candidate], working[index]
		}
	}
	permute(0)
}

func resolvedContributionIDs(values []ResolvedContribution) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID()
	}
	return result
}

func resolvedContributionPluginIDs(values []ResolvedContribution) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.PluginID()
	}
	return result
}

func contributionDependencyStrings(values []ContributionDependency) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Provider().ID() + " --" + value.Token().String() + "--> " + value.Consumer().ID()
	}
	return result
}
