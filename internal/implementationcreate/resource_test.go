package implementationcreate_test

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/implementationcreate"
)

func TestCreateScaffoldsUnfinishedDependencyResourceProvider(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	root, dependency := filepath.Join(parent, "app"), filepath.Join(parent, "contracts")
	writeProject(t, dependency, "example.com/contracts")
	writeFile(t, filepath.Join(dependency, "database", "resource.go"), `package database
import "context"
type hidden struct { value string }
type Alias = hidden
type Box[T any] struct { Value T }
type Embedded interface { Snapshot() struct { Value int } }
//plystra:resource storage.database/v1
type Resource interface {
 Embedded
 Name() string
 Health(context.Context) error
 Transform(func(Alias) map[string]Box[Alias], ...Alias) (<-chan Box[Alias], error)
}
`)
	writeProject(t, root, "example.com/app")
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire example.com/contracts v1.0.0\nreplace example.com/contracts => ../contracts\n")
	before, dependencyBefore := snapshotTree(t, root), snapshotTree(t, dependency)
	result, err := implementationcreate.Create(t.Context(), implementationcreate.Options{
		Start: root, ContractID: "storage.database/v1", Package: "./database", Environment: goEnvironment(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind() != "resource" || result.ContractID().String() != "storage.database/v1" || result.Constructor().String() != "example.com/app/database.New" || result.SourcePath() != "database/implementation.go" {
		t.Fatalf("Resource result = %#v", result)
	}
	after := snapshotTree(t, root)
	delete(after, result.SourcePath())
	if !equalSnapshot(before, after) || !equalSnapshot(dependencyBefore, snapshotTree(t, dependency)) {
		t.Fatal("scaffolding changed existing Project or dependency files")
	}
	source := readFile(t, filepath.Join(root, filepath.FromSlash(result.SourcePath())))
	for _, wanted := range []string{"//plystra:implements-resource storage.database/v1", "func New() (*Provider, error)", "return nil, errNotImplemented", "var _ contract.Resource = (*Provider)(nil)", "func (*Provider) Name() string", "func (*Provider) Snapshot() struct", "...contract.Alias"} {
		if !strings.Contains(source, wanted) {
			t.Fatalf("scaffold omits %q:\n%s", wanted, source)
		}
	}
	for _, forbidden := range []string{"func init(", "type Config", "func (*Provider) Start(", "func (*Provider) Stop(", "type Resource interface", "type Interface interface"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("scaffold invents %q", forbidden)
		}
	}
	writeFile(t, filepath.Join(root, "database", "implementation_test.go"), `package database
import "testing"
func TestUnfinishedProvider(t *testing.T) {
 value, err := New()
 if value != nil || err == nil || err.Error() != "storage.database/v1 Resource provider is not implemented" { t.Fatalf("New() = %v, %v", value, err) }
 defer func() { if recover() != err { t.Fatal("unfinished method did not fail with the constructor error") } }()
 new(Provider).Name()
}
`)
	command := exec.CommandContext(t.Context(), "go", "test", "-mod=readonly", "./...")
	command.Dir, command.Env = root, goEnvironment()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated ordinary provider did not compile or fail visibly: %v\n%s", err, output)
	}
}

func TestCreateRejectsAmbiguousAndInaccessibleContractsWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, method, extra string
		ambiguous           bool
		want                error
	}{
		{name: "sealed", method: "private()", want: implementationcreate.ErrUnimplementableContract},
		{name: "private parameter", method: "Use(private)", extra: "type private struct{}", want: implementationcreate.ErrUnimplementableContract},
		{name: "private anonymous field", method: "Read() struct { private int }", want: implementationcreate.ErrUnimplementableContract},
		{name: "private anonymous method", method: "Read() interface { private() }", want: implementationcreate.ErrUnimplementableContract},
		{name: "ambiguous kind", method: "Health() error", ambiguous: true, want: implementationcreate.ErrAmbiguousContract},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := newProject(t, "example.com/contracts")
			writeFile(t, filepath.Join(root, "contract", "resource.go"), "package contract\n"+test.extra+"\n//plystra:resource storage.database/v1\ntype Resource interface { "+test.method+" }\n")
			if test.ambiguous {
				writeFile(t, filepath.Join(root, "operation", "interface.go"), interfaceSource("operation", "storage.database/v1", "Run"))
			}
			before := snapshotTree(t, root)
			_, err := implementationcreate.Create(t.Context(), implementationcreate.Options{
				Start: root, ContractID: "storage.database/v1", Package: "./provider", Environment: goEnvironment(),
			})
			if !errors.Is(err, implementationcreate.ErrCreate) || !errors.Is(err, test.want) {
				t.Fatalf("Create() = %v, want %v", err, test.want)
			}
			if !equalSnapshot(before, snapshotTree(t, root)) {
				t.Fatal("rejected contract changed the Project")
			}
		})
	}
}
