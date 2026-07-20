// Package protobufmodel normalizes the exact canonical contracts selected for
// the generated Connect surface into one immutable provider-independent input.
package protobufmodel

import (
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
	"github.com/plystra/cli/internal/protobufidentity"
	"github.com/plystra/cli/internal/sdkmodel"
)

var (
	// ErrBuild reports invalid or internally inconsistent normalized
	// Protobuf projection input.
	ErrBuild = errors.New("build normalized Protobuf projection model")
	// ErrTarget reports an invalid canonical Capability projection input.
	ErrTarget = errors.New("invalid Protobuf projection target")
	// ErrAlias reports an invalid Capability Alias projection input.
	ErrAlias = errors.New("invalid Protobuf projection Alias")
	// ErrOperationKind reports a canonical Capability kind that cannot enter
	// the currently supported Connect operation boundary.
	ErrOperationKind = errors.New("unsupported Connect operation kind")
)

// CanonicalTargetView is the exact resolved canonical surface and immutable
// contract provenance required by the Protobuf projection.
// generation.CapabilityView satisfies this interface.
type CanonicalTargetView interface {
	sdkmodel.CanonicalTargetView
	Sources() []string
}

// AliasView is one final validated application-local Alias surface.
type AliasView = sdkmodel.AliasView

// Operation is one immutable exact canonical Capability contract selected for
// the Connect transport.
type Operation struct {
	id             generation.CapabilityID
	kind           capabilitymeta.CapabilityKind
	identity       protobufidentity.Identity
	contractDigest string
	sources        []string
	request        []sdkmodel.Field
	response       []sdkmodel.Field
	errors         []string
}

// Identity returns the canonical public Protobuf and Connect identity.
func (o Operation) Identity() protobufidentity.Identity { return o.identity }

// ID returns the exact canonical Capability ID.
func (o Operation) ID() generation.CapabilityID { return o.id }

// Kind returns the validated provider-independent operation kind projected by
// this Connect procedure.
func (o Operation) Kind() capabilitymeta.CapabilityKind { return o.kind }

// ContractDigest returns the digest of the complete normalized canonical
// contract, including generation-affecting extension metadata.
func (o Operation) ContractDigest() string { return o.contractDigest }

// Sources returns deterministic provenance for the exact canonical contract.
func (o Operation) Sources() []string { return append([]string(nil), o.sources...) }

// Request returns request fields sorted by canonical field name.
func (o Operation) Request() []sdkmodel.Field { return append([]sdkmodel.Field(nil), o.request...) }

// Response returns response fields sorted by canonical field name.
func (o Operation) Response() []sdkmodel.Field { return append([]sdkmodel.Field(nil), o.response...) }

// Errors returns canonical semantic error codes in deterministic order.
func (o Operation) Errors() []string { return append([]string(nil), o.errors...) }

// Alias is one immutable public Protobuf and Connect identity that reuses one
// direct canonical target contract.
type Alias struct {
	id                   generation.CapabilityID
	target               generation.CapabilityID
	identity             protobufidentity.Identity
	targetContractDigest string
	deprecated           string
}

// Identity returns the Alias public identity and canonical target identity.
func (a Alias) Identity() protobufidentity.Identity { return a.identity }

// ID returns the exact public Alias ID.
func (a Alias) ID() generation.CapabilityID { return a.id }

// Target returns the direct canonical Capability ID reused by the Alias.
func (a Alias) Target() generation.CapabilityID { return a.target }

// TargetContractDigest returns the complete canonical target contract digest.
func (a Alias) TargetContractDigest() string { return a.targetContractDigest }

// Deprecated returns application-local transport documentation guidance.
func (a Alias) Deprecated() string { return a.deprecated }

// Model is one immutable canonically digestable normalized Protobuf projection
// input. A disabled model is still valid and explicitly records that Connect
// contributes no public Protobuf surface.
type Model struct {
	enabled       bool
	operations    []Operation
	aliases       []Alias
	canonicalJSON []byte
	digest        string
	prepared      bool
}

// Valid reports whether Build produced the model.
func (m Model) Valid() bool { return m.prepared && len(m.canonicalJSON) != 0 && m.digest != "" }

