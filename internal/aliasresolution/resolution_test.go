package aliasresolution_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/generationresolution"
)

var _ aliasresolution.ExtensionOutputView = generationresolution.ExtensionOutput{}

func TestResolveMergesCompatibleAliasesWithCompleteProvenance(t *testing.T) {
	t.Parallel()

	context := aliasContext(t)
	order := aliasCapabilityID(t, "order.create/v1")
	health := aliasCapabilityID(t, "kernel.health/v1")
	goHTTP := generation.Exposure{Go: true, HTTP: true}
	authn := normalizeAliasOutput(t, context, "example.authn", []generation.CapabilityAliasContribution{
		{ID: "authn.health-status", Namespace: "authn", Source: order, Alias: aliasCapabilityID(t, "health.status/v1"), Target: health},
		{ID: "authn.order-start", Namespace: "authn", Source: order, Alias: aliasCapabilityID(t, "orders.start/v1"), Target: order},
		{ID: "authn.order-submit", Namespace: "authn", Source: order, Alias: aliasCapabilityID(t, "orders.submit/v1"), Target: order, Exposure: &goHTTP, Deprecated: "Compatibility only."},
		{ID: "authn.order-submit-compatibility", Namespace: "authn", Source: order, Alias: aliasCapabilityID(t, "orders.submit/v1"), Target: order, Exposure: &goHTTP, Deprecated: "Compatibility only."},
	})
	authz := normalizeAliasOutput(t, context, "example.authz", []generation.CapabilityAliasContribution{
		{ID: "authz.order-place", Namespace: "authz", Source: order, Alias: aliasCapabilityID(t, "orders.place/v1"), Target: order},
		{ID: "authz.order-submit", Namespace: "authz", Source: order, Alias: aliasCapabilityID(t, "orders.submit/v1"), Target: order, Exposure: &goHTTP, Deprecated: "Compatibility only."},
	})

	outputs := []fakeExtensionOutput{authz, authn}
	result, err := aliasresolution.Resolve(context, outputs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	aliases := result.Aliases()
	if got := aliasIDs(aliases); !slices.Equal(got, []string{
		"health.status/v1",
		"orders.create/v1",
		"orders.place/v1",
		"orders.start/v1",
		"orders.submit/v1",
	}) {
		t.Fatalf("Alias IDs = %v", got)
	}
	for _, id := range []string{"orders.create/v1", "orders.place/v1", "orders.start/v1", "orders.submit/v1"} {
		alias := aliasByID(t, aliases, id)
		if alias.Target() != order {
			t.Fatalf("%s target = %s", id, alias.Target())
		}
	}
	resolvedHealth := aliasByID(t, aliases, "health.status/v1")
	if resolvedHealth.Target() != health || resolvedHealth.Exposure() != (generation.Exposure{Go: true, HTTP: true}) {
		t.Fatalf("health Alias = %#v", resolvedHealth)
	}
	if _, narrowed := resolvedHealth.ExposureNarrowing(); narrowed {
		t.Fatal("health Alias unexpectedly narrowed inherited exposure")
	}
	submit := aliasByID(t, aliases, "orders.submit/v1")
	if submit.Exposure() != goHTTP || submit.Deprecated() != "Compatibility only." {
		t.Fatalf("submit Alias metadata = %#v", submit)
	}
	if narrowing, narrowed := submit.ExposureNarrowing(); !narrowed || narrowing != goHTTP {
		t.Fatalf("submit narrowing = %#v, %v", narrowing, narrowed)
	}
	orderView, _ := context.Capability(order)
	if submit.TargetContractDigest() != orderView.ContractDigest() {
		t.Fatalf("target digest = %q, want %q", submit.TargetContractDigest(), orderView.ContractDigest())
	}
	sources := submit.Sources()
	if got := sourceStrings(sources); !slices.Equal(got, []string{
		"application:application:::",
		"generation-extension:example.authn:authn.order-submit:authn:order.create/v1",
		"generation-extension:example.authn:authn.order-submit-compatibility:authn:order.create/v1",
		"generation-extension:example.authz:authz.order-submit:authz:order.create/v1",
	}) {
		t.Fatalf("submit sources = %v", got)
	}
	canonical := result.CanonicalJSON()
	for _, want := range []string{`"target_contract_digest":"sha256:`, `"contribution_id":"authn.order-submit"`, `"contribution_id":"authz.order-submit"`, `"kind":"application"`, `"kind":"generation-extension"`} {
		if !bytes.Contains(canonical, []byte(want)) {
			t.Fatalf("canonical Alias map %s omits %s", canonical, want)
		}
	}
	if !strings.HasPrefix(result.Digest(), "sha256:") || len(result.Digest()) != 71 {
		t.Fatalf("Digest = %q", result.Digest())
	}

	slices.Reverse(outputs)
	second, err := aliasresolution.Resolve(context, outputs)
	if err != nil || !bytes.Equal(second.CanonicalJSON(), canonical) || second.Digest() != result.Digest() {
		t.Fatalf("reordered outputs changed result = %s, %q, %v", second.CanonicalJSON(), second.Digest(), err)
	}

	input := aliasContextInput()
	input.CapabilityAliases = result.Inputs()
	roundTrip, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext(final Alias inputs): %v", err)
	}
	if len(roundTrip.CapabilityAliases()) != len(aliases) {
		t.Fatalf("round-trip Aliases = %#v", roundTrip.CapabilityAliases())
	}
	inputAliases := result.Inputs()
	resolvedSubmit := inputAliasByID(t, inputAliases, "orders.submit/v1")
	if got := inputSourceStrings(resolvedSubmit.Sources); !slices.Equal(got, []string{
		"application:application",
		"generation-extension:example.authn",
		"generation-extension:example.authz",
	}) {
		t.Fatalf("normalized input sources = %v", got)
	}

	aliases[0] = aliasresolution.Alias{}
	sources[0] = aliasresolution.Source{}
	inputAliases[0] = generation.CapabilityAliasInput{}
	canonical[0] = 'x'
	if fresh := result.Aliases(); fresh[0].ID().String() != "health.status/v1" || fresh[4].Sources()[0].Kind() != generation.AliasSourceApplication {
		t.Fatal("Result exposed mutable Alias or source storage")
	}
	if result.Inputs()[0].ID != "health.status/v1" || result.CanonicalJSON()[0] != '{' {
		t.Fatal("Result exposed mutable input or canonical storage")
	}
}

