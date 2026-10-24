package implementationselect_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/implementationselect"
)

const dependencyModule = "example.com/dependency-planning"
const dependencyOwner = dependencyModule + "/service.New"
const dependencyOtherOwner = dependencyModule + "/alternate.New"
const dependencyOperation = `{"kind":"dependency","action":"update","module":"example.com/dependency@v1.2.0"}`

type dependencyFixtureOptions struct {
	fields, parameters, providerFields, providerParameters string
	document, mode, alternateFields, dependencyManifest    string
	overlay                                                string
	onlyService, onlyAlternate                             bool
}

// Every version is independently discovered through the public boundary. No
// compiler objects or private SelectionInputs fields are fabricated or shared.
func dependencyInputs(t *testing.T, fixture dependencyFixtureOptions) applicationresolve.SelectionInputs {
	t.Helper()
	root := t.TempDir()
	writeImplementationFile(t, filepath.Join(root, "go.mod"), "module "+dependencyModule+"\n\ngo 1.26\n")
	if fixture.dependencyManifest != "" {
		writeImplementationFile(t, filepath.Join(root, "go.mod"), "module "+dependencyModule+"\n\ngo 1.26\nrequire example.com/template v0.0.0\nreplace example.com/template => ./template\n")
		writeImplementationFile(t, filepath.Join(root, "template/go.mod"), "module example.com/template\n\ngo 1.26\n")
		writeImplementationFile(t, filepath.Join(root, "template/template.go"), "package template\n")
		writeImplementationFile(t, filepath.Join(root, "template/plystra.yaml"), fixture.dependencyManifest)
	}
	writeImplementationFile(t, filepath.Join(root, "base/plystra.yaml"), "{}\n")
	for _, contract := range []struct{ name, id, method string }{{"entry", "app.main/v1", "Run"}, {"companion", "app.aux/v1", "Copy"}} {
		writeImplementationFile(t, filepath.Join(root, contract.name, "interface.go"), fmt.Sprintf(`package %s
import "context"
//plystra:interface %s
type Interface interface { %s(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct{}
`, contract.name, contract.id, contract.method))
	}
	for _, resource := range []struct{ name, id string }{{"db", "storage.database/v1"}, {"other", "storage.other/v1"}} {
		writeImplementationFile(t, filepath.Join(root, resource.name, "resource.go"), "package "+resource.name+"\n//plystra:resource "+resource.id+"\ntype Resource interface { Value() string }\n")
	}
	for _, name := range []string{"service", "alternate"} {
		if name == "alternate" && fixture.onlyService {
			continue
		}
		if name == "service" && fixture.onlyAlternate {
			continue
		}
		parameters, fields := fixture.parameters, fixture.fields
		if name == "alternate" {
			parameters, fields = "", `Name string `+"`yaml:\"name\"`"
			if fixture.alternateFields != "" {
				fields = fixture.alternateFields
			}
		}
		imports := ""
		for _, dependency := range []string{"db", "other"} {
			if strings.Contains(parameters, dependency+".Resource") {
				imports += fmt.Sprintf("%q\n", dependencyModule+"/"+dependency)
			}
		}
		if parameters != "" {
			parameters = ", " + parameters
		}
		writeImplementationFile(t, filepath.Join(root, name, "implementation.go"), fmt.Sprintf(`package %s
import (
 "context"
 mainv1 %q
 aux %q
 %s
)
type Config struct { %s }
type Service struct{}
//plystra:implements app.main/v1
//plystra:implements app.aux/v1
func New(c Config%s) (*Service,error) { panic("PRIVATE_CONSTRUCTOR_ENTRY") }
func (*Service) Run(context.Context,mainv1.Request)(mainv1.Response,error){return mainv1.Response{},nil}
func (*Service) Copy(context.Context,aux.Request)(aux.Response,error){return aux.Response{},nil}
`, name, dependencyModule+"/entry", dependencyModule+"/companion", imports, fields, parameters))
	}
	for _, provider := range []struct{ name, resource, fields, parameters string }{
		{"provider", "db", fixture.providerFields, fixture.providerParameters},
		{"leaf", "db", `Name string ` + "`yaml:\"name\"`", ""},
		{"otherleaf", "other", "", ""},
	} {
		id := "storage.database/v1"
		if provider.resource == "other" {
			id = "storage.other/v1"
		}
		imports, parameters := "", provider.parameters
		for _, dependency := range []string{"db", "other"} {
			if strings.Contains(parameters, dependency+".Resource") {
				imports += fmt.Sprintf("import %q\n", dependencyModule+"/"+dependency)
			}
		}
		if parameters != "" {
			parameters = ", " + parameters
		}
		writeImplementationFile(t, filepath.Join(root, provider.name, "provider.go"), fmt.Sprintf(`package %s
%s
type Config struct { %s }
type value struct{}
//plystra:implements-resource %s
func New(c Config%s) (*value,error) { panic("PRIVATE_PROVIDER_ENTRY") }
func (*value) Value() string { return "" }
`, provider.name, imports, provider.fields, id, parameters))
	}
	options := applicationresolve.Options{Start: root, Environment: implementationTestEnvironment()}
	selected := "plystra.yaml"
	switch fixture.mode {
	case "environment":
		options.EnvironmentName, selected = "test", "plystra.test.yaml"
		writeImplementationFile(t, filepath.Join(root, "plystra.yaml"), fixture.document)
		overlay := fixture.overlay
		if overlay == "" {
			overlay = "# Sparse overlay.\n{}\n"
		}
		writeImplementationFile(t, filepath.Join(root, selected), overlay)
	case "replacement":
		options.ConfigurationPath, selected = "deploy/app.yaml", "deploy/app.yaml"
		writeImplementationFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [unknown.root/v1]}\n")
		writeImplementationFile(t, filepath.Join(root, selected), fixture.document)
	default:
		writeImplementationFile(t, filepath.Join(root, selected), fixture.document)
	}
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	return inputs
}