// Enabled reports whether the selected application transport includes Connect.
func (m Model) Enabled() bool { return m.enabled }

// Operations returns canonical operations sorted by exact Capability ID.
func (m Model) Operations() []Operation { return append([]Operation(nil), m.operations...) }

// Aliases returns HTTP-exposed Aliases sorted by exact Alias ID.
func (m Model) Aliases() []Alias { return append([]Alias(nil), m.aliases...) }

// CanonicalJSON returns a defensive copy of the normalized projection input.
func (m Model) CanonicalJSON() []byte { return append([]byte(nil), m.canonicalJSON...) }

// Digest returns the SHA-256 digest of CanonicalJSON.
func (m Model) Digest() string { return m.digest }

type canonicalModel struct {
	Version    int                  `json:"version"`
	Enabled    bool                 `json:"enabled"`
	Operations []canonicalOperation `json:"operations"`
	Aliases    []canonicalAlias     `json:"aliases"`
}

type canonicalOperation struct {
	PublicID       string                        `json:"public_id"`
	CanonicalID    string                        `json:"canonical_id"`
	Kind           capabilitymeta.CapabilityKind `json:"kind"`
	Package        string                        `json:"package"`
	Service        string                        `json:"service"`
	Method         string                        `json:"method"`
	RequestType    string                        `json:"request_type"`
	ResponseType   string                        `json:"response_type"`
	Procedure      string                        `json:"procedure"`
	ContractDigest string                        `json:"contract_digest"`
	Sources        []string                      `json:"sources"`
	Request        []canonicalField              `json:"request"`
	Response       []canonicalField              `json:"response"`
	Errors         []string                      `json:"errors"`
}

type canonicalAlias struct {
	PublicID             string `json:"public_id"`
	CanonicalID          string `json:"canonical_id"`
	Package              string `json:"package"`
	Service              string `json:"service"`
	Method               string `json:"method"`
	RequestType          string `json:"request_type"`
	ResponseType         string `json:"response_type"`
	Procedure            string `json:"procedure"`
	TargetContractDigest string `json:"target_contract_digest"`
	Deprecated           string `json:"deprecated,omitempty"`
}

type canonicalField struct {
	Name     string            `json:"name"`
	Kind     sdkmodel.Kind     `json:"kind"`
	Items    sdkmodel.Kind     `json:"items,omitempty"`
	Required bool              `json:"required"`
	Enum     []json.RawMessage `json:"enum,omitempty"`
}

