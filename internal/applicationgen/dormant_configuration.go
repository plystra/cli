package applicationgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/resolutionevidence"
)

const dormantConstructorConfigurationsDomain = "plystra.dormant-constructor-configurations/v1"

const (
	maximumDormantConstructorConfigurations = 4096
	maximumDormantConfigurationFields       = 65_536
	maximumDormantConfigurationPathBytes    = 16_384
)

type applicationManifestDormantConstructorConfiguration struct {
	Constructor              string                                         `json:"constructor"`
	ConstructorModulePath    string                                         `json:"constructor_module_path"`
	ConstructorModuleVersion string                                         `json:"constructor_module_version"`
	ConstructorSource        string                                         `json:"constructor_source"`
	ConfigurationPath        string                                         `json:"configuration_path"`
	Fields                   []applicationManifestDormantConfigurationField `json:"fields"`
}

type applicationManifestDormantConfigurationField struct {
	Path          string                                            `json:"path"`
	Digest        string                                            `json:"digest"`
	Summary       string                                            `json:"summary"`
	Removed       bool                                              `json:"removed"`
	Owner         string                                            `json:"owner"`
	Effective     bool                                              `json:"effective"`
	Contributions []applicationManifestDormantSelectionContribution `json:"contributions"`
}

// DormantConstructorConfiguration is one normalized constructor-keyed
// configuration subtree whose selected constructor is not active or reachable.
// It contains only redacted typed identities and composition provenance.
type DormantConstructorConfiguration struct {
	constructor              string
	constructorModulePath    string
	constructorModuleVersion string
	constructorSource        string
	configurationPath        string
	fields                   []DormantConfigurationField
}

func (c DormantConstructorConfiguration) Constructor() string { return c.constructor }
func (c DormantConstructorConfiguration) ConstructorModulePath() string {
	return c.constructorModulePath
}
func (c DormantConstructorConfiguration) ConstructorModuleVersion() string {
	return c.constructorModuleVersion
}
func (c DormantConstructorConfiguration) ConstructorSource() string { return c.constructorSource }
func (c DormantConstructorConfiguration) ConfigurationPath() string { return c.configurationPath }
func (c DormantConstructorConfiguration) Fields() []DormantConfigurationField {
	return cloneDormantConfigurationFields(c.fields)
}

// DormantConfigurationField is one redacted typed path in dormant constructor
// configuration, including all lower- and higher-precedence contributions.
type DormantConfigurationField struct {
	path          string
	digest        string
	summary       string
	removed       bool
	owner         string
	effective     bool
	contributions []DormantConfigurationContribution
}

func (f DormantConfigurationField) Path() string    { return f.path }
func (f DormantConfigurationField) Digest() string  { return f.digest }
func (f DormantConfigurationField) Summary() string { return f.summary }
func (f DormantConfigurationField) Removed() bool   { return f.removed }
func (f DormantConfigurationField) Owner() string   { return f.owner }
func (f DormantConfigurationField) Effective() bool { return f.effective }
func (f DormantConfigurationField) Contributions() []DormantConfigurationContribution {
	return cloneDormantConfigurationContributions(f.contributions)
}

// DormantConfigurationContribution is the shared redacted composition record
// used by a dormant configuration field.
type DormantConfigurationContribution = DormantSelectionContribution

// DormantConfigurationSource is one stable module-relative configuration
// location contributing to dormant constructor configuration.
type DormantConfigurationSource = DormantSelectionSource

