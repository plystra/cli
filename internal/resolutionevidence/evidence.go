// Package resolutionevidence derives immutable diagnostic evidence from the
// same normalized application model used by generation and assembly.
package resolutionevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
)

const schemaVersion = 1

// ErrBuild reports an absent or internally inconsistent normalized model.
var ErrBuild = errors.New("build resolution evidence")

// Evidence is one immutable deterministic identity for a selected normalized
// application model. Detailed module, candidate, decision, and source records
// are added by their owning resolution boundaries rather than inferred here.
type Evidence struct {
	generationAPI            string
	selectedModelDigest      string
	buildModelDigest         string
	selectedPluginCount      int
	canonicalCapabilityCount int
	requirementCount         int
	selectedProviderCount    int
	capabilityAliasCount     int
	canonicalJSON            []byte
	digest                   string
	prepared                 bool
}

type canonicalCounts struct {
	SelectedPlugins       int `json:"selected_plugins"`
	CanonicalCapabilities int `json:"canonical_capabilities"`
	Requirements          int `json:"requirements"`
	SelectedProviders     int `json:"selected_providers"`
	CapabilityAliases     int `json:"capability_aliases"`
}

type canonicalEvidence struct {
	Version             int             `json:"version"`
	GenerationAPI       string          `json:"generation_api"`
	SelectedModelDigest string          `json:"selected_model_digest"`
	BuildModelDigest    string          `json:"build_model_digest"`
	Counts              canonicalCounts `json:"counts"`
}

// Build validates one constructor-produced generation context and derives its
// bounded evidence identity without copying contracts, metadata, source paths,
// configuration values, or Secret references into the evidence document.
func Build(context generation.Context) (Evidence, error) {
	canonicalModel := context.CanonicalJSON()
	if len(canonicalModel) == 0 || !json.Valid(canonicalModel) || digest(canonicalModel) != context.Digest() {
		return Evidence{}, fmt.Errorf("%w: normalized application context is absent or has an invalid digest", ErrBuild)
	}
	input := Evidence{
		generationAPI:            context.APIVersion(),
		selectedModelDigest:      context.Digest(),
		buildModelDigest:         context.BuildModelDigest(),
		selectedPluginCount:      len(context.Plugins()),
		canonicalCapabilityCount: len(context.Capabilities()),
		requirementCount:         len(context.Requirements()),
		selectedProviderCount:    len(context.Providers()),
		capabilityAliasCount:     len(context.CapabilityAliases()),
		prepared:                 true,
	}
	if err := validate(input); err != nil {
		return Evidence{}, fmt.Errorf("%w: %v", ErrBuild, err)
	}
	canonical, err := encode(input)
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: encode canonical evidence: %v", ErrBuild, err)
	}
	input.canonicalJSON = canonical
	input.digest = digest(canonical)
	return input, nil
}

// Valid reports whether Build produced this internally consistent evidence.
func (e Evidence) Valid() bool {
	if !e.prepared || validate(e) != nil {
		return false
	}
	canonical, err := encode(e)
	return err == nil && bytes.Equal(e.canonicalJSON, canonical) && e.digest == digest(canonical)
}

// SchemaVersion returns the internal resolution-evidence schema version.
func (Evidence) SchemaVersion() int { return schemaVersion }

// GenerationAPIVersion returns the normalized generation-context API version.
func (e Evidence) GenerationAPIVersion() string { return e.generationAPI }

// SelectedModelDigest returns the identity of the normalized model including
// stable selected-configuration provenance.
func (e Evidence) SelectedModelDigest() string { return e.selectedModelDigest }

// BuildModelDigest returns the identity of normalized build state excluding
// configuration-document provenance.
func (e Evidence) BuildModelDigest() string { return e.buildModelDigest }

// SelectedPluginCount returns the number of selected Plugins.
func (e Evidence) SelectedPluginCount() int { return e.selectedPluginCount }

// CanonicalCapabilityCount returns the number of resolved canonical contracts.
func (e Evidence) CanonicalCapabilityCount() int { return e.canonicalCapabilityCount }

// RequirementCount returns the number of required canonical Capabilities.
func (e Evidence) RequirementCount() int { return e.requirementCount }

// SelectedProviderCount returns the number of selected ordinary Providers.
func (e Evidence) SelectedProviderCount() int { return e.selectedProviderCount }

// CapabilityAliasCount returns the number of final application Aliases.
func (e Evidence) CapabilityAliasCount() int { return e.capabilityAliasCount }

// CanonicalJSON returns a defensive copy of the deterministic bounded evidence.
func (e Evidence) CanonicalJSON() []byte { return append([]byte(nil), e.canonicalJSON...) }

// Digest returns the lowercase SHA-256 identity of CanonicalJSON.
func (e Evidence) Digest() string { return e.digest }

func validate(e Evidence) error {
	if e.generationAPI != generation.Version {
		return fmt.Errorf("generation API must be %q", generation.Version)
	}
	if !validDigest(e.selectedModelDigest) {
		return errors.New("selected-model digest is not a canonical SHA-256 digest")
	}
	if !validDigest(e.buildModelDigest) {
		return errors.New("build-model digest is not a canonical SHA-256 digest")
	}
	counts := []struct {
		name  string
		value int
	}{
		{name: "selected Plugin", value: e.selectedPluginCount},
		{name: "canonical Capability", value: e.canonicalCapabilityCount},
		{name: "requirement", value: e.requirementCount},
		{name: "selected Provider", value: e.selectedProviderCount},
		{name: "Capability Alias", value: e.capabilityAliasCount},
	}
	for _, count := range counts {
		if count.value < 0 {
			return fmt.Errorf("%s count must not be negative", count.name)
		}
	}
	if e.requirementCount > e.canonicalCapabilityCount {
		return errors.New("requirement count exceeds canonical Capability count")
	}
	if e.selectedProviderCount > e.requirementCount {
		return errors.New("selected Provider count exceeds requirement count")
	}
	return nil
}

func encode(e Evidence) ([]byte, error) {
	return json.Marshal(canonicalEvidence{
		Version:             schemaVersion,
		GenerationAPI:       e.generationAPI,
		SelectedModelDigest: e.selectedModelDigest,
		BuildModelDigest:    e.buildModelDigest,
		Counts: canonicalCounts{
			SelectedPlugins:       e.selectedPluginCount,
			CanonicalCapabilities: e.canonicalCapabilityCount,
			Requirements:          e.requirementCount,
			SelectedProviders:     e.selectedProviderCount,
			CapabilityAliases:     e.capabilityAliasCount,
		},
	})
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
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
