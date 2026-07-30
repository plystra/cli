package interfaceprovenance_test

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfaceprovenance"
)

func TestProvenanceCanonicalizesCompleteInterfaceAndConstructorGraph(t *testing.T) {
	t.Parallel()

	input := completeInput()
	first, err := interfaceprovenance.New(input)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !first.Valid() || first.SchemaVersion() != interfaceprovenance.Schema {
		t.Fatalf("constructed provenance is invalid: schema %q digest %q", first.SchemaVersion(), first.Digest())
	}
	const expectedDigest = "sha256:5bebddaa40054144aa607784144e7ff61dfe09e4b9cfdbf49310e9ef30438f24"
	if first.Digest() != expectedDigest {
		t.Fatalf("digest = %q; want %q", first.Digest(), expectedDigest)
	}

	reordered := completeInput()
	slices.Reverse(reordered.Interfaces)
	slices.Reverse(reordered.Bindings)
	slices.Reverse(reordered.Constructors)
	slices.Reverse(reordered.Intrinsics)
	for index := range reordered.Bindings {
		slices.Reverse(reordered.Bindings[index].RootSources)
		slices.Reverse(reordered.Bindings[index].Selection.Sources)
	}
	for index := range reordered.Constructors {
		slices.Reverse(reordered.Constructors[index].Provides)
		slices.Reverse(reordered.Constructors[index].ConfigurationSources)
		slices.Reverse(reordered.Constructors[index].Dependencies)
	}
	second, err := interfaceprovenance.New(reordered)
	if err != nil {
		t.Fatalf("New(reordered): %v", err)
	}
	if !bytes.Equal(first.RecordJSON(), second.RecordJSON()) || first.Digest() != second.Digest() {
		t.Fatalf("input order changed provenance:\n%s\n%s", first.RecordJSON(), second.RecordJSON())
	}

	decoded, err := interfaceprovenance.Decode(first.RecordJSON())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !decoded.Valid() || !bytes.Equal(decoded.RecordJSON(), first.RecordJSON()) {
		t.Fatal("decoded provenance did not preserve the canonical record")
	}
	if got := decoded.Interfaces(); len(got) != 2 ||
		got[0].ID() != "audit.write/v1" ||
		got[1].ID() != "order.create/v1" ||
		got[1].ModulePath() != "example.com/contracts" ||
		got[1].ShapeDigest() != digest("2") {
		t.Fatalf("Interfaces = %#v", got)
	}
	bindings := decoded.Bindings()
	if len(bindings) != 2 ||
		bindings[0].InterfaceID() != "audit.write/v1" ||
		!slices.Equal(bindings[0].RequiringConstructors(), []string{"example.com/app/order.New"}) ||
		bindings[1].Selection().Constructor() != "example.com/app/order.New" ||
		bindings[1].Selection().ConstructionOrder() != 2 ||
		bindings[1].ConfigurationOwner() != `config["example.com/app/order.New"]` ||
		bindings[1].Policy().Timeout() != "5s" ||
		bindings[1].Mappings().ConnectProcedure() != "/plystra.generated.order.create.v1.OrderCreateV1Service/Invoke" ||
		bindings[1].Mappings().JavaScriptModulePath() != "generated/sdk/javascript/src/interfaces/order/create/v1.ts" {
		t.Fatalf("Bindings = %#v", bindings)
	}
	constructors := decoded.Constructors()
	if len(constructors) != 2 ||
		constructors[0].Symbol() != "example.com/app/audit.New" ||
		constructors[1].Symbol() != "example.com/app/order.New" ||
		len(constructors[1].Dependencies()) != 2 ||
		constructors[1].Dependencies()[0].SelectedConstructor() != "example.com/app/audit.New" ||
		constructors[1].Dependencies()[1].Available() {
		t.Fatalf("Constructors = %#v", constructors)
	}
	intrinsics := decoded.Intrinsics()
	if len(intrinsics) != 1 ||
		intrinsics[0].Interface().ID() != "kernel.health/v1" ||
		intrinsics[0].Mappings().AdapterPath() != "" ||
		intrinsics[0].Mappings().ProxyPath() != "generated/go/proxies/kernel/health/v1/proxy_gen.go" {
		t.Fatalf("Intrinsics = %#v", intrinsics)
	}

	rootSources := bindings[1].RootSources()
	rootSources[0] = "mutated"
	if decoded.Bindings()[1].RootSources()[0] == "mutated" {
		t.Fatal("binding source accessor leaked mutable storage")
	}
	dependencies := constructors[1].Dependencies()
	dependencies[0] = interfaceprovenance.Dependency{}
	if decoded.Constructors()[1].Dependencies()[0].InterfaceID() == "" {
		t.Fatal("constructor dependency accessor leaked mutable storage")
	}
	record := decoded.RecordJSON()
	record[0] = '!'
	if decoded.RecordJSON()[0] == '!' {
		t.Fatal("record accessor leaked mutable storage")
	}
}

