// Package interfacedigest calculates versioned Interface contract,
// documentation, and example digests from normalized typed inputs.
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

const (
	canonicalContractSchema      = "plystra.interface.contract/v1"
	canonicalDocumentationSchema = "plystra.interface.documentation/v1"
	canonicalExamplesSchema      = "plystra.interface.examples/v1"
)

// ErrInvalid reports normalized input that cannot form an Interface digest.
var ErrInvalid = errors.New("invalid Interface digest input")

// CalculateContract returns the SHA-256 digest of the versioned canonical
// Interface contract representation. Documentation-only metadata and examples
// are not inputs to this digest.
func CalculateContract(contract interfacecontract.Contract, metadata interfacemeta.Document, constraints []interfacemeta.ConstraintTarget) (string, error) {
	canonical, err := canonicalizeContract(contract, metadata, constraints)
	if err != nil {
		return "", err
	}
	return digest(canonical), nil
}

// CalculateDocumentation returns the SHA-256 digest of normalized public
// descriptions and deprecation presentation. Exact contract facts and examples
// are not inputs to this digest.
func CalculateDocumentation(contract interfacecontract.Contract, metadata interfacemeta.Document) (string, error) {
	canonical, err := canonicalizeDocumentation(contract, metadata)
	if err != nil {
		return "", err
	}
	return digest(canonical), nil
}

// CalculateExamples returns the SHA-256 digest of normalized validated
// request-and-outcome examples. Exact contract and documentation presentation
// are not inputs to this digest.
func CalculateExamples(contract interfacecontract.Contract, examples []interfacemeta.Example) (string, error) {
	canonical, err := canonicalizeExamples(contract, examples)
	if err != nil {
		return "", err
	}
	return digest(canonical), nil
}

type canonicalContractDocument struct {
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

type canonicalDocumentationDocument struct {
	Schema                    string                      `json:"schema"`
	ID                        string                      `json:"interface_id"`
	Description               *string                     `json:"description"`
	SemanticErrorDescriptions []canonicalErrorDescription `json:"semantic_error_descriptions"`
	Deprecation               *canonicalDeprecation       `json:"deprecation"`
}

type canonicalErrorDescription struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

type canonicalDeprecation struct {
	Message     string  `json:"message"`
	Replacement *string `json:"replacement"`
	Since       *string `json:"since"`
}

type canonicalExamplesDocument struct {
	Schema   string             `json:"schema"`
	ID       string             `json:"interface_id"`
	Examples []canonicalExample `json:"examples"`
}

type canonicalExample struct {
	Name      string          `json:"name"`
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
}

func canonicalizeContract(contract interfacecontract.Contract, metadata interfacemeta.Document, constraints []interfacemeta.ConstraintTarget) ([]byte, error) {
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

	canonical, err := json.Marshal(canonicalContractDocument{
		Schema: canonicalContractSchema,
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

func canonicalizeDocumentation(contract interfacecontract.Contract, metadata interfacemeta.Document) ([]byte, error) {
	identifier := contract.ID().String()
	if identifier == "" {
		return nil, fmt.Errorf("%w: a normalized Interface identity is required", ErrInvalid)
	}

	var description *string
	if value, present := metadata.Description(); present {
		description = stringPointer(value)
	}

	semanticErrors := metadata.Errors()
	errorDescriptions := make([]canonicalErrorDescription, 0, len(semanticErrors))
	for _, semanticError := range semanticErrors {
		value, present := semanticError.Description()
		if !present {
			continue
		}
		errorDescriptions = append(errorDescriptions, canonicalErrorDescription{
			Code:        semanticError.Code(),
			Description: value,
		})
	}
	sort.Slice(errorDescriptions, func(left, right int) bool {
		return errorDescriptions[left].Code < errorDescriptions[right].Code
	})

	var deprecation *canonicalDeprecation
	if value, present := metadata.Deprecation(); present {
		deprecation = &canonicalDeprecation{Message: value.Message()}
		if replacement, exists := value.Replacement(); exists {
			deprecation.Replacement = stringPointer(replacement.String())
		}
		if since, exists := value.Since(); exists {
			deprecation.Since = stringPointer(since)
		}
	}

	canonical, err := json.Marshal(canonicalDocumentationDocument{
		Schema:                    canonicalDocumentationSchema,
		ID:                        identifier,
		Description:               description,
		SemanticErrorDescriptions: errorDescriptions,
		Deprecation:               deprecation,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode canonical documentation: %w", ErrInvalid, err)
	}
	return canonical, nil
}

func canonicalizeExamples(contract interfacecontract.Contract, examples []interfacemeta.Example) ([]byte, error) {
	identifier := contract.ID().String()
	if identifier == "" {
		return nil, fmt.Errorf("%w: a normalized Interface identity is required", ErrInvalid)
	}

	canonicalExamples := make([]canonicalExample, len(examples))
	seen := make(map[string]struct{}, len(examples))
	for index, example := range examples {
		if example.Name() == "" {
			return nil, fmt.Errorf("%w: canonical example name is empty", ErrInvalid)
		}
		if _, duplicate := seen[example.Name()]; duplicate {
			return nil, fmt.Errorf("%w: duplicate canonical example name %q", ErrInvalid, example.Name())
		}
		seen[example.Name()] = struct{}{}

		request := json.RawMessage(example.Request().CanonicalJSON())
		if !json.Valid(request) {
			return nil, fmt.Errorf("%w: example %q has an invalid canonical request", ErrInvalid, example.Name())
		}
		normalizedExample := canonicalExample{Name: example.Name(), Request: request}
		if response, present := example.Response(); present {
			normalizedExample.Response = json.RawMessage(response.CanonicalJSON())
			if !json.Valid(normalizedExample.Response) {
				return nil, fmt.Errorf("%w: example %q has an invalid canonical response", ErrInvalid, example.Name())
			}
		} else if code, present := example.ErrorCode(); present {
			normalizedExample.ErrorCode = code
		} else {
			return nil, fmt.Errorf("%w: example %q has no canonical outcome", ErrInvalid, example.Name())
		}
		canonicalExamples[index] = normalizedExample
	}
	sort.Slice(canonicalExamples, func(left, right int) bool {
		return canonicalExamples[left].Name < canonicalExamples[right].Name
	})

	canonical, err := json.Marshal(canonicalExamplesDocument{
		Schema:   canonicalExamplesSchema,
		ID:       identifier,
		Examples: canonicalExamples,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode canonical examples: %w", ErrInvalid, err)
	}
	return canonical, nil
}

func digest(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func stringPointer(value string) *string { return &value }

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
