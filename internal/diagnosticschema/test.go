package diagnosticschema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/resolutionevidence"
)

const (
	maximumTestPluginCount       = 1_024
	maximumTestProviderCount     = 4_096
	maximumTestContributionCount = 4_096
	maximumTestOutcomeCount      = 4_096
	maximumTestFailureCount      = 16_384
)

var (
	// ErrTest reports incomplete, inconsistent, or unsafe input for the
	// plystra.test v1 result schema.
	ErrTest = errors.New("build plystra.test result")

	testSchemaV1 = mustSchema("plystra.test", 1)
)

// TestScope distinguishes a complete selected Project from one deterministic
// Plugin slice.
type TestScope string

const (
	TestScopeProject TestScope = "project"
	TestScopePlugin  TestScope = "plugin"
)

// TestStatus is the closed state of one test outcome and of the complete run.
type TestStatus string

const (
	TestStatusPassed  TestStatus = "passed"
	TestStatusFailed  TestStatus = "failed"
	TestStatusSkipped TestStatus = "skipped"
)

// TestProviderReason identifies how an ordinary Provider entered a test
// slice without exposing Provider implementation details.
type TestProviderReason string

const (
	TestProviderApplicationSelection  TestProviderReason = "application-selection"
	TestProviderSliceSelection        TestProviderReason = "slice-selection"
	TestProviderInvocationReplacement TestProviderReason = "invocation-replacement"
)

// TestContributionKind distinguishes selected generation activation from a
// selected generated canonical requirement.
type TestContributionKind string

const (
	TestContributionActivation  TestContributionKind = "activation"
	TestContributionRequirement TestContributionKind = "requirement"
)

// TestSlicePluginInput records the deterministic construction order of one
// Plugin in a targeted test slice.
type TestSlicePluginInput struct {
	Order    uint32
	PluginID string
}

// TestProviderInput records one final canonical Provider in a targeted slice.
// An empty PluginID identifies a Kernel intrinsic. Replacement is valid only
// for an ordinary invocation-local Provider substitution.
type TestProviderInput struct {
	Capability  string
	PluginID    string
	Replacement bool
}

// TestContributionInput identifies one generation contribution already
// selected by the test-slice resolver. Every field is matched against the
// canonical resolution evidence rather than trusted as free-form output.
type TestContributionInput struct {
	Kind                 TestContributionKind
	Namespace            string
	Capability           string
	SourceCapability     string
	ActivationCapability string
	PluginID             string
	RuleID               string
}

// TestSliceInput selects either the complete Project model or one explicit
// targeted Plugin slice. Project scope derives its complete membership from
// resolution evidence and therefore accepts no explicit members.
type TestSliceInput struct {
	Scope                  TestScope
	TargetPlugin           string
	Plugins                []TestSlicePluginInput
	Providers              []TestProviderInput
	GeneratedContributions []TestContributionInput
}

// TestFailure is one structured bounded detail owned by a failed outcome.
type TestFailure struct {
	Kind    string
	Subject string
	Summary string
	Sources []diagnosticjson.Source
}

// TestOutcome is one deterministic execution result. Order is contiguous and
// one-based; ID and Kind are stable lower-kebab identities.
type TestOutcome struct {
	Order    uint32
	ID       string
	Kind     string
	Subject  string
	Status   TestStatus
	Summary  string
	Failures []TestFailure
	Sources  []diagnosticjson.Source
}

// TestInput is the construction-only input for one plystra.test v1 result.
type TestInput struct {
	Evidence    resolutionevidence.Evidence
	Slice       TestSliceInput
	Outcomes    []TestOutcome
	Diagnostics []diagnosticjson.Diagnostic
	Sources     []diagnosticjson.Source
}

// TestConfigurationIdentity is the complete non-secret identity of the
// selected configuration used by the test run.
type TestConfigurationIdentity struct {
	Mode                        generation.ConfigurationMode
	Environment                 string
	RootPath                    string
	RootDigest                  string
	SelectedPath                string
	SelectedDigest              string
	DependencyCompositionDigest string
}

// TestSlicePlugin is one Plugin constructed by the selected test model.
type TestSlicePlugin struct {
	Order         uint32
	ID            string
	ProjectModule string
	ModuleVersion string
	Sources       []diagnosticjson.Source
}

// TestSliceCapability is one exact canonical Capability in the selected test
// model.
type TestSliceCapability struct {
	ID             string
	ContractDigest string
	Sources        []diagnosticjson.Source
}

// TestSliceProvider is one selected ordinary Provider in the test model.
type TestSliceProvider struct {
	Capability     string
	PluginID       string
	ContractDigest string
	Reason         TestProviderReason
	Sources        []diagnosticjson.Source
}

// TestSliceIntrinsic is one Kernel-owned canonical binding in the test model.
type TestSliceIntrinsic struct {
	Capability     string
	ContractDigest string
	Sources        []diagnosticjson.Source
}

// TestSliceContribution is one selected generation activation or generated
// requirement with stable source provenance.
type TestSliceContribution struct {
	Kind                 TestContributionKind
	Namespace            string
	Capability           string
	SourceCapability     string
	ActivationCapability string
	PluginID             string
	ProjectModule        string
	RuleID               string
	Sources              []diagnosticjson.Source
}

// TestProviderReplacement is one invocation-local ordinary Provider
// substitution. It never changes selected application configuration.
type TestProviderReplacement struct {
	Capability     string
	PluginID       string
	ContractDigest string
	Sources        []diagnosticjson.Source
}

// TestSlice is the normalized immutable membership and identity of one test
// model.
type TestSlice struct {
	Scope                  TestScope
	TargetPlugin           string
	Digest                 string
	Plugins                []TestSlicePlugin
	Capabilities           []TestSliceCapability
	Providers              []TestSliceProvider
	Intrinsics             []TestSliceIntrinsic
	GeneratedContributions []TestSliceContribution
	Replacements           []TestProviderReplacement
}

// TestResult is one immutable plystra.test v1 diagnostic result.
type TestResult struct {
	envelope               diagnosticjson.Envelope
	evidence               resolutionevidence.Evidence
	configuration          TestConfigurationIdentity
	sliceInput             TestSliceInput
	slice                  TestSlice
	status                 TestStatus
	failedOutcomeCount     int
	outcomes               []TestOutcome
	resolutionEvidenceJSON []byte
	prepared               bool
}

type testDocument struct {
	Status              TestStatus                `json:"status"`
	FailedOutcomeCount  int                       `json:"failed_outcome_count"`
	SelectedModelDigest string                    `json:"selected_model_digest"`
	Configuration       testConfigurationDocument `json:"configuration"`
	Slice               testSliceDocument         `json:"slice"`
	Outcomes            []testOutcomeDocument     `json:"outcomes"`
	ResolutionEvidence  json.RawMessage           `json:"resolution_evidence"`
}

type testConfigurationDocument struct {
	Mode                        generation.ConfigurationMode `json:"mode"`
	Environment                 string                       `json:"environment,omitempty"`
	RootPath                    string                       `json:"root_path"`
	RootDigest                  string                       `json:"root_digest"`
	SelectedPath                string                       `json:"selected_path"`
	SelectedDigest              string                       `json:"selected_digest"`
	DependencyCompositionDigest string                       `json:"dependency_composition_digest"`
}

