package command_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestPublicGenerationCommandsReportMissingActivationRequirementSourcesWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := []struct {
		name      string
		arguments []string
	}{
		{name: "generate", arguments: []string{"generate"}},
		{name: "generate-check", arguments: []string{"generate", "--check"}},
		{name: "check", arguments: []string{"check"}},
	}
	for _, command := range commands {
		command := command
		t.Run(command.name, func(t *testing.T) {
			t.Parallel()

			root := writeMissingGenerationActivationProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "records"), commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/library:plystra.yaml:1:1 (declaration)\n" +
				"Source: example.com/acme/library:plystra.yaml:1:1 (exposure)\n\n" +
				"Recovery:\nAdd the missing generation.activations entry to the intended Plugin's plugin.yaml.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationActivationMissing + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "extensions.audit") || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 2 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") {
				t.Fatalf("%s exposed an absolute or Module Cache path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the missing-activation Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicGenerationCommandsReportConflictingActivationDeclarationSourcesWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := []struct {
		name      string
		arguments []string
	}{
		{name: "generate", arguments: []string{"generate"}},
		{name: "generate-check", arguments: []string{"generate", "--check"}},
		{name: "check", arguments: []string{"check"}},
	}
	for _, command := range commands {
		command := command
		t.Run(command.name, func(t *testing.T) {
			t.Parallel()

			root := writeConflictingGenerationActivationProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "password"), commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/legacy:legacy/plugin.yaml:7:7 (plugin-declaration)\n" +
				"Source: example.com/acme/library:password/plugin.yaml:8:7 (plugin-declaration)\n\n" +
				"Recovery:\nEdit plugin.yaml generation.activations so the reported namespace uses one exact activation Capability.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationActivationConflict + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "namespace \"authn\"") || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 2 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") {
				t.Fatalf("%s exposed an absolute or Module Cache path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the conflicting-activation Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicGenerationCommandsReportSelectedProviderExtensionSourcesWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := []struct {
		name      string
		arguments []string
	}{
		{name: "generate", arguments: []string{"generate"}},
		{name: "generate-check", arguments: []string{"generate", "--check"}},
		{name: "check", arguments: []string{"check"}},
	}
	for _, command := range commands {
		command := command
		t.Run(command.name, func(t *testing.T) {
			t.Parallel()

			root := writeSelectedProviderWithoutGenerationExtensionProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "records"), commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/legacy:audit-legacy/capabilities/audit.write/v1/capability.yaml:1:1 (provider-declaration)\n" +
				"Source: example.com/acme/legacy:legacy/capabilities/authn.session.verify/v1/capability.yaml:1:1 (provider-declaration)\n" +
				"Source: example.com/acme/library:plystra.yaml:1:1 (provider-selection)\n\n" +
				"Recovery:\nAdd a compatible generation declaration to the selected activation Provider's plugin.yaml.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationProviderExtensionMissing + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "selected provider \"acme.library.authn-legacy\"") || !strings.Contains(stderr, "compatible extension providers: [acme.library.authn-password]") || !strings.Contains(stderr, "selected provider \"acme.library.audit-legacy\"") || !strings.Contains(stderr, "compatible extension providers: [acme.library.audit]") || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 3 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") {
				t.Fatalf("%s exposed an absolute or Module Cache path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the selected-Provider-extension Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicGenerationCommandsReportActivationCycleSourcesWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := []struct {
		name      string
		arguments []string
	}{
		{name: "generate", arguments: []string{"generate"}},
		{name: "generate-check", arguments: []string{"generate", "--check"}},
		{name: "check", arguments: []string{"check"}},
	}
	for _, command := range commands {
		command := command
		t.Run(command.name, func(t *testing.T) {
			t.Parallel()

			root := writeGenerationActivationCycleProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "alpha"), commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/library:plystra.yaml:1:1 (activation)\n" +
				"Source: example.com/acme/library:plystra.yaml:1:1 (declaration)\n" +
				"Source: example.com/acme/library:plystra.yaml:1:1 (exposure)\n\n" +
				"Recovery:\nEdit the reported generation declarations to remove the dependency cycle or unordered token flow.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationActivationCycle + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "alpha.call/v1 --extensions.authn") || !strings.Contains(stderr, "authn.session.verify/v1 --extensions.audit") || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 3 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") {
				t.Fatalf("%s exposed an absolute or Module Cache path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the activation-cycle Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicGenerationCommandsReportDependencyCycleSourcesWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := []struct {
		name      string
		arguments []string
	}{
		{name: "generate", arguments: []string{"generate"}},
		{name: "generate-check", arguments: []string{"generate", "--check"}},
		{name: "check", arguments: []string{"check"}},
	}
	for _, command := range commands {
		command := command
		t.Run(command.name, func(t *testing.T) {
			t.Parallel()

			root := writeGenerationDependencyCycleProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "order"), commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/library:audit/plugin.yaml:1:1 (generation-rule)\n" +
				"Source: example.com/acme/library:plystra.yaml:1:1 (declaration)\n" +
				"Source: example.com/acme/library:plystra.yaml:1:1 (exposure)\n\n" +
				"Recovery:\nEdit the reported generation declarations to remove the dependency cycle or unordered token flow.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationDependencyCycle + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "order.create/v1 --activation extensions.authn") || !strings.Contains(stderr, "authn.session.verify/v1 --generated by plugin \"acme.library.audit\" rule \"audit.require-order\" extensions.audit--> order.create/v1") || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 3 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") {
				t.Fatalf("%s exposed an absolute or Module Cache path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the dependency-cycle Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func writeMissingGenerationActivationProject(t *testing.T) string {
	t.Helper()

	root := writeCapabilityCommandModule(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `capabilities:
  require: [records.get/v1, records.list/v1]
http:
  expose: [records.get/v1, records.list/v1]
`)
	writeCommandFile(t, filepath.Join(root, "records", "plugin.yaml"), "id: acme.library.records\nprovides: [records.get/v1, records.list/v1]\n")
	writeCommandFile(t, filepath.Join(root, "records", "capabilities", "records.get", "v1", "capability.yaml"), `id: records.get/v1
request: {}
response: {}
errors: []
extensions:
  audit: {event: records.read}
`)
	writeCommandFile(t, filepath.Join(root, "records", "capabilities", "records.list", "v1", "capability.yaml"), `id: records.list/v1
request: {}
response: {}
errors: []
extensions:
  authn: {authenticated: true}
  audit: {event: records.listed}
`)
	return root
}

func writeConflictingGenerationActivationProject(t *testing.T) string {
	t.Helper()

	root := writeCapabilityCommandModule(t)
	legacyRoot := filepath.Join(root, "legacy-dependency")
	writeCommandFile(t, filepath.Join(legacyRoot, "go.mod"), "module example.com/acme/legacy\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(legacyRoot, "plystra.yaml"), "{}\n")
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read application go.mod: %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.mod"), string(goMod)+"\nrequire example.com/acme/legacy v1.2.3\n\nreplace example.com/acme/legacy => ./legacy-dependency\n")
	writeCommandFile(t, filepath.Join(legacyRoot, "legacy", "plugin.yaml"), `id: acme.library.authn-legacy
provides: [authn.token.verify/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: authn
      capability: authn.token.verify/v1
`)
	writeCommandFile(t, filepath.Join(legacyRoot, "legacy", "generation", "extension.go"), "package generation\n")
	writeCommandFile(t, filepath.Join(legacyRoot, "legacy", "capabilities", "authn.token.verify", "v1", "capability.yaml"), `id: authn.token.verify/v1
request: {}
response: {}
errors: []
`)
	writeCommandFile(t, filepath.Join(root, "password", "plugin.yaml"), `id: acme.library.authn-password
provides: [authn.session.verify/v1]
generation:
  api: v1
  package: ./generation
  activations:
    # Keep this declaration on a distinct trusted source line.
    - namespace: authn
      capability: authn.session.verify/v1
`)
	writeCommandFile(t, filepath.Join(root, "password", "generation", "extension.go"), "package generation\n")
	writeCommandFile(t, filepath.Join(root, "password", "capabilities", "authn.session.verify", "v1", "capability.yaml"), `id: authn.session.verify/v1
request: {}
response: {}
errors: []
`)
	return root
}

func writeSelectedProviderWithoutGenerationExtensionProject(t *testing.T) string {
	t.Helper()

	root := writeCapabilityCommandModule(t)
	legacyRoot := filepath.Join(root, "legacy-dependency")
	writeCommandFile(t, filepath.Join(legacyRoot, "go.mod"), "module example.com/acme/legacy\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(legacyRoot, "plystra.yaml"), "{}\n")
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read application go.mod: %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.mod"), string(goMod)+"\nrequire example.com/acme/legacy v1.2.3\n\nreplace example.com/acme/legacy => ./legacy-dependency\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `capabilities:
  require: [records.get/v1]
  use:
    authn.session.verify/v1: acme.library.authn-legacy
    audit.write/v1: acme.library.audit-legacy
`)
	writeCommandFile(t, filepath.Join(root, "records", "plugin.yaml"), "id: acme.library.records\nprovides: [records.get/v1]\n")
	writeCommandFile(t, filepath.Join(root, "records", "capabilities", "records.get", "v1", "capability.yaml"), `id: records.get/v1
request: {}
response: {}
errors: []
extensions:
  authn: {authenticated: true}
  audit: {durable: true}
`)
	verifyContract := `id: authn.session.verify/v1
request: {}
response: {}
errors: []
`
	writeCommandFile(t, filepath.Join(root, "password", "plugin.yaml"), `id: acme.library.authn-password
provides: [authn.session.verify/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: authn
      capability: authn.session.verify/v1
`)
	writeCommandFile(t, filepath.Join(root, "password", "generation", "extension.go"), "package generation\n")
	writeCommandFile(t, filepath.Join(root, "password", "capabilities", "authn.session.verify", "v1", "capability.yaml"), verifyContract)
	auditContract := `id: audit.write/v1
request: {}
response: {}
errors: []
`
	writeCommandFile(t, filepath.Join(root, "audit", "plugin.yaml"), `id: acme.library.audit
provides: [audit.write/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: audit
      capability: audit.write/v1
`)
	writeCommandFile(t, filepath.Join(root, "audit", "generation", "extension.go"), "package generation\n")
	writeCommandFile(t, filepath.Join(root, "audit", "capabilities", "audit.write", "v1", "capability.yaml"), auditContract)
	writeCommandFile(t, filepath.Join(legacyRoot, "legacy", "plugin.yaml"), "id: acme.library.authn-legacy\nprovides: [authn.session.verify/v1]\n")
	writeCommandFile(t, filepath.Join(legacyRoot, "legacy", "capabilities", "authn.session.verify", "v1", "capability.yaml"), verifyContract)
	writeCommandFile(t, filepath.Join(legacyRoot, "audit-legacy", "plugin.yaml"), "id: acme.library.audit-legacy\nprovides: [audit.write/v1]\n")
	writeCommandFile(t, filepath.Join(legacyRoot, "audit-legacy", "capabilities", "audit.write", "v1", "capability.yaml"), auditContract)
	return root
}

func writeGenerationActivationCycleProject(t *testing.T) string {
	t.Helper()

	root := writeCapabilityCommandModule(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `capabilities:
  require: [alpha.call/v1]
http:
  expose: [alpha.call/v1]
`)
	writeCommandFile(t, filepath.Join(root, "alpha", "plugin.yaml"), `id: acme.library.alpha
provides: [alpha.call/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: audit
      capability: alpha.call/v1
`)
	writeCommandFile(t, filepath.Join(root, "alpha", "generation", "extension.go"), "package generation\n")
	writeCommandFile(t, filepath.Join(root, "alpha", "capabilities", "alpha.call", "v1", "capability.yaml"), `id: alpha.call/v1
request: {}
response: {}
errors: []
extensions:
  authn: {authenticated: true}
`)
	writeCommandFile(t, filepath.Join(root, "authn", "plugin.yaml"), `id: acme.library.authn
provides: [authn.session.verify/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: authn
      capability: authn.session.verify/v1
`)
	writeCommandFile(t, filepath.Join(root, "authn", "generation", "extension.go"), "package generation\n")
	writeCommandFile(t, filepath.Join(root, "authn", "capabilities", "authn.session.verify", "v1", "capability.yaml"), `id: authn.session.verify/v1
request: {}
response: {}
errors: []
extensions:
  audit: {event: authn.verify}
`)
	return root
}

func writeGenerationDependencyCycleProject(t *testing.T) string {
	t.Helper()

	root := writeCapabilityCommandModule(t)
	cliRoot := commandRepositoryRoot(t)
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read application go.mod: %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.mod"), string(goMod)+`
require (
	github.com/plystra/cli v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/cli => `+filepath.ToSlash(cliRoot)+"\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `capabilities:
  require: [order.create/v1]
http:
  expose: [order.create/v1]
`)
	writeCommandFile(t, filepath.Join(root, "order", "plugin.yaml"), "id: acme.library.order\nprovides: [order.create/v1]\n")
	writeCommandFile(t, filepath.Join(root, "order", "capabilities", "order.create", "v1", "capability.yaml"), `id: order.create/v1
request: {}
response: {}
errors: []
extensions:
  authn: {authenticated: true}
`)
	writeCommandFile(t, filepath.Join(root, "authn", "plugin.yaml"), `id: acme.library.authn
provides: [authn.session.verify/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: authn
      capability: authn.session.verify/v1
`)
	writeCommandFile(t, filepath.Join(root, "authn", "generation", "extension.go"), emptyGenerationExtensionSource)
	writeCommandFile(t, filepath.Join(root, "authn", "capabilities", "authn.session.verify", "v1", "capability.yaml"), `id: authn.session.verify/v1
request: {}
response: {}
errors: []
extensions:
  audit: {durable: true}
`)
	writeCommandFile(t, filepath.Join(root, "audit", "plugin.yaml"), `id: acme.library.audit
provides: [audit.write/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: audit
      capability: audit.write/v1
`)
	writeCommandFile(t, filepath.Join(root, "audit", "generation", "extension.go"), dependencyCycleGenerationExtensionSource)
	writeCommandFile(t, filepath.Join(root, "audit", "capabilities", "audit.write", "v1", "capability.yaml"), `id: audit.write/v1
request: {}
response: {}
errors: []
`)
	return root
}

const emptyGenerationExtensionSource = `package generation

import generation "github.com/plystra/cli/generation/v1"

func Generate(generation.GenerationContext) (generation.Output, error) {
	return generation.Output{}, nil
}
`

const dependencyCycleGenerationExtensionSource = `package generation

import generation "github.com/plystra/cli/generation/v1"

func Generate(generation.GenerationContext) (generation.Output, error) {
	source, _ := generation.ParseCapabilityID("authn.session.verify/v1")
	requirement, _ := generation.ParseCapabilityID("order.create/v1")
	return generation.Output{Requirements: []generation.Requirement{{
		RuleID:     "audit.require-order",
		Namespace:  "audit",
		Source:     source,
		Capability: requirement,
	}}}, nil
}
`
