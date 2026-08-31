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

func TestGenerateInstallsOnlyReachableInterfaceRuntimeAndChecksProxyDrift(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/acme/interface-proxy-app")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [app.run/v1]}\n")
	writeGenerationGraphInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeGenerationGraphInterface(t, root, "audit/write/v1", "writev1", "audit.write/v1", "Write")
	writeFile(t, filepath.Join(root, "app", "service.go"), `package app

import (
	"context"

	runv1 "example.com/acme/interface-proxy-app/interfaces/app/run/v1"
	writev1 "example.com/acme/interface-proxy-app/interfaces/audit/write/v1"
)

type Service struct { audit writev1.Interface }

//plystra:implements app.run/v1
func New(audit writev1.Interface) (*Service, error) { return &Service{audit: audit}, nil }

func (*Service) Run(context.Context, runv1.Request) (runv1.Response, error) {
	return runv1.Response{}, nil
}
`)
	writeFile(t, filepath.Join(root, "audit", "service.go"), `package audit

import (
	"context"

	writev1 "example.com/acme/interface-proxy-app/interfaces/audit/write/v1"
)

type Service struct{}

//plystra:implements audit.write/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Write(context.Context, writev1.Request) (writev1.Response, error) {
	return writev1.Response{}, nil
}
`)
	writeGenerationGraphInterface(t, root, "unused/local/v1", "localv1", "unused.local/v1", "Run")
	writeFile(t, filepath.Join(root, "localcandidate", "service.go"), `package localcandidate

import (
	"context"

	localv1 "example.com/acme/interface-proxy-app/interfaces/unused/local/v1"
)

type Config struct {
	Label string
}

type Service struct{}

//plystra:implements unused.local/v1
func New(Config) (*Service, error) { return &Service{}, nil }

func (*Service) Start(context.Context) error { return nil }
func (*Service) Stop(context.Context) error  { return nil }

func (*Service) Run(context.Context, localv1.Request) (localv1.Response, error) {
	return localv1.Response{}, nil
}
`)
	dependencyRoot := filepath.Join(t.TempDir(), "unused-dependency")
	writeModule(t, dependencyRoot, "example.com/platform/unused", "")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
	writeGenerationGraphInterface(t, dependencyRoot, "unused/notify/v1", "notifyv1", "unused.notify/v1", "Notify")
	writeFile(t, filepath.Join(dependencyRoot, "unused", "service.go"), `package unused

import (
	"context"

	notifyv1 "example.com/platform/unused/interfaces/unused/notify/v1"
)

type Service struct{}

//plystra:implements unused.notify/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Notify(context.Context, notifyv1.Request) (notifyv1.Response, error) {
	return notifyv1.Response{}, nil
}
`)
	goModPath := filepath.Join(root, "go.mod")
	writeFile(t, goModPath, string(readAbsoluteFile(t, goModPath))+fmt.Sprintf(`
require example.com/platform/unused v1.0.0

replace example.com/platform/unused => %s
`, filepath.ToSlash(dependencyRoot)))

	options := applicationgenerate.Options{Start: root, Environment: goEnvironment(nil)}
	generated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !generated.Report().Clean() {
		t.Fatalf("Generate = %#v, %v", generated.Report().Changes(), err)
	}
	appProxy := "generated/go/proxies/app/run/v1/proxy_gen.go"
	auditProxy := "generated/go/proxies/audit/write/v1/proxy_gen.go"
	unusedDependencyProxy := "generated/go/proxies/unused/notify/v1/proxy_gen.go"
	unusedLocalProxy := "generated/go/proxies/unused/local/v1/proxy_gen.go"
	for _, path := range []string{appProxy, auditProxy} {
		assertFileExists(t, root, path)
	}
	for _, path := range []string{
		unusedDependencyProxy,
		unusedLocalProxy,
		"generated/go/adapters/implementations/unused/local/v1/adapter_gen.go",
	} {
		assertFileMissing(t, root, path)
	}
	const unusedLocalConstructor = "example.com/acme/interface-proxy-app/localcandidate.New"
	for _, entry := range snapshotGenerated(t, root) {
		if bytes.Contains(entry.data, []byte(unusedLocalConstructor)) {
			t.Fatalf("unrequired local candidate entered generated proxy, adapter, assembly, bootstrap, configuration, lifecycle, or provenance at %s", entry.path)
		}
	}
	appSource := readFile(t, root, appProxy)
	for _, required := range [][]byte{
		[]byte(`contract "example.com/acme/interface-proxy-app/interfaces/app/run/v1"`),
		[]byte(`var _ contract.Interface = Proxy{}`),
		[]byte(`return proxy.handle.Invoke(ctx, request)`),
	} {
		if !bytes.Contains(appSource, required) {
			t.Fatalf("generated app proxy omits %q:\n%s", required, appSource)
		}
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

	proxyPath := filepath.Join(root, filepath.FromSlash(appProxy))
	if err := os.WriteFile(proxyPath, []byte("manual proxy drift\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", proxyPath, err)
	}
	changedBefore := snapshotTree(t, root)
	changed, err := applicationgenerate.Generate(t.Context(), check)
	if err != nil || !reflect.DeepEqual(changed.Report().ManuallyModified(), []string{appProxy}) {
		t.Fatalf("changed proxy check = %#v, %v", changed.Report().Changes(), err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, changedBefore) {
		t.Fatal("changed proxy check mutated the Project")
	}

	if err := os.Remove(proxyPath); err != nil {
		t.Fatalf("Remove(%s): %v", proxyPath, err)
	}
	missingBefore := snapshotTree(t, root)
	missing, err := applicationgenerate.Generate(t.Context(), check)
	if err != nil || !reflect.DeepEqual(missing.Report().Missing(), []string{appProxy}) {
		t.Fatalf("missing proxy check = %#v, %v", missing.Report().Changes(), err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, missingBefore) {
		t.Fatal("missing proxy check mutated the Project")
	}

	repaired, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !repaired.Report().Clean() {
		t.Fatalf("repair proxy = %#v, %v", repaired.Report().Changes(), err)
	}
	if got := readFile(t, root, appProxy); !bytes.Equal(got, appSource) {
		t.Fatalf("repaired proxy differs:\nwant:\n%s\ngot:\n%s", appSource, got)
	}
}

func TestGenerateTypedInterfaceOutputIsStableAcrossAuthoredFilesystemOrder(t *testing.T) {
	t.Parallel()

	type result struct {
		generated []treeEntry
		digest    string
	}
	generate := func(order []string) result {
		root := t.TempDir()
		writeApplicationModule(t, root, "example.com/acme/interface-proxy-order")
		writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [alpha.run/v1, zeta.run/v1]}\n")
		for _, name := range order {
			writeGenerationGraphInterface(t, root, name+"/run/v1", name+"v1", name+".run/v1", "Run")
			writeFile(t, filepath.Join(root, name, "service.go"), fmt.Sprintf(`package %s

import (
	"context"

	contract "example.com/acme/interface-proxy-order/interfaces/%s/run/v1"
)

type Service struct{}

//plystra:implements %s.run/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Run(context.Context, contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`, name, name, name))
		}
		generated, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
			Start:       root,
			Environment: goEnvironment(nil),
		})
		if err != nil || !generated.Report().Clean() {
			t.Fatalf("Generate(%v) = %#v, %v", order, generated.Report().Changes(), err)
		}
		provenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
		if err != nil {
			t.Fatalf("DecodeManifestProvenance(%v): %v", order, err)
		}
		return result{generated: snapshotGenerated(t, root), digest: provenance.ApplicationModelDigest()}
	}

	forward := generate([]string{"alpha", "zeta"})
	reverse := generate([]string{"zeta", "alpha"})
	if !reflect.DeepEqual(forward.generated, reverse.generated) {
		t.Fatalf("authored filesystem order changed generated output:\nforward: %#v\nreverse: %#v", forward.generated, reverse.generated)
	}
	if forward.digest != reverse.digest {
		t.Fatalf("authored filesystem order changed application-model digest: %q != %q", forward.digest, reverse.digest)
	}
}
