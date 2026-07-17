// Package sdkmodel normalizes exact Capability contracts into an immutable,
// language-neutral application SDK operation model.
package sdkmodel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
)

const maximumOperations = 4096

var (
	// ErrNormalize reports that final canonical Capability views could not be
	// lowered into the shared SDK operation model.
	ErrNormalize = errors.New("normalize generated SDK model")
	// ErrTarget reports an invalid canonical target supplied to SDK lowering.
	ErrTarget = errors.New("invalid generated SDK canonical target")
	// ErrAlias reports an invalid final Capability Alias supplied to SDK
	// lowering.
	ErrAlias = errors.New("invalid generated SDK Capability Alias")
)

// CanonicalTargetView is the exact resolved application surface needed by all
// language SDK renderers. generation.CapabilityView satisfies this interface.
type CanonicalTargetView interface {
	ID() generation.CapabilityID
	ContractJSON() []byte
	ContractDigest() string
	Exposure() generation.Exposure
}

// AliasView is one final validated application-local Alias surface needed by
// language SDK renderers. aliasresolution.Alias satisfies this interface.
type AliasView interface {
	ID() generation.CapabilityID
	Target() generation.CapabilityID
	TargetContractDigest() string
	Exposure() generation.Exposure
	Deprecated() string
}

// Kind is one canonical field-value kind supported by generated SDKs.
type Kind string

const (
	KindString  Kind = "string"
	KindInteger Kind = "integer"
	KindNumber  Kind = "number"
	KindBoolean Kind = "boolean"
	KindObject  Kind = "object"
	KindArray   Kind = "array"
)

// Field is one immutable canonical request or response field.
type Field struct {
	name     string
	kind     Kind
	items    Kind
	required bool
	enumJSON []json.RawMessage
}

// Name returns the canonical lower-snake wire name.
func (f Field) Name() string { return f.name }

// Kind returns the field's canonical value kind.
func (f Field) Kind() Kind { return f.kind }

// Items returns the canonical item kind for an array field and the empty value
// for every scalar or object field.
func (f Field) Items() Kind { return f.items }

// Required reports whether the exact contract requires the field.
func (f Field) Required() bool { return f.required }

// EnumJSON returns canonical scalar JSON literals in deterministic order.
func (f Field) EnumJSON() []json.RawMessage { return cloneRawMessages(f.enumJSON) }

// Operation is one immutable canonical Capability exposed to an application
// SDK. It contains no provider identity or runtime configuration.
type Operation struct {
	id             generation.CapabilityID
	contractDigest string
	request        []Field
	response       []Field
	errors         []string
}

// ID returns the exact canonical Capability ID.
func (o Operation) ID() generation.CapabilityID { return o.id }

// ContractDigest returns the digest of the complete exact canonical contract,
// including generation-affecting extension metadata.
func (o Operation) ContractDigest() string { return o.contractDigest }

// Request returns fields sorted by canonical wire name.
func (o Operation) Request() []Field { return append([]Field(nil), o.request...) }

// Response returns fields sorted by canonical wire name.
func (o Operation) Response() []Field { return append([]Field(nil), o.response...) }

// Errors returns canonical semantic error codes in deterministic order.
func (o Operation) Errors() []string { return append([]string(nil), o.errors...) }

// Alias is one immutable application-local SDK name for a canonical
// operation. It owns no independent contract or provider.
type Alias struct {
	id         generation.CapabilityID
	target     generation.CapabilityID
	operation  Operation
	exposure   generation.Exposure
	deprecated string
}

// ID returns the exact application-local Alias ID.
func (a Alias) ID() generation.CapabilityID { return a.id }

// Target returns the direct canonical Capability ID.
func (a Alias) Target() generation.CapabilityID { return a.target }

// TargetOperation returns the exact canonical operation reused by this Alias.
func (a Alias) TargetOperation() Operation { return a.operation }

// TargetContractDigest returns the target's complete canonical contract
// digest. An Alias has no independent contract digest.
func (a Alias) TargetContractDigest() string { return a.operation.contractDigest }

// Exposure returns the final normalized application surfaces for this Alias.
func (a Alias) Exposure() generation.Exposure { return a.exposure }

// Deprecated returns application-local compatibility guidance, if present.
func (a Alias) Deprecated() string { return a.deprecated }

// Model is one immutable, canonically digestable SDK input shared by language
// renderers.
type Model struct {
	operations    []Operation
	aliases       []Alias
	canonicalJSON []byte
	digest        string
}

// Operations returns canonical operations sorted by exact Capability ID.
func (m Model) Operations() []Operation { return append([]Operation(nil), m.operations...) }

