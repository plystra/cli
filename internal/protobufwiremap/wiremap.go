// Package protobufwiremap owns deterministic committed Protobuf wire history
// for canonical Interface messages and the temporary legacy transport bridge.
package protobufwiremap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/plystra/cli/internal/protobufidentity"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/sdkmodel"
)

const (
	// Path is the one CLI-owned committed Protobuf field-history artifact.
	Path = "generated/proto/wire-map.json"
	// ProjectionSchema identifies the strict initial-release wire-map schema.
	ProjectionSchema = "plystra.proto-wire-map/v3"
	// MaximumBytes bounds managed history before parsing.
	MaximumBytes int64 = 16 << 20

	minimumFieldNumber        = 1
	maximumFieldNumber        = 536870911
	reservedRangeStart        = 19000
	reservedRangeEnd          = 19999
	maximumEnumNumber         = 2147483647
	maximumInterfaces         = 4096
	maximumLegacyCapabilities = 4096
	maximumFields             = 16384
)

var (
	// ErrBuild reports that current projection and prior history could not be
	// reconciled into one valid deterministic map.
	ErrBuild = errors.New("build Protobuf wire map")
	// ErrHistory reports missing, corrupt, modified, or inconsistent managed
	// wire history.
	ErrHistory = errors.New("invalid Protobuf wire-map history")
	// ErrProjection reports an invalid current normalized Protobuf projection.
	ErrProjection = errors.New("invalid Protobuf wire-map projection")
)

// Map is one immutable validated current wire map. CanonicalJSON is committed
// history; ActiveJSON contains only build-affecting active assignments.
type Map struct {
	canonicalJSON             []byte
	activeJSON                []byte
	activeInterfaces          []InterfaceProjection
	activeLegacyCapabilities  []CapabilityProjection
	digest                    string
	activeDigest              string
	projectionDigest          string
	legacyProjectionDigest    string
	interfaceProjectionDigest string
	prepared                  bool
}

// Valid reports whether Build produced the map.
func (m Map) Valid() bool {
	return m.prepared &&
		len(m.canonicalJSON) != 0 &&
		len(m.activeJSON) != 0 &&
		m.activeInterfaces != nil &&
		m.activeLegacyCapabilities != nil &&
		validDigest(m.digest) &&
		validDigest(m.activeDigest) &&
		validDigest(m.projectionDigest) &&
		validDigest(m.legacyProjectionDigest) &&
		validDigest(m.interfaceProjectionDigest)
}

// CanonicalJSON returns defensive canonical committed history bytes.
func (m Map) CanonicalJSON() []byte { return append([]byte(nil), m.canonicalJSON...) }

// ActiveJSON returns defensive canonical build-affecting assignment bytes.
func (m Map) ActiveJSON() []byte { return append([]byte(nil), m.activeJSON...) }

// Digest returns the digest of CanonicalJSON, including inactive history.
func (m Map) Digest() string { return m.digest }

// ActiveDigest returns the digest of ActiveJSON.
func (m Map) ActiveDigest() string { return m.activeDigest }

// ProjectionDigest identifies the exact normalized current Interface and
// temporary legacy Protobuf models against which this map was reconciled. It
// is intentionally not serialized.
func (m Map) ProjectionDigest() string { return m.projectionDigest }

// LegacyProjectionDigest identifies the temporary legacy transport model.
func (m Map) LegacyProjectionDigest() string { return m.legacyProjectionDigest }

// InterfaceProjectionDigest identifies the canonical Interface model.
func (m Map) InterfaceProjectionDigest() string { return m.interfaceProjectionDigest }

// Matches reports whether the map was reconciled against both exact models.
func (m Map) Matches(legacy protobufmodel.Model, interfaces protobufmodel.InterfaceModel) bool {
	return m.Valid() &&
		legacy.Valid() &&
		interfaces.Valid() &&
		legacy.Enabled() == interfaces.Enabled() &&
		m.legacyProjectionDigest == legacy.Digest() &&
		m.interfaceProjectionDigest == interfaces.Digest() &&
		m.projectionDigest == combinedProjectionDigest(legacy.Digest(), interfaces.Digest())
}

// CapabilityProjection is one active temporary legacy transport target's
// validated wire assignments. Canonical Interface history is exposed through
// InterfaceProjection.
type CapabilityProjection struct {
	id             string
	contractDigest string
	request        MessageProjection
	response       MessageProjection
}

// ID returns the exact canonical Capability ID.
func (p CapabilityProjection) ID() string { return p.id }

// ContractDigest returns the complete normalized canonical contract digest.
func (p CapabilityProjection) ContractDigest() string { return p.contractDigest }

// Request returns the canonical request-message projection.
func (p CapabilityProjection) Request() MessageProjection { return cloneMessageProjection(p.request) }

// Response returns the canonical response-message projection.
func (p CapabilityProjection) Response() MessageProjection { return cloneMessageProjection(p.response) }

// MessageProjection is one active canonical message plus permanent field and
// enum reservations retained from earlier projections.
type MessageProjection struct {
	canonicalName   string
	name            string
	fields          []FieldProjection
	enums           []EnumProjection
	reservedNumbers []int
	reservedNames   []string
}

// CanonicalName returns the authored Go message name for an Interface
// projection. It is empty for a temporary legacy transport message.
func (p MessageProjection) CanonicalName() string { return p.canonicalName }

// Name returns the unqualified generated message name.
func (p MessageProjection) Name() string { return p.name }

// Fields returns canonical-name-sorted active field assignments.
func (p MessageProjection) Fields() []FieldProjection {
	return append([]FieldProjection(nil), p.fields...)
}

