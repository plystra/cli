package interfacecontract_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacecontract"
)

func TestValidateNormalizesClosedSupportedFieldGraph(t *testing.T) {
	t.Parallel()

	source := `package createv1

import (
	"context"
	"time"
)

//plystra:interface order.create/v1
type Interface interface {
	Create(context.Context, Request) (Response, error)
}

type Request struct {
	Active    bool               ` + "`plystra:\"1\"`" + `
	Name      string             ` + "`plystra:\"2,required\"`" + `
	Count     int32              ` + "`plystra:\"3\"`" + `
	Total     int64              ` + "`plystra:\"4\"`" + `
	Small     uint32             ` + "`plystra:\"5\"`" + `
	Big       uint64             ` + "`plystra:\"6\"`" + `
	Ratio     float32            ` + "`plystra:\"7\"`" + `
	Score     float64            ` + "`plystra:\"8\"`" + `
	Payload   []byte             ` + "`plystra:\"9\"`" + `
	Tags      []string           ` + "`plystra:\"10\"`" + `
	Chunks    [][]byte           ` + "`plystra:\"11\"`" + `
	Lookup    map[string]Address ` + "`plystra:\"12\"`" + `
	Flags     map[bool]uint64    ` + "`plystra:\"13\"`" + `
	Address   Address            ` + "`plystra:\"14\"`" + `
	History   []Address          ` + "`plystra:\"15\"`" + `
	CreatedAt time.Time          ` + "`plystra:\"16\"`" + `
	Timeout   time.Duration      ` + "`plystra:\"17\"`" + `
	Root      Node               ` + "`plystra:\"18\"`" + `
	Int32Map  map[int32]string   ` + "`plystra:\"19\"`" + `
	Int64Map  map[int64]string   ` + "`plystra:\"20\"`" + `
	Uint32Map map[uint32]string  ` + "`plystra:\"21\"`" + `
	Uint64Map map[uint64]string  ` + "`plystra:\"22\"`" + `
}

type Response struct {
	Result Address ` + "`plystra:\"1\"`" + `
}

type Address struct {
	PostalCode int32  ` + "`plystra:\"1\"`" + `
	Street     string ` + "`plystra:\"2,required\"`" + `
}

type Node struct {
	Label    string          ` + "`plystra:\"1\"`" + `
	Children []Node          ` + "`plystra:\"2\"`" + `
	Index    map[string]Node ` + "`plystra:\"3\"`" + `
}
`
	contract, err := validateSource(t, source)
	if err != nil {
		t.Fatal(err)
	}

	wantRequest := map[string]string{
		"Active":    "boolean",
		"Name":      "string",
		"Count":     "int32",
		"Total":     "int64",
		"Small":     "uint32",
		"Big":       "uint64",
		"Ratio":     "float32",
		"Score":     "float64",
		"Payload":   "bytes",
		"Tags":      "repeated<string>",
		"Chunks":    "repeated<bytes>",
		"Lookup":    "map<string,message:Address>",
		"Flags":     "map<boolean,uint64>",
		"Address":   "message:Address",
		"History":   "repeated<message:Address>",
		"CreatedAt": "timestamp",
		"Timeout":   "duration",
		"Root":      "message:Node",
		"Int32Map":  "map<int32,string>",
		"Int64Map":  "map<int64,string>",
		"Uint32Map": "map<uint32,string>",
		"Uint64Map": "map<uint64,string>",
	}
	request := contract.RequestFields()
	if len(request) != len(wantRequest) {
		t.Fatalf("request fields = %d, want %d", len(request), len(wantRequest))
	}
	for _, field := range request {
		if got := field.Type().Canonical(); got != wantRequest[field.Name()] {
			t.Fatalf("field %s type = %q, want %q", field.Name(), got, wantRequest[field.Name()])
		}
	}

	messages := contract.Messages()
	wantMessages := []string{"Address", "Node", "Request", "Response"}
	gotMessages := make([]string, len(messages))
	for index, message := range messages {
		gotMessages[index] = message.Name()
	}
	if !slices.Equal(gotMessages, wantMessages) {
		t.Fatalf("messages = %v, want %v", gotMessages, wantMessages)
	}
	address, exists := contract.Message("Address")
	if !exists || len(address.Fields()) != 2 || address.Fields()[0].Name() != "PostalCode" || address.Fields()[1].Name() != "Street" {
		t.Fatalf("Address = %#v, %t", address, exists)
	}
	node, exists := contract.Message("Node")
	if !exists || len(node.Fields()) != 3 || node.Fields()[1].Type().Canonical() != "repeated<message:Node>" || node.Fields()[2].Type().Canonical() != "map<string,message:Node>" {
		t.Fatalf("recursive Node = %#v, %t", node, exists)
	}
	if _, exists := contract.Message("Missing"); exists {
		t.Fatal("Message found an unknown type")
	}

	repeated, exists := requestField(t, contract, "Tags").Type().Element()
	if !exists || repeated.Kind() != interfacecontract.TypeString || repeated.Canonical() != "string" {
		t.Fatalf("repeated element = %#v, %t", repeated, exists)
	}
	mapType := requestField(t, contract, "Lookup").Type()
	key, keyExists := mapType.Key()
	value, valueExists := mapType.Value()
	messageName, messageExists := value.MessageName()
	if !keyExists || key.Kind() != interfacecontract.TypeString || !valueExists || value.Kind() != interfacecontract.TypeMessage || !messageExists || messageName != "Address" {
		t.Fatalf("map type = key %#v/%t value %#v/%t message %q/%t", key, keyExists, value, valueExists, messageName, messageExists)
	}
	if _, exists := requestField(t, contract, "Name").Type().Element(); exists {
		t.Fatal("scalar exposed a repeated element")
	}
	if _, exists := requestField(t, contract, "Name").Type().MessageName(); exists {
		t.Fatal("scalar exposed a message name")
	}

	messages[0] = interfacecontract.Message{}
	fields := address.Fields()
	fields[0] = contract.ResponseFields()[0]
	againAddress, _ := contract.Message("Address")
	if contract.Messages()[0].Name() != "Address" || againAddress.Fields()[0].Name() != "PostalCode" {
		t.Fatal("message accessors exposed mutable graph storage")
	}
}

