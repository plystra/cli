// Package transporttoolchain owns the exact embedded transport-generation
// toolchain identity recorded in generated application manifests.
package transporttoolchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/plystra/cli/internal/connectgen"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
)

const (
	// Schema identifies the strict initial transport-toolchain record.
	Schema = "plystra.transport-toolchain/v1"

	maximumRecordBytes  = 64 << 10
	maximumVersionBytes = 256
)

// Kind classifies one closed transport-toolchain component.
type Kind string

const (
	KindEmbeddedRuntime        Kind = "embedded-runtime"
	KindGenerator              Kind = "generator"
	KindGeneratedGoDependency  Kind = "generated-go-dependency"
	KindGeneratedNPMDependency Kind = "generated-npm-dependency"
	KindGeneratedNPMDev        Kind = "generated-npm-development-dependency"
)

const (
	goFormatComponent             = "go/format"
	protobufModelComponent        = "protobuf-model"
	protobufDescriptorComponent   = "protobuf-descriptor"
	protobufWireMapComponent      = "protobuf-wire-map"
	connectGeneratorComponent     = "connect"
	javaScriptGeneratorComponent  = "javascript"
	typeScriptDependencyComponent = "typescript"
)

// ErrInvalid reports a missing, malformed, noncanonical, or tampered
// transport-toolchain identity.
var ErrInvalid = errors.New("invalid transport toolchain identity")

// ComponentInput is the construction and strict-restoration form of one
// transport-toolchain component.
type ComponentInput struct {
	Kind    Kind
	Name    string
	Version string
}

// Component is one immutable exact component identity.
type Component struct {
	kind    Kind
	name    string
	version string
}

// Kind returns the component classification.
func (c Component) Kind() Kind { return c.kind }

// Name returns the exact embedded generator, runtime, or dependency name.
func (c Component) Name() string { return c.name }

// Version returns the exact bounded version identity.
func (c Component) Version() string { return c.version }

// Identity is one immutable, validated, canonically ordered transport
// toolchain record.
type Identity struct {
	components    []Component
	canonicalJSON []byte
	recordJSON    []byte
	digest        string
	prepared      bool
}

// Schema returns the fixed identity schema.
func (i Identity) Schema() string {
	if !i.prepared {
		return ""
	}
	return Schema
}

// Components returns defensive values in canonical kind and name order.
func (i Identity) Components() []Component {
	return append([]Component(nil), i.components...)
}

// CanonicalJSON returns the defensive digest input containing the schema and
// closed component set. The digest itself is deliberately excluded.
func (i Identity) CanonicalJSON() []byte {
	return append([]byte(nil), i.canonicalJSON...)
}

// RecordJSON returns the defensive strict manifest record, including Digest.
func (i Identity) RecordJSON() []byte {
	return append([]byte(nil), i.recordJSON...)
}

// Digest returns the lowercase SHA-256 digest of CanonicalJSON.
func (i Identity) Digest() string { return i.digest }

// Valid reports whether this value is a complete constructor-produced
// identity whose cached canonical bytes and digest still agree.
func (i Identity) Valid() bool {
	if !i.prepared {
		return false
	}
	inputs := componentInputs(i.components)
	if err := validateComponents(inputs, true); err != nil {
		return false
	}
	canonical, err := encodeCanonical(inputs)
	if err != nil || !bytes.Equal(canonical, i.canonicalJSON) || digest(canonical) != i.digest {
		return false
	}
	record, err := encodeRecord(inputs, i.digest)
	return err == nil && bytes.Equal(record, i.recordJSON)
}

