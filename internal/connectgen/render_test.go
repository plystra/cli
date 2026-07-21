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
	testModulePath      = "example.com/acme/project"
	connectContractBody = `id: customer.profile.sync/v1
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
errors: [temporarily_unavailable]
extensions:
  policy: {credential: authorization}
`
	querySemanticsYAML = `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`
	commandSemanticsYAML = `semantics:
  kind: command
  effects: external-write
  idempotency: {mode: none}
  retry: {safety: never}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`
	connectContract        = connectContractBody + querySemanticsYAML
	commandConnectContract = connectContractBody + commandSemanticsYAML
)

func TestRenderEmitsDeterministicCanonicalAndAliasHandlers(t *testing.T) {
	t.Parallel()

	fixture := buildFixture(t, connectContract, "account.profile/v1")
	operations := fixture.model.Operations()
	if len(operations) != 1 || operations[0].Kind() != capabilitymeta.CapabilityKindQuery {
		t.Fatalf("Connect fixture operations = %#v", operations)
	}
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
		"connect.WithCodec(connectschema.BinaryCodec{})",
		"connect.WithCodec(connectschema.JSONCodec{})",
		"connect.WithReadMaxBytes(connectschema.MaximumRequestBytes)",
		"connect.WithSendMaxBytes(connectschema.MaximumResponseBytes)",
		"connect.WithRequireConnectProtocolHeader()",
		"plystraServeConnectOnly(writer, request, h.transport)",
		`const plystraConnectAcceptPost = "application/json, application/proto"`,
		"return target.Invoke(ctx, request)",
		"connectschema.DecodeStruct(",
		"connectschema.EncodeStruct(",
		"connectschema.ValidateMessage(message.ProtoReflect())",
		"connectschema.Message(plystraErrorDetailType)",
		"applicationinvocation.SafeTransportError(err)",
		"func (h Handler) InvokeRequested(",
		"connect.NewErrorDetail(message)",
		`const CapabilityID = "customer.profile.sync/v1"`,
		`const plystraErrorDetailType = "plystra.generated.transport.v1.PlystraErrorDetail"`,
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
		"target.InvokeRequested",
		`const CapabilityID = "account.profile/v1"`,
		"connect.NewUnaryHandler(",
		"connect.WithCodec(connectschema.BinaryCodec{})",
		"connect.WithCodec(connectschema.JSONCodec{})",
		"connect.WithReadMaxBytes(connectschema.MaximumRequestBytes)",
		"connect.WithSendMaxBytes(connectschema.MaximumResponseBytes)",
		"connect.WithRequireConnectProtocolHeader()",
		"plystraServeConnectOnly(writer, request, h.transport)",
	} {
		if !bytes.Contains(alias.Data(), []byte(required)) {
			t.Fatalf("Alias handler omits %q:\n%s", required, alias.Data())
		}
	}
	schema := byPath[wantPaths[2]]
	for _, required := range []string{
		"MaximumRequestBytes",
		"MaximumDecodeDepth",
		"MaximumDecodedNodes",
		"MaximumResponseBytes",
		"MaximumEncodeDepth",
		"MaximumEncodedNodes",
		"MaximumJSONRequestBytes",
		"MaximumJSONDecodeDepth",
		"MaximumJSONDecodedTokens",
		"MaximumJSONResponseBytes",
		"type BinaryCodec struct{}",
		"type JSONCodec struct{}",
		"(proto.MarshalOptions{Deterministic: true}).Marshal(message)",
		"protojson.UnmarshalOptions{DiscardUnknown: false, RecursionLimit: MaximumJSONDecodeDepth}",
		"func plystraValidateJSON(data []byte) error",
		"func plystraValidScalar(field protoreflect.FieldDescriptor, value protoreflect.Value) bool",
		"RecursionLimit: MaximumDecodeDepth",
		"func ValidateMessage(message protoreflect.Message)",
		"func ValidateResponseMessage(message protoreflect.Message)",
		"type ResponseEncodingBudget struct",
		"func Message(name protoreflect.FullName)",
		"len(message.GetUnknown()) != 0",
	} {
		if !bytes.Contains(schema.Data(), []byte(required)) {
			t.Fatalf("Connect schema runtime omits %q:\n%s", required, schema.Data())
		}
	}
	for _, forbidden := range []string{"applicationinvocation", "generated/go/invocation", "generated/go/providers", "github.com/plystra/kernel"} {
		if bytes.Contains(alias.Data(), []byte(forbidden)) {
			t.Fatalf("Alias handler contains forbidden independent target %q:\n%s", forbidden, alias.Data())
		}
	}
	if bytes.Contains(alias.Data(), []byte("\t\ttarget.Invoke,")) {
		t.Fatalf("Alias handler loses the requested Alias identity through a direct canonical method value:\n%s", alias.Data())
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
	var missingErrorDetail descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(fixture.descriptorSet, &missingErrorDetail); err != nil {
		t.Fatalf("Unmarshal descriptor set for safe-detail removal: %v", err)
	}
	missingErrorDetail.File = slices.DeleteFunc(missingErrorDetail.File, func(file *descriptorpb.FileDescriptorProto) bool {
		return file.GetName() == protobufdescriptor.ErrorDetailFileName
	})
	withoutErrorDetail, err := (proto.MarshalOptions{Deterministic: true}).Marshal(&missingErrorDetail)
	if err != nil {
		t.Fatalf("Marshal descriptor set without safe detail: %v", err)
	}
	if files, err := connectgen.Render(testModulePath, fixture.model, fixture.wireMap, withoutErrorDetail, fixture.plan, provenance); len(files) != 0 || !errors.Is(err, connectgen.ErrDescriptor) || !strings.Contains(err.Error(), "safe error detail") {
		t.Fatalf("Render(missing safe error detail) = %#v, %v", files, err)
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

func TestGeneratedUnaryQueryAndCommandHandlersInvokeOneCanonicalTarget(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		kind   capabilitymeta.CapabilityKind
	}{
		{name: "query", source: connectContract, kind: capabilitymeta.CapabilityKindQuery},
		{name: "command", source: commandConnectContract, kind: capabilitymeta.CapabilityKindCommand},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := buildFixture(t, test.source, "account.profile/v1")
			operations := fixture.model.Operations()
			if len(operations) != 1 || operations[0].Kind() != test.kind {
				t.Fatalf("generated handler target operations = %#v", operations)
			}
			files, err := connectgen.Render(testModulePath, fixture.model, fixture.wireMap, fixture.descriptorSet, fixture.plan, connectConfigurationProvenance(t, generation.ConfigurationModeDefault))
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			contract, err := contractgen.Render([]byte(test.source))
			if err != nil {
				t.Fatalf("Render contract: %v", err)
			}
			invocation, err := invocationgen.Render(testModulePath, []byte(test.source))
			if err != nil {
				t.Fatalf("Render invocation: %v", err)
			}
			assertGeneratedHandlersRun(t, contract, invocation, files, test.kind == capabilitymeta.CapabilityKindQuery)
		})
	}
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
	if !strings.Contains(source, "\nsemantics:") {
		if !strings.HasSuffix(source, "\n") {
			source += "\n"
		}
		source += querySemanticsYAML
	}
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

