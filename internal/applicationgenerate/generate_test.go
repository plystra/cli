package applicationgenerate_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/pluginmeta"
)

func TestGenerateChecksAndInstallsEmptyApplicationWithoutJavaScriptIdentity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/Acme/empty")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "timeouts:\n  startup: 17s\n")
	environment := goEnvironment(nil)
	before := snapshotTree(t, root)

	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Generate check: %v", err)
	}
	if !checked.Checked() || checked.Module().Path() != root || checked.Module().ModulePath() != "example.com/Acme/empty" {
		t.Fatalf("checked result = %#v", checked)
	}
	if got, want := checked.Report().Missing(), []string{generatedfiles.ManifestPath, "generated/go/assembly/compatibility_gen.go", "generated/go/assembly/invocations_gen.go", "generated/go/assembly/providers_gen.go", "generated/go/bootstrap/bootstrap_gen.go", "generated/manifest.json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missing files = %v, want %v", got, want)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("check mode mutated application:\nbefore: %#v\nafter:  %#v", before, after)
	}

	installed, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
		Validate:    func(_ context.Context, _ string) error { return nil },
	})
	if err != nil {
		t.Fatalf("Generate install: %v", err)
	}
	if installed.Checked() || !installed.Report().Clean() {
		t.Fatalf("installed result = checked %t, changes %#v", installed.Checked(), installed.Report().Changes())
	}
	applicationManifest := readFile(t, root, "generated/manifest.json")
	for _, required := range []string{
		`"capability_aliases":[]`,
		`"configuration":{"version":1,"mode":"default"`,
		`"root":{"path":"plystra.yaml"}`,
		`"dependency_composition_digest":"sha256:`,
		`"dependency_baseline":[]`,
	} {
		if !bytes.Contains(applicationManifest, []byte(required)) {
			t.Fatalf("generated application manifest omits %q: %s", required, applicationManifest)
		}
	}
	assertFileExists(t, root, generatedfiles.ManifestPath)
	assertFileExists(t, root, "generated/go/assembly/compatibility_gen.go")
	assertFileExists(t, root, "generated/go/assembly/invocations_gen.go")
	assertFileExists(t, root, "generated/go/assembly/providers_gen.go")
	assertFileExists(t, root, "generated/go/bootstrap/bootstrap_gen.go")
	if bootstrap := readFile(t, root, "generated/go/bootstrap/bootstrap_gen.go"); bytes.Contains(bootstrap, []byte("17s")) {
		t.Fatalf("generated bootstrap embeds application-specific startup timeout:\n%s", bootstrap)
	}
	assertFileMissing(t, root, "generated/sdk/javascript/package.json")

	writeFile(t, filepath.Join(root, "generated", "manifest.json"), "drift\n")
	driftedBefore := snapshotTree(t, root)
	drifted, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: environment,
	})
	if err != nil || !reflect.DeepEqual(drifted.Report().Changed(), []string{"generated/manifest.json"}) {
		t.Fatalf("drift check = %#v, %v", drifted.Report().Changes(), err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, driftedBefore) {
		t.Fatalf("drift check mutated application:\nbefore: %#v\nafter:  %#v", driftedBefore, after)
	}
	if repaired, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, Validate: func(_ context.Context, _ string) error { return nil }}); err != nil || !repaired.Report().Clean() {
		t.Fatalf("repair drift = %#v, %v", repaired.Report().Changes(), err)
	}

	clean, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: environment,
	})
	if err != nil || !clean.Report().Clean() {
		t.Fatalf("clean check = %#v, %v", clean.Report().Changes(), err)
	}
}

