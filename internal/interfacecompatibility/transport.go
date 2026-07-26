package interfacecompatibility

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufwiremap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

const (
	// TransportPath is the committed Interface transport-projection baseline.
	TransportPath = "generated/compatibility/interface-transport.json"
	// TransportSchema identifies the strict transport-projection baseline.
	TransportSchema = "plystra.interface-transport-baseline/v1"
	// TransportMaximumBytes bounds a managed transport baseline before parsing.
	TransportMaximumBytes int64 = 4 << 20

	descriptorProjectionSchema = "plystra.interface.protobuf-descriptor/v1"
	procedureProjectionSchema  = "plystra.interface.connect-procedure/v1"
	wireProjectionSchema       = "plystra.interface.proto-wire-map/v1"
)

// TransportBaseline is one immutable exact snapshot of the generated
// Protobuf descriptor, Connect procedure, and active wire-map projections for
// every exposed Interface. It contains no Implementation, configuration,
// Secret, source-path, or module-version data.
type TransportBaseline struct {
	record        transportWireRecord
	canonicalJSON []byte
	recordJSON    []byte
	digest        string
	prepared      bool
}

// Schema returns the exact transport-baseline schema.
func (b TransportBaseline) Schema() string {
	if !b.prepared {
		return ""
	}
	return TransportSchema
}

// Interfaces returns exact-ID-sorted defensive Interface projection views.
func (b TransportBaseline) Interfaces() []TransportInterface {
	result := make([]TransportInterface, len(b.record.Interfaces))
	for index, value := range b.record.Interfaces {
		result[index] = TransportInterface{record: value}
	}
	return result
}

// CanonicalJSON returns the defensive digest input without the top-level
// digest.
func (b TransportBaseline) CanonicalJSON() []byte {
	return append([]byte(nil), b.canonicalJSON...)
}

// RecordJSON returns the defensive strict generated transport baseline.
func (b TransportBaseline) RecordJSON() []byte {
	return append([]byte(nil), b.recordJSON...)
}

// Digest returns the lowercase SHA-256 digest of CanonicalJSON.
func (b TransportBaseline) Digest() string { return b.digest }

// Valid reports whether this value is complete and internally canonical.
func (b TransportBaseline) Valid() bool {
	if !b.prepared || b.record.Schema != TransportSchema || b.record.Digest != b.digest {
		return false
	}
	if err := validateTransportInterfaces(b.record.Interfaces, true); err != nil {
		return false
	}
	canonical, err := encodeTransportCanonical(b.record.Interfaces)
	if err != nil || !bytes.Equal(canonical, b.canonicalJSON) || digest(canonical) != b.digest {
		return false
	}
	record, err := encodeTransportRecord(b.record.Interfaces, b.digest)
	return err == nil && bytes.Equal(record, b.recordJSON)
}

// TransportInterface is one immutable set of classified generated transport
// projection digests.
type TransportInterface struct {
	record transportWireInterface
}

// ID returns the exact canonical Interface ID.
func (i TransportInterface) ID() string { return i.record.ID }

// DescriptorDigest returns the exact Protobuf descriptor projection digest.
func (i TransportInterface) DescriptorDigest() string {
	return i.record.DescriptorDigest
}

// ProcedureDigest returns the exact Connect procedure projection digest.
func (i TransportInterface) ProcedureDigest() string {
	return i.record.ProcedureDigest
}

// WireMapDigest returns the exact active wire-map projection digest.
func (i TransportInterface) WireMapDigest() string { return i.record.WireMapDigest }

