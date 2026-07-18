package applicationgen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/plystra/cli/internal/applicationmeta"
)

const applicationManifestConfigurationVersion = 1

type applicationManifestProvenance struct {
	Path    string   `json:"path"`
	Digest  string   `json:"digest"`
	Removed bool     `json:"removed,omitempty"`
	Sources []string `json:"sources"`
}

type applicationManifestDocumentReference struct {
	Path string `json:"path"`
}

type applicationManifestConfiguration struct {
	Version                     int                                  `json:"version"`
	Mode                        string                               `json:"mode"`
	Root                        applicationManifestDocumentReference `json:"root"`
	DependencyCompositionDigest string                               `json:"dependency_composition_digest"`
	DependencyBaseline          []applicationManifestProvenance      `json:"dependency_baseline"`
}

type applicationManifestDocument struct {
	CapabilityAliases json.RawMessage                  `json:"capability_aliases"`
	Configuration     applicationManifestConfiguration `json:"configuration"`
}

// RenderManifest combines the normalized Alias map with non-secret typed
// dependency-configuration provenance. It never serializes configuration
// values or Secret reference targets.
func RenderManifest(aliasJSON []byte, composition applicationmeta.Composition) ([]byte, error) {
	if !composition.Valid() {
		return nil, fmt.Errorf("%w: dependency configuration composition is absent or invalid", ErrResolution)
	}
	var aliases struct {
		CapabilityAliases json.RawMessage `json:"capability_aliases"`
	}
	if err := json.Unmarshal(aliasJSON, &aliases); err != nil || !jsonArray(aliases.CapabilityAliases) {
		return nil, fmt.Errorf("%w: final Alias manifest is invalid", ErrResolution)
	}
	baseline := composition.DependencyBaseline()
	records := baseline.Records()
	serialized := make([]applicationManifestProvenance, len(records))
	for index, record := range records {
		serialized[index] = applicationManifestProvenance{
			Path:    record.Path,
			Digest:  record.Digest,
			Removed: record.Removed,
			Sources: append([]string(nil), record.Sources...),
		}
	}
	document := applicationManifestDocument{
		CapabilityAliases: aliases.CapabilityAliases,
		Configuration: applicationManifestConfiguration{
			Version:                     applicationManifestConfigurationVersion,
			Mode:                        "default",
			Root:                        applicationManifestDocumentReference{Path: rootConfigurationPath},
			DependencyCompositionDigest: baseline.Digest(),
			DependencyBaseline:          serialized,
		},
	}
	data, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// DecodeDependencyBaseline restores and validates the prior non-secret
// dependency baseline from one generated application manifest.
func DecodeDependencyBaseline(data []byte) (applicationmeta.DependencyBaseline, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document applicationManifestDocument
	if err := decoder.Decode(&document); err != nil {
		return applicationmeta.DependencyBaseline{}, fmt.Errorf("decode generated application manifest: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return applicationmeta.DependencyBaseline{}, errors.New("generated application manifest contains trailing JSON")
	}
	if !jsonArray(document.CapabilityAliases) {
		return applicationmeta.DependencyBaseline{}, errors.New("generated application manifest capability_aliases must be an array")
	}
	configuration := document.Configuration
	if configuration.Version != applicationManifestConfigurationVersion {
		return applicationmeta.DependencyBaseline{}, fmt.Errorf("generated application manifest configuration must use version %d", applicationManifestConfigurationVersion)
	}
	if configuration.Mode != "default" || configuration.Root.Path != rootConfigurationPath {
		return applicationmeta.DependencyBaseline{}, errors.New("generated application manifest configuration selection is not the default root")
	}
	if configuration.DependencyBaseline == nil {
		return applicationmeta.DependencyBaseline{}, errors.New("generated application manifest configuration dependency_baseline must be an array")
	}
	records := make([]applicationmeta.BaselineRecord, len(configuration.DependencyBaseline))
	for index, record := range configuration.DependencyBaseline {
		records[index] = applicationmeta.BaselineRecord{
			Path:    record.Path,
			Digest:  record.Digest,
			Removed: record.Removed,
			Sources: append([]string(nil), record.Sources...),
		}
	}
	baseline, err := applicationmeta.RestoreDependencyBaseline(configuration.DependencyCompositionDigest, records)
	if err != nil {
		return applicationmeta.DependencyBaseline{}, fmt.Errorf("generated application manifest dependency baseline: %w", err)
	}
	return baseline, nil
}

func jsonArray(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) >= 2 && trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']'
}