func TestGenerateChecksAndRepairsDependencyCompositionDriftTransactionally(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "platform")
	writeModule(t, dependencyRoot, "example.com/platform", "")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities:\n  require: [kernel.health/v1]\n")
	writeApplicationModule(t, appRoot, "example.com/acme/composed")
	goModPath := filepath.Join(appRoot, "go.mod")
	goMod := string(readAbsoluteFile(t, goModPath)) + fmt.Sprintf("\nrequire example.com/platform v1.0.0\n\nreplace example.com/platform => %s\n", filepath.ToSlash(dependencyRoot))
	writeFile(t, goModPath, goMod)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "# shared application settings\nhttp:\n  address: \":8080\" # keep process comment\ncapabilities:\n  require: []\n")
	environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})
	noValidation := func(_ context.Context, _ string) error { return nil }

	initial, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       appRoot,
		Environment: environment,
		Validate:    noValidation,
	})
	if err != nil || !initial.ConfigurationChanged() || !initial.Report().Clean() {
		t.Fatalf("initial Generate = changed %t, report %#v, %v", initial.ConfigurationChanged(), initial.Report().Changes(), err)
	}
	initialManifest := readFile(t, appRoot, "plystra.yaml")
	for _, required := range [][]byte{[]byte("kernel.health/v1"), []byte("# shared application settings"), []byte("# keep process comment")} {
		if !bytes.Contains(initialManifest, required) {
			t.Fatalf("initial maintained manifest omits %q:\n%s", required, initialManifest)
		}
	}
	beforeDrift := snapshotTree(t, appRoot)
	generatedBefore := snapshotGenerated(t, appRoot)

	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities:\n  require: [kernel.info/v1]\n")
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       appRoot,
		Check:       true,
		Environment: environment,
	})
	if err != nil || !checked.Checked() || !checked.ConfigurationChanged() {
		t.Fatalf("drift check = checked %t, configuration changed %t, report %#v, %v", checked.Checked(), checked.ConfigurationChanged(), checked.Report().Changes(), err)
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, beforeDrift) {
		t.Fatalf("dependency-composition check mutated application:\nbefore: %#v\nafter:  %#v", beforeDrift, after)
	}

	validationFailure := errors.New("reject recomposed application")
	_, err = applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       appRoot,
		Environment: environment,
		Validate: func(_ context.Context, _ string) error {
			return validationFailure
		},
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, validationFailure) {
		t.Fatalf("recomposition validation failure = %v", err)
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, beforeDrift) {
		t.Fatalf("failed recomposition changed application:\nbefore: %#v\nafter:  %#v", beforeDrift, after)
	}

	concurrentManifest := append(append([]byte(nil), initialManifest...), []byte("# concurrent user comment\n")...)
	_, err = applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       appRoot,
		Environment: environment,
		Validate: func(_ context.Context, _ string) error {
			writeFile(t, filepath.Join(appRoot, "plystra.yaml"), string(concurrentManifest))
			return nil
		},
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, applicationgenerate.ErrConcurrentChange) {
		t.Fatalf("concurrent recomposition edit = %v", err)
	}
	if current := readFile(t, appRoot, "plystra.yaml"); !bytes.Equal(current, concurrentManifest) {
		t.Fatalf("concurrent manifest edit was overwritten:\n%s", current)
	}
	if after := snapshotGenerated(t, appRoot); !reflect.DeepEqual(after, generatedBefore) {
		t.Fatalf("generated rollback after concurrent configuration edit:\nbefore: %#v\nafter:  %#v", generatedBefore, after)
	}
	cleanupRecoveryTransactions(t, appRoot)

	installed, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       appRoot,
		Environment: environment,
		Validate:    noValidation,
	})
	if err != nil || !installed.ConfigurationChanged() || !installed.Report().Clean() {
		t.Fatalf("install recomposition = changed %t, report %#v, %v", installed.ConfigurationChanged(), installed.Report().Changes(), err)
	}
	updated := readFile(t, appRoot, "plystra.yaml")
	for _, required := range [][]byte{[]byte("kernel.info/v1"), []byte("# shared application settings"), []byte("# keep process comment"), []byte("# concurrent user comment")} {
		if !bytes.Contains(updated, required) {
			t.Fatalf("updated manifest omits %q:\n%s", required, updated)
		}
	}
	if bytes.Contains(updated, []byte("kernel.health/v1")) {
		t.Fatalf("updated manifest retained disappeared dependency requirement:\n%s", updated)
	}
	clean, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: appRoot, Check: true, Environment: environment})
	if err != nil || clean.ConfigurationChanged() || !clean.Report().Clean() {
		t.Fatalf("clean composed check = changed %t, report %#v, %v", clean.ConfigurationChanged(), clean.Report().Changes(), err)
	}
	assertNoTransactions(t, appRoot)
}

