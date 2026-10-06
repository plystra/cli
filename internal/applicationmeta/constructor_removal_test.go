package applicationmeta_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestConstructorEntriesRequireExactRemovalMappings(t *testing.T) {
	t.Parallel()
	for _, value := range []string{`{$remove: true}`, `{}`, `null`, `~`, `[]`, `false`, `{$remove: false}`, `{$remove: "true"}`, `{$remove: 1}`, `{$remove: null}`, `{$remove: true, endpoint: private}`, `{$remove: true, $remove: true}`} {
		t.Run(value, func(t *testing.T) {
			for _, source := range []string{"plystra.yaml", "plystra.production.yaml", "deploy/customer.yaml"} {
				data := []byte(fmt.Sprintf("config: {%s: %s}\n", constructorConfigurationSymbol, value))
				parse := applicationmeta.ParseCompleteSource
				if source == "plystra.production.yaml" {
					parse = applicationmeta.ParseOverlaySource
				}
				manifest, err := parse(source, data)
				validRemoval := value == `{$remove: true}` && source == "plystra.production.yaml"
				if !validRemoval && value != `{}` {
					if !errors.Is(err, applicationmeta.ErrInvalidManifest) {
						t.Fatalf("%s accepted invalid entry %s: %v", source, value, err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				want := 1
				if validRemoval {
					want = 0
				}
				if len(manifest.Configurations()) != want {
					t.Fatalf("entry %s became %d configuration values, want %d", value, len(manifest.Configurations()), want)
				}
			}
		})
	}
}
