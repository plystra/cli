// Package diagnosticschema defines immutable command-owned diagnostic result
// schemas built on the shared diagnostic JSON envelope.
package diagnosticschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/resolutionevidence"
)

const maximumNextActionLength = 1_024

var (
	// ErrInspect reports incomplete, inconsistent, or unsafe input for the
	// plystra.inspect v1 result schema.
	ErrInspect = errors.New("build plystra.inspect result")

	inspectSchemaV1 = mustSchema("plystra.inspect", 1)
)

// Transport is one closed generated HTTP transport selected by the effective
// application configuration.
type Transport string

const (
	TransportConnect Transport = "connect"
	TransportREST    Transport = "rest"
)

// Readiness is the closed summary state derived from structured diagnostics.
type Readiness string

const (
	ReadinessReady          Readiness = "ready"
	ReadinessNeedsAttention Readiness = "needs-attention"
	ReadinessBlocked        Readiness = "blocked"
)

// InspectInput is the construction-only input for one plystra.inspect v1
// result. Evidence must come from filesystem-backed application resolution.
// Sources may add command-owned provenance and are deduplicated with every
// source retained by the resolution evidence.
type InspectInput struct {
	Evidence    resolutionevidence.Evidence
	Diagnostics []diagnosticjson.Diagnostic
	Sources     []diagnosticjson.Source
	NextAction  string
}

// InspectResult is one immutable plystra.inspect v1 diagnostic result.
type InspectResult struct {
	envelope                 diagnosticjson.Envelope
	evidence                 resolutionevidence.Evidence
	projectModule            string
	configurationMode        generation.ConfigurationMode
	configurationEnvironment string
	configurationPath        string
	selectedPluginCount      int
	availableCapabilityCount int
	requiredCapabilityCount  int
	exposedCapabilityCount   int
	capabilityAliasCount     int
	authnActive              bool
	authzActive              bool
	transports               []Transport
	readiness                Readiness
	problemCount             int
	nextAction               string
	resolutionEvidenceJSON   []byte
	prepared                 bool
}

type inspectDocument struct {
	Project            inspectProject       `json:"project"`
	Configuration      inspectConfiguration `json:"configuration"`
	Summary            inspectSummary       `json:"summary"`
	Readiness          inspectReadiness     `json:"readiness"`
	ResolutionEvidence json.RawMessage      `json:"resolution_evidence"`
}

type inspectProject struct {
	Module string `json:"module"`
}

type inspectConfiguration struct {
	Mode        generation.ConfigurationMode `json:"mode"`
	Environment string                       `json:"environment"`
	Path        string                       `json:"path"`
}

type inspectSummary struct {
	SelectedPluginCount      int         `json:"selected_plugin_count"`
	AvailableCapabilityCount int         `json:"available_capability_count"`
	RequiredCapabilityCount  int         `json:"required_capability_count"`
	ExposedCapabilityCount   int         `json:"exposed_capability_count"`
	CapabilityAliasCount     int         `json:"capability_alias_count"`
	AuthNActive              bool        `json:"authn_active"`
	AuthZActive              bool        `json:"authz_active"`
	Transports               []Transport `json:"transports"`
}

type inspectReadiness struct {
	State        Readiness `json:"state"`
	ProblemCount int       `json:"problem_count"`
	NextAction   string    `json:"next_action"`
}

// InspectSchemaV1 returns the immutable command-owned schema descriptor.
func InspectSchemaV1() diagnosticjson.Schema { return inspectSchemaV1 }

