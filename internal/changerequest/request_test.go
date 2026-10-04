package changerequest_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/changerequest"
)

func TestParseNormalizesAllInstalledOperationKinds(t *testing.T) {
	request, err := changerequest.Parse([]byte(`{
  "schema": "plystra.change/v1",
  "operations": [
    {"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/db.New"},
    {"kind":"configuration","action":"set","owner":"example.com/orders.New","path":"endpoint.url","value":{"b":2,"a":1.0}},
    {"kind":"dependency","action":"add","module":"github.com/acme/orders@v1.2.3"},
    {"kind":"interface_root","action":"present","interface":"order.cancel/v1","root":"expose"},
    {"kind":"implementation_selection","action":"set","interface":"order.cancel/v1","constructor":"example.com/orders.New"},
    {"kind":"implementation_create","contract":"order.cancel/v1","package":"./orders"},
    {"kind":"interface_create","name":"order.cancel","semantics":"command"}
  ]
}`))
	if err != nil {
		t.Fatal(err)
	}
	if !request.Valid() || request.Digest() == "" {
		t.Fatalf("request is not valid: %#v", request)
	}
	operations := request.Operations()
	if len(operations) != 7 {
		t.Fatalf("operation count = %d", len(operations))
	}
	seen := make(map[changerequest.Kind]bool)
	for _, operation := range operations {
		seen[operation.Kind()] = true
	}
	for _, kind := range []changerequest.Kind{
		changerequest.KindInterfaceCreate,
		changerequest.KindImplementationCreate,
		changerequest.KindDependency,
		changerequest.KindConfiguration,
		changerequest.KindInterfaceRoot,
		changerequest.KindImplementationSelection,
		changerequest.KindResourceSelection,
	} {
		if !seen[kind] {
			t.Fatalf("missing operation kind %q", kind)
		}
	}

	for _, operation := range operations {
		switch operation.Kind() {
		case changerequest.KindInterfaceCreate:
			value, ok := operation.InterfaceCreate()
			if !ok || value.Name() != "order.cancel/v1" || value.Semantics() != changerequest.SemanticsCommand {
				t.Fatalf("Interface create = %#v, ok %t", value, ok)
			}
		case changerequest.KindImplementationCreate:
			value, ok := operation.ImplementationCreate()
			if !ok || value.Contract() != "order.cancel/v1" || value.Package() != "./orders" {
				t.Fatalf("Implementation create = %#v, ok %t", value, ok)
			}
		case changerequest.KindDependency:
			value, ok := operation.Dependency()
			if !ok || value.Action() != changerequest.DependencyAdd || value.Module() != "github.com/acme/orders@v1.2.3" {
				t.Fatalf("dependency = %#v, ok %t", value, ok)
			}
		case changerequest.KindConfiguration:
			value, ok := operation.Configuration()
			canonical, hasValue := value.Value()
			if !ok || value.Action() != changerequest.ConfigurationSet || value.Owner() != "example.com/orders.New" || value.Path() != "endpoint.url" || !hasValue || string(canonical) != `{"a":1,"b":2}` {
				t.Fatalf("configuration = %#v, value %q, ok %t", value, canonical, ok)
			}
		case changerequest.KindInterfaceRoot:
			value, ok := operation.InterfaceRoot()
			if !ok || value.Action() != changerequest.RootPresent || value.Interface() != "order.cancel/v1" || value.Root() != changerequest.RootExpose {
				t.Fatalf("root = %#v, ok %t", value, ok)
			}
		case changerequest.KindImplementationSelection:
			value, ok := operation.ImplementationSelection()
			constructor, hasConstructor := value.Constructor()
			if !ok || value.Action() != changerequest.SelectionSet || value.Interface() != "order.cancel/v1" || !hasConstructor || constructor != "example.com/orders.New" {
				t.Fatalf("Implementation selection = %#v, constructor %q, ok %t", value, constructor, ok)
			}
		case changerequest.KindResourceSelection:
			value, ok := operation.ResourceSelection()
			provider, hasProvider := value.Provider()
			if !ok || value.Action() != changerequest.SelectionSet || value.Instance() != "database.primary" || !hasProvider || provider != "example.com/db.New" {
				t.Fatalf("Resource selection = %#v, provider %q, ok %t", value, provider, ok)
			}
		}
	}

	canonical := string(request.CanonicalJSON())
	if !strings.Contains(canonical, `"name":"order.cancel/v1"`) || !strings.Contains(canonical, `"value":{"a":1,"b":2}`) {
		t.Fatalf("canonical request is not normalized: %s", canonical)
	}
	copyOfJSON := request.CanonicalJSON()
	copyOfJSON[0] = 'X'
	if bytes.Equal(copyOfJSON, request.CanonicalJSON()) {
		t.Fatal("CanonicalJSON did not return a defensive copy")
	}
	for _, operation := range operations {
		if operation.Kind() == changerequest.KindResourceSelection {
			value, _ := operation.ResourceSelection()
			if _, ok := value.Provider(); !ok {
				t.Fatal("Resource selection lost provider")
			}
		}
	}
}

