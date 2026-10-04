package applicationmeta_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestResourceSelectionIdentitiesPreserveUnboundEntriesAndExactRemovals(t *testing.T) {
	t.Parallel()
	lower := resourceManifest(t, "base.yaml", `resources:
  instances:
    primary: {use: example.com/database.New, config: {unknown: PRIVATE_OLD}}
    removed: {use: example.com/database.New}
  bind:
    instances:
      primary: {old: removed, retained: primary}
`)
	root := resourceManifest(t, "plystra.yaml", `resources:
  instances:
    primary: {$remove: true}
    unbound: {config: {unknown: PRIVATE_UNBOUND}}
    removed: {$remove: true}
  bind:
    instances:
      primary: {old: {$remove: true}}
`)
	overlay, err := applicationmeta.ParseOverlaySource("plystra.test.yaml", []byte(`resources:
  instances:
    primary: {}
    unbound: {config: {unknown: PRIVATE_OVERLAY}}
`))
	if err != nil {
		t.Fatal(err)
	}
	layered, err := applicationmeta.ApplyOverlay(root, overlay, resourceLookup(t, "Value string"))
	if err != nil {
		t.Fatal(err)
	}
	instances, bindings, err := applicationmeta.ResourceSelectionIdentities([]applicationmeta.Manifest{lower, layered})
	if err != nil || len(instances) != 2 || instances[0].Name() != "primary" || instances[1].Name() != "unbound" {
		t.Fatalf("selection identities = %v, %v", instances, err)
	}
	for _, instance := range instances {
		if instance.Provider().String() != "" || instance.HasConfiguration() || len(instance.ConfigurationYAML()) != 0 {
			t.Fatal("a removed provider reappeared or planning retained private Config")
		}
	}
	if len(bindings) != 1 || bindings[0].Consumer() != "primary" || bindings[0].ParameterName() != "retained" || bindings[0].Target() != "primary" {
		t.Fatal("identity planning changed unrelated bindings or ignored an exact tombstone")
	}
	if instances[0].DeclarationSource().Path() != "plystra.test.yaml" || len(lower.ResourceInstances()[0].ConfigurationYAML()) == 0 {
		t.Fatal("identity planning lost source or mutated captured layers")
	}
}

func TestWithoutConstructorConfigurationPreservesIdentityAndAllSources(t *testing.T) {
	t.Parallel()
	lookup := resourceLookup(t, "Value string")
	ordinary := composeSchemaLookup(map[string]implementationinventory.Configuration{
		"example.com/service.New": composeSchema(t, "Value string"),
		"example.com/removed.New": composeSchema(t, "Value string"),
	})
	allSchemas := func(namespace applicationmeta.ConfigurationNamespace, symbol constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		if namespace == applicationmeta.ConfigurationNamespaceResource {
			return lookup(namespace, symbol)
		}
		return ordinary(namespace, symbol)
	}
	root := resourceManifest(t, "plystra.yaml", `template: example.com/base
http: {address: localhost:8080, expose: {app.run/v1: {transport: connect}}}
timeouts: {startup: 5s}
interfaces:
  require: {add: [app.run/v1], remove: [old.run/v1]}
  use: {app.run/v1: example.com/service.New, old.run/v1: {$remove: true}}
  policies: {app.run/v1: {timeout: 1s}, old.run/v1: {$remove: true}}
config:
  example.com/service.New: {value: PRIVATE_IMPLEMENTATION}
  example.com/removed.New: {$remove: true}
resources:
  instances:
    primary: {use: example.com/database.New, config: {value: PRIVATE_RESOURCE}}
    unconfigured: {use: example.com/alternative.New, config: {$remove: true}}
    removed: {$remove: true}
  bind:
    instances:
      primary: {upstream: unconfigured, obsolete: {$remove: true}}
    implementations:
      example.com/service.New: {database: primary, obsolete: {$remove: true}}
`)
	overlay, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(`http: {expose: {old.run/v1: {$remove: true}}}
resources: {instances: {primary: {config: {value: PRIVATE_OVERLAY}}}}
`))
	if err != nil {
		t.Fatal(err)
	}
	overlay, err = applicationmeta.WithProjectModule(overlay, "example.com/current")
	if err != nil {
		t.Fatal(err)
	}
	layered, err := applicationmeta.ApplyOverlay(root, overlay, allSchemas)
	if err != nil {
		t.Fatal(err)
	}
	stripped := applicationmeta.WithoutConstructorConfiguration(layered)
	if !reflect.DeepEqual(stripped, applicationmeta.WithoutConstructorConfiguration(stripped)) {
		t.Fatal("identity projection is not idempotent")
	}
	beforeDecisions, err := applicationmeta.ConfigurationDecisions(layered, allSchemas)
	if err != nil {
		t.Fatal(err)
	}
	rejectLookup := func(applicationmeta.ConfigurationNamespace, constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		t.Fatal("identity composition attempted a Config schema lookup")
		return implementationinventory.Configuration{}, false
	}
	afterDecisions, err := applicationmeta.ConfigurationDecisions(stripped, rejectLookup)
	if err != nil {
		t.Fatal(err)
	}
	var identities []applicationmeta.ConfigurationDecision
	for _, decision := range beforeDecisions {
		if !strings.HasPrefix(decision.Path(), "config[") && !strings.Contains(decision.Path(), "].config") {
			identities = append(identities, decision)
		}
	}
	if !reflect.DeepEqual(identities, afterDecisions) {
		t.Fatal("identity projection lost declarations, tombstones, digests, or sources")
	}
	composition, err := applicationmeta.Compose(nil, stripped, rejectLookup)
	if err != nil {
		t.Fatal("stored layers retained configuration", err)
	}
	if len(composition.CurrentLayers()) != 2 || len(composition.Manifest().Configurations()) != 0 {
		t.Fatal("identity composition lost layer boundaries or kept Implementation Config")
	}
	for index, layer := range composition.CurrentLayers() {
		if _, err := applicationmeta.ConfigurationDecisions(layer, rejectLookup); err != nil {
			t.Fatal(err)
		}
		if index == 1 && resourceConfig(t, layer, "primary").DeclarationSource().Path() != "plystra.production.yaml" {
			t.Fatal("config-only layer lost its instance declaration source")
		}
	}
	instance := resourceConfig(t, composition.Manifest(), "primary")
	if instance.Provider().String() != resourceProvider || instance.ProviderDeclarationSource().Path() != "plystra.yaml" || instance.ProviderDeclarationSource().ModulePath() != "example.com/current" || instance.HasConfiguration() || len(instance.ConfigurationYAML()) != 0 {
		t.Fatal("identity composition lost inherited provider source or kept Resource Config")
	}
	if len(layered.Configurations()) != 1 || !bytes.Contains(resourceConfig(t, layered, "primary").ConfigurationYAML(), []byte("PRIVATE_OVERLAY")) {
		t.Fatal("projection mutated outer input")
	}
	original, err := applicationmeta.Compose(nil, layered, allSchemas)
	if err != nil {
		t.Fatal(err)
	}
	if len(original.Manifest().Configurations()) != 1 || !bytes.Contains(resourceConfig(t, original.Manifest(), "primary").ConfigurationYAML(), []byte("PRIVATE_OVERLAY")) || !bytes.Contains(resourceConfig(t, original.CurrentLayers()[0], "primary").ConfigurationYAML(), []byte("PRIVATE_RESOURCE")) {
		t.Fatal("projection mutated stored input layers")
	}
}