type testSliceDocument struct {
	Scope                  TestScope                       `json:"scope"`
	TargetPlugin           string                          `json:"target_plugin"`
	Digest                 string                          `json:"digest"`
	Plugins                []testSlicePluginDocument       `json:"plugins"`
	Capabilities           []testSliceCapabilityDocument   `json:"capabilities"`
	Providers              []testSliceProviderDocument     `json:"providers"`
	Intrinsics             []testSliceIntrinsicDocument    `json:"intrinsics"`
	GeneratedContributions []testSliceContributionDocument `json:"generated_contributions"`
	Replacements           []testReplacementDocument       `json:"replacements"`
}

type testSlicePluginDocument struct {
	Order         uint32       `json:"order"`
	ID            string       `json:"id"`
	ProjectModule string       `json:"project_module"`
	ModuleVersion string       `json:"module_version,omitempty"`
	Sources       []testSource `json:"sources"`
}

type testSliceCapabilityDocument struct {
	ID             string       `json:"id"`
	ContractDigest string       `json:"contract_digest"`
	Sources        []testSource `json:"sources"`
}

type testSliceProviderDocument struct {
	Capability     string             `json:"capability"`
	PluginID       string             `json:"plugin_id"`
	ContractDigest string             `json:"contract_digest"`
	Reason         TestProviderReason `json:"reason"`
	Sources        []testSource       `json:"sources"`
}

type testSliceIntrinsicDocument struct {
	Capability     string       `json:"capability"`
	ContractDigest string       `json:"contract_digest"`
	Sources        []testSource `json:"sources"`
}

type testSliceContributionDocument struct {
	Kind                 TestContributionKind `json:"kind"`
	Namespace            string               `json:"namespace"`
	Capability           string               `json:"capability,omitempty"`
	SourceCapability     string               `json:"source_capability"`
	ActivationCapability string               `json:"activation_capability"`
	PluginID             string               `json:"plugin_id"`
	ProjectModule        string               `json:"project_module"`
	RuleID               string               `json:"rule_id,omitempty"`
	Sources              []testSource         `json:"sources"`
}

type testReplacementDocument struct {
	Capability     string       `json:"capability"`
	PluginID       string       `json:"plugin_id"`
	ContractDigest string       `json:"contract_digest"`
	Sources        []testSource `json:"sources"`
}

type testOutcomeDocument struct {
	Order    uint32                `json:"order"`
	ID       string                `json:"id"`
	Kind     string                `json:"kind"`
	Subject  string                `json:"subject"`
	Status   TestStatus            `json:"status"`
	Summary  string                `json:"summary"`
	Failures []testFailureDocument `json:"failures"`
	Sources  []testSource          `json:"sources"`
}

type testFailureDocument struct {
	Kind    string       `json:"kind"`
	Subject string       `json:"subject"`
	Summary string       `json:"summary"`
	Sources []testSource `json:"sources"`
}

type testSource struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// TestSchemaV1 returns the immutable command-owned schema descriptor.
func TestSchemaV1() diagnosticjson.Schema { return testSchemaV1 }

// NewTest validates and constructs one complete plystra.test v1 result.
func NewTest(input TestInput) (TestResult, error) {
	if !input.Evidence.Valid() {
		return TestResult{}, fmt.Errorf("%w: resolution evidence is not valid", ErrTest)
	}
	selection, exists := input.Evidence.ConfigurationSelection()
	if !exists {
		return TestResult{}, fmt.Errorf("%w: resolution evidence omits selected configuration provenance", ErrTest)
	}
	if _, exists := input.Evidence.StaticAssembly(); !exists {
		return TestResult{}, fmt.Errorf("%w: resolution evidence omits static assembly membership", ErrTest)
	}
	if _, exists := input.Evidence.HTTPTransports(); !exists {
		return TestResult{}, fmt.Errorf("%w: resolution evidence omits selected HTTP transports", ErrTest)
	}
	for index, diagnostic := range input.Diagnostics {
		if err := validateDisplayText(fmt.Sprintf("diagnostics[%d].message", index), diagnostic.Message); err != nil {
			return TestResult{}, fmt.Errorf("%w: %v", ErrTest, err)
		}
	}

	configuration := testConfigurationIdentity(selection)
	sliceInput := cloneTestSliceInput(input.Slice)
	slice, err := normalizeTestSlice(input.Evidence, configuration, sliceInput)
	if err != nil {
		return TestResult{}, fmt.Errorf("%w: slice: %v", ErrTest, err)
	}
	outcomes, status, failedCount, err := normalizeTestOutcomes(selection.Mode(), input.Evidence.BuildModelDigest(), input.Outcomes)
	if err != nil {
		return TestResult{}, fmt.Errorf("%w: %v", ErrTest, err)
	}

	allSources := collectSources(input.Evidence, input.Sources)
	allSources = append(allSources, testSliceSources(slice)...)
	for _, outcome := range outcomes {
		allSources = append(allSources, outcome.Sources...)
		for _, failure := range outcome.Failures {
			allSources = append(allSources, failure.Sources...)
		}
	}
	allSources, err = normalizeTestSources(selection.Mode(), input.Evidence.BuildModelDigest(), allSources)
	if err != nil {
		return TestResult{}, fmt.Errorf("%w: sources: %v", ErrTest, err)
	}

	evidenceJSON := input.Evidence.CanonicalJSON()
	document := makeTestDocument(status, failedCount, input.Evidence.SelectedModelDigest(), configuration, slice, outcomes, evidenceJSON)
	resultJSON, err := encodeTestDocument(document)
	if err != nil {
		return TestResult{}, fmt.Errorf("%w: encode result: %v", ErrTest, err)
	}
	envelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 testSchemaV1,
		ConfigurationMode:      selection.Mode(),
		ApplicationModelDigest: input.Evidence.BuildModelDigest(),
		Diagnostics:            input.Diagnostics,
		Sources:                allSources,
		Result:                 resultJSON,
	})
	if err != nil {
		return TestResult{}, fmt.Errorf("%w: shared envelope: %v", ErrTest, err)
	}
	return TestResult{
		envelope:               envelope,
		evidence:               input.Evidence,
		configuration:          configuration,
		sliceInput:             sliceInput,
		slice:                  slice,
		status:                 status,
		failedOutcomeCount:     failedCount,
		outcomes:               outcomes,
		resolutionEvidenceJSON: evidenceJSON,
		prepared:               true,
	}, nil
}

