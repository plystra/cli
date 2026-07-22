package interfacemeta

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/plystra/cli/internal/interfacecontract"
	"go.yaml.in/yaml/v3"
)

const (
	// MaximumExampleNameLength bounds one stable example name.
	MaximumExampleNameLength = 128
	// MaximumExampleDepth bounds recursive example message and collection data.
	MaximumExampleDepth = 64
	// MaximumExampleNodes bounds validation work across one request and outcome.
	MaximumExampleNodes = 65_536
)

// ErrInvalidExamples reports invalid Interface request, response, or
// semantic-error examples.
var ErrInvalidExamples = errors.New("invalid Interface examples")

type exampleDeclaration struct {
	index     int
	name      string
	request   *yaml.Node
	response  *yaml.Node
	errorCode string
}

// ExampleValue is one immutable canonical request or response value. Its JSON
// form is deterministic and follows the canonical Interface field graph.
type ExampleValue struct {
	kind      interfacecontract.TypeKind
	canonical string
}

// Kind returns the exact canonical Go value kind.
func (v ExampleValue) Kind() interfacecontract.TypeKind { return v.kind }

// CanonicalJSON returns the deterministic normalized JSON representation.
func (v ExampleValue) CanonicalJSON() string { return v.canonical }

// Example is one immutable named request and exactly one successful response
// or declared semantic-error outcome.
type Example struct {
	name        string
	request     ExampleValue
	response    ExampleValue
	hasResponse bool
	errorCode   string
}

// Name returns the stable lower-kebab example name.
func (e Example) Name() string { return e.name }

// Request returns the normalized canonical request value.
func (e Example) Request() ExampleValue { return e.request }

// Response returns the normalized canonical response for a successful example.
func (e Example) Response() (ExampleValue, bool) { return e.response, e.hasResponse }

// ErrorCode returns the declared semantic-error code for an error example.
func (e Example) ErrorCode() (string, bool) { return e.errorCode, e.errorCode != "" }

// ResolveExamples validates every structurally normalized example against the
// canonical Go request and response graphs, required fields, and normalized
// field constraints. The returned view is ordered by example name.
func ResolveExamples(document Document, contract interfacecontract.Contract) ([]Example, error) {
	if len(document.examples) == 0 {
		return nil, nil
	}
	if contract.ID().String() == "" || contract.RequestName() == "" || contract.ResponseName() == "" {
		return nil, invalidWith(document.path, 0, 0, ErrInvalidExamples, "a canonical Interface contract is required to validate examples")
	}
	request, requestExists := contract.Message(contract.RequestName())
	response, responseExists := contract.Message(contract.ResponseName())
	if !requestExists || !responseExists {
		return nil, invalidWith(document.path, 0, 0, ErrInvalidExamples, "canonical request and response messages are required to validate examples")
	}
	constraintTargets, err := ResolveConstraintTargets(document, contract)
	if err != nil {
		return nil, err
	}
	constraints := make(map[string]exampleConstraint, len(constraintTargets))
	for _, target := range constraintTargets {
		constraints[target.GoPath()] = exampleConstraint{path: target.Path(), rules: target.Rules()}
	}

	result := make([]Example, 0, len(document.examples))
	for _, declaration := range document.examples {
		validator := exampleValidator{
			sourcePath:  document.path,
			contract:    contract,
			constraints: constraints,
		}
		prefix := fmt.Sprintf("examples[%d]", declaration.index)
		requestValue, err := validator.normalizeMessage(declaration.request, request, contract.RequestName(), prefix+".request", 1)
		if err != nil {
			return nil, err
		}
		example := Example{name: declaration.name, request: requestValue.value}
		if declaration.response != nil {
			responseValue, err := validator.normalizeMessage(declaration.response, response, contract.ResponseName(), prefix+".response", 1)
			if err != nil {
				return nil, err
			}
			example.response = responseValue.value
			example.hasResponse = true
		} else {
			example.errorCode = declaration.errorCode
		}
		result = append(result, example)
	}
	return result, nil
}