// NewDormantConstructorConfiguration constructs manifest-safe dormant
// configuration provenance from already validated resolution evidence.
func NewDormantConstructorConfiguration(
	selection DormantImplementationSelection,
	fields []resolutionevidence.ConfigurationField,
) (DormantConstructorConfiguration, error) {
	if err := validateDormantImplementationSelection(selection); err != nil {
		return DormantConstructorConfiguration{}, fmt.Errorf("dormant selection identity: %w", err)
	}
	configurationPath := "config[" + strconv.Quote(selection.constructor) + "]"
	normalizedFields := make([]DormantConfigurationField, len(fields))
	for fieldIndex, field := range fields {
		contributions := field.Contributors()
		normalizedContributions := make([]DormantConfigurationContribution, len(contributions))
		for contributionIndex, contribution := range contributions {
			sources := contribution.Sources()
			normalizedSources := make([]DormantConfigurationSource, len(sources))
			for sourceIndex, source := range sources {
				normalizedSources[sourceIndex] = DormantConfigurationSource{
					module: source.Module(), path: source.Path(), kind: source.Kind(),
					line: source.Line(), column: source.Column(),
				}
			}
			sort.Slice(normalizedSources, func(left, right int) bool {
				return dormantSelectionSourceKey(normalizedSources[left]) < dormantSelectionSourceKey(normalizedSources[right])
			})
			normalizedContributions[contributionIndex] = DormantConfigurationContribution{
				owner: string(contribution.Owner()), precedence: contribution.Precedence(),
				digest: contribution.Digest(), summary: contribution.Summary(),
				removed: contribution.Removed(), effective: contribution.Effective(), sources: normalizedSources,
			}
		}
		sort.Slice(normalizedContributions, func(left, right int) bool {
			return dormantSelectionContributionKey(normalizedContributions[left]) < dormantSelectionContributionKey(normalizedContributions[right])
		})
		normalizedFields[fieldIndex] = DormantConfigurationField{
			path: field.Path(), digest: field.Digest(), summary: field.Summary(),
			removed: field.Removed(), owner: string(field.Owner()), effective: field.Effective(),
			contributions: normalizedContributions,
		}
	}
	sort.Slice(normalizedFields, func(left, right int) bool { return normalizedFields[left].path < normalizedFields[right].path })
	configuration := DormantConstructorConfiguration{
		constructor:              selection.constructor,
		constructorModulePath:    selection.constructorModulePath,
		constructorModuleVersion: selection.constructorModuleVersion,
		constructorSource:        selection.constructorSource,
		configurationPath:        configurationPath,
		fields:                   normalizedFields,
	}
	if err := validateDormantConstructorConfiguration(configuration); err != nil {
		return DormantConstructorConfiguration{}, err
	}
	return configuration, nil
}

func normalizeDormantConstructorConfigurations(values []DormantConstructorConfiguration) ([]DormantConstructorConfiguration, error) {
	result := cloneDormantConstructorConfigurations(values)
	if result == nil {
		result = []DormantConstructorConfiguration{}
	}
	for configurationIndex := range result {
		for fieldIndex := range result[configurationIndex].fields {
			field := &result[configurationIndex].fields[fieldIndex]
			for contributionIndex := range field.contributions {
				sort.Slice(field.contributions[contributionIndex].sources, func(left, right int) bool {
					return dormantSelectionSourceKey(field.contributions[contributionIndex].sources[left]) < dormantSelectionSourceKey(field.contributions[contributionIndex].sources[right])
				})
			}
			sort.Slice(field.contributions, func(left, right int) bool {
				return dormantSelectionContributionKey(field.contributions[left]) < dormantSelectionContributionKey(field.contributions[right])
			})
		}
		sort.Slice(result[configurationIndex].fields, func(left, right int) bool {
			return result[configurationIndex].fields[left].path < result[configurationIndex].fields[right].path
		})
		if err := validateDormantConstructorConfiguration(result[configurationIndex]); err != nil {
			return nil, fmt.Errorf("dormant constructor configuration %d: %w", configurationIndex, err)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].constructor < result[right].constructor })
	for index := 1; index < len(result); index++ {
		if result[index-1].constructor == result[index].constructor {
			return nil, fmt.Errorf("dormant constructor configuration %d repeats constructor %q", index, result[index].constructor)
		}
	}
	return result, nil
}