func assertGeneratedHandlersRun(t testing.TB, contract contractgen.File, invocation invocationgen.File, handlers []connectgen.File, runFuzz bool) {
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
	download := exec.CommandContext(t.Context(), "go", "mod", "download", "all")
	download.Dir = root
	download.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	if output, err := download.CombinedOutput(); err != nil {
		t.Fatalf("download generated Connect module dependencies: %v\n%s", err, output)
	}
	command := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run generated Connect module: %v\n%s", err, output)
	}
	if runFuzz {
		fuzz := exec.CommandContext(t.Context(), "go", "test", "-run=^$", "-fuzz=^FuzzJSONCodecNeverPanics$", "-fuzztime=100x", "./generated/go/adapters/connect/customer/profile/sync/v1")
		fuzz.Dir = root
		fuzz.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
		if output, err := fuzz.CombinedOutput(); err != nil {
			t.Fatalf("fuzz generated Connect JSON codec: %v\n%s", err, output)
		}
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

import (
	"context"
	"strings"
)

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

type ErrorCode string

const (
	ErrorInvalidArgument ErrorCode = "invalid_argument"
	ErrorNotFound ErrorCode = "not_found"
	ErrorConflict ErrorCode = "conflict"
	ErrorDenied ErrorCode = "denied"
	ErrorUnauthenticated ErrorCode = "unauthenticated"
	ErrorUnavailable ErrorCode = "unavailable"
	ErrorTimeout ErrorCode = "timeout"
	ErrorCancelled ErrorCode = "cancelled"
	ErrorResultUnknown ErrorCode = "result_unknown"
	ErrorInternal ErrorCode = "internal"
	ErrorVersionIncompatible ErrorCode = "version_incompatible"
)

func (c ErrorCode) String() string { return string(c) }
func (c ErrorCode) Valid() bool {
	switch c {
	case ErrorInvalidArgument, ErrorNotFound, ErrorConflict, ErrorDenied, ErrorUnauthenticated, ErrorUnavailable, ErrorTimeout, ErrorCancelled, ErrorResultUnknown, ErrorInternal, ErrorVersionIncompatible:
		return true
	default:
		return false
	}
}

func ValidDetailCode(value string) bool {
	return value == "" || len(value) <= 128 && !strings.ContainsAny(value, " \r\n\x00")
}

type Error struct {
	code ErrorCode
	detail string
}

func NewError(code ErrorCode, detail string) *Error { return &Error{code: code, detail: detail} }
func (e *Error) Error() string { return "invocation error" }
func (e *Error) Code() ErrorCode { if e == nil { return "" }; return e.code }
func (e *Error) DetailCode() string { if e == nil { return "" }; return e.detail }
`

const generatedConnectRuntimeTest = `package customerprofilesyncv1_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestCanonicalAndAliasConnectInvocation(t *testing.T) {
	calls := 0
	rootCalls := 0
	target := kernelinvocation.NewTestHandle(func(_ context.Context, request contract.Request) (contract.Response, error) {
		calls++
		if request.Note != nil {
			switch *request.Note {
			case "provider-error":
				return contract.Response{}, errors.New("provider secret must not cross the Connect boundary")
			case "semantic-error":
				return contract.Response{}, contract.ErrTemporarilyUnavailable
			case "kernel-invalid-argument":
				return contract.Response{}, kernelinvocation.NewError(kernelinvocation.ErrorInvalidArgument, "contract.invalid_request")
			case "kernel-not-found":
				return contract.Response{}, kernelinvocation.NewError(kernelinvocation.ErrorNotFound, "resource.not_found")
			case "kernel-conflict":
				return contract.Response{}, kernelinvocation.NewError(kernelinvocation.ErrorConflict, "resource.conflict")
			case "kernel-denied":
				return contract.Response{}, kernelinvocation.NewError(kernelinvocation.ErrorDenied, "authorization.denied")
			case "kernel-unauthenticated":
				return contract.Response{}, kernelinvocation.NewError(kernelinvocation.ErrorUnauthenticated, "authentication.required")
			case "kernel-unavailable":
				return contract.Response{}, kernelinvocation.NewError(kernelinvocation.ErrorUnavailable, "runtime.unavailable")
			case "kernel-timeout":
				return contract.Response{}, kernelinvocation.NewError(kernelinvocation.ErrorTimeout, "runtime.timeout")
			case "kernel-cancelled":
				return contract.Response{}, kernelinvocation.NewError(kernelinvocation.ErrorCancelled, "runtime.cancelled")
			case "kernel-result-unknown":
				return contract.Response{}, kernelinvocation.NewError(kernelinvocation.ErrorResultUnknown, "runtime.result_unknown")
			case "kernel-internal":
				return contract.Response{}, kernelinvocation.NewError(kernelinvocation.ErrorInternal, "runtime.internal")
			case "kernel-version-incompatible":
				return contract.Response{}, kernelinvocation.NewError(kernelinvocation.ErrorVersionIncompatible, "runtime.version_incompatible")
			}
		}
		noteValue := ""
		expectedSource := "optional-null"
		expectedActive := true
		expectedCount := int64(42)
		expectedRatio := 1.5
		if request.Note != nil {
			noteValue = *request.Note
			expectedSource = "browser"
			switch noteValue {
			case "":
				expectedSource = "defaults"
				expectedActive = false
				expectedCount = 0
				expectedRatio = 0
			case "integer-max":
				expectedSource = "integer-max"
				expectedCount = 9223372036854775807
			case "hello", "oversized-response", "oversized-json-response", "excessive-response-depth", "excessive-response-nodes", "non-finite-response":
			default:
				t.Fatalf("canonical request note = %q", noteValue)
			}
		}
		if request.Active != expectedActive || request.Count == nil || *request.Count != expectedCount || request.Ratio != expectedRatio || request.State != contract.RequestStateReady || len(request.Tags) != 2 || request.Tags[1] != "two" || request.Metadata["source"] != expectedSource || len(request.Records) != 1 || request.Records[0]["id"] != "record-1" {
			t.Fatalf("canonical request = %#v", request)
		}
		note := "accepted"
		response := contract.Response{
			Accepted: true,
			Count: *request.Count,
			Metadata: map[string]any{"source": "canonical"},
			Note: &note,
			Ratio: request.Ratio,
			Records: []map[string]any{{"id": "record-1"}},
			State: contract.ResponseStateBlocked,
			Tags: append([]string(nil), request.Tags...),
		}
		switch noteValue {
		case "oversized-response":
			oversized := strings.Repeat("x", connectschema.MaximumResponseBytes)
			response.Note = &oversized
		case "oversized-json-response":
			oversized := strings.Repeat("\n", connectschema.MaximumResponseBytes/2)
			response.Note = &oversized
		case "excessive-response-depth":
			response.Metadata = nestedObject(40)
		case "excessive-response-nodes":
			response.Tags = make([]string, connectschema.MaximumEncodedNodes)
		case "non-finite-response":
			response.Ratio = math.NaN()
		}
		return response, nil
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
	directResponse, err := canonical.Invoke(t.Context(), directRequest)
	if err != nil {
		t.Fatalf("direct canonical Invoke: %v", err)
	} else {
		assertResponse(t, directResponse.Msg)
	}
	codec := connectschema.BinaryCodec{}
	firstEncoding, err := codec.Marshal(directResponse.Msg)
	if err != nil {
		t.Fatalf("Marshal direct response: %v", err)
	}
	secondEncoding, err := codec.Marshal(directResponse.Msg)
	if err != nil || !bytes.Equal(firstEncoding, secondEncoding) {
		t.Fatalf("binary response encoding is not deterministic: %v", err)
	}
	jsonCodec := connectschema.JSONCodec{}
	jsonEncoding, err := jsonCodec.Marshal(directResponse.Msg)
	if err != nil {
		t.Fatalf("Marshal direct JSON response: %v", err)
	}
	jsonRoundTrip := dynamicpb.NewMessage(directMethod.Output())
	if err := jsonCodec.Unmarshal(jsonEncoding, jsonRoundTrip); err != nil || !proto.Equal(directResponse.Msg, jsonRoundTrip) {
		t.Fatalf("JSON response round trip = %v, equal %t", err, proto.Equal(directResponse.Msg, jsonRoundTrip))
	}
	invalidResponses := map[string]any{
		"wrong message type": struct{}{},
		"nested unknown field": withNestedUnknown(proto.Clone(directResponse.Msg).(*dynamicpb.Message)),
		"invalid UTF-8": withString(t, proto.Clone(directResponse.Msg).(*dynamicpb.Message), "note", string([]byte{0xff})),
		"oversized message": withString(t, proto.Clone(directResponse.Msg).(*dynamicpb.Message), "note", strings.Repeat("x", connectschema.MaximumResponseBytes)),
		"excessive nesting": withExcessiveResponseNesting(t, proto.Clone(directResponse.Msg).(*dynamicpb.Message)),
		"excessive nodes": withExcessiveNodes(t, proto.Clone(directResponse.Msg).(*dynamicpb.Message)),
	}
	for name, invalid := range invalidResponses {
		t.Run("binary response codec rejects "+name, func(t *testing.T) {
			if encoded, err := codec.Marshal(invalid); err == nil || encoded != nil {
				t.Fatalf("Marshal(%s) = %d bytes, %v", name, len(encoded), err)
			}
		})
		t.Run("JSON response codec rejects "+name, func(t *testing.T) {
			if encoded, err := jsonCodec.Marshal(invalid); err == nil || encoded != nil {
				t.Fatalf("Marshal(%s) = %d bytes, %v", name, len(encoded), err)
			}
		})
	}
	cyclic := map[string]any{}
	cyclic["cycle"] = cyclic
	if err := connectschema.NewResponseEncodingBudget().ValidateObject(cyclic); err == nil {
		t.Fatal("cyclic response object passed bounded validation")
	}
	directInvalid := connect.NewRequest(withNestedUnknown(validRequest(t, directMethod, "hello")))
	directInvalid.Header().Set("Authorization", "Bearer test")
	_, err = canonical.Invoke(t.Context(), directInvalid)
	assertSafeConnectError(t, err, connect.CodeInvalidArgument, "customer.profile.sync/v1", "", "invalid_argument")
	directInvalidUTF8 := connect.NewRequest(withString(t, validRequest(t, directMethod, "hello"), "note", string([]byte{0xff})))
	directInvalidUTF8.Header().Set("Authorization", "Bearer test")
	_, err = canonical.Invoke(t.Context(), directInvalidUTF8)
	assertSafeConnectError(t, err, connect.CodeInvalidArgument, "customer.profile.sync/v1", "", "invalid_argument")
	if calls != 1 || rootCalls != 1 {
		t.Fatalf("direct invalid request crossed the boundary: calls %d/%d", calls, rootCalls)
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
		"unknown nested binary field": withNestedUnknown(validRequest(t, method, "hello")),
		"invalid enum sentinel": withEnumNumber(t, validRequest(t, method, "hello"), "state", 0),
		"non-finite number": withNumber(t, validRequest(t, method, "hello"), "ratio", math.NaN()),
		"non-finite object number": withObject(t, validRequest(t, method, "hello"), "metadata", map[string]any{"invalid": math.Inf(1)}),
		"excessive nesting": withExcessiveNesting(t, validRequest(t, method, "hello")),
		"excessive decoded nodes": withExcessiveNodes(t, validRequest(t, method, "hello")),
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
	validPayload, err := proto.Marshal(validRequest(t, method, "hello"))
	if err != nil {
		t.Fatalf("marshal valid request: %v", err)
	}
	for _, route := range routes {
		for _, malformed := range []struct {
			name string
			payload []byte
		}{
			{name: "truncated varint", payload: []byte{0x80}},
			{name: "truncated message", payload: validPayload[:len(validPayload)-1]},
		} {
			t.Run(route.name+" rejects "+malformed.name, func(t *testing.T) {
				request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+route.procedure, bytes.NewReader(malformed.payload))
				if err != nil {
					t.Fatalf("NewRequest: %v", err)
				}
				request.Header.Set("Content-Type", "application/proto")
				request.Header.Set("Connect-Protocol-Version", "1")
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
		}
		method := mustMethod(t, route.methodName)
		client := connect.NewClient[dynamicpb.Message, dynamicpb.Message](server.Client(), server.URL+route.procedure, connect.WithSchema(method), connect.WithResponseInitializer(connectschema.InitializeDynamicMessage))
		request := connect.NewRequest(validRequest(t, method, strings.Repeat("x", connectschema.MaximumRequestBytes)))
		request.Header().Set("Authorization", "Bearer test")
		if _, err := client.CallUnary(t.Context(), request); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("%s oversized request error = %v", route.name, err)
		}
	}
	if calls != 5 || rootCalls != 5 {
		t.Fatalf("malformed or oversized binary requests crossed the boundary: calls %d/%d", calls, rootCalls)
	}
	for _, route := range routes {
		method := mustMethod(t, route.methodName)
		payload, err := protojson.Marshal(validRequest(t, method, "hello"))
		if err != nil {
			t.Fatalf("Marshal JSON request: %v", err)
		}
		payload = compactJSON(t, payload)
		state := method.Input().Fields().ByName("state")
		if state == nil || state.Enum().Values().ByNumber(0) == nil || state.Enum().Values().ByNumber(2) == nil {
			t.Fatal("request state enum is incomplete")
		}
		readyState := string(state.Enum().Values().ByNumber(2).Name())
		unspecifiedState := string(state.Enum().Values().ByNumber(0).Name())
		invalidJSON := map[string][]byte{
			"empty body": nil,
			"malformed document": []byte("{\"active\":"),
			"trailing document": append(append([]byte(nil), payload...), []byte("{}")...),
			"duplicate field": addJSONField(t, payload, "\"active\":true"),
			"unknown top-level field": addJSONField(t, payload, "\"future\":true"),
			"unknown nested field": replaceJSON(t, payload, "\"tags\":{\"values\":[\"one\",\"two\"]}", "\"tags\":{\"future\":true,\"values\":[\"one\",\"two\"]}"),
			"required null": replaceJSON(t, payload, "\"active\":true", "\"active\":null"),
			"enum sentinel": replaceJSON(t, payload, "\"state\":\""+readyState+"\"", "\"state\":\""+unspecifiedState+"\""),
			"non-finite number": replaceJSON(t, payload, "\"ratio\":1.5", "\"ratio\":\"NaN\""),
			"invalid UTF-8": replaceJSONBytes(t, payload, []byte("\"note\":\"hello\""), []byte{'"', 'n', 'o', 't', 'e', '"', ':', '"', 0xff, '"'}),
			"excessive nesting": replaceJSON(t, payload, "\"metadata\":{\"source\":\"browser\"}", "\"metadata\":"+nestedJSON(connectschema.MaximumJSONDecodeDepth+1)),
			"excessive nodes": replaceJSON(t, payload, "\"tags\":{\"values\":[\"one\",\"two\"]}", "\"tags\":{\"values\":["+repeatedJSONStrings(connectschema.MaximumJSONDecodedTokens)+"]}"),
		}
		for name, invalid := range invalidJSON {
			t.Run(route.name+" rejects JSON "+name, func(t *testing.T) {
				status, _ := postJSON(t, server.Client(), server.URL+route.procedure, invalid)
				if status != http.StatusBadRequest {
					t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
				}
			})
		}
		jsonClient := connect.NewClient[dynamicpb.Message, dynamicpb.Message](server.Client(), server.URL+route.procedure, connect.WithSchema(method), connect.WithResponseInitializer(connectschema.InitializeDynamicMessage), connect.WithProtoJSON())
		oversized := connect.NewRequest(validRequest(t, method, strings.Repeat("x", connectschema.MaximumJSONRequestBytes)))
		oversized.Header().Set("Authorization", "Bearer test")
		if _, err := jsonClient.CallUnary(t.Context(), oversized); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("%s oversized JSON request error = %v", route.name, err)
		}
	}
	if calls != 5 || rootCalls != 5 {
		t.Fatalf("invalid JSON requests crossed the boundary: calls %d/%d", calls, rootCalls)
	}
	request := connect.NewRequest(validRequest(t, method, "provider-error"))
	request.Header().Set("Authorization", "Bearer test")
	_, err = client.CallUnary(t.Context(), request)
	if connect.CodeOf(err) != connect.CodeInternal || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe Provider error = %v", err)
	}
	directOversized := connect.NewRequest(validRequest(t, method, "oversized-response"))
	directOversized.Header().Set("Authorization", "Bearer test")
	if response, err := canonical.Invoke(t.Context(), directOversized); connect.CodeOf(err) != connect.CodeInternal || response != nil {
		t.Fatalf("direct oversized response = %#v, %v", response, err)
	}
	for _, route := range routes {
		method := mustMethod(t, route.methodName)
		for _, encoding := range []struct {
			name string
			option connect.ClientOption
		}{
			{name: "binary"},
			{name: "JSON", option: connect.WithProtoJSON()},
		} {
			options := []connect.ClientOption{connect.WithSchema(method), connect.WithResponseInitializer(connectschema.InitializeDynamicMessage)}
			if encoding.option != nil {
				options = append(options, encoding.option)
			}
			client := connect.NewClient[dynamicpb.Message, dynamicpb.Message](server.Client(), server.URL+route.procedure, options...)
			for _, note := range []string{"oversized-response", "excessive-response-depth", "excessive-response-nodes", "non-finite-response"} {
				t.Run(route.name+"/"+encoding.name+" rejects "+note, func(t *testing.T) {
					request := connect.NewRequest(validRequest(t, method, note))
					request.Header().Set("Authorization", "Bearer test")
					response, err := client.CallUnary(t.Context(), request)
					if connect.CodeOf(err) != connect.CodeInternal || response != nil {
						t.Fatalf("response = %#v, %v", response, err)
					}
				})
			}
		}
	}
	if calls != 23 || rootCalls != 23 {
		t.Fatalf("response rejection calls = %d/%d, want 23/23", calls, rootCalls)
	}
	for _, route := range routes {
		method := mustMethod(t, route.methodName)
		client := connect.NewClient[dynamicpb.Message, dynamicpb.Message](server.Client(), server.URL+route.procedure, connect.WithSchema(method), connect.WithResponseInitializer(connectschema.InitializeDynamicMessage), connect.WithProtoJSON())
		request := connect.NewRequest(validRequest(t, method, "oversized-json-response"))
		request.Header().Set("Authorization", "Bearer test")
		response, err := client.CallUnary(t.Context(), request)
		if connect.CodeOf(err) != connect.CodeInternal || response != nil {
			t.Fatalf("%s oversized JSON response = %#v, %v", route.name, response, err)
		}
	}
	if calls != 25 || rootCalls != 25 {
		t.Fatalf("JSON encoding rejection calls = %d/%d, want 25/25", calls, rootCalls)
	}
	optionalNull, err := protojson.Marshal(validRequest(t, method, "hello"))
	if err != nil {
		t.Fatalf("Marshal optional-null request: %v", err)
	}
	optionalNull = compactJSON(t, optionalNull)
	optionalNull = replaceJSON(t, optionalNull, "\"note\":\"hello\"", "\"note\":null")
	optionalNull = replaceJSON(t, optionalNull, "\"metadata\":{\"source\":\"browser\"}", "\"metadata\":{\"source\":\"optional-null\"}")
	status, body := postJSON(t, server.Client(), server.URL+canonicaladapter.Procedure, optionalNull)
	if status != http.StatusOK {
		t.Fatalf("optional null status = %d, body %s", status, body)
	}
	optionalNullResponse := dynamicpb.NewMessage(method.Output())
	if err := jsonCodec.Unmarshal(body, optionalNullResponse); err != nil {
		t.Fatalf("decode optional-null response: %v", err)
	}
	assertResponse(t, optionalNullResponse)
	if calls != 26 || rootCalls != 26 {
		t.Fatalf("optional null calls = %d/%d, want 26/26", calls, rootCalls)
	}
	explicitDefaults, err := protojson.Marshal(validRequest(t, method, "hello"))
	if err != nil {
		t.Fatalf("Marshal explicit-default request: %v", err)
	}
	explicitDefaults = compactJSON(t, explicitDefaults)
	explicitDefaults = replaceJSON(t, explicitDefaults, "\"active\":true", "\"active\":false")
	explicitDefaults = replaceJSON(t, explicitDefaults, "\"count\":\"42\"", "\"count\":\"0\"")
	explicitDefaults = replaceJSON(t, explicitDefaults, "\"note\":\"hello\"", "\"note\":\"\"")
	explicitDefaults = replaceJSON(t, explicitDefaults, "\"ratio\":1.5", "\"ratio\":0")
	explicitDefaults = replaceJSON(t, explicitDefaults, "\"metadata\":{\"source\":\"browser\"}", "\"metadata\":{\"source\":\"defaults\"}")
	status, _ = postJSON(t, server.Client(), server.URL+canonicaladapter.Procedure, explicitDefaults)
	if status != http.StatusOK {
		t.Fatalf("explicit defaults status = %d", status)
	}
	integerMax, err := protojson.Marshal(validRequest(t, method, "hello"))
	if err != nil {
		t.Fatalf("Marshal full-range integer request: %v", err)
	}
	integerMax = compactJSON(t, integerMax)
	integerMax = replaceJSON(t, integerMax, "\"count\":\"42\"", "\"count\":\"9223372036854775807\"")
	integerMax = replaceJSON(t, integerMax, "\"note\":\"hello\"", "\"note\":\"integer-max\"")
	integerMax = replaceJSON(t, integerMax, "\"metadata\":{\"source\":\"browser\"}", "\"metadata\":{\"source\":\"integer-max\"}")
	status, _ = postJSON(t, server.Client(), server.URL+canonicaladapter.Procedure, integerMax)
	if status != http.StatusOK {
		t.Fatalf("full-range integer status = %d", status)
	}
	if calls != 28 || rootCalls != 28 {
		t.Fatalf("JSON presence and integer calls = %d/%d, want 28/28", calls, rootCalls)
	}

	errorCases := []struct {
		name string
		note string
		code connect.Code
		semantic string
		kernel string
	}{
		{name: "semantic", note: "semantic-error", code: connect.CodeFailedPrecondition, semantic: "temporarily_unavailable"},
		{name: "unsafe provider", note: "provider-error", code: connect.CodeInternal, kernel: "internal"},
		{name: "invalid argument", note: "kernel-invalid-argument", code: connect.CodeInvalidArgument, kernel: "invalid_argument"},
		{name: "not found", note: "kernel-not-found", code: connect.CodeNotFound, kernel: "not_found"},
		{name: "conflict", note: "kernel-conflict", code: connect.CodeAborted, kernel: "conflict"},
		{name: "denied", note: "kernel-denied", code: connect.CodePermissionDenied, kernel: "denied"},
		{name: "unauthenticated", note: "kernel-unauthenticated", code: connect.CodeUnauthenticated, kernel: "unauthenticated"},
		{name: "unavailable", note: "kernel-unavailable", code: connect.CodeUnavailable, kernel: "unavailable"},
		{name: "timeout", note: "kernel-timeout", code: connect.CodeDeadlineExceeded, kernel: "timeout"},
		{name: "cancelled", note: "kernel-cancelled", code: connect.CodeCanceled, kernel: "cancelled"},
		{name: "result unknown", note: "kernel-result-unknown", code: connect.CodeUnavailable, kernel: "result_unknown"},
		{name: "internal", note: "kernel-internal", code: connect.CodeInternal, kernel: "internal"},
		{name: "version incompatible", note: "kernel-version-incompatible", code: connect.CodeUnimplemented, kernel: "version_incompatible"},
	}
	for _, test := range errorCases {
		t.Run("safe error detail/"+test.name, func(t *testing.T) {
			request := connect.NewRequest(validRequest(t, directMethod, test.note))
			request.Header().Set("Authorization", "Bearer test")
			response, err := canonical.Invoke(t.Context(), request)
			if response != nil {
				t.Fatalf("error response = %#v", response)
			}
			assertSafeConnectError(t, err, test.code, "customer.profile.sync/v1", test.semantic, test.kernel)
		})
	}

	aliasMethod := mustMethod(t, "plystra.generated.account.profile.v1.AccountProfileV1Service.Invoke")
	aliasClient := connect.NewClient[dynamicpb.Message, dynamicpb.Message](server.Client(), server.URL+aliasadapter.Procedure, connect.WithSchema(aliasMethod), connect.WithResponseInitializer(connectschema.InitializeDynamicMessage))
	aliasRequest := connect.NewRequest(validRequest(t, aliasMethod, "semantic-error"))
	aliasRequest.Header().Set("Authorization", "Bearer test")
	aliasResponse, aliasErr := aliasClient.CallUnary(t.Context(), aliasRequest)
	if aliasResponse != nil {
		t.Fatalf("Alias error response = %#v", aliasResponse)
	}
	assertSafeConnectError(t, aliasErr, connect.CodeFailedPrecondition, "account.profile/v1", "temporarily_unavailable", "")
}

func FuzzJSONCodecNeverPanics(f *testing.F) {
	f.Add([]byte("{}"))
	f.Add([]byte("{\"active\":null}"))
	f.Add([]byte("{\"unknown\":true}"))
	f.Add([]byte("[1,2,3]"))
	f.Fuzz(func(t *testing.T, payload []byte) {
		method, err := connectschema.Method("plystra.generated.customer.profile.sync.v1.CustomerProfileSyncV1Service.Invoke")
		if err != nil {
			t.Fatalf("Method: %v", err)
		}
		message := dynamicpb.NewMessage(method.Input())
		err = (connectschema.JSONCodec{}).Unmarshal(payload, message)
		if len(payload) > connectschema.MaximumJSONRequestBytes && err == nil {
			t.Fatal("oversized fuzz payload decoded")
		}
		if err == nil && connectschema.ValidateMessage(message.ProtoReflect()) != nil {
			t.Fatal("accepted fuzz payload produced an invalid message")
		}
	})
}

func postJSON(t *testing.T, client *http.Client, url string, payload []byte) (int, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	request.Header.Set("Authorization", "Bearer test")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	return response.StatusCode, body
}

func compactJSON(t *testing.T, payload []byte) []byte {
	t.Helper()
	var result bytes.Buffer
	if err := json.Compact(&result, payload); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	return result.Bytes()
}

func addJSONField(t *testing.T, payload []byte, field string) []byte {
	t.Helper()
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) < 2 || trimmed[len(trimmed)-1] != '}' {
		t.Fatalf("JSON payload is not an object: %s", payload)
	}
	result := append([]byte(nil), trimmed[:len(trimmed)-1]...)
	result = append(result, ',')
	result = append(result, field...)
	return append(result, '}')
}

func replaceJSON(t *testing.T, payload []byte, old, replacement string) []byte {
	t.Helper()
	result := bytes.Replace(payload, []byte(old), []byte(replacement), 1)
	if bytes.Equal(result, payload) {
		t.Fatalf("JSON payload %s does not contain %s", payload, old)
	}
	return result
}

func replaceJSONBytes(t *testing.T, payload, old, replacement []byte) []byte {
	t.Helper()
	result := bytes.Replace(payload, old, replacement, 1)
	if bytes.Equal(result, payload) {
		t.Fatalf("JSON payload %s does not contain %s", payload, old)
	}
	return result
}

func nestedJSON(depth int) string {
	return strings.Repeat("{\"nested\":", depth) + "null" + strings.Repeat("}", depth)
}

func repeatedJSONStrings(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("\"\",", count), ",")
}

