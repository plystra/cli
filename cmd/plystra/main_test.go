package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBinaryScaffoldsBuildableImplementationFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "app")
	writeAcceptanceFile(t, filepath.Join(project, "go.mod"), "module example.com/acceptance\n\ngo 1.26\n")
	writeAcceptanceFile(t, filepath.Join(project, "plystra.yaml"), "{}\n")
	writeAcceptanceFile(t, filepath.Join(project, "interfaces", "email", "send", "v1", "interface.go"), `package sendv1

import "context"

//plystra:interface email.send/v1
type Interface interface {
	Send(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`)
	nested := filepath.Join(project, "cmd", "server")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(root, "plystra")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build plystra binary: %v\n%s", err, output)
	}

	command := exec.Command(binary, "implement", "email.send/v1", "--package", "./smtp")
	command.Dir = nested
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run plystra implement: %v, stdout %q, stderr %q", err, stdout.String(), stderr.String())
	}
	if want := "created Implementation example.com/acceptance/smtp.New for email.send/v1 at smtp/implementation.go\n"; stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("plystra implement = stdout %q, stderr %q, want stdout %q", stdout.String(), stderr.String(), want)
	}

	data, err := os.ReadFile(filepath.Join(project, "smtp", "implementation.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, required := range []string{
		`contract "example.com/acceptance/interfaces/email/send/v1"`,
		"//plystra:implements email.send/v1",
		"func New() (*Service, error)",
		"func (*Service) Send(ctx context.Context, request contract.Request) (contract.Response, error)",
		"var _ contract.Interface = (*Service)(nil)",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("implementation.go omits %q:\n%s", required, source)
		}
	}
	for _, forbidden := range []string{"//plystra:interface", "type Interface interface", "Register"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("implementation.go contains forbidden substitute or registration content %q", forbidden)
		}
	}

	test := exec.Command("go", "test", "./...")
	test.Dir = project
	test.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	if output, err := test.CombinedOutput(); err != nil {
		t.Fatalf("test scaffolded Project: %v\n%s", err, output)
	}
}

func writeAcceptanceFile(t testing.TB, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