// Enums returns canonical-field-name-sorted active enum assignments.
func (p MessageProjection) Enums() []EnumProjection {
	result := make([]EnumProjection, len(p.enums))
	for index, value := range p.enums {
		result[index] = cloneEnumProjection(value)
	}
	return result
}

// ReservedNumbers returns ascending permanently reserved field numbers.
func (p MessageProjection) ReservedNumbers() []int {
	return append([]int(nil), p.reservedNumbers...)
}

// ReservedNames returns lexically sorted permanently reserved field names.
func (p MessageProjection) ReservedNames() []string {
	return append([]string(nil), p.reservedNames...)
}

// FieldProjection binds one canonical field to its stable Protobuf identity.
type FieldProjection struct {
	canonicalName string
	name          string
	number        int
}

// CanonicalName returns the exact canonical field name.
func (p FieldProjection) CanonicalName() string { return p.canonicalName }

// Name returns the stable generated Protobuf field name.
func (p FieldProjection) Name() string { return p.name }

// Number returns the stable positive Protobuf field number.
func (p FieldProjection) Number() int { return p.number }

// EnumProjection is one active generated enum and its stable wire history.
type EnumProjection struct {
	canonicalField  string
	identity        string
	kind            sdkmodel.Kind
	sentinel        EnumValueProjection
	members         []EnumValueProjection
	reservedNumbers []int
	reservedNames   []string
}

// CanonicalField returns the exact canonical field that owns this enum.
func (p EnumProjection) CanonicalField() string { return p.canonicalField }

// Identity returns the fully qualified generated enum identity.
func (p EnumProjection) Identity() string { return p.identity }

// Kind returns the canonical scalar kind represented by the enum.
func (p EnumProjection) Kind() sdkmodel.Kind { return p.kind }

// Sentinel returns the generated numeric-zero unspecified value.
func (p EnumProjection) Sentinel() EnumValueProjection {
	return cloneEnumValueProjection(p.sentinel)
}

// Members returns stable positive enum-member assignments.
func (p EnumProjection) Members() []EnumValueProjection {
	result := make([]EnumValueProjection, len(p.members))
	for index, value := range p.members {
		result[index] = cloneEnumValueProjection(value)
	}
	return result
}

// ReservedNumbers returns ascending permanently reserved enum numbers.
func (p EnumProjection) ReservedNumbers() []int {
	return append([]int(nil), p.reservedNumbers...)
}

// ReservedNames returns lexically sorted permanently reserved enum names.
func (p EnumProjection) ReservedNames() []string {
	return append([]string(nil), p.reservedNames...)
}

// EnumValueProjection is one generated enum name and number. CanonicalJSON is
// present for canonical members and absent for the generated sentinel.
type EnumValueProjection struct {
	canonical []byte
	name      string
	number    int
}

// CanonicalJSON returns the exact normalized canonical scalar value.
func (p EnumValueProjection) CanonicalJSON() []byte {
	return append([]byte(nil), p.canonical...)
}

// Name returns the generated enum value name.
func (p EnumValueProjection) Name() string { return p.name }

// Number returns the stable enum numeric value.
func (p EnumValueProjection) Number() int { return p.number }

// ActiveCapabilities returns exact-ID-sorted temporary legacy transport
// projections with defensive storage. Inactive history remains only in
// CanonicalJSON.
func (m Map) ActiveCapabilities() []CapabilityProjection {
	result := make([]CapabilityProjection, len(m.activeLegacyCapabilities))
	for index, capability := range m.activeLegacyCapabilities {
		result[index] = cloneCapabilityProjection(capability)
	}
	return result
}

type document struct {
	ProjectionSchema   string                      `json:"projection_schema"`
	Interfaces         map[string]interfaceHistory `json:"interfaces"`
	LegacyCapabilities map[string]capability       `json:"legacy_capabilities"`
}

type capability struct {
	Active                  bool     `json:"active"`
	CanonicalContractDigest string   `json:"canonical_contract_digest"`
	Provenance              []string `json:"provenance"`
	Request                 message  `json:"request"`
	Response                message  `json:"response"`
}

type message struct {
	Message         string                     `json:"message"`
	Fields          map[string]fieldAssignment `json:"fields"`
	Enums           map[string]enumAssignment  `json:"enums"`
	ReservedNumbers []int                      `json:"reserved_numbers"`
	ReservedNames   []string                   `json:"reserved_names"`
}

type fieldAssignment struct {
	Name   string `json:"name"`
	Number int    `json:"number"`
}

type enumAssignment struct {
	Active          bool          `json:"active"`
	Identity        string        `json:"identity"`
	Kind            sdkmodel.Kind `json:"kind"`
	Sentinel        enumSymbol    `json:"sentinel"`
	Members         []enumMember  `json:"members"`
	ReservedNumbers []int         `json:"reserved_numbers"`
	ReservedNames   []string      `json:"reserved_names"`
}

type enumSymbol struct {
	Name   string `json:"name"`
	Number int    `json:"number"`
}

type enumMember struct {
	Canonical json.RawMessage `json:"canonical"`
	Name      string          `json:"name"`
	Number    int             `json:"number"`
}

type activeDocument struct {
	ProjectionSchema   string                      `json:"projection_schema"`
	Interfaces         map[string]activeInterface  `json:"interfaces"`
	LegacyCapabilities map[string]activeCapability `json:"legacy_capabilities"`
}

type activeCapability struct {
	CanonicalContractDigest string  `json:"canonical_contract_digest"`
	Request                 message `json:"request"`
	Response                message `json:"response"`
}