func validateDormantConstructorConfigurations(
	values []DormantConstructorConfiguration,
	selections []DormantImplementationSelection,
	activeConstructors map[string]struct{},
	mode, rootPath, selectedPath string,
	currentProjectPaths []string,
) error {
	if values == nil || len(values) > maximumDormantConstructorConfigurations {
		return fmt.Errorf("dormant constructor configurations must be an array with at most %d entries", maximumDormantConstructorConfigurations)
	}
	selectionsByConstructor := make(map[string]DormantImplementationSelection, len(selections))
	for _, selection := range selections {
		if existing, present := selectionsByConstructor[selection.constructor]; present {
			if existing.constructorModulePath != selection.constructorModulePath ||
				existing.constructorModuleVersion != selection.constructorModuleVersion ||
				existing.constructorSource != selection.constructorSource {
				return fmt.Errorf("dormant selections for constructor %s disagree on implementation identity", selection.constructor)
			}
			continue
		}
		selectionsByConstructor[selection.constructor] = selection
	}
	totalFields := 0
	previous := ""
	for index, value := range values {
		if err := validateDormantConstructorConfiguration(value); err != nil {
			return fmt.Errorf("dormant constructor configuration %d: %w", index, err)
		}
		if previous != "" && value.constructor <= previous {
			return errors.New("dormant constructor configurations must be unique and canonically ordered")
		}
		previous = value.constructor
		totalFields += len(value.fields)
		if totalFields > maximumDormantConfigurationFields {
			return fmt.Errorf("dormant constructor configurations exceed %d total fields", maximumDormantConfigurationFields)
		}
		selection, selected := selectionsByConstructor[value.constructor]
		if !selected {
			return fmt.Errorf("dormant constructor configuration %s has no dormant explicit selection", value.constructor)
		}
		if _, active := activeConstructors[value.constructor]; active {
			return fmt.Errorf("dormant constructor configuration %s duplicates active reachable constructor configuration", value.constructor)
		}
		if value.constructorModulePath != selection.constructorModulePath ||
			value.constructorModuleVersion != selection.constructorModuleVersion ||
			value.constructorSource != selection.constructorSource {
			return fmt.Errorf("dormant constructor configuration %s disagrees with its selection identity", value.constructor)
		}
		for fieldIndex, field := range value.fields {
			currentOwned := sortedContains(currentProjectPaths, field.path)
			hasMaintainedContribution := false
			for contributionIndex, contribution := range field.contributions {
				if !dormantOwnerAllowed(contribution.owner, mode) {
					return fmt.Errorf("dormant constructor configuration %s field %d contribution %d has an owner outside the selected configuration mode", value.constructor, fieldIndex, contributionIndex)
				}
				if contribution.owner == string(resolutionevidence.ConfigurationOwnerRoot) ||
					contribution.owner == string(resolutionevidence.ConfigurationOwnerExplicit) {
					hasMaintainedContribution = true
				}
				expectedPath := dormantSourcePath(contribution.owner, rootPath, selectedPath)
				for _, source := range contribution.sources {
					if source.path != expectedPath {
						return fmt.Errorf("dormant constructor configuration %s field %d source path %q does not match owner %q", value.constructor, fieldIndex, source.path, contribution.owner)
					}
				}
			}
			if currentOwned != hasMaintainedContribution {
				return fmt.Errorf("dormant constructor configuration %s field %s current-project ownership disagrees with maintained paths", value.constructor, field.path)
			}
		}
	}
	return nil
}

func validateDormantConstructorConfiguration(value DormantConstructorConfiguration) error {
	if err := validateDormantConstructorIdentity(
		value.constructor,
		value.constructorModulePath,
		value.constructorModuleVersion,
		value.constructorSource,
	); err != nil {
		return err
	}
	expectedPath := "config[" + strconv.Quote(value.constructor) + "]"
	if value.configurationPath != expectedPath {
		return fmt.Errorf("configuration path must be %q", expectedPath)
	}
	if len(value.fields) == 0 || len(value.fields) > maximumDormantConfigurationFields {
		return fmt.Errorf("configuration fields must contain between 1 and %d entries", maximumDormantConfigurationFields)
	}
	previous := ""
	for index, field := range value.fields {
		if !validDormantConfigurationFieldPath(field.path, value.configurationPath) {
			return fmt.Errorf("configuration field %d has an invalid path", index)
		}
		if previous != "" && field.path <= previous {
			return errors.New("configuration fields must be unique and canonically ordered")
		}
		previous = field.path
		if err := validateDormantConfigurationField(field); err != nil {
			return fmt.Errorf("configuration field %d: %w", index, err)
		}
	}
	if value.fields[0].path != value.configurationPath || !value.fields[0].effective {
		return errors.New("configuration fields must begin with one effective constructor root")
	}
	return nil
}

