package assemblygen_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/clientgen"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/dependencygen"
	"github.com/plystra/cli/internal/invocationgen"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

const (
	wiringApplicationModule = "example.com/wiring-application"
	wiringDependencyModule  = "example.com/wiring-dependency"
	wiringLookupSchema      = `id: catalog.lookup/v1
request:
  key: {type: string, required: true}
  mode: {type: string, enum: [exact, fuzzy], required: true}
  hint: {type: string, enum: [fast, thorough]}
response:
  value: {type: string, required: true}
  status: {type: string, enum: [found, partial], required: true}
  source: {type: string, enum: [primary, replica]}
errors: [not_found]
semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`
	wiringOrderSchema = `id: order.place/v1
request:
  key: {type: string, required: true}
  mode: {type: string, enum: [exact, fuzzy], required: true}
  hint: {type: string, enum: [fast, thorough]}
response:
  receipt: {type: string, required: true}
  status: {type: string, enum: [accepted, partial], required: true}
  source: {type: string, enum: [primary, replica]}
errors: [not_found]
semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`
	wiringWorkflowSchema = `id: workflow.run/v1
request:
  key: {type: string, required: true}
  mode: {type: string, enum: [exact, fuzzy], required: true}
  hint: {type: string, enum: [fast, thorough]}
response:
  result: {type: string, required: true}
  status: {type: string, enum: [completed, partial], required: true}
  source: {type: string, enum: [primary, replica]}
errors: [not_found]
semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`
)

