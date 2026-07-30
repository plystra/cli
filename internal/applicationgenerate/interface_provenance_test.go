package applicationgenerate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/interfaceprovenance"
)

func TestGenerateRecordsCompleteInterfaceAndConstructorProvenance(t *testing.T) {
	const modulePath = "example.com/interface-provenance"
	root := t.TempDir()
	writeConnectApplicationModule(t, root, modulePath)
	configConstructor := writeConstructorConfigurationOwner(t, root, modulePath, true)
	writeFile(t, filepath.Join(root, "interfaces", "order", "create", "v1", "interface.go"), `package createv1

import "context"

//plystra:interface order.create/v1
type Interface interface {
	Create(context.Context, Request) (Response, error)
}

type Request struct {
	OrderID string `+"`json:\"order_id\" plystra:\"1,required\"`"+`
}

type Response struct {
	Accepted bool `+"`json:\"accepted\" plystra:\"1\"`"+`
}
`)
	writeFile(t, filepath.Join(root, "interfaces", "order", "create", "v1", "interface.yaml"), `description: Creates an order through its selected Implementation.
semantics: {kind: command}
errors:
  - code: order_rejected
    description: The order was rejected.
constraints:
  request.order_id: {min_length: 1}
examples:
  - name: accepted
    request: {order_id: ord_123}
    response: {accepted: true}
`)
	orderConstructor := modulePath + "/order.New"
	writeFile(t, filepath.Join(root, "order", "implementation.go"), `package order

import (
	"context"

	ownerv1 "example.com/interface-provenance/interfaces/configuration/owner/v1"
	createv1 "example.com/interface-provenance/interfaces/order/create/v1"
)

type Service struct {
	owner ownerv1.Interface
}

//plystra:implements order.create/v1
func New(owner ownerv1.Interface) (*Service, error) {
	return &Service{owner: owner}, nil
}

func (*Service) Create(context.Context, createv1.Request) (createv1.Response, error) {
	return createv1.Response{}, nil
}
`)
	writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  expose: [order.create/v1, kernel.health/v1]
interfaces:
  require: [order.create/v1]
  use:
    order.create/v1: example.com/interface-provenance/order.New
  policies:
    order.create/v1: {timeout: 5s}
config:
  example.com/interface-provenance/configowner.New:
    endpoint: https://private.example
    label: private-runtime-label
    password: {env: PLYSTRA_PROVENANCE_SECRET}
`)

	options := applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"PLYSTRA_PROVENANCE_SECRET": "resolved-provenance-secret"}),
		Validate:    func(context.Context, string) error { return nil },
	}
	generated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !generated.Report().Clean() {
		t.Fatalf("Generate = changes %#v, %v", generated.Report().Changes(), err)
	}
	manifestData := readFile(t, root, "generated/manifest.json")
	manifest, err := applicationgen.DecodeManifestProvenance(manifestData)
	if err != nil {
		t.Fatalf("DecodeManifestProvenance: %v", err)
	}
	provenance := manifest.InterfaceProvenance()
	if !provenance.Valid() || provenance.SchemaVersion() != interfaceprovenance.Schema {
		t.Fatalf("Interface provenance = schema %q digest %q", provenance.SchemaVersion(), provenance.Digest())
	}

	interfaces := provenance.Interfaces()
	if len(interfaces) != 2 ||
		interfaces[0].ID() != "configuration.owner/v1" ||
		interfaces[1].ID() != "order.create/v1" ||
		interfaces[0].ModulePath() != modulePath ||
		interfaces[0].ModuleVersion() != "local" ||
		interfaces[1].MetadataSource() == "" ||
		!canonicalDigest(interfaces[0].ShapeDigest()) ||
		!canonicalDigest(interfaces[1].ContractDigest()) ||
		!canonicalDigest(interfaces[1].DocumentationDigest()) ||
		!canonicalDigest(interfaces[1].ExampleDigest()) {
		t.Fatalf("authored Interface provenance = %#v", interfaces)
	}

	bindings := provenance.Bindings()
	if len(bindings) != 2 ||
		bindings[0].InterfaceID() != "configuration.owner/v1" ||
		bindings[1].InterfaceID() != "order.create/v1" {
		t.Fatalf("binding order = %#v", bindings)
	}
	configurationBinding := bindings[0]
	if len(configurationBinding.RootSources()) != 1 ||
		!strings.Contains(configurationBinding.RootSources()[0], "//plystra:implements configuration.owner/v1") ||
		!slices.Equal(configurationBinding.RequiringConstructors(), []string{orderConstructor}) ||
		configurationBinding.Selection().Constructor() != configConstructor ||
		configurationBinding.Selection().Reason() != interfaceprovenance.SelectionUniqueCompatible ||
		configurationBinding.ConfigurationOwner() != `config["example.com/interface-provenance/configowner.New"]` ||
		configurationBinding.Policy().Timeout() != "30s" {
		t.Fatalf("configuration binding = %#v", configurationBinding)
	}
	assertUnexposedOrdinaryMapping(t, configurationBinding.Mappings(), "configuration/owner/v1")

	orderBinding := bindings[1]
	if len(orderBinding.RootSources()) == 0 ||
		len(orderBinding.ExposureSources()) == 0 ||
		len(orderBinding.RequiringConstructors()) != 0 ||
		orderBinding.Selection().Constructor() != orderConstructor ||
		orderBinding.Selection().Reason() != interfaceprovenance.SelectionExplicit ||
		orderBinding.Selection().ConstructionOrder() != 2 ||
		orderBinding.Policy().Timeout() != "5s" {
		t.Fatalf("order binding = %#v", orderBinding)
	}
	assertExposedMapping(
		t,
		orderBinding.Mappings(),
		"order/create/v1",
		"/plystra.generated.order.create.v1.OrderCreateV1Service/Invoke",
		true,
	)

	constructors := provenance.Constructors()
	if len(constructors) != 2 ||
		constructors[0].Symbol() != configConstructor ||
		constructors[0].ConstructionOrder() != 1 ||
		constructors[0].ModuleVersion() != "local" ||
		!slices.Equal(constructors[0].Provides(), []string{"configuration.owner/v1"}) ||
		constructors[0].ConfigurationOwner() != configurationBinding.ConfigurationOwner() ||
		len(constructors[0].ConfigurationSources()) < 2 ||
		len(constructors[0].Dependencies()) != 0 ||
		constructors[1].Symbol() != orderConstructor ||
		constructors[1].ConstructionOrder() != 2 ||
		!slices.Equal(constructors[1].Provides(), []string{"order.create/v1"}) {
		t.Fatalf("constructor provenance = %#v", constructors)
	}
	dependencies := constructors[1].Dependencies()
	if len(dependencies) != 1 ||
		dependencies[0].InterfaceID() != "configuration.owner/v1" ||
		dependencies[0].PackagePath() != modulePath+"/interfaces/configuration/owner/v1" ||
		dependencies[0].ParameterName() != "owner" ||
		dependencies[0].ParameterPosition() != 1 ||
		dependencies[0].Optional() ||
		!dependencies[0].Available() ||
		dependencies[0].SelectedConstructor() != configConstructor {
		t.Fatalf("order constructor dependencies = %#v", dependencies)
	}

	intrinsics := provenance.Intrinsics()
	if len(intrinsics) != 2 ||
		intrinsics[0].Interface().ID() != "kernel.health/v1" ||
		intrinsics[1].Interface().ID() != "kernel.info/v1" ||
		len(intrinsics[0].ExposureSources()) == 0 ||
		len(intrinsics[1].ExposureSources()) != 0 ||
		intrinsics[0].Mappings().AdapterPath() != "" {
		t.Fatalf("intrinsic provenance = %#v", intrinsics)
	}
	assertExposedMapping(
		t,
		intrinsics[0].Mappings(),
		"kernel/health/v1",
		"/plystra.generated.kernel.health.v1.KernelHealthV1Service/Invoke",
		false,
	)
	if intrinsics[1].Mappings() != (interfaceprovenance.Mapping{}) {
		t.Fatalf("unexposed kernel.info/v1 mappings = %#v", intrinsics[1].Mappings())
	}

	rootSources := orderBinding.RootSources()
	rootSources[0] = "mutated"
	dependencies[0] = interfaceprovenance.Dependency{}
	record := provenance.RecordJSON()
	record[0] = '!'
	if provenance.Bindings()[1].RootSources()[0] == "mutated" ||
		provenance.Constructors()[1].Dependencies()[0].InterfaceID() == "" ||
		provenance.RecordJSON()[0] == '!' {
		t.Fatal("Interface provenance accessors exposed mutable storage")
	}

	for _, forbidden := range []string{
		"https://private.example",
		"private-runtime-label",
		"PLYSTRA_PROVENANCE_SECRET",
		"resolved-provenance-secret",
	} {
		if bytes.Contains(provenance.RecordJSON(), []byte(forbidden)) ||
			bytes.Contains(manifestData, []byte(forbidden)) {
			t.Fatalf("generated provenance leaked configuration or Secret material %q", forbidden)
		}
	}

	var ownership struct {
		ApplicationManifest json.RawMessage `json:"application_manifest"`
	}
	if err := json.Unmarshal(readFile(t, root, "generated/.plystra-manifest.json"), &ownership); err != nil {
		t.Fatalf("decode ownership manifest: %v", err)
	}
	recovered, err := applicationgen.DecodeManifestProvenance(ownership.ApplicationManifest)
	if err != nil ||
		recovered.InterfaceProvenance().Digest() != provenance.Digest() ||
		!bytes.Equal(recovered.InterfaceProvenance().RecordJSON(), provenance.RecordJSON()) {
		t.Fatalf("ownership Interface provenance = %#v, %v", recovered.InterfaceProvenance(), err)
	}

	options.Check = true
	checked, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !checked.Report().Clean() {
		t.Fatalf("Generate --check = changes %#v, %v", checked.Report().Changes(), err)
	}
	checkedManifest, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
	if err != nil ||
		!bytes.Equal(checkedManifest.InterfaceProvenance().RecordJSON(), provenance.RecordJSON()) {
		t.Fatalf("deterministic Interface provenance = %#v, %v", checkedManifest.InterfaceProvenance(), err)
	}
}

func assertUnexposedOrdinaryMapping(t testing.TB, mapping interfaceprovenance.Mapping, relative string) {
	t.Helper()
	if mapping.ProxyPath() != "generated/go/proxies/"+relative+"/proxy_gen.go" ||
		mapping.AdapterPath() != "generated/go/adapters/implementations/"+relative+"/adapter_gen.go" ||
		mapping.AssemblyPath() != "generated/go/assembly/interfaces_gen.go" {
		t.Fatalf("ordinary generated mapping = %#v", mapping)
	}
	if mapping.ProtobufSchemaPath() != "" ||
		mapping.ProtobufDescriptorSetPath() != "" ||
		mapping.WireMapPath() != "" ||
		mapping.ConnectHandlerPath() != "" ||
		mapping.JavaScriptModulePath() != "" {
		t.Fatalf("unexposed ordinary mapping contains public transport output = %#v", mapping)
	}
}

func assertExposedMapping(
	t testing.TB,
	mapping interfaceprovenance.Mapping,
	relative string,
	procedure string,
	ordinary bool,
) {
	t.Helper()
	if mapping.ProxyPath() != "generated/go/proxies/"+relative+"/proxy_gen.go" ||
		mapping.AssemblyPath() != "generated/go/assembly/interfaces_gen.go" ||
		mapping.ProtobufSchemaPath() != "generated/proto/plystra/generated/"+relative+"/interface.proto" ||
		mapping.ProtobufDescriptorSetPath() != "generated/proto/descriptor-set.pb" ||
		!canonicalDigest(mapping.ProtobufDescriptorDigest()) ||
		mapping.WireMapPath() != "generated/proto/wire-map.json" ||
		!canonicalDigest(mapping.WireMapDigest()) ||
		mapping.ConnectHandlerPath() != "generated/go/adapters/connect/"+relative+"/handler_gen.go" ||
		mapping.ConnectProcedure() != procedure ||
		!canonicalDigest(mapping.ConnectProcedureDigest()) ||
		mapping.HTTPRoute() != procedure ||
		mapping.JavaScriptModulePath() != "generated/sdk/javascript/src/interfaces/"+relative+".ts" ||
		!canonicalDigest(mapping.JavaScriptSurfaceDigest()) ||
		!canonicalDigest(mapping.JavaScriptTypesDigest()) ||
		!canonicalDigest(mapping.JavaScriptSemanticErrorsDigest()) {
		t.Fatalf("exposed generated mapping = %#v", mapping)
	}
	if ordinary && mapping.AdapterPath() != "generated/go/adapters/implementations/"+relative+"/adapter_gen.go" {
		t.Fatalf("ordinary exposed mapping adapter = %q", mapping.AdapterPath())
	}
	if !ordinary && mapping.AdapterPath() != "" {
		t.Fatalf("intrinsic exposed mapping contains ordinary adapter %q", mapping.AdapterPath())
	}
}

func canonicalDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && len(value) == len("sha256:")+64
}
