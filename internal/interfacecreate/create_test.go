package interfacecreate_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacecreate"
)

func TestCreateScaffoldsCanonicalV1Package(t *testing.T) {
	t.Parallel()

	root := newProject(t, "example.com/acme/orders")
	start := filepath.Join(root, "cmd", "worker")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := interfacecreate.Create(context.Background(), interfacecreate.Options{
		Start:       start,
		Name:        "order.create",
		Environment: goEnvironment(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID().String() != "order.create/v1" || result.ModuleRoot() != root || result.ImportPath() != "example.com/acme/orders/interfaces/order/create/v1" || result.SourcePath() != "interfaces/order/create/v1/interface.go" || result.PackagePath() != filepath.Join(root, "interfaces", "order", "create", "v1") {
		t.Fatalf("result = %#v", result)
	}
	want := `package createv1

import "context"

//plystra:interface order.create/v1
type Interface interface {
	Create(context.Context, Request) (Response, error)
}

// Request contains the order.create/v1 request fields.
type Request struct{}

// Response contains the order.create/v1 response fields.
type Response struct{}
`
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(result.SourcePath())))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("interface.go = %q, want %q", data, want)
	}
	if _, err := os.Stat(filepath.Join(result.PackagePath(), "interface.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interface.yaml exists or could not be inspected: %v", err)
	}
	assertFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/orders\n\ngo 1.26\n")
	assertFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
}

func TestCreateDerivesKebabCaseGoNames(t *testing.T) {
	t.Parallel()

	root := newProject(t, "example.com/acme/accounts")
	result, err := interfacecreate.Create(context.Background(), interfacecreate.Options{
		Start:       root,
		Name:        "account.reset-password",
		Environment: goEnvironment(),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(result.SourcePath())))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"package resetpasswordv1", "ResetPassword(context.Context, Request)"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("interface.go does not contain %q:\n%s", want, data)
		}
	}
}

func TestCreateRejectsInvalidUnversionedNamesBeforeMutation(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "order", "Order.create", "order/create", "order.create/v1", ".order.create", "order..create", "order.create ", "../order.create"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := newProject(t, "example.com/acme/invalid")
			_, err := interfacecreate.Create(context.Background(), interfacecreate.Options{Start: root, Name: name, Environment: goEnvironment()})
			if !errors.Is(err, interfacecreate.ErrCreate) || !errors.Is(err, interfacecreate.ErrInvalidName) {
				t.Fatalf("Create(%q) error = %v", name, err)
			}
			if _, statErr := os.Stat(filepath.Join(root, "interfaces")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("interfaces changed for %q: %v", name, statErr)
			}
		})
	}
}

func TestCreateRejectsExistingTargetPackageWithoutMutation(t *testing.T) {
	t.Parallel()

	root := newProject(t, "example.com/acme/existing")
	keep := filepath.Join(root, "interfaces", "order", "create", "v1", "keep.txt")
	writeFile(t, keep, "keep\n")
	_, err := interfacecreate.Create(context.Background(), interfacecreate.Options{Start: root, Name: "order.create", Environment: goEnvironment()})
	if !errors.Is(err, interfacecreate.ErrCreate) || !errors.Is(err, interfacecreate.ErrTargetExists) {
		t.Fatalf("Create error = %v", err)
	}
	assertFile(t, keep, "keep\n")
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(keep), "interface.go")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("interface.go changed: %v", statErr)
	}
}

func TestCreateRejectsExistingVisibleIDBeforeTargetMutation(t *testing.T) {
	t.Parallel()

	root := newProject(t, "example.com/acme/visible")
	writeFile(t, filepath.Join(root, "contracts", "legacy", "interface.go"), `package legacy
import "context"
//plystra:interface order.create/v1
type Interface interface { Create(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct{}
`)
	_, err := interfacecreate.Create(context.Background(), interfacecreate.Options{Start: root, Name: "order.create", Environment: goEnvironment()})
	if !errors.Is(err, interfacecreate.ErrCreate) || !errors.Is(err, interfacecreate.ErrTargetExists) || !strings.Contains(err.Error(), "example.com/acme/visible/contracts/legacy") {
		t.Fatalf("Create error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "interfaces")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target tree changed: %v", statErr)
	}
}

func TestCreateRequiresContext(t *testing.T) {
	t.Parallel()

	_, err := interfacecreate.Create(nil, interfacecreate.Options{Name: "order.create"})
	if !errors.Is(err, interfacecreate.ErrCreate) || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("Create error = %v", err)
	}
}

func newProject(t testing.TB, modulePath string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module "+modulePath+"\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	return root
}

func goEnvironment() []string {
	result := append([]string(nil), os.Environ()...)
	return append(result, "GOWORK=off")
}

func writeFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t testing.TB, name, want string) {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", name, data, want)
	}
}
