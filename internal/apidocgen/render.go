// Package apidocgen renders deterministic application-local HTTP Capability
// documentation from provider-independent canonical contracts and the final
// Capability Alias map.
package apidocgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"sort"
	"strings"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/sdkmodel"
	"github.com/plystra/cli/internal/transportprovenance"
)

const (
	// GeneratorVersion is the exact built-in documentation projection version
	// recorded in generated application provenance.
	GeneratorVersion = "plystra.api-documentation/v1"
	// InterfaceReferencePath is the managed application-local API reference.
	InterfaceReferencePath = "generated/docs/api.md"
	// OpenAPIPath is the managed application-local OpenAPI projection.
	OpenAPIPath        = "generated/docs/openapi.json"
	maximumOperations  = 4096
	openAPIVersion     = "3.1.0"
	applicationVersion = "0.0.0"
)

// ErrRender reports that a complete application API reference could not be
// rendered from validated canonical and Alias inputs.
var ErrRender = errors.New("render generated application API documentation")

// AliasView is one final application-local Alias mapping. The final
// aliasresolution.Alias value satisfies this interface.
type AliasView interface {
	ID() generation.CapabilityID
	Target() generation.CapabilityID
	TargetContractDigest() string
	Exposure() generation.Exposure
	Deprecated() string
}

// File is one immutable application-relative generated documentation file.
type File struct {
	path string
	data []byte
}

// Path returns the slash-separated generated file path.
func (f File) Path() string { return f.path }

// Data returns a defensive copy of generated file bytes.
func (f File) Data() []byte { return append([]byte(nil), f.data...) }

type renderedOperation struct {
	id          generation.CapabilityID
	target      generation.CapabilityID
	operation   sdkmodel.Operation
	exposure    generation.Exposure
	deprecated  string
	operationID string
}

func (o renderedOperation) isAlias() bool { return o.id != o.target }

// Render emits generated/docs/api.md and generated/docs/openapi.json. The SDK
// model supplies exact canonical schemas; aliases supplies the complete final
// map so HTTP-only exposure narrowing remains visible even when an Alias is
// intentionally absent from browser SDKs.
func Render(model sdkmodel.Model, aliases []AliasView, configurationProvenance transportprovenance.Provenance) ([]File, error) {
	if !configurationProvenance.Valid() {
		return nil, fmt.Errorf("%w: selected configuration provenance is absent or invalid", ErrRender)
	}
	canonical := model.CanonicalJSON()
	if len(canonical) == 0 || model.Digest() != digest(canonical) {
		return nil, fmt.Errorf("%w: canonical application model is absent or has an invalid digest", ErrRender)
	}
	operations, err := prepareOperations(model, aliases)
	if err != nil {
		return nil, err
	}
	openAPI, err := renderOpenAPI(operations)
	if err != nil {
		return nil, err
	}
	files := []File{
		newFile(InterfaceReferencePath, renderMarkdown(operations)),
		newFile(OpenAPIPath, openAPI),
	}
	return files, nil
}

