package implementationcreate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfaceinventory"
)

func TestCreateRollsBackAuthoredScaffoldWhenPostValidationFails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeInternalFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/rollback\n\ngo 1.26\n")
	writeInternalFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeInternalFile(t, filepath.Join(root, "interfaces", "email", "send", "v1", "interface.go"), `package sendv1

import "context"

//plystra:interface email.send/v1
type Interface interface { Send(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct{}
`)
	want := errors.New("forced post-validation failure")
	_, err := create(context.Background(), Options{
		Start:       root,
		InterfaceID: "email.send/v1",
		Package:     "./smtp",
		Environment: append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off"),
	}, func(string, interfaceinventory.Discovery) error {
		return want
	})
	if !errors.Is(err, ErrCreate) || !errors.Is(err, want) {
		t.Fatalf("create error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "smtp")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("scaffold was not rolled back: %v", statErr)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".plystra-files-") {
			t.Fatalf("transaction directory remains: %s", entry.Name())
		}
	}
}

func writeInternalFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
