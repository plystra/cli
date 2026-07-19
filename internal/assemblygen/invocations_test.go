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
	"strings"
	"testing"
	"time"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/clientgen"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/generationlowering"
	"github.com/plystra/cli/internal/invocationgen"
	kernelcatalog "github.com/plystra/kernel/capability/catalog"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

const (
	runtimePolicySchema = `id: policy.check/v1
request:
  marker: {type: string, required: true}
response:
  allowed: {type: boolean, required: true}
errors: [denied]
`
	runtimeMessageSchema = `id: message.send/v1
request:
  recipient: {type: string, required: true}
  mode: {type: string, required: true, enum: [fast, safe]}
  channel: {type: string, enum: [email, sms]}
  count: {type: integer, required: true}
  score: {type: number}
  enabled: {type: boolean, required: true}
  labels: {type: array, items: string, required: true}
  metadata: {type: object, required: true}
response:
  receipt: {type: string, required: true}
  status: {type: string, required: true, enum: [sent, queued]}
  retry: {type: string, enum: [later, never]}
errors: [invalid_recipient]
extensions:
  policy:
    required: true
`
)

func TestRenderInvocationsIsDeterministicCanonicalAssembly(t *testing.T) {
	t.Parallel()

	provider := assemblygen.ProviderInput{
		PluginID:      "acme.remote-service",
		ModulePath:    "example.com/runtime-dependency",
		ModuleVersion: "v1.2.3",
		ImportPath:    "example.com/runtime-dependency/remote-service",
	}
	invocations := []assemblygen.InvocationInput{
		{
			ContractJSON:    []byte(runtimeMessageSchema),
			ProviderID:      provider.PluginID,
			SelectionReason: kernelinvocation.SelectionReasonExplicit,
			Dependencies:    []string{"policy.check/v1"},
		},
		{
			ContractJSON:    []byte(runtimePolicySchema),
			ProviderID:      provider.PluginID,
			SelectionReason: kernelinvocation.SelectionReasonSoleProvider,
		},
	}
	options := assemblygen.InvocationOptions{
		ModulePath:               "example.com/runtime-application",
		ApplicationBuildIdentity: "sha256:0123456789abcdef",
		KernelModuleVersion:      "v0.1.0",
		KernelBuildIdentity:      "sha256:0123456789abcdef",
		DefaultTimeout:           30 * time.Second,
		Providers:                []assemblygen.ProviderInput{provider},
		Invocations:              invocations,
	}
	generated, err := assemblygen.RenderInvocations(options)
	if err != nil {
		t.Fatalf("RenderInvocations: %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), assemblygen.InvocationsPath, generated, parser.AllErrors); err != nil {
		t.Fatalf("parse generated invocations: %v\n%s", err, generated)
	}
	for _, required := range []string{
		`kernelintrinsic.NewBindings`,
		`len(i.catalog.Bindings()) != 4`,
		`kernelinvocation.NewModuleBuild("example.com/runtime-dependency", "v1.2.3", "sha256:0123456789abcdef")`,
		`kernelinvocation.SelectionReasonExplicit`,
		`kernelinvocation.SelectionReasonSoleProvider`,
		`ProviderPackage: "example.com/runtime-dependency/remote-service"`,
		`kernelcapability.MustParseContractWithSemanticErrors[contract0.Request, contract0.Response](contract0.CapabilityID, "invalid_recipient")`,
		`kernelcapability.MustParseContractWithSemanticErrors[contract1.Request, contract1.Response](contract1.CapabilityID, "denied")`,
		`result.Mode = providercontract0.RequestMode(value.Mode)`,
		`converted := providercontract0.RequestChannel(*value.Channel)`,
		`result.Status = contract0.ResponseStatus(value.Status)`,
		`converted := contract0.ResponseRetry(*value.Retry)`,
		`handle1 := applicationinvocation1.New(rawHandle1)`,
		`handle0 := applicationinvocation0.New(rawHandle0, applicationclient1.New(handle1))`,
		`func (i Invocations) MessageSendV1()`,
		`func (i Invocations) PolicyCheckV1()`,
		`func (i Invocations) IntrinsicHealth(ctx context.Context) (kernelintrinsic.HealthResponse, error)`,
		`kernelinvocation.NewHandle(i.dispatcher, kernelintrinsic.HealthContract(), true)`,
	} {
		if !bytes.Contains(generated, []byte(required)) {
			t.Fatalf("generated source omits %q:\n%s", required, generated)
		}
	}
	for _, forbidden := range []string{`"encoding/json"`, "json.Marshal", "json.Unmarshal", "authn.login/v1"} {
		if bytes.Contains(bytes.ToLower(generated), bytes.ToLower([]byte(forbidden))) {
			t.Fatalf("generated canonical assembly contains %q:\n%s", forbidden, generated)
		}
	}

	reversed := options
	reversed.Invocations = []assemblygen.InvocationInput{invocations[1], invocations[0]}
	repeated, err := assemblygen.RenderInvocations(reversed)
	if err != nil || !bytes.Equal(generated, repeated) {
		t.Fatalf("reordered RenderInvocations is not deterministic: %v", err)
	}

	empty, err := assemblygen.RenderInvocations(assemblygen.InvocationOptions{
		ModulePath:               options.ModulePath,
		ApplicationBuildIdentity: options.ApplicationBuildIdentity,
		KernelModuleVersion:      options.KernelModuleVersion,
		KernelBuildIdentity:      options.KernelBuildIdentity,
		DefaultTimeout:           options.DefaultTimeout,
	})
	if err != nil {
		t.Fatalf("RenderInvocations(empty): %v", err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), assemblygen.InvocationsPath, empty, parser.AllErrors); err != nil {
		t.Fatalf("parse empty generated invocations: %v\n%s", err, empty)
	}
	if !bytes.Contains(empty, []byte("return true")) || !bytes.Contains(empty, []byte("len(i.catalog.Bindings()) != 2")) || !bytes.Contains(empty, []byte("kernelintrinsic.NewBindings")) || !bytes.Contains(empty, []byte("func (i Invocations) IntrinsicHealth")) || bytes.Contains(empty, []byte("kernelcapability")) {
		t.Fatalf("empty runtime is not a valid zero-provider assembly:\n%s", empty)
	}
}

func TestRenderInvocationsRejectsInvalidRuntimePlans(t *testing.T) {
	t.Parallel()

	provider := assemblygen.ProviderInput{
		PluginID:      "acme.remote-service",
		ModulePath:    "example.com/runtime-dependency",
		ModuleVersion: "v1.2.3",
		ImportPath:    "example.com/runtime-dependency/remote-service",
	}
	valid := assemblygen.InvocationOptions{
		ModulePath:               "example.com/runtime-application",
		ApplicationBuildIdentity: "test-build",
		KernelModuleVersion:      "v0.1.0",
		KernelBuildIdentity:      "test-build",
		DefaultTimeout:           time.Second,
		Providers:                []assemblygen.ProviderInput{provider},
		Invocations: []assemblygen.InvocationInput{
			{ContractJSON: []byte(runtimeMessageSchema), ProviderID: provider.PluginID, SelectionReason: kernelinvocation.SelectionReasonSoleProvider},
			{ContractJSON: []byte(runtimePolicySchema), ProviderID: provider.PluginID, SelectionReason: kernelinvocation.SelectionReasonSoleProvider},
		},
	}
	tests := []struct {
		name   string
		edit   func(*assemblygen.InvocationOptions)
		reason error
		text   string
	}{
		{name: "invalid application module", edit: func(value *assemblygen.InvocationOptions) { value.ModulePath = "../application" }, reason: assemblygen.ErrInvalidInvocation},
		{name: "invalid timeout", edit: func(value *assemblygen.InvocationOptions) { value.DefaultTimeout = 0 }, reason: assemblygen.ErrInvalidInvocation},
		{name: "missing Kernel provenance", edit: func(value *assemblygen.InvocationOptions) {
			value.KernelModuleVersion = ""
			value.KernelBuildIdentity = ""
		}, reason: assemblygen.ErrInvalidInvocation, text: "intrinsic Kernel build provenance"},
		{name: "missing build provenance", edit: func(value *assemblygen.InvocationOptions) {
			value.ApplicationBuildIdentity = ""
			value.Providers[0].ModuleVersion = ""
		}, reason: assemblygen.ErrInvalidInvocation, text: "build provenance"},
		{name: "invalid provider", edit: func(value *assemblygen.InvocationOptions) { value.Providers[0].ImportPath += "/nested" }, reason: assemblygen.ErrInvalidProvider},
		{name: "absent selected provider", edit: func(value *assemblygen.InvocationOptions) { value.Invocations[0].ProviderID = "acme.absent" }, reason: assemblygen.ErrInvalidInvocation, text: "acme.absent"},
		{name: "intrinsic selection reason", edit: func(value *assemblygen.InvocationOptions) {
			value.Invocations[0].SelectionReason = kernelinvocation.SelectionReasonIntrinsic
		}, reason: assemblygen.ErrInvalidInvocation},
		{name: "malformed contract", edit: func(value *assemblygen.InvocationOptions) { value.Invocations[0].ContractJSON = []byte("id: Invalid") }, reason: assemblygen.ErrInvalidInvocation},
		{name: "duplicate capability", edit: func(value *assemblygen.InvocationOptions) {
			value.Invocations[1].ContractJSON = []byte(runtimeMessageSchema)
		}, reason: assemblygen.ErrDuplicateInvocation},
		{name: "generated accessor collision", edit: func(value *assemblygen.InvocationOptions) {
			value.Invocations[0].ContractJSON = []byte(strings.Replace(runtimePolicySchema, "policy.check/v1", "a-b.c/v1", 1))
			value.Invocations[1].ContractJSON = []byte(strings.Replace(runtimePolicySchema, "policy.check/v1", "a.b-c/v1", 1))
		}, reason: assemblygen.ErrDuplicateInvocation, text: "ABC"},
		{name: "missing dependency", edit: func(value *assemblygen.InvocationOptions) {
			value.Invocations[0].Dependencies = []string{"missing.target/v1"}
		}, reason: assemblygen.ErrInvocationDependency, text: "missing.target/v1"},
		{name: "self dependency", edit: func(value *assemblygen.InvocationOptions) {
			value.Invocations[0].Dependencies = []string{"message.send/v1"}
		}, reason: assemblygen.ErrInvocationDependency, text: "depends on itself"},
		{name: "repeated dependency", edit: func(value *assemblygen.InvocationOptions) {
			value.Invocations[0].Dependencies = []string{"policy.check/v1", "policy.check/v1"}
		}, reason: assemblygen.ErrInvocationDependency, text: "repeats dependency"},
		{name: "dependency cycle", edit: func(value *assemblygen.InvocationOptions) {
			value.Invocations[0].Dependencies = []string{"policy.check/v1"}
			value.Invocations[1].Dependencies = []string{"message.send/v1"}
		}, reason: assemblygen.ErrInvocationDependency, text: "message.send/v1 -> policy.check/v1 -> message.send/v1"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := cloneInvocationOptions(valid)
			test.edit(&options)
			generated, err := assemblygen.RenderInvocations(options)
			if generated != nil || !errors.Is(err, assemblygen.ErrRenderInvocations) || !errors.Is(err, test.reason) {
				t.Fatalf("RenderInvocations = %q, %v", generated, err)
			}
			if test.text != "" && !strings.Contains(err.Error(), test.text) {
				t.Fatalf("RenderInvocations error %q omits %q", err, test.text)
			}
		})
	}
}

