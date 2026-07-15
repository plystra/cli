package capabilitymeta

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
	"go.yaml.in/yaml/v3"
)

type canonicalSchema struct {
	ID         string                     `json:"id"`
	Request    map[string]canonicalField  `json:"request"`
	Response   map[string]canonicalField  `json:"response"`
	Errors     []string                   `json:"errors"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

type canonicalField struct {
	Type     string            `json:"type"`
	Items    string            `json:"items,omitempty"`
	Required bool              `json:"required,omitempty"`
	Enum     []json.RawMessage `json:"enum,omitempty"`
}

// NormalizeSchema returns the deterministic exact contract projection used to
// compare provider-carried declarations. Human descriptions and source
// formatting are excluded. Kernel remains authoritative for the public
// capability contract model and runtime validation.
func NormalizeSchema(data []byte) ([]byte, error) {
	root, err := decodeDocument(data)
	if err != nil {
		return nil, err
	}
	values, err := mapping(root, "document")
	if err != nil {
		return nil, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "id", "description", "request", "response", "errors", "extensions":
		default:
			return nil, invalid("unknown key %q", key)
		}
	}

	idNode, ok := values["id"]
	if !ok {
		return nil, invalid("id is required")
	}
	idValue, err := strictString(idNode)
	if err != nil {
		return nil, invalid("id must be a string")
	}
	identifier, err := capabilityid.Parse(idValue)
	if err != nil {
		return nil, invalid("id %q is not canonical", idValue)
	}
	if description, ok := values["description"]; ok {
		if _, err := strictString(description); err != nil {
			return nil, invalid("description must be a string")
		}
	}

	request, err := normalizeSection("request", values["request"])
	if err != nil {
		return nil, err
	}
	response, err := normalizeSection("response", values["response"])
	if err != nil {
		return nil, err
	}
	errorsList, err := normalizeErrors(values["errors"])
	if err != nil {
		return nil, err
	}
	extensions, err := normalizeCapabilityExtensions(values["extensions"])
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(canonicalSchema{
		ID:         identifier.String(),
		Request:    request,
		Response:   response,
		Errors:     errorsList,
		Extensions: extensions,
	})
	if err != nil {
		return nil, invalid("encode canonical schema: %v", err)
	}
	return encoded, nil
}

func normalizeCapabilityExtensions(node *yaml.Node) (map[string]json.RawMessage, error) {
	if node == nil {
		return nil, nil
	}
	extensions, err := parseCapabilityExtensions(node)
	if err != nil {
		return nil, err
	}
	if len(extensions.values) == 0 {
		return nil, nil
	}
	canonical := make(map[string]json.RawMessage, len(extensions.values))
	for _, extension := range extensions.values {
		canonical[extension.namespace] = json.RawMessage(extension.ValueJSON())
	}
	return canonical, nil
}

func normalizeSection(section string, node *yaml.Node) (map[string]canonicalField, error) {
	if node == nil {
		return map[string]canonicalField{}, nil
	}
	values, err := mapping(node, section)
	if err != nil {
		return nil, err
	}
	result := make(map[string]canonicalField, len(values))
	for _, name := range sortedNodeKeys(values) {
		fieldNode := values[name]
		if !validFieldName(name) {
			return nil, invalid("%s field name %q is not canonical lower snake case", section, name)
		}
		field, err := normalizeField(section+"."+name, fieldNode)
		if err != nil {
			return nil, err
		}
		result[name] = field
	}
	return result, nil
}

func normalizeField(path string, node *yaml.Node) (canonicalField, error) {
	values, err := mapping(node, path)
	if err != nil {
		return canonicalField{}, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "type", "required", "items", "enum":
		default:
			return canonicalField{}, invalid("%s contains unknown key %q", path, key)
		}
	}
	typeNode, ok := values["type"]
	if !ok {
		return canonicalField{}, invalid("%s.type is required", path)
	}
	typeName, err := strictString(typeNode)
	if err != nil || !validSchemaType(typeName) {
		return canonicalField{}, invalid("%s.type %q is not supported", path, typeNode.Value)
	}
	field := canonicalField{Type: typeName}
	if requiredNode, ok := values["required"]; ok {
		field.Required, err = strictBool(requiredNode)
		if err != nil {
			return canonicalField{}, invalid("%s.required must be true or false", path)
		}
	}
	if itemsNode, ok := values["items"]; ok {
		field.Items, err = strictString(itemsNode)
		if err != nil || typeName != "array" || !validSchemaType(field.Items) || field.Items == "array" {
			return canonicalField{}, invalid("%s.items must be a non-array schema type on an array field", path)
		}
	} else if typeName == "array" {
		return canonicalField{}, invalid("%s.items is required for an array field", path)
	}
	if enumNode, ok := values["enum"]; ok {
		field.Enum, err = normalizeEnum(path, typeName, enumNode)
		if err != nil {
			return canonicalField{}, err
		}
	}
	return field, nil
}

func normalizeEnum(path, schemaType string, node *yaml.Node) ([]json.RawMessage, error) {
	if schemaType == "object" || schemaType == "array" {
		return nil, invalid("%s.enum is not supported for %s fields", path, schemaType)
	}
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		return nil, invalid("%s.enum must be a non-empty sequence", path)
	}
	encoded := make([][]byte, 0, len(node.Content))
	seen := make(map[string]struct{}, len(node.Content))
	for index, valueNode := range node.Content {
		value, err := normalizeScalar(valueNode, schemaType)
		if err != nil {
			return nil, invalid("%s.enum[%d]: %v", path, index, err)
		}
		key := string(value)
		if _, duplicate := seen[key]; duplicate {
			return nil, invalid("%s.enum contains duplicate value %s", path, value)
		}
		seen[key] = struct{}{}
		encoded = append(encoded, value)
	}
	sort.Slice(encoded, func(left, right int) bool {
		return bytes.Compare(encoded[left], encoded[right]) < 0
	})
	result := make([]json.RawMessage, len(encoded))
	for index := range encoded {
		result[index] = json.RawMessage(encoded[index])
	}
	return result, nil
}

func normalizeScalar(node *yaml.Node, schemaType string) ([]byte, error) {
	var value any
	var err error
	switch schemaType {
	case "string":
		value, err = strictString(node)
	case "integer":
		value, err = strictInteger(node)
	case "number":
		value, err = strictNumber(node)
	case "boolean":
		value, err = strictBool(node)
	default:
		return nil, fmt.Errorf("unsupported scalar schema type %q", schemaType)
	}
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, errors.New("cannot be encoded")
	}
	return encoded, nil
}

func normalizeErrors(node *yaml.Node) ([]string, error) {
	if node == nil {
		return []string{}, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, invalid("errors must be a sequence")
	}
	result := make([]string, 0, len(node.Content))
	seen := make(map[string]struct{}, len(node.Content))
	for index, valueNode := range node.Content {
		value, err := strictString(valueNode)
		if err != nil || !validFieldName(value) {
			return nil, invalid("errors[%d] must be canonical lower snake case", index)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, invalid("errors contains duplicate code %q", value)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func mapping(node *yaml.Node, path string) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, invalid("%s must be a mapping", path)
	}
	result := make(map[string]*yaml.Node, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, err := strictString(node.Content[index])
		if err != nil {
			return nil, invalid("%s contains a non-string key", path)
		}
		if _, duplicate := result[key]; duplicate {
			return nil, invalid("%s contains duplicate key %q", path, key)
		}
		result[key] = node.Content[index+1]
	}
	return result, nil
}

func sortedNodeKeys(values map[string]*yaml.Node) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func strictString(node *yaml.Node) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", errors.New("must be a string")
	}
	return node.Value, nil
}

func strictBool(node *yaml.Node) (bool, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
		return false, errors.New("must be a boolean")
	}
	switch node.Value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("must be true or false")
	}
}

func strictInteger(node *yaml.Node) (int64, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!int" || !canonicalInteger(node.Value) {
		return 0, errors.New("must be a canonical base-10 integer")
	}
	value, err := strconv.ParseInt(node.Value, 10, 64)
	if err != nil {
		return 0, errors.New("integer is outside the signed 64-bit range")
	}
	return value, nil
}

func strictNumber(node *yaml.Node) (any, error) {
	if node == nil || node.Kind != yaml.ScalarNode {
		return nil, errors.New("must be a number")
	}
	if node.Tag == "!!int" {
		return strictInteger(node)
	}
	if node.Tag != "!!float" {
		return nil, errors.New("must be a number")
	}
	var value float64
	decoder := json.NewDecoder(strings.NewReader(node.Value))
	if err := decoder.Decode(&value); err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return nil, errors.New("must be a finite canonical JSON number")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("must be a finite canonical JSON number")
	}
	return value, nil
}

func canonicalInteger(value string) bool {
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

func validSchemaType(value string) bool {
	switch value {
	case "string", "integer", "number", "boolean", "object", "array":
		return true
	default:
		return false
	}
}

func validFieldName(value string) bool {
	if value == "" || len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousUnderscore := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousUnderscore = false
		case character == '_' && !previousUnderscore:
			previousUnderscore = true
		default:
			return false
		}
	}
	return !previousUnderscore
}
