package connectgen_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/connectgen"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/generationlowering"
	"github.com/plystra/cli/internal/invocationgen"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/transportprovenance"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	testModulePath  = "example.com/acme/project"
	connectContract = `id: customer.profile.sync/v1
request:
  active: {type: boolean, required: true}
  count: {type: integer}
  metadata: {type: object, required: true}
  note: {type: string}
  ratio: {type: number, required: true}
  records: {type: array, items: object, required: true}
  state: {type: string, enum: [ready, blocked], required: true}
  tags: {type: array, items: string, required: true}
response:
  accepted: {type: boolean, required: true}
  count: {type: integer, required: true}
  metadata: {type: object, required: true}
  note: {type: string}
  ratio: {type: number, required: true}
  records: {type: array, items: object, required: true}
  state: {type: string, enum: [ready, blocked], required: true}
  tags: {type: array, items: string, required: true}
errors: []
extensions:
  policy: {credential: authorization}
`
)

func TestRenderEmitsDeterministicCanonicalAndAliasHandlers(t *testing.T) {
	t.Parallel()

	fixture := buildFixture(t, connectContract, "account.profile/v1")
	provenance := connectConfigurationProvenance(t, generation.ConfigurationModeDefault)
	files, err := connectgen.Render(testModulePath, fixture.model, fixture.wireMap, fixture.descriptorSet, fixture.plan, provenance)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantPaths := []string{
		"generated/go/adapters/connect/account/profile/v1/handler_gen.go",
		"generated/go/adapters/connect/customer/profile/sync/v1/handler_gen.go",
		"generated/go/internal/connectschema/schema_gen.go",
	}
	if got := connectPaths(files); !slices.Equal(got, wantPaths) {
		t.Fatalf("paths = %v, want %v", got, wantPaths)
	}
	byPath := connectFilesByPath(files)
	canonical := byPath[wantPaths[1]]
	for _, required := range []string{
		`applicationinvocation "example.com/acme/project/generated/go/invocation/customer/profile/sync/v1"`,
		"connect.NewUnaryHandler(",
		"connect.WithSchema(method)",
		"connect.WithRequestInitializer(connectschema.InitializeDynamicMessage)",
		"connect.WithRequireConnectProtocolHeader()",
		"plystraServeConnectOnly(writer, request, h.transport)",
		`const plystraConnectAcceptPost = "application/json, application/proto"`,
		"return target.Invoke(ctx, request)",
		"connectschema.DecodeStruct(",
		"connectschema.EncodeStruct(",
		"protoreflect.ValueOfEnum(",
		"dynamicpb.NewMessage(",
		"func plystraPointer[Value any](value Value) *Value",
	} {
		if !bytes.Contains(canonical.Data(), []byte(required)) {
			t.Fatalf("canonical handler omits %q:\n%s", required, canonical.Data())
		}
	}
	for _, forbidden := range []string{"generated/go/providers", "github.com/plystra/kernel", "Provider", "Dispatcher"} {
		if bytes.Contains(canonical.Data(), []byte(forbidden)) {
			t.Fatalf("canonical handler contains forbidden provider boundary %q:\n%s", forbidden, canonical.Data())
		}
	}
	alias := byPath[wantPaths[0]]
	for _, required := range []string{
		`canonicaladapter "example.com/acme/project/generated/go/adapters/connect/customer/profile/sync/v1"`,
		"target.Invoke",
		"connect.NewUnaryHandler(",
		"connect.WithRequireConnectProtocolHeader()",
		"plystraServeConnectOnly(writer, request, h.transport)",
	} {
		if !bytes.Contains(alias.Data(), []byte(required)) {
			t.Fatalf("Alias handler omits %q:\n%s", required, alias.Data())
		}
	}
	for _, forbidden := range []string{"applicationinvocation", "generated/go/invocation", "generated/go/providers", "github.com/plystra/kernel"} {
		if bytes.Contains(alias.Data(), []byte(forbidden)) {
			t.Fatalf("Alias handler contains forbidden independent target %q:\n%s", forbidden, alias.Data())
		}
	}
	for _, file := range files {
		if _, err := parser.ParseFile(token.NewFileSet(), file.Path(), file.Data(), parser.AllErrors); err != nil {
			t.Fatalf("parse %s: %v\n%s", file.Path(), err, file.Data())
		}
	}
	repeated, err := connectgen.Render(testModulePath, fixture.model, fixture.wireMap, fixture.descriptorSet, fixture.plan, provenance)
	if err != nil || !connectFilesEqual(files, repeated) {
		t.Fatalf("repeated Render drifted: %v", err)
	}
	returned := files[0].Data()
	returned[0] = 'x'
	if bytes.Equal(returned, files[0].Data()) {
		t.Fatal("File.Data exposed mutable generated source")
	}
	if connectgen.ConnectModulePath != "connectrpc.com/connect" || connectgen.ConnectModuleVersion != "v1.20.0" || connectgen.ProtobufModulePath != "google.golang.org/protobuf" || connectgen.ProtobufModuleVersion != "v1.36.11" {
		t.Fatal("generated Connect runtime dependency contract changed unexpectedly")
	}
}