func TestGeneratedRuntimeInjectsLocalAndDependencyModuleClients(t *testing.T) {
	root := t.TempDir()
	applicationRoot := filepath.Join(root, "application")
	dependencyRoot := filepath.Join(root, "dependency")
	cliRoot := repositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))

	writeFile(t, filepath.Join(applicationRoot, "go.mod"), fmt.Sprintf(`module %s

go 1.26

require (
	%s v1.0.0
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace %s => %s

replace github.com/plystra/kernel => %s
`, wiringApplicationModule, wiringDependencyModule, wiringDependencyModule, filepath.ToSlash(dependencyRoot), filepath.ToSlash(kernelRoot)))
	writeFile(t, filepath.Join(dependencyRoot, "go.mod"), fmt.Sprintf(`module %s

go 1.26

require github.com/plystra/kernel v0.0.0

replace github.com/plystra/kernel => %s
`, wiringDependencyModule, filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeBytes(t, filepath.Join(applicationRoot, "go.sum"), goSum)

	for _, schema := range []string{wiringLookupSchema, wiringOrderSchema, wiringWorkflowSchema} {
		contract, err := contractgen.Render([]byte(schema))
		if err != nil {
			t.Fatalf("Render contract: %v", err)
		}
		writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(contract.Path())), contract.Data())
		invocation, err := invocationgen.Render(wiringApplicationModule, []byte(schema))
		if err != nil {
			t.Fatalf("Render invocation: %v", err)
		}
		writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(invocation.Path())), invocation.Data())
	}
	for _, schema := range []string{wiringLookupSchema, wiringOrderSchema, wiringWorkflowSchema} {
		contract, err := contractgen.Render([]byte(schema))
		if err != nil {
			t.Fatalf("Render dependency contract: %v", err)
		}
		writeBytes(t, filepath.Join(dependencyRoot, filepath.FromSlash(contract.Path())), contract.Data())
	}
	applicationLookupClient, err := clientgen.Render(wiringApplicationModule, []byte(wiringLookupSchema))
	if err != nil {
		t.Fatalf("Render application lookup client: %v", err)
	}
	writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(applicationLookupClient.Path())), applicationLookupClient.Data())
	dependencyOrderClient, err := clientgen.Render(wiringDependencyModule, []byte(wiringOrderSchema))
	if err != nil {
		t.Fatalf("Render dependency order client: %v", err)
	}
	writeBytes(t, filepath.Join(dependencyRoot, filepath.FromSlash(dependencyOrderClient.Path())), dependencyOrderClient.Data())

	localDependencies, err := dependencygen.Render(wiringApplicationModule, "orders", "acme.local-orders", []string{"catalog.lookup/v1"})
	if err != nil {
		t.Fatalf("Render local dependencies: %v", err)
	}
	writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(localDependencies.Path())), localDependencies.Data())
	moduleDependencies, err := dependencygen.Render(wiringDependencyModule, "workflow", "remote.workflow", []string{"order.place/v1"})
	if err != nil {
		t.Fatalf("Render module dependencies: %v", err)
	}
	writeBytes(t, filepath.Join(dependencyRoot, filepath.FromSlash(moduleDependencies.Path())), moduleDependencies.Data())

	localConfiguration := renderConfiguration(t, configurationgen.Input{
		PluginName: "orders",
		PluginID:   "acme.local-orders",
		Schema:     parseConfig(t, `mode: {type: string, default: ready, enum: [ready, nil]}`),
	})
	lookupConfiguration := renderConfiguration(t, configurationgen.Input{
		PluginName: "catalog",
		PluginID:   "remote.catalog",
		Schema:     parseConfig(t, "{}"),
	})
	workflowConfiguration := renderConfiguration(t, configurationgen.Input{
		PluginName: "workflow",
		PluginID:   "remote.workflow",
		Schema:     parseConfig(t, "{}"),
	})
	writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(localConfiguration.Path())), localConfiguration.Data())
	writeBytes(t, filepath.Join(dependencyRoot, filepath.FromSlash(lookupConfiguration.Path())), lookupConfiguration.Data())
	writeBytes(t, filepath.Join(dependencyRoot, filepath.FromSlash(workflowConfiguration.Path())), workflowConfiguration.Data())
	writeFile(t, filepath.Join(applicationRoot, "orders", "plugin.go"), wiringLocalOrdersSource)
	writeFile(t, filepath.Join(dependencyRoot, "catalog", "plugin.go"), wiringRemoteCatalogSource)
	writeFile(t, filepath.Join(dependencyRoot, "workflow", "plugin.go"), wiringRemoteWorkflowSource)

	providers := []assemblygen.ProviderInput{
		{
			PluginID:      "remote.workflow",
			ModulePath:    wiringDependencyModule,
			ModuleVersion: "v1.0.0",
			ImportPath:    wiringDependencyModule + "/workflow",
			Dependencies: []assemblygen.DependencyInput{{
				Capability:   "order.place/v1",
				ContractJSON: []byte(wiringOrderSchema),
			}},
		},
		{
			PluginID:      "remote.catalog",
			ModulePath:    wiringDependencyModule,
			ModuleVersion: "v1.0.0",
			ImportPath:    wiringDependencyModule + "/catalog",
		},
		{
			PluginID:   "acme.local-orders",
			ModulePath: wiringApplicationModule,
			ImportPath: wiringApplicationModule + "/orders",
			Dependencies: []assemblygen.DependencyInput{{
				Capability:   "catalog.lookup/v1",
				ContractJSON: []byte(wiringLookupSchema),
			}},
		},
	}
	providerSource, err := assemblygen.RenderProviders(wiringApplicationModule, providers)
	if err != nil {
		t.Fatalf("RenderProviders: %v", err)
	}
	invocationSource, err := assemblygen.RenderInvocations(assemblygen.InvocationOptions{
		ModulePath:               wiringApplicationModule,
		ApplicationBuildIdentity: "wiring-test-build",
		KernelModuleVersion:      "v0.0.0",
		KernelBuildIdentity:      "wiring-test-build",
		DefaultTimeout:           30 * time.Second,
		Providers:                providers,
		Invocations: []assemblygen.InvocationInput{
			{ContractJSON: []byte(wiringLookupSchema), ProviderID: "remote.catalog", SelectionReason: kernelinvocation.SelectionReasonSoleProvider},
			{ContractJSON: []byte(wiringOrderSchema), ProviderID: "acme.local-orders", SelectionReason: kernelinvocation.SelectionReasonSoleProvider},
			{ContractJSON: []byte(wiringWorkflowSchema), ProviderID: "remote.workflow", SelectionReason: kernelinvocation.SelectionReasonSoleProvider},
		},
	})
	if err != nil {
		t.Fatalf("RenderInvocations: %v", err)
	}
	compatibility, err := assemblygen.RenderCompatibility("assembly")
	if err != nil {
		t.Fatalf("RenderCompatibility: %v", err)
	}
	writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(assemblygen.ProvidersPath)), providerSource)
	writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(assemblygen.InvocationsPath)), invocationSource)
	writeBytes(t, filepath.Join(applicationRoot, "generated", "go", "assembly", "compatibility_gen.go"), compatibility)
	writeFile(t, filepath.Join(applicationRoot, "generated", "go", "assembly", "dependencies_gen_test.go"), wiringRuntimeTestSource)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-mod=readonly", "-count=1", "./...")
	command.Dir = applicationRoot
	command.Env = isolatedGoEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated dependency wiring test: %v\n%s", err, output)
	}
}

