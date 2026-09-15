package commandschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
)

// ErrSelectorSnapshot reports an invalid selected Project view snapshot.
var ErrSelectorSnapshot = errors.New("build Plystra selector snapshot")

// SelectorSnapshotInput is the construction-only selected Project view.
type SelectorSnapshotInput struct {
	Mode generation.ConfigurationMode
	Name string
	Path string
}

// SelectorSnapshot is one immutable, non-secret Project selector snapshot.
type SelectorSnapshot struct {
	mode          generation.ConfigurationMode
	name          string
	path          string
	canonicalJSON []byte
	prepared      bool
}

type selectorSnapshotDocument struct {
	Selector selectorSnapshotSelectorDocument `json:"selector"`
}

type selectorSnapshotSelectorDocument struct {
	Mode generation.ConfigurationMode `json:"mode"`
	Name string                       `json:"name,omitempty"`
	Path string                       `json:"path,omitempty"`
}

// NewSelectorSnapshot validates and constructs one selected Project view.
func NewSelectorSnapshot(input SelectorSnapshotInput) (SelectorSnapshot, error) {
	if err := validateSelectorSnapshot(input); err != nil {
		return SelectorSnapshot{}, fmt.Errorf("%w: %v", ErrSelectorSnapshot, err)
	}
	canonical, err := json.Marshal(selectorSnapshotDocumentFrom(input))
	if err != nil {
		return SelectorSnapshot{}, fmt.Errorf("%w: encode: %v", ErrSelectorSnapshot, err)
	}
	return SelectorSnapshot{
		mode:          input.Mode,
		name:          input.Name,
		path:          input.Path,
		canonicalJSON: canonical,
		prepared:      true,
	}, nil
}

// Valid reports whether NewSelectorSnapshot produced this snapshot.
func (s SelectorSnapshot) Valid() bool {
	if !s.prepared {
		return false
	}
	input := SelectorSnapshotInput{Mode: s.mode, Name: s.name, Path: s.path}
	if validateSelectorSnapshot(input) != nil {
		return false
	}
	canonical, err := json.Marshal(selectorSnapshotDocumentFrom(input))
	return err == nil && bytes.Equal(canonical, s.canonicalJSON)
}

// Mode returns default, environment, or explicit-config.
func (s SelectorSnapshot) Mode() generation.ConfigurationMode { return s.mode }

// Name returns the selected environment name in environment mode.
func (s SelectorSnapshot) Name() string { return s.name }

// Path returns the selected Project-relative path in explicit-config mode.
func (s SelectorSnapshot) Path() string { return s.path }

// CanonicalJSON returns a defensive copy of the complete snapshot object.
func (s SelectorSnapshot) CanonicalJSON() []byte {
	return append([]byte(nil), s.canonicalJSON...)
}

func validateSelectorSnapshot(input SelectorSnapshotInput) error {
	switch input.Mode {
	case generation.ConfigurationModeDefault:
		if input.Name != "" || input.Path != "" {
			return errors.New("default mode cannot contain a name or path")
		}
	case generation.ConfigurationModeEnvironment:
		if !validSelectorEnvironmentName(input.Name) || input.Path != "" {
			return errors.New("environment mode requires one safe name and no path")
		}
	case generation.ConfigurationModeExplicit:
		if input.Name != "" || !validRelativePath(input.Path, false) {
			return errors.New("explicit-config mode requires one Project-relative path and no name")
		}
	default:
		return fmt.Errorf("mode %q is not supported", input.Mode)
	}
	return nil
}

func validSelectorEnvironmentName(value string) bool {
	return len(value) <= 200 && strings.TrimSpace(value) != "" && value != "." && value != ".." &&
		validSafeText(value, 200) && !strings.ContainsAny(value, `/\\<>:"|?*`)
}

func selectorSnapshotDocumentFrom(input SelectorSnapshotInput) selectorSnapshotDocument {
	return selectorSnapshotDocument{Selector: selectorSnapshotSelectorDocument(input)}
}

func cloneSelectorSnapshot(value *SelectorSnapshot) *SelectorSnapshot {
	if value == nil {
		return nil
	}
	copy := *value
	copy.canonicalJSON = append([]byte(nil), value.canonicalJSON...)
	return &copy
}
