package applicationmeta_test

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/kernel/configuration"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
)

func TestComposeDeterministicallyAppliesTypedDependencyDeclarations(t *testing.T) {
	t.Parallel()

	schema := composeSchema(t, `
host: {type: string, required: true}
labels: {type: object}
port: {type: integer, required: true}
ratio: {type: number}
token: {type: secret, required: true}
`)
	lookup := composeSchemaLookup(map[string]kernelmanifest.Config{"acme.email.smtp": schema})
	dependencyA := applicationmeta.Dependency{
		ModulePath:    "example.com/a",
		ModuleVersion: "v1.2.0",
		Manifest: composeManifest(t, `
http:
  address: ":9001"
  expose: [email.send/v1]
timeouts: {startup: 1s}
capabilities:
  require: [audit.write/v1]
  use: {email.send/v1: acme.email.smtp}
  aliases: {mail.send/v1: email.send/v1}
config:
  acme.email.smtp:
    host: smtp.example.com
    ratio: 1
    token: {env: SMTP_TOKEN}
`),
	}
	dependencyB := applicationmeta.Dependency{
		ModulePath:    "example.com/b",
		ModuleVersion: "v2.0.0",
		Manifest: composeManifest(t, `
http:
  address: ":9002"
  expose: [email.send/v1, order.create/v1]
timeouts: {startup: 9s}
capabilities:
  require: [audit.write/v1]
  use: {email.send/v1: acme.email.smtp}
  aliases: {mail.send/v1: email.send/v1}
config:
  acme.email.smtp:
    port: 587
    ratio: 1.0
    token: {env: SMTP_TOKEN}
`),
	}
	current := composeManifest(t, `
http:
  address: ":8080"
  expose: [kernel.health/v1]
timeouts: {startup: 3s}
capabilities:
  require: [kernel.info/v1]
  aliases: {}
config:
  acme.email.smtp:
    labels: {region: primary}
`)

	first, err := applicationmeta.Compose([]applicationmeta.Dependency{dependencyB, dependencyA}, current, lookup)
	if err != nil || !first.Valid() {
		t.Fatalf("Compose = %#v, %v", first, err)
	}
	second, err := applicationmeta.Compose([]applicationmeta.Dependency{dependencyA, dependencyB}, current, lookup)
	if err != nil || !second.Valid() {
		t.Fatalf("Compose(reordered) = %#v, %v", second, err)
	}
	if first.DependencyDigest() != second.DependencyDigest() || !reflect.DeepEqual(provenanceStrings(first.Provenance()), provenanceStrings(second.Provenance())) {
		t.Fatalf("composition depends on dependency order:\nfirst:  %s %#v\nsecond: %s %#v", first.DependencyDigest(), provenanceStrings(first.Provenance()), second.DependencyDigest(), provenanceStrings(second.Provenance()))
	}

	manifest := first.Manifest()
	if address, exists := manifest.HTTPAddress(); !exists || address != ":8080" || manifest.StartupTimeout().String() != "3s" {
		t.Fatalf("current process settings = address %q, %t, timeout %s", address, exists, manifest.StartupTimeout())
	}
	if got := exposureIDs(manifest); !slices.Equal(got, []string{"email.send/v1", "kernel.health/v1", "order.create/v1"}) {
		t.Fatalf("HTTP exposures = %v", got)
	}
	if got := requirementIDs(manifest); !slices.Equal(got, []string{"audit.write/v1", "kernel.info/v1"}) {
		t.Fatalf("requirements = %v", got)
	}
	choices := manifest.ProviderChoices()
	if len(choices) != 1 || choices[0].Capability().String() != "email.send/v1" || choices[0].PluginID() != "acme.email.smtp" || !strings.HasPrefix(choices[0].Source(), "example.com/a@v1.2.0/plystra.yaml") {
		t.Fatalf("Provider choices = %#v", choices)
	}
	aliases := manifest.Aliases()
	if len(aliases) != 1 || aliases[0].ID().String() != "mail.send/v1" || aliases[0].Target().String() != "email.send/v1" || !strings.HasPrefix(aliases[0].Source(), "example.com/a@v1.2.0/plystra.yaml") {
		t.Fatalf("Aliases = %#v", aliases)
	}
	configured, exists := manifest.Configuration("acme.email.smtp")
	if !exists {
		t.Fatal("composed configuration is absent")
	}
	if err := configuration.Validate(schema, configured.YAML()); err != nil {
		t.Fatalf("composed configuration is invalid: %v\n%s", err, configured.YAML())
	}
	for _, expected := range [][]byte{[]byte("host: smtp.example.com"), []byte("labels:"), []byte("port: 587"), []byte("ratio: 1"), []byte("token:"), []byte("env: SMTP_TOKEN")} {
		if !bytes.Contains(configured.YAML(), expected) {
			t.Fatalf("composed configuration omits %q:\n%s", expected, configured.YAML())
		}
	}

	audit := findProvenance(t, first.Provenance(), `capabilities.require["audit.write/v1"]`)
	if len(audit) != 1 || len(audit[0].Sources()) != 2 {
		t.Fatalf("audit provenance = %#v", provenanceStrings(audit))
	}
	ratio := findProvenance(t, first.Provenance(), `config["acme.email.smtp"]["ratio"]`)
	if len(ratio) != 1 || len(ratio[0].Sources()) != 2 {
		t.Fatalf("normalized ratio provenance = %#v", provenanceStrings(ratio))
	}
	for _, forbidden := range []string{"http.address", "timeouts.startup"} {
		if len(findProvenance(t, first.Provenance(), forbidden)) != 0 {
			t.Fatalf("dependency-owned process setting entered provenance: %s", forbidden)
		}
	}
	provenance := first.Provenance()
	provenance[0] = applicationmeta.Provenance{}
	if first.Provenance()[0].Path() == "" {
		t.Fatal("Provenance exposed mutable composition storage")
	}
	sources := first.Provenance()[0].Sources()
	if len(sources) != 0 {
		sources[0] = "changed"
		if first.Provenance()[0].Sources()[0] == "changed" {
			t.Fatal("Provenance sources exposed mutable storage")
		}
	}
}