func TestParseIsPermutationStableAndDigestDeterministic(t *testing.T) {
	first, err := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_root","action":"present","interface":"order.create/v1","root":"require"},{"kind":"interface_create","name":"order.create/v1","semantics":"query"},{"kind":"configuration","action":"set","owner":"example.com/orders.New","path":"limit","value":{"z":0,"a":1e0}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := changerequest.Parse([]byte(`{"operations":[{"value":{"a":1.0,"z":0},"path":"limit","owner":"example.com/orders.New","action":"set","kind":"configuration"},{"semantics":"query","name":"order.create","kind":"interface_create"},{"root":"require","interface":"order.create/v1","action":"present","kind":"interface_root"}],"schema":"plystra.change/v1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
		t.Fatalf("permutation changed identity:\n%s\n%s\n%s\n%s", first.CanonicalJSON(), second.CanonicalJSON(), first.Digest(), second.Digest())
	}
	if first.Digest() != second.Digest() || !strings.HasPrefix(first.Digest(), "sha256:") || len(first.Digest()) != len("sha256:")+64 {
		t.Fatalf("digest = %q", first.Digest())
	}
}

func TestParsePreservesExactDecimalConfigurationValues(t *testing.T) {
	request, err := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[{"kind":"configuration","action":"set","owner":"example.com/orders.New","path":"amount","value":9007199254740993.123456789}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(request.CanonicalJSON()), `"value":9007199254740993.123456789`) {
		t.Fatalf("canonical JSON lost decimal precision: %s", request.CanonicalJSON())
	}
}

func TestParseUsesKnownAnswerCanonicalJSONAndDigest(t *testing.T) {
	request, err := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"order.create/v1","semantics":"query"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	wantCanonical := `{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"order.create/v1","semantics":"query"}]}`
	if string(request.CanonicalJSON()) != wantCanonical {
		t.Fatalf("canonical JSON = %s, want %s", request.CanonicalJSON(), wantCanonical)
	}
	if request.Digest() != "sha256:84447cc64fa6dc600e8398dc3949ca80c6a13b27dde36ba84c53d1d0207c33e6" {
		t.Fatalf("digest = %q", request.Digest())
	}
}

func TestParseRejectsClosedFieldAndOperationConflicts(t *testing.T) {
	tests := []string{
		`{"schema":"plystra.change/v1","operations":[],"extra":true}`,
		`{"schema":"plystra.change/v1","schema":"plystra.change/v1","operations":[]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"order.create","semantics":"query","extra":true}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"order.create","semantics":"query"},{"kind":"interface_create","name":"order.create/v1","semantics":"command"}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"dependency","action":"add","module":"github.com/acme/orders@v1.2.3"},{"kind":"dependency","action":"remove","module":"github.com/acme/orders"}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"configuration","action":"remove","owner":"example.com/orders.New","path":"endpoint","value":null}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"set","interface":"order.create/v1"}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"database.primary","provider":"example.com/db.New","provider":"example.com/db.New"}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"interface_root","action":"present","interface":"order.create","root":"require"}]}`,
	}
	for _, data := range tests {
		if _, err := changerequest.Parse([]byte(data)); !errors.Is(err, changerequest.ErrInvalid) {
			t.Errorf("Parse(%s) error = %v, want ErrInvalid", data, err)
		}
	}
}

