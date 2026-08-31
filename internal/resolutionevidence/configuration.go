package resolutionevidence

import (
	"errors"
	"fmt"
	pathpkg "path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
)

type configurationCandidate struct {
	path       string
	digest     string
	summary    string
	removed    bool
	owner      ConfigurationOwner
	precedence int
	sources    []Source
	effective  bool
}

func configurationSelectionFromContext(context generation.Context) (ConfigurationSelection, bool, error) {
	provenance, exists := context.ConfigurationProvenance()
	if !exists {
		return ConfigurationSelection{}, false, nil
	}
	selection := ConfigurationSelection{
		mode:             provenance.Mode(),
		environment:      provenance.Environment(),
		rootPath:         provenance.RootPath(),
		rootDigest:       provenance.RootDigest(),
		selectedPath:     provenance.SelectedPath(),
		selectedDigest:   provenance.SelectedDigest(),
		dependencyDigest: provenance.DependencyCompositionDigest(),
	}
	if err := validateConfigurationSelection(selection); err != nil {
		return ConfigurationSelection{}, false, err
	}
	return selection, true, nil
}

func configurationEvidenceFromInput(input *ConfigurationInput, context generation.Context, modules []Module) ([]ConfigurationField, error) {
	if input == nil {
		return nil, nil
	}
	provenance, exists := context.ConfigurationProvenance()
	if !exists {
		return nil, fmt.Errorf("configuration input requires selected-configuration provenance")
	}
	if !input.DependencyBaseline.Valid() {
		return nil, fmt.Errorf("dependency baseline is absent or invalid")
	}
	if input.DependencyBaseline.Digest() != provenance.DependencyCompositionDigest() {
		return nil, fmt.Errorf("dependency baseline digest disagrees with selected-model provenance")
	}
	if err := validateConfigurationLayers(provenance.Mode(), input.Layers); err != nil {
		return nil, err
	}
	candidates := make(map[string][]configurationCandidate)
	for _, record := range input.DependencyBaseline.Records() {
		candidate := configurationCandidate{
			path:       record.Path,
			digest:     record.Digest,
			removed:    record.Removed,
			owner:      ConfigurationOwnerDependency,
			precedence: 1,
			summary:    "redacted",
		}
		if record.Removed {
			candidate.summary = string(applicationmeta.ConfigurationSummaryRemoval)
		}
		for _, raw := range record.Sources {
			source, err := configurationSource(raw, ConfigurationOwnerDependency, record.Removed, modules)
			if err != nil {
				return nil, fmt.Errorf("dependency field %s: %w", record.Path, err)
			}
			candidate.sources = append(candidate.sources, source)
		}
		if err := validateConfigurationCandidate(candidate); err != nil {
			return nil, err
		}
		mergeConfigurationCandidate(candidates, candidate)
	}

	for _, layer := range input.Layers {
		precedence := configurationLayerPrecedence(layer.Owner)
		for _, decision := range layer.Decisions {
			candidate := configurationCandidate{
				path:       decision.Path(),
				digest:     decision.Digest(),
				summary:    string(decision.Summary()),
				removed:    decision.Removed(),
				owner:      layer.Owner,
				precedence: precedence,
			}
			source, err := configurationSource(decision.Source(), layer.Owner, decision.Removed(), modules)
			if err != nil {
				return nil, fmt.Errorf("current field %s: %w", decision.Path(), err)
			}
			candidate.sources = []Source{source}
			if err := validateConfigurationCandidate(candidate); err != nil {
				return nil, err
			}
			mergeConfigurationCandidate(candidates, candidate)
		}
	}

	fields, err := selectConfigurationFields(candidates)
	if err != nil {
		return nil, err
	}
	if err := crossCheckEffectiveConfiguration(fields, input.Effective); err != nil {
		return nil, err
	}
	return fields, nil
}