// Build reconciles current canonical Interface messages and the temporary
// legacy transport projection with exact prior managed history.
// previousDigest must be the digest retained in the prior generated
// application manifest whenever previousExists is true. A digest without a
// file, or a file without that baseline, fails rather than guessing.
func Build(
	legacy protobufmodel.Model,
	interfaces protobufmodel.InterfaceModel,
	previous []byte,
	previousExists bool,
	previousDigest string,
) (Map, error) {
	if !legacy.Valid() {
		return Map{}, fmt.Errorf("%w: %w: normalized legacy Protobuf model is absent", ErrBuild, ErrProjection)
	}
	if !interfaces.Valid() {
		return Map{}, fmt.Errorf("%w: %w: normalized Interface Protobuf model is absent", ErrBuild, ErrProjection)
	}
	if legacy.Enabled() != interfaces.Enabled() {
		return Map{}, fmt.Errorf("%w: %w: Interface and legacy Protobuf transport selection disagree", ErrBuild, ErrProjection)
	}
	current := document{
		ProjectionSchema:   ProjectionSchema,
		Interfaces:         make(map[string]interfaceHistory),
		LegacyCapabilities: make(map[string]capability),
	}
	if previousExists {
		if !validDigest(previousDigest) {
			return Map{}, fmt.Errorf("%w: %w: owned %s has no valid generated-manifest baseline digest", ErrBuild, ErrHistory, Path)
		}
		if int64(len(previous)) > MaximumBytes {
			return Map{}, fmt.Errorf("%w: %w: %s exceeds %d bytes", ErrBuild, ErrHistory, Path, MaximumBytes)
		}
		if actual := digest(previous); actual != previousDigest {
			return Map{}, fmt.Errorf("%w: %w: %s digest %s does not match generated-manifest baseline %s", ErrBuild, ErrHistory, Path, actual, previousDigest)
		}
		decoded, err := decode(previous)
		if err != nil {
			return Map{}, fmt.Errorf("%w: %w", ErrBuild, err)
		}
		current = cloneDocument(decoded)
	} else if previousDigest != "" {
		return Map{}, fmt.Errorf("%w: %w: generated-manifest baseline %s exists but owned %s is missing", ErrBuild, ErrHistory, previousDigest, Path)
	}

	if err := reconcileInterfaces(current.Interfaces, interfaces); err != nil {
		return Map{}, fmt.Errorf("%w: %w: %v", ErrBuild, ErrHistory, err)
	}
	for id, existing := range current.LegacyCapabilities {
		existing.Active = false
		current.LegacyCapabilities[id] = existing
	}
	for _, operation := range legacy.Operations() {
		id := operation.ID().String()
		identity := operation.Identity()
		requestName, err := unqualifiedMessage(identity.Package(), identity.RequestType())
		if err != nil {
			return Map{}, fmt.Errorf("%w: %w: %s request identity: %v", ErrBuild, ErrProjection, id, err)
		}
		responseName, err := unqualifiedMessage(identity.Package(), identity.ResponseType())
		if err != nil {
			return Map{}, fmt.Errorf("%w: %w: %s response identity: %v", ErrBuild, ErrProjection, id, err)
		}
		record, exists := current.LegacyCapabilities[id]
		if !exists {
			record = capability{
				Request:  emptyMessage(requestName),
				Response: emptyMessage(responseName),
			}
		}
		if record.Request.Message != requestName || record.Response.Message != responseName {
			return Map{}, fmt.Errorf("%w: %w: Capability %s message identity changed from %s/%s to %s/%s", ErrBuild, ErrHistory, id, record.Request.Message, record.Response.Message, requestName, responseName)
		}
		record.Request, err = reconcileMessage(record.Request, identity.Package(), operation.Request())
		if err != nil {
			return Map{}, fmt.Errorf("%w: %w: Capability %s request: %v", ErrBuild, ErrHistory, id, err)
		}
		record.Response, err = reconcileMessage(record.Response, identity.Package(), operation.Response())
		if err != nil {
			return Map{}, fmt.Errorf("%w: %w: Capability %s response: %v", ErrBuild, ErrHistory, id, err)
		}
		record.Active = true
		record.CanonicalContractDigest = operation.ContractDigest()
		record.Provenance = operation.Sources()
		current.LegacyCapabilities[id] = record
	}
	if err := validateDocument(current); err != nil {
		return Map{}, fmt.Errorf("%w: %w: reconciled map: %v", ErrBuild, ErrHistory, err)
	}
	canonical, err := encode(current, true)
	if err != nil {
		return Map{}, fmt.Errorf("%w: encode committed history: %v", ErrBuild, err)
	}
	active, err := encodeActive(current)
	if err != nil {
		return Map{}, fmt.Errorf("%w: encode active assignments: %v", ErrBuild, err)
	}
	return Map{
		canonicalJSON:             canonical,
		activeJSON:                active,
		activeInterfaces:          activeInterfaceProjections(current.Interfaces),
		activeLegacyCapabilities:  activeProjections(current),
		digest:                    digest(canonical),
		activeDigest:              digest(active),
		projectionDigest:          combinedProjectionDigest(legacy.Digest(), interfaces.Digest()),
		legacyProjectionDigest:    legacy.Digest(),
		interfaceProjectionDigest: interfaces.Digest(),
		prepared:                  true,
	}, nil
}

func activeProjections(value document) []CapabilityProjection {
	identifiers := make([]string, 0, len(value.LegacyCapabilities))
	for identifier, capability := range value.LegacyCapabilities {
		if capability.Active {
			identifiers = append(identifiers, identifier)
		}
	}
	sort.Strings(identifiers)
	result := make([]CapabilityProjection, len(identifiers))
	for index, identifier := range identifiers {
		capability := value.LegacyCapabilities[identifier]
		result[index] = CapabilityProjection{
			id:             identifier,
			contractDigest: capability.CanonicalContractDigest,
			request:        projectMessage(capability.Request),
			response:       projectMessage(capability.Response),
		}
	}
	return result
}

