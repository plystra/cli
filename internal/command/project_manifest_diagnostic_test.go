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
		},
		{
			name: "malformed-dependency-manifest",
			setup: func(t *testing.T, parent string) string {
				return writeManifestDiagnosticDependencyProject(t, parent, "unknown: true\n", false)
			},
			wantSource: "Source: example.com/dependency:plystra.yaml:1:1 (project-marker)",
		},
		{
			name: "malformed-current-template-relationship",
			setup: func(t *testing.T, parent string) string {
				applicationRoot := filepath.Join(parent, "application")
				writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), "module example.com/application\n\ngo 1.26\n")
				writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "template: [private-value]\n")
				return applicationRoot
			},
			wantSource: "Source: example.com/application:plystra.yaml:1:1 (configuration-declaration)",
		},
		{
			name: "malformed-template-ancestor-relationship",
			setup: func(t *testing.T, parent string) string {
				return writeManifestDiagnosticDependencyProject(t, parent, "template: {private-key: private-value}\n", false)
			},
			wantSource: "Source: example.com/dependency:plystra.yaml:1:1 (configuration-declaration)",
		},
		{
			name: "unsupported-current-resource",
			setup: func(t *testing.T, parent string) string {
				applicationRoot := filepath.Join(parent, "application")
				writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), "module example.com/application\n\ngo 1.26\n")
				writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "resources: {instances: {database: {config: {private-key: 1, private-key: 2}}}}\n")
				return applicationRoot
			},
			wantSource: "Source: example.com/application:plystra.yaml:1:1 (project-marker)",
		},
		{
			name: "unsupported-template-resource",
			setup: func(t *testing.T, parent string) string {
				return writeManifestDiagnosticDependencyProject(t, parent, "resources: {bind: {private-key: private-value}}\n", false)
			},
			wantSource: "Source: example.com/dependency:plystra.yaml:1:1 (project-marker)",
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
		},
		{
			name: "unsafe-dependency-marker",
			setup: func(t *testing.T, parent string) string {
				return writeManifestDiagnosticDependencyProject(t, parent, "", true)
			},
			wantSource: "Source: example.com/dependency:plystra.yaml (project-marker)",
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
			wantSource: "Source: example.com/application:plystra.yaml:1:1 (project-marker)",
		}, manifestCase{
			name: "invalid-dependency-resource-value/" + value,
			setup: func(t *testing.T, parent string) string {
				return writeManifestDiagnosticDependencyProject(t, parent, manifest, false)
			},
			wantSource: "Source: example.com/dependency:plystra.yaml:1:1 (project-marker)",
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
					if strings.Contains(test.name, "relationship") {
						code = diagnosticcode.TemplateInvalid
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

func writeManifestDiagnosticDependencyProject(t *testing.T, parent, manifest string, unsafe bool) string {
	t.Helper()
	applicationRoot := filepath.Join(parent, "application")
	dependencyRoot := filepath.Join(parent, "dependency")
	writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), "module example.com/application\n\ngo 1.26\n\nrequire example.com/dependency v0.0.0\n\nreplace example.com/dependency => ../dependency\n")
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "template: example.com/dependency\n")
	writeCommandFile(t, filepath.Join(dependencyRoot, "go.mod"), "module example.com/dependency\n\ngo 1.26\n")
	if unsafe {
		writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml", "sentinel.txt"), "preserve\n")
	} else {
		writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), manifest)
	}
	return applicationRoot
}
