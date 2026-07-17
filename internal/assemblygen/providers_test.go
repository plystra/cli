package assemblygen_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/bootstrapgen"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/kernel/plugin/manifest"
)

func TestRenderProvidersIsDeterministicSchemaOnlySource(t *testing.T) {
	t.Parallel()

	inputs := []assemblygen.ProviderInput{
		{PluginID: "zeta.remote-store", ModulePath: "example.com/dependency", ImportPath: "example.com/dependency/remote-store"},
		{PluginID: "acme.local-service", ModulePath: "example.com/application", ImportPath: "example.com/application/local-service"},
	}
	generated, err := assemblygen.RenderProviders(inputs)
	if err != nil {
		t.Fatalf("RenderProviders: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), assemblygen.ProvidersPath, generated, parser.AllErrors); err != nil {
		t.Fatalf("parse generated providers: %v\n%s", err, generated)
	}
	for _, required := range []string{
		"RequireKernelCompatibility()",
		"kernelconfiguration.ExtractObjectMap(document, \"config\")",
		"configuration0.DecodeLocalService",
		"configuration1.DecodeRemoteStore",
		"provider0.New(configuration)",
		"provider1.New(configuration)",
		"clear(configurationData0)",
		"ErrUnselectedPluginConfiguration",
		"NewProviderLifecycle",
		"kernellifecycle.NewBinding",
		"kernellifecycle.NewManager",
		"recover()",
		"type Providers struct",
	} {
		if !bytes.Contains(generated, []byte(required)) {
			t.Fatalf("generated source omits %q:\n%s", required, generated)
		}
	}
	for _, forbidden := range []string{
		"runtime-private-endpoint",
		"PLYSTRA_ASSEMBLY_PRIVATE_SECRET",
		"runtime-private-secret-value",
	} {
		if bytes.Contains(generated, []byte(forbidden)) {
			t.Fatalf("generated source contains runtime value %q:\n%s", forbidden, generated)
		}
	}
	if got := bytes.Count(generated, []byte(".New(configuration)")); got != len(inputs) {
		t.Fatalf("generated constructor call sites = %d, want %d\n%s", got, len(inputs), generated)
	}

	reversed := []assemblygen.ProviderInput{inputs[1], inputs[0]}
	repeated, err := assemblygen.RenderProviders(reversed)
	if err != nil || !bytes.Equal(generated, repeated) {
		t.Fatalf("reordered RenderProviders is not deterministic: %v", err)
	}

	sameModule, err := assemblygen.RenderProviders([]assemblygen.ProviderInput{
		{PluginID: "acme.alpha", ModulePath: "example.com/application", ImportPath: "example.com/application/alpha"},
		{PluginID: "acme.beta", ModulePath: "example.com/application", ImportPath: "example.com/application/beta"},
	})
	if err != nil {
		t.Fatalf("RenderProviders same module: %v", err)
	}
	if got := bytes.Count(sameModule, []byte(`"example.com/application/generated/go/configuration"`)); got != 1 {
		t.Fatalf("same-module configuration imports = %d, want 1\n%s", got, sameModule)
	}
}

func TestRenderProvidersRejectsInvalidOrDuplicateProvenance(t *testing.T) {
	t.Parallel()

	valid := assemblygen.ProviderInput{PluginID: "acme.valid", ModulePath: "example.com/application", ImportPath: "example.com/application/valid"}
	tests := []struct {
		name   string
		inputs []assemblygen.ProviderInput
		reason error
	}{
		{name: "invalid plugin ID", inputs: []assemblygen.ProviderInput{{PluginID: "Acme.Invalid", ModulePath: valid.ModulePath, ImportPath: valid.ImportPath}}, reason: assemblygen.ErrInvalidProvider},
		{name: "invalid module", inputs: []assemblygen.ProviderInput{{PluginID: valid.PluginID, ModulePath: "not a module path", ImportPath: valid.ImportPath}}, reason: assemblygen.ErrInvalidProvider},
		{name: "nested plugin", inputs: []assemblygen.ProviderInput{{PluginID: valid.PluginID, ModulePath: valid.ModulePath, ImportPath: valid.ModulePath + "/nested/valid"}}, reason: assemblygen.ErrInvalidProvider},
		{name: "invalid plugin directory", inputs: []assemblygen.ProviderInput{{PluginID: valid.PluginID, ModulePath: valid.ModulePath, ImportPath: valid.ModulePath + "/Invalid"}}, reason: assemblygen.ErrInvalidProvider},
		{name: "duplicate ID", inputs: []assemblygen.ProviderInput{valid, {PluginID: valid.PluginID, ModulePath: valid.ModulePath, ImportPath: valid.ModulePath + "/other"}}, reason: assemblygen.ErrDuplicateProvider},
		{name: "duplicate import", inputs: []assemblygen.ProviderInput{valid, {PluginID: "acme.other", ModulePath: valid.ModulePath, ImportPath: valid.ImportPath}}, reason: assemblygen.ErrDuplicateProvider},
		{name: "decoder collision", inputs: []assemblygen.ProviderInput{{PluginID: "acme.http", ModulePath: valid.ModulePath, ImportPath: valid.ModulePath + "/http"}, {PluginID: "acme.h-t-t-p", ModulePath: valid.ModulePath, ImportPath: valid.ModulePath + "/h-t-t-p"}}, reason: assemblygen.ErrDuplicateProvider},
		{name: "Kernel import collision", inputs: []assemblygen.ProviderInput{{PluginID: "acme.kernel-collision", ModulePath: "github.com/plystra/kernel", ImportPath: "github.com/plystra/kernel/configuration"}}, reason: assemblygen.ErrDuplicateProvider},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			generated, err := assemblygen.RenderProviders(test.inputs)
			if generated != nil || !errors.Is(err, assemblygen.ErrRenderProviders) || !errors.Is(err, test.reason) {
				t.Fatalf("RenderProviders = %q, %v", generated, err)
			}
		})
	}
}

