package commandschema_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
)

func TestResultDiagnosticIsCanonicalSourceBearingAndDefensive(t *testing.T) {
	t.Parallel()

	locations := []diagnosticjson.Source{
		{Module: "example.com/zeta", Path: "zeta/plugin.yaml", Kind: "plugin-declaration", Line: 2, Column: 3},
		{Module: "example.com/alpha", Path: "plystra.yaml", Kind: "configuration-selection"},
	}
	diagnostic, err := commandschema.NewDiagnostic(commandschema.DiagnosticInput{
		Code:      diagnosticcode.ProviderAmbiguous,
		Severity:  diagnosticjson.SeverityError,
		Message:   "Select one compatible Provider.",
		Locations: locations,
	})
	if err != nil || !diagnostic.Valid() {
		t.Fatalf("NewDiagnostic = %#v, %v", diagnostic, err)
	}
	want := `{"code":"PLYSTRA_PROVIDER_AMBIGUOUS","severity":"error","message":"Select one compatible Provider.","locations":[{"module":"example.com/alpha","path":"plystra.yaml","kind":"configuration-selection"},{"module":"example.com/zeta","path":"zeta/plugin.yaml","kind":"plugin-declaration","line":2,"column":3}]}`
	if got := string(diagnostic.CanonicalJSON()); got != want {
		t.Fatalf("CanonicalJSON = %s\nwant = %s", got, want)
	}
	if diagnostic.Code() != diagnosticcode.ProviderAmbiguous || diagnostic.Severity() != diagnosticjson.SeverityError || diagnostic.Message() != "Select one compatible Provider." {
		t.Fatalf("diagnostic accessors = code %q severity %q message %q", diagnostic.Code(), diagnostic.Severity(), diagnostic.Message())
	}
	locations[0].Path = "mutated"
	returned := diagnostic.Locations()
	returned[0].Path = "mutated-again"
	canonical := diagnostic.CanonicalJSON()
	canonical[0] = '['
	if !diagnostic.Valid() || diagnostic.Locations()[0].Path != "plystra.yaml" || bytes.HasPrefix(diagnostic.CanonicalJSON(), []byte("[")) {
		t.Fatal("diagnostic exposed mutable construction or result state")
	}

	withoutLocations := newResultDiagnostic(t, diagnosticcode.ProjectCreateFailed, diagnosticjson.SeverityError, "Project creation failed.", nil)
	if got := string(withoutLocations.CanonicalJSON()); got != `{"code":"PLYSTRA_PROJECT_CREATE_FAILED","severity":"error","message":"Project creation failed.","locations":[]}` {
		t.Fatalf("empty locations = %s", got)
	}
}

func TestResultDiagnosticRejectsInvalidOrDuplicatedInput(t *testing.T) {
	t.Parallel()

	valid := commandschema.DiagnosticInput{
		Code:     diagnosticcode.ProjectCreateFailed,
		Severity: diagnosticjson.SeverityError,
		Message:  "Project creation failed.",
	}
	tests := []struct {
		name   string
		mutate func(*commandschema.DiagnosticInput)
	}{
		{name: "code", mutate: func(input *commandschema.DiagnosticInput) { input.Code = "invalid" }},
		{name: "severity", mutate: func(input *commandschema.DiagnosticInput) { input.Severity = "fatal" }},
		{name: "message", mutate: func(input *commandschema.DiagnosticInput) { input.Message = "" }},
		{name: "message control", mutate: func(input *commandschema.DiagnosticInput) { input.Message = "failed\nagain" }},
		{name: "message length", mutate: func(input *commandschema.DiagnosticInput) { input.Message = strings.Repeat("x", 4_097) }},
		{name: "location", mutate: func(input *commandschema.DiagnosticInput) {
			input.Locations = []diagnosticjson.Source{{Module: "example.com/app", Path: "../secret", Kind: "configuration-value"}}
		}},
		{name: "duplicate location", mutate: func(input *commandschema.DiagnosticInput) {
			source := diagnosticjson.Source{Module: "example.com/app", Path: "plystra.yaml", Kind: "configuration-value"}
			input.Locations = []diagnosticjson.Source{source, source}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.mutate(&input)
			diagnostic, err := commandschema.NewDiagnostic(input)
			if !errors.Is(err, commandschema.ErrDiagnostic) || diagnostic.Valid() {
				t.Fatalf("NewDiagnostic = %#v, %v; want ErrDiagnostic", diagnostic, err)
			}
		})
	}
	if (commandschema.Diagnostic{}).Valid() {
		t.Fatal("zero result diagnostic is valid")
	}
}
