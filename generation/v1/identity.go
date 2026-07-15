package generation

import (
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/pluginid"
)

var (
	// ErrInvalidCapabilityID reports a non-canonical exact Capability ID.
	ErrInvalidCapabilityID = errors.New("invalid capability ID")
	// ErrInvalidPluginID reports a non-canonical Plugin ID.
	ErrInvalidPluginID = errors.New("invalid plugin ID")
)

// CapabilityID identifies one exact canonical Capability contract or one
// application-local Alias. Its role is determined by the containing view.
type CapabilityID struct {
	identifier capabilityid.Identifier
}

// ParseCapabilityID parses one canonical exact Capability or Alias ID.
func ParseCapabilityID(value string) (CapabilityID, error) {
	identifier, err := capabilityid.Parse(value)
	if err != nil {
		return CapabilityID{}, fmt.Errorf("%w %q: %v", ErrInvalidCapabilityID, value, err)
	}
	return CapabilityID{identifier: identifier}, nil
}

// Name returns the unversioned canonical name.
func (i CapabilityID) Name() string { return i.identifier.Name() }

// Major returns the exact positive major version.
func (i CapabilityID) Major() uint64 { return i.identifier.Major() }

// String returns the canonical exact ID, or an empty string for the zero value.
func (i CapabilityID) String() string { return i.identifier.String() }

// MarshalText encodes the canonical exact ID for protocol payloads.
func (i CapabilityID) MarshalText() ([]byte, error) {
	if i.String() == "" {
		return nil, fmt.Errorf("%w: zero value", ErrInvalidCapabilityID)
	}
	return []byte(i.String()), nil
}

// UnmarshalText validates one canonical exact ID from a protocol payload.
func (i *CapabilityID) UnmarshalText(data []byte) error {
	if i == nil {
		return fmt.Errorf("%w: nil destination", ErrInvalidCapabilityID)
	}
	parsed, err := ParseCapabilityID(string(data))
	if err != nil {
		return err
	}
	*i = parsed
	return nil
}

// PluginID identifies one exact plugin independently of its Go Module version.
type PluginID struct {
	value string
}

// ParsePluginID parses one canonical Plugin ID.
func ParsePluginID(value string) (PluginID, error) {
	if err := pluginid.Validate(value); err != nil {
		return PluginID{}, fmt.Errorf("%w %q: %v", ErrInvalidPluginID, value, err)
	}
	return PluginID{value: value}, nil
}

// String returns the canonical Plugin ID, or an empty string for the zero value.
func (i PluginID) String() string { return i.value }

// MarshalText encodes the canonical Plugin ID for protocol payloads.
func (i PluginID) MarshalText() ([]byte, error) {
	if i.String() == "" {
		return nil, fmt.Errorf("%w: zero value", ErrInvalidPluginID)
	}
	return []byte(i.String()), nil
}

// UnmarshalText validates one canonical Plugin ID from a protocol payload.
func (i *PluginID) UnmarshalText(data []byte) error {
	if i == nil {
		return fmt.Errorf("%w: nil destination", ErrInvalidPluginID)
	}
	parsed, err := ParsePluginID(string(data))
	if err != nil {
		return err
	}
	*i = parsed
	return nil
}
