package generationresolution_test

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/generationactivation"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/pluginmeta"
	"github.com/plystra/cli/internal/providerresolution"
)

func TestResolveBuildsStableActivationClosureAndExcludesUnselectedExtensions(t *testing.T) {
	t.Parallel()

	order := generationContract("order.cancel/v1", `extensions:
  authz: {permission: order.cancel}
  authn: {authenticated: true}
`)
	verify := generationContract("authn.session.verify/v1", "")
	check := generationContract("authz.check/v1", "")
	catalog := activationCatalog(t,
		activationDeclaration(t, "example.security", []activationBinding{{"authz", "authz.check/v1"}, {"authn", "authn.session.verify/v1"}}),
		activationDeclaration(t, "example.authn-passkey", []activationBinding{{"authn", "authn.session.verify/v1"}}),
		activationDeclaration(t, "example.audit", []activationBinding{{"audit", "audit.write/v1"}}),
	)
	input := generationresolution.Input{
		Requirements: []providerresolution.Requirement{{Contract: order, Source: "plystra.yaml http.expose.order"}},
		Candidates: []providerresolution.Candidate{
			{PluginID: "example.orders", Contract: order, Source: "orders/order.cancel"},
			{PluginID: "example.authn-passkey", Contract: verify, Source: "passkey/authn.session.verify"},
			{PluginID: "example.security", Contract: verify, Source: "security/authn.session.verify"},
			{PluginID: "example.security", Contract: check, Source: "security/authz.check"},
		},
		Choices:     []providerresolution.Choice{{Capability: "authn.session.verify/v1", PluginID: "example.security", Source: "plystra.yaml capabilities.use.authn.session.verify/v1"}},
		Activations: catalog,
	}
	before := cloneInput(input)
	result, err := generationresolution.Resolve(input)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Passes() != 2 {
		t.Fatalf("Passes = %d, want 2", result.Passes())
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatal("Resolve mutated input")
	}
	resolved := result.ProviderResolution()
	if got := resolvedCapabilityIDs(resolved); !slices.Equal(got, []string{"authn.session.verify/v1", "authz.check/v1", "order.cancel/v1"}) {
		t.Fatalf("resolved Capabilities = %v", got)
	}
	if got := selectedProviderStrings(resolved); !slices.Equal(got, []string{
		"authn.session.verify/v1=example.security",
		"authz.check/v1=example.security",
		"order.cancel/v1=example.orders",
	}) {
		t.Fatalf("selected providers = %v", got)
	}
	authn, exists := resolved.Capability(mustGenerationID(t, "authn.session.verify/v1"))
	if !exists || !slices.Equal(authn.Sources(), []string{"extensions.authn on order.cancel/v1"}) {
		t.Fatalf("authn requirement = %#v, %t", authn, exists)
	}

	activationRequirements := result.ActivationRequirements().Requirements()
	if got := activationRequirementValues(activationRequirements); !slices.Equal(got, []string{
		"authn.session.verify/v1=authn:order.cancel/v1",
		"authz.check/v1=authz:order.cancel/v1",
	}) {
		t.Fatalf("activation requirements = %v", got)
	}
	extensions := result.Extensions()
	if len(extensions) != 1 || extensions[0].PluginID() != "example.security" || extensions[0].API() != "v1" || extensions[0].Package() != "./generation" || extensions[0].Source() != "example.security/plugin.yaml" || !slices.Equal(extensions[0].Namespaces(), []string{"authn", "authz"}) {
		t.Fatalf("Extensions = %#v", extensions)
	}
	activations := extensions[0].Activations()
	if len(activations) != 2 || activations[0].Capability().String() != "authn.session.verify/v1" || activations[1].Capability().String() != "authz.check/v1" || activations[0].Uses()[0].SourceCapability().String() != "order.cancel/v1" {
		t.Fatalf("selected Activations = %#v", activations)
	}
	for _, excluded := range []string{"example.authn-passkey", "example.audit"} {
		if strings.Contains(strings.Join(selectedExtensionStrings(extensions), ","), excluded) {
			t.Fatalf("unselected extension %q entered closure", excluded)
		}
	}

	extensions[0] = generationresolution.SelectedExtension{}
	activations[0] = generationresolution.SelectedActivation{}
	uses := result.Extensions()[0].Activations()[0].Uses()
	uses[0] = generationactivation.NamespaceUse{}
	if result.Extensions()[0].PluginID() != "example.security" || result.Extensions()[0].Activations()[0].Namespace() != "authn" || result.Extensions()[0].Activations()[0].Uses()[0].SourceCapability().String() != "order.cancel/v1" {
		t.Fatal("Result exposed mutable extension storage")
	}
}

