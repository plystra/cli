package command_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestRunImplementUsesPublicCommandSurface(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeImplementationCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/records\n\ngo 1.26\n")
	writeImplementationCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeImplementationCommandFile(t, filepath.Join(root, "interfaces", "records", "list", "v1", "interface.go"), `package listv1

import "context"

//plystra:interface records.list/v1
type Interface interface { List(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct{}
`)
	start := filepath.Join(root, "cmd", "server")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := command.RunIn(
		[]string{"implement", "records.list/v1", "--package", "./postgres"},
		&stdout,
		&stderr,
		start,
		append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off"),
	)
	if exitCode != 0 || stdout.String() != "created Implementation example.com/acme/records/postgres.New for records.list/v1 at postgres/implementation.go\n" || stderr.Len() != 0 {
		t.Fatalf("implement = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "postgres", "implementation.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"//plystra:implements records.list/v1", `contract "example.com/acme/records/interfaces/records/list/v1"`, "List(ctx context.Context, request contract.Request) (contract.Response, error)", "var _ contract.Interface = (*Service)(nil)"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("implementation.go omits %q:\n%s", want, data)
		}
	}
}

func TestRunImplementClassifiesAuthoringFailuresWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		arguments  []string
		target     bool
		problem    string
		recovery   string
		diagnostic string
	}{
		{
			name:       "invalid Interface ID",
			arguments:  []string{"implement", "records.list", "--package", "./postgres"},
			problem:    "invalid Interface ID",
			recovery:   "Rerun `plystra implement <interface-name>/vN --package <project-relative-package>` with one canonical versioned Interface ID.",
			diagnostic: diagnosticcode.ImplementationCreateInterfaceInvalid,
		},
		{
			name:       "invalid package",
			arguments:  []string{"implement", "records.list/v1", "--package", "postgres"},
			problem:    "expected ./ followed by one safe Project-relative Go package path",
			recovery:   "Rerun with `--package ./<safe-project-relative-go-package>` naming one canonical child package.",
			diagnostic: diagnosticcode.ImplementationCreatePackageInvalid,
		},
		{
			name:       "missing Interface",
			arguments:  []string{"implement", "records.missing/v1", "--package", "./postgres"},
			problem:    "Interface is not visible",
			recovery:   "Replace the reported Interface ID with one canonical Interface visible in the effective Plystra Project graph, then rerun the command.",
			diagnostic: diagnosticcode.ImplementationCreateInterfaceNotFound,
		},
		{
			name:       "existing target",
			arguments:  []string{"implement", "records.list/v1", "--package", "./postgres"},
			target:     true,
			problem:    "Implementation target already exists",
			recovery:   "Rerun with a different `--package ./<project-relative-go-package>` whose target directory does not exist.",
			diagnostic: diagnosticcode.ImplementationCreateTargetExists,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := writeImplementationFailureProject(t, test.target)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, test.arguments, root, commandGoEnvironment())
			if exitCode != 1 || stdout != "" || !commandContainsAll(
				stderr,
				test.problem,
				"Recovery:\n"+test.recovery+"\n",
				"Diagnostic: "+test.diagnostic,
			) {
				t.Fatalf("%v = exit %d stdout %q stderr %q", test.arguments, exitCode, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("%v mutated Project:\nbefore: %#v\nafter:  %#v", test.arguments, before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func writeImplementationFailureProject(t testing.TB, targetExists bool) string {
	t.Helper()
	root := t.TempDir()
	writeImplementationCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/records\n\ngo 1.26\n")
	writeImplementationCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeImplementationCommandFile(t, filepath.Join(root, "interfaces", "records", "list", "v1", "interface.go"), `package listv1

import "context"

//plystra:interface records.list/v1
type Interface interface { List(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct{}
`)
	if targetExists {
		writeImplementationCommandFile(t, filepath.Join(root, "postgres", "keep.go"), "package postgres\n")
	}
	return root
}

func writeImplementationCommandFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
