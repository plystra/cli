package applicationmeta

import (
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"

	"github.com/plystra/cli/internal/modulepath"
	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/module"
)

const maximumExportNameBytes = 128

var (
	// ErrInvalidExportName reports a name outside the canonical reusable
	// configuration export grammar.
	ErrInvalidExportName = errors.New("invalid reusable configuration export name")
	// ErrResolveAdoptedExports reports failure to bind the selected current
	// Project's adoption set to exact visible module/export identities.
	ErrResolveAdoptedExports = errors.New("resolve reusable configuration export adoptions")
	// ErrExportNotFound reports an adoption whose exact module/export identity
	// is not present in the current effective Go Module graph.
	ErrExportNotFound = errors.New("reusable configuration export not found")
	// ErrSetExportAdoptions reports failure to write one exact complete adoption
	// set into a current-Project root document.
	ErrSetExportAdoptions = errors.New("set reusable configuration export adoptions")
)

// ConfigurationExport is one inert named reusable configuration fragment from
// a Project's root plystra.yaml.
type ConfigurationExport struct {
	name     string
	manifest Manifest
}

// Name returns the canonical export name.
func (e ConfigurationExport) Name() string { return e.name }

// Manifest returns the immutable typed fragment. The fragment contains no
// process settings, exposure, nested exports, or adoptions.
func (e ConfigurationExport) Manifest() Manifest { return e.manifest }

// ExportAdoption is one exact module/export identity selected by the current
// Project.
type ExportAdoption struct {
	modulePath string
	exportName string
	source     string
}

// ModulePath returns the exact effective Go Module path.
func (a ExportAdoption) ModulePath() string { return a.modulePath }

// ExportName returns the exact canonical export name.
func (a ExportAdoption) ExportName() string { return a.exportName }

// Source returns stable current-Project configuration provenance.
func (a ExportAdoption) Source() string { return a.source }

type adoptionSetMode uint8

const (
	adoptionSetAbsent adoptionSetMode = iota
	adoptionSetComplete
	adoptionSetSparse
)

// CheckExportName validates the canonical reusable configuration export name
// grammar.
func CheckExportName(name string) error {
	if !validExportName(name) {
		return fmt.Errorf("%w: %q must be at most %d bytes and match [a-z][a-z0-9]*(?:[.-][a-z0-9]+)*", ErrInvalidExportName, name, maximumExportNameBytes)
	}
	return nil
}

func validExportName(name string) bool {
	if name == "" || len(name) > maximumExportNameBytes || !utf8.ValidString(name) || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	separator := false
	for index := 1; index < len(name); index++ {
		value := name[index]
		switch {
		case value >= 'a' && value <= 'z', value >= '0' && value <= '9':
			separator = false
		case value == '.' || value == '-':
			if separator {
				return false
			}
			separator = true
		default:
			return false
		}
	}
	return !separator
}

// Exports returns defensive declarations sorted by canonical export name.
func (m Manifest) Exports() []ConfigurationExport {
	return append([]ConfigurationExport(nil), m.exports...)
}

// ExportAdoptions returns the effective exact adoption identities in stable
// module/export order. Sparse removals are retained only on the authored
// layer and are not returned as active adoptions.
func (m Manifest) ExportAdoptions() []ExportAdoption {
	return append([]ExportAdoption(nil), m.exportAdoptions...)
}

// ParseExportInventorySource reads only the inert root export inventory from a
// dependency Project. Other dependency root declarations are consumer-inert
// and are deliberately neither parsed nor validated here.
func ParseExportInventorySource(source string, data []byte) (Manifest, error) {
	if err := validateConfigurationSource(source); err != nil {
		return Manifest{}, err
	}
	root, err := decodeDocument(data)
	if err != nil {
		return Manifest{}, err
	}
	values, err := mapping(root, "document")
	if err != nil {
		return Manifest{}, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "composition", "http", "timeouts", "capabilities", "interfaces", "config":
		default:
			return Manifest{}, invalid("unknown key %q", key)
		}
	}
	composition := values["composition"]
	if composition == nil {
		return Manifest{source: source}, nil
	}
	compositionValues, err := mapping(composition, "composition")
	if err != nil {
		return Manifest{}, err
	}
	for _, key := range sortedNodeKeys(compositionValues) {
		switch key {
		case "exports", "adopt":
		default:
			return Manifest{}, invalid("composition contains unknown key %q", key)
		}
	}
	exports, err := parseConfigurationExports(compositionValues["exports"], source)
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{source: source, exports: exports}, nil
}