func validateConfigurationLayers(mode generation.ConfigurationMode, layers []ConfigurationLayerInput) error {
	counts := map[ConfigurationOwner]int{}
	for _, layer := range layers {
		if layer.Owner == ConfigurationOwnerDependency || configurationLayerPrecedence(layer.Owner) == 0 {
			return fmt.Errorf("configuration layer %q is invalid", layer.Owner)
		}
		counts[layer.Owner]++
	}
	switch mode {
	case generation.ConfigurationModeDefault:
		if counts[ConfigurationOwnerRoot] != 1 || counts[ConfigurationOwnerEnvironment] != 0 || counts[ConfigurationOwnerExplicit] != 0 {
			return fmt.Errorf("default configuration evidence must contain exactly one root layer")
		}
	case generation.ConfigurationModeEnvironment:
		if counts[ConfigurationOwnerRoot] != 1 || counts[ConfigurationOwnerEnvironment] != 1 || counts[ConfigurationOwnerExplicit] != 0 {
			return fmt.Errorf("environment configuration evidence must contain one root and one environment layer")
		}
	case generation.ConfigurationModeExplicit:
		if counts[ConfigurationOwnerRoot] != 0 || counts[ConfigurationOwnerEnvironment] != 0 || counts[ConfigurationOwnerExplicit] != 1 {
			return fmt.Errorf("explicit configuration evidence must contain exactly one replacement layer")
		}
	default:
		return fmt.Errorf("configuration mode %q is invalid", mode)
	}
	return nil
}

func configurationLayerPrecedence(owner ConfigurationOwner) int {
	switch owner {
	case ConfigurationOwnerDependency:
		return 1
	case ConfigurationOwnerRoot, ConfigurationOwnerExplicit:
		return 2
	case ConfigurationOwnerEnvironment:
		return 3
	default:
		return 0
	}
}

func validateConfigurationCandidate(candidate configurationCandidate) error {
	if !validConfigurationFieldPath(candidate.path) {
		return fmt.Errorf("configuration path %q is invalid", candidate.path)
	}
	if !validDigest(candidate.digest) {
		return fmt.Errorf("configuration path %s has a noncanonical digest", candidate.path)
	}
	if !validConfigurationSummary(candidate.summary) {
		return fmt.Errorf("configuration path %s has an invalid redacted summary", candidate.path)
	}
	if candidate.precedence != configurationLayerPrecedence(candidate.owner) {
		return fmt.Errorf("configuration path %s has an invalid owner", candidate.path)
	}
	if candidate.removed != (candidate.summary == string(applicationmeta.ConfigurationSummaryRemoval)) {
		return fmt.Errorf("configuration path %s has inconsistent removal evidence", candidate.path)
	}
	if candidate.owner == ConfigurationOwnerDependency && !configurationPathDependencyComposable(candidate.path) {
		return fmt.Errorf("configuration path %s cannot be inherited from a dependency Project", candidate.path)
	}
	if len(candidate.sources) == 0 {
		return fmt.Errorf("configuration path %s has no source provenance", candidate.path)
	}
	return nil
}

func mergeConfigurationCandidate(groups map[string][]configurationCandidate, candidate configurationCandidate) {
	values := groups[candidate.path]
	key := string(candidate.owner) + "\x00" + fmt.Sprintf("%d", candidate.precedence) + "\x00" + candidate.digest + "\x00" + fmt.Sprintf("%t", candidate.removed) + "\x00" + candidate.summary
	for index := range values {
		other := values[index]
		otherKey := string(other.owner) + "\x00" + fmt.Sprintf("%d", other.precedence) + "\x00" + other.digest + "\x00" + fmt.Sprintf("%t", other.removed) + "\x00" + other.summary
		if key != otherKey {
			continue
		}
		values[index].sources = append(values[index].sources, candidate.sources...)
		values[index].sources = uniqueConfigurationSources(values[index].sources)
		groups[candidate.path] = values
		return
	}
	values = append(values, candidate)
	groups[candidate.path] = values
}

