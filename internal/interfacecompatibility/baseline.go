// Package interfacecompatibility owns the deterministic authored Interface
// Go-shape compatibility baseline generated for one Plystra Project.
package interfacecompatibility

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfaceid"
	"golang.org/x/mod/module"
)

const (
	// Path is the one CLI-owned authored Interface shape baseline.
	Path = "generated/compatibility/interfaces.json"
	// Schema identifies the strict pointer-aware authored Interface shape record.
	Schema = "plystra.interface-shape-baseline/v2"
	// ShapeSchema domain-separates one Interface shape digest.
	ShapeSchema = "plystra.interface-shape/v2"
	// MaximumBytes bounds a managed baseline before parsing.
	MaximumBytes int64 = 16 << 20

	baselineSchemaV1 = "plystra.interface-shape-baseline/v1"
	shapeSchemaV1    = "plystra.interface-shape/v1"

	maximumInterfaces = 4096
	maximumMessages   = 16384
	maximumFields     = 65536
	maximumNameBytes  = 1024
	maximumTypeBytes  = 2048
)

var (
	// ErrInvalid reports malformed current input or baseline state.
	ErrInvalid = errors.New("invalid Interface compatibility baseline")
	// ErrHistory reports missing, corrupt, modified, or noncanonical prior
	// compatibility evidence.
	ErrHistory = errors.New("invalid Interface compatibility baseline history")
)

// Baseline is one immutable exact snapshot of visible authored Interface Go
// shapes. It deliberately excludes metadata, generated projections,
// Implementations, configuration, Secrets, source paths, and module versions.
type Baseline struct {
	record        wireRecord
	canonicalJSON []byte
	recordJSON    []byte
	digest        string
	prepared      bool
}

// Schema returns the exact baseline schema.
func (b Baseline) Schema() string {
	if !b.prepared {
		return ""
	}
	return b.record.Schema
}

// Interfaces returns exact-ID-sorted defensive Interface shape views.
func (b Baseline) Interfaces() []Interface {
	result := make([]Interface, len(b.record.Interfaces))
	for index, value := range b.record.Interfaces {
		result[index] = Interface{record: cloneWireInterface(value)}
	}
	return result
}

// CanonicalJSON returns the defensive digest input without the top-level
// digest.
func (b Baseline) CanonicalJSON() []byte {
	return append([]byte(nil), b.canonicalJSON...)
}

// RecordJSON returns the defensive strict generated baseline record.
func (b Baseline) RecordJSON() []byte {
	return append([]byte(nil), b.recordJSON...)
}

// Digest returns the lowercase SHA-256 digest of CanonicalJSON.
func (b Baseline) Digest() string { return b.digest }

// Valid reports whether this value is complete and internally canonical.
func (b Baseline) Valid() bool {
	if !b.prepared || !validBaselineSchema(b.record.Schema) || b.record.Digest != b.digest {
		return false
	}
	if err := validateInterfaces(b.record.Interfaces, true, b.record.Schema); err != nil {
		return false
	}
	canonical, err := encodeCanonical(b.record.Schema, b.record.Interfaces)
	if err != nil || !bytes.Equal(canonical, b.canonicalJSON) || digest(canonical) != b.digest {
		return false
	}
	record, err := encodeRecord(b.record.Schema, b.record.Interfaces, b.digest)
	return err == nil && bytes.Equal(record, b.recordJSON)
}

// Interface is one immutable normalized authored Interface Go shape.
type Interface struct {
	record wireInterface
}

// ID returns the exact canonical Interface ID.
func (i Interface) ID() string { return i.record.ID }

// PackagePath returns the canonical authored Go package path.
func (i Interface) PackagePath() string { return i.record.PackagePath }

// Method returns the one exported operation method name.
func (i Interface) Method() string { return i.record.Method }

// Request returns the exported request message name.
func (i Interface) Request() string { return i.record.Request }

// Response returns the exported response message name.
func (i Interface) Response() string { return i.record.Response }

// Digest returns the exact domain-separated Interface shape digest.
func (i Interface) Digest() string { return i.record.Digest }

// Messages returns Go-name-sorted defensive message views.
func (i Interface) Messages() []Message {
	result := make([]Message, len(i.record.Messages))
	for index, value := range i.record.Messages {
		result[index] = Message{record: cloneWireMessage(value)}
	}
	return result
}

// Message is one immutable exported same-package message shape.
type Message struct {
	record wireMessage
}