func parseExampleDeclarations(sourcePath string, root *yaml.Node, semanticErrors []SemanticError) ([]exampleDeclaration, error) {
	var value *yaml.Node
	for index := 0; index < len(root.Content); index += 2 {
		if root.Content[index].Value == "examples" {
			value = root.Content[index+1]
			break
		}
	}
	if value == nil {
		return nil, nil
	}
	if value.Kind != yaml.SequenceNode || value.Tag != "!!seq" {
		return nil, invalidWith(sourcePath, value.Line, value.Column, ErrInvalidExamples, "examples must be a sequence of mappings")
	}
	declaredErrors := make(map[string]struct{}, len(semanticErrors))
	for _, semanticError := range semanticErrors {
		declaredErrors[semanticError.Code()] = struct{}{}
	}
	nameLocations := make(map[string]string, len(value.Content))
	declarations := make([]exampleDeclaration, 0, len(value.Content))
	for index, entry := range value.Content {
		fieldPath := fmt.Sprintf("examples[%d]", index)
		if entry.Kind != yaml.MappingNode || entry.Tag != "!!map" {
			return nil, invalidWith(sourcePath, entry.Line, entry.Column, ErrInvalidExamples, "%s must be a mapping", fieldPath)
		}
		var nameNode, requestNode, responseNode, errorNode *yaml.Node
		for fieldIndex := 0; fieldIndex < len(entry.Content); fieldIndex += 2 {
			field := entry.Content[fieldIndex]
			switch field.Value {
			case "name":
				nameNode = entry.Content[fieldIndex+1]
			case "request":
				requestNode = entry.Content[fieldIndex+1]
			case "response":
				responseNode = entry.Content[fieldIndex+1]
			case "error":
				errorNode = entry.Content[fieldIndex+1]
			default:
				return nil, invalidWith(sourcePath, field.Line, field.Column, ErrInvalidExamples, "unknown field %q; allowed fields are name, request, response, and error", fieldPath+"."+field.Value)
			}
		}
		if nameNode == nil {
			return nil, invalidWith(sourcePath, entry.Line, entry.Column, ErrInvalidExamples, "required field %s.name is missing", fieldPath)
		}
		if nameNode.Kind != yaml.ScalarNode || nameNode.Tag != "!!str" || !validExampleName(nameNode.Value) {
			return nil, invalidWith(sourcePath, nameNode.Line, nameNode.Column, ErrInvalidExamples, "%s.name must be 1-%d characters of lower kebab case", fieldPath, MaximumExampleNameLength)
		}
		if first, duplicate := nameLocations[nameNode.Value]; duplicate {
			return nil, invalidWith(sourcePath, nameNode.Line, nameNode.Column, ErrInvalidExamples, "%s.name %q duplicates the declaration at %s", fieldPath, nameNode.Value, first)
		}
		nameLocations[nameNode.Value] = sourceLocation(sourcePath, nameNode)
		if requestNode == nil {
			return nil, invalidWith(sourcePath, entry.Line, entry.Column, ErrInvalidExamples, "required field %s.request is missing", fieldPath)
		}
		if (responseNode == nil) == (errorNode == nil) {
			return nil, invalidWith(sourcePath, entry.Line, entry.Column, ErrInvalidExamples, "%s must contain exactly one of response or error", fieldPath)
		}
		declaration := exampleDeclaration{index: index, name: nameNode.Value, request: requestNode, response: responseNode}
		if errorNode != nil {
			if errorNode.Kind != yaml.ScalarNode || errorNode.Tag != "!!str" {
				return nil, invalidWith(sourcePath, errorNode.Line, errorNode.Column, ErrInvalidExamples, "%s.error must be one declared semantic-error code", fieldPath)
			}
			if _, exists := declaredErrors[errorNode.Value]; !exists {
				return nil, invalidWith(sourcePath, errorNode.Line, errorNode.Column, ErrInvalidExamples, "%s.error references undeclared semantic-error code %q", fieldPath, errorNode.Value)
			}
			declaration.errorCode = errorNode.Value
		}
		declarations = append(declarations, declaration)
	}
	sort.Slice(declarations, func(left, right int) bool { return declarations[left].name < declarations[right].name })
	return declarations, nil
}