func selectConfigurationFields(groups map[string][]configurationCandidate) ([]ConfigurationField, error) {
	paths := make([]string, 0, len(groups))
	for path := range groups {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(left, right int) bool {
		leftDepth := configurationPathDepth(paths[left])
		rightDepth := configurationPathDepth(paths[right])
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return paths[left] < paths[right]
	})
	winners := make(map[string]int, len(paths))
	fields := make([]ConfigurationField, 0, len(paths))
	for _, path := range paths {
		values := groups[path]
		for index := range values {
			values[index].effective = false
		}
		if suppressedByConfigurationAncestor(path, groups, winners) {
			fields = append(fields, configurationFieldFromCandidates(path, values, -1, false))
			continue
		}
		winner, err := configurationWinner(path, values)
		if err != nil {
			return nil, err
		}
		winners[path] = winner
		values[winner].effective = true
		fields = append(fields, configurationFieldFromCandidates(path, values, winner, true))
	}
	sort.Slice(fields, func(left, right int) bool { return fields[left].path < fields[right].path })
	return fields, nil
}

func configurationWinner(path string, values []configurationCandidate) (int, error) {
	if len(values) == 0 {
		return -1, fmt.Errorf("configuration path %s has no candidates", path)
	}
	max := values[0].precedence
	for _, value := range values[1:] {
		if value.precedence > max {
			max = value.precedence
		}
	}
	winner := -1
	for index, value := range values {
		if value.precedence != max {
			continue
		}
		if winner == -1 {
			winner = index
			continue
		}
		if values[winner].digest != value.digest || values[winner].removed != value.removed || values[winner].summary != value.summary {
			return -1, fmt.Errorf("configuration path %s has incompatible same-layer decisions", path)
		}
	}
	if winner == -1 {
		return -1, fmt.Errorf("configuration path %s has no highest-precedence decision", path)
	}
	return winner, nil
}

func configurationFieldFromCandidates(path string, values []configurationCandidate, winner int, effective bool) ConfigurationField {
	field := ConfigurationField{path: path, effective: effective, contributors: make([]ConfigurationContribution, len(values))}
	for index, value := range values {
		field.contributors[index] = ConfigurationContribution{
			owner:      value.owner,
			precedence: value.precedence,
			digest:     value.digest,
			summary:    value.summary,
			removed:    value.removed,
			effective:  value.effective && effective,
			sources:    uniqueConfigurationSources(value.sources),
		}
	}
	sort.Slice(field.contributors, func(left, right int) bool {
		return configurationContributionKey(field.contributors[left]) < configurationContributionKey(field.contributors[right])
	})
	if winner >= 0 && effective {
		value := values[winner]
		field.digest = value.digest
		field.summary = value.summary
		field.removed = value.removed
		field.owner = value.owner
	}
	return field
}

func suppressedByConfigurationAncestor(path string, groups map[string][]configurationCandidate, winners map[string]int) bool {
	for _, ancestor := range configurationPathAncestors(path) {
		values, exists := groups[ancestor]
		if !exists {
			continue
		}
		winner, selected := winners[ancestor]
		if !selected {
			continue
		}
		value := values[winner]
		if value.removed || !configurationCandidateIsObject(value, groups, ancestor) {
			return true
		}
	}
	return false
}

func configurationCandidateIsObject(value configurationCandidate, groups map[string][]configurationCandidate, path string) bool {
	if value.summary == string(applicationmeta.ConfigurationSummaryObject) {
		return true
	}
	if value.summary != "redacted" {
		return false
	}
	for candidatePath := range groups {
		if candidatePath != path && strings.HasPrefix(candidatePath, path+"[") {
			return true
		}
	}
	return false
}

