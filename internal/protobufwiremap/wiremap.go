// Package protobufwiremap owns deterministic committed Protobuf field-number
// history for canonical Capability request and response messages.
package protobufwiremap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
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
	// ProjectionSchema identifies the strict initial wire-map schema.
	ProjectionSchema = "plystra.proto-wire-map/v1"
	// MaximumBytes bounds managed history before parsing.
	MaximumBytes int64 = 16 << 20

	minimumFieldNumber  = 1
	maximumFieldNumber  = 536870911
	reservedRangeStart  = 19000
	reservedRangeEnd    = 19999
	maximumCapabilities = 4096
	maximumFields       = 16384
)

var (
	// ErrBuild reports that current projection and prior history could not be
	// reconciled into one valid deterministic map.
	ErrBuild = errors.New("build Protobuf wire map")
	// ErrHistory reports missing, corrupt, modified, or inconsistent managed
	// field history.
	ErrHistory = errors.New("invalid Protobuf wire-map history")
	// ErrProjection reports an invalid current normalized Protobuf projection.
	ErrProjection = errors.New("invalid Protobuf wire-map projection")
)

// Map is one immutable validated current wire map. CanonicalJSON is committed
// history; ActiveJSON contains only build-affecting active assignments.
type Map struct {
	canonicalJSON    []byte
	activeJSON       []byte
	digest           string
	activeDigest     string
	projectionDigest string
	prepared         bool
}

// Valid reports whether Build produced the map.
func (m Map) Valid() bool {
	return m.prepared && len(m.canonicalJSON) != 0 && len(m.activeJSON) != 0 && validDigest(m.digest) && validDigest(m.activeDigest) && validDigest(m.projectionDigest)
}

// CanonicalJSON returns defensive canonical committed history bytes.
func (m Map) CanonicalJSON() []byte { return append([]byte(nil), m.canonicalJSON...) }

// ActiveJSON returns defensive canonical build-affecting assignment bytes.
func (m Map) ActiveJSON() []byte { return append([]byte(nil), m.activeJSON...) }

// Digest returns the digest of CanonicalJSON, including inactive history.
func (m Map) Digest() string { return m.digest }

// ActiveDigest returns the digest of ActiveJSON.
func (m Map) ActiveDigest() string { return m.activeDigest }

// ProjectionDigest identifies the exact normalized current Protobuf model
// against which this map was reconciled. It is intentionally not serialized.
func (m Map) ProjectionDigest() string { return m.projectionDigest }

type document struct {
	ProjectionSchema string                `json:"projection_schema"`
	Capabilities     map[string]capability `json:"capabilities"`
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
	ReservedNumbers []int                      `json:"reserved_numbers"`
	ReservedNames   []string                   `json:"reserved_names"`
}

type fieldAssignment struct {
	Name   string `json:"name"`
	Number int    `json:"number"`
}

type activeDocument struct {
	ProjectionSchema string                      `json:"projection_schema"`
	Capabilities     map[string]activeCapability `json:"capabilities"`
}

type activeCapability struct {
	CanonicalContractDigest string  `json:"canonical_contract_digest"`
	Request                 message `json:"request"`
	Response                message `json:"response"`
}

