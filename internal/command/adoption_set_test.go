package command_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
)

func TestPublicAdoptionSetReplacementRetainsSuppressionEvidence(t *testing.T) {
	for _, test := range []struct {
		name, overlay string
		bindings      int
		suppressed    bool
	}{
		{"empty", "composition: {adopt: []}", 0, true},
		{"subset", "composition: {adopt: [{module: example.com/acme/policy, export: empty}]}", 0, true},
		{"sparse removal", "composition: {adopt: {remove: [{module: example.com/acme/policy, export: email}]}}", 0, false},
		{"omitted", "{}", 1, false},
		{"sparse empty", "composition: {adopt: {}}", 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeCommandPolicyProject(t)
			rootData := "composition:\n  exports:\n    email:\n      interfaces: {require: [email.send/v1]}\n    empty: {}\n  adopt: [{module: example.com/acme/policy, export: email}, {module: example.com/acme/policy, export: empty}]\n"
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootData)
			writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), test.overlay+"\n")
			if code, stdout, stderr := runCommand(t, []string{"generate", "--env", "production"}, root, commandGoEnvironment()); code != 0 {
				t.Fatalf("generate = %d, %q, %q", code, stdout, stderr)
			}
			manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			if bindings := manifest.InterfaceProvenance().Bindings(); len(bindings) != test.bindings {
				t.Fatalf("effective bindings = %#v, want %d", bindings, test.bindings)
			}
			before := commandTree(t, root)
			for _, invocation := range [][]string{{"generate", "--check"}, {"check"}, {"inspect", "configuration", "--format", "json"}, {"explain", "config", `composition.adopt["example.com/acme/policy#email"]`, "--format", "json"}} {
				args := append(append([]string(nil), invocation...), "--env", "production")
				code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
				if code != 0 {
					t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
				}
				if invocation[0] == "inspect" {
					document := decodeInspectGraphCommandEnvelope(t, stdout)
					_, exists := findInspectGraphEdge(document.Result.Edges, "suppresses-configuration", "configuration-field:composition.adopt", `configuration-field:composition.adopt["example.com/acme/policy#email"]`, "ancestor-replacement")
					if exists != test.suppressed {
						t.Fatalf("adoption suppression = %t, want %t", exists, test.suppressed)
					}
				}
				if invocation[0] == "explain" && test.suppressed {
					document := decodeExplainCommandEnvelope(t, stdout)
					if document.Result.Decision.Outcome != "suppressed" || document.Result.Reason.Code != "ancestor-replacement" || document.Result.Change.Field != "composition.adopt" || document.Result.Change.Path != "plystra.production.yaml" {
						t.Fatalf("adoption explanation = %#v", document.Result)
					}
					if sources := document.Result.Reason.Sources; len(sources) != 1 || sources[0].Module != "example.com/acme/policy" || sources[0].Path != "plystra.production.yaml" {
						t.Fatalf("adoption suppression source = %#v", sources)
					}
				}
				if !reflect.DeepEqual(commandTree(t, root), before) {
					t.Fatalf("%v changed the Project", args)
				}
			}
			if string(readCommandFile(t, root, "plystra.yaml")) != rootData || string(readCommandFile(t, root, "plystra.production.yaml")) != test.overlay+"\n" {
				t.Fatal("generation changed authored adoption intent")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicReplacementAdoptionSetExcludesRootAdoptions(t *testing.T) {
	for _, selected := range []string{"{}", "composition: {adopt: []}", "composition: {adopt: {}}"} {
		t.Run(selected, func(t *testing.T) {
			root := writeCommandPolicyProject(t)
			rootData := "composition:\n  exports:\n    email:\n      interfaces: {require: [email.send/v1]}\n  adopt: [{module: example.com/acme/policy, export: email}]\n"
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootData)
			writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), selected+"\n")
			if code, stdout, stderr := runCommand(t, []string{"generate", "--config", "deploy/customer.yaml"}, root, commandGoEnvironment()); code != 0 {
				t.Fatalf("generate = %d, %q, %q", code, stdout, stderr)
			}
			manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			if bindings := manifest.InterfaceProvenance().Bindings(); len(bindings) != 0 {
				t.Fatalf("replacement inherited root adoptions: %#v", bindings)
			}
			before := commandTree(t, root)
			for _, invocation := range [][]string{{"generate", "--check"}, {"check"}, {"inspect", "configuration", "--format", "json"}} {
				args := append(invocation, "--config", "deploy/customer.yaml")
				if code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment()); code != 0 {
					t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
				}
				if !reflect.DeepEqual(commandTree(t, root), before) {
					t.Fatalf("%v changed the Project", args)
				}
			}
			if string(readCommandFile(t, root, "plystra.yaml")) != rootData || string(readCommandFile(t, root, "deploy/customer.yaml")) != selected+"\n" {
				t.Fatal("generation changed authored adoption intent")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}
