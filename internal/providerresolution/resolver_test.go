package providerresolution_test

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/providerresolution"
)

func TestResolveSelectsSoleOrdinaryProviderAndIntrinsicCapability(t *testing.T) {
	t.Parallel()

	auditContract := contract("audit.write/v1", "request: {event: {type: string, required: true}}\n")
	input := providerresolution.Input{
		Requirements: []providerresolution.Requirement{
			{Contract: contract("kernel.health/v1", "response: {status: {type: string, required: true}}\n"), Source: "kernel/catalog/health"},
			{Contract: auditContract, Source: "orders/plugin.yaml requires"},
			{Contract: append([]byte("description: Provider-independent audit.\n"), auditContract...), Source: "plystra.yaml capabilities.require"},
		},
		Candidates: []providerresolution.Candidate{
			{PluginID: "example.unused", Contract: contract("email.send/v1", ""), Source: "unused/capability.yaml"},
			{PluginID: "example.audit", Contract: append([]byte("request:\n  event: {required: true, type: string}\nid: audit.write/v1\n"), providerQuerySemanticsYAML...), Source: "audit/capabilities/audit.write/v1/capability.yaml"},
		},
	}
	result, err := providerresolution.Resolve(input)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	capabilities := result.Capabilities()
	if got := resolvedIDs(capabilities); !slices.Equal(got, []string{"audit.write/v1", "kernel.health/v1"}) {
		t.Fatalf("Capabilities = %v", got)
	}
	if capabilities[0].Intrinsic() || !capabilities[1].Intrinsic() {
		t.Fatalf("intrinsic flags = %t, %t", capabilities[0].Intrinsic(), capabilities[1].Intrinsic())
	}
	if got := capabilities[0].Sources(); !slices.Equal(got, []string{"orders/plugin.yaml requires", "plystra.yaml capabilities.require"}) {
		t.Fatalf("audit Sources = %v", got)
	}
	if !strings.HasPrefix(capabilities[0].ContractDigest(), "sha256:") || len(capabilities[0].ContractDigest()) != len("sha256:")+64 {
		t.Fatalf("audit digest = %q", capabilities[0].ContractDigest())
	}
	selection, ok := result.SelectedProvider(mustID(t, "audit.write/v1"))
	if !ok || selection.PluginID() != "example.audit" || selection.ProviderSource() != "audit/capabilities/audit.write/v1/capability.yaml" || selection.Explicit() || selection.ChoiceSource() != "" {
		t.Fatalf("SelectedProvider(audit) = %#v, %t", selection, ok)
	}
	if _, ok := result.SelectedProvider(mustID(t, "kernel.health/v1")); ok {
		t.Fatal("intrinsic Capability unexpectedly has a plugin provider")
	}
	if got := selectionStrings(result.Selections()); !slices.Equal(got, []string{"audit.write/v1=example.audit:auto"}) {
		t.Fatalf("Selections = %v", got)
	}

	input.Requirements[1].Contract[0] = 'x'
	input.Candidates[1].Contract[0] = 'x'
	capabilities[0] = providerresolution.ResolvedCapability{}
	contractJSON := result.Capabilities()[0].ContractJSON()
	contractJSON[0] = 'x'
	sources := result.Capabilities()[0].Sources()
	sources[0] = "changed"
	selections := result.Selections()
	selections[0] = providerresolution.Selection{}
	if result.Capabilities()[0].ID().String() != "audit.write/v1" || result.Capabilities()[0].ContractJSON()[0] != '{' || result.Capabilities()[0].Sources()[0] != "orders/plugin.yaml requires" || result.Selections()[0].PluginID() != "example.audit" {
		t.Fatal("Result exposed mutable input or result storage")
	}
}

