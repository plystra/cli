package protobufdescriptor_test

import (
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacedecl"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestBuildWithInterfacesProjectsAuthoredMessagesIntoOneDescriptorSet(t *testing.T) {
	t.Parallel()

	input := descriptorInterfaceInput(t, `package listv1

import (
	"context"
	"time"
)

//plystra:interface records.list/v1
type Interface interface {
	List(context.Context, Request) (Response, error)
}

type Request struct {
	PageSize   int32            `+"`json:\"page_size\" plystra:\"7,required\"`"+`
	Labels     map[string]int64 `+"`json:\"labels\" plystra:\"2\"`"+`
	IDs        []uint64         `+"`json:\"ids\" plystra:\"3\"`"+`
	Payload    []byte           `+"`json:\"payload\" plystra:\"4\"`"+`
	CreatedAt  time.Time        `+"`json:\"created_at\" plystra:\"5\"`"+`
	MaximumAge time.Duration    `+"`json:\"maximum_age\" plystra:\"6\"`"+`
	Filter     Filter           `+"`json:\"filter\" plystra:\"8\"`"+`
}

type Filter struct {
	Enabled bool `+"`json:\"enabled\" plystra:\"1\"`"+`
}

type Response struct {
	Records []Record `+"`json:\"records\" plystra:\"11,required\"`"+`
}

type Record struct {
	ID string `+"`json:\"id\" plystra:\"1,required\"`"+`
}
`)
	interfaces, err := protobufmodel.BuildInterfaces(true, []protobufmodel.InterfaceInput{input})
	if err != nil {
		t.Fatalf("BuildInterfaces: %v", err)
	}
	legacy, err := protobufmodel.Build(true, nil, nil)
	if err != nil {
		t.Fatalf("protobufmodel.Build: %v", err)
	}
	wireMap, err := protobufwiremap.Build(legacy, nil, false, "")
	if err != nil {
		t.Fatalf("protobufwiremap.Build: %v", err)
	}

	evidence, err := protobufdescriptor.BuildWithInterfaces(legacy, wireMap, interfaces)
	if err != nil {
		t.Fatalf("BuildWithInterfaces: %v", err)
	}
	repeated, err := protobufdescriptor.BuildWithInterfaces(legacy, wireMap, interfaces)
	if err != nil {
		t.Fatalf("BuildWithInterfaces(repeated): %v", err)
	}
	if !evidence.Valid() || evidence.DescriptorCount() != 1 || evidence.Digest() != repeated.Digest() || !reflect.DeepEqual(evidence.Files(), repeated.Files()) {
		t.Fatalf("evidence = count %d digest %q files %#v", evidence.DescriptorCount(), evidence.Digest(), evidence.Files())
	}

	files := evidence.Files()
	if len(files) != 2 || files[0].Path() != protobufdescriptor.DescriptorSetPath || files[1].Path() != "generated/proto/plystra/generated/records/list/v1/interface.proto" {
		t.Fatalf("files = %#v", files)
	}
	source := string(files[1].Data())
	for _, fragment := range []string{
		`import "google/protobuf/duration.proto";`,
		`import "google/protobuf/timestamp.proto";`,
		"message RecordsListV1Request {",
		`map<string, sint64> labels = 2 [json_name = "labels"];`,
		`repeated uint64 ids = 3 [json_name = "ids"];`,
		`optional bytes payload = 4 [json_name = "payload"];`,
		`.google.protobuf.Timestamp created_at = 5 [json_name = "created_at"];`,
		`.google.protobuf.Duration maximum_age = 6 [json_name = "maximum_age"];`,
		`optional sint32 page_size = 7 [json_name = "page_size"];`,
		`plystra.generated.records.list.v1.RecordsListV1Filter filter = 8 [json_name = "filter"];`,
		`repeated .plystra.generated.records.list.v1.RecordsListV1Record records = 11 [json_name = "records"];`,
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("generated Interface schema omits %q:\n%s", fragment, source)
		}
	}
	if strings.Contains(source, "service ") || strings.Contains(source, "rpc ") || strings.Contains(source, "capability") {
		t.Fatalf("message-only Interface projection contains a premature procedure or obsolete identity:\n%s", source)
	}

	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(evidence.DescriptorSet(), &set); err != nil {
		t.Fatalf("proto.Unmarshal: %v", err)
	}
	descriptors, err := protodesc.NewFiles(&set)
	if err != nil {
		t.Fatalf("protodesc.NewFiles: %v", err)
	}
	file, err := descriptors.FindFileByPath("plystra/generated/records/list/v1/interface.proto")
	if err != nil {
		t.Fatalf("FindFileByPath: %v", err)
	}
	if file.Services().Len() != 0 {
		t.Fatalf("Interface message file services = %d", file.Services().Len())
	}
	request := file.Messages().ByName("RecordsListV1Request")
	if request == nil {
		t.Fatal("request descriptor is absent")
	}
	assertInterfaceDescriptorField(t, request, "page_size", 7, protoreflect.Sint32Kind, true, "page_size")
	assertInterfaceDescriptorField(t, request, "payload", 4, protoreflect.BytesKind, true, "payload")
	assertInterfaceDescriptorField(t, request, "created_at", 5, protoreflect.MessageKind, true, "created_at")
	labels := request.Fields().ByName("labels")
	if labels == nil || !labels.IsMap() || labels.MapKey().Kind() != protoreflect.StringKind || labels.MapValue().Kind() != protoreflect.Sint64Kind {
		t.Fatalf("labels descriptor = %#v", labels)
	}
	ids := request.Fields().ByName("ids")
	if ids == nil || !ids.IsList() || ids.Kind() != protoreflect.Uint64Kind {
		t.Fatalf("ids descriptor = %#v", ids)
	}
}

