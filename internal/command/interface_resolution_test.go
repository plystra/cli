package command_test

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestPublicResolvingCommandsRejectInvalidRequiredConstructorGraphWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	rootKinds := []struct {
		name          string
		configuration string
		sourceKind    string
	}{
		{name: "requirement", configuration: "interfaces: {require: [app.run/v1]}\n", sourceKind: "declaration"},
		{name: "exposure", configuration: "http: {expose: [app.run/v1]}\n", sourceKind: "exposure"},
	}
	for _, rootKind := range rootKinds {
		rootKind := rootKind
		t.Run(rootKind.name, func(t *testing.T) {
			t.Parallel()
			for _, arguments := range commands {
				arguments := arguments
				t.Run(strings.Join(arguments, " "), func(t *testing.T) {
					root := writeCommandGraphFailureProject(t, rootKind.configuration)
					before := commandTree(t, root)
					exitCode, stdout, stderr := runCommand(t, arguments, filepath.Join(root, "app"), commandGoEnvironment())
					wantSuffix := strings.Join([]string{
						"",
						"Source: example.com/command-graph:app/service.go:13:6 (implementation-constructor)",
						"Source: example.com/command-graph:audit/service.go:13:6 (implementation-constructor)",
						"Source: example.com/command-graph:plystra.yaml:1:1 (" + rootKind.sourceKind + ")",
						"",
						"Recovery:",
						"Create one compatible local Implementation by running `plystra implement storage.read/v1 --package <project-relative-package>`.",
						"",
						"Diagnostic: " + diagnosticcode.ResolveMissingImplementation,
						"",
					}, "\n")
					if exitCode != 1 || stdout != "" || !commandContainsAll(
						stderr,
						"app.run/v1",
						"audit.write/v1",
						"storage.read/v1",
						"example.com/command-graph/app.New",
						"example.com/command-graph/audit.New",
					) || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 3 || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, root) {
						t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
					}
					if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
						t.Fatalf("%v mutated Project before rejecting graph:\nbefore: %#v\nafter:  %#v", arguments, before, after)
					}
					assertNoCommandTransactions(t, root)
				})
			}
		})
	}
}

func TestPublicResolvingCommandsReportDependencyMissingImplementationPathSourcesWithoutMutation(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	applicationRoot := filepath.Join(parent, "application")
	contractsRoot := filepath.Join(parent, "contracts")
	appConstructorRoot := filepath.Join(parent, "app-constructor")
	auditConstructorRoot := filepath.Join(parent, "audit-constructor")
	alphaRoot := filepath.Join(parent, "alpha-root")
	zetaRoot := filepath.Join(parent, "zeta-root")

	writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), `module example.com/missing-path-consumer

go 1.26

require (
	example.com/roots/zeta v1.4.0
	example.com/constructors/audit v1.2.0
	example.com/contracts v1.0.0
	example.com/roots/alpha v1.3.0
	example.com/constructors/app v1.1.0
)

replace example.com/contracts => ../contracts
replace example.com/constructors/app => ../app-constructor
replace example.com/constructors/audit => ../audit-constructor
replace example.com/roots/alpha => ../alpha-root
replace example.com/roots/zeta => ../zeta-root
`)
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(applicationRoot, "generated", "sentinel.txt"), "must remain unchanged\n")

	writeCommandFile(t, filepath.Join(contractsRoot, "go.mod"), "module example.com/contracts\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(contractsRoot, "plystra.yaml"), "{}\n")
	writeCommandGraphInterface(t, contractsRoot, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeCommandGraphInterface(t, contractsRoot, "audit/write/v1", "writev1", "audit.write/v1", "Write")
	writeCommandGraphInterface(t, contractsRoot, "storage/read/v1", "readv1", "storage.read/v1", "Read")

	writeCommandGraphImplementationModule(t, appConstructorRoot, "example.com/constructors/app", `package service

import (
	"context"
	runv1 "example.com/contracts/interfaces/app/run/v1"
	writev1 "example.com/contracts/interfaces/audit/write/v1"
)

type Service struct{}

//plystra:implements app.run/v1
func New(audit writev1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) Run(context.Context, runv1.Request) (runv1.Response, error) {
	return runv1.Response{}, nil
}
`)
	writeCommandGraphImplementationModule(t, auditConstructorRoot, "example.com/constructors/audit", `package service

import (
	"context"
	writev1 "example.com/contracts/interfaces/audit/write/v1"
	readv1 "example.com/contracts/interfaces/storage/read/v1"
)

type Service struct{}

//plystra:implements audit.write/v1
func New(storage readv1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) Write(context.Context, writev1.Request) (writev1.Response, error) {
	return writev1.Response{}, nil
}
`)
	for _, root := range []struct {
		path       string
		modulePath string
	}{
		{path: alphaRoot, modulePath: "example.com/roots/alpha"},
		{path: zetaRoot, modulePath: "example.com/roots/zeta"},
	} {
		writeCommandFile(t, filepath.Join(root.path, "go.mod"), "module "+root.modulePath+"\n\ngo 1.26\n")
		writeCommandFile(t, filepath.Join(root.path, "plystra.yaml"), "interfaces: {require: [app.run/v1]}\n")
	}

	roots := []string{applicationRoot, contractsRoot, appConstructorRoot, auditConstructorRoot, alphaRoot, zetaRoot}
	trees := make(map[string]map[string][]byte, len(roots))
	for _, root := range roots {
		trees[root] = commandTree(t, root)
	}
	for _, arguments := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
		exitCode, stdout, stderr := runCommand(t, arguments, applicationRoot, commandGoEnvironment())
		wantSuffix := strings.Join([]string{
			"",
			"Source: example.com/constructors/app:service/implementation.go:12:6 (implementation-constructor)",
			"Source: example.com/constructors/audit:service/implementation.go:12:6 (implementation-constructor)",
			"Source: example.com/roots/alpha:plystra.yaml:1:1 (declaration)",
			"Source: example.com/roots/zeta:plystra.yaml:1:1 (declaration)",
			"",
			"Recovery:",
			"Create one compatible local Implementation by running `plystra implement storage.read/v1 --package <project-relative-package>`.",
			"",
			"Diagnostic: " + diagnosticcode.ResolveMissingImplementation,
			"",
		}, "\n")
		if exitCode != 1 || stdout != "" || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 4 || !commandContainsAll(stderr, "app.run/v1", "audit.write/v1", "storage.read/v1", "example.com/constructors/app/service.New", "example.com/constructors/audit/service.New") {
			t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
		}
		for _, privatePath := range append([]string{parent, filepath.ToSlash(parent)}, roots...) {
			if strings.Contains(stderr, privatePath) || strings.Contains(stderr, filepath.ToSlash(privatePath)) {
				t.Fatalf("%v exposed private path %q: %q", arguments, privatePath, stderr)
			}
		}
		for _, root := range roots {
			if after := commandTree(t, root); !reflect.DeepEqual(after, trees[root]) {
				t.Fatalf("%v mutated %s:\nbefore: %#v\nafter:  %#v", arguments, root, trees[root], after)
			}
			assertNoCommandTransactions(t, root)
		}
	}
}