func TestCatalogReusesImmutableValidatedCandidates(t *testing.T) {
	t.Parallel()

	contractData := contract("audit.write/v1", "request: {event: {type: string, required: true}}\n")
	candidates := []providerresolution.Candidate{{
		PluginID: "example.audit",
		Contract: append([]byte(nil), contractData...),
		Source:   "audit/capability.yaml",
	}}
	catalog, err := providerresolution.NewCatalog(candidates)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	candidates[0].PluginID = "changed.invalid"
	candidates[0].Contract[0] = 'x'
	candidates[0].Source = "changed"

	requirements := []providerresolution.Requirement{{
		Capability: "audit.write/v1",
		Source:     "orders/plugin.yaml requires",
	}}
	result, err := catalog.Resolve(requirements, nil)
	if err != nil {
		t.Fatalf("Catalog.Resolve: %v", err)
	}
	selection, exists := result.SelectedProvider(mustID(t, "audit.write/v1"))
	if !exists || selection.PluginID() != "example.audit" || selection.ProviderSource() != "audit/capability.yaml" {
		t.Fatalf("Catalog selection = %#v, %t", selection, exists)
	}
	requirements[0].Capability = "changed.invalid/v1"
	again, err := catalog.Resolve([]providerresolution.Requirement{{Capability: "audit.write/v1", Source: "second pass"}}, nil)
	if err != nil || !slices.Equal(resolvedIDs(again.Capabilities()), []string{"audit.write/v1"}) {
		t.Fatalf("second Catalog.Resolve = %#v, %v", again.Capabilities(), err)
	}
	if _, err := (providerresolution.Catalog{}).Resolve(nil, nil); !errors.Is(err, providerresolution.ErrInvalidInput) {
		t.Fatalf("zero Catalog.Resolve error = %v", err)
	}
	if _, err := providerresolution.NewCatalog([]providerresolution.Candidate{{PluginID: "invalid", Contract: contractData, Source: "bad"}}); !errors.Is(err, providerresolution.ErrInvalidInput) {
		t.Fatalf("NewCatalog invalid candidate error = %v", err)
	}
}