// NewInspect validates and constructs one complete plystra.inspect v1 result.
func NewInspect(input InspectInput) (InspectResult, error) {
	if !input.Evidence.Valid() {
		return InspectResult{}, fmt.Errorf("%w: resolution evidence is not valid", ErrInspect)
	}
	selection, exists := input.Evidence.ConfigurationSelection()
	if !exists {
		return InspectResult{}, fmt.Errorf("%w: resolution evidence omits selected configuration provenance", ErrInspect)
	}
	if _, exists := input.Evidence.StaticAssembly(); !exists {
		return InspectResult{}, fmt.Errorf("%w: resolution evidence omits static assembly membership", ErrInspect)
	}
	httpTransports, exists := input.Evidence.HTTPTransports()
	if !exists {
		return InspectResult{}, fmt.Errorf("%w: resolution evidence omits selected HTTP transports", ErrInspect)
	}
	if err := validateDisplayText("next action", input.NextAction); err != nil {
		return InspectResult{}, fmt.Errorf("%w: %v", ErrInspect, err)
	}
	for index, diagnostic := range input.Diagnostics {
		if err := validateDisplayText(fmt.Sprintf("diagnostics[%d].message", index), diagnostic.Message); err != nil {
			return InspectResult{}, fmt.Errorf("%w: %v", ErrInspect, err)
		}
	}

	projectModule := ""
	for _, module := range input.Evidence.Modules() {
		if module.Role() == resolutionevidence.ModuleRoleCurrent {
			projectModule = module.Path()
			break
		}
	}
	if projectModule == "" {
		return InspectResult{}, fmt.Errorf("%w: resolution evidence omits the current Project module", ErrInspect)
	}

	transports := make([]Transport, 0, 2)
	if httpTransports.Connect {
		transports = append(transports, TransportConnect)
	}
	if httpTransports.REST {
		transports = append(transports, TransportREST)
	}

	namespaces := make([]string, 0, input.Evidence.GenerationActivationCount())
	for _, activation := range input.Evidence.GenerationActivations() {
		namespaces = append(namespaces, activation.Namespace())
	}
	authnActive, authzActive := activationStates(namespaces)

	readiness, problemCount := diagnosticReadiness(input.Diagnostics)
	evidenceJSON := input.Evidence.CanonicalJSON()
	resultJSON, err := encodeInspectDocument(inspectDocument{
		Project: inspectProject{Module: projectModule},
		Configuration: inspectConfiguration{
			Mode:        selection.Mode(),
			Environment: selection.Environment(),
			Path:        selection.SelectedPath(),
		},
		Summary: inspectSummary{
			SelectedPluginCount:      input.Evidence.SelectedPluginCount(),
			AvailableCapabilityCount: input.Evidence.CanonicalCapabilityCount(),
			RequiredCapabilityCount:  input.Evidence.RequirementCount(),
			ExposedCapabilityCount:   input.Evidence.PublicExposureCount(),
			CapabilityAliasCount:     input.Evidence.CapabilityAliasCount(),
			AuthNActive:              authnActive,
			AuthZActive:              authzActive,
			Transports:               transports,
		},
		Readiness: inspectReadiness{
			State:        readiness,
			ProblemCount: problemCount,
			NextAction:   input.NextAction,
		},
		ResolutionEvidence: evidenceJSON,
	})
	if err != nil {
		return InspectResult{}, fmt.Errorf("%w: encode result: %v", ErrInspect, err)
	}

	sources := collectSources(input.Evidence, input.Sources)
	envelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 inspectSchemaV1,
		ConfigurationMode:      selection.Mode(),
		ApplicationModelDigest: input.Evidence.BuildModelDigest(),
		Diagnostics:            input.Diagnostics,
		Sources:                sources,
		Result:                 resultJSON,
	})
	if err != nil {
		return InspectResult{}, fmt.Errorf("%w: shared envelope: %v", ErrInspect, err)
	}

	return InspectResult{
		envelope:                 envelope,
		evidence:                 input.Evidence,
		projectModule:            projectModule,
		configurationMode:        selection.Mode(),
		configurationEnvironment: selection.Environment(),
		configurationPath:        selection.SelectedPath(),
		selectedPluginCount:      input.Evidence.SelectedPluginCount(),
		availableCapabilityCount: input.Evidence.CanonicalCapabilityCount(),
		requiredCapabilityCount:  input.Evidence.RequirementCount(),
		exposedCapabilityCount:   input.Evidence.PublicExposureCount(),
		capabilityAliasCount:     input.Evidence.CapabilityAliasCount(),
		authnActive:              authnActive,
		authzActive:              authzActive,
		transports:               transports,
		readiness:                readiness,
		problemCount:             problemCount,
		nextAction:               input.NextAction,
		resolutionEvidenceJSON:   evidenceJSON,
		prepared:                 true,
	}, nil
}