// Aliases returns JavaScript-exposed application-local names sorted by exact
// Alias ID.
func (m Model) Aliases() []Alias { return append([]Alias(nil), m.aliases...) }

// CanonicalJSON returns a defensive copy of the deterministic SDK model.
func (m Model) CanonicalJSON() []byte { return append([]byte(nil), m.canonicalJSON...) }

// Digest returns the SHA-256 digest of CanonicalJSON.
func (m Model) Digest() string { return m.digest }

type canonicalContract struct {
	ID       string                    `json:"id"`
	Request  map[string]canonicalField `json:"request"`
	Response map[string]canonicalField `json:"response"`
	Errors   []string                  `json:"errors"`
}

type canonicalField struct {
	Type     string            `json:"type"`
	Items    string            `json:"items,omitempty"`
	Required bool              `json:"required,omitempty"`
	Enum     []json.RawMessage `json:"enum,omitempty"`
}

type canonicalModel struct {
	Operations []canonicalOperation `json:"operations"`
	Aliases    []canonicalAlias     `json:"aliases,omitempty"`
}

type canonicalOperation struct {
	ID             string                `json:"id"`
	ContractDigest string                `json:"contract_digest"`
	Request        []canonicalModelField `json:"request"`
	Response       []canonicalModelField `json:"response"`
	Errors         []string              `json:"errors"`
}

type canonicalModelField struct {
	Name     string            `json:"name"`
	Kind     Kind              `json:"kind"`
	Items    Kind              `json:"items,omitempty"`
	Required bool              `json:"required"`
	Enum     []json.RawMessage `json:"enum,omitempty"`
}

type canonicalAlias struct {
	ID                   string              `json:"id"`
	Target               string              `json:"target"`
	TargetContractDigest string              `json:"target_contract_digest"`
	Exposure             generation.Exposure `json:"exposure"`
	Deprecated           string              `json:"deprecated,omitempty"`
}

// BuildCanonical validates final JavaScript-exposed canonical targets and
// returns their provider-independent SDK model. Browser SDK operations require
// the matching generated HTTP surface because HTTP is their transport.
func BuildCanonical(targets []CanonicalTargetView) (Model, error) {
	if len(targets) > maximumOperations {
		return Model{}, fmt.Errorf("%w: %w: %d operations exceeds maximum %d", ErrNormalize, ErrTarget, len(targets), maximumOperations)
	}
	return Build(targets, nil)
}

// Build validates final JavaScript-exposed canonical targets and the final
// application Alias map. Aliases excluded from JavaScript are intentionally
// absent from this SDK model. Included Aliases reuse their direct target's
// request, response, errors, and contract digest.
func Build(targets []CanonicalTargetView, aliasViews []AliasView) (Model, error) {
	if len(targets)+len(aliasViews) > maximumOperations {
		return Model{}, fmt.Errorf("%w: %d canonical and Alias operations exceeds maximum %d", ErrNormalize, len(targets)+len(aliasViews), maximumOperations)
	}
	operations := make([]Operation, 0, len(targets))
	seen := make(map[generation.CapabilityID]int, len(targets))
	targetsByID := make(map[generation.CapabilityID]normalizedTarget, len(targets))
	for index, target := range targets {
		operation, err := normalizeTarget(index, target)
		if err != nil {
			return Model{}, err
		}
		if previous, duplicate := seen[operation.id]; duplicate {
			return Model{}, fmt.Errorf("%w: %w: targets[%d] duplicates canonical Capability %s from targets[%d]", ErrNormalize, ErrTarget, index, operation.id, previous)
		}
		seen[operation.id] = index
		operations = append(operations, operation)
		targetsByID[operation.id] = normalizedTarget{operation: operation, exposure: target.Exposure()}
	}
	sort.Slice(operations, func(left, right int) bool {
		return operations[left].id.String() < operations[right].id.String()
	})
	aliases := make([]Alias, 0, len(aliasViews))
	seenAliases := make(map[generation.CapabilityID]int, len(aliasViews))
	for index, view := range aliasViews {
		alias, include, err := normalizeAlias(index, view, targetsByID)
		if err != nil {
			return Model{}, err
		}
		if !include {
			continue
		}
		if previous, duplicate := seenAliases[alias.id]; duplicate {
			return Model{}, fmt.Errorf("%w: %w: aliases[%d] duplicates Capability Alias %s from aliases[%d]", ErrNormalize, ErrAlias, index, alias.id, previous)
		}
		seenAliases[alias.id] = index
		aliases = append(aliases, alias)
	}
	sort.Slice(aliases, func(left, right int) bool {
		return aliases[left].id.String() < aliases[right].id.String()
	})
	canonical := canonicalModel{
		Operations: make([]canonicalOperation, len(operations)),
		Aliases:    make([]canonicalAlias, len(aliases)),
	}
	for index, operation := range operations {
		canonical.Operations[index] = canonicalOperation{
			ID:             operation.id.String(),
			ContractDigest: operation.contractDigest,
			Request:        canonicalFields(operation.request),
			Response:       canonicalFields(operation.response),
			Errors:         append([]string(nil), operation.errors...),
		}
	}
	for index, alias := range aliases {
		canonical.Aliases[index] = canonicalAlias{
			ID:                   alias.id.String(),
			Target:               alias.target.String(),
			TargetContractDigest: alias.operation.contractDigest,
			Exposure:             alias.exposure,
			Deprecated:           alias.deprecated,
		}
	}
	if len(canonical.Aliases) == 0 {
		canonical.Aliases = nil
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return Model{}, fmt.Errorf("%w: encode canonical SDK model: %v", ErrNormalize, err)
	}
	return Model{
		operations:    operations,
		aliases:       aliases,
		canonicalJSON: append([]byte(nil), encoded...),
		digest:        digest(encoded),
	}, nil
}