func TestGeneratedBootstrapConstructsLocalAndDependencyPluginsSafely(t *testing.T) {
	root := t.TempDir()
	applicationRoot := filepath.Join(root, "application")
	dependencyRoot := filepath.Join(root, "dependency")
	cliRoot := repositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))

	writeFile(t, filepath.Join(applicationRoot, "go.mod"), fmt.Sprintf(`module example.com/assemblyapp

go 1.26

require (
	example.com/assemblydependency v0.0.0
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
)

replace example.com/assemblydependency => %s

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(dependencyRoot), filepath.ToSlash(kernelRoot)))
	writeFile(t, filepath.Join(dependencyRoot, "go.mod"), fmt.Sprintf(`module example.com/assemblydependency

go 1.26

require github.com/plystra/kernel v0.0.0

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeBytes(t, filepath.Join(applicationRoot, "go.sum"), goSum)

	localConfiguration := renderConfiguration(t, configurationgen.Input{
		PluginName: "local-service",
		PluginID:   "acme.local-service",
		Schema: parseConfig(t, `
mode: {type: string, default: ready, enum: [ready, panic, nil]}
label: {type: string, default: public-default-label}
`),
	})
	remoteConfiguration := renderConfiguration(t, configurationgen.Input{
		PluginName: "remote-store",
		PluginID:   "zeta.remote-store",
		Schema: parseConfig(t, `
endpoint: {type: string, required: true}
token: {type: secret, required: true}
startup: {type: string, default: ready, enum: [ready, wait]}
`),
	})
	writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(localConfiguration.Path())), localConfiguration.Data())
	writeBytes(t, filepath.Join(dependencyRoot, filepath.FromSlash(remoteConfiguration.Path())), remoteConfiguration.Data())
	writeFile(t, filepath.Join(applicationRoot, "local-service", "plugin.go"), localPluginSource)
	writeFile(t, filepath.Join(dependencyRoot, "remote-store", "plugin.go"), remotePluginSource)
	writeFile(t, filepath.Join(dependencyRoot, "lifecycleevents", "events.go"), lifecycleEventsSource)

	providers, err := assemblygen.RenderProviders([]assemblygen.ProviderInput{
		{PluginID: "zeta.remote-store", ModulePath: "example.com/assemblydependency", ImportPath: "example.com/assemblydependency/remote-store"},
		{PluginID: "acme.local-service", ModulePath: "example.com/assemblyapp", ImportPath: "example.com/assemblyapp/local-service"},
	})
	if err != nil {
		t.Fatalf("RenderProviders: %v", err)
	}
	for _, forbidden := range []string{"runtime-private-endpoint", "PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value"} {
		if bytes.Contains(providers, []byte(forbidden)) || bytes.Contains(localConfiguration.Data(), []byte(forbidden)) || bytes.Contains(remoteConfiguration.Data(), []byte(forbidden)) {
			t.Fatalf("generated source contains runtime value %q", forbidden)
		}
	}
	compatibility, err := assemblygen.RenderCompatibility("assembly")
	if err != nil {
		t.Fatalf("RenderCompatibility: %v", err)
	}
	bootstrap, err := bootstrapgen.Render(bootstrapgen.Options{
		ModulePath:            "example.com/assemblyapp",
		DefaultStartupTimeout: applicationmeta.DefaultStartupTimeout,
	})
	if err != nil {
		t.Fatalf("Render bootstrap: %v", err)
	}
	writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(assemblygen.ProvidersPath)), providers)
	writeBytes(t, filepath.Join(applicationRoot, "generated", "go", "assembly", "compatibility_gen.go"), compatibility)
	writeFile(t, filepath.Join(applicationRoot, "generated", "go", "assembly", "providers_gen_test.go"), generatedProvidersRuntimeTest)
	writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(bootstrapgen.Path)), bootstrap)
	writeFile(t, filepath.Join(applicationRoot, "generated", "go", "bootstrap", "bootstrap_gen_test.go"), generatedBootstrapRuntimeTest)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-mod=readonly", "-count=1", "./...")
	command.Dir = applicationRoot
	command.Env = isolatedGoEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated multi-module provider test: %v\n%s", err, output)
	}
}

