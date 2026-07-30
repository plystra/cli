// Package interfaceprovenance owns the strict non-secret Interface and
// constructor provenance embedded in the generated application manifest.
package interfaceprovenance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"golang.org/x/mod/module"
)

const (
	// Schema identifies the only supported Interface-provenance record.
	Schema = "plystra.interface-provenance/v1"
	// MaximumBytes bounds one generated manifest provenance record.
	MaximumBytes int64 = 16 << 20

	maximumRecords     = 4096
	maximumSources     = 4096
	maximumStringBytes = 8192
)

var (
	// ErrInvalid reports incomplete, contradictory, unsafe, or noncanonical
	// Interface and constructor provenance.
	ErrInvalid = errors.New("invalid Interface provenance")
	// ErrRecord reports a malformed, tampered, oversized, or noncanonical
	// generated Interface-provenance record.
	ErrRecord = errors.New("invalid Interface provenance record")
)

// SelectionReason is the closed reason for selecting one ordinary
// Implementation constructor.
type SelectionReason string

const (
	SelectionExplicit         SelectionReason = "explicit"
	SelectionUniqueCompatible SelectionReason = "unique-compatible"
)

// Input contains the complete visible authored Interface inventory, reachable
// ordinary bindings, dependency-first constructor graph, and required
// intrinsic Kernel Interfaces.
type Input struct {
	Interfaces   []InterfaceInput
	Bindings     []BindingInput
	Constructors []ConstructorInput
	Intrinsics   []IntrinsicInput
}

// InterfaceInput is one visible authored Interface and its exact non-secret
// source and compatibility identities.
type InterfaceInput struct {
	ID                  string
	PackagePath         string
	ModulePath          string
	ModuleVersion       string
	DirectiveSource     string
	MetadataSource      string
	ShapeDigest         string
	ContractDigest      string
	DocumentationDigest string
	ExampleDigest       string
}

// BindingInput is one reachable ordinary Interface binding.
type BindingInput struct {
	InterfaceID           string
	RootSources           []string
	ExposureSources       []string
	RequiringConstructors []string
	Selection             SelectionInput
	ConfigurationOwner    string
	Policy                PolicyInput
	Mappings              MappingInput
}

// SelectionInput is the complete selected constructor provenance repeated on
// a binding so the binding can be understood without an external join.
type SelectionInput struct {
	Constructor       string
	ModulePath        string
	ModuleVersion     string
	Source            string
	ConcreteType      string
	Reason            SelectionReason
	Sources           []string
	ConstructionOrder int
}

// ConstructorInput is one reachable constructor in dependency-first order.
type ConstructorInput struct {
	Symbol               string
	ModulePath           string
	ModuleVersion        string
	Source               string
	ConcreteType         string
	ConstructionOrder    int
	Provides             []string
	ConfigurationOwner   string
	ConfigurationSources []string
	Dependencies         []DependencyInput
}

// DependencyInput is one parameter-ordered required or optional constructor
// dependency and its resolved target when available.
type DependencyInput struct {
	InterfaceID         string
	PackagePath         string
	ParameterName       string
	ParameterPosition   int
	Optional            bool
	Available           bool
	SelectedConstructor string
}

// PolicyInput is the normalized effective declarative invocation-policy input.
// Runtime policy enforcement remains owned by its later roadmap gate.
type PolicyInput struct {
	Timeout string
	Sources []string
}

// MappingInput identifies every currently generated projection for one
// Interface. Transport and JavaScript fields remain empty when that public
// surface is not selected.
type MappingInput struct {
	ProxyPath                      string
	AdapterPath                    string
	AssemblyPath                   string
	ProtobufSchemaPath             string
	ProtobufDescriptorSetPath      string
	ProtobufDescriptorDigest       string
	WireMapPath                    string
	WireMapDigest                  string
	ConnectHandlerPath             string
	ConnectProcedure               string
	ConnectProcedureDigest         string
	HTTPRoute                      string
	JavaScriptModulePath           string
	JavaScriptSurfaceDigest        string
	JavaScriptTypesDigest          string
	JavaScriptSemanticErrorsDigest string
}

// IntrinsicInput is one required reserved Kernel Interface. Intrinsics have no
// ordinary selected constructor or adapter.
type IntrinsicInput struct {
	Interface          InterfaceInput
	RequirementSources []string
	ExposureSources    []string
	Policy             PolicyInput
	Mappings           MappingInput
}

// Provenance is one immutable canonical generated-manifest record.
type Provenance struct {
	record        wireRecord
	canonicalJSON []byte
	recordJSON    []byte
	digest        string
	prepared      bool
}

// SchemaVersion returns the exact strict schema.
func (p Provenance) SchemaVersion() string {
	if !p.prepared {
		return ""
	}
	return Schema
}

// Interfaces returns exact-ID-sorted defensive authored Interface records.
func (p Provenance) Interfaces() []Interface {
	result := make([]Interface, len(p.record.Interfaces))
	for index, value := range p.record.Interfaces {
		result[index] = Interface{record: value}
	}
	return result
}

// Bindings returns exact-ID-sorted defensive ordinary binding records.
func (p Provenance) Bindings() []Binding {
	result := make([]Binding, len(p.record.Bindings))
	for index, value := range p.record.Bindings {
		result[index] = Binding{record: cloneWireBinding(value)}
	}
	return result
}

// Constructors returns dependency-first defensive constructor records.
func (p Provenance) Constructors() []Constructor {
	result := make([]Constructor, len(p.record.Constructors))
	for index, value := range p.record.Constructors {
		result[index] = Constructor{record: cloneWireConstructor(value)}
	}
	return result
}

// Intrinsics returns exact-ID-sorted defensive intrinsic Interface records.
func (p Provenance) Intrinsics() []Intrinsic {
	result := make([]Intrinsic, len(p.record.Intrinsics))
	for index, value := range p.record.Intrinsics {
		result[index] = Intrinsic{record: cloneWireIntrinsic(value)}
	}
	return result
}