func TestBuildWithInterfacesRejectsTransportSelectionMismatch(t *testing.T) {
	t.Parallel()

	legacy, err := protobufmodel.Build(false, nil, nil)
	if err != nil {
		t.Fatalf("protobufmodel.Build: %v", err)
	}
	wireMap, err := protobufwiremap.Build(legacy, nil, false, "")
	if err != nil {
		t.Fatalf("protobufwiremap.Build: %v", err)
	}
	interfaces, err := protobufmodel.BuildInterfaces(true, nil)
	if err != nil {
		t.Fatalf("BuildInterfaces: %v", err)
	}
	evidence, err := protobufdescriptor.BuildWithInterfaces(legacy, wireMap, interfaces)
	if err == nil || evidence.Valid() || !strings.Contains(err.Error(), "transport selection disagree") {
		t.Fatalf("BuildWithInterfaces = %#v, %v", evidence, err)
	}
}

func assertInterfaceDescriptorField(t testing.TB, message protoreflect.MessageDescriptor, name protoreflect.Name, number protoreflect.FieldNumber, kind protoreflect.Kind, presence bool, jsonName string) {
	t.Helper()
	field := message.Fields().ByName(name)
	if field == nil || field.Number() != number || field.Kind() != kind || field.HasPresence() != presence || field.JSONName() != jsonName {
		t.Fatalf("field %s = %#v", name, field)
	}
}

func descriptorInterfaceInput(t testing.TB, source string) protobufmodel.InterfaceInput {
	t.Helper()
	const packagePath = "example.com/platform/interfaces/records/list/v1"
	declarations, err := interfacedecl.ParseFile("interface.go", []byte(source))
	if err != nil || len(declarations) != 1 {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, "interface.go", source, parser.AllErrors)
	if err != nil {
		t.Fatalf("parser.ParseFile: %v", err)
	}
	checked, err := (&types.Config{Importer: importer.Default()}).Check(packagePath, files, []*ast.File{file}, nil)
	if err != nil {
		t.Fatalf("types.Check: %v", err)
	}
	contract, err := interfacecontract.Validate(declarations[0], checked)
	if err != nil {
		t.Fatalf("interfacecontract.Validate: %v", err)
	}
	sum := sha256.Sum256([]byte(source))
	return protobufmodel.InterfaceInput{
		InterfaceID:    contract.ID(),
		PackagePath:    packagePath,
		Source:         "example.com/platform@v1.2.3/interfaces/records/list/v1/interface.go:10:1",
		Contract:       contract,
		ContractDigest: "sha256:" + hex.EncodeToString(sum[:]),
	}
}