// Valid reports whether NewTest produced this internally consistent result.
func (r TestResult) Valid() bool {
	if !r.prepared || !r.evidence.Valid() || !r.envelope.Valid() || r.envelope.Schema() != testSchemaV1 || r.envelope.ApplicationModelDigest() != r.evidence.BuildModelDigest() {
		return false
	}
	selection, exists := r.evidence.ConfigurationSelection()
	if !exists || selection.Mode() != r.envelope.ConfigurationMode() || r.configuration != testConfigurationIdentity(selection) {
		return false
	}
	if _, exists := r.evidence.StaticAssembly(); !exists {
		return false
	}
	if _, exists := r.evidence.HTTPTransports(); !exists {
		return false
	}
	for index, diagnostic := range r.envelope.Diagnostics() {
		if validateDisplayText(fmt.Sprintf("diagnostics[%d].message", index), diagnostic.Message) != nil {
			return false
		}
	}
	slice, err := normalizeTestSlice(r.evidence, r.configuration, r.sliceInput)
	if err != nil || !equalTestSlices(slice, r.slice) {
		return false
	}
	outcomes, status, failedCount, err := normalizeTestOutcomes(selection.Mode(), r.evidence.BuildModelDigest(), r.outcomes)
	if err != nil || status != r.status || failedCount != r.failedOutcomeCount || !equalTestOutcomes(outcomes, r.outcomes) {
		return false
	}
	if !bytes.Equal(r.resolutionEvidenceJSON, r.evidence.CanonicalJSON()) {
		return false
	}
	resultJSON, err := encodeTestDocument(makeTestDocument(r.status, r.failedOutcomeCount, r.evidence.SelectedModelDigest(), r.configuration, r.slice, r.outcomes, append([]byte(nil), r.resolutionEvidenceJSON...)))
	if err != nil {
		return false
	}
	canonicalEnvelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 testSchemaV1,
		ConfigurationMode:      selection.Mode(),
		ApplicationModelDigest: r.evidence.BuildModelDigest(),
		Diagnostics:            r.envelope.Diagnostics(),
		Sources:                r.envelope.Sources(),
		Result:                 resultJSON,
	})
	return err == nil && bytes.Equal(canonicalEnvelope.CanonicalJSON(), r.envelope.CanonicalJSON())
}

// Envelope returns the immutable shared diagnostic envelope.
func (r TestResult) Envelope() diagnosticjson.Envelope { return r.envelope }

// Configuration returns the selected non-secret configuration identity.
func (r TestResult) Configuration() TestConfigurationIdentity { return r.configuration }

// Slice returns a defensive copy of the selected test model.
func (r TestResult) Slice() TestSlice { return cloneTestSlice(r.slice) }

// Status returns passed or failed.
func (r TestResult) Status() TestStatus { return r.status }

// FailedOutcomeCount returns the number of failed ordered outcomes.
func (r TestResult) FailedOutcomeCount() int { return r.failedOutcomeCount }

// Outcomes returns a defensive copy in contiguous execution order.
func (r TestResult) Outcomes() []TestOutcome { return cloneTestOutcomes(r.outcomes) }

// ResolutionEvidenceJSON returns a defensive copy of the complete canonical
// resolution-evidence document embedded in this command result.
func (r TestResult) ResolutionEvidenceJSON() []byte {
	return append([]byte(nil), r.resolutionEvidenceJSON...)
}

func testConfigurationIdentity(selection resolutionevidence.ConfigurationSelection) TestConfigurationIdentity {
	return TestConfigurationIdentity{
		Mode:                        selection.Mode(),
		Environment:                 selection.Environment(),
		RootPath:                    selection.RootPath(),
		RootDigest:                  selection.RootDigest(),
		SelectedPath:                selection.SelectedPath(),
		SelectedDigest:              selection.SelectedDigest(),
		DependencyCompositionDigest: selection.DependencyCompositionDigest(),
	}
}

func normalizeTestSlice(evidence resolutionevidence.Evidence, configuration TestConfigurationIdentity, input TestSliceInput) (TestSlice, error) {
	assembly, exists := evidence.StaticAssembly()
	if !exists {
		return TestSlice{}, errors.New("static assembly membership is required")
	}
	switch input.Scope {
	case TestScopeProject:
		if input.TargetPlugin != "" || len(input.Plugins) != 0 || len(input.Providers) != 0 || len(input.GeneratedContributions) != 0 {
			return TestSlice{}, errors.New("project scope derives complete membership and accepts no explicit slice members")
		}
		slice, err := projectTestSlice(evidence, assembly)
		if err != nil {
			return TestSlice{}, err
		}
		slice.Digest, err = testSliceDigest(evidence, configuration, slice)
		return slice, err
	case TestScopePlugin:
		slice, err := pluginTestSlice(evidence, assembly, input)
		if err != nil {
			return TestSlice{}, err
		}
		slice.Digest, err = testSliceDigest(evidence, configuration, slice)
		return slice, err
	default:
		return TestSlice{}, fmt.Errorf("scope %q is not supported", input.Scope)
	}
}

func projectTestSlice(evidence resolutionevidence.Evidence, assembly resolutionevidence.StaticAssembly) (TestSlice, error) {
	if len(assembly.Plugins()) > maximumTestPluginCount {
		return TestSlice{}, fmt.Errorf("plugin count exceeds %d", maximumTestPluginCount)
	}
	if len(assembly.Bindings()) > maximumTestProviderCount {
		return TestSlice{}, fmt.Errorf("provider count exceeds %d", maximumTestProviderCount)
	}
	if evidence.GenerationActivationCount()+evidence.GeneratedRequirementCount() > maximumTestContributionCount {
		return TestSlice{}, fmt.Errorf("generated contribution count exceeds %d", maximumTestContributionCount)
	}
	plugins := make([]TestSlicePlugin, 0, len(assembly.Plugins()))
	for _, plugin := range assembly.Plugins() {
		sources, err := normalizeTestSourcesFromEvidence(evidence, []resolutionevidence.Source{plugin.Source()})
		if err != nil {
			return TestSlice{}, fmt.Errorf("plugin %s sources: %v", plugin.PluginID(), err)
		}
		plugins = append(plugins, TestSlicePlugin{Order: uint32(plugin.ConstructorOrder()), ID: plugin.PluginID(), ProjectModule: plugin.ProjectModule(), ModuleVersion: plugin.ModuleVersion(), Sources: sources})
	}
	providers, intrinsics, capabilities, err := projectTestProviders(evidence, assembly.Bindings())
	if err != nil {
		return TestSlice{}, err
	}
	contributions, err := allTestContributions(evidence)
	if err != nil {
		return TestSlice{}, err
	}
	return TestSlice{Scope: TestScopeProject, Plugins: plugins, Capabilities: capabilities, Providers: providers, Intrinsics: intrinsics, GeneratedContributions: contributions, Replacements: []TestProviderReplacement{}}, nil
}

func projectTestProviders(evidence resolutionevidence.Evidence, bindings []resolutionevidence.AssemblyBinding) ([]TestSliceProvider, []TestSliceIntrinsic, []TestSliceCapability, error) {
	providers := make([]TestSliceProvider, 0, len(bindings))
	intrinsics := make([]TestSliceIntrinsic, 0, len(bindings))
	capabilities := make([]TestSliceCapability, 0, len(bindings))
	for _, binding := range bindings {
		capabilitySources := testCapabilitySources(evidence, binding.Capability(), binding.ProviderSource())
		sources, err := normalizeTestSourcesFromEvidence(evidence, capabilitySources)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("capability %s sources: %v", binding.Capability(), err)
		}
		capabilities = append(capabilities, TestSliceCapability{ID: binding.Capability(), ContractDigest: binding.ContractDigest(), Sources: sources})
		providerSources, err := normalizeTestSourcesFromEvidence(evidence, []resolutionevidence.Source{binding.ProviderSource()})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("provider %s sources: %v", binding.Capability(), err)
		}
		if binding.Intrinsic() {
			intrinsics = append(intrinsics, TestSliceIntrinsic{Capability: binding.Capability(), ContractDigest: binding.ContractDigest(), Sources: providerSources})
			continue
		}
		providers = append(providers, TestSliceProvider{Capability: binding.Capability(), PluginID: binding.PluginID(), ContractDigest: binding.ContractDigest(), Reason: TestProviderApplicationSelection, Sources: providerSources})
	}
	sortTestSliceMembers(providers, intrinsics, capabilities)
	return providers, intrinsics, capabilities, nil
}

