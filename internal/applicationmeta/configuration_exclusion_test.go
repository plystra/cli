package applicationmeta_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestComposeWholeOwnerExclusionWithoutSchema(t *testing.T) {
	t.Parallel()
	const owner = "example.com/obsolete.New"
	lookup := composeSchemaLookup(nil)
	overlay := parseOverlayManifest(t, "plystra.test.yaml", "config: {"+owner+": {$remove: true}}\n")
	var first applicationmeta.Composition
	for _, value := range []string{"{password: {env: PRIVATE_OLD}, endpoint: PRIVATE_ENDPOINT}", "{unrelated: [PRIVATE_OTHER]}"} {
		root := composeManifest(t, "config: {"+owner+": "+value+"}\n")
		layered, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
		if err != nil {
			t.Fatal(err)
		}
		composition, err := applicationmeta.Compose(nil, layered, lookup)
		if err != nil || len(composition.Manifest().Configurations()) != 0 {
			t.Fatalf("whole-owner suppression: %v", err)
		}
		layers := composition.CurrentLayers()
		configured, exists := layers[0].Configuration(mustConstructorSymbol(t, owner))
		if len(layers) != 2 || !exists || !bytes.Contains(configured.YAML(), []byte("PRIVATE_")) {
			t.Fatal("composition lost the original authored lower layer")
		}
		if _, err := applicationmeta.ConfigurationDecisions(layers[0], lookup); !errors.Is(err, applicationmeta.ErrConfigurationSchema) {
			t.Fatalf("isolated unknown owner became valid without its exclusion: %v", err)
		}
		decisions, err := composition.CurrentLayerDecisions(lookup)
		if err != nil {
			t.Fatal(err)
		}
		var configDecisions []applicationmeta.ConfigurationDecision
		for _, layer := range decisions {
			for _, decision := range layer {
				if strings.HasPrefix(decision.Path(), "config[") {
					configDecisions = append(configDecisions, decision)
				}
			}
		}
		path := `config["` + owner + `"]`
		if len(configDecisions) != 2 || configDecisions[0].Path() != path || configDecisions[0].Summary() != applicationmeta.ConfigurationSummaryObject || configDecisions[0].Removed() || configDecisions[1].Path() != path || !configDecisions[1].Removed() {
			t.Fatal("excluded lower object did not retain value-free owner provenance")
		}
		if first.Valid() && (first.CompositionDigest() != composition.CompositionDigest() || !reflect.DeepEqual(first.Provenance(), composition.Provenance())) {
			t.Fatal("excluded private fields affected public composition identity")
		}
		first = composition
	}
}

func TestComposeWholeOwnerExclusionPreservesValidation(t *testing.T) {
	t.Parallel()
	const owner = "example.com/obsolete.New"
	known := composeSchemaLookup(map[string]implementationinventory.Configuration{owner: composeSchema(t, "Name string\nPassword configuration.Secret")})
	for _, test := range []struct {
		name, lower, upper string
		lookup             applicationmeta.SchemaLookup
		want               error
	}{
		{"unmasked unknown", "{name: PRIVATE_OLD}", "{}", composeSchemaLookup(nil), applicationmeta.ErrConfigurationSchema},
		{"unrelated exclusion", "{name: PRIVATE_OLD}", "config: {example.com/other.New: {$remove: true}}", composeSchemaLookup(nil), applicationmeta.ErrConfigurationSchema},
		{"nested exclusion", "{name: PRIVATE_OLD}", "config: {" + owner + ": {name: {$remove: true}}}", composeSchemaLookup(nil), applicationmeta.ErrConfigurationSchema},
		{"known invalid type", "{name: [PRIVATE_INVALID]}", "config: {" + owner + ": {$remove: true}}", known, applicationmeta.ErrConfigurationInvalidValue},
		{"known invalid secret", "{password: {env: PRIVATE_ENV, file: PRIVATE_FILE}}", "config: {" + owner + ": {$remove: true}}", known, applicationmeta.ErrConfigurationInvalidValue},
		{"known unknown field", "{undeclared: PRIVATE_INVALID}", "config: {" + owner + ": {$remove: true}}", known, applicationmeta.ErrConfigurationUnknownField},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := composeManifest(t, "config: {"+owner+": "+test.lower+"}\n")
			overlay := parseOverlayManifest(t, "plystra.test.yaml", test.upper+"\n")
			_, err := applicationmeta.ApplyOverlay(root, overlay, test.lookup)
			if !errors.Is(err, test.want) || strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatalf("lower-layer validation changed or disclosed values: %v", err)
			}
		})
	}
	for _, value := range []string{"{name: one, name: two}", "{name: &private PRIVATE_VALUE}", "[PRIVATE_VALUE]"} {
		root, err := applicationmeta.Parse([]byte("config: {" + owner + ": " + value + "}\n"))
		if err == nil {
			overlay := parseOverlayManifest(t, "plystra.test.yaml", "config: {"+owner+": {$remove: true}}\n")
			_, err = applicationmeta.ApplyOverlay(root, overlay, composeSchemaLookup(nil))
		}
		if err == nil {
			t.Fatal("whole-owner exclusion accepted malformed lower Config")
		}
	}
}