// Valid reports whether NewInspect produced this internally consistent result.
func (r InspectResult) Valid() bool {
	if !r.prepared || !r.evidence.Valid() || !r.envelope.Valid() || r.envelope.Schema() != inspectSchemaV1 || r.envelope.ConfigurationMode() != r.configurationMode || r.envelope.ApplicationModelDigest() != r.evidence.BuildModelDigest() {
		return false
	}
	selection, exists := r.evidence.ConfigurationSelection()
	if !exists || selection.Mode() != r.configurationMode || selection.Environment() != r.configurationEnvironment || selection.SelectedPath() != r.configurationPath {
		return false
	}
	currentProject := ""
	for _, module := range r.evidence.Modules() {
		if module.Role() == resolutionevidence.ModuleRoleCurrent {
			currentProject = module.Path()
			break
		}
	}
	if currentProject != r.projectModule {
		return false
	}
	if _, exists := r.evidence.StaticAssembly(); !exists {
		return false
	}
	httpTransports, exists := r.evidence.HTTPTransports()
	if !exists || !slicesEqualTransports(r.transports, httpTransports.Connect, httpTransports.REST) {
		return false
	}
	if r.selectedPluginCount != r.evidence.SelectedPluginCount() || r.availableCapabilityCount != r.evidence.CanonicalCapabilityCount() || r.requiredCapabilityCount != r.evidence.RequirementCount() || r.exposedCapabilityCount != r.evidence.PublicExposureCount() || r.capabilityAliasCount != r.evidence.CapabilityAliasCount() {
		return false
	}
	if !bytes.Equal(r.resolutionEvidenceJSON, r.evidence.CanonicalJSON()) {
		return false
	}
	namespaces := make([]string, 0, r.evidence.GenerationActivationCount())
	for _, activation := range r.evidence.GenerationActivations() {
		namespaces = append(namespaces, activation.Namespace())
	}
	authnActive, authzActive := activationStates(namespaces)
	if authnActive != r.authnActive || authzActive != r.authzActive {
		return false
	}
	readiness, problemCount := diagnosticReadiness(r.envelope.Diagnostics())
	if readiness != r.readiness || problemCount != r.problemCount {
		return false
	}
	for index, diagnostic := range r.envelope.Diagnostics() {
		if validateDisplayText(fmt.Sprintf("diagnostics[%d].message", index), diagnostic.Message) != nil {
			return false
		}
	}
	if !validTransports(r.transports) || !validReadiness(r.readiness, r.problemCount) || validateDisplayText("next action", r.nextAction) != nil {
		return false
	}
	document := inspectDocument{
		Project: inspectProject{Module: r.projectModule},
		Configuration: inspectConfiguration{
			Mode:        r.configurationMode,
			Environment: r.configurationEnvironment,
			Path:        r.configurationPath,
		},
		Summary: inspectSummary{
			SelectedPluginCount:      r.selectedPluginCount,
			AvailableCapabilityCount: r.availableCapabilityCount,
			RequiredCapabilityCount:  r.requiredCapabilityCount,
			ExposedCapabilityCount:   r.exposedCapabilityCount,
			CapabilityAliasCount:     r.capabilityAliasCount,
			AuthNActive:              r.authnActive,
			AuthZActive:              r.authzActive,
			Transports:               append([]Transport(nil), r.transports...),
		},
		Readiness: inspectReadiness{
			State:        r.readiness,
			ProblemCount: r.problemCount,
			NextAction:   r.nextAction,
		},
		ResolutionEvidence: append([]byte(nil), r.resolutionEvidenceJSON...),
	}
	encoded, err := encodeInspectDocument(document)
	if err != nil {
		return false
	}
	canonicalEnvelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 inspectSchemaV1,
		ConfigurationMode:      r.configurationMode,
		ApplicationModelDigest: r.envelope.ApplicationModelDigest(),
		Diagnostics:            r.envelope.Diagnostics(),
		Sources:                r.envelope.Sources(),
		Result:                 encoded,
	})
	return err == nil && bytes.Equal(canonicalEnvelope.CanonicalJSON(), r.envelope.CanonicalJSON())
}

// Envelope returns the immutable shared diagnostic envelope.
func (r InspectResult) Envelope() diagnosticjson.Envelope { return r.envelope }

// ProjectModule returns the selected current Plystra Project module identity.
func (r InspectResult) ProjectModule() string { return r.projectModule }

