package diagnosticjson_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/diagnosticjson"
)

func TestCommandSchemasOwnIndependentVersions(t *testing.T) {
	t.Parallel()

	inspectV1 := newSchema(t, "plystra.inspect", 1)
	checkV1 := newSchema(t, "plystra.check", 1)
	checkV2 := newSchema(t, "plystra.check", 2)

	inspect := newEnvelope(t, inspectV1)
	firstCheck := newEnvelope(t, checkV1)
	secondCheck := newEnvelope(t, checkV2)
	inspectAgain := newEnvelope(t, inspectV1)

	if inspect.Schema().Name() != "plystra.inspect" || inspect.SchemaVersion() != 1 || checkV1.Name() != checkV2.Name() || firstCheck.SchemaVersion() != 1 || secondCheck.SchemaVersion() != 2 {
		t.Fatalf("schema identities or versions are inconsistent: inspect=%#v check-v1=%#v check-v2=%#v", inspect.Schema(), firstCheck.Schema(), secondCheck.Schema())
	}
	if !bytes.Equal(inspect.CanonicalJSON(), inspectAgain.CanonicalJSON()) || inspect.Digest() != inspectAgain.Digest() {
		t.Fatal("changing another command schema affected inspect v1")
	}
	if bytes.Equal(firstCheck.CanonicalJSON(), secondCheck.CanonicalJSON()) || firstCheck.Digest() == secondCheck.Digest() {
		t.Fatal("changing check's schema version did not change its envelope identity")
	}
	for _, unexpected := range []string{`"schema":"plystra.check"`, `"schema_version":2`} {
		if bytes.Contains(inspect.CanonicalJSON(), []byte(unexpected)) {
			t.Fatalf("inspect envelope inherited check schema state %s: %s", unexpected, inspect.CanonicalJSON())
		}
	}
}

func TestSchemaRejectsInvalidNamesAndVersions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		schema  string
		version uint32
		want    string
	}{
		{name: "empty", version: 1, want: "name"},
		{name: "foreign namespace", schema: "acme.inspect", version: 1, want: "name"},
		{name: "missing command", schema: "plystra.", version: 1, want: "name"},
		{name: "embedded version", schema: "plystra.inspect/v1", version: 1, want: "name"},
		{name: "uppercase", schema: "plystra.Inspect", version: 1, want: "name"},
		{name: "empty segment", schema: "plystra..inspect", version: 1, want: "name"},
		{name: "zero version", schema: "plystra.inspect", want: "version"},
		{name: "excessive version", schema: "plystra.inspect", version: 1 << 31, want: "version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			schema, err := diagnosticjson.NewSchema(test.schema, test.version)
			if !errors.Is(err, diagnosticjson.ErrInvalidSchema) || !strings.Contains(err.Error(), test.want) || schema.Valid() {
				t.Fatalf("NewSchema = %#v, %v; want ErrInvalidSchema containing %q", schema, err, test.want)
			}
		})
	}
	if (diagnosticjson.Schema{}).Valid() {
		t.Fatal("zero Schema is valid")
	}
}

func newSchema(t testing.TB, name string, version uint32) diagnosticjson.Schema {
	t.Helper()
	schema, err := diagnosticjson.NewSchema(name, version)
	if err != nil || !schema.Valid() {
		t.Fatalf("NewSchema(%q, %d) = %#v, %v", name, version, schema, err)
	}
	return schema
}

func newEnvelope(t testing.TB, schema diagnosticjson.Schema) diagnosticjson.Envelope {
	t.Helper()
	envelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 schema,
		ConfigurationMode:      generation.ConfigurationModeDefault,
		ApplicationModelDigest: "sha256:" + strings.Repeat("a", 64),
	})
	if err != nil || !envelope.Valid() {
		t.Fatalf("New(%s v%d) = %#v, %v", schema.Name(), schema.Version(), envelope, err)
	}
	return envelope
}