func projectMessage(value message) MessageProjection {
	fieldNames := make([]string, 0, len(value.Fields))
	for canonicalName := range value.Fields {
		fieldNames = append(fieldNames, canonicalName)
	}
	sort.Strings(fieldNames)
	fields := make([]FieldProjection, len(fieldNames))
	for index, canonicalName := range fieldNames {
		assignment := value.Fields[canonicalName]
		fields[index] = FieldProjection{canonicalName: canonicalName, name: assignment.Name, number: assignment.Number}
	}
	enumNames := make([]string, 0, len(value.Enums))
	for canonicalField, assignment := range value.Enums {
		if assignment.Active {
			enumNames = append(enumNames, canonicalField)
		}
	}
	sort.Strings(enumNames)
	enums := make([]EnumProjection, len(enumNames))
	for index, canonicalField := range enumNames {
		assignment := value.Enums[canonicalField]
		members := make([]EnumValueProjection, len(assignment.Members))
		for memberIndex, member := range assignment.Members {
			members[memberIndex] = EnumValueProjection{canonical: append([]byte(nil), member.Canonical...), name: member.Name, number: member.Number}
		}
		enums[index] = EnumProjection{
			canonicalField:  canonicalField,
			identity:        assignment.Identity,
			kind:            assignment.Kind,
			sentinel:        EnumValueProjection{name: assignment.Sentinel.Name, number: assignment.Sentinel.Number},
			members:         members,
			reservedNumbers: append([]int(nil), assignment.ReservedNumbers...),
			reservedNames:   append([]string(nil), assignment.ReservedNames...),
		}
	}
	return MessageProjection{
		canonicalName:   "",
		name:            value.Message,
		fields:          fields,
		enums:           enums,
		reservedNumbers: append([]int(nil), value.ReservedNumbers...),
		reservedNames:   append([]string(nil), value.ReservedNames...),
	}
}

func cloneCapabilityProjection(value CapabilityProjection) CapabilityProjection {
	return CapabilityProjection{
		id:             value.id,
		contractDigest: value.contractDigest,
		request:        cloneMessageProjection(value.request),
		response:       cloneMessageProjection(value.response),
	}
}

func cloneMessageProjection(value MessageProjection) MessageProjection {
	result := MessageProjection{
		canonicalName:   value.canonicalName,
		name:            value.name,
		fields:          append([]FieldProjection(nil), value.fields...),
		reservedNumbers: append([]int(nil), value.reservedNumbers...),
		reservedNames:   append([]string(nil), value.reservedNames...),
		enums:           make([]EnumProjection, len(value.enums)),
	}
	for index, enum := range value.enums {
		result.enums[index] = cloneEnumProjection(enum)
	}
	return result
}

func cloneEnumProjection(value EnumProjection) EnumProjection {
	result := EnumProjection{
		canonicalField:  value.canonicalField,
		identity:        value.identity,
		kind:            value.kind,
		sentinel:        cloneEnumValueProjection(value.sentinel),
		members:         make([]EnumValueProjection, len(value.members)),
		reservedNumbers: append([]int(nil), value.reservedNumbers...),
		reservedNames:   append([]string(nil), value.reservedNames...),
	}
	for index, member := range value.members {
		result.members[index] = cloneEnumValueProjection(member)
	}
	return result
}

func cloneEnumValueProjection(value EnumValueProjection) EnumValueProjection {
	return EnumValueProjection{canonical: append([]byte(nil), value.canonical...), name: value.name, number: value.number}
}

