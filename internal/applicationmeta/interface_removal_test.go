package applicationmeta_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestInterfaceEntriesRequireExactRemovalMappings(t *testing.T) {
	t.Parallel()
	for _, field := range []string{
		"interfaces: {use: {email.send/v1: %s}}",
		"interfaces: {policies: {email.send/v1: %s}}",
		"http: {expose: {email.send/v1: %s}}",
	} {
		for _, value := range []string{`{$remove: true}`, `null`, `~`, `{}`, `[]`, `false`, `{$remove: false}`, `{$remove: "true"}`, `{$remove: 1}`, `{$remove: null}`, `{$remove: true, timeout: 1s}`, `{$remove: true, $remove: true}`} {
			t.Run(fmt.Sprintf(field, value), func(t *testing.T) {
				data := []byte(fmt.Sprintf(field, value) + "\n")
				for index, parse := range []func([]byte) (applicationmeta.Manifest, error){
					applicationmeta.Parse,
					func(data []byte) (applicationmeta.Manifest, error) {
						return applicationmeta.ParseOverlaySource("plystra.production.yaml", data)
					},
				} {
					manifest, err := parse(data)
					validRemoval := value == `{$remove: true}` && index == 1
					if validRemoval {
						if err != nil || len(manifest.ImplementationChoices()) != 0 || len(manifest.InterfacePolicies()) != 0 || len(manifest.HTTPExposures()) != 0 {
							t.Fatalf("valid tombstone became an effective value: %v, %v", manifest, err)
						}
					} else if !errors.Is(err, applicationmeta.ErrInvalidManifest) {
						t.Fatalf("invalid removal %s accepted: %v", value, err)
					}
				}
			})
		}
	}
}
