package diagnosticschema

import (
	"fmt"
	"sort"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/diagnosticjson"
)

func normalizeSchemaSources(schema diagnosticjson.Schema, mode generation.ConfigurationMode, digest string, values []diagnosticjson.Source) ([]diagnosticjson.Source, error) {
	values = deduplicateDiagnosticSources(values)
	envelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 schema,
		ConfigurationMode:      mode,
		ApplicationModelDigest: digest,
		Sources:                values,
	})
	if err != nil {
		return nil, err
	}
	return envelope.Sources(), nil
}

func deduplicateDiagnosticSources(values []diagnosticjson.Source) []diagnosticjson.Source {
	unique := make(map[diagnosticjson.Source]struct{}, len(values))
	for _, source := range values {
		unique[source] = struct{}{}
	}
	result := make([]diagnosticjson.Source, 0, len(unique))
	for source := range unique {
		result = append(result, source)
	}
	sort.Slice(result, func(left, right int) bool {
		return diagnosticSourceKey(result[left]) < diagnosticSourceKey(result[right])
	})
	return result
}

func diagnosticSourceKey(source diagnosticjson.Source) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%010d\x00%010d", source.Module, source.Path, source.Kind, source.Line, source.Column)
}

func equalDiagnosticSources(left, right []diagnosticjson.Source) bool {
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
