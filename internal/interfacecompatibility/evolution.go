package interfacecompatibility

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/interfaceid"
)

const (
	// EvolutionSchema identifies the deterministic aggregate Interface
	// compatibility assessment consumed by stable release policy.
	EvolutionSchema = "plystra.interface-evolution/v1"
)

var (
	// ErrEvolution reports incomplete or inconsistent compatibility
	// comparisons supplied for aggregate assessment.
	ErrEvolution = errors.New("assess Interface evolution")
	// ErrStableVersionRequired reports a breaking change to an existing
	// Interface identity that stable release policy cannot accept in place.
	ErrStableVersionRequired = errors.New("stable Interface change requires a new /vN")
)

// VersionSurface identifies one exact compatibility surface that requires an
// existing stable Interface identity to remain unchanged.
type VersionSurface string

const (
	VersionSurfaceGoShape                  VersionSurface = "go_shape"
	VersionSurfaceContract                 VersionSurface = "contract"
	VersionSurfaceProtobufDescriptor       VersionSurface = "protobuf_descriptor"
	VersionSurfaceConnectProcedure         VersionSurface = "connect_procedure"
	VersionSurfaceWireMap                  VersionSurface = "wire_map"
	VersionSurfaceJavaScriptSurface        VersionSurface = "javascript_surface"
	VersionSurfaceJavaScriptTypes          VersionSurface = "javascript_types"
	VersionSurfaceJavaScriptSemanticErrors VersionSurface = "javascript_semantic_errors"
)

var versionSurfaceOrder = []VersionSurface{
	VersionSurfaceGoShape,
	VersionSurfaceContract,
	VersionSurfaceProtobufDescriptor,
	VersionSurfaceConnectProcedure,
	VersionSurfaceWireMap,
	VersionSurfaceJavaScriptSurface,
	VersionSurfaceJavaScriptTypes,
	VersionSurfaceJavaScriptSemanticErrors,
}

// EvolutionInput is the complete set of current comparison classes required
// to assess whether stable Interface versioning is affected.
type EvolutionInput struct {
	Shape         Comparison
	Metadata      MetadataComparison
	Transport     TransportComparison
	JavaScript    JavaScriptComparison
	Documentation DocumentationComparison
}

// VersionChange is one immutable before-and-after compatibility-class change.
type VersionChange struct {
	record evolutionVersionChange
}

// Surface returns the exact affected compatibility surface.
func (c VersionChange) Surface() VersionSurface { return c.record.Surface }

// Kind returns changed or removed.
func (c VersionChange) Kind() ChangeKind { return c.record.Kind }

// PreviousDigest returns the exact prior class digest.
func (c VersionChange) PreviousDigest() string { return c.record.PreviousDigest }

// CurrentDigest returns the exact current class digest when the Interface or
// projection remains present.
func (c VersionChange) CurrentDigest() (string, bool) {
	return c.record.CurrentDigest, c.record.CurrentDigest != ""
}

// VersionRequirement groups every breaking compatibility-class change for one
// exact existing Interface ID.
type VersionRequirement struct {
	record evolutionVersionRequirement
}

// ID returns the exact existing Interface ID that must remain compatible.
func (r VersionRequirement) ID() string { return r.record.ID }

// Changes returns defensive surface-ordered evidence.
func (r VersionRequirement) Changes() []VersionChange {
	result := make([]VersionChange, len(r.record.Changes))
	for index, change := range r.record.Changes {
		result[index] = VersionChange{record: change}
	}
	return result
}

// EvolutionAssessment is one immutable exact-ID-sorted aggregate assessment.
// It classifies version requirements but does not decide whether the current
// operation is prerelease or stable.
type EvolutionAssessment struct {
	record        evolutionRecord
	canonicalJSON []byte
	digest        string
	prepared      bool
}

// CanonicalJSON returns defensive non-secret compatibility evidence.
func (a EvolutionAssessment) CanonicalJSON() []byte {
	return append([]byte(nil), a.canonicalJSON...)
}