func TestRenderSelectsHTTPAwareCanonicalInvocationOnlyWhenRequired(t *testing.T) {
	t.Parallel()

	fixture := buildFixture(t, connectContract, "")
	httpPlan := buildHTTPPlan(t, fixture.target)
	files, err := connectgen.Render(testModulePath, fixture.model, fixture.wireMap, fixture.descriptorSet, httpPlan, connectConfigurationProvenance(t, generation.ConfigurationModeDefault))
	if err != nil {
		t.Fatalf("Render(HTTP plan): %v", err)
	}
	source := connectFilesByPath(files)["generated/go/adapters/connect/customer/profile/sync/v1/handler_gen.go"].Data()
	for _, required := range []string{"target.InvokeHTTP(ctx, request", `plystraAdapterCredential(headers, name)`, "MaximumAdapterCredentialBytes"} {
		if !bytes.Contains(source, []byte(required)) {
			t.Fatalf("HTTP-aware handler omits %q:\n%s", required, source)
		}
	}
	if bytes.Contains(source, []byte("return target.Invoke(ctx, request)")) {
		t.Fatalf("HTTP-aware handler bypasses the contribution-aware invocation path:\n%s", source)
	}
}

func TestRenderRejectsInconsistentDescriptorAndPlanEvidence(t *testing.T) {
	t.Parallel()

	fixture := buildFixture(t, connectContract, "account.profile/v1")
	var descriptorSet descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(fixture.descriptorSet, &descriptorSet); err != nil {
		t.Fatalf("Unmarshal descriptor set: %v", err)
	}
	renamed := false
	for _, file := range descriptorSet.File {
		if file.GetPackage() != "plystra.generated.customer.profile.sync.v1" || len(file.Service) == 0 || len(file.Service[0].Method) == 0 {
			continue
		}
		file.Service[0].Method[0].Name = proto.String("Changed")
		renamed = true
	}
	if !renamed {
		t.Fatal("canonical descriptor method was absent from fixture")
	}
	inconsistent, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&descriptorSet)
	if err != nil {
		t.Fatalf("Marshal inconsistent descriptor set: %v", err)
	}
	provenance := connectConfigurationProvenance(t, generation.ConfigurationModeDefault)
	if files, err := connectgen.Render(testModulePath, fixture.model, fixture.wireMap, inconsistent, fixture.plan, provenance); len(files) != 0 || !errors.Is(err, connectgen.ErrRender) || !errors.Is(err, connectgen.ErrDescriptor) || !strings.Contains(err.Error(), "method") {
		t.Fatalf("Render(inconsistent descriptor) = %#v, %v", files, err)
	}
	otherPlan, err := generationlowering.Lower("example.com/other", []generation.NormalizedContribution{})
	if err != nil {
		t.Fatalf("Lower(other): %v", err)
	}
	if files, err := connectgen.Render(testModulePath, fixture.model, fixture.wireMap, fixture.descriptorSet, otherPlan, provenance); len(files) != 0 || !errors.Is(err, connectgen.ErrRender) || !errors.Is(err, connectgen.ErrProjection) || !strings.Contains(err.Error(), "example.com/other") {
		t.Fatalf("Render(module-drift plan) = %#v, %v", files, err)
	}
	if files, err := connectgen.Render(testModulePath, fixture.model, fixture.wireMap, []byte("not a descriptor set"), fixture.plan, provenance); len(files) != 0 || !errors.Is(err, connectgen.ErrDescriptor) {
		t.Fatalf("Render(corrupt descriptor) = %#v, %v", files, err)
	}
}