func TestPublicResolvingCommandsReportMissingProviderRequirementSourceWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, arguments := range commands {
		arguments := arguments
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/command-missing-provider\n\ngo 1.26\n")
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "capabilities: {require: [audit.write/v1]}\n")
			writeCommandFile(t, filepath.Join(root, "generated", "sentinel.txt"), "must remain unchanged\n")
			before := commandTree(t, root)

			exitCode, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
			wantSuffix := "\n\nSource: example.com/command-missing-provider:plystra.yaml:1:1 (declaration)\n\nRecovery:\nAdd an intended dependency with `plystra add <go-module-query>` whose Plugin provides audit.write/v1.\n\nDiagnostic: " + diagnosticcode.ProviderMissing + "\n"
			if exitCode != 1 || stdout != "" || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 1 || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, root) {
				t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%v mutated missing-Provider Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicResolvingCommandsReportAmbiguousProviderSourcesWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, arguments := range commands {
		arguments := arguments
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			t.Parallel()
			root := writeProviderCommandProject(t)
			before := commandTree(t, root)

			exitCode, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
			wantSuffix := strings.Join([]string{
				"",
				"Source: example.com/acme/provider-use:local/capabilities/email.send/v1/capability.yaml:1:1 (provider-declaration)",
				"Source: example.com/acme/provider-use:plystra.yaml:1:1 (declaration)",
				"Source: example.com/acme/provider-use:smtp/capabilities/email.send/v1/capability.yaml:1:1 (provider-declaration)",
				"",
				"Recovery:",
				"Select one compatible Provider explicitly by running `plystra use email.send/v1 <plugin-id>`.",
				"",
				"Diagnostic: " + diagnosticcode.ProviderAmbiguous,
				"",
			}, "\n")
			if exitCode != 1 || stdout != "" || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 3 || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, root) {
				t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%v mutated ambiguous-Provider Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicResolvingCommandsReportVisibleCapabilityContractConflictSourcesWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, arguments := range commands {
		arguments := arguments
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			t.Parallel()
			root := writeProviderCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "smtp", "capabilities", "email.send", "v1", "capability.yaml"), "id: email.send/v1\nrequest: {to: {type: string}}\nresponse: {}\nerrors: []\n")
			before := commandTree(t, root)

			exitCode, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
			wantSuffix := strings.Join([]string{
				"",
				"Source: example.com/acme/provider-use:local/capabilities/email.send/v1/capability.yaml:1:1 (provider-declaration)",
				"Source: example.com/acme/provider-use:smtp/capabilities/email.send/v1/capability.yaml:1:1 (provider-declaration)",
				"",
				"Recovery:",
				"Make every Provider of email.send/v1 carry one identical provider-independent capability.yaml.",
				"",
				"Diagnostic: " + diagnosticcode.CapabilityContractConflict,
				"",
			}, "\n")
			if exitCode != 1 || stdout != "" || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 2 || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, root) {
				t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%v mutated conflicting-contract Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicResolvingCommandsReportInvalidProviderSelectionSourceWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, arguments := range commands {
		arguments := arguments
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			t.Parallel()
			root := writeProviderCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "capabilities: {require: [email.send/v1], use: {email.send/v1: missing.email}}\n")
			before := commandTree(t, root)

			exitCode, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
			wantSuffix := "\n\nSource: example.com/acme/provider-use:plystra.yaml:1:1 (provider-selection)\n\nRecovery:\nReplace the invalid Provider choice with one visible compatible Plugin by running `plystra use email.send/v1 <plugin-id>`.\n\nDiagnostic: " + diagnosticcode.ProviderSelectionInvalid + "\n"
			if exitCode != 1 || stdout != "" || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 1 || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, root) {
				t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%v mutated invalid Provider-selection Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicResolvingCommandsEmitStableImplementationAmbiguityWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, arguments := range commands {
		arguments := arguments
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			t.Parallel()
			root := writeCommandAmbiguousImplementationProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
			wantSuffix := strings.Join([]string{
				"",
				"Source: example.com/acme/implementation-use:local/implementation.go:12:6 (implementation-constructor)",
				"Source: example.com/acme/implementation-use:smtp/implementation.go:12:6 (implementation-constructor)",
				"",
				"Recovery:",
				"Select one compatible Implementation by running `plystra use email.send/v1 <constructor-symbol>`.",
				"",
				"Diagnostic: " + diagnosticcode.ResolveMultipleImplementations,
				"",
			}, "\n")
			if exitCode != 1 || stdout != "" ||
				!commandContainsAll(stderr, "email.send/v1", "example.com/acme/implementation-use/local.New", "example.com/acme/implementation-use/smtp.New") ||
				!strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 2 ||
				strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, root) {
				t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%v mutated ambiguous Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicResolvingCommandsReportDependencyImplementationAmbiguitySourcesWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, arguments := range commands {
		arguments := arguments
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			parent := t.TempDir()
			applicationRoot := filepath.Join(parent, "application")
			contractRoot := filepath.Join(parent, "contracts")
			alphaRoot := filepath.Join(parent, "alpha")
			zetaRoot := filepath.Join(parent, "zeta")

			writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), `module example.com/implementation-source-consumer

go 1.26

require (
	example.com/providers/zeta v1.3.0
	example.com/contracts v1.0.0
	example.com/providers/alpha v1.2.0
)

replace example.com/contracts => ../contracts
replace example.com/providers/alpha => ../alpha
replace example.com/providers/zeta => ../zeta
`)
			writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "interfaces: {require: [email.send/v1]}\n")
			writeCommandFile(t, filepath.Join(applicationRoot, "generated", "sentinel.txt"), "must remain unchanged\n")

			writeCommandFile(t, filepath.Join(contractRoot, "go.mod"), "module example.com/contracts\n\ngo 1.26\n")
			writeCommandFile(t, filepath.Join(contractRoot, "plystra.yaml"), "{}\n")
			writeCommandGraphInterface(t, contractRoot, "email/send/v1", "sendv1", "email.send/v1", "Send")

			writeCommandDependencyImplementation(t, alphaRoot, "example.com/providers/alpha")
			writeCommandDependencyImplementation(t, zetaRoot, "example.com/providers/zeta")

			applicationBefore := commandTree(t, applicationRoot)
			contractBefore := commandTree(t, contractRoot)
			alphaBefore := commandTree(t, alphaRoot)
			zetaBefore := commandTree(t, zetaRoot)

			exitCode, stdout, stderr := runCommand(t, arguments, applicationRoot, commandGoEnvironment())
			wantSuffix := strings.Join([]string{
				"",
				"Source: example.com/providers/alpha:service/implementation.go:12:6 (implementation-constructor)",
				"Source: example.com/providers/zeta:service/implementation.go:12:6 (implementation-constructor)",
				"",
				"Recovery:",
				"Select one compatible Implementation by running `plystra use email.send/v1 <constructor-symbol>`.",
				"",
				"Diagnostic: " + diagnosticcode.ResolveMultipleImplementations,
				"",
			}, "\n")
			if exitCode != 1 || stdout != "" || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 2 {
				t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
			}
			for _, privatePath := range []string{parent, filepath.ToSlash(parent), applicationRoot, filepath.ToSlash(applicationRoot), contractRoot, filepath.ToSlash(contractRoot), alphaRoot, filepath.ToSlash(alphaRoot), zetaRoot, filepath.ToSlash(zetaRoot)} {
				if strings.Contains(stderr, privatePath) {
					t.Fatalf("%v exposed private path %q: %q", arguments, privatePath, stderr)
				}
			}
			if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, applicationBefore) {
				t.Fatalf("%v mutated consumer Project:\nbefore: %#v\nafter:  %#v", arguments, applicationBefore, after)
			}
			if after := commandTree(t, contractRoot); !reflect.DeepEqual(after, contractBefore) {
				t.Fatalf("%v mutated contract Project:\nbefore: %#v\nafter:  %#v", arguments, contractBefore, after)
			}
			if after := commandTree(t, alphaRoot); !reflect.DeepEqual(after, alphaBefore) {
				t.Fatalf("%v mutated alpha Project:\nbefore: %#v\nafter:  %#v", arguments, alphaBefore, after)
			}
			if after := commandTree(t, zetaRoot); !reflect.DeepEqual(after, zetaBefore) {
				t.Fatalf("%v mutated zeta Project:\nbefore: %#v\nafter:  %#v", arguments, zetaBefore, after)
			}
			for _, root := range []string{applicationRoot, contractRoot, alphaRoot, zetaRoot} {
				assertNoCommandTransactions(t, root)
			}
		})
	}
}

