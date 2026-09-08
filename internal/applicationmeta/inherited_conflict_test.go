package applicationmeta_test

import (
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestInheritedConflictErrorReportsDeterministicDefensiveSources(t *testing.T) {
	t.Parallel()

	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/c", ModuleVersion: "v1.2.0", Manifest: composeManifest(t, "interfaces: {use: {email.send/v1: example.com/secondary.New}}\n")},
		{ModulePath: "example.com/b", ModuleVersion: "v1.1.0", Manifest: composeManifest(t, "interfaces: {use: {email.send/v1: example.com/primary.New}}\n")},
		{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "interfaces: {use: {email.send/v1: example.com/primary.New}}\n")},
	}
	_, err := applicationmeta.Compose(dependencies, composeManifest(t, "{}\n"), composeSchemaLookup(nil))
	conflict := inheritedConflict(t, err)
	if conflict.Field() != `interfaces.use["email.send/v1"]` {
		t.Fatalf("Field = %q", conflict.Field())
	}
	want := []string{
		"example.com/a:plystra.yaml:1:1",
		"example.com/b:plystra.yaml:1:1",
		"example.com/c:plystra.yaml:1:1",
	}
	if got := configurationDeclarationSourceStrings(conflict.Sources()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Sources = %v, want %v", got, want)
	}
	sources := conflict.Sources()
	sources[0] = applicationmeta.ConfigurationDeclarationSource{}
	if got := configurationDeclarationSourceStrings(conflict.Sources()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Sources after caller mutation = %v, want %v", got, want)
	}

	reordered := []applicationmeta.Dependency{dependencies[2], dependencies[0], dependencies[1]}
	_, reorderedErr := applicationmeta.Compose(reordered, composeManifest(t, "{}\n"), composeSchemaLookup(nil))
	reorderedConflict := inheritedConflict(t, reorderedErr)
	if reorderedErr.Error() != err.Error() || !reflect.DeepEqual(configurationDeclarationSourceStrings(reorderedConflict.Sources()), want) {
		t.Fatalf("reordered conflict = %v / %v", reorderedErr, configurationDeclarationSourceStrings(reorderedConflict.Sources()))
	}
}

func TestInheritedConflictErrorCoversEveryComposableDeclarationFamily(t *testing.T) {
	t.Parallel()

	configurationSchema := composeSchema(t, "\tHost string\n")
	tests := []struct {
		name         string
		first        string
		second       string
		field        string
		schemaLookup applicationmeta.SchemaLookup
	}{
		{
			name:   "Capability requirement",
			first:  "capabilities: {require: [audit.write/v1]}\n",
			second: "capabilities: {require: {remove: [audit.write/v1]}}\n",
			field:  `capabilities.require["audit.write/v1"]`,
		},
		{
			name:   "Provider choice",
			first:  "capabilities: {use: {email.send/v1: example.smtp}}\n",
			second: "capabilities: {use: {email.send/v1: example.local}}\n",
			field:  `capabilities.use["email.send/v1"]`,
		},
		{
			name:   "Alias",
			first:  "capabilities: {aliases: {mail.send/v1: email.send/v1}}\n",
			second: "capabilities: {aliases: {mail.send/v1: message.send/v1}}\n",
			field:  `capabilities.aliases["mail.send/v1"]`,
		},
		{
			name:   "Interface requirement",
			first:  "interfaces: {require: [audit.write/v1]}\n",
			second: "interfaces: {require: {remove: [audit.write/v1]}}\n",
			field:  `interfaces.require["audit.write/v1"]`,
		},
		{
			name:   "Implementation choice",
			first:  "interfaces: {use: {email.send/v1: example.com/smtp.New}}\n",
			second: "interfaces: {use: {email.send/v1: example.com/local.New}}\n",
			field:  `interfaces.use["email.send/v1"]`,
		},
		{
			name:   "Interface policy",
			first:  "interfaces: {policies: {email.send/v1: {timeout: 5s}}}\n",
			second: "interfaces: {policies: {email.send/v1: {timeout: 10s}}}\n",
			field:  `interfaces.policies["email.send/v1"].timeout`,
		},
		{
			name:         "constructor configuration",
			first:        "config: {example.com/smtp.New: {host: private-a.example}}\n",
			second:       "config: {example.com/smtp.New: {host: private-b.example}}\n",
			field:        `config["example.com/smtp.New"]["host"]`,
			schemaLookup: composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/smtp.New": configurationSchema}),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lookup := test.schemaLookup
			if lookup == nil {
				lookup = composeSchemaLookup(nil)
			}
			dependencies := []applicationmeta.Dependency{
				{ModulePath: "example.com/b", ModuleVersion: "v2.0.0", Manifest: composeManifest(t, test.second)},
				{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, test.first)},
			}
			_, err := applicationmeta.Compose(dependencies, composeManifest(t, "{}\n"), lookup)
			conflict := inheritedConflict(t, err)
			if conflict.Field() != test.field {
				t.Fatalf("Field = %q, want %q", conflict.Field(), test.field)
			}
			want := []string{"example.com/a:plystra.yaml:1:1", "example.com/b:plystra.yaml:1:1"}
			if got := configurationDeclarationSourceStrings(conflict.Sources()); !reflect.DeepEqual(got, want) {
				t.Fatalf("Sources = %v, want %v", got, want)
			}
		})
	}
}

func TestMaintenanceInheritedConflictRetainsDependencyDeclarationSources(t *testing.T) {
	t.Parallel()

	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/b", ModuleVersion: "v2.0.0", Manifest: composeManifest(t, "capabilities: {use: {email.send/v1: example.local}}\n")},
		{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "capabilities: {use: {email.send/v1: example.smtp}}\n")},
	}
	_, err := applicationmeta.MaintainDependencyConfiguration([]byte("{}\n"), applicationmeta.DependencyBaseline{}, nil, dependencies, composeSchemaLookup(nil))
	conflict := inheritedConflict(t, err)
	if conflict.Field() != `capabilities.use["email.send/v1"]` {
		t.Fatalf("Field = %q", conflict.Field())
	}
	want := []string{"example.com/a:plystra.yaml:1:1", "example.com/b:plystra.yaml:1:1"}
	if got := configurationDeclarationSourceStrings(conflict.Sources()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Sources = %v, want %v", got, want)
	}
}

func inheritedConflict(t testing.TB, err error) *applicationmeta.InheritedConflictError {
	t.Helper()
	var conflict *applicationmeta.InheritedConflictError
	if !errors.As(err, &conflict) || conflict == nil || !errors.Is(err, applicationmeta.ErrInheritedConflict) {
		t.Fatalf("error = %v, want typed inherited conflict", err)
	}
	return conflict
}

func configurationDeclarationSourceStrings(sources []applicationmeta.ConfigurationDeclarationSource) []string {
	result := make([]string, len(sources))
	for index, source := range sources {
		result[index] = source.ModulePath() + ":" + source.Path() + ":" + strconv.Itoa(source.Line()) + ":" + strconv.Itoa(source.Column())
	}
	return result
}
