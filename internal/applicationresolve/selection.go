package applicationresolve

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/applicationgen"
)

const (
	configurationModeDefault  = applicationgen.ConfigurationModeDefault
	configurationModeExplicit = applicationgen.ConfigurationModeExplicit
	configurationEnvironment  = "PLYSTRA_CONFIG"
)

// ConfigurationSelection identifies the one current-project document used by
// a resolution. Paths are stable project-relative slash paths and digests are
// computed from normalized YAML without retaining document values.
type ConfigurationSelection struct {
	mode   string
	path   string
	digest string
}

// Mode returns default or explicit-config.
func (s ConfigurationSelection) Mode() string { return s.mode }

// Path returns the stable Project-relative current-project document path.
func (s ConfigurationSelection) Path() string { return s.path }

// Digest returns the normalized selected-document digest.
func (s ConfigurationSelection) Digest() string { return s.digest }

func (s ConfigurationSelection) explicit() bool { return s.mode == configurationModeExplicit }

type configurationSelector struct {
	mode string
	path string
}

func resolveConfigurationSelector(moduleRoot, explicit string, environment []string) (configurationSelector, error) {
	selected := explicit
	source := "--config"
	if selected == "" {
		value, exists, err := environmentValue(environment, configurationEnvironment)
		if err != nil {
			return configurationSelector{}, err
		}
		if !exists {
			return configurationSelector{mode: configurationModeDefault, path: applicationManifestName}, nil
		}
		selected = value
		source = configurationEnvironment
	}
	if strings.TrimSpace(selected) == "" {
		return configurationSelector{}, fmt.Errorf("%s selects an empty configuration path", source)
	}
	path, err := projectRelativeConfigurationPath(moduleRoot, selected)
	if err != nil {
		return configurationSelector{}, err
	}
	return configurationSelector{mode: configurationModeExplicit, path: path}, nil
}

func environmentValue(environment []string, name string) (string, bool, error) {
	var value string
	found := false
	for _, entry := range environment {
		key, current, exists := strings.Cut(entry, "=")
		if !exists || key != name {
			continue
		}
		if found {
			return "", false, fmt.Errorf("environment contains %s more than once", name)
		}
		value = current
		found = true
	}
	return value, found, nil
}

func projectRelativeConfigurationPath(moduleRoot, selected string) (string, error) {
	if strings.IndexByte(selected, 0) >= 0 {
		return "", errors.New("selected configuration path contains a NUL byte")
	}
	root, err := filepath.Abs(moduleRoot)
	if err != nil {
		return "", fmt.Errorf("resolve Project root: %w", err)
	}
	candidate := selected
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve selected configuration path %q: %w", selected, err)
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", fmt.Errorf("locate selected configuration path %q within the Project: %w", selected, err)
	}
	clean := filepath.Clean(relative)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("selected configuration path %q must identify a file within the Project root", selected)
	}
	return filepath.ToSlash(clean), nil
}