func TestProvenanceRejectsIncompleteAndTamperedRecords(t *testing.T) {
	t.Parallel()

	valid, err := interfaceprovenance.New(completeInput())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func(interfaceprovenance.Input) interfaceprovenance.Input
		want string
	}{
		{
			name: "unreachable binding",
			edit: func(input interfaceprovenance.Input) interfaceprovenance.Input {
				input.Bindings[0].RootSources = nil
				return input
			},
			want: "unreachable",
		},
		{
			name: "constructor order gap",
			edit: func(input interfaceprovenance.Input) interfaceprovenance.Input {
				input.Constructors[1].ConstructionOrder = 3
				input.Bindings[1].Selection.ConstructionOrder = 3
				return input
			},
			want: "contiguous",
		},
		{
			name: "binding constructor mismatch",
			edit: func(input interfaceprovenance.Input) interfaceprovenance.Input {
				input.Bindings[0].Selection.ConcreteType = "*example.com/app/order.replacement"
				return input
			},
			want: "does not match",
		},
		{
			name: "required dependency unavailable",
			edit: func(input interfaceprovenance.Input) interfaceprovenance.Input {
				input.Constructors[1].Dependencies[0].Available = false
				input.Constructors[1].Dependencies[0].SelectedConstructor = ""
				return input
			},
			want: "required dependency",
		},
		{
			name: "incomplete transport",
			edit: func(input interfaceprovenance.Input) interfaceprovenance.Input {
				input.Bindings[0].Mappings.ConnectProcedureDigest = ""
				return input
			},
			want: "transport mapping",
		},
		{
			name: "intrinsic ordinary adapter",
			edit: func(input interfaceprovenance.Input) interfaceprovenance.Input {
				input.Intrinsics[0].Mappings.AdapterPath = "generated/go/adapters/implementations/kernel/health/v1/adapter_gen.go"
				return input
			},
			want: "must not contain",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			_, err := interfaceprovenance.New(test.edit(completeInput()))
			if !errors.Is(err, interfaceprovenance.ErrInvalid) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New error = %v; want ErrInvalid containing %q", err, test.want)
			}
		})
	}

	tampered := bytes.Replace(valid.RecordJSON(), []byte(digest("9")), []byte(digest("8")), 1)
	if _, err := interfaceprovenance.Decode(tampered); !errors.Is(err, interfaceprovenance.ErrRecord) || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("Decode(tampered) error = %v", err)
	}
	unknown := bytes.Replace(valid.RecordJSON(), []byte(`"schema":`), []byte(`"unknown":true,"schema":`), 1)
	if _, err := interfaceprovenance.Decode(unknown); !errors.Is(err, interfaceprovenance.ErrRecord) || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Decode(unknown) error = %v", err)
	}
	noncanonical := bytes.Replace(valid.RecordJSON(), []byte(`"interfaces":[`), []byte(`"interfaces":null,"ignored":[`), 1)
	if _, err := interfaceprovenance.Decode(noncanonical); !errors.Is(err, interfaceprovenance.ErrRecord) {
		t.Fatalf("Decode(noncanonical) error = %v", err)
	}
	oversized := bytes.Repeat([]byte("x"), int(interfaceprovenance.MaximumBytes)+1)
	if _, err := interfaceprovenance.Decode(oversized); !errors.Is(err, interfaceprovenance.ErrRecord) {
		t.Fatalf("Decode(oversized) error = %v", err)
	}
}