func TestComposeRequiresCurrentProviderReplacementForInheritedConflict(t *testing.T) {
	t.Parallel()

	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {use: {email.send/v1: acme.smtp}}\n")},
		{ModulePath: "example.com/b", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {use: {email.send/v1: acme.local}}\n")},
	}
	_, err := applicationmeta.Compose(dependencies, composeManifest(t, "{}\n"), composeSchemaLookup(nil))
	if !errors.Is(err, applicationmeta.ErrCompose) || !errors.Is(err, applicationmeta.ErrInheritedConflict) {
		t.Fatalf("Compose conflict error = %v", err)
	}
	for _, required := range []string{`capabilities.use["email.send/v1"]`, "acme.smtp", "acme.local", "example.com/a@v1.0.0/plystra.yaml", "example.com/b@v1.0.0/plystra.yaml", "current Project"} {
		if !strings.Contains(err.Error(), required) {
			t.Fatalf("conflict error omits %q: %v", required, err)
		}
	}

	current := composeManifest(t, "capabilities: {use: {email.send/v1: acme.current}}\n")
	composed, err := applicationmeta.Compose(dependencies, current, composeSchemaLookup(nil))
	if err != nil {
		t.Fatalf("Compose with replacement: %v", err)
	}
	choices := composed.Manifest().ProviderChoices()
	if len(choices) != 1 || choices[0].PluginID() != "acme.current" || choices[0].Source() != `plystra.yaml capabilities.use["email.send/v1"]` {
		t.Fatalf("current replacement = %#v", choices)
	}
	if records := findProvenance(t, composed.Provenance(), `capabilities.use["email.send/v1"]`); len(records) != 2 {
		t.Fatalf("conflicting baseline provenance = %#v", provenanceStrings(records))
	}
}

func TestComposeRequiresExactCurrentAliasReplacement(t *testing.T) {
	t.Parallel()

	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {aliases: {mail.send/v1: email.send/v1}}\n")},
		{ModulePath: "example.com/b", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {aliases: {mail.send/v1: message.send/v1}}\n")},
	}
	_, err := applicationmeta.Compose(dependencies, composeManifest(t, "{}\n"), composeSchemaLookup(nil))
	if !errors.Is(err, applicationmeta.ErrInheritedConflict) || !strings.Contains(err.Error(), `capabilities.aliases["mail.send/v1"]`) || !strings.Contains(err.Error(), "email.send/v1") || !strings.Contains(err.Error(), "message.send/v1") {
		t.Fatalf("Alias conflict error = %v", err)
	}
	current := composeManifest(t, "capabilities: {aliases: {mail.send/v1: notification.send/v1}}\n")
	composed, err := applicationmeta.Compose(dependencies, current, composeSchemaLookup(nil))
	if err != nil {
		t.Fatalf("Compose with Alias replacement: %v", err)
	}
	aliases := composed.Manifest().Aliases()
	if len(aliases) != 1 || aliases[0].Target().String() != "notification.send/v1" {
		t.Fatalf("current Alias replacement = %#v", aliases)
	}
}

