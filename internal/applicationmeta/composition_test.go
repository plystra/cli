package applicationmeta_test

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestPrivateExportInventoryExcludesInertDependencyDeclarations(t *testing.T) {
	first := []byte("http: {address: PRIVATE_INERT}\ncomposition:\n  exports:\n    common: # PRIVATE_COMMENT\n      config:\n        example.com/dependency/service.New: {label: PRIVATE_EXPORT, pointer: null}\n  adopt: [{module: example.com/ignored, export: ignored}]\n")
	second := []byte("composition: {exports: {common: {config: {example.com/dependency/service.New: {pointer: ~, label: PRIVATE_EXPORT}}}}}\nhttp: {address: OTHER_INERT}\n")
	a, err := applicationmeta.PrivateExportInventoryYAML(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := applicationmeta.PrivateExportInventoryYAML(second)
	if err != nil || string(a) != string(b) {
		t.Fatal("inert or presentation change altered export inventory", err)
	}
	for _, forbidden := range []string{"PRIVATE_INERT", "PRIVATE_COMMENT", "ignored"} {
		if strings.Contains(string(a), forbidden) {
			t.Fatal("captured non-export data")
		}
	}
	if !strings.Contains(string(a), "PRIVATE_EXPORT") {
		t.Fatal("lost private export value")
	}
	if _, err := applicationmeta.ParseExportInventorySource("plystra.yaml", a); err != nil {
		t.Fatal(err)
	}
}

func TestParseReusableConfigurationExportsAndAdoptions(t *testing.T) {
	t.Parallel()

	manifest, err := applicationmeta.Parse([]byte(`composition:
  exports:
    defaults:
      interfaces:
        require:
          - email.send/v1
        use:
          email.send/v1: example.com/acme/platform/mailer.New
  adopt:
    - module: example.com/acme/platform
      export: defaults
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	exports := manifest.Exports()
	if len(exports) != 1 || exports[0].Name() != "defaults" {
		t.Fatalf("Exports = %#v", exports)
	}
	fragment := exports[0].Manifest()
	if got := interfaceRequirementIDs(fragment.InterfaceRequirements()); !reflect.DeepEqual(got, []string{"email.send/v1"}) {
		t.Fatalf("export requirements = %q", got)
	}
	choices := fragment.ImplementationChoices()
	if len(choices) != 1 || choices[0].InterfaceID().String() != "email.send/v1" || choices[0].Constructor().String() != "example.com/acme/platform/mailer.New" {
		t.Fatalf("export choices = %#v", choices)
	}
	adoptions := manifest.ExportAdoptions()
	if len(adoptions) != 1 || adoptions[0].ModulePath() != "example.com/acme/platform" || adoptions[0].ExportName() != "defaults" {
		t.Fatalf("ExportAdoptions = %#v", adoptions)
	}

	exports[0] = applicationmeta.ConfigurationExport{}
	adoptions[0] = applicationmeta.ExportAdoption{}
	if manifest.Exports()[0].Name() != "defaults" || manifest.ExportAdoptions()[0].ExportName() != "defaults" {
		t.Fatal("composition accessors exposed mutable storage")
	}
}

func TestParseExportInventorySourceReadsOnlyInertRootExports(t *testing.T) {
	t.Parallel()

	manifest, err := applicationmeta.ParseExportInventorySource("dependency/plystra.yaml", []byte(`http: invalid-consumer-value
timeouts: [invalid-consumer-value]
capabilities: invalid-consumer-value
interfaces: invalid-consumer-value
config: invalid-consumer-value
resources: [invalid-consumer-value]
data: invalid-consumer-value
composition:
  adopt: invalid-consumer-value
  exports:
    defaults:
      interfaces:
        require: [email.send/v1]
      resources:
        instances:
          primary:
            use: example.com/acme/platform/postgres.New
`))
	if err != nil {
		t.Fatalf("ParseExportInventorySource: %v", err)
	}
	exports := manifest.Exports()
	if len(exports) != 1 || exports[0].Name() != "defaults" {
		t.Fatalf("Exports = %#v", exports)
	}
	fragment := exports[0].Manifest()
	requirements := fragment.InterfaceRequirements()
	if len(requirements) != 1 || requirements[0].ID().String() != "email.send/v1" || requirements[0].Source() != `dependency/plystra.yaml composition.exports["defaults"].interfaces.require["email.send/v1"]` {
		t.Fatalf("export requirements = %#v", requirements)
	}
	if len(manifest.ExportAdoptions()) != 0 {
		t.Fatalf("dependency adoptions were activated: %#v", manifest.ExportAdoptions())
	}
}

func TestParseExportInventorySourceRejectsUnknownStructure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want string
	}{
		{name: "unknown root key", yaml: "unknown: true\n", want: `unknown key "unknown"`},
		{name: "unknown composition key", yaml: "composition: {unknown: true}\n", want: `composition contains unknown key "unknown"`},
		{name: "non-mapping composition", yaml: "composition: invalid\n", want: "composition must be a mapping"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := applicationmeta.ParseExportInventorySource("plystra.yaml", []byte(test.yaml))
			if !errors.Is(err, applicationmeta.ErrInvalidManifest) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseExportInventorySource error = %v, want ErrInvalidManifest containing %q", err, test.want)
			}
		})
	}
}

func TestParseRejectsInvalidReusableConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want string
	}{
		{name: "invalid export name", yaml: "composition: {exports: {Bad: {}}}\n", want: "export name"},
		{name: "export process setting", yaml: "composition: {exports: {defaults: {http: {address: ':8080'}}}}\n", want: "may contain only interfaces, config, and resources"},
		{name: "recursive export", yaml: "composition: {exports: {defaults: {composition: {adopt: []}}}}\n", want: "may contain only interfaces, config, and resources"},
		{name: "export data member", yaml: "composition: {exports: {defaults: {data: {members: {orders: {}}}}}}\n", want: "may contain only interfaces, config, and resources"},
		{name: "sparse export requirement", yaml: "composition: {exports: {defaults: {interfaces: {require: {add: [email.send/v1]}}}}}\n", want: "positive sequence"},
		{name: "export removal", yaml: "composition: {exports: {defaults: {interfaces: {use: {email.send/v1: {$remove: true}}}}}}\n", want: "cannot contain reserved $remove mappings"},
		{name: "missing adoption module", yaml: "composition: {adopt: [{export: defaults}]}\n", want: "exactly module and export"},
		{name: "extra adoption key", yaml: "composition: {adopt: [{module: example.com/acme/platform, export: defaults, version: v1.0.0}]}\n", want: "exactly module and export"},
		{name: "invalid adoption module", yaml: "composition: {adopt: [{module: '../platform', export: defaults}]}\n", want: "valid Go Module path"},
		{name: "duplicate adoption", yaml: "composition: {adopt: [{module: example.com/acme/platform, export: defaults}, {module: example.com/acme/platform, export: defaults}]}\n", want: "duplicates"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := applicationmeta.Parse([]byte(test.yaml))
			if !errors.Is(err, applicationmeta.ErrInvalidManifest) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse error = %v, want ErrInvalidManifest containing %q", err, test.want)
			}
		})
	}
}

func TestParseExportsRejectsReservedRemovalMappingsAtEveryDepth(t *testing.T) {
	t.Parallel()
	for _, fragment := range []string{
		`config: {example.com/acme/service.New: {$remove: true}}`,
		`config: {example.com/acme/service.New: {settings: {private-key: {$remove: true}}}}`,
		`config: {example.com/acme/service.New: {settings: [{$remove: true}]}}`,
		`config: {example.com/acme/service.New: {settings: {$remove: false}}}`,
		`config: {example.com/acme/service.New: {settings: {$remove: "private-value"}}}`,
		`resources: {instances: {database: {$remove: true}}}`,
		`resources: {bind: {instances: {database: {upstream: {$remove: true}}}}}`,
	} {
		for _, parse := range []struct {
			name string
			run  func([]byte) (applicationmeta.Manifest, error)
		}{
			{name: "current", run: applicationmeta.Parse},
			{name: "dependency", run: func(data []byte) (applicationmeta.Manifest, error) {
				return applicationmeta.ParseExportInventorySource("dependency/plystra.yaml", data)
			}},
		} {
			t.Run(parse.name+"/"+fragment, func(t *testing.T) {
				data := []byte("composition: {exports: {defaults: {" + fragment + "}}}\n")
				manifest, err := parse.run(data)
				if !errors.Is(err, applicationmeta.ErrInvalidManifest) || !strings.Contains(err.Error(), `composition.exports["defaults"] cannot contain reserved $remove mappings`) {
					t.Fatalf("Parse export = %#v, %v", manifest, err)
				}
				if len(manifest.Exports()) != 0 || strings.Contains(err.Error(), "private-key") || strings.Contains(err.Error(), "private-value") {
					t.Fatalf("failed export retained declarations or exposed private data: %v", err)
				}
			})
		}
	}
}

func TestParseExportsPreservesOrdinaryConfigurationValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{`null`, `{}`, `[]`, `false`, `"$remove"`, `{$remove-label: true}`, `{$remove: true, ordinary: 1}`} {
		data := []byte("composition: {exports: {defaults: {config: {example.com/acme/service.New: {settings: " + value + "}}}}}\n")
		manifest, err := applicationmeta.Parse(data)
		if err != nil || len(manifest.Exports()) != 1 || len(manifest.Exports()[0].Manifest().Configurations()) != 1 {
			t.Fatalf("Parse export with %s = %#v, %v", value, manifest, err)
		}
	}
}

func TestParseExportsRejectsConstructorRemovalWithSiblings(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		`{$remove: true, private-key: private-value}`, `{$remove: false, private-key: private-value}`,
		`{$remove: null, private-key: private-value}`, `{$remove: "private-value", private-key: private-value}`,
		`{$remove: {private-key: private-value}, private-key: private-value}`, `{private-key: private-value, $remove: null}`,
	} {
		data := []byte("composition: {exports: {unused: {config: {example.com/acme/service.New: " + value + "}}}}\n")
		for _, parse := range []struct {
			name string
			run  func([]byte) (applicationmeta.Manifest, error)
		}{
			{name: "current", run: applicationmeta.Parse},
			{name: "dependency", run: func(data []byte) (applicationmeta.Manifest, error) {
				return applicationmeta.ParseExportInventorySource("dependency/plystra.yaml", data)
			}},
		} {
			t.Run(parse.name+"/"+value, func(t *testing.T) {
				manifest, err := parse.run(data)
				if !errors.Is(err, applicationmeta.ErrInvalidManifest) || !strings.Contains(err.Error(), "must be a mapping or {$remove: true}") {
					t.Fatalf("Parse export = %#v, %v", manifest, err)
				}
				if len(manifest.Exports()) != 0 || strings.Contains(err.Error(), "private-key") || strings.Contains(err.Error(), "private-value") {
					t.Fatalf("failed export retained declarations or exposed private data: %v", err)
				}
			})
		}
	}
}

func TestResolveAdoptedExportsRejectsUnsupportedResourceFragmentOnlyAtActivation(t *testing.T) {
	t.Parallel()

	dependencyRoot, err := applicationmeta.ParseExportInventorySource("dependency/plystra.yaml", []byte(`composition:
  exports:
    defaults:
      interfaces:
        require: [email.send/v1]
      resources:
        instances:
          primary:
            use: example.com/acme/platform/postgres.New
`))
	if err != nil {
		t.Fatalf("ParseExportInventorySource: %v", err)
	}
	exports := dependencyRoot.Exports()
	if len(exports) != 1 || exports[0].Name() != "defaults" {
		t.Fatalf("Exports = %#v", exports)
	}
	if got := interfaceRequirementIDs(exports[0].Manifest().InterfaceRequirements()); !reflect.DeepEqual(got, []string{"email.send/v1"}) {
		t.Fatalf("supported export fragment requirements = %q", got)
	}

	empty := mustParseManifest(t, "{}\n")
	withoutAdoption, err := applicationmeta.ResolveAdoptedExports("example.com/acme/app", empty, empty, []applicationmeta.Dependency{{
		ModulePath:    "example.com/acme/platform",
		ModuleVersion: "v1.2.3",
		Manifest:      dependencyRoot,
	}})
	if err != nil || len(withoutAdoption) != 0 {
		t.Fatalf("ResolveAdoptedExports without adoption = %#v, %v", withoutAdoption, err)
	}

	selected := mustParseManifest(t, `composition:
  adopt:
    - module: example.com/acme/platform
      export: defaults
`)
	_, err = applicationmeta.ResolveAdoptedExports("example.com/acme/app", empty, selected, []applicationmeta.Dependency{{
		ModulePath:    "example.com/acme/platform",
		ModuleVersion: "v1.2.3",
		Manifest:      dependencyRoot,
	}})
	if !errors.Is(err, applicationmeta.ErrResolveAdoptedExports) ||
		!strings.Contains(err.Error(), `export "defaults" from module "example.com/acme/platform"`) ||
		!strings.Contains(err.Error(), `composition.exports["defaults"] resources are not supported by this installed CLI`) {
		t.Fatalf("ResolveAdoptedExports error = %v", err)
	}
}

func TestParseExportsRejectsMalformedResourceDeclarations(t *testing.T) {
	t.Parallel()
	for _, resource := range []string{
		`null`,
		`[]`,
		`{private-key: private-value}`,
		`{instances: {}, instances: {}}`,
		`{instances: null}`,
		`{instances: []}`,
		`{instances: {1: {}}}`,
		`{instances: {Database: {}}}`,
		`{instances: {database.1: {}}}`,
		`{instances: {database..primary: {}}}`,
		`{instances: {database--primary: {}}}`,
		`{instances: {database-: {}}}`,
		`{instances: {` + strings.Repeat("a", 129) + `: {}}}`,
		`{instances: {database: {}, database: {}}}`,
		`{instances: {database: null}}`,
		`{instances: {database: []}}`,
		`{instances: {database: {private-key: private-value}}}`,
		`{instances: {database: {use: private-value}}}`,
		`{instances: {database: {use: null}}}`,
		`{instances: {database: {use: example.com/db.new}}}`,
		`{instances: {database: {config: null}}}`,
		`{instances: {database: {config: []}}}`,
		`{instances: {database: {config: {1: private-value}}}}`,
		`{instances: {database: {config: {private-key: 1, private-key: 2}}}}`,
		`{bind: null}`,
		`{bind: []}`,
		`{bind: {example.com/service.New: {database: primary}}}`,
		`{bind: {implementations: null}}`,
		`{bind: {instances: []}}`,
		`{bind: {implementations: {private-key: {database: primary}}}}`,
		`{bind: {instances: {Bad: {database: primary}}}}`,
		`{bind: {instances: {primary: null}}}`,
		`{bind: {instances: {primary: {_ : secondary}}}}`,
		`{bind: {instances: {primary: {if: secondary}}}}`,
		`{bind: {instances: {primary: {private-key: private-value}}}}`,
		`{bind: {instances: {primary: {database: null}}}}`,
		`{bind: {instances: {primary: {database: 1}}}}`,
		`{bind: {instances: {primary: {database: Database}}}}`,
		`{bind: {instances: {primary: {database: {instance: secondary}}}}}`,
		`{bind: {instances: {primary: {database: secondary, database: third}}}}`,
	} {
		for _, parse := range []struct {
			name string
			run  func([]byte) (applicationmeta.Manifest, error)
		}{
			{name: "current", run: applicationmeta.Parse},
			{name: "dependency", run: func(data []byte) (applicationmeta.Manifest, error) {
				return applicationmeta.ParseExportInventorySource("dependency/plystra.yaml", data)
			}},
		} {
			t.Run(parse.name+"/"+resource, func(t *testing.T) {
				data := []byte("composition: {exports: {defaults: {resources: " + resource + "}}}\n")
				manifest, err := parse.run(data)
				if !errors.Is(err, applicationmeta.ErrInvalidManifest) || !strings.Contains(err.Error(), `composition.exports["defaults"].resources`) {
					t.Fatalf("Parse Resource export = %#v, %v", manifest, err)
				}
				if len(manifest.Exports()) != 0 || strings.Contains(err.Error(), "private-key") || strings.Contains(err.Error(), "private-value") {
					t.Fatalf("invalid Resource export retained declarations or exposed private data: %v", err)
				}
			})
		}
	}
}

func TestParseExportsPreservesInertResourceSyntaxWithoutResolvingIt(t *testing.T) {
	t.Parallel()
	for _, resource := range []string{
		`{}`,
		`{instances: {}}`,
		`{instances: {database: {}}}`,
		`{instances: {database: {config: {pool: 20}}}}`,
		`{instances: {` + strings.Repeat("a", 128) + `: {use: example.com/db.New}}}`,
		`{instances: {database-1.primary: {use: example.com/db.New, config: {url: {env: DATABASE_URL}, pool: 0, options: null, labels: {}, replicas: []}}}}`,
		`{bind: {implementations: {}, instances: {}}}`,
		`{bind: {implementations: {example.com/service.New: {database: database-1.primary, Database: database.secondary}}, instances: {cache: {upstream: database.primary}}}}`,
		`{bind: {instances: {cache: {"\u03b4": database}}}}`,
	} {
		t.Run(resource, func(t *testing.T) {
			data := []byte("composition: {exports: {defaults: {resources: " + resource + "}}}\n")
			for _, parse := range []func([]byte) (applicationmeta.Manifest, error){
				applicationmeta.Parse,
				func(data []byte) (applicationmeta.Manifest, error) {
					return applicationmeta.ParseExportInventorySource("dependency/plystra.yaml", data)
				},
			} {
				manifest, err := parse(data)
				if err != nil || len(manifest.Exports()) != 1 {
					t.Fatalf("Parse inert Resource export = %#v, %v", manifest, err)
				}
				if len(manifest.InterfaceRequirements()) != 0 || len(manifest.Configurations()) != 0 || len(manifest.ExportAdoptions()) != 0 {
					t.Fatalf("inert Resource export contributed active declarations: %#v", manifest)
				}
			}
		})
	}
}

func TestResolveAdoptedExportsKeepsDependencyRootsInert(t *testing.T) {
	t.Parallel()

	dependencyRoot := mustParseManifest(t, `interfaces:
  require: [hidden.root/v1]
composition:
  exports:
    defaults:
      interfaces:
        require: [email.send/v1]
`)
	root := mustParseManifest(t, "{}\n")
	withoutAdoption, err := applicationmeta.ResolveAdoptedExports("example.com/acme/app", root, root, []applicationmeta.Dependency{{
		ModulePath:    "example.com/acme/platform",
		ModuleVersion: "v1.2.3",
		Manifest:      dependencyRoot,
	}})
	if err != nil || len(withoutAdoption) != 0 {
		t.Fatalf("ResolveAdoptedExports without adoption = %#v, %v", withoutAdoption, err)
	}

	selected := mustParseManifest(t, `composition:
  adopt:
    - module: example.com/acme/platform
      export: defaults
`)
	adopted, err := applicationmeta.ResolveAdoptedExports("example.com/acme/app", root, selected, []applicationmeta.Dependency{{
		ModulePath:    "example.com/acme/platform",
		ModuleVersion: "v1.2.3",
		Manifest:      dependencyRoot,
	}})
	if err != nil {
		t.Fatalf("ResolveAdoptedExports: %v", err)
	}
	if len(adopted) != 1 || adopted[0].ModulePath != "example.com/acme/platform" || adopted[0].ModuleVersion != "v1.2.3" || adopted[0].ExportName != "defaults" {
		t.Fatalf("adopted exports = %#v", adopted)
	}
	composition, err := applicationmeta.Compose(adopted, selected, emptySchemaLookup)
	if err != nil {
		t.Fatalf("Compose adopted export: %v", err)
	}
	if got := interfaceRequirementIDs(composition.Manifest().InterfaceRequirements()); !reflect.DeepEqual(got, []string{"email.send/v1"}) {
		t.Fatalf("effective requirements = %q, want adopted export only", got)
	}
}

func TestResolveAdoptedExportsSupportsSeveralExportsAndSelfAdoption(t *testing.T) {
	t.Parallel()

	root := mustParseManifest(t, `composition:
  exports:
    local:
      interfaces:
        require: [local.health/v1]
  adopt:
    - module: example.com/acme/app
      export: local
    - module: example.com/acme/platform
      export: alpha
    - module: example.com/acme/platform
      export: beta
`)
	dependency := mustParseManifest(t, `composition:
  exports:
    alpha:
      interfaces:
        require: [alpha.run/v1]
    beta:
      interfaces:
        require: [beta.run/v1]
`)
	adopted, err := applicationmeta.ResolveAdoptedExports("example.com/acme/app", root, root, []applicationmeta.Dependency{{
		ModulePath:    "example.com/acme/platform",
		ModuleVersion: "v1.0.0",
		Manifest:      dependency,
	}})
	if err != nil {
		t.Fatalf("ResolveAdoptedExports: %v", err)
	}
	composition, err := applicationmeta.Compose(adopted, root, emptySchemaLookup)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if got := interfaceRequirementIDs(composition.Manifest().InterfaceRequirements()); !reflect.DeepEqual(got, []string{"alpha.run/v1", "beta.run/v1", "local.health/v1"}) {
		t.Fatalf("effective requirements = %q", got)
	}
}

func TestResolveAdoptedExportsReportsMissingExactIdentity(t *testing.T) {
	t.Parallel()

	selected := mustParseManifest(t, `composition:
  adopt:
    - module: example.com/acme/platform
      export: missing
`)
	_, err := applicationmeta.ResolveAdoptedExports("example.com/acme/app", mustParseManifest(t, "{}\n"), selected, []applicationmeta.Dependency{{
		ModulePath:    "example.com/acme/platform",
		ModuleVersion: "v1.0.0",
		Manifest:      mustParseManifest(t, "composition: {exports: {defaults: {}}}\n"),
	}})
	if !errors.Is(err, applicationmeta.ErrExportNotFound) || !strings.Contains(err.Error(), "example.com/acme/platform") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("ResolveAdoptedExports error = %v", err)
	}
}

func TestResolveAdoptedExportsKeepsShortProjectIdentityLocal(t *testing.T) {
	t.Parallel()
	root := mustParseManifest(t, "composition: {exports: {local: {interfaces: {require: [local.health/v1]}}}, adopt: [{module: my-app, export: local}]}\n")
	adopted, err := applicationmeta.ResolveAdoptedExports("my-app", root, root, nil)
	if err != nil || len(adopted) != 1 || adopted[0].ModulePath != "my-app" || adopted[0].ExportName != "local" {
		t.Fatalf("self-adoption = %#v, %v", adopted, err)
	}
	for _, module := range []string{"other-app", "example.com/my-app"} {
		_, err := applicationmeta.ResolveAdoptedExports(module, root, root, nil)
		if !errors.Is(err, applicationmeta.ErrExportNotFound) {
			t.Fatalf("unavailable short adoption in %q = %v", module, err)
		}
		_, err = applicationmeta.ResolveAdoptedExports(module, root, root, []applicationmeta.Dependency{{ModulePath: "my-app", Manifest: root}})
		if !errors.Is(err, applicationmeta.ErrResolveAdoptedExports) || !strings.Contains(err.Error(), "dependency module path") {
			t.Fatalf("short dependency inventory in %q = %v", module, err)
		}
	}
}

func TestApplyOverlayComposesExportAdoptionsAsCompleteOrSparseSets(t *testing.T) {
	t.Parallel()

	base := mustParseManifest(t, `composition:
  adopt:
    - {module: example.com/acme/platform, export: alpha}
    - {module: example.com/acme/platform, export: beta}
`)
	sparse, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(`composition:
  adopt:
    add:
      - {module: example.com/acme/platform, export: gamma}
    remove:
      - {module: example.com/acme/platform, export: alpha}
`))
	if err != nil {
		t.Fatalf("ParseOverlaySource(sparse): %v", err)
	}
	selected, err := applicationmeta.ApplyOverlay(base, sparse, emptySchemaLookup)
	if err != nil {
		t.Fatalf("ApplyOverlay(sparse): %v", err)
	}
	if got := exportAdoptionNames(selected.ExportAdoptions()); !reflect.DeepEqual(got, []string{"beta", "gamma"}) {
		t.Fatalf("sparse adoptions = %q", got)
	}

	complete, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(`composition:
  adopt:
    - {module: example.com/acme/platform, export: delta}
`))
	if err != nil {
		t.Fatalf("ParseOverlaySource(complete): %v", err)
	}
	selected, err = applicationmeta.ApplyOverlay(base, complete, emptySchemaLookup)
	if err != nil {
		t.Fatalf("ApplyOverlay(complete): %v", err)
	}
	if got := exportAdoptionNames(selected.ExportAdoptions()); !reflect.DeepEqual(got, []string{"delta"}) {
		t.Fatalf("complete adoptions = %q", got)
	}
}

func TestSetExportAdoptionsWritesDeterministicExactSet(t *testing.T) {
	t.Parallel()

	updated, err := applicationmeta.SetExportAdoptions([]byte("http:\n  address: ':8080'\n"), "example.com/acme/platform", []string{"zeta", "alpha"})
	if err != nil {
		t.Fatalf("SetExportAdoptions: %v", err)
	}
	manifest, err := applicationmeta.Parse(updated)
	if err != nil {
		t.Fatalf("Parse updated: %v", err)
	}
	adoptions := manifest.ExportAdoptions()
	if len(adoptions) != 2 || adoptions[0].ExportName() != "alpha" || adoptions[1].ExportName() != "zeta" {
		t.Fatalf("adoptions = %#v", adoptions)
	}
	if !strings.Contains(string(updated), "module: example.com/acme/platform\n      export: alpha") || !strings.Contains(string(updated), "module: example.com/acme/platform\n      export: zeta") {
		t.Fatalf("updated YAML =\n%s", updated)
	}
}

func TestExportAdoptionsAcceptProjectModuleIdentities(t *testing.T) {
	t.Parallel()
	for _, module := range []string{"my-app", "example.com/acme/app", "example.com/acme/app/v2", "gopkg.in/yaml.v3"} {
		t.Run(module, func(t *testing.T) {
			updated, err := applicationmeta.SetExportAdoptions([]byte("# retained\n{}\n"), module, []string{"local"})
			if err != nil {
				t.Fatal(err)
			}
			manifest := mustParseManifest(t, string(updated))
			if !strings.Contains(string(updated), "# retained") || len(manifest.ExportAdoptions()) != 1 || manifest.ExportAdoptions()[0].ModulePath() != module {
				t.Fatalf("adoption identity or comment lost: %s", updated)
			}
			for _, set := range []string{"[{module: " + module + ", export: local}]", "{add: [{module: " + module + ", export: local}]}", "{remove: [{module: " + module + ", export: local}]}"} {
				if _, err := applicationmeta.ParseOverlaySource("plystra.test.yaml", []byte("composition: {adopt: "+set+"}\n")); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	for _, module := range []string{"", ".", "..", "local/app", "example.com/acme/app/v1", "../app", "not a module", "example.com/app@v1.0.0", "NUL"} {
		t.Run("invalid/"+module, func(t *testing.T) {
			if _, err := applicationmeta.SetExportAdoptions([]byte("{}\n"), module, []string{"local"}); !errors.Is(err, applicationmeta.ErrSetExportAdoptions) {
				t.Fatalf("SetExportAdoptions(%q) = %v", module, err)
			}
			entry := "{module: " + strconv.Quote(module) + ", export: local}"
			for _, set := range []string{"[" + entry + "]", "{add: [" + entry + "]}", "{remove: [" + entry + "]}"} {
				if _, err := applicationmeta.Parse([]byte("composition: {adopt: " + set + "}\n")); !errors.Is(err, applicationmeta.ErrInvalidManifest) {
					t.Fatalf("Parse invalid adoption %q = %v", set, err)
				}
			}
		})
	}
}

func mustParseManifest(t *testing.T, data string) applicationmeta.Manifest {
	t.Helper()
	manifest, err := applicationmeta.Parse([]byte(data))
	if err != nil {
		t.Fatalf("Parse manifest: %v", err)
	}
	return manifest
}

func emptySchemaLookup(constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
	return implementationinventory.Configuration{}, false
}

func exportAdoptionNames(values []applicationmeta.ExportAdoption) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.ExportName()
	}
	return result
}