// Current returns the exact toolchain embedded in this CLI process. It
// deliberately records the Go runtime that supplies go/format and never
// inspects a global executable, hosted service, environment selector, VCS
// state, path, or timestamp.
func Current() (Identity, error) {
	return New([]ComponentInput{
		{Kind: KindEmbeddedRuntime, Name: goFormatComponent, Version: runtime.Version()},
		{Kind: KindGenerator, Name: protobufModelComponent, Version: protobufmodel.GeneratorVersion},
		{Kind: KindGenerator, Name: protobufDescriptorComponent, Version: protobufdescriptor.ProjectionSchema},
		{Kind: KindGenerator, Name: protobufWireMapComponent, Version: protobufwiremap.ProjectionSchema},
		{Kind: KindGenerator, Name: connectGeneratorComponent, Version: connectgen.GeneratorVersion},
		{Kind: KindGenerator, Name: javaScriptGeneratorComponent, Version: javascriptgen.GeneratorVersion},
		{Kind: KindGeneratedGoDependency, Name: connectgen.ConnectModulePath, Version: connectgen.ConnectModuleVersion},
		{Kind: KindGeneratedGoDependency, Name: connectgen.ProtobufModulePath, Version: connectgen.ProtobufModuleVersion},
		{Kind: KindGeneratedNPMDependency, Name: javascriptgen.ProtobufPackageName, Version: javascriptgen.ProtobufPackageVersion},
		{Kind: KindGeneratedNPMDependency, Name: javascriptgen.ConnectPackageName, Version: javascriptgen.ConnectPackageVersion},
		{Kind: KindGeneratedNPMDependency, Name: javascriptgen.ConnectWebPackageName, Version: javascriptgen.ConnectWebPackageVersion},
		{Kind: KindGeneratedNPMDev, Name: typeScriptDependencyComponent, Version: javascriptgen.TypeScriptPackageVersion},
	})
}

// New validates the exact closed component set and canonicalizes it
// independently of input order.
func New(inputs []ComponentInput) (Identity, error) {
	ordered := append([]ComponentInput(nil), inputs...)
	sort.Slice(ordered, func(left, right int) bool {
		return compareComponents(ordered[left], ordered[right]) < 0
	})
	if err := validateComponents(ordered, true); err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return build(ordered)
}

