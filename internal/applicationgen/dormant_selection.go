package applicationgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	pathpkg "path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/resolutionevidence"
	"golang.org/x/mod/module"
)

const dormantImplementationSelectionsDomain = "plystra.dormant-implementation-selections/v1"

const (
	maximumDormantImplementationSelections = 4096
	maximumDormantSelectionContributions   = 4096
	maximumDormantSelectionSources         = 4096
)

type applicationManifestDormantImplementationSelection struct {
	InterfaceID              string                                            `json:"interface_id"`
	Constructor              string                                            `json:"constructor"`
	ConstructorModulePath    string                                            `json:"constructor_module_path"`
	ConstructorModuleVersion string                                            `json:"constructor_module_version"`
	ConstructorSource        string                                            `json:"constructor_source"`
	SelectionPath            string                                            `json:"selection_path"`
	SelectionDigest          string                                            `json:"selection_digest"`
	SelectionOwner           string                                            `json:"selection_owner"`
	Contributions            []applicationManifestDormantSelectionContribution `json:"contributions"`
}

type applicationManifestDormantSelectionContribution struct {
	Owner      string                                      `json:"owner"`
	Precedence int                                         `json:"precedence"`
	Digest     string                                      `json:"digest"`
	Summary    string                                      `json:"summary"`
	Removed    bool                                        `json:"removed,omitempty"`
	Effective  bool                                        `json:"effective"`
	Sources    []applicationManifestDormantSelectionSource `json:"sources"`
}

type applicationManifestDormantSelectionSource struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// DormantImplementationSelection is one exact validated interfaces.use choice
// that is authored and effective but outside the reachable executable graph.
type DormantImplementationSelection struct {
	interfaceID              string
	constructor              string
	constructorModulePath    string
	constructorModuleVersion string
	constructorSource        string
	selectionPath            string
	selectionDigest          string
	selectionOwner           string
	contributions            []DormantSelectionContribution
}

func (s DormantImplementationSelection) InterfaceID() string { return s.interfaceID }
func (s DormantImplementationSelection) Constructor() string { return s.constructor }
func (s DormantImplementationSelection) ConstructorModulePath() string {
	return s.constructorModulePath
}
func (s DormantImplementationSelection) ConstructorModuleVersion() string {
	return s.constructorModuleVersion
}
func (s DormantImplementationSelection) ConstructorSource() string { return s.constructorSource }
func (s DormantImplementationSelection) SelectionPath() string     { return s.selectionPath }
func (s DormantImplementationSelection) SelectionDigest() string   { return s.selectionDigest }
func (s DormantImplementationSelection) SelectionOwner() string    { return s.selectionOwner }
func (s DormantImplementationSelection) Contributions() []DormantSelectionContribution {
	return cloneDormantSelectionContributions(s.contributions)
}

// DormantSelectionContribution is one normalized authored value or removal
// that participated in composing an effective dormant interfaces.use choice.
type DormantSelectionContribution struct {
	owner      string
	precedence int
	digest     string
	summary    string
	removed    bool
	effective  bool
	sources    []DormantSelectionSource
}

func (c DormantSelectionContribution) Owner() string   { return c.owner }
func (c DormantSelectionContribution) Precedence() int { return c.precedence }
func (c DormantSelectionContribution) Digest() string  { return c.digest }
func (c DormantSelectionContribution) Summary() string { return c.summary }
func (c DormantSelectionContribution) Removed() bool   { return c.removed }
func (c DormantSelectionContribution) Effective() bool { return c.effective }
func (c DormantSelectionContribution) Sources() []DormantSelectionSource {
	return append([]DormantSelectionSource(nil), c.sources...)
}

// DormantSelectionSource is one stable module-relative configuration location.
type DormantSelectionSource struct {
	module string
	path   string
	kind   string
	line   int
	column int
}

func (s DormantSelectionSource) Module() string { return s.module }
func (s DormantSelectionSource) Path() string   { return s.path }
func (s DormantSelectionSource) Kind() string   { return s.kind }
func (s DormantSelectionSource) Line() int      { return s.line }
func (s DormantSelectionSource) Column() int    { return s.column }