func TestParseRejectsMalformedKindsAndUnsafeValues(t *testing.T) {
	tests := []string{
		`{"schema":"plystra.change/v2","operations":[]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"unknown"}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"order.create/v0","semantics":"query"}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"implementation_create","contract":"order.create/v1","package":"./../unsafe"}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"implementation_create","contract":"database.primary","package":"./database"}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"dependency","action":"add","module":"github.com/acme/orders@none"}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"implementation_selection","action":"set","interface":"order.create/v1","constructor":"example.com/orders.new"}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"set","instance":"Database.Primary","provider":"example.com/db.New"}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"configuration","action":"set","owner":"example.com/orders.New","path":"../secret","value":true}]}`,
		`{"schema":"plystra.change/v1","operations":[{"kind":"configuration","action":"set","owner":"example.com/orders.New","path":"value"}]}`,
	}
	for _, data := range tests {
		if _, err := changerequest.Parse([]byte(data)); !errors.Is(err, changerequest.ErrInvalid) {
			t.Errorf("Parse(%s) error = %v, want ErrInvalid", data, err)
		}
	}
}

func TestParseEnforcesBoundsAndSingleValue(t *testing.T) {
	if _, err := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[]}{}`)); !errors.Is(err, changerequest.ErrInvalid) {
		t.Fatalf("multiple values error = %v", err)
	}
	if _, err := changerequest.Parse([]byte(strings.Repeat("x", changerequest.MaximumRequestBytes+1))); !errors.Is(err, changerequest.ErrInvalid) {
		t.Fatalf("oversized request error = %v", err)
	}
	deep := `{"schema":"plystra.change/v1","operations":[{"kind":"configuration","action":"set","owner":"example.com/orders.New","path":"value","value":`
	deep += strings.Repeat("[", changerequest.MaximumJSONDepth+2) + "null" + strings.Repeat("]", changerequest.MaximumJSONDepth+2) + `}]}`
	if _, err := changerequest.Parse([]byte(deep)); !errors.Is(err, changerequest.ErrInvalid) {
		t.Fatalf("deep request error = %v", err)
	}
}

func FuzzParseNeverPanics(f *testing.F) {
	f.Add([]byte(`{"schema":"plystra.change/v1","operations":[]}`))
	f.Add([]byte(`{"schema":"plystra.change/v1","operations":[{"kind":"configuration","action":"set","owner":"example.com/orders.New","path":"value","value":{"a":1}}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		request, err := changerequest.Parse(data)
		if err != nil {
			return
		}
		again, err := changerequest.Parse(request.CanonicalJSON())
		if err != nil || !bytes.Equal(request.CanonicalJSON(), again.CanonicalJSON()) || request.Digest() != again.Digest() {
			t.Fatalf("canonical request did not round trip: %v", err)
		}
	})
}