func crossCheckEffectiveConfiguration(fields []ConfigurationField, effective []applicationmeta.ConfigurationDecision) error {
	byPath := make(map[string]ConfigurationField, len(fields))
	for _, field := range fields {
		byPath[field.path] = field
	}
	for _, decision := range effective {
		if decision.Removed() {
			continue
		}
		field, exists := byPath[decision.Path()]
		if !exists || !field.effective || field.digest != decision.Digest() {
			return fmt.Errorf("effective configuration path %s disagrees with typed contributions", decision.Path())
		}
	}
	for _, field := range fields {
		if !field.effective || field.removed {
			continue
		}
		found := false
		for _, decision := range effective {
			if decision.Path() == field.path && !decision.Removed() && decision.Digest() == field.digest {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("effective configuration path %s is absent from the normalized manifest", field.path)
		}
	}
	return nil
}

func uniqueConfigurationSources(values []Source) []Source {
	result := append([]Source(nil), values...)
	sort.Slice(result, func(left, right int) bool {
		return configurationSourceKey(result[left]) < configurationSourceKey(result[right])
	})
	write := 0
	for _, value := range result {
		if write != 0 && configurationSourceKey(result[write-1]) == configurationSourceKey(value) {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}

func configurationSourceKey(value Source) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", value.module, value.path, value.kind, value.line, value.column)
}

func configurationSource(raw string, owner ConfigurationOwner, removed bool, modules []Module) (Source, error) {
	if raw == "" || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return Source{}, fmt.Errorf("source is empty or contains control characters")
	}
	document := raw
	module := ""
	if owner == ConfigurationOwnerDependency {
		identities := make([]struct{ identity, module string }, 0, len(modules))
		for _, candidate := range modules {
			if candidate.role != ModuleRoleDependency {
				continue
			}
			version := candidate.selectedVersion
			if version == "" {
				version = "workspace"
			}
			identities = append(identities, struct{ identity, module string }{identity: candidate.path + "@" + version, module: candidate.source.module})
		}
		sort.Slice(identities, func(left, right int) bool { return len(identities[left].identity) > len(identities[right].identity) })
		matched := false
		for _, candidate := range identities {
			prefix := candidate.identity + "/plystra.yaml"
			if raw == prefix || strings.HasPrefix(raw, prefix+" ") {
				module = candidate.module
				document = "plystra.yaml"
				matched = true
				break
			}
		}
		if !matched {
			return Source{}, fmt.Errorf("dependency source %q does not identify a participating module", raw)
		}
	} else {
		for _, candidate := range modules {
			if candidate.role == ModuleRoleCurrent {
				module = candidate.path
				break
			}
		}
		if module == "" {
			return Source{}, fmt.Errorf("current configuration source has no current Project module")
		}
	}
	if !safeConfigurationDocumentPath(document) {
		return Source{}, fmt.Errorf("configuration document path %q is unsafe", document)
	}
	kind := "configuration-value"
	if removed {
		kind = "configuration-removal"
	}
	return Source{module: module, path: document, kind: kind, line: 1, column: 1}, nil
}

func safeConfigurationDocumentPath(value string) bool {
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsAny(value, "\\\x00") || strings.IndexFunc(value, unicode.IsControl) >= 0 || pathpkg.IsAbs(value) || pathpkg.Clean(value) != value {
		return false
	}
	if len(value) >= 2 && value[1] == ':' && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	return true
}

func validConfigurationFieldPath(value string) bool {
	switch value {
	case "http.address", "http.transports.connect", "http.transports.rest", "http.cors", "http.cors.allowed_origins", "http.cors.allow_credentials", "timeouts.startup":
		return true
	}
	if keys, ok := configurationPathKeys(value, "http.expose"); ok && len(keys) == 1 {
		_, err := interfaceid.Parse(keys[0])
		return err == nil
	}
	for _, prefix := range []string{"capabilities.require", "capabilities.use", "capabilities.aliases"} {
		keys, ok := configurationPathKeys(value, prefix)
		if !ok || len(keys) != 1 {
			continue
		}
		_, err := capabilityid.Parse(keys[0])
		return err == nil
	}
	for _, prefix := range []string{"interfaces.require", "interfaces.use"} {
		keys, ok := configurationPathKeys(value, prefix)
		if !ok || len(keys) != 1 {
			continue
		}
		_, err := interfaceid.Parse(keys[0])
		return err == nil
	}
	if strings.HasSuffix(value, ".timeout") {
		keys, ok := configurationPathKeys(strings.TrimSuffix(value, ".timeout"), "interfaces.policies")
		if ok && len(keys) == 1 {
			identifier, err := interfaceid.Parse(keys[0])
			return err == nil && !strings.HasPrefix(identifier.Name(), "kernel.")
		}
	}
	keys, ok := configurationPathKeys(value, "config")
	if !ok || len(keys) == 0 {
		return false
	}
	if _, err := constructorsymbol.Parse(keys[0]); err != nil {
		return false
	}
	return true
}

func validConfigurationSummary(value string) bool {
	switch applicationmeta.ConfigurationDecisionSummary(value) {
	case applicationmeta.ConfigurationSummaryRemoval,
		applicationmeta.ConfigurationSummaryObject,
		applicationmeta.ConfigurationSummaryCapability,
		applicationmeta.ConfigurationSummaryProvider,
		applicationmeta.ConfigurationSummaryInterface,
		applicationmeta.ConfigurationSummaryImplementation,
		applicationmeta.ConfigurationSummaryAlias,
		applicationmeta.ConfigurationSummaryString,
		applicationmeta.ConfigurationSummaryBoolean,
		applicationmeta.ConfigurationSummaryDuration,
		applicationmeta.ConfigurationSummaryArray,
		applicationmeta.ConfigurationSummarySecret,
		applicationmeta.ConfigurationSummaryValue:
		return true
	}
	return value == "redacted"
}

func configurationPathDepth(value string) int {
	return strings.Count(value, "[") + strings.Count(value, ".")
}

func configurationPathAncestors(value string) []string {
	result := make([]string, 0)
	if strings.HasPrefix(value, "config[") {
		for index := strings.IndexByte(value, '['); index >= 0; {
			close := strings.IndexByte(value[index:], ']')
			if close < 0 {
				break
			}
			close += index
			result = append(result, value[:close+1])
			next := close + 1
			if next >= len(value) || value[next] != '[' {
				break
			}
			index = next
		}
		if len(result) > 0 {
			result = result[:len(result)-1]
		}
		return result
	}
	parts := strings.Split(value, ".")
	for index := 1; index < len(parts); index++ {
		result = append(result, strings.Join(parts[:index], "."))
	}
	return result
}

func validateConfigurationFields(fields []ConfigurationField, modules []Module, selection ConfigurationSelection, hasSelection bool) error {
	previousPath := ""
	for _, field := range fields {
		if !validConfigurationFieldPath(field.path) || field.path <= previousPath {
			return fmt.Errorf("configuration fields are not canonically ordered or contain an invalid path")
		}
		previousPath = field.path
		if field.effective {
			if field.digest == "" || !validDigest(field.digest) || field.summary == "" || field.owner == "" {
				return fmt.Errorf("effective configuration field %s is incomplete", field.path)
			}
			if field.removed != (field.summary == string(applicationmeta.ConfigurationSummaryRemoval)) {
				return fmt.Errorf("effective configuration field %s has inconsistent removal evidence", field.path)
			}
		} else if field.digest != "" || field.summary != "" || field.owner != "" || field.removed {
			return fmt.Errorf("suppressed configuration field %s carries effective state", field.path)
		}
		if len(field.contributors) == 0 {
			return fmt.Errorf("configuration field %s has no contributions", field.path)
		}
		effective := 0
		var effectiveContribution ConfigurationContribution
		previous := ""
		for _, contribution := range field.contributors {
			if contribution.precedence != configurationLayerPrecedence(contribution.owner) || !validDigest(contribution.digest) || !validConfigurationSummary(contribution.summary) || len(contribution.sources) == 0 {
				return fmt.Errorf("configuration field %s has an invalid contribution", field.path)
			}
			if contribution.removed != (contribution.summary == string(applicationmeta.ConfigurationSummaryRemoval)) {
				return fmt.Errorf("configuration field %s has inconsistent contribution removal evidence", field.path)
			}
			if contribution.owner == ConfigurationOwnerDependency && !configurationPathDependencyComposable(field.path) {
				return fmt.Errorf("configuration field %s has a dependency-owned process setting", field.path)
			}
			if !configurationOwnerAllowed(contribution.owner, selection, hasSelection) {
				return fmt.Errorf("configuration field %s has an owner outside the selected configuration mode", field.path)
			}
			key := configurationContributionKey(contribution)
			if previous != "" && key <= previous {
				return fmt.Errorf("configuration field %s contributions are not ordered", field.path)
			}
			previous = key
			if contribution.effective {
				effective++
				effectiveContribution = contribution
			}
			previousSource := ""
			for _, source := range contribution.sources {
				key := configurationSourceKey(source)
				if previousSource != "" && key <= previousSource {
					return fmt.Errorf("configuration field %s sources are not ordered", field.path)
				}
				previousSource = key
				if err := validateConfigurationSource(source, contribution.owner, contribution.removed, modules, selection); err != nil {
					return fmt.Errorf("configuration field %s source is invalid: %w", field.path, err)
				}
			}
		}
		if field.effective && effective != 1 {
			return fmt.Errorf("effective configuration field %s must have exactly one effective contribution", field.path)
		}
		if field.effective && (field.owner != effectiveContribution.owner || field.digest != effectiveContribution.digest || field.summary != effectiveContribution.summary || field.removed != effectiveContribution.removed) {
			return fmt.Errorf("effective configuration field %s disagrees with its winning contribution", field.path)
		}
		if !field.effective && effective != 0 {
			return fmt.Errorf("suppressed configuration field %s has an effective contribution", field.path)
		}
	}
	return nil
}

func validateConfigurationSelectionState(selection ConfigurationSelection, exists bool, fields []ConfigurationField) error {
	if !exists {
		if selection != (ConfigurationSelection{}) {
			return errors.New("absent configuration selection carries state")
		}
		if len(fields) != 0 {
			return errors.New("configuration fields require selected-configuration provenance")
		}
		return nil
	}
	return validateConfigurationSelection(selection)
}

func validateConfigurationSelection(selection ConfigurationSelection) error {
	if selection.rootPath != "plystra.yaml" {
		return fmt.Errorf("root configuration path must be %q", "plystra.yaml")
	}
	if !safeConfigurationDocumentPath(selection.selectedPath) {
		return fmt.Errorf("selected configuration path %q is unsafe", selection.selectedPath)
	}
	if !validDigest(selection.rootDigest) || !validDigest(selection.selectedDigest) || !validDigest(selection.dependencyDigest) {
		return errors.New("configuration selection has a noncanonical digest")
	}
	switch selection.mode {
	case generation.ConfigurationModeDefault:
		if selection.environment != "" || selection.selectedPath != selection.rootPath || selection.selectedDigest != selection.rootDigest {
			return errors.New("default configuration selection must select the exact root document")
		}
	case generation.ConfigurationModeEnvironment:
		if !validConfigurationEnvironment(selection.environment) || selection.selectedPath != "plystra."+selection.environment+".yaml" {
			return errors.New("environment configuration selection is inconsistent")
		}
	case generation.ConfigurationModeExplicit:
		if selection.environment != "" {
			return errors.New("explicit configuration selection cannot carry an environment")
		}
	default:
		return fmt.Errorf("configuration selection mode %q is invalid", selection.mode)
	}
	return nil
}

func validConfigurationEnvironment(value string) bool {
	return value != "" && len(value) <= 200 && value != "." && value != ".." && utf8.ValidString(value) && !strings.ContainsAny(value, `/\\<>:"|?*`) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func configurationOwnerAllowed(owner ConfigurationOwner, selection ConfigurationSelection, hasSelection bool) bool {
	if !hasSelection {
		return false
	}
	switch owner {
	case ConfigurationOwnerDependency:
		return true
	case ConfigurationOwnerRoot:
		return selection.mode == generation.ConfigurationModeDefault || selection.mode == generation.ConfigurationModeEnvironment
	case ConfigurationOwnerEnvironment:
		return selection.mode == generation.ConfigurationModeEnvironment
	case ConfigurationOwnerExplicit:
		return selection.mode == generation.ConfigurationModeExplicit
	default:
		return false
	}
}

func validateConfigurationSource(source Source, owner ConfigurationOwner, removed bool, modules []Module, selection ConfigurationSelection) error {
	if source.kind != "configuration-value" && source.kind != "configuration-removal" {
		return fmt.Errorf("kind %q is invalid", source.kind)
	}
	if removed != (source.kind == "configuration-removal") {
		return errors.New("kind disagrees with the removal marker")
	}
	if source.line != 1 || source.column != 1 || !safeConfigurationDocumentPath(source.path) {
		return errors.New("location is not stable")
	}
	var expectedPath string
	expectedRole := ModuleRoleCurrent
	switch owner {
	case ConfigurationOwnerDependency:
		expectedPath = "plystra.yaml"
		expectedRole = ModuleRoleDependency
	case ConfigurationOwnerRoot:
		expectedPath = selection.rootPath
	case ConfigurationOwnerEnvironment, ConfigurationOwnerExplicit:
		expectedPath = selection.selectedPath
	default:
		return fmt.Errorf("owner %q is invalid", owner)
	}
	if source.path != expectedPath {
		return fmt.Errorf("path %q does not match owner %q", source.path, owner)
	}
	for _, module := range modules {
		if module.role == expectedRole && module.source.module == source.module {
			return nil
		}
	}
	return fmt.Errorf("module %q is not a participating %s Project source", source.module, expectedRole)
}

func configurationContributionKey(value ConfigurationContribution) string {
	return fmt.Sprintf("%02d\x00%s\x00%s\x00%t\x00%s", value.precedence, value.owner, value.digest, value.removed, value.summary)
}

func configurationPathDependencyComposable(value string) bool {
	return strings.HasPrefix(value, "capabilities.require[") || strings.HasPrefix(value, "capabilities.use[") || strings.HasPrefix(value, "capabilities.aliases[") || strings.HasPrefix(value, "interfaces.require[") || strings.HasPrefix(value, "interfaces.use[") || strings.HasPrefix(value, "interfaces.policies[") || strings.HasPrefix(value, "config[")
}

func configurationPathKeys(value, prefix string) ([]string, bool) {
	if !strings.HasPrefix(value, prefix) {
		return nil, false
	}
	rest := value[len(prefix):]
	if rest == "" {
		return nil, false
	}
	keys := make([]string, 0, 2)
	for rest != "" {
		if len(rest) < 4 || rest[0] != '[' || rest[1] != '"' {
			return nil, false
		}
		end := -1
		escaped := false
		for index := 2; index < len(rest); index++ {
			character := rest[index]
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				end = index
				break
			}
		}
		if end < 0 || end+1 >= len(rest) || rest[end+1] != ']' {
			return nil, false
		}
		quoted := rest[1 : end+1]
		key, err := strconv.Unquote(quoted)
		if err != nil || key == "" || !utf8.ValidString(key) || strings.IndexFunc(key, unicode.IsControl) >= 0 || strconv.Quote(key) != quoted {
			return nil, false
		}
		keys = append(keys, key)
		rest = rest[end+2:]
	}
	return keys, true
}