// Build selects the exact HTTP-exposed canonical and Alias surfaces when
// Connect is enabled, validates their complete normalized contracts, assigns
// deterministic identities, and returns one order-independent projection.
func Build(connect bool, targets []CanonicalTargetView, aliasViews []AliasView) (Model, error) {
	if !connect {
		return finalize(false, nil, nil)
	}

	httpTargets := make([]CanonicalTargetView, 0, len(targets))
	for index, target := range targets {
		if target == nil {
			return Model{}, fmt.Errorf("%w: %w: targets[%d] view is absent", ErrBuild, ErrTarget, index)
		}
		if target.Exposure().HTTP {
			httpTargets = append(httpTargets, target)
		}
	}
	for index, alias := range aliasViews {
		if alias == nil {
			return Model{}, fmt.Errorf("%w: %w: aliases[%d] view is absent", ErrBuild, ErrAlias, index)
		}
	}

	sdkTargets := make([]sdkmodel.CanonicalTargetView, len(httpTargets))
	for index, target := range httpTargets {
		sdkTargets[index] = target
	}
	contracts, err := sdkmodel.BuildHTTP(sdkTargets, aliasViews)
	if err != nil {
		return Model{}, fmt.Errorf("%w: %w", ErrBuild, err)
	}
	surfaces := make([]protobufidentity.Surface, 0, len(contracts.Operations())+len(contracts.Aliases()))
	for _, operation := range contracts.Operations() {
		id := operation.ID().String()
		surfaces = append(surfaces, protobufidentity.Surface{PublicID: id, CanonicalID: id})
	}
	for _, alias := range contracts.Aliases() {
		surfaces = append(surfaces, protobufidentity.Surface{
			PublicID:    alias.ID().String(),
			CanonicalID: alias.Target().String(),
		})
	}
	identities, err := protobufidentity.Build(surfaces)
	if err != nil {
		return Model{}, fmt.Errorf("%w: %w", ErrBuild, err)
	}
	byPublicID := make(map[string]protobufidentity.Identity, len(surfaces))
	for _, identity := range identities.Identities() {
		byPublicID[identity.PublicID()] = identity
	}

	operations := make([]Operation, len(contracts.Operations()))
	for index, operation := range contracts.Operations() {
		identity, exists := byPublicID[operation.ID().String()]
		if !exists || identity.PublicID() != identity.CanonicalID() {
			return Model{}, fmt.Errorf("%w: %w: canonical identity for %s is absent or inconsistent", ErrBuild, ErrTarget, operation.ID())
		}
		sources, err := targetSources(httpTargets, operation.ID())
		if err != nil {
			return Model{}, fmt.Errorf("%w: %w: canonical contract provenance for %s: %v", ErrBuild, ErrTarget, operation.ID(), err)
		}
		request := operation.Request()
		if err := validateFieldIdentities(operation.ID().String(), "request", identity.RequestType(), request); err != nil {
			return Model{}, err
		}
		response := operation.Response()
		if err := validateFieldIdentities(operation.ID().String(), "response", identity.ResponseType(), response); err != nil {
			return Model{}, err
		}
		kind, err := targetKind(httpTargets, operation.ID())
		if err != nil {
			return Model{}, fmt.Errorf("%w: %w: canonical operation kind for %s: %v", ErrBuild, ErrTarget, operation.ID(), err)
		}
		if kind != capabilitymeta.CapabilityKindQuery {
			return Model{}, fmt.Errorf("%w: %w: %w: Capability %s declares semantics.kind %q for the requested Connect surface; the current unary boundary supports only semantics.kind %q; remove %s from http.expose until its Connect operation kind is supported", ErrBuild, ErrTarget, ErrOperationKind, operation.ID(), kind, capabilitymeta.CapabilityKindQuery, operation.ID())
		}
		operations[index] = Operation{
			id:             operation.ID(),
			kind:           kind,
			identity:       identity,
			contractDigest: operation.ContractDigest(),
			sources:        sources,
			request:        request,
			response:       response,
			errors:         operation.Errors(),
		}
	}
	aliases := make([]Alias, len(contracts.Aliases()))
	for index, alias := range contracts.Aliases() {
		identity, exists := byPublicID[alias.ID().String()]
		if !exists || identity.CanonicalID() != alias.Target().String() {
			return Model{}, fmt.Errorf("%w: %w: identity for Alias %s is absent or inconsistent", ErrBuild, ErrAlias, alias.ID())
		}
		aliases[index] = Alias{
			id:                   alias.ID(),
			target:               alias.Target(),
			identity:             identity,
			targetContractDigest: alias.TargetContractDigest(),
			deprecated:           alias.Deprecated(),
		}
	}
	return finalize(true, operations, aliases)
}

func validateFieldIdentities(capabilityID, direction, messageType string, fields []sdkmodel.Field) error {
	ordered := append([]sdkmodel.Field(nil), fields...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].Name() < ordered[right].Name()
	})
	jsonOwners := make(map[string]string, len(ordered))
	enumOwners := make(map[string]string, len(ordered))
	for _, field := range ordered {
		fieldName := field.Name()
		jsonName := protobufidentity.FieldJSONName(fieldName)
		if previous, exists := jsonOwners[jsonName]; exists && previous != fieldName {
			return fieldCollision(capabilityID, direction, previous, fieldName, "Protobuf JSON name", jsonName)
		}
		jsonOwners[jsonName] = fieldName
		if len(field.EnumJSON()) == 0 {
			continue
		}
		enumType := protobufidentity.EnumType(messageType, fieldName)
		if previous, exists := enumOwners[enumType]; exists && previous != fieldName {
			return fieldCollision(capabilityID, direction, previous, fieldName, "generated enum identity", enumType)
		}
		enumOwners[enumType] = fieldName
	}
	return nil
}