func TestResolveInfersReferenceOnlyRequirementFromExactProviders(t *testing.T) {
	t.Parallel()

	contractData := contract("authn.session.verify/v1", "extensions: {audit: {event: authn.session.verified}}\n")
	result, err := providerresolution.Resolve(providerresolution.Input{
		Requirements: []providerresolution.Requirement{{
			Capability: "authn.session.verify/v1",
			Source:     "extensions.authn on order.cancel/v1",
		}},
		Candidates: []providerresolution.Candidate{{
			PluginID: "example.authn",
			Contract: contractData,
			Source:   "authn/capabilities/authn.session.verify/v1/capability.yaml",
		}},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	capability, exists := result.Capability(mustID(t, "authn.session.verify/v1"))
	if !exists || capability.Intrinsic() || !slices.Equal(capability.Sources(), []string{"extensions.authn on order.cancel/v1"}) || !bytes.Contains(capability.ContractJSON(), []byte(`"audit"`)) {
		t.Fatalf("inferred Capability = %#v, %t", capability, exists)
	}
	selection, exists := result.SelectedProvider(capability.ID())
	if !exists || selection.PluginID() != "example.authn" || selection.Explicit() {
		t.Fatalf("inferred selection = %#v, %t", selection, exists)
	}

	result, err = providerresolution.Resolve(providerresolution.Input{
		Requirements: []providerresolution.Requirement{
			{Contract: contractData, Source: "official authn catalog"},
			{Capability: "authn.session.verify/v1", Source: "extensions.authn on order.cancel/v1"},
		},
		Candidates: []providerresolution.Candidate{{PluginID: "example.authn", Contract: contractData, Source: "authn/capability.yaml"}},
	})
	if err != nil {
		t.Fatalf("Resolve(mixed): %v", err)
	}
	capability, _ = result.Capability(mustID(t, "authn.session.verify/v1"))
	if !slices.Equal(capability.Sources(), []string{"extensions.authn on order.cancel/v1", "official authn catalog"}) {
		t.Fatalf("mixed requirement Sources = %v", capability.Sources())
	}
}

func TestResolveReferenceOnlyRequirementStillRequiresExplicitProviderChoice(t *testing.T) {
	t.Parallel()

	contractData := contract("authz.check/v1", "request: {permission: {type: string, required: true}}\n")
	input := providerresolution.Input{
		Requirements: []providerresolution.Requirement{{Capability: "authz.check/v1", Source: "extensions.authz on order.cancel/v1"}},
		Candidates: []providerresolution.Candidate{
			{PluginID: "example.authz-default", Contract: contractData, Source: "default/authz.check"},
			{PluginID: "example.authz-allow-all", Contract: contractData, Source: "allow-all/authz.check"},
		},
	}
	_, err := providerresolution.Resolve(input)
	if !errors.Is(err, providerresolution.ErrAmbiguousProvider) {
		t.Fatalf("Resolve error = %v, want ErrAmbiguousProvider", err)
	}
	input.Choices = []providerresolution.Choice{{Capability: "authz.check/v1", PluginID: "example.authz-default", Source: "plystra.yaml capabilities.use.authz.check/v1"}}
	result, err := providerresolution.Resolve(input)
	if err != nil {
		t.Fatalf("Resolve(explicit): %v", err)
	}
	selection, exists := result.SelectedProvider(mustID(t, "authz.check/v1"))
	if !exists || selection.PluginID() != "example.authz-default" || !selection.Explicit() {
		t.Fatalf("explicit reference selection = %#v, %t", selection, exists)
	}
}

func TestResolveReferenceOnlyRequirementReportsMissingAndConflictingProviders(t *testing.T) {
	t.Parallel()

	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		_, err := providerresolution.Resolve(providerresolution.Input{Requirements: []providerresolution.Requirement{{
			Capability: "audit.write/v1",
			Source:     "generation rule require-audit",
		}}})
		if !errors.Is(err, providerresolution.ErrMissingProvider) || !strings.Contains(err.Error(), "generation rule require-audit") {
			t.Fatalf("Resolve error = %v, want actionable ErrMissingProvider", err)
		}
	})

	t.Run("conflicting contracts", func(t *testing.T) {
		t.Parallel()
		input := providerresolution.Input{
			Requirements: []providerresolution.Requirement{{Capability: "audit.write/v1", Source: "generation rule require-audit"}},
			Candidates: []providerresolution.Candidate{
				{PluginID: "zeta.audit", Contract: contract("audit.write/v1", "extensions: {retention: {days: 30}}\n"), Source: "zeta/audit.write"},
				{PluginID: "acme.audit", Contract: contract("audit.write/v1", "extensions: {retention: {days: 7}}\n"), Source: "acme/audit.write"},
			},
		}
		_, firstErr := providerresolution.Resolve(input)
		slices.Reverse(input.Candidates)
		_, secondErr := providerresolution.Resolve(input)
		if !errors.Is(firstErr, providerresolution.ErrProviderContract) || firstErr.Error() != secondErr.Error() {
			t.Fatalf("contract conflict diagnostics:\nfirst:  %v\nsecond: %v", firstErr, secondErr)
		}
		var conflict *providerresolution.ProviderContractConflictError
		if !errors.As(firstErr, &conflict) || conflict.Capability().String() != "audit.write/v1" || !slices.Equal(conflict.Sources(), []string{"generation rule require-audit"}) {
			t.Fatalf("ProviderContractConflictError = %#v", conflict)
		}
		providers := conflict.Providers()
		if len(providers) != 2 || providers[0].PluginID() != "acme.audit" || providers[1].PluginID() != "zeta.audit" || providers[0].ContractDigest() == providers[1].ContractDigest() {
			t.Fatalf("conflicting Providers = %#v", providers)
		}
		providers[0] = providerresolution.ProviderDetail{}
		if conflict.Providers()[0].PluginID() != "acme.audit" {
			t.Fatal("ProviderContractConflictError exposed mutable providers")
		}
		for _, detail := range []string{"provider-independent contract", "normalized extension metadata", "new /vN", "no provider contract is chosen by ordering"} {
			if !strings.Contains(firstErr.Error(), detail) {
				t.Fatalf("contract conflict omits %q: %v", detail, firstErr)
			}
		}
	})
}

