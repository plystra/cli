package applicationmeta_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestCompositionAndMaintenanceAreDeterministicAcrossAllDependencyPermutations(t *testing.T) {
	t.Parallel()

	lookup := permutationSchemaLookup(t)
	dependencies := permutationDependencies(t)
	orders := dependencyPermutations(dependencies)
	if len(orders) != 24 {
		t.Fatalf("dependency permutations = %d, want 24", len(orders))
	}

	currentLayers := []struct {
		name     string
		manifest applicationmeta.Manifest
	}{
		{name: "default", manifest: permutationDefaultManifest(t)},
		{name: "environment", manifest: permutationEnvironmentManifest(t, lookup)},
		{name: "full replacement", manifest: permutationFullReplacementManifest(t)},
	}
	for _, layer := range currentLayers {
		layer := layer
		t.Run(layer.name, func(t *testing.T) {
			var expected []string
			for index, ordered := range orders {
				composition, err := applicationmeta.Compose(ordered, layer.manifest, lookup)
				if err != nil {
					t.Fatalf("Compose(permutation %d): %v", index, err)
				}
				actual := compositionSnapshot(composition)
				if index == 0 {
					expected = actual
					continue
				}
				if !reflect.DeepEqual(actual, expected) {
					t.Fatalf("permutation %d changed composition:\nwant: %#v\ngot:  %#v", index, expected, actual)
				}
			}
		})
	}

	assertMaintenancePermutationDeterminism(t, lookup)
	assertConflictPermutationDeterminism(t, lookup)
}

func permutationDependencies(t testing.TB) []applicationmeta.Dependency {
	t.Helper()
	return []applicationmeta.Dependency{
		{
			ModulePath:    "example.com/platform/a",
			ModuleVersion: "v1.2.0",
			Manifest: composeManifest(t, `
http:
  expose: [reports.read/v1, email.send/v1]
capabilities:
  require: [inventory.read/v1, audit.write/v1]
  use: {email.send/v1: acme.smtp.dependency-a}
  aliases: {mail.send/v1: email.send/v1}
config:
  example.com/acme/smtp.New:
    endpoint: a.example
    settings: {mode: dependency-a, region: global}
    token: {env: A_TOKEN}
`),
		},
		{
			ModulePath:    "example.com/platform/b",
			ModuleVersion: "v2.0.0",
			Manifest: composeManifest(t, `
config:
  example.com/acme/smtp.New:
    port: 587
    settings:
      region: global
      mode: dependency-b
    endpoint: b.example
capabilities:
  aliases:
    mail.send/v1: email.deliver/v1
  use:
    email.send/v1: acme.smtp.dependency-b
  require: [audit.write/v1]
http: {expose: [email.send/v1]}
`),
		},
		{
			ModulePath:    "example.com/platform/c",
			ModuleVersion: "v0.9.0",
			Manifest: composeManifest(t, `
http: {expose: [order.create/v1]}
capabilities:
  require: [billing.charge/v1]
config:
  example.com/acme/smtp.New:
    settings:
      legacy: dependency-value
      zone: 3
`),
		},
		{
			ModulePath:    "example.com/platform/d",
			ModuleVersion: "v3.1.4",
			Manifest: composeManifest(t, `
capabilities:
  require: {remove: [inventory.read/v1]}
config:
  example.com/acme/smtp.New:
    settings: {legacy: null}
http:
  expose: {remove: [reports.read/v1]}
`),
		},
	}
}

func permutationDefaultManifest(t testing.TB) applicationmeta.Manifest {
	t.Helper()
	return composeManifest(t, `
http:
  address: ":8080"
  transports: {connect: true, rest: false}
  cors:
    allowed_origins: [https://app.example, https://admin.example]
    allow_credentials: true
  expose:
    add: [kernel.health/v1]
    remove: [reports.read/v1]
timeouts: {startup: 7s}
capabilities:
  require:
    add: [kernel.info/v1]
    remove: [inventory.read/v1]
  use: {email.send/v1: acme.smtp.local}
  aliases: {mail.send/v1: email.send/v1}
config:
  example.com/acme/smtp.New:
    endpoint: current.example
    settings: {legacy: null, mode: current}
    token: null
`)
}

func permutationEnvironmentManifest(t testing.TB, lookup applicationmeta.SchemaLookup) applicationmeta.Manifest {
	t.Helper()
	base := permutationDefaultManifest(t)
	overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(`
http:
  address: ":8443"
  transports: {rest: true}
  expose: {add: [status.read/v1]}
capabilities:
  use: {email.send/v1: acme.smtp.production}
config:
  example.com/acme/smtp.New:
    endpoint: production.example
    settings: {mode: production}
`))
	if err != nil {
		t.Fatalf("ParseOverlaySource: %v", err)
	}
	combined, err := applicationmeta.ApplyOverlay(base, overlay, lookup)
	if err != nil {
		t.Fatalf("ApplyOverlay: %v", err)
	}
	return combined
}