func TestGenerateRejectsOrdinaryGoModuleWithoutProjectMarker(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/acme/ordinary")
	writePlugin(t, root, "business", "id: acme.ordinary.business\n")
	before := snapshotTree(t, root)
	_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       filepath.Join(root, "business"),
		Environment: goEnvironment(nil),
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !strings.Contains(err.Error(), "has no root plystra.yaml") {
		t.Fatalf("Generate ordinary module error = %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("ordinary module generation mutated files:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestGenerateWiresLocalPluginRequirementsThroughPublicRuntime(t *testing.T) {
	const modulePath = "example.com/acme/wired-application"
	root := t.TempDir()
	writeApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [order.place/v1]\n")
	writePlugin(t, root, "catalog", "id: acme.catalog\nprovides: [catalog.lookup/v1]\n")
	writeCapability(t, root, "catalog", "catalog.lookup/v1", `id: catalog.lookup/v1
request:
  key: {type: string, required: true}
response:
  value: {type: string, required: true}
errors: []
`)
	writePlugin(t, root, "orders", "id: acme.orders\nprovides: [order.place/v1]\nrequires: [catalog.lookup/v1]\n")
	writeCapability(t, root, "orders", "order.place/v1", `id: order.place/v1
request:
  key: {type: string, required: true}
response:
  value: {type: string, required: true}
errors: []
`)
	writeFile(t, filepath.Join(root, "catalog", "plugin.go"), `package catalog

import (
	"context"

	configuration "example.com/acme/wired-application/generated/go/configuration"
	contract "example.com/acme/wired-application/generated/go/contracts/catalog/lookup/v1"
)

type Config = configuration.CatalogConfig
type Plugin struct{}

func New(Config) *Plugin { return &Plugin{} }

func (*Plugin) Lookup(_ context.Context, request contract.Request) (contract.Response, error) {
	return contract.Response{Value: "catalog:" + request.Key}, nil
}
`)
	writeFile(t, filepath.Join(root, "orders", "plugin.go"), `package orders

import (
	"context"

	lookupcontract "example.com/acme/wired-application/generated/go/contracts/catalog/lookup/v1"
	ordercontract "example.com/acme/wired-application/generated/go/contracts/order/place/v1"
	configuration "example.com/acme/wired-application/generated/go/configuration"
	dependencies "example.com/acme/wired-application/generated/go/dependencies/orders"
)

type Config = configuration.OrdersConfig
type Plugin struct{ clients dependencies.Dependencies }

func New(_ Config, clients dependencies.Dependencies) *Plugin {
	return &Plugin{clients: clients}
}

func (p *Plugin) Place(ctx context.Context, request ordercontract.Request) (ordercontract.Response, error) {
	response, err := p.clients.CatalogLookupV1().Lookup(ctx, lookupcontract.Request{Key: request.Key})
	if err != nil {
		return ordercontract.Response{}, err
	}
	return ordercontract.Response{Value: "order:" + response.Value}, nil
}
`)
	environment := goEnvironment(nil)
	result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
	})
	if err != nil || !result.Report().Clean() {
		t.Fatalf("Generate wired application = %#v, %v", result.Report().Changes(), err)
	}
	for _, filePath := range []string{
		"generated/go/clients/catalog/lookup/v1/client_gen.go",
		"generated/go/dependencies/orders/dependencies_gen.go",
		"generated/go/invocation/catalog/lookup/v1/invocation_gen.go",
		"generated/go/invocation/order/place/v1/invocation_gen.go",
	} {
		assertFileExists(t, root, filePath)
	}
	writeFile(t, filepath.Join(root, "wiring_runtime_test.go"), wiredApplicationRuntimeTest)
	command := exec.CommandContext(t.Context(), "go", "test", "-mod=readonly", "-count=1", "./...")
	command.Dir = root
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated wired application runtime: %v\n%s", err, output)
	}
	if clean, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Check: true, Environment: environment}); err != nil || !clean.Report().Clean() {
		t.Fatalf("wired application Generate --check = %#v, %v", clean.Report().Changes(), err)
	}
}

func TestGenerateRequiresDirectKernelDependency(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/acme/missing-kernel", "")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: goEnvironment(nil),
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, applicationgenerate.ErrKernelDependency) || !strings.Contains(err.Error(), "go.mod must directly require github.com/plystra/kernel") {
		t.Fatalf("Generate without Kernel dependency = %v", err)
	}
}

