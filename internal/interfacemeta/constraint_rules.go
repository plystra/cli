package interfacemeta

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/plystra/cli/internal/interfacecontract"
	"go.yaml.in/yaml/v3"
)

const (
	// MaximumConstraintCount is the largest portable string, byte, repeated,
	// or map size bound accepted by Interface metadata.
	MaximumConstraintCount uint32 = 1<<31 - 1
	// MaximumConstraintPatternBytes bounds regular-expression parsing and
	// compilation work for one canonical string field.
	MaximumConstraintPatternBytes = 4096
)

type constraintRuleDeclaration struct {
	name   string
	line   int
	column int
	value  *yaml.Node
}

// NumericBound is one immutable normalized bound for an exact canonical Go
// numeric field type.
type NumericBound struct {
	kind      interfacecontract.TypeKind
	canonical string
	signed    int64
	unsigned  uint64
	floating  float64
}

// Kind returns the exact canonical Go field type governed by the bound.
func (b NumericBound) Kind() interfacecontract.TypeKind { return b.kind }

// Canonical returns the deterministic JSON-number spelling of the bound.
func (b NumericBound) Canonical() string { return b.canonical }

// Int64 returns an exact signed bound for an int32 or int64 field.
func (b NumericBound) Int64() (int64, bool) {
	return b.signed, b.kind == interfacecontract.TypeInt32 || b.kind == interfacecontract.TypeInt64
}

// Uint64 returns an exact unsigned bound for a uint32 or uint64 field.
func (b NumericBound) Uint64() (uint64, bool) {
	return b.unsigned, b.kind == interfacecontract.TypeUint32 || b.kind == interfacecontract.TypeUint64
}

// Float64 returns the normalized finite bound for a float32 or float64 field.
func (b NumericBound) Float64() (float64, bool) {
	return b.floating, b.kind == interfacecontract.TypeFloat32 || b.kind == interfacecontract.TypeFloat64
}

// ConstraintRules is one closed immutable field-type-specific rule set.
type ConstraintRules struct {
	minLength    uint32
	hasMinLength bool
	maxLength    uint32
	hasMaxLength bool
	pattern      string
	hasPattern   bool
	minimum      NumericBound
	hasMinimum   bool
	maximum      NumericBound
	hasMaximum   bool
	minItems     uint32
	hasMinItems  bool
	maxItems     uint32
	hasMaxItems  bool
}

// MinLength returns the minimum Unicode-code-point or byte length.
func (r ConstraintRules) MinLength() (uint32, bool) { return r.minLength, r.hasMinLength }

// MaxLength returns the maximum Unicode-code-point or byte length.
func (r ConstraintRules) MaxLength() (uint32, bool) { return r.maxLength, r.hasMaxLength }

// Pattern returns the deterministic Go regular expression for a string.
func (r ConstraintRules) Pattern() (string, bool) { return r.pattern, r.hasPattern }

// Minimum returns the exact lower bound for a numeric field.
func (r ConstraintRules) Minimum() (NumericBound, bool) { return r.minimum, r.hasMinimum }

// Maximum returns the exact upper bound for a numeric field.
func (r ConstraintRules) Maximum() (NumericBound, bool) { return r.maximum, r.hasMaximum }

// MinItems returns the minimum repeated or map item count.
func (r ConstraintRules) MinItems() (uint32, bool) { return r.minItems, r.hasMinItems }

// MaxItems returns the maximum repeated or map item count.
func (r ConstraintRules) MaxItems() (uint32, bool) { return r.maxItems, r.hasMaxItems }

// Empty reports whether the declaration contains no normalized rule.
func (r ConstraintRules) Empty() bool {
	return !r.hasMinLength && !r.hasMaxLength && !r.hasPattern &&
		!r.hasMinimum && !r.hasMaximum && !r.hasMinItems && !r.hasMaxItems
}

