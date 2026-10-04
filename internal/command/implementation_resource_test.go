package command_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestRunImplementScaffoldsUnfinishedResourceWithoutActivation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeImplementationCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/storage\n\ngo 1.26\n")
	writeImplementationCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeImplementationCommandFile(t, filepath.Join(root, "contracts", "resource.go"), `package contracts

//plystra:resource storage.database/v1
type Resource interface {
	Read() Value
}

type Value struct {
	Data string
}
`)

	var stdout, stderr bytes.Buffer
	exitCode := command.RunIn(
		[]string{"implement", "storage.database/v1", "--package", "./provider"},
		&stdout,
		&stderr,
		root,
		append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off"),
	)
	if exitCode != 0 || stderr.Len() != 0 {
		t.Fatalf("implement Resource = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if want := "created unfinished resource scaffold example.com/acme/storage/provider.New for storage.database/v1 at provider/implementation.go; not activated\n"; stdout.String() != want {
		t.Fatalf("Resource output = %q, want %q", stdout.String(), want)
	}
	source, err := os.ReadFile(filepath.Join(root, "provider", "implementation.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, phrase := range []string{
		"//plystra:implements-resource storage.database/v1",
		"type Provider struct{}",
		"func New() (*Provider, error)",
		"return nil, errNotImplemented",
		"func (*Provider) Read() contract.Value",
		"var _ contract.Resource = (*Provider)(nil)",
	} {
		if !strings.Contains(text, phrase) {
			t.Fatalf("Resource scaffold omits %q:\n%s", phrase, text)
		}
	}
	for _, forbidden := range []string{"type Config", "func init(", "func (*Provider) Start(", "func (*Provider) Stop(", "resources:", "instances:"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Resource scaffold contains forbidden %q:\n%s", forbidden, text)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "generated")); !os.IsNotExist(err) {
		t.Fatalf("Resource scaffold created generated output: %v", err)
	}
	writeImplementationCommandFile(t, filepath.Join(root, "provider", "implementation_test.go"), `package provider

import "testing"

func TestUnfinishedProvider(t *testing.T) {
	value, err := New()
	if value != nil || err == nil || err.Error() != "storage.database/v1 Resource provider is not implemented" {
		t.Fatalf("New() = %v, %v", value, err)
	}
	defer func() {
		if recover() != err {
			t.Fatal("unfinished method did not fail with the constructor error")
		}
	}()
	new(Provider).Read()
}
`)
	compile := exec.CommandContext(t.Context(), "go", "test", "-mod=readonly", "./...")
	compile.Dir = root
	compile.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("generated Resource provider did not compile or fail visibly: %v\n%s", err, output)
	}
}

func TestRunImplementRejectsAmbiguousResourceContractWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeImplementationCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/storage\n\ngo 1.26\n")
	writeImplementationCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeImplementationCommandFile(t, filepath.Join(root, "contracts", "resource.go"), `package contracts

//plystra:resource storage.database/v1
type Resource interface { Read() Value }
type Value struct{}
`)
	writeImplementationCommandFile(t, filepath.Join(root, "interfaces", "interface.go"), `package interfaces

import "context"

//plystra:interface storage.database/v1
type Interface interface { Read(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct{}
`)
	before := commandTree(t, root)
	var stdout, stderr bytes.Buffer
	exitCode := command.RunIn(
		[]string{"implement", "storage.database/v1", "--package", "./provider"},
		&stdout,
		&stderr,
		root,
		append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off"),
	)
	if exitCode != 1 || stdout.Len() != 0 || !commandContainsAll(stderr.String(),
		"contract ID names both an Interface and a Resource",
		"Diagnostic: "+diagnosticcode.ImplementationCreateContractAmbiguous,
		"Recovery:\nReplace the reported contract ID with an unambiguous visible Interface or Resource ID, then rerun the command.\n",
	) {
		t.Fatalf("ambiguous Resource = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if after := commandTree(t, root); !equalCommandTree(after, before) {
		t.Fatalf("ambiguous Resource changed Project:\nbefore: %#v\nafter: %#v", before, after)
	}
	assertNoCommandTransactions(t, root)
}

func TestRunImplementRejectsUnimplementableResourceWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeImplementationCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/storage\n\ngo 1.26\n")
	writeImplementationCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeImplementationCommandFile(t, filepath.Join(root, "contracts", "resource.go"), `package contracts

type private struct{}

//plystra:resource storage.database/v1
type Resource interface {
	Read(private) error
}
`)
	before := commandTree(t, root)
	var stdout, stderr bytes.Buffer
	exitCode := command.RunIn(
		[]string{"implement", "storage.database/v1", "--package", "./provider"},
		&stdout,
		&stderr,
		root,
		append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off"),
	)
	if exitCode != 1 || stdout.Len() != 0 || !commandContainsAll(stderr.String(),
		"contract cannot be implemented in the target package",
		"Diagnostic: "+diagnosticcode.ImplementationCreateContractUnimplementable,
		"Recovery:\nChoose a target package that can access the reported contract signature, or change the contract to an exported implementable signature, then rerun the command.\n",
	) {
		t.Fatalf("unimplementable Resource = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if after := commandTree(t, root); !equalCommandTree(after, before) {
		t.Fatalf("unimplementable Resource changed Project:\nbefore: %#v\nafter: %#v", before, after)
	}
	assertNoCommandTransactions(t, root)
}

func equalCommandTree(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for path, content := range left {
		if string(content) != string(right[path]) {
			return false
		}
	}
	return true
}