// NewDormantImplementationSelection constructs one manifest-safe dormant
// choice from the same typed implementation and configuration evidence used by
// application resolution. Runtime configuration values never enter this API.
func NewDormantImplementationSelection(
	choice applicationmeta.ImplementationChoice,
	implementation implementationinventory.Implementation,
	field resolutionevidence.ConfigurationField,
) (DormantImplementationSelection, error) {
	moduleVersion := implementation.ModuleVersion()
	if moduleVersion == "" {
		moduleVersion = "local"
	}
	contributions := field.Contributors()
	normalized := make([]DormantSelectionContribution, len(contributions))
	for index, contribution := range contributions {
		sources := contribution.Sources()
		normalizedSources := make([]DormantSelectionSource, len(sources))
		for sourceIndex, source := range sources {
			normalizedSources[sourceIndex] = DormantSelectionSource{
				module: source.Module(), path: source.Path(), kind: source.Kind(),
				line: source.Line(), column: source.Column(),
			}
		}
		sort.Slice(normalizedSources, func(left, right int) bool {
			return dormantSelectionSourceKey(normalizedSources[left]) < dormantSelectionSourceKey(normalizedSources[right])
		})
		normalized[index] = DormantSelectionContribution{
			owner: string(contribution.Owner()), precedence: contribution.Precedence(),
			digest: contribution.Digest(), summary: contribution.Summary(),
			removed: contribution.Removed(), effective: contribution.Effective(), sources: normalizedSources,
		}
	}
	sort.Slice(normalized, func(left, right int) bool {
		return dormantSelectionContributionKey(normalized[left]) < dormantSelectionContributionKey(normalized[right])
	})
	selection := DormantImplementationSelection{
		interfaceID: choice.InterfaceID().String(), constructor: choice.Constructor().String(),
		constructorModulePath: implementation.ModulePath(), constructorModuleVersion: moduleVersion,
		constructorSource: implementation.Source(), selectionPath: field.Path(),
		selectionDigest: field.Digest(), selectionOwner: string(field.Owner()), contributions: normalized,
	}
	if implementation.Symbol() != choice.Constructor() {
		return DormantImplementationSelection{}, errors.New("dormant selection constructor does not match its implementation")
	}
	implemented := false
	for _, declared := range implementation.Declaration().ImplementedInterfaces() {
		if declared.ID() == choice.InterfaceID() {
			implemented = true
			break
		}
	}
	if !implemented {
		return DormantImplementationSelection{}, errors.New("dormant selection constructor does not declare its Interface")
	}
	if err := validateDormantImplementationSelection(selection); err != nil {
		return DormantImplementationSelection{}, err
	}
	return selection, nil
}

func normalizeDormantImplementationSelections(values []DormantImplementationSelection) ([]DormantImplementationSelection, error) {
	result := cloneDormantImplementationSelections(values)
	if result == nil {
		result = []DormantImplementationSelection{}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].interfaceID < result[right].interfaceID })
	for index := range result {
		if err := validateDormantImplementationSelection(result[index]); err != nil {
			return nil, fmt.Errorf("dormant implementation selection %d: %w", index, err)
		}
		if index > 0 && result[index-1].interfaceID == result[index].interfaceID {
			return nil, fmt.Errorf("dormant implementation selection %d repeats Interface %q", index, result[index].interfaceID)
		}
	}
	return result, nil
}

