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

func TestPublicGenerationCommandsReportContributionCycleSourcesWithoutMutation(t *testing.T) {
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

			root := writeGenerationContributionCycleProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "order"), commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/library:audit/plugin.yaml:1:1 (generation-rule)\n" +
				"Source: example.com/acme/library:authn/plugin.yaml:1:1 (generation-rule)\n\n" +
				"Recovery:\nEdit the reported generation declarations to remove the dependency cycle or unordered token flow.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationContributionCycle + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "contribution \"audit.record\"") || !strings.Contains(stderr, "--token \"audit-recorded\"--> contribution \"authn.verify\"") || !strings.Contains(stderr, "--token \"authn-verified\"--> contribution \"audit.record\"") || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 2 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") {
				t.Fatalf("%s exposed an absolute or Module Cache path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the contribution-cycle Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicGenerationCommandsReportUnorderedContributionSourcesWithoutMutation(t *testing.T) {
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

			root := writeGenerationUnorderedContributionsProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "order"), commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/library:audit/plugin.yaml:1:1 (generation-rule)\n" +
				"Source: example.com/acme/library:authn/plugin.yaml:1:1 (generation-rule)\n\n" +
				"Recovery:\nEdit the reported generation declarations to remove the dependency cycle or unordered token flow.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationContributionsUnordered + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "ordered point \"invocation.prepare\" has simultaneously ready semantic work") || !strings.Contains(stderr, "contribution \"audit.record\"") || !strings.Contains(stderr, "contribution \"authn.attach\"") || !strings.Contains(stderr, "contribution \"authn.verify\"") || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 2 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") {
				t.Fatalf("%s exposed an absolute or Module Cache path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the unordered-contribution Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicGenerationCommandsReportRepeatedStateSourcesWithoutMutation(t *testing.T) {
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

			root := writeGenerationRepeatedStateProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "order"), commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/library:audit/plugin.yaml:1:1 (plugin-declaration)\n" +
				"Source: example.com/acme/library:authz/plugin.yaml:1:1 (plugin-declaration)\n\n" +
				"Recovery:\nMake the selected generation extensions deterministic and convergent for identical normalized input.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationStateRepeated + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "generation extension repeated input state with different output") || !strings.Contains(stderr, "first produced sha256:") || !strings.Contains(stderr, "and then sha256:") || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 2 || strings.Contains(stderr, ":authn/plugin.yaml:") || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") {
				t.Fatalf("%s exposed an absolute or Module Cache path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the repeated-state Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicGenerationCommandsReportUnsupportedAPISourceWithoutMutation(t *testing.T) {
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

			root := writeUnsupportedGenerationAPIProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, root, commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/unsupported:legacy/plugin.yaml:4:8 (plugin-declaration)\n\n" +
				"Recovery:\nEdit the Plugin generation declaration to use a supported API and a safe existing package, then rerun the command.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationAPIUnsupported + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, `generation.api "v2" is not supported`) || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 1 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") {
				t.Fatalf("%s exposed an absolute or Module Cache path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the unsupported-API Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicGenerationCommandsReportInvalidPackageSourceWithoutMutation(t *testing.T) {
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

			root := writeInvalidGenerationPackageProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, root, commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/invalid-package:legacy/plugin.yaml:5:12 (plugin-declaration)\n\n" +
				"Recovery:\nEdit the Plugin generation declaration to use a supported API and a safe existing package, then rerun the command.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationPackageInvalid + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, `generation.package "./generation/missing"`) || !strings.Contains(stderr, "does not exist") || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 1 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") {
				t.Fatalf("%s exposed an absolute or Module Cache path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the invalid-package Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicGenerationCommandsReportCompileFailureSourceWithoutMutation(t *testing.T) {
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

			root := writeGenerationCompileFailureProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "order"), commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/library:authn/plugin.yaml:5:12 (plugin-declaration)\n\n" +
				"Recovery:\nFix the selected generation package reported above, then rerun the command.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationCompileFailed + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "compile generation helper") || !strings.Contains(stderr, `plugin "acme.library.authn"`) || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 1 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") || strings.Contains(stderr, ".plystra-generation-") {
				t.Fatalf("%s exposed an absolute, Module Cache, or helper path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the compile-failure Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicGenerationCommandsReportInvocationFailureSourceWithoutMutation(t *testing.T) {
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

			root := writeGenerationInvocationFailureProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "order"), commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/library:authn/plugin.yaml:5:12 (plugin-declaration)\n\n" +
				"Recovery:\nFix the selected generation package reported above, then rerun the command.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationExtensionFailed + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "generation extension returned an error") || !strings.Contains(stderr, `plugin "acme.library.authn"`) || !strings.Contains(stderr, "extension failed in .") || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 1 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") || strings.Contains(stderr, ".plystra-generation-") || strings.Contains(stderr, `:\`) || strings.Contains(stderr, ":/") {
				t.Fatalf("%s exposed an absolute, Module Cache, or helper path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the invocation-failure Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicGenerationCommandsReportExtensionDiagnosticRuleSourceWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := []struct {
		name       string
		arguments  []string
		dependency bool
	}{
		{name: "generate", arguments: []string{"generate"}},
		{name: "generate-check", arguments: []string{"generate", "--check"}},
		{name: "check", arguments: []string{"check"}},
		{name: "generate-dependency", arguments: []string{"generate"}, dependency: true},
		{name: "generate-check-dependency", arguments: []string{"generate", "--check"}, dependency: true},
		{name: "check-dependency", arguments: []string{"check"}, dependency: true},
	}
	for _, command := range commands {
		command := command
		t.Run(command.name, func(t *testing.T) {
			t.Parallel()

			root := writeGenerationErrorDiagnosticProject(t, command.dependency)
			authnModule := "example.com/acme/library"
			if command.dependency {
				authnModule = "example.com/acme/security"
			}
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, command.arguments, filepath.Join(root, "order"), commandGoEnvironment())
			wantSuffix := "\n\n" +
				"Source: example.com/acme/library:audit/plugin.yaml:1:1 (generation-rule)\n" +
				"Source: " + authnModule + ":authn/plugin.yaml:1:1 (generation-rule)\n\n" +
				"Recovery:\nFix the selected generation package reported above, then rerun the command.\n\n" +
				"Diagnostic: " + diagnosticcode.GenerationExtensionDiagnostic + "\n"
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "generation extension reported an error") || !strings.Contains(stderr, `plugin "acme.library.audit"`) || !strings.Contains(stderr, `rule "audit.validate"`) || !strings.Contains(stderr, `plugin "acme.library.authn"`) || !strings.Contains(stderr, `rule "authn.require-session"`) || !strings.Contains(stderr, `rule "authn.validate"`) || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 2 || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("%s = exit %d, stdout %q, stderr %q", command.name, exitCode, stdout, stderr)
			}
			for _, excluded := range []string{"authn.advisory", "authentication metadata is discouraged", "audit/plugin.yaml:5:12 (plugin-declaration)", "authn/plugin.yaml:5:12 (plugin-declaration)"} {
				if strings.Contains(stderr, excluded) {
					t.Fatalf("%s exposed excluded diagnostic detail %q: %q", command.name, excluded, stderr)
				}
			}
			if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, "pkg\\mod") || strings.Contains(stderr, "pkg/mod") || strings.Contains(stderr, ".plystra-generation-") {
				t.Fatalf("%s exposed an absolute, Module Cache, or helper path: %q", command.name, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated the extension-diagnostic Project:\nbefore: %#v\nafter:  %#v", command.name, before, after)
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
  expose: {records.get/v1: {transport: connect}, records.list/v1: {transport: connect}}
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
  expose: {alpha.call/v1: {transport: connect}}
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
  expose: {order.create/v1: {transport: connect}}
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

func writeGenerationContributionCycleProject(t *testing.T) string {
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
  expose: {order.create/v1: {transport: connect}}
`)
	writeCommandFile(t, filepath.Join(root, "order", "plugin.yaml"), "id: acme.library.order\nprovides: [order.create/v1]\n")
	writeCommandFile(t, filepath.Join(root, "order", "capabilities", "order.create", "v1", "capability.yaml"), `id: order.create/v1
request: {}
response: {}
errors: []
extensions:
  authn: {authenticated: true}
  audit: {event: order.create}
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
	writeCommandFile(t, filepath.Join(root, "authn", "generation", "extension.go"), authnContributionCycleGenerationExtensionSource)
	writeCommandFile(t, filepath.Join(root, "authn", "capabilities", "authn.session.verify", "v1", "capability.yaml"), `id: authn.session.verify/v1
request: {}
response: {}
errors: []
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
	writeCommandFile(t, filepath.Join(root, "audit", "generation", "extension.go"), auditContributionCycleGenerationExtensionSource)
	writeCommandFile(t, filepath.Join(root, "audit", "capabilities", "audit.write", "v1", "capability.yaml"), `id: audit.write/v1
request: {}
response: {}
errors: []
`)
	return root
}

func writeGenerationUnorderedContributionsProject(t *testing.T) string {
	t.Helper()

	root := writeGenerationContributionCycleProject(t)
	writeCommandFile(t, filepath.Join(root, "authn", "generation", "extension.go"), authnUnorderedContributionsGenerationExtensionSource)
	writeCommandFile(t, filepath.Join(root, "audit", "generation", "extension.go"), auditUnorderedContributionsGenerationExtensionSource)
	return root
}

func writeGenerationRepeatedStateProject(t *testing.T) string {
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
`)
	writeCommandFile(t, filepath.Join(root, "order", "plugin.yaml"), "id: acme.library.order\nprovides: [order.create/v1]\n")
	writeCommandFile(t, filepath.Join(root, "order", "capabilities", "order.create", "v1", "capability.yaml"), `id: order.create/v1
request: {}
response: {}
errors: []
extensions:
  audit: {event: order.create}
  authn: {authenticated: true}
  authz: {permission: order.create}
  trace: {span: order.create}
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
`)
	writeCommandFile(t, filepath.Join(root, "audit", "plugin.yaml"), `id: acme.library.audit
provides: [audit.write/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: trace
      capability: audit.write/v1
    - namespace: audit
      capability: audit.write/v1
`)
	writeCommandFile(t, filepath.Join(root, "audit", "generation", "extension.go"), auditRepeatedStateGenerationExtensionSource)
	writeCommandFile(t, filepath.Join(root, "audit", "capabilities", "audit.write", "v1", "capability.yaml"), `id: audit.write/v1
request: {}
response: {}
errors: []
`)
	writeCommandFile(t, filepath.Join(root, "authz", "plugin.yaml"), `id: acme.library.authz
provides: [authz.check/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: authz
      capability: authz.check/v1
`)
	writeCommandFile(t, filepath.Join(root, "authz", "generation", "extension.go"), authzRepeatedStateGenerationExtensionSource)
	writeCommandFile(t, filepath.Join(root, "authz", "capabilities", "authz.check", "v1", "capability.yaml"), `id: authz.check/v1
request: {}
response: {}
errors: []
`)
	return root
}

func writeUnsupportedGenerationAPIProject(t *testing.T) string {
	t.Helper()

	root := writeCapabilityCommandModule(t)
	dependencyRoot := filepath.Join(root, "unsupported-dependency")
	writeCommandFile(t, filepath.Join(dependencyRoot, "go.mod"), "module example.com/acme/unsupported\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read application go.mod: %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.mod"), string(goMod)+"\nrequire example.com/acme/unsupported v1.2.3\n\nreplace example.com/acme/unsupported => ./unsupported-dependency\n")
	writeCommandFile(t, filepath.Join(dependencyRoot, "legacy", "plugin.yaml"), `id: acme.unsupported.legacy
provides: [authn.session.verify/v1]
generation:
  api: v2
  package: ./generation
  activations:
    - namespace: authn
      capability: authn.session.verify/v1
`)
	return root
}

func writeInvalidGenerationPackageProject(t *testing.T) string {
	t.Helper()

	root := writeCapabilityCommandModule(t)
	dependencyRoot := filepath.Join(root, "invalid-package-dependency")
	writeCommandFile(t, filepath.Join(dependencyRoot, "go.mod"), "module example.com/acme/invalid-package\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
	goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read application go.mod: %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.mod"), string(goMod)+"\nrequire example.com/acme/invalid-package v1.2.3\n\nreplace example.com/acme/invalid-package => ./invalid-package-dependency\n")
	writeCommandFile(t, filepath.Join(dependencyRoot, "legacy", "plugin.yaml"), `id: acme.invalid-package.legacy
provides: [authn.session.verify/v1]
generation:
  api: v1
  package: ./generation/missing
  activations:
    - namespace: authn
      capability: authn.session.verify/v1
`)
	return root
}

func writeGenerationCompileFailureProject(t *testing.T) string {
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
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [order.create/v1]\n")
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
	writeCommandFile(t, filepath.Join(root, "authn", "generation", "extension.go"), invalidGenerationSignatureSource)
	writeCommandFile(t, filepath.Join(root, "authn", "capabilities", "authn.session.verify", "v1", "capability.yaml"), `id: authn.session.verify/v1
request: {}
response: {}
errors: []
`)
	return root
}

func writeGenerationInvocationFailureProject(t *testing.T) string {
	t.Helper()

	root := writeGenerationCompileFailureProject(t)
	writeCommandFile(t, filepath.Join(root, "authn", "generation", "extension.go"), generationInvocationFailureSource)
	return root
}

func writeGenerationErrorDiagnosticProject(t *testing.T, dependency bool) string {
	t.Helper()

	root := writeGenerationContributionCycleProject(t)
	writeCommandFile(t, filepath.Join(root, "authn", "generation", "extension.go"), generationErrorDiagnosticSource)
	writeCommandFile(t, filepath.Join(root, "audit", "generation", "extension.go"), auditErrorDiagnosticSource)
	if dependency {
		dependencyRoot := filepath.Join(root, "security-dependency")
		goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
		if err != nil {
			t.Fatalf("read application go.mod: %v", err)
		}
		writeCommandFile(t, filepath.Join(dependencyRoot, "go.mod"), strings.Replace(string(goMod), "module example.com/acme/library", "module example.com/acme/security", 1))
		goSum, err := os.ReadFile(filepath.Join(root, "go.sum"))
		if err != nil {
			t.Fatalf("read fixture go.sum: %v", err)
		}
		writeCommandFile(t, filepath.Join(dependencyRoot, "go.sum"), string(goSum))
		writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
		if err := os.Rename(filepath.Join(root, "authn"), filepath.Join(dependencyRoot, "authn")); err != nil {
			t.Fatalf("move fixture extension into dependency Project: %v", err)
		}
		writeCommandFile(t, filepath.Join(root, "go.mod"), string(goMod)+"\nrequire example.com/acme/security v1.2.3\n\nreplace example.com/acme/security => ./security-dependency\n")
	}
	return root
}

const emptyGenerationExtensionSource = `package generation

import generation "github.com/plystra/cli/generation/v1"

func Generate(generation.GenerationContext) (generation.Output, error) {
	return generation.Output{}, nil
}
`

const invalidGenerationSignatureSource = `package generation

func Generate() {}
`

const generationInvocationFailureSource = `package generation

import (
	"fmt"
	"os"

	generation "github.com/plystra/cli/generation/v1"
)

func Generate(generation.GenerationContext) (generation.Output, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return generation.Output{}, fmt.Errorf("inspect helper working directory: %w", err)
	}
	return generation.Output{}, fmt.Errorf("extension failed in %s", workingDirectory)
}
`

const generationErrorDiagnosticSource = `package generation

import generation "github.com/plystra/cli/generation/v1"

func Generate(generation.GenerationContext) (generation.Output, error) {
	source, _ := generation.ParseCapabilityID("order.create/v1")
	return generation.Output{Diagnostics: []generation.Diagnostic{
		{
			Code:      "authn.unsupported",
			Severity:  generation.DiagnosticError,
			Message:   "authentication metadata is unsupported",
			Namespace: "authn",
			Source:    source,
			RuleID:    "authn.validate",
		},
		{
			Code:      "authn.denied",
			Severity:  generation.DiagnosticError,
			Message:   "authentication metadata is denied",
			Namespace: "authn",
			Source:    source,
			RuleID:    "authn.validate",
		},
		{
			Code:      "authn.required",
			Severity:  generation.DiagnosticError,
			Message:   "authentication metadata is required",
			Namespace: "authn",
			Source:    source,
			RuleID:    "authn.require-session",
		},
		{
			Code:      "authn.advisory",
			Severity:  generation.DiagnosticWarning,
			Message:   "authentication metadata is discouraged",
			Namespace: "authn",
			Source:    source,
			RuleID:    "authn.observe",
		},
	}}, nil
}
`

const auditErrorDiagnosticSource = `package generation

import generation "github.com/plystra/cli/generation/v1"

func Generate(generation.GenerationContext) (generation.Output, error) {
	source, _ := generation.ParseCapabilityID("order.create/v1")
	return generation.Output{Diagnostics: []generation.Diagnostic{{
		Code:      "audit.required",
		Severity:  generation.DiagnosticError,
		Message:   "audit metadata is required",
		Namespace: "audit",
		Source:    source,
		RuleID:    "audit.validate",
	}}}, nil
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

const authnContributionCycleGenerationExtensionSource = `package generation

import generation "github.com/plystra/cli/generation/v1"

func Generate(generation.GenerationContext) (generation.Output, error) {
	source, _ := generation.ParseCapabilityID("order.create/v1")
	return generation.Output{Contributions: []generation.Contribution{{
		ID:        "authn.verify",
		Namespace: "authn",
		Source:    source,
		Point:     generation.GenerationPointInvocationPrepare,
		Requires:  []generation.ContributionToken{"audit-recorded"},
		Provides:  []generation.ContributionToken{"authn-verified"},
	}}}, nil
}
`

const auditContributionCycleGenerationExtensionSource = `package generation

import generation "github.com/plystra/cli/generation/v1"

func Generate(generation.GenerationContext) (generation.Output, error) {
	source, _ := generation.ParseCapabilityID("order.create/v1")
	return generation.Output{Contributions: []generation.Contribution{{
		ID:        "audit.record",
		Namespace: "audit",
		Source:    source,
		Point:     generation.GenerationPointInvocationPrepare,
		Requires:  []generation.ContributionToken{"authn-verified"},
		Provides:  []generation.ContributionToken{"audit-recorded"},
	}}}, nil
}
`

const authnUnorderedContributionsGenerationExtensionSource = `package generation

import generation "github.com/plystra/cli/generation/v1"

func Generate(generation.GenerationContext) (generation.Output, error) {
	source, _ := generation.ParseCapabilityID("order.create/v1")
	return generation.Output{Contributions: []generation.Contribution{
		{
			ID:        "authn.verify",
			Namespace: "authn",
			Source:    source,
			Point:     generation.GenerationPointInvocationPrepare,
		},
		{
			ID:        "authn.attach",
			Namespace: "authn",
			Source:    source,
			Point:     generation.GenerationPointInvocationPrepare,
		},
	}}, nil
}
`

const auditUnorderedContributionsGenerationExtensionSource = `package generation

import generation "github.com/plystra/cli/generation/v1"

func Generate(generation.GenerationContext) (generation.Output, error) {
	source, _ := generation.ParseCapabilityID("order.create/v1")
	return generation.Output{Contributions: []generation.Contribution{{
		ID:        "audit.record",
		Namespace: "audit",
		Source:    source,
		Point:     generation.GenerationPointInvocationPrepare,
	}}}, nil
}
`

const auditRepeatedStateGenerationExtensionSource = `package generation

import (
	"fmt"
	"time"

	generation "github.com/plystra/cli/generation/v1"
)

func Generate(generation.GenerationContext) (generation.Output, error) {
	source, _ := generation.ParseCapabilityID("order.create/v1")
	return generation.Output{Diagnostics: []generation.Diagnostic{{
		Code:      "audit.state",
		Severity:  generation.DiagnosticInfo,
		Message:   fmt.Sprintf("time %d", time.Now().UnixNano()),
		Namespace: "audit",
		Source:    source,
		RuleID:    "audit.observe",
	}}}, nil
}
`

const authzRepeatedStateGenerationExtensionSource = `package generation

import (
	"fmt"
	"time"

	generation "github.com/plystra/cli/generation/v1"
)

func Generate(generation.GenerationContext) (generation.Output, error) {
	source, _ := generation.ParseCapabilityID("order.create/v1")
	return generation.Output{Diagnostics: []generation.Diagnostic{{
		Code:      "authz.state",
		Severity:  generation.DiagnosticInfo,
		Message:   fmt.Sprintf("time %d", time.Now().UnixNano()),
		Namespace: "authz",
		Source:    source,
		RuleID:    "authz.observe",
	}}}, nil
}
`
