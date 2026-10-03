package applicationmeta_test

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/implementationinventory"
)

func TestConstructorEntriesRequireExactRemovalMappings(t *testing.T) {
	t.Parallel()
	for _, value := range []string{`{$remove: true}`, `{}`, `null`, `~`, `[]`, `false`, `{$remove: false}`, `{$remove: "true"}`, `{$remove: 1}`, `{$remove: null}`, `{$remove: true, endpoint: private}`, `{$remove: true, $remove: true}`} {
		t.Run(value, func(t *testing.T) {
			for _, source := range []string{"plystra.yaml", "plystra.production.yaml", "deploy/customer.yaml"} {
				data := []byte(fmt.Sprintf("config: {%s: %s}\n", constructorConfigurationSymbol, value))
				manifest, err := applicationmeta.ParseSource(source, data)
				if value != `{$remove: true}` && value != `{}` {
					if !errors.Is(err, applicationmeta.ErrInvalidManifest) {
						t.Fatalf("%s accepted invalid entry %s: %v", source, value, err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				want := 1
				if value == `{$remove: true}` {
					want = 0
				}
				if len(manifest.Configurations()) != want {
					t.Fatalf("entry %s became %d configuration values, want %d", value, len(manifest.Configurations()), want)
				}
			}
		})
	}
}

func TestConstructorEntryTombstoneSurvivesAbsentAndChangedLowerValues(t *testing.T) {
	t.Parallel()
	lookup := composeSchemaLookup(map[string]implementationinventory.Configuration{
		constructorConfigurationSymbol: composeSchema(t, "\tEndpoint string\n"),
	})
	data := []byte("config: {" + constructorConfigurationSymbol + ": {$remove: true}}\n")
	current := composeManifest(t, string(data))
	before, err := applicationmeta.Compose(nil, current, lookup)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"first.internal", "changed.internal"} {
		dependencies := []applicationmeta.Dependency{{ModulePath: "example.com/platform", Manifest: composeManifest(t, "config: {"+constructorConfigurationSymbol+": {endpoint: "+value+"}}\n")}}
		maintained, err := applicationmeta.MaintainDependencyConfiguration(data, before.DependencyBaseline(), nil, dependencies, lookup)
		if err != nil || maintained.Changed() || !bytes.Equal(data, maintained.Data()) {
			t.Fatalf("maintenance changed authored exclusion: %v\n%s", err, maintained.Data())
		}
		selected, err := applicationmeta.ApplyOverlay(composeManifest(t, "{}"), current, lookup)
		if err != nil {
			t.Fatal(err)
		}
		composed, err := applicationmeta.Compose(dependencies, selected, lookup)
		if err != nil || len(composed.Manifest().Configurations()) != 0 {
			t.Fatalf("excluded constructor configuration became effective: %v, %v", composed.Manifest().Configurations(), err)
		}
		decisions, err := applicationmeta.ConfigurationDecisions(selected, lookup)
		if err != nil || len(decisions) != 1 || !decisions[0].Removed() || decisions[0].Path() != `config["example.com/acme/smtp.New"]` {
			t.Fatalf("retained exclusion evidence = %v, %v", decisions, err)
		}
	}
}