type normalizedTarget struct {
	operation Operation
	exposure  generation.Exposure
}

func normalizeAlias(index int, view AliasView, targets map[generation.CapabilityID]normalizedTarget) (Alias, bool, error) {
	field := fmt.Sprintf("aliases[%d]", index)
	if view == nil {
		return Alias{}, false, fmt.Errorf("%w: %w: %s view is absent", ErrNormalize, ErrAlias, field)
	}
	id, err := generation.ParseCapabilityID(view.ID().String())
	if err != nil {
		return Alias{}, false, fmt.Errorf("%w: %w: %s ID %q is not canonical: %v", ErrNormalize, ErrAlias, field, view.ID().String(), err)
	}
	targetID, err := generation.ParseCapabilityID(view.Target().String())
	if err != nil {
		return Alias{}, false, fmt.Errorf("%w: %w: %s target ID %q is not canonical: %v", ErrNormalize, ErrAlias, field, view.Target().String(), err)
	}
	deprecated := view.Deprecated()
	if len(deprecated) > 1024 || !utf8.ValidString(deprecated) || strings.ContainsRune(deprecated, '\x00') {
		return Alias{}, false, fmt.Errorf("%w: %w: %s %s deprecation metadata is invalid", ErrNormalize, ErrAlias, field, id)
	}
	exposure := view.Exposure()
	if !exposure.JavaScript {
		return Alias{}, false, nil
	}
	if !exposure.HTTP {
		return Alias{}, false, fmt.Errorf("%w: %w: %s %s has JavaScript exposure without its required HTTP transport", ErrNormalize, ErrAlias, field, id)
	}
	target, exists := targets[targetID]
	if !exists {
		return Alias{}, false, fmt.Errorf("%w: %w: %s %s target %s is not a JavaScript-exposed canonical operation", ErrNormalize, ErrAlias, field, id, targetID)
	}
	if _, collision := targets[id]; collision {
		return Alias{}, false, fmt.Errorf("%w: %w: %s %s collides with a canonical Capability", ErrNormalize, ErrAlias, field, id)
	}
	if strings.HasPrefix(id.Name(), "kernel.") {
		return Alias{}, false, fmt.Errorf("%w: %w: %s %s uses the reserved kernel.* namespace", ErrNormalize, ErrAlias, field, id)
	}
	if id == targetID {
		return Alias{}, false, fmt.Errorf("%w: %w: %s %s cannot target itself", ErrNormalize, ErrAlias, field, id)
	}
	if id.Major() != targetID.Major() {
		return Alias{}, false, fmt.Errorf("%w: %w: %s %s and target %s do not use the same version", ErrNormalize, ErrAlias, field, id, targetID)
	}
	if view.TargetContractDigest() != target.operation.contractDigest {
		return Alias{}, false, fmt.Errorf("%w: %w: %s %s target digest %q does not match %s", ErrNormalize, ErrAlias, field, id, view.TargetContractDigest(), target.operation.contractDigest)
	}
	if !exposureSubset(exposure, target.exposure) {
		return Alias{}, false, fmt.Errorf("%w: %w: %s %s exposure broadens target %s", ErrNormalize, ErrAlias, field, id, targetID)
	}
	return Alias{
		id:         id,
		target:     targetID,
		operation:  target.operation,
		exposure:   exposure,
		deprecated: deprecated,
	}, true, nil
}

func exposureSubset(alias, target generation.Exposure) bool {
	return (!alias.Go || target.Go) && (!alias.HTTP || target.HTTP) && (!alias.JavaScript || target.JavaScript)
}

