package interfacecompatibility

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
)

const (
	// DocumentationPath is the committed generated Interface-documentation
	// compatibility baseline.
	DocumentationPath = "generated/compatibility/interface-documentation.json"
	// DocumentationSchema identifies the strict generated documentation
	// compatibility baseline.
	DocumentationSchema = "plystra.interface-documentation-baseline/v1"
	// DocumentationProjectionSchema domain-separates one generated
	// documentation artifact projection.
	DocumentationProjectionSchema = "plystra.interface-documentation-projection/v1"
	// DocumentationMaximumBytes bounds a managed documentation baseline before
	// parsing.
	DocumentationMaximumBytes int64 = 4 << 20

	maximumDocumentationArtifacts = 4096
	documentationRoot             = "generated/docs/"
)

// DocumentationKind is one closed generated documentation artifact kind.
type DocumentationKind string

const (
	// DocumentationKindInterfaceReference identifies the generated
	// application-local Interface reference.
	DocumentationKindInterfaceReference DocumentationKind = "interface_reference"
	// DocumentationKindOpenAPI identifies the generated OpenAPI document.
	DocumentationKindOpenAPI DocumentationKind = "openapi"
)

// DocumentationInput supplies one generated documentation artifact. Data is
// hashed immediately and is never retained in the compatibility baseline.
type DocumentationInput struct {
	Path string
	Kind DocumentationKind
	Data []byte
}

// DocumentationBaseline is one immutable path-sorted snapshot of generated
// Interface documentation. It stores only classified digests, never artifact
// bytes.
type DocumentationBaseline struct {
	record        documentationWireRecord
	canonicalJSON []byte
	recordJSON    []byte
	digest        string
	prepared      bool
}

// Schema returns the exact documentation-baseline schema.
func (b DocumentationBaseline) Schema() string {
	if !b.prepared {
		return ""
	}
	return DocumentationSchema
}

// Artifacts returns canonical-path-sorted defensive artifact views.
func (b DocumentationBaseline) Artifacts() []DocumentationArtifact {
	result := make([]DocumentationArtifact, len(b.record.Artifacts))
	for index, value := range b.record.Artifacts {
		result[index] = DocumentationArtifact{record: value}
	}
	return result
}

// CanonicalJSON returns the defensive digest input without the top-level
// digest.
func (b DocumentationBaseline) CanonicalJSON() []byte {
	return append([]byte(nil), b.canonicalJSON...)
}

// RecordJSON returns the defensive strict generated documentation baseline.
func (b DocumentationBaseline) RecordJSON() []byte {
	return append([]byte(nil), b.recordJSON...)
}

// Digest returns the lowercase SHA-256 digest of CanonicalJSON.
func (b DocumentationBaseline) Digest() string { return b.digest }

// Valid reports whether this value is complete and internally canonical.
func (b DocumentationBaseline) Valid() bool {
	if !b.prepared ||
		b.record.Schema != DocumentationSchema ||
		b.record.Digest != b.digest {
		return false
	}
	if err := validateDocumentationArtifacts(b.record.Artifacts, true); err != nil {
		return false
	}
	canonical, err := encodeDocumentationCanonical(b.record.Artifacts)
	if err != nil ||
		!bytes.Equal(canonical, b.canonicalJSON) ||
		digest(canonical) != b.digest {
		return false
	}
	record, err := encodeDocumentationRecord(b.record.Artifacts, b.digest)
	return err == nil && bytes.Equal(record, b.recordJSON)
}

// DocumentationArtifact is one immutable generated documentation artifact
// projection.
type DocumentationArtifact struct {
	record documentationWireArtifact
}

// Path returns the canonical managed generated documentation path.
func (a DocumentationArtifact) Path() string { return a.record.Path }

// Kind returns the closed generated documentation kind.
func (a DocumentationArtifact) Kind() DocumentationKind { return a.record.Kind }