// Digest returns the lowercase SHA-256 identity of CanonicalJSON.
func (a EvolutionAssessment) Digest() string { return a.digest }

// Requirements returns defensive exact-ID-sorted version requirements.
func (a EvolutionAssessment) Requirements() []VersionRequirement {
	result := make([]VersionRequirement, len(a.record.Requirements))
	for index, requirement := range a.record.Requirements {
		result[index] = VersionRequirement{record: cloneEvolutionRequirement(requirement)}
	}
	return result
}

// RequiresNewVersion reports whether stable release policy must reject an
// in-place change to at least one existing Interface ID.
func (a EvolutionAssessment) RequiresNewVersion() bool {
	return a.Valid() && len(a.record.Requirements) != 0
}

// SharedJavaScriptChanged reports package-level JavaScript public API drift
// that is release compatibility evidence but cannot by itself identify one
// Interface that needs a new version.
func (a EvolutionAssessment) SharedJavaScriptChanged() bool {
	return a.Valid() && a.record.Comparisons.JavaScript.SharedPackageChanged
}

// Valid reports whether AssessEvolution produced complete canonical evidence.
func (a EvolutionAssessment) Valid() bool {
	if !a.prepared ||
		!validDigest(a.digest) ||
		validateEvolutionRecord(a.record) != nil {
		return false
	}
	canonical, err := json.Marshal(a.record)
	return err == nil &&
		bytes.Equal(canonical, a.canonicalJSON) &&
		digest(canonical) == a.digest
}

// ValidateStableVersioning rejects every assessed in-place breaking change.
// Prerelease workflows deliberately do not call this policy boundary.
func (a EvolutionAssessment) ValidateStableVersioning() error {
	if !a.Valid() {
		return fmt.Errorf("%w: assessment is absent or invalid", ErrEvolution)
	}
	if len(a.record.Requirements) == 0 {
		return nil
	}
	return &StableVersionError{requirements: cloneEvolutionRequirements(a.record.Requirements)}
}

// StableVersionError preserves deterministic version-requirement evidence for
// a stable release diagnostic.
type StableVersionError struct {
	requirements []evolutionVersionRequirement
}

// Error returns one bounded actionable stable-version diagnostic.
func (e *StableVersionError) Error() string {
	if e == nil || len(e.requirements) == 0 {
		return ErrStableVersionRequired.Error()
	}
	details := make([]string, len(e.requirements))
	for index, requirement := range e.requirements {
		changes := make([]string, len(requirement.Changes))
		for changeIndex, change := range requirement.Changes {
			changes[changeIndex] = string(change.Surface) + " " + string(change.Kind)
		}
		details[index] = requirement.ID + " (" + strings.Join(changes, ", ") + ")"
	}
	return ErrStableVersionRequired.Error() +
		": " + strings.Join(details, "; ") +
		". Keep each released Interface ID compatible with its baseline and define the breaking contract under a new higher /vN"
}

// Unwrap exposes the stable version requirement category.
func (*StableVersionError) Unwrap() error { return ErrStableVersionRequired }

// Requirements returns defensive exact-ID-sorted error evidence.
func (e *StableVersionError) Requirements() []VersionRequirement {
	if e == nil {
		return nil
	}
	result := make([]VersionRequirement, len(e.requirements))
	for index, requirement := range e.requirements {
		result[index] = VersionRequirement{record: cloneEvolutionRequirement(requirement)}
	}
	return result
}

