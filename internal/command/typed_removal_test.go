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

func TestPublicTypedConfigurationNilAndRemoval(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	for _, mode := range []string{"default", "environment", "replacement"} {
		for _, active := range []bool{false, true} {
			name := mode + "/dormant"
			if active {
				name = mode + "/active"
			}
			t.Run(name, func(t *testing.T) {
				root := writeImplementationSelectionCommandProject(t)
				writeCommandNullableConfigurationImplementation(t, root)
				lower := "config: {" + constructor + ": {settings: {keep: retained.internal, remove: removed.internal}, pointer: lower.internal, items: [lower.internal], labels: {private_key: lower.internal}}}\n"
				inventory := "composition:\n  exports:\n    defaults:\n      " + lower
				adoption := "  adopt: [{module: example.com/acme/implementation-use, export: defaults}]\n"
				selection := "interfaces: {use: {email.send/v1: " + constructor + "}}\n"
				if active {
					selection = "interfaces: {require: [email.send/v1], use: {email.send/v1: " + constructor + "}}\n"
				}
				configuration := "config: {" + constructor + ": {settings: {remove: {$remove: true}}, pointer: null, items: ~, labels: }}\n"
				rootData := inventory + adoption + selection + configuration
				selectedPath, selectedData := "plystra.yaml", rootData
				options := applicationresolve.Options{Start: root, Environment: commandGoEnvironment()}
				var selector []string
				switch mode {
				case "environment":
					rootData = inventory + adoption + selection + lower
					selectedPath, selectedData = "plystra.production.yaml", configuration
					selector = []string{"--env", "production"}
					options.EnvironmentName = "production"
				case "replacement":
					rootData = inventory + selection + lower
					selectedPath, selectedData = "deploy/customer.yaml", "composition:\n"+adoption+selection+configuration
					selector = []string{"--config", selectedPath}
					options.ConfigurationPath = selectedPath
				}
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootData)
				writeCommandFile(t, filepath.Join(root, selectedPath), selectedData)
				resolved, err := applicationresolve.Resolve(t.Context(), options)
				if err != nil {
					t.Fatal(err)
				}
				symbol, err := constructorsymbol.Parse(constructor)
				if err != nil {
					t.Fatal(err)
				}
				configured, exists := resolved.Manifest().Configuration(symbol)
				var values map[string]any
				if !exists {
					t.Fatal("resolved typed configuration is absent")
				}
				if err := yaml.Unmarshal(configured.YAML(), &values); err != nil {
					t.Fatal(err)
				}
				want := map[string]any{"settings": map[string]any{"keep": "retained.internal"}, "pointer": nil, "items": nil, "labels": nil}
				if !reflect.DeepEqual(values, want) {
					t.Fatalf("typed composition = %#v, want %#v", values, want)
				}
				if code, stdout, stderr := runCommand(t, append([]string{"generate"}, selector...), root, commandGoEnvironment()); code != 0 {
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
				removalPath := `config["` + constructor + `"]["settings"]["remove"]`
				nilPath := `config["` + constructor + `"]["pointer"]`
				for _, invocation := range [][]string{{"generate", "--check"}, {"check"}, {"inspect", "configuration", "--format", "json"}, {"explain", "config", removalPath, "--format", "json"}, {"explain", "config", nilPath, "--format", "json"}} {
					args := append(append([]string(nil), invocation...), selector...)
					code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
					if code != 0 || strings.Contains(stdout+stderr, ".internal") || strings.Contains(stdout+stderr, "private_key") {
						t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
					}
					if invocation[0] == "explain" {
						document := decodeExplainCommandEnvelope(t, stdout)
						outcome := "effective"
						if invocation[2] == removalPath {
							outcome = "removed"
						}
						if document.Result.Decision.Outcome != outcome || document.Result.Change.Path != selectedPath {
							t.Fatalf("typed value explanation = %#v", document.Result)
						}
					}
					if !reflect.DeepEqual(commandTree(t, root), before) {
						t.Fatalf("%v mutated the Project", args)
					}
				}
				if string(readCommandFile(t, root, "plystra.yaml")) != rootData || string(readCommandFile(t, root, selectedPath)) != selectedData {
					t.Fatal("generation changed authored configuration")
				}
				assertNoCommandTransactions(t, root)
			})
		}
	}
}

func TestPublicInvalidTypedConfigurationDoesNotMutate(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandNullableConfigurationImplementation(t, root)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			selected := "plystra.yaml"
			var selector []string
			switch mode {
			case "environment":
				selected, selector = "plystra.production.yaml", []string{"--env", "production"}
			case "replacement":
				selected, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
			}
			for _, value := range []string{
				"{settings: null}", "{settings: {keep: null}}", "{labels: {$remove: private_value}}",
				"{labels: {private_key: {$remove: true}}}", "{pointer: {$remove: true, private_key: private_value}}",
				"{pointer: !!null private_value}", "{items: !!null private_value}", "{labels: !!null private_value}",
			} {
				writeCommandFile(t, filepath.Join(root, selected), "interfaces: {use: {email.send/v1: "+constructor+"}}\nconfig: {"+constructor+": "+value+"}\n")
				before := commandTree(t, root)
				for _, invocation := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
					args := append(append([]string(nil), invocation...), selector...)
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