const wiringLocalOrdersSource = `package orders

import (
	"context"
	"sync"

	lookupclient "example.com/wiring-application/generated/go/clients/catalog/lookup/v1"
	lookupcontract "example.com/wiring-application/generated/go/contracts/catalog/lookup/v1"
	ordercontract "example.com/wiring-application/generated/go/contracts/order/place/v1"
	configuration "example.com/wiring-application/generated/go/configuration"
	dependencies "example.com/wiring-application/generated/go/dependencies/orders"
)

type Config = configuration.OrdersConfig

type Plugin struct{ lookup lookupclient.Client }

var state struct {
	sync.Mutex
	constructorCalls int
	constructorDispatchFailed bool
}

func New(config Config, clients dependencies.Dependencies) *Plugin {
	_, probeError := clients.CatalogLookupV1().Lookup(context.Background(), lookupcontract.Request{
		Key: "constructor-probe",
		Mode: lookupcontract.RequestModeExact,
	})
	state.Lock()
	state.constructorCalls++
	state.constructorDispatchFailed = probeError != nil
	state.Unlock()
	if config.Mode == "nil" {
		return nil
	}
	return &Plugin{lookup: clients.CatalogLookupV1()}
}

func (p *Plugin) Place(ctx context.Context, request ordercontract.Request) (ordercontract.Response, error) {
	var hint *lookupcontract.RequestHint
	if request.Hint != nil {
		converted := lookupcontract.RequestHint(*request.Hint)
		hint = &converted
	}
	response, err := p.lookup.Lookup(ctx, lookupcontract.Request{
		Key: request.Key,
		Mode: lookupcontract.RequestMode(request.Mode),
		Hint: hint,
	})
	if err != nil {
		return ordercontract.Response{}, err
	}
	status := ordercontract.ResponseStatusAccepted
	if response.Status == lookupcontract.ResponseStatusPartial {
		status = ordercontract.ResponseStatusPartial
	}
	var source *ordercontract.ResponseSource
	if response.Source != nil {
		converted := ordercontract.ResponseSource(*response.Source)
		source = &converted
	}
	return ordercontract.Response{Receipt: "order:" + response.Value, Status: status, Source: source}, nil
}

func Reset() {
	state.Lock()
	defer state.Unlock()
	state.constructorCalls = 0
	state.constructorDispatchFailed = false
}

func Snapshot() (int, bool) {
	state.Lock()
	defer state.Unlock()
	return state.constructorCalls, state.constructorDispatchFailed
}
`