func TestGeneratedBootstrapRunsWithoutSelectedProviders(t *testing.T) {
	applicationRoot := t.TempDir()
	cliRoot := repositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))

	writeFile(t, filepath.Join(applicationRoot, "go.mod"), fmt.Sprintf(`module example.com/emptyapp

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeBytes(t, filepath.Join(applicationRoot, "go.sum"), goSum)

	providers, err := assemblygen.RenderProviders(nil)
	if err != nil {
		t.Fatalf("RenderProviders: %v", err)
	}
	compatibility, err := assemblygen.RenderCompatibility("assembly")
	if err != nil {
		t.Fatalf("RenderCompatibility: %v", err)
	}
	bootstrap, err := bootstrapgen.Render(bootstrapgen.Options{
		ModulePath:            "example.com/emptyapp",
		DefaultStartupTimeout: applicationmeta.DefaultStartupTimeout,
	})
	if err != nil {
		t.Fatalf("Render bootstrap: %v", err)
	}
	writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(assemblygen.ProvidersPath)), providers)
	writeBytes(t, filepath.Join(applicationRoot, "generated", "go", "assembly", "compatibility_gen.go"), compatibility)
	writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(bootstrapgen.Path)), bootstrap)
	writeFile(t, filepath.Join(applicationRoot, "generated", "go", "bootstrap", "bootstrap_gen_test.go"), emptyGeneratedBootstrapRuntimeTest)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-mod=readonly", "-count=1", "./...")
	command.Dir = applicationRoot
	command.Env = isolatedGoEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated empty-application bootstrap test: %v\n%s", err, output)
	}
}

func renderConfiguration(t testing.TB, input configurationgen.Input) configurationgen.File {
	t.Helper()
	generated, err := configurationgen.Render(input)
	if err != nil {
		t.Fatalf("configurationgen.Render(%s): %v", input.PluginID, err)
	}
	return generated
}

func parseConfig(t testing.TB, source string) manifest.Config {
	t.Helper()
	schema, err := manifest.ParseConfig([]byte(source))
	if err != nil {
		t.Fatalf("manifest.ParseConfig: %v\n%s", err, source)
	}
	return schema
}

func writeFile(t testing.TB, name, data string) {
	t.Helper()
	writeBytes(t, name, []byte(data))
}

func writeBytes(t testing.TB, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
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

func isolatedGoEnvironment(environment []string) []string {
	removed := map[string]struct{}{
		"GOENV": {}, "GOFLAGS": {}, "GOPROXY": {}, "GOSUMDB": {}, "GOTOOLCHAIN": {}, "GOWORK": {},
	}
	result := make([]string, 0, len(environment)+6)
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if _, exists := removed[strings.ToUpper(key)]; !exists {
			result = append(result, entry)
		}
	}
	return append(result,
		"GOENV=off",
		"GOFLAGS=",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
	)
}

const localPluginSource = `package localservice

import (
	"context"
	"sync"

	"example.com/assemblydependency/lifecycleevents"
	configuration "example.com/assemblyapp/generated/go/configuration"
)

type Config = configuration.LocalServiceConfig
type Plugin struct{}

var state struct {
	sync.Mutex
	calls  int
	config Config
}

func New(config Config) *Plugin {
	state.Lock()
	state.calls++
	state.config = config
	mode := config.Mode
	state.Unlock()
	switch mode {
	case "panic":
		panic("runtime-private-constructor-payload")
	case "nil":
		return nil
	default:
		return &Plugin{}
	}
}

func (*Plugin) Start(context.Context) error {
	lifecycleevents.Add("acme.local-service.start")
	return nil
}

func (*Plugin) Stop(context.Context) error {
	lifecycleevents.Add("acme.local-service.stop")
	return nil
}

func Reset() {
	state.Lock()
	defer state.Unlock()
	state.calls = 0
	state.config = Config{}
}

func Snapshot() (int, Config) {
	state.Lock()
	defer state.Unlock()
	return state.calls, state.config
}
`

const remotePluginSource = `package remotestore

import (
	"context"
	"sync"

	configuration "example.com/assemblydependency/generated/go/configuration"
	"example.com/assemblydependency/lifecycleevents"
)

type Config = configuration.RemoteStoreConfig
type Plugin struct{ startup string }

var state struct {
	sync.Mutex
	calls  int
	config Config
}

func New(config Config) *Plugin {
	state.Lock()
	defer state.Unlock()
	state.calls++
	state.config = config
	return &Plugin{startup: config.Startup}
}

func (p *Plugin) Start(ctx context.Context) error {
	lifecycleevents.Add("zeta.remote-store.start")
	if p.startup == "wait" {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (*Plugin) Stop(context.Context) error {
	lifecycleevents.Add("zeta.remote-store.stop")
	return nil
}

func Reset() {
	state.Lock()
	defer state.Unlock()
	state.calls = 0
	state.config = Config{}
}

func Snapshot() (int, Config) {
	state.Lock()
	defer state.Unlock()
	return state.calls, state.config
}
`

const lifecycleEventsSource = `package lifecycleevents

import "sync"

var state struct {
	sync.Mutex
	events []string
}

func Add(event string) {
	state.Lock()
	defer state.Unlock()
	state.events = append(state.events, event)
}

func Reset() {
	state.Lock()
	defer state.Unlock()
	state.events = nil
}

func Snapshot() []string {
	state.Lock()
	defer state.Unlock()
	return append([]string(nil), state.events...)
}
`

const generatedProvidersRuntimeTest = `package assembly

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	localservice "example.com/assemblyapp/local-service"
	"example.com/assemblydependency/lifecycleevents"
	remotestore "example.com/assemblydependency/remote-store"
	kernelconfiguration "github.com/plystra/kernel/configuration"
)

const remoteConfiguration = "  zeta.remote-store:\n    endpoint: runtime-private-endpoint\n    token: {env: PLYSTRA_ASSEMBLY_PRIVATE_SECRET}\n"

func TestConstructProviders(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	localservice.Reset()
	remotestore.Reset()

	providers, err := NewProviders(context.Background(), newResolver(t), []byte("config:\n"+remoteConfiguration))
	if err != nil {
		t.Fatalf("NewProviders: %v", err)
	}
	if !providers.Valid() || providers.Len() != 2 {
		t.Fatalf("Providers validity = %t, len %d", providers.Valid(), providers.Len())
	}
	localCalls, localConfig := localservice.Snapshot()
	remoteCalls, remoteConfig := remotestore.Snapshot()
	if localCalls != 1 || localConfig.Mode != "ready" || localConfig.Label != "public-default-label" {
		t.Fatalf("local constructor = calls %d, config mode %q label %q", localCalls, localConfig.Mode, localConfig.Label)
	}
	if remoteCalls != 1 || remoteConfig.Endpoint != "runtime-private-endpoint" || string(remoteConfig.Token.Bytes()) != "runtime-private-secret-value" {
		t.Fatalf("remote constructor did not receive resolved configuration")
	}
	for _, formatted := range []string{fmt.Sprintf("%v", providers), fmt.Sprintf("%+v", providers), fmt.Sprintf("%#v", providers), fmt.Sprintf("%q", providers)} {
		if !strings.Contains(formatted, "redacted") || strings.Contains(formatted, "runtime-private") {
			t.Fatalf("provider formatting = %q", formatted)
		}
	}
	var logOutput bytes.Buffer
	slog.New(slog.NewJSONHandler(&logOutput, nil)).Info("providers", "value", providers)
	if !strings.Contains(logOutput.String(), "redacted") || strings.Contains(logOutput.String(), "runtime-private") {
		t.Fatalf("provider log = %s", logOutput.String())
	}
	if data, err := json.Marshal(providers); data != nil || !errors.Is(err, kernelconfiguration.ErrSecretExposure) {
		t.Fatalf("json.Marshal = %q, %v", data, err)
	}
	if data, err := providers.MarshalText(); data != nil || !errors.Is(err, kernelconfiguration.ErrSecretExposure) {
		t.Fatalf("MarshalText = %q, %v", data, err)
	}
	if data, err := providers.MarshalYAML(); data != nil || !errors.Is(err, kernelconfiguration.ErrSecretExposure) {
		t.Fatalf("MarshalYAML = %#v, %v", data, err)
	}
}

func TestLifecycleUsesDeterministicSelectedPluginOrder(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	providers, err := NewProviders(context.Background(), newResolver(t), []byte("config:\n"+remoteConfiguration))
	if err != nil {
		t.Fatalf("NewProviders: %v", err)
	}
	if manager, err := NewProviderLifecycle(Providers{}, time.Second); manager != nil || !errors.Is(err, ErrProviderLifecycle) {
		t.Fatalf("invalid NewProviderLifecycle = %#v, %v", manager, err)
	}
	manager, err := NewProviderLifecycle(providers, time.Second)
	if err != nil {
		t.Fatalf("NewProviderLifecycle: %v", err)
	}
	lifecycleevents.Reset()
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want := "acme.local-service.start,zeta.remote-store.start,zeta.remote-store.stop,acme.local-service.stop"
	if got := strings.Join(lifecycleevents.Snapshot(), ","); got != want {
		t.Fatalf("lifecycle order = %q, want %q", got, want)
	}
}

func TestRejectsUnselectedConfigurationBeforeConstructors(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	localservice.Reset()
	remotestore.Reset()
	document := []byte("config:\n  unknown.private:\n    token: runtime-private-unknown\n" + remoteConfiguration)
	providers, err := NewProviders(context.Background(), newResolver(t), document)
	if providers.Valid() || !errors.Is(err, ErrProviderAssembly) || !errors.Is(err, ErrUnselectedPluginConfiguration) {
		t.Fatalf("NewProviders = %v, %v", providers, err)
	}
	assertNoConstructorCalls(t)
	assertSafeError(t, err)
}

func TestConstructorPanicAndNilAreSafe(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	for _, mode := range []string{"panic", "nil"} {
		t.Run(mode, func(t *testing.T) {
			localservice.Reset()
			remotestore.Reset()
			document := []byte("config:\n  acme.local-service:\n    mode: " + mode + "\n" + remoteConfiguration)
			providers, err := NewProviders(context.Background(), newResolver(t), document)
			if providers.Valid() || !errors.Is(err, ErrProviderAssembly) || !errors.Is(err, ErrPluginConstructor) {
				t.Fatalf("NewProviders(%s) = %v, %v", mode, providers, err)
			}
			localCalls, _ := localservice.Snapshot()
			remoteCalls, _ := remotestore.Snapshot()
			if localCalls != 1 || remoteCalls != 0 {
				t.Fatalf("constructor calls for %s = local %d, remote %d", mode, localCalls, remoteCalls)
			}
			assertSafeError(t, err)
		})
	}
}

func TestConfigurationFailurePrecedesEveryConstructor(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	localservice.Reset()
	remotestore.Reset()
	document := []byte("config:\n  acme.local-service:\n    mode: runtime-private-invalid-enum\n" + remoteConfiguration)
	providers, err := NewProviders(context.Background(), newResolver(t), document)
	if providers.Valid() || !errors.Is(err, ErrProviderAssembly) {
		t.Fatalf("NewProviders = %v, %v", providers, err)
	}
	assertNoConstructorCalls(t)
	assertSafeError(t, err)
}

func newResolver(t *testing.T) *kernelconfiguration.Resolver {
	t.Helper()
	resolver, err := kernelconfiguration.NewResolver(kernelconfiguration.ResolverOptions{MaximumValueBytes: 1024})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return resolver
}

func assertNoConstructorCalls(t *testing.T) {
	t.Helper()
	localCalls, _ := localservice.Snapshot()
	remoteCalls, _ := remotestore.Snapshot()
	if localCalls != 0 || remoteCalls != 0 {
		t.Fatalf("constructors ran: local %d, remote %d", localCalls, remoteCalls)
	}
}

func assertSafeError(t *testing.T, err error) {
	t.Helper()
	for _, forbidden := range []string{
		"runtime-private-endpoint",
		"PLYSTRA_ASSEMBLY_PRIVATE_SECRET",
		"runtime-private-secret-value",
		"runtime-private-constructor-payload",
		"runtime-private-unknown",
		"runtime-private-invalid-enum",
	} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error exposed %q: %v", forbidden, err)
		}
	}
}
`

const generatedBootstrapRuntimeTest = `package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localservice "example.com/assemblyapp/local-service"
	"example.com/assemblydependency/lifecycleevents"
	remotestore "example.com/assemblydependency/remote-store"
	kernelconfiguration "github.com/plystra/kernel/configuration"
	kernellifecycle "github.com/plystra/kernel/lifecycle"
)