func TestResolveRequiresExplicitChoiceForSeveralProviders(t *testing.T) {
	t.Parallel()

	requirement := providerresolution.Requirement{Contract: contract("email.send/v1", "request: {to: {type: string, required: true}}\n"), Source: "plystra.yaml http.expose"}
	candidates := []providerresolution.Candidate{
		{PluginID: "zeta.email", Contract: requirement.Contract, Source: "zeta/capability.yaml"},
		{PluginID: "acme.email", Contract: requirement.Contract, Source: "acme/capability.yaml"},
	}
	_, firstErr := providerresolution.Resolve(providerresolution.Input{Requirements: []providerresolution.Requirement{requirement}, Candidates: candidates})
	slices.Reverse(candidates)
	_, secondErr := providerresolution.Resolve(providerresolution.Input{Requirements: []providerresolution.Requirement{requirement}, Candidates: candidates})
	if !errors.Is(firstErr, providerresolution.ErrResolve) || !errors.Is(firstErr, providerresolution.ErrAmbiguousProvider) || firstErr.Error() != secondErr.Error() {
		t.Fatalf("order-dependent ambiguity:\nfirst:  %v\nsecond: %v", firstErr, secondErr)
	}
	var ambiguous *providerresolution.AmbiguousProviderError
	if !errors.As(firstErr, &ambiguous) || ambiguous.Capability().String() != "email.send/v1" {
		t.Fatalf("ambiguity = %T %#v", firstErr, ambiguous)
	}
	providers := ambiguous.Providers()
	if len(providers) != 2 || providers[0].PluginID() != "acme.email" || providers[1].PluginID() != "zeta.email" {
		t.Fatalf("ambiguity Providers = %#v", providers)
	}
	providers[0] = providerresolution.ProviderDetail{}
	if ambiguous.Providers()[0].PluginID() != "acme.email" {
		t.Fatal("AmbiguousProviderError exposed mutable providers")
	}
	for _, detail := range []string{"plystra use email.send/v1 <plugin-id>", "capabilities.use[email.send/v1]", "acme.email", "zeta.email", "no priority", "discovery-order", "filesystem-order", "alphabetical fallback"} {
		if !strings.Contains(firstErr.Error(), detail) {
			t.Fatalf("ambiguity omits %q: %v", detail, firstErr)
		}
	}

	result, err := providerresolution.Resolve(providerresolution.Input{
		Requirements: []providerresolution.Requirement{requirement},
		Candidates:   candidates,
		Choices: []providerresolution.Choice{{
			Capability: "email.send/v1",
			PluginID:   "zeta.email",
			Source:     "plystra.yaml capabilities.use.email.send/v1",
		}},
	})
	if err != nil {
		t.Fatalf("Resolve(explicit): %v", err)
	}
	selection, ok := result.SelectedProvider(mustID(t, "email.send/v1"))
	if !ok || selection.PluginID() != "zeta.email" || !selection.Explicit() || selection.ChoiceSource() != "plystra.yaml capabilities.use.email.send/v1" {
		t.Fatalf("explicit selection = %#v, %t", selection, ok)
	}
}

func TestResolveReportsEveryMissingAndAmbiguousRequirementDeterministically(t *testing.T) {
	t.Parallel()

	a := providerresolution.Requirement{Contract: contract("alpha.call/v1", ""), Source: "alpha-client"}
	b := providerresolution.Requirement{Contract: contract("beta.call/v1", ""), Source: "beta-client"}
	candidates := []providerresolution.Candidate{
		{PluginID: "zeta.alpha", Contract: a.Contract, Source: "zeta/alpha"},
		{PluginID: "acme.alpha", Contract: a.Contract, Source: "acme/alpha"},
	}
	_, err := providerresolution.Resolve(providerresolution.Input{Requirements: []providerresolution.Requirement{b, a}, Candidates: candidates})
	if !errors.Is(err, providerresolution.ErrAmbiguousProvider) || !errors.Is(err, providerresolution.ErrMissingProvider) {
		t.Fatalf("Resolve error = %v", err)
	}
	var resolution *providerresolution.ResolutionError
	if !errors.As(err, &resolution) || len(resolution.Issues()) != 2 {
		t.Fatalf("ResolutionError = %#v", resolution)
	}
	issues := resolution.Issues()
	issues[0] = nil
	if len(resolution.Issues()) != 2 || resolution.Issues()[0] == nil {
		t.Fatal("ResolutionError exposed mutable issue storage")
	}
	slices.Reverse(candidates)
	_, reorderedErr := providerresolution.Resolve(providerresolution.Input{Requirements: []providerresolution.Requirement{a, b}, Candidates: candidates})
	if err.Error() != reorderedErr.Error() {
		t.Fatalf("order-dependent diagnostics:\nfirst:  %v\nsecond: %v", err, reorderedErr)
	}
}