func TestPublicResolvingCommandsRejectInvalidDormantChoiceWithoutMutation(t *testing.T) {
	tests := []struct {
		name           string
		constructor    string
		configuration  string
		selectedPath   string
		selectors      []string
		problem        string
		recoverySuffix string
		diagnostic     string
	}{
		{
			name:           "default incompatible",
			constructor:    "example.com/acme/implementation-use/reports.New",
			configuration:  "interfaces: {use: {email.send/v1: example.com/acme/implementation-use/reports.New}}\n",
			selectedPath:   "plystra.yaml",
			problem:        "does not implement Interface",
			recoverySuffix: "",
			diagnostic:     diagnosticcode.ResolveIncompatibleImplementation,
		},
		{
			name:           "environment unknown",
			constructor:    "example.com/acme/implementation-use/missing.New",
			configuration:  "interfaces: {use: {email.send/v1: example.com/acme/implementation-use/missing.New}}\n",
			selectedPath:   "plystra.production.yaml",
			selectors:      []string{"--env", "production"},
			problem:        "names invisible constructor",
			recoverySuffix: " --env \"production\"",
			diagnostic:     diagnosticcode.ResolveUnknownImplementation,
		},
		{
			name:           "explicit incompatible",
			constructor:    "example.com/acme/implementation-use/reports.New",
			configuration:  "interfaces: {use: {email.send/v1: example.com/acme/implementation-use/reports.New}}\n",
			selectedPath:   "deploy/customer.yaml",
			selectors:      []string{"--config", "deploy/customer.yaml"},
			problem:        "does not implement Interface",
			recoverySuffix: " --config \"deploy/customer.yaml\"",
			diagnostic:     diagnosticcode.ResolveIncompatibleImplementation,
		},
	}
	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			if test.selectedPath != "plystra.yaml" {
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			}
			writeCommandFile(t, filepath.Join(root, filepath.FromSlash(test.selectedPath)), test.configuration)
			before := commandTree(t, root)
			for _, command := range commands {
				arguments := append(append([]string(nil), command...), test.selectors...)
				exitCode, stdout, stderr := runCommand(t, arguments, filepath.Join(root, "reports"), commandGoEnvironment())
				wantSource := "Source: example.com/acme/implementation-use:" + test.selectedPath + ":1:1 (implementation-selection)"
				wantRecovery := "Recovery:\nReplace the reported choice with one visible compatible constructor by running `plystra use <interface-id> <constructor-symbol>" + test.recoverySuffix + "`.\n"
				if exitCode != 1 || stdout != "" || !commandContainsAll(
					stderr,
					"email.send/v1",
					test.constructor,
					test.problem,
					wantSource,
					wantRecovery,
					"Diagnostic: "+test.diagnostic,
				) || strings.Count(stderr, "Source: ") != 1 {
					t.Fatalf("%v invalid dormant choice = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
				}
				for _, privatePath := range []string{root, filepath.ToSlash(root)} {
					if strings.Contains(stderr, privatePath) {
						t.Fatalf("%v exposed private path %q: %q", arguments, privatePath, stderr)
					}
				}
				if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
					t.Fatalf("%v mutated invalid dormant Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
				}
				assertNoCommandTransactions(t, root)
			}
		})
	}
}

