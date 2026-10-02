package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
)

func TestPublicInterfaceRequirementCompleteSets(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		for _, test := range []struct {
			name, lower, selected string
			bindings              int
		}{
			{"empty", "", "interfaces: {require: []}", 0},
			{"subset", "", "interfaces: {require: [email.send/v1]}", 1},
			{"sparse removal", "", "interfaces: {require: {remove: [missing.read/v1]}}", 1},
			{"sparse over complete", "interfaces: {require: []}", "interfaces: {require: {add: [email.send/v1]}}", 1},
		} {
			if test.name == "sparse over complete" && mode != "environment" {
				continue
			}
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				root := writeCommandPolicyProject(t)
				dependency := t.TempDir()
				writeCommandFile(t, filepath.Join(dependency, "go.mod"), "module example.com/requirements\n\ngo 1.26\n")
				writeCommandFile(t, filepath.Join(dependency, "plystra.yaml"), "composition:\n  exports:\n    defaults:\n      interfaces: {require: [missing.read/v1, email.send/v1]}\n")
				mod := string(readCommandFile(t, root, "go.mod"))
				writeCommandFile(t, filepath.Join(root, "go.mod"), mod+"\nrequire example.com/requirements v1.0.0\nreplace example.com/requirements => "+filepath.ToSlash(dependency)+"\n")
				inventory := "composition:\n  exports:\n    defaults:\n      interfaces: {require: [missing.read/v1, email.send/v1]}\n"
				adopt := "  adopt: [{module: example.com/requirements, export: defaults}, {module: example.com/acme/policy, export: defaults}]\n"
				rootData := inventory + adopt + test.selected + "\n"
				selectedData, selectedPath := rootData, "plystra.yaml"
				var selector []string
				switch mode {
				case "environment":
					rootData = inventory + adopt + test.lower + "\n"
					selectedData, selectedPath = test.selected+"\n", "plystra.production.yaml"
					selector = []string{"--env", "production"}
				case "replacement":
					rootData = inventory + "interfaces: {require: [excluded.root/v1]}\n"
					selectedData, selectedPath = "composition:\n"+adopt+test.selected+"\n", "deploy/customer.yaml"
					selector = []string{"--config", selectedPath}
				}
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootData)
				writeCommandFile(t, filepath.Join(root, selectedPath), selectedData)
				dependencyBefore := commandTree(t, dependency)
				args := append([]string{"generate"}, selector...)
				if code, stdout, stderr := runCommand(t, args, filepath.Join(root, "smtp"), commandGoEnvironment()); code != 0 {
					t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
				}
				manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
				if err != nil {
					t.Fatal(err)
				}
				bindings := manifest.InterfaceProvenance().Bindings()
				if len(bindings) != test.bindings {
					t.Fatalf("effective bindings = %#v, want %d", bindings, test.bindings)
				}
				if test.bindings == 1 && test.name != "sparse removal" {
					for _, source := range bindings[0].RootSources() {
						if strings.Contains(source, "composition.exports") || strings.Contains(source, "example.com/requirements") {
							t.Fatalf("suppressed adopted requirement retained effective ownership: %s", source)
						}
					}
				}
				before := commandTree(t, root)
				for _, invocation := range [][]string{{"generate", "--check"}, {"check"}, {"inspect", "interfaces", "--format", "json"}, {"inspect", "configuration", "--format", "json"}, {"explain", "config", `interfaces.require["missing.read/v1"]`, "--format", "json"}} {
					args := append(append([]string(nil), invocation...), selector...)
					code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
					if code != 0 {
						t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
					}
					if invocation[0] == "inspect" && invocation[1] == "configuration" && test.name != "sparse removal" {
						document := decodeInspectGraphCommandEnvelope(t, stdout)
						if _, exists := findInspectGraphEdge(document.Result.Edges, "suppresses-configuration", "configuration-field:interfaces.require", `configuration-field:interfaces.require["missing.read/v1"]`, "ancestor-replacement"); !exists {
							t.Fatal("configuration graph omits complete-set suppression")
						}
					}
					if !reflect.DeepEqual(commandTree(t, root), before) {
						t.Fatalf("%v changed authored or generated files", args)
					}
				}
				if string(readCommandFile(t, root, "plystra.yaml")) != rootData || string(readCommandFile(t, root, selectedPath)) != selectedData || !reflect.DeepEqual(commandTree(t, dependency), dependencyBefore) {
					t.Fatal("generation changed authored requirements or dependency files")
				}
				assertNoCommandTransactions(t, root)
			})
		}
	}
}

func TestPublicEmptyRequirementSetPreservesExposureAndConstructorDependencies(t *testing.T) {
	root := writeCommandPolicyProject(t)
	writeCommandGraphInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeCommandFile(t, filepath.Join(root, "app", "service.go"), `package app

import (
	"context"
	runv1 "example.com/acme/policy/interfaces/app/run/v1"
	sendv1 "example.com/acme/policy/interfaces/email/send/v1"
)

type Service struct{}

//plystra:implements app.run/v1
func New(sender sendv1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) Run(context.Context, runv1.Request) (runv1.Response, error) {
	return runv1.Response{}, nil
}
`)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: []}\nhttp: {expose: {app.run/v1: {transport: connect}}}\n")
	if code, stdout, stderr := runCommand(t, []string{"generate"}, root, commandGoEnvironment()); code != 0 {
		t.Fatalf("generate = %d, %q, %q", code, stdout, stderr)
	}
	manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	bindings := manifest.InterfaceProvenance().Bindings()
	if len(bindings) != 2 || bindings[0].InterfaceID() != "app.run/v1" || len(bindings[0].ExposureSources()) != 1 || bindings[1].InterfaceID() != "email.send/v1" || len(bindings[1].RequiringConstructors()) != 1 || len(bindings[1].RootSources()) != 0 {
		t.Fatalf("empty explicit set suppressed independent requirements: %#v", bindings)
	}
	if code, stdout, stderr := runCommand(t, []string{"check"}, root, commandGoEnvironment()); code != 0 {
		t.Fatalf("check = %d, %q, %q", code, stdout, stderr)
	}
}