// CanonicalJSON returns the defensive digest input without its digest field.
func (p Provenance) CanonicalJSON() []byte {
	return append([]byte(nil), p.canonicalJSON...)
}

// RecordJSON returns the strict canonical manifest record including its digest.
func (p Provenance) RecordJSON() []byte {
	return append([]byte(nil), p.recordJSON...)
}

// Digest returns the lowercase SHA-256 identity of CanonicalJSON.
func (p Provenance) Digest() string { return p.digest }

// Valid reports whether this value is complete and internally canonical.
func (p Provenance) Valid() bool {
	if !p.prepared || p.record.Schema != Schema || p.record.Digest != p.digest {
		return false
	}
	if err := validateRecord(p.record, true); err != nil {
		return false
	}
	canonical, err := encodeCanonical(p.record)
	if err != nil || !bytes.Equal(canonical, p.canonicalJSON) || digest(canonical) != p.digest {
		return false
	}
	record, err := encodeRecord(p.record, p.digest)
	return err == nil && bytes.Equal(record, p.recordJSON)
}

// Interface is one immutable authored Interface provenance view.
type Interface struct{ record wireInterface }

func (i Interface) ID() string                  { return i.record.ID }
func (i Interface) PackagePath() string         { return i.record.PackagePath }
func (i Interface) ModulePath() string          { return i.record.ModulePath }
func (i Interface) ModuleVersion() string       { return i.record.ModuleVersion }
func (i Interface) DirectiveSource() string     { return i.record.DirectiveSource }
func (i Interface) MetadataSource() string      { return i.record.MetadataSource }
func (i Interface) ShapeDigest() string         { return i.record.ShapeDigest }
func (i Interface) ContractDigest() string      { return i.record.ContractDigest }
func (i Interface) DocumentationDigest() string { return i.record.DocumentationDigest }
func (i Interface) ExampleDigest() string       { return i.record.ExampleDigest }

// Binding is one immutable ordinary Interface binding view.
type Binding struct{ record wireBinding }

func (b Binding) InterfaceID() string { return b.record.InterfaceID }
func (b Binding) RootSources() []string {
	return append([]string(nil), b.record.RootSources...)
}
func (b Binding) ExposureSources() []string {
	return append([]string(nil), b.record.ExposureSources...)
}
func (b Binding) RequiringConstructors() []string {
	return append([]string(nil), b.record.RequiringConstructors...)
}
func (b Binding) Selection() Selection {
	return Selection{record: cloneWireSelection(b.record.Selection)}
}
func (b Binding) ConfigurationOwner() string { return b.record.ConfigurationOwner }
func (b Binding) Policy() Policy             { return Policy{record: cloneWirePolicy(b.record.Policy)} }
func (b Binding) Mappings() Mapping          { return Mapping{record: b.record.Mappings} }

// Selection is one immutable selected Implementation view.
type Selection struct{ record wireSelection }

func (s Selection) Constructor() string     { return s.record.Constructor }
func (s Selection) ModulePath() string      { return s.record.ModulePath }
func (s Selection) ModuleVersion() string   { return s.record.ModuleVersion }
func (s Selection) Source() string          { return s.record.Source }
func (s Selection) ConcreteType() string    { return s.record.ConcreteType }
func (s Selection) Reason() SelectionReason { return s.record.Reason }
func (s Selection) ConstructionOrder() int  { return s.record.ConstructionOrder }
func (s Selection) Sources() []string       { return append([]string(nil), s.record.Sources...) }

// Constructor is one immutable constructor-graph node view.
type Constructor struct{ record wireConstructor }

func (c Constructor) Symbol() string             { return c.record.Symbol }
func (c Constructor) ModulePath() string         { return c.record.ModulePath }
func (c Constructor) ModuleVersion() string      { return c.record.ModuleVersion }
func (c Constructor) Source() string             { return c.record.Source }
func (c Constructor) ConcreteType() string       { return c.record.ConcreteType }
func (c Constructor) ConstructionOrder() int     { return c.record.ConstructionOrder }
func (c Constructor) Provides() []string         { return append([]string(nil), c.record.Provides...) }
func (c Constructor) ConfigurationOwner() string { return c.record.ConfigurationOwner }
func (c Constructor) ConfigurationSources() []string {
	return append([]string(nil), c.record.ConfigurationSources...)
}
func (c Constructor) Dependencies() []Dependency {
	result := make([]Dependency, len(c.record.Dependencies))
	for index, value := range c.record.Dependencies {
		result[index] = Dependency{record: value}
	}
	return result
}

// Dependency is one immutable constructor dependency edge.
type Dependency struct{ record wireDependency }

func (d Dependency) InterfaceID() string         { return d.record.InterfaceID }
func (d Dependency) PackagePath() string         { return d.record.PackagePath }
func (d Dependency) ParameterName() string       { return d.record.ParameterName }
func (d Dependency) ParameterPosition() int      { return d.record.ParameterPosition }
func (d Dependency) Optional() bool              { return d.record.Optional }
func (d Dependency) Available() bool             { return d.record.Available }
func (d Dependency) SelectedConstructor() string { return d.record.SelectedConstructor }

// Policy is one immutable normalized invocation-policy input.
type Policy struct{ record wirePolicy }

func (p Policy) Timeout() string   { return p.record.Timeout }
func (p Policy) Sources() []string { return append([]string(nil), p.record.Sources...) }

// Mapping is one immutable generated-projection mapping.
type Mapping struct{ record wireMapping }