const bootstrapRemoteConfiguration = "  zeta.remote-store:\n    endpoint: runtime-private-endpoint\n    token: {env: PLYSTRA_ASSEMBLY_PRIVATE_SECRET}\n"

const validRuntimeDocument = "config:\n" + bootstrapRemoteConfiguration

func TestApplicationConstructsStartsAndStopsSelectedProviders(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	localservice.Reset()
	remotestore.Reset()

	application, err := New(context.Background(), writeRuntimeDocument(t, validRuntimeDocument))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !application.Valid() || application.State() != kernellifecycle.StateNew {
		t.Fatalf("application validity = %t, state %q", application.Valid(), application.State())
	}
	localCalls, localConfig := localservice.Snapshot()
	remoteCalls, remoteConfig := remotestore.Snapshot()
	if localCalls != 1 || localConfig.Mode != "ready" || localConfig.Label != "public-default-label" {
		t.Fatalf("local constructor = calls %d, config mode %q label %q", localCalls, localConfig.Mode, localConfig.Label)
	}
	if remoteCalls != 1 || remoteConfig.Endpoint != "runtime-private-endpoint" || string(remoteConfig.Token.Bytes()) != "runtime-private-secret-value" || remoteConfig.Startup != "ready" {
		t.Fatalf("remote constructor did not receive resolved configuration")
	}

	for _, formatted := range []string{fmt.Sprintf("%v", application), fmt.Sprintf("%+v", application), fmt.Sprintf("%#v", application), fmt.Sprintf("%q", application)} {
		if !strings.Contains(formatted, "redacted") || strings.Contains(formatted, "runtime-private") {
			t.Fatalf("application formatting = %q", formatted)
		}
	}
	var logOutput bytes.Buffer
	slog.New(slog.NewJSONHandler(&logOutput, nil)).Info("application", "value", application)
	if !strings.Contains(logOutput.String(), "redacted") || strings.Contains(logOutput.String(), "runtime-private") {
		t.Fatalf("application log = %s", logOutput.String())
	}
	if data, err := json.Marshal(application); data != nil || !errors.Is(err, kernelconfiguration.ErrSecretExposure) {
		t.Fatalf("json.Marshal = %q, %v", data, err)
	}
	if data, err := application.MarshalText(); data != nil || !errors.Is(err, kernelconfiguration.ErrSecretExposure) {
		t.Fatalf("MarshalText = %q, %v", data, err)
	}
	if data, err := application.MarshalYAML(); data != nil || !errors.Is(err, kernelconfiguration.ErrSecretExposure) {
		t.Fatalf("MarshalYAML = %#v, %v", data, err)
	}

	lifecycleevents.Reset()
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if application.State() != kernellifecycle.StateRunning {
		t.Fatalf("state after Start = %q", application.State())
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if application.State() != kernellifecycle.StateStopped {
		t.Fatalf("state after Stop = %q", application.State())
	}
	want := "acme.local-service.start,zeta.remote-store.start,zeta.remote-store.stop,acme.local-service.stop"
	if got := strings.Join(lifecycleevents.Snapshot(), ","); got != want {
		t.Fatalf("lifecycle order = %q, want %q", got, want)
	}
}

func TestApplicationRejectsInvalidSettingsBeforeConstructors(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	tests := map[string]string{
		"unknown":   "timeouts:\n  shutdown: 1s\n" + validRuntimeDocument,
		"zero":      "timeouts:\n  startup: 0s\n" + validRuntimeDocument,
		"non-string": "timeouts:\n  startup: 25\n" + validRuntimeDocument,
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			localservice.Reset()
			remotestore.Reset()
			application, err := New(context.Background(), writeRuntimeDocument(t, document))
			if application != nil || !errors.Is(err, ErrBootstrap) || !errors.Is(err, ErrRuntimeSettings) {
				t.Fatalf("New = %#v, %v", application, err)
			}
			assertNoBootstrapConstructorCalls(t)
			assertSafeBootstrapError(t, err)
		})
	}
}