func parseComposition(node *yaml.Node, source string, sparseOverlay, allowExports bool) ([]ConfigurationExport, []ExportAdoption, []ExportAdoption, adoptionSetMode, error) {
	if node == nil {
		return nil, nil, nil, adoptionSetAbsent, nil
	}
	values, err := mapping(node, "composition")
	if err != nil {
		return nil, nil, nil, adoptionSetAbsent, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "exports", "adopt":
		default:
			return nil, nil, nil, adoptionSetAbsent, invalid("composition contains unknown key %q", key)
		}
	}
	if values["exports"] != nil && (!allowExports || sparseOverlay) {
		return nil, nil, nil, adoptionSetAbsent, invalid("composition.exports may be declared only in root plystra.yaml")
	}
	exports, err := parseConfigurationExports(values["exports"], source)
	if err != nil {
		return nil, nil, nil, adoptionSetAbsent, err
	}
	adoptions, removals, mode, err := parseExportAdoptionSet(values["adopt"], source)
	if err != nil {
		return nil, nil, nil, adoptionSetAbsent, err
	}
	return exports, adoptions, removals, mode, nil
}

func parseConfigurationExports(node *yaml.Node, source string) ([]ConfigurationExport, error) {
	if node == nil {
		return nil, nil
	}
	values, err := mapping(node, "composition.exports")
	if err != nil {
		return nil, err
	}
	result := make([]ConfigurationExport, 0, len(values))
	for _, name := range sortedNodeKeys(values) {
		if err := CheckExportName(name); err != nil {
			return nil, invalid("composition.exports export name %q is invalid: %v", name, err)
		}
		path := fmt.Sprintf("composition.exports[%q]", name)
		fragmentValues, err := mapping(values[name], path)
		if err != nil {
			return nil, err
		}
		for _, key := range sortedNodeKeys(fragmentValues) {
			switch key {
			case "interfaces", "config":
			case "resources":
				return nil, invalid("%s resources are not supported by this installed CLI", path)
			default:
				return nil, invalid("%s may contain only interfaces, config, and resources", path)
			}
		}
		if interfaces := fragmentValues["interfaces"]; interfaces != nil {
			interfaceValues, err := mapping(interfaces, path+".interfaces")
			if err != nil {
				return nil, err
			}
			if requirement := interfaceValues["require"]; requirement != nil && requirement.Kind != yaml.SequenceNode {
				return nil, invalid("%s.interfaces.require must use the positive sequence form", path)
			}
		}
		data, err := yaml.Marshal(values[name])
		if err != nil {
			return nil, invalid("%s cannot be normalized", path)
		}
		fragment, err := parseSource("plystra.yaml", data, false)
		if err != nil {
			return nil, invalid("%s is invalid: %v", path, err)
		}
		if manifestContainsRemoval(fragment) {
			return nil, invalid("%s cannot contain removals", path)
		}
		rewriteManifestSourcePrefix(&fragment, source, path+".")
		fragment.source = source
		result = append(result, ConfigurationExport{name: name, manifest: fragment})
	}
	return result, nil
}

func manifestContainsRemoval(manifest Manifest) bool {
	return len(manifest.removedHTTPExposures) != 0 ||
		len(manifest.removedRequirements) != 0 ||
		len(manifest.removedProviderChoices) != 0 ||
		len(manifest.removedInterfaceReqs) != 0 ||
		len(manifest.removedImplementationChoices) != 0 ||
		len(manifest.removedInterfacePolicies) != 0 ||
		len(manifest.removedAliases) != 0 ||
		len(manifest.removedConfigurations) != 0
}

func parseExportAdoptionSet(node *yaml.Node, source string) ([]ExportAdoption, []ExportAdoption, adoptionSetMode, error) {
	if node == nil {
		return nil, nil, adoptionSetAbsent, nil
	}
	switch node.Kind {
	case yaml.SequenceNode:
		values, err := parseExportAdoptionSequence(node, source, "composition.adopt")
		return values, nil, adoptionSetComplete, err
	case yaml.MappingNode:
		fields, err := mapping(node, "composition.adopt")
		if err != nil {
			return nil, nil, adoptionSetAbsent, err
		}
		for _, key := range sortedNodeKeys(fields) {
			if key != "add" && key != "remove" {
				return nil, nil, adoptionSetAbsent, invalid("composition.adopt contains unknown sparse-edit key %q", key)
			}
		}
		adds, err := parseExportAdoptionSequence(fields["add"], source, "composition.adopt.add")
		if err != nil {
			return nil, nil, adoptionSetAbsent, err
		}
		removes, err := parseExportAdoptionSequence(fields["remove"], source, "composition.adopt.remove")
		if err != nil {
			return nil, nil, adoptionSetAbsent, err
		}
		added := make(map[string]struct{}, len(adds))
		for _, adoption := range adds {
			added[exportAdoptionKey(adoption)] = struct{}{}
		}
		for _, removal := range removes {
			if _, exists := added[exportAdoptionKey(removal)]; exists {
				return nil, nil, adoptionSetAbsent, invalid("composition.adopt cannot both add and remove %s", renderExportAdoption(removal))
			}
		}
		return adds, removes, adoptionSetSparse, nil
	default:
		return nil, nil, adoptionSetAbsent, invalid("composition.adopt must be a sequence or sparse {add, remove} mapping")
	}
}

