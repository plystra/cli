// Package interfacedigest calculates versioned exact Interface contract digests
// from normalized Go contracts and compatibility metadata.
package interfacedigest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacemeta"
)

const canonicalSchema = "plystra.interface.contract/v1"

// ErrInvalid reports normalized input that cannot form an exact Interface
// contract digest.
var ErrInvalid = errors.New("invalid Interface contract digest input")

// Calculate returns the SHA-256 digest of the versioned canonical Interface
// contract representation. Documentation-only metadata and examples are not
// inputs to this digest.
func Calculate(contract interfacecontract.Contract, metadata interfacemeta.Document, constraints []interfacemeta.ConstraintTarget) (string, error) {
	canonical, err := canonicalize(contract, metadata, constraints)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

type canonicalDocument struct {
	Schema      string                `json:"schema"`
	ID          string                `json:"interface_id"`
	Method      canonicalMethod       `json:"method"`
	Messages    []canonicalMessage    `json:"messages"`
	Semantics   *canonicalSemantics   `json:"semantics"`
	Errors      []string              `json:"semantic_errors"`
	Constraints []canonicalConstraint `json:"constraints"`
	Conformance *canonicalConformance `json:"behavioral_conformance"`
}

type canonicalMethod struct {
	Name         string `json:"name"`
	ContextType  string `json:"context_type"`
	RequestType  string `json:"request_type"`
	ResponseType string `json:"response_type"`
	ErrorType    string `json:"error_type"`
}

type canonicalMessage struct {
	Name   string           `json:"name"`
	Fields []canonicalField `json:"fields"`
}

type canonicalField struct {
	Number   uint64 `json:"number"`
	GoName   string `json:"go_name"`
	JSONName string `json:"json_name"`
	Required bool   `json:"required"`
	Type     string `json:"type"`
}

type canonicalSemantics struct {
	Kind string `json:"kind"`
}

type canonicalConstraint struct {
	Path  string          `json:"path"`
	Rules []canonicalRule `json:"rules"`
}

type canonicalRule struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type canonicalConformance struct {
	Package string `json:"package"`
}

func canonicalize(contract interfacecontract.Contract, metadata interfacemeta.Document, constraints []interfacemeta.ConstraintTarget) ([]byte, error) {
	if contract.ID().String() == "" || contract.MethodName() == "" || contract.RequestName() == "" || contract.ResponseName() == "" {
		return nil, fmt.Errorf("%w: a complete normalized Interface contract is required", ErrInvalid)
	}

	messages := contract.Messages()
	canonicalMessages := make([]canonicalMessage, len(messages))
	for messageIndex, message := range messages {
		if message.Name() == "" {
			return nil, fmt.Errorf("%w: canonical message name is empty", ErrInvalid)
		}
		fields := message.Fields()
		canonicalFields := make([]canonicalField, len(fields))
		for fieldIndex, field := range fields {
			typeName := field.Type().Canonical()
			if field.Name() == "" || field.Number() == 0 || typeName == "" {
				return nil, fmt.Errorf("%w: message %s has an incomplete canonical field", ErrInvalid, message.Name())
			}
			jsonName := field.Name()
			if field.HasExplicitJSONName() {
				jsonName = field.JSONName()
			}
			canonicalFields[fieldIndex] = canonicalField{
				Number:   field.Number(),
				GoName:   field.Name(),
				JSONName: jsonName,
				Required: field.Required(),
				Type:     typeName,
			}
		}
		canonicalMessages[messageIndex] = canonicalMessage{Name: message.Name(), Fields: canonicalFields}
	}

	var semantics *canonicalSemantics
	if normalized, present := metadata.Semantics(); present {
		semantics = &canonicalSemantics{Kind: string(normalized.Kind())}
	}

	semanticErrors := metadata.Errors()
	canonicalErrors := make([]string, len(semanticErrors))
	for index, semanticError := range semanticErrors {
		canonicalErrors[index] = semanticError.Code()
	}
	sort.Strings(canonicalErrors)

	canonicalConstraints := make([]canonicalConstraint, 0, len(constraints))
	for _, target := range constraints {
		rules := canonicalRules(target.Rules())
		if len(rules) == 0 {
			continue
		}
		if target.Path() == "" {
			return nil, fmt.Errorf("%w: canonical constraint path is empty", ErrInvalid)
		}
		canonicalConstraints = append(canonicalConstraints, canonicalConstraint{Path: target.Path(), Rules: rules})
	}
	sort.Slice(canonicalConstraints, func(left, right int) bool {
		return canonicalConstraints[left].Path < canonicalConstraints[right].Path
	})

	var conformance *canonicalConformance
	if normalized, present := metadata.Conformance(); present {
		if normalized.Package() == "" {
			return nil, fmt.Errorf("%w: canonical Behavioral Conformance package is empty", ErrInvalid)
		}
		conformance = &canonicalConformance{Package: normalized.Package()}
	}

	canonical, err := json.Marshal(canonicalDocument{
		Schema: canonicalSchema,
		ID:     contract.ID().String(),
		Method: canonicalMethod{
			Name:         contract.MethodName(),
			ContextType:  "context.Context",
			RequestType:  contract.RequestName(),
			ResponseType: contract.ResponseName(),
			ErrorType:    "error",
		},
		Messages:    canonicalMessages,
		Semantics:   semantics,
		Errors:      canonicalErrors,
		Constraints: canonicalConstraints,
		Conformance: conformance,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode canonical contract: %w", ErrInvalid, err)
	}
	return canonical, nil
}

func canonicalRules(rules interfacemeta.ConstraintRules) []canonicalRule {
	result := make([]canonicalRule, 0, 7)
	if value, present := rules.MinLength(); present {
		result = append(result, canonicalRule{Name: "min_length", Value: strconv.FormatUint(uint64(value), 10)})
	}
	if value, present := rules.MaxLength(); present {
		result = append(result, canonicalRule{Name: "max_length", Value: strconv.FormatUint(uint64(value), 10)})
	}
	if value, present := rules.Pattern(); present {
		result = append(result, canonicalRule{Name: "pattern", Value: value})
	}
	if value, present := rules.Minimum(); present {
		result = append(result, canonicalRule{Name: "minimum", Value: value.Canonical()})
	}
	if value, present := rules.Maximum(); present {
		result = append(result, canonicalRule{Name: "maximum", Value: value.Canonical()})
	}
	if value, present := rules.MinItems(); present {
		result = append(result, canonicalRule{Name: "min_items", Value: strconv.FormatUint(uint64(value), 10)})
	}
	if value, present := rules.MaxItems(); present {
		result = append(result, canonicalRule{Name: "max_items", Value: strconv.FormatUint(uint64(value), 10)})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result
}