// ConfigurationMode returns default, environment, or explicit-config.
func (r InspectResult) ConfigurationMode() generation.ConfigurationMode {
	return r.configurationMode
}

// ConfigurationEnvironment returns the selected environment name when present.
func (r InspectResult) ConfigurationEnvironment() string {
	return r.configurationEnvironment
}

// ConfigurationPath returns the selected Project-relative configuration path.
func (r InspectResult) ConfigurationPath() string { return r.configurationPath }

// SelectedPluginCount returns final constructor Plugin membership.
func (r InspectResult) SelectedPluginCount() int { return r.selectedPluginCount }

// AvailableCapabilityCount returns every visible canonical contract.
func (r InspectResult) AvailableCapabilityCount() int { return r.availableCapabilityCount }

// RequiredCapabilityCount returns the final canonical requirement closure.
func (r InspectResult) RequiredCapabilityCount() int { return r.requiredCapabilityCount }

// ExposedCapabilityCount returns canonical and Alias public-surface records.
func (r InspectResult) ExposedCapabilityCount() int { return r.exposedCapabilityCount }

// CapabilityAliasCount returns final application-local Alias membership.
func (r InspectResult) CapabilityAliasCount() int { return r.capabilityAliasCount }

// AuthNActive reports whether the final generation model activates authn.
func (r InspectResult) AuthNActive() bool { return r.authnActive }

// AuthZActive reports whether the final generation model activates authz.
func (r InspectResult) AuthZActive() bool { return r.authzActive }

// Transports returns selected Connect and REST transports in closed order.
func (r InspectResult) Transports() []Transport {
	return append([]Transport(nil), r.transports...)
}

// Readiness returns ready, needs-attention, or blocked.
func (r InspectResult) Readiness() Readiness { return r.readiness }

// ProblemCount returns warning and error diagnostic membership.
func (r InspectResult) ProblemCount() int { return r.problemCount }

// NextAction returns the bounded primary recovery or continuation action.
func (r InspectResult) NextAction() string { return r.nextAction }

// ResolutionEvidenceJSON returns a defensive copy of the complete canonical
// resolution-evidence document embedded in this command result.
func (r InspectResult) ResolutionEvidenceJSON() []byte {
	return append([]byte(nil), r.resolutionEvidenceJSON...)
}

func encodeInspectDocument(document inspectDocument) ([]byte, error) {
	if len(document.ResolutionEvidence) == 0 || !json.Valid(document.ResolutionEvidence) {
		return nil, errors.New("resolution evidence is not valid JSON")
	}
	return json.Marshal(document)
}

func diagnosticReadiness(diagnostics []diagnosticjson.Diagnostic) (Readiness, int) {
	problems, errorsFound := 0, false
	for _, diagnostic := range diagnostics {
		switch diagnostic.Severity {
		case diagnosticjson.SeverityWarning:
			problems++
		case diagnosticjson.SeverityError:
			problems++
			errorsFound = true
		}
	}
	if errorsFound {
		return ReadinessBlocked, problems
	}
	if problems > 0 {
		return ReadinessNeedsAttention, problems
	}
	return ReadinessReady, 0
}

func activationStates(namespaces []string) (bool, bool) {
	authnActive, authzActive := false, false
	for _, namespace := range namespaces {
		switch namespace {
		case "authn":
			authnActive = true
		case "authz":
			authzActive = true
		}
	}
	return authnActive, authzActive
}

func validReadiness(value Readiness, problems int) bool {
	switch value {
	case ReadinessReady:
		return problems == 0
	case ReadinessNeedsAttention, ReadinessBlocked:
		return problems > 0
	default:
		return false
	}
}

