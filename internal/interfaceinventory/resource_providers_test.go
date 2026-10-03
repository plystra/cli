package interfaceinventory_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/resourceproviderinventory"
)

func TestResourceProviderDiscoverySharesProjectBoundaryAndGoIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	app := filepath.Join(root, "app")
	writeProject(t, app, "example.com/app")
	writeProject(t, filepath.Join(root, "contract"), "example.com/contract")
	writeProject(t, filepath.Join(root, "provider"), "example.com/provider")
	writeFile(t, filepath.Join(app, "go.mod"), "module example.com/app\ngo 1.26\nrequire (\nexample.com/contract v1.0.0\nexample.com/provider v1.1.0\nexample.com/value v1.1.0\nexample.com/transitive v1.2.0\n)\nreplace example.com/contract => ../contract\nreplace example.com/provider => ../provider\nreplace example.com/value => ../value\nreplace example.com/transitive => ../transitive\n")
	writeFile(t, filepath.Join(root, "provider", "go.mod"), "module example.com/provider\ngo 1.26\nrequire (\nexample.com/value v1.0.0\nexample.com/transitive v1.2.0\n)\nreplace example.com/value => ../wrong\n")
	writeProject(t, filepath.Join(root, "transitive"), "example.com/transitive")
	writeFile(t, filepath.Join(root, "value", "go.mod"), "module example.com/value\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "value", "value.go"), "package value\n//plystra:implements-resource invalid\ntype Value struct{Data string}\n")
	writeFile(t, filepath.Join(root, "wrong", "go.mod"), "module example.com/value\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "wrong", "value.go"), "package value\ntype Wrong struct{}\n")
	writeFile(t, filepath.Join(root, "contract", "go.mod"), "module example.com/contract\ngo 1.26\nrequire example.com/value v1.0.0\n")
	writeFile(t, filepath.Join(root, "contract", "contract.go"), "package contract\nimport \"example.com/value\"\n//plystra:resource data.database/v1\ntype Resource interface{Read() value.Value}\n")
	source := "package provider\nimport \"example.com/value\"\ntype database struct{}\nfunc (*database) Read() value.Value{return value.Value{}}\n//plystra:implements-resource data.database/v1\nfunc New()(*database,error){panic(\"constructor-entry\")}\n"
	writeFile(t, filepath.Join(root, "provider", "provider.go"), source)
	writeFile(t, filepath.Join(app, "provider", "provider.go"), source)
	writeFile(t, filepath.Join(root, "transitive", "go.mod"), "module example.com/transitive\ngo 1.26\nrequire example.com/value v1.0.0\n")
	writeFile(t, filepath.Join(root, "transitive", "provider.go"), source)
	for _, directory := range []string{"generated", "testdata", "vendor", "fixture", "fixtures", ".private", "_private"} {
		writeFile(t, filepath.Join(app, directory, "provider.go"), strings.Replace(source, "data.database/v1", "invalid", 1))
	}
	writeFile(t, filepath.Join(app, "inactive", "provider.go"), "//go:build provider_inactive\n\n"+strings.Replace(source, "data.database/v1", "invalid", 1))
	writeFile(t, filepath.Join(app, "tests", "provider_test.go"), strings.Replace(source, "data.database/v1", "invalid", 1))
	writeFile(t, filepath.Join(app, "literal", "literal.go"), "package literal\nconst marker = `//plystra:implements-resource invalid`\n")
	writeProject(t, filepath.Join(app, "nested"), "example.com/nested")
	writeFile(t, filepath.Join(app, "nested", "provider.go"), strings.Replace(source, "data.database/v1", "invalid", 1))
	before := snapshotFiles(t, root)
	environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"})
	discovery := discoverApplication(t, app, environment)
	providers := discovery.ResourceProviders().Providers()
	if len(providers) != 3 || len(discovery.Implementations().Implementations()) != 0 {
		t.Fatalf("providers = %#v", providers)
	}
	for n, symbol := range []string{"example.com/app/provider.New", "example.com/provider.New", "example.com/transitive.New"} {
		if providers[n].Symbol().String() != symbol || providers[n].ID() != "data.database/v1" || providers[n].Local() != (n == 0) {
			t.Fatalf("provider = %#v", providers[n])
		}
	}
	if providers[1].ModuleVersion() != "v1.1.0" || providers[2].ModuleVersion() != "v1.2.0" {
		t.Fatal("lost dependency version")
	}
	second := discoverApplication(t, app, environment).ResourceProviders().Providers()
	for n := range providers {
		if providers[n].Source() != second[n].Source() || providers[n].Symbol() != second[n].Symbol() || providers[n].ConcreteType().String() != second[n].ConcreteType().String() {
			t.Fatal("unstable provider inventory")
		}
	}
	if !reflect.DeepEqual(before, snapshotFiles(t, root)) {
		t.Fatal("provider discovery mutated authored files")
	}
}

func TestResourceProviderDiscoveryRemovesTemporaryModuleFiles(t *testing.T) {
	root := t.TempDir()
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	t.Setenv("TEMP", temporary)
	t.Setenv("TMP", temporary)
	writeProject(t, root, "example.com/app")
	writeFile(t, filepath.Join(root, "api", "resource.go"), resourceSource)
	source := "package provider\nimport api \"example.com/app/api\"\ntype database struct{}\nfunc (*database) Read() api.Value{return api.Value{}}\n//plystra:implements-resource data.database/v1\nfunc New()(*database,error){panic(\"constructor-entry\")}\n"
	for _, mode := range []string{"valid", "conformance", "package-load"} {
		body := source
		if mode == "conformance" {
			body = strings.Replace(source, "Read() api.Value", "Other() api.Value", 1)
		}
		if mode == "package-load" {
			body = strings.Replace(source, "example.com/app/api", "example.com/app/missing", 1)
		}
		writeFile(t, filepath.Join(root, "provider", "provider.go"), body)
		before := snapshotFiles(t, root)
		_, err := discoverApplicationResult(t, root, goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}))
		if (err != nil) != (mode != "valid") {
			t.Fatalf("discovery = %v", err)
		}
		if err != nil && (strings.Contains(err.Error(), temporary) || strings.Contains(err.Error(), "plystra-discovery-")) {
			t.Fatalf("temporary path disclosed: %v", err)
		}
		if !reflect.DeepEqual(before, snapshotFiles(t, root)) {
			t.Fatal("discovery changed source files")
		}
		entries, err := os.ReadDir(temporary)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "plystra-discovery-") {
				t.Fatal("discovery left temporary module state")
			}
		}
	}
}

