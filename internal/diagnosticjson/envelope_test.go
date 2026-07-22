package diagnosticjson_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/diagnosticjson"
)

func TestEnvelopeDefinesRequiredCanonicalShape(t *testing.T) {
	t.Parallel()

	input := validInput()
	input.ConfigurationMode = generation.ConfigurationModeEnvironment
	input.Diagnostics = []diagnosticjson.Diagnostic{
		{Code: "PLYSTRA-ZETA", Severity: diagnosticjson.SeverityWarning, Message: "Later finding."},
		{Code: "PLYSTRA-ALPHA", Severity: diagnosticjson.SeverityInfo, Message: "First finding."},
	}
	input.Sources = []diagnosticjson.Source{
		{Module: "example.com/platform", Path: "mailer/plugin.yaml", Kind: "plugin-declaration"},
		{Module: "example.com/app", Path: "plystra.production.yaml", Kind: "provider-selection", Line: 7, Column: 5},
	}
	input.Result = json.RawMessage(`{
		"z": 1.0,
		"nested": {"z": true, "a": -0.0},
		"array": [3, 2],
		"a": 1
	}`)

	first, err := diagnosticjson.New(input)
	if err != nil || !first.Valid() {
		t.Fatalf("New = %#v, %v", first, err)
	}
	want := `{"schema":"plystra.inspect","schema_version":1,"configuration_mode":"environment","application_model_digest":"` + testDigest("a") + `","diagnostics":[{"code":"PLYSTRA-ALPHA","severity":"info","message":"First finding."},{"code":"PLYSTRA-ZETA","severity":"warning","message":"Later finding."}],"sources":[{"module":"example.com/app","path":"plystra.production.yaml","kind":"provider-selection","line":7,"column":5},{"module":"example.com/platform","path":"mailer/plugin.yaml","kind":"plugin-declaration"}],"result":{"a":1,"array":[3,2],"nested":{"a":0,"z":true},"z":1}}`
	if got := string(first.CanonicalJSON()); got != want || !json.Valid([]byte(got)) {
		t.Fatalf("CanonicalJSON = %s\nwant          = %s", got, want)
	}
	if first.Schema() != input.Schema || first.SchemaVersion() != input.SchemaVersion || first.ConfigurationMode() != input.ConfigurationMode || first.ApplicationModelDigest() != input.ApplicationModelDigest || string(first.ResultJSON()) != `{"a":1,"array":[3,2],"nested":{"a":0,"z":true},"z":1}` || !strings.HasPrefix(first.Digest(), "sha256:") || len(first.Digest()) != 71 {
		t.Fatalf("accessors do not preserve canonical identity: %#v", first)
	}

	reordered := input
	reordered.Diagnostics = []diagnosticjson.Diagnostic{input.Diagnostics[1], input.Diagnostics[0]}
	reordered.Sources = []diagnosticjson.Source{input.Sources[1], input.Sources[0]}
	reordered.Result = json.RawMessage(`{"array":[3,2],"a":1,"nested":{"a":0e9,"z":true},"z":1}`)
	second, err := diagnosticjson.New(reordered)
	if err != nil || second.Digest() != first.Digest() || !bytes.Equal(second.CanonicalJSON(), first.CanonicalJSON()) {
		t.Fatalf("equivalent reordered input changed envelope: %s, %s, %v", second.CanonicalJSON(), second.Digest(), err)
	}
}

func TestEnvelopeAcceptsEveryConfigurationModeAndEmptyCollections(t *testing.T) {
	t.Parallel()

	for _, mode := range []generation.ConfigurationMode{
		generation.ConfigurationModeDefault,
		generation.ConfigurationModeEnvironment,
		generation.ConfigurationModeExplicit,
	} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			input := validInput()
			input.ConfigurationMode = mode
			envelope, err := diagnosticjson.New(input)
			if err != nil || !envelope.Valid() {
				t.Fatalf("New(%s) = %#v, %v", mode, envelope, err)
			}
			for _, required := range []string{
				`"schema":"plystra.inspect"`,
				`"schema_version":1`,
				`"configuration_mode":"` + string(mode) + `"`,
				`"application_model_digest":"` + testDigest("a") + `"`,
				`"diagnostics":[]`,
				`"sources":[]`,
				`"result":{}`,
			} {
				if !bytes.Contains(envelope.CanonicalJSON(), []byte(required)) {
					t.Fatalf("CanonicalJSON omits %s: %s", required, envelope.CanonicalJSON())
				}
			}
		})
	}
}

func TestEnvelopeRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*diagnosticjson.Input)
		want   string
	}{
		{name: "empty", mutate: func(input *diagnosticjson.Input) { *input = diagnosticjson.Input{} }, want: "schema"},
		{name: "foreign schema", mutate: func(input *diagnosticjson.Input) { input.Schema = "acme.inspect" }, want: "schema"},
		{name: "schema embeds version", mutate: func(input *diagnosticjson.Input) { input.Schema = "plystra.inspect/v1" }, want: "schema"},
		{name: "noncanonical schema", mutate: func(input *diagnosticjson.Input) { input.Schema = "plystra.Inspect" }, want: "schema"},
		{name: "zero version", mutate: func(input *diagnosticjson.Input) { input.SchemaVersion = 0 }, want: "schema version"},
		{name: "excessive version", mutate: func(input *diagnosticjson.Input) { input.SchemaVersion = 1 << 31 }, want: "schema version"},
		{name: "unsupported mode", mutate: func(input *diagnosticjson.Input) { input.ConfigurationMode = "profile" }, want: "configuration mode"},
		{name: "missing digest", mutate: func(input *diagnosticjson.Input) { input.ApplicationModelDigest = "" }, want: "application-model digest"},
		{name: "uppercase digest", mutate: func(input *diagnosticjson.Input) { input.ApplicationModelDigest = "sha256:" + strings.Repeat("A", 64) }, want: "application-model digest"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := validInput()
			test.mutate(&input)
			envelope, err := diagnosticjson.New(input)
			if !errors.Is(err, diagnosticjson.ErrInvalid) || !strings.Contains(err.Error(), test.want) || envelope.Valid() {
				t.Fatalf("New = %#v, %v; want ErrInvalid containing %q", envelope, err, test.want)
			}
		})
	}
}

func TestEnvelopeRejectsInvalidDiagnostics(t *testing.T) {
	t.Parallel()

	valid := diagnosticjson.Diagnostic{Code: "PLYSTRA-RESOLUTION-001", Severity: diagnosticjson.SeverityError, Message: "Select one Provider."}
	tests := []struct {
		name        string
		diagnostics []diagnosticjson.Diagnostic
		want        string
	}{
		{name: "missing code", diagnostics: []diagnosticjson.Diagnostic{{Severity: valid.Severity, Message: valid.Message}}, want: "code"},
		{name: "lowercase code", diagnostics: []diagnosticjson.Diagnostic{{Code: "plystra-resolution", Severity: valid.Severity, Message: valid.Message}}, want: "code"},
		{name: "empty code segment", diagnostics: []diagnosticjson.Diagnostic{{Code: "PLYSTRA--RESOLUTION", Severity: valid.Severity, Message: valid.Message}}, want: "code"},
		{name: "trailing separator", diagnostics: []diagnosticjson.Diagnostic{{Code: "PLYSTRA-RESOLUTION-", Severity: valid.Severity, Message: valid.Message}}, want: "code"},
		{name: "unsupported severity", diagnostics: []diagnosticjson.Diagnostic{{Code: valid.Code, Severity: "fatal", Message: valid.Message}}, want: "severity"},
		{name: "empty message", diagnostics: []diagnosticjson.Diagnostic{{Code: valid.Code, Severity: valid.Severity}}, want: "message"},
		{name: "long message", diagnostics: []diagnosticjson.Diagnostic{{Code: valid.Code, Severity: valid.Severity, Message: strings.Repeat("x", 4097)}}, want: "message"},
		{name: "NUL message", diagnostics: []diagnosticjson.Diagnostic{{Code: valid.Code, Severity: valid.Severity, Message: "unsafe\x00message"}}, want: "message"},
		{name: "invalid UTF-8", diagnostics: []diagnosticjson.Diagnostic{{Code: valid.Code, Severity: valid.Severity, Message: string([]byte{0xff})}}, want: "message"},
		{name: "duplicate", diagnostics: []diagnosticjson.Diagnostic{valid, valid}, want: "duplicates"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := validInput()
			input.Diagnostics = test.diagnostics
			envelope, err := diagnosticjson.New(input)
			if !errors.Is(err, diagnosticjson.ErrInvalid) || !strings.Contains(err.Error(), test.want) || envelope.Valid() {
				t.Fatalf("New = %#v, %v; want ErrInvalid containing %q", envelope, err, test.want)
			}
		})
	}
}