const wiringRemoteCatalogSource = `package catalog

import (
	"context"
	"sync"

	lookupcontract "example.com/wiring-dependency/generated/go/contracts/catalog/lookup/v1"
	configuration "example.com/wiring-dependency/generated/go/configuration"
)

type Config = configuration.CatalogConfig
type Plugin struct{}

var state struct {
	sync.Mutex
	lastMode lookupcontract.RequestMode
	lastHint *lookupcontract.RequestHint
}

func New(Config) *Plugin { return &Plugin{} }

func (*Plugin) Lookup(_ context.Context, request lookupcontract.Request) (lookupcontract.Response, error) {
	state.Lock()
	state.lastMode = request.Mode
	state.lastHint = request.Hint
	state.Unlock()
	if request.Key == "missing" {
		return lookupcontract.Response{}, lookupcontract.ErrNotFound
	}
	status := lookupcontract.ResponseStatusFound
	if request.Mode == lookupcontract.RequestModeFuzzy {
		status = lookupcontract.ResponseStatusPartial
	}
	source := lookupcontract.ResponseSourcePrimary
	return lookupcontract.Response{Value: request.Key + ":" + string(request.Mode), Status: status, Source: &source}, nil
}

func Snapshot() (lookupcontract.RequestMode, *lookupcontract.RequestHint) {
	state.Lock()
	defer state.Unlock()
	return state.lastMode, state.lastHint
}
`

const wiringRemoteWorkflowSource = `package workflow

import (
	"context"

	ordercontract "example.com/wiring-dependency/generated/go/contracts/order/place/v1"
	workflowcontract "example.com/wiring-dependency/generated/go/contracts/workflow/run/v1"
	configuration "example.com/wiring-dependency/generated/go/configuration"
	dependencies "example.com/wiring-dependency/generated/go/dependencies/workflow"
)

type Config = configuration.WorkflowConfig
type Plugin struct{ orders dependencies.Dependencies }

func New(_ Config, clients dependencies.Dependencies) *Plugin {
	return &Plugin{orders: clients}
}

func (p *Plugin) Run(ctx context.Context, request workflowcontract.Request) (workflowcontract.Response, error) {
	var hint *ordercontract.RequestHint
	if request.Hint != nil {
		converted := ordercontract.RequestHint(*request.Hint)
		hint = &converted
	}
	response, err := p.orders.OrderPlaceV1().Place(ctx, ordercontract.Request{
		Key: request.Key,
		Mode: ordercontract.RequestMode(request.Mode),
		Hint: hint,
	})
	if err != nil {
		return workflowcontract.Response{}, err
	}
	status := workflowcontract.ResponseStatusCompleted
	if response.Status == ordercontract.ResponseStatusPartial {
		status = workflowcontract.ResponseStatusPartial
	}
	var source *workflowcontract.ResponseSource
	if response.Source != nil {
		converted := workflowcontract.ResponseSource(*response.Source)
		source = &converted
	}
	return workflowcontract.Response{Result: "workflow:" + response.Receipt, Status: status, Source: source}, nil
}
`