func validTransports(values []Transport) bool {
	if len(values) > 2 {
		return false
	}
	for index, value := range values {
		switch value {
		case TransportConnect:
			if index != 0 {
				return false
			}
		case TransportREST:
			if index > 1 || index == 1 && values[0] != TransportConnect {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func slicesEqualTransports(values []Transport, connect, rest bool) bool {
	expected := make([]Transport, 0, 2)
	if connect {
		expected = append(expected, TransportConnect)
	}
	if rest {
		expected = append(expected, TransportREST)
	}
	if len(values) != len(expected) {
		return false
	}
	for index := range values {
		if values[index] != expected[index] {
			return false
		}
	}
	return true
}

func validateDisplayText(name, value string) error {
	if value == "" || len(value) > maximumNextActionLength || !utf8.ValidString(value) {
		return fmt.Errorf("%s must be non-empty valid UTF-8 and at most %d bytes", name, maximumNextActionLength)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%s must be one line without control characters", name)
	}
	if containsAbsolutePath(value) {
		return fmt.Errorf("%s must not contain a machine-specific absolute path", name)
	}
	return nil
}

func containsAbsolutePath(value string) bool {
	for _, field := range strings.Fields(value) {
		candidate := strings.Trim(field, "`'\"()[]{}<>,;:")
		if candidate == "" {
			continue
		}
		if path.IsAbs(candidate) || strings.HasPrefix(candidate, "\\") || strings.HasPrefix(strings.ToLower(candidate), "file://") {
			return true
		}
		if len(candidate) >= 3 && ((candidate[0] >= 'A' && candidate[0] <= 'Z') || (candidate[0] >= 'a' && candidate[0] <= 'z')) && candidate[1] == ':' && (candidate[2] == '/' || candidate[2] == '\\') {
			return true
		}
	}
	return false
}

func collectSources(evidence resolutionevidence.Evidence, additional []diagnosticjson.Source) []diagnosticjson.Source {
	unique := make(map[diagnosticjson.Source]struct{})
	addDiagnostic := func(source diagnosticjson.Source) {
		unique[source] = struct{}{}
	}
	add := func(source resolutionevidence.Source) {
		addDiagnostic(diagnosticjson.Source{
			Module: source.Module(),
			Path:   source.Path(),
			Kind:   source.Kind(),
			Line:   source.Line(),
			Column: source.Column(),
		})
	}
	for _, source := range additional {
		addDiagnostic(source)
	}
	currentModule := ""
	for _, module := range evidence.Modules() {
		add(module.Source())
		if module.Role() == resolutionevidence.ModuleRoleCurrent {
			currentModule = module.Path()
		}
	}
	if selection, exists := evidence.ConfigurationSelection(); exists && currentModule != "" {
		addDiagnostic(diagnosticjson.Source{
			Module: currentModule,
			Path:   selection.SelectedPath(),
			Kind:   "configuration-selection",
		})
	}
	for _, plugin := range evidence.PluginCandidates() {
		add(plugin.Source())
	}
	for _, plugin := range evidence.SelectedPlugins() {
		add(plugin.Source())
	}
	for _, requirement := range evidence.Requirements() {
		for _, source := range requirement.Sources() {
			add(source.Source())
		}
	}
	for _, candidate := range evidence.ProviderCandidates() {
		add(candidate.Source())
	}
	for _, provider := range evidence.SelectedProviders() {
		add(provider.ProviderSource())
		for _, source := range provider.SelectionSources() {
			add(source.Source())
		}
	}
	for _, activation := range evidence.GenerationActivations() {
		for _, cause := range activation.Causes() {
			add(cause.Source())
		}
	}
	for _, requirement := range evidence.GeneratedRequirements() {
		add(requirement.Source())
	}
	for _, alias := range evidence.CapabilityAliases() {
		for _, source := range alias.Sources() {
			add(source.Source())
		}
	}
	for _, exposure := range evidence.PublicExposures() {
		for _, source := range exposure.Sources() {
			add(source.Source())
		}
	}
	for _, field := range evidence.ConfigurationFields() {
		for _, contribution := range field.Contributors() {
			for _, source := range contribution.Sources() {
				add(source)
			}
		}
	}
	if assembly, exists := evidence.StaticAssembly(); exists {
		for _, plugin := range assembly.Plugins() {
			add(plugin.Source())
		}
		for _, binding := range assembly.Bindings() {
			add(binding.ProviderSource())
		}
	}
	result := make([]diagnosticjson.Source, 0, len(unique))
	for source := range unique {
		result = append(result, source)
	}
	return result
}

func mustSchema(name string, version uint32) diagnosticjson.Schema {
	schema, err := diagnosticjson.NewSchema(name, version)
	if err != nil {
		panic(err)
	}
	return schema
}
