package applicationgenerate

import (
	"errors"

	"github.com/plystra/cli/internal/protobufwiremap"
)

const protobufWireHistorySourceKind = "generated-artifact"

// ProtobufWireHistorySourceError attaches the current Project identity and the
// managed wire-map path to a history failure without changing its human
// message or typed error chain.
type ProtobufWireHistorySourceError struct {
	modulePath string
	cause      error
}

// ModulePath returns the current Project module containing the wire history.
func (e *ProtobufWireHistorySourceError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// SourcePath returns the canonical module-relative wire-history path.
func (e *ProtobufWireHistorySourceError) SourcePath() string {
	if e == nil {
		return ""
	}
	return protobufwiremap.Path
}

// SourceKind returns the canonical diagnostic source category.
func (e *ProtobufWireHistorySourceError) SourceKind() string {
	if e == nil {
		return ""
	}
	return protobufWireHistorySourceKind
}

// Line returns zero because generated wire history has no authored span.
func (e *ProtobufWireHistorySourceError) Line() int { return 0 }

// Column returns zero because generated wire history has no authored span.
func (e *ProtobufWireHistorySourceError) Column() int { return 0 }

func (e *ProtobufWireHistorySourceError) Error() string {
	if e == nil || e.cause == nil {
		return "invalid Protobuf wire-map history"
	}
	return e.cause.Error()
}

// Unwrap preserves the original wire-history failure chain.
func (e *ProtobufWireHistorySourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func protobufWireHistorySourceError(modulePath string, cause error) error {
	if modulePath == "" || cause == nil || !errors.Is(cause, protobufwiremap.ErrHistory) {
		return cause
	}
	var existing *ProtobufWireHistorySourceError
	if errors.As(cause, &existing) && existing != nil {
		return cause
	}
	return &ProtobufWireHistorySourceError{modulePath: modulePath, cause: cause}
}