func dependencyRequest(t *testing.T, operations ...string) changerequest.Request {
	t.Helper()
	request, err := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[` + strings.Join(operations, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func dependencySelection(owner string) string {
	return `{"kind":"implementation_selection","action":"set","interface":"app.main/v1","constructor":"` + owner + `"}`
}

func dependencyDocument() string {
	return "interfaces: {require: [app.main/v1], use: {app.main/v1: " + dependencyOwner + "}}\n"
}

func TestPreviewDependencyConfigurationsComparesSameSymbolSchemas(t *testing.T) {
	t.Parallel()
	oldFields := "Name string `yaml:\"name\" plystra-default:\"PRIVATE_OLD_NAME\"`; Nested struct { Value string `yaml:\"value\"` } `yaml:\"nested\"`; Hidden string `yaml:\"hidden\" plystra-default:\"PRIVATE_OLD_DEFAULT\"`"
	newFields := "Name string `yaml:\"name\" plystra:\"required\"`; Added string `yaml:\"added\" plystra:\"required\"`; Nested struct { Value string `yaml:\"value\" plystra:\"required\"` } `yaml:\"nested\"`; Hidden string `yaml:\"hidden\" plystra:\"build-visible\" plystra-default:\"PRIVATE_NEW_DEFAULT\"`; Pointer *struct { Value string `yaml:\"value\" plystra:\"required\"` } `yaml:\"pointer\"`"
	old := dependencyInputs(t, dependencyFixtureOptions{fields: oldFields, document: dependencyDocument()})
	current := dependencyInputs(t, dependencyFixtureOptions{fields: newFields, document: dependencyDocument()})
	operations := []string{dependencyOperation, dependencySelection(dependencyOwner),
		`{"kind":"configuration","action":"set","owner":"` + dependencyOwner + `","path":"name","value":"PRIVATE_NAME"}`,
		`{"kind":"configuration","action":"set","owner":"` + dependencyOwner + `","path":"added","value":"PRIVATE_ADDED"}`,
		`{"kind":"configuration","action":"set","owner":"` + dependencyOwner + `","path":"nested.value","value":"PRIVATE_NESTED"}`,
	}
	plan, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, dependencyRequest(t, operations...))
	if err != nil {
		t.Fatal(err)
	}
	fields := plan.ConfigurationImpact().NewlyRequiredFields()
	var paths []string
	for _, field := range fields {
		paths = append(paths, field.Path)
		if field.Owner != dependencyOwner || field.SchemaSource == "" || field.DefaultSatisfies || field.RequirednessReason != "newly-required-field-in-constructor-schema" {
			t.Fatalf("incorrect required schema evidence: %#v", field)
		}
	}
	want := []string{`config["` + dependencyOwner + `"]["added"]`, `config["` + dependencyOwner + `"]["name"]`, `config["` + dependencyOwner + `"]["nested"]["value"]`}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("required fields = %v, want %v", paths, want)
	}
	if strings.Contains(string(plan.ConfigurationImpact().CanonicalJSON()), "PRIVATE_") {
		t.Fatal("private values or compiled defaults leaked into impact")
	}
	if !plan.Changed() || plan.Inputs().Module().Path() != current.Module().Path() || plan.CompositionDigest() == "" {
		t.Fatal("missing current snapshot, final identity or write intent")
	}
	for left, right := 0, len(operations)-1; left < right; left, right = left+1, right-1 {
		operations[left], operations[right] = operations[right], operations[left]
	}
	again, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, dependencyRequest(t, operations...))
	if err != nil || !bytes.Equal(plan.Updated(), again.Updated()) || !bytes.Equal(plan.ConfigurationImpact().CanonicalJSON(), again.ConfigurationImpact().CanonicalJSON()) {
		t.Fatalf("operation order changed the complete plan: %v", err)
	}
	if _, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, dependencyRequest(t, dependencyOperation, dependencySelection(dependencyOwner))); !errors.Is(err, applicationmeta.ErrConfigurationRequired) {
		t.Fatalf("missing required fields produced a partial plan: %v", err)
	}
}