func mustMethod(t *testing.T, name protoreflect.FullName) protoreflect.MethodDescriptor {
	t.Helper()
	method, err := connectschema.Method(name)
	if err != nil {
		t.Fatalf("Method(%s): %v", name, err)
	}
	return method
}

func assertSafeConnectError(t *testing.T, err error, code connect.Code, requestedCapabilityID, semanticErrorCode, kernelErrorClass string) {
	t.Helper()
	var connectError *connect.Error
	if !errors.As(err, &connectError) || connectError == nil || connectError.Code() != code {
		t.Fatalf("Connect error = %#v, want code %s", err, code)
	}
	details := connectError.Details()
	if len(details) != 1 || details[0] == nil || details[0].Type() != "plystra.generated.transport.v1.PlystraErrorDetail" {
		t.Fatalf("Connect error details = %#v", details)
	}
	descriptor, descriptorErr := connectschema.Message("plystra.generated.transport.v1.PlystraErrorDetail")
	if descriptorErr != nil {
		t.Fatalf("safe error descriptor: %v", descriptorErr)
	}
	message := dynamicpb.NewMessage(descriptor)
	if decodeErr := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(details[0].Bytes(), message); decodeErr != nil {
		t.Fatalf("decode safe error detail: %v", decodeErr)
	}
	reflected := message.ProtoReflect()
	if len(reflected.GetUnknown()) != 0 {
		t.Fatalf("safe error detail contains unknown fields: %x", reflected.GetUnknown())
	}
	field := func(number protoreflect.FieldNumber) string {
		return reflected.Get(descriptor.Fields().ByNumber(number)).String()
	}
	if field(1) != requestedCapabilityID || field(2) != "customer.profile.sync/v1" || field(3) != semanticErrorCode || field(4) != kernelErrorClass || field(5) != "" {
		t.Fatalf("safe error detail = requested %q canonical %q semantic %q kernel %q trace %q", field(1), field(2), field(3), field(4), field(5))
	}
	if strings.Contains(err.Error(), "provider secret") || bytes.Contains(details[0].Bytes(), []byte("provider secret")) {
		t.Fatalf("unsafe provider text crossed the Connect boundary: %v %x", err, details[0].Bytes())
	}
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

func withNestedUnknown(message *dynamicpb.Message) *dynamicpb.Message {
	reflected := message.ProtoReflect()
	field := reflected.Descriptor().Fields().ByName("metadata")
	reflected.Get(field).Message().SetUnknown([]byte{0xa0, 0x06, 0x01})
	return message
}

func withEnumNumber(t *testing.T, message *dynamicpb.Message, name protoreflect.Name, number protoreflect.EnumNumber) *dynamicpb.Message {
	t.Helper()
	setEnum(t, message.ProtoReflect(), name, number)
	return message
}

func withString(t *testing.T, message *dynamicpb.Message, name protoreflect.Name, value string) *dynamicpb.Message {
	t.Helper()
	setScalar(t, message.ProtoReflect(), name, protoreflect.ValueOfString(value))
	return message
}

func withNumber(t *testing.T, message *dynamicpb.Message, name protoreflect.Name, value float64) *dynamicpb.Message {
	t.Helper()
	setScalar(t, message.ProtoReflect(), name, protoreflect.ValueOfFloat64(value))
	return message
}

func withObject(t *testing.T, message *dynamicpb.Message, name protoreflect.Name, value map[string]any) *dynamicpb.Message {
	t.Helper()
	setUnvalidatedObject(t, message.ProtoReflect(), name, value)
	return message
}

func withExcessiveNesting(t *testing.T, message *dynamicpb.Message) *dynamicpb.Message {
	t.Helper()
	setUnvalidatedObject(t, message.ProtoReflect(), "metadata", nestedObject(connectschema.MaximumDecodeDepth+1))
	return message
}

func withExcessiveResponseNesting(t *testing.T, message *dynamicpb.Message) *dynamicpb.Message {
	t.Helper()
	setUnvalidatedObject(t, message.ProtoReflect(), "metadata", nestedObject(connectschema.MaximumEncodeDepth/2+1))
	return message
}

func nestedObject(depth int) map[string]any {
	value := map[string]any{"leaf": "value"}
	for level := 0; level < depth; level++ {
		value = map[string]any{"nested": value}
	}
	return value
}

func setUnvalidatedObject(t *testing.T, message protoreflect.Message, name protoreflect.Name, value map[string]any) {
	t.Helper()
	field := message.Descriptor().Fields().ByName(name)
	structured, err := structpb.NewStruct(value)
	if err != nil {
		t.Fatalf("NewStruct(%s): %v", name, err)
	}
	data, err := proto.Marshal(structured)
	if err != nil {
		t.Fatalf("Marshal(%s): %v", name, err)
	}
	dynamic := dynamicpb.NewMessage(field.Message())
	if err := (proto.UnmarshalOptions{RecursionLimit: connectschema.MaximumDecodeDepth * 4}).Unmarshal(data, dynamic); err != nil {
		t.Fatalf("Unmarshal(%s): %v", name, err)
	}
	message.Set(field, protoreflect.ValueOfMessage(dynamic.ProtoReflect()))
}

func withExcessiveNodes(t *testing.T, message *dynamicpb.Message) *dynamicpb.Message {
	t.Helper()
	values := make([]protoreflect.Value, connectschema.MaximumDecodedNodes)
	for index := range values {
		values[index] = protoreflect.ValueOfString("")
	}
	setScalarList(t, message.ProtoReflect(), "tags", values)
	return message
}
`
