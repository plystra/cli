package interfacemeta_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacemeta"
)

func TestParseFileNormalizesClosedConformanceConfiguration(t *testing.T) {
	t.Parallel()

	tests := []string{
		"conformance:\n  package: ./conformance\n",
		"conformance: {package: ./conformance}\n",
		"conformance:\n  package: \"./conformance\"\n",
	}
	var normalized interfacemeta.Conformance
	for _, data := range tests {
		document, err := interfacemeta.ParseFile("interfaces/order/interface.yaml", []byte(data))
		if err != nil {
			t.Fatal(err)
		}
		conformance, present := document.Conformance()
		if !present || conformance.Package() != interfacemeta.CanonicalConformancePackage {
			t.Fatalf("conformance = %#v, %t", conformance, present)
		}
		if normalized.Package() == "" {
			normalized = conformance
		} else if !reflect.DeepEqual(normalized, conformance) {
			t.Fatalf("equivalent conformance configurations differ: %#v and %#v", normalized, conformance)
		}
	}
}

func TestParseFileAcceptsAbsentConformanceConfiguration(t *testing.T) {
	t.Parallel()

	document, err := interfacemeta.ParseFile("interfaces/order/interface.yaml", []byte("{}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if conformance, present := document.Conformance(); present || conformance.Package() != "" {
		t.Fatalf("conformance = %#v, %t", conformance, present)
	}
}

func TestParseFileRejectsInvalidConformanceConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		location string
		want     string
	}{
		{name: "null", data: "conformance: null\n", location: "interface.yaml:1:14", want: "must be a mapping"},
		{name: "scalar", data: "conformance: ./conformance\n", location: "interface.yaml:1:14", want: "must be a mapping"},
		{name: "sequence", data: "conformance: []\n", location: "interface.yaml:1:14", want: "must be a mapping"},
		{name: "empty", data: "conformance: {}\n", location: "interface.yaml:1:14", want: "conformance.package is missing"},
		{name: "unknown field", data: "conformance:\n  package: ./conformance\n  api: v1\n", location: "interface.yaml:3:3", want: "conformance.api"},
		{name: "command field", data: "conformance:\n  package: ./conformance\n  command: go test\n", location: "interface.yaml:3:3", want: "conformance.command"},
		{name: "null package", data: "conformance:\n  package: null\n", location: "interface.yaml:2:12", want: "must be the exact string"},
		{name: "boolean package", data: "conformance:\n  package: true\n", location: "interface.yaml:2:12", want: "must be the exact string"},
		{name: "empty package", data: "conformance:\n  package: \"\"\n", location: "interface.yaml:2:12", want: `must be exactly "./conformance"`},
		{name: "missing dot", data: "conformance:\n  package: conformance\n", location: "interface.yaml:2:12", want: `must be exactly "./conformance"`},
		{name: "trailing separator", data: "conformance:\n  package: ./conformance/\n", location: "interface.yaml:2:12", want: `must be exactly "./conformance"`},
		{name: "subdirectory", data: "conformance:\n  package: ./conformance/suite\n", location: "interface.yaml:2:12", want: `must be exactly "./conformance"`},
		{name: "traversal", data: "conformance:\n  package: ../conformance\n", location: "interface.yaml:2:12", want: `must be exactly "./conformance"`},
		{name: "absolute", data: "conformance:\n  package: /conformance\n", location: "interface.yaml:2:12", want: `must be exactly "./conformance"`},
		{name: "backslash", data: "conformance:\n  package: '.\\conformance'\n", location: "interface.yaml:2:12", want: `must be exactly "./conformance"`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document, err := interfacemeta.ParseFile("interfaces/order/interface.yaml", []byte(test.data))
			if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrInvalidConformance) || document.Path() != "" || !strings.Contains(err.Error(), test.location) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFile = %#v, %v; want %q at %s", document, err, test.want, test.location)
			}
		})
	}
}