func TestRenderRequiresConfigurationProvenanceWithoutEmbeddingSelection(t *testing.T) {
	t.Parallel()

	fixture := buildFixture(t, connectContract, "account.profile/v1")
	if files, err := connectgen.Render(testModulePath, fixture.model, fixture.wireMap, fixture.descriptorSet, fixture.plan, transportprovenance.Provenance{}); len(files) != 0 || !errors.Is(err, connectgen.ErrProjection) || !strings.Contains(err.Error(), "configuration provenance") {
		t.Fatalf("Render(missing provenance) = %#v, %v", files, err)
	}
	defaultFiles, err := connectgen.Render(testModulePath, fixture.model, fixture.wireMap, fixture.descriptorSet, fixture.plan, connectConfigurationProvenance(t, generation.ConfigurationModeDefault))
	if err != nil {
		t.Fatalf("Render(default): %v", err)
	}
	environmentFiles, err := connectgen.Render(testModulePath, fixture.model, fixture.wireMap, fixture.descriptorSet, fixture.plan, connectConfigurationProvenance(t, generation.ConfigurationModeEnvironment))
	if err != nil || !connectFilesEqual(defaultFiles, environmentFiles) {
		t.Fatalf("environment selection changed equal-model Connect source: %v", err)
	}
}

func TestGeneratedCanonicalAndAliasHandlersInvokeOneCanonicalTarget(t *testing.T) {
	fixture := buildFixture(t, connectContract, "account.profile/v1")
	files, err := connectgen.Render(testModulePath, fixture.model, fixture.wireMap, fixture.descriptorSet, fixture.plan, connectConfigurationProvenance(t, generation.ConfigurationModeDefault))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	contract, err := contractgen.Render([]byte(connectContract))
	if err != nil {
		t.Fatalf("Render contract: %v", err)
	}
	invocation, err := invocationgen.Render(testModulePath, []byte(connectContract))
	if err != nil {
		t.Fatalf("Render invocation: %v", err)
	}
	assertGeneratedHandlersRun(t, contract, invocation, files)
}

type connectFixture struct {
	target        targetView
	model         protobufmodel.Model
	wireMap       protobufwiremap.Map
	descriptorSet []byte
	plan          generationlowering.Plan
}

func buildFixture(t testing.TB, source, aliasID string) connectFixture {
	t.Helper()
	target := newTarget(t, source)
	var aliases []protobufmodel.AliasView
	if aliasID != "" {
		aliases = []protobufmodel.AliasView{newAlias(t, aliasID, target)}
	}
	model, err := protobufmodel.Build(true, []protobufmodel.CanonicalTargetView{target}, aliases)
	if err != nil {
		t.Fatalf("Build Protobuf model: %v", err)
	}
	wireMap, err := protobufwiremap.Build(model, nil, false, "")
	if err != nil {
		t.Fatalf("Build wire map: %v", err)
	}
	evidence, err := protobufdescriptor.Build(model, wireMap)
	if err != nil {
		t.Fatalf("Build descriptor evidence: %v", err)
	}
	descriptorSet := evidence.DescriptorSet()
	if len(descriptorSet) == 0 {
		t.Fatal("descriptor evidence omitted the binary descriptor set")
	}
	plan, err := generationlowering.Lower(testModulePath, []generation.NormalizedContribution{})
	if err != nil {
		t.Fatalf("Lower empty plan: %v", err)
	}
	return connectFixture{target: target, model: model, wireMap: wireMap, descriptorSet: descriptorSet, plan: plan}
}

type targetView struct {
	id       generation.CapabilityID
	contract []byte
	digest   string
	sources  []string
}