func TestValidateNormalizesZeroValueOmissionSemantics(t *testing.T) {
	t.Parallel()

	source := `package createv1

import (
	"context"
	"time"
)

//plystra:interface order.create/v1
type Interface interface {
	Create(context.Context, Request) (Response, error)
}

type Request struct {
	Boolean   bool              ` + "`plystra:\"1\"`" + `
	String    string            ` + "`json:\"string,omitempty\" plystra:\"2\"`" + `
	Int32     int32             ` + "`plystra:\"3\"`" + `
	Int64     int64             ` + "`plystra:\"4\"`" + `
	Uint32    uint32            ` + "`plystra:\"5\"`" + `
	Uint64    uint64            ` + "`plystra:\"6\"`" + `
	Float32   float32           ` + "`plystra:\"7\"`" + `
	Float64   float64           ` + "`plystra:\"8\"`" + `
	Message   Detail            ` + "`plystra:\"9\"`" + `
	Timestamp time.Time         ` + "`plystra:\"10\"`" + `
	Duration  time.Duration     ` + "`plystra:\"11\"`" + `
	Bytes     []byte            ` + "`plystra:\"12\"`" + `
	Repeated  []string          ` + "`plystra:\"13\"`" + `
	Map       map[string]string ` + "`plystra:\"14\"`" + `
	Required  string            ` + "`plystra:\"15,required\"`" + `
}

type Response struct {
	Message Detail ` + "`plystra:\"1\"`" + `
}

type Detail struct {
	Value    string ` + "`plystra:\"1\"`" + `
	Required int64  ` + "`plystra:\"2,required\"`" + `
}
`
	contract, err := validateSource(t, source)
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"Boolean",
		"String",
		"Int32",
		"Int64",
		"Uint32",
		"Uint64",
		"Float32",
		"Float64",
		"Message",
		"Timestamp",
		"Duration",
	} {
		if field := requestField(t, contract, name); !field.OmissionEqualsZeroValue() {
			t.Fatalf("field %s does not normalize omission to the ordinary Go zero value", name)
		}
	}
	for _, name := range []string{"Bytes", "Repeated", "Map", "Required"} {
		if field := requestField(t, contract, name); field.OmissionEqualsZeroValue() {
			t.Fatalf("field %s claims unsupported zero-value omission semantics", name)
		}
	}
	response := contract.ResponseFields()
	if len(response) != 1 || !response[0].OmissionEqualsZeroValue() {
		t.Fatalf("response fields = %#v", response)
	}
	detail, exists := contract.Message("Detail")
	if !exists || len(detail.Fields()) != 2 || !detail.Fields()[0].OmissionEqualsZeroValue() || detail.Fields()[1].OmissionEqualsZeroValue() {
		t.Fatalf("Detail = %#v, %t", detail, exists)
	}
	if (interfacecontract.Field{}).OmissionEqualsZeroValue() {
		t.Fatal("zero Field claims zero-value omission semantics")
	}
}

