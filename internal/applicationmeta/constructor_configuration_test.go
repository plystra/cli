package applicationmeta_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
)

const constructorConfigurationSymbol = "example.com/acme/smtp.New"

func TestComposeRetainsEveryDependencyConstructorConfigurationSource(t *testing.T) {
	t.Parallel()

	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
		constructorConfigurationSymbol: composeSchema(t, "\tEndpoint string\n"),
	})
	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/zeta", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "config: {"+constructorConfigurationSymbol+": {endpoint: shared.internal}}\n")},
		{ModulePath: "example.com/alpha", ModuleVersion: "v1.0.0", Manifest: composeManifest(t, "config: {"+constructorConfigurationSymbol+": {endpoint: shared.internal}}\n")},
	}
	for _, ordered := range [][]applicationmeta.Dependency{dependencies, {dependencies[1], dependencies[0]}} {
		composition, err := applicationmeta.Compose(ordered, composeManifest(t, "{}\n"), lookup)
		if err != nil {
			t.Fatalf("Compose: %v", err)
		}
		configured, exists := composition.Manifest().Configuration(mustConstructorSymbol(t, constructorConfigurationSymbol))
		if !exists {
			t.Fatal("composed dependency configuration is absent")
		}
		source := configured.DeclarationSource()
		if source.ModulePath() != "example.com/alpha" || source.Path() != "plystra.yaml" || source.Line() != 1 || source.Column() != 1 {
			t.Fatalf("representative dependency configuration source = %#v", source)
		}
		path := fmt.Sprintf("config[%q]", constructorConfigurationSymbol)
		records := findProvenance(t, composition.ResolutionSources(), path)
		wantSources := []string{
			`example.com/alpha@v1.0.0/plystra.yaml config["example.com/acme/smtp.New"]`,
			`example.com/zeta@v1.0.0/plystra.yaml config["example.com/acme/smtp.New"]`,
		}
		if len(records) != 1 || !reflect.DeepEqual(records[0].Sources(), wantSources) {
			t.Fatalf("dependency configuration resolution sources = %#v, want %#v", provenanceStrings(records), wantSources)
		}
	}
}

func TestComposeNormalizesEveryCompiledConstructorConfigurationValue(t *testing.T) {
	t.Parallel()

	schema := composeSchema(t, `
	Text string
	Enabled bool
	Signed8 int8
	Signed16 int16
	Signed32 int32
	Signed64 int64
	PortableInt int
	Unsigned8 uint8
	Unsigned16 uint16
	Unsigned32 uint32
	Unsigned64 uint64
	PortableUint uint
	Float32 float32
	Float64 float64
	Delay time.Duration
	Endpoint url.URL
	Password configuration.Secret
	Optional *string
	Targets []string
	Window [2]uint16
	Labels map[string]int16
	Settings struct {
		Mode string
		Nested struct {
			Enabled bool
		}
	}
`)
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{constructorConfigurationSymbol: schema})
	first := composeManifest(t, `
config:
  example.com/acme/smtp.New:
    unsigned64: 18446744073709551615
    signed64: -9223372036854775808
    text: exact text
    endpoint: https://example.test/path?q=value
    enabled: true
    delay: 60s
    float64: 1
    float32: 1.23456789
    unsigned8: 255
    unsigned16: 65535
    unsigned32: 4294967295
    portableuint: 4294967295
    signed8: -128
    signed16: -32768
    signed32: -2147483648
    portableint: 2147483647
    password: {env: PRIVATE_SMTP_PASSWORD}
    optional: present
    targets: [second, first]
    window: [0, 65535]
    labels: {z: -32768, a: 32767}
    settings:
      nested: {enabled: false}
      mode: strict
`)
	second := composeManifest(t, `
config:
  example.com/acme/smtp.New:
    settings: {mode: strict, nested: {enabled: false}}
    labels: {a: 32767, z: -32768}
    window: [0, 65535]
    targets: [second, first]
    optional: present
    password: {env: PRIVATE_SMTP_PASSWORD}
    portableint: 2147483647
    signed32: -2147483648
    signed16: -32768
    signed8: -128
    portableuint: 4294967295
    unsigned32: 4294967295
    unsigned16: 65535
    unsigned8: 255
    float32: 1.23456789
    float64: 1.0
    delay: 1m0s
    enabled: true
    endpoint: https://example.test/path?q=value
    text: exact text
    signed64: -9223372036854775808
    unsigned64: 18446744073709551615
`)

	firstComposition, err := applicationmeta.Compose(nil, first, lookup)
	if err != nil {
		t.Fatalf("Compose first: %v", err)
	}
	secondComposition, err := applicationmeta.Compose(nil, second, lookup)
	if err != nil {
		t.Fatalf("Compose second: %v", err)
	}
	firstConfiguration, exists := firstComposition.Manifest().Configuration(mustConstructorSymbol(t, constructorConfigurationSymbol))
	if !exists {
		t.Fatal("normalized constructor configuration is absent")
	}
	secondConfiguration, exists := secondComposition.Manifest().Configuration(mustConstructorSymbol(t, constructorConfigurationSymbol))
	if !exists || !bytes.Equal(firstConfiguration.YAML(), secondConfiguration.YAML()) {
		t.Fatalf("equivalent typed values normalized differently:\nfirst:\n%s\nsecond:\n%s", firstConfiguration.YAML(), secondConfiguration.YAML())
	}

	for _, expected := range []string{
		"delay: 1m0s",
		"float32: 1.2345679",
		"float64: 1",
		"labels:\n  a: 32767\n  z: -32768",
		"password:\n  env: PRIVATE_SMTP_PASSWORD",
		"signed64: -9223372036854775808",
		"unsigned64: 18446744073709551615",
		"window:\n  - 0\n  - 65535",
	} {
		if !bytes.Contains(firstConfiguration.YAML(), []byte(expected)) {
			t.Fatalf("normalized configuration omits %q:\n%s", expected, firstConfiguration.YAML())
		}
	}
	firstDecisions := constructorConfigurationDecisionDigests(t, first, lookup)
	secondDecisions := constructorConfigurationDecisionDigests(t, second, lookup)
	if strings.Join(firstDecisions, "\n") != strings.Join(secondDecisions, "\n") {
		t.Fatalf("equivalent typed values produced different decisions:\nfirst: %v\nsecond: %v", firstDecisions, secondDecisions)
	}

	changedData := strings.Replace(string(firstConfiguration.YAML()), "float64: 1", "float64: 2", 1)
	changed := composeManifest(t, "config:\n  "+constructorConfigurationSymbol+":\n"+indentConfigurationYAML([]byte(changedData), 4))
	if strings.Join(firstDecisions, "\n") == strings.Join(constructorConfigurationDecisionDigests(t, changed, lookup), "\n") {
		t.Fatal("different normalized values produced identical configuration decisions")
	}
}