func normalizeConstraintRules(sourcePath string, declaration constraintPathDeclaration, fieldType interfacecontract.Type) (ConstraintRules, error) {
	var rules ConstraintRules
	var minLengthNode, maxLengthNode, minimumNode, maximumNode, minItemsNode, maxItemsNode *yaml.Node
	for _, rule := range declaration.rules {
		if !knownConstraintRule(rule.name) {
			return ConstraintRules{}, invalidWith(sourcePath, rule.line, rule.column, ErrInvalidConstraints, "unknown constraint rule %q for path %q; allowed rules are max_items, max_length, maximum, min_items, min_length, minimum, and pattern", rule.name, declaration.path)
		}
		if !constraintRuleSupportsType(rule.name, fieldType.Kind()) {
			return ConstraintRules{}, invalidWith(sourcePath, rule.line, rule.column, ErrInvalidConstraints, "constraint rule %q is not supported for %s field %q", rule.name, fieldType.Canonical(), declaration.path)
		}

		switch rule.name {
		case "min_length":
			value, err := parseConstraintCount(rule.value)
			if err != nil {
				return ConstraintRules{}, invalidConstraintRuleValue(sourcePath, declaration.path, rule, err)
			}
			rules.minLength, rules.hasMinLength, minLengthNode = value, true, rule.value
		case "max_length":
			value, err := parseConstraintCount(rule.value)
			if err != nil {
				return ConstraintRules{}, invalidConstraintRuleValue(sourcePath, declaration.path, rule, err)
			}
			rules.maxLength, rules.hasMaxLength, maxLengthNode = value, true, rule.value
		case "pattern":
			value, err := parseConstraintPattern(rule.value)
			if err != nil {
				return ConstraintRules{}, invalidConstraintRuleValue(sourcePath, declaration.path, rule, err)
			}
			rules.pattern, rules.hasPattern = value, true
		case "minimum":
			value, err := parseConstraintNumericBound(rule.value, fieldType.Kind())
			if err != nil {
				return ConstraintRules{}, invalidConstraintRuleValue(sourcePath, declaration.path, rule, err)
			}
			rules.minimum, rules.hasMinimum, minimumNode = value, true, rule.value
		case "maximum":
			value, err := parseConstraintNumericBound(rule.value, fieldType.Kind())
			if err != nil {
				return ConstraintRules{}, invalidConstraintRuleValue(sourcePath, declaration.path, rule, err)
			}
			rules.maximum, rules.hasMaximum, maximumNode = value, true, rule.value
		case "min_items":
			value, err := parseConstraintCount(rule.value)
			if err != nil {
				return ConstraintRules{}, invalidConstraintRuleValue(sourcePath, declaration.path, rule, err)
			}
			rules.minItems, rules.hasMinItems, minItemsNode = value, true, rule.value
		case "max_items":
			value, err := parseConstraintCount(rule.value)
			if err != nil {
				return ConstraintRules{}, invalidConstraintRuleValue(sourcePath, declaration.path, rule, err)
			}
			rules.maxItems, rules.hasMaxItems, maxItemsNode = value, true, rule.value
		}
	}

	if rules.hasMinLength && rules.hasMaxLength && rules.minLength > rules.maxLength {
		return ConstraintRules{}, invalidWith(sourcePath, maxLengthNode.Line, maxLengthNode.Column, ErrInvalidConstraints, "constraint path %q min_length must not exceed max_length declared at %s", declaration.path, sourceLocation(sourcePath, minLengthNode))
	}
	if rules.hasMinimum && rules.hasMaximum && compareNumericBounds(rules.minimum, rules.maximum) > 0 {
		return ConstraintRules{}, invalidWith(sourcePath, maximumNode.Line, maximumNode.Column, ErrInvalidConstraints, "constraint path %q minimum must not exceed maximum declared at %s", declaration.path, sourceLocation(sourcePath, minimumNode))
	}
	if rules.hasMinItems && rules.hasMaxItems && rules.minItems > rules.maxItems {
		return ConstraintRules{}, invalidWith(sourcePath, maxItemsNode.Line, maxItemsNode.Column, ErrInvalidConstraints, "constraint path %q min_items must not exceed max_items declared at %s", declaration.path, sourceLocation(sourcePath, minItemsNode))
	}
	return rules, nil
}

func knownConstraintRule(name string) bool {
	switch name {
	case "min_length", "max_length", "pattern", "minimum", "maximum", "min_items", "max_items":
		return true
	default:
		return false
	}
}

func constraintRuleSupportsType(name string, kind interfacecontract.TypeKind) bool {
	switch name {
	case "min_length", "max_length":
		return kind == interfacecontract.TypeString || kind == interfacecontract.TypeBytes
	case "pattern":
		return kind == interfacecontract.TypeString
	case "minimum", "maximum":
		return numericConstraintKind(kind)
	case "min_items", "max_items":
		return kind == interfacecontract.TypeRepeated || kind == interfacecontract.TypeMap
	default:
		return false
	}
}

func numericConstraintKind(kind interfacecontract.TypeKind) bool {
	switch kind {
	case interfacecontract.TypeInt32, interfacecontract.TypeInt64,
		interfacecontract.TypeUint32, interfacecontract.TypeUint64,
		interfacecontract.TypeFloat32, interfacecontract.TypeFloat64:
		return true
	default:
		return false
	}
}

func invalidConstraintRuleValue(sourcePath, fieldPath string, rule constraintRuleDeclaration, err error) error {
	line, column := rule.line, rule.column
	if rule.value != nil {
		line, column = rule.value.Line, rule.value.Column
	}
	return invalidWith(sourcePath, line, column, ErrInvalidConstraints, "constraint rule %q for path %q %v", rule.name, fieldPath, err)
}

func parseConstraintCount(node *yaml.Node) (uint32, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" || !canonicalUnsignedInteger(node.Value) {
		return 0, fmt.Errorf("must be a canonical integer from 0 through %d", MaximumConstraintCount)
	}
	value, err := strconv.ParseUint(node.Value, 10, 32)
	if err != nil || value > uint64(MaximumConstraintCount) {
		return 0, fmt.Errorf("must be a canonical integer from 0 through %d", MaximumConstraintCount)
	}
	return uint32(value), nil
}