func TestResolveRejectsContractDifferencesIncludingExtensionMetadata(t *testing.T) {
	t.Parallel()

	required := contract("order.cancel/v1", "extensions:\n  authn: {authenticated: true}\n  authz: {permission: order.cancel}\n")
	input := providerresolution.Input{
		Requirements: []providerresolution.Requirement{{Contract: required, Source: "official/order.cancel/v1"}},
		Candidates: []providerresolution.Candidate{
			{PluginID: "acme.orders", Contract: append([]byte("extensions: {authz: {permission: order.cancel}, authn: {authenticated: true}}\nid: order.cancel/v1\n"), providerQuerySemanticsYAML...), Source: "acme/orders/capability.yaml"},
			{PluginID: "legacy.orders", Contract: contract("order.cancel/v1", "extensions: {authz: {permission: order.cancel}}\n"), Source: "legacy/orders/capability.yaml"},
		},
	}
	_, err := providerresolution.Resolve(input)
	if !errors.Is(err, providerresolution.ErrProviderContract) {
		t.Fatalf("Resolve error = %v, want ErrProviderContract", err)
	}
	var mismatch *providerresolution.ProviderContractError
	if !errors.As(err, &mismatch) || mismatch.Capability().String() != "order.cancel/v1" || len(mismatch.Providers()) != 1 || mismatch.Providers()[0].PluginID() != "legacy.orders" {
		t.Fatalf("ProviderContractError = %#v", mismatch)
	}
	if mismatch.ExpectedDigest() == mismatch.Providers()[0].ContractDigest() || !slices.Equal(mismatch.ExpectedSources(), []string{"official/order.cancel/v1"}) {
		t.Fatalf("contract mismatch details = %q, %#v", mismatch.ExpectedDigest(), mismatch.Providers())
	}
	for _, detail := range []string{"legacy.orders", "normalized extension metadata", "new /vN"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("contract mismatch omits %q: %v", detail, err)
		}
	}
}

func TestResolveRejectsProviderLocalSemanticsOverride(t *testing.T) {
	t.Parallel()

	required := contract("order.cancel/v1", "")
	overridden := []byte(strings.Replace(string(required), "response: public", "response: restricted", 1))
	result, err := providerresolution.Resolve(providerresolution.Input{
		Requirements: []providerresolution.Requirement{{Contract: required, Source: "official/order.cancel/v1"}},
		Candidates: []providerresolution.Candidate{{
			PluginID: "acme.orders",
			Contract: overridden,
			Source:   "acme/orders/capability.yaml",
		}},
	})
	if !errors.Is(err, providerresolution.ErrProviderContract) || len(result.Selections()) != 0 {
		t.Fatalf("Resolve = %#v, %v, want rejected Provider-local semantics", result, err)
	}
	var mismatch *providerresolution.ProviderContractError
	if !errors.As(err, &mismatch) || mismatch.Capability().String() != "order.cancel/v1" || len(mismatch.Providers()) != 1 || mismatch.Providers()[0].PluginID() != "acme.orders" {
		t.Fatalf("ProviderContractError = %#v", mismatch)
	}
	if mismatch.ExpectedDigest() == mismatch.Providers()[0].ContractDigest() || !slices.Equal(mismatch.ExpectedSources(), []string{"official/order.cancel/v1"}) {
		t.Fatalf("contract mismatch details = %q, %#v", mismatch.ExpectedDigest(), mismatch.Providers())
	}
	for _, detail := range []string{"acme.orders", "typed semantics", "normalized extension metadata", "new /vN"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("Provider-local semantics diagnostic omits %q: %v", detail, err)
		}
	}
}

