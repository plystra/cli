package applicationmeta_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestRemoveResourceInstancePreservesExactLayerBoundary(t *testing.T) {
	t.Parallel()
	for _, overlay := range []bool{false, true} {
		remove := applicationmeta.RemoveResourceInstance
		if overlay {
			remove = applicationmeta.RemoveResourceInstanceOverlay
		}
		for _, exists := range []bool{false, true} {
			input := []byte(`# keep document
resources:
  instances:
    retained: {use: example.com/provider.New, config: {value: PRIVATE_RETAINED}} # keep instance
  bind:
    instances: {primary: {db: retained}, retained: {db: primary}} # caller owns cleanup
http: {address: ":9080"} # keep address
`)
			if exists {
				input = bytes.Replace(input, []byte("  instances:\n"), []byte("  instances:\n    primary: {use: example.com/provider.New, config: {value: [PRIVATE_INVALID]}}\n"), 1)
			}
			before := bytes.Clone(input)
			updated, changed, err := remove(input, "primary")
			if err != nil || changed != (exists || overlay) {
				t.Fatalf("remove = %t, %v", changed, err)
			}
			want := selectionYAML(t, input)
			instances := selectionMap(t, want, "resources", "instances")
			delete(instances, "primary")
			if overlay {
				instances["primary"] = map[string]any{"$remove": true}
			}
			if !reflect.DeepEqual(selectionYAML(t, updated), want) || !bytes.Equal(input, before) {
				t.Fatalf("removed more than one instance: %s", updated)
			}
			for _, comment := range []string{"# keep document", "# keep instance", "# caller owns cleanup", "# keep address"} {
				if !bytes.Contains(updated, []byte(comment)) {
					t.Fatalf("lost comment %s", comment)
				}
			}
			again, changed, err := remove(updated, "primary")
			if err != nil || changed || !bytes.Equal(updated, again) {
				t.Fatalf("not byte-idempotent: %t, %v", changed, err)
			}
			again[0] = '!'
			if bytes.Equal(updated, again) || !bytes.Equal(input, before) {
				t.Fatal("returned aliased bytes")
			}
		}
	}
}

func TestRemoveResourceInstanceRejectsInvalidInputPrivately(t *testing.T) {
	t.Parallel()
	for _, remove := range []func([]byte, string) ([]byte, bool, error){applicationmeta.RemoveResourceInstance, applicationmeta.RemoveResourceInstanceOverlay} {
		for _, test := range []struct{ document, name string }{
			{"{}", "PRIVATE_INPUT!"},
			{"{}", ""},
			{"PRIVATE_INPUT: true", "primary"},
			{"resources: {instances: {primary: {use: PRIVATE_INPUT}}}", "primary"},
			{"resources: {instances: {primary: {}, primary: {}}}", "primary"},
		} {
			updated, changed, err := remove([]byte(test.document), test.name)
			if !errors.Is(err, applicationmeta.ErrRemoveResourceInstance) || updated != nil || changed || strings.Contains(err.Error(), "PRIVATE_INPUT") {
				t.Fatalf("unsafe removal accepted or leaked: %s, %t, %v", updated, changed, err)
			}
		}
	}
}