func TestApplicationStartupTimeoutCancelsAndRollsBack(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	localservice.Reset()
	remotestore.Reset()
	lifecycleevents.Reset()
	document := "timeouts:\n  startup: 25ms\nconfig:\n  zeta.remote-store:\n    endpoint: runtime-private-endpoint\n    token: {env: PLYSTRA_ASSEMBLY_PRIVATE_SECRET}\n    startup: wait\n"
	application, err := New(context.Background(), writeRuntimeDocument(t, document))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := application.Start(context.Background()); !errors.Is(err, ErrApplicationStart) || !errors.Is(err, kernellifecycle.ErrStart) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start = %v", err)
	} else {
		assertSafeBootstrapError(t, err)
	}
	if application.State() != kernellifecycle.StateFailed {
		t.Fatalf("state after timeout = %q", application.State())
	}
	want := "acme.local-service.start,zeta.remote-store.start,zeta.remote-store.stop,acme.local-service.stop"
	if got := strings.Join(lifecycleevents.Snapshot(), ","); got != want {
		t.Fatalf("rollback order = %q, want %q", got, want)
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after rollback: %v", err)
	}
	if application.State() != kernellifecycle.StateStopped {
		t.Fatalf("state after Stop = %q", application.State())
	}
}

func TestApplicationRejectsMissingRuntimeDocument(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	localservice.Reset()
	remotestore.Reset()
	missing := filepath.Join(t.TempDir(), "runtime-private-missing-document")
	application, err := New(context.Background(), missing)
	if application != nil || !errors.Is(err, ErrBootstrap) || !errors.Is(err, kernelconfiguration.ErrLoadDocument) || !errors.Is(err, kernelconfiguration.ErrDocumentUnavailable) {
		t.Fatalf("New(missing) = %#v, %v", application, err)
	}
	assertNoBootstrapConstructorCalls(t)
	assertSafeBootstrapError(t, err)
}