func validateDormantImplementationSelections(
	values []DormantImplementationSelection,
	mode, rootPath, selectedPath string,
	currentProjectPaths []string,
) error {
	if values == nil || len(values) > maximumDormantImplementationSelections {
		return fmt.Errorf("dormant implementation selections must be an array with at most %d entries", maximumDormantImplementationSelections)
	}
	for index, value := range values {
		if err := validateDormantImplementationSelection(value); err != nil {
			return fmt.Errorf("dormant implementation selection %d: %w", index, err)
		}
		if index > 0 && values[index-1].interfaceID >= value.interfaceID {
			return errors.New("dormant implementation selections must be unique and canonically ordered")
		}
		currentOwned := sortedContains(currentProjectPaths, value.selectionPath)
		switch resolutionevidence.ConfigurationOwner(value.selectionOwner) {
		case resolutionevidence.ConfigurationOwnerDependency:
			if currentOwned {
				return fmt.Errorf("dormant implementation selection %s dependency ownership disagrees with current-project paths", value.interfaceID)
			}
		case resolutionevidence.ConfigurationOwnerRoot, resolutionevidence.ConfigurationOwnerExplicit:
			if !currentOwned {
				return fmt.Errorf("dormant implementation selection %s current-project ownership is absent from maintained paths", value.interfaceID)
			}
		case resolutionevidence.ConfigurationOwnerEnvironment:
			// Environment-overlay paths are selected-layer provenance, not shared
			// root-maintenance ownership. Their exact owner and source path are
			// validated below without polluting retained root baseline state.
		}
		for contributionIndex, contribution := range value.contributions {
			if !dormantOwnerAllowed(contribution.owner, mode) {
				return fmt.Errorf("dormant implementation selection %s contribution %d has an owner outside the selected configuration mode", value.interfaceID, contributionIndex)
			}
			expectedPath := dormantSourcePath(contribution.owner, rootPath, selectedPath)
			for _, source := range contribution.sources {
				if source.path != expectedPath {
					return fmt.Errorf("dormant implementation selection %s contribution source path %q does not match owner %q", value.interfaceID, source.path, contribution.owner)
				}
			}
		}
	}
	return nil
}

func validateDormantImplementationSelection(value DormantImplementationSelection) error {
	identifier, err := interfaceid.Parse(value.interfaceID)
	if err != nil || identifier.String() != value.interfaceID || strings.HasPrefix(identifier.Name(), "kernel.") {
		return fmt.Errorf("interface ID %q is not an ordinary canonical Interface", value.interfaceID)
	}
	symbol, err := constructorsymbol.Parse(value.constructor)
	if err != nil || symbol.String() != value.constructor {
		return fmt.Errorf("constructor %q is not canonical", value.constructor)
	}
	if err := module.CheckPath(value.constructorModulePath); err != nil || symbol.PackagePath() != value.constructorModulePath && !strings.HasPrefix(symbol.PackagePath(), value.constructorModulePath+"/") {
		return errors.New("constructor package is outside its owning module")
	}
	if !validDormantModuleVersion(value.constructorModulePath, value.constructorModuleVersion) {
		return errors.New("constructor module version is invalid")
	}
	if !validDormantConstructorSource(value.constructorSource, value.constructorModulePath, value.constructorModuleVersion, symbol.PackagePath()) {
		return errors.New("constructor source is not stable module-qualified provenance")
	}
	expectedPath := "interfaces.use[" + strconv.Quote(value.interfaceID) + "]"
	if value.selectionPath != expectedPath {
		return fmt.Errorf("selection path must be %q", expectedPath)
	}
	if value.selectionDigest != dormantImplementationChoiceDigest(value.interfaceID, value.constructor) {
		return errors.New("selection digest does not match the exact Interface and constructor")
	}
	if !validDormantOwner(value.selectionOwner) {
		return errors.New("selection owner is invalid")
	}
	if len(value.contributions) == 0 || len(value.contributions) > maximumDormantSelectionContributions {
		return fmt.Errorf("selection contributions must contain between 1 and %d entries", maximumDormantSelectionContributions)
	}
	effective := 0
	previous := ""
	for index, contribution := range value.contributions {
		if err := validateDormantSelectionContribution(contribution); err != nil {
			return fmt.Errorf("selection contribution %d: %w", index, err)
		}
		key := dormantSelectionContributionKey(contribution)
		if previous != "" && key <= previous {
			return errors.New("selection contributions must be unique and canonically ordered")
		}
		previous = key
		if contribution.effective {
			effective++
			if contribution.removed || contribution.owner != value.selectionOwner || contribution.digest != value.selectionDigest {
				return errors.New("effective selection contribution disagrees with the dormant choice")
			}
		}
	}
	if effective != 1 {
		return errors.New("selection contributions must contain exactly one effective value")
	}
	return nil
}

