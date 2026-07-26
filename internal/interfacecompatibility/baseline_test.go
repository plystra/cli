package interfacecompatibility_test

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacecompatibility"
	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacedecl"
)

func TestBaselineHasKnownCanonicalShapeAndDefensiveAccess(t *testing.T) {
	t.Parallel()

	contract := parseContract(t, "example.com/acme/interfaces/records/echo/v1", baseInterfaceSource())
	baseline, err := interfacecompatibility.New([]interfacecontract.Contract{contract})
	if err != nil || !baseline.Valid() {
		t.Fatalf("New = %#v, %v", baseline, err)
	}
	const wantDigest = "sha256:dc31ef111ec4c8af1d95c9732baed6f65c898327e2e4f3746772095c0a40a4f4"
	if baseline.Digest() != wantDigest {
		t.Fatalf("baseline digest = %q; canonical = %s; record = %s", baseline.Digest(), baseline.CanonicalJSON(), baseline.RecordJSON())
	}
	interfaces := baseline.Interfaces()
	if len(interfaces) != 1 ||
		interfaces[0].ID() != "records.echo/v1" ||
		interfaces[0].PackagePath() != "example.com/acme/interfaces/records/echo/v1" ||
		interfaces[0].Method() != "Echo" ||
		interfaces[0].Request() != "Request" ||
		interfaces[0].Response() != "Response" ||
		!strings.HasPrefix(interfaces[0].Digest(), "sha256:") {
		t.Fatalf("Interfaces = %#v", interfaces)
	}
	messages := interfaces[0].Messages()
	if got := messageSummaries(messages); !reflect.DeepEqual(got, []string{
		"Detail:1:Code:code:false:string",
		"Request:1:Name:name:true:string|2:Detail:detail:false:message:Detail",
		"Response:1:Accepted:accepted:false:boolean",
	}) {
		t.Fatalf("messages = %#v", got)
	}

	interfaces[0] = interfacecompatibility.Interface{}
	messages[0] = interfacecompatibility.Message{}
	fields := baseline.Interfaces()[0].Messages()[0].Fields()
	fields[0] = interfacecompatibility.Field{}
	if !baseline.Valid() ||
		baseline.Interfaces()[0].ID() != "records.echo/v1" ||
		baseline.Interfaces()[0].Messages()[0].Name() != "Detail" ||
		baseline.Interfaces()[0].Messages()[0].Fields()[0].GoName() != "Code" {
		t.Fatal("Baseline exposed mutable internal storage")
	}

	decoded, err := interfacecompatibility.Decode(baseline.RecordJSON())
	if err != nil || !decoded.Valid() ||
		decoded.Digest() != baseline.Digest() ||
		!bytes.Equal(decoded.CanonicalJSON(), baseline.CanonicalJSON()) {
		t.Fatalf("Decode = %#v, %v", decoded, err)
	}
}