func pluginTestSlice(evidence resolutionevidence.Evidence, assembly resolutionevidence.StaticAssembly, input TestSliceInput) (TestSlice, error) {
	if input.TargetPlugin == "" {
		return TestSlice{}, errors.New("target Plugin is required for plugin scope")
	}
	if len(input.Plugins) == 0 {
		return TestSlice{}, errors.New("at least one selected Plugin is required for plugin scope")
	}
	if len(input.Plugins) > maximumTestPluginCount {
		return TestSlice{}, fmt.Errorf("plugin count exceeds %d", maximumTestPluginCount)
	}
	if len(input.Providers) > maximumTestProviderCount {
		return TestSlice{}, fmt.Errorf("provider count exceeds %d", maximumTestProviderCount)
	}
	if len(input.GeneratedContributions) > maximumTestContributionCount {
		return TestSlice{}, fmt.Errorf("generated contribution count exceeds %d", maximumTestContributionCount)
	}

	candidates := make(map[string]resolutionevidence.PluginCandidate)
	for _, candidate := range evidence.PluginCandidates() {
		candidates[candidate.ID()] = candidate
	}
	target, exists := candidates[input.TargetPlugin]
	if !exists || !target.Local() {
		return TestSlice{}, fmt.Errorf("target Plugin %q is not a current-Project Plugin", input.TargetPlugin)
	}
	modules := make(map[string]resolutionevidence.Module)
	for _, module := range evidence.Modules() {
		modules[module.Path()] = module
	}
	selectedVersions := make(map[string]string)
	for _, plugin := range evidence.SelectedPlugins() {
		selectedVersions[plugin.ID()] = plugin.ModuleVersion()
	}

	pluginInputs := append([]TestSlicePluginInput(nil), input.Plugins...)
	pluginIDs := make(map[string]struct{}, len(pluginInputs))
	orders := make(map[uint32]struct{}, len(pluginInputs))
	plugins := make([]TestSlicePlugin, len(pluginInputs))
	for index, value := range pluginInputs {
		if value.Order == 0 {
			return TestSlice{}, fmt.Errorf("plugins[%d].order must be positive", index)
		}
		if _, duplicate := orders[value.Order]; duplicate {
			return TestSlice{}, fmt.Errorf("plugins[%d].order %d is duplicated", index, value.Order)
		}
		candidate, exists := candidates[value.PluginID]
		if !exists {
			return TestSlice{}, fmt.Errorf("plugins[%d] %q is absent from resolution evidence", index, value.PluginID)
		}
		if _, duplicate := pluginIDs[value.PluginID]; duplicate {
			return TestSlice{}, fmt.Errorf("plugins[%d] %q is duplicated", index, value.PluginID)
		}
		version := selectedVersions[value.PluginID]
		if version == "" {
			version = modules[candidate.ModulePath()].SelectedVersion()
		}
		sources, err := normalizeTestSourcesFromEvidence(evidence, []resolutionevidence.Source{candidate.Source()})
		if err != nil {
			return TestSlice{}, fmt.Errorf("plugins[%d].sources: %v", index, err)
		}
		orders[value.Order] = struct{}{}
		pluginIDs[value.PluginID] = struct{}{}
		plugins[index] = TestSlicePlugin{Order: value.Order, ID: value.PluginID, ProjectModule: candidate.ModulePath(), ModuleVersion: version, Sources: sources}
	}
	if _, exists := pluginIDs[input.TargetPlugin]; !exists {
		return TestSlice{}, fmt.Errorf("target Plugin %q is absent from selected slice Plugins", input.TargetPlugin)
	}
	sort.Slice(plugins, func(left, right int) bool { return plugins[left].Order < plugins[right].Order })
	for index, plugin := range plugins {
		if plugin.Order != uint32(index+1) {
			return TestSlice{}, fmt.Errorf("plugin order must be contiguous from 1; position %d has order %d", index+1, plugin.Order)
		}
	}

	providers, intrinsics, capabilities, replacements, referencedPlugins, capabilitySet, err := normalizeTargetedProviders(evidence, input.Providers, pluginIDs)
	if err != nil {
		return TestSlice{}, err
	}
	contributions, contributionPlugins, err := normalizeTargetedContributions(evidence, input.GeneratedContributions, capabilitySet, pluginIDs)
	if err != nil {
		return TestSlice{}, err
	}
	referencedPlugins[input.TargetPlugin] = struct{}{}
	for plugin := range contributionPlugins {
		referencedPlugins[plugin] = struct{}{}
	}
	for plugin := range pluginIDs {
		if _, referenced := referencedPlugins[plugin]; !referenced {
			return TestSlice{}, fmt.Errorf("selected Plugin %q is unrelated to the target, Providers, or generated contributions", plugin)
		}
	}
	if err := validateTargetedAssemblyClosure(input.TargetPlugin, assembly, pluginIDs, capabilitySet, providers); err != nil {
		return TestSlice{}, err
	}
	return TestSlice{Scope: TestScopePlugin, TargetPlugin: input.TargetPlugin, Plugins: plugins, Capabilities: capabilities, Providers: providers, Intrinsics: intrinsics, GeneratedContributions: contributions, Replacements: replacements}, nil
}

