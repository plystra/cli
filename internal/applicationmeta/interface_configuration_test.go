package applicationmeta_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestParseNormalizesTypedInterfaceConfiguration(t *testing.T) {
	t.Parallel()

	manifest, err := applicationmeta.ParseSource("deploy/production.yaml", []byte(`
interfaces:
  require:
    add: [email.send/v1, audit.write/v1]
    remove: [cache.read/v1]
  use:
    email.send/v1: github.com/acme/app/smtp.New
    cache.read/v1: {$remove: true}
  policies:
    email.send/v1: {timeout: 5000ms}
    audit.write/v1: {$remove: true}
`))
	if err != nil {
		t.Fatal(err)
	}
	requirements := manifest.InterfaceRequirements()
	if got := interfaceRequirementStrings(requirements); !reflect.DeepEqual(got, []string{
		`audit.write/v1@deploy/production.yaml interfaces.require.add["audit.write/v1"]`,
		`email.send/v1@deploy/production.yaml interfaces.require.add["email.send/v1"]`,
	}) {
		t.Fatalf("InterfaceRequirements = %v", got)
	}
	choices := manifest.ImplementationChoices()
	if got := implementationChoiceStrings(choices); !reflect.DeepEqual(got, []string{
		`email.send/v1->github.com/acme/app/smtp.New@deploy/production.yaml interfaces.use["email.send/v1"]`,
	}) {
		t.Fatalf("ImplementationChoices = %v", got)
	}
	policies := manifest.InterfacePolicies()
	if got := interfacePolicyStrings(policies); !reflect.DeepEqual(got, []string{
		`email.send/v1=5s@deploy/production.yaml interfaces.policies["email.send/v1"]`,
	}) {
		t.Fatalf("InterfacePolicies = %v", got)
	}
	requirements[0] = applicationmeta.InterfaceRequirement{}
	choices[0] = applicationmeta.ImplementationChoice{}
	policies[0] = applicationmeta.InterfacePolicy{}
	if manifest.InterfaceRequirements()[0].ID().String() != "audit.write/v1" || manifest.ImplementationChoices()[0].Constructor().String() != "github.com/acme/app/smtp.New" || manifest.InterfacePolicies()[0].Timeout().String() != "5s" {
		t.Fatal("Manifest returned aliased Interface configuration storage")
	}

	decisions, err := applicationmeta.ConfigurationDecisions(manifest, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		summary applicationmeta.ConfigurationDecisionSummary
		removed bool
	}{
		`interfaces.require["audit.write/v1"]`:  {summary: applicationmeta.ConfigurationSummaryInterface},
		`interfaces.require["email.send/v1"]`:   {summary: applicationmeta.ConfigurationSummaryInterface},
		`interfaces.require["cache.read/v1"]`:   {summary: applicationmeta.ConfigurationSummaryRemoval, removed: true},
		`interfaces.use["email.send/v1"]`:       {summary: applicationmeta.ConfigurationSummaryImplementation},
		`interfaces.use["cache.read/v1"]`:       {summary: applicationmeta.ConfigurationSummaryRemoval, removed: true},
		`interfaces.policies["email.send/v1"]`:  {summary: applicationmeta.ConfigurationSummaryObject},
		`interfaces.policies["audit.write/v1"]`: {summary: applicationmeta.ConfigurationSummaryRemoval, removed: true},
	}
	if len(decisions) != len(want) {
		t.Fatalf("ConfigurationDecisions = %#v", decisions)
	}
	for _, decision := range decisions {
		expected, exists := want[decision.Path()]
		if !exists || decision.Summary() != expected.summary || decision.Removed() != expected.removed || decision.Source() != "deploy/production.yaml" || !decision.DependencyComposable() || decision.Digest() == "" {
			t.Fatalf("ConfigurationDecision = %#v, expected %#v", decision, expected)
		}
	}
}

