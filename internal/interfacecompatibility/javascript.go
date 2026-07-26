package interfacecompatibility

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/javascriptgen"
)

const (
	// JavaScriptPath is the committed generated JavaScript API baseline.
	JavaScriptPath = "generated/compatibility/interface-javascript.json"
	// JavaScriptSchema identifies the strict generated JavaScript API baseline.
	JavaScriptSchema = "plystra.interface-javascript-baseline/v1"
	// JavaScriptMaximumBytes bounds a managed JavaScript baseline before parsing.
	JavaScriptMaximumBytes int64 = 4 << 20
)

// JavaScriptBaseline is one immutable exact snapshot of the shared package API
// and every exposed Interface's generated JavaScript surface, public types,
// and semantic-error union.
type JavaScriptBaseline struct {
	record        javaScriptWireRecord
	canonicalJSON []byte
	recordJSON    []byte
	digest        string
	prepared      bool
}

// Schema returns the exact JavaScript-baseline schema.
func (b JavaScriptBaseline) Schema() string {
	if !b.prepared {
		return ""
	}
	return JavaScriptSchema
}

// PackageDigest returns the shared package-root and public-runtime API digest.
func (b JavaScriptBaseline) PackageDigest() string {
	if !b.prepared {
		return ""
	}
	return b.record.PackageDigest
}

// Interfaces returns exact-ID-sorted defensive Interface API views.
func (b JavaScriptBaseline) Interfaces() []JavaScriptInterface {
	result := make([]JavaScriptInterface, len(b.record.Interfaces))
	for index, value := range b.record.Interfaces {
		result[index] = JavaScriptInterface{record: value}
	}
	return result
}

// CanonicalJSON returns the defensive digest input without the top-level
// digest.
func (b JavaScriptBaseline) CanonicalJSON() []byte {
	return append([]byte(nil), b.canonicalJSON...)
}

// RecordJSON returns the defensive strict generated JavaScript baseline.
func (b JavaScriptBaseline) RecordJSON() []byte {
	return append([]byte(nil), b.recordJSON...)
}

// Digest returns the lowercase SHA-256 digest of CanonicalJSON.
func (b JavaScriptBaseline) Digest() string { return b.digest }

// Valid reports whether this value is complete and internally canonical.
func (b JavaScriptBaseline) Valid() bool {
	if !b.prepared || b.record.Schema != JavaScriptSchema ||
		!validDigest(b.record.PackageDigest) ||
		b.record.Digest != b.digest {
		return false
	}
	if err := validateJavaScriptInterfaces(b.record.Interfaces, true); err != nil {
		return false
	}
	canonical, err := encodeJavaScriptCanonical(b.record.PackageDigest, b.record.Interfaces)
	if err != nil || !bytes.Equal(canonical, b.canonicalJSON) || digest(canonical) != b.digest {
		return false
	}
	record, err := encodeJavaScriptRecord(b.record.PackageDigest, b.record.Interfaces, b.digest)
	return err == nil && bytes.Equal(record, b.recordJSON)
}

// JavaScriptInterface is one immutable classified generated JavaScript API.
type JavaScriptInterface struct {
	record javaScriptWireInterface
}

// ID returns the exact canonical Interface ID.
func (i JavaScriptInterface) ID() string { return i.record.ID }

// SurfaceDigest returns the generated client/factory/export surface digest.
func (i JavaScriptInterface) SurfaceDigest() string { return i.record.SurfaceDigest }

// TypesDigest returns the generated public TypeScript shape digest.
func (i JavaScriptInterface) TypesDigest() string { return i.record.TypesDigest }

// SemanticErrorsDigest returns the generated semantic-error union digest.
func (i JavaScriptInterface) SemanticErrorsDigest() string {
	return i.record.SemanticErrorsDigest
}