func TestComposeMergesConfigurationByDeclaredFieldAndRedactsConflicts(t *testing.T) {
	t.Parallel()

	schema := composeSchema(t, `
host: {type: string}
token: {type: secret}
`)
	lookup := composeSchemaLookup(map[string]kernelmanifest.Config{"acme.smtp": schema})
	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "config: {acme.smtp: {host: private-a.example, token: {env: PRIVATE_A}}}\n")},
		{ModulePath: "example.com/b", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "config: {acme.smtp: {host: private-b.example, token: {env: PRIVATE_B}}}\n")},
	}
	_, err := applicationmeta.Compose(dependencies, composeManifest(t, "{}\n"), lookup)
	if !errors.Is(err, applicationmeta.ErrInheritedConflict) || !strings.Contains(err.Error(), `config["acme.smtp"]["host"]`) {
		t.Fatalf("configuration conflict error = %v", err)
	}
	for _, forbidden := range []string{"private-a.example", "private-b.example", "PRIVATE_A", "PRIVATE_B"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("configuration conflict exposed %q: %v", forbidden, err)
		}
	}

	current := composeManifest(t, "config: {acme.smtp: {host: current.example, token: {env: CURRENT_TOKEN}}}\n")
	composed, err := applicationmeta.Compose(dependencies, current, lookup)
	if err != nil {
		t.Fatalf("Compose with configuration replacements: %v", err)
	}
	configured, _ := composed.Manifest().Configuration("acme.smtp")
	if !bytes.Contains(configured.YAML(), []byte("host: current.example")) || !bytes.Contains(configured.YAML(), []byte("CURRENT_TOKEN")) {
		t.Fatalf("current configuration replacement = %s", configured.YAML())
	}
	if records := findProvenance(t, composed.Provenance(), `config["acme.smtp"]["token"]`); len(records) != 2 {
		t.Fatalf("configuration baseline provenance = %#v", provenanceStrings(records))
	}
}

func TestComposeRejectsInvalidConfigurationAndCrossDocumentAliasChains(t *testing.T) {
	t.Parallel()

	schema := composeSchema(t, "host: {type: string}\n")
	lookup := composeSchemaLookup(map[string]kernelmanifest.Config{"acme.smtp": schema})
	tests := []struct {
		name         string
		dependencies []applicationmeta.Dependency
		current      applicationmeta.Manifest
		lookup       applicationmeta.SchemaLookup
		want         string
	}{
		{
			name:         "unknown configuration field",
			dependencies: []applicationmeta.Dependency{{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "config: {acme.smtp: {private_unknown: hidden}}\n")}},
			current:      composeManifest(t, "{}\n"),
			lookup:       lookup,
			want:         "unknown plugin configuration field",
		},
		{
			name: "Alias chain",
			dependencies: []applicationmeta.Dependency{
				{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {aliases: {orders.submit/v1: order.create/v1}}\n")},
				{ModulePath: "example.com/b", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {aliases: {order.create/v1: order.execute/v1}}\n")},
			},
			current: composeManifest(t, "{}\n"),
			lookup:  composeSchemaLookup(nil),
			want:    "forbidden Alias chain",
		},
		{
			name:         "require inherited Alias",
			dependencies: []applicationmeta.Dependency{{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {aliases: {orders.submit/v1: order.create/v1}}\n")}},
			current:      composeManifest(t, "capabilities: {require: [orders.submit/v1]}\n"),
			lookup:       composeSchemaLookup(nil),
			want:         "requirements must name canonical Capabilities",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := applicationmeta.Compose(test.dependencies, test.current, test.lookup)
			if !errors.Is(err, applicationmeta.ErrCompose) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compose error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), "private_unknown") || strings.Contains(err.Error(), "hidden") {
				t.Fatalf("Compose error exposed private configuration: %v", err)
			}
		})
	}
}

func composeManifest(t testing.TB, source string) applicationmeta.Manifest {
	t.Helper()
	manifest, err := applicationmeta.Parse([]byte(source))
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, source)
	}
	return manifest
}

func composeSchema(t testing.TB, source string) kernelmanifest.Config {
	t.Helper()
	schema, err := kernelmanifest.ParseConfig([]byte(source))
	if err != nil {
		t.Fatalf("ParseConfig: %v\n%s", err, source)
	}
	return schema
}

func composeSchemaLookup(schemas map[string]kernelmanifest.Config) applicationmeta.SchemaLookup {
	return func(pluginID string) (kernelmanifest.Config, bool) {
		schema, exists := schemas[pluginID]
		return schema, exists
	}
}

func exposureIDs(manifest applicationmeta.Manifest) []string {
	values := manifest.HTTPExposures()
	result := make([]string, len(values))
	for index := range values {
		result[index] = values[index].ID().String()
	}
	return result
}

func requirementIDs(manifest applicationmeta.Manifest) []string {
	values := manifest.Requirements()
	result := make([]string, len(values))
	for index := range values {
		result[index] = values[index].ID().String()
	}
	return result
}

func findProvenance(t testing.TB, values []applicationmeta.Provenance, path string) []applicationmeta.Provenance {
	t.Helper()
	var result []applicationmeta.Provenance
	for _, value := range values {
		if value.Path() == path {
			result = append(result, value)
		}
	}
	return result
}

func provenanceStrings(values []applicationmeta.Provenance) []string {
	result := make([]string, len(values))
	for index := range values {
		result[index] = values[index].Path() + "=" + values[index].Digest() + "@" + strings.Join(values[index].Sources(), ",")
	}
	return result
}
