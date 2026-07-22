package interfacemeta_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacemeta"
)

func TestResolveDeprecationNormalizesLifecycleDocumentation(t *testing.T) {
	t.Parallel()

	data := []byte(`deprecation:
  message: Use order.create/v2.
  replacement: order.create/v2
  since: v0.0.1-rc.2
`)
	document, err := interfacemeta.ParseFile("interfaces/constraints/interface.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	parsed, present := document.Deprecation()
	if !present || parsed.Message() != "Use order.create/v2." {
		t.Fatalf("parsed deprecation = %#v, %t", deprecationSummary(parsed), present)
	}
	contract := constraintTestContract(t, canonicalConstraintInterfaceSource)
	resolved, present, err := interfacemeta.ResolveDeprecation(document, contract, map[string]struct{}{"order.create/v2": {}, "order.create/v1": {}})
	if err != nil || !present || !reflect.DeepEqual(deprecationSummary(resolved), deprecationSummary(parsed)) {
		t.Fatalf("ResolveDeprecation = %#v, %t, %v", deprecationSummary(resolved), present, err)
	}
	replacement, hasReplacement := resolved.Replacement()
	since, hasSince := resolved.Since()
	if !hasReplacement || replacement.String() != "order.create/v2" || !hasSince || since != "v0.0.1-rc.2" {
		t.Fatalf("resolved fields = replacement %q/%t since %q/%t", replacement.String(), hasReplacement, since, hasSince)
	}
}

func TestResolveDeprecationAcceptsNoReplacementAndAbsence(t *testing.T) {
	t.Parallel()

	contract := constraintTestContract(t, canonicalConstraintInterfaceSource)
	document, err := interfacemeta.ParseFile("interfaces/constraints/interface.yaml", []byte("deprecation: {message: This Interface is obsolete.}\n"))
	if err != nil {
		t.Fatal(err)
	}
	deprecation, present, err := interfacemeta.ResolveDeprecation(document, contract, nil)
	if err != nil || !present || deprecation.Message() != "This Interface is obsolete." {
		t.Fatalf("message-only ResolveDeprecation = %#v, %t, %v", deprecationSummary(deprecation), present, err)
	}
	if replacement, exists := deprecation.Replacement(); exists || replacement.String() != "" {
		t.Fatalf("unexpected replacement = %q, %t", replacement.String(), exists)
	}
	if since, exists := deprecation.Since(); exists || since != "" {
		t.Fatalf("unexpected since = %q, %t", since, exists)
	}

	absent, err := interfacemeta.ParseFile("interfaces/constraints/interface.yaml", []byte("{}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if parsed, exists := absent.Deprecation(); exists || parsed.Message() != "" {
		t.Fatalf("absent parsed deprecation = %#v, %t", parsed, exists)
	}
	if resolved, exists, err := interfacemeta.ResolveDeprecation(absent, interfacecontract.Contract{}, nil); err != nil || exists || resolved.Message() != "" {
		t.Fatalf("absent ResolveDeprecation = %#v, %t, %v", resolved, exists, err)
	}
}

func TestParseFileRejectsInvalidDeprecationSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		data     string
		location string
		want     string
	}{
		{name: "null", data: "deprecation: null\n", location: "interface.yaml:1:14", want: "must be a mapping"},
		{name: "scalar", data: "deprecation: obsolete\n", location: "interface.yaml:1:14", want: "must be a mapping"},
		{name: "sequence", data: "deprecation: []\n", location: "interface.yaml:1:14", want: "must be a mapping"},
		{name: "missing message", data: "deprecation: {}\n", location: "interface.yaml:1:14", want: "deprecation.message is missing"},
		{name: "unknown field", data: "deprecation:\n  message: obsolete\n  remove_after: v1\n", location: "interface.yaml:3:3", want: "deprecation.remove_after"},
		{name: "null message", data: "deprecation:\n  message: null\n", location: "interface.yaml:2:12", want: "message must be a string"},
		{name: "empty message", data: "deprecation:\n  message: \"\"\n", location: "interface.yaml:2:12", want: "message must not be empty"},
		{name: "whitespace message", data: "deprecation:\n  message: \"  \"\n", location: "interface.yaml:2:12", want: "message must not be empty"},
		{name: "oversized message", data: "deprecation:\n  message: " + strings.Repeat("x", interfacemeta.MaximumDeprecationMessageLength+1) + "\n", location: "interface.yaml:2:12", want: "at most 1024 UTF-8 bytes"},
		{name: "NUL message", data: "deprecation:\n  message: \"bad\\0message\"\n", location: "interface.yaml:2:12", want: "without NUL"},
		{name: "boolean replacement", data: "deprecation:\n  message: obsolete\n  replacement: true\n", location: "interface.yaml:3:16", want: "replacement must be a canonical Interface ID string"},
		{name: "invalid replacement", data: "deprecation:\n  message: obsolete\n  replacement: invalid\n", location: "interface.yaml:3:16", want: "is not a canonical Interface ID"},
		{name: "numeric since", data: "deprecation:\n  message: obsolete\n  since: 1\n", location: "interface.yaml:3:10", want: "since must be a string"},
		{name: "empty since", data: "deprecation:\n  message: obsolete\n  since: \"\"\n", location: "interface.yaml:3:10", want: "since must not be empty"},
		{name: "oversized since", data: "deprecation:\n  message: obsolete\n  since: " + strings.Repeat("x", interfacemeta.MaximumDeprecationSinceLength+1) + "\n", location: "interface.yaml:3:10", want: "at most 128 UTF-8 bytes"},
		{name: "NUL since", data: "deprecation:\n  message: obsolete\n  since: \"bad\\0label\"\n", location: "interface.yaml:3:10", want: "without NUL"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document, err := interfacemeta.ParseFile("interfaces/deprecated/interface.yaml", []byte(test.data))
			if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrInvalidDeprecation) || document.Path() != "" || !strings.Contains(err.Error(), test.location) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseFile = %#v, %v; want %q at %s", document, err, test.want, test.location)
			}
		})
	}
}