// NewJavaScript constructs a baseline from the exact structured API produced
// by the JavaScript renderer.
func NewJavaScript(api javascriptgen.PublicAPI) (JavaScriptBaseline, error) {
	if !api.Valid() {
		return JavaScriptBaseline{}, fmt.Errorf("%w: generated JavaScript public API is absent or invalid", ErrInvalid)
	}
	views := api.Interfaces()
	interfaces := make([]javaScriptWireInterface, len(views))
	for index, view := range views {
		interfaces[index] = javaScriptWireInterface{
			ID:                   view.ID(),
			SurfaceDigest:        view.SurfaceDigest(),
			TypesDigest:          view.TypesDigest(),
			SemanticErrorsDigest: view.SemanticErrorsDigest(),
		}
	}
	if err := validateJavaScriptInterfaces(interfaces, true); err != nil {
		return JavaScriptBaseline{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return buildJavaScript(api.PackageDigest(), interfaces)
}

// DecodeJavaScript strictly restores one canonical generated JavaScript API
// baseline.
func DecodeJavaScript(data []byte) (JavaScriptBaseline, error) {
	if len(data) == 0 || int64(len(data)) > JavaScriptMaximumBytes {
		return JavaScriptBaseline{}, fmt.Errorf(
			"%w: JavaScript record must contain between 1 and %d bytes",
			ErrHistory,
			JavaScriptMaximumBytes,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record javaScriptWireRecord
	if err := decoder.Decode(&record); err != nil {
		return JavaScriptBaseline{}, fmt.Errorf("%w: decode JavaScript record: %v", ErrHistory, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return JavaScriptBaseline{}, fmt.Errorf("%w: JavaScript record contains trailing JSON", ErrHistory)
	}
	if record.Schema != JavaScriptSchema {
		return JavaScriptBaseline{}, fmt.Errorf("%w: JavaScript schema must equal %q", ErrHistory, JavaScriptSchema)
	}
	if !validDigest(record.PackageDigest) {
		return JavaScriptBaseline{}, fmt.Errorf("%w: JavaScript package digest is invalid", ErrHistory)
	}
	if err := validateJavaScriptInterfaces(record.Interfaces, true); err != nil {
		return JavaScriptBaseline{}, fmt.Errorf("%w: %v", ErrHistory, err)
	}
	canonical, err := encodeJavaScriptCanonical(record.PackageDigest, record.Interfaces)
	if err != nil {
		return JavaScriptBaseline{}, fmt.Errorf("%w: encode canonical JavaScript record: %v", ErrHistory, err)
	}
	if !validDigest(record.Digest) || record.Digest != digest(canonical) {
		return JavaScriptBaseline{}, fmt.Errorf("%w: JavaScript digest does not match the canonical projections", ErrHistory)
	}
	encoded, err := encodeJavaScriptRecord(record.PackageDigest, record.Interfaces, record.Digest)
	if err != nil {
		return JavaScriptBaseline{}, fmt.Errorf("%w: encode JavaScript record: %v", ErrHistory, err)
	}
	if !bytes.Equal(encoded, data) {
		return JavaScriptBaseline{}, fmt.Errorf("%w: JavaScript record is not in canonical byte form", ErrHistory)
	}
	return buildJavaScriptWithEncoding(
		record.PackageDigest,
		record.Interfaces,
		canonical,
		encoded,
		record.Digest,
	), nil
}

// ReconcileJavaScript constructs the current JavaScript API baseline and
// compares it with exact prior owned evidence. A missing prior record is the
// valid initial state.
func ReconcileJavaScript(
	api javascriptgen.PublicAPI,
	previous []byte,
	previousExists bool,
) (JavaScriptBaseline, JavaScriptComparison, error) {
	current, err := NewJavaScript(api)
	if err != nil {
		return JavaScriptBaseline{}, JavaScriptComparison{}, err
	}
	emptyAPI, err := javascriptgen.BuildPublicAPIEmpty()
	if err != nil {
		return JavaScriptBaseline{}, JavaScriptComparison{}, err
	}
	prior, err := NewJavaScript(emptyAPI)
	if err != nil {
		return JavaScriptBaseline{}, JavaScriptComparison{}, err
	}
	if previousExists {
		prior, err = DecodeJavaScript(previous)
		if err != nil {
			return JavaScriptBaseline{}, JavaScriptComparison{}, err
		}
	} else if len(previous) != 0 {
		return JavaScriptBaseline{}, JavaScriptComparison{}, fmt.Errorf("%w: absent prior JavaScript record has bytes", ErrHistory)
	}
	comparison, err := CompareJavaScript(prior, current)
	if err != nil {
		return JavaScriptBaseline{}, JavaScriptComparison{}, err
	}
	return current, comparison, nil
}

// JavaScriptClass identifies one per-Interface JavaScript compatibility class.
type JavaScriptClass string

const (
	// JavaScriptClassSurface covers client path, factory, operation, and root
	// exports.
	JavaScriptClassSurface JavaScriptClass = "surface"
	// JavaScriptClassTypes covers request, response, reachable messages,
	// requiredness, and exact TypeScript mappings.
	JavaScriptClassTypes JavaScriptClass = "types"
	// JavaScriptClassSemanticErrors covers the declared semantic-error union.
	JavaScriptClassSemanticErrors JavaScriptClass = "semantic_errors"
)

// JavaScriptChange is one immutable exact-ID JavaScript API difference.
type JavaScriptChange struct {
	kind     ChangeKind
	id       string
	classes  []JavaScriptClass
	previous javaScriptWireInterface
	current  javaScriptWireInterface
}

// Kind returns added, removed, or changed.
func (c JavaScriptChange) Kind() ChangeKind { return c.kind }

// ID returns the exact canonical Interface ID.
func (c JavaScriptChange) ID() string { return c.id }

// Classes returns the changed JavaScript classes in canonical order.
func (c JavaScriptChange) Classes() []JavaScriptClass {
	return append([]JavaScriptClass(nil), c.classes...)
}

// PreviousDigest returns the prior digest for one class when the Interface
// existed in the previous baseline.
func (c JavaScriptChange) PreviousDigest(class JavaScriptClass) (string, bool) {
	return javaScriptClassDigest(c.previous, class)
}

// CurrentDigest returns the current digest for one class when the Interface
// exists in the current baseline.
func (c JavaScriptChange) CurrentDigest(class JavaScriptClass) (string, bool) {
	return javaScriptClassDigest(c.current, class)
}

// JavaScriptComparison is one immutable package and exact-ID-sorted Interface
// API comparison.
type JavaScriptComparison struct {
	previousDigest        string
	currentDigest         string
	previousPackageDigest string
	currentPackageDigest  string
	packageChanged        bool
	changes               []JavaScriptChange
	prepared              bool
}

// Clean reports whether the shared package and every Interface API are
// unchanged.
func (c JavaScriptComparison) Clean() bool {
	return c.prepared && !c.packageChanged && len(c.changes) == 0
}

// PackageChanged reports whether shared package-root exports or public runtime
// types changed.
func (c JavaScriptComparison) PackageChanged() bool {
	return c.prepared && c.packageChanged
}

// PreviousPackageDigest returns the prior shared package API digest.
func (c JavaScriptComparison) PreviousPackageDigest() string {
	return c.previousPackageDigest
}

// CurrentPackageDigest returns the current shared package API digest.
func (c JavaScriptComparison) CurrentPackageDigest() string {
	return c.currentPackageDigest
}

// PreviousDigest returns the compared prior JavaScript-baseline digest.
func (c JavaScriptComparison) PreviousDigest() string { return c.previousDigest }

// CurrentDigest returns the compared current JavaScript-baseline digest.
func (c JavaScriptComparison) CurrentDigest() string { return c.currentDigest }

// Changes returns defensive differences sorted by exact Interface ID.
func (c JavaScriptComparison) Changes() []JavaScriptChange {
	result := make([]JavaScriptChange, len(c.changes))
	for index, change := range c.changes {
		result[index] = change
		result[index].classes = append([]JavaScriptClass(nil), change.classes...)
	}
	return result
}

// Valid reports whether the comparison has complete baseline identities and
// exact JavaScript-class differences.
func (c JavaScriptComparison) Valid() bool {
	if !c.prepared ||
		!validDigest(c.previousDigest) ||
		!validDigest(c.currentDigest) ||
		!validDigest(c.previousPackageDigest) ||
		!validDigest(c.currentPackageDigest) ||
		c.packageChanged != (c.previousPackageDigest != c.currentPackageDigest) {
		return false
	}
	for index, change := range c.changes {
		if index > 0 && c.changes[index-1].id >= change.id {
			return false
		}
		if !validJavaScriptChange(change) {
			return false
		}
	}
	return true
}

// CompareJavaScript returns exact shared-package plus added, removed, and
// per-Interface JavaScript-class changes.
func CompareJavaScript(previous, current JavaScriptBaseline) (JavaScriptComparison, error) {
	if !previous.Valid() || !current.Valid() {
		return JavaScriptComparison{}, fmt.Errorf("%w: both compared JavaScript baselines must be valid", ErrInvalid)
	}
	before := make(map[string]javaScriptWireInterface, len(previous.record.Interfaces))
	after := make(map[string]javaScriptWireInterface, len(current.record.Interfaces))
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

	changes := make([]JavaScriptChange, 0)
	for _, identifier := range ordered {
		previousValue, previousExists := before[identifier]
		currentValue, currentExists := after[identifier]
		switch {
		case !previousExists:
			changes = append(changes, JavaScriptChange{
				kind:    ChangeAdded,
				id:      identifier,
				classes: allJavaScriptClasses(),
				current: currentValue,
			})
		case !currentExists:
			changes = append(changes, JavaScriptChange{
				kind:     ChangeRemoved,
				id:       identifier,
				classes:  allJavaScriptClasses(),
				previous: previousValue,
			})
		default:
			classes := changedJavaScriptClasses(previousValue, currentValue)
			if len(classes) != 0 {
				changes = append(changes, JavaScriptChange{
					kind:     ChangeChanged,
					id:       identifier,
					classes:  classes,
					previous: previousValue,
					current:  currentValue,
				})
			}
		}
	}
	result := JavaScriptComparison{
		previousDigest:        previous.digest,
		currentDigest:         current.digest,
		previousPackageDigest: previous.record.PackageDigest,
		currentPackageDigest:  current.record.PackageDigest,
		packageChanged:        previous.record.PackageDigest != current.record.PackageDigest,
		changes:               changes,
		prepared:              true,
	}
	if !result.Valid() {
		return JavaScriptComparison{}, fmt.Errorf("%w: constructed JavaScript comparison is invalid", ErrInvalid)
	}
	return result, nil
}

type javaScriptWireRecord struct {
	Schema        string                    `json:"schema"`
	PackageDigest string                    `json:"package_digest"`
	Interfaces    []javaScriptWireInterface `json:"interfaces"`
	Digest        string                    `json:"digest"`
}

type javaScriptCanonicalRecord struct {
	Schema        string                    `json:"schema"`
	PackageDigest string                    `json:"package_digest"`
	Interfaces    []javaScriptWireInterface `json:"interfaces"`
}

type javaScriptWireInterface struct {
	ID                   string `json:"id"`
	SurfaceDigest        string `json:"surface_digest"`
	TypesDigest          string `json:"types_digest"`
	SemanticErrorsDigest string `json:"semantic_errors_digest"`
}

func buildJavaScript(packageDigest string, interfaces []javaScriptWireInterface) (JavaScriptBaseline, error) {
	if interfaces == nil {
		interfaces = []javaScriptWireInterface{}
	}
	if !validDigest(packageDigest) {
		return JavaScriptBaseline{}, fmt.Errorf("%w: JavaScript package digest is invalid", ErrInvalid)
	}
	canonical, err := encodeJavaScriptCanonical(packageDigest, interfaces)
	if err != nil {
		return JavaScriptBaseline{}, fmt.Errorf("%w: encode canonical JavaScript record: %v", ErrInvalid, err)
	}
	identityDigest := digest(canonical)
	record, err := encodeJavaScriptRecord(packageDigest, interfaces, identityDigest)
	if err != nil {
		return JavaScriptBaseline{}, fmt.Errorf("%w: encode JavaScript record: %v", ErrInvalid, err)
	}
	if int64(len(record)) > JavaScriptMaximumBytes {
		return JavaScriptBaseline{}, fmt.Errorf("%w: encoded JavaScript record exceeds %d bytes", ErrInvalid, JavaScriptMaximumBytes)
	}
	return buildJavaScriptWithEncoding(packageDigest, interfaces, canonical, record, identityDigest), nil
}

func buildJavaScriptWithEncoding(
	packageDigest string,
	interfaces []javaScriptWireInterface,
	canonical []byte,
	record []byte,
	identityDigest string,
) JavaScriptBaseline {
	clonedInterfaces := append([]javaScriptWireInterface(nil), interfaces...)
	if clonedInterfaces == nil {
		clonedInterfaces = []javaScriptWireInterface{}
	}
	return JavaScriptBaseline{
		record: javaScriptWireRecord{
			Schema:        JavaScriptSchema,
			PackageDigest: packageDigest,
			Interfaces:    clonedInterfaces,
			Digest:        identityDigest,
		},
		canonicalJSON: append([]byte(nil), canonical...),
		recordJSON:    append([]byte(nil), record...),
		digest:        identityDigest,
		prepared:      true,
	}
}

func encodeJavaScriptCanonical(
	packageDigest string,
	interfaces []javaScriptWireInterface,
) ([]byte, error) {
	return json.Marshal(javaScriptCanonicalRecord{
		Schema:        JavaScriptSchema,
		PackageDigest: packageDigest,
		Interfaces:    interfaces,
	})
}

func encodeJavaScriptRecord(
	packageDigest string,
	interfaces []javaScriptWireInterface,
	identityDigest string,
) ([]byte, error) {
	return json.Marshal(javaScriptWireRecord{
		Schema:        JavaScriptSchema,
		PackageDigest: packageDigest,
		Interfaces:    interfaces,
		Digest:        identityDigest,
	})
}

func validateJavaScriptInterfaces(values []javaScriptWireInterface, requireOrdered bool) error {
	if values == nil || len(values) > maximumInterfaces {
		return fmt.Errorf("JavaScript interfaces must be an array with at most %d entries", maximumInterfaces)
	}
	for index, value := range values {
		if requireOrdered && index > 0 && values[index-1].ID >= value.ID {
			return errors.New("JavaScript interfaces must be unique and sorted by exact ID")
		}
		if err := validateJavaScriptInterface(value); err != nil {
			return fmt.Errorf("JavaScript interfaces[%d]: %v", index, err)
		}
	}
	return nil
}

func validateJavaScriptInterface(value javaScriptWireInterface) error {
	identifier, err := interfaceid.Parse(value.ID)
	if err != nil || identifier.String() != value.ID {
		return fmt.Errorf("ID %q is not canonical", value.ID)
	}
	if !validDigest(value.SurfaceDigest) ||
		!validDigest(value.TypesDigest) ||
		!validDigest(value.SemanticErrorsDigest) {
		return errors.New("surface, types, and semantic-error digests must be lower-case SHA-256 values")
	}
	return nil
}

func changedJavaScriptClasses(
	previous javaScriptWireInterface,
	current javaScriptWireInterface,
) []JavaScriptClass {
	classes := make([]JavaScriptClass, 0, 3)
	if previous.SurfaceDigest != current.SurfaceDigest {
		classes = append(classes, JavaScriptClassSurface)
	}
	if previous.TypesDigest != current.TypesDigest {
		classes = append(classes, JavaScriptClassTypes)
	}
	if previous.SemanticErrorsDigest != current.SemanticErrorsDigest {
		classes = append(classes, JavaScriptClassSemanticErrors)
	}
	return classes
}

func allJavaScriptClasses() []JavaScriptClass {
	return []JavaScriptClass{
		JavaScriptClassSurface,
		JavaScriptClassTypes,
		JavaScriptClassSemanticErrors,
	}
}

func javaScriptClassDigest(
	value javaScriptWireInterface,
	class JavaScriptClass,
) (string, bool) {
	if value.ID == "" {
		return "", false
	}
	switch class {
	case JavaScriptClassSurface:
		return value.SurfaceDigest, true
	case JavaScriptClassTypes:
		return value.TypesDigest, true
	case JavaScriptClassSemanticErrors:
		return value.SemanticErrorsDigest, true
	default:
		return "", false
	}
}

func validJavaScriptChange(change JavaScriptChange) bool {
	identifier, err := interfaceid.Parse(change.id)
	if err != nil || identifier.String() != change.id {
		return false
	}
	expected := allJavaScriptClasses()
	switch change.kind {
	case ChangeAdded:
		return change.previous.ID == "" &&
			change.current.ID == change.id &&
			equalJavaScriptClasses(change.classes, expected)
	case ChangeRemoved:
		return change.previous.ID == change.id &&
			change.current.ID == "" &&
			equalJavaScriptClasses(change.classes, expected)
	case ChangeChanged:
		return change.previous.ID == change.id &&
			change.current.ID == change.id &&
			len(change.classes) != 0 &&
			equalJavaScriptClasses(
				change.classes,
				changedJavaScriptClasses(change.previous, change.current),
			)
	default:
		return false
	}
}

func equalJavaScriptClasses(left, right []JavaScriptClass) bool {
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
