package interfacemeta

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/interfacecontract"
	"go.yaml.in/yaml/v3"
)

// ErrInvalidConstraints reports an invalid Interface field-constraint
// declaration or target path.
var ErrInvalidConstraints = errors.New("invalid Interface constraints")

type constraintPathDeclaration struct {
	path   string
	line   int
	column int
}

// ConstraintTarget is one immutable metadata path resolved to its exact
// canonical Go field.
type ConstraintTarget struct {
	path   string
	goPath string
	field  interfacecontract.Field
}

// Path returns the exact authored request or response metadata field path.
func (t ConstraintTarget) Path() string { return t.path }

// GoPath returns the stable request or response Go field path.
func (t ConstraintTarget) GoPath() string { return t.goPath }

// Field returns the normalized canonical Go field selected by the path.
func (t ConstraintTarget) Field() interfacecontract.Field { return t.field }

// ResolveConstraintTargets resolves every structurally valid constraints key
// against the canonical request and response message graphs. Returned targets
// are ordered by metadata path and Go path.
func ResolveConstraintTargets(document Document, contract interfacecontract.Contract) ([]ConstraintTarget, error) {
	if len(document.constraintPaths) == 0 {
		return nil, nil
	}
	if contract.ID().String() == "" || contract.RequestName() == "" || contract.ResponseName() == "" {
		return nil, invalidWith(document.path, 0, 0, ErrInvalidConstraints, "a canonical Interface contract is required to resolve constraint paths")
	}

	targets := make([]ConstraintTarget, 0, len(document.constraintPaths))
	for _, declaration := range document.constraintPaths {
		matches, err := resolveConstraintPath(contract, declaration.path)
		if err != nil {
			return nil, invalidWith(document.path, declaration.line, declaration.column, ErrInvalidConstraints, "%v", err)
		}
		if len(matches) == 0 {
			return nil, invalidWith(document.path, declaration.line, declaration.column, ErrInvalidConstraints, "constraint path %q does not identify a canonical request or response field by its effective JSON name", declaration.path)
		}
		if len(matches) > 1 {
			paths := make([]string, len(matches))
			for index, match := range matches {
				paths[index] = match.goPath
			}
			sort.Strings(paths)
			return nil, invalidWith(document.path, declaration.line, declaration.column, ErrInvalidConstraints, "constraint path %q is ambiguous between Go fields %s; use unambiguous JSON names", declaration.path, strings.Join(paths, ", "))
		}
		targets = append(targets, ConstraintTarget{
			path:   declaration.path,
			goPath: matches[0].goPath,
			field:  matches[0].field,
		})
	}
	sort.Slice(targets, func(left, right int) bool {
		if targets[left].path != targets[right].path {
			return targets[left].path < targets[right].path
		}
		return targets[left].goPath < targets[right].goPath
	})
	return targets, nil
}

func parseConstraintPathDeclarations(sourcePath string, root *yaml.Node) ([]constraintPathDeclaration, error) {
	var value *yaml.Node
	for index := 0; index < len(root.Content); index += 2 {
		if root.Content[index].Value == "constraints" {
			value = root.Content[index+1]
			break
		}
	}
	if value == nil {
		return nil, nil
	}
	if value.Kind != yaml.MappingNode || value.Tag != "!!map" {
		return nil, invalidWith(sourcePath, value.Line, value.Column, ErrInvalidConstraints, "constraints must be a mapping from request or response field paths to constraint mappings")
	}
	declarations := make([]constraintPathDeclaration, 0, len(value.Content)/2)
	for index := 0; index < len(value.Content); index += 2 {
		key, rules := value.Content[index], value.Content[index+1]
		if rules.Kind != yaml.MappingNode || rules.Tag != "!!map" {
			return nil, invalidWith(sourcePath, rules.Line, rules.Column, ErrInvalidConstraints, "constraint rules for path %q must be a mapping", key.Value)
		}
		declarations = append(declarations, constraintPathDeclaration{
			path:   key.Value,
			line:   key.Line,
			column: key.Column,
		})
	}
	return declarations, nil
}

type constraintMatch struct {
	goPath string
	field  interfacecontract.Field
}

func resolveConstraintPath(contract interfacecontract.Contract, fieldPath string) ([]constraintMatch, error) {
	root, remainder, exists := strings.Cut(fieldPath, ".")
	if !exists || remainder == "" || root != "request" && root != "response" {
		return nil, fmt.Errorf("constraint path %q must begin with request. or response. and identify at least one field", fieldPath)
	}
	if root == "request" {
		return resolveConstraintMatches(contract, contract.RequestFields(), contract.RequestName(), remainder), nil
	}
	return resolveConstraintMatches(contract, contract.ResponseFields(), contract.ResponseName(), remainder), nil
}

func resolveConstraintMatches(contract interfacecontract.Contract, fields []interfacecontract.Field, goPath, remainder string) []constraintMatch {
	matches := make([]constraintMatch, 0, 1)
	for _, field := range fields {
		name := field.Name()
		if field.HasExplicitJSONName() {
			name = field.JSONName()
		}
		fieldGoPath := goPath + "." + field.Name()
		if remainder == name {
			matches = append(matches, constraintMatch{goPath: fieldGoPath, field: field})
		}
		prefix := name + "."
		if !strings.HasPrefix(remainder, prefix) {
			continue
		}
		messageName, exists := constraintChildMessage(field.Type())
		if !exists {
			continue
		}
		message, exists := contract.Message(messageName)
		if !exists {
			continue
		}
		matches = append(matches, resolveConstraintMatches(contract, message.Fields(), fieldGoPath, strings.TrimPrefix(remainder, prefix))...)
	}
	return matches
}

func constraintChildMessage(fieldType interfacecontract.Type) (string, bool) {
	switch fieldType.Kind() {
	case interfacecontract.TypeMessage:
		return fieldType.MessageName()
	case interfacecontract.TypeRepeated:
		element, exists := fieldType.Element()
		if exists && element.Kind() == interfacecontract.TypeMessage {
			return element.MessageName()
		}
	case interfacecontract.TypeMap:
		value, exists := fieldType.Value()
		if exists && value.Kind() == interfacecontract.TypeMessage {
			return value.MessageName()
		}
	}
	return "", false
}