func parseConstraintPattern(node *yaml.Node) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" || !utf8.ValidString(node.Value) {
		return "", fmt.Errorf("must be a valid UTF-8 string")
	}
	if len(node.Value) > MaximumConstraintPatternBytes {
		return "", fmt.Errorf("must not exceed %d UTF-8 bytes", MaximumConstraintPatternBytes)
	}
	if _, err := regexp.Compile(node.Value); err != nil {
		return "", fmt.Errorf("must use valid deterministic Go regular-expression syntax")
	}
	return node.Value, nil
}

func parseConstraintNumericBound(node *yaml.Node, kind interfacecontract.TypeKind) (NumericBound, error) {
	if node == nil || node.Kind != yaml.ScalarNode {
		return NumericBound{}, fmt.Errorf("must be one canonical %s number", kind)
	}
	switch kind {
	case interfacecontract.TypeInt32, interfacecontract.TypeInt64:
		if node.Tag != "!!int" && node.Tag != "!!float" || !canonicalSignedInteger(node.Value) {
			return NumericBound{}, fmt.Errorf("must be one canonical %s integer", kind)
		}
		bits := 64
		if kind == interfacecontract.TypeInt32 {
			bits = 32
		}
		value, err := strconv.ParseInt(node.Value, 10, bits)
		if err != nil {
			return NumericBound{}, fmt.Errorf("must be one canonical %s integer within the Go type range", kind)
		}
		return NumericBound{kind: kind, canonical: strconv.FormatInt(value, 10), signed: value}, nil
	case interfacecontract.TypeUint32, interfacecontract.TypeUint64:
		if node.Tag != "!!int" && node.Tag != "!!float" || !canonicalUnsignedInteger(node.Value) {
			return NumericBound{}, fmt.Errorf("must be one canonical %s integer", kind)
		}
		bits := 64
		if kind == interfacecontract.TypeUint32 {
			bits = 32
		}
		value, err := strconv.ParseUint(node.Value, 10, bits)
		if err != nil {
			return NumericBound{}, fmt.Errorf("must be one canonical %s integer within the Go type range", kind)
		}
		return NumericBound{kind: kind, canonical: strconv.FormatUint(value, 10), unsigned: value}, nil
	case interfacecontract.TypeFloat32, interfacecontract.TypeFloat64:
		return parseConstraintFloatBound(node, kind)
	default:
		return NumericBound{}, fmt.Errorf("is not supported for %s fields", kind)
	}
}

func parseConstraintFloatBound(node *yaml.Node, kind interfacecontract.TypeKind) (NumericBound, error) {
	if node.Tag != "!!int" && node.Tag != "!!float" || !canonicalJSONNumber(node.Value) {
		return NumericBound{}, fmt.Errorf("must be one finite canonical JSON number representable by %s", kind)
	}
	bits := 64
	if kind == interfacecontract.TypeFloat32 {
		bits = 32
	}
	value, err := strconv.ParseFloat(node.Value, bits)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return NumericBound{}, fmt.Errorf("must be one finite canonical JSON number representable by %s", kind)
	}
	if value == 0 {
		value = 0
	}
	canonical := strconv.FormatFloat(value, 'g', -1, bits)
	sourceValue, sourceOK := new(big.Rat).SetString(node.Value)
	canonicalValue, canonicalOK := new(big.Rat).SetString(canonical)
	if !sourceOK || !canonicalOK || sourceValue.Cmp(canonicalValue) != 0 {
		return NumericBound{}, fmt.Errorf("cannot be represented exactly by the normalized %s constraint model", kind)
	}
	return NumericBound{kind: kind, canonical: canonical, floating: value}, nil
}

func canonicalJSONNumber(value string) bool {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return false
	}
	if _, ok := decoded.(json.Number); !ok {
		return false
	}
	return decoder.Decode(new(any)) == io.EOF
}

func canonicalSignedInteger(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value == "-0" {
		return false
	}
	start := 0
	if value[0] == '-' {
		if len(value) == 1 {
			return false
		}
		start = 1
	}
	if value[start] < '1' || value[start] > '9' {
		return false
	}
	for index := start + 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func canonicalUnsignedInteger(value string) bool {
	if value == "0" {
		return true
	}
	if value == "" || value[0] < '1' || value[0] > '9' {
		return false
	}
	for index := 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func compareNumericBounds(left, right NumericBound) int {
	switch left.kind {
	case interfacecontract.TypeInt32, interfacecontract.TypeInt64:
		switch {
		case left.signed < right.signed:
			return -1
		case left.signed > right.signed:
			return 1
		default:
			return 0
		}
	case interfacecontract.TypeUint32, interfacecontract.TypeUint64:
		switch {
		case left.unsigned < right.unsigned:
			return -1
		case left.unsigned > right.unsigned:
			return 1
		default:
			return 0
		}
	default:
		switch {
		case left.floating < right.floating:
			return -1
		case left.floating > right.floating:
			return 1
		default:
			return 0
		}
	}
}

func sourceLocation(sourcePath string, node *yaml.Node) string {
	if node == nil || node.Line == 0 {
		return sourcePath
	}
	return fmt.Sprintf("%s:%d:%d", sourcePath, node.Line, node.Column)
}