func prepareOperations(model sdkmodel.Model, aliasViews []AliasView) ([]renderedOperation, error) {
	canonical := model.Operations()
	if len(canonical)+len(aliasViews) > maximumOperations {
		return nil, fmt.Errorf("%w: %d canonical and Alias operations exceeds maximum %d", ErrRender, len(canonical)+len(aliasViews), maximumOperations)
	}
	targets := make(map[generation.CapabilityID]sdkmodel.Operation, len(canonical))
	operations := make([]renderedOperation, 0, len(canonical)+len(aliasViews))
	seen := make(map[generation.CapabilityID]int, len(canonical)+len(aliasViews))
	for index, operation := range canonical {
		id, err := generation.ParseCapabilityID(operation.ID().String())
		if err != nil || operation.ContractDigest() == "" {
			return nil, fmt.Errorf("%w: canonical operations[%d] is incomplete", ErrRender, index)
		}
		if previous, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("%w: canonical operations[%d] duplicates %s from operations[%d]", ErrRender, index, id, previous)
		}
		seen[id] = index
		targets[id] = operation
		operations = append(operations, renderedOperation{
			id:        id,
			target:    id,
			operation: operation,
			exposure:  generation.Exposure{HTTP: true, JavaScript: true},
		})
	}
	finalAliases := make(map[generation.CapabilityID]renderedOperation, len(aliasViews))
	for index, view := range aliasViews {
		operation, include, err := normalizeAlias(index, view, targets)
		if err != nil {
			return nil, err
		}
		if !include {
			continue
		}
		if previous, duplicate := seen[operation.id]; duplicate {
			return nil, fmt.Errorf("%w: aliases[%d] duplicates or collides at %s with input %d", ErrRender, index, operation.id, previous)
		}
		seen[operation.id] = len(canonical) + index
		finalAliases[operation.id] = operation
		operations = append(operations, operation)
	}
	for _, expected := range model.Aliases() {
		actual, exists := finalAliases[expected.ID()]
		if !exists || actual.target != expected.Target() || actual.operation.ContractDigest() != expected.TargetContractDigest() || actual.deprecated != expected.Deprecated() || actual.exposure != expected.Exposure() {
			return nil, fmt.Errorf("%w: final Alias map disagrees with HTTP documentation Alias %s", ErrRender, expected.ID())
		}
	}
	sort.Slice(operations, func(left, right int) bool {
		return operations[left].id.String() < operations[right].id.String()
	})
	operationIDCounts := make(map[string]int, len(operations))
	for index := range operations {
		operations[index].operationID = openAPIOperationID(operations[index].id)
		operationIDCounts[operations[index].operationID]++
	}
	for index := range operations {
		if operationIDCounts[operations[index].operationID] > 1 {
			operations[index].operationID += "_" + hex.EncodeToString([]byte(operations[index].id.String()))
		}
	}
	return operations, nil
}

func normalizeAlias(index int, view AliasView, targets map[generation.CapabilityID]sdkmodel.Operation) (renderedOperation, bool, error) {
	field := fmt.Sprintf("aliases[%d]", index)
	if view == nil {
		return renderedOperation{}, false, fmt.Errorf("%w: %s view is absent", ErrRender, field)
	}
	id, err := generation.ParseCapabilityID(view.ID().String())
	if err != nil {
		return renderedOperation{}, false, fmt.Errorf("%w: %s ID %q is not canonical: %v", ErrRender, field, view.ID().String(), err)
	}
	targetID, err := generation.ParseCapabilityID(view.Target().String())
	if err != nil {
		return renderedOperation{}, false, fmt.Errorf("%w: %s target ID %q is not canonical: %v", ErrRender, field, view.Target().String(), err)
	}
	deprecated := view.Deprecated()
	if len(deprecated) > 1024 || !utf8.ValidString(deprecated) || strings.ContainsRune(deprecated, '\x00') {
		return renderedOperation{}, false, fmt.Errorf("%w: %s %s deprecation metadata is invalid", ErrRender, field, id)
	}
	exposure := view.Exposure()
	if !exposure.HTTP {
		return renderedOperation{}, false, nil
	}
	target, exists := targets[targetID]
	if !exists {
		return renderedOperation{}, false, fmt.Errorf("%w: %s %s target %s is not a documented canonical HTTP operation", ErrRender, field, id, targetID)
	}
	if _, collision := targets[id]; collision {
		return renderedOperation{}, false, fmt.Errorf("%w: %s %s collides with a canonical Capability", ErrRender, field, id)
	}
	if strings.HasPrefix(id.Name(), "kernel.") {
		return renderedOperation{}, false, fmt.Errorf("%w: %s %s uses the reserved kernel.* namespace", ErrRender, field, id)
	}
	if id == targetID {
		return renderedOperation{}, false, fmt.Errorf("%w: %s %s cannot target itself", ErrRender, field, id)
	}
	if id.Major() != targetID.Major() {
		return renderedOperation{}, false, fmt.Errorf("%w: %s %s and target %s do not use the same version", ErrRender, field, id, targetID)
	}
	if view.TargetContractDigest() != target.ContractDigest() {
		return renderedOperation{}, false, fmt.Errorf("%w: %s %s target digest %q does not match %s", ErrRender, field, id, view.TargetContractDigest(), target.ContractDigest())
	}
	return renderedOperation{
		id:         id,
		target:     targetID,
		operation:  target,
		exposure:   exposure,
		deprecated: deprecated,
	}, true, nil
}