func validateDormantSelectionContribution(value DormantSelectionContribution) error {
	if !validDormantOwner(value.owner) || value.precedence != dormantOwnerPrecedence(value.owner) || !validSHA256(value.digest) {
		return errors.New("owner, precedence, or digest is invalid")
	}
	if value.removed {
		if value.summary != string(applicationmeta.ConfigurationSummaryRemoval) || value.effective {
			return errors.New("removal contribution has inconsistent summary or effective state")
		}
	} else if value.summary != string(applicationmeta.ConfigurationSummaryImplementation) && value.summary != "redacted" {
		return errors.New("value contribution summary is invalid")
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

func validateDormantSelectionSource(value DormantSelectionSource, removed bool) error {
	if err := module.CheckPath(value.module); err != nil || !safeDormantSelectionSourcePath(value.path) || value.line != 1 || value.column != 1 {
		return errors.New("module or stable Project-relative location is invalid")
	}
	wantKind := "configuration-value"
	if removed {
		wantKind = "configuration-removal"
	}
	if value.kind != wantKind {
		return errors.New("kind disagrees with the contribution removal state")
	}
	return nil
}

func dormantImplementationSelectionWires(values []DormantImplementationSelection) []applicationManifestDormantImplementationSelection {
	result := make([]applicationManifestDormantImplementationSelection, len(values))
	for index, value := range values {
		contributions := make([]applicationManifestDormantSelectionContribution, len(value.contributions))
		for contributionIndex, contribution := range value.contributions {
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
		result[index] = applicationManifestDormantImplementationSelection{
			InterfaceID: value.interfaceID, Constructor: value.constructor,
			ConstructorModulePath:    value.constructorModulePath,
			ConstructorModuleVersion: value.constructorModuleVersion,
			ConstructorSource:        value.constructorSource,
			SelectionPath:            value.selectionPath, SelectionDigest: value.selectionDigest,
			SelectionOwner: value.selectionOwner, Contributions: contributions,
		}
	}
	return result
}

func restoreDormantImplementationSelections(values []applicationManifestDormantImplementationSelection) []DormantImplementationSelection {
	if values == nil {
		return nil
	}
	result := make([]DormantImplementationSelection, len(values))
	for index, value := range values {
		contributions := make([]DormantSelectionContribution, len(value.Contributions))
		for contributionIndex, contribution := range value.Contributions {
			sources := make([]DormantSelectionSource, len(contribution.Sources))
			for sourceIndex, source := range contribution.Sources {
				sources[sourceIndex] = DormantSelectionSource{
					module: source.Module, path: source.Path, kind: source.Kind,
					line: source.Line, column: source.Column,
				}
			}
			contributions[contributionIndex] = DormantSelectionContribution{
				owner: contribution.Owner, precedence: contribution.Precedence,
				digest: contribution.Digest, summary: contribution.Summary,
				removed: contribution.Removed, effective: contribution.Effective, sources: sources,
			}
		}
		result[index] = DormantImplementationSelection{
			interfaceID: value.InterfaceID, constructor: value.Constructor,
			constructorModulePath:    value.ConstructorModulePath,
			constructorModuleVersion: value.ConstructorModuleVersion,
			constructorSource:        value.ConstructorSource,
			selectionPath:            value.SelectionPath, selectionDigest: value.SelectionDigest,
			selectionOwner: value.SelectionOwner, contributions: contributions,
		}
	}
	return result
}

func dormantImplementationSelectionsDigest(values []DormantImplementationSelection) (string, error) {
	data, err := json.Marshal(dormantImplementationSelectionWires(values))
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	for _, value := range []string{dormantImplementationSelectionsDomain, string(data)} {
		_, _ = hash.Write([]byte(strconv.Itoa(len(value))))
		_, _ = hash.Write([]byte(":"))
		_, _ = hash.Write([]byte(value))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func dormantImplementationChoiceDigest(interfaceID, constructor string) string {
	hash := sha256.New()
	for _, value := range []string{"interfaces.use", interfaceID, constructor} {
		_, _ = hash.Write([]byte(strconv.Itoa(len(value))))
		_, _ = hash.Write([]byte(":"))
		_, _ = hash.Write([]byte(value))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func dormantOwnerPrecedence(owner string) int {
	switch resolutionevidence.ConfigurationOwner(owner) {
	case resolutionevidence.ConfigurationOwnerDependency:
		return 1
	case resolutionevidence.ConfigurationOwnerRoot, resolutionevidence.ConfigurationOwnerExplicit:
		return 2
	case resolutionevidence.ConfigurationOwnerEnvironment:
		return 3
	default:
		return 0
	}
}

func validDormantOwner(owner string) bool { return dormantOwnerPrecedence(owner) != 0 }

func dormantOwnerAllowed(owner, mode string) bool {
	switch resolutionevidence.ConfigurationOwner(owner) {
	case resolutionevidence.ConfigurationOwnerDependency:
		return true
	case resolutionevidence.ConfigurationOwnerRoot:
		return mode == ConfigurationModeDefault || mode == ConfigurationModeEnvironment
	case resolutionevidence.ConfigurationOwnerEnvironment:
		return mode == ConfigurationModeEnvironment
	case resolutionevidence.ConfigurationOwnerExplicit:
		return mode == ConfigurationModeExplicit
	default:
		return false
	}
}

func dormantSourcePath(owner, rootPath, selectedPath string) string {
	switch resolutionevidence.ConfigurationOwner(owner) {
	case resolutionevidence.ConfigurationOwnerDependency, resolutionevidence.ConfigurationOwnerRoot:
		return rootPath
	case resolutionevidence.ConfigurationOwnerEnvironment, resolutionevidence.ConfigurationOwnerExplicit:
		return selectedPath
	default:
		return ""
	}
}

func validDormantModuleVersion(modulePath, version string) bool {
	return version == "local" || version != "" && len(version) <= 1024 && module.Check(modulePath, version) == nil
}

func validDormantConstructorSource(value, modulePath, moduleVersion, packagePath string) bool {
	prefix := modulePath + "@" + moduleVersion + "/"
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) ||
		!strings.HasPrefix(value, prefix) || strings.Contains(value, "\\") ||
		strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	location := strings.TrimPrefix(value, prefix)
	columnSeparator := strings.LastIndexByte(location, ':')
	if columnSeparator <= 0 || columnSeparator == len(location)-1 {
		return false
	}
	lineLocation := location[:columnSeparator]
	lineSeparator := strings.LastIndexByte(lineLocation, ':')
	if lineSeparator <= 0 || lineSeparator == len(lineLocation)-1 {
		return false
	}
	sourcePath := lineLocation[:lineSeparator]
	line := lineLocation[lineSeparator+1:]
	column := location[columnSeparator+1:]
	if !safeDormantSelectionSourcePath(sourcePath) || !strings.HasSuffix(sourcePath, ".go") ||
		!canonicalPositiveDecimal(line) || !canonicalPositiveDecimal(column) {
		return false
	}
	packageDirectory := strings.TrimPrefix(packagePath, modulePath)
	packageDirectory = strings.TrimPrefix(packageDirectory, "/")
	if packageDirectory == "" {
		return pathpkg.Dir(sourcePath) == "."
	}
	return pathpkg.Dir(sourcePath) == packageDirectory
}

func canonicalPositiveDecimal(value string) bool {
	parsed, err := strconv.ParseUint(value, 10, 31)
	return err == nil && parsed > 0 && strconv.FormatUint(parsed, 10) == value
}

func safeDormantSelectionSourcePath(value string) bool {
	return value != "" && len(value) <= 4096 && utf8.ValidString(value) &&
		!strings.Contains(value, "\\") && strings.IndexFunc(value, unicode.IsControl) < 0 &&
		!pathpkg.IsAbs(value) && pathpkg.Clean(value) == value && value != "." &&
		value != ".." && !strings.HasPrefix(value, "../")
}

func dormantSelectionContributionKey(value DormantSelectionContribution) string {
	return fmt.Sprintf("%02d\x00%s\x00%s\x00%t\x00%s", value.precedence, value.owner, value.digest, value.removed, value.summary)
}

func dormantSelectionSourceKey(value DormantSelectionSource) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", value.module, value.path, value.kind, value.line, value.column)
}

func sortedContains(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func cloneDormantImplementationSelections(values []DormantImplementationSelection) []DormantImplementationSelection {
	if values == nil {
		return nil
	}
	result := make([]DormantImplementationSelection, len(values))
	copy(result, values)
	for index := range result {
		result[index].contributions = cloneDormantSelectionContributions(result[index].contributions)
	}
	return result
}

func cloneDormantSelectionContributions(values []DormantSelectionContribution) []DormantSelectionContribution {
	result := append([]DormantSelectionContribution(nil), values...)
	for index := range result {
		result[index].sources = append([]DormantSelectionSource(nil), values[index].sources...)
	}
	return result
}