// BuildTransport constructs the current transport-projection baseline from the
// same validated wire map and descriptor evidence consumed by the built-in
// Connect and JavaScript generators.
func BuildTransport(wireMap protobufwiremap.Map, evidence protobufdescriptor.Evidence) (TransportBaseline, error) {
	if !wireMap.Valid() {
		return TransportBaseline{}, fmt.Errorf("%w: Protobuf wire map is absent or invalid", ErrInvalid)
	}
	if !evidence.Valid() {
		return TransportBaseline{}, fmt.Errorf("%w: Protobuf descriptor evidence is absent or invalid", ErrInvalid)
	}
	var descriptorSet descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(evidence.DescriptorSet(), &descriptorSet); err != nil {
		return TransportBaseline{}, fmt.Errorf("%w: decode Protobuf descriptor evidence: %v", ErrInvalid, err)
	}
	sharedDescriptor := descriptorFileByName(&descriptorSet, protobufdescriptor.ErrorDetailFileName)
	projections := wireMap.ActiveInterfaces()
	if len(projections) != 0 && sharedDescriptor == nil {
		return TransportBaseline{}, fmt.Errorf("%w: shared safe-error descriptor is absent", ErrInvalid)
	}
	sharedBytes, err := marshalDescriptor(sharedDescriptor)
	if err != nil {
		return TransportBaseline{}, fmt.Errorf("%w: encode shared safe-error descriptor: %v", ErrInvalid, err)
	}

	interfaces := make([]transportWireInterface, len(projections))
	usedDescriptors := make(map[string]string, len(projections))
	for index, projection := range projections {
		descriptor, err := findInterfaceDescriptor(&descriptorSet, projection)
		if err != nil {
			return TransportBaseline{}, fmt.Errorf("%w: Interface %s: %v", ErrInvalid, projection.ID(), err)
		}
		if previous, duplicate := usedDescriptors[descriptor.GetName()]; duplicate {
			return TransportBaseline{}, fmt.Errorf(
				"%w: Interfaces %s and %s share descriptor %s",
				ErrInvalid,
				previous,
				projection.ID(),
				descriptor.GetName(),
			)
		}
		usedDescriptors[descriptor.GetName()] = projection.ID()
		descriptorBytes, err := marshalDescriptor(descriptor)
		if err != nil {
			return TransportBaseline{}, fmt.Errorf("%w: Interface %s descriptor: %v", ErrInvalid, projection.ID(), err)
		}
		procedureBytes, err := encodeProcedureProjection(projection)
		if err != nil {
			return TransportBaseline{}, fmt.Errorf("%w: Interface %s procedure: %v", ErrInvalid, projection.ID(), err)
		}
		wireBytes, err := encodeWireProjection(projection)
		if err != nil {
			return TransportBaseline{}, fmt.Errorf("%w: Interface %s wire map: %v", ErrInvalid, projection.ID(), err)
		}
		interfaces[index] = transportWireInterface{
			ID: projection.ID(),
			DescriptorDigest: projectionDigest(
				descriptorProjectionSchema,
				[]byte(projection.ID()),
				descriptorBytes,
				sharedBytes,
			),
			ProcedureDigest: digest(procedureBytes),
			WireMapDigest:   digest(wireBytes),
		}
	}
	sort.Slice(interfaces, func(left, right int) bool {
		return interfaces[left].ID < interfaces[right].ID
	})
	if err := validateTransportInterfaces(interfaces, true); err != nil {
		return TransportBaseline{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return buildTransport(interfaces)
}

// DecodeTransport strictly restores one canonical generated transport
// baseline.
func DecodeTransport(data []byte) (TransportBaseline, error) {
	if len(data) == 0 || int64(len(data)) > TransportMaximumBytes {
		return TransportBaseline{}, fmt.Errorf("%w: transport record must contain between 1 and %d bytes", ErrHistory, TransportMaximumBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record transportWireRecord
	if err := decoder.Decode(&record); err != nil {
		return TransportBaseline{}, fmt.Errorf("%w: decode transport record: %v", ErrHistory, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return TransportBaseline{}, fmt.Errorf("%w: transport record contains trailing JSON", ErrHistory)
	}
	if record.Schema != TransportSchema {
		return TransportBaseline{}, fmt.Errorf("%w: transport schema must equal %q", ErrHistory, TransportSchema)
	}
	if err := validateTransportInterfaces(record.Interfaces, true); err != nil {
		return TransportBaseline{}, fmt.Errorf("%w: %v", ErrHistory, err)
	}
	canonical, err := encodeTransportCanonical(record.Interfaces)
	if err != nil {
		return TransportBaseline{}, fmt.Errorf("%w: encode canonical transport record: %v", ErrHistory, err)
	}
	if !validDigest(record.Digest) || record.Digest != digest(canonical) {
		return TransportBaseline{}, fmt.Errorf("%w: transport digest does not match the canonical projections", ErrHistory)
	}
	encoded, err := encodeTransportRecord(record.Interfaces, record.Digest)
	if err != nil {
		return TransportBaseline{}, fmt.Errorf("%w: encode transport record: %v", ErrHistory, err)
	}
	if !bytes.Equal(encoded, data) {
		return TransportBaseline{}, fmt.Errorf("%w: transport record is not in canonical byte form", ErrHistory)
	}
	return buildTransportWithEncoding(record.Interfaces, canonical, encoded, record.Digest), nil
}

// ReconcileTransport constructs the current transport baseline and compares it
// with exact prior owned evidence. A missing prior record is the valid initial
// state.
func ReconcileTransport(
	wireMap protobufwiremap.Map,
	evidence protobufdescriptor.Evidence,
	previous []byte,
	previousExists bool,
) (TransportBaseline, TransportComparison, error) {
	current, err := BuildTransport(wireMap, evidence)
	if err != nil {
		return TransportBaseline{}, TransportComparison{}, err
	}
	prior, err := buildTransport(nil)
	if err != nil {
		return TransportBaseline{}, TransportComparison{}, err
	}
	if previousExists {
		prior, err = DecodeTransport(previous)
		if err != nil {
			return TransportBaseline{}, TransportComparison{}, err
		}
	} else if len(previous) != 0 {
		return TransportBaseline{}, TransportComparison{}, fmt.Errorf("%w: absent prior transport record has bytes", ErrHistory)
	}
	comparison, err := CompareTransport(prior, current)
	if err != nil {
		return TransportBaseline{}, TransportComparison{}, err
	}
	return current, comparison, nil
}

// TransportClass identifies one generated transport compatibility class.
type TransportClass string

const (
	// TransportClassDescriptor covers the exact Interface Protobuf file and
	// shared safe-error descriptor.
	TransportClassDescriptor TransportClass = "protobuf_descriptor"
	// TransportClassProcedure covers the stable unary Connect procedure.
	TransportClassProcedure TransportClass = "connect_procedure"
	// TransportClassWireMap covers exact active Interface wire-map history.
	TransportClassWireMap TransportClass = "wire_map"
)

// TransportChange is one immutable exact-ID transport-baseline difference.
type TransportChange struct {
	kind     ChangeKind
	id       string
	classes  []TransportClass
	previous transportWireInterface
	current  transportWireInterface
}

// Kind returns added, removed, or changed.
func (c TransportChange) Kind() ChangeKind { return c.kind }

// ID returns the exact canonical Interface ID.
func (c TransportChange) ID() string { return c.id }

// Classes returns the changed transport classes in canonical order.
func (c TransportChange) Classes() []TransportClass {
	return append([]TransportClass(nil), c.classes...)
}

// PreviousDigest returns the prior digest for one class when the Interface
// existed in the previous baseline.
func (c TransportChange) PreviousDigest(class TransportClass) (string, bool) {
	return transportClassDigest(c.previous, class)
}

// CurrentDigest returns the current digest for one class when the Interface
// exists in the current baseline.
func (c TransportChange) CurrentDigest(class TransportClass) (string, bool) {
	return transportClassDigest(c.current, class)
}

// TransportComparison is one immutable exact-ID-sorted transport comparison.
type TransportComparison struct {
	previousDigest string
	currentDigest  string
	changes        []TransportChange
	prepared       bool
}

// Clean reports whether every Interface transport projection is unchanged.
func (c TransportComparison) Clean() bool {
	return c.prepared && len(c.changes) == 0
}

// PreviousDigest returns the compared prior transport-baseline digest.
func (c TransportComparison) PreviousDigest() string { return c.previousDigest }

// CurrentDigest returns the compared current transport-baseline digest.
func (c TransportComparison) CurrentDigest() string { return c.currentDigest }

// Changes returns defensive differences sorted by exact Interface ID.
func (c TransportComparison) Changes() []TransportChange {
	result := make([]TransportChange, len(c.changes))
	for index, change := range c.changes {
		result[index] = change
		result[index].classes = append([]TransportClass(nil), change.classes...)
	}
	return result
}

// Valid reports whether the comparison has complete baseline identities and
// exact transport-class differences.
func (c TransportComparison) Valid() bool {
	if !c.prepared || !validDigest(c.previousDigest) || !validDigest(c.currentDigest) {
		return false
	}
	for index, change := range c.changes {
		if index > 0 && c.changes[index-1].id >= change.id {
			return false
		}
		if !validTransportChange(change) {
			return false
		}
	}
	return true
}

// CompareTransport returns exact added, removed, and transport-class changes.
func CompareTransport(previous, current TransportBaseline) (TransportComparison, error) {
	if !previous.Valid() || !current.Valid() {
		return TransportComparison{}, fmt.Errorf("%w: both compared transport baselines must be valid", ErrInvalid)
	}
	before := make(map[string]transportWireInterface, len(previous.record.Interfaces))
	after := make(map[string]transportWireInterface, len(current.record.Interfaces))
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

	changes := make([]TransportChange, 0)
	for _, identifier := range ordered {
		previousValue, previousExists := before[identifier]
		currentValue, currentExists := after[identifier]
		switch {
		case !previousExists:
			changes = append(changes, TransportChange{
				kind:    ChangeAdded,
				id:      identifier,
				classes: allTransportClasses(),
				current: currentValue,
			})
		case !currentExists:
			changes = append(changes, TransportChange{
				kind:     ChangeRemoved,
				id:       identifier,
				classes:  allTransportClasses(),
				previous: previousValue,
			})
		default:
			classes := changedTransportClasses(previousValue, currentValue)
			if len(classes) != 0 {
				changes = append(changes, TransportChange{
					kind:     ChangeChanged,
					id:       identifier,
					classes:  classes,
					previous: previousValue,
					current:  currentValue,
				})
			}
		}
	}
	result := TransportComparison{
		previousDigest: previous.digest,
		currentDigest:  current.digest,
		changes:        changes,
		prepared:       true,
	}
	if !result.Valid() {
		return TransportComparison{}, fmt.Errorf("%w: constructed transport comparison is invalid", ErrInvalid)
	}
	return result, nil
}

type transportWireRecord struct {
	Schema     string                   `json:"schema"`
	Interfaces []transportWireInterface `json:"interfaces"`
	Digest     string                   `json:"digest"`
}

type transportCanonicalRecord struct {
	Schema     string                   `json:"schema"`
	Interfaces []transportWireInterface `json:"interfaces"`
}

type transportWireInterface struct {
	ID               string `json:"id"`
	DescriptorDigest string `json:"protobuf_descriptor_digest"`
	ProcedureDigest  string `json:"connect_procedure_digest"`
	WireMapDigest    string `json:"wire_map_digest"`
}

type procedureProjection struct {
	Schema          string `json:"schema"`
	ID              string `json:"id"`
	ProtobufPackage string `json:"protobuf_package"`
	Service         string `json:"service"`
	Method          string `json:"method"`
	Procedure       string `json:"procedure"`
	RequestMessage  string `json:"request_message"`
	ResponseMessage string `json:"response_message"`
}

type interfaceWireProjection struct {
	Schema          string                 `json:"schema"`
	ID              string                 `json:"id"`
	ContractDigest  string                 `json:"contract_digest"`
	ProtobufPackage string                 `json:"protobuf_package"`
	Service         string                 `json:"service"`
	Method          string                 `json:"method"`
	Procedure       string                 `json:"procedure"`
	RequestMessage  string                 `json:"request_message"`
	ResponseMessage string                 `json:"response_message"`
	Messages        []interfaceWireMessage `json:"messages"`
}

type interfaceWireMessage struct {
	GoName          string               `json:"go_name"`
	ProtobufName    string               `json:"protobuf_name"`
	Fields          []interfaceWireField `json:"fields"`
	ReservedNumbers []int                `json:"reserved_numbers"`
	ReservedNames   []string             `json:"reserved_names"`
}

type interfaceWireField struct {
	GoName       string `json:"go_name"`
	ProtobufName string `json:"protobuf_name"`
	Number       int    `json:"number"`
}

func buildTransport(interfaces []transportWireInterface) (TransportBaseline, error) {
	if interfaces == nil {
		interfaces = []transportWireInterface{}
	}
	canonical, err := encodeTransportCanonical(interfaces)
	if err != nil {
		return TransportBaseline{}, fmt.Errorf("%w: encode canonical transport record: %v", ErrInvalid, err)
	}
	identityDigest := digest(canonical)
	record, err := encodeTransportRecord(interfaces, identityDigest)
	if err != nil {
		return TransportBaseline{}, fmt.Errorf("%w: encode transport record: %v", ErrInvalid, err)
	}
	if int64(len(record)) > TransportMaximumBytes {
		return TransportBaseline{}, fmt.Errorf("%w: encoded transport record exceeds %d bytes", ErrInvalid, TransportMaximumBytes)
	}
	return buildTransportWithEncoding(interfaces, canonical, record, identityDigest), nil
}

func buildTransportWithEncoding(interfaces []transportWireInterface, canonical, record []byte, identityDigest string) TransportBaseline {
	clonedInterfaces := append([]transportWireInterface(nil), interfaces...)
	if clonedInterfaces == nil {
		clonedInterfaces = []transportWireInterface{}
	}
	return TransportBaseline{
		record: transportWireRecord{
			Schema:     TransportSchema,
			Interfaces: clonedInterfaces,
			Digest:     identityDigest,
		},
		canonicalJSON: append([]byte(nil), canonical...),
		recordJSON:    append([]byte(nil), record...),
		digest:        identityDigest,
		prepared:      true,
	}
}

func encodeTransportCanonical(interfaces []transportWireInterface) ([]byte, error) {
	return json.Marshal(transportCanonicalRecord{
		Schema:     TransportSchema,
		Interfaces: interfaces,
	})
}

func encodeTransportRecord(interfaces []transportWireInterface, identityDigest string) ([]byte, error) {
	return json.Marshal(transportWireRecord{
		Schema:     TransportSchema,
		Interfaces: interfaces,
		Digest:     identityDigest,
	})
}

func validateTransportInterfaces(values []transportWireInterface, requireOrdered bool) error {
	if values == nil || len(values) > maximumInterfaces {
		return fmt.Errorf("transport interfaces must be an array with at most %d entries", maximumInterfaces)
	}
	for index, value := range values {
		if requireOrdered && index > 0 && values[index-1].ID >= value.ID {
			return errors.New("transport interfaces must be unique and sorted by exact ID")
		}
		if err := validateTransportInterface(value); err != nil {
			return fmt.Errorf("transport interfaces[%d]: %v", index, err)
		}
	}
	return nil
}

func validateTransportInterface(value transportWireInterface) error {
	identifier, err := interfaceid.Parse(value.ID)
	if err != nil || identifier.String() != value.ID {
		return fmt.Errorf("ID %q is not canonical", value.ID)
	}
	if !validDigest(value.DescriptorDigest) {
		return errors.New("protobuf descriptor digest is invalid")
	}
	if !validDigest(value.ProcedureDigest) {
		return errors.New("connect procedure digest is invalid")
	}
	if !validDigest(value.WireMapDigest) {
		return errors.New("wire-map digest is invalid")
	}
	return nil
}

func allTransportClasses() []TransportClass {
	return []TransportClass{
		TransportClassDescriptor,
		TransportClassProcedure,
		TransportClassWireMap,
	}
}

func changedTransportClasses(previous, current transportWireInterface) []TransportClass {
	result := make([]TransportClass, 0, 3)
	for _, class := range allTransportClasses() {
		previousDigest, _ := transportClassDigest(previous, class)
		currentDigest, _ := transportClassDigest(current, class)
		if previousDigest != currentDigest {
			result = append(result, class)
		}
	}
	return result
}

func transportClassDigest(value transportWireInterface, class TransportClass) (string, bool) {
	if value.ID == "" {
		return "", false
	}
	switch class {
	case TransportClassDescriptor:
		return value.DescriptorDigest, true
	case TransportClassProcedure:
		return value.ProcedureDigest, true
	case TransportClassWireMap:
		return value.WireMapDigest, true
	default:
		return "", false
	}
}

func validTransportChange(change TransportChange) bool {
	identifier, err := interfaceid.Parse(change.id)
	if err != nil || identifier.String() != change.id {
		return false
	}
	if !equalTransportClasses(change.classes, expectedTransportClasses(change)) {
		return false
	}
	switch change.kind {
	case ChangeAdded:
		return change.previous.ID == "" &&
			change.current.ID == change.id &&
			validateTransportInterface(change.current) == nil
	case ChangeRemoved:
		return change.previous.ID == change.id &&
			validateTransportInterface(change.previous) == nil &&
			change.current.ID == ""
	case ChangeChanged:
		return change.previous.ID == change.id &&
			change.current.ID == change.id &&
			validateTransportInterface(change.previous) == nil &&
			validateTransportInterface(change.current) == nil &&
			len(change.classes) != 0
	default:
		return false
	}
}

func expectedTransportClasses(change TransportChange) []TransportClass {
	switch change.kind {
	case ChangeAdded, ChangeRemoved:
		return allTransportClasses()
	case ChangeChanged:
		return changedTransportClasses(change.previous, change.current)
	default:
		return nil
	}
}

func equalTransportClasses(left, right []TransportClass) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func descriptorFileByName(set *descriptorpb.FileDescriptorSet, name string) *descriptorpb.FileDescriptorProto {
	if set == nil {
		return nil
	}
	for _, file := range set.File {
		if file.GetName() == name {
			return file
		}
	}
	return nil
}

func findInterfaceDescriptor(
	set *descriptorpb.FileDescriptorSet,
	projection protobufwiremap.InterfaceProjection,
) (*descriptorpb.FileDescriptorProto, error) {
	var matched *descriptorpb.FileDescriptorProto
	for _, file := range set.File {
		if file.GetPackage() != projection.ProtobufPackage() {
			continue
		}
		for _, service := range file.Service {
			if service.GetName() != projection.Service() {
				continue
			}
			if matched != nil {
				return nil, fmt.Errorf("more than one descriptor owns service %s", projection.Service())
			}
			if len(service.Method) != 1 {
				return nil, fmt.Errorf("service %s must contain exactly one method", projection.Service())
			}
			method := service.Method[0]
			request := "." + projection.ProtobufPackage() + "." + projection.RequestMessage()
			response := "." + projection.ProtobufPackage() + "." + projection.ResponseMessage()
			if method.GetName() != projection.Method() ||
				method.GetInputType() != request ||
				method.GetOutputType() != response ||
				method.GetClientStreaming() ||
				method.GetServerStreaming() {
				return nil, fmt.Errorf("service %s does not match the active unary procedure", projection.Service())
			}
			matched = file
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("descriptor for service %s is absent", projection.Service())
	}
	return matched, nil
}

func marshalDescriptor(value *descriptorpb.FileDescriptorProto) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return (proto.MarshalOptions{Deterministic: true}).Marshal(value)
}

func encodeProcedureProjection(projection protobufwiremap.InterfaceProjection) ([]byte, error) {
	return json.Marshal(procedureProjection{
		Schema:          procedureProjectionSchema,
		ID:              projection.ID(),
		ProtobufPackage: projection.ProtobufPackage(),
		Service:         projection.Service(),
		Method:          projection.Method(),
		Procedure:       projection.Procedure(),
		RequestMessage:  projection.RequestMessage(),
		ResponseMessage: projection.ResponseMessage(),
	})
}

func encodeWireProjection(projection protobufwiremap.InterfaceProjection) ([]byte, error) {
	projectedMessages := projection.Messages()
	messages := make([]interfaceWireMessage, len(projectedMessages))
	for index, message := range projectedMessages {
		projectedFields := message.Fields()
		fields := make([]interfaceWireField, len(projectedFields))
		for fieldIndex, field := range projectedFields {
			fields[fieldIndex] = interfaceWireField{
				GoName:       field.CanonicalName(),
				ProtobufName: field.Name(),
				Number:       field.Number(),
			}
		}
		sort.Slice(fields, func(left, right int) bool {
			if fields[left].ProtobufName != fields[right].ProtobufName {
				return fields[left].ProtobufName < fields[right].ProtobufName
			}
			return fields[left].GoName < fields[right].GoName
		})
		messages[index] = interfaceWireMessage{
			GoName:          message.CanonicalName(),
			ProtobufName:    message.Name(),
			Fields:          fields,
			ReservedNumbers: message.ReservedNumbers(),
			ReservedNames:   message.ReservedNames(),
		}
	}
	sort.Slice(messages, func(left, right int) bool {
		return messages[left].ProtobufName < messages[right].ProtobufName
	})
	return json.Marshal(interfaceWireProjection{
		Schema:          wireProjectionSchema,
		ID:              projection.ID(),
		ContractDigest:  projection.ContractDigest(),
		ProtobufPackage: projection.ProtobufPackage(),
		Service:         projection.Service(),
		Method:          projection.Method(),
		Procedure:       projection.Procedure(),
		RequestMessage:  projection.RequestMessage(),
		ResponseMessage: projection.ResponseMessage(),
		Messages:        messages,
	})
}

func projectionDigest(schema string, parts ...[]byte) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(schema))
	_, _ = hash.Write([]byte{0})
	var size [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(part)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