func TestPreviewDependencyConfigurationsRepairsOldStateAndPreservesOwners(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			oldDocument := dependencyDocument() + "config: {" + dependencyOwner + ": {name: [PRIVATE_INVALID]}}\nresources:\n  instances: {database: {use: " + dependencyModule + "/leaf.New}}\n  bind:\n    implementations:\n      " + dependencyOwner + ": {primary: missing}\n"
			if mode == "environment" {
				// Typed lower layers remain mandatory even when suppressed by
				// the final overlay. Binding validity is a final graph concern.
				oldDocument = strings.ReplaceAll(oldDocument, "[PRIVATE_INVALID]", "PRIVATE_VALID")
			}
			fixture := dependencyFixtureOptions{fields: "Name string `yaml:\"name\"`", parameters: "primary db.Resource", document: oldDocument, mode: mode}
			old, current := dependencyInputs(t, fixture), dependencyInputs(t, fixture)
			before := implementationProjectTree(t, current.Module().Path())
			request := dependencyRequest(t, dependencyOperation, dependencySelection(dependencyOtherOwner))
			plan, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, request)
			if err != nil {
				t.Fatal(err)
			}
			impact := plan.ConfigurationImpact()
			if len(impact.NewlyUnownedPaths()) != 2 || len(impact.DerivedOperations()) != 2 || len(impact.RetainedOwners()) != 0 || len(impact.UnresolvedDecisions()) != 0 {
				t.Fatalf("lost config/binding impact = %s", impact.CanonicalJSON())
			}
			if mode == "environment" && (!bytes.Contains(plan.Updated(), []byte("$remove: true")) || bytes.Contains(plan.Updated(), []byte("PRIVATE_INVALID"))) {
				t.Fatal("inherited cleanup did not stay sparse")
			}
			if !equalImplementationTrees(before, implementationProjectTree(t, current.Module().Path())) {
				t.Fatal("preview wrote captured Project files")
			}
			unknown, err := implementationselect.PreviewDependencyConfigurations(t.Context(), applicationresolve.SelectionInputs{}, current, request)
			if err != nil || !bytes.Equal(unknown.Updated(), plan.Updated()) {
				t.Fatalf("authored explicit owner failed cleanup with unknown previous declarations: %v", err)
			}
		})
	}
}

