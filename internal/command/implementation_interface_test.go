package command_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestRunImplementRejectsInaccessibleInterfacePackageWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeImplementationCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/records\n\ngo 1.26\n")
	writeImplementationCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeImplementationCommandFile(t, filepath.Join(root, "interfaces", "internal", "records", "list", "v1", "interface.go"), `package listv1

import "context"

//plystra:interface records.list/v1
type Interface interface { List(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct{}
`)

	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"implement", "records.list/v1", "--package", "./postgres"}, root, append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off"))
	if exitCode != 1 || stdout != "" || !commandContainsAll(
		stderr,
		"contract is not visible",
		"Recovery:\nReplace the reported contract ID with one canonical Interface or Resource visible in the effective Plystra Project graph, then rerun the command.\n",
		"Diagnostic: "+diagnosticcode.ImplementationCreateContractNotFound,
	) {
		t.Fatalf("inaccessible Interface = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
		t.Fatalf("inaccessible Interface emitted unstable diagnostic framing: %q", stderr)
	}
	if after := commandTree(t, root); !equalCommandTree(after, before) {
		t.Fatalf("inaccessible Interface changed Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoCommandTransactions(t, root)
}