func validExampleName(value string) bool {
	if len(value) == 0 || len(value) > MaximumExampleNameLength || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousHyphen := false
	for _, character := range value[1:] {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}

type exampleConstraint struct {
	path  string
	rules ConstraintRules
}

type normalizedExampleValue struct {
	value     ExampleValue
	text      string
	length    uint32
	signed    int64
	unsigned  uint64
	floating  float64
	itemCount uint32
}

type exampleValidator struct {
	sourcePath  string
	contract    interfacecontract.Contract
	constraints map[string]exampleConstraint
	nodes       int
}

func (v *exampleValidator) normalizeMessage(node *yaml.Node, message interfacecontract.Message, goPath, valuePath string, depth int) (normalizedExampleValue, error) {
	if err := v.consume(node, valuePath, depth); err != nil {
		return normalizedExampleValue{}, err
	}
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" {
		return normalizedExampleValue{}, v.invalidAt(node, "%s must be a mapping matching Go message %s", valuePath, message.Name())
	}
	fields := message.Fields()
	byName := make(map[string]interfacecontract.Field, len(fields))
	for _, field := range fields {
		byName[effectiveJSONName(field)] = field
	}
	values := make(map[string]*yaml.Node, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, fieldValue := node.Content[index], node.Content[index+1]
		if err := v.consume(key, valuePath, depth+1); err != nil {
			return normalizedExampleValue{}, err
		}
		field, exists := byName[key.Value]
		if !exists {
			return normalizedExampleValue{}, v.invalidAt(key, "%s contains unknown field %q", valuePath, key.Value)
		}
		values[effectiveJSONName(field)] = fieldValue
	}

	pairs := make([]canonicalPair, 0, len(values))
	for _, field := range fields {
		name := effectiveJSONName(field)
		fieldNode, exists := values[name]
		if !exists {
			if field.Required() {
				return normalizedExampleValue{}, v.invalidAt(node, "%s is missing required field %q", valuePath, name)
			}
			continue
		}
		fieldGoPath := goPath + "." + field.Name()
		fieldValuePath := valuePath + "." + name
		normalized, err := v.normalizeValue(fieldNode, field.Type(), fieldGoPath, fieldValuePath, depth+1)
		if err != nil {
			return normalizedExampleValue{}, err
		}
		if err := v.validateConstraints(fieldNode, fieldGoPath, fieldValuePath, normalized); err != nil {
			return normalizedExampleValue{}, err
		}
		pairs = append(pairs, canonicalPair{key: name, value: normalized.value.canonical})
	}
	return normalizedExampleValue{value: ExampleValue{kind: interfacecontract.TypeMessage, canonical: canonicalObject(pairs)}}, nil
}

