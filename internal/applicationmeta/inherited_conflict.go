package applicationmeta

import (
	"fmt"
	"sort"
	"strings"
)

// ConfigurationDeclarationSource identifies one Project configuration
// document that contributes to an inherited conflict or a prior declaration
// whose current-Project ownership became ambiguous. The exact field is exposed
// separately by the corresponding typed error.
type ConfigurationDeclarationSource struct {
	modulePath string
	path       string
	line       int
	column     int
}

// ModulePath returns the owning Project's Go Module path.
func (s ConfigurationDeclarationSource) ModulePath() string { return s.modulePath }

// Path returns the slash-separated module-relative configuration document.
func (s ConfigurationDeclarationSource) Path() string { return s.path }

// Line returns the one-based declaration line, or zero when unavailable.
func (s ConfigurationDeclarationSource) Line() int { return s.line }

// Column returns the one-based declaration column, or zero when unavailable.
func (s ConfigurationDeclarationSource) Column() int { return s.column }

// InheritedConflictError reports one exact incompatible configuration field
// and every Project document that contributed a competing declaration.
type InheritedConflictError struct {
	field   string
	message string
	sources []ConfigurationDeclarationSource
}

// Field returns the exact conflicting configuration schema path.
func (e *InheritedConflictError) Field() string {
	if e == nil {
		return ""
	}
	return e.field
}

// Sources returns a defensive copy in deterministic module/path/span order.
func (e *InheritedConflictError) Sources() []ConfigurationDeclarationSource {
	if e == nil {
		return nil
	}
	return append([]ConfigurationDeclarationSource(nil), e.sources...)
}

func (e *InheritedConflictError) Error() string {
	if e == nil {
		return ErrInheritedConflict.Error()
	}
	return e.message
}

// Unwrap supports errors.Is with ErrInheritedConflict.
func (*InheritedConflictError) Unwrap() error { return ErrInheritedConflict }

type configurationDeclarationSources map[ConfigurationDeclarationSource]struct{}

func newInheritedConflictError(field, detail string, declarations configurationDeclarationSources) error {
	return &InheritedConflictError{
		field:   field,
		message: ErrInheritedConflict.Error() + ": " + detail,
		sources: sortedConfigurationDeclarationSources(declarations),
	}
}

func dependencyConfigurationDeclarationSource(dependency Dependency) ConfigurationDeclarationSource {
	return ConfigurationDeclarationSource{
		modulePath: dependency.ModulePath,
		path:       dependency.Manifest.source,
		line:       1,
		column:     1,
	}
}

func manifestConfigurationDeclarationSource(manifest Manifest, sourcePath string) ConfigurationDeclarationSource {
	return ConfigurationDeclarationSource{
		modulePath: manifest.modulePath,
		path:       sourcePath,
		line:       1,
		column:     1,
	}
}

func addConfigurationDeclarationSource(target configurationDeclarationSources, source ConfigurationDeclarationSource) {
	if target == nil {
		return
	}
	target[source] = struct{}{}
}

func mergeConfigurationDeclarationSources(target configurationDeclarationSources, sources ...configurationDeclarationSources) {
	for _, values := range sources {
		for source := range values {
			target[source] = struct{}{}
		}
	}
}

func sortedConfigurationDeclarationSources(values configurationDeclarationSources) []ConfigurationDeclarationSource {
	result := make([]ConfigurationDeclarationSource, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool {
		return configurationDeclarationSourceKey(result[left]) < configurationDeclarationSourceKey(result[right])
	})
	return result
}

func configurationDeclarationSourceKey(value ConfigurationDeclarationSource) string {
	return strings.Join([]string{
		value.modulePath,
		value.path,
		fmt.Sprintf("%010d", value.line),
		fmt.Sprintf("%010d", value.column),
	}, "\x00")
}
