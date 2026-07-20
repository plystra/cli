package generation_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
)

func TestNewContextBuildsDeterministicImmutableViews(t *testing.T) {
	t.Parallel()

	input := validInput()
	orderContract := append([]byte(nil), input.Capabilities[0].ContractJSON...)
	context, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	if context.APIVersion() != generation.Version {
		t.Fatalf("APIVersion = %q", context.APIVersion())
	}
	provenance, exists := context.ConfigurationProvenance()
	if !exists || provenance.Mode() != generation.ConfigurationModeEnvironment || provenance.Environment() != "production" || provenance.RootPath() != "plystra.yaml" || provenance.SelectedPath() != "plystra.production.yaml" || !digestPattern.MatchString(provenance.RootDigest()) || !digestPattern.MatchString(provenance.SelectedDigest()) || !digestPattern.MatchString(provenance.DependencyCompositionDigest()) {
		t.Fatalf("ConfigurationProvenance = %#v, %t", provenance, exists)
	}

	plugins := context.Plugins()
	if got := pluginStrings(plugins); !slices.Equal(got, []string{"acme.audit", "acme.orders"}) {
		t.Fatalf("Plugins = %v", got)
	}
	auditID := mustPluginID(t, "acme.audit")
	audit, ok := context.Plugin(auditID)
	if !ok || audit.Module().Path() != "github.com/acme/audit" || audit.Module().Version() != "v1.2.3" {
		t.Fatalf("Plugin(acme.audit) = %#v, %t", audit, ok)
	}
	if got := string(audit.BuildMetadataJSON()); got != `{"batch":{"enabled":true,"size":2},"region":"global"}` {
		t.Fatalf("BuildMetadataJSON = %s", got)
	}
	provides := audit.Provides()
	provides[0] = generation.CapabilityID{}
	if audit.Provides()[0].String() != "audit.write/v1" {
		t.Fatal("PluginView.Provides exposed mutable storage")
	}
	metadata := audit.BuildMetadataJSON()
	metadata[0] = '['
	if audit.BuildMetadataJSON()[0] != '{' {
		t.Fatal("PluginView.BuildMetadataJSON exposed mutable storage")
	}

	capabilities := context.Capabilities()
	if got := capabilityStrings(capabilities); !slices.Equal(got, []string{"audit.write/v1", "kernel.health/v1", "order.create/v1"}) {
		t.Fatalf("Capabilities = %v", got)
	}
	orderID := mustCapabilityID(t, "order.create/v1")
	order, ok := context.Capability(orderID)
	if !ok || order.Intrinsic() || order.Exposure() != (generation.Exposure{Go: true, HTTP: true, JavaScript: true}) {
		t.Fatalf("Capability(order.create/v1) = %#v, %t", order, ok)
	}
	if !bytes.Equal(order.ContractJSON(), orderContract) || !digestPattern.MatchString(order.ContractDigest()) {
		t.Fatalf("order contract = %s, digest %q", order.ContractJSON(), order.ContractDigest())
	}
	if got := order.Sources(); !slices.Equal(got, []string{"github.com/acme/app@local/orders/capability.yaml", "github.com/acme/contracts@v1.0.0/order.create/v1/capability.yaml"}) {
		t.Fatalf("order Sources = %v", got)
	}
	orderSources := order.Sources()
	orderSources[0] = "changed"
	if order.Sources()[0] != "github.com/acme/app@local/orders/capability.yaml" {
		t.Fatal("CapabilityView.Sources exposed mutable storage")
	}
	if got := extensionNamespaces(order.Extensions()); !slices.Equal(got, []string{"authn", "authz"}) {
		t.Fatalf("order Extensions = %v", got)
	}
	authz, ok := order.Extension("authz")
	if !ok || string(authz.ValueJSON()) != `{"permission":"order.create","space":"request.space_id"}` {
		t.Fatalf("order authz extension = %#v, %t", authz, ok)
	}
	value := authz.ValueJSON()
	value[0] = '['
	authzAgain, _ := order.Extension("authz")
	if authzAgain.ValueJSON()[0] != '{' {
		t.Fatal("ExtensionView.ValueJSON exposed mutable storage")
	}
	contract := order.ContractJSON()
	contract[0] = '['
	if order.ContractJSON()[0] != '{' {
		t.Fatal("CapabilityView.ContractJSON exposed mutable storage")
	}

	if got := capabilityIDStrings(context.Requirements()); !slices.Equal(got, []string{"audit.write/v1", "kernel.health/v1", "order.create/v1"}) {
		t.Fatalf("Requirements = %v", got)
	}
	if got := providerStrings(context.Providers()); !slices.Equal(got, []string{"audit.write/v1=acme.audit", "order.create/v1=acme.orders"}) {
		t.Fatalf("Providers = %v", got)
	}
	if provider, ok := context.SelectedProvider(orderID); !ok || provider.String() != "acme.orders" {
		t.Fatalf("SelectedProvider(order) = %q, %t", provider.String(), ok)
	}
	if _, ok := context.SelectedProvider(mustCapabilityID(t, "kernel.health/v1")); ok {
		t.Fatal("intrinsic Capability unexpectedly has a plugin provider")
	}

	aliasID := mustCapabilityID(t, "orders.submit/v1")
	alias, ok := context.CapabilityAlias(aliasID)
	if !ok || alias.Target() != orderID || alias.Exposure() != (generation.Exposure{Go: true, HTTP: true}) || alias.Deprecated() != "Use order.create/v1 instead." {
		t.Fatalf("CapabilityAlias(orders.submit/v1) = %#v, %t", alias, ok)
	}
	if _, ok := context.Capability(aliasID); ok {
		t.Fatal("Capability resolved an Alias ID")
	}
	if _, ok := context.SelectedProvider(aliasID); ok {
		t.Fatal("Alias unexpectedly has a provider")
	}
	if got := aliasSourceStrings(alias.Sources()); !slices.Equal(got, []string{"application=application", "generation-extension=acme.orders"}) {
		t.Fatalf("Alias sources = %v", got)
	}
	sources := alias.Sources()
	sources[0] = generation.AliasSourceView{}
	if alias.Sources()[0].ID() != "application" {
		t.Fatal("CapabilityAliasView.Sources exposed mutable storage")
	}

	canonical := context.CanonicalJSON()
	if !json.Valid(canonical) || !digestPattern.MatchString(context.Digest()) {
		t.Fatalf("CanonicalJSON = %s, Digest = %q", canonical, context.Digest())
	}
	canonical[0] = '['
	if context.CanonicalJSON()[0] != '{' {
		t.Fatal("Context.CanonicalJSON exposed mutable storage")
	}
	plugins[0] = generation.PluginView{}
	capabilities[0] = generation.CapabilityView{}
	if context.Plugins()[0].ID().String() != "acme.audit" || context.Capabilities()[0].ID().String() != "audit.write/v1" {
		t.Fatal("Context collection accessors exposed mutable storage")
	}

	input.Capabilities[0].ContractJSON[0] = '['
	input.Capabilities[0].Sources[0] = "changed"
	input.Plugins[1].BuildMetadataJSON[0] = '['
	input.Plugins[0].Provides[0] = "changed.invalid/v1"
	input.CapabilityAliases[0].Sources[0].ID = "changed"
	input.ConfigurationProvenance.Environment = "changed"
	if current, _ := context.ConfigurationProvenance(); !bytes.Equal(order.ContractJSON(), orderContract) || order.Sources()[0] != "github.com/acme/app@local/orders/capability.yaml" || audit.BuildMetadataJSON()[0] != '{' || context.Plugins()[1].Provides()[0].String() != "order.create/v1" || alias.Sources()[0].ID() != "application" || current.Environment() != "production" {
		t.Fatal("NewContext retained mutable input storage")
	}

	equivalent := validInput()
	slices.Reverse(equivalent.Plugins)
	slices.Reverse(equivalent.Capabilities)
	slices.Reverse(equivalent.Requirements)
	slices.Reverse(equivalent.Providers)
	slices.Reverse(equivalent.CapabilityAliases[0].Sources)
	for index := range equivalent.Capabilities {
		slices.Reverse(equivalent.Capabilities[index].Sources)
	}
	equivalent.Plugins[0].BuildMetadataJSON = json.RawMessage(`{"batch":{"enabled":true,"size":2},"region":"global"}`)
	second, err := generation.NewContext(equivalent)
	if err != nil {
		t.Fatalf("NewContext(equivalent): %v", err)
	}
	if !bytes.Equal(context.CanonicalJSON(), second.CanonicalJSON()) || context.Digest() != second.Digest() {
		t.Fatalf("equivalent input changed canonical form:\nfirst  %s\nsecond %s", context.CanonicalJSON(), second.CanonicalJSON())
	}
}

