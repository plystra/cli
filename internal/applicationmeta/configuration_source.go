package applicationmeta

// ConfigurationDeclarationSource identifies one Project configuration
// document behind a current or inherited declaration. The exact
// field or constructor is exposed separately by the corresponding typed value.
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
