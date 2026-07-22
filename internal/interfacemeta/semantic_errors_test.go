package interfacemeta_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacemeta"
)

func TestParseFileNormalizesSemanticErrorsAsDeterministicSet(t *testing.T) {
	t.Parallel()

	data := []byte(`errors:
  - code: temporarily_unavailable
    description: Try again later.
  - description: The order already exists.
    code: order_already_exists
  - code: error_404
`)
	document, err := interfacemeta.ParseFile("interfaces/order/create/v1/interface.yaml", data)
	if err != nil || string(document.Data()) != string(data) {
		t.Fatalf("ParseFile = %#v, %v", document, err)
	}
	declared := document.Errors()
	if len(declared) != 3 || declared[0].Code() != "error_404" || declared[1].Code() != "order_already_exists" || declared[2].Code() != "temporarily_unavailable" {
		t.Fatalf("Errors = %#v", declared)
	}
	if description, present := declared[0].Description(); present || description != "" {
		t.Fatalf("description-less error = %q, %t", description, present)
	}
	if description, present := declared[1].Description(); !present || description != "The order already exists." {
		t.Fatalf("described error = %q, %t", description, present)
	}

	equivalent, err := interfacemeta.ParseFile("interfaces/order/create/v1/interface.yaml", []byte(`errors: [{code: error_404}, {code: order_already_exists, description: The order already exists.}, {description: Try again later., code: temporarily_unavailable}]
`))
	if err != nil {
		t.Fatal(err)
	}
	equivalentErrors := equivalent.Errors()
	for index := range declared {
		if declared[index] != equivalentErrors[index] {
			t.Fatalf("equivalent errors differ at %d: %#v and %#v", index, declared[index], equivalentErrors[index])
		}
	}

	declared[0] = interfacemeta.SemanticError{}
	if document.Errors()[0].Code() != "error_404" {
		t.Fatal("Errors exposed mutable normalized storage")
	}
}

func TestParseFileNormalizesAbsentAndExplicitlyEmptySemanticErrors(t *testing.T) {
	t.Parallel()

	for _, data := range []string{"{}\n", "errors: []\n"} {
		document, err := interfacemeta.ParseFile("interfaces/empty/interface.yaml", []byte(data))
		if err != nil || len(document.Errors()) != 0 {
			t.Fatalf("ParseFile(%q) = %#v, %v", data, document.Errors(), err)
		}
	}
}

func TestParseFileRejectsInvalidSemanticErrorSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		location string
		want     string
	}{
		{name: "null", data: "errors: null\n", location: "interface.yaml:1:9", want: "errors must be a sequence"},
		{name: "scalar", data: "errors: invalid_value\n", location: "interface.yaml:1:9", want: "errors must be a sequence"},
		{name: "mapping", data: "errors: {}\n", location: "interface.yaml:1:9", want: "errors must be a sequence"},
		{name: "old scalar item", data: "errors:\n  - invalid_value\n", location: "interface.yaml:2:5", want: "errors[0] must be a mapping"},
		{name: "null item", data: "errors:\n  - null\n", location: "interface.yaml:2:5", want: "errors[0] must be a mapping"},
		{name: "sequence item", data: "errors:\n  - []\n", location: "interface.yaml:2:5", want: "errors[0] must be a mapping"},
		{name: "missing code", data: "errors:\n  - description: Missing code.\n", location: "interface.yaml:2:5", want: "errors[0].code is missing"},
		{name: "unknown field", data: "errors:\n  - code: invalid_value\n    retryable: true\n", location: "interface.yaml:3:5", want: "errors[0].retryable"},
		{name: "null code", data: "errors:\n  - code: null\n", location: "interface.yaml:2:11", want: "code must be a lower-snake-case string"},
		{name: "sequence code", data: "errors:\n  - code: []\n", location: "interface.yaml:2:11", want: "code must be a lower-snake-case string"},
		{name: "boolean description", data: "errors:\n  - code: invalid_value\n    description: true\n", location: "interface.yaml:3:18", want: "description must be a string"},
		{name: "empty description", data: "errors:\n  - code: invalid_value\n    description: \"  \"\n", location: "interface.yaml:3:18", want: "description must not be empty"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document, err := interfacemeta.ParseFile("interfaces/errors/interface.yaml", []byte(test.data))
			if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrInvalidSemanticErrors) || len(document.Errors()) != 0 || !strings.Contains(err.Error(), test.location) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFile = %#v, %v; want %q at %q", document, err, test.want, test.location)
			}
		})
	}
}

func TestParseFileRejectsInvalidOrDuplicateSemanticErrorCodes(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"InvalidValue",
		"invalid-value",
		"invalid.value",
		"1invalid",
		"_invalid",
		"invalid_",
		"invalid__value",
		"inválid",
		strings.Repeat("a", 129),
	}
	for _, code := range invalid {
		code := code
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			document, err := interfacemeta.ParseFile("interfaces/errors/interface.yaml", []byte("errors:\n  - code: \""+code+"\"\n"))
			if !errors.Is(err, interfacemeta.ErrInvalidSemanticErrors) || len(document.Errors()) != 0 || !strings.Contains(err.Error(), "interface.yaml:2:11") || !strings.Contains(err.Error(), "lower snake case") {
				t.Fatalf("ParseFile(%q) = %#v, %v", code, document, err)
			}
		})
	}

	document, err := interfacemeta.ParseFile("interfaces/errors/interface.yaml", []byte("errors:\n  - code: repeated\n  - code: repeated\n"))
	if !errors.Is(err, interfacemeta.ErrInvalidSemanticErrors) || len(document.Errors()) != 0 || !strings.Contains(err.Error(), "interface.yaml:3:11") || !strings.Contains(err.Error(), "interfaces/errors/interface.yaml:2:11") || !strings.Contains(err.Error(), "duplicates") {
		t.Fatalf("duplicate ParseFile = %#v, %v", document, err)
	}
}