func TestResolveExpandsTransitiveActivationRequirementsToStability(t *testing.T) {
	t.Parallel()

	order := generationContract("order.cancel/v1", "extensions: {authn: {authenticated: true}}\n")
	verify := generationContract("authn.session.verify/v1", "extensions: {audit: {event: authn.session.verified}}\n")
	audit := generationContract("audit.write/v1", "")
	result, err := generationresolution.Resolve(generationresolution.Input{
		Requirements: []providerresolution.Requirement{{Contract: order, Source: "order route"}},
		Candidates: []providerresolution.Candidate{
			{PluginID: "example.orders", Contract: order, Source: "orders/order.cancel"},
			{PluginID: "example.authn", Contract: verify, Source: "authn/session.verify"},
			{PluginID: "example.audit", Contract: audit, Source: "audit/write"},
		},
		Activations: activationCatalog(t,
			activationDeclaration(t, "example.authn", []activationBinding{{"authn", "authn.session.verify/v1"}}),
			activationDeclaration(t, "example.audit", []activationBinding{{"audit", "audit.write/v1"}}),
		),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if result.Passes() != 3 {
		t.Fatalf("Passes = %d, want 3", result.Passes())
	}
	if got := resolvedCapabilityIDs(result.ProviderResolution()); !slices.Equal(got, []string{"audit.write/v1", "authn.session.verify/v1", "order.cancel/v1"}) {
		t.Fatalf("resolved Capabilities = %v", got)
	}
	if got := activationRequirementValues(result.ActivationRequirements().Requirements()); !slices.Equal(got, []string{
		"audit.write/v1=audit:authn.session.verify/v1",
		"authn.session.verify/v1=authn:order.cancel/v1",
	}) {
		t.Fatalf("activation requirements = %v", got)
	}
	if got := selectedExtensionStrings(result.Extensions()); !slices.Equal(got, []string{
		"example.audit|audit",
		"example.authn|authn",
	}) {
		t.Fatalf("Extensions = %v", got)
	}
}

func TestResolveReportsActivationProviderFailures(t *testing.T) {
	t.Parallel()

	order := generationContract("order.cancel/v1", "extensions: {authn: {authenticated: true}}\n")
	verify := generationContract("authn.session.verify/v1", "")
	root := providerresolution.Requirement{Contract: order, Source: "order route"}
	orderProvider := providerresolution.Candidate{PluginID: "example.orders", Contract: order, Source: "orders/order.cancel"}

	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		_, err := generationresolution.Resolve(generationresolution.Input{
			Requirements: []providerresolution.Requirement{root},
			Candidates:   []providerresolution.Candidate{orderProvider},
			Activations: activationCatalog(t,
				activationDeclaration(t, "example.authn", []activationBinding{{"authn", "authn.session.verify/v1"}}),
			),
		})
		if !errors.Is(err, generationresolution.ErrResolve) || !errors.Is(err, providerresolution.ErrMissingProvider) {
			t.Fatalf("Resolve error = %v", err)
		}
		for _, detail := range []string{"pass 2", "authn.session.verify/v1", "extensions.authn on order.cancel/v1"} {
			if !strings.Contains(err.Error(), detail) {
				t.Fatalf("missing-provider error omits %q: %v", detail, err)
			}
		}
	})

	t.Run("ambiguous", func(t *testing.T) {
		t.Parallel()
		_, err := generationresolution.Resolve(generationresolution.Input{
			Requirements: []providerresolution.Requirement{root},
			Candidates: []providerresolution.Candidate{
				orderProvider,
				{PluginID: "example.authn-password", Contract: verify, Source: "password/session.verify"},
				{PluginID: "example.authn-passkey", Contract: verify, Source: "passkey/session.verify"},
			},
			Activations: activationCatalog(t,
				activationDeclaration(t, "example.authn-password", []activationBinding{{"authn", "authn.session.verify/v1"}}),
				activationDeclaration(t, "example.authn-passkey", []activationBinding{{"authn", "authn.session.verify/v1"}}),
			),
		})
		if !errors.Is(err, providerresolution.ErrAmbiguousProvider) || !strings.Contains(err.Error(), "capabilities.use[authn.session.verify/v1]") {
			t.Fatalf("Resolve error = %v", err)
		}
	})

	t.Run("selected provider has no extension", func(t *testing.T) {
		t.Parallel()
		_, err := generationresolution.Resolve(generationresolution.Input{
			Requirements: []providerresolution.Requirement{root},
			Candidates: []providerresolution.Candidate{
				orderProvider,
				{PluginID: "example.authn-password", Contract: verify, Source: "password/session.verify"},
				{PluginID: "example.authn-legacy", Contract: verify, Source: "legacy/session.verify"},
			},
			Choices: []providerresolution.Choice{{Capability: "authn.session.verify/v1", PluginID: "example.authn-legacy", Source: "plystra.yaml capabilities.use.authn"}},
			Activations: activationCatalog(t,
				activationDeclaration(t, "example.authn-password", []activationBinding{{"authn", "authn.session.verify/v1"}}),
			),
		})
		if !errors.Is(err, generationresolution.ErrResolve) || !errors.Is(err, generationresolution.ErrSelectExtension) || !errors.Is(err, generationactivation.ErrSelectedProviderExtension) {
			t.Fatalf("Resolve error = %v", err)
		}
		var closure *generationresolution.ClosureError
		if !errors.As(err, &closure) || len(closure.Issues()) != 1 {
			t.Fatalf("ClosureError = %#v", closure)
		}
		issues := closure.Issues()
		issues[0] = nil
		if closure.Issues()[0] == nil {
			t.Fatal("ClosureError exposed mutable issues")
		}
		for _, detail := range []string{"example.authn-legacy", "example.authn-password", "authn.session.verify/v1"} {
			if !strings.Contains(err.Error(), detail) {
				t.Fatalf("extension selection error omits %q: %v", detail, err)
			}
		}
	})
}

