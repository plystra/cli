package applicationmeta_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestConstructorConfigurationRejectsResourceProviderNamespace(t *testing.T) {
	t.Parallel()
	lookup := resourceLookup(t, "Value string; Token configuration.Secret")
	for _, entry := range []string{"{}", "{value: PRIVATE_VALUE, token: {env: PRIVATE_SECRET}}", "{$remove: true}", "{value: {$remove: true}}"} {
		for _, mode := range []string{"root", "environment", "replacement", "removed lower"} {
			t.Run(mode+"/"+entry, func(t *testing.T) {
				if strings.Contains(entry, "$remove") && mode != "environment" {
					t.Skip("root and complete replacement documents cannot contain removal markers")
				}
				module, source := "example.com/current", "plystra.yaml"
				switch mode {
				case "environment":
					source = "plystra.production.yaml"
				case "replacement":
					source = "deploy/selected.yaml"
				}
				data := []byte("config: {" + resourceProvider + ": " + entry + "}\n")
				original := bytes.Clone(data)
				parse := applicationmeta.ParseSource
				if mode == "environment" {
					parse = applicationmeta.ParseOverlaySource
				} else if mode == "replacement" {
					parse = applicationmeta.ParseCompleteSource
				}
				manifest, err := parse(source, data)
				if err != nil {
					t.Fatal(err)
				}
				manifest, err = applicationmeta.WithProjectModule(manifest, module)
				if err != nil {
					t.Fatal(err)
				}
				assertSchemaError := func(err error) {
					t.Helper()
					var detail *applicationmeta.ConstructorConfigurationSchemaError
					if !errors.Is(err, applicationmeta.ErrConfigurationSchema) || !errors.As(err, &detail) {
						t.Fatalf("provider-only constructor supplied an ordinary Config schema: %v", err)
					}
					if detail.Constructor().String() != resourceProvider || detail.ModulePath() != module || detail.SourcePath() != source || detail.Line() != 1 || detail.Column() != 1 {
						t.Fatalf("schema error lost declaration ownership: %v", err)
					}
					if strings.Contains(err.Error(), "PRIVATE_") {
						t.Fatal("schema error exposed a private value or Secret reference")
					}
				}
				_, err = applicationmeta.ConfigurationDecisions(manifest, lookup)
				assertSchemaError(err)
				switch mode {
				case "environment":
					_, err = applicationmeta.ApplyOverlay(composeManifest(t, "{}"), manifest, lookup)
				case "removed lower":
					removal := resourceOverlayManifest(t, "plystra.production.yaml", "config: {"+resourceProvider+": {$remove: true}}")
					_, err = applicationmeta.ApplyOverlay(manifest, removal, lookup)
				default:
					_, err = applicationmeta.Compose(nil, manifest, lookup)
				}
				assertSchemaError(err)
				if !bytes.Equal(data, original) {
					t.Fatal("namespace validation changed authored bytes")
				}
			})
		}
	}
}

func TestNamespaceLookupPreservesDormantConfigurationAndResourceOverlay(t *testing.T) {
	t.Parallel()
	implementation := composeSchemaLookup(map[string]implementationinventory.Configuration{
		constructorConfigurationSymbol: composeSchema(t, "Value string `plystra:\"required\"`"),
	})
	resources := resourceLookup(t, "Value string `plystra:\"required\"`; Nested struct { Left string; Right string }")
	lookup := func(namespace applicationmeta.ConfigurationNamespace, symbol constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		if namespace == applicationmeta.ConfigurationNamespaceImplementation {
			return implementation(namespace, symbol)
		}
		return resources(namespace, symbol)
	}
	root := resourceManifest(t, "plystra.yaml", "interfaces: {use: {mail.send/v1: "+constructorConfigurationSymbol+"}}\nconfig: {"+constructorConfigurationSymbol+": {value: PRIVATE_DORMANT}}\n"+resourceDocument("{use: "+resourceProvider+", config: {value: PRIVATE_RESOURCE}}"))
	overlay := resourceOverlayManifest(t, "plystra.production.yaml", resourceDocument("{config: {nested: {right: selected}}}"))
	selected, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
	if err != nil {
		t.Fatal(err)
	}
	composition, err := applicationmeta.Compose(nil, selected, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if err := composition.ValidateRequiredConfiguration(lookup, nil, "plystra.production.yaml"); err != nil {
		t.Fatal(err)
	}
	if len(composition.Manifest().InterfaceRequirements()) != 0 || len(composition.Manifest().Configurations()) != 1 {
		t.Fatal("dormant owned Implementation configuration was discarded or activated")
	}
	instance := resourceConfig(t, composition.Manifest(), "database.primary")
	assertResourceYAML(t, instance, "{value: PRIVATE_RESOURCE, nested: {right: selected}}")
	if instance.ProviderDeclarationSource().ModulePath() != "example.com/current" {
		t.Fatal("current-project provider selection provenance was lost")
	}
	for _, layer := range composition.CurrentLayers() {
		if _, err := applicationmeta.ConfigurationDecisions(layer, lookup); err != nil {
			t.Fatal(err)
		}
	}
	removal := resourceOverlayManifest(t, "plystra.production.yaml", "config: {"+constructorConfigurationSymbol+": {$remove: true}}\n"+resourceDocument("{config: {value: {$remove: true}}}"))
	selected, err = applicationmeta.ApplyOverlay(root, removal, lookup)
	if err != nil {
		t.Fatal(err)
	}
	composition, err = applicationmeta.Compose(nil, selected, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if len(composition.Manifest().Configurations()) != 0 {
		t.Fatal("valid ordinary constructor tombstone was not applied")
	}
	var missing *applicationmeta.ResourceConfigurationError
	if err := composition.ValidateRequiredConfiguration(lookup, nil, "plystra.production.yaml"); !errors.Is(err, applicationmeta.ErrConfigurationRequired) || !errors.As(err, &missing) || missing.InstanceName() != "database.primary" {
		t.Fatalf("selected Resource requiredness used the wrong namespace: %v", err)
	}
}

func TestExcludedConfigurationDigestDoesNotBorrowOtherNamespaceVisibility(t *testing.T) {
	t.Parallel()
	lookup := resourceLookup(t, "Value string `plystra:\"build-visible\"`")
	var previous string
	for _, value := range []string{"PRIVATE_FIRST", "PRIVATE_SECOND"} {
		manifest := resourceManifest(t, "plystra.yaml", "config: {"+resourceProvider+": {value: "+value+"}}")
		digest, err := applicationmeta.ConfigurationLayerDigest(manifest, lookup)
		if err != nil {
			t.Fatal(err)
		}
		if previous != "" && digest != previous {
			t.Fatal("provider-only build visibility fingerprinted excluded ordinary configuration")
		}
		previous = digest
	}
}
