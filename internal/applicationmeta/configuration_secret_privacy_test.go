package applicationmeta_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestSecretReferencePublicIdentityExcludesKindAndTarget(t *testing.T) {
	t.Parallel()
	schema := composeSchema(t, "Password configuration.Secret\nNested struct { Token configuration.Secret }\n")
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
	for _, wrapper := range []string{
		"config: {example.com/acme/smtp.New: %s}",
		"composition: {exports: {shared: {config: {example.com/acme/smtp.New: %s}}}}",
		"config: {example.com/acme/smtp.New: %s, example.com/unavailable/service.New: {unknown: private}}",
	} {
		var first string
		for _, reference := range []string{"{env: PRIVATE_FIRST}", "{env: PRIVATE_SECOND}", "{file: /PRIVATE_FILE}"} {
			values := "{password: " + reference + ", nested: {token: " + reference + "}}"
			manifest := composeManifest(t, fmt.Sprintf(wrapper, values))
			digest, err := applicationmeta.ConfigurationLayerDigest(manifest, lookup)
			if err != nil {
				t.Fatal(err)
			}
			if first != "" && digest != first {
				t.Fatal("Secret reference kind or target changed public identity")
			}
			first = digest
		}
		for _, values := range []string{"{}", "{password: {$remove: true}}"} {
			if strings.Contains(wrapper, "exports") && strings.Contains(values, "$remove") {
				continue
			}
			digest, err := applicationmeta.ConfigurationLayerDigest(composeManifest(t, fmt.Sprintf(wrapper, values)), lookup)
			if err != nil || digest == first {
				t.Fatalf("absence or explicit removal lost its public identity: %v", err)
			}
		}
	}
}

func TestSecretReferencePrivateConflictsSurvivePublicRedaction(t *testing.T) {
	t.Parallel()
	schema := composeSchema(t, "Password configuration.Secret\n")
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
	manifest := func(reference string) applicationmeta.Manifest {
		return composeManifest(t, "config: {example.com/acme/smtp.New: {password: "+reference+"}}")
	}
	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: manifest("{env: PRIVATE_FIRST}")},
		{ModulePath: "example.com/b", ModuleVersion: "v1.0.0", Manifest: manifest("{env: PRIVATE_FIRST}")},
	}
	empty := composeManifest(t, "{}")
	identical, err := applicationmeta.Compose(dependencies, empty, lookup)
	if err != nil {
		t.Fatal(err)
	}
	path := `config["example.com/acme/smtp.New"]["password"]`
	records := findProvenance(t, identical.Provenance(), path)
	if len(records) != 1 || len(records[0].Sources()) != 2 {
		t.Fatal("identical Secret references did not deduplicate with both sources")
	}
	for _, reference := range []string{"{env: PRIVATE_SECOND}", "{file: /PRIVATE_FILE}"} {
		dependencies[1].Manifest = manifest(reference)
		_, err := applicationmeta.Compose(dependencies, empty, lookup)
		if !errors.Is(err, applicationmeta.ErrInheritedConflict) || !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "PRIVATE_") {
			t.Fatalf("private Secret conflict = %v", err)
		}
		for _, current := range []string{"{env: PRIVATE_LOCAL}", "{$remove: true}"} {
			resolved, err := applicationmeta.Compose(dependencies, manifest(current), lookup)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(resolved.DependencyBaseline(), identical.DependencyBaseline()) {
				t.Fatal("private equality or target leaked through dependency baseline grouping")
			}
			configuration, _ := resolved.Manifest().Configuration(mustConstructorSymbol(t, "example.com/acme/smtp.New"))
			if current != "{$remove: true}" && !bytes.Contains(configuration.YAML(), []byte("PRIVATE_LOCAL")) {
				t.Fatal("public redaction changed private selected configuration")
			}
		}
	}
	current := []byte("config: {example.com/acme/smtp.New: {password: {env: PRIVATE_FIRST}}}\n")
	maintained, err := applicationmeta.MaintainDependencyConfiguration(current, identical.DependencyBaseline(), nil, dependencies, lookup)
	if err != nil || maintained.Changed() || !bytes.Equal(maintained.Data(), current) {
		t.Fatalf("public baseline inferred private ownership: %v", err)
	}
}

func FuzzSecretReferencePublicIdentity(f *testing.F) {
	f.Add([]byte("one"), false)
	f.Add([]byte("two"), true)
	f.Add(bytes.Repeat([]byte("0"), 124), false)
	f.Add(bytes.Repeat([]byte("0"), 125), false)
	schema := composeSchema(f, "Password configuration.Secret\n")
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
	digest := func(t testing.TB, reference string) (string, error) {
		t.Helper()
		manifest := composeManifest(t, "config: {example.com/acme/smtp.New: {password: "+reference+"}}")
		_, validationErr := applicationmeta.ConfigurationDecisions(manifest, lookup)
		value, err := applicationmeta.ConfigurationLayerDigest(manifest, lookup)
		if err != nil {
			t.Fatal(err)
		}
		return value, validationErr
	}
	want, err := digest(f, "{env: PRIVATE_BASELINE}")
	if err != nil {
		f.Fatal(err)
	}
	wantInvalid, err := digest(f, "{env: "+strings.Repeat("A", 257)+"}")
	if !errors.Is(err, applicationmeta.ErrConfigurationInvalidValue) {
		f.Fatalf("overlong environment name = %v", err)
	}
	f.Fuzz(func(t *testing.T, target []byte, file bool) {
		if len(target) > 128 {
			t.Skip()
		}
		reference := fmt.Sprintf("{env: PRIVATE_%x}", target)
		if file {
			reference = fmt.Sprintf("{file: /PRIVATE_%x}", target)
		}
		got, err := digest(t, reference)
		if !file && len(target) > 124 {
			if !errors.Is(err, applicationmeta.ErrConfigurationInvalidValue) || got != wantInvalid {
				t.Fatal("invalid reference bypassed validation or entered opaque public identity")
			}
			return
		}
		if err != nil || got != want {
			t.Fatal("Secret reference changed public identity")
		}
	})
}
