package interfacecompatibility

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/plystra/cli/internal/interfaceid"
)

const (
	// MetadataPath is the one CLI-owned Interface metadata-class baseline.
	MetadataPath = "generated/compatibility/interface-metadata.json"
	// MetadataSchema identifies the strict Interface metadata-class baseline.
	MetadataSchema = "plystra.interface-metadata-baseline/v1"
	// MetadataMaximumBytes bounds a managed metadata baseline before parsing.
	MetadataMaximumBytes int64 = 4 << 20
)

// MetadataInput supplies the already validated digest classes for one visible
// authored Interface.
type MetadataInput struct {
	ID                  string
	ContractDigest      string
	DocumentationDigest string
	ExampleDigest       string
}

// MetadataBaseline is one immutable exact snapshot of the contract,
// documentation, and example digest classes for every visible authored
// Interface. It contains no metadata values or source provenance.
type MetadataBaseline struct {
	record        metadataWireRecord
	canonicalJSON []byte
	recordJSON    []byte
	digest        string
	prepared      bool
}

// Schema returns the exact metadata-baseline schema.
func (b MetadataBaseline) Schema() string {
	if !b.prepared {
		return ""
	}
	return MetadataSchema
}

// Interfaces returns exact-ID-sorted defensive Interface digest views.
func (b MetadataBaseline) Interfaces() []MetadataInterface {
	result := make([]MetadataInterface, len(b.record.Interfaces))
	for index, value := range b.record.Interfaces {
		result[index] = MetadataInterface{record: value}
	}
	return result
}

// CanonicalJSON returns the defensive digest input without the top-level
// digest.
func (b MetadataBaseline) CanonicalJSON() []byte {
	return append([]byte(nil), b.canonicalJSON...)
}

// RecordJSON returns the defensive strict generated metadata baseline.
func (b MetadataBaseline) RecordJSON() []byte {
	return append([]byte(nil), b.recordJSON...)
}

// Digest returns the lowercase SHA-256 digest of CanonicalJSON.
func (b MetadataBaseline) Digest() string { return b.digest }

// Valid reports whether this value is complete and internally canonical.
func (b MetadataBaseline) Valid() bool {
	if !b.prepared || b.record.Schema != MetadataSchema || b.record.Digest != b.digest {
		return false
	}
	if err := validateMetadataInterfaces(b.record.Interfaces, true); err != nil {
		return false
	}
	canonical, err := encodeMetadataCanonical(b.record.Interfaces)
	if err != nil || !bytes.Equal(canonical, b.canonicalJSON) || digest(canonical) != b.digest {
		return false
	}
	record, err := encodeMetadataRecord(b.record.Interfaces, b.digest)
	return err == nil && bytes.Equal(record, b.recordJSON)
}

// MetadataInterface is one immutable set of classified Interface digests.
type MetadataInterface struct {
	record metadataWireInterface
}

// ID returns the exact canonical Interface ID.
func (i MetadataInterface) ID() string { return i.record.ID }

// ContractDigest returns the exact contract compatibility-class digest.
func (i MetadataInterface) ContractDigest() string { return i.record.ContractDigest }

// DocumentationDigest returns the documentation compatibility-class digest.
func (i MetadataInterface) DocumentationDigest() string {
	return i.record.DocumentationDigest
}

// ExampleDigest returns the example compatibility-class digest.
func (i MetadataInterface) ExampleDigest() string { return i.record.ExampleDigest }