func (v targetView) ID() generation.CapabilityID   { return v.id }
func (v targetView) ContractJSON() []byte          { return append([]byte(nil), v.contract...) }
func (v targetView) ContractDigest() string        { return v.digest }
func (v targetView) Sources() []string             { return append([]string(nil), v.sources...) }
func (v targetView) Exposure() generation.Exposure { return generation.Exposure{HTTP: true} }

type aliasView struct {
	id     generation.CapabilityID
	target targetView
}

func (v aliasView) ID() generation.CapabilityID     { return v.id }
func (v aliasView) Target() generation.CapabilityID { return v.target.id }
func (v aliasView) TargetContractDigest() string    { return v.target.digest }
func (v aliasView) Exposure() generation.Exposure   { return generation.Exposure{HTTP: true} }
func (v aliasView) Deprecated() string              { return "Use " + v.target.id.String() + "." }

func newTarget(t testing.TB, source string) targetView {
	t.Helper()
	canonical, err := capabilitymeta.NormalizeSchema([]byte(source))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	var identity struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(canonical, &identity); err != nil {
		t.Fatalf("decode canonical identity: %v", err)
	}
	id, err := generation.ParseCapabilityID(identity.ID)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%s): %v", identity.ID, err)
	}
	return targetView{id: id, contract: canonical, digest: digest(canonical), sources: []string{"example.com/contracts@v1/" + id.String() + "/capability.yaml"}}
}

func newAlias(t testing.TB, value string, target targetView) aliasView {
	t.Helper()
	id, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%s): %v", value, err)
	}
	return aliasView{id: id, target: target}
}

func buildHTTPPlan(t testing.TB, target targetView) generationlowering.Plan {
	t.Helper()
	context, err := generation.NewContext(generation.Input{
		Plugins: []generation.PluginInput{{
			ID:                "example.customer",
			ModulePath:        testModulePath,
			Provides:          []string{target.id.String()},
			BuildMetadataJSON: []byte("{}"),
		}},
		Capabilities: []generation.CapabilityInput{{ContractJSON: target.contract}},
		Requirements: []string{target.id.String()},
		Providers:    []generation.ProviderInput{{Capability: target.id.String(), Plugin: "example.customer"}},
	})
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	contribution := generation.Contribution{
		ID:        "policy.credential",
		Namespace: "policy",
		Source:    target.id,
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{{
			ID: "derive-credential",
			ContextDerivation: &generation.GeneratedContextDerivation{
				Key:          "policy.credential",
				Value:        generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{Source: generation.GeneratedInvocationAdapterCredential, Name: "authorization"}},
				Type:         generation.GeneratedValueString,
				Presence:     generation.GeneratedContextRequired,
				MaximumBytes: 64,
			},
		}},
	}
	output, err := generation.NormalizeOutput(context, generation.Output{Contributions: []generation.Contribution{contribution}})
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	plan, err := generationlowering.Lower(testModulePath, output.Contributions())
	if err != nil {
		t.Fatalf("Lower HTTP plan: %v", err)
	}
	if !plan.RequiresHTTPPath(target.id) {
		t.Fatal("adapter credential did not select the HTTP-aware invocation path")
	}
	return plan
}

func connectPaths(files []connectgen.File) []string {
	result := make([]string, len(files))
	for index, file := range files {
		result[index] = file.Path()
	}
	return result
}

func connectFilesByPath(files []connectgen.File) map[string]connectgen.File {
	result := make(map[string]connectgen.File, len(files))
	for _, file := range files {
		result[file.Path()] = file
	}
	return result
}

func connectFilesEqual(left, right []connectgen.File) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Path() != right[index].Path() || left[index].PackageName() != right[index].PackageName() || !bytes.Equal(left[index].Data(), right[index].Data()) {
			return false
		}
	}
	return true
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func connectConfigurationProvenance(t testing.TB, mode generation.ConfigurationMode) transportprovenance.Provenance {
	t.Helper()
	rootDigest := "sha256:" + strings.Repeat("1", 64)
	input := transportprovenance.Input{
		Mode:                        mode,
		RootPath:                    "plystra.yaml",
		RootDigest:                  rootDigest,
		SelectedPath:                "plystra.yaml",
		SelectedDigest:              rootDigest,
		DependencyCompositionDigest: "sha256:" + strings.Repeat("2", 64),
		ApplicationModelDigest:      "sha256:" + strings.Repeat("3", 64),
	}
	if mode == generation.ConfigurationModeEnvironment {
		input.Environment = "production"
		input.SelectedPath = "plystra.production.yaml"
		input.SelectedDigest = "sha256:" + strings.Repeat("4", 64)
	}
	provenance, err := transportprovenance.New(input)
	if err != nil {
		t.Fatalf("transportprovenance.New: %v", err)
	}
	return provenance
}

