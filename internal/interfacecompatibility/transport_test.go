package interfacecompatibility

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
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
)

func TestBuildTransportProjectsDescriptorProcedureAndWireMap(t *testing.T) {
	t.Parallel()

	wireMap, evidence := transportArtifacts(t, transportInterfaceSource("string"))
	baseline, err := BuildTransport(wireMap, evidence)
	if err != nil || !baseline.Valid() {
		t.Fatalf("BuildTransport = %#v, %v", baseline, err)
	}
	const wantDigest = "sha256:12f7ab01646418e779323839aa40cd3074cb0ecd3ce1c7923f1c27c0cd2942be"
	if baseline.Schema() != TransportSchema || baseline.Digest() != wantDigest {
		t.Fatalf("transport baseline schema = %q, digest = %q, canonical = %s", baseline.Schema(), baseline.Digest(), baseline.CanonicalJSON())
	}
	interfaces := baseline.Interfaces()
	if len(interfaces) != 1 ||
		interfaces[0].ID() != "records.echo/v1" ||
		!validDigest(interfaces[0].DescriptorDigest()) ||
		!validDigest(interfaces[0].ProcedureDigest()) ||
		!validDigest(interfaces[0].WireMapDigest()) {
		t.Fatalf("transport interfaces = %#v", interfaces)
	}
	if interfaces[0].DescriptorDigest() == interfaces[0].ProcedureDigest() ||
		interfaces[0].DescriptorDigest() == interfaces[0].WireMapDigest() ||
		interfaces[0].ProcedureDigest() == interfaces[0].WireMapDigest() {
		t.Fatalf("transport class digests are not independently domain-specific: %#v", interfaces[0])
	}
	for _, forbidden := range []string{
		"example.com/transport",
		"RecordsEchoV1Service",
		"/plystra.generated.records.echo.v1",
		"Message",
		"secret",
		"implementation",
	} {
		if bytes.Contains(bytes.ToLower(baseline.RecordJSON()), bytes.ToLower([]byte(forbidden))) {
			t.Fatalf("transport baseline contains forbidden projection value %q: %s", forbidden, baseline.RecordJSON())
		}
	}

	record := baseline.RecordJSON()
	decoded, err := DecodeTransport(record)
	if err != nil || !decoded.Valid() || decoded.Digest() != baseline.Digest() ||
		!bytes.Equal(decoded.CanonicalJSON(), baseline.CanonicalJSON()) {
		t.Fatalf("DecodeTransport = %#v, %v", decoded, err)
	}
	record[0] ^= 0xff
	interfaces[0] = TransportInterface{}
	if !baseline.Valid() || baseline.Interfaces()[0].ID() != "records.echo/v1" {
		t.Fatal("transport baseline exposed mutable storage")
	}
}

func TestBuildTransportSeparatesProjectionClasses(t *testing.T) {
	t.Parallel()

	baseRecord := transportRecord("records.echo/v1", "descriptor-a", "procedure-a", "wire-a")
	base, err := buildTransport([]transportWireInterface{baseRecord})
	if err != nil {
		t.Fatalf("buildTransport(base): %v", err)
	}
	tests := []struct {
		name  string
		class TransportClass
		edit  func(*transportWireInterface)
	}{
		{
			name:  "descriptor",
			class: TransportClassDescriptor,
			edit:  func(value *transportWireInterface) { value.DescriptorDigest = digest([]byte("descriptor-b")) },
		},
		{
			name:  "procedure",
			class: TransportClassProcedure,
			edit:  func(value *transportWireInterface) { value.ProcedureDigest = digest([]byte("procedure-b")) },
		},
		{
			name:  "wire map",
			class: TransportClassWireMap,
			edit:  func(value *transportWireInterface) { value.WireMapDigest = digest([]byte("wire-b")) },
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			changedRecord := baseRecord
			test.edit(&changedRecord)
			changed, err := buildTransport([]transportWireInterface{changedRecord})
			if err != nil {
				t.Fatalf("buildTransport(changed): %v", err)
			}
			comparison, err := CompareTransport(base, changed)
			if err != nil || !comparison.Valid() || comparison.Clean() {
				t.Fatalf("CompareTransport = %#v, %v", comparison, err)
			}
			changes := comparison.Changes()
			if len(changes) != 1 ||
				changes[0].Kind() != ChangeChanged ||
				changes[0].ID() != baseRecord.ID ||
				!reflect.DeepEqual(changes[0].Classes(), []TransportClass{test.class}) {
				t.Fatalf("transport changes = %#v", changes)
			}
			previous, previousExists := changes[0].PreviousDigest(test.class)
			current, currentExists := changes[0].CurrentDigest(test.class)
			if !previousExists || !currentExists || previous == current {
				t.Fatalf("class %s digests = %q, %t -> %q, %t", test.class, previous, previousExists, current, currentExists)
			}
			classes := changes[0].Classes()
			classes[0] = TransportClass("mutated")
			if !comparison.Valid() || changes[0].Classes()[0] != test.class {
				t.Fatal("transport comparison exposed mutable class storage")
			}
		})
	}
}

