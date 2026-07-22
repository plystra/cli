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

	data := []byte(`examples:
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
    error:
      code: rejected
      details:
        method_name: value
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
		{name: "non-string key", path: "interfaces/order/interface.yaml", data: "1: value\n", want: "mapping keys must be nonempty strings"},
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
	for _, seed := range []string{"{}\n", "description: value\n", "[\n", "---\n{}\n---\n{}\n", "description: &value text\ncopy: *value\n"} {
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
	})
}
