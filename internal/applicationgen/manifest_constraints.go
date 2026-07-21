package applicationgen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	kernelmanifest "github.com/plystra/kernel/plugin/manifest"
)

const applicationManifestConstraintProjectionVersion = 1

type applicationManifestConstraintProjection struct {
	Version      int                                        `json:"version"`
	Digest       string                                     `json:"digest"`
	Capabilities []applicationManifestCapabilityConstraints `json:"capabilities"`
}

type applicationManifestCapabilityConstraints struct {
	ID               string                               `json:"id"`
	ContractDigest   string                               `json:"contract_digest"`
	ConstraintDigest string                               `json:"constraint_digest"`
	Fields           []applicationManifestConstraintField `json:"fields"`
}

type applicationManifestConstraintField struct {
	Path        string                              `json:"path"`
	Type        kernelmanifest.SchemaType           `json:"type"`
	Constraints applicationManifestFieldConstraints `json:"constraints"`
}

type applicationManifestFieldConstraints struct {
	MinLength *uint32         `json:"min_length,omitempty"`
	MaxLength *uint32         `json:"max_length,omitempty"`
	Pattern   *string         `json:"pattern,omitempty"`
	Minimum   json.RawMessage `json:"minimum,omitempty"`
	Maximum   json.RawMessage `json:"maximum,omitempty"`
	MinItems  *uint32         `json:"min_items,omitempty"`
	MaxItems  *uint32         `json:"max_items,omitempty"`
}

type applicationManifestConstraintDigestRecord struct {
	ID               string `json:"id"`
	ConstraintDigest string `json:"constraint_digest"`
}

func buildManifestConstraintProjection(context generation.Context) (applicationManifestConstraintProjection, error) {
	if !validContext(context) {
		return applicationManifestConstraintProjection{}, errors.New("generation context is absent or invalid")
	}
	capabilities := context.Capabilities()
	records := make([]applicationManifestCapabilityConstraints, len(capabilities))
	for index, capability := range capabilities {
		contract := capability.ContractJSON()
		if capability.ContractDigest() != digest(contract) {
			return applicationManifestConstraintProjection{}, fmt.Errorf("capability %s contract digest is inconsistent", capability.ID())
		}
		declaration, err := kernelmanifest.ParseCapability(contract)
		if err != nil {
			return applicationManifestConstraintProjection{}, fmt.Errorf("capability %s canonical contract: %w", capability.ID(), err)
		}
		canonical, err := declaration.CanonicalSchemaJSON()
		if err != nil {
			return applicationManifestConstraintProjection{}, fmt.Errorf("capability %s canonical contract: %w", capability.ID(), err)
		}
		if declaration.ID().String() != capability.ID().String() || !bytes.Equal(canonical, contract) {
			return applicationManifestConstraintProjection{}, fmt.Errorf("capability %s contract is not the authoritative canonical form", capability.ID())
		}
		fields := manifestConstraintFields(declaration)
		constraintDigest, err := manifestConstraintFieldsDigest(fields)
		if err != nil {
			return applicationManifestConstraintProjection{}, fmt.Errorf("capability %s constraint projection: %w", capability.ID(), err)
		}
		records[index] = applicationManifestCapabilityConstraints{
			ID:               capability.ID().String(),
			ContractDigest:   capability.ContractDigest(),
			ConstraintDigest: constraintDigest,
			Fields:           fields,
		}
	}
	projectionDigest, err := manifestConstraintProjectionDigest(records)
	if err != nil {
		return applicationManifestConstraintProjection{}, err
	}
	return applicationManifestConstraintProjection{
		Version:      applicationManifestConstraintProjectionVersion,
		Digest:       projectionDigest,
		Capabilities: records,
	}, nil
}

func manifestConstraintFields(declaration kernelmanifest.Capability) []applicationManifestConstraintField {
	fields := make([]applicationManifestConstraintField, 0)
	appendSchema := func(section string, schema kernelmanifest.Schema) {
		for _, field := range schema.Fields() {
			constraints := field.Constraints()
			if constraints.Empty() {
				continue
			}
			fields = append(fields, applicationManifestConstraintField{
				Path:        section + "." + field.Name(),
				Type:        field.Type(),
				Constraints: manifestFieldConstraints(constraints),
			})
		}
	}
	appendSchema("request", declaration.Request())
	appendSchema("response", declaration.Response())
	sort.Slice(fields, func(left, right int) bool { return fields[left].Path < fields[right].Path })
	return fields
}

func manifestFieldConstraints(constraints kernelmanifest.FieldConstraints) applicationManifestFieldConstraints {
	result := applicationManifestFieldConstraints{}
	if value, exists := constraints.MinLength(); exists {
		result.MinLength = &value
	}
	if value, exists := constraints.MaxLength(); exists {
		result.MaxLength = &value
	}
	if value, exists := constraints.Pattern(); exists {
		result.Pattern = &value
	}
	if value, exists := constraints.Minimum(); exists {
		result.Minimum = json.RawMessage(value.JSON())
	}
	if value, exists := constraints.Maximum(); exists {
		result.Maximum = json.RawMessage(value.JSON())
	}
	if value, exists := constraints.MinItems(); exists {
		result.MinItems = &value
	}
	if value, exists := constraints.MaxItems(); exists {
		result.MaxItems = &value
	}
	return result
}