func TestResolveDeprecationRejectsSelfAndInvisibleReplacements(t *testing.T) {
	t.Parallel()

	contract := constraintTestContract(t, canonicalConstraintInterfaceSource)
	tests := []struct {
		name        string
		replacement string
		visible     map[string]struct{}
		want        string
	}{
		{name: "self", replacement: "order.create/v1", visible: map[string]struct{}{"order.create/v1": {}}, want: "must differ from the deprecated Interface"},
		{name: "invisible", replacement: "order.create/v2", visible: map[string]struct{}{"order.create/v1": {}}, want: "is not a visible Interface"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			data := []byte("deprecation:\n  message: obsolete\n  replacement: " + test.replacement + "\n")
			document, err := interfacemeta.ParseFile("interfaces/constraints/interface.yaml", data)
			if err != nil {
				t.Fatal(err)
			}
			deprecation, present, err := interfacemeta.ResolveDeprecation(document, contract, test.visible)
			if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrInvalidDeprecation) || present || deprecation.Message() != "" || !strings.Contains(err.Error(), "interfaces/constraints/interface.yaml:3:16") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ResolveDeprecation = %#v, %t, %v", deprecation, present, err)
			}
		})
	}
}

func TestDeprecationNormalizationIsDeterministic(t *testing.T) {
	t.Parallel()

	first := parseDeprecation(t, "deprecation:\n  message: Use order.create/v2.\n  replacement: order.create/v2\n  since: next-release\n")
	second := parseDeprecation(t, "deprecation: {since: next-release, replacement: order.create/v2, message: 'Use order.create/v2.'}\n")
	if !reflect.DeepEqual(deprecationSummary(first), deprecationSummary(second)) {
		t.Fatalf("equivalent deprecations differ: %#v and %#v", first, second)
	}
}

func parseDeprecation(t testing.TB, data string) interfacemeta.Deprecation {
	t.Helper()
	document, err := interfacemeta.ParseFile("interfaces/constraints/interface.yaml", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	deprecation, present := document.Deprecation()
	if !present {
		t.Fatal("deprecation is absent")
	}
	return deprecation
}

func deprecationSummary(deprecation interfacemeta.Deprecation) []any {
	replacement, hasReplacement := deprecation.Replacement()
	since, hasSince := deprecation.Since()
	return []any{deprecation.Message(), replacement.String(), hasReplacement, since, hasSince}
}