type openAPIDocument struct {
	OpenAPI    string                 `json:"openapi"`
	Info       openAPIInfo            `json:"info"`
	Paths      map[string]openAPIPath `json:"paths"`
	Components openAPIComponents      `json:"components"`
	Generated  bool                   `json:"x-plystra-generated"`
}

type openAPIInfo struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

type openAPIPath struct {
	Post openAPIOperation `json:"post"`
}

type openAPIOperation struct {
	OperationID     string                     `json:"operationId"`
	Summary         string                     `json:"summary"`
	Description     string                     `json:"description,omitempty"`
	Deprecated      bool                       `json:"deprecated,omitempty"`
	CapabilityID    string                     `json:"x-plystra-capability-id"`
	CanonicalTarget string                     `json:"x-plystra-canonical-target,omitempty"`
	ContractDigest  string                     `json:"x-plystra-contract-digest"`
	SemanticErrors  []string                   `json:"x-plystra-semantic-errors,omitempty"`
	RequestBody     openAPIRequestBody         `json:"requestBody"`
	Responses       map[string]openAPIResponse `json:"responses"`
}

type openAPIRequestBody struct {
	Required bool                        `json:"required"`
	Content  map[string]openAPIMediaType `json:"content"`
}

type openAPIResponse struct {
	Description string                      `json:"description"`
	Content     map[string]openAPIMediaType `json:"content,omitempty"`
}

type openAPIMediaType struct {
	Schema openAPISchema `json:"schema"`
}

type openAPISchema struct {
	Ref                  string                   `json:"$ref,omitempty"`
	Type                 string                   `json:"type,omitempty"`
	Properties           map[string]openAPISchema `json:"properties,omitempty"`
	Required             []string                 `json:"required,omitempty"`
	Items                *openAPISchema           `json:"items,omitempty"`
	Enum                 []any                    `json:"enum,omitempty"`
	AdditionalProperties any                      `json:"additionalProperties,omitempty"`
}

type openAPIComponents struct {
	Schemas map[string]openAPISchema `json:"schemas"`
}

func renderOpenAPI(operations []renderedOperation) ([]byte, error) {
	paths := make(map[string]openAPIPath, len(operations))
	for _, operation := range operations {
		request, err := openAPIObjectSchema(operation.operation.Request())
		if err != nil {
			return nil, fmt.Errorf("%w: %s request schema: %v", ErrRender, operation.id, err)
		}
		response, err := openAPIObjectSchema(operation.operation.Response())
		if err != nil {
			return nil, fmt.Errorf("%w: %s response schema: %v", ErrRender, operation.id, err)
		}
		description := "Invoke the exact canonical Capability contract."
		canonicalTarget := ""
		if operation.isAlias() {
			canonicalTarget = operation.target.String()
			description = "Application-local Alias for " + operation.target.String() + "."
			if operation.deprecated != "" {
				description += " Deprecated: " + operation.deprecated
			}
		}
		paths[routePath(operation.id)] = openAPIPath{Post: openAPIOperation{
			OperationID:     operation.operationID,
			Summary:         "Invoke " + operation.id.String(),
			Description:     description,
			Deprecated:      operation.deprecated != "",
			CapabilityID:    operation.id.String(),
			CanonicalTarget: canonicalTarget,
			ContractDigest:  operation.operation.ContractDigest(),
			SemanticErrors:  operation.operation.Errors(),
			RequestBody: openAPIRequestBody{
				Required: true,
				Content: map[string]openAPIMediaType{
					"application/json": {Schema: request},
				},
			},
			Responses: map[string]openAPIResponse{
				"200": {
					Description: "Successful exact Capability response.",
					Content: map[string]openAPIMediaType{
						"application/json": {Schema: response},
					},
				},
				"default": {
					Description: "Stable transport or invocation error.",
					Content: map[string]openAPIMediaType{
						"application/json": {Schema: openAPISchema{Ref: "#/components/schemas/ErrorEnvelope"}},
					},
				},
			},
		}}
	}
	document := openAPIDocument{
		OpenAPI: openAPIVersion,
		Info: openAPIInfo{
			Title:   "Plystra Application API",
			Version: applicationVersion,
		},
		Paths:      paths,
		Components: openAPIComponents{Schemas: errorSchemas()},
		Generated:  true,
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("%w: encode OpenAPI: %v", ErrRender, err)
	}
	return append(encoded, '\n'), nil
}