func TestBaselineIsDeterministicAndComparisonDetectsShapeChanges(t *testing.T) {
	t.Parallel()

	base := parseContract(t, "example.com/acme/interfaces/records/echo/v1", baseInterfaceSource())
	other := parseContract(t, "example.com/acme/interfaces/accounts/read/v1", strings.ReplaceAll(baseInterfaceSource(), "records.echo/v1", "accounts.read/v1"))
	first, err := interfacecompatibility.New([]interfacecontract.Contract{base, other})
	if err != nil {
		t.Fatalf("New(first): %v", err)
	}
	second, err := interfacecompatibility.New([]interfacecontract.Contract{other, base})
	if err != nil {
		t.Fatalf("New(second): %v", err)
	}
	if first.Digest() != second.Digest() || !bytes.Equal(first.RecordJSON(), second.RecordJSON()) {
		t.Fatalf("input order changed baseline:\n%s\n%s", first.RecordJSON(), second.RecordJSON())
	}
	if got := []string{first.Interfaces()[0].ID(), first.Interfaces()[1].ID()}; !reflect.DeepEqual(got, []string{"accounts.read/v1", "records.echo/v1"}) {
		t.Fatalf("ordered Interface IDs = %#v", got)
	}

	reordered := parseContract(t, "example.com/acme/interfaces/records/echo/v1", strings.Replace(
		baseInterfaceSource(),
		"\tName   string `plystra:\"1,required\" json:\"name\"`\n\tDetail Detail `plystra:\"2\" json:\"detail\"`",
		"\tDetail Detail `plystra:\"2\" json:\"detail\"`\n\tName   string `plystra:\"1,required\" json:\"name\"`",
		1,
	))
	equivalent, err := interfacecompatibility.New([]interfacecontract.Contract{reordered})
	if err != nil {
		t.Fatalf("New(reordered): %v", err)
	}
	baseOnly, err := interfacecompatibility.New([]interfacecontract.Contract{base})
	if err != nil {
		t.Fatalf("New(base): %v", err)
	}
	comparison, err := interfacecompatibility.Compare(baseOnly, equivalent)
	if err != nil || !comparison.Clean() {
		t.Fatalf("Compare(reordered) = %#v, %v", comparison.Changes(), err)
	}

	variants := map[string]struct {
		packagePath string
		source      string
	}{
		"package": {
			packagePath: "example.com/acme/v2/interfaces/records/echo/v1",
			source:      baseInterfaceSource(),
		},
		"method": {
			packagePath: "example.com/acme/interfaces/records/echo/v1",
			source:      strings.Replace(baseInterfaceSource(), "Echo(context.Context", "Send(context.Context", 1),
		},
		"request": {
			packagePath: "example.com/acme/interfaces/records/echo/v1",
			source:      strings.ReplaceAll(baseInterfaceSource(), "Request", "Input"),
		},
		"response": {
			packagePath: "example.com/acme/interfaces/records/echo/v1",
			source:      strings.ReplaceAll(baseInterfaceSource(), "Response", "Output"),
		},
		"field Go name": {
			packagePath: "example.com/acme/interfaces/records/echo/v1",
			source:      strings.Replace(baseInterfaceSource(), "Name   string", "Title  string", 1),
		},
		"field number": {
			packagePath: "example.com/acme/interfaces/records/echo/v1",
			source:      strings.Replace(baseInterfaceSource(), `plystra:"1,required"`, `plystra:"7,required"`, 1),
		},
		"JSON name": {
			packagePath: "example.com/acme/interfaces/records/echo/v1",
			source:      strings.Replace(baseInterfaceSource(), `json:"name"`, `json:"title"`, 1),
		},
		"requiredness": {
			packagePath: "example.com/acme/interfaces/records/echo/v1",
			source:      strings.Replace(baseInterfaceSource(), `plystra:"1,required"`, `plystra:"1"`, 1),
		},
		"field type": {
			packagePath: "example.com/acme/interfaces/records/echo/v1",
			source:      strings.Replace(baseInterfaceSource(), "Name   string", "Name   []byte", 1),
		},
		"field addition": {
			packagePath: "example.com/acme/interfaces/records/echo/v1",
			source: strings.Replace(
				baseInterfaceSource(),
				"\tDetail Detail `plystra:\"2\" json:\"detail\"`",
				"\tDetail Detail `plystra:\"2\" json:\"detail\"`\n\tCount int64 `plystra:\"3\" json:\"count\"`",
				1,
			),
		},
		"field removal": {
			packagePath: "example.com/acme/interfaces/records/echo/v1",
			source:      strings.Replace(baseInterfaceSource(), "\n\tDetail Detail `plystra:\"2\" json:\"detail\"`", "", 1),
		},
		"reachable message": {
			packagePath: "example.com/acme/interfaces/records/echo/v1",
			source: strings.Replace(
				baseInterfaceSource(),
				"\tDetail Detail `plystra:\"2\" json:\"detail\"`",
				"\tDetail Extra `plystra:\"2\" json:\"detail\"`",
				1,
			) + "\ntype Extra struct {\n\tValue uint64 `plystra:\"1\" json:\"value\"`\n}\n",
		},
	}
	for name, test := range variants {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			changed := parseContract(t, test.packagePath, test.source)
			current, err := interfacecompatibility.New([]interfacecontract.Contract{changed})
			if err != nil {
				t.Fatalf("New(changed): %v", err)
			}
			comparison, err := interfacecompatibility.Compare(baseOnly, current)
			if err != nil || comparison.Clean() || !comparison.Valid() {
				t.Fatalf("Compare = %#v, %v", comparison.Changes(), err)
			}
			changes := comparison.Changes()
			if len(changes) != 1 ||
				changes[0].Kind() != interfacecompatibility.ChangeChanged ||
				changes[0].ID() != "records.echo/v1" ||
				changes[0].PreviousDigest() == "" ||
				changes[0].CurrentDigest() == "" ||
				changes[0].PreviousDigest() == changes[0].CurrentDigest() {
				t.Fatalf("changes = %#v", changes)
			}
		})
	}
}