func validateDormantConfigurationField(value DormantConfigurationField) error {
	if value.effective {
		if !validSHA256(value.digest) || !validDormantConfigurationSummary(value.summary) || !validDormantOwner(value.owner) {
			return errors.New("effective field digest, summary, or owner is invalid")
		}
		if value.removed != (value.summary == string(applicationmeta.ConfigurationSummaryRemoval)) {
			return errors.New("effective field removal state is inconsistent")
		}
	} else if value.digest != "" || value.summary != "" || value.removed || value.owner != "" {
		return errors.New("suppressed field carries effective state")
	}
	if len(value.contributions) == 0 || len(value.contributions) > maximumDormantSelectionContributions {
		return fmt.Errorf("field contributions must contain between 1 and %d entries", maximumDormantSelectionContributions)
	}
	effective := 0
	previous := ""
	for index, contribution := range value.contributions {
		if err := validateDormantConfigurationContribution(contribution); err != nil {
			return fmt.Errorf("field contribution %d: %w", index, err)
		}
		key := dormantSelectionContributionKey(contribution)
		if previous != "" && key <= previous {
			return errors.New("field contributions must be unique and canonically ordered")
		}
		previous = key
		if contribution.effective {
			effective++
			if !value.effective || contribution.owner != value.owner || contribution.digest != value.digest ||
				contribution.summary != value.summary || contribution.removed != value.removed {
				return errors.New("effective field contribution disagrees with the field")
			}
		}
	}
	if value.effective && effective != 1 {
		return errors.New("effective field must contain exactly one effective contribution")
	}
	if !value.effective && effective != 0 {
		return errors.New("suppressed field cannot contain an effective contribution")
	}
	return nil
}

func validateDormantConfigurationContribution(value DormantConfigurationContribution) error {
	if !validDormantOwner(value.owner) || value.precedence != dormantOwnerPrecedence(value.owner) || !validSHA256(value.digest) {
		return errors.New("owner, precedence, or digest is invalid")
	}
	if !validDormantConfigurationSummary(value.summary) {
		return errors.New("summary is invalid")
	}
	if value.removed != (value.summary == string(applicationmeta.ConfigurationSummaryRemoval)) {
		return errors.New("removal state is inconsistent")
	}
	if len(value.sources) == 0 || len(value.sources) > maximumDormantSelectionSources {
		return fmt.Errorf("sources must contain between 1 and %d entries", maximumDormantSelectionSources)
	}
	previous := ""
	for index, source := range value.sources {
		if err := validateDormantSelectionSource(source, value.removed); err != nil {
			return fmt.Errorf("source %d: %w", index, err)
		}
		key := dormantSelectionSourceKey(source)
		if previous != "" && key <= previous {
			return errors.New("sources must be unique and canonically ordered")
		}
		previous = key
	}
	return nil
}

func validDormantConfigurationSummary(value string) bool {
	switch applicationmeta.ConfigurationDecisionSummary(value) {
	case applicationmeta.ConfigurationSummaryRemoval,
		applicationmeta.ConfigurationSummaryObject,
		applicationmeta.ConfigurationSummaryString,
		applicationmeta.ConfigurationSummaryBoolean,
		applicationmeta.ConfigurationSummaryDuration,
		applicationmeta.ConfigurationSummaryArray,
		applicationmeta.ConfigurationSummarySecret,
		applicationmeta.ConfigurationSummaryValue:
		return true
	default:
		return value == "redacted"
	}
}