func TestCompareTransportReportsDeterministicAddedAndRemovedInterfaces(t *testing.T) {
	t.Parallel()

	records := transportRecord("records.echo/v1", "records-descriptor", "records-procedure", "records-wire")
	accounts := transportRecord("accounts.read/v1", "accounts-descriptor", "accounts-procedure", "accounts-wire")
	orders := transportRecord("orders.submit/v1", "orders-descriptor", "orders-procedure", "orders-wire")
	previous, err := buildTransport([]transportWireInterface{accounts, records})
	if err != nil {
		t.Fatalf("buildTransport(previous): %v", err)
	}
	current, err := buildTransport([]transportWireInterface{orders, records})
	if err != nil {
		t.Fatalf("buildTransport(current): %v", err)
	}
	comparison, err := CompareTransport(previous, current)
	if err != nil || !comparison.Valid() || comparison.Clean() {
		t.Fatalf("CompareTransport = %#v, %v", comparison, err)
	}
	changes := comparison.Changes()
	if len(changes) != 2 ||
		changes[0].ID() != "accounts.read/v1" ||
		changes[0].Kind() != ChangeRemoved ||
		changes[1].ID() != "orders.submit/v1" ||
		changes[1].Kind() != ChangeAdded ||
		!reflect.DeepEqual(changes[0].Classes(), allTransportClasses()) ||
		!reflect.DeepEqual(changes[1].Classes(), allTransportClasses()) {
		t.Fatalf("transport changes = %#v", changes)
	}
}

func TestTransportBaselineIsInputOrderIndependent(t *testing.T) {
	t.Parallel()

	records := transportRecord("records.echo/v1", "records-descriptor", "records-procedure", "records-wire")
	accounts := transportRecord("accounts.read/v1", "accounts-descriptor", "accounts-procedure", "accounts-wire")
	firstRecords := []transportWireInterface{records, accounts}
	sortTransportRecords(firstRecords)
	first, err := buildTransport(firstRecords)
	if err != nil {
		t.Fatalf("buildTransport(first): %v", err)
	}
	secondRecords := []transportWireInterface{accounts, records}
	sortTransportRecords(secondRecords)
	second, err := buildTransport(secondRecords)
	if err != nil {
		t.Fatalf("buildTransport(second): %v", err)
	}
	if first.Digest() != second.Digest() || !bytes.Equal(first.RecordJSON(), second.RecordJSON()) {
		t.Fatalf("input order changed transport baseline:\n%s\n%s", first.RecordJSON(), second.RecordJSON())
	}
}

func TestDecodeTransportRejectsMalformedHistory(t *testing.T) {
	t.Parallel()

	recordValue := transportRecord("records.echo/v1", "descriptor", "procedure", "wire")
	baseline, err := buildTransport([]transportWireInterface{recordValue})
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}
	record := baseline.RecordJSON()
	tests := map[string][]byte{
		"empty":          nil,
		"unknown schema": bytes.Replace(record, []byte(TransportSchema), []byte("plystra.interface-transport-baseline/v2"), 1),
		"unknown field":  bytes.Replace(record, []byte(`"digest":`), []byte(`"unknown":true,"digest":`), 1),
		"tampered class": bytes.Replace(record, []byte(recordValue.DescriptorDigest), []byte(digest([]byte("tampered"))), 1),
		"nil interfaces": []byte(`{"schema":"` + TransportSchema + `","interfaces":null,"digest":"` + digest([]byte("nil")) + `"}`),
		"trailing":       append(append([]byte(nil), record...), '\n'),
		"oversized":      bytes.Repeat([]byte{'x'}, int(TransportMaximumBytes)+1),
	}
	for name, data := range tests {
		name, data := name, data
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			decoded, err := DecodeTransport(data)
			if !errors.Is(err, ErrHistory) || decoded.Valid() {
				t.Fatalf("DecodeTransport(%s) = %#v, %v", name, decoded, err)
			}
		})
	}
}

