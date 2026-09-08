package applicationmeta_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestAmbiguousConfigurationOwnershipErrorReportsEveryPriorDeclarationSource(t *testing.T) {
	t.Parallel()

	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
		"example.com/acme/smtp.New": composeSchema(t, "\tHost string\n\tToken configuration.Secret\n"),
	})
	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/c", ModuleVersion: "v1.2.0", Manifest: composeManifest(t, "config: {example.com/acme/smtp.New: {host: shared.example, token: {env: PRIVATE_TOKEN_TARGET}}}\n")},
		{ModulePath: "example.com/b", ModuleVersion: "v1.1.0", Manifest: composeManifest(t, "config: {example.com/acme/smtp.New: {host: shared.example, token: {env: PRIVATE_TOKEN_TARGET}}}\n")},
		{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "config: {example.com/acme/smtp.New: {host: shared.example, token: {env: PRIVATE_TOKEN_TARGET}}}\n")},
	}
	initial, err := applicationmeta.MaintainDependencyConfiguration([]byte("{}\n"), applicationmeta.DependencyBaseline{}, nil, dependencies, lookup)
	if err != nil {
		t.Fatalf("materialize dependency configuration: %v", err)
	}
	previous, err := applicationmeta.Compose(dependencies, composeManifest(t, "{}\n"), lookup)
	if err != nil {
		t.Fatalf("compose dependency baseline: %v", err)
	}
	withoutHost := bytes.Replace(initial.Data(), []byte("host: shared.example, "), nil, 1)
	if bytes.Equal(withoutHost, initial.Data()) {
		t.Fatalf("test did not remove inherited host:\n%s", initial.Data())
	}
	if _, err := applicationmeta.Parse(withoutHost); err != nil {
		t.Fatalf("removed inherited host produced invalid YAML: %v\nbefore:\n%s\nafter:\n%s", err, initial.Data(), withoutHost)
	}

	_, err = applicationmeta.MaintainDependencyConfiguration(withoutHost, previous.DependencyBaseline(), initial.LocalPaths(), dependencies, lookup)
	conflict := ambiguousConfigurationOwnership(t, err)
	if conflict.Field() != `config["example.com/acme/smtp.New"]["host"]` {
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
	for _, fragment := range []string{
		`example.com/a@v1.0.0/plystra.yaml config["example.com/acme/smtp.New"]["host"]`,
		`example.com/b@v1.1.0/plystra.yaml config["example.com/acme/smtp.New"]["host"]`,
		`example.com/c@v1.2.0/plystra.yaml config["example.com/acme/smtp.New"]["host"]`,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error omits %q: %v", fragment, err)
		}
	}
	for _, forbidden := range []string{"shared.example", "PRIVATE_TOKEN_TARGET"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error exposes configured value %q: %v", forbidden, err)
		}
	}

	reordered := []applicationmeta.Dependency{dependencies[2], dependencies[0], dependencies[1]}
	_, reorderedErr := applicationmeta.MaintainDependencyConfiguration(withoutHost, previous.DependencyBaseline(), initial.LocalPaths(), reordered, lookup)
	reorderedConflict := ambiguousConfigurationOwnership(t, reorderedErr)
	if reorderedErr.Error() != err.Error() || !reflect.DeepEqual(configurationDeclarationSourceStrings(reorderedConflict.Sources()), want) {
		t.Fatalf("reordered conflict = %v / %v", reorderedErr, configurationDeclarationSourceStrings(reorderedConflict.Sources()))
	}
}

func ambiguousConfigurationOwnership(t testing.TB, err error) *applicationmeta.AmbiguousConfigurationOwnershipError {
	t.Helper()
	var conflict *applicationmeta.AmbiguousConfigurationOwnershipError
	if !errors.As(err, &conflict) || conflict == nil || !errors.Is(err, applicationmeta.ErrAmbiguousConfigurationOwnership) || !errors.Is(err, applicationmeta.ErrMaintainConfiguration) {
		t.Fatalf("error = %v, want typed ambiguous configuration ownership", err)
	}
	return conflict
}