func TestPublicResolvingCommandsReportEveryInheritedInvalidImplementationChoiceSource(t *testing.T) {
	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, command := range commands {
		command := command
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			t.Parallel()

			parent := t.TempDir()
			contractsRoot := filepath.Join(parent, "contracts")
			alphaRoot := filepath.Join(parent, "alpha")
			zetaRoot := filepath.Join(parent, "zeta")
			applicationRoot := filepath.Join(parent, "application")
			const constructor = "example.com/missing/private.New"

			writeCommandFile(t, filepath.Join(contractsRoot, "go.mod"), "module example.com/contracts\n\ngo 1.26\n")
			writeCommandFile(t, filepath.Join(contractsRoot, "plystra.yaml"), "{}\n")
			writeCommandGraphInterface(t, contractsRoot, "email/send/v1", "sendv1", "email.send/v1", "Send")
			for _, dependency := range []struct {
				root       string
				modulePath string
			}{
				{root: zetaRoot, modulePath: "example.com/zeta"},
				{root: alphaRoot, modulePath: "example.com/alpha"},
			} {
				writeCommandFile(t, filepath.Join(dependency.root, "go.mod"), "module "+dependency.modulePath+"\n\ngo 1.26\n")
				writeCommandFile(t, filepath.Join(dependency.root, "plystra.yaml"), "interfaces: {use: {email.send/v1: "+constructor+"}}\n")
			}
			writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), `module example.com/consumer

go 1.26

require (
	example.com/alpha v1.0.0
	example.com/contracts v1.0.0
	example.com/zeta v1.0.0
)

replace example.com/alpha => ../alpha
replace example.com/contracts => ../contracts
replace example.com/zeta => ../zeta
`)
			writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "{}\n")

			before := map[string]map[string][]byte{
				"application": commandTree(t, applicationRoot),
				"contracts":   commandTree(t, contractsRoot),
				"alpha":       commandTree(t, alphaRoot),
				"zeta":        commandTree(t, zetaRoot),
			}
			exitCode, stdout, stderr := runCommand(t, command, applicationRoot, commandGoEnvironment())
			wantSuffix := strings.Join([]string{
				"",
				"Source: example.com/alpha:plystra.yaml:1:1 (implementation-selection)",
				"Source: example.com/zeta:plystra.yaml:1:1 (implementation-selection)",
				"",
				"Recovery:",
				"Replace the reported choice with one visible compatible constructor by running `plystra use <interface-id> <constructor-symbol>`.",
				"",
				"Diagnostic: " + diagnosticcode.ResolveUnknownImplementation,
				"",
			}, "\n")
			if exitCode != 1 || stdout != "" || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 2 {
				t.Fatalf("%v inherited invalid choice = exit %d stdout %q stderr %q", command, exitCode, stdout, stderr)
			}
			for _, privatePath := range []string{parent, filepath.ToSlash(parent), applicationRoot, filepath.ToSlash(applicationRoot), alphaRoot, filepath.ToSlash(alphaRoot), zetaRoot, filepath.ToSlash(zetaRoot)} {
				if strings.Contains(stderr, privatePath) {
					t.Fatalf("%v exposed private path %q: %q", command, privatePath, stderr)
				}
			}
			for name, root := range map[string]string{
				"application": applicationRoot,
				"contracts":   contractsRoot,
				"alpha":       alphaRoot,
				"zeta":        zetaRoot,
			} {
				if after := commandTree(t, root); !reflect.DeepEqual(after, before[name]) {
					t.Fatalf("%v mutated %s Project:\nbefore: %#v\nafter:  %#v", command, name, before[name], after)
				}
				assertNoCommandTransactions(t, root)
			}
		})
	}
}