func (v *exampleValidator) normalizeValue(node *yaml.Node, fieldType interfacecontract.Type, goPath, valuePath string, depth int) (normalizedExampleValue, error) {
	if fieldType.Kind() == interfacecontract.TypeMessage {
		name, _ := fieldType.MessageName()
		message, exists := v.contract.Message(name)
		if !exists {
			return normalizedExampleValue{}, v.invalidAt(node, "%s references unknown canonical Go message %s", valuePath, name)
		}
		return v.normalizeMessage(node, message, goPath, valuePath, depth)
	}
	if err := v.consume(node, valuePath, depth); err != nil {
		return normalizedExampleValue{}, err
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return normalizedExampleValue{}, v.invalidAt(node, "%s must not be null", valuePath)
	}

	switch fieldType.Kind() {
	case interfacecontract.TypeBoolean:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		value, err := strconv.ParseBool(node.Value)
		if err != nil {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		canonical := strconv.FormatBool(value)
		return normalizedExampleValue{value: ExampleValue{kind: fieldType.Kind(), canonical: canonical}}, nil
	case interfacecontract.TypeString:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || !utf8.ValidString(node.Value) {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		return normalizedExampleValue{
			value:  ExampleValue{kind: fieldType.Kind(), canonical: quoteJSON(node.Value)},
			text:   node.Value,
			length: uint32(utf8.RuneCountInString(node.Value)),
		}, nil
	case interfacecontract.TypeInt32, interfacecontract.TypeInt64:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" || !canonicalSignedInteger(node.Value) {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		bits := 64
		if fieldType.Kind() == interfacecontract.TypeInt32 {
			bits = 32
		}
		value, err := strconv.ParseInt(node.Value, 10, bits)
		if err != nil {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		canonical := strconv.FormatInt(value, 10)
		return normalizedExampleValue{value: ExampleValue{kind: fieldType.Kind(), canonical: canonical}, signed: value}, nil
	case interfacecontract.TypeUint32, interfacecontract.TypeUint64:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" || !canonicalUnsignedInteger(node.Value) {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		bits := 64
		if fieldType.Kind() == interfacecontract.TypeUint32 {
			bits = 32
		}
		value, err := strconv.ParseUint(node.Value, 10, bits)
		if err != nil {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		canonical := strconv.FormatUint(value, 10)
		return normalizedExampleValue{value: ExampleValue{kind: fieldType.Kind(), canonical: canonical}, unsigned: value}, nil
	case interfacecontract.TypeFloat32, interfacecontract.TypeFloat64:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!int" && node.Tag != "!!float" || !canonicalJSONNumber(node.Value) {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		bits := 64
		if fieldType.Kind() == interfacecontract.TypeFloat32 {
			bits = 32
		}
		value, err := strconv.ParseFloat(node.Value, bits)
		if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		if value == 0 {
			value = 0
		}
		canonical := strconv.FormatFloat(value, 'g', -1, bits)
		return normalizedExampleValue{value: ExampleValue{kind: fieldType.Kind(), canonical: canonical}, floating: value}, nil
	case interfacecontract.TypeBytes:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(node.Value)
		if err != nil || base64.StdEncoding.EncodeToString(decoded) != node.Value {
			return normalizedExampleValue{}, v.invalidAt(node, "%s must be a canonical padded base64 string for bytes", valuePath)
		}
		return normalizedExampleValue{
			value:  ExampleValue{kind: fieldType.Kind(), canonical: quoteJSON(node.Value)},
			length: uint32(len(decoded)),
		}, nil
	case interfacecontract.TypeRepeated:
		if node.Kind != yaml.SequenceNode || node.Tag != "!!seq" {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		element, _ := fieldType.Element()
		values := make([]string, 0, len(node.Content))
		for index, item := range node.Content {
			normalized, err := v.normalizeValue(item, element, goPath, fmt.Sprintf("%s[%d]", valuePath, index), depth+1)
			if err != nil {
				return normalizedExampleValue{}, err
			}
			values = append(values, normalized.value.canonical)
		}
		return normalizedExampleValue{
			value:     ExampleValue{kind: fieldType.Kind(), canonical: "[" + strings.Join(values, ",") + "]"},
			itemCount: uint32(len(node.Content)),
		}, nil
	case interfacecontract.TypeMap:
		if node.Kind != yaml.MappingNode || node.Tag != "!!map" {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		keyType, _ := fieldType.Key()
		valueType, _ := fieldType.Value()
		pairs := make([]canonicalPair, 0, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			keyNode, valueNode := node.Content[index], node.Content[index+1]
			if err := v.consume(keyNode, valuePath, depth+1); err != nil {
				return normalizedExampleValue{}, err
			}
			key, err := v.normalizeMapKey(keyNode, keyType, valuePath)
			if err != nil {
				return normalizedExampleValue{}, err
			}
			normalized, err := v.normalizeValue(valueNode, valueType, goPath, valuePath+"[*]", depth+1)
			if err != nil {
				return normalizedExampleValue{}, err
			}
			pairs = append(pairs, canonicalPair{key: key, value: normalized.value.canonical})
		}
		sort.Slice(pairs, func(left, right int) bool { return pairs[left].key < pairs[right].key })
		return normalizedExampleValue{
			value:     ExampleValue{kind: fieldType.Kind(), canonical: canonicalObject(pairs)},
			itemCount: uint32(len(pairs)),
		}, nil
	case interfacecontract.TypeTimestamp:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" && node.Tag != "!!timestamp" {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		value, err := time.Parse(time.RFC3339Nano, node.Value)
		if err != nil || value.Year() < 1 || value.Year() > 9999 {
			return normalizedExampleValue{}, v.invalidAt(node, "%s must be an RFC 3339 timestamp in the supported year range", valuePath)
		}
		return normalizedExampleValue{value: ExampleValue{kind: fieldType.Kind(), canonical: quoteJSON(value.UTC().Format(time.RFC3339Nano))}}, nil
	case interfacecontract.TypeDuration:
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
			return normalizedExampleValue{}, v.typeError(node, valuePath, fieldType)
		}
		value, err := time.ParseDuration(node.Value)
		if err != nil {
			return normalizedExampleValue{}, v.invalidAt(node, "%s must be a valid Go duration string", valuePath)
		}
		return normalizedExampleValue{value: ExampleValue{kind: fieldType.Kind(), canonical: quoteJSON(value.String())}}, nil
	default:
		return normalizedExampleValue{}, v.invalidAt(node, "%s uses unsupported canonical type %s", valuePath, fieldType.Canonical())
	}
}

func (v *exampleValidator) normalizeMapKey(node *yaml.Node, keyType interfacecontract.Type, valuePath string) (string, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || !utf8.ValidString(node.Value) {
		return "", v.invalidAt(node, "%s map keys must be YAML strings encoding canonical %s values", valuePath, keyType.Canonical())
	}
	switch keyType.Kind() {
	case interfacecontract.TypeString:
		return node.Value, nil
	case interfacecontract.TypeBoolean:
		if node.Value == "true" || node.Value == "false" {
			return node.Value, nil
		}
	case interfacecontract.TypeInt32, interfacecontract.TypeInt64:
		bits := 64
		if keyType.Kind() == interfacecontract.TypeInt32 {
			bits = 32
		}
		if canonicalSignedInteger(node.Value) {
			if value, err := strconv.ParseInt(node.Value, 10, bits); err == nil {
				return strconv.FormatInt(value, 10), nil
			}
		}
	case interfacecontract.TypeUint32, interfacecontract.TypeUint64:
		bits := 64
		if keyType.Kind() == interfacecontract.TypeUint32 {
			bits = 32
		}
		if canonicalUnsignedInteger(node.Value) {
			if value, err := strconv.ParseUint(node.Value, 10, bits); err == nil {
				return strconv.FormatUint(value, 10), nil
			}
		}
	}
	return "", v.invalidAt(node, "%s contains a map key that is not a canonical %s value", valuePath, keyType.Canonical())
}

func (v *exampleValidator) validateConstraints(node *yaml.Node, goPath, valuePath string, value normalizedExampleValue) error {
	constraint, exists := v.constraints[goPath]
	if !exists || constraint.rules.Empty() {
		return nil
	}
	rules := constraint.rules
	if minimum, present := rules.MinLength(); present && value.length < minimum {
		return v.constraintViolation(node, valuePath, constraint.path, "min_length")
	}
	if maximum, present := rules.MaxLength(); present && value.length > maximum {
		return v.constraintViolation(node, valuePath, constraint.path, "max_length")
	}
	if pattern, present := rules.Pattern(); present {
		matched, err := regexp.MatchString(pattern, value.text)
		if err != nil || !matched {
			return v.constraintViolation(node, valuePath, constraint.path, "pattern")
		}
	}
	if minimum, present := rules.Minimum(); present && exampleNumberBelow(value, minimum) {
		return v.constraintViolation(node, valuePath, constraint.path, "minimum")
	}
	if maximum, present := rules.Maximum(); present && exampleNumberAbove(value, maximum) {
		return v.constraintViolation(node, valuePath, constraint.path, "maximum")
	}
	if minimum, present := rules.MinItems(); present && value.itemCount < minimum {
		return v.constraintViolation(node, valuePath, constraint.path, "min_items")
	}
	if maximum, present := rules.MaxItems(); present && value.itemCount > maximum {
		return v.constraintViolation(node, valuePath, constraint.path, "max_items")
	}
	return nil
}

func exampleNumberBelow(value normalizedExampleValue, bound NumericBound) bool {
	switch bound.Kind() {
	case interfacecontract.TypeInt32, interfacecontract.TypeInt64:
		minimum, _ := bound.Int64()
		return value.signed < minimum
	case interfacecontract.TypeUint32, interfacecontract.TypeUint64:
		minimum, _ := bound.Uint64()
		return value.unsigned < minimum
	default:
		minimum, _ := bound.Float64()
		return value.floating < minimum
	}
}

func exampleNumberAbove(value normalizedExampleValue, bound NumericBound) bool {
	switch bound.Kind() {
	case interfacecontract.TypeInt32, interfacecontract.TypeInt64:
		maximum, _ := bound.Int64()
		return value.signed > maximum
	case interfacecontract.TypeUint32, interfacecontract.TypeUint64:
		maximum, _ := bound.Uint64()
		return value.unsigned > maximum
	default:
		maximum, _ := bound.Float64()
		return value.floating > maximum
	}
}

func (v *exampleValidator) consume(node *yaml.Node, valuePath string, depth int) error {
	if node == nil {
		return invalidWith(v.sourcePath, 0, 0, ErrInvalidExamples, "%s contains an empty YAML node", valuePath)
	}
	if depth > MaximumExampleDepth {
		return v.invalidAt(node, "%s exceeds the maximum example depth of %d", valuePath, MaximumExampleDepth)
	}
	v.nodes++
	if v.nodes > MaximumExampleNodes {
		return v.invalidAt(node, "%s exceeds the maximum example node count of %d", valuePath, MaximumExampleNodes)
	}
	return nil
}

func (v *exampleValidator) typeError(node *yaml.Node, valuePath string, fieldType interfacecontract.Type) error {
	return v.invalidAt(node, "%s must be a canonical %s value", valuePath, fieldType.Canonical())
}

func (v *exampleValidator) constraintViolation(node *yaml.Node, valuePath, fieldPath, rule string) error {
	return v.invalidAt(node, "%s violates %s for canonical field %q", valuePath, rule, fieldPath)
}

func (v *exampleValidator) invalidAt(node *yaml.Node, format string, arguments ...any) error {
	line, column := 0, 0
	if node != nil {
		line, column = node.Line, node.Column
	}
	return invalidWith(v.sourcePath, line, column, ErrInvalidExamples, format, arguments...)
}

type canonicalPair struct {
	key   string
	value string
}

func effectiveJSONName(field interfacecontract.Field) string {
	if field.HasExplicitJSONName() {
		return field.JSONName()
	}
	return field.Name()
}

func canonicalObject(pairs []canonicalPair) string {
	var builder strings.Builder
	builder.WriteByte('{')
	for index, pair := range pairs {
		if index != 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(quoteJSON(pair.key))
		builder.WriteByte(':')
		builder.WriteString(pair.value)
	}
	builder.WriteByte('}')
	return builder.String()
}

func quoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
