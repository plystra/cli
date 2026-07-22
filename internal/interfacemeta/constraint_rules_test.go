package interfacemeta_test

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacemeta"
)

func TestResolveConstraintTargetsNormalizesClosedRuleSchema(t *testing.T) {
	t.Parallel()

	contract := constraintTestContract(t, typedConstraintInterfaceSource)
	document, err := interfacemeta.ParseFile("interfaces/rules/interface.yaml", []byte(`constraints:
  response.payload: {max_length: 2147483647, min_length: 0}
  request.name: {pattern: '^[a-z]+$', max_length: 64, min_length: 1}
  request.i32: {maximum: 2147483647, minimum: -2147483648}
  request.i64: {minimum: -9223372036854775808, maximum: 9223372036854775807}
  request.u32: {maximum: 4294967295, minimum: 0}
  request.u64: {minimum: 0, maximum: 18446744073709551615}
  request.f32: {maximum: 3.5, minimum: -0.0}
  request.f64: {minimum: 1e3, maximum: 1000.0}
  request.tags: {max_items: 8, min_items: 1}
  request.lookup: {min_items: 2, max_items: 9}
  request.enabled: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	targets, err := interfacemeta.ResolveConstraintTargets(document, contract)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 11 {
		t.Fatalf("targets = %#v", targets)
	}
	byPath := make(map[string]interfacemeta.ConstraintTarget, len(targets))
	for _, target := range targets {
		byPath[target.Path()] = target
	}

	name := byPath["request.name"].Rules()
	assertConstraintCount(t, name.MinLength, 1, true, "name min_length")
	assertConstraintCount(t, name.MaxLength, 64, true, "name max_length")
	if pattern, ok := name.Pattern(); !ok || pattern != "^[a-z]+$" {
		t.Fatalf("name pattern = %q, %t", pattern, ok)
	}
	payload := byPath["response.payload"].Rules()
	assertConstraintCount(t, payload.MinLength, 0, true, "payload min_length")
	assertConstraintCount(t, payload.MaxLength, interfacemeta.MaximumConstraintCount, true, "payload max_length")

	assertSignedBounds(t, byPath["request.i32"].Rules(), interfacecontract.TypeInt32, -2147483648, 2147483647)
	assertSignedBounds(t, byPath["request.i64"].Rules(), interfacecontract.TypeInt64, -9223372036854775808, 9223372036854775807)
	assertUnsignedBounds(t, byPath["request.u32"].Rules(), interfacecontract.TypeUint32, 0, 4294967295)
	assertUnsignedBounds(t, byPath["request.u64"].Rules(), interfacecontract.TypeUint64, 0, 18446744073709551615)
	assertFloatBounds(t, byPath["request.f32"].Rules(), interfacecontract.TypeFloat32, 0, "0", 3.5, "3.5")
	assertFloatBounds(t, byPath["request.f64"].Rules(), interfacecontract.TypeFloat64, 1000, "1000", 1000, "1000")

	tags := byPath["request.tags"].Rules()
	assertConstraintCount(t, tags.MinItems, 1, true, "tags min_items")
	assertConstraintCount(t, tags.MaxItems, 8, true, "tags max_items")
	lookup := byPath["request.lookup"].Rules()
	assertConstraintCount(t, lookup.MinItems, 2, true, "lookup min_items")
	assertConstraintCount(t, lookup.MaxItems, 9, true, "lookup max_items")
	if !byPath["request.enabled"].Rules().Empty() {
		t.Fatalf("empty rules = %#v", byPath["request.enabled"].Rules())
	}

	targets[0] = interfacemeta.ConstraintTarget{}
	again, err := interfacemeta.ResolveConstraintTargets(document, contract)
	if err != nil || len(again) != 11 || again[0].Path() == "" {
		t.Fatalf("constraint rules exposed mutable storage: %#v, %v", again, err)
	}
}

func TestResolveConstraintTargetsRejectsInvalidRules(t *testing.T) {
	t.Parallel()

	contract := constraintTestContract(t, typedConstraintInterfaceSource)
	tests := []struct {
		name     string
		path     string
		rules    string
		location string
		want     string
		also     string
	}{
		{name: "unknown rule", path: "request.name", rules: "    format: email\n", location: "interface.yaml:3:5", want: `unknown constraint rule "format"`},
		{name: "numeric rule on string", path: "request.name", rules: "    minimum: 1\n", location: "interface.yaml:3:5", want: `rule "minimum" is not supported for string`},
		{name: "pattern on bytes", path: "response.payload", rules: "    pattern: x\n", location: "interface.yaml:3:5", want: `rule "pattern" is not supported for bytes`},
		{name: "rule on boolean", path: "request.enabled", rules: "    min_items: 1\n", location: "interface.yaml:3:5", want: `rule "min_items" is not supported for boolean`},
		{name: "rule on message", path: "request.detail", rules: "    min_length: 1\n", location: "interface.yaml:3:5", want: `rule "min_length" is not supported for message:Detail`},
		{name: "rule on timestamp", path: "request.created_at", rules: "    minimum: 0\n", location: "interface.yaml:3:5", want: `rule "minimum" is not supported for timestamp`},
		{name: "rule on duration", path: "request.delay", rules: "    maximum: 1\n", location: "interface.yaml:3:5", want: `rule "maximum" is not supported for duration`},
		{name: "quoted count", path: "request.name", rules: "    min_length: '1'\n", location: "interface.yaml:3:17", want: "canonical integer from 0 through"},
		{name: "negative count", path: "request.name", rules: "    min_length: -1\n", location: "interface.yaml:3:17", want: "canonical integer from 0 through"},
		{name: "leading zero count", path: "request.name", rules: "    min_length: 01\n", location: "interface.yaml:3:17", want: "canonical integer from 0 through"},
		{name: "excessive count", path: "request.name", rules: "    max_length: 2147483648\n", location: "interface.yaml:3:17", want: "canonical integer from 0 through"},
		{name: "mapping count", path: "request.name", rules: "    min_length: {}\n", location: "interface.yaml:3:17", want: "canonical integer from 0 through"},
		{name: "reversed length", path: "request.name", rules: "    min_length: 2\n    max_length: 1\n", location: "interface.yaml:4:17", want: "min_length must not exceed max_length", also: "interface.yaml:3:17"},
		{name: "pattern not string", path: "request.name", rules: "    pattern: 1\n", location: "interface.yaml:3:14", want: "valid UTF-8 string"},
		{name: "invalid pattern", path: "request.name", rules: "    pattern: '['\n", location: "interface.yaml:3:14", want: "valid deterministic Go regular-expression syntax"},
		{name: "oversized pattern", path: "request.name", rules: "    pattern: '" + strings.Repeat("a", interfacemeta.MaximumConstraintPatternBytes+1) + "'\n", location: "interface.yaml:3:14", want: "must not exceed 4096 UTF-8 bytes"},
		{name: "int32 fraction", path: "request.i32", rules: "    minimum: 1.5\n", location: "interface.yaml:3:14", want: "canonical int32 integer"},
		{name: "int32 overflow", path: "request.i32", rules: "    maximum: 2147483648\n", location: "interface.yaml:3:14", want: "int32 integer within the Go type range"},
		{name: "int64 overflow", path: "request.i64", rules: "    minimum: -9223372036854775809\n", location: "interface.yaml:3:14", want: "int64 integer within the Go type range"},
		{name: "uint32 negative", path: "request.u32", rules: "    minimum: -1\n", location: "interface.yaml:3:14", want: "canonical uint32 integer"},
		{name: "uint32 overflow", path: "request.u32", rules: "    maximum: 4294967296\n", location: "interface.yaml:3:14", want: "uint32 integer within the Go type range"},
		{name: "uint64 overflow", path: "request.u64", rules: "    maximum: 18446744073709551616\n", location: "interface.yaml:3:14", want: "uint64 integer within the Go type range"},
		{name: "float32 non finite", path: "request.f32", rules: "    minimum: .inf\n", location: "interface.yaml:3:14", want: "finite canonical JSON number representable by float32"},
		{name: "float32 overflow", path: "request.f32", rules: "    maximum: 1e100\n", location: "interface.yaml:3:14", want: "finite canonical JSON number representable by float32"},
		{name: "float32 inexact", path: "request.f32", rules: "    maximum: 16777217\n", location: "interface.yaml:3:14", want: "cannot be represented exactly by the normalized float32"},
		{name: "float64 inexact", path: "request.f64", rules: "    minimum: 9007199254740993\n", location: "interface.yaml:3:14", want: "cannot be represented exactly by the normalized float64"},
		{name: "reversed signed bounds", path: "request.i64", rules: "    minimum: 2\n    maximum: 1\n", location: "interface.yaml:4:14", want: "minimum must not exceed maximum", also: "interface.yaml:3:14"},
		{name: "reversed unsigned bounds", path: "request.u64", rules: "    minimum: 2\n    maximum: 1\n", location: "interface.yaml:4:14", want: "minimum must not exceed maximum", also: "interface.yaml:3:14"},
		{name: "reversed float bounds", path: "request.f64", rules: "    minimum: 2.5\n    maximum: 1.5\n", location: "interface.yaml:4:14", want: "minimum must not exceed maximum", also: "interface.yaml:3:14"},
		{name: "items not integer", path: "request.tags", rules: "    min_items: false\n", location: "interface.yaml:3:16", want: "canonical integer from 0 through"},
		{name: "reversed items", path: "request.lookup", rules: "    min_items: 2\n    max_items: 1\n", location: "interface.yaml:4:16", want: "min_items must not exceed max_items", also: "interface.yaml:3:16"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			data := []byte("constraints:\n  " + test.path + ":\n" + test.rules)
			document, err := interfacemeta.ParseFile("interfaces/rules/interface.yaml", data)
			if err != nil {
				t.Fatalf("ParseFile: %v", err)
			}
			targets, err := interfacemeta.ResolveConstraintTargets(document, contract)
			if !errors.Is(err, interfacemeta.ErrInvalid) || !errors.Is(err, interfacemeta.ErrInvalidConstraints) || len(targets) != 0 || !strings.Contains(err.Error(), test.location) || !strings.Contains(err.Error(), test.want) || test.also != "" && !strings.Contains(err.Error(), test.also) {
				t.Fatalf("ResolveConstraintTargets = %#v, %v", targets, err)
			}
		})
	}
}

func TestConstraintRulesNormalizeDeterministically(t *testing.T) {
	t.Parallel()

	contract := constraintTestContract(t, typedConstraintInterfaceSource)
	first := []byte(`constraints:
  request.name: {pattern: '', max_length: 10, min_length: 1}
  request.f64: {maximum: 1e3, minimum: -0.0}
  request.tags: {max_items: 3, min_items: 0}
`)
	second := []byte(`constraints:
  request.tags:
    min_items: 0
    max_items: 3
  request.f64:
    minimum: 0
    maximum: 1000.0
  request.name:
    min_length: 1
    max_length: 10
    pattern: ''
`)
	want := resolveConstraintSummary(t, first, contract)
	got := resolveConstraintSummary(t, second, contract)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("equivalent rules normalized differently:\nfirst:  %#v\nsecond: %#v", want, got)
	}
}

func FuzzResolveConstraintRules(f *testing.F) {
	contract := constraintTestContract(f, typedConstraintInterfaceSource)
	for _, seed := range []string{
		"min_length: 1\nmax_length: 10\npattern: '^[a-z]+$'",
		"minimum: -1\nmaximum: 1",
		"unknown: true",
		"pattern: '['",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 16*1024 {
			t.Skip()
		}
		var builder strings.Builder
		builder.WriteString("constraints:\n  request.name:\n")
		for _, line := range strings.Split(source, "\n") {
			builder.WriteString("    ")
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
		document, err := interfacemeta.ParseFile("interfaces/fuzz/interface.yaml", []byte(builder.String()))
		if err != nil {
			if !errors.Is(err, interfacemeta.ErrInvalid) {
				t.Fatalf("ParseFile returned unexpected error: %v", err)
			}
			return
		}
		first, err := interfacemeta.ResolveConstraintTargets(document, contract)
		if err != nil {
			if !errors.Is(err, interfacemeta.ErrInvalidConstraints) || len(first) != 0 {
				t.Fatalf("ResolveConstraintTargets returned inconsistent error: %#v, %v", first, err)
			}
			return
		}
		second, err := interfacemeta.ResolveConstraintTargets(document, contract)
		if err != nil || !reflect.DeepEqual(constraintSummary(first), constraintSummary(second)) {
			t.Fatalf("constraint normalization is not deterministic: %#v then %#v, %v", first, second, err)
		}
	})
}

func assertConstraintCount(t *testing.T, getter func() (uint32, bool), want uint32, present bool, name string) {
	t.Helper()
	got, ok := getter()
	if got != want || ok != present {
		t.Fatalf("%s = %d, %t; want %d, %t", name, got, ok, want, present)
	}
}

func assertSignedBounds(t *testing.T, rules interfacemeta.ConstraintRules, kind interfacecontract.TypeKind, wantMinimum, wantMaximum int64) {
	t.Helper()
	minimum, minimumPresent := rules.Minimum()
	maximum, maximumPresent := rules.Maximum()
	gotMinimum, minimumSigned := minimum.Int64()
	gotMaximum, maximumSigned := maximum.Int64()
	if !minimumPresent || !maximumPresent || !minimumSigned || !maximumSigned || minimum.Kind() != kind || maximum.Kind() != kind || gotMinimum != wantMinimum || gotMaximum != wantMaximum || minimum.Canonical() != strconv.FormatInt(wantMinimum, 10) || maximum.Canonical() != strconv.FormatInt(wantMaximum, 10) {
		t.Fatalf("signed bounds = %#v, %#v", minimum, maximum)
	}
}

func assertUnsignedBounds(t *testing.T, rules interfacemeta.ConstraintRules, kind interfacecontract.TypeKind, wantMinimum, wantMaximum uint64) {
	t.Helper()
	minimum, minimumPresent := rules.Minimum()
	maximum, maximumPresent := rules.Maximum()
	gotMinimum, minimumUnsigned := minimum.Uint64()
	gotMaximum, maximumUnsigned := maximum.Uint64()
	if !minimumPresent || !maximumPresent || !minimumUnsigned || !maximumUnsigned || minimum.Kind() != kind || maximum.Kind() != kind || gotMinimum != wantMinimum || gotMaximum != wantMaximum || minimum.Canonical() != strconv.FormatUint(wantMinimum, 10) || maximum.Canonical() != strconv.FormatUint(wantMaximum, 10) {
		t.Fatalf("unsigned bounds = %#v, %#v", minimum, maximum)
	}
}

func assertFloatBounds(t *testing.T, rules interfacemeta.ConstraintRules, kind interfacecontract.TypeKind, wantMinimum float64, wantMinimumCanonical string, wantMaximum float64, wantMaximumCanonical string) {
	t.Helper()
	minimum, minimumPresent := rules.Minimum()
	maximum, maximumPresent := rules.Maximum()
	gotMinimum, minimumFloat := minimum.Float64()
	gotMaximum, maximumFloat := maximum.Float64()
	if !minimumPresent || !maximumPresent || !minimumFloat || !maximumFloat || minimum.Kind() != kind || maximum.Kind() != kind || gotMinimum != wantMinimum || gotMaximum != wantMaximum || minimum.Canonical() != wantMinimumCanonical || maximum.Canonical() != wantMaximumCanonical {
		t.Fatalf("float bounds = %#v, %#v", minimum, maximum)
	}
}

func resolveConstraintSummary(t testing.TB, data []byte, contract interfacecontract.Contract) []string {
	t.Helper()
	document, err := interfacemeta.ParseFile("interfaces/rules/interface.yaml", data)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := interfacemeta.ResolveConstraintTargets(document, contract)
	if err != nil {
		t.Fatal(err)
	}
	return constraintSummary(targets)
}

func constraintSummary(targets []interfacemeta.ConstraintTarget) []string {
	result := make([]string, 0, len(targets))
	for _, target := range targets {
		rules := target.Rules()
		parts := []string{target.Path(), target.GoPath(), target.Field().Type().Canonical()}
		if value, ok := rules.MinLength(); ok {
			parts = append(parts, "min_length="+strconv.FormatUint(uint64(value), 10))
		}
		if value, ok := rules.MaxLength(); ok {
			parts = append(parts, "max_length="+strconv.FormatUint(uint64(value), 10))
		}
		if value, ok := rules.Pattern(); ok {
			parts = append(parts, "pattern="+value)
		}
		if value, ok := rules.Minimum(); ok {
			parts = append(parts, "minimum="+string(value.Kind())+":"+value.Canonical())
		}
		if value, ok := rules.Maximum(); ok {
			parts = append(parts, "maximum="+string(value.Kind())+":"+value.Canonical())
		}
		if value, ok := rules.MinItems(); ok {
			parts = append(parts, "min_items="+strconv.FormatUint(uint64(value), 10))
		}
		if value, ok := rules.MaxItems(); ok {
			parts = append(parts, "max_items="+strconv.FormatUint(uint64(value), 10))
		}
		result = append(result, strings.Join(parts, "|"))
	}
	return result
}

const typedConstraintInterfaceSource = `package contract

import (
	"context"
	"time"
)

//plystra:interface rules.validate/v1
type Interface interface { Validate(context.Context, Request) (Response, error) }

type Detail struct { Value string ` + "`plystra:\"1\" json:\"value\"`" + ` }

type Request struct {
	Name string ` + "`plystra:\"1\" json:\"name\"`" + `
	I32 int32 ` + "`plystra:\"2\" json:\"i32\"`" + `
	I64 int64 ` + "`plystra:\"3\" json:\"i64\"`" + `
	U32 uint32 ` + "`plystra:\"4\" json:\"u32\"`" + `
	U64 uint64 ` + "`plystra:\"5\" json:\"u64\"`" + `
	F32 float32 ` + "`plystra:\"6\" json:\"f32\"`" + `
	F64 float64 ` + "`plystra:\"7\" json:\"f64\"`" + `
	Tags []string ` + "`plystra:\"8\" json:\"tags\"`" + `
	Lookup map[string]string ` + "`plystra:\"9\" json:\"lookup\"`" + `
	Enabled bool ` + "`plystra:\"10\" json:\"enabled\"`" + `
	Detail Detail ` + "`plystra:\"11\" json:\"detail\"`" + `
	CreatedAt time.Time ` + "`plystra:\"12\" json:\"created_at\"`" + `
	Delay time.Duration ` + "`plystra:\"13\" json:\"delay\"`" + `
}

type Response struct { Payload []byte ` + "`plystra:\"1\" json:\"payload\"`" + ` }
`
