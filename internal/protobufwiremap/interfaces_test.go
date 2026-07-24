package protobufwiremap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/plystra/cli/internal/protobufmodel"
)

func TestBuildRecordsEveryCanonicalInterfaceMessageDeterministically(t *testing.T) {
	t.Parallel()

	firstInput := interfaceHistoryInput(t, `package listv1

import "context"

//plystra:interface records.list/v1
type Interface interface {
	List(context.Context, Request) (Response, error)
}

type Request struct {
	PageSize int32            `+"`json:\"page_size\" plystra:\"7,required\"`"+`
	Labels   map[string]int64 `+"`json:\"labels\" plystra:\"2\"`"+`
	Filter   Filter           `+"`json:\"filter\" plystra:\"8\"`"+`
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
	reorderedInput := interfaceHistoryInput(t, `package listv1

import "context"

//plystra:interface records.list/v1
type Interface interface {
	List(context.Context, Request) (Response, error)
}

type Record struct {
	ID string `+"`json:\"id\" plystra:\"1,required\"`"+`
}

type Response struct {
	Records []Record `+"`json:\"records\" plystra:\"11,required\"`"+`
}

type Filter struct {
	Enabled bool `+"`json:\"enabled\" plystra:\"1\"`"+`
}

type Request struct {
	Filter   Filter           `+"`json:\"filter\" plystra:\"8\"`"+`
	Labels   map[string]int64 `+"`json:\"labels\" plystra:\"2\"`"+`
	PageSize int32            `+"`json:\"page_size\" plystra:\"7,required\"`"+`
}
`)
	reorderedInput.Source = firstInput.Source
	reorderedInput.ContractDigest = firstInput.ContractDigest

	firstModel := interfaceHistoryModel(t, true, firstInput)
	reorderedModel := interfaceHistoryModel(t, true, reorderedInput)
	if !bytes.Equal(firstModel.CanonicalJSON(), reorderedModel.CanonicalJSON()) {
		t.Fatalf("equivalent Interface models differ:\n%s\n%s", firstModel.CanonicalJSON(), reorderedModel.CanonicalJSON())
	}
	legacy := wireModel(t, true)
	first, err := Build(legacy, firstModel, nil, false, "")
	if err != nil {
		t.Fatalf("Build(first): %v", err)
	}
	reordered, err := Build(legacy, reorderedModel, nil, false, "")
	if err != nil {
		t.Fatalf("Build(reordered): %v", err)
	}
	if !first.Valid() ||
		!first.Matches(legacy, firstModel) ||
		first.ProjectionDigest() == legacy.Digest() ||
		first.InterfaceProjectionDigest() != firstModel.Digest() ||
		!bytes.Equal(first.CanonicalJSON(), reordered.CanonicalJSON()) ||
		first.Digest() != reordered.Digest() ||
		first.ActiveDigest() != reordered.ActiveDigest() {
		t.Fatalf("deterministic Interface wire maps differ:\n%s\n%s", first.CanonicalJSON(), reordered.CanonicalJSON())
	}

	document := decodeTestDocument(t, first.CanonicalJSON())
	record := document.Interfaces["records.list/v1"]
	if !record.Active ||
		record.ProtobufPackage != "plystra.generated.records.list.v1" ||
		record.RequestMessage != "RecordsListV1Request" ||
		record.ResponseMessage != "RecordsListV1Response" ||
		len(record.Messages) != 4 {
		t.Fatalf("Interface history = %#v", record)
	}
	request := record.Messages["RecordsListV1Request"]
	if !request.Active ||
		request.GoName != "Request" ||
		request.Fields["page_size"] != (interfaceFieldAssignment{GoName: "PageSize", Number: 7}) ||
		request.Fields["labels"] != (interfaceFieldAssignment{GoName: "Labels", Number: 2}) {
		t.Fatalf("request history = %#v", request)
	}

	active := first.ActiveInterfaces()
	if len(active) != 1 ||
		active[0].ID() != "records.list/v1" ||
		active[0].ContractDigest() != firstInput.ContractDigest ||
		active[0].ProtobufPackage() != "plystra.generated.records.list.v1" ||
		active[0].RequestMessage() != "RecordsListV1Request" ||
		active[0].ResponseMessage() != "RecordsListV1Response" ||
		len(active[0].Messages()) != 4 {
		t.Fatalf("ActiveInterfaces = %#v", active)
	}
	projectedRequest := interfaceProjectedMessage(t, active[0], "RecordsListV1Request")
	if projectedRequest.CanonicalName() != "Request" ||
		!reflect.DeepEqual(projectedRequest.Fields(), []FieldProjection{
			{canonicalName: "Filter", name: "filter", number: 8},
			{canonicalName: "Labels", name: "labels", number: 2},
			{canonicalName: "PageSize", name: "page_size", number: 7},
		}) {
		t.Fatalf("projected request = %#v", projectedRequest)
	}

	messages := active[0].Messages()
	fields := messages[0].Fields()
	active[0] = InterfaceProjection{}
	messages[0] = MessageProjection{}
	if len(fields) != 0 {
		fields[0] = FieldProjection{}
	}
	fresh := first.ActiveInterfaces()[0]
	if fresh.ID() != "records.list/v1" || len(fresh.Messages()) != 4 {
		t.Fatal("ActiveInterfaces exposed mutable projection storage")
	}
}

func TestBuildPreservesInterfaceRemovalsAndRejectsWireReuse(t *testing.T) {
	t.Parallel()

	initialInput := interfaceHistoryInput(t, `package listv1
import "context"
//plystra:interface records.list/v1
type Interface interface { List(context.Context, Request) (Response, error) }
type Request struct {
	Keep    string `+"`json:\"keep\" plystra:\"1\"`"+`
	Removed string `+"`json:\"removed\" plystra:\"7\"`"+`
	Filter  Filter `+"`json:\"filter\" plystra:\"8\"`"+`
}
type Filter struct { Value string `+"`json:\"value\" plystra:\"3\"`"+` }
type Response struct{}
`)
	legacy := wireModel(t, true)
	initialModel := interfaceHistoryModel(t, true, initialInput)
	initial, err := Build(legacy, initialModel, nil, false, "")
	if err != nil {
		t.Fatalf("Build(initial): %v", err)
	}

	removedInput := interfaceHistoryInput(t, `package listv1
import "context"
//plystra:interface records.list/v1
type Interface interface { List(context.Context, Request) (Response, error) }
type Request struct { Keep string `+"`json:\"keep\" plystra:\"1\"`"+` }
type Response struct{}
`)
	removedModel := interfaceHistoryModel(t, true, removedInput)
	removed, err := Build(legacy, removedModel, initial.CanonicalJSON(), true, initial.Digest())
	if err != nil {
		t.Fatalf("Build(removed): %v", err)
	}
	record := decodeTestDocument(t, removed.CanonicalJSON()).Interfaces["records.list/v1"]
	request := record.Messages["RecordsListV1Request"]
	if !reflect.DeepEqual(request.ReservedNumbers, []int{7, 8}) ||
		!reflect.DeepEqual(request.ReservedNames, []string{"filter", "removed"}) ||
		record.Messages["RecordsListV1Filter"].Active {
		t.Fatalf("removed Interface history = %#v", record)
	}
	if messages := removed.ActiveInterfaces()[0].Messages(); len(messages) != 2 {
		t.Fatalf("active messages after removal = %#v", messages)
	}

	tests := []struct {
		name     string
		source   string
		contains string
	}{
		{
			name: "retained field number change",
			source: `package listv1
import "context"
//plystra:interface records.list/v1
type Interface interface { List(context.Context, Request) (Response, error) }
type Request struct { Keep string ` + "`json:\"keep\" plystra:\"9\"`" + ` }
type Response struct{}
`,
			contains: "authored number changed from 1 to 9",
		},
		{
			name: "removed name reuse",
			source: `package listv1
import "context"
//plystra:interface records.list/v1
type Interface interface { List(context.Context, Request) (Response, error) }
type Request struct {
	Keep    string ` + "`json:\"keep\" plystra:\"1\"`" + `
	Removed string ` + "`json:\"removed\" plystra:\"12\"`" + `
}
type Response struct{}
`,
			contains: "reuses a permanently reserved Protobuf name",
		},
		{
			name: "removed number reuse",
			source: `package listv1
import "context"
//plystra:interface records.list/v1
type Interface interface { List(context.Context, Request) (Response, error) }
type Request struct {
	Keep  string ` + "`json:\"keep\" plystra:\"1\"`" + `
	Other string ` + "`json:\"other\" plystra:\"7\"`" + `
}
type Response struct{}
`,
			contains: "authored number 7 is permanently occupied",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			model := interfaceHistoryModel(t, true, interfaceHistoryInput(t, test.source))
			result, buildErr := Build(legacy, model, removed.CanonicalJSON(), true, removed.Digest())
			if !errors.Is(buildErr, ErrHistory) || result.Valid() || !strings.Contains(buildErr.Error(), test.contains) {
				t.Fatalf("Build = %#v, %v; want %q", result, buildErr, test.contains)
			}
		})
	}
}