func TestGeneratedInvocationsBridgeModulesAndPublishCanonicalRuntime(t *testing.T) {
	root := t.TempDir()
	applicationRoot := filepath.Join(root, "application")
	dependencyRoot := filepath.Join(root, "dependency")
	cliRoot := repositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))

	writeFile(t, filepath.Join(applicationRoot, "go.mod"), fmt.Sprintf(`module example.com/runtime-application

go 1.26

require (
	example.com/runtime-dependency v1.2.3
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace example.com/runtime-dependency => %s

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(dependencyRoot), filepath.ToSlash(kernelRoot)))
	writeFile(t, filepath.Join(dependencyRoot, "go.mod"), fmt.Sprintf(`module example.com/runtime-dependency

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeBytes(t, filepath.Join(applicationRoot, "go.sum"), goSum)

	policyContract := renderContract(t, runtimePolicySchema)
	messageContract := renderContract(t, runtimeMessageSchema)
	for _, file := range []contractgen.File{policyContract, messageContract} {
		writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(file.Path())), file.Data())
		writeBytes(t, filepath.Join(dependencyRoot, filepath.FromSlash(file.Path())), file.Data())
	}
	healthSchema := intrinsicSchema(t, "kernel.health/v1")
	healthContract, err := contractgen.RenderIntrinsic(healthSchema)
	if err != nil {
		t.Fatalf("render health contract: %v", err)
	}
	writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(healthContract.Path())), healthContract.Data())
	policyInvocation, err := invocationgen.Render("example.com/runtime-application", []byte(runtimePolicySchema))
	if err != nil {
		t.Fatalf("render policy invocation: %v", err)
	}
	messageInvocation, err := invocationgen.RenderPlan(
		"example.com/runtime-application",
		[]byte(runtimeMessageSchema),
		runtimeDependencyPlan(t),
	)
	if err != nil {
		t.Fatalf("render message invocation: %v", err)
	}
	healthInvocation, err := invocationgen.Render("example.com/runtime-application", healthSchema)
	if err != nil {
		t.Fatalf("render health invocation: %v", err)
	}
	policyClient, err := clientgen.Render("example.com/runtime-application", []byte(runtimePolicySchema))
	if err != nil {
		t.Fatalf("render policy client: %v", err)
	}
	healthClient, err := clientgen.Render("example.com/runtime-application", healthSchema)
	if err != nil {
		t.Fatalf("render health client: %v", err)
	}
	for _, file := range []struct {
		path string
		data []byte
	}{
		{policyInvocation.Path(), policyInvocation.Data()},
		{messageInvocation.Path(), messageInvocation.Data()},
		{healthInvocation.Path(), healthInvocation.Data()},
		{policyClient.Path(), policyClient.Data()},
		{healthClient.Path(), healthClient.Data()},
	} {
		writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(file.path)), file.data)
	}
	if got := messageInvocation.Dependencies(); len(got) != 2 || got[0] != "kernel.health/v1" || got[1] != "policy.check/v1" {
		t.Fatalf("message invocation dependencies = %q", got)
	}

	configuration := renderConfiguration(t, configurationgen.Input{
		PluginName: "remote-service",
		PluginID:   "acme.remote-service",
		Schema:     parseConfig(t, "{}"),
	})
	writeBytes(t, filepath.Join(dependencyRoot, filepath.FromSlash(configuration.Path())), configuration.Data())
	writeFile(t, filepath.Join(dependencyRoot, "remote-service", "plugin.go"), runtimeProviderSource)

	provider := assemblygen.ProviderInput{
		PluginID:      "acme.remote-service",
		ModulePath:    "example.com/runtime-dependency",
		ModuleVersion: "v1.2.3",
		ImportPath:    "example.com/runtime-dependency/remote-service",
	}
	providers, err := assemblygen.RenderProviders("example.com/runtime-application", []assemblygen.ProviderInput{provider})
	if err != nil {
		t.Fatalf("RenderProviders: %v", err)
	}
	invocations, err := assemblygen.RenderInvocations(assemblygen.InvocationOptions{
		ModulePath:               "example.com/runtime-application",
		ApplicationBuildIdentity: "runtime-build-123",
		KernelModuleVersion:      "v0.0.0",
		KernelBuildIdentity:      "runtime-build-123",
		DefaultTimeout:           30 * time.Second,
		Providers:                []assemblygen.ProviderInput{provider},
		Invocations: []assemblygen.InvocationInput{
			{
				ContractJSON:    []byte(runtimeMessageSchema),
				ProviderID:      provider.PluginID,
				SelectionReason: kernelinvocation.SelectionReasonExplicit,
				Dependencies:    messageInvocation.Dependencies(),
			},
			{
				ContractJSON:    healthSchema,
				Intrinsic:       true,
				SelectionReason: kernelinvocation.SelectionReasonIntrinsic,
			},
			{
				ContractJSON:    []byte(runtimePolicySchema),
				ProviderID:      provider.PluginID,
				SelectionReason: kernelinvocation.SelectionReasonSoleProvider,
			},
		},
	})
	if err != nil {
		t.Fatalf("RenderInvocations: %v", err)
	}
	if bytes.Contains(invocations, []byte(`"encoding/json"`)) || bytes.Contains(invocations, []byte("json.Marshal")) {
		t.Fatalf("cross-module adapter uses JSON conversion:\n%s", invocations)
	}
	compatibility, err := assemblygen.RenderCompatibility("assembly")
	if err != nil {
		t.Fatalf("RenderCompatibility: %v", err)
	}
	writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(assemblygen.ProvidersPath)), providers)
	writeBytes(t, filepath.Join(applicationRoot, filepath.FromSlash(assemblygen.InvocationsPath)), invocations)
	writeBytes(t, filepath.Join(applicationRoot, "generated", "go", "assembly", "compatibility_gen.go"), compatibility)
	writeFile(t, filepath.Join(applicationRoot, "generated", "go", "assembly", "invocations_gen_test.go"), runtimeAssemblyTestSource)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-mod=readonly", "-count=1", "./...")
	command.Dir = applicationRoot
	command.Env = isolatedGoEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated canonical invocation runtime test: %v\n%s", err, output)
	}
}