func TestResolveRejectsCompleteActivationCycles(t *testing.T) {
	t.Parallel()

	t.Run("two capabilities", func(t *testing.T) {
		t.Parallel()
		alpha := generationContract("alpha.call/v1", "extensions: {authn: {authenticated: true}}\n")
		verify := generationContract("authn.session.verify/v1", "extensions: {audit: {event: authn.verify}}\n")
		_, err := generationresolution.Resolve(generationresolution.Input{
			Requirements: []providerresolution.Requirement{{Contract: alpha, Source: "alpha route"}},
			Candidates: []providerresolution.Candidate{
				{PluginID: "example.alpha", Contract: alpha, Source: "alpha/call"},
				{PluginID: "example.authn", Contract: verify, Source: "authn/verify"},
			},
			Activations: activationCatalog(t,
				activationDeclaration(t, "example.authn", []activationBinding{{"authn", "authn.session.verify/v1"}}),
				activationDeclaration(t, "example.alpha", []activationBinding{{"audit", "alpha.call/v1"}}),
			),
		})
		if !errors.Is(err, generationresolution.ErrResolve) || !errors.Is(err, generationresolution.ErrActivationCycle) {
			t.Fatalf("Resolve error = %v", err)
		}
		var cycle *generationresolution.ActivationCycleError
		if !errors.As(err, &cycle) {
			t.Fatalf("ActivationCycleError = %T", err)
		}
		edges := cycle.Edges()
		if len(edges) != 2 || edges[0].Source().String() != "alpha.call/v1" || edges[0].Target().String() != "authn.session.verify/v1" || edges[0].Namespace() != "authn" || edges[1].Source().String() != "authn.session.verify/v1" || edges[1].Target().String() != "alpha.call/v1" || edges[1].Namespace() != "audit" {
			t.Fatalf("cycle Edges = %#v", edges)
		}
		if !slices.Equal(edges[0].RequirementSources(), []string{"alpha route"}) || !slices.Equal(edges[1].RequirementSources(), []string{"extensions.authn on alpha.call/v1"}) {
			t.Fatalf("cycle provenance = %#v", edges)
		}
		edges[0] = generationresolution.ActivationEdge{}
		if cycle.Edges()[0].Source().String() != "alpha.call/v1" {
			t.Fatal("ActivationCycleError exposed mutable edges")
		}
		for _, detail := range []string{"alpha.call/v1", "extensions.authn", "authn.session.verify/v1", "extensions.audit", "activation order cannot break semantic cycles"} {
			if !strings.Contains(err.Error(), detail) {
				t.Fatalf("cycle error omits %q: %v", detail, err)
			}
		}
	})

	t.Run("self activation", func(t *testing.T) {
		t.Parallel()
		self := generationContract("authn.session.verify/v1", "extensions: {authn: {authenticated: true}}\n")
		_, err := generationresolution.Resolve(generationresolution.Input{
			Requirements: []providerresolution.Requirement{{Contract: self, Source: "self root"}},
			Candidates:   []providerresolution.Candidate{{PluginID: "example.authn", Contract: self, Source: "authn/verify"}},
			Activations: activationCatalog(t,
				activationDeclaration(t, "example.authn", []activationBinding{{"authn", "authn.session.verify/v1"}}),
			),
		})
		var cycle *generationresolution.ActivationCycleError
		if !errors.As(err, &cycle) || len(cycle.Edges()) != 1 || cycle.Edges()[0].Source() != cycle.Edges()[0].Target() {
			t.Fatalf("self-cycle error = %v, cycle %#v", err, cycle)
		}
	})
}