func TestResolveAutomaticAliasDisappearsWhileExplicitAliasesRemainStable(t *testing.T) {
	t.Parallel()

	context := aliasContext(t)
	withoutExtensions, err := aliasresolution.Resolve(context, []fakeExtensionOutput{})
	if err != nil {
		t.Fatalf("Resolve without extensions: %v", err)
	}
	if got := aliasIDs(withoutExtensions.Aliases()); !slices.Equal(got, []string{"orders.create/v1", "orders.submit/v1"}) {
		t.Fatalf("base Alias IDs = %v", got)
	}
	order := aliasCapabilityID(t, "order.create/v1")
	withAutomatic, err := aliasresolution.Resolve(context, []fakeExtensionOutput{
		normalizeAliasOutput(t, context, "example.authn", []generation.CapabilityAliasContribution{{
			ID: "authn.order-start", Namespace: "authn", Source: order, Alias: aliasCapabilityID(t, "orders.start/v1"), Target: order,
		}}),
	})
	if err != nil {
		t.Fatalf("Resolve with automatic Alias: %v", err)
	}
	if got := aliasIDs(withAutomatic.Aliases()); !slices.Equal(got, []string{"orders.create/v1", "orders.start/v1", "orders.submit/v1"}) {
		t.Fatalf("expanded Alias IDs = %v", got)
	}
	for _, result := range []aliasresolution.Result{withoutExtensions, withAutomatic} {
		if aliasByID(t, result.Aliases(), "orders.submit/v1").Target() != order || aliasByID(t, result.Aliases(), "orders.create/v1").Target() != order {
			t.Fatal("explicit Alias retargeted while automatic candidates changed")
		}
	}
}

