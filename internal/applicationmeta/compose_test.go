package applicationmeta_test

import (
	"bytes"
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestComposeDerivesTransportFromEffectiveExposure(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		dependencies []applicationmeta.Dependency
		current      string
		want         applicationmeta.HTTPTransports
	}{
		{
			name:    "empty exposure selects no transport",
			current: "http: {}\n",
		},
		{
			name:    "Connect carries exposure",
			current: "http: {expose: {kernel.health/v1: {transport: connect}}}\n",
			want:    applicationmeta.HTTPTransports{Connect: true},
		},
		{
			name:    "removed exposure selects no transport",
			current: "http: {expose: {}}\n",
		},
		{
			name:    "current Project exposure enables its transport",
			want:    applicationmeta.HTTPTransports{Connect: true},
			current: "http: {expose: {kernel.info/v1: {transport: connect}}}\n",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			composition, err := applicationmeta.Compose(test.dependencies, composeManifest(t, test.current), composeSchemaLookup(nil))
			if err != nil || !composition.Valid() {
				t.Fatalf("Compose = %#v, %v", composition, err)
			}
			if got := composition.Manifest().HTTPTransports(); got != test.want {
				t.Fatalf("HTTPTransports = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestComposeRetainsExposureTransportAndSources(t *testing.T) {
	t.Parallel()

	base, err := applicationmeta.ParseSource("plystra.yaml", []byte("http: {expose: {kernel.health/v1: {transport: connect}}}\n"))
	if err != nil {
		t.Fatalf("ParseSource(base): %v", err)
	}
	base, err = applicationmeta.WithProjectModule(base, "example.com/application")
	if err != nil {
		t.Fatalf("WithProjectModule(base): %v", err)
	}
	overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte("http: {expose: {kernel.info/v1: {transport: connect}}}\n"))
	if err != nil {
		t.Fatalf("ParseOverlaySource: %v", err)
	}
	overlay, err = applicationmeta.WithProjectModule(overlay, "example.com/application")
	if err != nil {
		t.Fatalf("WithProjectModule(overlay): %v", err)
	}
	selected, err := applicationmeta.ApplyOverlay(base, overlay, composeSchemaLookup(nil))
	if err != nil {
		t.Fatalf("ApplyOverlay: %v", err)
	}

	composition, err := applicationmeta.Compose(nil, selected, composeSchemaLookup(nil))
	if !composition.Valid() || err != nil {
		t.Fatalf("Compose = %#v, %v", composition, err)
	}
	exposures := composition.Manifest().HTTPExposures()
	if len(exposures) != 2 || exposures[0].ID().String() != "kernel.health/v1" || exposures[1].ID().String() != "kernel.info/v1" {
		t.Fatalf("transport selection exposures = %#v", exposures)
	}
	for index, wantPath := range []string{"plystra.yaml", "plystra.production.yaml"} {
		if exposures[index].Transport() != applicationmeta.HTTPTransportConnect {
			t.Fatalf("exposure transport = %q", exposures[index].Transport())
		}
		source := exposures[index].DeclarationSource()
		if source.ModulePath() != "example.com/application" || source.Path() != wantPath || source.Line() != 1 || source.Column() != 1 {
			t.Fatalf("transport selection source %d = %#v", index, source)
		}
	}
	exposures[0] = applicationmeta.HTTPExposure{}
	if composition.Manifest().HTTPExposures()[0].ID().String() != "kernel.health/v1" {
		t.Fatal("Manifest exposed mutable exposure storage")
	}
}

func TestComposeRejectsNestedConfigurationTypeMismatchWithoutValues(t *testing.T) {
	t.Parallel()

	privateValue := "private-lower-value"
	privateCurrent := "private-current-value"
	schema := composeSchema(t, "\tSettings struct { Mode string }\n")
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
	dependency := applicationmeta.Dependency{
		ModulePath:    "example.com/lower",
		ModuleVersion: "v1.0.0",
		Manifest:      composeManifest(t, "config: {example.com/acme/smtp.New: {settings: {mode: "+privateValue+"}}}\n"),
	}
	current := composeManifest(t, "config: {example.com/acme/smtp.New: {settings: {mode: {name: "+privateCurrent+"}}}}\n")
	_, err := applicationmeta.Compose([]applicationmeta.Dependency{dependency}, current, lookup)
	if !errors.Is(err, applicationmeta.ErrConfigurationValues) || !errors.Is(err, applicationmeta.ErrConfigurationInvalidValue) || !strings.Contains(err.Error(), `config["example.com/acme/smtp.New"]["settings"]["mode"]`) {
		t.Fatalf("nested type mismatch = %v", err)
	}
	for _, forbidden := range []string{privateValue, privateCurrent} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("nested type mismatch exposed %q: %v", forbidden, err)
		}
	}
}

func TestComposeRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	schema := composeSchema(t, "\tHost string\n")
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
	tests := []struct {
		name         string
		dependencies []applicationmeta.Dependency
		current      applicationmeta.Manifest
		lookup       applicationmeta.SchemaLookup
		want         string
	}{
		{
			name:    "unknown configuration field",
			current: composeManifest(t, "config: {example.com/acme/smtp.New: {private_unknown: hidden}}\n"),
			lookup:  lookup,
			want:    "unknown constructor configuration field",
		},
		{
			name:    "unknown removed configuration field",
			current: composeManifest(t, "config: {example.com/acme/smtp.New: {private_unknown: hidden}}\n"),
			lookup:  lookup,
			want:    "unknown constructor configuration field",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := applicationmeta.Compose(test.dependencies, test.current, test.lookup)
			if !errors.Is(err, applicationmeta.ErrCompose) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compose error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), "private_unknown") || strings.Contains(err.Error(), "hidden") {
				t.Fatalf("Compose error exposed private configuration: %v", err)
			}
		})
	}
}