func TestResolveRejectsProviderLocalConstraintOverride(t *testing.T) {
	t.Parallel()

	required := contract("order.cancel/v1", "request:\n  reason: {type: string, required: true, constraints: {min_length: 1, max_length: 256}}\n")
	overridden := []byte(strings.Replace(string(required), "max_length: 256", "max_length: 512", 1))
	result, err := providerresolution.Resolve(providerresolution.Input{
		Requirements: []providerresolution.Requirement{{Contract: required, Source: "official/order.cancel/v1"}},
		Candidates: []providerresolution.Candidate{{
			PluginID: "acme.orders",
			Contract: overridden,
			Source:   "acme/orders/capability.yaml",
		}},
	})
	if !errors.Is(err, providerresolution.ErrProviderContract) || len(result.Selections()) != 0 {
		t.Fatalf("Resolve = %#v, %v, want rejected Provider-local constraint", result, err)
	}
	var mismatch *providerresolution.ProviderContractError
	if !errors.As(err, &mismatch) || mismatch.Capability().String() != "order.cancel/v1" || len(mismatch.Providers()) != 1 || mismatch.Providers()[0].PluginID() != "acme.orders" {
		t.Fatalf("ProviderContractError = %#v", mismatch)
	}
	if mismatch.ExpectedDigest() == mismatch.Providers()[0].ContractDigest() || !slices.Equal(mismatch.ExpectedSources(), []string{"official/order.cancel/v1"}) {
		t.Fatalf("contract mismatch details = %q, %#v", mismatch.ExpectedDigest(), mismatch.Providers())
	}
	for _, detail := range []string{"acme.orders", "closed field constraints", "typed semantics", "normalized extension metadata", "new /vN"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("Provider-local constraint diagnostic omits %q: %v", detail, err)
		}
	}
}

func TestResolveRejectsConflictingRequirementContracts(t *testing.T) {
	t.Parallel()

	input := providerresolution.Input{Requirements: []providerresolution.Requirement{
		{Contract: contract("order.cancel/v1", "extensions: {authn: {authenticated: true}}\n"), Source: "protected-client"},
		{Contract: contract("order.cancel/v1", ""), Source: "public-client"},
	}}
	_, err := providerresolution.Resolve(input)
	if !errors.Is(err, providerresolution.ErrRequirementConflict) {
		t.Fatalf("Resolve error = %v, want ErrRequirementConflict", err)
	}
	var conflict *providerresolution.RequirementConflictError
	if !errors.As(err, &conflict) || conflict.Capability().String() != "order.cancel/v1" || len(conflict.Variants()) != 2 {
		t.Fatalf("RequirementConflictError = %#v", conflict)
	}
	variants := conflict.Variants()
	variants[0] = providerresolution.ContractVariant{}
	if len(conflict.Variants()[0].Sources()) == 0 {
		t.Fatal("RequirementConflictError exposed mutable variants")
	}
	for _, detail := range []string{"protected-client", "public-client", "provider-independent", "new /vN"} {
		if !strings.Contains(err.Error(), detail) {
			t.Fatalf("requirement conflict omits %q: %v", detail, err)
		}
	}
}

func TestResolveRejectsInvalidExplicitChoices(t *testing.T) {
	t.Parallel()

	email := contract("email.send/v1", "")
	kernel := contract("kernel.health/v1", "")
	audit := contract("audit.write/v1", "")
	base := providerresolution.Input{
		Requirements: []providerresolution.Requirement{
			{Contract: email, Source: "email-client"},
			{Contract: kernel, Source: "health-route"},
		},
		Candidates: []providerresolution.Candidate{
			{PluginID: "acme.email", Contract: email, Source: "acme/email"},
			{PluginID: "acme.audit", Contract: audit, Source: "acme/audit"},
		},
	}
	tests := map[string]struct {
		choice  providerresolution.Choice
		problem providerresolution.ChoiceProblem
	}{
		"intrinsic": {
			choice:  providerresolution.Choice{Capability: "kernel.health/v1", PluginID: "acme.email", Source: "choice/intrinsic"},
			problem: providerresolution.ChoiceIntrinsicCapability,
		},
		"unknown capability": {
			choice:  providerresolution.Choice{Capability: "unknown.call/v1", PluginID: "acme.email", Source: "choice/unknown"},
			problem: providerresolution.ChoiceUnknownCapability,
		},
		"unrequired capability": {
			choice:  providerresolution.Choice{Capability: "audit.write/v1", PluginID: "acme.audit", Source: "choice/unrequired"},
			problem: providerresolution.ChoiceUnrequiredCapability,
		},
		"unknown plugin": {
			choice:  providerresolution.Choice{Capability: "email.send/v1", PluginID: "missing.email", Source: "choice/unknown-plugin"},
			problem: providerresolution.ChoiceUnknownPlugin,
		},
		"non provider": {
			choice:  providerresolution.Choice{Capability: "email.send/v1", PluginID: "acme.audit", Source: "choice/non-provider"},
			problem: providerresolution.ChoiceNonProvider,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := base
			input.Choices = []providerresolution.Choice{test.choice}
			_, err := providerresolution.Resolve(input)
			if !errors.Is(err, providerresolution.ErrInvalidChoice) {
				t.Fatalf("Resolve error = %v, want ErrInvalidChoice", err)
			}
			var choice *providerresolution.ChoiceError
			if !errors.As(err, &choice) || choice.Problem() != test.problem || choice.Source() != test.choice.Source || choice.PluginID() != test.choice.PluginID {
				t.Fatalf("ChoiceError = %#v", choice)
			}
		})
	}

	input := base
	input.Choices = []providerresolution.Choice{
		{Capability: "email.send/v1", PluginID: "acme.email", Source: "choice/first"},
		{Capability: "email.send/v1", PluginID: "acme.email", Source: "choice/second"},
	}
	_, err := providerresolution.Resolve(input)
	if !errors.Is(err, providerresolution.ErrInvalidChoice) {
		t.Fatalf("duplicate choice error = %v", err)
	}
	var duplicate *providerresolution.ChoiceError
	if !errors.As(err, &duplicate) || duplicate.Problem() != providerresolution.ChoiceDuplicate {
		t.Fatalf("duplicate ChoiceError = %#v", duplicate)
	}
}