func TestResourceDiscoveryDoesNotPromoteUnrelatedOrdinaryModules(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	app := filepath.Join(root, "app")
	writeProject(t, app, "example.com/app")
	writeFile(t, filepath.Join(app, "go.mod"), "module example.com/app\ngo 1.26\nrequire (\nexample.com/project v1.0.0\nexample.com/value v1.0.0\n)\nreplace example.com/project => ../project\nreplace example.com/unused => ../unused\nreplace example.com/value v1.0.0 => ../value\nreplace example.com/value v1.1.0 => ../new-value\n")
	writeProject(t, filepath.Join(root, "project"), "example.com/project")
	writeFile(t, filepath.Join(root, "project", "go.mod"), "module example.com/project\ngo 1.26\nrequire example.com/unused v1.0.0\n")
	writeFile(t, filepath.Join(root, "project", "resource.go"), "package project\nimport \"example.com/value\"\n//plystra:resource data.database/v1\ntype Resource interface{Read() value.Value}\ntype database struct{}\nfunc (*database) Read() value.Value{return value.Value{Legacy:1}}\n//plystra:implements-resource data.database/v1\nfunc New()(*database,error){panic(\"constructor-entry\")}\n")
	writeFile(t, filepath.Join(root, "unused", "go.mod"), "module example.com/unused\ngo 1.26\nrequire example.com/value v1.1.0\n")
	writeFile(t, filepath.Join(root, "value", "go.mod"), "module example.com/value\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "value", "value.go"), "package value\ntype Value struct{Legacy int}\n")
	writeFile(t, filepath.Join(root, "new-value", "go.mod"), "module example.com/value\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "new-value", "value.go"), "package value\ntype Value struct{Updated bool}\n")
	environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"})
	project, err := projectlocate.Find(app)
	if err != nil {
		t.Fatal(err)
	}
	dependencies, err := moduledependency.Discover(t.Context(), project, moduledependency.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	value, found := dependencies.ByPath("example.com/value")
	if !found || value.SelectedVersion() != "v1.0.0" {
		t.Fatalf("fixture selected value = %#v", value)
	}
	before := snapshotFiles(t, root)
	discovery, err := interfaceinventory.DiscoverApplication(t.Context(), project, dependencies, interfaceinventory.Options{Environment: environment})
	if err != nil || len(discovery.ResourceProviders().Providers()) != 1 {
		t.Fatalf("discovery = %v", err)
	}
	if !reflect.DeepEqual(before, snapshotFiles(t, root)) {
		t.Fatal("discovery mutated the pruned graph")
	}
}

func TestResourceProviderDiscoveryRetainsNamedDependencies(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeProject(t, root, "example.com/app")
	writeFile(t, filepath.Join(root, "api", "resource.go"), resourceSource)
	source := "package provider\nimport api \"example.com/app/api\"\ntype Config struct{Limit int}\ntype database struct{}\nfunc (*database) Read() api.Value{return api.Value{}}\n//plystra:implements-resource data.database/v1\nfunc New(cfg Config, primary api.Resource, Secondary api.Resource)(*database,error){panic(\"constructor-entry\")}\n"
	writeFile(t, filepath.Join(root, "provider", "provider.go"), source)
	environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"})
	provider := discoverApplication(t, root, environment).ResourceProviders().Providers()[0]
	for n, name := range []string{"primary", "Secondary"} {
		dep := provider.Dependencies()[n]
		if dep.ParameterName() != name || dep.ParameterPosition() != n+2 || dep.ID() != "data.database/v1" {
			t.Fatalf("dependency = %#v", dep)
		}
	}
	writeFile(t, filepath.Join(root, "provider", "provider.go"), strings.Replace(source, "Read() api.Value", "Other() api.Value", 1))
	_, err := discoverApplicationResult(t, root, environment)
	var invalid *resourceproviderinventory.ValidationError
	if !errors.As(err, &invalid) || invalid.ModulePath() != "example.com/app" || invalid.SourcePath() != "provider/provider.go" || invalid.Line() != 7 {
		t.Fatalf("error = %v", err)
	}
}
