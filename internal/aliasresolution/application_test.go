package aliasresolution_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/applicationmeta"
)

func TestNormalizeApplicationResolvesInheritanceNarrowingAndIntrinsicTargets(t *testing.T) {
	t.Parallel()

	context, input := applicationAliasContext(t)
	declarations := parseApplicationAliases(t, `capabilities:
  aliases:
    orders.start/v1: order.create/v1
    internal.order/v1:
      target: order.create/v1
      expose: {go: true, http: false, javascript: false}
    health.status/v1: kernel.health/v1
    orders.place/v1: order.create/v1
`)
	aliases, err := aliasresolution.NormalizeApplication(context, declarations)
	if err != nil {
		t.Fatalf("NormalizeApplication: %v", err)
	}
	if got := inputAliasIDs(aliases); !slices.Equal(got, []string{
		"health.status/v1",
		"internal.order/v1",
		"orders.place/v1",
		"orders.start/v1",
	}) {
		t.Fatalf("Alias IDs = %v", got)
	}
	if health := applicationInputAlias(t, aliases, "health.status/v1"); health.Target != "kernel.health/v1" || health.Exposure != (generation.Exposure{Go: true, HTTP: true}) {
		t.Fatalf("health Alias = %#v", health)
	}
	if internal := applicationInputAlias(t, aliases, "internal.order/v1"); internal.Exposure != (generation.Exposure{Go: true}) {
		t.Fatalf("internal Alias = %#v", internal)
	}
	for _, id := range []string{"orders.place/v1", "orders.start/v1"} {
		alias := applicationInputAlias(t, aliases, id)
		if alias.Target != "order.create/v1" || alias.Exposure != (generation.Exposure{Go: true, HTTP: true, JavaScript: true}) {
			t.Fatalf("%s inherited Alias = %#v", id, alias)
		}
	}
	for _, alias := range aliases {
		if !slices.Equal(alias.Sources, []generation.AliasSourceInput{{Kind: generation.AliasSourceApplication, ID: "application"}}) {
			t.Fatalf("%s sources = %#v", alias.ID, alias.Sources)
		}
	}

	input.CapabilityAliases = aliases
	roundTrip, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext(normalized application Aliases): %v", err)
	}
	if len(roundTrip.CapabilityAliases()) != len(aliases) {
		t.Fatalf("round-trip Aliases = %#v", roundTrip.CapabilityAliases())
	}
	aliases[0].ID = "changed.alias/v1"
	aliases[1].Sources[0].ID = "changed"
	fresh, err := aliasresolution.NormalizeApplication(context, declarations)
	if err != nil || fresh[0].ID != "health.status/v1" || fresh[1].Sources[0].ID != "application" {
		t.Fatalf("NormalizeApplication exposed mutable state = %#v, %v", fresh, err)
	}

	slices.Reverse(declarations)
	reordered, err := aliasresolution.NormalizeApplication(context, declarations)
	if err != nil || !slices.Equal(inputAliasIDs(reordered), inputAliasIDs(fresh)) {
		t.Fatalf("reordered declarations = %v, %v", inputAliasIDs(reordered), err)
	}
}