func FuzzComposeConstructorConfigurationDeterminism(f *testing.F) {
	schema := composeSchema(f, `
	Hosts []string
	Optional *struct { Region string; Zone string }
	Settings struct {
		Region string
		Zone string
		Legacy string
		Enabled bool
	}
	Token configuration.Secret
`)
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{"example.com/acme/smtp.New": schema})
	seeds := [][3]string{
		{
			"config: {example.com/acme/smtp.New: {optional: {region: one}}}\n",
			"config: {example.com/acme/smtp.New: {optional: {zone: two}}}\n",
			"config: {example.com/acme/smtp.New: {optional: {}}}\n",
		},
		{
			"config: {example.com/acme/smtp.New: {settings: {region: one}, hosts: [a]}}\n",
			"config: {example.com/acme/smtp.New: {settings: {zone: two}}}\n",
			"config: {example.com/acme/smtp.New: {settings: {region: current}}}\n",
		},
		{
			"config: {example.com/acme/smtp.New: {settings: {legacy: value}, token: {env: PRIVATE_TOKEN}}}\n",
			"config: {example.com/acme/smtp.New: {settings: {legacy: {$remove: true}}}}\n",
			"config: {example.com/acme/smtp.New: {settings: {legacy: {$remove: true}}, token: {$remove: true}}}\n",
		},
		{
			"config: {example.com/acme/smtp.New: {$remove: true}}\n",
			"config: {example.com/acme/smtp.New: {settings: {enabled: true}}}\n",
			"config: {example.com/acme/smtp.New: {}}\n",
		},
		{
			"config: {example.com/acme/smtp.New: {optional: null, hosts: null}}\n",
			"config: {example.com/acme/smtp.New: {optional: {}, hosts: []}}\n",
			"config: {example.com/acme/smtp.New: {optional: ~, hosts: {$remove: true}}}\n",
		},
		{
			"config: {example.com/acme/smtp.New: {settings: ,token: {}}}",
			"config: {c.b2c0: {2}}",
			"config: {}",
		},
	}
	for _, seed := range seeds {
		f.Add(seed[0], seed[1], seed[2])
	}
	f.Fuzz(func(t *testing.T, firstYAML, secondYAML, currentYAML string) {
		if len(firstYAML)+len(secondYAML)+len(currentYAML) > 3*applicationmeta.MaximumSize {
			return
		}
		firstManifest, firstParseErr := applicationmeta.Parse([]byte(firstYAML))
		secondManifest, secondParseErr := applicationmeta.Parse([]byte(secondYAML))
		current, currentParseErr := applicationmeta.Parse([]byte(currentYAML))
		if firstParseErr != nil || secondParseErr != nil || currentParseErr != nil {
			return
		}
		dependencies := []applicationmeta.Dependency{
			{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: firstManifest},
			{ModulePath: "example.com/b", ModuleVersion: "v1.0.0", Manifest: secondManifest},
		}
		first, firstErr := applicationmeta.Compose(dependencies, current, lookup)
		second, secondErr := applicationmeta.Compose(dependencies, current, lookup)
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("Compose changed success on repeat: %v then %v", firstErr, secondErr)
		}
		if firstErr != nil {
			if firstErr.Error() != secondErr.Error() {
				t.Fatalf("Compose changed error on repeat: %v then %v", firstErr, secondErr)
			}
			return
		}
		if first.CompositionDigest() != second.CompositionDigest() || !reflect.DeepEqual(provenanceStrings(first.Provenance()), provenanceStrings(second.Provenance())) {
			t.Fatalf("Compose changed provenance on repeat: %s %#v then %s %#v", first.CompositionDigest(), provenanceStrings(first.Provenance()), second.CompositionDigest(), provenanceStrings(second.Provenance()))
		}
		firstConfig, firstExists := first.Manifest().Configuration(mustConstructorSymbol(t, "example.com/acme/smtp.New"))
		secondConfig, secondExists := second.Manifest().Configuration(mustConstructorSymbol(t, "example.com/acme/smtp.New"))
		if firstExists != secondExists || firstExists && !bytes.Equal(firstConfig.YAML(), secondConfig.YAML()) {
			t.Fatalf("Compose changed redacted configuration on repeat: present %t then %t", firstExists, secondExists)
		}
	})
}