func TestValidateAcceptsAliasesOfSupportedExactTypes(t *testing.T) {
	t.Parallel()

	source := `package createv1
import "context"
type Text = string
type Count = int64
//plystra:interface order.create/v1
type Interface interface { Create(context.Context, Request) (Response, error) }
type Request struct {
	Text Text ` + "`plystra:\"1\"`" + `
	Count Count ` + "`plystra:\"2\"`" + `
}
type Response struct{}
`
	contract, err := validateSource(t, source)
	if err != nil {
		t.Fatal(err)
	}
	if got := requestField(t, contract, "Text").Type().Canonical(); got != "string" {
		t.Fatalf("Text alias = %q", got)
	}
	if got := requestField(t, contract, "Count").Type().Canonical(); got != "int64" {
		t.Fatalf("Count alias = %q", got)
	}
}

func TestValidateRejectsUnsupportedFieldGraphTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		imports      string
		declarations string
		fieldType    string
		want         string
	}{
		{name: "int", fieldType: "int", want: "unsupported Go scalar type int"},
		{name: "uint", fieldType: "uint", want: "unsupported Go scalar type uint"},
		{name: "int8", fieldType: "int8", want: "unsupported Go scalar type int8"},
		{name: "int16", fieldType: "int16", want: "unsupported Go scalar type int16"},
		{name: "uint8", fieldType: "uint8", want: "unsupported Go scalar type uint8"},
		{name: "uint16", fieldType: "uint16", want: "unsupported Go scalar type uint16"},
		{name: "uintptr", fieldType: "uintptr", want: "unsupported Go scalar type uintptr"},
		{name: "complex64", fieldType: "complex64", want: "unsupported Go scalar type complex64"},
		{name: "complex128", fieldType: "complex128", want: "unsupported Go scalar type complex128"},
		{name: "defined scalar", declarations: "type Defined string", fieldType: "Defined", want: "unsupported defined non-message type Defined"},
		{name: "defined slice", declarations: "type Defined []string", fieldType: "Defined", want: "unsupported defined non-message type Defined"},
		{name: "pointer scalar", fieldType: "*string", want: "pointer presence is not part of the initial field graph"},
		{name: "pointer message", declarations: taggedNestedMessage, fieldType: "*Nested", want: "pointer presence is not part of the initial field graph"},
		{name: "pointer timestamp", imports: `"time"`, fieldType: "*time.Time", want: "pointer presence is not part of the initial field graph"},
		{name: "fixed array", fieldType: "[2]string", want: "unsupported fixed array type"},
		{name: "byte array", fieldType: "[16]byte", want: "unsupported fixed array type"},
		{name: "anonymous struct", fieldType: "struct{ Value string }", want: "unsupported anonymous struct type"},
		{name: "interface value", fieldType: "any", want: "unsupported interface-valued field type"},
		{name: "function", fieldType: "func()", want: "unsupported function field type"},
		{name: "channel", fieldType: "chan string", want: "unsupported channel field type"},
		{name: "unsafe pointer", imports: `"unsafe"`, fieldType: "unsafe.Pointer", want: "unsupported Go scalar type Pointer"},
		{name: "external struct", imports: `"net/url"`, fieldType: "url.URL", want: "unsupported external defined type net/url.URL"},
		{name: "external defined scalar", imports: `"time"`, fieldType: "time.Month", want: "unsupported external defined type time.Month"},
		{name: "unexported message", declarations: "type nested struct{}", fieldType: "nested", want: "uses unexported message type nested"},
		{name: "generic message", declarations: "type Box[T any] struct{}", fieldType: "Box[string]", want: "uses generic message type Box"},
		{name: "nested repeated", fieldType: "[][]string", want: "unsupported nested collection type"},
		{name: "map repeated value", fieldType: "map[string][]string", want: "unsupported collection-valued map type"},
		{name: "map map value", fieldType: "map[string]map[string]string", want: "unsupported collection-valued map type"},
		{name: "map float key", fieldType: "map[float64]string", want: "unsupported map key type float64"},
		{name: "map message key", declarations: taggedNestedMessage, fieldType: "map[Nested]string", want: "unsupported map key type"},
		{name: "nested missing tag", declarations: "type Nested struct { Value string }", fieldType: "Nested", want: "Interface message Nested field Value: missing plystra field-number tag"},
		{name: "nested unexported field", declarations: "type Nested struct { value string `plystra:\"1\"` }", fieldType: "Nested", want: "Interface message Nested field value must be an exported named field"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			imports := `"context"`
			if test.imports != "" {
				imports += "; " + test.imports
			}
			source := "package createv1\nimport (" + imports + ")\n//plystra:interface order.create/v1\ntype Interface interface { Create(context.Context, Request) (Response, error) }\ntype Request struct { Value " + test.fieldType + " `plystra:\"1\"` }\ntype Response struct{}\n" + test.declarations + "\n"
			contract, err := validateSource(t, source)
			if !errors.Is(err, interfacecontract.ErrInvalid) || contract.ID().String() != "" || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate = %#v, %v; want %q", contract, err, test.want)
			}
		})
	}
}