func TestResolveRejectsInvalidProviderAndInputEnvelopes(t *testing.T) {
	t.Parallel()

	ordinary := contract("email.send/v1", "")
	tests := map[string]struct {
		input providerresolution.Input
		also  error
	}{
		"plugin provides intrinsic": {
			input: providerresolution.Input{Candidates: []providerresolution.Candidate{{PluginID: "acme.kernel", Contract: contract("kernel.health/v1", ""), Source: "acme/kernel"}}},
			also:  providerresolution.ErrInvalidProvider,
		},
		"duplicate provider": {
			input: providerresolution.Input{Candidates: []providerresolution.Candidate{
				{PluginID: "acme.email", Contract: ordinary, Source: "acme/email/first"},
				{PluginID: "acme.email", Contract: ordinary, Source: "acme/email/second"},
			}},
			also: providerresolution.ErrInvalidProvider,
		},
		"invalid requirement contract": {
			input: providerresolution.Input{Requirements: []providerresolution.Requirement{{Contract: []byte("[]\n"), Source: "bad/contract"}}},
		},
		"invalid provider plugin": {
			input: providerresolution.Input{Candidates: []providerresolution.Candidate{{PluginID: "Acme.Email", Contract: ordinary, Source: "bad/plugin"}}},
		},
		"invalid choice capability": {
			input: providerresolution.Input{Choices: []providerresolution.Choice{{Capability: "email.send", PluginID: "acme.email", Source: "bad/choice"}}},
		},
		"invalid source": {
			input: providerresolution.Input{Requirements: []providerresolution.Requirement{{Contract: ordinary, Source: "forged\nsource"}}},
		},
		"empty requirement": {
			input: providerresolution.Input{Requirements: []providerresolution.Requirement{{Source: "empty/requirement"}}},
		},
		"invalid referenced capability": {
			input: providerresolution.Input{Requirements: []providerresolution.Requirement{{Capability: "email.send", Source: "bad/reference"}}},
		},
		"mismatched capability and contract": {
			input: providerresolution.Input{Requirements: []providerresolution.Requirement{{Capability: "audit.write/v1", Contract: ordinary, Source: "bad/mismatch"}}},
		},
		"intrinsic without contract": {
			input: providerresolution.Input{Requirements: []providerresolution.Requirement{{Capability: "kernel.health/v1", Source: "bad/intrinsic-reference"}}},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result, err := providerresolution.Resolve(test.input)
			if !errors.Is(err, providerresolution.ErrResolve) || !errors.Is(err, providerresolution.ErrInvalidInput) || test.also != nil && !errors.Is(err, test.also) {
				t.Fatalf("Resolve = %#v, %v", result, err)
			}
			if len(result.Capabilities()) != 0 || len(result.Selections()) != 0 {
				t.Fatalf("invalid Resolve returned partial result %#v", result)
			}
		})
	}
}

func TestResolveSupportsEmptyApplication(t *testing.T) {
	t.Parallel()

	result, err := providerresolution.Resolve(providerresolution.Input{})
	if err != nil || len(result.Capabilities()) != 0 || len(result.Selections()) != 0 {
		t.Fatalf("Resolve(empty) = %#v, %v", result, err)
	}
	if _, ok := result.Capability(mustID(t, "kernel.health/v1")); ok {
		t.Fatal("empty result contains a Capability")
	}
}

