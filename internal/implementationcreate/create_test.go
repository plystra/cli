package implementationcreate_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/implementationcreate"
)

func TestCreateScaffoldsOrdinaryImplementationOfDependencyInterface(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "app")
	dependencyRoot := filepath.Join(parent, "contracts")
	writeProject(t, dependencyRoot, "example.com/contracts")
	writeFile(t, filepath.Join(dependencyRoot, "interfaces", "email", "send", "v1", "interface.go"), interfaceSource("sendv1", "email.send/v1", "Send"))
	writeFile(t, filepath.Join(root, "go.mod"), `module example.com/shop

go 1.26

require example.com/contracts v1.2.3

replace example.com/contracts => ../contracts
`)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	start := filepath.Join(root, "cmd", "server")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}
	beforeGoMod := readFile(t, filepath.Join(root, "go.mod"))
	beforeDependency := snapshotTree(t, dependencyRoot)

	result, err := implementationcreate.Create(context.Background(), implementationcreate.Options{
		Start:       start,
		InterfaceID: "email.send/v1",
		Package:     "./smtp",
		Environment: goEnvironment(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.InterfaceID().String() != "email.send/v1" || result.ModuleRoot() != root || result.PackagePath() != filepath.Join(root, "smtp") || result.ImportPath() != "example.com/shop/smtp" || result.SourcePath() != "smtp/implementation.go" || result.Constructor().String() != "example.com/shop/smtp.New" {
		t.Fatalf("Result = %#v", result)
	}
	want := `package smtp

import (
	"context"
	"errors"

	contract "example.com/contracts/interfaces/email/send/v1"
)

var errNotImplemented = errors.New("email.send/v1 implementation is not implemented")

type Service struct{}

//plystra:implements email.send/v1
func New() (*Service, error) {
	return &Service{}, nil
}

func (*Service) Send(ctx context.Context, request contract.Request) (contract.Response, error) {
	return contract.Response{}, errNotImplemented
}

var _ contract.Interface = (*Service)(nil)
`
	got := readFile(t, filepath.Join(root, filepath.FromSlash(result.SourcePath())))
	if got != want {
		t.Fatalf("implementation.go = %q, want %q", got, want)
	}
	if got := readFile(t, filepath.Join(root, "go.mod")); got != beforeGoMod {
		t.Fatalf("go.mod changed:\n%s", got)
	}
	if after := snapshotTree(t, dependencyRoot); !equalSnapshot(after, beforeDependency) {
		t.Fatalf("dependency Project changed:\nbefore: %#v\nafter: %#v", beforeDependency, after)
	}
	for _, forbidden := range []string{"//plystra:interface", "type Interface interface", "Register", "generated/"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("scaffold contains forbidden substitute or registration content %q", forbidden)
		}
	}
}

func TestCreateSupportsNestedImplementationPackage(t *testing.T) {
	t.Parallel()

	root := newProject(t, "example.com/acme/orders")
	writeFile(t, filepath.Join(root, "interfaces", "order", "create", "v1", "interface.go"), interfaceSource("createv1", "order.create/v1", "Create"))
	result, err := implementationcreate.Create(context.Background(), implementationcreate.Options{
		Start:       root,
		InterfaceID: "order.create/v1",
		Package:     "./internal/orders/postgres",
		Environment: goEnvironment(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ImportPath() != "example.com/acme/orders/internal/orders/postgres" || result.SourcePath() != "internal/orders/postgres/implementation.go" {
		t.Fatalf("Result = %#v", result)
	}
	data := readFile(t, filepath.Join(root, filepath.FromSlash(result.SourcePath())))
	for _, want := range []string{"package postgres", `contract "example.com/acme/orders/interfaces/order/create/v1"`, "Create(ctx context.Context, request contract.Request) (contract.Response, error)", "var _ contract.Interface = (*Service)(nil)"} {
		if !strings.Contains(data, want) {
			t.Fatalf("implementation.go omits %q:\n%s", want, data)
		}
	}
}

func TestCreateRejectsInvalidInputsBeforeMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		interfaceID string
		packagePath string
		want        error
	}{
		{name: "invalid Interface", interfaceID: "email.send", packagePath: "./smtp", want: implementationcreate.ErrInvalidInterface},
		{name: "empty package", interfaceID: "email.send/v1", want: implementationcreate.ErrInvalidPackage},
		{name: "root package", interfaceID: "email.send/v1", packagePath: ".", want: implementationcreate.ErrInvalidPackage},
		{name: "missing relative prefix", interfaceID: "email.send/v1", packagePath: "smtp", want: implementationcreate.ErrInvalidPackage},
		{name: "absolute", interfaceID: "email.send/v1", packagePath: filepath.Join(t.TempDir(), "smtp"), want: implementationcreate.ErrInvalidPackage},
		{name: "traversal", interfaceID: "email.send/v1", packagePath: "./../smtp", want: implementationcreate.ErrInvalidPackage},
		{name: "noncanonical", interfaceID: "email.send/v1", packagePath: "./mail/../smtp", want: implementationcreate.ErrInvalidPackage},
		{name: "backslash", interfaceID: "email.send/v1", packagePath: `.\smtp`, want: implementationcreate.ErrInvalidPackage},
		{name: "invalid package identifier", interfaceID: "email.send/v1", packagePath: "./smtp-client", want: implementationcreate.ErrInvalidPackage},
		{name: "generated package", interfaceID: "email.send/v1", packagePath: "./generated/smtp", want: implementationcreate.ErrInvalidPackage},
		{name: "vendor package", interfaceID: "email.send/v1", packagePath: "./mail/vendor/smtp", want: implementationcreate.ErrInvalidPackage},
		{name: "hidden package", interfaceID: "email.send/v1", packagePath: "./mail/.private/smtp", want: implementationcreate.ErrInvalidPackage},
		{name: "underscore package", interfaceID: "email.send/v1", packagePath: "./mail/_private/smtp", want: implementationcreate.ErrInvalidPackage},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := newProject(t, "example.com/acme/invalid")
			before := snapshotTree(t, root)
			_, err := implementationcreate.Create(context.Background(), implementationcreate.Options{
				Start:       root,
				InterfaceID: test.interfaceID,
				Package:     test.packagePath,
				Environment: goEnvironment(),
			})
			if !errors.Is(err, implementationcreate.ErrCreate) || !errors.Is(err, test.want) {
				t.Fatalf("Create error = %v", err)
			}
			if after := snapshotTree(t, root); !equalSnapshot(after, before) {
				t.Fatalf("invalid input mutated Project:\nbefore: %#v\nafter: %#v", before, after)
			}
		})
	}
}

