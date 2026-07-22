package interfacemeta_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacemeta"
)

func TestParseFilePreservesValidMetadataBytes(t *testing.T) {
	t.Parallel()

	data := []byte("# public contract\ndescription: Lists records.\nsemantics:\n  kind: query\n")
	document, err := interfacemeta.ParseFile("interfaces/records/list/v1/interface.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	if document.Path() != "interfaces/records/list/v1/interface.yaml" || string(document.Data()) != string(data) {
		t.Fatalf("document = path %q data %q", document.Path(), document.Data())
	}
	semantics, present := document.Semantics()
	if !present || semantics.Kind() != interfacemeta.OperationKindQuery {
		t.Fatalf("semantics = %#v, %t", semantics, present)
	}
	view := document.Data()
	view[0] = 'x'
	if string(document.Data()) != string(data) {
		t.Fatal("Data exposed mutable document storage")
	}
}

func TestParseFileAcceptsEmptyMapping(t *testing.T) {
	t.Parallel()

	document, err := interfacemeta.ParseFile("interfaces/empty/interface.yaml", []byte("{}\n"))
	if err != nil || document.Path() == "" {
		t.Fatalf("ParseFile = %#v, %v", document, err)
	}
	if semantics, present := document.Semantics(); present || semantics.Kind() != "" {
		t.Fatalf("absent semantics = %#v, %t", semantics, present)
	}
	if deprecation, present := document.Deprecation(); present || deprecation.Message() != "" {
		t.Fatalf("absent deprecation = %#v, %t", deprecation, present)
	}
}

func TestParseFileAcceptsOnlyNonDuplicatedMetadataFields(t *testing.T) {
	t.Parallel()

	data := []byte(`description: Creates an order.
semantics:
  kind: command
errors:
  - code: already_exists
constraints:
  request.order_id:
    min_length: 1
examples:
  - name: accepted
    request:
      order_id: ord_123
    response:
      accepted: true
deprecation:
  message: use order.submit/v1
conformance:
  package: ./conformance
`)
	document, err := interfacemeta.ParseFile("interfaces/order/create/v1/interface.yaml", data)
	if err != nil || string(document.Data()) != string(data) {
		t.Fatalf("ParseFile = %#v, %v", document, err)
	}
	semantics, present := document.Semantics()
	if !present || semantics.Kind() != interfacemeta.OperationKindCommand {
		t.Fatalf("semantics = %#v, %t", semantics, present)
	}
	deprecation, present := document.Deprecation()
	if !present || deprecation.Message() != "use order.submit/v1" {
		t.Fatalf("deprecation = %#v, %t", deprecation, present)
	}
}

func TestParseFileNormalizesClosedOperationSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		kind interfacemeta.OperationKind
	}{
		{name: "query block", data: "semantics:\n  kind: query\n", kind: interfacemeta.OperationKindQuery},
		{name: "query flow", data: "semantics: {kind: query}\n", kind: interfacemeta.OperationKindQuery},
		{name: "query quoted", data: "semantics:\n  kind: \"query\"\n", kind: interfacemeta.OperationKindQuery},
		{name: "command block", data: "semantics:\n  kind: command\n", kind: interfacemeta.OperationKindCommand},
		{name: "command flow", data: "semantics: {kind: command}\n", kind: interfacemeta.OperationKindCommand},
	}
	var query interfacemeta.Semantics
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document, err := interfacemeta.ParseFile("interfaces/operation/interface.yaml", []byte(test.data))
			semantics, present := document.Semantics()
			if err != nil || !present || semantics.Kind() != test.kind || string(document.Data()) != test.data {
				t.Fatalf("ParseFile = %#v, %#v, %t, %v", document, semantics, present, err)
			}
		})
		if test.kind == interfacemeta.OperationKindQuery {
			document, err := interfacemeta.ParseFile("interfaces/operation/interface.yaml", []byte(test.data))
			if err != nil {
				t.Fatal(err)
			}
			semantics, _ := document.Semantics()
			if query.Kind() == "" {
				query = semantics
			} else if semantics != query {
				t.Fatalf("equivalent query semantics differ: %#v and %#v", query, semantics)
			}
		}
	}
}

func TestParseFileRejectsInvalidOperationSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		location string
		want     string
	}{
		{name: "null", data: "semantics: null\n", location: "interface.yaml:1:12", want: "semantics must be a mapping"},
		{name: "scalar", data: "semantics: query\n", location: "interface.yaml:1:12", want: "semantics must be a mapping"},
		{name: "sequence", data: "semantics: []\n", location: "interface.yaml:1:12", want: "semantics must be a mapping"},
		{name: "empty mapping", data: "semantics: {}\n", location: "interface.yaml:1:12", want: "semantics.kind is missing"},
		{name: "unknown field", data: "semantics:\n  behavior: query\n", location: "interface.yaml:2:3", want: "semantics.behavior"},
		{name: "extra field", data: "semantics:\n  kind: query\n  effects: none\n", location: "interface.yaml:3:3", want: "semantics.effects"},
		{name: "null kind", data: "semantics:\n  kind: null\n", location: "interface.yaml:2:9", want: "semantics.kind must be the string"},
		{name: "boolean kind", data: "semantics:\n  kind: true\n", location: "interface.yaml:2:9", want: "semantics.kind must be the string"},
		{name: "sequence kind", data: "semantics:\n  kind: []\n", location: "interface.yaml:2:9", want: "semantics.kind must be the string"},
		{name: "mapping kind", data: "semantics:\n  kind: {}\n", location: "interface.yaml:2:9", want: "semantics.kind must be the string"},
		{name: "event", data: "semantics:\n  kind: event\n", location: "interface.yaml:2:9", want: "expected query or command"},
		{name: "stream", data: "semantics:\n  kind: stream\n", location: "interface.yaml:2:9", want: "expected query or command"},
		{name: "case mismatch", data: "semantics:\n  kind: Query\n", location: "interface.yaml:2:9", want: "expected query or command"},
		{name: "empty kind", data: "semantics:\n  kind: \"\"\n", location: "interface.yaml:2:9", want: "expected query or command"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document, err := interfacemeta.ParseFile("interfaces/operation/interface.yaml", []byte(test.data))
			if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrInvalidSemantics) || document.Path() != "" || !strings.Contains(err.Error(), test.location) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFile = %#v, %v; want %q at %q", document, err, test.want, test.location)
			}
		})
	}
}

func TestParseFileRejectsLegacyCapabilitySemanticsFields(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"effects", "idempotency", "retry", "cancellation", "completion", "ordering", "data"} {
		field := field
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			data := []byte("semantics:\n  kind: query\n  " + field + ": {}\n")
			document, err := interfacemeta.ParseFile("interfaces/operation/interface.yaml", data)
			if !errors.Is(err, interfacemeta.ErrInvalidSemantics) || document.Path() != "" || !strings.Contains(err.Error(), "interface.yaml:3:3") || !strings.Contains(err.Error(), "semantics."+field) {
				t.Fatalf("ParseFile = %#v, %v", document, err)
			}
		})
	}
}

func TestParseFileRejectsAuthoritativeGoContractFields(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"id":                "Interface ID",
		"interface_id":      "Interface ID",
		"method":            "operation method",
		"method_name":       "operation method",
		"request":           "request type",
		"request_fields":    "request fields",
		"response":          "response type",
		"response_fields":   "response fields",
		"types":             "Go field types",
		"field_numbers":     "stable field numbers",
		"required_fields":   "required-field markers",
		"json_names":        "explicit JSON field names",
		"implementation":    "Implementation identity",
		"implementation_id": "Implementation identity",
		"constructor":       "Implementation constructor identity",
	}
	for field, authority := range tests {
		field, authority := field, authority
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			document, err := interfacemeta.ParseFile("interfaces/order/interface.yaml", []byte(field+": duplicate\n"))
			if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrAuthoritativeField) || document.Path() != "" || !strings.Contains(err.Error(), "interface.yaml:1:1") || !strings.Contains(err.Error(), authority) {
				t.Fatalf("ParseFile = %#v, %v", document, err)
			}
		})
	}
}

func TestParseFileRejectsNestedAuthoritativeFieldsOutsideExamplePayloads(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"semantics":    "semantics:\n  method: Create\n",
		"errors":       "errors:\n  - request_fields: duplicate\n",
		"deprecation":  "deprecation:\n  interface_id: order.submit/v1\n",
		"conformance":  "conformance:\n  constructor: example.com/acme.New\n",
		"example item": "examples:\n  - name: invalid\n    method_name: Create\n",
	}
	for name, data := range tests {
		name, data := name, data
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			document, err := interfacemeta.ParseFile("interfaces/order/interface.yaml", []byte(data))
			if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrAuthoritativeField) || document.Path() != "" || !strings.Contains(err.Error(), "authoritative in the Interface Go package") {
				t.Fatalf("ParseFile = %#v, %v", document, err)
			}
		})
	}
}

func TestParseFileAllowsContractLikeNamesInsideExampleApplicationData(t *testing.T) {
	t.Parallel()

	data := []byte(`errors:
  - code: rejected
examples:
  - name: contract-like-data
    request:
      id: value
      method: value
      request_fields: value
      response_fields: value
      types: value
      field_numbers: value
      required_fields: value
      json_names: value
      implementation: value
      constructor: value
    response:
      interface_id: value
  - name: rejected
    request:
      id: value
    error: rejected
`)
	document, err := interfacemeta.ParseFile("interfaces/order/interface.yaml", data)
	if err != nil || string(document.Data()) != string(data) {
		t.Fatalf("ParseFile = %#v, %v", document, err)
	}
}