func TestPublicResolvingCommandsRejectUnownedConstructorConfigurationWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, arguments := range commands {
		name := strings.Join(arguments, " ")
		t.Run(name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			constructor := "example.com/acme/implementation-use/reports.New"
			writeCommandConfigurableImplementation(t, root, "reports", "reports.read/v1", "reports/read/v1", "Read")
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "config: {"+constructor+": {endpoint: private.internal}}\n")
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, arguments, filepath.Join(root, "reports"), implementationSelectionCommandEnvironment(nil))
			if exitCode != 1 || stdout != "" || !commandContainsAll(
				stderr,
				constructor,
				"Recovery:\nName the reported constructor in an effective interfaces.use entry, make it reachable through an Interface requirement, or remove its configuration from plystra.yaml, then rerun the command.\n",
				"Diagnostic: "+diagnosticcode.ConstructorConfigurationUnselected,
			) || strings.Contains(stderr, "private.internal") {
				t.Fatalf("%v unowned constructor configuration = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%v mutated unowned constructor configuration Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicResolvingCommandsEmitStableConstructorCycleWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, arguments := range commands {
		arguments := arguments
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			t.Parallel()
			root := writeCommandCycleFailureProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
			wantSuffix := strings.Join([]string{
				"",
				"Source: example.com/command-cycle:cyclea/service.go:13:6 (implementation-constructor)",
				"Source: example.com/command-cycle:cycleb/service.go:13:6 (implementation-constructor)",
				"",
				"Recovery:",
				"Remove one required Interface parameter from the reported constructor cycle, then rerun the command.",
				"",
				"Diagnostic: " + diagnosticcode.ResolveConstructorCycle,
				"",
			}, "\n")
			if exitCode != 1 || stdout != "" || !commandContainsAll(
				stderr,
				"cycle.a/v1",
				"cycle.b/v1",
				"example.com/command-cycle/cyclea.New",
				"example.com/command-cycle/cycleb.New",
			) || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 2 || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, root) {
				t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%v mutated cyclic Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicResolvingCommandsReportDependencyConstructorCycleSourcesWithoutMutation(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	applicationRoot := filepath.Join(parent, "application")
	contractsRoot := filepath.Join(parent, "contracts")
	alphaRoot := filepath.Join(parent, "alpha")
	zetaRoot := filepath.Join(parent, "zeta")

	writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), `module example.com/cycle-consumer

go 1.26

require (
	example.com/cycles/zeta v1.2.0
	example.com/contracts v1.0.0
	example.com/cycles/alpha v1.1.0
)

replace example.com/contracts => ../contracts
replace example.com/cycles/alpha => ../alpha
replace example.com/cycles/zeta => ../zeta
`)
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "interfaces: {require: [cycle.a/v1]}\n")
	writeCommandFile(t, filepath.Join(applicationRoot, "generated", "sentinel.txt"), "must remain unchanged\n")
	writeCommandFile(t, filepath.Join(contractsRoot, "go.mod"), "module example.com/contracts\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(contractsRoot, "plystra.yaml"), "{}\n")
	writeCommandGraphInterface(t, contractsRoot, "cycle/a/v1", "av1", "cycle.a/v1", "A")
	writeCommandGraphInterface(t, contractsRoot, "cycle/b/v1", "bv1", "cycle.b/v1", "B")
	writeCommandGraphImplementationModule(t, alphaRoot, "example.com/cycles/alpha", `package service

import (
	"context"

	av1 "example.com/contracts/interfaces/cycle/a/v1"
	bv1 "example.com/contracts/interfaces/cycle/b/v1"
)

type Service struct{}

//plystra:implements cycle.a/v1
func New(next bv1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) A(context.Context, av1.Request) (av1.Response, error) {
	return av1.Response{}, nil
}
`)
	writeCommandGraphImplementationModule(t, zetaRoot, "example.com/cycles/zeta", `package service

import (
	"context"

	av1 "example.com/contracts/interfaces/cycle/a/v1"
	bv1 "example.com/contracts/interfaces/cycle/b/v1"
)

type Service struct{}

//plystra:implements cycle.b/v1
func New(previous av1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) B(context.Context, bv1.Request) (bv1.Response, error) {
	return bv1.Response{}, nil
}
`)

	roots := []string{applicationRoot, contractsRoot, alphaRoot, zetaRoot}
	trees := make(map[string]map[string][]byte, len(roots))
	for _, root := range roots {
		trees[root] = commandTree(t, root)
	}
	for _, arguments := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
		exitCode, stdout, stderr := runCommand(t, arguments, applicationRoot, commandGoEnvironment())
		wantSuffix := strings.Join([]string{
			"",
			"Source: example.com/cycles/alpha:service/implementation.go:13:6 (implementation-constructor)",
			"Source: example.com/cycles/zeta:service/implementation.go:13:6 (implementation-constructor)",
			"",
			"Recovery:",
			"Remove one required Interface parameter from the reported constructor cycle, then rerun the command.",
			"",
			"Diagnostic: " + diagnosticcode.ResolveConstructorCycle,
			"",
		}, "\n")
		if exitCode != 1 || stdout != "" || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 2 || !commandContainsAll(stderr, "cycle.a/v1", "cycle.b/v1", "example.com/cycles/alpha/service.New", "example.com/cycles/zeta/service.New", "unique-compatible") {
			t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
		}
		for _, privatePath := range append([]string{parent, filepath.ToSlash(parent)}, roots...) {
			if strings.Contains(stderr, privatePath) || strings.Contains(stderr, filepath.ToSlash(privatePath)) {
				t.Fatalf("%v exposed private path %q: %q", arguments, privatePath, stderr)
			}
		}
		for _, root := range roots {
			if after := commandTree(t, root); !reflect.DeepEqual(after, trees[root]) {
				t.Fatalf("%v mutated %s:\nbefore: %#v\nafter:  %#v", arguments, root, trees[root], after)
			}
			assertNoCommandTransactions(t, root)
		}
	}
}

func TestPublicResolvingCommandsRejectInvalidIntrinsicInterfaceWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, arguments := range commands {
		arguments := arguments
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			t.Parallel()
			for _, shadow := range []bool{false, true} {
				shadow := shadow
				name := "unknown"
				want := []string{"kernel.missing/v1", "selected Kernel API"}
				code := diagnosticcode.ResolveUnknownInterface
				if shadow {
					name = "shadow"
					want = []string{"kernel.health/v1", "reserved kernel.* namespace"}
					code = diagnosticcode.ResolveReservedInterface
				}
				t.Run(name, func(t *testing.T) {
					root := writeCommandIntrinsicFailureProject(t, shadow)
					before := commandTree(t, root)
					exitCode, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
					want = append(want, "Recovery:\n", "Diagnostic: "+code)
					if exitCode != 1 || stdout != "" || !commandContainsAll(stderr, want...) {
						t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
					}
					if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
						t.Fatalf("%v mutated Project before rejecting intrinsic Interface:\nbefore: %#v\nafter: %#v", arguments, before, after)
					}
					assertNoCommandTransactions(t, root)
				})
			}
		})
	}
}

