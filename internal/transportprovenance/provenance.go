// Package transportprovenance carries the bounded, non-secret configuration
// identity supplied to built-in transport renderers.
package transportprovenance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
)

const schemaVersion = 1

// ErrInvalid reports configuration provenance that cannot identify one
// normalized transport-generation input without machine-specific or secret
// state.
var ErrInvalid = errors.New("invalid transport configuration provenance")

// Input is the construction-only form of selected configuration provenance.
// It intentionally has no field capable of carrying YAML values, Secret
// references, resolved Secrets, absolute paths, or generated-output paths.
type Input struct {
	Mode                        generation.ConfigurationMode
	Environment                 string
	RootPath                    string
	RootDigest                  string
	SelectedPath                string
	SelectedDigest              string
	DependencyCompositionDigest string
	ApplicationModelDigest      string
}

// Provenance is one immutable, validated configuration identity tied to the
// final build-affecting application model supplied to transport renderers.
type Provenance struct {
	mode                        generation.ConfigurationMode
	environment                 string
	rootPath                    string
	rootDigest                  string
	selectedPath                string
	selectedDigest              string
	dependencyCompositionDigest string
	applicationModelDigest      string
	canonicalJSON               []byte
	digest                      string
	prepared                    bool
}

type canonicalProvenance struct {
	Version                     int                          `json:"version"`
	Mode                        generation.ConfigurationMode `json:"mode"`
	Environment                 string                       `json:"environment,omitempty"`
	RootPath                    string                       `json:"root_path"`
	RootDigest                  string                       `json:"root_digest"`
	SelectedPath                string                       `json:"selected_path"`
	SelectedDigest              string                       `json:"selected_digest"`
	DependencyCompositionDigest string                       `json:"dependency_composition_digest"`
	ApplicationModelDigest      string                       `json:"application_model_digest"`
}

// New validates and canonicalizes one transport configuration identity.
func New(input Input) (Provenance, error) {
	if err := validateInput(input); err != nil {
		return Provenance{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	canonical, err := encode(input)
	if err != nil {
		return Provenance{}, fmt.Errorf("%w: encode canonical input: %v", ErrInvalid, err)
	}
	return Provenance{
		mode:                        input.Mode,
		environment:                 input.Environment,
		rootPath:                    input.RootPath,
		rootDigest:                  input.RootDigest,
		selectedPath:                input.SelectedPath,
		selectedDigest:              input.SelectedDigest,
		dependencyCompositionDigest: input.DependencyCompositionDigest,
		applicationModelDigest:      input.ApplicationModelDigest,
		canonicalJSON:               canonical,
		digest:                      digest(canonical),
		prepared:                    true,
	}, nil
}

// Valid reports whether the value is a complete constructor-produced identity.
func (p Provenance) Valid() bool {
	if !p.prepared {
		return false
	}
	input := p.input()
	if validateInput(input) != nil {
		return false
	}
	canonical, err := encode(input)
	return err == nil && bytes.Equal(p.canonicalJSON, canonical) && p.digest == digest(canonical)
}

// Mode returns default, environment, or explicit-config.
func (p Provenance) Mode() generation.ConfigurationMode { return p.mode }

// Environment returns the selected environment in environment mode.
func (p Provenance) Environment() string { return p.environment }

// RootPath returns the mandatory root marker's Project-relative path.
func (p Provenance) RootPath() string { return p.rootPath }

// RootDigest returns the normalized root-document digest.
func (p Provenance) RootDigest() string { return p.rootDigest }

// SelectedPath returns the selected document's Project-relative path.
func (p Provenance) SelectedPath() string { return p.selectedPath }

// SelectedDigest returns the normalized selected-document digest.
func (p Provenance) SelectedDigest() string { return p.selectedDigest }

// DependencyCompositionDigest returns the normalized dependency baseline and
// all-source provenance digest.
func (p Provenance) DependencyCompositionDigest() string {
	return p.dependencyCompositionDigest
}

// ApplicationModelDigest returns the final build-affecting model identity.
func (p Provenance) ApplicationModelDigest() string { return p.applicationModelDigest }

// CanonicalJSON returns a defensive copy of the deterministic bounded input.
func (p Provenance) CanonicalJSON() []byte {
	return append([]byte(nil), p.canonicalJSON...)
}

// Digest returns the lowercase SHA-256 identity of CanonicalJSON.
func (p Provenance) Digest() string { return p.digest }

func (p Provenance) input() Input {
	return Input{
		Mode:                        p.mode,
		Environment:                 p.environment,
		RootPath:                    p.rootPath,
		RootDigest:                  p.rootDigest,
		SelectedPath:                p.selectedPath,
		SelectedDigest:              p.selectedDigest,
		DependencyCompositionDigest: p.dependencyCompositionDigest,
		ApplicationModelDigest:      p.applicationModelDigest,
	}
}

func validateInput(input Input) error {
	if input.RootPath != "plystra.yaml" {
		return errors.New("root path must be \"plystra.yaml\"")
	}
	if !validProjectRelativePath(input.SelectedPath) {
		return fmt.Errorf("selected path %q is not a stable Project-relative slash path", input.SelectedPath)
	}
	if !validDigest(input.RootDigest) {
		return errors.New("root digest is not a canonical SHA-256 digest")
	}
	if !validDigest(input.SelectedDigest) {
		return errors.New("selected digest is not a canonical SHA-256 digest")
	}
	if !validDigest(input.DependencyCompositionDigest) {
		return errors.New("dependency-composition digest is not a canonical SHA-256 digest")
	}
	if !validDigest(input.ApplicationModelDigest) {
		return errors.New("application-model digest is not a canonical SHA-256 digest")
	}
	switch input.Mode {
	case generation.ConfigurationModeDefault:
		if input.Environment != "" {
			return errors.New("environment must be empty in default mode")
		}
		if input.SelectedPath != input.RootPath || input.SelectedDigest != input.RootDigest {
			return errors.New("default mode must select the exact root document")
		}
	case generation.ConfigurationModeEnvironment:
		if !validEnvironmentName(input.Environment) {
			return fmt.Errorf("environment %q is not one safe filename component", input.Environment)
		}
		expected := "plystra." + input.Environment + ".yaml"
		if input.SelectedPath != expected {
			return fmt.Errorf("selected path must be %q in environment mode", expected)
		}
	case generation.ConfigurationModeExplicit:
		if input.Environment != "" {
			return errors.New("environment must be empty in explicit-config mode")
		}
	default:
		return fmt.Errorf("mode %q is not supported", input.Mode)
	}
	return nil
}

func encode(input Input) ([]byte, error) {
	return json.Marshal(canonicalProvenance{
		Version:                     schemaVersion,
		Mode:                        input.Mode,
		Environment:                 input.Environment,
		RootPath:                    input.RootPath,
		RootDigest:                  input.RootDigest,
		SelectedPath:                input.SelectedPath,
		SelectedDigest:              input.SelectedDigest,
		DependencyCompositionDigest: input.DependencyCompositionDigest,
		ApplicationModelDigest:      input.ApplicationModelDigest,
	})
}

func validProjectRelativePath(value string) bool {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || path.IsAbs(value) || path.Clean(value) != value || value == "." || value == ".." || strings.HasPrefix(value, "../") || strings.Contains(value, "\\") || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	if len(value) >= 2 && value[1] == ':' && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) {
		return false
	}
	return true
}

func validEnvironmentName(value string) bool {
	return value != "" && len(value) <= 200 && value != "." && value != ".." && utf8.ValidString(value) && !strings.ContainsAny(value, `/\\<>:"|?*`) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