func TestConfigurationValueBounds(t *testing.T) {
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{"string boundary", `"` + strings.Repeat("x", changerequest.MaximumStringBytes) + `"`, true},
		{"string overflow", `"` + strings.Repeat("x", changerequest.MaximumStringBytes+1) + `"`, false},
		{"key boundary", `{"` + strings.Repeat("x", changerequest.MaximumStringBytes) + `":null}`, true},
		{"key overflow", `{"` + strings.Repeat("x", changerequest.MaximumStringBytes+1) + `":null}`, false},
		{"dynamic keys", `{"":1," a ":2,"line\nbreak":3}`, true},
		{"aggregate expansion", `[1e600000,1e600000]`, false},
		{"positive exponent overflow", `1e9223372036854775807`, false},
		{"negative exponent overflow", `0.1e-9223372036854775808`, false},
		{"depth boundary", strings.Repeat("[", changerequest.MaximumJSONDepth-4) + "null" + strings.Repeat("]", changerequest.MaximumJSONDepth-4), true},
		{"depth overflow", strings.Repeat("[", changerequest.MaximumJSONDepth-3) + "null" + strings.Repeat("]", changerequest.MaximumJSONDepth-3), false},
		{"node boundary", "[" + strings.Repeat("0,", changerequest.MaximumJSONNodes-10) + "0]", true},
		{"node overflow", "[" + strings.Repeat("0,", changerequest.MaximumJSONNodes-9) + "0]", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := changerequest.Parse(configurationRequest(test.value))
			if test.valid && err != nil {
				t.Fatal(err)
			}
			if !test.valid && !errors.Is(err, changerequest.ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestConfigurationErrorsDoNotExposePrivateKeys(t *testing.T) {
	for _, value := range []string{
		`{"private-target":1,"private-target":2}`,
		`{"private-target":1e999999999999999999999}`,
		`{"private-target":?}`,
	} {
		_, err := changerequest.Parse(configurationRequest(value))
		if !errors.Is(err, changerequest.ErrInvalid) || strings.Contains(err.Error(), "private-target") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}

func TestOperationCountBound(t *testing.T) {
	operations := make([]string, changerequest.MaximumOperations+1)
	for index := range operations {
		operations[index] = fmt.Sprintf(`{"kind":"interface_root","action":"present","interface":"order.action%d/v1","root":"require"}`, index)
	}
	for _, count := range []int{changerequest.MaximumOperations, changerequest.MaximumOperations + 1} {
		data := []byte(`{"schema":"plystra.change/v1","operations":[` + strings.Join(operations[:count], ",") + `]}`)
		_, err := changerequest.Parse(data)
		if (err == nil) != (count == changerequest.MaximumOperations) {
			t.Fatalf("count %d error = %v", count, err)
		}
	}
}

func TestExactNumberNormalization(t *testing.T) {
	for input, want := range map[string]string{
		"-0.0": "0", "1e3": "1000", "1.2300e-2": "0.0123",
		"0.00100": "0.001", "-123.4500": "-123.45", "1200e-2": "12",
	} {
		request, err := changerequest.Parse(configurationRequest(input))
		if err != nil {
			t.Fatal(err)
		}
		configuration, _ := request.Operations()[0].Configuration()
		value, ok := configuration.Value()
		if !ok || string(value) != want {
			t.Fatalf("%s normalized to %s, want %s", input, value, want)
		}
	}
}

func configurationRequest(value string) []byte {
	return []byte(`{"schema":"plystra.change/v1","operations":[{"kind":"configuration","action":"set","owner":"example.com/orders.New","path":"value","value":` + value + `}]}`)
}

func TestPrivateValueAccessAndFormatting(t *testing.T) {
	request, err := changerequest.Parse(configurationRequest(`{"private-target":"private-value"}`))
	if err != nil {
		t.Fatal(err)
	}
	original := request.CanonicalJSON()
	operations := request.Operations()
	configuration, _ := operations[0].Configuration()
	value, _ := configuration.Value()
	value[0] = 'X'
	operations[0] = changerequest.Operation{}
	if !bytes.Equal(original, request.CanonicalJSON()) {
		t.Fatal("accessor modified request")
	}
	again, _ := request.Operations()[0].Configuration()
	intact, _ := again.Value()
	if intact[0] != '{' {
		t.Fatal("accessor modified configuration")
	}
	for _, test := range []struct {
		value any
		want  string
	}{
		{request, "<redacted-change-request>"},
		{request.Operations()[0], "<redacted-change-operation>"},
		{configuration, "<redacted-change-configuration>"},
	} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			if got := fmt.Sprintf(format, test.value); got != test.want {
				t.Fatalf("format %s = %s", format, got)
			}
		}
	}
}

func TestImplementationCreationTargetsPackages(t *testing.T) {
	for _, test := range []struct {
		secondContract, secondPackage string
		valid                         bool
	}{
		{"email.send/v1", "./httpmail", true},
		{"order.create/v1", "./smtp", false},
		{"email.send/v1", "./smtp", false},
	} {
		data := []byte(fmt.Sprintf(`{"schema":"plystra.change/v1","operations":[{"kind":"implementation_create","contract":"email.send/v1","package":"./smtp"},{"kind":"implementation_create","contract":%q,"package":%q}]}`, test.secondContract, test.secondPackage))
		_, err := changerequest.Parse(data)
		if (err == nil) != test.valid {
			t.Fatalf("contract %s package %s: %v", test.secondContract, test.secondPackage, err)
		}
	}
}

func TestPackagePathUsesGoImportRules(t *testing.T) {
	for _, value := range []string{"./bad name/smtp", "./bad:segment/smtp", "./con/smtp", "./a../smtp", "./smtp/../mail", "./generated/smtp"} {
		data := []byte(fmt.Sprintf(`{"schema":"plystra.change/v1","operations":[{"kind":"implementation_create","contract":"email.send/v1","package":%q}]}`, value))
		if _, err := changerequest.Parse(data); !errors.Is(err, changerequest.ErrInvalid) {
			t.Fatalf("path %s error = %v", value, err)
		}
	}
	for _, value := range []string{"./email/smtp", "./email.v1/smtp", "./email-v1/smtp"} {
		data := []byte(fmt.Sprintf(`{"schema":"plystra.change/v1","operations":[{"kind":"implementation_create","contract":"email.send/v1","package":%q}]}`, value))
		if _, err := changerequest.Parse(data); err != nil {
			t.Fatalf("path %s error = %v", value, err)
		}
	}
}

func TestConfigurationPathsUseCanonicalFields(t *testing.T) {
	for _, value := range []string{"retry-count", "RetryCount", "_retry", "retry_", "retry__count", "1retry", "nested.Retry", strings.Repeat("x", 129)} {
		data := []byte(fmt.Sprintf(`{"schema":"plystra.change/v1","operations":[{"kind":"configuration","action":"set","owner":"example.com/orders.New","path":%q,"value":3}]}`, value))
		if _, err := changerequest.Parse(data); !errors.Is(err, changerequest.ErrInvalid) {
			t.Fatalf("path %s error = %v", value, err)
		}
	}
	data := []byte(`{"schema":"plystra.change/v1","operations":[{"kind":"configuration","action":"set","owner":"example.com/orders.New","path":"nested.retry_count2","value":3}]}`)
	if _, err := changerequest.Parse(data); err != nil {
		t.Fatal(err)
	}
}

func TestRemovalUpdateAndResourceOperations(t *testing.T) {
	request, err := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[
{"kind":"dependency","action":"remove","module":"example.com/old"},
{"kind":"dependency","action":"update","module":"example.com/new@v2.0.0+incompatible"},
{"kind":"configuration","action":"remove","owner":"database.primary","path":"connection_limit"},
{"kind":"interface_root","action":"absent","interface":"order.create/v1","root":"require"},
{"kind":"implementation_selection","action":"remove","interface":"order.create/v1"},
{"kind":"resource_selection","action":"remove","instance":"database.primary"},
{"kind":"implementation_create","contract":"data.database/v1","package":"./postgres"}
]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range request.Operations() {
		switch operation.Kind() {
		case changerequest.KindConfiguration:
			value, _ := operation.Configuration()
			if _, present := value.Value(); present || value.Action() != changerequest.ConfigurationRemove {
				t.Fatal("removal retained value")
			}
		case changerequest.KindImplementationSelection:
			value, _ := operation.ImplementationSelection()
			if _, present := value.Constructor(); present || value.Action() != changerequest.SelectionRemove {
				t.Fatal("removal retained constructor")
			}
		case changerequest.KindResourceSelection:
			value, _ := operation.ResourceSelection()
			if _, present := value.Provider(); present || value.Action() != changerequest.SelectionRemove {
				t.Fatal("removal retained provider")
			}
		case changerequest.KindInterfaceRoot:
			value, _ := operation.InterfaceRoot()
			if value.Action() != changerequest.RootAbsent {
				t.Fatal("removal retained root")
			}
		}
	}
	canonical := request.CanonicalJSON()
	again, err := changerequest.Parse(canonical)
	if err != nil || !bytes.Equal(canonical, again.CanonicalJSON()) {
		t.Fatalf("round trip: %v", err)
	}
}

func TestEveryKindRejectsDuplicateTargetAndUnknownFields(t *testing.T) {
	operations := []string{
		`{"kind":"interface_create","name":"order.create","semantics":"query"}`,
		`{"kind":"implementation_create","contract":"order.create/v1","package":"./orders"}`,
		`{"kind":"dependency","action":"add","module":"example.com/orders"}`,
		`{"kind":"configuration","action":"set","owner":"example.com/orders.New","path":"amount","value":null}`,
		`{"kind":"interface_root","action":"present","interface":"order.create/v1","root":"require"}`,
		`{"kind":"implementation_selection","action":"remove","interface":"order.create/v1"}`,
		`{"kind":"resource_selection","action":"remove","instance":"database.primary"}`,
	}
	for _, operation := range operations {
		for _, list := range []string{operation + "," + operation, strings.TrimSuffix(operation, "}") + `,"private-extra":true}`} {
			_, err := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[` + list + `]}`))
			if !errors.Is(err, changerequest.ErrInvalid) || strings.Contains(err.Error(), "private-extra") {
				t.Fatalf("operation error = %v", err)
			}
		}
	}
}

func TestNormalizedInterfaceNameBound(t *testing.T) {
	for length := changerequest.MaximumStringBytes - 3; length <= changerequest.MaximumStringBytes; length++ {
		name := "a." + strings.Repeat("b", length-2)
		data := []byte(fmt.Sprintf(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":%q,"semantics":"query"}]}`, name))
		request, err := changerequest.Parse(data)
		if length+3 > changerequest.MaximumStringBytes {
			if !errors.Is(err, changerequest.ErrInvalid) {
				t.Fatalf("name length %d: %v", length, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := changerequest.Parse(request.CanonicalJSON()); err != nil {
			t.Fatal(err)
		}
	}
}