func validDormantConfigurationFieldPath(value, root string) bool {
	if value == "" || len(value) > maximumDormantConfigurationPathBytes || !utf8.ValidString(value) || !strings.HasPrefix(value, root) {
		return false
	}
	rest := strings.TrimPrefix(value, root)
	for rest != "" {
		if len(rest) < 4 || rest[0] != '[' || rest[1] != '"' {
			return false
		}
		end := -1
		escaped := false
		for index := 2; index < len(rest); index++ {
			if escaped {
				escaped = false
				continue
			}
			switch rest[index] {
			case '\\':
				escaped = true
			case '"':
				end = index
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 || end+1 >= len(rest) || rest[end+1] != ']' {
			return false
		}
		quoted := rest[1 : end+1]
		segment, err := strconv.Unquote(quoted)
		if err != nil || segment == "" || strconv.Quote(segment) != quoted || !utf8.ValidString(segment) || strings.IndexFunc(segment, unicode.IsControl) >= 0 {
			return false
		}
		rest = rest[end+2:]
	}
	return true
}

func dormantConstructorConfigurationWires(values []DormantConstructorConfiguration) []applicationManifestDormantConstructorConfiguration {
	result := make([]applicationManifestDormantConstructorConfiguration, len(values))
	for configurationIndex, value := range values {
		fields := make([]applicationManifestDormantConfigurationField, len(value.fields))
		for fieldIndex, field := range value.fields {
			contributions := make([]applicationManifestDormantSelectionContribution, len(field.contributions))
			for contributionIndex, contribution := range field.contributions {
				sources := make([]applicationManifestDormantSelectionSource, len(contribution.sources))
				for sourceIndex, source := range contribution.sources {
					sources[sourceIndex] = applicationManifestDormantSelectionSource{
						Module: source.module, Path: source.path, Kind: source.kind,
						Line: source.line, Column: source.column,
					}
				}
				contributions[contributionIndex] = applicationManifestDormantSelectionContribution{
					Owner: contribution.owner, Precedence: contribution.precedence,
					Digest: contribution.digest, Summary: contribution.summary,
					Removed: contribution.removed, Effective: contribution.effective, Sources: sources,
				}
			}
			fields[fieldIndex] = applicationManifestDormantConfigurationField{
				Path: field.path, Digest: field.digest, Summary: field.summary,
				Removed: field.removed, Owner: field.owner, Effective: field.effective,
				Contributions: contributions,
			}
		}
		result[configurationIndex] = applicationManifestDormantConstructorConfiguration{
			Constructor:              value.constructor,
			ConstructorModulePath:    value.constructorModulePath,
			ConstructorModuleVersion: value.constructorModuleVersion,
			ConstructorSource:        value.constructorSource,
			ConfigurationPath:        value.configurationPath,
			Fields:                   fields,
		}
	}
	return result
}

func restoreDormantConstructorConfigurations(values []applicationManifestDormantConstructorConfiguration) []DormantConstructorConfiguration {
	if values == nil {
		return nil
	}
	result := make([]DormantConstructorConfiguration, len(values))
	for configurationIndex, value := range values {
		fields := make([]DormantConfigurationField, len(value.Fields))
		for fieldIndex, field := range value.Fields {
			contributions := make([]DormantConfigurationContribution, len(field.Contributions))
			for contributionIndex, contribution := range field.Contributions {
				sources := make([]DormantConfigurationSource, len(contribution.Sources))
				for sourceIndex, source := range contribution.Sources {
					sources[sourceIndex] = DormantConfigurationSource{
						module: source.Module, path: source.Path, kind: source.Kind,
						line: source.Line, column: source.Column,
					}
				}
				contributions[contributionIndex] = DormantConfigurationContribution{
					owner: contribution.Owner, precedence: contribution.Precedence,
					digest: contribution.Digest, summary: contribution.Summary,
					removed: contribution.Removed, effective: contribution.Effective, sources: sources,
				}
			}
			fields[fieldIndex] = DormantConfigurationField{
				path: field.Path, digest: field.Digest, summary: field.Summary,
				removed: field.Removed, owner: field.Owner, effective: field.Effective,
				contributions: contributions,
			}
		}
		result[configurationIndex] = DormantConstructorConfiguration{
			constructor:              value.Constructor,
			constructorModulePath:    value.ConstructorModulePath,
			constructorModuleVersion: value.ConstructorModuleVersion,
			constructorSource:        value.ConstructorSource,
			configurationPath:        value.ConfigurationPath,
			fields:                   fields,
		}
	}
	return result
}

func dormantConstructorConfigurationsDigest(values []DormantConstructorConfiguration) (string, error) {
	data, err := json.Marshal(dormantConstructorConfigurationWires(values))
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, value := range []string{dormantConstructorConfigurationsDomain, string(data)} {
		_, _ = hash.Write([]byte(strconv.Itoa(len(value))))
		_, _ = hash.Write([]byte(":"))
		_, _ = hash.Write([]byte(value))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func cloneDormantConstructorConfigurations(values []DormantConstructorConfiguration) []DormantConstructorConfiguration {
	if values == nil {
		return nil
	}
	result := make([]DormantConstructorConfiguration, len(values))
	copy(result, values)
	for index := range result {
		result[index].fields = cloneDormantConfigurationFields(values[index].fields)
	}
	return result
}

func cloneDormantConfigurationFields(values []DormantConfigurationField) []DormantConfigurationField {
	result := append([]DormantConfigurationField(nil), values...)
	for index := range result {
		result[index].contributions = cloneDormantConfigurationContributions(values[index].contributions)
	}
	return result
}

func cloneDormantConfigurationContributions(values []DormantConfigurationContribution) []DormantConfigurationContribution {
	result := append([]DormantConfigurationContribution(nil), values...)
	for index := range result {
		result[index].sources = append([]DormantConfigurationSource(nil), values[index].sources...)
	}
	return result
}