func TestResolveSupportsApplicationsWithoutGenerationExtensions(t *testing.T) {
	t.Parallel()

	plain := generationContract("order.read/v1", "")
	result, err := generationresolution.Resolve(generationresolution.Input{
		Requirements: []providerresolution.Requirement{{Contract: plain, Source: "order client"}},
		Candidates:   []providerresolution.Candidate{{PluginID: "example.orders", Contract: plain, Source: "orders/read"}},
	})
	if err != nil || result.Passes() != 1 || len(result.ActivationRequirements().Requirements()) != 0 || len(result.Extensions()) != 0 {
		t.Fatalf("Resolve(plain) = %#v, %v", result, err)
	}
	empty, err := generationresolution.Resolve(generationresolution.Input{})
	if err != nil || empty.Passes() != 1 || len(empty.ProviderResolution().Capabilities()) != 0 || len(empty.Extensions()) != 0 {
		t.Fatalf("Resolve(empty) = %#v, %v", empty, err)
	}
}

func TestResolveBoundsGeneratedActivationProvenance(t *testing.T) {
	t.Parallel()

	longID := strings.Repeat("a", 1100) + ".call/v1"
	root := generationContract(longID, "extensions: {authn: {authenticated: true}}\n")
	verify := generationContract("authn.session.verify/v1", "")
	result, err := generationresolution.Resolve(generationresolution.Input{
		Requirements: []providerresolution.Requirement{{Contract: root, Source: "long root"}},
		Candidates: []providerresolution.Candidate{
			{PluginID: "example.long", Contract: root, Source: "long/call"},
			{PluginID: "example.authn", Contract: verify, Source: "authn/verify"},
		},
		Activations: activationCatalog(t,
			activationDeclaration(t, "example.authn", []activationBinding{{"authn", "authn.session.verify/v1"}}),
		),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	authn, exists := result.ProviderResolution().Capability(mustGenerationID(t, "authn.session.verify/v1"))
	if !exists || len(authn.Sources()) != 1 || len(authn.Sources()[0]) != 1024 || !strings.Contains(authn.Sources()[0], "...#sha256:") {
		t.Fatalf("bounded activation source = %q", authn.Sources())
	}
}

func TestResolveIsStableAcrossEveryInputOrder(t *testing.T) {
	t.Parallel()

	alpha := generationContract("alpha.call/v1", "extensions: {authn: {authenticated: true}, authz: {permission: alpha.call}}\n")
	verify := generationContract("authn.session.verify/v1", "")
	check := generationContract("authz.check/v1", "")
	requirements := []providerresolution.Requirement{{Contract: alpha, Source: "alpha root"}}
	candidates := []providerresolution.Candidate{
		{PluginID: "example.alpha", Contract: alpha, Source: "alpha/call"},
		{PluginID: "example.authn", Contract: verify, Source: "authn/verify"},
		{PluginID: "example.authz", Contract: check, Source: "authz/check"},
	}
	declarations := []generationactivation.Declaration{
		activationDeclaration(t, "example.authz", []activationBinding{{"authz", "authz.check/v1"}}),
		activationDeclaration(t, "example.authn", []activationBinding{{"authn", "authn.session.verify/v1"}}),
	}
	first, err := generationresolution.Resolve(generationresolution.Input{
		Requirements: requirements,
		Candidates:   candidates,
		Activations:  activationCatalog(t, declarations...),
	})
	if err != nil {
		t.Fatalf("Resolve(first): %v", err)
	}
	slices.Reverse(requirements)
	slices.Reverse(candidates)
	slices.Reverse(declarations)
	second, err := generationresolution.Resolve(generationresolution.Input{
		Requirements: requirements,
		Candidates:   candidates,
		Activations:  activationCatalog(t, declarations...),
	})
	if err != nil {
		t.Fatalf("Resolve(second): %v", err)
	}
	if got, want := renderGenerationResult(first), renderGenerationResult(second); !slices.Equal(got, want) {
		t.Fatalf("order-dependent closure: %v != %v", got, want)
	}
}

type activationBinding struct {
	namespace  string
	capability string
}

func activationDeclaration(t *testing.T, pluginID string, bindings []activationBinding) generationactivation.Declaration {
	t.Helper()
	var manifest strings.Builder
	manifest.WriteString("id: " + pluginID + "\nprovides:\n")
	seen := make(map[string]struct{})
	for _, binding := range bindings {
		if _, exists := seen[binding.capability]; exists {
			continue
		}
		seen[binding.capability] = struct{}{}
		manifest.WriteString("  - " + binding.capability + "\n")
	}
	manifest.WriteString("generation:\n  api: v1\n  package: ./generation\n  activations:\n")
	for _, binding := range bindings {
		manifest.WriteString("    - namespace: " + binding.namespace + "\n      capability: " + binding.capability + "\n")
	}
	metadata, err := pluginmeta.Parse([]byte(manifest.String()))
	if err != nil {
		t.Fatalf("pluginmeta.Parse(%s): %v\n%s", pluginID, err, manifest.String())
	}
	generation, exists := metadata.Generation()
	if !exists {
		t.Fatalf("plugin %s has no generation declaration", pluginID)
	}
	return generationactivation.Declaration{PluginID: pluginID, Source: pluginID + "/plugin.yaml", Generation: generation}
}

func activationCatalog(t *testing.T, declarations ...generationactivation.Declaration) generationactivation.Catalog {
	t.Helper()
	catalog, err := generationactivation.New(declarations)
	if err != nil {
		t.Fatalf("generationactivation.New: %v", err)
	}
	return catalog
}

func generationContract(id, body string) []byte {
	return []byte("id: " + id + "\n" + generationQuerySemanticsYAML + body)
}

const generationQuerySemanticsYAML = `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`

func mustGenerationID(t *testing.T, value string) capabilityid.Identifier {
	t.Helper()
	id, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("capabilityid.Parse(%q): %v", value, err)
	}
	return id
}

func cloneInput(input generationresolution.Input) generationresolution.Input {
	clone := input
	clone.Requirements = make([]providerresolution.Requirement, len(input.Requirements))
	for index, requirement := range input.Requirements {
		clone.Requirements[index] = requirement
		clone.Requirements[index].Contract = append([]byte(nil), requirement.Contract...)
	}
	clone.Candidates = make([]providerresolution.Candidate, len(input.Candidates))
	for index, candidate := range input.Candidates {
		clone.Candidates[index] = candidate
		clone.Candidates[index].Contract = append([]byte(nil), candidate.Contract...)
	}
	clone.Choices = append([]providerresolution.Choice(nil), input.Choices...)
	return clone
}

func resolvedCapabilityIDs(result providerresolution.Result) []string {
	capabilities := result.Capabilities()
	values := make([]string, len(capabilities))
	for index, capability := range capabilities {
		values[index] = capability.ID().String()
	}
	return values
}

func selectedProviderStrings(result providerresolution.Result) []string {
	selections := result.Selections()
	values := make([]string, len(selections))
	for index, selection := range selections {
		values[index] = selection.Capability().String() + "=" + selection.PluginID()
	}
	return values
}

func activationRequirementValues(requirements []generationactivation.ActivationRequirement) []string {
	values := make([]string, len(requirements))
	for index, requirement := range requirements {
		uses := requirement.Uses()
		parts := make([]string, len(uses))
		for useIndex, use := range uses {
			parts[useIndex] = use.Namespace() + ":" + use.SourceCapability().String()
		}
		values[index] = requirement.Capability().String() + "=" + strings.Join(parts, ",")
	}
	return values
}

func selectedExtensionStrings(extensions []generationresolution.SelectedExtension) []string {
	values := make([]string, len(extensions))
	for index, extension := range extensions {
		values[index] = extension.PluginID() + "|" + strings.Join(extension.Namespaces(), ",")
	}
	return values
}

func renderGenerationResult(result generationresolution.Result) []string {
	values := []string{"passes=" + strconv.Itoa(result.Passes())}
	values = append(values, resolvedCapabilityIDs(result.ProviderResolution())...)
	values = append(values, selectedProviderStrings(result.ProviderResolution())...)
	values = append(values, activationRequirementValues(result.ActivationRequirements().Requirements())...)
	values = append(values, selectedExtensionStrings(result.Extensions())...)
	return values
}