func parseExportAdoptionSequence(node *yaml.Node, source, path string) ([]ExportAdoption, error) {
	if node == nil {
		return nil, nil
	}
	if node.Kind != yaml.SequenceNode {
		return nil, invalid("%s must be a sequence of exact module/export objects", path)
	}
	result := make([]ExportAdoption, 0, len(node.Content))
	seen := make(map[string]int, len(node.Content))
	for index, item := range node.Content {
		fields, err := mapping(item, fmt.Sprintf("%s[%d]", path, index))
		if err != nil || len(fields) != 2 || fields["module"] == nil || fields["export"] == nil {
			return nil, invalid("%s[%d] must contain exactly module and export", path, index)
		}
		modulePath, moduleErr := strictString(fields["module"])
		exportName, exportErr := strictString(fields["export"])
		if moduleErr != nil || module.CheckPath(modulePath) != nil {
			return nil, invalid("%s[%d].module must be a valid Go Module path", path, index)
		}
		if exportErr != nil || CheckExportName(exportName) != nil {
			return nil, invalid("%s[%d].export must be a canonical reusable configuration export name", path, index)
		}
		adoption := ExportAdoption{
			modulePath: modulePath,
			exportName: exportName,
			source:     fmt.Sprintf("%s %s[%q]", source, path, modulePath+"#"+exportName),
		}
		key := exportAdoptionKey(adoption)
		if previous, duplicate := seen[key]; duplicate {
			return nil, invalid("%s[%d] duplicates %s from %s[%d]", path, index, renderExportAdoption(adoption), path, previous)
		}
		seen[key] = index
		result = append(result, adoption)
	}
	sortExportAdoptions(result)
	return result, nil
}

func sortExportAdoptions(values []ExportAdoption) {
	sort.Slice(values, func(left, right int) bool {
		return exportAdoptionKey(values[left]) < exportAdoptionKey(values[right])
	})
}

func exportAdoptionKey(value ExportAdoption) string {
	return value.modulePath + "\x00" + value.exportName
}

func renderExportAdoption(value ExportAdoption) string {
	return fmt.Sprintf("export %q from module %q", value.exportName, value.modulePath)
}

func findConfigurationExport(manifest Manifest, name string) (ConfigurationExport, bool) {
	index := sort.Search(len(manifest.exports), func(index int) bool { return manifest.exports[index].name >= name })
	if index >= len(manifest.exports) || manifest.exports[index].name != name {
		return ConfigurationExport{}, false
	}
	return manifest.exports[index], true
}

// ResolveAdoptedExports binds the selected current Project's exact adoption
// set to inert root export inventories. Dependency top-level configuration is
// deliberately ignored.
func ResolveAdoptedExports(currentProjectModule string, root, selected Manifest, dependencies []Dependency) ([]Dependency, error) {
	if err := modulepath.CheckProject(currentProjectModule); err != nil {
		return nil, fmt.Errorf("%w: current Project module %q is invalid: %v", ErrResolveAdoptedExports, currentProjectModule, err)
	}
	byModule := make(map[string]Dependency, len(dependencies))
	for _, dependency := range dependencies {
		if module.CheckPath(dependency.ModulePath) != nil {
			return nil, fmt.Errorf("%w: dependency module path %q is invalid", ErrResolveAdoptedExports, dependency.ModulePath)
		}
		if _, duplicate := byModule[dependency.ModulePath]; duplicate {
			return nil, fmt.Errorf("%w: dependency module %q is repeated", ErrResolveAdoptedExports, dependency.ModulePath)
		}
		byModule[dependency.ModulePath] = dependency
	}
	result := make([]Dependency, 0, len(selected.exportAdoptions))
	for _, adoption := range selected.exportAdoptions {
		owner := Dependency{ModulePath: currentProjectModule, Manifest: root}
		if adoption.modulePath != currentProjectModule {
			var exists bool
			owner, exists = byModule[adoption.modulePath]
			if !exists {
				return nil, fmt.Errorf("%w: %w: %s is outside the effective Go Module graph", ErrResolveAdoptedExports, ErrExportNotFound, renderExportAdoption(adoption))
			}
		}
		export, exists := findConfigurationExport(owner.Manifest, adoption.exportName)
		if !exists {
			return nil, fmt.Errorf("%w: %w: %s", ErrResolveAdoptedExports, ErrExportNotFound, renderExportAdoption(adoption))
		}
		fragment, err := WithProjectModule(export.Manifest(), adoption.modulePath)
		if err != nil {
			return nil, fmt.Errorf("%w: bind %s: %v", ErrResolveAdoptedExports, renderExportAdoption(adoption), err)
		}
		result = append(result, Dependency{
			ModulePath:    adoption.modulePath,
			ModuleVersion: owner.ModuleVersion,
			ExportName:    adoption.exportName,
			Manifest:      fragment,
		})
	}
	return result, nil
}