func TestPublicResolvingCommandsClassifyInvalidImplementationAuthoringWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		identity string
		problem  string
		sources  []string
		recovery string
		code     string
	}{
		{
			name:     "declaration",
			source:   invalidImplementationDeclarationSource,
			identity: "service/service.go:5:1",
			problem:  "expected //plystra:implements <interface-id>",
			sources:  []string{"example.com/command-invalid-implementation:service/service.go:5:1 (implementation-declaration)"},
			recovery: "Correct the reported //plystra:implements directive so it immediately documents one exported package-level constructor and names canonical Interface IDs, then rerun the command.",
			code:     diagnosticcode.ImplementationDeclarationInvalid,
		},
		{
			name:     "Config",
			source:   invalidImplementationConfigSource,
			identity: "example.com/command-invalid-implementation/service.New",
			problem:  "Config field Unsupported",
			sources:  []string{"example.com/command-invalid-implementation:service/service.go:16:6 (implementation-constructor)"},
			recovery: "Correct the reported constructor's first Config parameter and exported Config fields to use the supported typed configuration schema, then rerun the command.",
			code:     diagnosticcode.ImplementationConfigInvalid,
		},
		{
			name:     "required Interface",
			source:   invalidImplementationRequiredSource,
			identity: "example.com/command-invalid-implementation/service.New",
			problem:  "parameter 1 must be a canonical Interface type",
			sources:  []string{"example.com/command-invalid-implementation:service/service.go:12:6 (implementation-constructor)"},
			recovery: "Replace the reported required constructor parameter with one visible canonical Interface type, then rerun the command.",
			code:     diagnosticcode.ImplementationRequiredInvalid,
		},
		{
			name:     "optional Interface",
			source:   invalidImplementationOptionalSource,
			identity: "example.com/command-invalid-implementation/service.New",
			problem:  "Optional must be github.com/plystra/kernel.Optional[T]",
			sources:  []string{"example.com/command-invalid-implementation:service/service.go:13:6 (implementation-constructor)"},
			recovery: "Replace the reported optional constructor parameter with the exact plystra.Optional[T] value type around one visible canonical Interface, then rerun the command.",
			code:     diagnosticcode.ImplementationOptionalInvalid,
		},
		{
			name:     "result",
			source:   invalidImplementationResultSource,
			identity: "example.com/command-invalid-implementation/service.New",
			problem:  "constructor must return exactly one concrete pointer and error",
			sources:  []string{"example.com/command-invalid-implementation:service/service.go:12:6 (implementation-constructor)"},
			recovery: "Change the reported constructor to return exactly one concrete value plus error, then rerun the command.",
			code:     diagnosticcode.ImplementationResultInvalid,
		},
		{
			name:     "conformance",
			source:   invalidImplementationConformanceSource,
			identity: "example.com/command-invalid-implementation/service.New",
			problem:  "missing method Send",
			sources:  []string{"example.com/command-invalid-implementation:service/service.go:6:6 (implementation-constructor)"},
			recovery: "Implement every reported canonical Interface method on the constructor's concrete result type, then rerun the command.",
			code:     diagnosticcode.ImplementationConformanceInvalid,
		},
	}
	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, arguments := range commands {
				arguments := arguments
				t.Run(strings.Join(arguments, " "), func(t *testing.T) {
					root := writeCommandInvalidImplementationProject(t, test.source)
					before := commandTree(t, root)
					exitCode, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
					suffix := "\n\n"
					for _, source := range test.sources {
						suffix += "Source: " + source + "\n"
					}
					suffix += "\nRecovery:\n" + test.recovery + "\n\nDiagnostic: " + test.code + "\n"
					if exitCode != 1 || stdout != "" || !commandContainsAll(
						stderr,
						test.identity,
						test.problem,
						"Recovery:\n"+test.recovery+"\n",
						"Diagnostic: "+test.code,
					) || !strings.HasSuffix(stderr, suffix) || strings.Count(stderr, "Source: ") != len(test.sources) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, root) {
						t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
					}
					if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
						t.Fatalf("%v mutated invalid Implementation Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
					}
					assertNoCommandTransactions(t, root)
				})
			}
		})
	}
}

func TestPublicResolvingCommandsClassifyInvalidInterfaceAuthoringWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		source          string
		metadata        string
		duplicateSource string
		problems        []string
		sources         []string
		recovery        string
		code            string
	}{
		{
			name:     "declaration",
			source:   invalidInterfaceDeclarationSource,
			problems: []string{"interfaces/email/send/v1/interface.go:3:1", "expected //plystra:interface <interface-id>"},
			sources:  []string{"example.com/command-invalid-interface:interfaces/email/send/v1/interface.go:3:1 (interface-declaration)"},
			recovery: "Correct the reported //plystra:interface directive so it immediately documents the exported defined type Interface and names one canonical Interface ID, then rerun the command.",
			code:     diagnosticcode.InterfaceDeclarationInvalid,
		},
		{
			name:     "contract",
			source:   invalidInterfaceContractSource,
			problems: []string{"interfaces/email/send/v1/interface.go:3:1", "exactly one operation method"},
			sources:  []string{"example.com/command-invalid-interface:interfaces/email/send/v1/interface.go:3:1 (interface-contract)"},
			recovery: "Correct the reported Interface Go package to the canonical single-operation method, request, response, field, and error shape, then rerun the command.",
			code:     diagnosticcode.InterfaceContractInvalid,
		},
		{
			name:     "metadata",
			source:   validAuthoredInterfaceSource,
			metadata: "unknown: true\n",
			problems: []string{"interfaces/email/send/v1/interface.yaml:1:1", "unknown top-level field"},
			sources:  []string{"example.com/command-invalid-interface:interfaces/email/send/v1/interface.yaml:1:1 (interface-metadata)"},
			recovery: "Correct the reported module-relative interface.yaml field to match the closed Interface metadata schema, then rerun the command.",
			code:     diagnosticcode.InterfaceMetadataInvalid,
		},
		{
			name:            "duplicate ID",
			source:          validAuthoredInterfaceSource,
			duplicateSource: duplicateAuthoredInterfaceSource,
			problems: []string{
				`duplicate visible Interface ID "email.send/v1"`,
				"example.com/command-invalid-interface/interfaces/email/send/v1",
				"example.com/command-invalid-interface/interfaces/duplicate/email/v1",
			},
			sources: []string{
				"example.com/command-invalid-interface:interfaces/duplicate/email/v1/interface.go:5:1 (interface-declaration)",
				"example.com/command-invalid-interface:interfaces/email/send/v1/interface.go:5:1 (interface-declaration)",
			},
			recovery: "Make the reported visible Go packages declare distinct canonical Interface IDs, then rerun the command.",
			code:     diagnosticcode.InterfaceIDDuplicate,
		},
		{
			name:     "package",
			source:   invalidAuthoredPackageSource,
			problems: []string{"invalid Interface package", "missingSymbol"},
			sources:  []string{"example.com/command-invalid-interface:interfaces/email/send/v1/interface.go (authored-package)"},
			recovery: "Correct the reported authored Go package in its owning Project so ordinary Go tooling can load it, then rerun the command.",
			code:     diagnosticcode.AuthoredPackageInvalid,
		},
	}
	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, arguments := range commands {
				arguments := arguments
				t.Run(strings.Join(arguments, " "), func(t *testing.T) {
					root := writeCommandInvalidInterfaceProject(t, test.source, test.metadata, test.duplicateSource)
					before := commandTree(t, root)
					exitCode, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
					want := append([]string(nil), test.problems...)
					want = append(want, "Recovery:\n"+test.recovery+"\n", "Diagnostic: "+test.code)
					suffix := "\n\n"
					for _, source := range test.sources {
						suffix += "Source: " + source + "\n"
					}
					suffix += "\nRecovery:\n" + test.recovery + "\n\nDiagnostic: " + test.code + "\n"
					if exitCode != 1 || stdout != "" || !commandContainsAll(stderr, want...) || !strings.HasSuffix(stderr, suffix) || strings.Count(stderr, "Source: ") != len(test.sources) || strings.Contains(stderr, filepath.ToSlash(root)) || strings.Contains(stderr, root) {
						t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
					}
					if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
						t.Fatalf("%v mutated invalid Interface Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
					}
					assertNoCommandTransactions(t, root)
				})
			}
		})
	}
}