func completeInput() interfaceprovenance.Input {
	auditConstructor := interfaceprovenance.ConstructorInput{
		Symbol:            "example.com/app/audit.New",
		ModulePath:        "example.com/app",
		ModuleVersion:     "local",
		Source:            "example.com/app@local/audit/audit.go:14:1",
		ConcreteType:      "*example.com/app/audit.Service",
		ConstructionOrder: 1,
		Provides:          []string{"audit.write/v1"},
		Dependencies:      []interfaceprovenance.DependencyInput{},
	}
	orderConstructor := interfaceprovenance.ConstructorInput{
		Symbol:               "example.com/app/order.New",
		ModulePath:           "example.com/app",
		ModuleVersion:        "local",
		Source:               "example.com/app@local/order/order.go:22:1",
		ConcreteType:         "*example.com/app/order.service",
		ConstructionOrder:    2,
		Provides:             []string{"order.create/v1"},
		ConfigurationOwner:   `config["example.com/app/order.New"]`,
		ConfigurationSources: []string{`plystra.yaml config["example.com/app/order.New"]`, "example.com/app@local/order/config.go:5:1 Config"},
		Dependencies: []interfaceprovenance.DependencyInput{
			{
				InterfaceID:         "audit.write/v1",
				PackagePath:         "example.com/contracts/audit/v1",
				ParameterName:       "audit",
				ParameterPosition:   2,
				Available:           true,
				SelectedConstructor: "example.com/app/audit.New",
			},
			{
				InterfaceID:       "notifications.send/v1",
				PackagePath:       "example.com/contracts/notifications/v1",
				ParameterName:     "notifications",
				ParameterPosition: 3,
				Optional:          true,
			},
		},
	}
	orderMapping := completeMapping(
		"order.create/v1",
		"/plystra.generated.order.create.v1.OrderCreateV1Service/Invoke",
	)
	return interfaceprovenance.Input{
		Interfaces: []interfaceprovenance.InterfaceInput{
			{
				ID:                  "order.create/v1",
				PackagePath:         "example.com/contracts/order/v1",
				ModulePath:          "example.com/contracts",
				ModuleVersion:       "v1.2.3",
				DirectiveSource:     "example.com/contracts@v1.2.3/order/v1/interface.go:9:1",
				MetadataSource:      "example.com/contracts@v1.2.3/order/v1/interface.yaml",
				ShapeDigest:         digest("2"),
				ContractDigest:      digest("3"),
				DocumentationDigest: digest("4"),
				ExampleDigest:       digest("5"),
			},
			{
				ID:                  "audit.write/v1",
				PackagePath:         "example.com/contracts/audit/v1",
				ModulePath:          "example.com/contracts",
				ModuleVersion:       "v1.2.3",
				DirectiveSource:     "example.com/contracts@v1.2.3/audit/v1/interface.go:7:1",
				ShapeDigest:         digest("6"),
				ContractDigest:      digest("7"),
				DocumentationDigest: digest("8"),
				ExampleDigest:       digest("9"),
			},
		},
		Bindings: []interfaceprovenance.BindingInput{
			{
				InterfaceID:           "order.create/v1",
				RootSources:           []string{`plystra.yaml interfaces.require["order.create/v1"]`, `plystra.yaml http.expose["order.create/v1"]`},
				ExposureSources:       []string{`plystra.yaml http.expose["order.create/v1"]`},
				RequiringConstructors: []string{},
				Selection: interfaceprovenance.SelectionInput{
					Constructor:       orderConstructor.Symbol,
					ModulePath:        orderConstructor.ModulePath,
					ModuleVersion:     orderConstructor.ModuleVersion,
					Source:            orderConstructor.Source,
					ConcreteType:      orderConstructor.ConcreteType,
					Reason:            interfaceprovenance.SelectionExplicit,
					Sources:           []string{`plystra.yaml interfaces.use["order.create/v1"]`, "example.com/platform@v1.0.0/plystra.yaml interfaces.use"},
					ConstructionOrder: orderConstructor.ConstructionOrder,
				},
				ConfigurationOwner: orderConstructor.ConfigurationOwner,
				Policy: interfaceprovenance.PolicyInput{
					Timeout: "5s",
					Sources: []string{`plystra.yaml interfaces.policies["order.create/v1"].timeout`},
				},
				Mappings: orderMapping,
			},
			{
				InterfaceID:           "audit.write/v1",
				RootSources:           []string{},
				ExposureSources:       []string{},
				RequiringConstructors: []string{"example.com/app/order.New"},
				Selection: interfaceprovenance.SelectionInput{
					Constructor:       auditConstructor.Symbol,
					ModulePath:        auditConstructor.ModulePath,
					ModuleVersion:     auditConstructor.ModuleVersion,
					Source:            auditConstructor.Source,
					ConcreteType:      auditConstructor.ConcreteType,
					Reason:            interfaceprovenance.SelectionUniqueCompatible,
					Sources:           []string{"unique compatible Implementation example.com/app/audit.New"},
					ConstructionOrder: auditConstructor.ConstructionOrder,
				},
				Policy: interfaceprovenance.PolicyInput{
					Timeout: "2m0s",
					Sources: []string{"built-in Plystra default Interface invocation timeout"},
				},
				Mappings: interfaceprovenance.MappingInput{
					ProxyPath:    "generated/go/proxies/audit/write/v1/proxy_gen.go",
					AdapterPath:  "generated/go/adapters/implementations/audit/write/v1/adapter_gen.go",
					AssemblyPath: "generated/go/assembly/interfaces_gen.go",
				},
			},
		},
		Constructors: []interfaceprovenance.ConstructorInput{auditConstructor, orderConstructor},
		Intrinsics: []interfaceprovenance.IntrinsicInput{
			{
				Interface: interfaceprovenance.InterfaceInput{
					ID:                  "kernel.health/v1",
					PackagePath:         "github.com/plystra/kernel/interfaces/kernel/health/v1",
					ModulePath:          "github.com/plystra/kernel",
					ModuleVersion:       "v0.0.1-rc.1",
					DirectiveSource:     "github.com/plystra/kernel@v0.0.1-rc.1/interfaces/kernel/health/v1/interface.go:5:1",
					MetadataSource:      "github.com/plystra/kernel@v0.0.1-rc.1/interfaces/kernel/health/v1/interface.yaml",
					ShapeDigest:         digest("a"),
					ContractDigest:      digest("b"),
					DocumentationDigest: digest("c"),
					ExampleDigest:       digest("d"),
				},
				RequirementSources: []string{"Kernel intrinsic inventory kernel.health/v1", `plystra.yaml http.expose["kernel.health/v1"]`},
				ExposureSources:    []string{`plystra.yaml http.expose["kernel.health/v1"]`},
				Policy: interfaceprovenance.PolicyInput{
					Timeout: "2m0s",
					Sources: []string{"built-in Plystra default Interface invocation timeout"},
				},
				Mappings: intrinsicMapping(),
			},
		},
	}
}

