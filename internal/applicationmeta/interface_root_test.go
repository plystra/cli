package applicationmeta_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/interfaceid"
)

func TestSetInterfaceRootPreservesLayerBoundaries(t *testing.T) {
	t.Parallel()
	id := mustInterfaceRootID(t)
	for _, test := range []struct {
		name, input, want string
		overlay, present  bool
	}{
		{"complete add", "interfaces: {require: [z.run/v1]}", "interfaces: {require: [app.run/v1, z.run/v1]}", false, true},
		{"complete remove last", "interfaces: {require: [app.run/v1]}", "interfaces: {require: []}", false, false},
		{"complete missing", "{}", "{}", false, false},
		{"complete create", "{}", "interfaces: {require: [app.run/v1]}", false, true},
		{"overlay complete add", "interfaces: {require: []}", "interfaces: {require: [app.run/v1]}", true, true},
		{"overlay complete remove last", "interfaces: {require: [app.run/v1]}", "interfaces: {require: []}", true, false},
		{"overlay sparse create", "{}", "interfaces: {require: {add: [app.run/v1]}}", true, true},
		{"overlay exclusion without lower", "{}", "interfaces: {require: {remove: [app.run/v1]}}", true, false},
		{"overlay sparse add", "interfaces: {require: {add: [z.run/v1], remove: [app.run/v1, other.run/v1]}}", "interfaces: {require: {add: [app.run/v1, z.run/v1], remove: [other.run/v1]}}", true, true},
		{"overlay sparse remove", "interfaces: {require: {add: [app.run/v1, z.run/v1], remove: [other.run/v1]}}", "interfaces: {require: {add: [z.run/v1], remove: [app.run/v1, other.run/v1]}}", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			edit := applicationmeta.SetInterfaceRoot
			if test.overlay {
				edit = applicationmeta.SetInterfaceRootOverlay
			}
			input := []byte(test.input + "\n")
			before := bytes.Clone(input)
			updated, changed, err := edit(input, id, "require", test.present)
			wantChanged := test.input != test.want
			if err != nil || changed != wantChanged || !reflect.DeepEqual(selectionYAML(t, updated), selectionYAML(t, []byte(test.want))) {
				t.Fatalf("root edit = %s, changed %t, err %v", updated, changed, err)
			}
			if !bytes.Equal(input, before) {
				t.Fatal("editor changed caller bytes")
			}
			again, changed, err := edit(updated, id, "require", test.present)
			if err != nil || changed || !bytes.Equal(updated, again) {
				t.Fatalf("not byte-idempotent: %s, %t, %v", again, changed, err)
			}
			again[0] = '!'
			if !bytes.Equal(input, before) || bytes.Equal(updated, again) {
				t.Fatal("editor returned aliased bytes")
			}
		})
	}
}

func TestSetInterfaceRootExposureIsExactAndPreservesUnrelatedYAML(t *testing.T) {
	t.Parallel()
	id := mustInterfaceRootID(t)
	for _, overlay := range []bool{false, true} {
		edit := applicationmeta.SetInterfaceRoot
		if overlay {
			edit = applicationmeta.SetInterfaceRootOverlay
		}
		input := []byte(`# keep document
interfaces: {require: [other.run/v1]} # keep requirement
http:
  address: ":9080" # keep address
  expose: {other.run/v1: {transport: connect}} # keep other exposure
config: {example.com/service.New: {value: PRIVATE_VALUE}} # keep config
`)
		updated, changed, err := edit(input, id, "expose", true)
		if err != nil || !changed {
			t.Fatalf("add exposure: %t, %v", changed, err)
		}
		want := selectionYAML(t, input)
		selectionMap(t, want, "http", "expose")[id.String()] = map[string]any{"transport": "connect"}
		if !reflect.DeepEqual(selectionYAML(t, updated), want) {
			t.Fatalf("unexpected exposure edit: %s", updated)
		}
		for _, comment := range []string{"# keep document", "# keep requirement", "# keep address", "# keep other exposure", "# keep config"} {
			if !bytes.Contains(updated, []byte(comment)) {
				t.Fatalf("lost %s", comment)
			}
		}
		again, changed, err := edit(updated, id, "expose", true)
		if err != nil || changed || !bytes.Equal(updated, again) {
			t.Fatalf("present exposure changed: %t, %v", changed, err)
		}
		removed, changed, err := edit(updated, id, "expose", false)
		if err != nil || !changed {
			t.Fatalf("remove exposure: %t, %v", changed, err)
		}
		if overlay {
			selectionMap(t, want, "http", "expose")[id.String()] = map[string]any{"$remove": true}
		} else {
			delete(selectionMap(t, want, "http", "expose"), id.String())
		}
		if !reflect.DeepEqual(selectionYAML(t, removed), want) {
			t.Fatalf("exposure removal changed other paths: %s", removed)
		}
		again, changed, err = edit(removed, id, "expose", false)
		if err != nil || changed || !bytes.Equal(removed, again) {
			t.Fatalf("absent exposure changed: %t, %v", changed, err)
		}
		restored, changed, err := edit(removed, id, "expose", true)
		if err != nil || !changed || !reflect.DeepEqual(selectionYAML(t, restored), selectionYAML(t, updated)) {
			t.Fatalf("cannot replace exclusion: %t, %v", changed, err)
		}
	}
}

func TestSetInterfaceRootRejectsUnsafeInputPrivately(t *testing.T) {
	t.Parallel()
	for _, overlay := range []bool{false, true} {
		edit := applicationmeta.SetInterfaceRoot
		if overlay {
			edit = applicationmeta.SetInterfaceRootOverlay
		}
		for _, test := range []struct{ data, root string }{
			{"PRIVATE_INPUT: true", "require"},
			{"interfaces: {require: [app.run/v1, app.run/v1]}", "require"},
			{"interfaces: {require: {add: [app.run/v1], remove: [app.run/v1]}}", "require"},
			{"http: {expose: {app.run/v1: {transport: PRIVATE_INPUT}}}", "expose"},
			{"{}", "PRIVATE_INPUT"},
		} {
			updated, changed, err := edit([]byte(test.data), mustInterfaceRootID(t), test.root, true)
			if !errors.Is(err, applicationmeta.ErrSetInterfaceRoot) || updated != nil || changed || strings.Contains(err.Error(), "PRIVATE_INPUT") {
				t.Fatalf("unsafe edit accepted or leaked: %s, %t, %v", updated, changed, err)
			}
		}
		if _, _, err := edit([]byte("{}"), interfaceid.Identifier{}, "require", true); !errors.Is(err, applicationmeta.ErrSetInterfaceRoot) {
			t.Fatalf("empty Interface accepted: %v", err)
		}
	}
}

func mustInterfaceRootID(t testing.TB) interfaceid.Identifier {
	t.Helper()
	id, err := interfaceid.Parse("app.run/v1")
	if err != nil {
		t.Fatal(err)
	}
	return id
}
