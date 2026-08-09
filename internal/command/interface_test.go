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

func TestRunInterfaceCreateUsesPublicCommandSurface(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeInterfaceCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/records\n\ngo 1.26\n")
	writeInterfaceCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	start := filepath.Join(root, "cmd", "server")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := command.RunIn(
		[]string{"interface", "create", "records.list"},
		&stdout,
		&stderr,
		start,
		append(os.Environ(), "GOWORK=off"),
	)
	if exitCode != 0 || stdout.String() != "created Interface records.list/v1 at interfaces/records/list/v1/interface.go\n" || stderr.Len() != 0 {
		t.Fatalf("interface create = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "interfaces", "records", "list", "v1", "interface.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"//plystra:interface records.list/v1", "List(context.Context, Request) (Response, error)"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("interface.go does not contain %q:\n%s", want, data)
		}
	}
}

func TestRunInterfaceCreateReportsExistingIdentityWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeInterfaceCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/records\n\ngo 1.26\n")
	writeInterfaceCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	environment := append(os.Environ(), "GOWORK=off")
	if exitCode := command.RunIn([]string{"interface", "create", "records.list"}, &stdout, &stderr, root, environment); exitCode != 0 {
		t.Fatalf("first create = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	before, err := os.ReadFile(filepath.Join(root, "interfaces", "records", "list", "v1", "interface.go"))
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	exitCode := command.RunIn([]string{"interface", "create", "records.list"}, &stdout, &stderr, root, environment)
	if exitCode != 1 || stdout.Len() != 0 || !commandContainsAll(
		stderr.String(),
		"create Interface: create Interface package: Interface target already exists",
		"Recovery:\nChoose a different unversioned Interface name whose v1 package and visible ID do not already exist.\n",
		"Diagnostic: "+diagnosticcode.InterfaceCreateTargetExists,
	) {
		t.Fatalf("duplicate create = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	after, err := os.ReadFile(filepath.Join(root, "interfaces", "records", "list", "v1", "interface.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("duplicate create changed the authored Interface")
	}
	assertNoCommandTransactions(t, root)
}

func TestRunInterfaceCreateClassifiesInvalidNameWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeInterfaceCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/records\n\ngo 1.26\n")
	writeInterfaceCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"interface", "create", "records"}, root, commandGoEnvironment())
	if exitCode != 1 || stdout != "" || !commandContainsAll(
		stderr,
		"invalid Interface name",
		"Recovery:\nRun `plystra interface create <domain.operation>` with one unversioned canonical lower-case name containing at least two dot-separated segments.\n",
		"Diagnostic: "+diagnosticcode.InterfaceCreateNameInvalid,
	) {
		t.Fatalf("invalid create = exit %d stdout %q stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("invalid Interface name mutated Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoCommandTransactions(t, root)
}

func writeInterfaceCommandFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