// Name returns the exported Go message name.
func (m Message) Name() string { return m.record.Name }

// Fields returns field-number-then-name-sorted defensive field views.
func (m Message) Fields() []Field {
	result := make([]Field, len(m.record.Fields))
	for index, value := range m.record.Fields {
		result[index] = Field{record: value}
	}
	return result
}

// Field is one immutable authored Go field shape.
type Field struct {
	record wireField
}

// Number returns the stable positive authored field number.
func (f Field) Number() uint64 { return f.record.Number }

// GoName returns the exported Go field name.
func (f Field) GoName() string { return f.record.GoName }

// JSONName returns the effective public JSON field name.
func (f Field) JSONName() string { return f.record.JSONName }

// Required reports whether the authored plystra tag marks the field required.
func (f Field) Required() bool { return f.record.Required }

// PointerDepth returns the exact direct pointer depth around the canonical
// field value. Valid values are zero, one, or two.
func (f Field) PointerDepth() uint8 { return f.record.PointerDepth }

// Type returns the canonical closed-graph Go field type.
func (f Field) Type() string { return f.record.Type }

// New constructs the exact current authored Interface shape baseline
// independently of discovery order.
func New(contracts []interfacecontract.Contract) (Baseline, error) {
	if len(contracts) > maximumInterfaces {
		return Baseline{}, fmt.Errorf("%w: %d Interfaces exceeds maximum %d", ErrInvalid, len(contracts), maximumInterfaces)
	}
	interfaces := make([]wireInterface, len(contracts))
	for index, contract := range contracts {
		value, err := interfaceFromContract(contract)
		if err != nil {
			return Baseline{}, fmt.Errorf("%w: contracts[%d]: %v", ErrInvalid, index, err)
		}
		interfaces[index] = value
	}
	sort.Slice(interfaces, func(left, right int) bool {
		return interfaces[left].ID < interfaces[right].ID
	})
	if err := validateInterfaces(interfaces, true, Schema); err != nil {
		return Baseline{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return build(interfaces)
}

// Decode strictly restores one canonical generated baseline record.
func Decode(data []byte) (Baseline, error) {
	if len(data) == 0 || int64(len(data)) > MaximumBytes {
		return Baseline{}, fmt.Errorf("%w: record must contain between 1 and %d bytes", ErrHistory, MaximumBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record wireRecord
	if err := decoder.Decode(&record); err != nil {
		return Baseline{}, fmt.Errorf("%w: decode record: %v", ErrHistory, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Baseline{}, fmt.Errorf("%w: record contains trailing JSON", ErrHistory)
	}
	if !validBaselineSchema(record.Schema) {
		return Baseline{}, fmt.Errorf("%w: schema must equal %q or %q", ErrHistory, baselineSchemaV1, Schema)
	}
	if err := validateInterfaces(record.Interfaces, true, record.Schema); err != nil {
		return Baseline{}, fmt.Errorf("%w: %v", ErrHistory, err)
	}
	canonical, err := encodeCanonical(record.Schema, record.Interfaces)
	if err != nil {
		return Baseline{}, fmt.Errorf("%w: encode canonical record: %v", ErrHistory, err)
	}
	if !validDigest(record.Digest) || record.Digest != digest(canonical) {
		return Baseline{}, fmt.Errorf("%w: digest does not match the canonical Interface shapes", ErrHistory)
	}
	encoded, err := encodeRecord(record.Schema, record.Interfaces, record.Digest)
	if err != nil {
		return Baseline{}, fmt.Errorf("%w: encode record: %v", ErrHistory, err)
	}
	if !bytes.Equal(encoded, data) {
		return Baseline{}, fmt.Errorf("%w: record is not in canonical byte form", ErrHistory)
	}
	return buildWithEncoding(record.Schema, record.Interfaces, canonical, encoded, record.Digest), nil
}

// Reconcile constructs the current baseline and compares it with exact prior
// owned evidence. A missing prior record is the valid initial state.
func Reconcile(contracts []interfacecontract.Contract, previous []byte, previousExists bool) (Baseline, Comparison, error) {
	current, err := New(contracts)
	if err != nil {
		return Baseline{}, Comparison{}, err
	}
	prior, err := New(nil)
	if err != nil {
		return Baseline{}, Comparison{}, err
	}
	if previousExists {
		prior, err = Decode(previous)
		if err != nil {
			return Baseline{}, Comparison{}, err
		}
	} else if len(previous) != 0 {
		return Baseline{}, Comparison{}, fmt.Errorf("%w: absent prior record has bytes", ErrHistory)
	}
	comparison, err := Compare(prior, current)
	if err != nil {
		return Baseline{}, Comparison{}, err
	}
	return current, comparison, nil
}

// ChangeKind classifies one exact Interface shape-baseline difference.
type ChangeKind string

const (
	ChangeAdded   ChangeKind = "added"
	ChangeRemoved ChangeKind = "removed"
	ChangeChanged ChangeKind = "changed"
)

// Change is one immutable exact-ID compatibility difference.
type Change struct {
	kind                      ChangeKind
	id                        string
	previousDigest            string
	currentDigest             string
	additivePointerFieldsOnly bool
}

// Kind returns added, removed, or changed.
func (c Change) Kind() ChangeKind { return c.kind }

// ID returns the exact canonical Interface ID.
func (c Change) ID() string { return c.id }

// PreviousDigest returns the prior shape digest when one existed.
func (c Change) PreviousDigest() string { return c.previousDigest }

// CurrentDigest returns the current shape digest when one exists.
func (c Change) CurrentDigest() string { return c.currentDigest }

// AdditivePointerFieldsOnly reports whether the changed Interface preserves
// every existing shape byte and only adds non-required one- or two-pointer
// fields. Any newly reachable messages must be rooted through those fields.
func (c Change) AdditivePointerFieldsOnly() bool {
	return c.kind == ChangeChanged && c.additivePointerFieldsOnly
}

// Comparison is one immutable exact-ID-sorted baseline comparison.
type Comparison struct {
	previousDigest string
	currentDigest  string
	changes        []Change
	prepared       bool
}

// Clean reports whether every authored Interface Go shape is unchanged.
func (c Comparison) Clean() bool { return c.prepared && len(c.changes) == 0 }

// PreviousDigest returns the compared prior baseline digest.
func (c Comparison) PreviousDigest() string { return c.previousDigest }

// CurrentDigest returns the compared current baseline digest.
func (c Comparison) CurrentDigest() string { return c.currentDigest }

// Changes returns defensive differences sorted by exact Interface ID.
func (c Comparison) Changes() []Change { return append([]Change(nil), c.changes...) }

// Valid reports whether the comparison has complete baseline identities.
func (c Comparison) Valid() bool {
	if !c.prepared || !validDigest(c.previousDigest) || !validDigest(c.currentDigest) {
		return false
	}
	for index, change := range c.changes {
		if _, err := interfaceid.Parse(change.id); err != nil {
			return false
		}
		if index > 0 && c.changes[index-1].id >= change.id {
			return false
		}
		switch change.kind {
		case ChangeAdded:
			if change.previousDigest != "" || !validDigest(change.currentDigest) || change.additivePointerFieldsOnly {
				return false
			}
		case ChangeRemoved:
			if !validDigest(change.previousDigest) || change.currentDigest != "" || change.additivePointerFieldsOnly {
				return false
			}
		case ChangeChanged:
			if !validDigest(change.previousDigest) || !validDigest(change.currentDigest) || change.previousDigest == change.currentDigest {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// Compare returns exact added, removed, and changed authored Interface shapes.
func Compare(previous, current Baseline) (Comparison, error) {
	if !previous.Valid() || !current.Valid() {
		return Comparison{}, fmt.Errorf("%w: both compared baselines must be valid", ErrInvalid)
	}
	if previous.record.Schema == Schema && current.record.Schema == baselineSchemaV1 {
		return Comparison{}, fmt.Errorf("%w: Interface shape schema cannot downgrade from v2 to v1", ErrInvalid)
	}
	before := make(map[string]wireInterface, len(previous.record.Interfaces))
	after := make(map[string]wireInterface, len(current.record.Interfaces))
	identifiers := make(map[string]struct{}, len(previous.record.Interfaces)+len(current.record.Interfaces))
	for _, value := range previous.record.Interfaces {
		before[value.ID] = value
		identifiers[value.ID] = struct{}{}
	}
	for _, value := range current.record.Interfaces {
		after[value.ID] = value
		identifiers[value.ID] = struct{}{}
	}
	ordered := make([]string, 0, len(identifiers))
	for identifier := range identifiers {
		ordered = append(ordered, identifier)
	}
	sort.Strings(ordered)
	changes := make([]Change, 0)
	for _, identifier := range ordered {
		previousValue, previousExists := before[identifier]
		currentValue, currentExists := after[identifier]
		switch {
		case !previousExists:
			changes = append(changes, Change{kind: ChangeAdded, id: identifier, currentDigest: currentValue.Digest})
		case !currentExists:
			changes = append(changes, Change{kind: ChangeRemoved, id: identifier, previousDigest: previousValue.Digest})
		case !equivalentInterfaceShape(previousValue, previous.record.Schema, currentValue, current.record.Schema):
			changes = append(changes, Change{
				kind:                      ChangeChanged,
				id:                        identifier,
				previousDigest:            previousValue.Digest,
				currentDigest:             currentValue.Digest,
				additivePointerFieldsOnly: onlyAddsOptionalPointerFields(previousValue, currentValue),
			})
		}
	}
	result := Comparison{
		previousDigest: previous.digest,
		currentDigest:  current.digest,
		changes:        changes,
		prepared:       true,
	}
	if !result.Valid() {
		return Comparison{}, fmt.Errorf("%w: constructed comparison is invalid", ErrInvalid)
	}
	return result, nil
}

type wireRecord struct {
	Schema     string          `json:"schema"`
	Interfaces []wireInterface `json:"interfaces"`
	Digest     string          `json:"digest"`
}

type canonicalRecord struct {
	Schema     string          `json:"schema"`
	Interfaces []wireInterface `json:"interfaces"`
}

type wireInterface struct {
	ID          string        `json:"id"`
	PackagePath string        `json:"package"`
	Method      string        `json:"method"`
	Request     string        `json:"request"`
	Response    string        `json:"response"`
	Messages    []wireMessage `json:"messages"`
	Digest      string        `json:"digest"`
}

type shapeDigestRecord struct {
	Schema   string        `json:"schema"`
	ID       string        `json:"id"`
	Package  string        `json:"package"`
	Method   string        `json:"method"`
	Request  string        `json:"request"`
	Response string        `json:"response"`
	Messages []wireMessage `json:"messages"`
}

type wireMessage struct {
	Name   string      `json:"name"`
	Fields []wireField `json:"fields"`
}

type wireField struct {
	Number       uint64 `json:"number"`
	GoName       string `json:"go_name"`
	JSONName     string `json:"json_name"`
	Required     bool   `json:"required"`
	PointerDepth uint8  `json:"pointer_depth,omitempty"`
	Type         string `json:"type"`
}

func interfaceFromContract(contract interfacecontract.Contract) (wireInterface, error) {
	value := wireInterface{
		ID:          contract.ID().String(),
		PackagePath: contract.PackagePath(),
		Method:      contract.MethodName(),
		Request:     contract.RequestName(),
		Response:    contract.ResponseName(),
	}
	messages := contract.Messages()
	value.Messages = make([]wireMessage, len(messages))
	for messageIndex, message := range messages {
		fields := message.Fields()
		value.Messages[messageIndex] = wireMessage{
			Name:   message.Name(),
			Fields: make([]wireField, len(fields)),
		}
		for fieldIndex, field := range fields {
			jsonName := field.Name()
			if field.HasExplicitJSONName() {
				jsonName = field.JSONName()
			}
			value.Messages[messageIndex].Fields[fieldIndex] = wireField{
				Number:       field.Number(),
				GoName:       field.Name(),
				JSONName:     jsonName,
				Required:     field.Required(),
				PointerDepth: field.PointerDepth(),
				Type:         field.Type().Canonical(),
			}
		}
		sort.Slice(value.Messages[messageIndex].Fields, func(left, right int) bool {
			leftField := value.Messages[messageIndex].Fields[left]
			rightField := value.Messages[messageIndex].Fields[right]
			if leftField.Number != rightField.Number {
				return leftField.Number < rightField.Number
			}
			return leftField.GoName < rightField.GoName
		})
	}
	sort.Slice(value.Messages, func(left, right int) bool {
		return value.Messages[left].Name < value.Messages[right].Name
	})
	if err := validateInterface(value, Schema); err != nil {
		return wireInterface{}, err
	}
	shape, err := encodeShape(Schema, value)
	if err != nil {
		return wireInterface{}, err
	}
	value.Digest = digest(shape)
	return value, nil
}

func build(interfaces []wireInterface) (Baseline, error) {
	canonical, err := encodeCanonical(Schema, interfaces)
	if err != nil {
		return Baseline{}, fmt.Errorf("%w: encode canonical record: %v", ErrInvalid, err)
	}
	identityDigest := digest(canonical)
	record, err := encodeRecord(Schema, interfaces, identityDigest)
	if err != nil {
		return Baseline{}, fmt.Errorf("%w: encode record: %v", ErrInvalid, err)
	}
	if int64(len(record)) > MaximumBytes {
		return Baseline{}, fmt.Errorf("%w: encoded record exceeds %d bytes", ErrInvalid, MaximumBytes)
	}
	return buildWithEncoding(Schema, interfaces, canonical, record, identityDigest), nil
}

func buildWithEncoding(schema string, interfaces []wireInterface, canonical, record []byte, identityDigest string) Baseline {
	return Baseline{
		record: wireRecord{
			Schema:     schema,
			Interfaces: cloneWireInterfaces(interfaces),
			Digest:     identityDigest,
		},
		canonicalJSON: append([]byte(nil), canonical...),
		recordJSON:    append([]byte(nil), record...),
		digest:        identityDigest,
		prepared:      true,
	}
}

func encodeCanonical(schema string, interfaces []wireInterface) ([]byte, error) {
	return json.Marshal(canonicalRecord{Schema: schema, Interfaces: interfaces})
}

func encodeRecord(schema string, interfaces []wireInterface, identityDigest string) ([]byte, error) {
	return json.Marshal(wireRecord{Schema: schema, Interfaces: interfaces, Digest: identityDigest})
}

func encodeShape(schema string, value wireInterface) ([]byte, error) {
	shapeSchema, valid := shapeSchemaForBaseline(schema)
	if !valid {
		return nil, fmt.Errorf("unsupported Interface shape schema %q", schema)
	}
	return json.Marshal(shapeDigestRecord{
		Schema:   shapeSchema,
		ID:       value.ID,
		Package:  value.PackagePath,
		Method:   value.Method,
		Request:  value.Request,
		Response: value.Response,
		Messages: value.Messages,
	})
}

func equivalentInterfaceShape(previous wireInterface, previousSchema string, current wireInterface, currentSchema string) bool {
	if previous.Digest == current.Digest && previousSchema == currentSchema {
		return true
	}
	previousShape, err := encodeShape(currentSchema, previous)
	if err != nil {
		return false
	}
	currentShape, err := encodeShape(currentSchema, current)
	return err == nil && bytes.Equal(previousShape, currentShape)
}

func validBaselineSchema(value string) bool {
	return value == baselineSchemaV1 || value == Schema
}

func shapeSchemaForBaseline(value string) (string, bool) {
	switch value {
	case baselineSchemaV1:
		return shapeSchemaV1, true
	case Schema:
		return ShapeSchema, true
	default:
		return "", false
	}
}

func validateInterfaces(values []wireInterface, requireOrdered bool, schema string) error {
	if !validBaselineSchema(schema) {
		return fmt.Errorf("Interface shape schema %q is unsupported", schema)
	}
	if values == nil || len(values) > maximumInterfaces {
		return fmt.Errorf("interfaces must be an array with at most %d entries", maximumInterfaces)
	}
	totalMessages := 0
	totalFields := 0
	for index, value := range values {
		if requireOrdered && index > 0 && values[index-1].ID >= value.ID {
			return errors.New("interfaces must be unique and sorted by exact ID")
		}
		if err := validateInterface(value, schema); err != nil {
			return fmt.Errorf("interfaces[%d]: %v", index, err)
		}
		shape, err := encodeShape(schema, value)
		if err != nil {
			return fmt.Errorf("interfaces[%d]: encode shape: %v", index, err)
		}
		if !validDigest(value.Digest) || value.Digest != digest(shape) {
			return fmt.Errorf("interfaces[%d] digest does not match its canonical Go shape", index)
		}
		totalMessages += len(value.Messages)
		for _, message := range value.Messages {
			totalFields += len(message.Fields)
		}
		if totalMessages > maximumMessages || totalFields > maximumFields {
			return fmt.Errorf("baseline exceeds %d messages or %d fields", maximumMessages, maximumFields)
		}
	}
	return nil
}

func validateInterface(value wireInterface, schema string) error {
	identifier, err := interfaceid.Parse(value.ID)
	if err != nil || identifier.String() != value.ID {
		return fmt.Errorf("ID %q is not canonical", value.ID)
	}
	if module.CheckImportPath(value.PackagePath) != nil {
		return fmt.Errorf("package %q is not a canonical Go import path", value.PackagePath)
	}
	if !validExportedIdentifier(value.Method) ||
		!validExportedIdentifier(value.Request) ||
		!validExportedIdentifier(value.Response) {
		return errors.New("method, request, and response must be exported Go identifiers")
	}
	if len(value.Messages) == 0 || len(value.Messages) > maximumMessages {
		return fmt.Errorf("messages must contain between 1 and %d entries", maximumMessages)
	}
	messageNames := make(map[string]struct{}, len(value.Messages))
	for index, message := range value.Messages {
		if index > 0 && value.Messages[index-1].Name >= message.Name {
			return errors.New("messages must be unique and sorted by Go name")
		}
		if !validExportedIdentifier(message.Name) {
			return fmt.Errorf("message name %q is invalid", message.Name)
		}
		messageNames[message.Name] = struct{}{}
	}
	if _, exists := messageNames[value.Request]; !exists {
		return fmt.Errorf("request message %s is absent", value.Request)
	}
	if _, exists := messageNames[value.Response]; !exists {
		return fmt.Errorf("response message %s is absent", value.Response)
	}
	for _, message := range value.Messages {
		if err := validateMessage(message, messageNames, schema); err != nil {
			return fmt.Errorf("message %s: %v", message.Name, err)
		}
	}
	if err := validateMessageGraph(value); err != nil {
		return err
	}
	return nil
}

type messageReference struct {
	target  string
	direct  bool
	pointer bool
}

func validateMessageGraph(value wireInterface) error {
	messages := make(map[string]wireMessage, len(value.Messages))
	for _, message := range value.Messages {
		messages[message.Name] = message
	}

	reachable := make(map[string]struct{}, len(messages))
	var visitReachable func(string)
	visitReachable = func(name string) {
		if _, seen := reachable[name]; seen {
			return
		}
		reachable[name] = struct{}{}
		for _, reference := range messageReferences(messages[name]) {
			visitReachable(reference.target)
		}
	}
	visitReachable(value.Request)
	visitReachable(value.Response)
	for _, message := range value.Messages {
		if _, exists := reachable[message.Name]; !exists {
			return fmt.Errorf("message %s is unreachable from request %s or response %s", message.Name, value.Request, value.Response)
		}
	}

	if err := validateDirectMessageCycles(value.Messages, messages); err != nil {
		return err
	}
	return validatePointerMessageCycles(value.Messages, messages)
}

func validateDirectMessageCycles(ordered []wireMessage, messages map[string]wireMessage) error {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(messages))
	stack := make([]string, 0, len(messages))
	stackIndex := make(map[string]int, len(messages))
	var visit func(string) error
	visit = func(name string) error {
		state[name] = visiting
		stackIndex[name] = len(stack)
		stack = append(stack, name)
		defer func() {
			delete(stackIndex, name)
			stack = stack[:len(stack)-1]
			state[name] = visited
		}()
		for _, reference := range messageReferences(messages[name]) {
			if !reference.direct || reference.pointer {
				continue
			}
			switch state[reference.target] {
			case unvisited:
				if err := visit(reference.target); err != nil {
					return err
				}
			case visiting:
				cycle := append([]string(nil), stack[stackIndex[reference.target]:]...)
				cycle = append(cycle, reference.target)
				return fmt.Errorf("direct-value message cycle %s is invalid", strings.Join(cycle, " -> "))
			}
		}
		return nil
	}
	for _, message := range ordered {
		if state[message.Name] == unvisited {
			if err := visit(message.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePointerMessageCycles(ordered []wireMessage, messages map[string]wireMessage) error {
	indices := make(map[string]int, len(messages))
	lowLinks := make(map[string]int, len(messages))
	onStack := make(map[string]bool, len(messages))
	stack := make([]string, 0, len(messages))
	components := make(map[string]int, len(messages))
	nextIndex := 0
	nextComponent := 0
	var connect func(string)
	connect = func(name string) {
		indices[name] = nextIndex
		lowLinks[name] = nextIndex
		nextIndex++
		stack = append(stack, name)
		onStack[name] = true
		for _, reference := range messageReferences(messages[name]) {
			if _, seen := indices[reference.target]; !seen {
				connect(reference.target)
				if lowLinks[reference.target] < lowLinks[name] {
					lowLinks[name] = lowLinks[reference.target]
				}
			} else if onStack[reference.target] && indices[reference.target] < lowLinks[name] {
				lowLinks[name] = indices[reference.target]
			}
		}
		if lowLinks[name] != indices[name] {
			return
		}
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			components[member] = nextComponent
			if member == name {
				break
			}
		}
		nextComponent++
	}
	for _, message := range ordered {
		if _, seen := indices[message.Name]; !seen {
			connect(message.Name)
		}
	}
	for _, message := range ordered {
		for _, field := range message.Fields {
			reference, exists := fieldMessageReference(field)
			if !exists || !reference.direct || !reference.pointer {
				continue
			}
			if components[message.Name] == components[reference.target] {
				return fmt.Errorf("pointer-to-message field %s.%s creates a recursive cycle with %s", message.Name, field.GoName, reference.target)
			}
		}
	}
	return nil
}

func messageReferences(message wireMessage) []messageReference {
	result := make([]messageReference, 0, len(message.Fields))
	for _, field := range message.Fields {
		if reference, exists := fieldMessageReference(field); exists {
			result = append(result, reference)
		}
	}
	return result
}

func fieldMessageReference(field wireField) (messageReference, bool) {
	if name, found := strings.CutPrefix(field.Type, "message:"); found {
		return messageReference{target: name, direct: true, pointer: field.PointerDepth != 0}, true
	}
	if element, found := boundedGeneric(field.Type, "repeated<"); found {
		if name, message := strings.CutPrefix(element, "message:"); message {
			return messageReference{target: name}, true
		}
		return messageReference{}, false
	}
	if body, found := boundedGeneric(field.Type, "map<"); found {
		_, mapped, separated := strings.Cut(body, ",")
		if separated {
			if name, message := strings.CutPrefix(mapped, "message:"); message {
				return messageReference{target: name}, true
			}
		}
	}
	return messageReference{}, false
}

func onlyAddsOptionalPointerFields(previous, current wireInterface) bool {
	if previous.ID != current.ID ||
		previous.PackagePath != current.PackagePath ||
		previous.Method != current.Method ||
		previous.Request != current.Request ||
		previous.Response != current.Response {
		return false
	}
	currentMessages := make(map[string]wireMessage, len(current.Messages))
	for _, message := range current.Messages {
		currentMessages[message.Name] = message
	}
	added := false
	for _, previousMessage := range previous.Messages {
		currentMessage, exists := currentMessages[previousMessage.Name]
		if !exists {
			return false
		}
		previousIndex := 0
		currentIndex := 0
		for previousIndex < len(previousMessage.Fields) && currentIndex < len(currentMessage.Fields) {
			previousField := previousMessage.Fields[previousIndex]
			currentField := currentMessage.Fields[currentIndex]
			switch {
			case currentField.Number < previousField.Number:
				if currentField.Required || currentField.PointerDepth == 0 {
					return false
				}
				added = true
				currentIndex++
			case currentField.Number > previousField.Number:
				return false
			default:
				if previousField != currentField {
					return false
				}
				previousIndex++
				currentIndex++
			}
		}
		if previousIndex != len(previousMessage.Fields) {
			return false
		}
		for ; currentIndex < len(currentMessage.Fields); currentIndex++ {
			field := currentMessage.Fields[currentIndex]
			if field.Required || field.PointerDepth == 0 {
				return false
			}
			added = true
		}
	}
	return added
}

func validateMessage(value wireMessage, messageNames map[string]struct{}, schema string) error {
	if value.Fields == nil || len(value.Fields) > maximumFields {
		return fmt.Errorf("fields must be an array with at most %d entries", maximumFields)
	}
	numbers := make(map[uint64]string, len(value.Fields))
	goNames := make(map[string]struct{}, len(value.Fields))
	jsonNames := make(map[string]struct{}, len(value.Fields))
	for index, field := range value.Fields {
		if index > 0 {
			previous := value.Fields[index-1]
			if previous.Number > field.Number || previous.Number == field.Number && previous.GoName >= field.GoName {
				return errors.New("fields must be unique and sorted by number then Go name")
			}
		}
		if field.Number == 0 {
			return fmt.Errorf("field %q number must be positive", field.GoName)
		}
		if owner, duplicate := numbers[field.Number]; duplicate {
			return fmt.Errorf("fields %s and %s duplicate number %d", owner, field.GoName, field.Number)
		}
		numbers[field.Number] = field.GoName
		if !validExportedIdentifier(field.GoName) {
			return fmt.Errorf("field Go name %q is invalid", field.GoName)
		}
		if _, duplicate := goNames[field.GoName]; duplicate {
			return fmt.Errorf("field Go name %q is duplicated", field.GoName)
		}
		goNames[field.GoName] = struct{}{}
		if !validJSONName(field.JSONName) {
			return fmt.Errorf("field %s JSON name %q is invalid", field.GoName, field.JSONName)
		}
		if _, duplicate := jsonNames[field.JSONName]; duplicate {
			return fmt.Errorf("field JSON name %q is duplicated", field.JSONName)
		}
		jsonNames[field.JSONName] = struct{}{}
		if field.PointerDepth > 2 {
			return fmt.Errorf("field %s pointer depth %d exceeds maximum 2", field.GoName, field.PointerDepth)
		}
		if schema == baselineSchemaV1 && field.PointerDepth != 0 {
			return fmt.Errorf("field %s pointer depth is unavailable in shape schema v1", field.GoName)
		}
		if !validCanonicalType(field.Type, messageNames) {
			return fmt.Errorf("field %s canonical type %q is invalid", field.GoName, field.Type)
		}
	}
	return nil
}

func validCanonicalType(value string, messages map[string]struct{}) bool {
	if value == "" || len(value) > maximumTypeBytes || !utf8.ValidString(value) {
		return false
	}
	if scalarType(value) {
		return true
	}
	if name, found := strings.CutPrefix(value, "message:"); found {
		_, exists := messages[name]
		return validExportedIdentifier(name) && exists
	}
	if element, found := boundedGeneric(value, "repeated<"); found {
		return scalarOrMessageType(element, messages)
	}
	if body, found := boundedGeneric(value, "map<"); found {
		key, mapped, ok := strings.Cut(body, ",")
		return ok && mapKeyType(key) && scalarOrMessageType(mapped, messages)
	}
	return false
}

func scalarOrMessageType(value string, messages map[string]struct{}) bool {
	if scalarType(value) {
		return true
	}
	name, found := strings.CutPrefix(value, "message:")
	if !found {
		return false
	}
	_, exists := messages[name]
	return validExportedIdentifier(name) && exists
}

func boundedGeneric(value, prefix string) (string, bool) {
	body, found := strings.CutPrefix(value, prefix)
	if !found || len(body) < 2 || body[len(body)-1] != '>' {
		return "", false
	}
	body = body[:len(body)-1]
	if body == "" || strings.ContainsAny(body, "<>") {
		return "", false
	}
	return body, true
}

func scalarType(value string) bool {
	switch value {
	case "boolean", "string", "int32", "int64", "uint32", "uint64",
		"float32", "float64", "bytes", "timestamp", "duration":
		return true
	default:
		return false
	}
}

func mapKeyType(value string) bool {
	switch value {
	case "boolean", "string", "int32", "int64", "uint32", "uint64":
		return true
	default:
		return false
	}
}

func validExportedIdentifier(value string) bool {
	return value != "" &&
		len(value) <= maximumNameBytes &&
		utf8.ValidString(value) &&
		token.IsIdentifier(value) &&
		ast.IsExported(value)
}

func validJSONName(value string) bool {
	if value == "" || value == "-" || len(value) > maximumNameBytes || !utf8.ValidString(value) {
		return false
	}
	const permittedPunctuation = "!#$%&()*+-./:;<=>?@[]^_{|}~ "
	for _, character := range value {
		if strings.ContainsRune(permittedPunctuation, character) {
			continue
		}
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	encoded, found := strings.CutPrefix(value, "sha256:")
	if !found || len(encoded) != sha256.Size*2 {
		return false
	}
	for _, character := range encoded {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func cloneWireInterfaces(values []wireInterface) []wireInterface {
	result := make([]wireInterface, len(values))
	for index, value := range values {
		result[index] = cloneWireInterface(value)
	}
	return result
}

func cloneWireInterface(value wireInterface) wireInterface {
	result := value
	result.Messages = make([]wireMessage, len(value.Messages))
	for index, message := range value.Messages {
		result.Messages[index] = cloneWireMessage(message)
	}
	return result
}

func cloneWireMessage(value wireMessage) wireMessage {
	result := value
	result.Fields = make([]wireField, len(value.Fields))
	copy(result.Fields, value.Fields)
	return result
}