func TestWithoutConstructorConfigurationAllowsPlanningButNotInvalidFinalState(t *testing.T) {
	t.Parallel()
	lookup := resourceLookup(t, "Value string")
	data := []byte(resourceDocument("{use: " + resourceProvider + ", config: {value: [PRIVATE_INVALID]}}"))
	before := bytes.Clone(data)
	manifest := resourceManifest(t, "plystra.yaml", string(data))
	if _, err := applicationmeta.Compose(nil, manifest, lookup); !errors.Is(err, applicationmeta.ErrConfigurationInvalidValue) {
		t.Fatalf("original typed Resource Config unexpectedly valid: %v", err)
	}
	identity := applicationmeta.WithoutConstructorConfiguration(manifest)
	plan, err := applicationmeta.Compose(nil, identity, lookup)
	if err != nil || len(plan.Manifest().ResourceInstances()) != 1 {
		t.Fatalf("identity-only planning rejected invalid old Config: %v", err)
	}
	if _, err := applicationmeta.Compose(nil, manifest, lookup); !errors.Is(err, applicationmeta.ErrConfigurationInvalidValue) {
		t.Fatal("planning weakened validation of the original manifest", err)
	}
	updated, changed, err := applicationmeta.SetResourceProvider(data, "database.primary", mustImplementationChoiceConstructor(t, replacementResourceProvider), true, nil)
	if err != nil || !changed || !bytes.Equal(before, data) {
		t.Fatalf("repair of old typed Config: %t, %v", changed, err)
	}
	final, err := applicationmeta.Compose(nil, resourceManifest(t, "plystra.yaml", string(updated)), lookup)
	if err != nil || resourceConfig(t, final.Manifest(), "database.primary").HasConfiguration() {
		t.Fatal("discarded old Config still affected final composition", err)
	}
	ordinary := resourceManifest(t, "plystra.yaml", "interfaces: {use: {app.run/v1: example.com/service.New}}\nconfig: {example.com/service.New: {value: PRIVATE_INVALID}, example.com/missing.New: {$remove: true}}\n")
	if _, err := applicationmeta.Compose(nil, ordinary, lookup); !errors.Is(err, applicationmeta.ErrConfigurationSchema) {
		t.Fatal("original Implementation Config unexpectedly valid", err)
	}
	if _, err := applicationmeta.Compose(nil, applicationmeta.WithoutConstructorConfiguration(ordinary), lookup); err != nil {
		t.Fatal("identity plan retained Implementation Config or removals", err)
	}
}