func FuzzRenderInvocations(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(runtimeMessageSchema),
		[]byte(runtimePolicySchema),
		[]byte("id: invalid\n"),
		[]byte("{}\n"),
	} {
		f.Add(seed)
	}
	provider := assemblygen.ProviderInput{
		PluginID:      "acme.remote-service",
		ModulePath:    "example.com/runtime-dependency",
		ModuleVersion: "v1.2.3",
		ImportPath:    "example.com/runtime-dependency/remote-service",
	}
	f.Fuzz(func(t *testing.T, schema []byte) {
		options := assemblygen.InvocationOptions{
			ModulePath:               "example.com/runtime-application",
			ApplicationBuildIdentity: "fuzz-build",
			KernelModuleVersion:      "v0.1.0",
			KernelBuildIdentity:      "fuzz-build",
			DefaultTimeout:           time.Second,
			Providers:                []assemblygen.ProviderInput{provider},
			Invocations: []assemblygen.InvocationInput{{
				ContractJSON:    schema,
				ProviderID:      provider.PluginID,
				SelectionReason: kernelinvocation.SelectionReasonSoleProvider,
			}},
		}
		generated, err := assemblygen.RenderInvocations(options)
		if err != nil {
			if generated != nil || !errors.Is(err, assemblygen.ErrRenderInvocations) {
				t.Fatalf("RenderInvocations = %q, %v", generated, err)
			}
			return
		}
		if _, err := parser.ParseFile(token.NewFileSet(), assemblygen.InvocationsPath, generated, parser.AllErrors); err != nil {
			t.Fatalf("parse generated invocations: %v\n%s", err, generated)
		}
		repeated, err := assemblygen.RenderInvocations(options)
		if err != nil || !bytes.Equal(generated, repeated) {
			t.Fatalf("repeated RenderInvocations = %q, %v", repeated, err)
		}
	})
}

