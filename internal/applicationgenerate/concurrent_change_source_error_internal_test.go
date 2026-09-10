package applicationgenerate

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

func TestConcurrentChangeSourceErrorCanonicalizesNestedSources(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf("generation snapshot changed: %w", ErrConcurrentChange)
	nested := concurrentChangeSourceError([]ConcurrentChangeSource{
		concurrentChangeSource("example.com/z", "plystra.yaml", "configuration-declaration"),
		concurrentChangeSource("example.com/a", `generated\go\app.go`, "generated-artifact"),
		concurrentChangeSource("example.com/z", "plystra.yaml", "configuration-declaration"),
	}, cause)
	err := concurrentChangeSourceError([]ConcurrentChangeSource{
		concurrentChangeSource("example.com/m", "go.mod", "module-dependency"),
		concurrentChangeSource("example.com/a", "generated/go/app.go", "generated-artifact"),
	}, nested)

	if err.Error() != cause.Error() || !errors.Is(err, ErrConcurrentChange) {
		t.Fatalf("concurrentChangeSourceError = %v, want unchanged message and ErrConcurrentChange", err)
	}
	var concurrent *ConcurrentChangeSourceError
	if !errors.As(err, &concurrent) || concurrent == nil {
		t.Fatalf("ConcurrentChangeSourceError missing from %v", err)
	}
	want := []string{
		"example.com/a:generated/go/app.go:generated-artifact",
		"example.com/m:go.mod:module-dependency",
		"example.com/z:plystra.yaml:configuration-declaration",
	}
	if got := concurrentSourceSummaries(concurrent.Sources()); !slices.Equal(got, want) {
		t.Fatalf("ConcurrentChangeSourceError.Sources = %v, want %v", got, want)
	}
	sources := concurrent.Sources()
	sources[0] = ConcurrentChangeSource{}
	if got := concurrentSourceSummaries(concurrent.Sources()); !slices.Equal(got, want) {
		t.Fatalf("ConcurrentChangeSourceError.Sources changed through returned slice: %v", got)
	}
}

func concurrentSourceSummaries(sources []ConcurrentChangeSource) []string {
	summaries := make([]string, len(sources))
	for index, source := range sources {
		summaries[index] = source.ModulePath() + ":" + source.SourcePath() + ":" + source.SourceKind()
	}
	return summaries
}