func TestNormalizeApplicationRejectsCatalogAndExposureViolations(t *testing.T) {
	t.Parallel()

	context, _ := applicationAliasContext(t)
	baseContext := aliasContext(t)
	tests := []struct {
		name         string
		context      generation.Context
		declarations []applicationmeta.Alias
		want         []string
	}{
		{
			name:         "missing target",
			context:      context,
			declarations: parseApplicationAliases(t, applicationAliasYAML("orders.missing/v1: missing.target/v1")),
			want:         []string{"orders.missing/v1", "missing.target/v1", "not a visible canonical"},
		},
		{
			name:         "unrequired target",
			context:      context,
			declarations: parseApplicationAliases(t, applicationAliasYAML("profiles.lookup/v1: profile.get/v1")),
			want:         []string{"profiles.lookup/v1", "profile.get/v1", "not a resolved canonical requirement"},
		},
		{
			name:         "canonical collision",
			context:      context,
			declarations: parseApplicationAliases(t, applicationAliasYAML("order.create/v1: kernel.health/v1")),
			want:         []string{"order.create/v1", "collides with a canonical Capability"},
		},
		{
			name:         "Alias target",
			context:      baseContext,
			declarations: parseApplicationAliases(t, applicationAliasYAML("compat.order/v1: orders.create/v1")),
			want:         []string{"compat.order/v1", "orders.create/v1", "Alias chains are forbidden"},
		},
		{
			name:    "exposure broadening",
			context: context,
			declarations: parseApplicationAliases(t, applicationAliasYAML(`authn.verify/v1:
      target: authn.session.verify/v1
      expose: {go: true, http: true, javascript: false}`)),
			want: []string{"authn.verify/v1", "authn.session.verify/v1", "broadens", "http=true"},
		},
		{
			name:    "duplicate declaration input",
			context: context,
			declarations: func() []applicationmeta.Alias {
				values := parseApplicationAliases(t, applicationAliasYAML("orders.start/v1: order.create/v1"))
				return append(values, values[0])
			}(),
			want: []string{"orders.start/v1", "duplicates"},
		},
		{
			name:         "existing Alias duplicate",
			context:      baseContext,
			declarations: parseApplicationAliases(t, applicationAliasYAML("orders.create/v1: order.create/v1")),
			want:         []string{"orders.create/v1", "duplicates existing normalized Alias"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			aliases, err := aliasresolution.NormalizeApplication(test.context, test.declarations)
			if !errors.Is(err, aliasresolution.ErrResolve) || !errors.Is(err, aliasresolution.ErrInvalidApplicationAlias) {
				t.Fatalf("NormalizeApplication error = %v", err)
			}
			for _, want := range append([]string{"plystra.yaml capabilities.aliases"}, test.want...) {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("NormalizeApplication error %q omits %q", err, want)
				}
			}
			if len(aliases) != 0 {
				t.Fatalf("invalid normalization returned %#v", aliases)
			}
		})
	}
}

func TestNormalizeApplicationRejectsInvalidContext(t *testing.T) {
	t.Parallel()

	if aliases, err := aliasresolution.NormalizeApplication(generation.Context{}, nil); !errors.Is(err, aliasresolution.ErrResolve) || !errors.Is(err, aliasresolution.ErrInvalidContext) || len(aliases) != 0 {
		t.Fatalf("NormalizeApplication zero context = %#v, %v", aliases, err)
	}
}

func applicationAliasContext(t *testing.T) (generation.Context, generation.Input) {
	t.Helper()
	input := aliasContextInput()
	input.CapabilityAliases = nil
	input.Capabilities = append(input.Capabilities, generation.CapabilityInput{
		ContractJSON: json.RawMessage(`{"id":"profile.get/v1","request":{},"response":{},"errors":[]}`),
		Exposure:     generation.Exposure{Go: true},
	})
	context, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	return context, input
}

func parseApplicationAliases(t *testing.T, data string) []applicationmeta.Alias {
	t.Helper()
	manifest, err := applicationmeta.Parse([]byte(data))
	if err != nil {
		t.Fatalf("applicationmeta.Parse: %v\n%s", err, data)
	}
	return manifest.Aliases()
}

func applicationAliasYAML(body string) string {
	return "capabilities:\n  aliases:\n    " + body + "\n"
}

func inputAliasIDs(values []generation.CapabilityAliasInput) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID
	}
	return result
}

func applicationInputAlias(t *testing.T, values []generation.CapabilityAliasInput, id string) generation.CapabilityAliasInput {
	t.Helper()
	for _, value := range values {
		if value.ID == id {
			return value
		}
	}
	t.Fatalf("Alias input %s not found in %v", id, inputAliasIDs(values))
	return generation.CapabilityAliasInput{}
}