func TestComposeRejectsInvalidCompiledConstructorConfigurationValuesWithoutDisclosure(t *testing.T) {
	t.Parallel()

	schema := composeSchema(t, `
	Text string
	Enabled bool
	Signed8 int8
	PortableInt int
	Unsigned8 uint8
	PortableUint uint
	Float32 float32
	Delay time.Duration
	Endpoint url.URL
	Password configuration.Secret
	Targets []string
	Window [2]uint16
	Labels map[string]int16
	Settings struct { Mode string }
`)
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{constructorConfigurationSymbol: schema})
	tests := []struct {
		name      string
		field     string
		value     string
		reason    error
		forbidden []string
	}{
		{name: "string type", field: "text", value: "true", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"true"}},
		{name: "boolean type", field: "enabled", value: "private-boolean", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"private-boolean"}},
		{name: "signed minimum", field: "signed8", value: "-129", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"-129"}},
		{name: "signed maximum", field: "signed8", value: "128", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"128"}},
		{name: "portable signed width", field: "portableint", value: "2147483648", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"2147483648"}},
		{name: "negative unsigned", field: "unsigned8", value: "-1", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"-1"}},
		{name: "unsigned maximum", field: "unsigned8", value: "256", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"256"}},
		{name: "portable unsigned width", field: "portableuint", value: "4294967296", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"4294967296"}},
		{name: "non-finite number", field: "float32", value: ".nan", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{".nan"}},
		{name: "overflowing number", field: "float32", value: "3.5e38", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"3.5e38"}},
		{name: "duration", field: "delay", value: "private-duration", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"private-duration"}},
		{name: "URL", field: "endpoint", value: "https://private.example/%zz", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"private.example", "%zz"}},
		{name: "list", field: "targets", value: "private-target", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"private-target"}},
		{name: "array length", field: "window", value: "[1]", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"[1]"}},
		{name: "map", field: "labels", value: "[private-label]", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"private-label"}},
		{name: "unknown nested field", field: "settings", value: "{private_unknown: private-value}", reason: applicationmeta.ErrConfigurationUnknownField, forbidden: []string{"private_unknown", "private-value"}},
		{name: "unknown Secret resolver", field: "password", value: "{vault: PRIVATE_SECRET_TARGET}", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"vault", "PRIVATE_SECRET_TARGET"}},
		{name: "invalid environment reference", field: "password", value: `{env: "PRIVATE SECRET TARGET"}`, reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"PRIVATE SECRET TARGET"}},
		{name: "invalid file reference", field: "password", value: "{file: private/secret.txt}", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"private/secret.txt"}},
		{name: "ambiguous Secret reference", field: "password", value: "{env: PRIVATE_SECRET_TARGET, file: /private/secret}", reason: applicationmeta.ErrConfigurationInvalidValue, forbidden: []string{"PRIVATE_SECRET_TARGET", "/private/secret"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			manifest, err := applicationmeta.ParseSource("deploy/customer.yaml", []byte(fmt.Sprintf("config:\n  %s:\n    %s: %s\n", constructorConfigurationSymbol, test.field, test.value)))
			if err != nil {
				t.Fatalf("ParseSource: %v", err)
			}
			manifest, err = applicationmeta.WithProjectModule(manifest, "example.com/application")
			if err != nil {
				t.Fatalf("WithProjectModule: %v", err)
			}
			_, err = applicationmeta.Compose(nil, manifest, lookup)
			var valueError *applicationmeta.ConstructorConfigurationValueError
			wantField := `config["` + constructorConfigurationSymbol + `"]["` + test.field + `"]`
			if !errors.As(err, &valueError) || valueError == nil || !errors.Is(err, applicationmeta.ErrConfigurationValues) || !errors.Is(err, test.reason) || valueError.Constructor().String() != constructorConfigurationSymbol || valueError.Field() != wantField || valueError.ModulePath() != "example.com/application" || valueError.SourcePath() != "deploy/customer.yaml" || valueError.SourceKind() != "configuration-declaration" || valueError.Line() != 1 || valueError.Column() != 1 || !strings.Contains(err.Error(), wantField) {
				t.Fatalf("Compose error = %v", err)
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("Compose error exposed %q: %v", forbidden, err)
				}
			}
		})
	}
}

