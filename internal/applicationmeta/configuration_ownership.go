package applicationmeta

import (
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/plystra/cli/internal/modulepath"
)

// AmbiguousConfigurationOwnershipError reports one inherited configuration
// field whose current-project representation disappeared without an explicit
// typed removal, together with every dependency Project document that
// contributed the prior compatible declaration.
type AmbiguousConfigurationOwnershipError struct {
	field   string
	message string
	sources []ConfigurationDeclarationSource
}

// Field returns the exact configuration schema path whose ownership is
// ambiguous.
func (e *AmbiguousConfigurationOwnershipError) Field() string {
	if e == nil {
		return ""
	}
	return e.field
}

// Sources returns a defensive copy in deterministic module/path/span order.
func (e *AmbiguousConfigurationOwnershipError) Sources() []ConfigurationDeclarationSource {
	if e == nil {
		return nil
	}
	return append([]ConfigurationDeclarationSource(nil), e.sources...)
}

func (e *AmbiguousConfigurationOwnershipError) Error() string {
	if e == nil {
		return ErrAmbiguousConfigurationOwnership.Error()
	}
	return e.message
}

// Unwrap supports errors.Is with ErrAmbiguousConfigurationOwnership.
func (*AmbiguousConfigurationOwnershipError) Unwrap() error {
	return ErrAmbiguousConfigurationOwnership
}

func newAmbiguousConfigurationOwnershipError(field, detail string, references []string) error {
	sources := make(configurationDeclarationSources)
	for _, reference := range references {
		if source, ok := dependencyConfigurationDeclarationSourceFromReference(reference, field); ok {
			addConfigurationDeclarationSource(sources, source)
		}
	}
	return &AmbiguousConfigurationOwnershipError{
		field:   field,
		message: ErrAmbiguousConfigurationOwnership.Error() + ": " + detail,
		sources: sortedConfigurationDeclarationSources(sources),
	}
}

func dependencyConfigurationDeclarationSourceFromReference(reference, field string) (ConfigurationDeclarationSource, bool) {
	document, ok := configurationReferenceDocument(reference, field)
	if !ok {
		return ConfigurationDeclarationSource{}, false
	}
	separator := strings.IndexByte(document, '@')
	if separator <= 0 || separator == len(document)-1 {
		return ConfigurationDeclarationSource{}, false
	}
	projectModule := document[:separator]
	if err := modulepath.CheckProject(projectModule); err != nil {
		return ConfigurationDeclarationSource{}, false
	}
	remainder := document[separator+1:]
	slash := strings.IndexByte(remainder, '/')
	if slash <= 0 || slash == len(remainder)-1 {
		return ConfigurationDeclarationSource{}, false
	}
	version := remainder[:slash]
	if strings.ContainsAny(version, "\\/\x00\r\n") || strings.IndexFunc(version, unicode.IsSpace) >= 0 {
		return ConfigurationDeclarationSource{}, false
	}
	relativePath := remainder[slash+1:]
	if !validConfigurationDeclarationPath(relativePath) {
		return ConfigurationDeclarationSource{}, false
	}
	return ConfigurationDeclarationSource{
		modulePath: projectModule,
		path:       relativePath,
		line:       1,
		column:     1,
	}, true
}

func configurationReferenceDocument(reference, field string) (string, bool) {
	fields := []string{field}
	if position := strings.IndexByte(field, '['); position > 0 {
		prefix, suffix := field[:position], field[position:]
		fields = append(fields, prefix+".add"+suffix, prefix+".remove"+suffix)
	}
	for _, candidate := range fields {
		suffix := " " + candidate
		if strings.HasSuffix(reference, suffix) && len(reference) > len(suffix) {
			return strings.TrimSuffix(reference, suffix), true
		}
	}
	return "", false
}

func validConfigurationDeclarationPath(value string) bool {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\\x00\r\n") || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	if path.IsAbs(value) || path.Clean(value) != value || value == "." || value == ".." || strings.HasPrefix(value, "../") || strings.Contains(value, "/../") {
		return false
	}
	return len(value) < 2 || value[1] != ':' || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z'))
}