func TestApplicationRejectsSymbolicRuntimeDocument(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	localservice.Reset()
	remotestore.Reset()
	root := t.TempDir()
	target := filepath.Join(root, "runtime-private-target")
	link := filepath.Join(root, "runtime-private-link")
	if err := os.WriteFile(target, []byte(validRuntimeDocument), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	application, err := New(context.Background(), link)
	if application != nil || !errors.Is(err, ErrBootstrap) || !errors.Is(err, kernelconfiguration.ErrLoadDocument) || !errors.Is(err, kernelconfiguration.ErrDocumentUnavailable) {
		t.Fatalf("New(symbolic link) = %#v, %v", application, err)
	}
	assertNoBootstrapConstructorCalls(t)
	assertSafeBootstrapError(t, err)
}

func TestApplicationRejectsInvalidContextsAndValues(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	localservice.Reset()
	remotestore.Reset()
	documentPath := writeRuntimeDocument(t, validRuntimeDocument)
	if application, err := New(nil, documentPath); application != nil || !errors.Is(err, ErrBootstrap) || !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("New(nil) = %#v, %v", application, err)
	}
	assertNoBootstrapConstructorCalls(t)

	application, err := New(context.Background(), documentPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := application.Start(nil); !errors.Is(err, ErrApplicationStart) || !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Start(nil) = %v", err)
	}
	if err := application.Stop(nil); !errors.Is(err, ErrApplicationStop) || !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Stop(nil) = %v", err)
	}
	var absent *Application
	if absent.Valid() || absent.State() != "" {
		t.Fatalf("absent application validity = %t, state %q", absent.Valid(), absent.State())
	}
	if err := absent.Start(context.Background()); !errors.Is(err, ErrApplicationStart) || !errors.Is(err, ErrInvalidApplication) {
		t.Fatalf("absent Start = %v", err)
	}
	if err := absent.Stop(context.Background()); !errors.Is(err, ErrApplicationStop) || !errors.Is(err, ErrInvalidApplication) {
		t.Fatalf("absent Stop = %v", err)
	}
}

