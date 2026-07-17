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
	"github.com/plystra/kernel/plugin/manifest"
)

func TestResolveBindsExactlyOneValidatedObjectPerSelectedPlugin(t *testing.T) {
	t.Parallel()

	inventory := configurationInventory(t)
	context := configurationContext(t, inventory, "acme.email.smtp", "acme.optional")
	application := configurationApplication(t, `config:
  acme.email.smtp:
    host: private.smtp.example.com
    password: {env: PLYSTRA_CONFIGURATION_RESOLUTION_MISSING}
`)
	result, err := configurationresolve.Resolve(application, inventory, context)
	if err != nil || !result.Valid() || result.Digest() == "" {
		t.Fatalf("Resolve = %#v, %v", result, err)
	}
	bindings := result.Bindings()
	if got := bindingIDs(bindings); !reflect.DeepEqual(got, []string{"acme.email.smtp", "acme.optional"}) {
		t.Fatalf("Bindings = %v", got)
	}
	smtp, ok := result.Binding("acme.email.smtp")
	if !ok || !smtp.Explicit() || smtp.ModulePath() != "example.com/app" || smtp.ImportPath() != "example.com/app/smtp" || smtp.Source() != `plystra.yaml config["acme.email.smtp"]` {
		t.Fatalf("smtp Binding = %#v, %t", smtp, ok)
	}
	password, ok := smtp.Schema().Lookup("password")
	if !ok || password.Type() != manifest.ConfigSecret || !password.Required() {
		t.Fatalf("smtp password schema = %#v, %t", password, ok)
	}
	data := smtp.YAML()
	if !bytes.Contains(data, []byte("private.smtp.example.com")) || !bytes.Contains(data, []byte("PLYSTRA_CONFIGURATION_RESOLUTION_MISSING")) {
		t.Fatalf("smtp YAML = %q", data)
	}
	data[0] = 'X'
	if bytes.Equal(data, smtp.YAML()) {
		t.Fatal("Binding YAML exposed mutable storage")
	}
	optional, ok := result.Binding("acme.optional")
	if !ok || optional.Explicit() || string(optional.YAML()) != "{}\n" || !strings.Contains(optional.Source(), "implicit empty configuration") {
		t.Fatalf("optional Binding = %#v, %t, YAML %q", optional, ok, optional.YAML())
	}
	if _, exists := result.Binding("acme.missing"); exists {
		t.Fatal("Binding(missing) succeeded")
	}
	bindings[0] = configurationresolve.Binding{}
	if result.Bindings()[0].PluginID() != "acme.email.smtp" {
		t.Fatal("Bindings exposed mutable result storage")
	}
	for _, value := range []any{smtp, result} {
		for _, formatted := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value), fmt.Sprintf("%q", value)} {
			if !strings.Contains(formatted, "redacted") || strings.Contains(formatted, "private.smtp.example.com") || strings.Contains(formatted, "PLYSTRA_CONFIGURATION_RESOLUTION_MISSING") {
				t.Fatalf("configuration formatting = %q", formatted)
			}
		}
	}

	changed, err := configurationresolve.Resolve(configurationApplication(t, `config:
  acme.email.smtp:
    host: changed.smtp.example.com
    password: {env: PLYSTRA_CONFIGURATION_RESOLUTION_MISSING}
`), inventory, context)
	if err != nil || changed.Digest() == result.Digest() {
		t.Fatalf("changed configuration digest = %q, original %q, error %v", changed.Digest(), result.Digest(), err)
	}
}

func TestResolveRejectsUnselectedAndInvalidConfigurationSafely(t *testing.T) {
	t.Parallel()

	inventory := configurationInventory(t)
	tests := []struct {
		name        string
		selected    []string
		application string
		reason      error
		also        error
	}{
		{
			name:        "unselected",
			selected:    []string{"acme.optional"},
			application: "config: {acme.email.smtp: {host: private, password: {env: SECRET_TARGET}}}\n",
			reason:      configurationresolve.ErrUnselectedConfiguration,
		},
		{
			name:        "missing required",
			selected:    []string{"acme.email.smtp"},
			application: "config: {}\n",
			reason:      configurationresolve.ErrInvalidConfiguration,
			also:        configuration.ErrMissingField,
		},
		{
			name:        "unknown field",
			selected:    []string{"acme.email.smtp"},
			application: "config: {acme.email.smtp: {host: private, password: {env: SECRET_TARGET}, private_value: hidden-secret-value}}\n",
			reason:      configurationresolve.ErrInvalidConfiguration,
			also:        configuration.ErrUnknownField,
		},
		{
			name:        "invalid type",
			selected:    []string{"acme.email.smtp"},
			application: "config: {acme.email.smtp: {host: 1, password: {env: SECRET_TARGET}}}\n",
			reason:      configurationresolve.ErrInvalidConfiguration,
			also:        configuration.ErrInvalidValue,
		},
		{
			name:        "invalid Secret reference",
			selected:    []string{"acme.email.smtp"},
			application: "config: {acme.email.smtp: {host: private, password: {env: BAD=SECRET_TARGET}}}\n",
			reason:      configurationresolve.ErrInvalidConfiguration,
			also:        configuration.ErrInvalidValue,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := configurationresolve.Resolve(configurationApplication(t, test.application), inventory, configurationContext(t, inventory, test.selected...))
			if result.Valid() || !errors.Is(err, configurationresolve.ErrResolve) || !errors.Is(err, test.reason) || test.also != nil && !errors.Is(err, test.also) {
				t.Fatalf("Resolve = %#v, %v", result, err)
			}
			for _, forbidden := range []string{"SECRET_TARGET", "BAD=SECRET_TARGET", "hidden-secret-value"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("error exposed %q: %v", forbidden, err)
				}
			}
		})
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