func TestValidateRejectsUnsupportedResponseFieldGraph(t *testing.T) {
	t.Parallel()

	source := `package createv1
import "context"
//plystra:interface order.create/v1
type Interface interface { Create(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct { Value int ` + "`plystra:\"1\"`" + ` }
`
	contract, err := validateSource(t, source)
	if !errors.Is(err, interfacecontract.ErrInvalid) || contract.ID().String() != "" || !strings.Contains(err.Error(), "Interface response field Value uses unsupported Go scalar type int") {
		t.Fatalf("Validate = %#v, %v", contract, err)
	}
}

func FuzzValidateClosedFieldTypeDispatch(f *testing.F) {
	types := []struct {
		value string
		valid bool
	}{
		{value: "bool", valid: true},
		{value: "string", valid: true},
		{value: "int32", valid: true},
		{value: "int64", valid: true},
		{value: "uint32", valid: true},
		{value: "uint64", valid: true},
		{value: "float32", valid: true},
		{value: "float64", valid: true},
		{value: "[]byte", valid: true},
		{value: "[]string", valid: true},
		{value: "map[string]int64", valid: true},
		{value: "int"},
		{value: "uint"},
		{value: "*string"},
		{value: "[2]string"},
		{value: "[][]string"},
		{value: "map[float64]string"},
		{value: "map[string][]string"},
		{value: "any"},
		{value: "func()"},
	}
	for index := range types {
		f.Add(uint8(index))
	}
	f.Fuzz(func(t *testing.T, selector uint8) {
		selected := types[int(selector)%len(types)]
		source := fmt.Sprintf("package createv1\nimport \"context\"\n//plystra:interface order.create/v1\ntype Interface interface { Create(context.Context, Request) (Response, error) }\ntype Request struct { Value %s `plystra:\"1\"` }\ntype Response struct{}\n", selected.value)
		contract, err := validateSource(t, source)
		if selected.valid {
			if err != nil || len(contract.RequestFields()) != 1 || contract.RequestFields()[0].Type().Canonical() == "" {
				t.Fatalf("valid type %s = %#v, %v", selected.value, contract, err)
			}
			return
		}
		if !errors.Is(err, interfacecontract.ErrInvalid) || contract.ID().String() != "" {
			t.Fatalf("unsupported type %s = %#v, %v", selected.value, contract, err)
		}
	})
}

const taggedNestedMessage = "type Nested struct { Value string `plystra:\"1\"` }"

func requestField(t testing.TB, contract interfacecontract.Contract, name string) interfacecontract.Field {
	t.Helper()
	for _, field := range contract.RequestFields() {
		if field.Name() == name {
			return field
		}
	}
	t.Fatalf("request field %s is absent", name)
	return interfacecontract.Field{}
}