func assertGeneratedHandlersRun(t testing.TB, contract contractgen.File, invocation invocationgen.File, handlers []connectgen.File) {
	t.Helper()
	root := t.TempDir()
	writeGeneratedFile(t, root, contract.Path(), contract.Data())
	writeGeneratedFile(t, root, invocation.Path(), invocation.Data())
	for _, file := range handlers {
		writeGeneratedFile(t, root, file.Path(), file.Data())
	}
	writeGeneratedFile(t, root, "kernel/go.mod", []byte("module github.com/plystra/kernel\n\ngo 1.26\n"))
	writeGeneratedFile(t, root, "kernel/invocation/handle.go", []byte(testKernelInvocationSource))
	writeGeneratedFile(t, root, "generated/go/adapters/connect/customer/profile/sync/v1/handler_gen_test.go", []byte(generatedConnectRuntimeTest))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire (\n\tconnectrpc.com/connect "+connectgen.ConnectModuleVersion+"\n\tgithub.com/plystra/kernel v0.0.0\n\tgoogle.golang.org/protobuf "+connectgen.ProtobufModuleVersion+"\n)\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	goSum, err := os.ReadFile(filepath.Join(workingDirectory, "..", "..", "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeGeneratedFile(t, root, "go.sum", goSum)
	command := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run generated Connect module: %v\n%s", err, output)
	}
}

func writeGeneratedFile(t testing.TB, root, relative string, data []byte) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", name, err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

const testKernelInvocationSource = `package invocation

import "context"

type Handle[Request, Response any] struct {
	available bool
	invoke func(context.Context, Request) (Response, error)
}

func NewTestHandle[Request, Response any](invoke func(context.Context, Request) (Response, error)) Handle[Request, Response] {
	return Handle[Request, Response]{available: invoke != nil, invoke: invoke}
}

func (h Handle[Request, Response]) Available() bool { return h.available }

func (h Handle[Request, Response]) Invoke(ctx context.Context, request Request) (Response, error) {
	if h.invoke != nil {
		return h.invoke(ctx, request)
	}
	var response Response
	return response, nil
}
`

const generatedConnectRuntimeTest = `package customerprofilesyncv1_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	aliasadapter "example.com/acme/project/generated/go/adapters/connect/account/profile/v1"
	canonicaladapter "example.com/acme/project/generated/go/adapters/connect/customer/profile/sync/v1"
	contract "example.com/acme/project/generated/go/contracts/customer/profile/sync/v1"
	connectschema "example.com/acme/project/generated/go/internal/connectschema"
	applicationinvocation "example.com/acme/project/generated/go/invocation/customer/profile/sync/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestCanonicalAndAliasConnectInvocation(t *testing.T) {
	calls := 0
	rootCalls := 0
	target := kernelinvocation.NewTestHandle(func(_ context.Context, request contract.Request) (contract.Response, error) {
		calls++
		if request.Note != nil && *request.Note == "provider-error" {
			return contract.Response{}, errors.New("provider secret must not cross the Connect boundary")
		}
		if !request.Active || request.Count == nil || *request.Count != 42 || request.Ratio != 1.5 || request.State != contract.RequestStateReady || request.Note == nil || *request.Note != "hello" || len(request.Tags) != 2 || request.Tags[1] != "two" || request.Metadata["source"] != "browser" || len(request.Records) != 1 || request.Records[0]["id"] != "record-1" {
			t.Fatalf("canonical request = %#v", request)
		}
		note := "accepted"
		return contract.Response{
			Accepted: true,
			Count: *request.Count,
			Metadata: map[string]any{"source": "canonical"},
			Note: &note,
			Ratio: request.Ratio,
			Records: []map[string]any{{"id": "record-1"}},
			State: contract.ResponseStateBlocked,
			Tags: append([]string(nil), request.Tags...),
		}, nil
	})
	canonical, err := canonicaladapter.New(func(parent context.Context, headers http.Header) (context.Context, error) {
		rootCalls++
		if headers.Get("Authorization") != "Bearer test" {
			t.Fatalf("root headers = %v", headers)
		}
		return parent, nil
	}, applicationinvocation.New(target))
	if err != nil || !canonicaladapter.Available(canonical) {
		t.Fatalf("canonical New = %#v, %v", canonical, err)
	}
	alias, err := aliasadapter.New(canonical)
	if err != nil || !aliasadapter.Available(alias) {
		t.Fatalf("Alias New = %#v, %v", alias, err)
	}
	directMethod := mustMethod(t, "plystra.generated.customer.profile.sync.v1.CustomerProfileSyncV1Service.Invoke")
	directRequest := connect.NewRequest(validRequest(t, directMethod, "hello"))
	directRequest.Header().Set("Authorization", "Bearer test")
	if response, err := canonical.Invoke(t.Context(), directRequest); err != nil {
		t.Fatalf("direct canonical Invoke: %v", err)
	} else {
		assertResponse(t, response.Msg)
	}
	mux := http.NewServeMux()
	mux.Handle(canonicaladapter.Procedure, canonical)
	mux.Handle(aliasadapter.Procedure, alias)
	server := httptest.NewServer(mux)
	defer server.Close()

	routes := []struct {
		name string
		procedure string
		methodName protoreflect.FullName
	}{
		{name: "canonical", procedure: canonicaladapter.Procedure, methodName: "plystra.generated.customer.profile.sync.v1.CustomerProfileSyncV1Service.Invoke"},
		{name: "Alias", procedure: aliasadapter.Procedure, methodName: "plystra.generated.account.profile.v1.AccountProfileV1Service.Invoke"},
	}
	for _, route := range routes {
		for _, encoding := range []struct {
			name string
			option connect.ClientOption
		}{
			{name: "binary"},
			{name: "json", option: connect.WithProtoJSON()},
		} {
			t.Run(route.name+"/"+encoding.name, func(t *testing.T) {
				method := mustMethod(t, route.methodName)
				options := []connect.ClientOption{connect.WithSchema(method), connect.WithResponseInitializer(connectschema.InitializeDynamicMessage)}
				if encoding.option != nil {
					options = append(options, encoding.option)
				}
				client := connect.NewClient[dynamicpb.Message, dynamicpb.Message](server.Client(), server.URL+route.procedure, options...)
				request := connect.NewRequest(validRequest(t, method, "hello"))
				request.Header().Set("Authorization", "Bearer test")
				response, err := client.CallUnary(t.Context(), request)
				if err != nil {
					t.Fatalf("CallUnary: %v", err)
				}
				assertResponse(t, response.Msg)
			})
		}
	}
	if calls != 5 || rootCalls != 5 {
		t.Fatalf("canonical calls = %d, root calls = %d", calls, rootCalls)
	}

	for _, route := range routes {
		for _, protocol := range []struct {
			name string
			contentType string
		}{
			{name: "grpc", contentType: "application/grpc"},
			{name: "grpc-web", contentType: "application/grpc-web"},
		} {
			t.Run(route.name+" rejects "+protocol.name, func(t *testing.T) {
				request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+route.procedure, strings.NewReader("not a Connect envelope"))
				if err != nil {
					t.Fatalf("NewRequest: %v", err)
				}
				request.Header.Set("Content-Type", protocol.contentType)
				request.Header.Set("Connect-Protocol-Version", "1")
				response, err := server.Client().Do(request)
				if err != nil {
					t.Fatalf("Do: %v", err)
				}
				_ = response.Body.Close()
				if response.StatusCode != http.StatusUnsupportedMediaType {
					t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnsupportedMediaType)
				}
				acceptPost := response.Header.Get("Accept-Post")
				if acceptPost != "application/json, application/proto" || strings.Contains(strings.ToLower(acceptPost), "grpc") {
					t.Fatalf("Accept-Post = %q", acceptPost)
				}
			})
		}
	}
	if calls != 5 || rootCalls != 5 {
		t.Fatalf("rejected protocols crossed the boundary: calls %d/%d", calls, rootCalls)
	}
	t.Run("Connect protocol header is required", func(t *testing.T) {
		method := mustMethod(t, "plystra.generated.customer.profile.sync.v1.CustomerProfileSyncV1Service.Invoke")
		payload, err := protojson.Marshal(validRequest(t, method, "hello"))
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+canonicaladapter.Procedure, strings.NewReader(string(payload)))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer test")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
		}
	})
	if calls != 5 || rootCalls != 5 {
		t.Fatalf("request without the Connect protocol header crossed the boundary: calls %d/%d", calls, rootCalls)
	}
	t.Run("non-POST methods are not exposed", func(t *testing.T) {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+canonicaladapter.Procedure, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusMethodNotAllowed || response.Header.Get("Allow") != http.MethodPost {
			t.Fatalf("GET response = %d Allow=%q", response.StatusCode, response.Header.Get("Allow"))
		}
	})
	if calls != 5 || rootCalls != 5 {
		t.Fatalf("non-POST request crossed the boundary: calls %d/%d", calls, rootCalls)
	}

	method := mustMethod(t, "plystra.generated.customer.profile.sync.v1.CustomerProfileSyncV1Service.Invoke")
	client := connect.NewClient[dynamicpb.Message, dynamicpb.Message](server.Client(), server.URL+canonicaladapter.Procedure, connect.WithSchema(method), connect.WithResponseInitializer(connectschema.InitializeDynamicMessage))
	for name, message := range map[string]*dynamicpb.Message{
		"missing required fields": dynamicpb.NewMessage(method.Input()),
		"unknown binary field": withUnknown(validRequest(t, method, "hello")),
	} {
		t.Run(name, func(t *testing.T) {
			request := connect.NewRequest(message)
			request.Header().Set("Authorization", "Bearer test")
			_, err := client.CallUnary(t.Context(), request)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("CallUnary error = %v", err)
			}
		})
	}
	if calls != 5 || rootCalls != 5 {
		t.Fatalf("invalid requests crossed the boundary: calls %d/%d", calls, rootCalls)
	}
	request := connect.NewRequest(validRequest(t, method, "provider-error"))
	request.Header().Set("Authorization", "Bearer test")
	_, err = client.CallUnary(t.Context(), request)
	if connect.CodeOf(err) != connect.CodeInternal || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe Provider error = %v", err)
	}
}

func mustMethod(t *testing.T, name protoreflect.FullName) protoreflect.MethodDescriptor {
	t.Helper()
	method, err := connectschema.Method(name)
	if err != nil {
		t.Fatalf("Method(%s): %v", name, err)
	}
	return method
}

func validRequest(t *testing.T, method protoreflect.MethodDescriptor, note string) *dynamicpb.Message {
	t.Helper()
	message := dynamicpb.NewMessage(method.Input())
	reflected := message.ProtoReflect()
	setScalar(t, reflected, "active", protoreflect.ValueOfBool(true))
	setScalar(t, reflected, "count", protoreflect.ValueOfInt64(42))
	setObject(t, reflected, "metadata", map[string]any{"source": "browser"})
	setScalar(t, reflected, "note", protoreflect.ValueOfString(note))
	setScalar(t, reflected, "ratio", protoreflect.ValueOfFloat64(1.5))
	setObjectList(t, reflected, "records", []map[string]any{{"id": "record-1"}})
	setEnum(t, reflected, "state", 2)
	setScalarList(t, reflected, "tags", []protoreflect.Value{protoreflect.ValueOfString("one"), protoreflect.ValueOfString("two")})
	return message
}

func setScalar(t *testing.T, message protoreflect.Message, name protoreflect.Name, value protoreflect.Value) {
	t.Helper()
	field := message.Descriptor().Fields().ByName(name)
	if field == nil {
		t.Fatalf("field %s is absent", name)
	}
	message.Set(field, value)
}

func setEnum(t *testing.T, message protoreflect.Message, name protoreflect.Name, number protoreflect.EnumNumber) {
	t.Helper()
	field := message.Descriptor().Fields().ByName(name)
	if field == nil || field.Enum().Values().ByNumber(number) == nil {
		t.Fatalf("enum field %s has no member %d", name, number)
	}
	message.Set(field, protoreflect.ValueOfEnum(number))
}

func setObject(t *testing.T, message protoreflect.Message, name protoreflect.Name, value map[string]any) {
	t.Helper()
	field := message.Descriptor().Fields().ByName(name)
	object, err := connectschema.EncodeStruct(field.Message(), value)
	if err != nil {
		t.Fatalf("EncodeStruct(%s): %v", name, err)
	}
	message.Set(field, protoreflect.ValueOfMessage(object))
}

func setScalarList(t *testing.T, message protoreflect.Message, name protoreflect.Name, values []protoreflect.Value) {
	t.Helper()
	field := message.Descriptor().Fields().ByName(name)
	wrapper := dynamicpb.NewMessage(field.Message())
	valuesField := wrapper.Descriptor().Fields().ByName("values")
	list := wrapper.Mutable(valuesField).List()
	for _, value := range values {
		list.Append(value)
	}
	message.Set(field, protoreflect.ValueOfMessage(wrapper.ProtoReflect()))
}

func setObjectList(t *testing.T, message protoreflect.Message, name protoreflect.Name, values []map[string]any) {
	t.Helper()
	field := message.Descriptor().Fields().ByName(name)
	wrapper := dynamicpb.NewMessage(field.Message())
	valuesField := wrapper.Descriptor().Fields().ByName("values")
	list := wrapper.Mutable(valuesField).List()
	for _, value := range values {
		object, err := connectschema.EncodeStruct(valuesField.Message(), value)
		if err != nil {
			t.Fatalf("EncodeStruct(%s item): %v", name, err)
		}
		list.Append(protoreflect.ValueOfMessage(object))
	}
	message.Set(field, protoreflect.ValueOfMessage(wrapper.ProtoReflect()))
}

func assertResponse(t *testing.T, message *dynamicpb.Message) {
	t.Helper()
	reflected := message.ProtoReflect()
	if !reflected.Get(reflected.Descriptor().Fields().ByName("accepted")).Bool() || reflected.Get(reflected.Descriptor().Fields().ByName("count")).Int() != 42 || reflected.Get(reflected.Descriptor().Fields().ByName("ratio")).Float() != 1.5 || reflected.Get(reflected.Descriptor().Fields().ByName("note")).String() != "accepted" {
		t.Fatalf("scalar response = %v", message)
	}
	metadata, err := connectschema.DecodeStruct(reflected.Get(reflected.Descriptor().Fields().ByName("metadata")).Message())
	if err != nil || metadata["source"] != "canonical" {
		t.Fatalf("metadata = %#v, %v", metadata, err)
	}
	state := reflected.Get(reflected.Descriptor().Fields().ByName("state")).Enum()
	if state != 1 {
		t.Fatalf("state number = %d", state)
	}
	tagsWrapper := reflected.Get(reflected.Descriptor().Fields().ByName("tags")).Message()
	tags := tagsWrapper.Get(tagsWrapper.Descriptor().Fields().ByName("values")).List()
	if tags.Len() != 2 || tags.Get(1).String() != "two" {
		t.Fatalf("tags = %v", tags)
	}
	recordsWrapper := reflected.Get(reflected.Descriptor().Fields().ByName("records")).Message()
	records := recordsWrapper.Get(recordsWrapper.Descriptor().Fields().ByName("values")).List()
	decoded, err := connectschema.DecodeStruct(records.Get(0).Message())
	if err != nil || records.Len() != 1 || decoded["id"] != "record-1" {
		t.Fatalf("records = %#v, %v", decoded, err)
	}
}

func withUnknown(message *dynamicpb.Message) *dynamicpb.Message {
	message.SetUnknown([]byte{0xa0, 0x06, 0x01})
	return message
}
`
