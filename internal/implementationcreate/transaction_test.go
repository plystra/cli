package implementationcreate_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/implementationcreate"
)

func TestCreateValidatesAndGeneratesCompleteSelectedApplication(t *testing.T) {
	for _, mode := range []string{"dormant", "automatic", "explicit", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root := implementationTransactionProject(t)
			environment := goEnvironment()
			configuration := "{}\n"
			if mode != "dormant" {
				configuration = "interfaces:\n  require: [email.send/v1]\n"
			}
			if mode == "explicit" || mode == "environment" || mode == "replacement" {
				writeExistingImplementation(t, root)
				selected := configuration + "  use:\n    email.send/v1: example.com/acme/transaction/existing.New\n"
				switch mode {
				case "explicit":
					configuration = selected
				case "environment":
					writeFile(t, filepath.Join(root, "plystra.production.yaml"), "interfaces:\n  use:\n    email.send/v1: example.com/acme/transaction/existing.New\n")
					environment = append(environment, "PLYSTRA_ENV=production")
				case "replacement":
					writeFile(t, filepath.Join(root, "deploy", "customer.yaml"), selected)
					environment = append(environment, "PLYSTRA_CONFIG=deploy/customer.yaml")
				}
			}
			writeFile(t, filepath.Join(root, "plystra.yaml"), configuration)
			before := snapshotTree(t, root)
			_, err := implementationcreate.Create(t.Context(), implementationcreate.Options{
				Start: root, ContractID: "email.send/v1", Package: "./smtp", Environment: environment,
			})
			if err != nil {
				t.Fatal(err)
			}
			after := snapshotTree(t, root)
			for name, original := range before {
				if name != "go.mod" && name != "go.sum" && after[name] != original {
					t.Fatalf("unrelated authored file changed: %s", name)
				}
			}
			assembly := readFile(t, filepath.Join(root, "generated", "go", "assembly", "interfaces_gen.go"))
			if selected := strings.Contains(assembly, `"example.com/acme/transaction/smtp"`); selected != (mode == "automatic") {
				t.Fatalf("new candidate assembly membership = %t in %s", selected, mode)
			}
			if _, err := os.Stat(filepath.Join(root, "dist", "runtime-baseline.json")); err != nil {
				t.Fatal(err)
			}
			checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, Check: true})
			if err != nil || !checked.Report().Clean() {
				t.Fatalf("generated state is stale: %#v, %v", checked.Report().Changes(), err)
			}
		})
	}
}

func TestCreateRollsBackCompleteApplicationAfterValidationFailure(t *testing.T) {
	for _, generated := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-output", true: "existing-output"}[generated], func(t *testing.T) {
			t.Parallel()
			root := implementationTransactionProject(t)
			if generated {
				_, err := implementationcreate.Create(t.Context(), implementationcreate.Options{
					Start: root, ContractID: "email.send/v1", Package: "./previous", Environment: goEnvironment(),
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			before := snapshotTree(t, root)
			failure := errors.New("candidate build rejected")
			validated := false
			_, err := implementationcreate.Create(t.Context(), implementationcreate.Options{
				Start: root, ContractID: "email.send/v1", Package: "./smtp", Environment: goEnvironment(),
				Validate: func(_ context.Context, root string) error {
					validated = true
					for _, name := range []string{"smtp/implementation.go", "generated/go/assembly/interfaces_gen.go", "dist/runtime-baseline.json"} {
						if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
							t.Errorf("candidate output %s unavailable: %v", name, err)
						}
					}
					return failure
				},
			})
			if !validated || !errors.Is(err, implementationcreate.ErrCreate) || !errors.Is(err, failure) {
				t.Fatalf("validation = %t, error = %v", validated, err)
			}
			if !equalSnapshot(before, snapshotTree(t, root)) {
				t.Fatal("failed candidate did not restore complete bytes and modes")
			}
		})
	}
}

func TestCreatePreservesConcurrentEditsWhenValidationFails(t *testing.T) {
	for _, path := range []string{"smtp/implementation.go", "plystra.yaml", "go.mod", "generated/go/assembly/interfaces_gen.go"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			root := implementationTransactionProject(t)
			before := snapshotTree(t, root)
			failure := errors.New("candidate validation failed after concurrent edit")
			var edited fileState
			_, err := implementationcreate.Create(t.Context(), implementationcreate.Options{
				Start: root, ContractID: "email.send/v1", Package: "./smtp", Environment: goEnvironment(),
				Validate: func(_ context.Context, root string) error {
					name := filepath.Join(root, filepath.FromSlash(path))
					comment := "\n// concurrent authored edit\n"
					if strings.HasSuffix(path, ".yaml") {
						comment = "\n# concurrent authored edit\n"
					}
					writeFile(t, name, readFile(t, name)+comment)
					edited = snapshotTree(t, root)[path]
					return failure
				},
			})
			if !errors.Is(err, failure) {
				t.Fatalf("validation error lost: %v", err)
			}
			before[path] = edited
			if !equalSnapshot(before, snapshotTree(t, root)) {
				t.Fatal("rollback overwrote a concurrent edit or retained unrelated output")
			}
		})
	}
}

func TestCreateRejectsChangedScaffoldAfterSuccessfulValidation(t *testing.T) {
	t.Parallel()
	root := implementationTransactionProject(t)
	before := snapshotTree(t, root)
	const path = "smtp/implementation.go"
	var edited fileState
	_, err := implementationcreate.Create(t.Context(), implementationcreate.Options{
		Start: root, ContractID: "email.send/v1", Package: "./smtp", Environment: goEnvironment(),
		Validate: func(_ context.Context, root string) error {
			name := filepath.Join(root, filepath.FromSlash(path))
			writeFile(t, name, readFile(t, name)+"\n// concurrent authored edit\n")
			edited = snapshotTree(t, root)[path]
			return nil
		},
	})
	if !errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("changed scaffold was accepted: %v", err)
	}
	before[path] = edited
	if !equalSnapshot(before, snapshotTree(t, root)) {
		t.Fatal("concurrent scaffold edit was lost or generated output remained")
	}
}

func implementationTransactionProject(t testing.TB) string {
	t.Helper()
	root := newProject(t, "example.com/acme/transaction")
	writeFile(t, filepath.Join(root, "interfaces", "email", "send", "v1", "interface.go"), interfaceSource("sendv1", "email.send/v1", "Send"))
	writeFile(t, filepath.Join(root, "business_test.go"), "package transaction\nimport \"testing\"\nfunc TestMustNotRun(t *testing.T) { t.Fatal(\"application tests must not execute during scaffolding\") }\n")
	return root
}

func writeExistingImplementation(t testing.TB, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "existing", "implementation.go"), `package existing
import (
 "context"
 contract "example.com/acme/transaction/interfaces/email/send/v1"
)
type Service struct{}
//plystra:implements email.send/v1
func New() (*Service, error) { panic("business constructor must not run during scaffolding") }
func (*Service) Send(context.Context, contract.Request) (contract.Response, error) { panic("business method must not run during scaffolding") }
`)
}