func TestComposeRejectsUnavailableConstructorConfigurationSchemaWithoutDisclosure(t *testing.T) {
	t.Parallel()

	const (
		constructor  = "example.com/acme/missing.New"
		privateValue = "private-configuration-value"
	)
	assertError := func(t *testing.T, err error, modulePath, sourcePath string) {
		t.Helper()
		var schemaError *applicationmeta.ConstructorConfigurationSchemaError
		if !errors.As(err, &schemaError) || schemaError == nil || !errors.Is(err, applicationmeta.ErrConfigurationSchema) {
			t.Fatalf("Compose missing schema error = %v", err)
		}
		if schemaError.Constructor().String() != constructor || schemaError.ModulePath() != modulePath || schemaError.SourcePath() != sourcePath || schemaError.SourceKind() != "configuration-declaration" || schemaError.Line() != 1 || schemaError.Column() != 1 {
			t.Fatalf("constructor configuration schema error = %#v", schemaError)
		}
		if !strings.Contains(err.Error(), constructor) || strings.Contains(err.Error(), privateValue) {
			t.Fatalf("Compose missing schema error disclosed a value or omitted its constructor: %v", err)
		}
	}

	t.Run("current replacement value", func(t *testing.T) {
		manifest, err := applicationmeta.ParseSource("deploy/customer.yaml", []byte("config: {"+constructor+": {field: "+privateValue+"}}\n"))
		if err != nil {
			t.Fatalf("ParseSource: %v", err)
		}
		manifest, err = applicationmeta.WithProjectModule(manifest, "example.com/application")
		if err != nil {
			t.Fatalf("WithProjectModule: %v", err)
		}
		_, err = applicationmeta.Compose(nil, manifest, composeSchemaLookup(nil))
		assertError(t, err, "example.com/application", "deploy/customer.yaml")
	})

	t.Run("current environment removal", func(t *testing.T) {
		manifest, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte("config: {"+constructor+": null}\n"))
		if err != nil {
			t.Fatalf("ParseOverlaySource: %v", err)
		}
		manifest, err = applicationmeta.WithProjectModule(manifest, "example.com/application")
		if err != nil {
			t.Fatalf("WithProjectModule: %v", err)
		}
		_, err = applicationmeta.Compose(nil, manifest, composeSchemaLookup(nil))
		assertError(t, err, "example.com/application", "plystra.production.yaml")
	})

	t.Run("dependency value", func(t *testing.T) {
		dependencyManifest := composeManifest(t, "config: {"+constructor+": {field: "+privateValue+"}}\n")
		configured, exists := dependencyManifest.Configuration(mustConstructorSymbol(t, constructor))
		if !exists || configured.DeclarationSource().ModulePath() != "" {
			t.Fatalf("unexpected dependency fixture source = %#v, %t", configured.DeclarationSource(), exists)
		}
		_, err := applicationmeta.Compose([]applicationmeta.Dependency{{
			ModulePath:    "example.com/dependency",
			ModuleVersion: "v1.0.0",
			Manifest:      dependencyManifest,
		}}, composeManifest(t, "{}\n"), composeSchemaLookup(nil))
		assertError(t, err, "example.com/dependency", "plystra.yaml")
		configured, exists = dependencyManifest.Configuration(mustConstructorSymbol(t, constructor))
		if !exists || configured.DeclarationSource().ModulePath() != "" {
			t.Fatal("Compose mutated dependency manifest provenance")
		}
	})
}

func constructorConfigurationDecisionDigests(t testing.TB, manifest applicationmeta.Manifest, lookup applicationmeta.SchemaLookup) []string {
	t.Helper()
	decisions, err := applicationmeta.ConfigurationDecisions(manifest, lookup)
	if err != nil {
		t.Fatalf("ConfigurationDecisions: %v", err)
	}
	result := make([]string, len(decisions))
	for index, decision := range decisions {
		result[index] = decision.Path() + "\x00" + decision.Digest() + "\x00" + string(decision.Summary())
	}
	return result
}

func indentConfigurationYAML(data []byte, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	for index := range lines {
		lines[index] = prefix + lines[index]
	}
	return strings.Join(lines, "\n") + "\n"
}
