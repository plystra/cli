package protobufmodel_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

func TestBuildInterfacesProjectsCanonicalGoMessagesDeterministically(t *testing.T) {
	t.Parallel()

	records := interfaceProjectionInput(t, "records.list/v1", "example.com/platform/interfaces/records/list/v1", `package listv1

import (
	"context"
	"time"
)

//plystra:interface records.list/v1
type Interface interface {
	List(context.Context, Request) (Response, error)
}

type Request struct {
	PageSize   int32            `+"`json:\"page_size\" plystra:\"1,required\"`"+`
	Labels     map[string]int64 `+"`json:\"labels\" plystra:\"2\"`"+`
	IDs        []uint64         `+"`json:\"ids\" plystra:\"3\"`"+`
	Payload    []byte           `+"`json:\"payload\" plystra:\"4\"`"+`
	CreatedAt  time.Time        `+"`json:\"created_at\" plystra:\"5\"`"+`
	MaximumAge time.Duration    `+"`json:\"maximum_age\" plystra:\"6\"`"+`
	Filter     Filter           `+"`json:\"filter\" plystra:\"7\"`"+`
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
	records.Source = "example.com/platform@v1.2.3/interfaces/records/list/v1/interface.go:10:1"
	records.MetadataSource = "example.com/platform@v1.2.3/interfaces/records/list/v1/interface.yaml"
	records.SemanticErrors = []string{"unavailable", "invalid_filter"}

	health := interfaceProjectionInput(t, "system.health/v1", "example.com/platform/interfaces/system/health/v1", `package healthv1

import "context"

//plystra:interface system.health/v1
type Interface interface {
	Health(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct {
	Healthy bool `+"`json:\"healthy\" plystra:\"1,required\"`"+`
}
`)
	health.Source = "example.com/platform@v1.2.3/interfaces/system/health/v1/interface.go:5:1"

	first, err := protobufmodel.BuildInterfaces(true, []protobufmodel.InterfaceInput{records, health})
	if err != nil {
		t.Fatalf("BuildInterfaces: %v", err)
	}
	second, err := protobufmodel.BuildInterfaces(true, []protobufmodel.InterfaceInput{health, records})
	if err != nil {
		t.Fatalf("BuildInterfaces(reordered): %v", err)
	}
	if !first.Valid() || !first.Enabled() || !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
		t.Fatalf("deterministic models differ:\n%s\n%s", first.CanonicalJSON(), second.CanonicalJSON())
	}

	operations := first.Operations()
	if len(operations) != 2 || operations[0].ID().String() != "records.list/v1" || operations[1].ID().String() != "system.health/v1" {
		t.Fatalf("operations = %#v", operations)
	}
	operation := operations[0]
	if operation.PackagePath() != records.PackagePath ||
		operation.Source() != records.Source ||
		operation.MetadataSource() != records.MetadataSource ||
		operation.ContractDigest() != records.ContractDigest ||
		operation.MethodName() != "List" ||
		operation.RequestGoName() != "Request" ||
		operation.ResponseGoName() != "Response" ||
		!reflect.DeepEqual(operation.SemanticErrors(), []string{"invalid_filter", "unavailable"}) {
		t.Fatalf("operation = %#v", operation)
	}
	if got, exists := operation.ProtobufMessageName("Filter"); !exists || got != "RecordsListV1Filter" {
		t.Fatalf("Filter Protobuf identity = %q, %t", got, exists)
	}

	messages := operation.Messages()
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	var request protobufmodel.InterfaceMessage
	for _, message := range messages {
		if message.GoName() == "Request" {
			request = message
		}
	}
	fields := request.Fields()
	if len(fields) != 7 ||
		fields[0].GoName() != "PageSize" ||
		fields[0].ProtobufName() != "page_size" ||
		fields[0].JSONName() != "page_size" ||
		fields[0].Number() != 1 ||
		!fields[0].Required() ||
		fields[0].Type().Kind() != interfacecontract.TypeInt32 ||
		fields[1].Type().Kind() != interfacecontract.TypeMap ||
		fields[2].Type().Kind() != interfacecontract.TypeRepeated ||
		fields[3].Type().Kind() != interfacecontract.TypeBytes ||
		fields[4].Type().Kind() != interfacecontract.TypeTimestamp ||
		fields[5].Type().Kind() != interfacecontract.TypeDuration ||
		fields[6].Type().Kind() != interfacecontract.TypeMessage {
		t.Fatalf("request fields = %#v", fields)
	}

	operations[0] = protobufmodel.InterfaceOperation{}
	messages[0] = protobufmodel.InterfaceMessage{}
	fields[0] = protobufmodel.InterfaceField{}
	errors := operation.SemanticErrors()
	errors[0] = "changed"
	canonical := first.CanonicalJSON()
	canonical[0] = '!'
	fresh := first.Operations()[0]
	if fresh.ID().String() != "records.list/v1" ||
		fresh.Messages()[0].GoName() == "" ||
		fresh.SemanticErrors()[0] != "invalid_filter" ||
		first.CanonicalJSON()[0] != '{' {
		t.Fatal("Interface model exposed mutable state")
	}
}

func TestBuildInterfacesDisabledModelIgnoresInputs(t *testing.T) {
	t.Parallel()

	model, err := protobufmodel.BuildInterfaces(false, []protobufmodel.InterfaceInput{{}})
	if err != nil || !model.Valid() || model.Enabled() || len(model.Operations()) != 0 ||
		string(model.CanonicalJSON()) != `{"version":1,"enabled":false,"interfaces":[]}` {
		t.Fatalf("BuildInterfaces(false) = %s, %#v, %v", model.CanonicalJSON(), model.Operations(), err)
	}
}

func TestBuildInterfacesRejectsInvalidProjectionFacts(t *testing.T) {
	t.Parallel()

	valid := interfaceProjectionInput(t, "records.list/v1", "example.com/interfaces/records/list/v1", `package listv1
import "context"
//plystra:interface records.list/v1
type Interface interface { List(context.Context, Request) (Response, error) }
type Request struct { Value string `+"`json:\"value\" plystra:\"1\"`"+` }
type Response struct{}
`)
	valid.Source = "example.com/interfaces@v1/records/list/v1/interface.go:3:1"

	reserved := interfaceProjectionInput(t, "records.reserved/v1", "example.com/interfaces/records/reserved/v1", `package reservedv1
import "context"
//plystra:interface records.reserved/v1
type Interface interface { Read(context.Context, Request) (Response, error) }
type Request struct { Value string `+"`plystra:\"19000\"`"+` }
type Response struct{}
`)
	reserved.Source = "example.com/interfaces@v1/records/reserved/v1/interface.go:3:1"

	collision := interfaceProjectionInput(t, "records.collision/v1", "example.com/interfaces/records/collision/v1", `package collisionv1
import "context"
//plystra:interface records.collision/v1
type Interface interface { Read(context.Context, Request) (Response, error) }
type Request struct {
	HTTPStatus string `+"`plystra:\"1\"`"+`
	HttpStatus string `+"`plystra:\"2\"`"+`
}
type Response struct{}
`)
	collision.Source = "example.com/interfaces@v1/records/collision/v1/interface.go:3:1"

	tests := []struct {
		name   string
		inputs []protobufmodel.InterfaceInput
		want   string
	}{
		{name: "duplicate", inputs: []protobufmodel.InterfaceInput{valid, valid}, want: "appears more than once"},
		{name: "package mismatch", inputs: []protobufmodel.InterfaceInput{withInterfacePackage(valid, "example.com/other")}, want: "does not match input package"},
		{name: "unsafe source", inputs: []protobufmodel.InterfaceInput{withInterfaceSource(valid, "bad\nsource")}, want: "bounded nonempty line"},
		{name: "invalid digest", inputs: []protobufmodel.InterfaceInput{withInterfaceDigest(valid, "sha256:no")}, want: "lower-case SHA-256"},
		{name: "reserved field number", inputs: []protobufmodel.InterfaceInput{reserved}, want: "outside the available Protobuf field-number space"},
		{name: "field identity collision", inputs: []protobufmodel.InterfaceInput{collision}, want: "duplicate Protobuf name http_status"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			model, err := protobufmodel.BuildInterfaces(true, test.inputs)
			if !strings.Contains(errString(err), test.want) || model.Valid() || !errorsAreInterfaceProjection(err) {
				t.Fatalf("BuildInterfaces = %s, %v; want %q", model.CanonicalJSON(), err, test.want)
			}
		})
	}
}

func interfaceProjectionInput(t testing.TB, identifier, packagePath, source string) protobufmodel.InterfaceInput {
	t.Helper()
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
	sum := sha256.Sum256([]byte(identifier + "\x00" + packagePath + "\x00" + source))
	return protobufmodel.InterfaceInput{
		InterfaceID:    contract.ID(),
		PackagePath:    packagePath,
		Source:         packagePath + "@v1/interface.go:1:1",
		Contract:       contract,
		ContractDigest: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

func withInterfacePackage(input protobufmodel.InterfaceInput, value string) protobufmodel.InterfaceInput {
	input.PackagePath = value
	return input
}

func withInterfaceSource(input protobufmodel.InterfaceInput, value string) protobufmodel.InterfaceInput {
	input.Source = value
	return input
}

func withInterfaceDigest(input protobufmodel.InterfaceInput, value string) protobufmodel.InterfaceInput {
	input.ContractDigest = value
	return input
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func errorsAreInterfaceProjection(err error) bool {
	return errors.Is(err, protobufmodel.ErrInterfaceBuild) && errors.Is(err, protobufmodel.ErrInterfaceInput)
}