const wiringRuntimeTestSource = `package assembly

import (
	"context"
	"errors"
	"strings"
	"testing"

	orders "example.com/wiring-application/orders"
	lookupcontract "example.com/wiring-application/generated/go/contracts/catalog/lookup/v1"
	ordercontract "example.com/wiring-application/generated/go/contracts/order/place/v1"
	workflowcontract "example.com/wiring-application/generated/go/contracts/workflow/run/v1"
	remotecatalog "example.com/wiring-dependency/catalog"
	kernelconfiguration "github.com/plystra/kernel/configuration"
)

func TestInjectedDependencyChain(t *testing.T) {
	orders.Reset()
	providers, invocations, err := NewRuntime(context.Background(), newWiringResolver(t), []byte("config: {}\n"))
	if err != nil || !providers.Valid() || !invocations.Valid() {
		t.Fatalf("NewRuntime = %v, %v, %v", providers, invocations, err)
	}
	constructorCalls, constructorDispatchFailed := orders.Snapshot()
	if constructorCalls != 1 || !constructorDispatchFailed {
		t.Fatalf("constructor state = calls %d, dispatch failed %t", constructorCalls, constructorDispatchFailed)
	}
	lookupHint := lookupcontract.RequestHintFast
	lookupResponse, err := invocations.CatalogLookupV1().Invoke(context.Background(), lookupcontract.Request{
		Key: "direct",
		Mode: lookupcontract.RequestModeFuzzy,
		Hint: &lookupHint,
	})
	if err != nil || lookupResponse.Value != "direct:fuzzy" || lookupResponse.Status != lookupcontract.ResponseStatusPartial {
		t.Fatalf("CatalogLookupV1.Invoke = %#v, %v", lookupResponse, err)
	}
	orderHint := ordercontract.RequestHintFast
	orderResponse, err := invocations.OrderPlaceV1().Invoke(context.Background(), ordercontract.Request{
		Key: "direct",
		Mode: ordercontract.RequestModeFuzzy,
		Hint: &orderHint,
	})
	if err != nil || orderResponse.Receipt != "order:direct:fuzzy" || orderResponse.Status != ordercontract.ResponseStatusPartial {
		t.Fatalf("OrderPlaceV1.Invoke = %#v, %v", orderResponse, err)
	}
	hint := workflowcontract.RequestHintFast
	response, err := invocations.WorkflowRunV1().Invoke(context.Background(), workflowcontract.Request{
		Key: "item",
		Mode: workflowcontract.RequestModeFuzzy,
		Hint: &hint,
	})
	if err != nil || response.Result != "workflow:order:item:fuzzy" || response.Status != workflowcontract.ResponseStatusPartial || response.Source == nil || *response.Source != workflowcontract.ResponseSourcePrimary {
		t.Fatalf("WorkflowRunV1.Invoke = %#v, %v", response, err)
	}
	mode, observedHint := remotecatalog.Snapshot()
	if string(mode) != "fuzzy" || observedHint == nil || string(*observedHint) != "fast" {
		t.Fatalf("remote typed request = mode %q, hint %v", mode, observedHint)
	}
	_, err = invocations.OrderPlaceV1().Invoke(context.Background(), ordercontract.Request{Key: "missing", Mode: ordercontract.RequestModeExact})
	assertWiringSemanticError(t, err, "not_found")
	_, err = invocations.WorkflowRunV1().Invoke(context.Background(), workflowcontract.Request{Key: "missing", Mode: workflowcontract.RequestModeExact})
	assertWiringSemanticError(t, err, "not_found")
	_, err = invocations.CatalogLookupV1().Invoke(context.Background(), lookupcontract.Request{Key: "missing", Mode: lookupcontract.RequestModeExact})
	assertWiringSemanticError(t, err, "not_found")
}

func TestConstructorFailureDoesNotPublishDispatch(t *testing.T) {
	orders.Reset()
	providers, invocations, err := NewRuntime(context.Background(), newWiringResolver(t), []byte("config:\n  acme.local-orders:\n    mode: \"nil\"\n"))
	if providers.Valid() || invocations.Valid() || !errors.Is(err, ErrRuntimeAssembly) || !errors.Is(err, ErrPluginConstructor) {
		t.Fatalf("NewRuntime(nil constructor) = %v, %v, %v", providers, invocations, err)
	}
	constructorCalls, constructorDispatchFailed := orders.Snapshot()
	if constructorCalls != 1 || !constructorDispatchFailed {
		t.Fatalf("failed constructor state = calls %d, dispatch failed %t", constructorCalls, constructorDispatchFailed)
	}
	if strings.Contains(err.Error(), "constructor-probe") {
		t.Fatalf("constructor failure exposed request data: %v", err)
	}
}

func newWiringResolver(t *testing.T) *kernelconfiguration.Resolver {
	t.Helper()
	resolver, err := kernelconfiguration.NewResolver(kernelconfiguration.ResolverOptions{MaximumValueBytes: 1024})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return resolver
}

func assertWiringSemanticError(t *testing.T, err error, code string) {
	t.Helper()
	var semantic interface{ SemanticErrorCode() string }
	if !errors.As(err, &semantic) || semantic.SemanticErrorCode() != code {
		t.Fatalf("semantic error = %v", err)
	}
}
`