func openAPIObjectSchema(fields []sdkmodel.Field) (openAPISchema, error) {
	properties := make(map[string]openAPISchema, len(fields))
	required := make([]string, 0, len(fields))
	for _, field := range fields {
		schema, err := openAPIFieldSchema(field)
		if err != nil {
			return openAPISchema{}, err
		}
		properties[field.Name()] = schema
		if field.Required() {
			required = append(required, field.Name())
		}
	}
	return openAPISchema{
		Type:                 "object",
		Properties:           properties,
		Required:             required,
		AdditionalProperties: false,
	}, nil
}

func openAPIFieldSchema(field sdkmodel.Field) (openAPISchema, error) {
	schema := openAPISchema{}
	if field.Kind() == sdkmodel.KindArray {
		items := openAPISchema{Type: openAPIType(field.Items())}
		if field.Items() == sdkmodel.KindObject {
			items.AdditionalProperties = &openAPISchema{}
		}
		schema.Type = "array"
		schema.Items = &items
	} else {
		schema.Type = openAPIType(field.Kind())
		if field.Kind() == sdkmodel.KindObject {
			schema.AdditionalProperties = &openAPISchema{}
		}
	}
	for _, raw := range field.EnumJSON() {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return openAPISchema{}, fmt.Errorf("field %q enum value %s is invalid: %v", field.Name(), raw, err)
		}
		schema.Enum = append(schema.Enum, value)
	}
	return schema, nil
}

func openAPIType(kind sdkmodel.Kind) string {
	switch kind {
	case sdkmodel.KindString:
		return "string"
	case sdkmodel.KindInteger:
		return "integer"
	case sdkmodel.KindNumber:
		return "number"
	case sdkmodel.KindBoolean:
		return "boolean"
	case sdkmodel.KindObject:
		return "object"
	default:
		panic("unsupported normalized API kind " + string(kind))
	}
}

func errorSchemas() map[string]openAPISchema {
	return map[string]openAPISchema{
		"Error": {
			Type: "object",
			Properties: map[string]openAPISchema{
				"code":        {Type: "string"},
				"detail_code": {Type: "string"},
			},
			Required:             []string{"code"},
			AdditionalProperties: false,
		},
		"ErrorEnvelope": {
			Type: "object",
			Properties: map[string]openAPISchema{
				"error": {Ref: "#/components/schemas/Error"},
			},
			Required:             []string{"error"},
			AdditionalProperties: false,
		},
	}
}

func renderMarkdown(operations []renderedOperation) []byte {
	var document strings.Builder
	fmt.Fprintln(&document, "<!-- Code generated by Plystra CLI. DO NOT EDIT. -->")
	fmt.Fprintln(&document)
	fmt.Fprintln(&document, "# Application API")
	fmt.Fprintln(&document)
	fmt.Fprintln(&document, "This application-local reference lists exact generated HTTP Capability routes. Alias routes reuse their direct canonical target contract and invocation path. Provider identities, runtime configuration, verified internal context, and Secret values are intentionally absent.")
	fmt.Fprintln(&document)
	fmt.Fprintln(&document, "## Canonical capabilities")
	canonicalCount := 0
	for _, operation := range operations {
		if operation.isAlias() {
			continue
		}
		canonicalCount++
		renderMarkdownOperation(&document, operation)
	}
	if canonicalCount == 0 {
		fmt.Fprintln(&document)
		fmt.Fprintln(&document, "No canonical HTTP Capabilities are exposed.")
	}
	aliasCount := 0
	for _, operation := range operations {
		if operation.isAlias() {
			aliasCount++
		}
	}
	if aliasCount != 0 {
		fmt.Fprintln(&document)
		fmt.Fprintln(&document, "## Capability aliases")
		for _, operation := range operations {
			if operation.isAlias() {
				renderMarkdownOperation(&document, operation)
			}
		}
	}
	return []byte(document.String())
}

