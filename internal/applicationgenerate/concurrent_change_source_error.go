package applicationgenerate

import (
	"path/filepath"
	"sort"
)

// ConcurrentChangeSource is one whole-file Project-relative input or generated
// output affected by a concurrent generation change.
type ConcurrentChangeSource struct {
	modulePath string
	sourcePath string
	sourceKind string
}

// ModulePath returns the Go Module that owns the affected source.
func (s ConcurrentChangeSource) ModulePath() string { return s.modulePath }

// SourcePath returns the stable slash-separated module-relative path.
func (s ConcurrentChangeSource) SourcePath() string { return s.sourcePath }

// SourceKind returns the canonical diagnostic source category.
func (s ConcurrentChangeSource) SourceKind() string { return s.sourceKind }

// Line returns zero because concurrent comparisons identify a whole file.
func (ConcurrentChangeSource) Line() int { return 0 }

// Column returns zero because concurrent comparisons identify a whole file.
func (ConcurrentChangeSource) Column() int { return 0 }

// ConcurrentChangeSourceError attaches every deterministically known affected
// source to a generation race without changing the human-readable error.
type ConcurrentChangeSourceError struct {
	sources []ConcurrentChangeSource
	cause   error
}

// Sources returns defensive sources in module, path, then kind order.
func (e *ConcurrentChangeSourceError) Sources() []ConcurrentChangeSource {
	if e == nil {
		return nil
	}
	return append([]ConcurrentChangeSource(nil), e.sources...)
}

func (e *ConcurrentChangeSourceError) Error() string {
	if e == nil || e.cause == nil {
		return ErrConcurrentChange.Error()
	}
	return e.cause.Error()
}

// Unwrap preserves every lower-level source and failure classification.
func (e *ConcurrentChangeSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Is preserves the public generation-level concurrent-change classification.
func (e *ConcurrentChangeSourceError) Is(target error) bool {
	return e != nil && target == ErrConcurrentChange
}

func concurrentChangeSource(modulePath, sourcePath, sourceKind string) ConcurrentChangeSource {
	return ConcurrentChangeSource{
		modulePath: modulePath,
		sourcePath: filepath.ToSlash(filepath.Clean(sourcePath)),
		sourceKind: sourceKind,
	}
}

func concurrentChangeSourceError(sources []ConcurrentChangeSource, cause error) error {
	if cause == nil {
		return nil
	}
	sources = append(sources, nestedConcurrentChangeSources(cause)...)
	sources = canonicalConcurrentChangeSources(sources)
	return &ConcurrentChangeSourceError{sources: sources, cause: cause}
}

func nestedConcurrentChangeSources(err error) []ConcurrentChangeSource {
	var sources []ConcurrentChangeSource
	var walk func(error)
	walk = func(current error) {
		if current == nil {
			return
		}
		if concurrent, ok := current.(*ConcurrentChangeSourceError); ok {
			sources = append(sources, concurrent.sources...)
			walk(concurrent.cause)
			return
		}
		switch current := current.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range current.Unwrap() {
				walk(child)
			}
		case interface{ Unwrap() error }:
			walk(current.Unwrap())
		}
	}
	walk(err)
	return sources
}

func canonicalConcurrentChangeSources(sources []ConcurrentChangeSource) []ConcurrentChangeSource {
	unique := make(map[ConcurrentChangeSource]struct{}, len(sources))
	for _, source := range sources {
		if source.modulePath == "" || source.sourcePath == "" || source.sourcePath == "." || source.sourceKind == "" {
			continue
		}
		unique[source] = struct{}{}
	}
	canonical := make([]ConcurrentChangeSource, 0, len(unique))
	for source := range unique {
		canonical = append(canonical, source)
	}
	sort.Slice(canonical, func(left, right int) bool {
		if canonical[left].modulePath != canonical[right].modulePath {
			return canonical[left].modulePath < canonical[right].modulePath
		}
		if canonical[left].sourcePath != canonical[right].sourcePath {
			return canonical[left].sourcePath < canonical[right].sourcePath
		}
		return canonical[left].sourceKind < canonical[right].sourceKind
	})
	return canonical
}