func TestPreviewDependencyConfigurationsExactParameterChanges(t *testing.T) {
	t.Parallel()
	document := dependencyDocument() + "resources:\n  instances: {database: {use: " + dependencyModule + "/leaf.New}, other: {use: " + dependencyModule + "/otherleaf.New}}\n  bind:\n    implementations:\n      " + dependencyOwner + ": {primary: database}\n"
	oldFixture := dependencyFixtureOptions{parameters: "primary db.Resource", document: document}
	old := dependencyInputs(t, oldFixture)
	for _, test := range []struct {
		name, parameters string
		removed          bool
	}{
		{"removed", "", true}, {"retained", "primary db.Resource", false}, {"changed-contract", "primary other.Resource", false}, {"changed-case", "Primary db.Resource", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := oldFixture
			fixture.parameters = test.parameters
			current := dependencyInputs(t, fixture)
			plan, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, dependencyRequest(t, dependencyOperation))
			if test.name == "changed-contract" {
				if err == nil {
					t.Fatal("retargeted a retained wrong-contract binding")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := len(plan.ConfigurationImpact().DerivedOperations()); got != map[bool]int{false: 0, true: 1}[test.removed] {
				t.Fatalf("parameter cleanup count = %d", got)
			}
			if test.removed {
				if _, err := implementationselect.PreviewDependencyConfigurations(t.Context(), applicationresolve.SelectionInputs{}, current, dependencyRequest(t, dependencyOperation)); err == nil {
					t.Fatal("unknown old parameter established cleanup")
				}
			}
		})
	}
}

func TestPreviewDependencyConfigurationsSameSymbolResourceSchemaAndParameters(t *testing.T) {
	t.Parallel()
	document := "resources:\n  instances: {primary: {use: " + dependencyModule + "/provider.New, config: {name: [PRIVATE_INVALID]}}, database: {use: " + dependencyModule + "/leaf.New}}\n  bind:\n    instances:\n      primary: {upstream: missing}\n"
	old := dependencyInputs(t, dependencyFixtureOptions{document: document, providerParameters: "upstream db.Resource", providerFields: "Name string `yaml:\"name\"`"})
	current := dependencyInputs(t, dependencyFixtureOptions{document: document, providerFields: "Name string `yaml:\"name\" plystra:\"required\"`"})
	request := dependencyRequest(t, dependencyOperation,
		`{"kind":"configuration","action":"set","owner":"primary","path":"name","value":"PRIVATE_NEW"}`)
	plan, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, request)
	if err != nil {
		t.Fatal(err)
	}
	impact := plan.ConfigurationImpact()
	if len(impact.NewlyRequiredFields()) != 1 || impact.NewlyRequiredFields()[0].Path != `resources.instances["primary"].config["name"]` || impact.NewlyRequiredFields()[0].SchemaSource == "" || len(impact.DerivedOperations()) != 1 || len(impact.RetainedOwners()) != 1 {
		t.Fatalf("same-symbol provider impact = %s", impact.CanonicalJSON())
	}
	if _, err := implementationselect.PreviewDependencyConfigurations(t.Context(), applicationresolve.SelectionInputs{}, current, request); err == nil {
		t.Fatal("unknown old provider proved parameter cleanup")
	}
}

func TestPreviewDependencyConfigurationsPureDependencyAndWriteIntent(t *testing.T) {
	t.Parallel()
	document := dependencyDocument() + "config: {" + dependencyOwner + ": {name: PRIVATE_NAME}}\n"
	fixture := dependencyFixtureOptions{fields: "Name string `yaml:\"name\"`", document: document}
	old, current := dependencyInputs(t, fixture), dependencyInputs(t, fixture)
	for _, previous := range []applicationresolve.SelectionInputs{old, {}} {
		plan, err := implementationselect.PreviewDependencyConfigurations(t.Context(), previous, current, dependencyRequest(t, dependencyOperation))
		if err != nil {
			t.Fatal(err)
		}
		if plan.Changed() || !bytes.Equal(plan.Original(), plan.Updated()) || len(plan.ConfigurationImpact().NewlyRequiredFields()) != 0 || len(plan.ConfigurationImpact().UnresolvedDecisions()) != 0 {
			t.Fatal("pure dependency invented configuration changes or decisions")
		}
		set := `{"kind":"configuration","action":"set","owner":"` + dependencyOwner + `","path":"name","value":"PRIVATE_NAME"}`
		plan, err = implementationselect.PreviewDependencyConfigurations(t.Context(), previous, current, dependencyRequest(t, dependencyOperation, set))
		if err != nil || !plan.Changed() {
			t.Fatalf("equal private set lost write intent: %v", err)
		}
	}
}