func TestContextSeparatesConfigurationProvenanceFromBuildModelDigest(t *testing.T) {
	t.Parallel()

	input := validInput()
	first, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext(first): %v", err)
	}
	input.ConfigurationProvenance.SelectedDigest = "sha256:" + strings.Repeat("4", 64)
	second, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext(second): %v", err)
	}
	if first.Digest() == second.Digest() || bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) {
		t.Fatal("selected-document provenance did not change the extension context identity")
	}
	if first.BuildModelDigest() != second.BuildModelDigest() {
		t.Fatalf("provenance-only change altered build model digest: %q != %q", first.BuildModelDigest(), second.BuildModelDigest())
	}
	input.ConfigurationProvenance = nil
	synthetic, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext(synthetic): %v", err)
	}
	if _, exists := synthetic.ConfigurationProvenance(); exists || synthetic.BuildModelDigest() != first.BuildModelDigest() {
		t.Fatalf("synthetic provenance = present %t, build digest %q", exists, synthetic.BuildModelDigest())
	}
}

func TestNewContextSupportsAnEmptyApplication(t *testing.T) {
	t.Parallel()

	context, err := generation.NewContext(generation.Input{})
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	want := `{"api":"v1","plugins":[],"capabilities":[],"requirements":[],"providers":[],"capability_aliases":[]}`
	if string(context.CanonicalJSON()) != want || len(context.Plugins()) != 0 || len(context.Capabilities()) != 0 || len(context.Requirements()) != 0 || len(context.Providers()) != 0 || len(context.CapabilityAliases()) != 0 {
		t.Fatalf("empty Context = %s", context.CanonicalJSON())
	}
	second, err := generation.NewContext(generation.Input{})
	if err != nil || second.Digest() != context.Digest() {
		t.Fatalf("empty context is not deterministic: %q, %q, %v", context.Digest(), second.Digest(), err)
	}
}

