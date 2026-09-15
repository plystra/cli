package applicationgenerate

import (
	"errors"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/protobufmodel"
)

const protobufPointerProjectionSourceKind = "exposure"

// ProtobufPointerProjectionSourceError attaches the effective http.expose
// declaration to an unsupported pointer-presence projection without changing
// its human message or typed error chain.
type ProtobufPointerProjectionSourceError struct {
	interfaceID interfaceid.Identifier
	modulePath  string
	sourcePath  string
	line        int
	column      int
	cause       error
}

// InterfaceID returns the exact canonical Interface rejected by projection.
func (e *ProtobufPointerProjectionSourceError) InterfaceID() interfaceid.Identifier {
	if e == nil {
		return interfaceid.Identifier{}
	}
	return e.interfaceID
}

// ModulePath returns the Go Module that owns the effective exposure.
func (e *ProtobufPointerProjectionSourceError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// SourcePath returns the stable module-relative configuration document.
func (e *ProtobufPointerProjectionSourceError) SourcePath() string {
	if e == nil {
		return ""
	}
	return e.sourcePath
}

// SourceKind returns the canonical diagnostic source category.
func (e *ProtobufPointerProjectionSourceError) SourceKind() string {
	if e == nil {
		return ""
	}
	return protobufPointerProjectionSourceKind
}

// Line returns the one-based exposure declaration line.
func (e *ProtobufPointerProjectionSourceError) Line() int {
	if e == nil {
		return 0
	}
	return e.line
}

// Column returns the one-based exposure declaration column.
func (e *ProtobufPointerProjectionSourceError) Column() int {
	if e == nil {
		return 0
	}
	return e.column
}

func (e *ProtobufPointerProjectionSourceError) Error() string {
	if e == nil || e.cause == nil {
		return protobufmodel.ErrPointerProjection.Error()
	}
	return e.cause.Error()
}

// Unwrap preserves the original typed projection failure chain.
func (e *ProtobufPointerProjectionSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func protobufPointerProjectionSourceError(exposures []applicationmeta.HTTPExposure, cause error) error {
	if cause == nil {
		return nil
	}
	var existing *ProtobufPointerProjectionSourceError
	if errors.As(cause, &existing) && existing != nil {
		return cause
	}
	var unsupported *protobufmodel.InterfacePointerProjectionError
	if !errors.As(cause, &unsupported) || unsupported == nil {
		return cause
	}
	identifier := unsupported.InterfaceID()
	for _, exposure := range exposures {
		if exposure.ID() != identifier {
			continue
		}
		source := exposure.DeclarationSource()
		return &ProtobufPointerProjectionSourceError{
			interfaceID: identifier,
			modulePath:  source.ModulePath(),
			sourcePath:  source.Path(),
			line:        source.Line(),
			column:      source.Column(),
			cause:       cause,
		}
	}
	return cause
}