func TestBaselinePreservesCanonicalEmptyMessageFieldArrays(t *testing.T) {
	t.Parallel()

	source := `package emptyv1

import "context"

//plystra:interface records.empty/v1
type Interface interface {
	Read(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`
	contract := parseContract(t, "example.com/acme/interfaces/records/empty/v1", source)
	baseline, err := interfacecompatibility.New([]interfacecontract.Contract{contract})
	if err != nil || !baseline.Valid() {
		t.Fatalf("New = %#v, %v", baseline, err)
	}
	if bytes.Count(baseline.RecordJSON(), []byte(`"fields":[]`)) != 2 {
		t.Fatalf("empty message fields are not canonical arrays: %s", baseline.RecordJSON())
	}
	decoded, err := interfacecompatibility.Decode(baseline.RecordJSON())
	if err != nil || !decoded.Valid() {
		t.Fatalf("Decode = %#v, %v", decoded, err)
	}
}

func TestComparisonClassifiesAddedRemovedAndChangedInterfaces(t *testing.T) {
	t.Parallel()

	records := parseContract(t, "example.com/acme/interfaces/records/echo/v1", baseInterfaceSource())
	accounts := parseContract(t, "example.com/acme/interfaces/accounts/read/v1", strings.ReplaceAll(baseInterfaceSource(), "records.echo/v1", "accounts.read/v1"))
	orders := parseContract(t, "example.com/acme/interfaces/orders/create/v1", strings.ReplaceAll(baseInterfaceSource(), "records.echo/v1", "orders.create/v1"))
	changedRecords := parseContract(t, "example.com/acme/interfaces/records/echo/v1", strings.Replace(baseInterfaceSource(), `json:"name"`, `json:"value"`, 1))

	previous, err := interfacecompatibility.New([]interfacecontract.Contract{records, accounts})
	if err != nil {
		t.Fatalf("New(previous): %v", err)
	}
	current, err := interfacecompatibility.New([]interfacecontract.Contract{changedRecords, orders})
	if err != nil {
		t.Fatalf("New(current): %v", err)
	}
	comparison, err := interfacecompatibility.Compare(previous, current)
	if err != nil || comparison.Clean() || !comparison.Valid() {
		t.Fatalf("Compare = %#v, %v", comparison, err)
	}
	changes := comparison.Changes()
	got := make([]string, len(changes))
	for index, change := range changes {
		got[index] = string(change.Kind()) + ":" + change.ID()
	}
	if !reflect.DeepEqual(got, []string{
		"removed:accounts.read/v1",
		"added:orders.create/v1",
		"changed:records.echo/v1",
	}) {
		t.Fatalf("changes = %#v", got)
	}
	if changes[0].PreviousDigest() == "" || changes[0].CurrentDigest() != "" ||
		changes[1].PreviousDigest() != "" || changes[1].CurrentDigest() == "" {
		t.Fatalf("change digests = %#v", changes)
	}
}

func TestDecodeRejectsMalformedTamperedAndNoncanonicalHistory(t *testing.T) {
	t.Parallel()

	contract := parseContract(t, "example.com/acme/interfaces/records/echo/v1", baseInterfaceSource())
	baseline, err := interfacecompatibility.New([]interfacecontract.Contract{contract})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	record := baseline.RecordJSON()
	tamperedDigest := append([]byte(nil), record...)
	digestIndex := bytes.Index(tamperedDigest, []byte(`"digest":"sha256:`))
	if digestIndex < 0 {
		t.Fatal("record has no digest")
	}
	digestIndex += len(`"digest":"sha256:`)
	if tamperedDigest[digestIndex] == '0' {
		tamperedDigest[digestIndex] = '1'
	} else {
		tamperedDigest[digestIndex] = '0'
	}

	tests := map[string][]byte{
		"empty":          nil,
		"unknown field":  bytes.Replace(record, []byte(`"schema":`), []byte(`"unknown":true,"schema":`), 1),
		"unknown schema": bytes.Replace(record, []byte(interfacecompatibility.Schema), []byte("plystra.interface-shape-baseline/v2"), 1),
		"invalid type":   bytes.Replace(record, []byte(`"type":"string"`), []byte(`"type":"pointer"`), 1),
		"missing request": bytes.Replace(
			record,
			[]byte(`"request":"Request"`),
			[]byte(`"request":"Missing"`),
			1,
		),
		"tampered digest": tamperedDigest,
		"whitespace":      append(append([]byte(nil), record...), '\n'),
		"trailing JSON":   append(append([]byte(nil), record...), []byte(` {}`)...),
		"malformed JSON":  []byte(`{"schema":`),
		"oversized":       bytes.Repeat([]byte{'x'}, int(interfacecompatibility.MaximumBytes)+1),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			decoded, err := interfacecompatibility.Decode(data)
			if !errors.Is(err, interfacecompatibility.ErrHistory) || decoded.Valid() {
				t.Fatalf("Decode = %#v, %v", decoded, err)
			}
		})
	}
}