func TestContextPreservesTheCanonicalContractAndDigest(t *testing.T) {
	t.Parallel()

	declaration := []byte(`id: order.create/v1
request:
  space_id: {required: true, type: string}
response: {}
errors: [invalid_state]
extensions:
  authz: {space: request.space_id, permission: order.create}
`)
	contract, err := capabilitymeta.NormalizeSchema(declaration)
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	context, err := generation.NewContext(generation.Input{
		Capabilities: []generation.CapabilityInput{{ContractJSON: contract, Exposure: generation.Exposure{Go: true}}},
	})
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	capability, ok := context.Capability(mustCapabilityID(t, "order.create/v1"))
	if !ok || !bytes.Equal(capability.ContractJSON(), contract) {
		t.Fatalf("Capability contract = %s, %t; want %s", capability.ContractJSON(), ok, contract)
	}
	sum := sha256.Sum256(contract)
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])
	if capability.ContractDigest() != wantDigest {
		t.Fatalf("ContractDigest = %q, want %q", capability.ContractDigest(), wantDigest)
	}
}

func TestContextAllowsSeveralAliasesForOneCanonicalTarget(t *testing.T) {
	t.Parallel()

	input := validInput()
	input.CapabilityAliases = append(input.CapabilityAliases, generation.CapabilityAliasInput{
		ID:       "orders.create/v1",
		Target:   "order.create/v1",
		Exposure: generation.Exposure{Go: true},
		Sources:  []generation.AliasSourceInput{{Kind: generation.AliasSourceApplication, ID: "application"}},
	})
	context, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	aliases := context.CapabilityAliases()
	if len(aliases) != 2 || aliases[0].ID().String() != "orders.create/v1" || aliases[1].ID().String() != "orders.submit/v1" || aliases[0].Target() != aliases[1].Target() {
		t.Fatalf("CapabilityAliases = %#v", aliases)
	}
}

