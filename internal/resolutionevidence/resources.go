package resolutionevidence

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfaceprovenance"
)

// WithResources adds the selected named instances and exact resolved edges to
// live evidence. The graph is authoritative; evidence performs no resolution.
func WithResources(evidence Evidence, graph constructorgraph.Graph, catalog interfaceinventory.ResourceIndex) (Evidence, error) {
	if !evidence.Valid() {
		return Evidence{}, fmt.Errorf("%w: invalid existing evidence", ErrBuild)
	}
	resources, bindings, err := interfaceprovenance.ResourceInputs(graph, catalog)
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: Resource provenance: %w", ErrBuild, err)
	}
	for i := range resources {
		resources[i].ConfigurationSources = evidence.ResourceConfigurationSources(resources[i].Name)
	}
	evidence.resources, evidence.resourceBindings = resources, bindings
	if err := validate(evidence); err != nil {
		return Evidence{}, fmt.Errorf("%w: %v", ErrBuild, err)
	}
	canonical, err := encode(evidence)
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: encode Resource provenance: %w", ErrBuild, err)
	}
	evidence.canonicalJSON, evidence.digest = canonical, digest(canonical)
	return evidence, nil
}

// Resources returns defensive selected instance records, including unconsumed instances.
func (e Evidence) Resources() []interfaceprovenance.ResourceInput {
	result := append([]interfaceprovenance.ResourceInput(nil), e.resources...)
	for i := range result {
		result[i].SelectionSources = append([]interfaceprovenance.ResourceSource{}, result[i].SelectionSources...)
		result[i].ConfigurationSources = append([]interfaceprovenance.ResourceSource{}, result[i].ConfigurationSources...)
	}
	return result
}

// ResourceConfigurationSources returns every effective current-project
// configuration contributor for one instance.
// It exposes coordinates only, not configuration keys, digests or values.
func (e Evidence) ResourceConfigurationSources(instanceName string) []interfaceprovenance.ResourceSource {
	prefix := "resources.instances[" + strconv.Quote(instanceName) + "].config"
	sources := make(map[interfaceprovenance.ResourceSource]bool)
	for _, field := range e.configurationFields {
		if field.path != prefix && !strings.HasPrefix(field.path, prefix+"[") {
			continue
		}
		for _, contribution := range field.contributors {
			if !contribution.effective {
				continue
			}
			for _, source := range contribution.sources {
				sources[interfaceprovenance.ResourceSource{Module: source.module, Path: source.path, Kind: "configuration-declaration", Line: source.line, Column: source.column}] = true
			}
		}
	}
	result := make([]interfaceprovenance.ResourceSource, 0, len(sources))
	for source := range sources {
		result = append(result, source)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.Module != b.Module {
			return a.Module < b.Module
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	return result
}

// ResourceBindings returns defensive active consumer-to-instance edges.
func (e Evidence) ResourceBindings() []interfaceprovenance.ResourceBindingInput {
	result := append([]interfaceprovenance.ResourceBindingInput(nil), e.resourceBindings...)
	for i := range result {
		result[i].BindingSources = append([]interfaceprovenance.ResourceSource{}, result[i].BindingSources...)
		result[i].SelectionSources = append([]interfaceprovenance.ResourceSource{}, result[i].SelectionSources...)
		result[i].ConsumerSelectionSources = append([]interfaceprovenance.ResourceSource{}, result[i].ConsumerSelectionSources...)
	}
	return result
}