// ContentDigest returns the digest of the exact raw artifact bytes.
func (a DocumentationArtifact) ContentDigest() string { return a.record.ContentDigest }

// ProjectionDigest returns the domain-separated digest of the artifact path,
// kind, and content digest.
func (a DocumentationArtifact) ProjectionDigest() string {
	return a.record.ProjectionDigest
}

// Valid reports whether this artifact view contains one canonical projection.
func (a DocumentationArtifact) Valid() bool {
	return validateDocumentationArtifact(a.record) == nil
}

// NewDocumentation constructs the current generated documentation baseline
// independently of renderer output order.
func NewDocumentation(inputs []DocumentationInput) (DocumentationBaseline, error) {
	if len(inputs) > maximumDocumentationArtifacts {
		return DocumentationBaseline{}, fmt.Errorf(
			"%w: %d documentation inputs exceeds maximum %d",
			ErrInvalid,
			len(inputs),
			maximumDocumentationArtifacts,
		)
	}
	artifacts := make([]documentationWireArtifact, len(inputs))
	for index, input := range inputs {
		contentDigest := digest(input.Data)
		projectionDigest, err := documentationProjectionDigest(
			input.Path,
			input.Kind,
			contentDigest,
		)
		if err != nil {
			return DocumentationBaseline{}, fmt.Errorf(
				"%w: documentation inputs[%d]: %v",
				ErrInvalid,
				index,
				err,
			)
		}
		artifacts[index] = documentationWireArtifact{
			Path:             input.Path,
			Kind:             input.Kind,
			ContentDigest:    contentDigest,
			ProjectionDigest: projectionDigest,
		}
	}
	sort.Slice(artifacts, func(left, right int) bool {
		return artifacts[left].Path < artifacts[right].Path
	})
	if err := validateDocumentationArtifacts(artifacts, true); err != nil {
		return DocumentationBaseline{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return buildDocumentation(artifacts)
}

// DecodeDocumentation strictly restores one canonical generated
// documentation baseline.
func DecodeDocumentation(data []byte) (DocumentationBaseline, error) {
	if len(data) == 0 || int64(len(data)) > DocumentationMaximumBytes {
		return DocumentationBaseline{}, fmt.Errorf(
			"%w: documentation record must contain between 1 and %d bytes",
			ErrHistory,
			DocumentationMaximumBytes,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record documentationWireRecord
	if err := decoder.Decode(&record); err != nil {
		return DocumentationBaseline{}, fmt.Errorf(
			"%w: decode documentation record: %v",
			ErrHistory,
			err,
		)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return DocumentationBaseline{}, fmt.Errorf(
			"%w: documentation record contains trailing JSON",
			ErrHistory,
		)
	}
	if record.Schema != DocumentationSchema {
		return DocumentationBaseline{}, fmt.Errorf(
			"%w: documentation schema must equal %q",
			ErrHistory,
			DocumentationSchema,
		)
	}
	if err := validateDocumentationArtifacts(record.Artifacts, true); err != nil {
		return DocumentationBaseline{}, fmt.Errorf("%w: %v", ErrHistory, err)
	}
	canonical, err := encodeDocumentationCanonical(record.Artifacts)
	if err != nil {
		return DocumentationBaseline{}, fmt.Errorf(
			"%w: encode canonical documentation record: %v",
			ErrHistory,
			err,
		)
	}
	if !validDigest(record.Digest) || record.Digest != digest(canonical) {
		return DocumentationBaseline{}, fmt.Errorf(
			"%w: documentation digest does not match the canonical artifact projections",
			ErrHistory,
		)
	}
	encoded, err := encodeDocumentationRecord(record.Artifacts, record.Digest)
	if err != nil {
		return DocumentationBaseline{}, fmt.Errorf(
			"%w: encode documentation record: %v",
			ErrHistory,
			err,
		)
	}
	if !bytes.Equal(encoded, data) {
		return DocumentationBaseline{}, fmt.Errorf(
			"%w: documentation record is not in canonical byte form",
			ErrHistory,
		)
	}
	return buildDocumentationWithEncoding(
		record.Artifacts,
		canonical,
		encoded,
		record.Digest,
	), nil
}

// ReconcileDocumentation constructs the current documentation baseline and
// compares it with exact prior owned evidence. A missing prior record is the
// valid initial state.
func ReconcileDocumentation(
	inputs []DocumentationInput,
	previous []byte,
	previousExists bool,
) (DocumentationBaseline, DocumentationComparison, error) {
	current, err := NewDocumentation(inputs)
	if err != nil {
		return DocumentationBaseline{}, DocumentationComparison{}, err
	}
	prior, err := NewDocumentation(nil)
	if err != nil {
		return DocumentationBaseline{}, DocumentationComparison{}, err
	}
	if previousExists {
		prior, err = DecodeDocumentation(previous)
		if err != nil {
			return DocumentationBaseline{}, DocumentationComparison{}, err
		}
	} else if len(previous) != 0 {
		return DocumentationBaseline{}, DocumentationComparison{}, fmt.Errorf(
			"%w: absent prior documentation record has bytes",
			ErrHistory,
		)
	}
	comparison, err := CompareDocumentation(prior, current)
	if err != nil {
		return DocumentationBaseline{}, DocumentationComparison{}, err
	}
	return current, comparison, nil
}

// DocumentationClass identifies one generated documentation compatibility
// class.
type DocumentationClass string

const (
	// DocumentationClassKind covers the closed artifact classification.
	DocumentationClassKind DocumentationClass = "kind"
	// DocumentationClassContent covers the exact raw artifact bytes.
	DocumentationClassContent DocumentationClass = "content"
)

// DocumentationChange is one immutable canonical-path documentation
// difference.
type DocumentationChange struct {
	kind     ChangeKind
	path     string
	classes  []DocumentationClass
	previous documentationWireArtifact
	current  documentationWireArtifact
}

// Kind returns added, removed, or changed.
func (c DocumentationChange) Kind() ChangeKind { return c.kind }

// Path returns the canonical generated documentation path.
func (c DocumentationChange) Path() string { return c.path }

// Classes returns changed documentation classes in canonical order.
func (c DocumentationChange) Classes() []DocumentationClass {
	return append([]DocumentationClass(nil), c.classes...)
}

// PreviousArtifact returns the prior immutable artifact when it existed.
func (c DocumentationChange) PreviousArtifact() (DocumentationArtifact, bool) {
	if c.previous.Path == "" {
		return DocumentationArtifact{}, false
	}
	return DocumentationArtifact{record: c.previous}, true
}

// CurrentArtifact returns the current immutable artifact when it exists.
func (c DocumentationChange) CurrentArtifact() (DocumentationArtifact, bool) {
	if c.current.Path == "" {
		return DocumentationArtifact{}, false
	}
	return DocumentationArtifact{record: c.current}, true
}

// PreviousKind returns the prior artifact kind when the path existed.
func (c DocumentationChange) PreviousKind() (DocumentationKind, bool) {
	artifact, exists := c.PreviousArtifact()
	if !exists {
		return "", false
	}
	return artifact.Kind(), true
}

// CurrentKind returns the current artifact kind when the path exists.
func (c DocumentationChange) CurrentKind() (DocumentationKind, bool) {
	artifact, exists := c.CurrentArtifact()
	if !exists {
		return "", false
	}
	return artifact.Kind(), true
}

// PreviousContentDigest returns the prior raw-content digest when the path
// existed.
func (c DocumentationChange) PreviousContentDigest() (string, bool) {
	artifact, exists := c.PreviousArtifact()
	if !exists {
		return "", false
	}
	return artifact.ContentDigest(), true
}

// CurrentContentDigest returns the current raw-content digest when the path
// exists.
func (c DocumentationChange) CurrentContentDigest() (string, bool) {
	artifact, exists := c.CurrentArtifact()
	if !exists {
		return "", false
	}
	return artifact.ContentDigest(), true
}

// DocumentationComparison is one immutable canonical-path-sorted generated
// documentation comparison.
type DocumentationComparison struct {
	previousDigest string
	currentDigest  string
	changes        []DocumentationChange
	prepared       bool
}

// Clean reports whether every generated documentation artifact is unchanged.
func (c DocumentationComparison) Clean() bool {
	return c.prepared && len(c.changes) == 0
}

// PreviousDigest returns the compared prior documentation-baseline digest.
func (c DocumentationComparison) PreviousDigest() string { return c.previousDigest }

// CurrentDigest returns the compared current documentation-baseline digest.
func (c DocumentationComparison) CurrentDigest() string { return c.currentDigest }

// Changes returns defensive differences sorted by canonical artifact path.
func (c DocumentationComparison) Changes() []DocumentationChange {
	result := make([]DocumentationChange, len(c.changes))
	for index, change := range c.changes {
		result[index] = change
		result[index].classes = append([]DocumentationClass(nil), change.classes...)
	}
	return result
}

// Valid reports whether the comparison has complete baseline identities and
// exact documentation-class differences.
func (c DocumentationComparison) Valid() bool {
	if !c.prepared ||
		!validDigest(c.previousDigest) ||
		!validDigest(c.currentDigest) {
		return false
	}
	for index, change := range c.changes {
		if index > 0 && c.changes[index-1].path >= change.path {
			return false
		}
		if !validDocumentationChange(change) {
			return false
		}
	}
	return true
}

// CompareDocumentation returns exact added, removed, and classified changed
// generated documentation artifacts.
func CompareDocumentation(
	previous DocumentationBaseline,
	current DocumentationBaseline,
) (DocumentationComparison, error) {
	if !previous.Valid() || !current.Valid() {
		return DocumentationComparison{}, fmt.Errorf(
			"%w: both compared documentation baselines must be valid",
			ErrInvalid,
		)
	}
	before := make(map[string]documentationWireArtifact, len(previous.record.Artifacts))
	after := make(map[string]documentationWireArtifact, len(current.record.Artifacts))
	paths := make(map[string]struct{}, len(previous.record.Artifacts)+len(current.record.Artifacts))
	for _, value := range previous.record.Artifacts {
		before[value.Path] = value
		paths[value.Path] = struct{}{}
	}
	for _, value := range current.record.Artifacts {
		after[value.Path] = value
		paths[value.Path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for artifactPath := range paths {
		ordered = append(ordered, artifactPath)
	}
	sort.Strings(ordered)

	changes := make([]DocumentationChange, 0)
	for _, artifactPath := range ordered {
		previousValue, previousExists := before[artifactPath]
		currentValue, currentExists := after[artifactPath]
		switch {
		case !previousExists:
			changes = append(changes, DocumentationChange{
				kind:    ChangeAdded,
				path:    artifactPath,
				classes: allDocumentationClasses(),
				current: currentValue,
			})
		case !currentExists:
			changes = append(changes, DocumentationChange{
				kind:     ChangeRemoved,
				path:     artifactPath,
				classes:  allDocumentationClasses(),
				previous: previousValue,
			})
		default:
			classes := changedDocumentationClasses(previousValue, currentValue)
			if len(classes) != 0 {
				changes = append(changes, DocumentationChange{
					kind:     ChangeChanged,
					path:     artifactPath,
					classes:  classes,
					previous: previousValue,
					current:  currentValue,
				})
			}
		}
	}
	result := DocumentationComparison{
		previousDigest: previous.digest,
		currentDigest:  current.digest,
		changes:        changes,
		prepared:       true,
	}
	if !result.Valid() {
		return DocumentationComparison{}, fmt.Errorf(
			"%w: constructed documentation comparison is invalid",
			ErrInvalid,
		)
	}
	return result, nil
}

type documentationWireRecord struct {
	Schema    string                      `json:"schema"`
	Artifacts []documentationWireArtifact `json:"artifacts"`
	Digest    string                      `json:"digest"`
}

type documentationCanonicalRecord struct {
	Schema    string                      `json:"schema"`
	Artifacts []documentationWireArtifact `json:"artifacts"`
}

type documentationWireArtifact struct {
	Path             string            `json:"path"`
	Kind             DocumentationKind `json:"kind"`
	ContentDigest    string            `json:"content_digest"`
	ProjectionDigest string            `json:"projection_digest"`
}

type documentationProjection struct {
	Schema        string            `json:"schema"`
	Path          string            `json:"path"`
	Kind          DocumentationKind `json:"kind"`
	ContentDigest string            `json:"content_digest"`
}

func buildDocumentation(
	artifacts []documentationWireArtifact,
) (DocumentationBaseline, error) {
	if artifacts == nil {
		artifacts = []documentationWireArtifact{}
	}
	canonical, err := encodeDocumentationCanonical(artifacts)
	if err != nil {
		return DocumentationBaseline{}, fmt.Errorf(
			"%w: encode canonical documentation record: %v",
			ErrInvalid,
			err,
		)
	}
	identityDigest := digest(canonical)
	record, err := encodeDocumentationRecord(artifacts, identityDigest)
	if err != nil {
		return DocumentationBaseline{}, fmt.Errorf(
			"%w: encode documentation record: %v",
			ErrInvalid,
			err,
		)
	}
	if int64(len(record)) > DocumentationMaximumBytes {
		return DocumentationBaseline{}, fmt.Errorf(
			"%w: encoded documentation record exceeds %d bytes",
			ErrInvalid,
			DocumentationMaximumBytes,
		)
	}
	return buildDocumentationWithEncoding(
		artifacts,
		canonical,
		record,
		identityDigest,
	), nil
}

func buildDocumentationWithEncoding(
	artifacts []documentationWireArtifact,
	canonical []byte,
	record []byte,
	identityDigest string,
) DocumentationBaseline {
	clonedArtifacts := append([]documentationWireArtifact(nil), artifacts...)
	if clonedArtifacts == nil {
		clonedArtifacts = []documentationWireArtifact{}
	}
	return DocumentationBaseline{
		record: documentationWireRecord{
			Schema:    DocumentationSchema,
			Artifacts: clonedArtifacts,
			Digest:    identityDigest,
		},
		canonicalJSON: append([]byte(nil), canonical...),
		recordJSON:    append([]byte(nil), record...),
		digest:        identityDigest,
		prepared:      true,
	}
}

func encodeDocumentationCanonical(
	artifacts []documentationWireArtifact,
) ([]byte, error) {
	return json.Marshal(documentationCanonicalRecord{
		Schema:    DocumentationSchema,
		Artifacts: artifacts,
	})
}

func encodeDocumentationRecord(
	artifacts []documentationWireArtifact,
	identityDigest string,
) ([]byte, error) {
	return json.Marshal(documentationWireRecord{
		Schema:    DocumentationSchema,
		Artifacts: artifacts,
		Digest:    identityDigest,
	})
}

func validateDocumentationArtifacts(
	values []documentationWireArtifact,
	requireOrdered bool,
) error {
	if values == nil || len(values) > maximumDocumentationArtifacts {
		return fmt.Errorf(
			"documentation artifacts must be an array with at most %d entries",
			maximumDocumentationArtifacts,
		)
	}
	for index, value := range values {
		if requireOrdered && index > 0 && values[index-1].Path >= value.Path {
			return errors.New(
				"documentation artifacts must be unique and sorted by canonical path",
			)
		}
		if err := validateDocumentationArtifact(value); err != nil {
			return fmt.Errorf("documentation artifacts[%d]: %v", index, err)
		}
	}
	return nil
}

func validateDocumentationArtifact(value documentationWireArtifact) error {
	if !validDocumentationPath(value.Path) {
		return fmt.Errorf(
			"path %q is not a canonical managed generated documentation path",
			value.Path,
		)
	}
	if !validDocumentationKind(value.Kind) {
		return fmt.Errorf("kind %q is unknown", value.Kind)
	}
	if !validDigest(value.ContentDigest) {
		return errors.New("content digest is invalid")
	}
	expected, err := documentationProjectionDigest(
		value.Path,
		value.Kind,
		value.ContentDigest,
	)
	if err != nil {
		return fmt.Errorf("projection: %v", err)
	}
	if !validDigest(value.ProjectionDigest) || value.ProjectionDigest != expected {
		return errors.New("projection digest does not match path, kind, and content digest")
	}
	return nil
}

func validDocumentationPath(value string) bool {
	return fs.ValidPath(value) &&
		!strings.ContainsRune(value, '\\') &&
		strings.HasPrefix(value, documentationRoot) &&
		len(value) > len(documentationRoot)
}

func validDocumentationKind(value DocumentationKind) bool {
	switch value {
	case DocumentationKindInterfaceReference, DocumentationKindOpenAPI:
		return true
	default:
		return false
	}
}

func documentationProjectionDigest(
	artifactPath string,
	kind DocumentationKind,
	contentDigest string,
) (string, error) {
	if !validDocumentationPath(artifactPath) {
		return "", fmt.Errorf(
			"path %q is not a canonical managed generated documentation path",
			artifactPath,
		)
	}
	if !validDocumentationKind(kind) {
		return "", fmt.Errorf("kind %q is unknown", kind)
	}
	if !validDigest(contentDigest) {
		return "", errors.New("content digest is invalid")
	}
	encoded, err := json.Marshal(documentationProjection{
		Schema:        DocumentationProjectionSchema,
		Path:          artifactPath,
		Kind:          kind,
		ContentDigest: contentDigest,
	})
	if err != nil {
		return "", err
	}
	return digest(encoded), nil
}

func allDocumentationClasses() []DocumentationClass {
	return []DocumentationClass{
		DocumentationClassKind,
		DocumentationClassContent,
	}
}

func changedDocumentationClasses(
	previous documentationWireArtifact,
	current documentationWireArtifact,
) []DocumentationClass {
	classes := make([]DocumentationClass, 0, 2)
	if previous.Kind != current.Kind {
		classes = append(classes, DocumentationClassKind)
	}
	if previous.ContentDigest != current.ContentDigest {
		classes = append(classes, DocumentationClassContent)
	}
	return classes
}

func validDocumentationChange(change DocumentationChange) bool {
	if !validDocumentationPath(change.path) ||
		!equalDocumentationClasses(
			change.classes,
			expectedDocumentationClasses(change),
		) {
		return false
	}
	switch change.kind {
	case ChangeAdded:
		return change.previous.Path == "" &&
			change.current.Path == change.path &&
			validateDocumentationArtifact(change.current) == nil
	case ChangeRemoved:
		return change.previous.Path == change.path &&
			validateDocumentationArtifact(change.previous) == nil &&
			change.current.Path == ""
	case ChangeChanged:
		return change.previous.Path == change.path &&
			change.current.Path == change.path &&
			validateDocumentationArtifact(change.previous) == nil &&
			validateDocumentationArtifact(change.current) == nil &&
			len(change.classes) != 0
	default:
		return false
	}
}

func expectedDocumentationClasses(
	change DocumentationChange,
) []DocumentationClass {
	switch change.kind {
	case ChangeAdded, ChangeRemoved:
		return allDocumentationClasses()
	case ChangeChanged:
		return changedDocumentationClasses(change.previous, change.current)
	default:
		return nil
	}
}

func equalDocumentationClasses(
	left []DocumentationClass,
	right []DocumentationClass,
) bool {
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