func TestReconcileRequiresExactPriorOwnershipBytes(t *testing.T) {
	t.Parallel()

	contract := parseContract(t, "example.com/acme/interfaces/records/echo/v1", baseInterfaceSource())
	initial, comparison, err := interfacecompatibility.Reconcile([]interfacecontract.Contract{contract}, nil, false)
	if err != nil || !initial.Valid() || comparison.Clean() || !comparison.Valid() {
		t.Fatalf("Reconcile(initial) = %#v, %#v, %v", initial, comparison, err)
	}
	if changes := comparison.Changes(); len(changes) != 1 || changes[0].Kind() != interfacecompatibility.ChangeAdded {
		t.Fatalf("initial changes = %#v", changes)
	}
	repeated, comparison, err := interfacecompatibility.Reconcile([]interfacecontract.Contract{contract}, initial.RecordJSON(), true)
	if err != nil || !repeated.Valid() || !comparison.Clean() || !comparison.Valid() {
		t.Fatalf("Reconcile(repeated) = %#v, %#v, %v", repeated, comparison, err)
	}
	if _, _, err := interfacecompatibility.Reconcile([]interfacecontract.Contract{contract}, []byte("{}"), false); !errors.Is(err, interfacecompatibility.ErrHistory) {
		t.Fatalf("Reconcile(absent bytes) error = %v", err)
	}
}

func parseContract(t testing.TB, packagePath, source string) interfacecontract.Contract {
	t.Helper()
	files := token.NewFileSet()
	syntax, err := parser.ParseFile(files, "interface.go", source, parser.ParseComments|parser.AllErrors)
	if err != nil {
		t.Fatalf("ParseFile: %v\n%s", err, source)
	}
	checked, err := (&types.Config{Importer: importer.Default()}).Check(packagePath, files, []*ast.File{syntax}, nil)
	if err != nil {
		t.Fatalf("types.Check: %v\n%s", err, source)
	}
	declarations, err := interfacedecl.ParseFile("interface.go", []byte(source))
	if err != nil || len(declarations) != 1 {
		t.Fatalf("interfacedecl.ParseFile = %#v, %v", declarations, err)
	}
	contract, err := interfacecontract.Validate(declarations[0], checked)
	if err != nil {
		t.Fatalf("interfacecontract.Validate: %v", err)
	}
	return contract
}

func baseInterfaceSource() string {
	return `package echov1

import "context"

//plystra:interface records.echo/v1
type Interface interface {
	Echo(context.Context, Request) (Response, error)
}

type Request struct {
	Name   string ` + "`" + `plystra:"1,required" json:"name"` + "`" + `
	Detail Detail ` + "`" + `plystra:"2" json:"detail"` + "`" + `
}

type Detail struct {
	Code string ` + "`" + `plystra:"1" json:"code"` + "`" + `
}

type Response struct {
	Accepted bool ` + "`" + `plystra:"1" json:"accepted"` + "`" + `
}
`
}

func messageSummaries(messages []interfacecompatibility.Message) []string {
	result := make([]string, len(messages))
	for messageIndex, message := range messages {
		fields := message.Fields()
		parts := make([]string, len(fields))
		for fieldIndex, field := range fields {
			parts[fieldIndex] = fmt.Sprintf(
				"%d:%s:%s:%t:%s",
				field.Number(),
				field.GoName(),
				field.JSONName(),
				field.Required(),
				field.Type(),
			)
		}
		result[messageIndex] = message.Name() + ":" + strings.Join(parts, "|")
	}
	return result
}
