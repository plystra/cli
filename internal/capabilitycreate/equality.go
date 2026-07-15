package capabilitycreate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
)

// ErrSchemaConflict reports semantically different declarations for one exact
// capability version.
var ErrSchemaConflict = errors.New("capability schema conflict")

const maximumDifferenceValueRunes = 256

// SchemaDifference is one deterministic structural difference between two
// canonical capability contracts.
type SchemaDifference struct {
	path        string
	baseline    string
	conflicting string
}

// Path returns the dot-separated contract path that differs.
func (d SchemaDifference) Path() string { return d.path }

// Baseline returns the canonical JSON value carried by the first provider, or
// <missing> when the path is absent.
func (d SchemaDifference) Baseline() string { return d.baseline }

// Conflicting returns the canonical JSON value carried by the conflicting
// provider, or <missing> when the path is absent.
func (d SchemaDifference) Conflicting() string { return d.conflicting }

// SchemaConflictError identifies two providers carrying meaningfully different
// contracts for the same exact capability version.
type SchemaConflictError struct {
	capability          capabilityid.Identifier
	baselineProvider    Provider
	baselineSourcePath  string
	conflictingProvider Provider
	conflictingPath     string
	differences         []SchemaDifference
}

// Capability returns the exact conflicting capability ID.
func (e *SchemaConflictError) Capability() capabilityid.Identifier { return e.capability }

// BaselineProvider returns the deterministic first provider used for comparison.
func (e *SchemaConflictError) BaselineProvider() Provider { return e.baselineProvider }

// BaselineSourcePath returns the first provider's absolute capability.yaml path.
func (e *SchemaConflictError) BaselineSourcePath() string { return e.baselineSourcePath }

// ConflictingProvider returns the provider carrying the different schema.
func (e *SchemaConflictError) ConflictingProvider() Provider { return e.conflictingProvider }

// ConflictingSourcePath returns the conflicting provider's absolute capability.yaml path.
func (e *SchemaConflictError) ConflictingSourcePath() string { return e.conflictingPath }

// Differences returns a defensive copy in deterministic contract-path order.
func (e *SchemaConflictError) Differences() []SchemaDifference {
	return append([]SchemaDifference(nil), e.differences...)
}

func (e *SchemaConflictError) Error() string {
	if e == nil {
		return ErrSchemaConflict.Error()
	}
	var message strings.Builder
	fmt.Fprintf(
		&message,
		"%s: %s differs between %s at %s and %s at %s",
		ErrSchemaConflict,
		e.capability,
		e.baselineProvider.PluginID(),
		e.baselineSourcePath,
		e.conflictingProvider.PluginID(),
		e.conflictingPath,
	)
	const maximumReportedDifferences = 8
	reported := min(len(e.differences), maximumReportedDifferences)
	for _, difference := range e.differences[:reported] {
		fmt.Fprintf(&message, "; %s: %s != %s", difference.path, difference.baseline, difference.conflicting)
	}
	if remaining := len(e.differences) - reported; remaining != 0 {
		fmt.Fprintf(&message, "; %d more difference(s)", remaining)
	}
	fmt.Fprintf(
		&message,
		"; correction: make every provider of %s carry one semantically identical capability.yaml, or assign meaningful changes a new capability version",
		e.capability,
	)
	return message.String()
}

// Unwrap supports errors.Is with ErrSchemaConflict.
func (e *SchemaConflictError) Unwrap() error { return ErrSchemaConflict }

func newSchemaConflict(baseline ResolvedSource, baselineSchema []byte, conflicting ResolvedSource, conflictingSchema []byte) (*SchemaConflictError, error) {
	differences, err := schemaDifferences(baselineSchema, conflictingSchema)
	if err != nil {
		return nil, err
	}
	return &SchemaConflictError{
		capability:          baseline.Source().ID(),
		baselineProvider:    baseline.Provider(),
		baselineSourcePath:  baseline.Source().Path(),
		conflictingProvider: conflicting.Provider(),
		conflictingPath:     conflicting.Source().Path(),
		differences:         differences,
	}, nil
}

func schemaDifferences(baseline, conflicting []byte) ([]SchemaDifference, error) {
	left, err := decodeCanonicalSchema(baseline)
	if err != nil {
		return nil, fmt.Errorf("decode baseline canonical schema: %w", err)
	}
	right, err := decodeCanonicalSchema(conflicting)
	if err != nil {
		return nil, fmt.Errorf("decode conflicting canonical schema: %w", err)
	}
	differences := make([]SchemaDifference, 0)
	diffSchemaValue("", left, true, right, true, &differences)
	return differences, nil
}

func decodeCanonicalSchema(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func diffSchemaValue(path string, baseline any, hasBaseline bool, conflicting any, hasConflicting bool, differences *[]SchemaDifference) {
	if !hasBaseline || !hasConflicting {
		*differences = append(*differences, schemaDifference(path, baseline, hasBaseline, conflicting, hasConflicting))
		return
	}
	baselineMap, baselineIsMap := baseline.(map[string]any)
	conflictingMap, conflictingIsMap := conflicting.(map[string]any)
	if baselineIsMap && conflictingIsMap {
		keys := make(map[string]struct{}, len(baselineMap)+len(conflictingMap))
		for key := range baselineMap {
			keys[key] = struct{}{}
		}
		for key := range conflictingMap {
			keys[key] = struct{}{}
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			left, leftExists := baselineMap[key]
			right, rightExists := conflictingMap[key]
			diffSchemaValue(joinSchemaPath(path, key), left, leftExists, right, rightExists, differences)
		}
		return
	}
	if reflect.DeepEqual(baseline, conflicting) {
		return
	}
	*differences = append(*differences, schemaDifference(path, baseline, true, conflicting, true))
}

func schemaDifference(path string, baseline any, hasBaseline bool, conflicting any, hasConflicting bool) SchemaDifference {
	return SchemaDifference{
		path:        path,
		baseline:    renderSchemaValue(baseline, hasBaseline),
		conflicting: renderSchemaValue(conflicting, hasConflicting),
	}
}

func renderSchemaValue(value any, exists bool) string {
	if !exists {
		return "<missing>"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "<unrenderable>"
	}
	runes := []rune(string(encoded))
	if len(runes) <= maximumDifferenceValueRunes {
		return string(runes)
	}
	return string(runes[:maximumDifferenceValueRunes-3]) + "..."
}

func joinSchemaPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}
