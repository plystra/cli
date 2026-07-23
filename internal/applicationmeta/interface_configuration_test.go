package applicationmeta_test

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestParseNormalizesTypedInterfaceRequirementsAndImplementationChoices(t *testing.T) {
	t.Parallel()

	manifest, err := applicationmeta.ParseSource("deploy/production.yaml", []byte(`
interfaces:
  require:
    add: [email.send/v1, audit.write/v1]
    remove: [cache.read/v1]
  use:
    email.send/v1: github.com/acme/app/smtp.New
    cache.read/v1: null
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
	requirements[0] = applicationmeta.InterfaceRequirement{}
	choices[0] = applicationmeta.ImplementationChoice{}
	if manifest.InterfaceRequirements()[0].ID().String() != "audit.write/v1" || manifest.ImplementationChoices()[0].Constructor().String() != "github.com/acme/app/smtp.New" {
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
		`interfaces.require["audit.write/v1"]`: {summary: applicationmeta.ConfigurationSummaryInterface},
		`interfaces.require["email.send/v1"]`:  {summary: applicationmeta.ConfigurationSummaryInterface},
		`interfaces.require["cache.read/v1"]`:  {summary: applicationmeta.ConfigurationSummaryRemoval, removed: true},
		`interfaces.use["email.send/v1"]`:      {summary: applicationmeta.ConfigurationSummaryImplementation},
		`interfaces.use["cache.read/v1"]`:      {summary: applicationmeta.ConfigurationSummaryRemoval, removed: true},
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

func TestParseRejectsInvalidInterfaceConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "nonmapping", data: "interfaces: []\n", want: "interfaces must be a mapping"},
		{name: "unknown field", data: "interfaces: {policies: {}}\n", want: `interfaces contains unknown key "policies"`},
		{name: "invalid requirement", data: "interfaces: {require: [email/v1]}\n", want: "not a canonical Interface ID"},
		{name: "duplicate requirement", data: "interfaces: {require: [email.send/v1, email.send/v1]}\n", want: "duplicates Interface"},
		{name: "ambiguous sparse edit", data: "interfaces: {require: {add: [email.send/v1], remove: [email.send/v1]}}\n", want: "cannot both add and remove Interface"},
		{name: "unknown sparse edit", data: "interfaces: {require: {append: [email.send/v1]}}\n", want: "unknown sparse-edit key"},
		{name: "invalid choice key", data: "interfaces: {use: {email/v1: github.com/acme/smtp.New}}\n", want: "not a canonical Interface ID"},
		{name: "invalid constructor", data: "interfaces: {use: {email.send/v1: acme.smtp}}\n", want: "not a fully qualified constructor symbol"},
		{name: "nonstring constructor", data: "interfaces: {use: {email.send/v1: true}}\n", want: "must be a fully qualified constructor symbol or null"},
		{name: "intrinsic selection", data: "interfaces: {use: {kernel.health/v1: github.com/acme/health.New}}\n", want: "intrinsic kernel.* Interface"},
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

func TestComposeInterfaceConfigurationDeterministicallyWithCurrentReplacement(t *testing.T) {
	t.Parallel()

	dependencies := []applicationmeta.Dependency{
		{
			ModulePath:    "example.com/platform-a",
			ModuleVersion: "v1.0.0",
			Manifest: composeManifest(t, `
interfaces:
  require: [audit.write/v1, email.send/v1]
  use:
    email.send/v1: github.com/acme/smtp.New
`),
		},
		{
			ModulePath:    "example.com/platform-b",
			ModuleVersion: "v2.0.0",
			Manifest: composeManifest(t, `
interfaces:
  require: [email.send/v1]
  use:
    email.send/v1: github.com/acme/smtp.New
`),
		},
	}
	first, err := applicationmeta.Compose(dependencies, composeManifest(t, "{}\n"), composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	second, err := applicationmeta.Compose([]applicationmeta.Dependency{dependencies[1], dependencies[0]}, composeManifest(t, "{}\n"), composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(interfaceRequirementStrings(first.Manifest().InterfaceRequirements()), []string{
		`audit.write/v1@example.com/platform-a@v1.0.0/plystra.yaml interfaces.require["audit.write/v1"]`,
		`email.send/v1@example.com/platform-a@v1.0.0/plystra.yaml interfaces.require["email.send/v1"]`,
	}) || !reflect.DeepEqual(implementationChoiceStrings(first.Manifest().ImplementationChoices()), []string{
		`email.send/v1->github.com/acme/smtp.New@example.com/platform-a@v1.0.0/plystra.yaml interfaces.use["email.send/v1"]`,
	}) {
		t.Fatalf("composed Interface configuration = %v / %v", interfaceRequirementStrings(first.Manifest().InterfaceRequirements()), implementationChoiceStrings(first.Manifest().ImplementationChoices()))
	}
	if first.DependencyDigest() != second.DependencyDigest() || !reflect.DeepEqual(first.Provenance(), second.Provenance()) || !reflect.DeepEqual(first.ResolutionSources(), second.ResolutionSources()) {
		t.Fatalf("dependency permutation changed composition: first %#v second %#v", first.Provenance(), second.Provenance())
	}
	requirementProvenance := findProvenance(t, first.Provenance(), `interfaces.require["email.send/v1"]`)
	selectionProvenance := findProvenance(t, first.Provenance(), `interfaces.use["email.send/v1"]`)
	if len(requirementProvenance) != 1 || len(requirementProvenance[0].Sources()) != 2 || len(selectionProvenance) != 1 || len(selectionProvenance[0].Sources()) != 2 || len(findProvenance(t, first.ResolutionSources(), `interfaces.require["email.send/v1"]`)) != 1 || len(findProvenance(t, first.ResolutionSources(), `interfaces.use["email.send/v1"]`)) != 1 {
		t.Fatalf("Interface provenance = requirements %#v selections %#v resolution %#v", requirementProvenance, selectionProvenance, first.ResolutionSources())
	}

	conflicting := append([]applicationmeta.Dependency(nil), dependencies...)
	conflicting[1].Manifest = composeManifest(t, `
interfaces:
  require: [email.send/v1]
  use:
    email.send/v1: github.com/acme/memory.New
`)
	_, err = applicationmeta.Compose(conflicting, composeManifest(t, "{}\n"), composeSchemaLookup(nil))
	if !errors.Is(err, applicationmeta.ErrCompose) || !errors.Is(err, applicationmeta.ErrInheritedConflict) || !containsAllFragments(err.Error(), `interfaces.use["email.send/v1"]`, "github.com/acme/smtp.New", "github.com/acme/memory.New", "example.com/platform-a@v1.0.0", "example.com/platform-b@v2.0.0") {
		t.Fatalf("inherited selection conflict = %v", err)
	}
	current := composeManifest(t, `
interfaces:
  use:
    email.send/v1: example.com/current/local.New
`)
	resolved, err := applicationmeta.Compose(conflicting, current, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if choices := resolved.Manifest().ImplementationChoices(); len(choices) != 1 || choices[0].Constructor().String() != "example.com/current/local.New" || choices[0].Source() != `plystra.yaml interfaces.use["email.send/v1"]` || len(findProvenance(t, resolved.ResolutionSources(), `interfaces.use["email.send/v1"]`)) != 0 {
		t.Fatalf("current replacement = %#v / %#v", choices, resolved.ResolutionSources())
	}
}

func TestComposeInterfaceRequirementAddRemoveConflictNeedsCurrentDecision(t *testing.T) {
	t.Parallel()

	dependencies := []applicationmeta.Dependency{
		{
			ModulePath:    "example.com/add",
			ModuleVersion: "v1.0.0",
			Manifest:      composeManifest(t, "interfaces: {require: [audit.write/v1]}\n"),
		},
		{
			ModulePath:    "example.com/remove",
			ModuleVersion: "v1.0.0",
			Manifest:      composeManifest(t, "interfaces: {require: {remove: [audit.write/v1]}}\n"),
		},
	}
	_, err := applicationmeta.Compose(dependencies, composeManifest(t, "{}\n"), composeSchemaLookup(nil))
	if !errors.Is(err, applicationmeta.ErrInheritedConflict) || !containsAllFragments(err.Error(), `interfaces.require["audit.write/v1"]`, "example.com/add@v1.0.0", "example.com/remove@v1.0.0", "explicitly add or remove") {
		t.Fatalf("requirement conflict = %v", err)
	}
	current := composeManifest(t, "interfaces: {require: {remove: [audit.write/v1]}}\n")
	composition, err := applicationmeta.Compose(dependencies, current, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(composition.Manifest().InterfaceRequirements()) != 0 || len(findProvenance(t, composition.ResolutionSources(), `interfaces.require["audit.write/v1"]`)) != 0 || len(findProvenance(t, composition.Provenance(), `interfaces.require["audit.write/v1"]`)) != 2 {
		t.Fatalf("resolved removal = manifest %#v provenance %#v resolution %#v", composition.Manifest().InterfaceRequirements(), composition.Provenance(), composition.ResolutionSources())
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
`)
	overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(`
interfaces:
  require:
    add: [cache.read/v1]
    remove: [audit.write/v1]
  use:
    audit.write/v1: github.com/acme/auditprod.New
    email.send/v1: null
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
	composed, err := applicationmeta.Compose([]applicationmeta.Dependency{{
		ModulePath:    "example.com/dependency",
		ModuleVersion: "v1.0.0",
		Manifest: composeManifest(t, `
interfaces:
  require: [audit.write/v1]
  use:
    email.send/v1: github.com/dependency/smtp.New
`),
	}}, effective, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := interfaceRequirementIDs(composed.Manifest().InterfaceRequirements()); !reflect.DeepEqual(got, []string{"cache.read/v1", "email.send/v1"}) || len(composed.Manifest().ImplementationChoices()) != 1 || composed.Manifest().ImplementationChoices()[0].Constructor().String() != "github.com/acme/auditprod.New" {
		t.Fatalf("overlay dependency suppression = %v / %v", got, implementationChoiceStrings(composed.Manifest().ImplementationChoices()))
	}
}

func TestMaintainDependencyConfigurationPreservesLocalInterfaceIntent(t *testing.T) {
	t.Parallel()

	oldDependencies := []applicationmeta.Dependency{{
		ModulePath:    "example.com/platform",
		ModuleVersion: "v1.0.0",
		Manifest: composeManifest(t, `
interfaces:
  require: [audit.write/v1]
  use:
    email.send/v1: github.com/acme/smtp.New
`),
	}}
	initial, err := applicationmeta.MaintainDependencyConfiguration([]byte("# project configuration\n{}\n"), applicationmeta.DependencyBaseline{}, oldDependencies, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !initial.Changed() || !bytes.Contains(initial.Data(), []byte("# project configuration")) || !bytes.Contains(initial.Data(), []byte("audit.write/v1")) || !bytes.Contains(initial.Data(), []byte("github.com/acme/smtp.New")) {
		t.Fatalf("initial maintenance = changed %t\n%s", initial.Changed(), initial.Data())
	}
	oldComposition, err := applicationmeta.Compose(oldDependencies, composeManifest(t, string(initial.Data())), composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}

	locallyEdited := []byte(`# project configuration
interfaces:
  require:
    remove:
      - audit.write/v1 # explicit local removal
  use:
    email.send/v1: example.com/current/local.New # explicit local selection
`)
	newDependencies := []applicationmeta.Dependency{{
		ModulePath:    "example.com/platform",
		ModuleVersion: "v2.0.0",
		Manifest: composeManifest(t, `
interfaces:
  require: [audit.write/v1, cache.read/v1]
  use:
    authz.check/v1: github.com/acme/rbac.New
    email.send/v1: github.com/acme/smtp.New
`),
	}}
	maintained, err := applicationmeta.MaintainDependencyConfiguration(locallyEdited, oldComposition.DependencyBaseline(), newDependencies, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !maintained.Changed() {
		t.Fatal("new dependency Interface values did not update configuration")
	}
	for _, fragment := range []string{"# project configuration", "# explicit local removal", "# explicit local selection", "audit.write/v1", "cache.read/v1", "example.com/current/local.New", "authz.check/v1", "github.com/acme/rbac.New"} {
		if !bytes.Contains(maintained.Data(), []byte(fragment)) {
			t.Fatalf("maintained YAML omits %q:\n%s", fragment, maintained.Data())
		}
	}
	if bytes.Contains(maintained.Data(), []byte("github.com/acme/smtp.New")) {
		t.Fatalf("dependency selection overwrote local choice:\n%s", maintained.Data())
	}
	for _, localPath := range []string{`interfaces.require["audit.write/v1"]`, `interfaces.use["email.send/v1"]`} {
		if !slices.Contains(maintained.LocalPaths(), localPath) {
			t.Fatalf("local paths %v omit %s", maintained.LocalPaths(), localPath)
		}
	}
	manifest := composeManifest(t, string(maintained.Data()))
	composition, err := applicationmeta.Compose(newDependencies, manifest, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := interfaceRequirementIDs(composition.Manifest().InterfaceRequirements()); !reflect.DeepEqual(got, []string{"cache.read/v1"}) {
		t.Fatalf("maintained effective requirements = %v", got)
	}
	if got := implementationChoiceStrings(composition.Manifest().ImplementationChoices()); !reflect.DeepEqual(got, []string{
		`authz.check/v1->github.com/acme/rbac.New@plystra.yaml interfaces.use["authz.check/v1"]`,
		`email.send/v1->example.com/current/local.New@plystra.yaml interfaces.use["email.send/v1"]`,
	}) {
		t.Fatalf("maintained effective choices = %v", got)
	}
	repeated, err := applicationmeta.MaintainDependencyConfiguration(maintained.Data(), composition.DependencyBaseline(), newDependencies, composeSchemaLookup(nil))
	if err != nil || repeated.Changed() || !bytes.Equal(repeated.Data(), maintained.Data()) {
		t.Fatalf("repeated maintenance = changed %t error %v\n%s", repeated.Changed(), err, repeated.Data())
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

func interfaceRequirementIDs(values []applicationmeta.InterfaceRequirement) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ID().String()
	}
	return result
}

func containsAllFragments(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}