func TestGenerateCompilesUnrequiredLocalCapabilityWithoutRuntimeAssembly(t *testing.T) {
	const modulePath = "example.com/acme/unrequired-capability"
	root := t.TempDir()
	writeApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writePlugin(t, root, "business", "id: acme.business\nprovides: [email.send/v1]\n")
	writeCapability(t, root, "business", "email.send/v1", `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`)
	writeFile(t, filepath.Join(root, "business", "plugin.go"), `package business

import (
	"context"

	configuration "example.com/acme/unrequired-capability/generated/go/configuration"
	contract "example.com/acme/unrequired-capability/generated/go/contracts/email/send/v1"
)

type Config = configuration.BusinessConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Send(_ context.Context, request contract.Request) (contract.Response, error) {
	if request.To == "" {
		return contract.Response{}, contract.ErrInvalidRecipient
	}
	return contract.Response{Accepted: true}, nil
}
`)
	writeFile(t, filepath.Join(root, "unrequired_runtime_test.go"), `package unrequiredapp_test

import (
	"context"
	"testing"

	bootstrap "example.com/acme/unrequired-capability/generated/go/bootstrap"
)

func TestUnrequiredCapabilityIsNotRegistered(t *testing.T) {
	application, err := bootstrap.New(context.Background(), "plystra.yaml")
	if err != nil || !application.Valid() {
		t.Fatalf("bootstrap.New = %#v, %v", application, err)
	}
	bindings := application.Invocations().Catalog().Bindings()
	if len(bindings) != 2 || bindings[0].Capability().String() != "kernel.health/v1" || bindings[1].Capability().String() != "kernel.info/v1" {
		t.Fatalf("runtime catalog bindings = %#v, want only intrinsic Kernel Capabilities", bindings)
	}
}
`)
	environment := goEnvironment(nil)
	result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       filepath.Join(root, "business"),
		Environment: environment,
	})
	if err != nil || !result.Report().Clean() {
		t.Fatalf("Generate unrequired local Capability = %#v, %v", result.Report().Changes(), err)
	}
	for _, filePath := range []string{
		"generated/go/configuration/business_gen.go",
		"generated/go/contracts/email/send/v1/contract_gen.go",
		"generated/go/providers/email/send/v1/provider_gen.go",
	} {
		assertFileExists(t, root, filePath)
	}
	for _, filePath := range []string{
		"generated/docs/api.md",
		"generated/go/adapters/http/email/send/v1/handler_gen.go",
		"generated/go/clients/email/send/v1/client_gen.go",
		"generated/go/internal/invocationcontext/context_gen.go",
		"generated/go/invocation/email/send/v1/invocation_gen.go",
		"generated/sdk/javascript/package.json",
	} {
		assertFileMissing(t, root, filePath)
	}
	if assembly := readFile(t, root, "generated/go/assembly/invocations_gen.go"); bytes.Contains(assembly, []byte("email.send/v1")) {
		t.Fatalf("unrequired local Capability entered runtime assembly:\n%s", assembly)
	}
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: environment,
	})
	if err != nil || !checked.Report().Clean() {
		t.Fatalf("Generate --check = %#v, %v", checked.Report().Changes(), err)
	}
}

