package applicationresolve

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/plystra/cli/internal/applicationgen"
)

const (
	configurationModeDefault     = applicationgen.ConfigurationModeDefault
	configurationModeEnvironment = applicationgen.ConfigurationModeEnvironment
	configurationModeExplicit    = applicationgen.ConfigurationModeExplicit
	configurationPathEnvironment = "PLYSTRA_CONFIG"
	configurationNameEnvironment = "PLYSTRA_ENV"
)

// ConfigurationSelection identifies the selected current-project mode and its
// selected document. Environment mode selects one overlay above the mandatory
// root document. Paths are stable project-relative slash paths and digests are
// computed from normalized YAML without retaining document values.
type ConfigurationSelection struct {
	mode        string
	path        string
	environment string
	digest      string
}

// Mode returns default, environment, or explicit-config.
func (s ConfigurationSelection) Mode() string { return s.mode }

// Path returns the stable Project-relative current-project document path.
func (s ConfigurationSelection) Path() string { return s.path }

// Environment returns the selected environment name in environment mode.
func (s ConfigurationSelection) Environment() string { return s.environment }

// Digest returns the normalized selected-document digest.
func (s ConfigurationSelection) Digest() string { return s.digest }

// ConfigurationTarget is one validated selected current-project document and
// the exact bounded filesystem snapshot from which a targeted mutation may be
// planned. Environment targets are sparse overlays; default and explicit
// targets are complete current-project documents.
type ConfigurationTarget struct {
	selection ConfigurationSelection
	snapshot  ManifestSnapshot
}

// Selection returns the normalized selector and document digest.
func (t ConfigurationTarget) Selection() ConfigurationSelection { return t.selection }

// Snapshot returns the immutable selected-document snapshot. Snapshot.Data
// returns defensive bytes for an atomic write precondition.
func (t ConfigurationTarget) Snapshot() ManifestSnapshot { return t.snapshot }

// EnvironmentOverlay reports whether the selected document must be parsed and
// edited with sparse environment-overlay semantics.
func (t ConfigurationTarget) EnvironmentOverlay() bool {
	return t.selection.mode == configurationModeEnvironment
}

// SelectConfigurationTarget applies the same explicit and ambient selector
// rules as complete application resolution, safely reads the selected file,
// validates it for its mode, and computes its normalized non-secret digest.
// moduleRoot must already be the detected Plystra Project root.
func SelectConfigurationTarget(moduleRoot, explicitConfiguration, explicitEnvironment string, environment []string) (ConfigurationTarget, error) {
	selector, err := resolveConfigurationSelector(moduleRoot, explicitConfiguration, explicitEnvironment, environment)
	if err != nil {
		return ConfigurationTarget{}, err
	}
	var snapshot ManifestSnapshot
	if selector.mode == configurationModeEnvironment {
		snapshot, _, err = loadEnvironmentOverlay(moduleRoot, selector.path)
	} else {
		snapshot, _, err = loadConfiguration(moduleRoot, selector.path)
	}
	if err != nil {
		return ConfigurationTarget{}, err
	}
	digestFunction := applicationgen.ConfigurationDigest
	if selector.mode == configurationModeEnvironment {
		digestFunction = applicationgen.EnvironmentOverlayDigest
	}
	digest, err := digestFunction(snapshot.Data())
	if err != nil {
		return ConfigurationTarget{}, fmt.Errorf("digest selected configuration %s: %w", selector.path, err)
	}
	return ConfigurationTarget{
		selection: ConfigurationSelection{
			mode:        selector.mode,
			path:        selector.path,
			environment: selector.environment,
			digest:      digest,
		},
		snapshot: snapshot,
	}, nil
}

type configurationSelector struct {
	mode        string
	path        string
	environment string
}

func resolveConfigurationSelector(moduleRoot, explicitConfiguration, explicitEnvironment string, environment []string) (configurationSelector, error) {
	if explicitConfiguration != "" && explicitEnvironment != "" {
		return configurationSelector{}, errors.New("--config and --env cannot be used together")
	}
	if explicitConfiguration != "" {
		path, err := selectedConfigurationPath(moduleRoot, explicitConfiguration, "--config")
		if err != nil {
			return configurationSelector{}, err
		}
		return configurationSelector{mode: configurationModeExplicit, path: path}, nil
	}
	if explicitEnvironment != "" {
		return selectedEnvironment(explicitEnvironment, "--env")
	}

	configurationPath, hasConfiguration, err := environmentValue(environment, configurationPathEnvironment)
	if err != nil {
		return configurationSelector{}, err
	}
	environmentName, hasEnvironment, err := environmentValue(environment, configurationNameEnvironment)
	if err != nil {
		return configurationSelector{}, err
	}
	if hasConfiguration && hasEnvironment {
		return configurationSelector{}, fmt.Errorf("%s and %s cannot be used together", configurationPathEnvironment, configurationNameEnvironment)
	}
	if hasConfiguration {
		path, err := selectedConfigurationPath(moduleRoot, configurationPath, configurationPathEnvironment)
		if err != nil {
			return configurationSelector{}, err
		}
		return configurationSelector{mode: configurationModeExplicit, path: path}, nil
	}
	if hasEnvironment {
		return selectedEnvironment(environmentName, configurationNameEnvironment)
	}
	return configurationSelector{mode: configurationModeDefault, path: applicationManifestName}, nil
}

func selectedConfigurationPath(moduleRoot, selected, source string) (string, error) {
	if strings.TrimSpace(selected) == "" {
		return "", fmt.Errorf("%s selects an empty configuration path", source)
	}
	path, err := projectRelativeConfigurationPath(moduleRoot, selected)
	if err != nil {
		return "", err
	}
	return path, nil
}

func selectedEnvironment(value, source string) (configurationSelector, error) {
	if strings.TrimSpace(value) == "" {
		return configurationSelector{}, fmt.Errorf("%s selects an empty environment name", source)
	}
	if len(value) > 200 {
		return configurationSelector{}, fmt.Errorf("%s environment name exceeds 200 bytes", source)
	}
	if value == "." || value == ".." || filepath.IsAbs(value) || filepath.VolumeName(value) != "" ||
		strings.ContainsAny(value, `/\\<>:"|?*`) || strings.IndexFunc(value, unicode.IsControl) >= 0 ||
		filepath.Clean(value) != value {
		return configurationSelector{}, fmt.Errorf("%s environment %q must be one safe filename component", source, value)
	}
	return configurationSelector{
		mode:        configurationModeEnvironment,
		path:        "plystra." + value + ".yaml",
		environment: value,
	}, nil
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