func TestPublicResolvingCommandsReportDependencyOwnedSourceWithoutPrivatePaths(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	applicationRoot := filepath.Join(parent, "application")
	dependencyRoot := filepath.Join(parent, "dependency")
	writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), `module example.com/command-invalid-dependency-consumer

go 1.26

require example.com/command-invalid-dependency v1.2.3

replace example.com/command-invalid-dependency => ../dependency
`)
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(applicationRoot, "generated", "sentinel.txt"), "must remain unchanged\n")
	writeCommandFile(t, filepath.Join(dependencyRoot, "go.mod"), "module example.com/command-invalid-dependency\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
	writeCommandGraphInterface(t, dependencyRoot, "email/send/v1", "sendv1", "email.send/v1", "Send")
	writeCommandFile(t, filepath.Join(dependencyRoot, "interfaces", "email", "send", "v1", "interface.yaml"), "unknown: true\n")
	applicationBefore := commandTree(t, applicationRoot)
	dependencyBefore := commandTree(t, dependencyRoot)

	for _, arguments := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
		arguments := arguments
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, arguments, applicationRoot, commandGoEnvironment())
			wantSuffix := "\n\nSource: example.com/command-invalid-dependency:interfaces/email/send/v1/interface.yaml:1:1 (interface-metadata)\n\nRecovery:\nCorrect the reported module-relative interface.yaml field to match the closed Interface metadata schema, then rerun the command.\n\nDiagnostic: " + diagnosticcode.InterfaceMetadataInvalid + "\n"
			if exitCode != 1 || stdout != "" || !strings.HasSuffix(stderr, wantSuffix) || strings.Count(stderr, "Source: ") != 1 {
				t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
			}
			for _, privatePath := range []string{parent, filepath.ToSlash(parent), applicationRoot, filepath.ToSlash(applicationRoot), dependencyRoot, filepath.ToSlash(dependencyRoot)} {
				if strings.Contains(stderr, privatePath) {
					t.Fatalf("%v exposed private path %q: %q", arguments, privatePath, stderr)
				}
			}
			if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, applicationBefore) {
				t.Fatalf("%v mutated consumer Project:\nbefore: %#v\nafter:  %#v", arguments, applicationBefore, after)
			}
			if after := commandTree(t, dependencyRoot); !reflect.DeepEqual(after, dependencyBefore) {
				t.Fatalf("%v mutated dependency Project:\nbefore: %#v\nafter:  %#v", arguments, dependencyBefore, after)
			}
			assertNoCommandTransactions(t, applicationRoot)
			assertNoCommandTransactions(t, dependencyRoot)
		})
	}
}

func writeCommandIntrinsicFailureProject(t testing.TB, shadow bool) string {
	t.Helper()
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/command-intrinsic\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "generated", "sentinel.txt"), "must remain unchanged\n")
	if !shadow {
		writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [kernel.missing/v1]}\n")
		return root
	}
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeCommandGraphInterface(t, root, "kernel/health/v1", "healthv1", "kernel.health/v1", "Health")
	return root
}

func writeCommandInvalidImplementationProject(t testing.TB, source string) string {
	t.Helper()
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/command-invalid-implementation\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "generated", "sentinel.txt"), "must remain unchanged\n")
	writeCommandGraphInterface(t, root, "email/send/v1", "sendv1", "email.send/v1", "Send")
	writeCommandFile(t, filepath.Join(root, "service", "service.go"), source)
	return root
}

func writeCommandInvalidInterfaceProject(t testing.TB, source, metadata, duplicateSource string) string {
	t.Helper()
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/command-invalid-interface\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "generated", "sentinel.txt"), "must remain unchanged\n")
	writeCommandFile(t, filepath.Join(root, "interfaces", "email", "send", "v1", "interface.go"), source)
	if metadata != "" {
		writeCommandFile(t, filepath.Join(root, "interfaces", "email", "send", "v1", "interface.yaml"), metadata)
	}
	if duplicateSource != "" {
		writeCommandFile(t, filepath.Join(root, "interfaces", "duplicate", "email", "v1", "interface.go"), duplicateSource)
	}
	return root
}

const invalidInterfaceDeclarationSource = `package sendv1

//plystra:interface
type Interface interface{}
`

const invalidInterfaceContractSource = `package sendv1

//plystra:interface email.send/v1
type Interface interface{}

type Request struct{}
type Response struct{}
`

const validAuthoredInterfaceSource = `package sendv1

import "context"

//plystra:interface email.send/v1
type Interface interface {
	Send(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`

const duplicateAuthoredInterfaceSource = `package duplicatev1

import "context"

//plystra:interface email.send/v1
type Interface interface {
	Send(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`

const invalidAuthoredPackageSource = `package sendv1

import "context"

//plystra:interface email.send/v1
type Interface interface {
	Send(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}

var _ = missingSymbol
`