func normalizeTarget(index int, target CanonicalTargetView) (Operation, error) {
	field := fmt.Sprintf("targets[%d]", index)
	if target == nil {
		return Operation{}, fmt.Errorf("%w: %w: %s view is absent", ErrNormalize, ErrTarget, field)
	}
	id, err := generation.ParseCapabilityID(target.ID().String())
	if err != nil {
		return Operation{}, fmt.Errorf("%w: %w: %s ID %q is not canonical: %v", ErrNormalize, ErrTarget, field, target.ID().String(), err)
	}
	exposure := target.Exposure()
	if !exposure.JavaScript {
		return Operation{}, fmt.Errorf("%w: %w: %s %s is not exposed to JavaScript", ErrNormalize, ErrTarget, field, id)
	}
	if !exposure.HTTP {
		return Operation{}, fmt.Errorf("%w: %w: %s %s has JavaScript exposure without its required HTTP transport", ErrNormalize, ErrTarget, field, id)
	}
	contractJSON := target.ContractJSON()
	normalized, err := capabilitymeta.NormalizeSchema(contractJSON)
	if err != nil {
		return Operation{}, fmt.Errorf("%w: %w: %s %s contract is invalid: %v", ErrNormalize, ErrTarget, field, id, err)
	}
	if !bytes.Equal(normalized, contractJSON) {
		return Operation{}, fmt.Errorf("%w: %w: %s %s contract is not in canonical encoding", ErrNormalize, ErrTarget, field, id)
	}
	var contract canonicalContract
	if err := json.Unmarshal(contractJSON, &contract); err != nil {
		return Operation{}, fmt.Errorf("%w: %w: %s %s contract cannot be decoded: %v", ErrNormalize, ErrTarget, field, id, err)
	}
	declaredID, err := generation.ParseCapabilityID(contract.ID)
	if err != nil || declaredID != id {
		return Operation{}, fmt.Errorf("%w: %w: %s ID %s does not match contract identity %q", ErrNormalize, ErrTarget, field, id, contract.ID)
	}
	contractDigest := digest(contractJSON)
	if target.ContractDigest() != contractDigest {
		return Operation{}, fmt.Errorf("%w: %w: %s %s contract digest %q does not match %s", ErrNormalize, ErrTarget, field, id, target.ContractDigest(), contractDigest)
	}
	request, err := normalizeFields(field+".request", contract.Request)
	if err != nil {
		return Operation{}, err
	}
	response, err := normalizeFields(field+".response", contract.Response)
	if err != nil {
		return Operation{}, err
	}
	return Operation{
		id:             id,
		contractDigest: contractDigest,
		request:        request,
		response:       response,
		errors:         append([]string(nil), contract.Errors...),
	}, nil
}

func normalizeFields(path string, values map[string]canonicalField) ([]Field, error) {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]Field, 0, len(names))
	for _, name := range names {
		value := values[name]
		kind := Kind(value.Type)
		if !kind.valid() {
			return nil, fmt.Errorf("%w: %w: %s.%s has unsupported kind %q", ErrNormalize, ErrTarget, path, name, value.Type)
		}
		items := Kind(value.Items)
		if kind == KindArray {
			if !items.valid() || items == KindArray {
				return nil, fmt.Errorf("%w: %w: %s.%s has unsupported array items %q", ErrNormalize, ErrTarget, path, name, value.Items)
			}
		} else if items != "" {
			return nil, fmt.Errorf("%w: %w: %s.%s has items on non-array kind %q", ErrNormalize, ErrTarget, path, name, value.Type)
		}
		fields = append(fields, Field{
			name:     name,
			kind:     kind,
			items:    items,
			required: value.Required,
			enumJSON: cloneRawMessages(value.Enum),
		})
	}
	return fields, nil
}

func (k Kind) valid() bool {
	switch k {
	case KindString, KindInteger, KindNumber, KindBoolean, KindObject, KindArray:
		return true
	default:
		return false
	}
}

func canonicalFields(fields []Field) []canonicalModelField {
	result := make([]canonicalModelField, len(fields))
	for index, field := range fields {
		result[index] = canonicalModelField{
			Name:     field.name,
			Kind:     field.kind,
			Items:    field.items,
			Required: field.required,
			Enum:     cloneRawMessages(field.enumJSON),
		}
	}
	return result
}

func cloneRawMessages(values []json.RawMessage) []json.RawMessage {
	if len(values) == 0 {
		return nil
	}
	result := make([]json.RawMessage, len(values))
	for index, value := range values {
		result[index] = append(json.RawMessage(nil), value...)
	}
	return result
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