func TestGenerateRunsIntrinsicApplicationWithoutOrdinaryPlugins(t *testing.T) {
	const modulePath = "example.com/acme/intrinsic-app"
	root := t.TempDir()
	writeApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  expose: [kernel.health/v1]
capabilities:
  require: [kernel.info/v1]
  aliases:
    health.status/v1: kernel.health/v1
`)
	environment := goEnvironment(nil)
	result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
	})
	if err != nil || !result.Report().Clean() {
		t.Fatalf("Generate intrinsic application = %#v, %v", result.Report().Changes(), err)
	}

	for _, filePath := range []string{
		"generated/go/adapters/http/kernel/health/v1/handler_gen.go",
		"generated/go/adapters/http/health/status/v1/handler_gen.go",
		"generated/go/clients/kernel/health/v1/client_gen.go",
		"generated/go/clients/kernel/info/v1/client_gen.go",
		"generated/go/contracts/kernel/health/v1/contract_gen.go",
		"generated/go/contracts/kernel/info/v1/contract_gen.go",
		"generated/go/invocation/kernel/health/v1/invocation_gen.go",
		"generated/go/invocation/kernel/info/v1/invocation_gen.go",
		"generated/sdk/javascript/src/operations/kernel/health/v1.ts",
	} {
		assertFileExists(t, root, filePath)
	}
	for _, filePath := range []string{
		"generated/go/adapters/http/kernel/info/v1/handler_gen.go",
		"generated/sdk/javascript/src/operations/kernel/info/v1.ts",
	} {
		assertFileMissing(t, root, filePath)
	}

	healthContract := readFile(t, root, "generated/go/contracts/kernel/health/v1/contract_gen.go")
	for _, required := range [][]byte{
		[]byte("type Request = kernelintrinsic.HealthRequest"),
		[]byte("type Response = kernelintrinsic.HealthResponse"),
		[]byte("type ResponseStatus = kernelintrinsic.HealthStatus"),
	} {
		if !bytes.Contains(healthContract, required) {
			t.Fatalf("generated health contract omits %q:\n%s", required, healthContract)
		}
	}
	assembly := readFile(t, root, "generated/go/assembly/invocations_gen.go")
	for _, required := range [][]byte{
		[]byte("kernelintrinsic.NewBindings"),
		[]byte("kernelintrinsic.HealthContract()"),
		[]byte("kernelintrinsic.InfoContract()"),
	} {
		if !bytes.Contains(assembly, required) {
			t.Fatalf("generated intrinsic assembly omits %q:\n%s", required, assembly)
		}
	}

	writeFile(t, filepath.Join(root, "intrinsic_runtime_test.go"), intrinsicApplicationRuntimeTest)
	command := exec.CommandContext(t.Context(), "go", "test", "-mod=readonly", "-count=1", "./...")
	command.Dir = root
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated intrinsic application runtime: %v\n%s", err, output)
	}
}

func TestGenerateRendersValidatesAndCleansCanonicalAliasSurfaces(t *testing.T) {
	root := t.TempDir()
	writeApplicationModule(t, root, "github.com/acme/my-app")
	writePlugin(t, root, "business", "id: acme.business\nprovides: [email.send/v1]\n")
	writeCapability(t, root, "business", "email.send/v1", `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`)
	writeFile(t, filepath.Join(root, "business", "plugin.go"), `package business

import (
	"context"

	configuration "github.com/acme/my-app/generated/go/configuration"
	contract "github.com/acme/my-app/generated/go/contracts/email/send/v1"
)

type Config = configuration.BusinessConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Send(_ context.Context, request contract.Request) (contract.Response, error) {
	if request.To == "" {
		return contract.Response{}, contract.ErrInvalidRecipient
	}
	return contract.Response{Accepted: true}, nil
}
`)
	withAlias := `http:
  expose: [email.send/v1]
capabilities:
  aliases:
    mail.deliver/v1:
      target: email.send/v1
      deprecated:
        message: Use email.send/v1 instead.
`
	writeFile(t, filepath.Join(root, "plystra.yaml"), withAlias)
	environment := goEnvironment(nil)

	result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       filepath.Join(root, "business"),
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !result.Report().Clean() {
		t.Fatalf("installed changes = %#v", result.Report().Changes())
	}
	for _, filePath := range []string{
		"generated/docs/api.md",
		"generated/docs/openapi.json",
		"generated/go/adapters/http/email/send/v1/handler_gen.go",
		"generated/go/adapters/http/mail/deliver/v1/handler_gen.go",
		"generated/go/assembly/compatibility_gen.go",
		"generated/go/assembly/invocations_gen.go",
		"generated/go/assembly/providers_gen.go",
		"generated/go/bootstrap/bootstrap_gen.go",
		"generated/go/clients/email/send/v1/client_gen.go",
		"generated/go/clients/mail/deliver/v1/client_gen.go",
		"generated/go/contracts/email/send/v1/contract_gen.go",
		"generated/go/configuration/business_gen.go",
		"generated/go/invocation/email/send/v1/invocation_gen.go",
		"generated/go/providers/email/send/v1/provider_gen.go",
		"generated/manifest.json",
		"generated/sdk/javascript/package.json",
		"generated/sdk/javascript/src/operations/email/send/v1.ts",
		"generated/sdk/javascript/src/operations/mail/deliver/v1.ts",
		generatedfiles.ManifestPath,
	} {
		assertFileExists(t, root, filePath)
	}
	packageJSON := readFile(t, root, "generated/sdk/javascript/package.json")
	if !bytes.Contains(packageJSON, []byte(`"name": "@acme/my-app-sdk"`)) {
		t.Fatalf("package.json has wrong inferred identity:\n%s", packageJSON)
	}
	manifest := readFile(t, root, "generated/manifest.json")
	for _, value := range [][]byte{[]byte(`"id":"mail.deliver/v1"`), []byte(`"target":"email.send/v1"`), []byte(`"deprecated":"Use email.send/v1 instead."`)} {
		if !bytes.Contains(manifest, value) {
			t.Fatalf("manifest omits %s:\n%s", value, manifest)
		}
	}

	writeFile(t, filepath.Join(root, "plystra.yaml"), "http:\n  expose: [email.send/v1]\n")
	withoutAlias, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
	})
	if err != nil || !withoutAlias.Report().Clean() {
		t.Fatalf("Generate without Alias = %#v, %v", withoutAlias.Report().Changes(), err)
	}
	for _, obsolete := range []string{
		"generated/go/adapters/http/mail/deliver/v1/handler_gen.go",
		"generated/go/clients/mail/deliver/v1/client_gen.go",
		"generated/sdk/javascript/src/operations/mail/deliver/v1.ts",
	} {
		assertFileMissing(t, root, obsolete)
	}
	assertFileExists(t, root, "generated/go/clients/email/send/v1/client_gen.go")
	clean, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Check: true, Environment: environment})
	if err != nil || !clean.Report().Clean() {
		t.Fatalf("final check = %#v, %v", clean.Report().Changes(), err)
	}
}

func TestGenerateRollsBackValidationFailureAndPreservesConcurrentSourceEdit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/acme/rollback-app")
	writePlugin(t, root, "business", "id: acme.business\nprovides: [email.send/v1]\n")
	writeCapability(t, root, "business", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	withoutAlias := "capabilities:\n  require: [email.send/v1]\n"
	withAlias := withoutAlias + "  aliases:\n    mail.deliver/v1: email.send/v1\n"
	manifestPath := filepath.Join(root, "plystra.yaml")
	writeFile(t, manifestPath, withoutAlias)
	environment := goEnvironment(nil)
	noValidation := func(_ context.Context, _ string) error { return nil }
	if result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, Validate: noValidation}); err != nil || !result.Report().Clean() {
		t.Fatalf("initial Generate = %#v, %v", result.Report().Changes(), err)
	}
	generatedBefore := snapshotGenerated(t, root)

	writeFile(t, manifestPath, withAlias)
	validationFailure := errors.New("validation rejected generated tree")
	_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
		Validate: func(_ context.Context, _ string) error {
			return validationFailure
		},
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, validationFailure) {
		t.Fatalf("validation failure = %v", err)
	}
	if after := snapshotGenerated(t, root); !reflect.DeepEqual(after, generatedBefore) {
		t.Fatalf("generated tree changed after validation rollback:\nbefore: %#v\nafter:  %#v", generatedBefore, after)
	}

	_, err = applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
		Validate: func(_ context.Context, _ string) error {
			writeFile(t, manifestPath, withoutAlias)
			return nil
		},
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, applicationgenerate.ErrConcurrentChange) {
		t.Fatalf("concurrent source edit = %v", err)
	}
	if got := string(readAbsoluteFile(t, manifestPath)); got != withoutAlias {
		t.Fatalf("concurrent manifest edit was not preserved: %q", got)
	}
	if after := snapshotGenerated(t, root); !reflect.DeepEqual(after, generatedBefore) {
		t.Fatalf("generated tree changed after concurrent-edit rollback:\nbefore: %#v\nafter:  %#v", generatedBefore, after)
	}
	assertNoTransactions(t, root)
}

func TestGenerateDetectsConcurrentPrivateConfigurationChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/acme/config-change-app")
	writePlugin(t, root, "business", "id: acme.business\nconfig: {label: {type: string}}\n")
	manifestPath := filepath.Join(root, "plystra.yaml")
	first := "config:\n  acme.business:\n    label: private-one\n"
	second := "config:\n  acme.business:\n    label: private-two\n"
	writeFile(t, manifestPath, first)
	environment := goEnvironment(nil)
	noValidation := func(_ context.Context, _ string) error { return nil }
	if result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, Validate: noValidation}); err != nil || !result.Report().Clean() {
		t.Fatalf("initial Generate = %#v, %v", result.Report().Changes(), err)
	}
	generatedBefore := snapshotGenerated(t, root)
	configurationSource := readFile(t, root, "generated/go/configuration/business_gen.go")
	for _, forbidden := range []string{"private-one", "private-two"} {
		if bytes.Contains(configurationSource, []byte(forbidden)) {
			t.Fatalf("generated configuration source exposed %q:\n%s", forbidden, configurationSource)
		}
	}

	_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
		Validate: func(_ context.Context, _ string) error {
			writeFile(t, manifestPath, second)
			return nil
		},
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, applicationgenerate.ErrConcurrentChange) {
		t.Fatalf("concurrent private configuration edit = %v", err)
	}
	if got := string(readAbsoluteFile(t, manifestPath)); got != second {
		t.Fatalf("concurrent private configuration edit was not preserved: %q", got)
	}
	if after := snapshotGenerated(t, root); !reflect.DeepEqual(after, generatedBefore) {
		t.Fatalf("generated tree changed after private configuration edit:\nbefore: %#v\nafter:  %#v", generatedBefore, after)
	}
	assertNoTransactions(t, root)
}

func TestGenerateExecutesRealSelectedExtensionAndCleansHelpers(t *testing.T) {
	root := t.TempDir()
	temporaryParent := t.TempDir()
	cliRoot := repositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/extension-app

go 1.26

require (
	github.com/plystra/cli v0.0.0
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/cli => %s

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(cliRoot), filepath.ToSlash(kernelRoot))
	writeFile(t, filepath.Join(root, "go.mod"), goMod)
	writeFile(t, filepath.Join(root, "go.sum"), string(readAbsoluteFile(t, filepath.Join(cliRoot, "go.sum"))))
	writeFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [order.create/v1]\n")
	writePlugin(t, root, "business", "id: example.business\nprovides: [order.create/v1]\n")
	writePlugin(t, root, "authn", `id: example.authn
provides: [authn.session.verify/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: authn
      capability: authn.session.verify/v1
`)
	writePlugin(t, root, "audit", "id: example.audit\nprovides: [audit.write/v1]\n")
	writeCapability(t, root, "business", "order.create/v1", `id: order.create/v1
request: {}
response: {}
errors: []
extensions:
  authn: {authenticated: true}
`)
	writeCapability(t, root, "authn", "authn.session.verify/v1", "id: authn.session.verify/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCapability(t, root, "audit", "audit.write/v1", "id: audit.write/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(root, "authn", "generation", "generate.go"), realExtensionSource)
	extensionBefore := readFile(t, root, "authn/generation/generate.go")

	result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:            root,
		Environment:      goEnvironment(map[string]string{"GOPROXY": "off"}),
		CompileTimeout:   2 * time.Minute,
		ExecutionTimeout: 10 * time.Second,
		TemporaryParent:  temporaryParent,
		Validate:         func(_ context.Context, _ string) error { return nil },
	})
	if err != nil || !result.Report().Clean() {
		t.Fatalf("Generate with extension = %#v, %v", result.Report().Changes(), err)
	}
	for _, capability := range []string{"order/create/v1", "authn/session/verify/v1", "audit/write/v1"} {
		assertFileExists(t, root, "generated/go/clients/"+capability+"/client_gen.go")
		assertFileExists(t, root, "generated/go/invocation/"+capability+"/invocation_gen.go")
	}
	entries, err := os.ReadDir(temporaryParent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary extension helpers = %v, %v", entries, err)
	}
	if after := readFile(t, root, "authn/generation/generate.go"); !bytes.Equal(after, extensionBefore) {
		t.Fatal("extension source changed during generation")
	}
}

func writeApplicationModule(t testing.TB, root, modulePath string) {
	t.Helper()
	cliRoot := repositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	extra := fmt.Sprintf(`require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeModule(t, root, modulePath, extra)
	writeFile(t, filepath.Join(root, "go.sum"), string(readAbsoluteFile(t, filepath.Join(cliRoot, "go.sum"))))
}

func writeModule(t testing.TB, root, modulePath, extra string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "go.mod"), "module "+modulePath+"\n\ngo 1.26\n\n"+extra)
}