func TestBuildRetainsInactiveInterfaceHistoryOutsideActiveProjection(t *testing.T) {
	t.Parallel()

	input := interfaceHistoryInput(t, `package healthv1
import "context"
//plystra:interface system.health/v1
type Interface interface { Health(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct { Healthy bool `+"`json:\"healthy\" plystra:\"1\"`"+` }
`)
	enabledLegacy := wireModel(t, true)
	enabledInterfaces := interfaceHistoryModel(t, true, input)
	initial, err := Build(enabledLegacy, enabledInterfaces, nil, false, "")
	if err != nil {
		t.Fatalf("Build(initial): %v", err)
	}

	disabledLegacy := wireModel(t, false)
	disabledInterfaces := interfaceHistoryModel(t, false)
	inactive, err := Build(disabledLegacy, disabledInterfaces, initial.CanonicalJSON(), true, initial.Digest())
	if err != nil {
		t.Fatalf("Build(inactive): %v", err)
	}
	record := decodeTestDocument(t, inactive.CanonicalJSON()).Interfaces["system.health/v1"]
	if record.Active || record.Messages["SystemHealthV1Request"].Active || record.Messages["SystemHealthV1Response"].Active {
		t.Fatalf("inactive Interface history = %#v", record)
	}
	if len(inactive.ActiveInterfaces()) != 0 {
		t.Fatalf("inactive ActiveInterfaces = %#v", inactive.ActiveInterfaces())
	}
	var active activeDocument
	if err := json.Unmarshal(inactive.ActiveJSON(), &active); err != nil || len(active.Interfaces) != 0 {
		t.Fatalf("inactive ActiveJSON = %s, %v", inactive.ActiveJSON(), err)
	}

	reactivated, err := Build(enabledLegacy, enabledInterfaces, inactive.CanonicalJSON(), true, inactive.Digest())
	if err != nil {
		t.Fatalf("Build(reactivated): %v", err)
	}
	if len(reactivated.ActiveInterfaces()) != 1 ||
		!bytes.Equal(initial.ActiveJSON(), reactivated.ActiveJSON()) {
		t.Fatalf("reactivated Interface history = %s", reactivated.CanonicalJSON())
	}
}

func interfaceHistoryInput(t testing.TB, source string) protobufmodel.InterfaceInput {
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
		Source:         "example.com/platform@v1/interfaces/records/list/v1/interface.go:1:1",
		Contract:       contract,
		ContractDigest: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

func interfaceHistoryModel(t testing.TB, enabled bool, inputs ...protobufmodel.InterfaceInput) protobufmodel.InterfaceModel {
	t.Helper()
	model, err := protobufmodel.BuildInterfaces(enabled, inputs)
	if err != nil {
		t.Fatalf("protobufmodel.BuildInterfaces: %v", err)
	}
	return model
}

func interfaceProjectedMessage(t testing.TB, projection InterfaceProjection, name string) MessageProjection {
	t.Helper()
	for _, message := range projection.Messages() {
		if message.Name() == name {
			return message
		}
	}
	t.Fatalf("message %s is absent from %#v", name, projection.Messages())
	return MessageProjection{}
}
