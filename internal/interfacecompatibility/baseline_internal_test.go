package interfacecompatibility

import (
	"errors"
	"strings"
	"testing"
)

func TestDecodeAcceptsCanonicalShapeV1ForMigration(t *testing.T) {
	t.Parallel()

	messages := []wireMessage{
		{Name: "Request", Fields: []wireField{{Number: 1, GoName: "Value", JSONName: "value", Type: "string"}}},
		{Name: "Response", Fields: []wireField{}},
	}
	legacy, err := Decode(encodedShapeHistoryForSchema(t, baselineSchemaV1, messages))
	if err != nil || !legacy.Valid() || legacy.Schema() != baselineSchemaV1 {
		t.Fatalf("Decode(v1) = %#v, %v", legacy, err)
	}
	current, err := Decode(encodedShapeHistoryForSchema(t, Schema, messages))
	if err != nil || !current.Valid() || current.Schema() != Schema {
		t.Fatalf("Decode(v2) = %#v, %v", current, err)
	}
	comparison, err := Compare(legacy, current)
	if err != nil || !comparison.Valid() || !comparison.Clean() || comparison.PreviousDigest() == comparison.CurrentDigest() {
		t.Fatalf("Compare(v1, v2) = %#v, %v", comparison, err)
	}
	if comparison, err := Compare(current, legacy); !errors.Is(err, ErrInvalid) || comparison.Valid() || !strings.Contains(err.Error(), "cannot downgrade from v2 to v1") {
		t.Fatalf("Compare(v2, v1) = %#v, %v", comparison, err)
	}

	pointerMessages := []wireMessage{
		{Name: "Request", Fields: []wireField{{Number: 1, GoName: "Value", JSONName: "value", PointerDepth: 1, Type: "string"}}},
		{Name: "Response", Fields: []wireField{}},
	}
	decoded, err := Decode(encodedShapeHistoryForSchema(t, baselineSchemaV1, pointerMessages))
	if !errors.Is(err, ErrHistory) || decoded.Valid() || !strings.Contains(err.Error(), "unavailable in shape schema v1") {
		t.Fatalf("Decode(v1 pointer) = %#v, %v", decoded, err)
	}
}

func TestDecodeRejectsUnreachableAndInvalidRecursiveMessageGraphs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		messages []wireMessage
		want     string
	}{
		{
			name: "unreachable message",
			messages: []wireMessage{
				{Name: "Extra", Fields: []wireField{}},
				{Name: "Request", Fields: []wireField{}},
				{Name: "Response", Fields: []wireField{}},
			},
			want: "message Extra is unreachable",
		},
		{
			name: "direct value cycle",
			messages: []wireMessage{
				{Name: "First", Fields: []wireField{{Number: 1, GoName: "Second", JSONName: "second", Type: "message:Second"}}},
				{Name: "Request", Fields: []wireField{{Number: 1, GoName: "Root", JSONName: "root", Type: "message:First"}}},
				{Name: "Response", Fields: []wireField{}},
				{Name: "Second", Fields: []wireField{{Number: 1, GoName: "First", JSONName: "first", Type: "message:First"}}},
			},
			want: "direct-value message cycle First -> Second -> First",
		},
		{
			name: "pointer created cycle",
			messages: []wireMessage{
				{Name: "First", Fields: []wireField{{Number: 1, GoName: "Second", JSONName: "second", PointerDepth: 1, Type: "message:Second"}}},
				{Name: "Request", Fields: []wireField{{Number: 1, GoName: "Root", JSONName: "root", Type: "message:First"}}},
				{Name: "Response", Fields: []wireField{}},
				{Name: "Second", Fields: []wireField{{Number: 1, GoName: "First", JSONName: "first", Type: "repeated<message:First>"}}},
			},
			want: "pointer-to-message field First.Second creates a recursive cycle",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			record := encodedShapeHistory(t, test.messages)
			decoded, err := Decode(record)
			if !errors.Is(err, ErrHistory) || decoded.Valid() || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode = %#v, %v; want %q", decoded, err, test.want)
			}
		})
	}
}

func TestDecodeAcceptsCollectionBrokenRecursionIncludingPointerWrappedCollections(t *testing.T) {
	t.Parallel()

	record := encodedShapeHistory(t, []wireMessage{
		{Name: "Node", Fields: []wireField{
			{Number: 1, GoName: "Children", JSONName: "children", Type: "repeated<message:Node>"},
			{Number: 2, GoName: "OptionalChildren", JSONName: "optional_children", PointerDepth: 1, Type: "repeated<message:Node>"},
			{Number: 3, GoName: "NullableIndex", JSONName: "nullable_index", PointerDepth: 2, Type: "map<string,message:Node>"},
		}},
		{Name: "Request", Fields: []wireField{{Number: 1, GoName: "Root", JSONName: "root", Type: "message:Node"}}},
		{Name: "Response", Fields: []wireField{}},
	})
	decoded, err := Decode(record)
	if err != nil || !decoded.Valid() {
		t.Fatalf("Decode = %#v, %v", decoded, err)
	}
}

func encodedShapeHistory(t testing.TB, messages []wireMessage) []byte {
	return encodedShapeHistoryForSchema(t, Schema, messages)
}

func encodedShapeHistoryForSchema(t testing.TB, schema string, messages []wireMessage) []byte {
	t.Helper()
	value := wireInterface{
		ID:          "records.echo/v1",
		PackagePath: "example.com/acme/interfaces/records/echo/v1",
		Method:      "Echo",
		Request:     "Request",
		Response:    "Response",
		Messages:    messages,
	}
	shape, err := encodeShape(schema, value)
	if err != nil {
		t.Fatalf("encodeShape: %v", err)
	}
	value.Digest = digest(shape)
	canonical, err := encodeCanonical(schema, []wireInterface{value})
	if err != nil {
		t.Fatalf("encodeCanonical: %v", err)
	}
	record, err := encodeRecord(schema, []wireInterface{value}, digest(canonical))
	if err != nil {
		t.Fatalf("encodeRecord: %v", err)
	}
	return record
}