// SetExportAdoptions writes one deterministic complete adoption set while
// preserving unrelated root-document content and comments.
func SetExportAdoptions(data []byte, modulePath string, exportNames []string) ([]byte, error) {
	if module.CheckPath(modulePath) != nil {
		return nil, fmt.Errorf("%w: module %q is not a valid Go Module path", ErrSetExportAdoptions, modulePath)
	}
	names := append([]string(nil), exportNames...)
	sort.Strings(names)
	for index, name := range names {
		if err := CheckExportName(name); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrSetExportAdoptions, err)
		}
		if index > 0 && names[index-1] == name {
			return nil, fmt.Errorf("%w: export name %q is repeated", ErrSetExportAdoptions, name)
		}
	}
	root, err := decodeDocument(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSetExportAdoptions, err)
	}
	if _, err := mapping(root, "document"); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSetExportAdoptions, err)
	}
	composition := mappingChild(root, "composition")
	if composition == nil {
		composition = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(root, "composition", composition)
	} else if composition.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: composition must be a mapping", ErrSetExportAdoptions)
	}
	adoptions := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, name := range names {
		item := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(item, "module", stringYAMLNode(modulePath))
		setMappingValue(item, "export", stringYAMLNode(name))
		adoptions.Content = append(adoptions.Content, item)
	}
	setMappingValue(composition, "adopt", adoptions)
	sortYAMLMapping(composition)
	sortYAMLMapping(root)
	updated, err := encodeMaintainedDocument(root)
	if err != nil {
		return nil, fmt.Errorf("%w: encode updated root document: %v", ErrSetExportAdoptions, err)
	}
	if len(updated) > MaximumSize {
		return nil, fmt.Errorf("%w: updated root document exceeds %d bytes", ErrSetExportAdoptions, MaximumSize)
	}
	if _, err := Parse(updated); err != nil {
		return nil, fmt.Errorf("%w: validate updated root document: %v", ErrSetExportAdoptions, err)
	}
	return updated, nil
}

func compositionConfigurationDecisions(manifest Manifest, schemas SchemaLookup) ([]ConfigurationDecision, error) {
	result := make([]ConfigurationDecision, 0, len(manifest.exports)+len(manifest.exportAdoptions)+len(manifest.removedExportAdoptions))
	source := manifest.source
	if source == "" {
		source = "plystra.yaml"
	}
	for _, export := range manifest.exports {
		digest, err := ConfigurationLayerDigest(export.manifest, schemas)
		if err != nil {
			return nil, fmt.Errorf("digest composition export %q: %w", export.name, err)
		}
		result = append(result, ConfigurationDecision{
			path:                 fmt.Sprintf("composition.exports[%q]", export.name),
			digest:               digestStrings("composition.exports", export.name, digest),
			summary:              ConfigurationSummaryObject,
			source:               source,
			dependencyComposable: false,
		})
	}
	appendAdoption := func(adoption ExportAdoption, removed bool) {
		state := "adopted"
		summary := ConfigurationSummaryValue
		if removed {
			state = "removed"
			summary = ConfigurationSummaryRemoval
		}
		result = append(result, ConfigurationDecision{
			path:                 fmt.Sprintf("composition.adopt[%q]", adoption.modulePath+"#"+adoption.exportName),
			digest:               digestStrings("composition.adopt", adoption.modulePath, adoption.exportName, state),
			summary:              summary,
			removed:              removed,
			source:               source,
			dependencyComposable: false,
		})
	}
	for _, adoption := range manifest.exportAdoptions {
		appendAdoption(adoption, false)
	}
	for _, removal := range manifest.removedExportAdoptions {
		appendAdoption(removal, true)
	}
	return result, nil
}