func cloneInvocationOptions(value assemblygen.InvocationOptions) assemblygen.InvocationOptions {
	result := value
	result.Providers = append([]assemblygen.ProviderInput(nil), value.Providers...)
	result.Invocations = make([]assemblygen.InvocationInput, len(value.Invocations))
	for index, invocation := range value.Invocations {
		result.Invocations[index] = invocation
		result.Invocations[index].ContractJSON = append([]byte(nil), invocation.ContractJSON...)
		result.Invocations[index].Dependencies = append([]string(nil), invocation.Dependencies...)
	}
	return result
}

func renderContract(t testing.TB, schema string) contractgen.File {
	t.Helper()
	file, err := contractgen.Render([]byte(schema))
	if err != nil {
		t.Fatalf("contractgen.Render: %v", err)
	}
	return file
}

func runtimeDependencyPlan(t testing.TB) generationlowering.Plan {
	t.Helper()
	messageID := runtimeCapabilityID(t, "message.send/v1")
	policyID := runtimeCapabilityID(t, "policy.check/v1")
	healthID := runtimeCapabilityID(t, "kernel.health/v1")
	messageContract := runtimeCanonicalContract(t, runtimeMessageSchema)
	policyContract := runtimeCanonicalContract(t, runtimePolicySchema)
	healthContract := intrinsicSchema(t, "kernel.health/v1")
	context, err := generation.NewContext(generation.Input{
		Plugins: []generation.PluginInput{{
			ID:                "acme.remote-service",
			ModulePath:        "example.com/runtime-dependency",
			ModuleVersion:     "v1.2.3",
			Provides:          []string{messageID.String(), policyID.String()},
			BuildMetadataJSON: []byte("{}"),
		}},
		Capabilities: []generation.CapabilityInput{{ContractJSON: messageContract}, {ContractJSON: policyContract}, {ContractJSON: healthContract, Intrinsic: true}},
		Requirements: []string{messageID.String(), policyID.String(), healthID.String()},
		Providers: []generation.ProviderInput{
			{Capability: messageID.String(), Plugin: "acme.remote-service"},
			{Capability: policyID.String(), Plugin: "acme.remote-service"},
		},
	})
	if err != nil {
		t.Fatalf("generation.NewContext: %v", err)
	}
	output, err := generation.NormalizeOutput(context, generation.Output{Contributions: []generation.Contribution{{
		ID:        "policy.check-message",
		Namespace: "policy",
		Source:    messageID,
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{{
			ID: "check",
			CapabilityCall: &generation.GeneratedCapabilityCall{
				Capability: policyID,
				Request: []generation.GeneratedFieldBinding{{
					Field: "marker",
					Value: generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{
						Source: generation.GeneratedInvocationRequestField,
						Name:   "recipient",
					}},
				}},
				TimeoutMilliseconds: 250,
				OnError:             generation.GeneratedCallFailClosed,
			},
		}, {
			ID: "health",
			CapabilityCall: &generation.GeneratedCapabilityCall{
				Capability:          healthID,
				TimeoutMilliseconds: 250,
				OnError:             generation.GeneratedCallFailClosed,
			},
		}},
	}}})
	if err != nil {
		t.Fatalf("generation.NormalizeOutput: %v", err)
	}
	plan, err := generationlowering.Lower("example.com/runtime-application", output.Contributions())
	if err != nil {
		t.Fatalf("generationlowering.Lower: %v", err)
	}
	return plan
}