func TestResolveRejectsConflictingAliasesDeterministically(t *testing.T) {
	t.Parallel()

	context := aliasContext(t)
	order := aliasCapabilityID(t, "order.create/v1")
	health := aliasCapabilityID(t, "kernel.health/v1")
	alias := aliasCapabilityID(t, "orders.choice/v1")
	goOnly := generation.Exposure{Go: true}

	tests := []struct {
		name  string
		left  generation.CapabilityAliasContribution
		right generation.CapabilityAliasContribution
		want  []string
	}{
		{
			name:  "different target",
			left:  generation.CapabilityAliasContribution{ID: "authn.choice", Namespace: "authn", Source: order, Alias: alias, Target: order},
			right: generation.CapabilityAliasContribution{ID: "authz.choice", Namespace: "authz", Source: order, Alias: alias, Target: health},
			want:  []string{"order.create/v1", "kernel.health/v1"},
		},
		{
			name:  "different exposure",
			left:  generation.CapabilityAliasContribution{ID: "authn.choice", Namespace: "authn", Source: order, Alias: alias, Target: order},
			right: generation.CapabilityAliasContribution{ID: "authz.choice", Namespace: "authz", Source: order, Alias: alias, Target: order, Exposure: &goOnly},
			want:  []string{"exposure(go=true,http=true,javascript=true)", "exposure(go=true,http=false,javascript=false)"},
		},
		{
			name:  "different deprecation",
			left:  generation.CapabilityAliasContribution{ID: "authn.choice", Namespace: "authn", Source: order, Alias: alias, Target: order, Deprecated: "Use A."},
			right: generation.CapabilityAliasContribution{ID: "authz.choice", Namespace: "authz", Source: order, Alias: alias, Target: order, Deprecated: "Use B."},
			want:  []string{`deprecated="Use A."`, `deprecated="Use B."`},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			outputs := []fakeExtensionOutput{
				normalizeAliasOutput(t, context, "example.authn", []generation.CapabilityAliasContribution{test.left}),
				normalizeAliasOutput(t, context, "example.authz", []generation.CapabilityAliasContribution{test.right}),
			}
			var first string
			for iteration := 0; iteration < 2; iteration++ {
				result, err := aliasresolution.Resolve(context, outputs)
				if !errors.Is(err, aliasresolution.ErrResolve) || !errors.Is(err, aliasresolution.ErrConflict) {
					t.Fatalf("Resolve error = %v, want ErrResolve and ErrConflict", err)
				}
				if len(result.Aliases()) != 0 || len(result.CanonicalJSON()) != 0 || result.Digest() != "" {
					t.Fatalf("conflict returned partial result %#v", result)
				}
				for _, want := range append([]string{"orders.choice/v1", "example.authn", "authn.choice", "example.authz", "authz.choice", "no source priority"}, test.want...) {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("Resolve error %q omits %q", err, want)
					}
				}
				if first == "" {
					first = err.Error()
				} else if err.Error() != first {
					t.Fatalf("conflict diagnostic changed: %q then %q", first, err)
				}
				slices.Reverse(outputs)
			}
		})
	}
}

func TestResolveRejectsInvalidContextAndExtensionProvenance(t *testing.T) {
	t.Parallel()

	if _, err := aliasresolution.Resolve(generation.Context{}, []fakeExtensionOutput{}); !errors.Is(err, aliasresolution.ErrResolve) || !errors.Is(err, aliasresolution.ErrInvalidContext) {
		t.Fatalf("zero context error = %v", err)
	}
	context := aliasContext(t)
	order := aliasCapabilityID(t, "order.create/v1")
	valid := normalizeAliasOutput(t, context, "example.authn", []generation.CapabilityAliasContribution{{
		ID: "authn.start", Namespace: "authn", Source: order, Alias: aliasCapabilityID(t, "orders.start/v1"), Target: order,
	}})
	if _, err := aliasresolution.Resolve(context, []fakeExtensionOutput{valid, valid}); !errors.Is(err, aliasresolution.ErrInvalidExtensionOutput) || !strings.Contains(err.Error(), "more than one final output") {
		t.Fatalf("duplicate output error = %v", err)
	}
	valid.pluginID = "example.missing"
	if _, err := aliasresolution.Resolve(context, []fakeExtensionOutput{valid}); !errors.Is(err, aliasresolution.ErrInvalidExtensionOutput) || !strings.Contains(err.Error(), "not selected") {
		t.Fatalf("unselected output error = %v", err)
	}
	valid.pluginID = "Invalid_Plugin"
	if _, err := aliasresolution.Resolve(context, []fakeExtensionOutput{valid}); !errors.Is(err, aliasresolution.ErrInvalidExtensionOutput) || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("invalid plugin output error = %v", err)
	}
}