func TestPreviewDependencyConfigurationsValidatesRequestAndCapturedInputs(t *testing.T) {
	t.Parallel()
	fixture := dependencyFixtureOptions{document: dependencyDocument()}
	old, current := dependencyInputs(t, fixture), dependencyInputs(t, fixture)
	request := dependencyRequest(t, dependencyOperation, dependencySelection(dependencyOwner))
	for _, test := range []struct {
		name    string
		ctx     context.Context
		inputs  applicationresolve.SelectionInputs
		request changerequest.Request
	}{
		{"nil-context", nil, current, request},
		{"empty-inputs", t.Context(), applicationresolve.SelectionInputs{}, request},
		{"empty-request", t.Context(), current, changerequest.Request{}},
		{"no-dependency", t.Context(), current, dependencyRequest(t, dependencySelection(dependencyOwner))},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := implementationselect.PreviewDependencyConfigurations(test.ctx, old, test.inputs, test.request); !errors.Is(err, implementationselect.ErrSelect) {
				t.Fatalf("invalid API input = %v", err)
			}
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := implementationselect.PreviewDependencyConfigurations(canceled, old, current, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	// Poison source and document files after discovery. Planning must use only
	// the captured inputs; the transaction separately owns snapshot rechecks.
	writeImplementationFile(t, filepath.Join(current.Module().Path(), "service/implementation.go"), "invalid Go\n")
	writeImplementationFile(t, filepath.Join(current.Module().Path(), "plystra.yaml"), "[invalid YAML\n")
	if _, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, request); err != nil {
		t.Fatalf("planner rediscovered captured inputs: %v", err)
	}
}

func TestPreviewDependencyConfigurationsResourceRemovalKeepsIncomingBindings(t *testing.T) {
	t.Parallel()
	document := dependencyDocument() + "resources:\n  instances: {database: {use: " + dependencyModule + "/leaf.New}, wrapper: {use: " + dependencyModule + "/provider.New}}\n  bind:\n    implementations:\n      " + dependencyOwner + ": {primary: wrapper}\n    instances:\n      wrapper: {upstream: database}\n"
	fixture := dependencyFixtureOptions{document: document, parameters: "primary db.Resource", providerParameters: "upstream db.Resource"}
	old, current := dependencyInputs(t, fixture), dependencyInputs(t, fixture)
	remove := `{"kind":"resource_selection","action":"remove","instance":"wrapper"}`
	if _, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, dependencyRequest(t, dependencyOperation, remove)); err == nil {
		t.Fatal("removed or retargeted surviving incoming binding")
	} else if !errors.Is(err, constructorgraph.ErrInvalidResourceBinding) {
		t.Fatalf("unexpected final dangling-binding error: %v", err)
	}
	plan, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, dependencyRequest(t, dependencyOperation, remove, dependencySelection(dependencyOtherOwner)))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ConfigurationImpact().DerivedOperations()) != 2 {
		t.Fatalf("removed-consumer outgoing cleanup = %s", plan.ConfigurationImpact().CanonicalJSON())
	}
	unknown, err := implementationselect.PreviewDependencyConfigurations(t.Context(), applicationresolve.SelectionInputs{}, current, dependencyRequest(t, dependencyOperation, remove, dependencySelection(dependencyOtherOwner)))
	if err != nil || !bytes.Equal(unknown.Updated(), plan.Updated()) {
		t.Fatalf("authored removed consumers failed cleanup without old declarations: %v", err)
	}
}

func TestPreviewDependencyConfigurationsSameRequestRetainsDormantOwner(t *testing.T) {
	t.Parallel()
	document := dependencyDocument() + "config: {" + dependencyOwner + ": {name: PRIVATE_RETAINED}}\nresources:\n  instances: {database: {use: " + dependencyModule + "/leaf.New}}\n  bind:\n    implementations:\n      " + dependencyOwner + ": {primary: database}\n"
	fixture := dependencyFixtureOptions{fields: "Name string `yaml:\"name\"`", parameters: "primary db.Resource", document: document}
	old, current := dependencyInputs(t, fixture), dependencyInputs(t, fixture)
	plan, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, dependencyRequest(t, dependencyOperation,
		dependencySelection(dependencyOtherOwner),
		`{"kind":"interface_root","action":"absent","interface":"app.main/v1","root":"require"}`,
		`{"kind":"interface_root","action":"present","interface":"app.main/v1","root":"expose"}`,
		`{"kind":"implementation_selection","action":"set","interface":"app.aux/v1","constructor":"`+dependencyOwner+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	impact := plan.ConfigurationImpact()
	if len(impact.DerivedOperations()) != 0 || len(impact.NewlyUnownedPaths()) != 0 || len(impact.RetainedOwners()) != 1 || !bytes.Contains(plan.Updated(), []byte("PRIVATE_RETAINED")) {
		t.Fatalf("same-request dormant choice lost its owned paths: %s", impact.CanonicalJSON())
	}
	removeField := `{"kind":"configuration","action":"remove","owner":"` + dependencyOwner + `","path":"name"}`
	if _, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, dependencyRequest(t, dependencyOperation, dependencySelection(dependencyOtherOwner), removeField)); err == nil {
		t.Fatal("accepted a field removal on a disappearing owner")
	}
}