func TestEnvelopeRejectsUnsafeOrDuplicateSources(t *testing.T) {
	t.Parallel()

	valid := diagnosticjson.Source{Module: "example.com/app", Path: "plugins/mail/plugin.yaml", Kind: "plugin-declaration", Line: 1, Column: 1}
	tests := []struct {
		name    string
		sources []diagnosticjson.Source
		want    string
	}{
		{name: "missing module", sources: []diagnosticjson.Source{{Path: valid.Path, Kind: valid.Kind}}, want: "module"},
		{name: "invalid module", sources: []diagnosticjson.Source{{Module: "not a module", Path: valid.Path, Kind: valid.Kind}}, want: "module"},
		{name: "absolute slash path", sources: []diagnosticjson.Source{{Module: valid.Module, Path: "/private/plugin.yaml", Kind: valid.Kind}}, want: "module-relative"},
		{name: "absolute drive path", sources: []diagnosticjson.Source{{Module: valid.Module, Path: "C:/private/plugin.yaml", Kind: valid.Kind}}, want: "module-relative"},
		{name: "traversal", sources: []diagnosticjson.Source{{Module: valid.Module, Path: "../plugin.yaml", Kind: valid.Kind}}, want: "module-relative"},
		{name: "backslash", sources: []diagnosticjson.Source{{Module: valid.Module, Path: `plugins\mail\plugin.yaml`, Kind: valid.Kind}}, want: "module-relative"},
		{name: "invalid kind", sources: []diagnosticjson.Source{{Module: valid.Module, Path: valid.Path, Kind: "Plugin"}}, want: "kind"},
		{name: "negative line", sources: []diagnosticjson.Source{{Module: valid.Module, Path: valid.Path, Kind: valid.Kind, Line: -1}}, want: "line"},
		{name: "column without line", sources: []diagnosticjson.Source{{Module: valid.Module, Path: valid.Path, Kind: valid.Kind, Column: 1}}, want: "column"},
		{name: "duplicate", sources: []diagnosticjson.Source{valid, valid}, want: "duplicates"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := validInput()
			input.Sources = test.sources
			envelope, err := diagnosticjson.New(input)
			if !errors.Is(err, diagnosticjson.ErrInvalid) || !strings.Contains(err.Error(), test.want) || envelope.Valid() {
				t.Fatalf("New = %#v, %v; want ErrInvalid containing %q", envelope, err, test.want)
			}
		})
	}

	input := validInput()
	input.Sources = []diagnosticjson.Source{{Module: "my-app", Path: "plystra.yaml", Kind: "project-marker"}}
	if envelope, err := diagnosticjson.New(input); err != nil || !envelope.Valid() {
		t.Fatalf("source without exact line or column = %#v, %v", envelope, err)
	}
}

func TestEnvelopeStrictlyCanonicalizesAndBoundsResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result string
		want   string
	}{
		{name: "array root", result: `[]`, want: "must be a JSON object"},
		{name: "scalar root", result: `true`, want: "must be a JSON object"},
		{name: "duplicate key", result: `{"a":1,"a":2}`, want: "duplicate key"},
		{name: "trailing value", result: `{} {}`, want: "multiple JSON values"},
		{name: "integer overflow", result: `{"a":9223372036854775808}`, want: "signed 64-bit"},
		{name: "excessive depth", result: `{"a":` + strings.Repeat("[", 64) + `0` + strings.Repeat("]", 64) + `}`, want: "maximum depth"},
		{name: "oversized", result: `{"a":"` + strings.Repeat("x", 1<<20) + `"}`, want: "exceeds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := validInput()
			input.Result = json.RawMessage(test.result)
			envelope, err := diagnosticjson.New(input)
			if !errors.Is(err, diagnosticjson.ErrInvalid) || !strings.Contains(err.Error(), test.want) || envelope.Valid() {
				t.Fatalf("New = %#v, %v; want ErrInvalid containing %q", envelope, err, test.want)
			}
		})
	}

	input := validInput()
	input.Result = json.RawMessage([]byte{'{', '"', 'a', '"', ':', '"', 0xff, '"', '}'})
	if envelope, err := diagnosticjson.New(input); !errors.Is(err, diagnosticjson.ErrInvalid) || !strings.Contains(err.Error(), "UTF-8") || envelope.Valid() {
		t.Fatalf("invalid UTF-8 result = %#v, %v", envelope, err)
	}
}

func TestEnvelopeOwnsAllMutableInputAndOutputStorage(t *testing.T) {
	t.Parallel()

	diagnostic := diagnosticjson.Diagnostic{Code: "PLYSTRA-CHECK", Severity: diagnosticjson.SeverityInfo, Message: "Project is valid."}
	source := diagnosticjson.Source{Module: "example.com/app", Path: "plystra.yaml", Kind: "project-marker", Line: 1, Column: 1}
	input := validInput()
	input.Diagnostics = []diagnosticjson.Diagnostic{diagnostic}
	input.Sources = []diagnosticjson.Source{source}
	input.Result = json.RawMessage(`{"ok":true}`)
	envelope, err := diagnosticjson.New(input)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	canonical := envelope.CanonicalJSON()
	digest := envelope.Digest()

	input.Diagnostics[0].Message = "changed"
	input.Sources[0].Path = "changed"
	input.Result[0] = '!'
	diagnostics := envelope.Diagnostics()
	diagnostics[0].Message = "changed"
	sources := envelope.Sources()
	sources[0].Path = "changed"
	result := envelope.ResultJSON()
	result[0] = '!'
	copyOfCanonical := envelope.CanonicalJSON()
	copyOfCanonical[0] = '!'

	if !envelope.Valid() || envelope.Digest() != digest || !bytes.Equal(envelope.CanonicalJSON(), canonical) || envelope.Diagnostics()[0] != diagnostic || envelope.Sources()[0] != source || string(envelope.ResultJSON()) != `{"ok":true}` {
		t.Fatalf("mutable storage escaped envelope: %s", envelope.CanonicalJSON())
	}
	if (diagnosticjson.Envelope{}).Valid() {
		t.Fatal("zero Envelope is valid")
	}
}

func validInput() diagnosticjson.Input {
	return diagnosticjson.Input{
		Schema:                 "plystra.inspect",
		SchemaVersion:          1,
		ConfigurationMode:      generation.ConfigurationModeDefault,
		ApplicationModelDigest: testDigest("a"),
	}
}

func testDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
