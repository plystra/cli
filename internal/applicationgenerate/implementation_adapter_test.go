package applicationgenerate_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationgenerate"
)

func TestGenerateInstallsOnlyReachableImplementationAdaptersAndChecksDrift(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const modulePath = "example.com/acme/implementation-adapter-app"
	writeApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [app.check/v1, app.run/v1]}\n")
	writeGenerationGraphInterface(t, root, "app/check/v1", "checkv1", "app.check/v1", "Check")
	writeGenerationGraphInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeGenerationGraphInterface(t, root, "audit/write/v1", "writev1", "audit.write/v1", "Write")
	writeFile(t, filepath.Join(root, "interfaces", "app", "run", "v1", "interface.yaml"), `errors:
  - code: app_invalid
`)
	writeFile(t, filepath.Join(root, "app", "service.go"), `package app

import (
	"context"

	checkv1 "example.com/acme/implementation-adapter-app/interfaces/app/check/v1"
	runv1 "example.com/acme/implementation-adapter-app/interfaces/app/run/v1"
	writev1 "example.com/acme/implementation-adapter-app/interfaces/audit/write/v1"
)

type service struct { audit writev1.Interface }

//plystra:implements app.check/v1
//plystra:implements app.run/v1
func New(audit writev1.Interface) (*service, error) { return &service{audit: audit}, nil }

func (*service) Check(context.Context, checkv1.Request) (checkv1.Response, error) {
	return checkv1.Response{}, nil
}

func (*service) Run(context.Context, runv1.Request) (runv1.Response, error) {
	return runv1.Response{}, nil
}
`)
	writeFile(t, filepath.Join(root, "audit", "service.go"), `package audit

import (
	"context"

	writev1 "example.com/acme/implementation-adapter-app/interfaces/audit/write/v1"
)

type service struct{}

//plystra:implements audit.write/v1
func New() (*service, error) { return &service{}, nil }

func (*service) Write(context.Context, writev1.Request) (writev1.Response, error) {
	return writev1.Response{}, nil
}
`)

	dependencyRoot := filepath.Join(t.TempDir(), "unused-dependency")
	writeModule(t, dependencyRoot, "example.com/platform/unused-adapter", "")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
	writeGenerationGraphInterface(t, dependencyRoot, "unused/notify/v1", "notifyv1", "unused.notify/v1", "Notify")
	writeFile(t, filepath.Join(dependencyRoot, "unused", "service.go"), `package unused

import (
	"context"

	notifyv1 "example.com/platform/unused-adapter/interfaces/unused/notify/v1"
)

type service struct{}

//plystra:implements unused.notify/v1
func New() (*service, error) { return &service{}, nil }

func (*service) Notify(context.Context, notifyv1.Request) (notifyv1.Response, error) {
	return notifyv1.Response{}, nil
}
`)
	goModPath := filepath.Join(root, "go.mod")
	writeFile(t, goModPath, string(readAbsoluteFile(t, goModPath))+fmt.Sprintf(`
require example.com/platform/unused-adapter v1.0.0

replace example.com/platform/unused-adapter => %s
`, filepath.ToSlash(dependencyRoot)))

	options := applicationgenerate.Options{Start: root, Environment: goEnvironment(nil)}
	generated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !generated.Report().Clean() {
		t.Fatalf("Generate = %#v, %v", generated.Report().Changes(), err)
	}
	checkAdapter := "generated/go/adapters/implementations/app/check/v1/adapter_gen.go"
	runAdapter := "generated/go/adapters/implementations/app/run/v1/adapter_gen.go"
	auditAdapter := "generated/go/adapters/implementations/audit/write/v1/adapter_gen.go"
	unusedAdapter := "generated/go/adapters/implementations/unused/notify/v1/adapter_gen.go"
	for _, path := range []string{checkAdapter, runAdapter, auditAdapter} {
		assertFileExists(t, root, path)
	}
	assertFileMissing(t, root, unusedAdapter)

	checkSource := readFile(t, root, checkAdapter)
	runSource := readFile(t, root, runAdapter)
	for name, source := range map[string][]byte{"check": checkSource, "run": runSource} {
		for _, required := range [][]byte{
			[]byte(`const ConstructorSymbol = "example.com/acme/implementation-adapter-app/app.New"`),
			[]byte(`const ConcreteType = "*example.com/acme/implementation-adapter-app/app.service"`),
			[]byte(`func NewEndpoint(implementation contract.Interface) (kernelinvocation.Endpoint, error)`),
		} {
			if !bytes.Contains(source, required) {
				t.Fatalf("generated %s adapter omits %q:\n%s", name, required, source)
			}
		}
	}
	if !bytes.Contains(runSource, []byte(`"app_invalid"`)) || bytes.Contains(checkSource, []byte(`"app_invalid"`)) {
		t.Fatalf("semantic errors were not bound to their exact Interface:\ncheck:\n%s\nrun:\n%s", checkSource, runSource)
	}
	provenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
	if err != nil || provenance.ApplicationModelDigest() == "" {
		t.Fatalf("DecodeManifestProvenance = %#v, %v", provenance, err)
	}

	check := options
	check.Check = true
	clean, err := applicationgenerate.Generate(t.Context(), check)
	if err != nil || !clean.Report().Clean() {
		t.Fatalf("clean Generate check = %#v, %v", clean.Report().Changes(), err)
	}

	runPath := filepath.Join(root, filepath.FromSlash(runAdapter))
	if err := os.WriteFile(runPath, []byte("manual adapter drift\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", runPath, err)
	}
	changedBefore := snapshotTree(t, root)
	changed, err := applicationgenerate.Generate(t.Context(), check)
	if err != nil || !reflect.DeepEqual(changed.Report().ManuallyModified(), []string{runAdapter}) {
		t.Fatalf("changed adapter check = %#v, %v", changed.Report().Changes(), err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, changedBefore) {
		t.Fatal("changed adapter check mutated the Project")
	}

	if err := os.Remove(runPath); err != nil {
		t.Fatalf("Remove(%s): %v", runPath, err)
	}
	missingBefore := snapshotTree(t, root)
	missing, err := applicationgenerate.Generate(t.Context(), check)
	if err != nil || !reflect.DeepEqual(missing.Report().Missing(), []string{runAdapter}) {
		t.Fatalf("missing adapter check = %#v, %v", missing.Report().Changes(), err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, missingBefore) {
		t.Fatal("missing adapter check mutated the Project")
	}

	repaired, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !repaired.Report().Clean() {
		t.Fatalf("repair adapter = %#v, %v", repaired.Report().Changes(), err)
	}
	if got := readFile(t, root, runAdapter); !bytes.Equal(got, runSource) {
		t.Fatalf("repaired adapter differs:\nwant:\n%s\ngot:\n%s", runSource, got)
	}
}