func TestPreviewDependencyConfigurationsUnknownClosureCannotProveNewReachability(t *testing.T) {
	t.Parallel()
	for _, incomplete := range []bool{false, true} {
		t.Run(fmt.Sprint(incomplete), func(t *testing.T) {
			document := "{}\n"
			if incomplete {
				document = "interfaces: {require: [unknown.root/v1]}\n"
			}
			fixture := dependencyFixtureOptions{document: document, alternateFields: "Name string `yaml:\"name\" plystra:\"required\"`"}
			old, current := dependencyInputs(t, fixture), dependencyInputs(t, fixture)
			request := dependencyRequest(t, dependencyOperation, dependencySelection(dependencyOtherOwner),
				`{"kind":"interface_root","action":"absent","interface":"unknown.root/v1","root":"require"}`,
				`{"kind":"interface_root","action":"present","interface":"app.main/v1","root":"require"}`,
				`{"kind":"configuration","action":"set","owner":"`+dependencyOtherOwner+`","path":"name","value":"PRIVATE_REQUIRED"}`)
			plan, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, request)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if incomplete {
				want = 0
			}
			if got := len(plan.ConfigurationImpact().NewlyRequiredFields()); got != want {
				t.Fatalf("newly reachable fields = %d, want %d (incomplete old closure: %v)", got, want, incomplete)
			}
		})
	}
}

func TestPreviewDependencyConfigurationsUsesOldAutomaticOwnershipOnly(t *testing.T) {
	t.Parallel()
	document := "interfaces: {require: [app.main/v1]}\nconfig: {" + dependencyOwner + ": {name: PRIVATE_OLD}}\n"
	fixture := dependencyFixtureOptions{fields: "Name string `yaml:\"name\"`", document: document, onlyService: true}
	old := dependencyInputs(t, fixture)
	fixture.onlyService = false
	current := dependencyInputs(t, fixture)
	request := dependencyRequest(t, dependencyOperation, dependencySelection(dependencyOtherOwner))
	plan, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ConfigurationImpact().DerivedOperations()) != 1 {
		t.Fatalf("old automatic owner lost its proof across versions: %s", plan.ConfigurationImpact().CanonicalJSON())
	}
	if _, err := implementationselect.PreviewDependencyConfigurations(t.Context(), applicationresolve.SelectionInputs{}, current, request); err == nil {
		t.Fatal("new declarations were treated as proof of old automatic ownership")
	}
}

func TestPreviewDependencyConfigurationsProviderReplacementUsesOldParameters(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			document := "resources:\n  instances: {primary: {use: " + dependencyModule + "/provider.New, config: {name: PRIVATE_OLD}}, database: {use: " + dependencyModule + "/leaf.New}}\n  bind:\n    instances:\n      primary: {upstream: missing}\n"
			fixture := dependencyFixtureOptions{document: document, mode: mode, providerParameters: "upstream db.Resource", providerFields: "Name string `yaml:\"name\"`"}
			old, current := dependencyInputs(t, fixture), dependencyInputs(t, fixture)
			request := dependencyRequest(t, dependencyOperation,
				`{"kind":"resource_selection","action":"set","instance":"primary","provider":"`+dependencyModule+`/leaf.New"}`,
				`{"kind":"configuration","action":"set","owner":"primary","path":"name","value":"PRIVATE_NEW"}`)
			plan, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, request)
			if err != nil {
				t.Fatal(err)
			}
			impact := plan.ConfigurationImpact()
			want := 2
			if mode == "environment" {
				want = 1 // Root Config is suppressed by the provider boundary.
			}
			if len(impact.NewlyUnownedPaths()) != 2 || len(impact.DerivedOperations()) != want || bytes.Contains(plan.Updated(), []byte("PRIVATE_OLD")) {
				t.Fatalf("provider replacement impact = %s", impact.CanonicalJSON())
			}
			if mode == "environment" && !bytes.Contains(plan.Updated(), []byte("$remove: true")) {
				t.Fatal("inherited obsolete binding did not receive an exact tombstone")
			}
			if _, err := implementationselect.PreviewDependencyConfigurations(t.Context(), applicationresolve.SelectionInputs{}, current, request); err == nil {
				t.Fatal("provider replacement guessed obsolete parameters without old declarations")
			}
		})
	}
}

