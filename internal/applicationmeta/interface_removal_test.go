package applicationmeta_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
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
				for _, parse := range []func([]byte) (applicationmeta.Manifest, error){
					applicationmeta.Parse,
					func(data []byte) (applicationmeta.Manifest, error) {
						return applicationmeta.ParseOverlaySource("plystra.production.yaml", data)
					},
				} {
					manifest, err := parse(data)
					if value == `{$remove: true}` {
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

func TestInterfaceTombstonesSurviveAbsentAndChangedAdoptedValues(t *testing.T) {
	t.Parallel()
	data := []byte(`interfaces:
  use: {email.send/v1: {$remove: true}}
  policies: {email.send/v1: {$remove: true}}
http:
  expose: {email.send/v1: {$remove: true}}
`)
	lookup := composeSchemaLookup(nil)
	initial, err := applicationmeta.MaintainDependencyConfiguration(data, applicationmeta.DependencyBaseline{}, nil, nil, lookup)
	if err != nil || initial.Changed() || !bytes.Equal(initial.Data(), data) {
		t.Fatalf("absent lower values changed authored tombstones: %v\n%s", err, initial.Data())
	}
	current := composeManifest(t, string(data))
	before, err := applicationmeta.Compose(nil, current, lookup)
	if err != nil {
		t.Fatal(err)
	}
	for _, timeout := range []string{"1s", "5s"} {
		dependencies := []applicationmeta.Dependency{{
			ModulePath: "example.com/platform", ModuleVersion: "v1.0.0", ExportName: "defaults",
			Manifest: composeManifest(t, "interfaces: {use: {email.send/v1: example.com/platform.New}, policies: {email.send/v1: {timeout: "+timeout+"}}}\n"),
		}}
		maintained, err := applicationmeta.MaintainDependencyConfiguration(data, before.DependencyBaseline(), initial.LocalPaths(), dependencies, lookup)
		if err != nil || maintained.Changed() || !bytes.Equal(maintained.Data(), data) {
			t.Fatalf("new lower values changed authored tombstones: %v\n%s", err, maintained.Data())
		}
		composed, err := applicationmeta.Compose(dependencies, composeManifest(t, string(maintained.Data())), lookup)
		if err != nil {
			t.Fatal(err)
		}
		effective := composed.Manifest()
		if len(effective.ImplementationChoices()) != 0 || len(effective.InterfacePolicies()) != 0 || len(effective.HTTPExposures()) != 0 {
			t.Fatal("dependency update reintroduced a suppressed Interface entry")
		}
		for _, path := range []string{`interfaces.use["email.send/v1"]`, `interfaces.policies["email.send/v1"]`, `http.expose["email.send/v1"]`} {
			if !strings.Contains(strings.Join(maintained.LocalPaths(), "\n"), path) {
				t.Fatalf("local exclusion provenance omits %s: %v", path, maintained.LocalPaths())
			}
		}
	}
}