func composeManifest(t testing.TB, source string) applicationmeta.Manifest {
	t.Helper()
	manifest, err := applicationmeta.Parse([]byte(source))
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, source)
	}
	return manifest
}

func composeSchema(t testing.TB, fields string) implementationinventory.Configuration {
	t.Helper()
	source := `package fixture

import (
	"net/url"
	"time"
	"github.com/plystra/kernel/configuration"
)

var (
	_ time.Duration
	_ url.URL
	_ configuration.Secret
)

type Config struct {
` + fields + `
}

func New(Config) {}
`
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, "config.go", source, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse Config fixture: %v\n%s", err, source)
	}
	compiled, err := (&types.Config{Importer: composeConfigurationImporter{standard: importer.Default()}}).Check("example.com/acme/smtp", files, []*ast.File{parsed}, nil)
	if err != nil {
		t.Fatalf("compile Config fixture: %v\n%s", err, source)
	}
	constructor, _ := compiled.Scope().Lookup("New").(*types.Func)
	schema, exists, err := implementationinventory.CompileConfiguration(compiled, constructor)
	if err != nil || !exists {
		t.Fatalf("CompileConfiguration = %#v, %t, %v\n%s", schema, exists, err, source)
	}
	return schema
}

func composeSchemaLookup(schemas map[string]implementationinventory.Configuration) applicationmeta.SchemaLookup {
	return namespacedSchemaLookup(applicationmeta.ConfigurationNamespaceImplementation, schemas)
}

func namespacedSchemaLookup(namespace applicationmeta.ConfigurationNamespace, schemas map[string]implementationinventory.Configuration) applicationmeta.SchemaLookup {
	return func(requested applicationmeta.ConfigurationNamespace, constructor constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		if requested != namespace {
			return implementationinventory.Configuration{}, false
		}
		schema, exists := schemas[constructor.String()]
		return schema, exists
	}
}

type composeConfigurationImporter struct {
	standard types.Importer
}

func (i composeConfigurationImporter) Import(path string) (*types.Package, error) {
	if path != "github.com/plystra/kernel/configuration" {
		return i.standard.Import(path)
	}
	pkg := types.NewPackage(path, "configuration")
	name := types.NewTypeName(token.NoPos, pkg, "Secret", nil)
	types.NewNamed(name, types.NewStruct(nil, nil), nil)
	pkg.Scope().Insert(name)
	pkg.MarkComplete()
	return pkg, nil
}

func mustConstructorSymbol(t testing.TB, value string) constructorsymbol.Symbol {
	t.Helper()
	symbol, err := constructorsymbol.Parse(value)
	if err != nil {
		t.Fatalf("constructorsymbol.Parse(%q): %v", value, err)
	}
	return symbol
}

func findProvenance(t testing.TB, values []applicationmeta.Provenance, path string) []applicationmeta.Provenance {
	t.Helper()
	var result []applicationmeta.Provenance
	for _, value := range values {
		if value.Path() == path {
			result = append(result, value)
		}
	}
	return result
}

func provenanceStrings(values []applicationmeta.Provenance) []string {
	result := make([]string, len(values))
	for index := range values {
		kind := "value"
		if values[index].Removed() {
			kind = "removed"
		}
		result[index] = values[index].Path() + "=" + kind + ":" + values[index].Digest() + "@" + strings.Join(values[index].Sources(), ",")
	}
	return result
}
