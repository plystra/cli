package applicationmeta

import (
	"fmt"
	"path/filepath"
)

type httpCORSCompositionSource struct {
	declaration ConfigurationDeclarationSource
	overlay     bool
}

// Keep ownership without evaluating requiredness or cross-field invariants
// until every current-project layer has been applied.
type httpCORSCompositionSources struct {
	object, origins, latest *httpCORSCompositionSource
}

func (s *httpCORSCompositionSources) apply(layer Manifest, currentModule string, overlay bool) {
	cors := layer.httpCORS
	if cors.remove {
		*s = httpCORSCompositionSources{}
		return
	}
	if !cors.present {
		return
	}
	modulePath := layer.modulePath
	if modulePath == "" {
		modulePath = currentModule
	}
	source := &httpCORSCompositionSource{
		declaration: ConfigurationDeclarationSource{modulePath: modulePath, path: filepath.ToSlash(layer.source), line: 1, column: 1},
		overlay:     overlay,
	}
	if s.object == nil {
		s.object, s.latest = source, source
	}
	if cors.hasAllowedOrigins || cors.removeAllowedOrigins {
		s.origins, s.latest = source, source
	}
	if cors.hasAllowCredentials || cors.removeAllowCredentials {
		s.latest = source
	}
}

func (s httpCORSCompositionSources) invalid(cors httpCORSLayer, cause error) error {
	source := s.latest
	if !cors.hasAllowedOrigins || len(cors.allowedOrigins) == 0 {
		// A missing required field belongs to its tombstone, or to the layer
		// that introduced the object when no layer ever supplied the field.
		source = s.origins
		if source == nil {
			source = s.object
		}
	}
	cause = fmt.Errorf("%w: %w", ErrCompose, cause)
	if source == nil {
		return cause
	}
	if source.overlay {
		return &EnvironmentOverlayError{source: source.declaration, cause: fmt.Errorf("%w: %w", ErrApplyOverlay, cause)}
	}
	return &ConfigurationCompositionError{source: source.declaration, cause: cause}
}