const invalidImplementationDeclarationSource = `package service

type Service struct{}

//plystra:implements
func New() (*Service, error) { return &Service{}, nil }
`

const invalidImplementationConfigSource = `package service

import (
	"context"

	sendv1 "example.com/command-invalid-implementation/interfaces/email/send/v1"
)

type Config struct {
	Unsupported chan int
}

type Service struct{}

//plystra:implements email.send/v1
func New(config Config) (*Service, error) { return &Service{}, nil }

func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) {
	return sendv1.Response{}, nil
}
`

const invalidImplementationRequiredSource = `package service

import (
	"context"

	sendv1 "example.com/command-invalid-implementation/interfaces/email/send/v1"
)

type Service struct{}

//plystra:implements email.send/v1
func New(value string) (*Service, error) { return &Service{}, nil }

func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) {
	return sendv1.Response{}, nil
}
`

const invalidImplementationOptionalSource = `package service

import (
	"context"

	sendv1 "example.com/command-invalid-implementation/interfaces/email/send/v1"
)

type Optional[T any] struct{}
type Service struct{}

//plystra:implements email.send/v1
func New(value Optional[sendv1.Interface]) (*Service, error) { return &Service{}, nil }

func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) {
	return sendv1.Response{}, nil
}
`

const invalidImplementationResultSource = `package service

import (
	"context"

	sendv1 "example.com/command-invalid-implementation/interfaces/email/send/v1"
)

type Service struct{}

//plystra:implements email.send/v1
func New() *Service { return &Service{} }

func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) {
	return sendv1.Response{}, nil
}
`

const invalidImplementationConformanceSource = `package service

type Service struct{}

//plystra:implements email.send/v1
func New() (*Service, error) { return &Service{}, nil }
`

func writeCommandGraphFailureProject(t testing.TB, configuration string) string {
	t.Helper()
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/command-graph\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), configuration)
	writeCommandFile(t, filepath.Join(root, "generated", "sentinel.txt"), "must remain unchanged\n")
	writeCommandGraphInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeCommandGraphInterface(t, root, "audit/write/v1", "writev1", "audit.write/v1", "Write")
	writeCommandGraphInterface(t, root, "storage/read/v1", "readv1", "storage.read/v1", "Read")
	writeCommandFile(t, filepath.Join(root, "app", "service.go"), `package app

import (
	"context"

	runv1 "example.com/command-graph/interfaces/app/run/v1"
	writev1 "example.com/command-graph/interfaces/audit/write/v1"
)

type Service struct{}

//plystra:implements app.run/v1
func New(audit writev1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) Run(context.Context, runv1.Request) (runv1.Response, error) {
	return runv1.Response{}, nil
}
`)
	writeCommandFile(t, filepath.Join(root, "audit", "service.go"), `package audit

import (
	"context"

	writev1 "example.com/command-graph/interfaces/audit/write/v1"
	readv1 "example.com/command-graph/interfaces/storage/read/v1"
)

type Service struct{}

//plystra:implements audit.write/v1
func New(storage readv1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) Write(context.Context, writev1.Request) (writev1.Response, error) {
	return writev1.Response{}, nil
}
`)
	return root
}

func writeCommandAmbiguousImplementationProject(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/implementation-use\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [email.send/v1]}\n")
	writeCommandFile(t, filepath.Join(root, "generated", "sentinel.txt"), "must remain unchanged\n")
	writeCommandInterface(t, root, "email/send/v1", "sendv1", "email.send/v1", "Send")
	writeCommandImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
	writeCommandImplementation(t, root, "local", "email.send/v1", "email/send/v1", "Send")
	return root
}

func writeCommandCycleFailureProject(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/command-cycle\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [cycle.a/v1]}\n")
	writeCommandFile(t, filepath.Join(root, "generated", "sentinel.txt"), "must remain unchanged\n")
	writeCommandGraphInterface(t, root, "cycle/a/v1", "av1", "cycle.a/v1", "A")
	writeCommandGraphInterface(t, root, "cycle/b/v1", "bv1", "cycle.b/v1", "B")
	writeCommandFile(t, filepath.Join(root, "cyclea", "service.go"), `package cyclea

import (
	"context"

	av1 "example.com/command-cycle/interfaces/cycle/a/v1"
	bv1 "example.com/command-cycle/interfaces/cycle/b/v1"
)

type Service struct{}

//plystra:implements cycle.a/v1
func New(next bv1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) A(context.Context, av1.Request) (av1.Response, error) {
	return av1.Response{}, nil
}
`)
	writeCommandFile(t, filepath.Join(root, "cycleb", "service.go"), `package cycleb

import (
	"context"

	av1 "example.com/command-cycle/interfaces/cycle/a/v1"
	bv1 "example.com/command-cycle/interfaces/cycle/b/v1"
)

type Service struct{}

//plystra:implements cycle.b/v1
func New(previous av1.Interface) (*Service, error) { return &Service{}, nil }

func (*Service) B(context.Context, bv1.Request) (bv1.Response, error) {
	return bv1.Response{}, nil
}
`)
	return root
}

func writeCommandGraphInterface(t testing.TB, root, relative, packageName, identifier, method string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, "interfaces", filepath.FromSlash(relative), "interface.go"), fmt.Sprintf(`package %s

import "context"

//plystra:interface %s
type Interface interface {
	%s(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`, packageName, identifier, method))
}

func writeCommandDependencyImplementation(t testing.TB, root, modulePath string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire example.com/contracts v1.0.0\n\nreplace example.com/contracts => ../contracts\n", modulePath))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "service", "implementation.go"), `package service

import (
	"context"

	sendv1 "example.com/contracts/interfaces/email/send/v1"
)

type Service struct{}

//plystra:implements email.send/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) {
	return sendv1.Response{}, nil
}
`)
}

func writeCommandGraphImplementationModule(t testing.TB, root, modulePath, source string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26\n\nrequire example.com/contracts v1.0.0\n\nreplace example.com/contracts => ../contracts\n", modulePath))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "service", "implementation.go"), source)
}

func commandContainsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}