func TestNewContextRejectsInconsistentOrUnsafeInput(t *testing.T) {
	t.Parallel()

	tests := map[string]func(*generation.Input){
		"invalid plugin ID":      func(input *generation.Input) { input.Plugins[0].ID = "Acme.Audit" },
		"duplicate plugin":       func(input *generation.Input) { input.Plugins = append(input.Plugins, input.Plugins[0]) },
		"invalid module path":    func(input *generation.Input) { input.Plugins[0].ModulePath = "../audit" },
		"invalid module version": func(input *generation.Input) { input.Plugins[0].ModuleVersion = "v2.0.0" },
		"duplicate provided capability": func(input *generation.Input) {
			input.Plugins[0].Provides = append(input.Plugins[0].Provides, input.Plugins[0].Provides[0])
		},
		"non object build metadata": func(input *generation.Input) { input.Plugins[0].BuildMetadataJSON = json.RawMessage(`[]`) },
		"duplicate build metadata key": func(input *generation.Input) {
			input.Plugins[0].BuildMetadataJSON = json.RawMessage(`{"mode":1,"mode":2}`)
		},
		"build metadata too deep": func(input *generation.Input) {
			input.Plugins[0].BuildMetadataJSON = json.RawMessage(strings.Repeat(`{"nested":`, 65) + `true` + strings.Repeat(`}`, 65))
		},
		"build metadata too large": func(input *generation.Input) {
			input.Plugins[0].BuildMetadataJSON = json.RawMessage(`{"value":"` + strings.Repeat("a", 1<<20) + `"}`)
		},
		"non compact contract": func(input *generation.Input) {
			input.Capabilities[0].ContractJSON = append([]byte(" "), input.Capabilities[0].ContractJSON...)
		},
		"duplicate contract key": func(input *generation.Input) {
			input.Capabilities[0].ContractJSON = json.RawMessage(`{"id":"order.create/v1","id":"order.create/v1","request":{},"response":{},"errors":[]}`)
		},
		"unknown contract field": func(input *generation.Input) {
			input.Capabilities[0].ContractJSON = json.RawMessage(`{"id":"order.create/v1","request":{},"response":{},"errors":[],"secret":"must-not-enter"}`)
		},
		"invalid contract ID": func(input *generation.Input) {
			input.Capabilities[0].ContractJSON = canonicalContract("Order.Create/v1", nil)
		},
		"ordinary marked intrinsic": func(input *generation.Input) { input.Capabilities[0].Intrinsic = true },
		"kernel not intrinsic":      func(input *generation.Input) { input.Capabilities[1].Intrinsic = false },
		"duplicate contract source": func(input *generation.Input) {
			input.Capabilities[0].Sources = append(input.Capabilities[0].Sources, input.Capabilities[0].Sources[0])
		},
		"invalid contract source": func(input *generation.Input) { input.Capabilities[0].Sources[0] = "bad\x00source" },
		"null extensions": func(input *generation.Input) {
			input.Capabilities[0].ContractJSON = json.RawMessage(`{"id":"order.create/v1","request":{},"response":{},"errors":[],"extensions":null}`)
		},
		"invalid extension namespace": func(input *generation.Input) {
			input.Capabilities[0].ContractJSON = canonicalContract("order.create/v1", json.RawMessage(`{"AuthN":true}`))
		},
		"duplicate capability":      func(input *generation.Input) { input.Capabilities = append(input.Capabilities, input.Capabilities[0]) },
		"unknown requirement":       func(input *generation.Input) { input.Requirements = append(input.Requirements, "missing.operation/v1") },
		"duplicate requirement":     func(input *generation.Input) { input.Requirements = append(input.Requirements, input.Requirements[0]) },
		"unknown provider plugin":   func(input *generation.Input) { input.Providers[0].Plugin = "acme.missing" },
		"provider does not provide": func(input *generation.Input) { input.Providers[0].Plugin = "acme.audit" },
		"intrinsic provider": func(input *generation.Input) {
			input.Providers = append(input.Providers, generation.ProviderInput{Capability: "kernel.health/v1", Plugin: "acme.orders"})
		},
		"duplicate provider": func(input *generation.Input) { input.Providers = append(input.Providers, input.Providers[0]) },
		"missing provider":   func(input *generation.Input) { input.Providers = input.Providers[1:] },
		"plugin provides unknown": func(input *generation.Input) {
			input.Plugins[0].Provides = append(input.Plugins[0].Provides, "missing.operation/v1")
		},
		"plugin requires unknown": func(input *generation.Input) {
			input.Plugins[0].Requires = append(input.Plugins[0].Requires, "missing.operation/v1")
		},
		"plugin requirement unresolved": func(input *generation.Input) {
			input.Requirements = slices.DeleteFunc(input.Requirements, func(value string) bool { return value == "audit.write/v1" })
			input.Providers = slices.DeleteFunc(input.Providers, func(value generation.ProviderInput) bool { return value.Capability == "audit.write/v1" })
		},
		"alias canonical collision":         func(input *generation.Input) { input.CapabilityAliases[0].ID = "order.create/v1" },
		"alias reserved kernel namespace":   func(input *generation.Input) { input.CapabilityAliases[0].ID = "kernel.compat/v1" },
		"alias missing target":              func(input *generation.Input) { input.CapabilityAliases[0].Target = "missing.operation/v1" },
		"alias version mismatch":            func(input *generation.Input) { input.CapabilityAliases[0].ID = "orders.submit/v2" },
		"alias exposure broadening":         func(input *generation.Input) { input.CapabilityAliases[0].Target = "audit.write/v1" },
		"alias without provenance":          func(input *generation.Input) { input.CapabilityAliases[0].Sources = nil },
		"alias invalid source kind":         func(input *generation.Input) { input.CapabilityAliases[0].Sources[0].Kind = "priority" },
		"alias unselected extension source": func(input *generation.Input) { input.CapabilityAliases[0].Sources[1].ID = "acme.missing" },
		"duplicate alias source": func(input *generation.Input) {
			input.CapabilityAliases[0].Sources = append(input.CapabilityAliases[0].Sources, input.CapabilityAliases[0].Sources[0])
		},
		"duplicate alias": func(input *generation.Input) {
			input.CapabilityAliases = append(input.CapabilityAliases, input.CapabilityAliases[0])
		},
		"plugin requires alias": func(input *generation.Input) {
			input.Plugins[0].Requires = append(input.Plugins[0].Requires, "orders.submit/v1")
		},
		"unsupported configuration mode": func(input *generation.Input) {
			input.ConfigurationProvenance.Mode = "profile"
		},
		"wrong root configuration path": func(input *generation.Input) {
			input.ConfigurationProvenance.RootPath = "deploy/root.yaml"
		},
		"unsafe selected configuration path": func(input *generation.Input) {
			input.ConfigurationProvenance.SelectedPath = "C:/private/plystra.yaml"
		},
		"invalid root configuration digest": func(input *generation.Input) {
			input.ConfigurationProvenance.RootDigest = "sha256:ABC"
		},
		"invalid selected configuration digest": func(input *generation.Input) {
			input.ConfigurationProvenance.SelectedDigest = "sha256:" + strings.Repeat("g", 64)
		},
		"invalid dependency composition digest": func(input *generation.Input) {
			input.ConfigurationProvenance.DependencyCompositionDigest = ""
		},
		"default selection mismatch": func(input *generation.Input) {
			input.ConfigurationProvenance.Mode = generation.ConfigurationModeDefault
			input.ConfigurationProvenance.Environment = ""
		},
		"unsafe environment name": func(input *generation.Input) {
			input.ConfigurationProvenance.Environment = "../production"
		},
		"environment path mismatch": func(input *generation.Input) {
			input.ConfigurationProvenance.SelectedPath = "plystra.staging.yaml"
		},
		"explicit selection has environment": func(input *generation.Input) {
			input.ConfigurationProvenance.Mode = generation.ConfigurationModeExplicit
			input.ConfigurationProvenance.SelectedPath = "deploy/customer.yaml"
		},
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := validInput()
			mutate(&input)
			context, err := generation.NewContext(input)
			if !errors.Is(err, generation.ErrInvalidContext) {
				t.Fatalf("NewContext error = %v, want ErrInvalidContext", err)
			}
			if len(context.CanonicalJSON()) != 0 || context.Digest() != "" {
				t.Fatalf("invalid NewContext returned %s, %q", context.CanonicalJSON(), context.Digest())
			}
		})
	}
}