func TestResolutionIsStableAcrossEveryInputOrder(t *testing.T) {
	t.Parallel()

	firstContract := contract("first.call/v1", "extensions: {authn: {authenticated: true}}\n")
	secondContract := contract("second.call/v1", "")
	input := providerresolution.Input{
		Requirements: []providerresolution.Requirement{
			{Contract: secondContract, Source: "second"},
			{Contract: firstContract, Source: "first-b"},
			{Contract: firstContract, Source: "first-a"},
		},
		Candidates: []providerresolution.Candidate{
			{PluginID: "zeta.first", Contract: firstContract, Source: "zeta/first"},
			{PluginID: "acme.second", Contract: secondContract, Source: "acme/second"},
			{PluginID: "acme.first", Contract: firstContract, Source: "acme/first"},
		},
		Choices: []providerresolution.Choice{{Capability: "first.call/v1", PluginID: "zeta.first", Source: "choice/first"}},
	}
	first, err := providerresolution.Resolve(input)
	if err != nil {
		t.Fatalf("Resolve(first): %v", err)
	}
	slices.Reverse(input.Requirements)
	slices.Reverse(input.Candidates)
	second, err := providerresolution.Resolve(input)
	if err != nil {
		t.Fatalf("Resolve(second): %v", err)
	}
	if got, want := renderResult(first), renderResult(second); !reflect.DeepEqual(got, want) {
		t.Fatalf("order-dependent results: %v != %v", got, want)
	}
}

func contract(id, body string) []byte {
	return []byte("id: " + id + "\n" + body + providerQuerySemanticsYAML)
}

const providerQuerySemanticsYAML = `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`

func mustID(t *testing.T, value string) capabilityid.Identifier {
	t.Helper()
	id, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return id
}

func resolvedIDs(values []providerresolution.ResolvedCapability) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID().String()
	}
	return result
}

func selectionStrings(values []providerresolution.Selection) []string {
	result := make([]string, len(values))
	for index, value := range values {
		kind := "auto"
		if value.Explicit() {
			kind = "explicit"
		}
		result[index] = value.Capability().String() + "=" + value.PluginID() + ":" + kind
	}
	return result
}

func renderResult(result providerresolution.Result) []string {
	values := make([]string, 0, len(result.Capabilities())+len(result.Selections()))
	for _, capability := range result.Capabilities() {
		values = append(values, capability.ID().String()+"|"+capability.ContractDigest()+"|"+strings.Join(capability.Sources(), ","))
	}
	values = append(values, selectionStrings(result.Selections())...)
	return values
}

func FuzzResolveNeverSelectsIntrinsicOrIncompatibleProvider(f *testing.F) {
	f.Add("email.send/v1", "acme.email", false)
	f.Add("kernel.health/v1", "acme.kernel", true)
	f.Add("authn.login.password/v1", "example.authn-password", true)
	f.Fuzz(func(t *testing.T, capability, plugin string, changeContract bool) {
		required := contract(capability, "extensions: {audit: {enabled: true}}\n")
		candidate := required
		if changeContract {
			candidate = contract(capability, "")
		}
		result, err := providerresolution.Resolve(providerresolution.Input{
			Requirements: []providerresolution.Requirement{{Contract: required, Source: "fuzz/requirement"}},
			Candidates:   []providerresolution.Candidate{{PluginID: plugin, Contract: candidate, Source: "fuzz/provider"}},
		})
		if err != nil {
			if !errors.Is(err, providerresolution.ErrResolve) {
				t.Fatalf("Resolve returned unexpected error: %v", err)
			}
			return
		}
		capabilities := result.Capabilities()
		if len(capabilities) != 1 || capabilities[0].Intrinsic() || changeContract {
			t.Fatalf("invalid successful result: %#v", capabilities)
		}
		selection, ok := result.SelectedProvider(capabilities[0].ID())
		if !ok || selection.PluginID() != plugin || bytes.Equal(candidate, required) != !changeContract {
			t.Fatalf("invalid selection: %#v, %t", selection, ok)
		}
	})
}