// Decode strictly restores one manifest record. Unlike New, Decode rejects
// noncanonical component ordering so generated evidence has one byte form.
func Decode(data []byte) (Identity, error) {
	if len(data) == 0 || len(data) > maximumRecordBytes {
		return Identity{}, fmt.Errorf("%w: record must contain between 1 and %d bytes", ErrInvalid, maximumRecordBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record wireRecord
	if err := decoder.Decode(&record); err != nil {
		return Identity{}, fmt.Errorf("%w: decode record: %v", ErrInvalid, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Identity{}, fmt.Errorf("%w: record contains trailing JSON", ErrInvalid)
	}
	if record.Schema != Schema {
		return Identity{}, fmt.Errorf("%w: schema must equal %q", ErrInvalid, Schema)
	}
	inputs := make([]ComponentInput, len(record.Components))
	for index, component := range record.Components {
		inputs[index] = ComponentInput(component)
	}
	if err := validateComponents(inputs, true); err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	canonical, err := encodeCanonical(inputs)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: encode canonical record: %v", ErrInvalid, err)
	}
	if !validDigest(record.Digest) || record.Digest != digest(canonical) {
		return Identity{}, fmt.Errorf("%w: digest does not match the canonical component record", ErrInvalid)
	}
	return buildWithEncoding(inputs, canonical, record.Digest)
}

type componentKey struct {
	kind Kind
	name string
}

var expectedComponents = []componentKey{
	{kind: KindEmbeddedRuntime, name: goFormatComponent},
	{kind: KindGenerator, name: connectGeneratorComponent},
	{kind: KindGenerator, name: javaScriptGeneratorComponent},
	{kind: KindGenerator, name: protobufDescriptorComponent},
	{kind: KindGenerator, name: protobufModelComponent},
	{kind: KindGenerator, name: protobufWireMapComponent},
	{kind: KindGeneratedGoDependency, name: connectgen.ConnectModulePath},
	{kind: KindGeneratedGoDependency, name: connectgen.ProtobufModulePath},
	{kind: KindGeneratedNPMDependency, name: javascriptgen.ProtobufPackageName},
	{kind: KindGeneratedNPMDependency, name: javascriptgen.ConnectPackageName},
	{kind: KindGeneratedNPMDependency, name: javascriptgen.ConnectWebPackageName},
	{kind: KindGeneratedNPMDev, name: typeScriptDependencyComponent},
}

var expectedComponentSet = func() map[componentKey]struct{} {
	result := make(map[componentKey]struct{}, len(expectedComponents))
	for _, component := range expectedComponents {
		result[component] = struct{}{}
	}
	return result
}()

type wireRecord struct {
	Schema     string          `json:"schema"`
	Components []wireComponent `json:"components"`
	Digest     string          `json:"digest"`
}

type canonicalRecord struct {
	Schema     string          `json:"schema"`
	Components []wireComponent `json:"components"`
}

type wireComponent struct {
	Kind    Kind   `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

func build(inputs []ComponentInput) (Identity, error) {
	canonical, err := encodeCanonical(inputs)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: encode canonical record: %v", ErrInvalid, err)
	}
	return buildWithEncoding(inputs, canonical, digest(canonical))
}

func buildWithEncoding(inputs []ComponentInput, canonical []byte, identityDigest string) (Identity, error) {
	record, err := encodeRecord(inputs, identityDigest)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: encode manifest record: %v", ErrInvalid, err)
	}
	components := make([]Component, len(inputs))
	for index, input := range inputs {
		components[index] = Component{kind: input.Kind, name: input.Name, version: input.Version}
	}
	return Identity{
		components:    components,
		canonicalJSON: append([]byte(nil), canonical...),
		recordJSON:    record,
		digest:        identityDigest,
		prepared:      true,
	}, nil
}

func encodeCanonical(inputs []ComponentInput) ([]byte, error) {
	return json.Marshal(canonicalRecord{
		Schema:     Schema,
		Components: wireComponents(inputs),
	})
}

func encodeRecord(inputs []ComponentInput, identityDigest string) ([]byte, error) {
	return json.Marshal(wireRecord{
		Schema:     Schema,
		Components: wireComponents(inputs),
		Digest:     identityDigest,
	})
}

func wireComponents(inputs []ComponentInput) []wireComponent {
	result := make([]wireComponent, len(inputs))
	for index, input := range inputs {
		result[index] = wireComponent(input)
	}
	return result
}

func componentInputs(components []Component) []ComponentInput {
	result := make([]ComponentInput, len(components))
	for index, component := range components {
		result[index] = ComponentInput{
			Kind:    component.kind,
			Name:    component.name,
			Version: component.version,
		}
	}
	return result
}

func validateComponents(inputs []ComponentInput, requireOrdered bool) error {
	seen := make(map[componentKey]struct{}, len(inputs))
	for index, input := range inputs {
		key := componentKey{kind: input.Kind, name: input.Name}
		if _, known := expectedComponentSet[key]; !known {
			return fmt.Errorf("components[%d] contains unknown component %q/%q", index, input.Kind, input.Name)
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("components[%d] duplicates component %q/%q", index, input.Kind, input.Name)
		}
		if !validVersion(input.Version) {
			return fmt.Errorf("components[%d] version is not a bounded safe identity", index)
		}
		if requireOrdered && index > 0 && compareComponents(inputs[index-1], input) >= 0 {
			return errors.New("components must be unique and canonically ordered by kind and name")
		}
		seen[key] = struct{}{}
	}
	for _, expected := range expectedComponents {
		if _, exists := seen[expected]; !exists {
			return fmt.Errorf("component %q/%q is missing", expected.kind, expected.name)
		}
	}
	if len(inputs) != len(expectedComponents) {
		return fmt.Errorf("components must contain exactly %d entries", len(expectedComponents))
	}
	return nil
}

func compareComponents(left, right ComponentInput) int {
	leftRank := kindRank(left.Kind)
	rightRank := kindRank(right.Kind)
	if leftRank != rightRank {
		return leftRank - rightRank
	}
	return strings.Compare(left.Name, right.Name)
}

func kindRank(kind Kind) int {
	switch kind {
	case KindEmbeddedRuntime:
		return 0
	case KindGenerator:
		return 1
	case KindGeneratedGoDependency:
		return 2
	case KindGeneratedNPMDependency:
		return 3
	case KindGeneratedNPMDev:
		return 4
	default:
		return 5
	}
}

func validVersion(value string) bool {
	if value == "" || len(value) > maximumVersionBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case strings.ContainsRune(".+-_/", character):
		default:
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