func manifestConstraintFieldsDigest(fields []applicationManifestConstraintField) (string, error) {
	data, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

func manifestConstraintProjectionDigest(records []applicationManifestCapabilityConstraints) (string, error) {
	canonical := make([]applicationManifestConstraintDigestRecord, len(records))
	for index, record := range records {
		canonical[index] = applicationManifestConstraintDigestRecord{
			ID:               record.ID,
			ConstraintDigest: record.ConstraintDigest,
		}
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

func validateManifestConstraintProjection(projection applicationManifestConstraintProjection) error {
	if projection.Version != applicationManifestConstraintProjectionVersion {
		return fmt.Errorf("must use version %d", applicationManifestConstraintProjectionVersion)
	}
	if projection.Capabilities == nil {
		return errors.New("capabilities must be an array")
	}
	for capabilityIndex, capability := range projection.Capabilities {
		if _, err := generation.ParseCapabilityID(capability.ID); err != nil {
			return fmt.Errorf("capabilities[%d].id is invalid", capabilityIndex)
		}
		if !validSHA256(capability.ContractDigest) || !validSHA256(capability.ConstraintDigest) {
			return fmt.Errorf("capabilities[%d] digests must be lower-case SHA-256 digests", capabilityIndex)
		}
		if capabilityIndex > 0 && projection.Capabilities[capabilityIndex-1].ID >= capability.ID {
			return errors.New("capabilities must be unique and canonically ordered")
		}
		if capability.Fields == nil {
			return fmt.Errorf("capabilities[%d].fields must be an array", capabilityIndex)
		}
		for fieldIndex, field := range capability.Fields {
			if fieldIndex > 0 && capability.Fields[fieldIndex-1].Path >= field.Path {
				return fmt.Errorf("capabilities[%d].fields must be unique and canonically ordered", capabilityIndex)
			}
			if err := validateManifestConstraintField(field); err != nil {
				return fmt.Errorf("capabilities[%d].fields[%d]: %w", capabilityIndex, fieldIndex, err)
			}
		}
		expected, err := manifestConstraintFieldsDigest(capability.Fields)
		if err != nil || expected != capability.ConstraintDigest {
			return fmt.Errorf("capabilities[%d] constraint digest is inconsistent", capabilityIndex)
		}
	}
	expected, err := manifestConstraintProjectionDigest(projection.Capabilities)
	if err != nil || !validSHA256(projection.Digest) || expected != projection.Digest {
		return errors.New("constraint projection digest is inconsistent")
	}
	return nil
}

func validateManifestConstraintField(field applicationManifestConstraintField) error {
	section, name, found := strings.Cut(field.Path, ".")
	if !found || strings.Contains(name, ".") || (section != "request" && section != "response") {
		return errors.New("path must identify one canonical request or response field")
	}
	if manifestFieldConstraintsEmpty(field.Constraints) {
		return errors.New("constraints must not be empty")
	}
	type validationField struct {
		Type        kernelmanifest.SchemaType           `json:"type"`
		Items       kernelmanifest.SchemaType           `json:"items,omitempty"`
		Constraints applicationManifestFieldConstraints `json:"constraints"`
	}
	value := validationField{Type: field.Type, Constraints: field.Constraints}
	if field.Type == kernelmanifest.SchemaArray {
		value.Items = kernelmanifest.SchemaString
	}
	document := struct {
		ID        string                     `json:"id"`
		Request   map[string]validationField `json:"request"`
		Response  map[string]validationField `json:"response"`
		Semantics json.RawMessage            `json:"semantics"`
	}{
		ID:        "manifest.constraint/v1",
		Request:   map[string]validationField{},
		Response:  map[string]validationField{},
		Semantics: json.RawMessage(`{"kind":"query","effects":"none","idempotency":{"mode":"inherent"},"retry":{"safety":"safe"},"cancellation":{"mode":"best-effort"},"completion":{"mode":"completed-before-return"},"ordering":{"mode":"none"},"data":{"request":"public","response":"public"}}`),
	}
	if section == "request" {
		document.Request[name] = value
	} else {
		document.Response[name] = value
	}
	data, err := json.Marshal(document)
	if err != nil {
		return errors.New("constraints cannot be encoded")
	}
	declaration, err := kernelmanifest.ParseCapability(data)
	if err != nil {
		return fmt.Errorf("invalid canonical constraints: %w", err)
	}
	var validated kernelmanifest.SchemaField
	var exists bool
	if section == "request" {
		validated, exists = declaration.Request().Lookup(name)
	} else {
		validated, exists = declaration.Response().Lookup(name)
	}
	if !exists || validated.Type() != field.Type {
		return errors.New("constraint field identity is inconsistent")
	}
	canonical := manifestFieldConstraints(validated.Constraints())
	want, wantErr := json.Marshal(canonical)
	got, gotErr := json.Marshal(field.Constraints)
	if wantErr != nil || gotErr != nil || !bytes.Equal(want, got) {
		return errors.New("constraints are not in canonical form")
	}
	return nil
}

func manifestFieldConstraintsEmpty(constraints applicationManifestFieldConstraints) bool {
	return constraints.MinLength == nil && constraints.MaxLength == nil && constraints.Pattern == nil &&
		len(constraints.Minimum) == 0 && len(constraints.Maximum) == 0 &&
		constraints.MinItems == nil && constraints.MaxItems == nil
}