func TestReconcileTransportUsesStrictOwnedHistory(t *testing.T) {
	t.Parallel()

	wireMap, evidence := transportArtifacts(t, transportInterfaceSource("string"))
	initial, comparison, err := ReconcileTransport(wireMap, evidence, nil, false)
	if err != nil || !initial.Valid() || !comparison.Valid() || comparison.Clean() {
		t.Fatalf("ReconcileTransport(initial) = %#v, %#v, %v", initial, comparison, err)
	}
	changes := comparison.Changes()
	if len(changes) != 1 || changes[0].Kind() != ChangeAdded {
		t.Fatalf("initial changes = %#v", changes)
	}
	repeated, comparison, err := ReconcileTransport(wireMap, evidence, initial.RecordJSON(), true)
	if err != nil || repeated.Digest() != initial.Digest() || !comparison.Valid() || !comparison.Clean() {
		t.Fatalf("ReconcileTransport(repeated) = %#v, %#v, %v", repeated, comparison, err)
	}
	if _, _, err := ReconcileTransport(wireMap, evidence, []byte("{}"), false); !errors.Is(err, ErrHistory) {
		t.Fatalf("ReconcileTransport(absent bytes) error = %v", err)
	}
	if _, _, err := ReconcileTransport(wireMap, evidence, []byte("{}"), true); !errors.Is(err, ErrHistory) {
		t.Fatalf("ReconcileTransport(malformed) error = %v", err)
	}
}

func TestBuildTransportDetectsDescriptorChangesWithoutProcedureDrift(t *testing.T) {
	t.Parallel()

	stringMap, stringEvidence := transportArtifacts(t, transportInterfaceSource("string"))
	stringBaseline, err := BuildTransport(stringMap, stringEvidence)
	if err != nil {
		t.Fatalf("BuildTransport(string): %v", err)
	}
	bytesMap, bytesEvidence := transportArtifacts(t, transportInterfaceSource("[]byte"))
	bytesBaseline, err := BuildTransport(bytesMap, bytesEvidence)
	if err != nil {
		t.Fatalf("BuildTransport(bytes): %v", err)
	}
	comparison, err := CompareTransport(stringBaseline, bytesBaseline)
	if err != nil || !comparison.Valid() || comparison.Clean() {
		t.Fatalf("CompareTransport = %#v, %v", comparison, err)
	}
	changes := comparison.Changes()
	if len(changes) != 1 ||
		!reflect.DeepEqual(
			changes[0].Classes(),
			[]TransportClass{TransportClassDescriptor, TransportClassWireMap},
		) {
		t.Fatalf("descriptor change classes = %#v", changes)
	}
}

func transportRecord(id, descriptor, procedure, wire string) transportWireInterface {
	return transportWireInterface{
		ID:               id,
		DescriptorDigest: digest([]byte(descriptor)),
		ProcedureDigest:  digest([]byte(procedure)),
		WireMapDigest:    digest([]byte(wire)),
	}
}

func sortTransportRecords(records []transportWireInterface) {
	for left := 0; left < len(records); left++ {
		for right := left + 1; right < len(records); right++ {
			if records[right].ID < records[left].ID {
				records[left], records[right] = records[right], records[left]
			}
		}
	}
}

func transportArtifacts(t testing.TB, source string) (protobufwiremap.Map, protobufdescriptor.Evidence) {
	t.Helper()
	const packagePath = "example.com/transport/interfaces/records/echo/v1"
	declarations, err := interfacedecl.ParseFile("interface.go", []byte(source))
	if err != nil || len(declarations) != 1 {
		t.Fatalf("interfacedecl.ParseFile = %#v, %v", declarations, err)
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
	interfaces, err := protobufmodel.BuildInterfaces(true, []protobufmodel.InterfaceInput{{
		InterfaceID:    contract.ID(),
		PackagePath:    packagePath,
		Source:         packagePath + "/interface.go:5:1",
		Contract:       contract,
		ContractDigest: "sha256:" + hex.EncodeToString(sum[:]),
	}})
	if err != nil {
		t.Fatalf("protobufmodel.BuildInterfaces: %v", err)
	}
	legacy, err := protobufmodel.Build(true, nil, nil)
	if err != nil {
		t.Fatalf("protobufmodel.Build: %v", err)
	}
	wireMap, err := protobufwiremap.Build(legacy, interfaces, nil, false, "")
	if err != nil {
		t.Fatalf("protobufwiremap.Build: %v", err)
	}
	evidence, err := protobufdescriptor.BuildWithInterfaces(legacy, wireMap, interfaces)
	if err != nil {
		t.Fatalf("protobufdescriptor.BuildWithInterfaces: %v", err)
	}
	return wireMap, evidence
}

func transportInterfaceSource(fieldType string) string {
	return strings.ReplaceAll(`package echov1

import "context"

//plystra:interface records.echo/v1
type Interface interface {
	Echo(context.Context, Request) (Response, error)
}

type Request struct {
	Message FIELD_TYPE `+"`json:\"message\" plystra:\"1,required\"`"+`
}

type Response struct {
	Message FIELD_TYPE `+"`json:\"message\" plystra:\"1,required\"`"+`
}
`, "FIELD_TYPE", fieldType)
}