func (m Mapping) ProxyPath() string                 { return m.record.ProxyPath }
func (m Mapping) AdapterPath() string               { return m.record.AdapterPath }
func (m Mapping) AssemblyPath() string              { return m.record.AssemblyPath }
func (m Mapping) ProtobufSchemaPath() string        { return m.record.ProtobufSchemaPath }
func (m Mapping) ProtobufDescriptorSetPath() string { return m.record.ProtobufDescriptorSetPath }
func (m Mapping) ProtobufDescriptorDigest() string  { return m.record.ProtobufDescriptorDigest }
func (m Mapping) WireMapPath() string               { return m.record.WireMapPath }
func (m Mapping) WireMapDigest() string             { return m.record.WireMapDigest }
func (m Mapping) ConnectHandlerPath() string        { return m.record.ConnectHandlerPath }
func (m Mapping) ConnectProcedure() string          { return m.record.ConnectProcedure }
func (m Mapping) ConnectProcedureDigest() string    { return m.record.ConnectProcedureDigest }
func (m Mapping) HTTPRoute() string                 { return m.record.HTTPRoute }
func (m Mapping) JavaScriptModulePath() string      { return m.record.JavaScriptModulePath }
func (m Mapping) JavaScriptSurfaceDigest() string   { return m.record.JavaScriptSurfaceDigest }
func (m Mapping) JavaScriptTypesDigest() string     { return m.record.JavaScriptTypesDigest }
func (m Mapping) JavaScriptSemanticErrorsDigest() string {
	return m.record.JavaScriptSemanticErrorsDigest
}

// Intrinsic is one immutable reserved Kernel Interface view.
type Intrinsic struct{ record wireIntrinsic }

func (i Intrinsic) Interface() Interface { return Interface{record: i.record.Interface} }
func (i Intrinsic) RequirementSources() []string {
	return append([]string(nil), i.record.RequirementSources...)
}
func (i Intrinsic) ExposureSources() []string {
	return append([]string(nil), i.record.ExposureSources...)
}
func (i Intrinsic) Policy() Policy    { return Policy{record: cloneWirePolicy(i.record.Policy)} }
func (i Intrinsic) Mappings() Mapping { return Mapping{record: i.record.Mappings} }