// Build reconciles current canonical operations with exact prior managed
// history. previousDigest must be the digest retained in the prior generated
// application manifest whenever previousExists is true. A digest without a
// file, or a file without that baseline, fails rather than guessing.
func Build(model protobufmodel.Model, previous []byte, previousExists bool, previousDigest string) (Map, error) {
	if !model.Valid() {
		return Map{}, fmt.Errorf("%w: %w: normalized Protobuf model is absent", ErrBuild, ErrProjection)
	}
	current := document{ProjectionSchema: ProjectionSchema, Capabilities: make(map[string]capability)}
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

	for id, existing := range current.Capabilities {
		existing.Active = false
		current.Capabilities[id] = existing
	}
	for _, operation := range model.Operations() {
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
		record, exists := current.Capabilities[id]
		if !exists {
			record = capability{
				Request:  emptyMessage(requestName),
				Response: emptyMessage(responseName),
			}
		}
		if record.Request.Message != requestName || record.Response.Message != responseName {
			return Map{}, fmt.Errorf("%w: %w: Capability %s message identity changed from %s/%s to %s/%s", ErrBuild, ErrHistory, id, record.Request.Message, record.Response.Message, requestName, responseName)
		}
		record.Request, err = reconcileMessage(record.Request, operation.Request())
		if err != nil {
			return Map{}, fmt.Errorf("%w: %w: Capability %s request: %v", ErrBuild, ErrHistory, id, err)
		}
		record.Response, err = reconcileMessage(record.Response, operation.Response())
		if err != nil {
			return Map{}, fmt.Errorf("%w: %w: Capability %s response: %v", ErrBuild, ErrHistory, id, err)
		}
		record.Active = true
		record.CanonicalContractDigest = operation.ContractDigest()
		record.Provenance = operation.Sources()
		current.Capabilities[id] = record
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
		canonicalJSON:    canonical,
		activeJSON:       active,
		digest:           digest(canonical),
		activeDigest:     digest(active),
		projectionDigest: model.Digest(),
		prepared:         true,
	}, nil
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

func reconcileMessage(previous message, fields []sdkmodel.Field) (message, error) {
	result := cloneMessage(previous)
	if len(fields) > maximumFields {
		return message{}, fmt.Errorf("%d fields exceeds maximum %d", len(fields), maximumFields)
	}
	names := make([]string, len(fields))
	for index, field := range fields {
		names[index] = field.Name()
		if !validFieldName(names[index]) {
			return message{}, fmt.Errorf("field %q has no valid deterministic Protobuf name", names[index])
		}
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

func emptyMessage(name string) message {
	return message{
		Message:         name,
		Fields:          make(map[string]fieldAssignment),
		ReservedNumbers: []int{},
		ReservedNames:   []string{},
	}
}

func validateDocument(value document) error {
	if value.ProjectionSchema != ProjectionSchema {
		return fmt.Errorf("projection_schema must equal %q", ProjectionSchema)
	}
	if value.Capabilities == nil || len(value.Capabilities) > maximumCapabilities {
		return fmt.Errorf("capabilities must be an object with at most %d entries", maximumCapabilities)
	}
	for id, record := range value.Capabilities {
		if id == "" || len(id) > 1024 || !utf8.ValidString(id) {
			return fmt.Errorf("capabilities contains invalid identity %q", id)
		}
		requestName, responseName, err := canonicalMessageNames(id)
		if err != nil {
			return fmt.Errorf("Capability identity %q is invalid: %v", id, err)
		}
		if record.Request.Message != requestName || record.Response.Message != responseName {
			return fmt.Errorf("Capability %s message identities must be %s and %s", id, requestName, responseName)
		}
		if !validDigest(record.CanonicalContractDigest) {
			return fmt.Errorf("Capability %s has invalid canonical_contract_digest", id)
		}
		if err := validateSources(record.Provenance); err != nil {
			return fmt.Errorf("Capability %s provenance: %v", id, err)
		}
		if err := validateMessage(record.Request); err != nil {
			return fmt.Errorf("Capability %s request: %v", id, err)
		}
		if err := validateMessage(record.Response); err != nil {
			return fmt.Errorf("Capability %s response: %v", id, err)
		}
	}
	return nil
}

func canonicalMessageNames(id string) (string, string, error) {
	identities, err := protobufidentity.Build([]protobufidentity.Surface{{PublicID: id, CanonicalID: id}})
	if err != nil {
		return "", "", err
	}
	values := identities.Identities()
	if len(values) != 1 {
		return "", "", errors.New("canonical Protobuf identity is absent")
	}
	request, err := unqualifiedMessage(values[0].Package(), values[0].RequestType())
	if err != nil {
		return "", "", err
	}
	response, err := unqualifiedMessage(values[0].Package(), values[0].ResponseType())
	if err != nil {
		return "", "", err
	}
	return request, response, nil
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

func validateMessage(value message) error {
	if !validMessageName(value.Message) {
		return fmt.Errorf("message %q is invalid", value.Message)
	}
	if value.Fields == nil || len(value.Fields) > maximumFields || value.ReservedNumbers == nil || value.ReservedNames == nil {
		return fmt.Errorf("message fields and reservations must be arrays or objects bounded to %d entries", maximumFields)
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
	active := activeDocument{ProjectionSchema: ProjectionSchema, Capabilities: make(map[string]activeCapability)}
	for id, record := range value.Capabilities {
		if !record.Active {
			continue
		}
		active.Capabilities[id] = activeCapability{
			CanonicalContractDigest: record.CanonicalContractDigest,
			Request:                 cloneMessage(record.Request),
			Response:                cloneMessage(record.Response),
		}
	}
	return json.Marshal(active)
}

func cloneDocument(value document) document {
	result := document{ProjectionSchema: value.ProjectionSchema, Capabilities: make(map[string]capability, len(value.Capabilities))}
	for id, record := range value.Capabilities {
		record.Provenance = append([]string(nil), record.Provenance...)
		record.Request = cloneMessage(record.Request)
		record.Response = cloneMessage(record.Response)
		result.Capabilities[id] = record
	}
	return result
}

func cloneMessage(value message) message {
	fields := make(map[string]fieldAssignment, len(value.Fields))
	for path, assignment := range value.Fields {
		fields[path] = assignment
	}
	reservedNumbers := make([]int, len(value.ReservedNumbers))
	copy(reservedNumbers, value.ReservedNumbers)
	reservedNames := make([]string, len(value.ReservedNames))
	copy(reservedNames, value.ReservedNames)
	return message{
		Message:         value.Message,
		Fields:          fields,
		ReservedNumbers: reservedNumbers,
		ReservedNames:   reservedNames,
	}
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

func hasControl(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) {
			return true
		}
	}
	return false
}
