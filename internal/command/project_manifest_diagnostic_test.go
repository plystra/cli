package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestRunReportsProjectManifestSourcesWithoutMutation(t *testing.T) {
	commands := []struct {
		name      string
		arguments []string
	}{
		{name: "generate", arguments: []string{"generate"}},
		{name: "generate-check", arguments: []string{"generate", "--check"}},
		{name: "check", arguments: []string{"check"}},
	}
	type manifestCase struct {
		name       string
		setup      func(*testing.T, string) string
		wantSource string
		wantCode   string
	}
	tests := []manifestCase{
		{
			name: "malformed-current-manifest",
			setup: func(t *testing.T, parent string) string {
				applicationRoot := filepath.Join(parent, "application")
				writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), "module example.com/application\n\ngo 1.26\n")
				writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "unknown: true\n")
				return applicationRoot
			},
			wantSource: "Source: example.com/application:plystra.yaml:1:1 (project-marker)",
			wantCode:   diagnosticcode.ProjectManifestInvalid,
		},
		{
			name: "unsupported-current-resource",
			setup: func(t *testing.T, parent string) string {
				applicationRoot := filepath.Join(parent, "application")
				writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), "module example.com/application\n\ngo 1.26\n")
				writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "resources: {instances: {database: {config: {private-key: 1, private-key: 2}}}}\n")
				return applicationRoot
			},
			wantSource: "Source: example.com/application:plystra.yaml:1:44 (configuration-declaration)",
			wantCode:   diagnosticcode.ResourceMetadataInvalid,
		},
		{
			name: "unsafe-current-marker",
			setup: func(t *testing.T, parent string) string {
				applicationRoot := filepath.Join(parent, "application")
				writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), "module example.com/application\n\ngo 1.26\n")
				writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml", "sentinel.txt"), "preserve\n")
				return applicationRoot
			},
			wantSource: "Source: example.com/application:plystra.yaml (project-marker)",
			wantCode:   diagnosticcode.ProjectManifestInvalid,
		},
		{
			name: "unsafe-dependency-marker",
			setup: func(t *testing.T, parent string) string {
				return writeManifestDiagnosticDependencyProject(t, parent, "", true)
			},
			wantSource: "Source: example.com/dependency:plystra.yaml (project-marker)",
			wantCode:   diagnosticcode.ProjectManifestInvalid,
		},
	}

	for _, value := range []string{
		"!!int private-value", "!!bool private-value", "!!float private-value",
		"!!timestamp private-value", "!!binary private-value", "!!null private-value",
		"[!!int private-value]", "{private-key: !!null private-value}",
		"{private-key: 1, private-key: 2}", "[{private-key: 1, private-key: 2}]",
		"{1: private-value}", "[{1: private-value}]",
	} {
		manifest := "resources: {instances: {database: {config: {value: " + value + "}}}}\n"
		tests = append(tests, manifestCase{
			name: "invalid-current-resource-value/" + value,
			setup: func(t *testing.T, parent string) string {
				root := filepath.Join(parent, "application")
				writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/application\n\ngo 1.26\n")
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), manifest)
				return root
			},
			wantSource: "Source: example.com/application:plystra.yaml:1:44 (configuration-declaration)",
			wantCode:   diagnosticcode.ResourceMetadataInvalid,
		})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			applicationRoot := test.setup(t, parent)
			before := commandTree(t, parent)

			for _, command := range commands {
				t.Run(command.name, func(t *testing.T) {
					exitCode, stdout, stderr := runCommand(t, command.arguments, applicationRoot, commandGoEnvironment())
					if exitCode != 1 || stdout != "" {
						t.Fatalf("%v = exit %d, stdout %q, stderr %q", command.arguments, exitCode, stdout, stderr)
					}
					if !strings.Contains(stderr, "\n\n"+test.wantSource+"\n\nRecovery:\n") || strings.Count(stderr, "Source: ") != 1 {
						t.Fatalf("%v source output = %q, want exactly %q", command.arguments, stderr, test.wantSource)
					}
					code := diagnosticcode.ProjectManifestInvalid
					if test.wantCode != "" {
						code = test.wantCode
					}
					if !strings.HasSuffix(stderr, "Diagnostic: "+code+"\n") || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
						t.Fatalf("%v diagnostic envelope = %q", command.arguments, stderr)
					}
					for _, privatePath := range []string{parent, filepath.ToSlash(parent), applicationRoot, filepath.ToSlash(applicationRoot), "private-key", "private-value"} {
						if strings.Contains(stderr, privatePath) {
							t.Fatalf("%v exposed private path %q: %q", command.arguments, privatePath, stderr)
						}
					}
					if after := commandTree(t, parent); !reflect.DeepEqual(after, before) {
						t.Fatalf("%v mutated the rejected Project tree:\nbefore: %#v\nafter:  %#v", command.arguments, before, after)
					}
					assertNoCommandTransactions(t, applicationRoot)
					assertNoCommandTransactions(t, filepath.Join(parent, "dependency"))
				})
			}
		})
	}
}

func TestRunIgnoresDependencyApplicationConfiguration(t *testing.T) {
	parent := t.TempDir()
	root := writeManifestDiagnosticDependencyProject(t, parent, "unknown: dependency-only-value\ninterfaces: {require: [missing.read/v1]}\n", false)
	dependency := filepath.Join(parent, "dependency")
	before := commandTree(t, dependency)
	for _, arguments := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
		code, _, stderr := runCommand(t, arguments, root, commandGoEnvironment())
		if code != 0 || stderr != "" {
			t.Fatalf("%v = %d, %q", arguments, code, stderr)
		}
		if !reflect.DeepEqual(commandTree(t, dependency), before) {
			t.Fatalf("%v changed dependency Project", arguments)
		}
	}
	if strings.Contains(string(readCommandFile(t, root, "generated/manifest.json")), "dependency-only-value") {
		t.Fatal("dependency configuration entered generated provenance")
	}
}

func writeManifestDiagnosticDependencyProject(t *testing.T, parent, manifest string, unsafe bool) string {
	t.Helper()
	applicationRoot := filepath.Join(parent, "application")
	dependencyRoot := filepath.Join(parent, "dependency")
	writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), "module example.com/application\n\ngo 1.26\n\nrequire example.com/dependency v0.0.0\n\nreplace example.com/dependency => ../dependency\n")
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(dependencyRoot, "go.mod"), "module example.com/dependency\n\ngo 1.26\n")
	if unsafe {
		writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml", "sentinel.txt"), "preserve\n")
	} else {
		writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), manifest)
	}
	return applicationRoot
}