// New validates and canonicalizes complete Interface and constructor
// provenance independently of discovery, filesystem, and map order.
func New(input Input) (Provenance, error) {
	record := wireRecord{
		Schema:       Schema,
		Interfaces:   make([]wireInterface, len(input.Interfaces)),
		Bindings:     make([]wireBinding, len(input.Bindings)),
		Constructors: make([]wireConstructor, len(input.Constructors)),
		Intrinsics:   make([]wireIntrinsic, len(input.Intrinsics)),
	}
	for index, value := range input.Interfaces {
		record.Interfaces[index] = wireInterface(value)
	}
	for index, value := range input.Bindings {
		record.Bindings[index] = wireBinding{
			InterfaceID:           value.InterfaceID,
			RootSources:           canonicalStrings(value.RootSources),
			ExposureSources:       canonicalStrings(value.ExposureSources),
			RequiringConstructors: canonicalStrings(value.RequiringConstructors),
			Selection:             wireSelection(value.Selection),
			ConfigurationOwner:    value.ConfigurationOwner,
			Policy: wirePolicy{
				Timeout: value.Policy.Timeout,
				Sources: canonicalStrings(value.Policy.Sources),
			},
			Mappings: wireMapping(value.Mappings),
		}
		record.Bindings[index].Selection.Sources = canonicalStrings(value.Selection.Sources)
	}
	for index, value := range input.Constructors {
		dependencies := make([]wireDependency, len(value.Dependencies))
		for dependencyIndex, dependency := range value.Dependencies {
			dependencies[dependencyIndex] = wireDependency(dependency)
		}
		sort.Slice(dependencies, func(left, right int) bool {
			return dependencies[left].ParameterPosition < dependencies[right].ParameterPosition
		})
		record.Constructors[index] = wireConstructor{
			Symbol:               value.Symbol,
			ModulePath:           value.ModulePath,
			ModuleVersion:        value.ModuleVersion,
			Source:               value.Source,
			ConcreteType:         value.ConcreteType,
			ConstructionOrder:    value.ConstructionOrder,
			Provides:             canonicalStrings(value.Provides),
			ConfigurationOwner:   value.ConfigurationOwner,
			ConfigurationSources: canonicalStrings(value.ConfigurationSources),
			Dependencies:         dependencies,
		}
	}
	for index, value := range input.Intrinsics {
		record.Intrinsics[index] = wireIntrinsic{
			Interface:          wireInterface(value.Interface),
			RequirementSources: canonicalStrings(value.RequirementSources),
			ExposureSources:    canonicalStrings(value.ExposureSources),
			Policy: wirePolicy{
				Timeout: value.Policy.Timeout,
				Sources: canonicalStrings(value.Policy.Sources),
			},
			Mappings: wireMapping(value.Mappings),
		}
	}
	sort.Slice(record.Interfaces, func(left, right int) bool {
		return record.Interfaces[left].ID < record.Interfaces[right].ID
	})
	sort.Slice(record.Bindings, func(left, right int) bool {
		return record.Bindings[left].InterfaceID < record.Bindings[right].InterfaceID
	})
	sort.Slice(record.Constructors, func(left, right int) bool {
		if record.Constructors[left].ConstructionOrder != record.Constructors[right].ConstructionOrder {
			return record.Constructors[left].ConstructionOrder < record.Constructors[right].ConstructionOrder
		}
		return record.Constructors[left].Symbol < record.Constructors[right].Symbol
	})
	sort.Slice(record.Intrinsics, func(left, right int) bool {
		return record.Intrinsics[left].Interface.ID < record.Intrinsics[right].Interface.ID
	})
	if err := validateRecord(record, true); err != nil {
		return Provenance{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return build(record)
}

// Decode strictly restores one canonical generated-manifest record.
func Decode(data []byte) (Provenance, error) {
	if len(data) == 0 || int64(len(data)) > MaximumBytes {
		return Provenance{}, fmt.Errorf("%w: record must contain between 1 and %d bytes", ErrRecord, MaximumBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record wireRecord
	if err := decoder.Decode(&record); err != nil {
		return Provenance{}, fmt.Errorf("%w: decode record: %v", ErrRecord, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Provenance{}, fmt.Errorf("%w: record contains trailing JSON", ErrRecord)
	}
	if record.Schema != Schema {
		return Provenance{}, fmt.Errorf("%w: schema must equal %q", ErrRecord, Schema)
	}
	if err := validateRecord(record, true); err != nil {
		return Provenance{}, fmt.Errorf("%w: %v", ErrRecord, err)
	}
	canonical, err := encodeCanonical(record)
	if err != nil {
		return Provenance{}, fmt.Errorf("%w: encode canonical record: %v", ErrRecord, err)
	}
	if !validDigest(record.Digest) || record.Digest != digest(canonical) {
		return Provenance{}, fmt.Errorf("%w: digest does not match canonical Interface and constructor provenance", ErrRecord)
	}
	encoded, err := encodeRecord(record, record.Digest)
	if err != nil {
		return Provenance{}, fmt.Errorf("%w: encode record: %v", ErrRecord, err)
	}
	return buildWithEncoding(record, canonical, encoded, record.Digest), nil
}

type wireRecord struct {
	Schema       string            `json:"schema"`
	Interfaces   []wireInterface   `json:"interfaces"`
	Bindings     []wireBinding     `json:"bindings"`
	Constructors []wireConstructor `json:"constructors"`
	Intrinsics   []wireIntrinsic   `json:"intrinsics"`
	Digest       string            `json:"digest"`
}

type canonicalRecord struct {
	Schema       string            `json:"schema"`
	Interfaces   []wireInterface   `json:"interfaces"`
	Bindings     []wireBinding     `json:"bindings"`
	Constructors []wireConstructor `json:"constructors"`
	Intrinsics   []wireIntrinsic   `json:"intrinsics"`
}

type wireInterface struct {
	ID                  string `json:"id"`
	PackagePath         string `json:"package_path"`
	ModulePath          string `json:"module_path"`
	ModuleVersion       string `json:"module_version"`
	DirectiveSource     string `json:"directive_source"`
	MetadataSource      string `json:"metadata_source,omitempty"`
	ShapeDigest         string `json:"shape_digest"`
	ContractDigest      string `json:"contract_digest"`
	DocumentationDigest string `json:"documentation_digest"`
	ExampleDigest       string `json:"example_digest"`
}

type wireBinding struct {
	InterfaceID           string        `json:"interface_id"`
	RootSources           []string      `json:"root_sources"`
	ExposureSources       []string      `json:"exposure_sources"`
	RequiringConstructors []string      `json:"requiring_constructors"`
	Selection             wireSelection `json:"selection"`
	ConfigurationOwner    string        `json:"configuration_owner,omitempty"`
	Policy                wirePolicy    `json:"policy"`
	Mappings              wireMapping   `json:"mappings"`
}

type wireSelection struct {
	Constructor       string          `json:"constructor"`
	ModulePath        string          `json:"module_path"`
	ModuleVersion     string          `json:"module_version"`
	Source            string          `json:"source"`
	ConcreteType      string          `json:"concrete_type"`
	Reason            SelectionReason `json:"reason"`
	Sources           []string        `json:"sources"`
	ConstructionOrder int             `json:"construction_order"`
}

type wireConstructor struct {
	Symbol               string           `json:"symbol"`
	ModulePath           string           `json:"module_path"`
	ModuleVersion        string           `json:"module_version"`
	Source               string           `json:"source"`
	ConcreteType         string           `json:"concrete_type"`
	ConstructionOrder    int              `json:"construction_order"`
	Provides             []string         `json:"provides"`
	ConfigurationOwner   string           `json:"configuration_owner,omitempty"`
	ConfigurationSources []string         `json:"configuration_sources"`
	Dependencies         []wireDependency `json:"dependencies"`
}

type wireDependency struct {
	InterfaceID         string `json:"interface_id"`
	PackagePath         string `json:"package_path"`
	ParameterName       string `json:"parameter_name,omitempty"`
	ParameterPosition   int    `json:"parameter_position"`
	Optional            bool   `json:"optional"`
	Available           bool   `json:"available"`
	SelectedConstructor string `json:"selected_constructor,omitempty"`
}

type wirePolicy struct {
	Timeout string   `json:"timeout"`
	Sources []string `json:"sources"`
}

type wireMapping struct {
	ProxyPath                      string `json:"proxy_path,omitempty"`
	AdapterPath                    string `json:"adapter_path,omitempty"`
	AssemblyPath                   string `json:"assembly_path,omitempty"`
	ProtobufSchemaPath             string `json:"protobuf_schema_path,omitempty"`
	ProtobufDescriptorSetPath      string `json:"protobuf_descriptor_set_path,omitempty"`
	ProtobufDescriptorDigest       string `json:"protobuf_descriptor_digest,omitempty"`
	WireMapPath                    string `json:"wire_map_path,omitempty"`
	WireMapDigest                  string `json:"wire_map_digest,omitempty"`
	ConnectHandlerPath             string `json:"connect_handler_path,omitempty"`
	ConnectProcedure               string `json:"connect_procedure,omitempty"`
	ConnectProcedureDigest         string `json:"connect_procedure_digest,omitempty"`
	HTTPRoute                      string `json:"http_route,omitempty"`
	JavaScriptModulePath           string `json:"javascript_module_path,omitempty"`
	JavaScriptSurfaceDigest        string `json:"javascript_surface_digest,omitempty"`
	JavaScriptTypesDigest          string `json:"javascript_types_digest,omitempty"`
	JavaScriptSemanticErrorsDigest string `json:"javascript_semantic_errors_digest,omitempty"`
}

type wireIntrinsic struct {
	Interface          wireInterface `json:"interface"`
	RequirementSources []string      `json:"requirement_sources"`
	ExposureSources    []string      `json:"exposure_sources"`
	Policy             wirePolicy    `json:"policy"`
	Mappings           wireMapping   `json:"mappings"`
}

func build(record wireRecord) (Provenance, error) {
	canonical, err := encodeCanonical(record)
	if err != nil {
		return Provenance{}, fmt.Errorf("%w: encode canonical record: %v", ErrInvalid, err)
	}
	identity := digest(canonical)
	encoded, err := encodeRecord(record, identity)
	if err != nil {
		return Provenance{}, fmt.Errorf("%w: encode record: %v", ErrInvalid, err)
	}
	if int64(len(encoded)) > MaximumBytes {
		return Provenance{}, fmt.Errorf("%w: encoded record exceeds %d bytes", ErrInvalid, MaximumBytes)
	}
	return buildWithEncoding(record, canonical, encoded, identity), nil
}

func buildWithEncoding(record wireRecord, canonical, encoded []byte, identity string) Provenance {
	cloned := cloneWireRecord(record)
	cloned.Digest = identity
	return Provenance{
		record:        cloned,
		canonicalJSON: append([]byte(nil), canonical...),
		recordJSON:    append([]byte(nil), encoded...),
		digest:        identity,
		prepared:      true,
	}
}

func encodeCanonical(record wireRecord) ([]byte, error) {
	return json.Marshal(canonicalRecord{
		Schema:       record.Schema,
		Interfaces:   record.Interfaces,
		Bindings:     record.Bindings,
		Constructors: record.Constructors,
		Intrinsics:   record.Intrinsics,
	})
}

func encodeRecord(record wireRecord, identity string) ([]byte, error) {
	record.Digest = identity
	return json.Marshal(record)
}

func validateRecord(record wireRecord, requireOrdered bool) error {
	if record.Schema != Schema {
		return fmt.Errorf("schema must equal %q", Schema)
	}
	if record.Interfaces == nil || len(record.Interfaces) > maximumRecords {
		return fmt.Errorf("interfaces must be an array with at most %d entries", maximumRecords)
	}
	if record.Bindings == nil || len(record.Bindings) > maximumRecords {
		return fmt.Errorf("bindings must be an array with at most %d entries", maximumRecords)
	}
	if record.Constructors == nil || len(record.Constructors) > maximumRecords {
		return fmt.Errorf("constructors must be an array with at most %d entries", maximumRecords)
	}
	if record.Intrinsics == nil || len(record.Intrinsics) > maximumRecords {
		return fmt.Errorf("intrinsics must be an array with at most %d entries", maximumRecords)
	}

	interfaces := make(map[string]wireInterface, len(record.Interfaces))
	for index, value := range record.Interfaces {
		if requireOrdered && index > 0 && record.Interfaces[index-1].ID >= value.ID {
			return errors.New("interfaces must be unique and sorted by exact ID")
		}
		if err := validateInterface(value, false); err != nil {
			return fmt.Errorf("interfaces[%d]: %v", index, err)
		}
		interfaces[value.ID] = value
	}

	constructors := make(map[string]wireConstructor, len(record.Constructors))
	for index, value := range record.Constructors {
		if requireOrdered && value.ConstructionOrder != index+1 {
			return errors.New("constructors must be in unique contiguous dependency-first construction order")
		}
		if err := validateConstructor(value); err != nil {
			return fmt.Errorf("constructors[%d]: %v", index, err)
		}
		if _, duplicate := constructors[value.Symbol]; duplicate {
			return fmt.Errorf("constructors[%d] duplicates symbol %q", index, value.Symbol)
		}
		constructors[value.Symbol] = value
	}

	bindings := make(map[string]wireBinding, len(record.Bindings))
	for index, value := range record.Bindings {
		if requireOrdered && index > 0 && record.Bindings[index-1].InterfaceID >= value.InterfaceID {
			return errors.New("bindings must be unique and sorted by exact Interface ID")
		}
		if err := validateBinding(value); err != nil {
			return fmt.Errorf("bindings[%d]: %v", index, err)
		}
		if _, exists := interfaces[value.InterfaceID]; !exists {
			return fmt.Errorf("bindings[%d] references absent authored Interface %q", index, value.InterfaceID)
		}
		constructor, exists := constructors[value.Selection.Constructor]
		if !exists {
			return fmt.Errorf("bindings[%d] references absent constructor %q", index, value.Selection.Constructor)
		}
		if !selectionMatchesConstructor(value.Selection, constructor) {
			return fmt.Errorf("bindings[%d] selection does not match constructor %q provenance", index, constructor.Symbol)
		}
		if value.ConfigurationOwner != constructor.ConfigurationOwner {
			return fmt.Errorf("bindings[%d] configuration owner does not match constructor %q", index, constructor.Symbol)
		}
		bindings[value.InterfaceID] = value
	}

	for index, constructor := range record.Constructors {
		provided := make([]string, 0)
		for _, binding := range record.Bindings {
			if binding.Selection.Constructor == constructor.Symbol {
				provided = append(provided, binding.InterfaceID)
			}
		}
		sort.Strings(provided)
		if !equalStrings(provided, constructor.Provides) {
			return fmt.Errorf("constructors[%d] provides does not match selected bindings", index)
		}
		for dependencyIndex, dependency := range constructor.Dependencies {
			if !dependency.Available {
				continue
			}
			binding, exists := bindings[dependency.InterfaceID]
			if !exists || binding.Selection.Constructor != dependency.SelectedConstructor {
				return fmt.Errorf("constructors[%d].dependencies[%d] selected target does not match binding %q", index, dependencyIndex, dependency.InterfaceID)
			}
			target := constructors[dependency.SelectedConstructor]
			if target.ConstructionOrder >= constructor.ConstructionOrder {
				return fmt.Errorf("constructors[%d].dependencies[%d] target must precede its consumer", index, dependencyIndex)
			}
		}
	}

	for index, binding := range record.Bindings {
		expected := make([]string, 0)
		for _, constructor := range record.Constructors {
			for _, dependency := range constructor.Dependencies {
				if dependency.Available && dependency.InterfaceID == binding.InterfaceID {
					expected = append(expected, constructor.Symbol)
					break
				}
			}
		}
		sort.Strings(expected)
		if !equalStrings(expected, binding.RequiringConstructors) {
			return fmt.Errorf("bindings[%d] requiring constructors do not match the constructor graph", index)
		}
		if len(binding.RootSources) == 0 && len(binding.RequiringConstructors) == 0 {
			return fmt.Errorf("bindings[%d] is unreachable from any root or constructor", index)
		}
	}

	for index, value := range record.Intrinsics {
		if requireOrdered && index > 0 && record.Intrinsics[index-1].Interface.ID >= value.Interface.ID {
			return errors.New("intrinsics must be unique and sorted by exact Interface ID")
		}
		if err := validateIntrinsic(value); err != nil {
			return fmt.Errorf("intrinsics[%d]: %v", index, err)
		}
		if _, duplicate := interfaces[value.Interface.ID]; duplicate {
			return fmt.Errorf("intrinsics[%d] duplicates authored Interface %q", index, value.Interface.ID)
		}
		if _, duplicate := bindings[value.Interface.ID]; duplicate {
			return fmt.Errorf("intrinsics[%d] duplicates ordinary binding %q", index, value.Interface.ID)
		}
	}
	return nil
}

func validateInterface(value wireInterface, intrinsic bool) error {
	identifier, err := interfaceid.Parse(value.ID)
	if err != nil || identifier.String() != value.ID {
		return fmt.Errorf("ID %q is not canonical", value.ID)
	}
	if intrinsic != strings.HasPrefix(identifier.Name(), "kernel.") {
		if intrinsic {
			return errors.New("intrinsic Interface ID must use the reserved kernel.* namespace")
		}
		return errors.New("authored Interface ID must not use the reserved kernel.* namespace")
	}
	if err := module.CheckImportPath(value.PackagePath); err != nil {
		return fmt.Errorf("package path %q is invalid", value.PackagePath)
	}
	if err := module.CheckPath(value.ModulePath); err != nil {
		return fmt.Errorf("module path %q is invalid", value.ModulePath)
	}
	if !validModuleVersion(value.ModulePath, value.ModuleVersion) {
		return fmt.Errorf("module version %q is invalid", value.ModuleVersion)
	}
	if !validSource(value.DirectiveSource) {
		return errors.New("directive source is not bounded stable provenance")
	}
	if value.MetadataSource != "" && !validSource(value.MetadataSource) {
		return errors.New("metadata source is not bounded stable provenance")
	}
	for name, candidate := range map[string]string{
		"shape":         value.ShapeDigest,
		"contract":      value.ContractDigest,
		"documentation": value.DocumentationDigest,
		"example":       value.ExampleDigest,
	} {
		if !validDigest(candidate) {
			return fmt.Errorf("%s digest is invalid", name)
		}
	}
	return nil
}

func validateBinding(value wireBinding) error {
	identifier, err := interfaceid.Parse(value.InterfaceID)
	if err != nil || identifier.String() != value.InterfaceID || strings.HasPrefix(identifier.Name(), "kernel.") {
		return fmt.Errorf("Interface ID %q is not an ordinary canonical Interface", value.InterfaceID)
	}
	if err := validateSources(value.RootSources, "root_sources"); err != nil {
		return err
	}
	if err := validateSources(value.ExposureSources, "exposure_sources"); err != nil {
		return err
	}
	if err := validateConstructorSymbols(value.RequiringConstructors, "requiring_constructors"); err != nil {
		return err
	}
	if err := validateSelection(value.Selection); err != nil {
		return err
	}
	if value.ConfigurationOwner != "" && value.ConfigurationOwner != configurationOwner(value.Selection.Constructor) {
		return errors.New("configuration owner does not identify the selected constructor")
	}
	if err := validatePolicy(value.Policy); err != nil {
		return err
	}
	if err := validateMapping(value.Mappings, true); err != nil {
		return err
	}
	return nil
}

func validateSelection(value wireSelection) error {
	symbol, err := constructorsymbol.Parse(value.Constructor)
	if err != nil || symbol.String() != value.Constructor {
		return fmt.Errorf("selected constructor %q is not canonical", value.Constructor)
	}
	if err := module.CheckPath(value.ModulePath); err != nil || symbol.PackagePath() != value.ModulePath && !strings.HasPrefix(symbol.PackagePath(), value.ModulePath+"/") {
		return errors.New("selected constructor package is outside its owning module")
	}
	if !validModuleVersion(value.ModulePath, value.ModuleVersion) {
		return errors.New("selected constructor module version is invalid")
	}
	if !validSource(value.Source) {
		return errors.New("selected constructor source is not bounded stable provenance")
	}
	if !validConcreteType(value.ConcreteType) {
		return errors.New("selected constructor concrete type is invalid")
	}
	if value.Reason != SelectionExplicit && value.Reason != SelectionUniqueCompatible {
		return fmt.Errorf("selection reason %q is not supported", value.Reason)
	}
	if err := validateSources(value.Sources, "selection sources"); err != nil || len(value.Sources) == 0 {
		return errors.New("selection sources must contain canonical provenance")
	}
	if value.ConstructionOrder <= 0 || value.ConstructionOrder > maximumRecords {
		return errors.New("selection construction order is invalid")
	}
	return nil
}

func validateConstructor(value wireConstructor) error {
	selection := wireSelection{
		Constructor:       value.Symbol,
		ModulePath:        value.ModulePath,
		ModuleVersion:     value.ModuleVersion,
		Source:            value.Source,
		ConcreteType:      value.ConcreteType,
		Reason:            SelectionUniqueCompatible,
		Sources:           []string{value.Source},
		ConstructionOrder: value.ConstructionOrder,
	}
	if err := validateSelection(selection); err != nil {
		return err
	}
	if len(value.Provides) == 0 || len(value.Provides) > maximumRecords {
		return fmt.Errorf("provides must contain between 1 and %d Interface IDs", maximumRecords)
	}
	if err := validateInterfaceIDs(value.Provides, false, "provides"); err != nil {
		return err
	}
	if value.ConfigurationOwner != "" && value.ConfigurationOwner != configurationOwner(value.Symbol) {
		return errors.New("configuration owner does not identify this constructor")
	}
	if err := validateSources(value.ConfigurationSources, "configuration_sources"); err != nil {
		return err
	}
	if value.ConfigurationOwner == "" && len(value.ConfigurationSources) != 0 {
		return errors.New("configuration sources require a configuration owner")
	}
	if value.ConfigurationOwner != "" && len(value.ConfigurationSources) == 0 {
		return errors.New("configuration owner requires at least one source")
	}
	if value.Dependencies == nil || len(value.Dependencies) > maximumRecords {
		return fmt.Errorf("dependencies must be an array with at most %d entries", maximumRecords)
	}
	for index, dependency := range value.Dependencies {
		if index > 0 && value.Dependencies[index-1].ParameterPosition >= dependency.ParameterPosition {
			return errors.New("dependencies must be unique and ordered by parameter position")
		}
		if err := validateDependency(dependency); err != nil {
			return fmt.Errorf("dependencies[%d]: %v", index, err)
		}
	}
	return nil
}

func validateDependency(value wireDependency) error {
	identifier, err := interfaceid.Parse(value.InterfaceID)
	if err != nil || identifier.String() != value.InterfaceID {
		return fmt.Errorf("Interface ID %q is not canonical", value.InterfaceID)
	}
	if err := module.CheckImportPath(value.PackagePath); err != nil {
		return fmt.Errorf("package path %q is invalid", value.PackagePath)
	}
	if value.ParameterName != "" && !token.IsIdentifier(value.ParameterName) {
		return fmt.Errorf("parameter name %q is not a Go identifier", value.ParameterName)
	}
	if value.ParameterPosition <= 0 || value.ParameterPosition > 65535 {
		return errors.New("parameter position is invalid")
	}
	if !value.Optional && !value.Available {
		return errors.New("required dependency must be available")
	}
	if value.Available {
		symbol, err := constructorsymbol.Parse(value.SelectedConstructor)
		if err != nil || symbol.String() != value.SelectedConstructor {
			return errors.New("available dependency selected constructor is not canonical")
		}
	} else if value.SelectedConstructor != "" {
		return errors.New("unavailable optional dependency must not select a constructor")
	}
	return nil
}

func validatePolicy(value wirePolicy) error {
	timeout, err := time.ParseDuration(value.Timeout)
	if err != nil || timeout <= 0 || value.Timeout != timeout.String() {
		return fmt.Errorf("policy timeout %q is not one normalized positive Go duration", value.Timeout)
	}
	if err := validateSources(value.Sources, "policy sources"); err != nil || len(value.Sources) == 0 {
		return errors.New("policy sources must contain canonical provenance")
	}
	return nil
}

func validateMapping(value wireMapping, ordinary bool) error {
	if ordinary {
		if !safeGeneratedPath(value.ProxyPath) || !safeGeneratedPath(value.AdapterPath) || !safeGeneratedPath(value.AssemblyPath) {
			return errors.New("ordinary mapping requires safe proxy, adapter, and assembly paths")
		}
	} else if value.AdapterPath != "" {
		return errors.New("intrinsic mapping must not contain an Implementation adapter")
	}
	transportFields := []string{
		value.ProtobufSchemaPath,
		value.ProtobufDescriptorSetPath,
		value.ProtobufDescriptorDigest,
		value.WireMapPath,
		value.WireMapDigest,
		value.ConnectHandlerPath,
		value.ConnectProcedure,
		value.ConnectProcedureDigest,
		value.HTTPRoute,
	}
	transportSelected := anyNonempty(transportFields)
	if transportSelected {
		if !allNonempty(transportFields) {
			return errors.New("transport mapping must be complete when selected")
		}
		for _, generatedPath := range []string{
			value.ProtobufSchemaPath,
			value.ProtobufDescriptorSetPath,
			value.WireMapPath,
			value.ConnectHandlerPath,
		} {
			if !safeGeneratedPath(generatedPath) {
				return fmt.Errorf("transport path %q is invalid", generatedPath)
			}
		}
		if !validDigest(value.ProtobufDescriptorDigest) || !validDigest(value.WireMapDigest) || !validDigest(value.ConnectProcedureDigest) {
			return errors.New("transport mapping digests are invalid")
		}
		if !validProcedure(value.ConnectProcedure) || value.HTTPRoute != value.ConnectProcedure {
			return errors.New("connect procedure and HTTP route mapping are inconsistent")
		}
	}
	javaScriptFields := []string{
		value.JavaScriptModulePath,
		value.JavaScriptSurfaceDigest,
		value.JavaScriptTypesDigest,
		value.JavaScriptSemanticErrorsDigest,
	}
	javaScriptSelected := anyNonempty(javaScriptFields)
	if javaScriptSelected {
		if !transportSelected || !allNonempty(javaScriptFields) || !safeGeneratedPath(value.JavaScriptModulePath) {
			return errors.New("JavaScript mapping must be complete and backed by a transport")
		}
		if !validDigest(value.JavaScriptSurfaceDigest) || !validDigest(value.JavaScriptTypesDigest) || !validDigest(value.JavaScriptSemanticErrorsDigest) {
			return errors.New("JavaScript mapping digests are invalid")
		}
	}
	return nil
}

func validateIntrinsic(value wireIntrinsic) error {
	if err := validateInterface(value.Interface, true); err != nil {
		return err
	}
	if err := validateSources(value.RequirementSources, "requirement_sources"); err != nil || len(value.RequirementSources) == 0 {
		return errors.New("intrinsic requirement sources must contain canonical provenance")
	}
	if err := validateSources(value.ExposureSources, "exposure_sources"); err != nil {
		return err
	}
	if err := validatePolicy(value.Policy); err != nil {
		return err
	}
	if err := validateMapping(value.Mappings, false); err != nil {
		return err
	}
	if value.Mappings.ProxyPath == "" {
		if value.Mappings.AssemblyPath != "" || anyNonempty([]string{
			value.Mappings.ProtobufSchemaPath,
			value.Mappings.JavaScriptModulePath,
		}) {
			return errors.New("intrinsic generated mappings require a governed proxy")
		}
	} else if !safeGeneratedPath(value.Mappings.ProxyPath) || !safeGeneratedPath(value.Mappings.AssemblyPath) {
		return errors.New("intrinsic proxy and assembly paths are invalid")
	}
	return nil
}

func selectionMatchesConstructor(selection wireSelection, constructor wireConstructor) bool {
	return selection.Constructor == constructor.Symbol &&
		selection.ModulePath == constructor.ModulePath &&
		selection.ModuleVersion == constructor.ModuleVersion &&
		selection.Source == constructor.Source &&
		selection.ConcreteType == constructor.ConcreteType &&
		selection.ConstructionOrder == constructor.ConstructionOrder
}

func validateInterfaceIDs(values []string, intrinsic bool, field string) error {
	for index, value := range values {
		if index > 0 && values[index-1] >= value {
			return fmt.Errorf("%s must be unique and sorted", field)
		}
		identifier, err := interfaceid.Parse(value)
		if err != nil || identifier.String() != value || intrinsic != strings.HasPrefix(identifier.Name(), "kernel.") {
			return fmt.Errorf("%s[%d] %q is not a canonical expected Interface ID", field, index, value)
		}
	}
	return nil
}

func validateConstructorSymbols(values []string, field string) error {
	if values == nil || len(values) > maximumRecords {
		return fmt.Errorf("%s must be an array with at most %d entries", field, maximumRecords)
	}
	for index, value := range values {
		if index > 0 && values[index-1] >= value {
			return fmt.Errorf("%s must be unique and sorted", field)
		}
		symbol, err := constructorsymbol.Parse(value)
		if err != nil || symbol.String() != value {
			return fmt.Errorf("%s[%d] is not a canonical constructor symbol", field, index)
		}
	}
	return nil
}

func validateSources(values []string, field string) error {
	if values == nil || len(values) > maximumSources {
		return fmt.Errorf("%s must be an array with at most %d entries", field, maximumSources)
	}
	for index, value := range values {
		if index > 0 && values[index-1] >= value {
			return fmt.Errorf("%s must be unique and sorted", field)
		}
		if !validSource(value) {
			return fmt.Errorf("%s[%d] is not bounded stable provenance", field, index)
		}
	}
	return nil
}

func validSource(value string) bool {
	return value != "" &&
		len(value) <= maximumStringBytes &&
		utf8.ValidString(value) &&
		!strings.Contains(value, "\\") &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func validModuleVersion(modulePath, version string) bool {
	if version == "local" {
		return true
	}
	return version != "" && len(version) <= 1024 && module.Check(modulePath, version) == nil
}

func validConcreteType(value string) bool {
	return value != "" &&
		len(value) <= maximumStringBytes &&
		utf8.ValidString(value) &&
		strings.HasPrefix(value, "*") &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func safeGeneratedPath(value string) bool {
	return value != "" &&
		len(value) <= 1024 &&
		utf8.ValidString(value) &&
		strings.HasPrefix(value, "generated/") &&
		!path.IsAbs(value) &&
		path.Clean(value) == value &&
		!strings.Contains(value, "\\") &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func validProcedure(value string) bool {
	return value != "" &&
		len(value) <= 2048 &&
		utf8.ValidString(value) &&
		strings.HasPrefix(value, "/") &&
		!strings.Contains(value, "\\") &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func configurationOwner(symbol string) string {
	return "config[" + strconv.Quote(symbol) + "]"
}

func canonicalStrings(values []string) []string {
	result := append(make([]string, 0, len(values)), values...)
	sort.Strings(result)
	write := 0
	for _, value := range result {
		if write != 0 && result[write-1] == value {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}

func anyNonempty(values []string) bool {
	for _, value := range values {
		if value != "" {
			return true
		}
	}
	return false
}

func allNonempty(values []string) bool {
	for _, value := range values {
		if value == "" {
			return false
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
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

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func cloneWireRecord(value wireRecord) wireRecord {
	result := value
	result.Interfaces = cloneSlice(value.Interfaces)
	result.Bindings = make([]wireBinding, len(value.Bindings))
	for index, binding := range value.Bindings {
		result.Bindings[index] = cloneWireBinding(binding)
	}
	result.Constructors = make([]wireConstructor, len(value.Constructors))
	for index, constructor := range value.Constructors {
		result.Constructors[index] = cloneWireConstructor(constructor)
	}
	result.Intrinsics = make([]wireIntrinsic, len(value.Intrinsics))
	for index, intrinsic := range value.Intrinsics {
		result.Intrinsics[index] = cloneWireIntrinsic(intrinsic)
	}
	return result
}

func cloneWireBinding(value wireBinding) wireBinding {
	result := value
	result.RootSources = cloneSlice(value.RootSources)
	result.ExposureSources = cloneSlice(value.ExposureSources)
	result.RequiringConstructors = cloneSlice(value.RequiringConstructors)
	result.Selection = cloneWireSelection(value.Selection)
	result.Policy = cloneWirePolicy(value.Policy)
	return result
}

func cloneWireSelection(value wireSelection) wireSelection {
	result := value
	result.Sources = cloneSlice(value.Sources)
	return result
}

func cloneWireConstructor(value wireConstructor) wireConstructor {
	result := value
	result.Provides = cloneSlice(value.Provides)
	result.ConfigurationSources = cloneSlice(value.ConfigurationSources)
	result.Dependencies = cloneSlice(value.Dependencies)
	return result
}

func cloneWirePolicy(value wirePolicy) wirePolicy {
	result := value
	result.Sources = cloneSlice(value.Sources)
	return result
}

func cloneWireIntrinsic(value wireIntrinsic) wireIntrinsic {
	result := value
	result.RequirementSources = cloneSlice(value.RequirementSources)
	result.ExposureSources = cloneSlice(value.ExposureSources)
	result.Policy = cloneWirePolicy(value.Policy)
	return result
}

func cloneSlice[T any](values []T) []T {
	result := make([]T, len(values))
	copy(result, values)
	return result
}