func writePlugin(t testing.TB, root, name, manifest string) {
	t.Helper()
	writeFile(t, filepath.Join(root, name, "plugin.yaml"), manifest)
	metadata, err := pluginmeta.Parse([]byte(manifest))
	if err != nil {
		t.Fatalf("pluginmeta.Parse(%s): %v", name, err)
	}
	module, err := modulelocate.Find(root)
	if err != nil {
		t.Fatalf("modulelocate.Find(%s): %v", root, err)
	}
	names, err := configurationgen.DeriveGoNames(name)
	if err != nil {
		t.Fatalf("configurationgen.DeriveGoNames(%s): %v", name, err)
	}
	packageName := strings.ReplaceAll(name, "-", "")
	if token.Lookup(packageName).IsKeyword() {
		packageName += "plugin"
	}
	source, err := format.Source([]byte(fmt.Sprintf(`package %s

import configuration %q

type Config = configuration.%s
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }
`, packageName, module.ModulePath()+"/generated/go/configuration", names.TypeName())))
	if err != nil {
		t.Fatalf("format %s/plugin.go for %s: %v", name, metadata.ID(), err)
	}
	writeFile(t, filepath.Join(root, name, "plugin.go"), string(source))
}

func writeCapability(t testing.TB, root, plugin, value, source string) {
	t.Helper()
	identifier, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("capabilityid.Parse(%s): %v", value, err)
	}
	writeFile(t, filepath.Join(root, plugin, "capabilities", filepath.FromSlash(identifier.Name()), "v"+strconv.FormatUint(identifier.Major(), 10), "capability.yaml"), source)
}

func writeFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func readFile(t testing.TB, root, name string) []byte {
	t.Helper()
	return readAbsoluteFile(t, filepath.Join(root, filepath.FromSlash(name)))
}

func readAbsoluteFile(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	return data
}

func assertFileExists(t testing.TB, root, name string) {
	t.Helper()
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file: %v", name, err)
	}
}

func assertFileMissing(t testing.TB, root, name string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s exists: %v", name, err)
	}
}

type treeEntry struct {
	path string
	mode fs.FileMode
	data []byte
}

func snapshotTree(t testing.TB, root string) []treeEntry {
	t.Helper()
	return snapshotSubtree(t, root, ".")
}

func snapshotGenerated(t testing.TB, root string) []treeEntry {
	t.Helper()
	return snapshotSubtree(t, root, "generated")
}

func snapshotSubtree(t testing.TB, root, subtree string) []treeEntry {
	t.Helper()
	var result []treeEntry
	err := fs.WalkDir(os.DirFS(root), subtree, func(name string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		item := treeEntry{path: filepath.ToSlash(name), mode: info.Mode()}
		if info.Mode().IsRegular() {
			item.data, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
			if err != nil {
				return err
			}
		}
		result = append(result, item)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s): %v", subtree, err)
	}
	return result
}

func assertNoTransactions(t testing.TB, root string) {
	t.Helper()
	for _, pattern := range []string{".plystra-files-*", ".plystra-generation-*"} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil || len(matches) != 0 {
			t.Fatalf("transaction matches for %s = %v, %v", pattern, matches, err)
		}
	}
}

func cleanupRecoveryTransactions(t testing.TB, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".plystra-files-*"))
	if err != nil {
		t.Fatalf("glob recovery transactions: %v", err)
	}
	for _, match := range matches {
		if err := os.RemoveAll(match); err != nil {
			t.Fatalf("remove recovery transaction %s: %v", match, err)
		}
	}
}

