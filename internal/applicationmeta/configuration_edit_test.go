package applicationmeta_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestEditConfigurationValuePreservesExactTypedBoundary(t *testing.T) {
	t.Parallel()
	schema := composeSchema(t, "Value string\nNested struct { Count uint64; Keep string }\n")
	for _, resource := range []bool{false, true} {
		for _, overlay := range []bool{false, true} {
			namespace, owner := applicationmeta.ConfigurationNamespaceImplementation, constructorConfigurationSymbol
			path := []string{"config", owner, "nested"}
			document := "config:\n  " + owner + ":\n    value: PRIVATE_KEEP # keep value\n    nested: {count: 7, keep: PRIVATE_NESTED}\n"
			if resource {
				namespace, owner = applicationmeta.ConfigurationNamespaceResource, "database.primary"
				path = []string{"resources", "instances", owner, "config", "nested"}
				document = "resources:\n  instances:\n    " + owner + ":\n      use: " + constructorConfigurationSymbol + "\n      config:\n        value: PRIVATE_KEEP # keep value\n        nested: {count: 7, keep: PRIVATE_NESTED}\n"
			}
			input := []byte("# keep document\n" + document)
			before := bytes.Clone(input)
			for _, value := range [][]byte{[]byte("18446744073709551615"), nil} {
				updated, changed, err := applicationmeta.EditConfigurationValue(input, namespace, owner, "nested.count", schema, value, overlay)
				if err != nil || !changed || !bytes.Equal(input, before) {
					t.Fatalf("edit = %t, %v", changed, err)
				}
				want := selectionYAML(t, input)
				parent := selectionMap(t, want, path...)
				if value == nil {
					delete(parent, "count")
					if overlay {
						parent["count"] = map[string]any{"$remove": true}
					}
				} else {
					parent["count"] = uint64(18446744073709551615)
				}
				if !reflect.DeepEqual(selectionYAML(t, updated), want) {
					t.Fatal("edit changed a sibling or lost integer precision")
				}
				for _, comment := range []string{"# keep document", "# keep value"} {
					if !bytes.Contains(updated, []byte(comment)) {
						t.Fatalf("lost comment %s", comment)
					}
				}
				again, changed, err := applicationmeta.EditConfigurationValue(updated, namespace, owner, "nested.count", schema, value, overlay)
				if err != nil || value == nil && changed || !bytes.Equal(updated, again) {
					t.Fatalf("repeated edit changed bytes: %t, %v", changed, err)
				}
				again[0] = '!'
				if bytes.Equal(updated, again) {
					t.Fatal("returned aliased bytes")
				}
			}
		}
	}
}

func TestEditConfigurationValueRejectsUnsafeTraversalAndValuesPrivately(t *testing.T) {
	t.Parallel()
	schema := composeSchema(t, "Value string\nNested struct { Count int8 }\nMapping map[string]string\nPointer *struct { Value string }\n")
	for _, test := range []struct{ document, path, value string }{
		{"{}", "PRIVATE_UNKNOWN", `"PRIVATE_VALUE"`},
		{"{}", "mapping.key", `"PRIVATE_VALUE"`},
		{"{}", "pointer.value", `"PRIVATE_VALUE"`},
		{"{}", "nested.count", "128"},
		{"{}", "value", "null"},
		{"{}", "value", `{"$remove":true}`},
		{"{}", "nested", `{"PRIVATE_UNKNOWN":true}`},
		{"{}", "value", "PRIVATE_INVALID_JSON"},
		{"config: {" + constructorConfigurationSymbol + ": {nested: [PRIVATE_VALUE]}}", "nested.count", "1"},
		{"config: {" + constructorConfigurationSymbol + ": {$remove: true}}", "value", `"PRIVATE_VALUE"`},
	} {
		updated, changed, err := applicationmeta.EditConfigurationValue([]byte(test.document), applicationmeta.ConfigurationNamespaceImplementation, constructorConfigurationSymbol, test.path, schema, []byte(test.value), true)
		if !errors.Is(err, applicationmeta.ErrEditConfiguration) || strings.Contains(err.Error(), "PRIVATE_") || changed || updated != nil {
			t.Fatalf("unsafe edit accepted or leaked: %t, %v", changed, err)
		}
	}
}

func TestEditConfigurationValueRemovalRetainsAncestorExclusions(t *testing.T) {
	t.Parallel()
	schema := composeSchema(t, "Nested struct { Count int8 }\n")
	for _, declaration := range []string{"{$remove: true}", "{nested: {$remove: true}}"} {
		input := []byte("config: {" + constructorConfigurationSymbol + ": " + declaration + "}\n")
		updated, changed, err := applicationmeta.EditConfigurationValue(input, applicationmeta.ConfigurationNamespaceImplementation, constructorConfigurationSymbol, "nested.count", schema, nil, true)
		if err != nil || changed || !bytes.Equal(input, updated) {
			t.Fatalf("ancestor exclusion was widened: %t, %v", changed, err)
		}
	}
}