func normalizeTargetedProviders(evidence resolutionevidence.Evidence, input []TestProviderInput, pluginIDs map[string]struct{}) ([]TestSliceProvider, []TestSliceIntrinsic, []TestSliceCapability, []TestProviderReplacement, map[string]struct{}, map[string]struct{}, error) {
	candidates := make(map[string]resolutionevidence.ProviderCandidate)
	for _, candidate := range evidence.ProviderCandidates() {
		candidates[candidate.Capability()+"\x00"+candidate.PluginID()] = candidate
	}
	selected := make(map[string]resolutionevidence.SelectedProvider)
	for _, provider := range evidence.SelectedProviders() {
		selected[provider.Capability()] = provider
	}
	bindings := make(map[string]resolutionevidence.AssemblyBinding)
	if assembly, exists := evidence.StaticAssembly(); exists {
		for _, binding := range assembly.Bindings() {
			bindings[binding.Capability()] = binding
		}
	}
	providers := make([]TestSliceProvider, 0, len(input))
	intrinsics := make([]TestSliceIntrinsic, 0, len(input))
	capabilities := make([]TestSliceCapability, 0, len(input))
	replacements := make([]TestProviderReplacement, 0, len(input))
	referencedPlugins := make(map[string]struct{})
	capabilitySet := make(map[string]struct{}, len(input))
	for index, value := range input {
		if value.Capability == "" {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("providers[%d].capability is required", index)
		}
		if _, duplicate := capabilitySet[value.Capability]; duplicate {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("providers[%d].capability %q is duplicated", index, value.Capability)
		}
		if value.PluginID == "" {
			if value.Replacement {
				return nil, nil, nil, nil, nil, nil, fmt.Errorf("providers[%d] cannot replace an intrinsic Provider", index)
			}
			binding, exists := bindings[value.Capability]
			if !exists || !binding.Intrinsic() {
				return nil, nil, nil, nil, nil, nil, fmt.Errorf("providers[%d] %q is not a Kernel intrinsic", index, value.Capability)
			}
			sources, err := normalizeTestSourcesFromEvidence(evidence, []resolutionevidence.Source{binding.ProviderSource()})
			if err != nil {
				return nil, nil, nil, nil, nil, nil, fmt.Errorf("providers[%d].sources: %v", index, err)
			}
			intrinsics = append(intrinsics, TestSliceIntrinsic{Capability: value.Capability, ContractDigest: binding.ContractDigest(), Sources: sources})
			capabilitySources, err := normalizeTestSourcesFromEvidence(evidence, testCapabilitySources(evidence, value.Capability, binding.ProviderSource()))
			if err != nil {
				return nil, nil, nil, nil, nil, nil, fmt.Errorf("providers[%d].capability sources: %v", index, err)
			}
			capabilities = append(capabilities, TestSliceCapability{ID: value.Capability, ContractDigest: binding.ContractDigest(), Sources: capabilitySources})
			capabilitySet[value.Capability] = struct{}{}
			continue
		}
		if _, exists := pluginIDs[value.PluginID]; !exists {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("providers[%d] Plugin %q is absent from selected slice Plugins", index, value.PluginID)
		}
		candidate, exists := candidates[value.Capability+"\x00"+value.PluginID]
		if !exists {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("providers[%d] %s=%s is not a visible structurally conforming Provider", index, value.Capability, value.PluginID)
		}
		base, baseExists := selected[value.Capability]
		reason := TestProviderSliceSelection
		providerSources := []resolutionevidence.Source{candidate.Source()}
		if value.Replacement {
			if baseExists && base.PluginID() == value.PluginID {
				return nil, nil, nil, nil, nil, nil, fmt.Errorf("providers[%d] replacement repeats the selected application Provider", index)
			}
			if baseExists && base.ContractDigest() != candidate.ContractDigest() {
				return nil, nil, nil, nil, nil, nil, fmt.Errorf("providers[%d] replacement contract differs from the canonical requirement", index)
			}
			reason = TestProviderInvocationReplacement
		} else if baseExists {
			if base.Intrinsic() || base.PluginID() != value.PluginID {
				return nil, nil, nil, nil, nil, nil, fmt.Errorf("providers[%d] differs from the application selection without Replacement", index)
			}
			reason = TestProviderApplicationSelection
			providerSources = append(providerSources, base.ProviderSource())
			for _, source := range base.SelectionSources() {
				providerSources = append(providerSources, source.Source())
			}
		}
		sources, err := normalizeTestSourcesFromEvidence(evidence, providerSources)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("providers[%d].sources: %v", index, err)
		}
		providers = append(providers, TestSliceProvider{Capability: value.Capability, PluginID: value.PluginID, ContractDigest: candidate.ContractDigest(), Reason: reason, Sources: sources})
		capabilitySources, err := normalizeTestSourcesFromEvidence(evidence, testCapabilitySources(evidence, value.Capability, candidate.Source()))
		if err != nil {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("providers[%d].capability sources: %v", index, err)
		}
		capabilities = append(capabilities, TestSliceCapability{ID: value.Capability, ContractDigest: candidate.ContractDigest(), Sources: capabilitySources})
		if value.Replacement {
			replacements = append(replacements, TestProviderReplacement{Capability: value.Capability, PluginID: value.PluginID, ContractDigest: candidate.ContractDigest(), Sources: sources})
		}
		referencedPlugins[value.PluginID] = struct{}{}
		capabilitySet[value.Capability] = struct{}{}
	}
	sortTestSliceMembers(providers, intrinsics, capabilities)
	sort.Slice(replacements, func(left, right int) bool { return replacements[left].Capability < replacements[right].Capability })
	return providers, intrinsics, capabilities, replacements, referencedPlugins, capabilitySet, nil
}