func completeMapping(identifier, procedure string) interfaceprovenance.MappingInput {
	segments := strings.ReplaceAll(strings.TrimSuffix(identifier, "/v1"), ".", "/")
	return interfaceprovenance.MappingInput{
		ProxyPath:                      "generated/go/proxies/" + segments + "/v1/proxy_gen.go",
		AdapterPath:                    "generated/go/adapters/implementations/" + segments + "/v1/adapter_gen.go",
		AssemblyPath:                   "generated/go/assembly/interfaces_gen.go",
		ProtobufSchemaPath:             "generated/proto/plystra/generated/" + segments + "/v1/interface.proto",
		ProtobufDescriptorSetPath:      "generated/proto/descriptor-set.pb",
		ProtobufDescriptorDigest:       digest("e"),
		WireMapPath:                    "generated/proto/wire-map.json",
		WireMapDigest:                  digest("f"),
		ConnectHandlerPath:             "generated/go/adapters/connect/" + segments + "/v1/handler_gen.go",
		ConnectProcedure:               procedure,
		ConnectProcedureDigest:         digest("1"),
		HTTPRoute:                      procedure,
		JavaScriptModulePath:           "generated/sdk/javascript/src/interfaces/" + segments + "/v1.ts",
		JavaScriptSurfaceDigest:        digest("2"),
		JavaScriptTypesDigest:          digest("3"),
		JavaScriptSemanticErrorsDigest: digest("4"),
	}
}

func intrinsicMapping() interfaceprovenance.MappingInput {
	mapping := completeMapping(
		"kernel.health/v1",
		"/plystra.generated.kernel.health.v1.KernelHealthV1Service/Invoke",
	)
	mapping.AdapterPath = ""
	return mapping
}

func digest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
