package applicationmeta

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	// ErrDependencyBaseline reports malformed or inconsistent generated
	// dependency-composition provenance.
	ErrDependencyBaseline = errors.New("invalid dependency configuration baseline")
)

// BaselineRecord is one serializable non-secret dependency decision. It
// contains only a typed path, normalized digest, removal marker, and source
// locations; configuration values and Secret reference targets are absent.
type BaselineRecord struct {
	Path    string
	Digest  string
	Removed bool
	Sources []string
}

// DependencyBaseline is one validated immutable prior dependency baseline
// restored from the generated application manifest.
type DependencyBaseline struct {
	records  []Provenance
	digest   string
	prepared bool
}

// RestoreDependencyBaseline validates generated provenance and its canonical
// digest before it is used to infer current-project ownership.
func RestoreDependencyBaseline(digest string, records []BaselineRecord) (DependencyBaseline, error) {
	if !validCompositionDigest(digest) {
		return DependencyBaseline{}, fmt.Errorf("%w: dependency digest is not canonical", ErrDependencyBaseline)
	}
	provenance := make([]Provenance, len(records))
	seen := make(map[string]struct{}, len(records))
	for index, record := range records {
		if record.Path == "" || strings.ContainsAny(record.Path, "\r\n\x00") {
			return DependencyBaseline{}, fmt.Errorf("%w: records[%d] has an invalid path", ErrDependencyBaseline, index)
		}
		if !validCompositionDigest(record.Digest) {
			return DependencyBaseline{}, fmt.Errorf("%w: records[%d] has an invalid digest", ErrDependencyBaseline, index)
		}
		if len(record.Sources) == 0 {
			return DependencyBaseline{}, fmt.Errorf("%w: records[%d] has no sources", ErrDependencyBaseline, index)
		}
		sources := append([]string(nil), record.Sources...)
		sort.Strings(sources)
		for sourceIndex, source := range sources {
			if source == "" || strings.ContainsAny(source, "\r\n\x00") {
				return DependencyBaseline{}, fmt.Errorf("%w: records[%d].sources[%d] is invalid", ErrDependencyBaseline, index, sourceIndex)
			}
			if sourceIndex > 0 && sources[sourceIndex-1] == source {
				return DependencyBaseline{}, fmt.Errorf("%w: records[%d] repeats source %q", ErrDependencyBaseline, index, source)
			}
		}
		key := provenanceKey(record.Path, record.Digest, record.Removed)
		if _, duplicate := seen[key]; duplicate {
			return DependencyBaseline{}, fmt.Errorf("%w: records[%d] repeats path and digest", ErrDependencyBaseline, index)
		}
		seen[key] = struct{}{}
		provenance[index] = Provenance{
			path:    record.Path,
			digest:  record.Digest,
			removed: record.Removed,
			sources: sources,
		}
	}
	sortProvenance(provenance)
	actual, err := digestProvenance(provenance)
	if err != nil {
		return DependencyBaseline{}, fmt.Errorf("%w: calculate digest: %v", ErrDependencyBaseline, err)
	}
	if actual != digest {
		return DependencyBaseline{}, fmt.Errorf("%w: recorded digest does not match the dependency records", ErrDependencyBaseline)
	}
	return DependencyBaseline{records: provenance, digest: digest, prepared: true}, nil
}

// Valid reports whether the baseline was validated by
// RestoreDependencyBaseline or produced by a valid Composition.
func (b DependencyBaseline) Valid() bool {
	return b.prepared && validCompositionDigest(b.digest)
}

// Digest returns the canonical dependency-composition digest.
func (b DependencyBaseline) Digest() string {
	if !b.Valid() {
		return ""
	}
	return b.digest
}

// Records returns a defensive path-and-digest-sorted copy.
func (b DependencyBaseline) Records() []BaselineRecord {
	if !b.Valid() {
		return nil
	}
	result := make([]BaselineRecord, len(b.records))
	for index, record := range b.records {
		result[index] = BaselineRecord{
			Path:    record.path,
			Digest:  record.digest,
			Removed: record.removed,
			Sources: append([]string(nil), record.sources...),
		}
	}
	return result
}

func provenanceKey(path, digest string, removed bool) string {
	return fmt.Sprintf("%s\x00%s\x00%t", path, digest, removed)
}

func sortProvenance(records []Provenance) {
	sort.Slice(records, func(left, right int) bool {
		if records[left].path != records[right].path {
			return records[left].path < records[right].path
		}
		if records[left].removed != records[right].removed {
			return !records[left].removed
		}
		return records[left].digest < records[right].digest
	})
}