// AssessEvolution combines every compatibility comparison without assigning
// release state. Additions and presentation-only changes remain version
// neutral. Changed source or generated public projections require a new stable
// Interface version; a removed transport or JavaScript projection does so only
// when the canonical Interface itself was also removed.
func AssessEvolution(input EvolutionInput) (EvolutionAssessment, error) {
	if !input.Shape.Valid() ||
		!input.Metadata.Valid() ||
		!input.Transport.Valid() ||
		!input.JavaScript.Valid() ||
		!input.Documentation.Valid() {
		return EvolutionAssessment{}, fmt.Errorf(
			"%w: every comparison class must be present and valid",
			ErrEvolution,
		)
	}

	changesByID := make(map[string]map[VersionSurface]evolutionVersionChange)
	removedInterfaces := make(map[string]struct{})
	add := func(
		identifier string,
		surface VersionSurface,
		kind ChangeKind,
		previousDigest string,
		currentDigest string,
	) error {
		change := evolutionVersionChange{
			Surface:        surface,
			Kind:           kind,
			PreviousDigest: previousDigest,
			CurrentDigest:  currentDigest,
		}
		if err := validateEvolutionVersionChange(change); err != nil {
			return fmt.Errorf("%w: Interface %s: %v", ErrEvolution, identifier, err)
		}
		bySurface := changesByID[identifier]
		if bySurface == nil {
			bySurface = make(map[VersionSurface]evolutionVersionChange)
			changesByID[identifier] = bySurface
		}
		if previous, duplicate := bySurface[surface]; duplicate && previous != change {
			return fmt.Errorf(
				"%w: Interface %s has conflicting %s evidence",
				ErrEvolution,
				identifier,
				surface,
			)
		}
		bySurface[surface] = change
		return nil
	}

	for _, change := range input.Shape.Changes() {
		if change.Kind() == ChangeAdded {
			continue
		}
		if change.Kind() == ChangeRemoved {
			removedInterfaces[change.ID()] = struct{}{}
		}
		if err := add(
			change.ID(),
			VersionSurfaceGoShape,
			change.Kind(),
			change.PreviousDigest(),
			change.CurrentDigest(),
		); err != nil {
			return EvolutionAssessment{}, err
		}
	}
	for _, change := range input.Metadata.Changes() {
		if change.Kind() == ChangeAdded ||
			!containsMetadataClass(change.Classes(), MetadataClassContract) {
			continue
		}
		previousDigest, _ := change.PreviousDigest(MetadataClassContract)
		currentDigest, _ := change.CurrentDigest(MetadataClassContract)
		if err := add(
			change.ID(),
			VersionSurfaceContract,
			change.Kind(),
			previousDigest,
			currentDigest,
		); err != nil {
			return EvolutionAssessment{}, err
		}
	}
	for _, change := range input.Transport.Changes() {
		_, interfaceRemoved := removedInterfaces[change.ID()]
		if change.Kind() == ChangeAdded ||
			change.Kind() == ChangeRemoved && !interfaceRemoved {
			continue
		}
		for _, class := range change.Classes() {
			previousDigest, _ := change.PreviousDigest(class)
			currentDigest, _ := change.CurrentDigest(class)
			if err := add(
				change.ID(),
				versionSurfaceForTransport(class),
				change.Kind(),
				previousDigest,
				currentDigest,
			); err != nil {
				return EvolutionAssessment{}, err
			}
		}
	}
	for _, change := range input.JavaScript.Changes() {
		_, interfaceRemoved := removedInterfaces[change.ID()]
		if change.Kind() == ChangeAdded ||
			change.Kind() == ChangeRemoved && !interfaceRemoved {
			continue
		}
		for _, class := range change.Classes() {
			previousDigest, _ := change.PreviousDigest(class)
			currentDigest, _ := change.CurrentDigest(class)
			if err := add(
				change.ID(),
				versionSurfaceForJavaScript(class),
				change.Kind(),
				previousDigest,
				currentDigest,
			); err != nil {
				return EvolutionAssessment{}, err
			}
		}
	}

	identifiers := make([]string, 0, len(changesByID))
	for identifier := range changesByID {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	requirements := make([]evolutionVersionRequirement, len(identifiers))
	for index, identifier := range identifiers {
		changes := make([]evolutionVersionChange, 0, len(changesByID[identifier]))
		for _, surface := range versionSurfaceOrder {
			if change, exists := changesByID[identifier][surface]; exists {
				changes = append(changes, change)
			}
		}
		requirements[index] = evolutionVersionRequirement{
			ID:      identifier,
			Changes: changes,
		}
	}

	record := evolutionRecord{
		Schema: EvolutionSchema,
		Comparisons: evolutionComparisons{
			Shape: evolutionDigestPair{
				Previous: input.Shape.PreviousDigest(),
				Current:  input.Shape.CurrentDigest(),
			},
			Metadata: evolutionDigestPair{
				Previous: input.Metadata.PreviousDigest(),
				Current:  input.Metadata.CurrentDigest(),
			},
			Transport: evolutionDigestPair{
				Previous: input.Transport.PreviousDigest(),
				Current:  input.Transport.CurrentDigest(),
			},
			JavaScript: evolutionJavaScriptComparison{
				Previous:             input.JavaScript.PreviousDigest(),
				Current:              input.JavaScript.CurrentDigest(),
				PreviousPackage:      input.JavaScript.PreviousPackageDigest(),
				CurrentPackage:       input.JavaScript.CurrentPackageDigest(),
				SharedPackageChanged: input.JavaScript.PackageChanged(),
			},
			Documentation: evolutionDigestPair{
				Previous: input.Documentation.PreviousDigest(),
				Current:  input.Documentation.CurrentDigest(),
			},
		},
		Requirements: requirements,
	}
	if err := validateEvolutionRecord(record); err != nil {
		return EvolutionAssessment{}, fmt.Errorf("%w: %v", ErrEvolution, err)
	}
	canonical, err := json.Marshal(record)
	if err != nil {
		return EvolutionAssessment{}, fmt.Errorf("%w: encode canonical assessment: %v", ErrEvolution, err)
	}
	return EvolutionAssessment{
		record:        record,
		canonicalJSON: canonical,
		digest:        digest(canonical),
		prepared:      true,
	}, nil
}

type evolutionRecord struct {
	Schema       string                        `json:"schema"`
	Comparisons  evolutionComparisons          `json:"comparisons"`
	Requirements []evolutionVersionRequirement `json:"requirements"`
}

type evolutionComparisons struct {
	Shape         evolutionDigestPair           `json:"shape"`
	Metadata      evolutionDigestPair           `json:"metadata"`
	Transport     evolutionDigestPair           `json:"transport"`
	JavaScript    evolutionJavaScriptComparison `json:"javascript"`
	Documentation evolutionDigestPair           `json:"documentation"`
}

type evolutionDigestPair struct {
	Previous string `json:"previous_digest"`
	Current  string `json:"current_digest"`
}

type evolutionJavaScriptComparison struct {
	Previous             string `json:"previous_digest"`
	Current              string `json:"current_digest"`
	PreviousPackage      string `json:"previous_package_digest"`
	CurrentPackage       string `json:"current_package_digest"`
	SharedPackageChanged bool   `json:"shared_package_changed"`
}

type evolutionVersionRequirement struct {
	ID      string                   `json:"id"`
	Changes []evolutionVersionChange `json:"changes"`
}

type evolutionVersionChange struct {
	Surface        VersionSurface `json:"surface"`
	Kind           ChangeKind     `json:"kind"`
	PreviousDigest string         `json:"previous_digest"`
	CurrentDigest  string         `json:"current_digest,omitempty"`
}

func validateEvolutionRecord(record evolutionRecord) error {
	if record.Schema != EvolutionSchema {
		return fmt.Errorf("schema must equal %q", EvolutionSchema)
	}
	comparisonPairs := []struct {
		name string
		pair evolutionDigestPair
	}{
		{name: "shape", pair: record.Comparisons.Shape},
		{name: "metadata", pair: record.Comparisons.Metadata},
		{name: "transport", pair: record.Comparisons.Transport},
		{name: "documentation", pair: record.Comparisons.Documentation},
	}
	for _, comparison := range comparisonPairs {
		if !validDigest(comparison.pair.Previous) ||
			!validDigest(comparison.pair.Current) {
			return fmt.Errorf("%s comparison digests are invalid", comparison.name)
		}
	}
	javaScript := record.Comparisons.JavaScript
	if !validDigest(javaScript.Previous) ||
		!validDigest(javaScript.Current) ||
		!validDigest(javaScript.PreviousPackage) ||
		!validDigest(javaScript.CurrentPackage) ||
		javaScript.SharedPackageChanged != (javaScript.PreviousPackage != javaScript.CurrentPackage) {
		return errors.New("invalid JavaScript comparison identity")
	}
	if record.Requirements == nil {
		return errors.New("requirements must be an array")
	}
	if len(record.Requirements) > maximumInterfaces {
		return fmt.Errorf(
			"requirements must contain at most %d entries",
			maximumInterfaces,
		)
	}
	for index, requirement := range record.Requirements {
		if index > 0 && record.Requirements[index-1].ID >= requirement.ID {
			return errors.New("requirements must be unique and sorted by exact Interface ID")
		}
		identifier, err := interfaceid.Parse(requirement.ID)
		if err != nil || identifier.String() != requirement.ID {
			return fmt.Errorf("requirements[%d].id %q is not canonical", index, requirement.ID)
		}
		if len(requirement.Changes) == 0 ||
			len(requirement.Changes) > len(versionSurfaceOrder) {
			return fmt.Errorf(
				"requirements[%d].changes must contain between 1 and %d entries",
				index,
				len(versionSurfaceOrder),
			)
		}
		previousOrder := -1
		for changeIndex, change := range requirement.Changes {
			order := versionSurfaceIndex(change.Surface)
			if order < 0 || order <= previousOrder {
				return fmt.Errorf(
					"requirements[%d].changes must be unique and surface ordered",
					index,
				)
			}
			previousOrder = order
			if err := validateEvolutionVersionChange(change); err != nil {
				return fmt.Errorf(
					"requirements[%d].changes[%d]: %v",
					index,
					changeIndex,
					err,
				)
			}
		}
	}
	return nil
}

func validateEvolutionVersionChange(change evolutionVersionChange) error {
	if versionSurfaceIndex(change.Surface) < 0 {
		return fmt.Errorf("surface %q is unknown", change.Surface)
	}
	if !validDigest(change.PreviousDigest) {
		return errors.New("previous digest is invalid")
	}
	switch change.Kind {
	case ChangeChanged:
		if !validDigest(change.CurrentDigest) ||
			change.CurrentDigest == change.PreviousDigest {
			return errors.New("changed evidence requires different valid digests")
		}
	case ChangeRemoved:
		if change.CurrentDigest != "" {
			return errors.New("removed evidence cannot have a current digest")
		}
	default:
		return fmt.Errorf("kind %q is not a version requirement", change.Kind)
	}
	return nil
}

func versionSurfaceIndex(value VersionSurface) int {
	for index, surface := range versionSurfaceOrder {
		if value == surface {
			return index
		}
	}
	return -1
}

func versionSurfaceForTransport(class TransportClass) VersionSurface {
	switch class {
	case TransportClassDescriptor:
		return VersionSurfaceProtobufDescriptor
	case TransportClassProcedure:
		return VersionSurfaceConnectProcedure
	case TransportClassWireMap:
		return VersionSurfaceWireMap
	default:
		return ""
	}
}

func versionSurfaceForJavaScript(class JavaScriptClass) VersionSurface {
	switch class {
	case JavaScriptClassSurface:
		return VersionSurfaceJavaScriptSurface
	case JavaScriptClassTypes:
		return VersionSurfaceJavaScriptTypes
	case JavaScriptClassSemanticErrors:
		return VersionSurfaceJavaScriptSemanticErrors
	default:
		return ""
	}
}

func containsMetadataClass(values []MetadataClass, expected MetadataClass) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func cloneEvolutionRequirement(value evolutionVersionRequirement) evolutionVersionRequirement {
	result := value
	result.Changes = append([]evolutionVersionChange(nil), value.Changes...)
	return result
}

func cloneEvolutionRequirements(values []evolutionVersionRequirement) []evolutionVersionRequirement {
	result := make([]evolutionVersionRequirement, len(values))
	for index, value := range values {
		result[index] = cloneEvolutionRequirement(value)
	}
	return result
}
