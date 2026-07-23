package command_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
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

func writeImplementationCommandFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