func TestParseFileRejectsUnknownTopLevelField(t *testing.T) {
	t.Parallel()

	document, err := interfacemeta.ParseFile("interfaces/order/interface.yaml", []byte("custom: value\n"))
	if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrUnknownField) || errors.Is(err, interfacemeta.ErrAuthoritativeField) || document.Path() != "" || !strings.Contains(err.Error(), "unknown top-level field \"custom\"") {
		t.Fatalf("ParseFile = %#v, %v", document, err)
	}
}

func TestParseFileRejectsUnsafeOrMalformedDocuments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		data string
		want string
	}{
		{name: "absolute path", path: "/interface.yaml", data: "{}\n", want: "canonical module-relative"},
		{name: "wrong filename", path: "interfaces/metadata.yaml", data: "{}\n", want: "canonical module-relative"},
		{name: "empty", path: "interfaces/order/interface.yaml", want: "document is empty"},
		{name: "comments only", path: "interfaces/order/interface.yaml", data: "# empty\n", want: "expected one YAML document"},
		{name: "malformed", path: "interfaces/order/interface.yaml", data: "[\n", want: "decode YAML"},
		{name: "multiple documents", path: "interfaces/order/interface.yaml", data: "{}\n---\n{}\n", want: "multiple YAML documents"},
		{name: "sequence root", path: "interfaces/order/interface.yaml", data: "- description\n", want: "root must be a mapping"},
		{name: "scalar root", path: "interfaces/order/interface.yaml", data: "description\n", want: "root must be a mapping"},
		{name: "anchor", path: "interfaces/order/interface.yaml", data: "description: &text value\n", want: "anchors and aliases"},
		{name: "alias", path: "interfaces/order/interface.yaml", data: "description: &text value\ncopy: *text\n", want: "anchors and aliases"},
		{name: "duplicate key", path: "interfaces/order/interface.yaml", data: "description: one\ndescription: two\n", want: "duplicate mapping key"},
		{name: "non-string key", path: "interfaces/order/interface.yaml", data: "1: value\n", want: "mapping keys must be strings"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document, err := interfacemeta.ParseFile(test.path, []byte(test.data))
			if !errors.Is(err, interfacemeta.ErrInvalid) || document.Path() != "" || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFile = %#v, %v; want %q", document, err, test.want)
			}
		})
	}
}

func TestParseFileRejectsOversizedDocument(t *testing.T) {
	t.Parallel()

	document, err := interfacemeta.ParseFile("interfaces/order/interface.yaml", []byte(strings.Repeat("x", interfacemeta.MaximumSize+1)))
	if !errors.Is(err, interfacemeta.ErrInvalid) || document.Path() != "" || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("ParseFile = %#v, %v", document, err)
	}
}

func FuzzParseFile(f *testing.F) {
	for _, seed := range []string{"{}\n", "description: value\n", "semantics: {kind: query}\n", "semantics: {kind: command}\n", "semantics: {kind: event}\n", "errors: [{code: invalid_value}]\n", "errors: [invalid_value]\n", "examples: [{name: accepted, request: {}, response: {}}]\n", "errors: [{code: rejected}]\nexamples: [{name: rejected, request: {}, error: rejected}]\n", "deprecation: {message: obsolete}\n", "deprecation: {message: obsolete, replacement: order.create/v2, since: next-release}\n", "[\n", "---\n{}\n---\n{}\n", "description: &value text\ncopy: *value\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data string) {
		if len(data) > interfacemeta.MaximumSize+1 {
			t.Skip()
		}
		document, err := interfacemeta.ParseFile("interfaces/fuzz/interface.yaml", []byte(data))
		if err != nil {
			if !errors.Is(err, interfacemeta.ErrInvalid) || document.Path() != "" {
				t.Fatalf("ParseFile returned inconsistent error: %#v, %v", document, err)
			}
			return
		}
		if document.Path() == "" || string(document.Data()) != data {
			t.Fatalf("ParseFile returned inconsistent document: %#v", document)
		}
		if semantics, present := document.Semantics(); present && semantics.Kind() != interfacemeta.OperationKindQuery && semantics.Kind() != interfacemeta.OperationKindCommand {
			t.Fatalf("ParseFile returned unsupported semantics: %#v", semantics)
		}
		previousCode := ""
		for _, semanticError := range document.Errors() {
			if semanticError.Code() == "" || previousCode >= semanticError.Code() {
				t.Fatalf("ParseFile returned unordered semantic errors: %#v", document.Errors())
			}
			if description, present := semanticError.Description(); present && strings.TrimSpace(description) == "" {
				t.Fatalf("ParseFile returned an empty semantic-error description: %#v", semanticError)
			}
			previousCode = semanticError.Code()
		}
	})
}
