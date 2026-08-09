package command_test

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestPublicResolvingCommandsCollectLocalApplicationRootsAndRejectInvalidGraphWithoutMutation(t *testing.T) {
	t.Parallel()

	commands := [][]string{{"generate"}, {"generate", "--check"}, {"check"}}
	for _, arguments := range commands {
		name := strings.Join(arguments, " ")
		t.Run(name, func(t *testing.T) {
			root := writeCommandGraphFailureProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, arguments, filepath.Join(root, "app"), commandGoEnvironment())
			if exitCode != 1 || stdout != "" || !commandContainsAll(
				stderr,
				"app.run/v1",
				"audit.write/v1",
				"example.com/command-graph/app.New",
				"//plystra:implements app.run/v1",
				"Recovery:\nCreate one compatible local Implementation by running `plystra implement audit.write/v1 --package <project-relative-package>`.\n",
				"Diagnostic: "+diagnosticcode.ResolveMissingImplementation,
			) {
				t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%v mutated Project before rejecting graph:\nbefore: %#v\nafter:  %#v", arguments, before, after)
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
			if exitCode != 1 || stdout != "" || !commandContainsAll(
				stderr,
				"email.send/v1",
				"example.com/acme/implementation-use/local.New",
				"example.com/acme/implementation-use/smtp.New",
				"Recovery:\nSelect one compatible Implementation by running `plystra use email.send/v1 <constructor-symbol>`.\n",
				"Diagnostic: "+diagnosticcode.ResolveMultipleImplementations,
			) {
				t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%v mutated ambiguous Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
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
			if exitCode != 1 || stdout != "" || !commandContainsAll(
				stderr,
				"cycle.a/v1",
				"cycle.b/v1",
				"example.com/command-cycle/cyclea.New",
				"example.com/command-cycle/cycleb.New",
				"Recovery:\nRemove one required Interface parameter from the reported constructor cycle, then rerun the command.\n",
				"Diagnostic: "+diagnosticcode.ResolveConstructorCycle,
			) {
				t.Fatalf("%v = exit %d stdout %q stderr %q", arguments, exitCode, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%v mutated cyclic Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
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

func writeCommandGraphFailureProject(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/command-graph\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "generated", "sentinel.txt"), "must remain unchanged\n")
	writeCommandGraphInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeCommandGraphInterface(t, root, "audit/write/v1", "writev1", "audit.write/v1", "Write")
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
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
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

func commandContainsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}