func goEnvironment(overrides map[string]string) []string {
	values := map[string]string{
		"GOENV":       "off",
		"GOFLAGS":     "",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	}
	for key, value := range overrides {
		values[strings.ToUpper(key)] = value
	}
	environment := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := values[strings.ToUpper(key)]; !replaced {
			environment = append(environment, entry)
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment
}

func repositoryRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatalf("Abs(repository root): %v", err)
	}
	return root
}

const realExtensionSource = `package extension

import generation "github.com/plystra/cli/generation/v1"

func Generate(context generation.GenerationContext) (generation.Output, error) {
	order, _ := generation.ParseCapabilityID("order.create/v1")
	audit, _ := generation.ParseCapabilityID("audit.write/v1")
	if _, exists := context.Capability(order); !exists {
		return generation.Output{}, nil
	}
	return generation.Output{Requirements: []generation.Requirement{{
		RuleID: "authn.require-audit", Namespace: "authn", Source: order, Capability: audit,
	}}}, nil
}
`

const intrinsicApplicationRuntimeTest = `package intrinsicapp_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	bootstrap "example.com/acme/intrinsic-app/generated/go/bootstrap"
	healthcontract "example.com/acme/intrinsic-app/generated/go/contracts/kernel/health/v1"
	infocontract "example.com/acme/intrinsic-app/generated/go/contracts/kernel/info/v1"
	kernelintrinsic "github.com/plystra/kernel/intrinsic"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestIntrinsicApplicationRuntime(t *testing.T) {
	application, err := bootstrap.New(context.Background(), "plystra.yaml")
	if err != nil || !application.Valid() {
		t.Fatalf("bootstrap.New = %#v, %v", application, err)
	}
	invocations := application.Invocations()
	bindings := invocations.Catalog().Bindings()
	if len(bindings) != 2 || bindings[0].Capability().String() != "kernel.health/v1" || bindings[1].Capability().String() != "kernel.info/v1" {
		t.Fatalf("intrinsic catalog = %#v", bindings)
	}
	for _, binding := range bindings {
		build := binding.ProviderBuild()
		if binding.ProviderKind() != kernelinvocation.ProviderKindKernel ||
			binding.ProviderID().String() != "" ||
			binding.ProviderPackage() != kernelintrinsic.ProviderPackage ||
			binding.SelectionReason() != kernelinvocation.SelectionReasonIntrinsic ||
			build.ModulePath() != kernelintrinsic.ModulePath ||
			build.ModuleVersion() != "v0.0.0" ||
			build.BuildIdentity() == "" || binding.SchemaDigest() == [32]byte{} {
			t.Fatalf("intrinsic provenance for %s is incomplete", binding.Capability())
		}
	}

	health, err := invocations.KernelHealthV1().Invoke(context.Background(), healthcontract.Request{})
	if err != nil || health.Status != healthcontract.ResponseStatusHealthy {
		t.Fatalf("kernel.health/v1 = %#v, %v", health, err)
	}
	info, err := invocations.KernelInfoV1().Invoke(context.Background(), infocontract.Request{})
	if err != nil || info.AssemblyAPI != "v1" || info.KernelModule != kernelintrinsic.ModulePath || info.KernelVersion != "v0.0.0" {
		t.Fatalf("kernel.info/v1 = %#v, %v", info, err)
	}
	formatted := fmt.Sprintf("%+v", info)
	for _, forbidden := range []string{"sha256:", "plystra.yaml", "intrinsic-app", "secret"} {
		if strings.Contains(strings.ToLower(formatted), strings.ToLower(forbidden)) {
			t.Fatalf("kernel.info/v1 exposed %q in %s", forbidden, formatted)
		}
	}
}
`

const wiredApplicationRuntimeTest = `package wiredapplication_test

import (
	"context"
	"testing"

	bootstrap "example.com/acme/wired-application/generated/go/bootstrap"
	ordercontract "example.com/acme/wired-application/generated/go/contracts/order/place/v1"
)

func TestGeneratedCrossPluginCall(t *testing.T) {
	application, err := bootstrap.New(context.Background(), "plystra.yaml")
	if err != nil || !application.Valid() {
		t.Fatalf("bootstrap.New = %#v, %v", application, err)
	}
	invocations := application.Invocations()
	response, err := invocations.OrderPlaceV1().Invoke(context.Background(), ordercontract.Request{Key: "item"})
	if err != nil || response.Value != "order:catalog:item" {
		t.Fatalf("OrderPlaceV1.Invoke = %#v, %v", response, err)
	}
	bindings := invocations.Catalog().Bindings()
	if len(bindings) != 4 {
		t.Fatalf("catalog bindings = %#v", bindings)
	}
}
`
