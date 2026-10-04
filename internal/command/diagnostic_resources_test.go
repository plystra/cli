package command

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
)

func TestResourceDiagnosticsRetainSelectionBindingAndCycleSources(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":          "module example.com/app\n\ngo 1.26\n",
		"plystra.yaml":    "{}\n",
		"api/resource.go": "package api\n//plystra:resource data.database/v1\ntype Resource interface{Read()}\n",
		"provider/new.go": "package provider\ntype Config struct{ Limit int };type Value struct{}\nfunc (*Value) Read(){}\n//plystra:implements-resource data.database/v1\nfunc New(cfg Config)(*Value,error){panic(\"PRIVATE_CONSTRUCTOR\")}\n",
		"wrapper/new.go":  "package wrapper\nimport api \"example.com/app/api\"\ntype Value struct{}\nfunc (*Value) Read(){}\n//plystra:implements-resource data.database/v1\nfunc New(upstream api.Resource)(*Value,error){panic(\"PRIVATE_CONSTRUCTOR\")}\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	project, err := projectlocate.Find(root)
	if err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	modules, err := moduledependency.Discover(t.Context(), project, moduledependency.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := interfaceinventory.DiscoverApplication(t.Context(), project, modules, interfaceinventory.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := constructorsymbol.Parse("example.com/app/provider.New")
	wrapper, _ := constructorsymbol.Parse("example.com/app/wrapper.New")
	unknown, _ := constructorsymbol.Parse("example.com/app/unknown.New")
	selection := []constructorgraph.ResourceSource{{Reference: "PRIVATE_REFERENCE", ModulePath: "example.com/app", Path: "plystra.production.yaml", Line: 3, Column: 5}}
	bindings := []constructorgraph.ResourceSource{{Reference: "PRIVATE_REFERENCE", ModulePath: "example.com/app", Path: "plystra.production.yaml", Line: 8, Column: 7}}
	instance := func(name string, provider constructorsymbol.Symbol) constructorgraph.ResourceInstanceInput {
		return constructorgraph.ResourceInstanceInput{Name: name, Provider: provider, Sources: selection}
	}
	binding := func(consumer, parameter, target string) constructorgraph.ResourceBindingInput {
		return constructorgraph.ResourceBindingInput{Namespace: constructorgraph.ResourceConsumerInstance, Consumer: consumer, Parameter: parameter, Target: target, Sources: bindings}
	}
	for _, test := range []struct {
		name, code string
		instances  []constructorgraph.ResourceInstanceInput
		bindings   []constructorgraph.ResourceBindingInput
		want       []diagnosticjson.Source
	}{
		{name: "unknown-provider", code: diagnosticcode.ResourceInstanceInvalid, instances: []constructorgraph.ResourceInstanceInput{instance("database", unknown)}, want: []diagnosticjson.Source{{Module: "example.com/app", Path: "plystra.production.yaml", Kind: "resource-selection", Line: 3, Column: 5}}},
		{name: "missing-target", code: diagnosticcode.ResourceBindingInvalid, instances: []constructorgraph.ResourceInstanceInput{instance("view", wrapper)}, bindings: []constructorgraph.ResourceBindingInput{binding("view", "upstream", "missing")}, want: []diagnosticjson.Source{{Module: "example.com/app", Path: "plystra.production.yaml", Kind: "resource-binding", Line: 8, Column: 7}}},
		{name: "unknown-parameter", code: diagnosticcode.ResourceBindingInvalid, instances: []constructorgraph.ResourceInstanceInput{instance("database", provider)}, bindings: []constructorgraph.ResourceBindingInput{binding("database", "missing", "database")}, want: []diagnosticjson.Source{{Module: "example.com/app", Path: "plystra.production.yaml", Kind: "resource-binding", Line: 8, Column: 7}}},
		{name: "ambiguous", code: diagnosticcode.ResourceBindingAmbiguous, instances: []constructorgraph.ResourceInstanceInput{instance("database", provider), instance("view", wrapper)}, want: []diagnosticjson.Source{{Module: "example.com/app", Path: "provider/new.go", Kind: "resource-provider-constructor", Line: 5, Column: 6}, {Module: "example.com/app", Path: "wrapper/new.go", Kind: "resource-provider-constructor", Line: 6, Column: 6}, {Module: "example.com/app", Path: "plystra.production.yaml", Kind: "resource-selection", Line: 3, Column: 5}}},
		{name: "cycle", code: diagnosticcode.ResolveConstructorCycle, instances: []constructorgraph.ResourceInstanceInput{instance("first", wrapper), instance("second", wrapper)}, bindings: []constructorgraph.ResourceBindingInput{binding("first", "upstream", "second"), binding("second", "upstream", "first")}, want: []diagnosticjson.Source{{Module: "example.com/app", Path: "wrapper/new.go", Kind: "resource-provider-constructor", Line: 6, Column: 6}, {Module: "example.com/app", Path: "plystra.production.yaml", Kind: "resource-selection", Line: 3, Column: 5}, {Module: "example.com/app", Path: "plystra.production.yaml", Kind: "resource-binding", Line: 8, Column: 7}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := constructorgraph.Build(constructorgraph.Input{ResourceProviders: discovery.ResourceProviders(), ResourceInstances: test.instances, ResourceBindings: test.bindings})
			if err == nil {
				t.Fatal("expected resolution failure")
			}
			diagnostic, ok := primaryActionableDiagnostic(err, recoveryContext{environmentName: "production"})
			if !ok || diagnostic.code != test.code || diagnostic.recovery == "" {
				t.Fatalf("diagnostic: %#v %v", diagnostic, err)
			}
			sources := actionableDiagnosticSources(err, diagnostic.code)
			for _, want := range test.want {
				if !slices.Contains(sources, want) {
					t.Fatalf("missing source %#v in %#v", want, sources)
				}
			}
			if _, err := diagnosticjson.CanonicalizeSources(sources); err != nil {
				t.Fatalf("noncanonical sources: %v", err)
			}
			if strings.Contains(diagnostic.recovery, "PRIVATE_") || strings.Contains(diagnostic.recovery, root) {
				t.Fatal("private data in recovery")
			}
		})
	}
	if code := resourceResolutionDiagnosticCode(errors.New("unrelated")); code != "" {
		t.Fatalf("guessed code %s", code)
	}
	for _, test := range []struct{ provider, code string }{{provider.String(), diagnosticcode.ResourceConfigurationValuesInvalid}, {wrapper.String(), diagnosticcode.ResourceConfigurationSchemaInvalid}} {
		t.Run(test.code, func(t *testing.T) {
			manifest, err := applicationmeta.ParseSource("plystra.production.yaml", []byte("resources:\n  instances:\n    database:\n      use: "+test.provider+"\n      config: {limit: PRIVATE_VALUE}\n"))
			if err != nil {
				t.Fatal(err)
			}
			manifest, err = applicationmeta.WithProjectModule(manifest, "example.com/app")
			if err != nil {
				t.Fatal(err)
			}
			_, err = applicationmeta.Compose(nil, manifest, func(symbol constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
				p, ok := discovery.ResourceProviders().BySymbol(symbol)
				if !ok {
					return implementationinventory.Configuration{}, false
				}
				return p.Configuration()
			})
			if err == nil {
				t.Fatal("expected invalid Resource configuration")
			}
			diagnostic, ok := primaryActionableDiagnostic(err, recoveryContext{environmentName: "production"})
			if !ok || diagnostic.code != test.code || strings.Contains(err.Error()+diagnostic.recovery, "PRIVATE_VALUE") {
				t.Fatalf("configuration diagnostic %#v: %v", diagnostic, err)
			}
			sources := actionableDiagnosticSources(err, test.code)
			if len(sources) != 1 || sources[0].Module != "example.com/app" || sources[0].Path != "plystra.production.yaml" || sources[0].Line != 5 || sources[0].Column != 7 {
				t.Fatalf("configuration sources: %#v", sources)
			}
		})
	}
}

func TestResourceMetadataDiagnosticsUseExactSafeCoordinates(t *testing.T) {
	t.Parallel()
	_, err := applicationmeta.ParseSource("plystra.production.yaml", []byte("resources:\n  instances:\n    PRIVATE_INVALID_NAME: {use: PRIVATE_PROVIDER}\n"))
	var metadata *applicationmeta.ResourceMetadataError
	if !errors.As(err, &metadata) {
		t.Fatalf("metadata error missing: %v", err)
	}
	wrapped := locatedResourceMetadataError{error: err}
	diagnostic, ok := primaryActionableDiagnostic(wrapped, recoveryContext{environmentName: "production"})
	if !ok || diagnostic.code != diagnosticcode.ResourceMetadataInvalid || strings.Contains(err.Error()+diagnostic.recovery, "PRIVATE_") {
		t.Fatalf("metadata diagnostic: %#v %v", diagnostic, err)
	}
	sources := actionableDiagnosticSources(wrapped, diagnostic.code)
	if len(sources) != 1 || sources[0].Path != "plystra.production.yaml" || sources[0].Module != "example.com/app" || sources[0].Kind != "configuration-declaration" || sources[0].Line != 3 {
		t.Fatalf("metadata sources: %#v", sources)
	}
}

type locatedResourceMetadataError struct{ error }

func (e locatedResourceMetadataError) Unwrap() error    { return e.error }
func (locatedResourceMetadataError) ModulePath() string { return "example.com/app" }
func (locatedResourceMetadataError) SourcePath() string { return "plystra.production.yaml" }
func (locatedResourceMetadataError) SourceKind() string { return "configuration-declaration" }
func (locatedResourceMetadataError) Line() int          { return 1 }
func (locatedResourceMetadataError) Column() int        { return 1 }