func intrinsicSchema(t testing.TB, value string) []byte {
	t.Helper()
	for _, definition := range kernelcatalog.Definitions() {
		if definition.ID().String() != value {
			continue
		}
		return runtimeCanonicalContract(t, string(definition.Source()))
	}
	t.Fatalf("Kernel catalog omits %s", value)
	return nil
}

func runtimeCanonicalContract(t testing.TB, schema string) []byte {
	t.Helper()
	canonical, err := capabilitymeta.NormalizeSchema([]byte(schema))
	if err != nil {
		t.Fatalf("capabilitymeta.NormalizeSchema: %v", err)
	}
	return canonical
}

func runtimeCapabilityID(t testing.TB, value string) generation.CapabilityID {
	t.Helper()
	identifier, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("generation.ParseCapabilityID(%s): %v", value, err)
	}
	return identifier
}

const runtimeProviderSource = `package remoteservice

import (
	"context"
	"fmt"
	"sync"

	configuration "example.com/runtime-dependency/generated/go/configuration"
	messagecontract "example.com/runtime-dependency/generated/go/contracts/message/send/v1"
	policycontract "example.com/runtime-dependency/generated/go/contracts/policy/check/v1"
)

type Config = configuration.RemoteServiceConfig
type Plugin struct{}

var events struct {
	sync.Mutex
	values []string
}

func New(Config) *Plugin { return &Plugin{} }

func (*Plugin) Check(_ context.Context, request policycontract.Request) (policycontract.Response, error) {
	addEvent("policy:" + request.Marker)
	return policycontract.Response{Allowed: true}, nil
}

func (*Plugin) Send(_ context.Context, request messagecontract.Request) (messagecontract.Response, error) {
	addEvent("message:" + request.Recipient)
	switch request.Recipient {
	case "semantic":
		return messagecontract.Response{Receipt: "must-not-escape"}, providerSemanticError{}
	case "panic":
		panic("runtime-private-provider-panic")
	}
	channel := ""
	if request.Channel != nil {
		channel = string(*request.Channel)
	}
	score := 0.0
	if request.Score != nil {
		score = *request.Score
	}
	retry := messagecontract.ResponseRetry("later")
	return messagecontract.Response{
		Receipt: fmt.Sprintf("%s|%s|%s|%d|%.1f|%t|%d|%v", request.Recipient, request.Mode, channel, request.Count, score, request.Enabled, len(request.Labels), request.Metadata["source"]),
		Status:  messagecontract.ResponseStatus("sent"),
		Retry:   &retry,
	}, nil
}

func ResetEvents() {
	events.Lock()
	defer events.Unlock()
	events.values = nil
}

func Events() []string {
	events.Lock()
	defer events.Unlock()
	return append([]string(nil), events.values...)
}

func addEvent(value string) {
	events.Lock()
	defer events.Unlock()
	events.values = append(events.values, value)
}

type providerSemanticError struct{}

func (providerSemanticError) Error() string { return "runtime-private-provider-semantic-payload" }
func (providerSemanticError) SemanticErrorCode() string { return "invalid_recipient" }
`