func fieldCollision(capabilityID, direction, left, right, kind, identity string) error {
	return fmt.Errorf("%w: %w: %w: Capability %s %s canonical fields %q and %q produce the same %s %q", ErrBuild, ErrTarget, protobufidentity.ErrCollision, capabilityID, direction, left, right, kind, identity)
}

func finalize(enabled bool, operations []Operation, aliases []Alias) (Model, error) {
	document := canonicalModel{
		Version:    2,
		Enabled:    enabled,
		Operations: make([]canonicalOperation, len(operations)),
		Aliases:    make([]canonicalAlias, len(aliases)),
	}
	for index, operation := range operations {
		identity := operation.identity
		document.Operations[index] = canonicalOperation{
			PublicID:       identity.PublicID(),
			CanonicalID:    identity.CanonicalID(),
			Kind:           operation.kind,
			Package:        identity.Package(),
			Service:        identity.Service(),
			Method:         identity.Method(),
			RequestType:    identity.RequestType(),
			ResponseType:   identity.ResponseType(),
			Procedure:      identity.Procedure(),
			ContractDigest: operation.contractDigest,
			Sources:        append([]string(nil), operation.sources...),
			Request:        canonicalFields(operation.request),
			Response:       canonicalFields(operation.response),
			Errors:         append([]string(nil), operation.errors...),
		}
	}
	for index, alias := range aliases {
		identity := alias.identity
		document.Aliases[index] = canonicalAlias{
			PublicID:             identity.PublicID(),
			CanonicalID:          identity.CanonicalID(),
			Package:              identity.Package(),
			Service:              identity.Service(),
			Method:               identity.Method(),
			RequestType:          identity.RequestType(),
			ResponseType:         identity.ResponseType(),
			Procedure:            identity.Procedure(),
			TargetContractDigest: alias.targetContractDigest,
			Deprecated:           alias.deprecated,
		}
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return Model{}, fmt.Errorf("%w: encode canonical model: %v", ErrBuild, err)
	}
	sum := sha256.Sum256(canonical)
	return Model{
		enabled:       enabled,
		operations:    append([]Operation(nil), operations...),
		aliases:       append([]Alias(nil), aliases...),
		canonicalJSON: canonical,
		digest:        "sha256:" + hex.EncodeToString(sum[:]),
		prepared:      true,
	}, nil
}

func targetKind(targets []CanonicalTargetView, id generation.CapabilityID) (capabilitymeta.CapabilityKind, error) {
	for _, target := range targets {
		if target.ID() != id {
			continue
		}
		manifest, err := capabilitymeta.Parse(target.ContractJSON())
		if err != nil {
			return "", fmt.Errorf("parse canonical contract: %w", err)
		}
		if manifest.ID().String() != id.String() {
			return "", fmt.Errorf("contract identity %s does not match target %s", manifest.ID(), id)
		}
		return manifest.Semantics().Kind(), nil
	}
	return "", errors.New("target view is absent")
}

func targetSources(targets []CanonicalTargetView, id generation.CapabilityID) ([]string, error) {
	for _, target := range targets {
		if target.ID() == id {
			result := target.Sources()
			if len(result) == 0 {
				return nil, errors.New("at least one source is required")
			}
			for index, source := range result {
				if source == "" || len(source) > 1024 || !utf8.ValidString(source) || strings.ContainsRune(source, '\x00') {
					return nil, fmt.Errorf("sources[%d] is invalid", index)
				}
			}
			sort.Strings(result)
			for index := 1; index < len(result); index++ {
				if result[index] == result[index-1] {
					return nil, fmt.Errorf("sources duplicates %q", result[index])
				}
			}
			return result, nil
		}
	}
	return nil, errors.New("source view is absent")
}

func canonicalFields(fields []sdkmodel.Field) []canonicalField {
	result := make([]canonicalField, len(fields))
	for index, field := range fields {
		result[index] = canonicalField{
			Name:     field.Name(),
			Kind:     field.Kind(),
			Items:    field.Items(),
			Required: field.Required(),
			Enum:     field.EnumJSON(),
		}
	}
	return result
}
