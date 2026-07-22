package diagnosticjson

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// ErrInvalidSchema reports a malformed command-owned JSON schema identity or
// version.
var ErrInvalidSchema = errors.New("invalid diagnostic JSON schema")

// Schema is one immutable command-owned JSON schema identity and version. It
// deliberately has no shared global version: changing one command's result
// contract requires constructing a new version only for that schema.
type Schema struct {
	name     string
	version  uint32
	prepared bool
}

// NewSchema validates one command-owned schema identity and version.
func NewSchema(name string, version uint32) (Schema, error) {
	if !validSchemaName(name) {
		return Schema{}, fmt.Errorf("%w: name %q is not a stable plystra lower-kebab identity", ErrInvalidSchema, name)
	}
	if version == 0 || version > math.MaxInt32 {
		return Schema{}, fmt.Errorf("%w: version must be between 1 and 2147483647", ErrInvalidSchema)
	}
	return Schema{name: name, version: version, prepared: true}, nil
}

// Valid reports whether NewSchema produced this descriptor.
func (s Schema) Valid() bool {
	return s.prepared && validSchemaName(s.name) && s.version > 0 && s.version <= math.MaxInt32
}

// Name returns the stable command-specific schema identity without a version.
func (s Schema) Name() string { return s.name }

// Version returns this command schema's independent positive version.
func (s Schema) Version() uint32 { return s.version }

func validSchemaName(value string) bool {
	if len(value) > 256 || !strings.HasPrefix(value, "plystra.") {
		return false
	}
	segments := strings.Split(value, ".")
	if len(segments) < 2 {
		return false
	}
	for _, segment := range segments {
		if !validLowerKebab(segment, 128) {
			return false
		}
	}
	return true
}