func TestResolveEmptyAliasMapIsCanonical(t *testing.T) {
	t.Parallel()

	input := aliasContextInput()
	input.CapabilityAliases = nil
	context, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	result, err := aliasresolution.Resolve(context, []fakeExtensionOutput{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if string(result.CanonicalJSON()) != `{"capability_aliases":[]}` || len(result.Aliases()) != 0 || len(result.Inputs()) != 0 || !strings.HasPrefix(result.Digest(), "sha256:") {
		t.Fatalf("empty result = %s, %q", result.CanonicalJSON(), result.Digest())
	}
}

type fakeExtensionOutput struct {
	pluginID string
	output   generation.NormalizedOutput
}

func (o fakeExtensionOutput) PluginID() string                    { return o.pluginID }
func (o fakeExtensionOutput) Output() generation.NormalizedOutput { return o.output }

func normalizeAliasOutput(t *testing.T, context generation.Context, pluginID string, aliases []generation.CapabilityAliasContribution) fakeExtensionOutput {
	t.Helper()
	output, err := generation.NormalizeOutput(context, generation.Output{AliasContributions: aliases})
	if err != nil {
		t.Fatalf("NormalizeOutput(%s): %v", pluginID, err)
	}
	return fakeExtensionOutput{pluginID: pluginID, output: output}
}

func aliasContext(t *testing.T) generation.Context {
	t.Helper()
	context, err := generation.NewContext(aliasContextInput())
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	return context
}

func aliasContextInput() generation.Input {
	return generation.Input{
		Plugins: []generation.PluginInput{
			{ID: "example.business", ModulePath: "example.com/app", Provides: []string{"order.create/v1"}},
			{ID: "example.authn", ModulePath: "example.com/authn", Provides: []string{"authn.session.verify/v1"}},
			{ID: "example.authz", ModulePath: "example.com/authz", Provides: []string{"authz.check/v1"}},
		},
		Capabilities: []generation.CapabilityInput{
			{ContractJSON: json.RawMessage(`{"id":"order.create/v1","request":{},"response":{},"errors":[],"semantics":` + aliasQuerySemanticsJSON + `,"extensions":{"authn":{"authenticated":true},"authz":{"permission":"order.create","space":"request.space_id"}}}`), Exposure: generation.Exposure{Go: true, HTTP: true, JavaScript: true}},
			{ContractJSON: aliasContract("authn.session.verify/v1"), Exposure: generation.Exposure{Go: true}},
			{ContractJSON: aliasContract("authz.check/v1"), Exposure: generation.Exposure{Go: true}},
			{ContractJSON: aliasContract("kernel.health/v1"), Intrinsic: true, Exposure: generation.Exposure{Go: true, HTTP: true}},
		},
		Requirements: []string{"order.create/v1", "authn.session.verify/v1", "authz.check/v1", "kernel.health/v1"},
		Providers: []generation.ProviderInput{
			{Capability: "order.create/v1", Plugin: "example.business"},
			{Capability: "authn.session.verify/v1", Plugin: "example.authn"},
			{Capability: "authz.check/v1", Plugin: "example.authz"},
		},
		CapabilityAliases: []generation.CapabilityAliasInput{
			{ID: "orders.create/v1", Target: "order.create/v1", Exposure: generation.Exposure{Go: true, HTTP: true, JavaScript: true}, Sources: []generation.AliasSourceInput{{Kind: generation.AliasSourceApplication, ID: "application"}}},
			{ID: "orders.submit/v1", Target: "order.create/v1", Exposure: generation.Exposure{Go: true, HTTP: true}, Deprecated: "Compatibility only.", Sources: []generation.AliasSourceInput{{Kind: generation.AliasSourceApplication, ID: "application"}}},
		},
	}
}

func aliasContract(id string) json.RawMessage {
	return json.RawMessage(`{"id":"` + id + `","request":{},"response":{},"errors":[],"semantics":` + aliasQuerySemanticsJSON + `}`)
}

const aliasQuerySemanticsJSON = `{"kind":"query","effects":"none","idempotency":{"mode":"inherent"},"retry":{"safety":"safe"},"cancellation":{"mode":"best-effort"},"completion":{"mode":"completed-before-return"},"ordering":{"mode":"none"},"data":{"request":"public","response":"public"}}`

func aliasCapabilityID(t *testing.T, value string) generation.CapabilityID {
	t.Helper()
	id, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%q): %v", value, err)
	}
	return id
}

func aliasIDs(values []aliasresolution.Alias) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID().String()
	}
	return result
}

func aliasByID(t *testing.T, values []aliasresolution.Alias, id string) aliasresolution.Alias {
	t.Helper()
	for _, value := range values {
		if value.ID().String() == id {
			return value
		}
	}
	t.Fatalf("Alias %s not found in %v", id, aliasIDs(values))
	return aliasresolution.Alias{}
}

func inputAliasByID(t *testing.T, values []generation.CapabilityAliasInput, id string) generation.CapabilityAliasInput {
	t.Helper()
	for _, value := range values {
		if value.ID == id {
			return value
		}
	}
	t.Fatalf("Alias input %s not found", id)
	return generation.CapabilityAliasInput{}
}

func sourceStrings(values []aliasresolution.Source) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = strings.Join([]string{string(value.Kind()), value.ID(), value.ContributionID(), value.Namespace(), value.SourceCapability().String()}, ":")
	}
	return result
}

func inputSourceStrings(values []generation.AliasSourceInput) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value.Kind) + ":" + value.ID
	}
	return result
}