func TestCreateRejectsUnknownInterfaceAndExistingTarget(t *testing.T) {
	t.Parallel()

	t.Run("unknown Interface", func(t *testing.T) {
		t.Parallel()
		root := newProject(t, "example.com/acme/unknown")
		before := snapshotTree(t, root)
		_, err := implementationcreate.Create(context.Background(), implementationcreate.Options{Start: root, InterfaceID: "email.send/v1", Package: "./smtp", Environment: goEnvironment()})
		if !errors.Is(err, implementationcreate.ErrCreate) || !errors.Is(err, implementationcreate.ErrInterfaceNotFound) || !strings.Contains(err.Error(), "email.send/v1") {
			t.Fatalf("Create error = %v", err)
		}
		if after := snapshotTree(t, root); !equalSnapshot(after, before) {
			t.Fatalf("unknown Interface mutated Project:\nbefore: %#v\nafter: %#v", before, after)
		}
	})

	t.Run("existing target", func(t *testing.T) {
		t.Parallel()
		root := newProject(t, "example.com/acme/existing")
		writeFile(t, filepath.Join(root, "interfaces", "email", "send", "v1", "interface.go"), interfaceSource("sendv1", "email.send/v1", "Send"))
		keep := filepath.Join(root, "smtp", "keep.go")
		writeFile(t, keep, "package smtp\n\nconst Keep = true\n")
		before := snapshotTree(t, root)
		_, err := implementationcreate.Create(context.Background(), implementationcreate.Options{Start: root, InterfaceID: "email.send/v1", Package: "./smtp", Environment: goEnvironment()})
		if !errors.Is(err, implementationcreate.ErrCreate) || !errors.Is(err, implementationcreate.ErrTargetExists) {
			t.Fatalf("Create error = %v", err)
		}
		if after := snapshotTree(t, root); !equalSnapshot(after, before) {
			t.Fatalf("existing target mutated Project:\nbefore: %#v\nafter: %#v", before, after)
		}
	})
}

func TestCreateRejectsDuplicateVisibleInterfaceDefinitionsBeforeMutation(t *testing.T) {
	t.Parallel()

	root := newProject(t, "example.com/acme/duplicate")
	writeFile(t, filepath.Join(root, "interfaces", "email", "send", "v1", "interface.go"), interfaceSource("sendv1", "email.send/v1", "Send"))
	writeFile(t, filepath.Join(root, "duplicate", "interface.go"), interfaceSource("duplicate", "email.send/v1", "Send"))
	before := snapshotTree(t, root)
	_, err := implementationcreate.Create(context.Background(), implementationcreate.Options{
		Start:       root,
		InterfaceID: "email.send/v1",
		Package:     "./smtp",
		Environment: goEnvironment(),
	})
	if !errors.Is(err, implementationcreate.ErrCreate) || !strings.Contains(err.Error(), "duplicate visible Interface ID") {
		t.Fatalf("Create error = %v", err)
	}
	if after := snapshotTree(t, root); !equalSnapshot(after, before) {
		t.Fatalf("duplicate Interface definitions mutated Project:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestCreateRequiresContext(t *testing.T) {
	t.Parallel()

	_, err := implementationcreate.Create(nil, implementationcreate.Options{InterfaceID: "email.send/v1", Package: "./smtp"})
	if !errors.Is(err, implementationcreate.ErrCreate) || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("Create error = %v", err)
	}
}

func interfaceSource(packageName, id, method string) string {
	return `package ` + packageName + `

import "context"

//plystra:interface ` + id + `
type Interface interface {
	` + method + `(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`
}

func newProject(t testing.TB, modulePath string) string {
	t.Helper()
	root := t.TempDir()
	writeProject(t, root, modulePath)
	return root
}

func writeProject(t testing.TB, root, modulePath string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "go.mod"), "module "+modulePath+"\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
}

func goEnvironment() []string {
	return append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
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

func readFile(t testing.TB, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type fileState struct {
	data string
	mode os.FileMode
}

func snapshotTree(t testing.TB, root string) map[string]fileState {
	t.Helper()
	result := make(map[string]fileState)
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = fileState{data: string(data), mode: info.Mode()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func equalSnapshot(left, right map[string]fileState) bool {
	if len(left) != len(right) {
		return false
	}
	for name, value := range left {
		if right[name] != value {
			return false
		}
	}
	return true
}