func decode(data []byte) (document, error) {
	if len(data) == 0 || int64(len(data)) > MaximumBytes {
		return document{}, fmt.Errorf("%w: %s is empty or exceeds %d bytes", ErrHistory, Path, MaximumBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result document
	if err := decoder.Decode(&result); err != nil {
		return document{}, fmt.Errorf("%w: decode %s: %v", ErrHistory, Path, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return document{}, fmt.Errorf("%w: %s contains trailing JSON", ErrHistory, Path)
	}
	if err := validateDocument(result); err != nil {
		return document{}, fmt.Errorf("%w: %s: %v", ErrHistory, Path, err)
	}
	canonical, err := encode(result, true)
	if err != nil {
		return document{}, fmt.Errorf("%w: encode %s: %v", ErrHistory, Path, err)
	}
	if !bytes.Equal(data, canonical) {
		return document{}, fmt.Errorf("%w: %s is not in canonical generated encoding", ErrHistory, Path)
	}
	return result, nil
}

func reconcileMessage(previous message, packageName string, fields []sdkmodel.Field) (message, error) {
	result := cloneMessage(previous)
	if len(fields) > maximumFields {
		return message{}, fmt.Errorf("%d fields exceeds maximum %d", len(fields), maximumFields)
	}
	for name, assignment := range result.Enums {
		assignment.Active = false
		result.Enums[name] = assignment
	}
	names := make([]string, len(fields))
	fieldsByName := make(map[string]sdkmodel.Field, len(fields))
	for index, field := range fields {
		names[index] = field.Name()
		if !validFieldName(names[index]) {
			return message{}, fmt.Errorf("field %q has no valid deterministic Protobuf name", names[index])
		}
		fieldsByName[names[index]] = field
	}
	sort.Strings(names)
	for index := 1; index < len(names); index++ {
		if names[index] == names[index-1] {
			return message{}, fmt.Errorf("field %q is duplicated", names[index])
		}
	}
	current := make(map[string]struct{}, len(names))
	usedNumbers := make(map[int]struct{}, len(result.Fields)+len(result.ReservedNumbers))
	for _, assignment := range result.Fields {
		usedNumbers[assignment.Number] = struct{}{}
	}
	for _, number := range result.ReservedNumbers {
		usedNumbers[number] = struct{}{}
	}
	reservedNames := make(map[string]struct{}, len(result.ReservedNames))
	for _, name := range result.ReservedNames {
		reservedNames[name] = struct{}{}
	}
	for _, name := range names {
		current[name] = struct{}{}
		if _, exists := result.Fields[name]; exists {
			continue
		}
		if _, reserved := reservedNames[name]; reserved {
			return message{}, fmt.Errorf("field %q reuses a permanently reserved generated name", name)
		}
		number, err := nextFieldNumber(usedNumbers)
		if err != nil {
			return message{}, err
		}
		usedNumbers[number] = struct{}{}
		result.Fields[name] = fieldAssignment{Name: name, Number: number}
	}
	for name, assignment := range result.Fields {
		if _, retained := current[name]; retained {
			continue
		}
		delete(result.Fields, name)
		result.ReservedNumbers = append(result.ReservedNumbers, assignment.Number)
		result.ReservedNames = append(result.ReservedNames, assignment.Name)
	}
	for _, name := range names {
		field := fieldsByName[name]
		members := field.EnumJSON()
		if len(members) == 0 {
			continue
		}
		identity := enumIdentity(packageName, result.Message, name)
		assignment, exists := result.Enums[name]
		if !exists {
			assignment = enumAssignment{
				Identity:        identity,
				Kind:            field.Kind(),
				Sentinel:        enumSymbol{Name: enumPrefix(identity) + "_UNSPECIFIED", Number: 0},
				Members:         []enumMember{},
				ReservedNumbers: []int{},
				ReservedNames:   []string{},
			}
		}
		if assignment.Identity != identity {
			return message{}, fmt.Errorf("field %q enum identity changed from %s to %s", name, assignment.Identity, identity)
		}
		if assignment.Kind != field.Kind() {
			return message{}, fmt.Errorf("field %q enum kind changed from %s to %s", name, assignment.Kind, field.Kind())
		}
		assignment, err := reconcileEnum(assignment, members)
		if err != nil {
			return message{}, fmt.Errorf("field %q enum: %v", name, err)
		}
		assignment.Active = true
		result.Enums[name] = assignment
	}
	sort.Ints(result.ReservedNumbers)
	sort.Strings(result.ReservedNames)
	return result, nil
}

func reconcileEnum(previous enumAssignment, values []json.RawMessage) (enumAssignment, error) {
	result := cloneEnum(previous)
	ordered := cloneRawMessages(values)
	sort.Slice(ordered, func(left, right int) bool { return bytes.Compare(ordered[left], ordered[right]) < 0 })
	for index := 1; index < len(ordered); index++ {
		if bytes.Equal(ordered[index], ordered[index-1]) {
			return enumAssignment{}, fmt.Errorf("canonical member %s is duplicated", ordered[index])
		}
	}
	existing := make(map[string]enumMember, len(result.Members))
	usedNumbers := make(map[int]string, len(result.Members)+len(result.ReservedNumbers)+1)
	usedNames := make(map[string]string, len(result.Members)+len(result.ReservedNames)+1)
	usedNumbers[0] = "sentinel"
	usedNames[result.Sentinel.Name] = "sentinel"
	for _, member := range result.Members {
		existing[string(member.Canonical)] = member
		usedNumbers[member.Number] = string(member.Canonical)
		usedNames[member.Name] = string(member.Canonical)
	}
	for _, number := range result.ReservedNumbers {
		usedNumbers[number] = "reserved"
	}
	for _, name := range result.ReservedNames {
		usedNames[name] = "reserved"
	}
	current := make(map[string]struct{}, len(ordered))
	for _, canonical := range ordered {
		key := string(canonical)
		current[key] = struct{}{}
		if _, retained := existing[key]; retained {
			continue
		}
		name := enumMemberName(result.Identity, canonical)
		if owner, collision := usedNames[name]; collision {
			return enumAssignment{}, fmt.Errorf("canonical member %s maps to permanently occupied generated name %s from %s", canonical, name, owner)
		}
		number, err := nextEnumNumber(usedNumbers)
		if err != nil {
			return enumAssignment{}, err
		}
		member := enumMember{Canonical: append([]byte(nil), canonical...), Name: name, Number: number}
		result.Members = append(result.Members, member)
		usedNumbers[number] = key
		usedNames[name] = key
	}
	retained := result.Members[:0]
	for _, member := range result.Members {
		if _, exists := current[string(member.Canonical)]; exists {
			retained = append(retained, member)
			continue
		}
		result.ReservedNumbers = append(result.ReservedNumbers, member.Number)
		result.ReservedNames = append(result.ReservedNames, member.Name)
	}
	result.Members = retained
	sort.Slice(result.Members, func(left, right int) bool {
		return bytes.Compare(result.Members[left].Canonical, result.Members[right].Canonical) < 0
	})
	sort.Ints(result.ReservedNumbers)
	sort.Strings(result.ReservedNames)
	return result, nil
}

func nextFieldNumber(used map[int]struct{}) (int, error) {
	for number := minimumFieldNumber; number <= maximumFieldNumber; number++ {
		if number == reservedRangeStart {
			number = reservedRangeEnd
			continue
		}
		if _, occupied := used[number]; !occupied {
			return number, nil
		}
	}
	return 0, errors.New("no permitted Protobuf field number remains")
}

func nextEnumNumber(used map[int]string) (int, error) {
	for number := 1; number <= maximumEnumNumber; number++ {
		if _, occupied := used[number]; !occupied {
			return number, nil
		}
	}
	return 0, errors.New("no permitted positive Protobuf enum number remains")
}

func emptyMessage(name string) message {
	return message{
		Message:         name,
		Fields:          make(map[string]fieldAssignment),
		Enums:           make(map[string]enumAssignment),
		ReservedNumbers: []int{},
		ReservedNames:   []string{},
	}
}

func validateDocument(value document) error {
	if value.ProjectionSchema != ProjectionSchema {
		return fmt.Errorf("projection_schema must equal %q", ProjectionSchema)
	}
	if err := validateInterfaceHistories(value.Interfaces); err != nil {
		return err
	}
	if value.LegacyCapabilities == nil || len(value.LegacyCapabilities) > maximumLegacyCapabilities {
		return fmt.Errorf("legacy_capabilities must be an object with at most %d entries", maximumLegacyCapabilities)
	}
	for id, record := range value.LegacyCapabilities {
		if id == "" || len(id) > 1024 || !utf8.ValidString(id) {
			return fmt.Errorf("legacy_capabilities contains invalid identity %q", id)
		}
		packageName, requestName, responseName, err := canonicalMessageNames(id)
		if err != nil {
			return fmt.Errorf("invalid Capability identity %q: %v", id, err)
		}
		if record.Request.Message != requestName || record.Response.Message != responseName {
			return fmt.Errorf("invalid Capability %s message identities: must be %s and %s", id, requestName, responseName)
		}
		if !validDigest(record.CanonicalContractDigest) {
			return fmt.Errorf("invalid Capability %s canonical_contract_digest", id)
		}
		if err := validateSources(record.Provenance); err != nil {
			return fmt.Errorf("invalid Capability %s provenance: %v", id, err)
		}
		if err := validateMessage(record.Request, packageName); err != nil {
			return fmt.Errorf("invalid Capability %s request: %v", id, err)
		}
		if err := validateMessage(record.Response, packageName); err != nil {
			return fmt.Errorf("invalid Capability %s response: %v", id, err)
		}
	}
	return nil
}

func canonicalMessageNames(id string) (string, string, string, error) {
	identities, err := protobufidentity.Build([]protobufidentity.Surface{{PublicID: id, CanonicalID: id}})
	if err != nil {
		return "", "", "", err
	}
	values := identities.Identities()
	if len(values) != 1 {
		return "", "", "", errors.New("canonical Protobuf identity is absent")
	}
	request, err := unqualifiedMessage(values[0].Package(), values[0].RequestType())
	if err != nil {
		return "", "", "", err
	}
	response, err := unqualifiedMessage(values[0].Package(), values[0].ResponseType())
	if err != nil {
		return "", "", "", err
	}
	return values[0].Package(), request, response, nil
}

func validateSources(values []string) error {
	if len(values) == 0 || len(values) > maximumFields {
		return errors.New("must contain at least one bounded source")
	}
	for index, value := range values {
		if value == "" || len(value) > 1024 || !utf8.ValidString(value) || hasControl(value) {
			return fmt.Errorf("source[%d] is invalid", index)
		}
		if index != 0 && values[index-1] >= value {
			return errors.New("sources must be unique and lexically sorted")
		}
	}
	return nil
}

func validateMessage(value message, packageName string) error {
	if !validMessageName(value.Message) {
		return fmt.Errorf("message %q is invalid", value.Message)
	}
	if value.Fields == nil || len(value.Fields) > maximumFields || value.Enums == nil || len(value.Enums) > maximumFields || value.ReservedNumbers == nil || value.ReservedNames == nil {
		return fmt.Errorf("message fields, enums, and reservations must be arrays or objects bounded to %d entries", maximumFields)
	}
	usedNumbers := make(map[int]string, len(value.Fields)+len(value.ReservedNumbers))
	usedNames := make(map[string]string, len(value.Fields)+len(value.ReservedNames))
	for path, assignment := range value.Fields {
		if path != assignment.Name || !validFieldName(path) {
			return fmt.Errorf("field path %q and generated name %q are inconsistent", path, assignment.Name)
		}
		if !permittedNumber(assignment.Number) {
			return fmt.Errorf("field %s has invalid number %d", path, assignment.Number)
		}
		if previous, duplicate := usedNumbers[assignment.Number]; duplicate {
			return fmt.Errorf("fields %s and %s duplicate number %d", previous, path, assignment.Number)
		}
		usedNumbers[assignment.Number] = path
		usedNames[assignment.Name] = path
	}
	for index, number := range value.ReservedNumbers {
		if !permittedNumber(number) {
			return fmt.Errorf("reserved number %d is invalid", number)
		}
		if index != 0 && value.ReservedNumbers[index-1] >= number {
			return errors.New("reserved_numbers must be unique and ascending")
		}
		if field, collision := usedNumbers[number]; collision {
			return fmt.Errorf("reserved number %d collides with field %s", number, field)
		}
		usedNumbers[number] = "reserved"
	}
	for index, name := range value.ReservedNames {
		if !validFieldName(name) {
			return fmt.Errorf("reserved name %q is invalid", name)
		}
		if index != 0 && value.ReservedNames[index-1] >= name {
			return errors.New("reserved_names must be unique and lexically sorted")
		}
		if field, collision := usedNames[name]; collision {
			return fmt.Errorf("reserved name %s collides with field %s", name, field)
		}
		usedNames[name] = "reserved"
	}
	for field, assignment := range value.Enums {
		if !validFieldName(field) {
			return fmt.Errorf("enum field path %q is invalid", field)
		}
		_, activeField := value.Fields[field]
		_, removedField := usedNames[field]
		if !activeField && !removedField {
			return fmt.Errorf("enum field %q is neither active nor reserved", field)
		}
		if assignment.Active && !activeField {
			return fmt.Errorf("active enum field %q has no active field assignment", field)
		}
		if err := validateEnum(assignment, enumIdentity(packageName, value.Message, field)); err != nil {
			return fmt.Errorf("enum field %s: %v", field, err)
		}
	}
	return nil
}

func validateEnum(value enumAssignment, expectedIdentity string) error {
	if value.Identity != expectedIdentity {
		return fmt.Errorf("identity %q must equal %q", value.Identity, expectedIdentity)
	}
	if !validEnumKind(value.Kind) {
		return fmt.Errorf("kind %q is not a supported canonical scalar enum kind", value.Kind)
	}
	wantSentinel := enumPrefix(value.Identity) + "_UNSPECIFIED"
	if value.Sentinel.Name != wantSentinel || value.Sentinel.Number != 0 {
		return fmt.Errorf("sentinel must be %s at numeric value 0", wantSentinel)
	}
	if value.Members == nil || value.ReservedNumbers == nil || value.ReservedNames == nil || len(value.Members)+len(value.ReservedNumbers) > maximumFields || len(value.Members)+len(value.ReservedNames) > maximumFields {
		return fmt.Errorf("members and reservations must be arrays bounded to %d entries", maximumFields)
	}
	if value.Active && len(value.Members) == 0 {
		return errors.New("active enum must contain at least one canonical member")
	}
	usedNumbers := map[int]string{0: value.Sentinel.Name}
	usedNames := map[string]string{value.Sentinel.Name: "sentinel"}
	for index, member := range value.Members {
		if index != 0 && bytes.Compare(value.Members[index-1].Canonical, member.Canonical) >= 0 {
			return errors.New("members must be unique and sorted by canonical value")
		}
		if err := validateCanonicalEnumValue(member.Canonical, value.Kind); err != nil {
			return fmt.Errorf("member[%d] canonical value: %v", index, err)
		}
		if want := enumMemberName(value.Identity, member.Canonical); member.Name != want {
			return fmt.Errorf("member %s generated name %q must equal %q", member.Canonical, member.Name, want)
		}
		if member.Number <= 0 || member.Number > maximumEnumNumber {
			return fmt.Errorf("member %s has invalid positive number %d", member.Canonical, member.Number)
		}
		if previous, duplicate := usedNumbers[member.Number]; duplicate {
			return fmt.Errorf("member %s and %s duplicate number %d", previous, member.Canonical, member.Number)
		}
		if previous, duplicate := usedNames[member.Name]; duplicate {
			return fmt.Errorf("member %s and %s duplicate generated name %s", previous, member.Canonical, member.Name)
		}
		usedNumbers[member.Number] = string(member.Canonical)
		usedNames[member.Name] = string(member.Canonical)
	}
	for index, number := range value.ReservedNumbers {
		if number <= 0 || number > maximumEnumNumber {
			return fmt.Errorf("reserved enum number %d is invalid", number)
		}
		if index != 0 && value.ReservedNumbers[index-1] >= number {
			return errors.New("reserved enum numbers must be unique and ascending")
		}
		if member, collision := usedNumbers[number]; collision {
			return fmt.Errorf("reserved enum number %d collides with %s", number, member)
		}
		usedNumbers[number] = "reserved"
	}
	for index, name := range value.ReservedNames {
		if !validReservedEnumMemberName(value.Identity, name) {
			return fmt.Errorf("reserved enum name %q is invalid", name)
		}
		if index != 0 && value.ReservedNames[index-1] >= name {
			return errors.New("reserved enum names must be unique and lexically sorted")
		}
		if member, collision := usedNames[name]; collision {
			return fmt.Errorf("reserved enum name %s collides with %s", name, member)
		}
		usedNames[name] = "reserved"
	}
	return nil
}

func encode(value document, pretty bool) ([]byte, error) {
	var data []byte
	var err error
	if pretty {
		data, err = json.MarshalIndent(value, "", "  ")
	} else {
		data, err = json.Marshal(value)
	}
	if err != nil {
		return nil, err
	}
	if pretty {
		data = append(data, '\n')
	}
	if int64(len(data)) > MaximumBytes {
		return nil, fmt.Errorf("wire map exceeds %d bytes", MaximumBytes)
	}
	return data, nil
}

func encodeActive(value document) ([]byte, error) {
	active := activeDocument{
		ProjectionSchema:   ProjectionSchema,
		Interfaces:         activeInterfaceHistory(value.Interfaces),
		LegacyCapabilities: make(map[string]activeCapability),
	}
	for id, record := range value.LegacyCapabilities {
		if !record.Active {
			continue
		}
		active.LegacyCapabilities[id] = activeCapability{
			CanonicalContractDigest: record.CanonicalContractDigest,
			Request:                 cloneActiveMessage(record.Request),
			Response:                cloneActiveMessage(record.Response),
		}
	}
	return json.Marshal(active)
}

func cloneDocument(value document) document {
	result := document{
		ProjectionSchema:   value.ProjectionSchema,
		Interfaces:         cloneInterfaceHistories(value.Interfaces),
		LegacyCapabilities: make(map[string]capability, len(value.LegacyCapabilities)),
	}
	for id, record := range value.LegacyCapabilities {
		record.Provenance = append([]string(nil), record.Provenance...)
		record.Request = cloneMessage(record.Request)
		record.Response = cloneMessage(record.Response)
		result.LegacyCapabilities[id] = record
	}
	return result
}

func cloneMessage(value message) message {
	fields := make(map[string]fieldAssignment, len(value.Fields))
	for path, assignment := range value.Fields {
		fields[path] = assignment
	}
	enums := make(map[string]enumAssignment, len(value.Enums))
	for path, assignment := range value.Enums {
		enums[path] = cloneEnum(assignment)
	}
	reservedNumbers := make([]int, len(value.ReservedNumbers))
	copy(reservedNumbers, value.ReservedNumbers)
	reservedNames := make([]string, len(value.ReservedNames))
	copy(reservedNames, value.ReservedNames)
	return message{
		Message:         value.Message,
		Fields:          fields,
		Enums:           enums,
		ReservedNumbers: reservedNumbers,
		ReservedNames:   reservedNames,
	}
}

func cloneActiveMessage(value message) message {
	result := cloneMessage(value)
	result.Enums = make(map[string]enumAssignment)
	for path, assignment := range value.Enums {
		if assignment.Active {
			result.Enums[path] = cloneEnum(assignment)
		}
	}
	return result
}

func cloneEnum(value enumAssignment) enumAssignment {
	result := value
	result.Members = make([]enumMember, len(value.Members))
	for index, member := range value.Members {
		result.Members[index] = member
		result.Members[index].Canonical = append(json.RawMessage(nil), member.Canonical...)
	}
	result.ReservedNumbers = make([]int, len(value.ReservedNumbers))
	copy(result.ReservedNumbers, value.ReservedNumbers)
	result.ReservedNames = make([]string, len(value.ReservedNames))
	copy(result.ReservedNames, value.ReservedNames)
	return result
}

func cloneRawMessages(values []json.RawMessage) []json.RawMessage {
	result := make([]json.RawMessage, len(values))
	for index, value := range values {
		result[index] = append(json.RawMessage(nil), value...)
	}
	return result
}

func enumIdentity(packageName, messageName, fieldName string) string {
	return protobufidentity.EnumType(packageName+"."+messageName, fieldName)
}

func enumPrefix(identity string) string {
	if index := strings.LastIndexByte(identity, '.'); index >= 0 {
		identity = identity[index+1:]
	}
	return strings.ToUpper(identity)
}

func enumMemberName(identity string, canonical json.RawMessage) string {
	sum := sha256.Sum256(canonical)
	return enumPrefix(identity) + "_VALUE_" + strings.ToUpper(hex.EncodeToString(sum[:]))
}

func validEnumKind(value sdkmodel.Kind) bool {
	switch value {
	case sdkmodel.KindString, sdkmodel.KindInteger, sdkmodel.KindNumber, sdkmodel.KindBoolean:
		return true
	default:
		return false
	}
}

func validateCanonicalEnumValue(value json.RawMessage, kind sdkmodel.Kind) error {
	if len(value) == 0 || !json.Valid(value) {
		return errors.New("must be one valid canonical JSON scalar")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return errors.New("must be one valid canonical JSON scalar")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("must contain exactly one JSON scalar")
	}
	var normalized []byte
	var err error
	switch kind {
	case sdkmodel.KindString:
		text, ok := decoded.(string)
		if !ok {
			return errors.New("must be a JSON string")
		}
		normalized, err = json.Marshal(text)
	case sdkmodel.KindInteger:
		number, ok := decoded.(json.Number)
		if !ok || !canonicalInteger(number.String()) {
			return errors.New("must be a canonical signed 64-bit JSON integer")
		}
		parsed, parseErr := strconv.ParseInt(number.String(), 10, 64)
		if parseErr != nil {
			return errors.New("must be a canonical signed 64-bit JSON integer")
		}
		normalized, err = json.Marshal(parsed)
	case sdkmodel.KindNumber:
		number, ok := decoded.(json.Number)
		if !ok {
			return errors.New("must be a finite canonical JSON number")
		}
		if canonicalInteger(number.String()) {
			if integer, parseErr := strconv.ParseInt(number.String(), 10, 64); parseErr == nil {
				normalized, err = json.Marshal(integer)
				break
			}
		}
		parsed, parseErr := number.Float64()
		if parseErr != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
			return errors.New("must be a finite canonical JSON number")
		}
		normalized, err = json.Marshal(parsed)
	case sdkmodel.KindBoolean:
		boolean, ok := decoded.(bool)
		if !ok {
			return errors.New("must be a JSON boolean")
		}
		normalized, err = json.Marshal(boolean)
	default:
		return fmt.Errorf("unsupported enum kind %q", kind)
	}
	if err != nil || !bytes.Equal(normalized, value) {
		return errors.New("must use canonical JSON encoding")
	}
	return nil
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

func validReservedEnumMemberName(identity, value string) bool {
	prefix := enumPrefix(identity) + "_VALUE_"
	encoded, ok := strings.CutPrefix(value, prefix)
	if !ok || len(encoded) != sha256.Size*2 {
		return false
	}
	for _, character := range encoded {
		if (character < '0' || character > '9') && (character < 'A' || character > 'F') {
			return false
		}
	}
	return true
}

func unqualifiedMessage(packageName, qualified string) (string, error) {
	prefix := packageName + "."
	name, ok := strings.CutPrefix(qualified, prefix)
	if !ok || !validMessageName(name) {
		return "", fmt.Errorf("%q is not a message in package %q", qualified, packageName)
	}
	return name, nil
}

func permittedNumber(value int) bool {
	return value >= minimumFieldNumber && value <= maximumFieldNumber && (value < reservedRangeStart || value > reservedRangeEnd)
}

func validFieldName(value string) bool {
	if value == "" || len(value) > 128 || value[0] < 'a' || value[0] > 'z' || value[len(value)-1] == '_' {
		return false
	}
	underscore := false
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			underscore = false
		case char == '_' && !underscore:
			underscore = true
		default:
			return false
		}
	}
	return true
}

func validMessageName(value string) bool {
	if value == "" || len(value) > 512 || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	for _, char := range value {
		if !((char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')) {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	encoded, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(encoded) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(encoded)
	return err == nil && len(decoded) == sha256.Size
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func combinedProjectionDigest(legacy, interfaces string) string {
	return digest([]byte("plystra.proto-wire-map.projection/v1\x00" + legacy + "\x00" + interfaces))
}

func hasControl(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}