func renderMarkdownOperation(document *strings.Builder, operation renderedOperation) {
	fmt.Fprintln(document)
	fmt.Fprintf(document, "### `%s`", operation.id)
	if operation.deprecated != "" {
		fmt.Fprint(document, " [deprecated]")
	}
	fmt.Fprintln(document)
	fmt.Fprintln(document)
	if operation.isAlias() {
		fmt.Fprintf(document, "Application-local Alias of `%s`.\n\n", operation.target)
	}
	fmt.Fprintf(document, "- Route: `POST %s`\n", routePath(operation.id))
	fmt.Fprintf(document, "- Canonical contract digest: `%s`\n", operation.operation.ContractDigest())
	if operation.isAlias() {
		var surfaces []string
		if operation.exposure.Go {
			surfaces = append(surfaces, "Go")
		}
		if operation.exposure.HTTP {
			surfaces = append(surfaces, "HTTP")
		}
		if operation.exposure.JavaScript {
			surfaces = append(surfaces, "JavaScript")
		}
		fmt.Fprintf(document, "- Generated surfaces: %s\n", strings.Join(surfaces, ", "))
	}
	if operation.deprecated != "" {
		fmt.Fprintf(document, "- Deprecation: %s\n", markdownText(operation.deprecated))
	}
	renderMarkdownFields(document, "Request", operation.operation.Request())
	renderMarkdownFields(document, "Response", operation.operation.Response())
	errors := operation.operation.Errors()
	fmt.Fprintln(document)
	fmt.Fprintln(document, "#### Semantic errors")
	fmt.Fprintln(document)
	if len(errors) == 0 {
		fmt.Fprintln(document, "None declared.")
		return
	}
	for _, code := range errors {
		fmt.Fprintf(document, "- `%s`\n", code)
	}
}

func renderMarkdownFields(document *strings.Builder, section string, fields []sdkmodel.Field) {
	fmt.Fprintln(document)
	fmt.Fprintf(document, "#### %s\n\n", section)
	if len(fields) == 0 {
		fmt.Fprintln(document, "No fields.")
		return
	}
	fmt.Fprintln(document, "| Field | Type | Required | Enum |")
	fmt.Fprintln(document, "| --- | --- | --- | --- |")
	for _, field := range fields {
		enum := "-"
		if values := field.EnumJSON(); len(values) != 0 {
			encoded := make([]string, len(values))
			for index, value := range values {
				encoded[index] = "<code>" + html.EscapeString(string(value)) + "</code>"
			}
			enum = strings.Join(encoded, ", ")
		}
		required := "no"
		if field.Required() {
			required = "yes"
		}
		fmt.Fprintf(document, "| <code>%s</code> | `%s` | %s | %s |\n", html.EscapeString(field.Name()), markdownType(field), required, enum)
	}
}

func markdownType(field sdkmodel.Field) string {
	if field.Kind() == sdkmodel.KindArray {
		return "array<" + string(field.Items()) + ">"
	}
	return string(field.Kind())
}

func markdownText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = html.EscapeString(value)
	value = strings.ReplaceAll(value, "|", "&#124;")
	value = strings.ReplaceAll(value, "\n", "<br>")
	return value
}

func openAPIOperationID(id generation.CapabilityID) string {
	var result strings.Builder
	result.WriteString("invoke_")
	for _, character := range id.String() {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			result.WriteRune(character)
		default:
			result.WriteByte('_')
		}
	}
	return strings.TrimRight(result.String(), "_")
}

func routePath(id generation.CapabilityID) string {
	return "/api/v1/capabilities/" + id.String() + "/invoke"
}

func newFile(filePath string, data []byte) File {
	return File{path: filePath, data: append([]byte(nil), data...)}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
