package interfacemeta_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacemeta"
)

func TestParseFileNormalizesOptionalDescription(t *testing.T) {
	t.Parallel()

	tests := []string{
		"description: Lists records.\n",
		"description: 'Lists records.'\n",
		"description: \"Lists records.\"\n",
	}
	for _, data := range tests {
		document, err := interfacemeta.ParseFile("interfaces/records/interface.yaml", []byte(data))
		description, present := document.Description()
		if err != nil || !present || description != "Lists records." {
			t.Fatalf("ParseFile = %q, %t, %v", description, present, err)
		}
	}
}

func TestParseFileRejectsInvalidDescription(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		location string
		want     string
	}{
		{name: "null", data: "description: null\n", location: "interface.yaml:1:14", want: "must be a string"},
		{name: "boolean", data: "description: true\n", location: "interface.yaml:1:14", want: "must be a string"},
		{name: "mapping", data: "description: {}\n", location: "interface.yaml:1:14", want: "must be a string"},
		{name: "sequence", data: "description: []\n", location: "interface.yaml:1:14", want: "must be a string"},
		{name: "empty", data: "description: \"\"\n", location: "interface.yaml:1:14", want: "must not be empty"},
		{name: "whitespace", data: "description: '  '\n", location: "interface.yaml:1:14", want: "must not be empty"},
		{name: "NUL", data: "description: \"unsafe\\0text\"\n", location: "interface.yaml:1:14", want: "without NUL"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document, err := interfacemeta.ParseFile("interfaces/records/interface.yaml", []byte(test.data))
			if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrInvalidDescription) || document.Path() != "" || !strings.Contains(err.Error(), test.location) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFile = %#v, %v; want %q at %s", document, err, test.want, test.location)
			}
		})
	}
}