func TestPreviewDependencyConfigurationsDependencyManifestCannotProveOldOwnership(t *testing.T) {
	t.Parallel()
	template := "interfaces: {use: {app.main/v1: " + dependencyOwner + "}}\nresources: {instances: {ghost: {use: " + dependencyModule + "/provider.New}}}\n"
	for _, consumer := range []string{"implementation", "instance"} {
		t.Run(consumer, func(t *testing.T) {
			document := "interfaces: {require: [app.main/v1]}\nconfig: {" + dependencyOwner + ": {name: PRIVATE_UNPROVEN}}\n"
			operations := []string{dependencyOperation, dependencySelection(dependencyOtherOwner)}
			if consumer == "instance" {
				document = "resources: {bind: {instances: {ghost: {upstream: missing}}}}\n"
				operations = []string{dependencyOperation, `{"kind":"resource_selection","action":"remove","instance":"ghost"}`}
			}
			current := dependencyInputs(t, dependencyFixtureOptions{document: document, fields: "Name string `yaml:\"name\"`", dependencyManifest: template})
			plan, err := implementationselect.PreviewDependencyConfigurations(t.Context(), applicationresolve.SelectionInputs{}, current, dependencyRequest(t, operations...))
			if err == nil {
				t.Fatalf("a dependency's template content proved old %s ownership: %s", consumer, plan.ConfigurationImpact().CanonicalJSON())
			}
		})
	}
}

func TestPreviewDependencyConfigurationsExcludedLiteralIsNotOldOwner(t *testing.T) {
	t.Parallel()
	for _, consumer := range []string{"implementation", "instance"} {
		t.Run(consumer, func(t *testing.T) {
			document := dependencyDocument() + "config: {" + dependencyOwner + ": {name: PRIVATE_UNPROVEN}}\n"
			overlay := "interfaces: {use: {app.main/v1: {$remove: true}}}\n"
			operations := []string{dependencyOperation, dependencySelection(dependencyOtherOwner)}
			if consumer == "instance" {
				document = "resources:\n  instances: {ghost: {use: " + dependencyModule + "/provider.New}}\n  bind: {instances: {ghost: {upstream: missing}}}\n"
				overlay = "resources: {instances: {ghost: {$remove: true}}}\n"
				operations = []string{dependencyOperation, `{"kind":"resource_selection","action":"remove","instance":"ghost"}`}
			}
			current := dependencyInputs(t, dependencyFixtureOptions{document: document, fields: "Name string `yaml:\"name\"`", mode: "environment", overlay: overlay})
			if _, err := implementationselect.PreviewDependencyConfigurations(t.Context(), applicationresolve.SelectionInputs{}, current, dependencyRequest(t, operations...)); err == nil {
				t.Fatalf("an excluded lower-layer %s proved old ownership", consumer)
			}
		})
	}
}

func TestPreviewDependencyConfigurationsDependencyOnlyCleansLostAutomaticOwner(t *testing.T) {
	t.Parallel()
	document := "interfaces: {require: [app.main/v1]}\nconfig: {" + dependencyOwner + ": {name: PRIVATE_OLD}}\n"
	fixture := dependencyFixtureOptions{fields: "Name string `yaml:\"name\"`", document: document, onlyService: true}
	old := dependencyInputs(t, fixture)
	fixture.onlyService, fixture.onlyAlternate = false, true
	current := dependencyInputs(t, fixture)
	request := dependencyRequest(t, dependencyOperation)
	plan, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, request)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Changed() || bytes.Contains(plan.Updated(), []byte(dependencyOwner)) || len(plan.ConfigurationImpact().DerivedOperations()) != 1 {
		t.Fatalf("dependency-only automatic cleanup = %s", plan.ConfigurationImpact().CanonicalJSON())
	}
	if _, err := implementationselect.PreviewDependencyConfigurations(t.Context(), applicationresolve.SelectionInputs{}, current, request); err == nil {
		t.Fatal("unknown old automatic owner authorized dependency-only cleanup")
	}
}
