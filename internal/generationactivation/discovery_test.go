package generationactivation_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/generationactivation"
	"github.com/plystra/cli/internal/providerresolution"
)

func TestDiscoverRequirementsMapsEveryNamespaceUseToOneActivationCapability(t *testing.T) {
	t.Parallel()

	order := exactContract("order.cancel/v1", `extensions:
  authz: {permission: order.cancel}
  authn: {authenticated: true}
`)
	invoice := exactContract("invoice.create/v1", "extensions: {authn: {authenticated: true}}\n")
	resolution := resolved(t,
		[]providerresolution.Requirement{
			{Contract: order, Source: "orders/plugin.yaml requires"},
			{Contract: invoice, Source: "plystra.yaml http.expose.invoice"},
			{Contract: order, Source: "plystra.yaml http.expose.order"},
		},
		[]providerresolution.Candidate{
			{PluginID: "example.orders", Contract: order, Source: "orders/capability.yaml"},
			{PluginID: "example.billing", Contract: invoice, Source: "billing/capability.yaml"},
		},
	)
	catalog, err := generationactivation.New([]generationactivation.Declaration{
		declaration(t, "example.authz", "authz.check/v1", "authz", "authz/plugin.yaml"),
		declaration(t, "example.authn-password", "authn.session.verify/v1", "authn", "password/plugin.yaml"),
		declaration(t, "example.audit", "audit.write/v1", "audit", "audit/plugin.yaml"),
		declaration(t, "example.authn-passkey", "authn.session.verify/v1", "authn", "passkey/plugin.yaml"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	set, err := catalog.DiscoverRequirements(resolution)
	if err != nil {
		t.Fatalf("DiscoverRequirements: %v", err)
	}
	requirements := set.Requirements()
	if got := activationRequirementStrings(requirements); !slices.Equal(got, []string{
		"authn.session.verify/v1=authn:invoice.create/v1,authn:order.cancel/v1",
		"authz.check/v1=authz:order.cancel/v1",
	}) {
		t.Fatalf("Requirements = %v", got)
	}
	authn, exists := set.Requirement(mustCapabilityID(t, "authn.session.verify/v1"))
	if !exists {
		t.Fatal("authn activation requirement is absent")
	}
	uses := authn.Uses()
	if len(uses) != 2 || uses[0].Namespace() != "authn" || uses[0].SourceCapability().String() != "invoice.create/v1" || !slices.Equal(uses[1].RequirementSources(), []string{"orders/plugin.yaml requires", "plystra.yaml http.expose.order"}) {
		t.Fatalf("authn Uses = %#v", uses)
	}
	if _, exists := set.Requirement(mustCapabilityID(t, "audit.write/v1")); exists {
		t.Fatal("unused visible activation became a requirement")
	}

	requirements[0] = generationactivation.ActivationRequirement{}
	uses[0] = generationactivation.NamespaceUse{}
	sources := authn.Uses()[1].RequirementSources()
	sources[0] = "changed"
	if set.Requirements()[0].Capability().String() != "authn.session.verify/v1" || authn.Uses()[0].SourceCapability().String() != "invoice.create/v1" || authn.Uses()[1].RequirementSources()[0] != "orders/plugin.yaml requires" {
		t.Fatal("activation requirements exposed mutable storage")
	}
}

func TestDiscoverRequirementsReportsEveryMissingNamespaceWithProvenance(t *testing.T) {
	t.Parallel()

	order := exactContract("order.cancel/v1", `extensions:
  idempotency: {key: request.request_id}
  authn: {authenticated: true}
  audit: {event: order.cancelled}
`)
	resolution := resolved(t,
		[]providerresolution.Requirement{{Contract: order, Source: "plystra.yaml http.expose.order"}},
		[]providerresolution.Candidate{{PluginID: "example.orders", Contract: order, Source: "orders/capability.yaml"}},
	)
	catalog, err := generationactivation.New([]generationactivation.Declaration{
		declaration(t, "example.authn", "authn.session.verify/v1", "authn", "authn/plugin.yaml"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	set, err := catalog.DiscoverRequirements(resolution)
	if !errors.Is(err, generationactivation.ErrDiscoverRequirements) || !errors.Is(err, generationactivation.ErrMissingAssociation) {
		t.Fatalf("DiscoverRequirements error = %v", err)
	}
	if len(set.Requirements()) != 0 {
		t.Fatalf("failed discovery returned partial set %#v", set.Requirements())
	}
	var discovery *generationactivation.DiscoveryError
	if !errors.As(err, &discovery) || len(discovery.Issues()) != 2 {
		t.Fatalf("DiscoveryError = %#v", discovery)
	}
	issues := discovery.Issues()
	issues[0] = nil
	if len(discovery.Issues()) != 2 || discovery.Issues()[0] == nil {
		t.Fatal("DiscoveryError exposed mutable issues")
	}
	var missing *generationactivation.MissingAssociationError
	if !errors.As(err, &missing) || missing.Namespace() != "audit" {
		t.Fatalf("first MissingAssociationError = %#v", missing)
	}
	uses := missing.Uses()
	if len(uses) != 1 || uses[0].SourceCapability().String() != "order.cancel/v1" || !slices.Equal(uses[0].RequirementSources(), []string{"plystra.yaml http.expose.order"}) {
		t.Fatalf("missing Uses = %#v", uses)
	}
	uses[0] = generationactivation.NamespaceUse{}
	if missing.Uses()[0].SourceCapability().String() != "order.cancel/v1" {
		t.Fatal("MissingAssociationError exposed mutable uses")
	}
	for _, detail := range []string{"extensions.audit", "extensions.idempotency", "order.cancel/v1", "plystra.yaml http.expose.order", "one exact ordinary activation Capability"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("missing-association diagnostic omits %q: %v", detail, err)
		}
	}
}

func TestDiscoverRequirementsSupportsApplicationsWithoutExtensionMetadata(t *testing.T) {
	t.Parallel()

	plain := exactContract("order.read/v1", "")
	resolution := resolved(t,
		[]providerresolution.Requirement{{Contract: plain, Source: "order-client"}},
		[]providerresolution.Candidate{{PluginID: "example.orders", Contract: plain, Source: "orders/read"}},
	)
	set, err := (generationactivation.Catalog{}).DiscoverRequirements(resolution)
	if err != nil || len(set.Requirements()) != 0 {
		t.Fatalf("DiscoverRequirements(plain) = %#v, %v", set.Requirements(), err)
	}
	emptyResolution := resolved(t, nil, nil)
	set, err = (generationactivation.Catalog{}).DiscoverRequirements(emptyResolution)
	if err != nil || len(set.Requirements()) != 0 {
		t.Fatalf("DiscoverRequirements(empty) = %#v, %v", set.Requirements(), err)
	}
	if _, exists := set.Requirement(mustCapabilityID(t, "authn.session.verify/v1")); exists {
		t.Fatal("empty set contains an activation requirement")
	}
}

func TestDiscoverRequirementsIsStableAcrossResolutionAndCatalogOrder(t *testing.T) {
	t.Parallel()

	alpha := exactContract("alpha.call/v1", "extensions: {authn: {authenticated: true}, authz: {permission: alpha.call}}\n")
	beta := exactContract("beta.call/v1", "extensions: {authn: {authenticated: true}}\n")
	requirements := []providerresolution.Requirement{
		{Contract: beta, Source: "beta"},
		{Contract: alpha, Source: "alpha"},
	}
	candidates := []providerresolution.Candidate{
		{PluginID: "example.beta", Contract: beta, Source: "beta/provider"},
		{PluginID: "example.alpha", Contract: alpha, Source: "alpha/provider"},
	}
	declarations := []generationactivation.Declaration{
		declaration(t, "example.authz", "authz.check/v1", "authz", "authz/plugin.yaml"),
		declaration(t, "example.authn", "authn.session.verify/v1", "authn", "authn/plugin.yaml"),
	}
	firstResolution := resolved(t, requirements, candidates)
	firstCatalog, err := generationactivation.New(declarations)
	if err != nil {
		t.Fatalf("New(first): %v", err)
	}
	first, err := firstCatalog.DiscoverRequirements(firstResolution)
	if err != nil {
		t.Fatalf("DiscoverRequirements(first): %v", err)
	}

	slices.Reverse(requirements)
	slices.Reverse(candidates)
	slices.Reverse(declarations)
	secondResolution := resolved(t, requirements, candidates)
	secondCatalog, err := generationactivation.New(declarations)
	if err != nil {
		t.Fatalf("New(second): %v", err)
	}
	second, err := secondCatalog.DiscoverRequirements(secondResolution)
	if err != nil {
		t.Fatalf("DiscoverRequirements(second): %v", err)
	}
	if got, want := activationRequirementStrings(first.Requirements()), activationRequirementStrings(second.Requirements()); !slices.Equal(got, want) {
		t.Fatalf("order-dependent discovery: %v != %v", got, want)
	}
}

func resolved(t *testing.T, requirements []providerresolution.Requirement, candidates []providerresolution.Candidate) providerresolution.Result {
	t.Helper()
	result, err := providerresolution.Resolve(providerresolution.Input{Requirements: requirements, Candidates: candidates})
	if err != nil {
		t.Fatalf("providerresolution.Resolve: %v", err)
	}
	return result
}

func exactContract(id, body string) []byte {
	return []byte("id: " + id + "\n" + body + activationQuerySemanticsYAML)
}

const activationQuerySemanticsYAML = `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`

func mustCapabilityID(t *testing.T, value string) capabilityid.Identifier {
	t.Helper()
	id, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return id
}

func activationRequirementStrings(requirements []generationactivation.ActivationRequirement) []string {
	result := make([]string, len(requirements))
	for index, requirement := range requirements {
		uses := requirement.Uses()
		values := make([]string, len(uses))
		for useIndex, use := range uses {
			values[useIndex] = use.Namespace() + ":" + use.SourceCapability().String()
		}
		result[index] = requirement.Capability().String() + "=" + strings.Join(values, ",")
	}
	return result
}
