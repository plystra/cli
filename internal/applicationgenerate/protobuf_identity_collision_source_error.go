package applicationgenerate

import (
	"errors"

	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/protobufmodel"
)

const protobufIdentityCollisionSourceKind = "interface-contract"

// ProtobufIdentityCollisionSourceError attaches the owning Interface contract
// declaration to a generated Protobuf identity collision without changing its
// human message or typed error chain.
type ProtobufIdentityCollisionSourceError struct {
	interfaceID interfaceid.Identifier
	modulePath  string
	sourcePath  string
	line        int
	column      int
	cause       error
}

// InterfaceID returns the exact canonical Interface containing the collision.
func (e *ProtobufIdentityCollisionSourceError) InterfaceID() interfaceid.Identifier {
	if e == nil {
		return interfaceid.Identifier{}
	}
	return e.interfaceID
}

// ModulePath returns the Go Module that owns the authored Interface contract.
func (e *ProtobufIdentityCollisionSourceError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// SourcePath returns the stable module-relative Interface Go source path.
func (e *ProtobufIdentityCollisionSourceError) SourcePath() string {
	if e == nil {
		return ""
	}
	return e.sourcePath
}

// SourceKind returns the canonical diagnostic source category.
func (e *ProtobufIdentityCollisionSourceError) SourceKind() string {
	if e == nil {
		return ""
	}
	return protobufIdentityCollisionSourceKind
}

// Line returns the one-based Interface declaration line.
func (e *ProtobufIdentityCollisionSourceError) Line() int {
	if e == nil {
		return 0
	}
	return e.line
}

// Column returns the one-based Interface declaration column.
func (e *ProtobufIdentityCollisionSourceError) Column() int {
	if e == nil {
		return 0
	}
	return e.column
}

func (e *ProtobufIdentityCollisionSourceError) Error() string {
	if e == nil || e.cause == nil {
		return "generated Protobuf identity collision"
	}
	return e.cause.Error()
}

// Unwrap preserves the original typed projection failure chain.
func (e *ProtobufIdentityCollisionSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func protobufIdentityCollisionSourceError(definitions []interfaceinventory.Interface, cause error) error {
	if cause == nil {
		return nil
	}
	var existing *ProtobufIdentityCollisionSourceError
	if errors.As(cause, &existing) && existing != nil {
		return cause
	}
	var collision *protobufmodel.InterfaceIdentityCollisionError
	if !errors.As(cause, &collision) || collision == nil {
		return cause
	}
	identifier := collision.InterfaceID()
	for _, definition := range definitions {
		if definition.ID() != identifier.String() {
			continue
		}
		position := definition.Declaration().Position()
		return &ProtobufIdentityCollisionSourceError{
			interfaceID: identifier,
			modulePath:  definition.ModulePath(),
			sourcePath:  definition.SourcePath(),
			line:        position.Line,
			column:      position.Column,
			cause:       cause,
		}
	}
	return cause
}
