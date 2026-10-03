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

func TestPublicPointerConfigurationReplacement(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	for _, test := range []struct {
		mode   string
		active bool
	}{
		{"default", false}, {"environment", false}, {"replacement", false},
		{"default", true}, {"environment", true}, {"replacement", true},
	} {
		name := test.mode + "/dormant"
		if test.active {
			name = test.mode + "/active"
		}
		t.Run(name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandPointerConfigurationImplementation(t, root)
			inventory, _ := writeCommandTemplate(t, root, "pointer", "config: {"+constructor+": {settings: {first: inherited.internal, second: lower.internal}}}\n")
			selection := "interfaces: {use: {email.send/v1: " + constructor + "}}\n"
			if test.active {
				selection = "interfaces: {require: [email.send/v1], use: {email.send/v1: " + constructor + "}}\n"
			}
			configuration := "config: {" + constructor + ": {settings: {first: current.internal}}}\n"
			rootData := inventory + selection + configuration
			selectedPath, selectedData := "plystra.yaml", rootData
			options := applicationresolve.Options{Start: root, Environment: commandGoEnvironment()}
			var selector []string
			switch test.mode {
			case "environment":
				rootData = inventory + selection + "config: {" + constructor + ": {settings: {first: root.internal, second: root-only.internal}}}\n"
				selectedPath, selectedData = "plystra.production.yaml", configuration
				selector = []string{"--env", "production"}
				options.EnvironmentName = "production"
			case "replacement":
				rootData = inventory + selection + "config: {" + constructor + ": {settings: {second: root-only.internal}}}\n"
				selectedPath, selectedData = "deploy/customer.yaml", selection+configuration
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
			if !exists {
				t.Fatal("resolved pointer configuration is absent")
			}
			var values struct{ Settings map[string]string }
			if err := yaml.Unmarshal(configured.YAML(), &values); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(values.Settings, map[string]string{"first": "current.internal"}) {
				t.Fatalf("pointer inherited omitted lower fields: %#v", values.Settings)
			}
			if code, stdout, stderr := runCommand(t, append([]string{"generate"}, selector...), root, commandGoEnvironment()); code != 0 {
				t.Fatalf("generate = %d, %q, %q", code, stdout, stderr)
			}
			manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			configurations := manifest.DormantConstructorConfigurations()
			pointerPath := `config["` + constructor + `"]["settings"]`
			if test.active {
				if len(configurations) != 0 || len(manifest.InterfaceProvenance().Bindings()) != 1 {
					t.Fatal("active pointer configuration has inconsistent runtime membership")
				}
			} else {
				if len(configurations) != 1 || len(manifest.InterfaceProvenance().Bindings()) != 0 {
					t.Fatal("pointer configuration did not remain dormant")
				}
				fields := configurations[0].Fields()
				if len(fields) != 2 || fields[1].Path() != pointerPath || fields[1].Summary() != "value" || !fields[1].Effective() || fields[1].Removed() || len(fields[1].Contributions()) < 2 {
					t.Fatalf("pointer configuration is not one atomic value with retained sources: %#v", fields)
				}
			}
			before := commandTree(t, root)
			for _, invocation := range [][]string{{"generate", "--check"}, {"check"}, {"inspect", "configuration", "--format", "json"}, {"explain", "config", pointerPath, "--format", "json"}} {
				args := append(append([]string(nil), invocation...), selector...)
				code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
				if code != 0 || strings.Contains(stdout+stderr, ".internal") {
					t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
				}
				if invocation[0] == "explain" {
					document := decodeExplainCommandEnvelope(t, stdout)
					if document.Result.Decision.Outcome != "effective" || document.Result.Change.Path != selectedPath {
						t.Fatalf("pointer replacement explanation = %#v", document.Result)
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

func TestPublicPointerConfigurationUsesNearestTemplateWithoutMergingAtomicFields(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	root := writeImplementationSelectionCommandProject(t)
	writeCommandPointerConfigurationImplementation(t, root)
	ancestor, oldest := writeCommandTemplate(t, root, "oldest-pointer", "config: {"+constructor+": {settings: {first: private-first}}}\n")
	relationship, nearest := writeCommandTemplate(t, root, "nearest-pointer", ancestor+"config: {"+constructor+": {settings: {second: private-second}}}\n")
	configuration := relationship + "interfaces: {use: {email.send/v1: " + constructor + "}}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), configuration)
	oldestBefore, nearestBefore := commandTree(t, oldest), commandTree(t, nearest)
	resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, Environment: commandGoEnvironment()})
	if err != nil {
		t.Fatal(err)
	}
	symbol, err := constructorsymbol.Parse(constructor)
	if err != nil {
		t.Fatal(err)
	}
	configured, exists := resolved.Manifest().Configuration(symbol)
	if !exists {
		t.Fatal("inherited pointer configuration is absent")
	}
	var values struct{ Settings map[string]string }
	if err := yaml.Unmarshal(configured.YAML(), &values); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(values.Settings, map[string]string{"second": "private-second"}) {
		t.Fatalf("nearest pointer inherited atomic fields: %#v", values.Settings)
	}
	if code, _, stderr := runCommand(t, []string{"generate"}, root, commandGoEnvironment()); code != 0 {
		t.Fatalf("generate = %d, %s", code, stderr)
	}
	before := commandTree(t, root)
	for _, invocation := range [][]string{{"generate", "--check"}, {"check"}, {"inspect", "configuration", "--format", "json"}} {
		code, stdout, stderr := runCommand(t, invocation, root, commandGoEnvironment())
		if code != 0 || strings.Contains(stdout+stderr, "private-") {
			t.Fatalf("%v = %d, %q, %q", invocation, code, stdout, stderr)
		}
		if !reflect.DeepEqual(commandTree(t, root), before) {
			t.Fatalf("%v mutated Project", invocation)
		}
	}
	if string(readCommandFile(t, root, "plystra.yaml")) != configuration || !reflect.DeepEqual(commandTree(t, oldest), oldestBefore) || !reflect.DeepEqual(commandTree(t, nearest), nearestBefore) {
		t.Fatal("composition changed authored template inputs")
	}
	assertNoCommandTransactions(t, root)
}

func writeCommandPointerConfigurationImplementation(t testing.TB, root string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, "smtp", "implementation.go"), `package smtp

import (
	"context"
	contract "example.com/acme/implementation-use/interfaces/email/send/v1"
)

type Config struct { Settings *struct { First string; Second string } }
type Service struct{}

//plystra:implements email.send/v1
func New(Config) (*Service, error) { return &Service{}, nil }
func (*Service) Send(context.Context, contract.Request) (contract.Response, error) { return contract.Response{}, nil }
var _ contract.Interface = (*Service)(nil)
`)
}
