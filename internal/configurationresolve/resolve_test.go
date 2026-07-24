package configurationresolve_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/kernel/configuration"
)

func TestResolveKeepsConstructorConfigurationOutOfLegacyPluginBindings(t *testing.T) {
	t.Parallel()

	inventory := configurationInventory(t)
	context := configurationContext(t, inventory, "acme.optional")
	application := configurationApplication(t, `config:
  example.com/app/smtp.New:
    host: private.smtp.example.com
    password: {env: PLYSTRA_CONFIGURATION_RESOLUTION_MISSING}
`)
	result, err := configurationresolve.Resolve(application, inventory, context)
	if err != nil || !result.Valid() || result.Digest() == "" {
		t.Fatalf("Resolve = %#v, %v", result, err)
	}
	bindings := result.Bindings()
	if got := bindingIDs(bindings); !reflect.DeepEqual(got, []string{"acme.optional"}) {
		t.Fatalf("Bindings = %v", got)
	}
	optional, ok := result.Binding("acme.optional")
	if !ok || optional.Explicit() || string(optional.YAML()) != "{}\n" || !strings.Contains(optional.Source(), "implicit empty configuration") {
		t.Fatalf("optional Binding = %#v, %t, YAML %q", optional, ok, optional.YAML())
	}
	data := optional.YAML()
	data[0] = 'X'
	if bytes.Equal(data, optional.YAML()) {
		t.Fatal("Binding YAML exposed mutable storage")
	}
	if _, exists := result.Binding("acme.missing"); exists {
		t.Fatal("Binding(missing) succeeded")
	}
	bindings[0] = configurationresolve.Binding{}
	if result.Bindings()[0].PluginID() != "acme.optional" {
		t.Fatal("Bindings exposed mutable result storage")
	}
	for _, value := range []any{optional, result} {
		for _, formatted := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value), fmt.Sprintf("%q", value)} {
			if !strings.Contains(formatted, "redacted") || strings.Contains(formatted, "private.smtp.example.com") || strings.Contains(formatted, "PLYSTRA_CONFIGURATION_RESOLUTION_MISSING") {
				t.Fatalf("configuration formatting = %q", formatted)
			}
		}
	}

	changed, err := configurationresolve.Resolve(configurationApplication(t, `config:
  example.com/app/smtp.New:
    host: changed.smtp.example.com
    password: {env: PLYSTRA_CONFIGURATION_RESOLUTION_MISSING}
`), inventory, context)
	if err != nil || changed.Digest() != result.Digest() {
		t.Fatalf("constructor configuration changed legacy digest = %q, original %q, error %v", changed.Digest(), result.Digest(), err)
	}
}

func TestResolveRejectsRequiredLegacyPluginConfigurationWithoutReadingConstructorValues(t *testing.T) {
	t.Parallel()

	inventory := configurationInventory(t)
	application := configurationApplication(t, "config: {example.com/app/smtp.New: {host: private, password: {env: SECRET_TARGET}}}\n")
	result, err := configurationresolve.Resolve(application, inventory, configurationContext(t, inventory, "acme.email.smtp"))
	if result.Valid() || !errors.Is(err, configurationresolve.ErrResolve) || !errors.Is(err, configurationresolve.ErrInvalidConfiguration) || !errors.Is(err, configuration.ErrMissingField) {
		t.Fatalf("Resolve = %#v, %v", result, err)
	}
	for _, forbidden := range []string{"private", "SECRET_TARGET"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error exposed %q: %v", forbidden, err)
		}
	}
}

func TestResolveSupportsEmptyApplicationAndRejectsInvalidContext(t *testing.T) {
	t.Parallel()

	context, err := generation.NewContext(generation.Input{})
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	result, err := configurationresolve.Resolve(configurationApplication(t, "{}\n"), plugininventory.Index{}, context)
	if err != nil || !result.Valid() || len(result.Bindings()) != 0 || result.Digest() == "" {
		t.Fatalf("Resolve empty = %#v, %v", result, err)
	}
	if invalid, err := configurationresolve.Resolve(configurationApplication(t, "{}\n"), plugininventory.Index{}, generation.Context{}); invalid.Valid() || !errors.Is(err, configurationresolve.ErrInvalidContext) {
		t.Fatalf("Resolve zero context = %#v, %v", invalid, err)
	}
	missingContext, err := generation.NewContext(generation.Input{Plugins: []generation.PluginInput{{
		ID:                "acme.missing",
		ModulePath:        "example.com/missing",
		BuildMetadataJSON: []byte("{}"),
	}}})
	if err != nil {
		t.Fatalf("NewContext missing: %v", err)
	}
	if invalid, err := configurationresolve.Resolve(configurationApplication(t, "{}\n"), plugininventory.Index{}, missingContext); invalid.Valid() || !errors.Is(err, configurationresolve.ErrMissingPlugin) {
		t.Fatalf("Resolve missing plugin = %#v, %v", invalid, err)
	}
	var zero configurationresolve.Result
	if zero.Valid() || zero.Digest() != "" || len(zero.Bindings()) != 0 {
		t.Fatalf("zero Result = %#v", zero)
	}
}

func configurationInventory(t testing.TB) plugininventory.Index {
	t.Helper()
	root := t.TempDir()
	writeConfigurationFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n")
	writeConfigurationFile(t, filepath.Join(root, "smtp", "plugin.yaml"), `id: acme.email.smtp
config:
  host: {type: string, required: true}
  password: {type: secret, required: true}
  port: {type: integer, default: 587}
`)
	writeConfigurationFile(t, filepath.Join(root, "optional", "plugin.yaml"), `id: acme.optional
config:
  enabled: {type: boolean, default: true}
  note: {type: string}
`)
	module, err := modulelocate.Find(root)
	if err != nil {
		t.Fatalf("modulelocate.Find: %v", err)
	}
	inventory, err := plugininventory.Build(module, moduledependency.Index{})
	if err != nil {
		t.Fatalf("plugininventory.Build: %v", err)
	}
	return inventory
}

func configurationContext(t testing.TB, inventory plugininventory.Index, ids ...string) generation.Context {
	t.Helper()
	inputs := make([]generation.PluginInput, len(ids))
	for index, id := range ids {
		plugin, exists := inventory.ByID(id)
		if !exists {
			t.Fatalf("inventory omits %s", id)
		}
		inputs[index] = generation.PluginInput{
			ID:                id,
			ModulePath:        plugin.ModulePath(),
			ModuleVersion:     plugin.ModuleVersion(),
			BuildMetadataJSON: []byte("{}"),
		}
	}
	context, err := generation.NewContext(generation.Input{Plugins: inputs})
	if err != nil {
		t.Fatalf("generation.NewContext: %v", err)
	}
	return context
}

func configurationApplication(t testing.TB, source string) applicationmeta.Manifest {
	t.Helper()
	application, err := applicationmeta.Parse([]byte(source))
	if err != nil {
		t.Fatalf("applicationmeta.Parse: %v\n%s", err, source)
	}
	return application
}

func writeConfigurationFile(t testing.TB, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func bindingIDs(bindings []configurationresolve.Binding) []string {
	result := make([]string, len(bindings))
	for index, binding := range bindings {
		result[index] = binding.PluginID()
	}
	return result
}