func permutationFullReplacementManifest(t testing.TB) applicationmeta.Manifest {
	t.Helper()
	return composeManifest(t, `
http:
  address: ":9000"
  expose: {remove: [reports.read/v1]}
capabilities:
  require: {remove: [inventory.read/v1]}
  use: {email.send/v1: acme.smtp.customer}
  aliases: {mail.send/v1: email.send/v1}
config:
  example.com/acme/smtp.New:
    endpoint: customer.example
    settings: {legacy: null, mode: customer}
    token: null
`)
}

func permutationSchemaLookup(t testing.TB) applicationmeta.SchemaLookup {
	t.Helper()
	schema := composeSchema(t, `
	Endpoint string
	Port int64
	Settings struct {
		Mode string
		Region string
		Legacy string
		Zone int64
	}
	Token configuration.Secret
`)
	return composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
}

func compositionSnapshot(composition applicationmeta.Composition) []string {
	manifest := composition.Manifest()
	address, hasAddress := manifest.HTTPAddress()
	transports := manifest.HTTPTransports()
	cors, hasCORS := manifest.HTTPCORS()
	result := []string{
		"dependency-digest=" + composition.DependencyDigest(),
		fmt.Sprintf("address=%q/%t", address, hasAddress),
		fmt.Sprintf("transports=connect:%t/rest:%t", transports.Connect, transports.REST),
		fmt.Sprintf("cors=%q/%t/%t", cors.AllowedOrigins, cors.AllowCredentials, hasCORS),
		"startup=" + manifest.StartupTimeout().String(),
	}
	for _, record := range composition.DependencyBaseline().Records() {
		result = append(result, fmt.Sprintf("baseline=%s:%s:%t@%s", record.Path, record.Digest, record.Removed, strings.Join(record.Sources, ",")))
	}
	for _, exposure := range manifest.HTTPExposures() {
		result = append(result, "exposure="+exposure.ID().String()+"@"+exposure.Source())
	}
	for _, requirement := range manifest.Requirements() {
		result = append(result, "requirement="+requirement.ID().String()+"@"+requirement.Source())
	}
	for _, choice := range manifest.ProviderChoices() {
		result = append(result, "provider="+choice.Capability().String()+"="+choice.PluginID()+"@"+choice.Source())
	}
	for _, alias := range manifest.Aliases() {
		exposure, explicit := alias.Exposure()
		result = append(result, fmt.Sprintf("alias=%s=%s:%v/%t:%s@%s", alias.ID(), alias.Target(), exposure, explicit, alias.Deprecated(), alias.Source()))
	}
	for _, configured := range manifest.Configurations() {
		result = append(result, fmt.Sprintf("config=%s@%s:%q", configured.Constructor(), configured.Source(), configured.YAML()))
	}
	return result
}

func assertMaintenancePermutationDeterminism(t testing.TB, lookup applicationmeta.SchemaLookup) {
	t.Helper()
	oldDependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "http: {expose: [email.send/v1]}\ncapabilities: {require: [audit.write/v1]}\n")},
		{ModulePath: "example.com/b", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {require: [inventory.read/v1], use: {email.send/v1: acme.smtp}, aliases: {mail.send/v1: email.send/v1}}\nconfig: {example.com/acme/smtp.New: {endpoint: dependency.example}}\n")},
		{ModulePath: "example.com/c", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {require: [audit.write/v1], use: {email.send/v1: acme.smtp}, aliases: {mail.send/v1: email.send/v1}}\nconfig: {example.com/acme/smtp.New: {endpoint: dependency.example}}\n")},
		{ModulePath: "example.com/d", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "config: {example.com/acme/smtp.New: {port: 587}}\n")},
	}
	data := []byte("# current-project comment\nhttp:\n  address: \":8080\" # retained\n  expose: [kernel.health/v1]\ncapabilities:\n  require: [kernel.info/v1]\n")
	var maintained []byte
	for index, ordered := range dependencyPermutations(oldDependencies) {
		result, err := applicationmeta.MaintainDependencyConfiguration(data, applicationmeta.DependencyBaseline{}, ordered, lookup)
		if err != nil {
			t.Fatalf("initial maintenance permutation %d: %v", index, err)
		}
		if index == 0 {
			maintained = result.Data()
			continue
		}
		if !reflect.DeepEqual(result.Data(), maintained) {
			t.Fatalf("initial maintenance permutation %d changed YAML:\nwant:\n%s\ngot:\n%s", index, maintained, result.Data())
		}
	}

	maintainedManifest := composeManifest(t, string(maintained))
	previousComposition, err := applicationmeta.Compose(oldDependencies, maintainedManifest, lookup)
	if err != nil {
		t.Fatalf("Compose previous maintenance baseline: %v", err)
	}
	newDependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/a", ModuleVersion: "v1.1.0", Manifest: composeManifest(t, "capabilities: {require: [audit.write/v1, billing.charge/v1]}\n")},
		{ModulePath: "example.com/b", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {use: {email.send/v1: acme.smtp}, aliases: {mail.send/v1: email.send/v1}}\nconfig: {example.com/acme/smtp.New: {endpoint: dependency.example}}\n")},
		{ModulePath: "example.com/c", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {require: [audit.write/v1], use: {email.send/v1: acme.smtp}, aliases: {mail.send/v1: email.send/v1}}\nconfig: {example.com/acme/smtp.New: {endpoint: dependency.example}}\n")},
		{ModulePath: "example.com/d", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "config: {example.com/acme/smtp.New: {port: 587}}\n")},
	}
	var recomposed []byte
	for index, ordered := range dependencyPermutations(newDependencies) {
		result, err := applicationmeta.MaintainDependencyConfiguration(maintained, previousComposition.DependencyBaseline(), ordered, lookup)
		if err != nil {
			t.Fatalf("recomposition maintenance permutation %d: %v", index, err)
		}
		if index == 0 {
			recomposed = result.Data()
			continue
		}
		if !reflect.DeepEqual(result.Data(), recomposed) {
			t.Fatalf("recomposition maintenance permutation %d changed YAML:\nwant:\n%s\ngot:\n%s", index, recomposed, result.Data())
		}
	}
	for _, required := range []string{"# current-project comment", "# retained", "billing.charge/v1", "kernel.info/v1"} {
		if !strings.Contains(string(recomposed), required) {
			t.Fatalf("recomposed YAML omits %q:\n%s", required, recomposed)
		}
	}
	recomposedManifest := composeManifest(t, string(recomposed))
	if containsString(exposureIDs(recomposedManifest), "email.send/v1") {
		t.Fatalf("recomposed YAML retained disappeared exposure:\n%s", recomposed)
	}
	if containsString(requirementIDs(recomposedManifest), "inventory.read/v1") {
		t.Fatalf("recomposed YAML retained disappeared requirement:\n%s", recomposed)
	}
}

