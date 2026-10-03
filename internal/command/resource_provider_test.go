package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func commandResourceProviderSource(module string) string {
	return "package provider\nimport api \"" + module + "/api\"\ntype Config struct{Limit int}\ntype database struct{}\nfunc (*database) Read() api.Value{return api.Value{}}\n//plystra:implements-resource data.database/v1\nfunc New(cfg Config, primary api.Resource)(*database,error){panic(\"constructor-entry-marker\")}\n"
}

func TestPublicCommandsRejectInvalidResourceProvidersWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, old, replacement, code, kind string }{
		{"identity", "data.database/v1", "invalid-private-marker", diagnosticcode.ResourceProviderDeclarationInvalid, "resource-provider-declaration"},
		{"duplicate", "//plystra:implements-resource data.database/v1", "//plystra:implements-resource data.database/v1\n//plystra:implements-resource data.database/v1", diagnosticcode.ResourceProviderDeclarationInvalid, "resource-provider-declaration"},
		{"mixed", "//plystra:implements-resource data.database/v1", "//plystra:implements-resource data.database/v1\n//plystra:implements data.database/v1", diagnosticcode.ResourceProviderDeclarationInvalid, "resource-provider-declaration"},
		{"unknown", "data.database/v1", "data.missing/v1", diagnosticcode.ResourceProviderInvalid, "resource-provider-constructor"},
		{"conformance", "Read() api.Value", "Other() api.Value", diagnosticcode.ResourceProviderInvalid, "resource-provider-constructor"},
		{"config", "cfg Config", "cfg *Config", diagnosticcode.ResourceProviderInvalid, "resource-provider-constructor"},
		{"private-default", "Limit int", "Limit int `plystra-default:\"PRIVATE_DEFAULT_MARKER\"`", diagnosticcode.ResourceProviderInvalid, "resource-provider-constructor"},
		{"blank", "primary api.Resource", "_ api.Resource", diagnosticcode.ResourceProviderInvalid, "resource-provider-constructor"},
		{"unnamed", "cfg Config, primary api.Resource", "api.Resource", diagnosticcode.ResourceProviderInvalid, "resource-provider-constructor"},
		{"dependency", "primary api.Resource", "primary interface{Read() api.Value}", diagnosticcode.ResourceProviderInvalid, "resource-provider-constructor"},
		{"result", "(*database,error)", "(api.Resource,error)", diagnosticcode.ResourceProviderInvalid, "resource-provider-constructor"},
	} {
		for _, dependency := range []bool{false, true} {
			name := test.name + "/local"
			if dependency {
				name = test.name + "/dependency"
			}
			t.Run(name, func(t *testing.T) {
				root, _ := createInspectModuleGraphProject(t)
				owner, module := root, "example.com/acme/inspect"
				if dependency {
					owner, module = filepath.Join(root, "library"), "example.com/acme/library"
				}
				writeCommandFile(t, filepath.Join(owner, "api", "resource.go"), commandResourceSource)
				writeCommandFile(t, filepath.Join(owner, "provider", "provider.go"), strings.Replace(commandResourceProviderSource(module), test.old, test.replacement, 1))
				writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
				writeCommandFile(t, filepath.Join(root, "deploy", "replacement.yaml"), "{}\n")
				before := snapshotInspectProject(t, root)
				for _, arguments := range [][]string{{"generate"}, {"generate", "--check", "--env", "production"}, {"check", "--config", "deploy/replacement.yaml"}, {"inspect", "resources", "--format", "json"}} {
					code, stdout, stderr := runCommand(t, arguments, root, inspectCommandEnvironment(nil))
					if code != 1 || !strings.Contains(stderr, "Diagnostic: "+test.code) || !strings.Contains(stderr, "Source: "+module+":provider/provider.go:") || !strings.Contains(stderr, "("+test.kind+")") {
						t.Fatalf("%v = %d, %s, %s", arguments, code, stdout, stderr)
					}
					for _, private := range []string{root, "invalid-private-marker", "constructor-entry-marker", "resolved-secret-marker", "PRIVATE_DEFAULT_MARKER"} {
						if strings.Contains(stdout+stderr, private) {
							t.Fatalf("disclosed %q: %s %s", private, stdout, stderr)
						}
					}
					if !reflect.DeepEqual(before, snapshotInspectProject(t, root)) {
						t.Fatalf("%v mutated invalid Project", arguments)
					}
				}
			})
		}
	}
}

func TestResourceProviderPackageErrorUsesDependencyRelativeSource(t *testing.T) {
	t.Parallel()
	for _, directory := range []string{"", "provider"} {
		t.Run(directory, func(t *testing.T) {
			root, _ := createInspectModuleGraphProject(t)
			owner := filepath.Join(root, "library")
			writeCommandFile(t, filepath.Join(owner, directory, "broken.go"), "package library\nimport \"example.com/missing\"\n//plystra:implements-resource data.database/v1\nfunc New() (*missing.Value,error) { panic(\"constructor-entry-marker\") }\n")
			before := snapshotInspectProject(t, root)
			for _, arguments := range [][]string{{"generate"}, {"inspect", "resources", "--format", "json"}} {
				code, stdout, stderr := runCommand(t, arguments, root, inspectCommandEnvironment(nil))
				source := "Source: example.com/acme/library:" + filepath.ToSlash(filepath.Join(directory, "broken.go")) + ":2:8 (authored-package)"
				if code != 1 || !strings.Contains(stderr, "Diagnostic: "+diagnosticcode.AuthoredPackageInvalid) || !strings.Contains(stderr, source) {
					t.Fatalf("%v = %d, %s, %s; want %s", arguments, code, stdout, stderr, source)
				}
				if strings.Contains(stdout+stderr, root) || strings.Contains(stdout+stderr, "constructor-entry-marker") || !reflect.DeepEqual(before, snapshotInspectProject(t, root)) {
					t.Fatal("package failure disclosed private input or mutated Project")
				}
			}
		})
	}
}