func TestPublicIdentityParsersFollowCanonicalGrammar(t *testing.T) {
	t.Parallel()

	capability, err := generation.ParseCapabilityID("authn.login.password-/v18446744073709551615")
	if err != nil || capability.Name() != "authn.login.password-" || capability.Major() != ^uint64(0) || capability.String() != "authn.login.password-/v18446744073709551615" {
		t.Fatalf("ParseCapabilityID = %#v, %v", capability, err)
	}
	if _, err := generation.ParseCapabilityID("authn.login/v01"); !errors.Is(err, generation.ErrInvalidCapabilityID) {
		t.Fatalf("ParseCapabilityID error = %v", err)
	}
	plugin, err := generation.ParsePluginID("acme.authn-password")
	if err != nil || plugin.String() != "acme.authn-password" {
		t.Fatalf("ParsePluginID = %#v, %v", plugin, err)
	}
	if _, err := generation.ParsePluginID("acme.authn-"); !errors.Is(err, generation.ErrInvalidPluginID) {
		t.Fatalf("ParsePluginID error = %v", err)
	}
	payload, err := json.Marshal(struct {
		Capability generation.CapabilityID `json:"capability"`
		Plugin     generation.PluginID     `json:"plugin"`
	}{Capability: capability, Plugin: plugin})
	if err != nil || string(payload) != `{"capability":"authn.login.password-/v18446744073709551615","plugin":"acme.authn-password"}` {
		t.Fatalf("Marshal identities = %s, %v", payload, err)
	}
	var decoded struct {
		Capability generation.CapabilityID `json:"capability"`
		Plugin     generation.PluginID     `json:"plugin"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil || decoded.Capability != capability || decoded.Plugin != plugin {
		t.Fatalf("Unmarshal identities = %#v, %v", decoded, err)
	}
}

func FuzzNewContextJSONNormalization(f *testing.F) {
	for _, seed := range []string{`{}`, `{"mode":"strict"}`, `{"nested":{"enabled":true,"count":2}}`, `[]`, `{"duplicate":1,"duplicate":2}`, `{`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		input := validInput()
		input.Plugins[0].BuildMetadataJSON = json.RawMessage(value)
		first, firstErr := generation.NewContext(input)
		second, secondErr := generation.NewContext(input)
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("NewContext result changed: %v then %v", firstErr, secondErr)
		}
		if firstErr != nil {
			if !errors.Is(firstErr, generation.ErrInvalidContext) || !errors.Is(secondErr, generation.ErrInvalidContext) {
				t.Fatalf("NewContext errors = %v and %v", firstErr, secondErr)
			}
			return
		}
		if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
			t.Fatal("successful NewContext output is nondeterministic")
		}
	})
}

func FuzzNewContextCanonicalContract(f *testing.F) {
	for _, seed := range []string{
		`{"id":"order.create/v1","request":{},"response":{},"errors":[]}`,
		`{"id":"order.create/v1","request":{},"response":{},"errors":[],"extensions":{"authn":{"authenticated":true}}}`,
		` {"id":"order.create/v1","request":{},"response":{},"errors":[]}`,
		`{"id":"order.create/v1","secret":"value"}`,
		`[]`,
		`{`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		input := generation.Input{Capabilities: []generation.CapabilityInput{{ContractJSON: json.RawMessage(value)}}}
		first, firstErr := generation.NewContext(input)
		second, secondErr := generation.NewContext(input)
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("NewContext result changed: %v then %v", firstErr, secondErr)
		}
		if firstErr != nil {
			if !errors.Is(firstErr, generation.ErrInvalidContext) || !errors.Is(secondErr, generation.ErrInvalidContext) {
				t.Fatalf("NewContext errors = %v and %v", firstErr, secondErr)
			}
			return
		}
		if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
			t.Fatal("successful NewContext output is nondeterministic")
		}
	})
}

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func validInput() generation.Input {
	orderContract := json.RawMessage(`{"id":"order.create/v1","request":{"space_id":{"type":"string","required":true}},"response":{},"errors":["invalid_state"],"extensions":{"authn":{"authenticated":true},"authz":{"permission":"order.create","space":"request.space_id"}}}`)
	return generation.Input{
		ConfigurationProvenance: &generation.ConfigurationProvenanceInput{
			Mode:                        generation.ConfigurationModeEnvironment,
			Environment:                 "production",
			RootPath:                    "plystra.yaml",
			RootDigest:                  "sha256:" + strings.Repeat("1", 64),
			SelectedPath:                "plystra.production.yaml",
			SelectedDigest:              "sha256:" + strings.Repeat("2", 64),
			DependencyCompositionDigest: "sha256:" + strings.Repeat("3", 64),
		},
		Plugins: []generation.PluginInput{
			{
				ID:         "acme.orders",
				ModulePath: "github.com/acme/app",
				Provides:   []string{"order.create/v1"},
				Requires:   []string{"audit.write/v1"},
			},
			{
				ID:                "acme.audit",
				ModulePath:        "github.com/acme/audit",
				ModuleVersion:     "v1.2.3",
				Provides:          []string{"audit.write/v1"},
				BuildMetadataJSON: json.RawMessage(`{"region":"global","batch":{"size":2,"enabled":true}}`),
			},
		},
		Capabilities: []generation.CapabilityInput{
			{ContractJSON: orderContract, Sources: []string{"github.com/acme/contracts@v1.0.0/order.create/v1/capability.yaml", "github.com/acme/app@local/orders/capability.yaml"}, Exposure: generation.Exposure{Go: true, HTTP: true, JavaScript: true}},
			{ContractJSON: canonicalContract("kernel.health/v1", nil), Sources: []string{"github.com/plystra/kernel/intrinsic/kernel.health/v1"}, Intrinsic: true, Exposure: generation.Exposure{Go: true, HTTP: true}},
			{ContractJSON: canonicalContract("audit.write/v1", nil), Sources: []string{"github.com/acme/audit@v1.2.3/audit.write/v1/capability.yaml"}, Exposure: generation.Exposure{Go: true}},
		},
		Requirements: []string{"order.create/v1", "kernel.health/v1", "audit.write/v1"},
		Providers: []generation.ProviderInput{
			{Capability: "order.create/v1", Plugin: "acme.orders"},
			{Capability: "audit.write/v1", Plugin: "acme.audit"},
		},
		CapabilityAliases: []generation.CapabilityAliasInput{
			{
				ID:         "orders.submit/v1",
				Target:     "order.create/v1",
				Exposure:   generation.Exposure{Go: true, HTTP: true},
				Deprecated: "Use order.create/v1 instead.",
				Sources: []generation.AliasSourceInput{
					{Kind: generation.AliasSourceGenerationExtension, ID: "acme.orders"},
					{Kind: generation.AliasSourceApplication, ID: "application"},
				},
			},
		},
	}
}

func canonicalContract(id string, extensions json.RawMessage) json.RawMessage {
	if len(extensions) == 0 {
		return json.RawMessage(`{"id":"` + id + `","request":{},"response":{},"errors":[]}`)
	}
	return json.RawMessage(`{"id":"` + id + `","request":{},"response":{},"errors":[],"extensions":` + string(extensions) + `}`)
}

func mustCapabilityID(t *testing.T, value string) generation.CapabilityID {
	t.Helper()
	id, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%q): %v", value, err)
	}
	return id
}

func mustPluginID(t *testing.T, value string) generation.PluginID {
	t.Helper()
	id, err := generation.ParsePluginID(value)
	if err != nil {
		t.Fatalf("ParsePluginID(%q): %v", value, err)
	}
	return id
}

func pluginStrings(values []generation.PluginView) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID().String()
	}
	return result
}

func capabilityStrings(values []generation.CapabilityView) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID().String()
	}
	return result
}

func capabilityIDStrings(values []generation.CapabilityID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return result
}

func extensionNamespaces(values []generation.ExtensionView) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Namespace()
	}
	return result
}

func providerStrings(values []generation.ProviderView) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Capability().String() + "=" + value.Plugin().String()
	}
	return result
}

func aliasSourceStrings(values []generation.AliasSourceView) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value.Kind()) + "=" + value.ID()
	}
	return result
}
