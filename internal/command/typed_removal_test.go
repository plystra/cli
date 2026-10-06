package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorsymbol"
	"go.yaml.in/yaml/v3"
)

func TestPublicTypedConfigurationNilAndOverlayRemoval(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	for _, mode := range []struct {
		name     string
		selected string
		selector []string
	}{
		{name: "default", selected: "plystra.yaml"},
		{name: "environment", selected: "plystra.production.yaml", selector: []string{"--env", "production"}},
		{name: "replacement", selected: "deploy/customer.yaml", selector: []string{"--config", "deploy/customer.yaml"}},
	} {
		mode := mode
		for _, active := range []bool{false, true} {
			t.Run(mode.name+"/"+map[bool]string{false: "dormant", true: "active"}[active], func(t *testing.T) {
				root := writeImplementationSelectionCommandProject(t)
				writeCommandNullableConfigurationImplementation(t, root)
				selection := "interfaces: {use: {email.send/v1: " + constructor + "}}\n"
				if active {
					selection = "interfaces: {require: [email.send/v1], use: {email.send/v1: " + constructor + "}}\n"
				}
				baseConfig := "config: {" + constructor + ": {settings: {keep: retained.internal, remove: removed.internal}, pointer: lower.internal, items: [lower.internal], labels: {private_key: lower.internal}}}\n"
				completeConfig := "config: {" + constructor + ": {settings: {keep: retained.internal, remove: ''}, pointer: null, items: ~, labels: }}\n"
				overlayConfig := "config: {" + constructor + ": {settings: {remove: {$remove: true}}, pointer: null, items: ~, labels: }}\n"
				rootData, selectedData := selection+completeConfig, selection+completeConfig
				options := applicationresolve.Options{Start: root, Environment: commandGoEnvironment()}
				switch mode.name {
				case "environment":
					rootData, selectedData = selection+baseConfig, overlayConfig
					options.EnvironmentName = "production"
				case "replacement":
					rootData, selectedData = selection+baseConfig, selection+completeConfig
					options.ConfigurationPath = mode.selected
				}
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootData)
				writeCommandFile(t, filepath.Join(root, mode.selected), selectedData)

				resolved, err := applicationresolve.Resolve(t.Context(), options)
				if err != nil {
					t.Fatal(err)
				}
				symbol, err := constructorsymbol.Parse(constructor)
				if err != nil {
					t.Fatal(err)
				}
				configured, exists := resolved.Manifest().Configuration(symbol)
				if !exists {
					t.Fatal("resolved typed configuration is absent")
				}
				var values map[string]any
				if err := yaml.Unmarshal(configured.YAML(), &values); err != nil {
					t.Fatal(err)
				}
				want := map[string]any{
					"settings": map[string]any{"keep": "retained.internal", "remove": ""},
					"pointer":  nil,
					"items":    nil,
					"labels":   nil,
				}
				if mode.name == "environment" {
					want = map[string]any{
						"settings": map[string]any{"keep": "retained.internal"},
						"pointer":  nil,
						"items":    nil,
						"labels":   nil,
					}
				}
				if !reflect.DeepEqual(values, want) {
					t.Fatalf("typed composition = %#v, want %#v", values, want)
				}
				if code, stdout, stderr := runCommand(t, append([]string{"generate"}, mode.selector...), root, commandGoEnvironment()); code != 0 {
					t.Fatalf("generate = %d, %q, %q", code, stdout, stderr)
				}
				manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
				if err != nil {
					t.Fatal(err)
				}
				if active != (len(manifest.InterfaceProvenance().Bindings()) == 1) || active == (len(manifest.DormantConstructorConfigurations()) == 1) {
					t.Fatal("typed configuration changed constructor activation")
				}
				before := commandTree(t, root)
				for _, invocation := range [][]string{{"generate", "--check"}, {"check"}, {"inspect", "configuration", "--format", "json"}} {
					args := append(append([]string(nil), invocation...), mode.selector...)
					code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
					if code != 0 || strings.Contains(stdout+stderr, ".internal") || strings.Contains(stdout+stderr, "private_key") {
						t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
					}
					if !reflect.DeepEqual(commandTree(t, root), before) {
						t.Fatalf("%v mutated the Project", args)
					}
				}
				if string(readCommandFile(t, root, "plystra.yaml")) != rootData || string(readCommandFile(t, root, mode.selected)) != selectedData {
					t.Fatal("generation changed authored configuration")
				}
				assertNoCommandTransactions(t, root)
			})
		}
	}
}

func TestPublicInvalidTypedConfigurationDoesNotMutate(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	for _, mode := range []struct {
		name     string
		selected string
		selector []string
	}{
		{name: "default", selected: "plystra.yaml"},
		{name: "environment", selected: "plystra.production.yaml", selector: []string{"--env", "production"}},
		{name: "replacement", selected: "deploy/customer.yaml", selector: []string{"--config", "deploy/customer.yaml"}},
	} {
		mode := mode
		t.Run(mode.name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandNullableConfigurationImplementation(t, root)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			for _, value := range []string{
				"{settings: null}", "{settings: {keep: null}}", "{labels: {$remove: private_value}}",
				"{labels: {private_key: {$remove: true}}}", "{pointer: {$remove: true, private_key: private_value}}",
				"{pointer: !!null private_value}", "{items: !!null private_value}", "{labels: !!null private_value}",
			} {
				writeCommandFile(t, filepath.Join(root, mode.selected), "interfaces: {use: {email.send/v1: "+constructor+"}}\nconfig: {"+constructor+": "+value+"}\n")
				before := commandTree(t, root)
				for _, invocation := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
					args := append(append([]string(nil), invocation...), mode.selector...)
					code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
					if code == 0 || stdout != "" || !strings.Contains(stderr, "PLYSTRA_CONSTRUCTOR_CONFIGURATION_VALUES_INVALID") || strings.Contains(stderr, "private_key") || strings.Contains(stderr, "private_value") {
						t.Fatalf("%s: %v = %d, %q, %q", value, args, code, stdout, stderr)
					}
					if !reflect.DeepEqual(commandTree(t, root), before) {
						t.Fatalf("%v mutated rejected Project", args)
					}
				}
				assertNoCommandTransactions(t, root)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func writeCommandNullableConfigurationImplementation(t testing.TB, root string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, "smtp", "implementation.go"), `package smtp

import (
	"context"
	contract "example.com/acme/implementation-use/interfaces/email/send/v1"
)

type Config struct {
	Settings struct { Keep string; Remove string }
	Pointer *string
	Items []string
	Labels map[string]string
}
type Service struct{}

//plystra:implements email.send/v1
func New(Config) (*Service, error) { return &Service{}, nil }
func (*Service) Send(context.Context, contract.Request) (contract.Response, error) { return contract.Response{}, nil }
var _ contract.Interface = (*Service)(nil)
`)
}