func TestParsePreservesIntrinsicImplementationChoiceForResolutionValidation(t *testing.T) {
	t.Parallel()

	manifest, err := applicationmeta.ParseSource("deploy/customer.yaml", []byte(`interfaces:
  use:
    kernel.health/v1: example.com/acme/health.New
    kernel.info/v1: {$remove: true}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := implementationChoiceStrings(manifest.ImplementationChoices()); !reflect.DeepEqual(got, []string{
		`kernel.health/v1->example.com/acme/health.New@deploy/customer.yaml interfaces.use["kernel.health/v1"]`,
	}) {
		t.Fatalf("ImplementationChoices = %v", got)
	}
	decisions, err := applicationmeta.ConfigurationDecisions(manifest, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		`interfaces.use["kernel.health/v1"]`: false,
		`interfaces.use["kernel.info/v1"]`:   true,
	}
	if len(decisions) != len(want) {
		t.Fatalf("ConfigurationDecisions = %#v", decisions)
	}
	for _, decision := range decisions {
		removed, exists := want[decision.Path()]
		if !exists || decision.Removed() != removed || decision.Source() != "deploy/customer.yaml" {
			t.Fatalf("ConfigurationDecision = %#v", decision)
		}
	}
}

func TestParseRejectsInvalidInterfaceConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "nonmapping", data: "interfaces: []\n", want: "interfaces must be a mapping"},
		{name: "unknown field", data: "interfaces: {unknown: {}}\n", want: `interfaces contains unknown key "unknown"`},
		{name: "invalid requirement", data: "interfaces: {require: [email/v1]}\n", want: "not a canonical Interface ID"},
		{name: "duplicate requirement", data: "interfaces: {require: [email.send/v1, email.send/v1]}\n", want: "duplicates Interface"},
		{name: "ambiguous sparse edit", data: "interfaces: {require: {add: [email.send/v1], remove: [email.send/v1]}}\n", want: "cannot both add and remove Interface"},
		{name: "unknown sparse edit", data: "interfaces: {require: {append: [email.send/v1]}}\n", want: "unknown sparse-edit key"},
		{name: "invalid choice key", data: "interfaces: {use: {email/v1: github.com/acme/smtp.New}}\n", want: "not a canonical Interface ID"},
		{name: "invalid constructor", data: "interfaces: {use: {email.send/v1: acme.smtp}}\n", want: "not a fully qualified constructor symbol"},
		{name: "nonstring constructor", data: "interfaces: {use: {email.send/v1: true}}\n", want: "must be a fully qualified constructor symbol or {$remove: true}"},
		{name: "policies nonmapping", data: "interfaces: {policies: []}\n", want: "interfaces.policies must be a mapping"},
		{name: "invalid policy key", data: "interfaces: {policies: {email/v1: {timeout: 1s}}}\n", want: "not a canonical Interface ID"},
		{name: "intrinsic policy", data: "interfaces: {policies: {kernel.health/v1: {timeout: 1s}}}\n", want: "intrinsic kernel.* Interface"},
		{name: "policy nonmapping", data: "interfaces: {policies: {email.send/v1: 1s}}\n", want: `interfaces.policies["email.send/v1"] must be a mapping`},
		{name: "empty policy", data: "interfaces: {policies: {email.send/v1: {}}}\n", want: `.timeout is required`},
		{name: "unknown policy field", data: "interfaces: {policies: {email.send/v1: {replay: 2}}}\n", want: `contains unknown key "replay"`},
		{name: "nonstring policy timeout", data: "interfaces: {policies: {email.send/v1: {timeout: 5}}}\n", want: "must be a non-empty trimmed Go duration string"},
		{name: "null policy timeout", data: "interfaces: {policies: {email.send/v1: {timeout: null}}}\n", want: "must be a non-empty trimmed Go duration string"},
		{name: "zero policy timeout", data: "interfaces: {policies: {email.send/v1: {timeout: 0s}}}\n", want: "must be a positive Go duration"},
		{name: "negative policy timeout", data: "interfaces: {policies: {email.send/v1: {timeout: -1s}}}\n", want: "must be a positive Go duration"},
		{name: "malformed policy timeout", data: "interfaces: {policies: {email.send/v1: {timeout: soon}}}\n", want: "must be a positive Go duration"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := applicationmeta.Parse([]byte(test.data))
			if !errors.Is(err, applicationmeta.ErrInvalidManifest) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestApplyOverlayUsesTypedSparseInterfaceSemantics(t *testing.T) {
	t.Parallel()

	base := composeManifest(t, `
interfaces:
  require: [audit.write/v1, email.send/v1]
  use:
    audit.write/v1: github.com/acme/audit.New
    email.send/v1: github.com/acme/smtp.New
  policies:
    audit.write/v1: {timeout: 1s}
    email.send/v1: {timeout: 5s}
`)
	overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(`
interfaces:
  require:
    add: [cache.read/v1]
    remove: [audit.write/v1]
  use:
    audit.write/v1: github.com/acme/auditprod.New
    email.send/v1: {$remove: true}
  policies:
    audit.write/v1: {timeout: 2s}
    email.send/v1: {$remove: true}
`))
	if err != nil {
		t.Fatal(err)
	}
	effective, err := applicationmeta.ApplyOverlay(base, overlay, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := interfaceRequirementIDs(effective.InterfaceRequirements()); !reflect.DeepEqual(got, []string{"cache.read/v1", "email.send/v1"}) {
		t.Fatalf("overlay requirements = %v", got)
	}
	if got := implementationChoiceStrings(effective.ImplementationChoices()); !reflect.DeepEqual(got, []string{
		`audit.write/v1->github.com/acme/auditprod.New@plystra.production.yaml interfaces.use["audit.write/v1"]`,
	}) {
		t.Fatalf("overlay choices = %v", got)
	}
	if got := interfacePolicyStrings(effective.InterfacePolicies()); !reflect.DeepEqual(got, []string{
		`audit.write/v1=2s@plystra.production.yaml interfaces.policies["audit.write/v1"]`,
	}) {
		t.Fatalf("overlay policies = %v", got)
	}
	composed, err := applicationmeta.Compose([]applicationmeta.Dependency{{
		ModulePath:    "example.com/dependency",
		ModuleVersion: "v1.0.0",
		Manifest: composeManifest(t, `
interfaces:
  require: [audit.write/v1]
  use:
    email.send/v1: github.com/dependency/smtp.New
  policies:
    email.send/v1: {timeout: 10s}
`),
	}}, effective, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := interfaceRequirementIDs(composed.Manifest().InterfaceRequirements()); !reflect.DeepEqual(got, []string{"cache.read/v1", "email.send/v1"}) || len(composed.Manifest().ImplementationChoices()) != 1 || composed.Manifest().ImplementationChoices()[0].Constructor().String() != "github.com/acme/auditprod.New" || !reflect.DeepEqual(interfacePolicyStrings(composed.Manifest().InterfacePolicies()), []string{`audit.write/v1=2s@plystra.production.yaml interfaces.policies["audit.write/v1"]`}) {
		t.Fatalf("overlay dependency suppression = %v / %v / %v", got, implementationChoiceStrings(composed.Manifest().ImplementationChoices()), interfacePolicyStrings(composed.Manifest().InterfacePolicies()))
	}
}

func interfaceRequirementStrings(values []applicationmeta.InterfaceRequirement) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID().String() + "@" + value.Source()
	}
	return result
}

func implementationChoiceStrings(values []applicationmeta.ImplementationChoice) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.InterfaceID().String() + "->" + value.Constructor().String() + "@" + value.Source()
	}
	return result
}

func interfacePolicyStrings(values []applicationmeta.InterfacePolicy) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.InterfaceID().String() + "=" + value.Timeout().String() + "@" + value.Source()
	}
	return result
}

func interfaceRequirementIDs(values []applicationmeta.InterfaceRequirement) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID().String()
	}
	return result
}