func assertConflictPermutationDeterminism(t testing.TB, lookup applicationmeta.SchemaLookup) {
	t.Helper()
	tests := []struct {
		name         string
		dependencies []applicationmeta.Dependency
		wantPath     string
	}{
		{
			name: "Provider",
			dependencies: conflictDependencies(t,
				"capabilities: {use: {email.send/v1: acme.smtp.one}}\n",
				"capabilities: {use: {email.send/v1: acme.smtp.two}}\n",
			),
			wantPath: `capabilities.use["email.send/v1"]`,
		},
		{
			name: "Alias",
			dependencies: conflictDependencies(t,
				"capabilities: {aliases: {mail.send/v1: email.send/v1}}\n",
				"capabilities: {aliases: {mail.send/v1: email.deliver/v1}}\n",
			),
			wantPath: `capabilities.aliases["mail.send/v1"]`,
		},
		{
			name: "configuration",
			dependencies: conflictDependencies(t,
				"config: {example.com/acme/smtp.New: {endpoint: one.example}}\n",
				"config: {example.com/acme/smtp.New: {endpoint: two.example}}\n",
			),
			wantPath: `config["example.com/acme/smtp.New"]["endpoint"]`,
		},
		{
			name: "set tombstone",
			dependencies: conflictDependencies(t,
				"capabilities: {require: [audit.write/v1]}\n",
				"capabilities: {require: {remove: [audit.write/v1]}}\n",
			),
			wantPath: `capabilities.require["audit.write/v1"]`,
		},
	}
	for _, test := range tests {
		var expected string
		for index, ordered := range dependencyPermutations(test.dependencies) {
			_, err := applicationmeta.Compose(ordered, composeManifest(t, "{}\n"), lookup)
			if err == nil || !strings.Contains(err.Error(), test.wantPath) {
				t.Fatalf("%s conflict permutation %d = %v, want path %s", test.name, index, err, test.wantPath)
			}
			if index == 0 {
				expected = err.Error()
				continue
			}
			if err.Error() != expected {
				t.Fatalf("%s conflict permutation %d changed diagnostic:\nwant: %s\ngot:  %s", test.name, index, expected, err)
			}
		}
	}
}

func conflictDependencies(t testing.TB, first, second string) []applicationmeta.Dependency {
	t.Helper()
	return []applicationmeta.Dependency{
		{ModulePath: "example.com/conflict/a", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, first)},
		{ModulePath: "example.com/conflict/b", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, second)},
		{ModulePath: "example.com/conflict/c", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, first)},
		{ModulePath: "example.com/conflict/d", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "{}\n")},
	}
}

func dependencyPermutations(values []applicationmeta.Dependency) [][]applicationmeta.Dependency {
	working := append([]applicationmeta.Dependency(nil), values...)
	result := make([][]applicationmeta.Dependency, 0, 24)
	var visit func(int)
	visit = func(index int) {
		if index == len(working) {
			result = append(result, append([]applicationmeta.Dependency(nil), working...))
			return
		}
		for candidate := index; candidate < len(working); candidate++ {
			working[index], working[candidate] = working[candidate], working[index]
			visit(index + 1)
			working[index], working[candidate] = working[candidate], working[index]
		}
	}
	visit(0)
	return result
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