// NewMetadata constructs the current metadata-class baseline independently of
// discovery order.
func NewMetadata(inputs []MetadataInput) (MetadataBaseline, error) {
	if len(inputs) > maximumInterfaces {
		return MetadataBaseline{}, fmt.Errorf("%w: %d metadata inputs exceeds maximum %d", ErrInvalid, len(inputs), maximumInterfaces)
	}
	interfaces := make([]metadataWireInterface, len(inputs))
	for index, input := range inputs {
		interfaces[index] = metadataWireInterface(input)
	}
	sort.Slice(interfaces, func(left, right int) bool {
		return interfaces[left].ID < interfaces[right].ID
	})
	if err := validateMetadataInterfaces(interfaces, true); err != nil {
		return MetadataBaseline{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return buildMetadata(interfaces)
}

// DecodeMetadata strictly restores one canonical generated metadata baseline.
func DecodeMetadata(data []byte) (MetadataBaseline, error) {
	if len(data) == 0 || int64(len(data)) > MetadataMaximumBytes {
		return MetadataBaseline{}, fmt.Errorf("%w: metadata record must contain between 1 and %d bytes", ErrHistory, MetadataMaximumBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record metadataWireRecord
	if err := decoder.Decode(&record); err != nil {
		return MetadataBaseline{}, fmt.Errorf("%w: decode metadata record: %v", ErrHistory, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return MetadataBaseline{}, fmt.Errorf("%w: metadata record contains trailing JSON", ErrHistory)
	}
	if record.Schema != MetadataSchema {
		return MetadataBaseline{}, fmt.Errorf("%w: metadata schema must equal %q", ErrHistory, MetadataSchema)
	}
	if err := validateMetadataInterfaces(record.Interfaces, true); err != nil {
		return MetadataBaseline{}, fmt.Errorf("%w: %v", ErrHistory, err)
	}
	canonical, err := encodeMetadataCanonical(record.Interfaces)
	if err != nil {
		return MetadataBaseline{}, fmt.Errorf("%w: encode canonical metadata record: %v", ErrHistory, err)
	}
	if !validDigest(record.Digest) || record.Digest != digest(canonical) {
		return MetadataBaseline{}, fmt.Errorf("%w: metadata digest does not match the canonical digest classes", ErrHistory)
	}
	encoded, err := encodeMetadataRecord(record.Interfaces, record.Digest)
	if err != nil {
		return MetadataBaseline{}, fmt.Errorf("%w: encode metadata record: %v", ErrHistory, err)
	}
	if !bytes.Equal(encoded, data) {
		return MetadataBaseline{}, fmt.Errorf("%w: metadata record is not in canonical byte form", ErrHistory)
	}
	return buildMetadataWithEncoding(record.Interfaces, canonical, encoded, record.Digest), nil
}

// ReconcileMetadata constructs the current metadata baseline and compares it
// with exact prior owned evidence. A missing prior record is the valid initial
// state.
func ReconcileMetadata(inputs []MetadataInput, previous []byte, previousExists bool) (MetadataBaseline, MetadataComparison, error) {
	current, err := NewMetadata(inputs)
	if err != nil {
		return MetadataBaseline{}, MetadataComparison{}, err
	}
	prior, err := NewMetadata(nil)
	if err != nil {
		return MetadataBaseline{}, MetadataComparison{}, err
	}
	if previousExists {
		prior, err = DecodeMetadata(previous)
		if err != nil {
			return MetadataBaseline{}, MetadataComparison{}, err
		}
	} else if len(previous) != 0 {
		return MetadataBaseline{}, MetadataComparison{}, fmt.Errorf("%w: absent prior metadata record has bytes", ErrHistory)
	}
	comparison, err := CompareMetadata(prior, current)
	if err != nil {
		return MetadataBaseline{}, MetadataComparison{}, err
	}
	return current, comparison, nil
}

// MetadataClass identifies one declared Interface compatibility class.
type MetadataClass string

const (
	// MetadataClassContract covers Go shape, semantics, semantic-error codes,
	// constraints, and Behavioral Conformance declarations.
	MetadataClassContract MetadataClass = "contract"
	// MetadataClassDocumentation covers descriptions and deprecation.
	MetadataClassDocumentation MetadataClass = "documentation"
	// MetadataClassExamples covers validated request-and-outcome examples.
	MetadataClassExamples MetadataClass = "examples"
)

// MetadataChange is one immutable exact-ID metadata-baseline difference.
type MetadataChange struct {
	kind     ChangeKind
	id       string
	classes  []MetadataClass
	previous metadataWireInterface
	current  metadataWireInterface
}

// Kind returns added, removed, or changed.
func (c MetadataChange) Kind() ChangeKind { return c.kind }

// ID returns the exact canonical Interface ID.
func (c MetadataChange) ID() string { return c.id }

// Classes returns the changed compatibility classes in canonical order.
func (c MetadataChange) Classes() []MetadataClass {
	return append([]MetadataClass(nil), c.classes...)
}

// PreviousDigest returns the prior digest for one class when the Interface
// existed in the previous baseline.
func (c MetadataChange) PreviousDigest(class MetadataClass) (string, bool) {
	return metadataClassDigest(c.previous, class)
}

// CurrentDigest returns the current digest for one class when the Interface
// exists in the current baseline.
func (c MetadataChange) CurrentDigest(class MetadataClass) (string, bool) {
	return metadataClassDigest(c.current, class)
}

// MetadataComparison is one immutable exact-ID-sorted metadata comparison.
type MetadataComparison struct {
	previousDigest string
	currentDigest  string
	changes        []MetadataChange
	prepared       bool
}

// Clean reports whether every Interface compatibility-class digest is
// unchanged.
func (c MetadataComparison) Clean() bool {
	return c.prepared && len(c.changes) == 0
}

// PreviousDigest returns the compared prior metadata-baseline digest.
func (c MetadataComparison) PreviousDigest() string { return c.previousDigest }

// CurrentDigest returns the compared current metadata-baseline digest.
func (c MetadataComparison) CurrentDigest() string { return c.currentDigest }

// Changes returns defensive differences sorted by exact Interface ID.
func (c MetadataComparison) Changes() []MetadataChange {
	result := make([]MetadataChange, len(c.changes))
	for index, change := range c.changes {
		result[index] = change
		result[index].classes = append([]MetadataClass(nil), change.classes...)
	}
	return result
}

// Valid reports whether the comparison has complete baseline identities and
// exact compatibility-class differences.
func (c MetadataComparison) Valid() bool {
	if !c.prepared || !validDigest(c.previousDigest) || !validDigest(c.currentDigest) {
		return false
	}
	for index, change := range c.changes {
		if index > 0 && c.changes[index-1].id >= change.id {
			return false
		}
		if !validMetadataChange(change) {
			return false
		}
	}
	return true
}

// CompareMetadata returns exact added, removed, and compatibility-class
// changes.
func CompareMetadata(previous, current MetadataBaseline) (MetadataComparison, error) {
	if !previous.Valid() || !current.Valid() {
		return MetadataComparison{}, fmt.Errorf("%w: both compared metadata baselines must be valid", ErrInvalid)
	}
	before := make(map[string]metadataWireInterface, len(previous.record.Interfaces))
	after := make(map[string]metadataWireInterface, len(current.record.Interfaces))
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

	changes := make([]MetadataChange, 0)
	for _, identifier := range ordered {
		previousValue, previousExists := before[identifier]
		currentValue, currentExists := after[identifier]
		switch {
		case !previousExists:
			changes = append(changes, MetadataChange{
				kind:    ChangeAdded,
				id:      identifier,
				classes: allMetadataClasses(),
				current: currentValue,
			})
		case !currentExists:
			changes = append(changes, MetadataChange{
				kind:     ChangeRemoved,
				id:       identifier,
				classes:  allMetadataClasses(),
				previous: previousValue,
			})
		default:
			classes := changedMetadataClasses(previousValue, currentValue)
			if len(classes) != 0 {
				changes = append(changes, MetadataChange{
					kind:     ChangeChanged,
					id:       identifier,
					classes:  classes,
					previous: previousValue,
					current:  currentValue,
				})
			}
		}
	}
	result := MetadataComparison{
		previousDigest: previous.digest,
		currentDigest:  current.digest,
		changes:        changes,
		prepared:       true,
	}
	if !result.Valid() {
		return MetadataComparison{}, fmt.Errorf("%w: constructed metadata comparison is invalid", ErrInvalid)
	}
	return result, nil
}

type metadataWireRecord struct {
	Schema     string                  `json:"schema"`
	Interfaces []metadataWireInterface `json:"interfaces"`
	Digest     string                  `json:"digest"`
}

type metadataCanonicalRecord struct {
	Schema     string                  `json:"schema"`
	Interfaces []metadataWireInterface `json:"interfaces"`
}

type metadataWireInterface struct {
	ID                  string `json:"id"`
	ContractDigest      string `json:"contract_digest"`
	DocumentationDigest string `json:"documentation_digest"`
	ExampleDigest       string `json:"example_digest"`
}

func buildMetadata(interfaces []metadataWireInterface) (MetadataBaseline, error) {
	canonical, err := encodeMetadataCanonical(interfaces)
	if err != nil {
		return MetadataBaseline{}, fmt.Errorf("%w: encode canonical metadata record: %v", ErrInvalid, err)
	}
	identityDigest := digest(canonical)
	record, err := encodeMetadataRecord(interfaces, identityDigest)
	if err != nil {
		return MetadataBaseline{}, fmt.Errorf("%w: encode metadata record: %v", ErrInvalid, err)
	}
	if int64(len(record)) > MetadataMaximumBytes {
		return MetadataBaseline{}, fmt.Errorf("%w: encoded metadata record exceeds %d bytes", ErrInvalid, MetadataMaximumBytes)
	}
	return buildMetadataWithEncoding(interfaces, canonical, record, identityDigest), nil
}

func buildMetadataWithEncoding(interfaces []metadataWireInterface, canonical, record []byte, identityDigest string) MetadataBaseline {
	clonedInterfaces := make([]metadataWireInterface, len(interfaces))
	copy(clonedInterfaces, interfaces)
	return MetadataBaseline{
		record: metadataWireRecord{
			Schema:     MetadataSchema,
			Interfaces: clonedInterfaces,
			Digest:     identityDigest,
		},
		canonicalJSON: append([]byte(nil), canonical...),
		recordJSON:    append([]byte(nil), record...),
		digest:        identityDigest,
		prepared:      true,
	}
}

func encodeMetadataCanonical(interfaces []metadataWireInterface) ([]byte, error) {
	return json.Marshal(metadataCanonicalRecord{
		Schema:     MetadataSchema,
		Interfaces: interfaces,
	})
}

func encodeMetadataRecord(interfaces []metadataWireInterface, identityDigest string) ([]byte, error) {
	return json.Marshal(metadataWireRecord{
		Schema:     MetadataSchema,
		Interfaces: interfaces,
		Digest:     identityDigest,
	})
}

func validateMetadataInterfaces(values []metadataWireInterface, requireOrdered bool) error {
	if values == nil || len(values) > maximumInterfaces {
		return fmt.Errorf("metadata interfaces must be an array with at most %d entries", maximumInterfaces)
	}
	for index, value := range values {
		if requireOrdered && index > 0 && values[index-1].ID >= value.ID {
			return errors.New("metadata interfaces must be unique and sorted by exact ID")
		}
		if err := validateMetadataInterface(value); err != nil {
			return fmt.Errorf("metadata interfaces[%d]: %v", index, err)
		}
	}
	return nil
}

func validateMetadataInterface(value metadataWireInterface) error {
	identifier, err := interfaceid.Parse(value.ID)
	if err != nil || identifier.String() != value.ID {
		return fmt.Errorf("ID %q is not canonical", value.ID)
	}
	if !validDigest(value.ContractDigest) {
		return errors.New("contract digest is invalid")
	}
	if !validDigest(value.DocumentationDigest) {
		return errors.New("documentation digest is invalid")
	}
	if !validDigest(value.ExampleDigest) {
		return errors.New("example digest is invalid")
	}
	return nil
}

func allMetadataClasses() []MetadataClass {
	return []MetadataClass{
		MetadataClassContract,
		MetadataClassDocumentation,
		MetadataClassExamples,
	}
}

func changedMetadataClasses(previous, current metadataWireInterface) []MetadataClass {
	result := make([]MetadataClass, 0, 3)
	for _, class := range allMetadataClasses() {
		previousDigest, _ := metadataClassDigest(previous, class)
		currentDigest, _ := metadataClassDigest(current, class)
		if previousDigest != currentDigest {
			result = append(result, class)
		}
	}
	return result
}

func metadataClassDigest(value metadataWireInterface, class MetadataClass) (string, bool) {
	if value.ID == "" {
		return "", false
	}
	switch class {
	case MetadataClassContract:
		return value.ContractDigest, true
	case MetadataClassDocumentation:
		return value.DocumentationDigest, true
	case MetadataClassExamples:
		return value.ExampleDigest, true
	default:
		return "", false
	}
}

func validMetadataChange(change MetadataChange) bool {
	identifier, err := interfaceid.Parse(change.id)
	if err != nil || identifier.String() != change.id {
		return false
	}
	if !equalMetadataClasses(change.classes, expectedMetadataClasses(change)) {
		return false
	}
	switch change.kind {
	case ChangeAdded:
		return change.previous.ID == "" &&
			change.current.ID == change.id &&
			validateMetadataInterface(change.current) == nil
	case ChangeRemoved:
		return change.previous.ID == change.id &&
			validateMetadataInterface(change.previous) == nil &&
			change.current.ID == ""
	case ChangeChanged:
		return change.previous.ID == change.id &&
			change.current.ID == change.id &&
			validateMetadataInterface(change.previous) == nil &&
			validateMetadataInterface(change.current) == nil &&
			len(change.classes) != 0
	default:
		return false
	}
}

func expectedMetadataClasses(change MetadataChange) []MetadataClass {
	switch change.kind {
	case ChangeAdded, ChangeRemoved:
		return allMetadataClasses()
	case ChangeChanged:
		return changedMetadataClasses(change.previous, change.current)
	default:
		return nil
	}
}

func equalMetadataClasses(left, right []MetadataClass) bool {
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