func normalizeTargetedContributions(evidence resolutionevidence.Evidence, input []TestContributionInput, capabilities, plugins map[string]struct{}) ([]TestSliceContribution, map[string]struct{}, error) {
	available := make(map[string]TestSliceContribution)
	for _, activation := range evidence.GenerationActivations() {
		record, err := testActivationContribution(evidence, activation)
		if err != nil {
			return nil, nil, err
		}
		available[testContributionKeyFromRecord(record)] = record
	}
	for _, requirement := range evidence.GeneratedRequirements() {
		record, err := testRequirementContribution(evidence, requirement)
		if err != nil {
			return nil, nil, err
		}
		available[testContributionKeyFromRecord(record)] = record
	}
	result := make([]TestSliceContribution, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	contributionPlugins := make(map[string]struct{})
	for index, value := range input {
		key := testContributionKey(value)
		if _, duplicate := seen[key]; duplicate {
			return nil, nil, fmt.Errorf("generated_contributions[%d] is duplicated", index)
		}
		record, exists := available[key]
		if !exists {
			return nil, nil, fmt.Errorf("generated_contributions[%d] is absent from resolution evidence", index)
		}
		if _, exists := plugins[record.PluginID]; !exists {
			return nil, nil, fmt.Errorf("generated_contributions[%d] Plugin %q is absent from selected slice Plugins", index, record.PluginID)
		}
		if _, exists := capabilities[record.SourceCapability]; !exists {
			return nil, nil, fmt.Errorf("generated_contributions[%d] source Capability %q is absent from selected slice", index, record.SourceCapability)
		}
		if _, exists := capabilities[record.ActivationCapability]; !exists {
			return nil, nil, fmt.Errorf("generated_contributions[%d] activation Capability %q is absent from selected slice", index, record.ActivationCapability)
		}
		if record.Kind == TestContributionRequirement {
			if _, exists := capabilities[record.Capability]; !exists {
				return nil, nil, fmt.Errorf("generated_contributions[%d] generated Capability %q is absent from selected slice", index, record.Capability)
			}
		}
		seen[key] = struct{}{}
		contributionPlugins[record.PluginID] = struct{}{}
		result = append(result, record)
	}
	for _, requirement := range evidence.GeneratedRequirements() {
		if _, selected := capabilities[requirement.Capability()]; !selected {
			continue
		}
		record, err := testRequirementContribution(evidence, requirement)
		if err != nil {
			return nil, nil, err
		}
		if _, exists := seen[testContributionKeyFromRecord(record)]; !exists {
			return nil, nil, fmt.Errorf("generated requirement %s from rule %s is missing from selected slice evidence", requirement.Capability(), requirement.RuleID())
		}
		activationPresent := false
		for _, candidate := range result {
			if candidate.Kind == TestContributionActivation && candidate.Namespace == record.Namespace && candidate.SourceCapability == record.SourceCapability && candidate.ActivationCapability == record.ActivationCapability && candidate.PluginID == record.PluginID {
				activationPresent = true
				break
			}
		}
		if !activationPresent {
			return nil, nil, fmt.Errorf("generated requirement %s omits its selected activation", requirement.Capability())
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return testContributionKeyFromRecord(result[left]) < testContributionKeyFromRecord(result[right])
	})
	return result, contributionPlugins, nil
}

func validateTargetedAssemblyClosure(target string, assembly resolutionevidence.StaticAssembly, pluginIDs, capabilities map[string]struct{}, providers []TestSliceProvider) error {
	assemblyPlugins := make(map[string]resolutionevidence.AssemblyPlugin)
	for _, plugin := range assembly.Plugins() {
		assemblyPlugins[plugin.PluginID()] = plugin
	}
	for pluginID := range pluginIDs {
		plugin, exists := assemblyPlugins[pluginID]
		if !exists {
			continue
		}
		for _, required := range plugin.RequiredClients() {
			if _, exists := capabilities[required]; !exists {
				return fmt.Errorf("selected Plugin %q requires missing slice Capability %q", pluginID, required)
			}
		}
	}
	plugin, exists := assemblyPlugins[target]
	if !exists {
		return fmt.Errorf("target Plugin %q is absent from static assembly", target)
	}
	providerIndex := make(map[string]string, len(providers))
	for _, provider := range providers {
		providerIndex[provider.Capability] = provider.PluginID
	}
	for _, binding := range plugin.ProviderBindings() {
		if providerIndex[binding] != target {
			return fmt.Errorf("target Plugin %q Provider binding %q is absent or replaced", target, binding)
		}
	}
	return nil
}

func allTestContributions(evidence resolutionevidence.Evidence) ([]TestSliceContribution, error) {
	result := make([]TestSliceContribution, 0, evidence.GenerationActivationCount()+evidence.GeneratedRequirementCount())
	for _, activation := range evidence.GenerationActivations() {
		record, err := testActivationContribution(evidence, activation)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	for _, requirement := range evidence.GeneratedRequirements() {
		record, err := testRequirementContribution(evidence, requirement)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	sort.Slice(result, func(left, right int) bool {
		return testContributionKeyFromRecord(result[left]) < testContributionKeyFromRecord(result[right])
	})
	return result, nil
}

func testActivationContribution(evidence resolutionevidence.Evidence, activation resolutionevidence.GenerationActivation) (TestSliceContribution, error) {
	sources := make([]resolutionevidence.Source, 0, len(activation.Causes()))
	for _, cause := range activation.Causes() {
		sources = append(sources, cause.Source())
	}
	normalized, err := normalizeTestSourcesFromEvidence(evidence, sources)
	if err != nil {
		return TestSliceContribution{}, fmt.Errorf("activation %s sources: %v", activation.Namespace(), err)
	}
	return TestSliceContribution{Kind: TestContributionActivation, Namespace: activation.Namespace(), SourceCapability: activation.SourceCapability(), ActivationCapability: activation.ActivationCapability(), PluginID: activation.PluginID(), ProjectModule: activation.ProjectModule(), Sources: normalized}, nil
}

func testRequirementContribution(evidence resolutionevidence.Evidence, requirement resolutionevidence.GeneratedRequirement) (TestSliceContribution, error) {
	sources, err := normalizeTestSourcesFromEvidence(evidence, []resolutionevidence.Source{requirement.Source()})
	if err != nil {
		return TestSliceContribution{}, fmt.Errorf("generated requirement %s sources: %v", requirement.Capability(), err)
	}
	return TestSliceContribution{Kind: TestContributionRequirement, Namespace: requirement.Namespace(), Capability: requirement.Capability(), SourceCapability: requirement.SourceCapability(), ActivationCapability: requirement.ActivationCapability(), PluginID: requirement.PluginID(), ProjectModule: requirement.ProjectModule(), RuleID: requirement.RuleID(), Sources: sources}, nil
}

func testContributionKey(value TestContributionInput) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s", value.Kind, value.Namespace, value.Capability, value.SourceCapability, value.ActivationCapability, value.PluginID, value.RuleID)
}

func testContributionKeyFromRecord(value TestSliceContribution) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s", value.Kind, value.Namespace, value.Capability, value.SourceCapability, value.ActivationCapability, value.PluginID, value.RuleID)
}

func testCapabilitySources(evidence resolutionevidence.Evidence, capability string, provider resolutionevidence.Source) []resolutionevidence.Source {
	result := []resolutionevidence.Source{provider}
	for _, requirement := range evidence.Requirements() {
		if requirement.Capability() != capability {
			continue
		}
		for _, source := range requirement.Sources() {
			result = append(result, source.Source())
		}
	}
	return result
}

func normalizeTestSourcesFromEvidence(evidence resolutionevidence.Evidence, values []resolutionevidence.Source) ([]diagnosticjson.Source, error) {
	selection, exists := evidence.ConfigurationSelection()
	if !exists {
		return nil, errors.New("configuration selection is absent")
	}
	converted := make([]diagnosticjson.Source, len(values))
	for index, source := range values {
		converted[index] = diagnosticjson.Source{Module: source.Module(), Path: source.Path(), Kind: source.Kind(), Line: source.Line(), Column: source.Column()}
	}
	return normalizeTestSources(selection.Mode(), evidence.BuildModelDigest(), converted)
}

func sortTestSliceMembers(providers []TestSliceProvider, intrinsics []TestSliceIntrinsic, capabilities []TestSliceCapability) {
	sort.Slice(providers, func(left, right int) bool { return providers[left].Capability < providers[right].Capability })
	sort.Slice(intrinsics, func(left, right int) bool { return intrinsics[left].Capability < intrinsics[right].Capability })
	sort.Slice(capabilities, func(left, right int) bool { return capabilities[left].ID < capabilities[right].ID })
}

func testSliceDigest(evidence resolutionevidence.Evidence, configuration TestConfigurationIdentity, slice TestSlice) (string, error) {
	identity := struct {
		SelectedModelDigest string                    `json:"selected_model_digest"`
		ApplicationDigest   string                    `json:"application_model_digest"`
		Configuration       testConfigurationDocument `json:"configuration"`
		Slice               testSliceDocument         `json:"slice"`
	}{
		SelectedModelDigest: evidence.SelectedModelDigest(),
		ApplicationDigest:   evidence.BuildModelDigest(),
		Configuration:       testConfigurationDocumentFrom(configuration),
		Slice:               testSliceDocumentFrom(slice),
	}
	identity.Slice.Digest = ""
	canonical, err := json.Marshal(identity)
	if err != nil {
		return "", fmt.Errorf("encode slice identity: %v", err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func normalizeTestOutcomes(mode generation.ConfigurationMode, digest string, input []TestOutcome) ([]TestOutcome, TestStatus, int, error) {
	if len(input) == 0 {
		return nil, "", 0, errors.New("at least one test outcome is required")
	}
	if len(input) > maximumTestOutcomeCount {
		return nil, "", 0, fmt.Errorf("test outcome count exceeds %d", maximumTestOutcomeCount)
	}
	outcomes := make([]TestOutcome, len(input))
	ids := make(map[string]struct{}, len(input))
	orders := make(map[uint32]struct{}, len(input))
	failedCount := 0
	completedCount := 0
	totalFailureCount := 0
	for index, value := range input {
		if value.Order == 0 {
			return nil, "", 0, fmt.Errorf("outcomes[%d].order must be positive", index)
		}
		if _, duplicate := orders[value.Order]; duplicate {
			return nil, "", 0, fmt.Errorf("outcomes[%d].order %d is duplicated", index, value.Order)
		}
		if !validExplanationCode(value.ID) {
			return nil, "", 0, fmt.Errorf("outcomes[%d].id %q is not canonical lower kebab case", index, value.ID)
		}
		if _, duplicate := ids[value.ID]; duplicate {
			return nil, "", 0, fmt.Errorf("outcomes[%d].id %q is duplicated", index, value.ID)
		}
		if !validExplanationCode(value.Kind) {
			return nil, "", 0, fmt.Errorf("outcomes[%d].kind %q is not canonical lower kebab case", index, value.Kind)
		}
		if !validCheckSubject(value.Subject) {
			return nil, "", 0, fmt.Errorf("outcomes[%d].subject %q is not a safe stable identity", index, value.Subject)
		}
		if !validTestStatus(value.Status) {
			return nil, "", 0, fmt.Errorf("outcomes[%d].status %q is not supported", index, value.Status)
		}
		if err := validateDisplayText(fmt.Sprintf("outcomes[%d].summary", index), value.Summary); err != nil {
			return nil, "", 0, err
		}
		totalFailureCount += len(value.Failures)
		if totalFailureCount > maximumTestFailureCount {
			return nil, "", 0, fmt.Errorf("total failure count exceeds %d", maximumTestFailureCount)
		}
		failures, err := normalizeTestFailures(mode, digest, index, value.Failures)
		if err != nil {
			return nil, "", 0, err
		}
		switch value.Status {
		case TestStatusPassed:
			completedCount++
			if len(failures) != 0 {
				return nil, "", 0, fmt.Errorf("outcomes[%d] passed but contains failure details", index)
			}
		case TestStatusFailed:
			completedCount++
			failedCount++
			if len(failures) == 0 {
				return nil, "", 0, fmt.Errorf("outcomes[%d] failed without a structured failure", index)
			}
		case TestStatusSkipped:
			if len(failures) != 0 {
				return nil, "", 0, fmt.Errorf("outcomes[%d] was skipped but contains failure details", index)
			}
		}
		sources, err := normalizeTestSources(mode, digest, value.Sources)
		if err != nil {
			return nil, "", 0, fmt.Errorf("outcomes[%d].sources: %v", index, err)
		}
		ids[value.ID] = struct{}{}
		orders[value.Order] = struct{}{}
		outcomes[index] = TestOutcome{Order: value.Order, ID: value.ID, Kind: value.Kind, Subject: value.Subject, Status: value.Status, Summary: value.Summary, Failures: failures, Sources: sources}
	}
	if completedCount == 0 {
		return nil, "", 0, errors.New("all test outcomes are skipped")
	}
	sort.Slice(outcomes, func(left, right int) bool { return outcomes[left].Order < outcomes[right].Order })
	for index, outcome := range outcomes {
		if outcome.Order != uint32(index+1) {
			return nil, "", 0, fmt.Errorf("test outcome order must be contiguous from 1; position %d has order %d", index+1, outcome.Order)
		}
	}
	status := TestStatusPassed
	if failedCount > 0 {
		status = TestStatusFailed
	}
	return outcomes, status, failedCount, nil
}

func normalizeTestFailures(mode generation.ConfigurationMode, digest string, outcomeIndex int, input []TestFailure) ([]TestFailure, error) {
	if len(input) > maximumTestFailureCount {
		return nil, fmt.Errorf("outcomes[%d].failure count exceeds %d", outcomeIndex, maximumTestFailureCount)
	}
	failures := make([]TestFailure, len(input))
	identities := make(map[string]struct{}, len(input))
	for index, value := range input {
		if !validExplanationCode(value.Kind) {
			return nil, fmt.Errorf("outcomes[%d].failures[%d].kind %q is not canonical lower kebab case", outcomeIndex, index, value.Kind)
		}
		if !validCheckSubject(value.Subject) {
			return nil, fmt.Errorf("outcomes[%d].failures[%d].subject %q is not a safe stable identity", outcomeIndex, index, value.Subject)
		}
		identity := value.Kind + "\x00" + value.Subject
		if _, duplicate := identities[identity]; duplicate {
			return nil, fmt.Errorf("outcomes[%d].failures[%d] duplicates %s for %s", outcomeIndex, index, value.Kind, value.Subject)
		}
		if err := validateDisplayText(fmt.Sprintf("outcomes[%d].failures[%d].summary", outcomeIndex, index), value.Summary); err != nil {
			return nil, err
		}
		sources, err := normalizeTestSources(mode, digest, value.Sources)
		if err != nil {
			return nil, fmt.Errorf("outcomes[%d].failures[%d].sources: %v", outcomeIndex, index, err)
		}
		identities[identity] = struct{}{}
		failures[index] = TestFailure{Kind: value.Kind, Subject: value.Subject, Summary: value.Summary, Sources: sources}
	}
	sort.Slice(failures, func(left, right int) bool {
		return failures[left].Kind+"\x00"+failures[left].Subject < failures[right].Kind+"\x00"+failures[right].Subject
	})
	return failures, nil
}

func validTestStatus(value TestStatus) bool {
	return value == TestStatusPassed || value == TestStatusFailed || value == TestStatusSkipped
}

func normalizeTestSources(mode generation.ConfigurationMode, digest string, values []diagnosticjson.Source) ([]diagnosticjson.Source, error) {
	return normalizeSchemaSources(testSchemaV1, mode, digest, values)
}

func makeTestDocument(status TestStatus, failedCount int, selectedDigest string, configuration TestConfigurationIdentity, slice TestSlice, outcomes []TestOutcome, evidenceJSON []byte) testDocument {
	return testDocument{
		Status:              status,
		FailedOutcomeCount:  failedCount,
		SelectedModelDigest: selectedDigest,
		Configuration:       testConfigurationDocumentFrom(configuration),
		Slice:               testSliceDocumentFrom(slice),
		Outcomes:            testOutcomeDocuments(outcomes),
		ResolutionEvidence:  evidenceJSON,
	}
}

func testConfigurationDocumentFrom(value TestConfigurationIdentity) testConfigurationDocument {
	return testConfigurationDocument(value)
}

func testSliceDocumentFrom(value TestSlice) testSliceDocument {
	return testSliceDocument{Scope: value.Scope, TargetPlugin: value.TargetPlugin, Digest: value.Digest, Plugins: testSlicePluginDocuments(value.Plugins), Capabilities: testSliceCapabilityDocuments(value.Capabilities), Providers: testSliceProviderDocuments(value.Providers), Intrinsics: testSliceIntrinsicDocuments(value.Intrinsics), GeneratedContributions: testSliceContributionDocuments(value.GeneratedContributions), Replacements: testReplacementDocuments(value.Replacements)}
}

func testSlicePluginDocuments(values []TestSlicePlugin) []testSlicePluginDocument {
	result := make([]testSlicePluginDocument, len(values))
	for index, value := range values {
		result[index] = testSlicePluginDocument{Order: value.Order, ID: value.ID, ProjectModule: value.ProjectModule, ModuleVersion: value.ModuleVersion, Sources: testSources(value.Sources)}
	}
	return result
}

func testSliceCapabilityDocuments(values []TestSliceCapability) []testSliceCapabilityDocument {
	result := make([]testSliceCapabilityDocument, len(values))
	for index, value := range values {
		result[index] = testSliceCapabilityDocument{ID: value.ID, ContractDigest: value.ContractDigest, Sources: testSources(value.Sources)}
	}
	return result
}

func testSliceProviderDocuments(values []TestSliceProvider) []testSliceProviderDocument {
	result := make([]testSliceProviderDocument, len(values))
	for index, value := range values {
		result[index] = testSliceProviderDocument{Capability: value.Capability, PluginID: value.PluginID, ContractDigest: value.ContractDigest, Reason: value.Reason, Sources: testSources(value.Sources)}
	}
	return result
}

func testSliceIntrinsicDocuments(values []TestSliceIntrinsic) []testSliceIntrinsicDocument {
	result := make([]testSliceIntrinsicDocument, len(values))
	for index, value := range values {
		result[index] = testSliceIntrinsicDocument{Capability: value.Capability, ContractDigest: value.ContractDigest, Sources: testSources(value.Sources)}
	}
	return result
}

func testSliceContributionDocuments(values []TestSliceContribution) []testSliceContributionDocument {
	result := make([]testSliceContributionDocument, len(values))
	for index, value := range values {
		result[index] = testSliceContributionDocument{Kind: value.Kind, Namespace: value.Namespace, Capability: value.Capability, SourceCapability: value.SourceCapability, ActivationCapability: value.ActivationCapability, PluginID: value.PluginID, ProjectModule: value.ProjectModule, RuleID: value.RuleID, Sources: testSources(value.Sources)}
	}
	return result
}

func testReplacementDocuments(values []TestProviderReplacement) []testReplacementDocument {
	result := make([]testReplacementDocument, len(values))
	for index, value := range values {
		result[index] = testReplacementDocument{Capability: value.Capability, PluginID: value.PluginID, ContractDigest: value.ContractDigest, Sources: testSources(value.Sources)}
	}
	return result
}

func testOutcomeDocuments(values []TestOutcome) []testOutcomeDocument {
	result := make([]testOutcomeDocument, len(values))
	for index, value := range values {
		result[index] = testOutcomeDocument{Order: value.Order, ID: value.ID, Kind: value.Kind, Subject: value.Subject, Status: value.Status, Summary: value.Summary, Failures: testFailureDocuments(value.Failures), Sources: testSources(value.Sources)}
	}
	return result
}

func testFailureDocuments(values []TestFailure) []testFailureDocument {
	result := make([]testFailureDocument, len(values))
	for index, value := range values {
		result[index] = testFailureDocument{Kind: value.Kind, Subject: value.Subject, Summary: value.Summary, Sources: testSources(value.Sources)}
	}
	return result
}

func testSources(values []diagnosticjson.Source) []testSource {
	result := make([]testSource, len(values))
	for index, source := range values {
		result[index] = testSource{Module: source.Module, Path: source.Path, Kind: source.Kind, Line: source.Line, Column: source.Column}
	}
	return result
}

func testSliceSources(slice TestSlice) []diagnosticjson.Source {
	var result []diagnosticjson.Source
	for _, plugin := range slice.Plugins {
		result = append(result, plugin.Sources...)
	}
	for _, capability := range slice.Capabilities {
		result = append(result, capability.Sources...)
	}
	for _, provider := range slice.Providers {
		result = append(result, provider.Sources...)
	}
	for _, intrinsic := range slice.Intrinsics {
		result = append(result, intrinsic.Sources...)
	}
	for _, contribution := range slice.GeneratedContributions {
		result = append(result, contribution.Sources...)
	}
	for _, replacement := range slice.Replacements {
		result = append(result, replacement.Sources...)
	}
	return result
}

func cloneTestSliceInput(input TestSliceInput) TestSliceInput {
	result := input
	result.Plugins = append([]TestSlicePluginInput(nil), input.Plugins...)
	result.Providers = append([]TestProviderInput(nil), input.Providers...)
	result.GeneratedContributions = append([]TestContributionInput(nil), input.GeneratedContributions...)
	return result
}

func cloneTestSlice(value TestSlice) TestSlice {
	result := value
	result.Plugins = append([]TestSlicePlugin(nil), value.Plugins...)
	for index := range result.Plugins {
		result.Plugins[index].Sources = append([]diagnosticjson.Source(nil), value.Plugins[index].Sources...)
	}
	result.Capabilities = append([]TestSliceCapability(nil), value.Capabilities...)
	for index := range result.Capabilities {
		result.Capabilities[index].Sources = append([]diagnosticjson.Source(nil), value.Capabilities[index].Sources...)
	}
	result.Providers = append([]TestSliceProvider(nil), value.Providers...)
	for index := range result.Providers {
		result.Providers[index].Sources = append([]diagnosticjson.Source(nil), value.Providers[index].Sources...)
	}
	result.Intrinsics = append([]TestSliceIntrinsic(nil), value.Intrinsics...)
	for index := range result.Intrinsics {
		result.Intrinsics[index].Sources = append([]diagnosticjson.Source(nil), value.Intrinsics[index].Sources...)
	}
	result.GeneratedContributions = append([]TestSliceContribution(nil), value.GeneratedContributions...)
	for index := range result.GeneratedContributions {
		result.GeneratedContributions[index].Sources = append([]diagnosticjson.Source(nil), value.GeneratedContributions[index].Sources...)
	}
	result.Replacements = append([]TestProviderReplacement(nil), value.Replacements...)
	for index := range result.Replacements {
		result.Replacements[index].Sources = append([]diagnosticjson.Source(nil), value.Replacements[index].Sources...)
	}
	return result
}

func cloneTestOutcomes(values []TestOutcome) []TestOutcome {
	result := make([]TestOutcome, len(values))
	for index, outcome := range values {
		result[index] = outcome
		result[index].Sources = append([]diagnosticjson.Source(nil), outcome.Sources...)
		result[index].Failures = cloneTestFailures(outcome.Failures)
	}
	return result
}

func cloneTestFailures(values []TestFailure) []TestFailure {
	result := make([]TestFailure, len(values))
	for index, failure := range values {
		result[index] = failure
		result[index].Sources = append([]diagnosticjson.Source(nil), failure.Sources...)
	}
	return result
}

func equalTestSlices(left, right TestSlice) bool {
	leftJSON, leftErr := json.Marshal(testSliceDocumentFrom(left))
	rightJSON, rightErr := json.Marshal(testSliceDocumentFrom(right))
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func equalTestOutcomes(left, right []TestOutcome) bool {
	leftJSON, leftErr := json.Marshal(testOutcomeDocuments(left))
	rightJSON, rightErr := json.Marshal(testOutcomeDocuments(right))
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func encodeTestDocument(document testDocument) ([]byte, error) {
	if len(document.ResolutionEvidence) == 0 || !json.Valid(document.ResolutionEvidence) {
		return nil, errors.New("resolution evidence is not valid JSON")
	}
	return json.Marshal(document)
}
