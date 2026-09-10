package applicationgenerate

import (
	"errors"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/protobufmodel"
)

const protobufOperationKindSourceKind = "exposure"

// ProtobufOperationKindSourceError attaches the effective http.expose
// declaration to an unsupported Connect operation kind without changing its
// human message or typed error chain.
type ProtobufOperationKindSourceError struct {
	capabilityID generation.CapabilityID
	modulePath   string
	sourcePath   string
	line         int
	column       int
	cause        error
}

// CapabilityID returns the exact canonical Capability rejected by projection.
func (e *ProtobufOperationKindSourceError) CapabilityID() generation.CapabilityID {
	if e == nil {
		return generation.CapabilityID{}
	}
	return e.capabilityID
}

// ModulePath returns the Go Module that owns the effective exposure.
func (e *ProtobufOperationKindSourceError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// SourcePath returns the stable module-relative configuration document.
func (e *ProtobufOperationKindSourceError) SourcePath() string {
	if e == nil {
		return ""
	}
	return e.sourcePath
}

// SourceKind returns the canonical diagnostic source category.
func (e *ProtobufOperationKindSourceError) SourceKind() string {
	if e == nil {
		return ""
	}
	return protobufOperationKindSourceKind
}

// Line returns the one-based exposure declaration line.
func (e *ProtobufOperationKindSourceError) Line() int {
	if e == nil {
		return 0
	}
	return e.line
}

// Column returns the one-based exposure declaration column.
func (e *ProtobufOperationKindSourceError) Column() int {
	if e == nil {
		return 0
	}
	return e.column
}

func (e *ProtobufOperationKindSourceError) Error() string {
	if e == nil || e.cause == nil {
		return "unsupported Connect operation kind"
	}
	return e.cause.Error()
}

// Unwrap preserves the original typed projection failure chain.
func (e *ProtobufOperationKindSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func protobufOperationKindSourceError(exposures []applicationmeta.HTTPExposure, cause error) error {
	if cause == nil {
		return nil
	}
	var existing *ProtobufOperationKindSourceError
	if errors.As(cause, &existing) && existing != nil {
		return cause
	}
	var unsupported *protobufmodel.OperationKindError
	if !errors.As(cause, &unsupported) || unsupported == nil {
		return cause
	}
	identifier := unsupported.CapabilityID()
	for _, exposure := range exposures {
		if exposure.ID().String() != identifier.String() {
			continue
		}
		source := exposure.DeclarationSource()
		return &ProtobufOperationKindSourceError{
			capabilityID: identifier,
			modulePath:   source.ModulePath(),
			sourcePath:   source.Path(),
			line:         source.Line(),
			column:       source.Column(),
			cause:        cause,
		}
	}
	return cause
}