func writeRuntimeDocument(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime.yaml")
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("write runtime document: %v", err)
	}
	return path
}

func assertNoBootstrapConstructorCalls(t *testing.T) {
	t.Helper()
	localCalls, _ := localservice.Snapshot()
	remoteCalls, _ := remotestore.Snapshot()
	if localCalls != 0 || remoteCalls != 0 {
		t.Fatalf("constructors ran: local %d, remote %d", localCalls, remoteCalls)
	}
}

func assertSafeBootstrapError(t *testing.T, err error) {
	t.Helper()
	for _, forbidden := range []string{
		"runtime-private-endpoint",
		"PLYSTRA_ASSEMBLY_PRIVATE_SECRET",
		"runtime-private-secret-value",
		"runtime-private-missing-document",
		"runtime-private-target",
		"runtime-private-link",
	} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error exposed %q: %v", forbidden, err)
		}
	}
}
`

const emptyGeneratedBootstrapRuntimeTest = `package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	kernellifecycle "github.com/plystra/kernel/lifecycle"
)

func TestEmptyApplicationLifecycle(t *testing.T) {
	documentPath := filepath.Join(t.TempDir(), "runtime.yaml")
	if err := os.WriteFile(documentPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write runtime document: %v", err)
	}
	application, err := New(context.Background(), documentPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !application.Valid() || application.State() != kernellifecycle.StateNew {
		t.Fatalf("application validity = %t, state %q", application.Valid(), application.State())
	}
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if application.State() != kernellifecycle.StateRunning {
		t.Fatalf("state after Start = %q", application.State())
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if application.State() != kernellifecycle.StateStopped {
		t.Fatalf("state after Stop = %q", application.State())
	}
}
`