const runtimeAssemblyTestSource = `package assembly

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	healthcontract "example.com/runtime-application/generated/go/contracts/kernel/health/v1"
	messagecontract "example.com/runtime-application/generated/go/contracts/message/send/v1"
	remoteservice "example.com/runtime-dependency/remote-service"
	kernelconfiguration "github.com/plystra/kernel/configuration"
	kernelintrinsic "github.com/plystra/kernel/intrinsic"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestCanonicalInvocationRuntime(t *testing.T) {
	resolver, err := kernelconfiguration.NewResolver(kernelconfiguration.ResolverOptions{MaximumValueBytes: 1024})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if invocations, err := publishInvocations(pendingInvocations{}, Providers{}); invocations.Valid() || !errors.Is(err, ErrInvocationAssembly) {
		t.Fatalf("publishInvocations(invalid) = %v, %v", invocations, err)
	}
	providers, invocations, err := NewRuntime(context.Background(), resolver, []byte("config: {}\n"))
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if !providers.Valid() || !invocations.Valid() {
		t.Fatalf("generated runtime is invalid")
	}

	bindings := invocations.Catalog().Bindings()
	if len(bindings) != 4 {
		t.Fatalf("catalog bindings = %d", len(bindings))
	}
	reasons := map[string]kernelinvocation.SelectionReason{
		"message.send/v1": kernelinvocation.SelectionReasonExplicit,
		"policy.check/v1": kernelinvocation.SelectionReasonSoleProvider,
	}
	for _, binding := range bindings {
		capabilityID := binding.Capability().String()
		if capabilityID == "kernel.health/v1" || capabilityID == "kernel.info/v1" {
			build := binding.ProviderBuild()
			if binding.ProviderKind() != kernelinvocation.ProviderKindKernel ||
				binding.ProviderID().String() != "" ||
				binding.ProviderPackage() != "github.com/plystra/kernel/intrinsic" ||
				binding.SelectionReason() != kernelinvocation.SelectionReasonIntrinsic ||
				build.ModulePath() != "github.com/plystra/kernel" ||
				build.ModuleVersion() != "v0.0.0" ||
				build.BuildIdentity() != "runtime-build-123" ||
				binding.SchemaDigest() == [32]byte{} {
				t.Fatalf("intrinsic binding provenance for %s is incomplete", binding.Capability())
			}
			continue
		}
		wantReason, exists := reasons[capabilityID]
		if !exists {
			t.Fatalf("catalog contains non-canonical ID %q", binding.Capability())
		}
		build := binding.ProviderBuild()
		if binding.ProviderKind() != kernelinvocation.ProviderKindPlugin ||
			binding.ProviderID().String() != "acme.remote-service" ||
			binding.ProviderPackage() != "example.com/runtime-dependency/remote-service" ||
			binding.SelectionReason() != wantReason ||
			build.ModulePath() != "example.com/runtime-dependency" ||
			build.ModuleVersion() != "v1.2.3" ||
			build.BuildIdentity() != "runtime-build-123" ||
			binding.SchemaDigest() == [32]byte{} {
			t.Fatalf("binding provenance for %s is incomplete", binding.Capability())
		}
	}
	bindings[0] = kernelinvocation.Binding{}
	if fresh := invocations.Catalog().Bindings(); len(fresh) != 4 || fresh[0].Capability().String() == "" {
		t.Fatalf("catalog bindings were mutable: %#v", fresh)
	}
	health, err := invocations.KernelHealthV1().Invoke(context.Background(), healthcontract.Request{})
	if err != nil || health.Status != healthcontract.ResponseStatusHealthy {
		t.Fatalf("KernelHealthV1.Invoke = %#v, %v", health, err)
	}
	intrinsicHealth, err := invocations.IntrinsicHealth(context.Background())
	if err != nil || intrinsicHealth.Status != kernelintrinsic.HealthStatusHealthy {
		t.Fatalf("IntrinsicHealth = %#v, %v", intrinsicHealth, err)
	}

	channel := messagecontract.RequestChannel("sms")
	score := 1.5
	remoteservice.ResetEvents()
	response, err := invocations.MessageSendV1().Invoke(context.Background(), messagecontract.Request{
		Recipient: "alice@example.com",
		Mode:      messagecontract.RequestMode("fast"),
		Channel:   &channel,
		Count:     3,
		Score:     &score,
		Enabled:   true,
		Labels:    []string{"one", "two"},
		Metadata:  map[string]any{"source": "test"},
	})
	if err != nil {
		t.Fatalf("MessageSendV1.Invoke: %v", err)
	}
	if response.Receipt != "alice@example.com|fast|sms|3|1.5|true|2|test" || response.Status != messagecontract.ResponseStatus("sent") || response.Retry == nil || *response.Retry != messagecontract.ResponseRetry("later") {
		t.Fatalf("cross-module response = %#v", response)
	}
	if got := strings.Join(remoteservice.Events(), ","); got != "policy:alice@example.com,message:alice@example.com" {
		t.Fatalf("generated invocation order = %q", got)
	}

	remoteservice.ResetEvents()
	response, err = invocations.MessageSendV1().Invoke(context.Background(), messagecontract.Request{Recipient: "semantic", Mode: messagecontract.RequestMode("safe"), Enabled: true, Labels: []string{}, Metadata: map[string]any{}})
	var semantic *kernelinvocation.SemanticError
	if !errors.As(err, &semantic) || semantic.SemanticErrorCode() != "invalid_recipient" || response != (messagecontract.Response{}) || strings.Contains(fmt.Sprint(err), "runtime-private") {
		t.Fatalf("semantic boundary = response %#v, error %v", response, err)
	}
	if got := strings.Join(remoteservice.Events(), ","); got != "policy:semantic,message:semantic" {
		t.Fatalf("semantic invocation order = %q", got)
	}

	remoteservice.ResetEvents()
	response, err = invocations.MessageSendV1().Invoke(context.Background(), messagecontract.Request{Recipient: "panic", Mode: messagecontract.RequestMode("safe"), Enabled: true, Labels: []string{}, Metadata: map[string]any{}})
	var boundary *kernelinvocation.Error
	if !errors.As(err, &boundary) || boundary.Code() != kernelinvocation.ErrorInternal || boundary.DetailCode() != "provider.panic_recovered" || response != (messagecontract.Response{}) || strings.Contains(fmt.Sprint(err), "runtime-private") {
		t.Fatalf("panic boundary = response %#v, error %v", response, err)
	}
	if got := strings.Join(remoteservice.Events(), ","); got != "policy:panic,message:panic" {
		t.Fatalf("panic invocation order = %q", got)
	}
}
`
