package commandschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/transporttoolchain"
	"golang.org/x/mod/semver"
)

const CapabilitiesSchemaV1 = "plystra.capabilities/v1"

const maximumCapabilitiesJSON = 1 << 20

// ErrCapabilities reports invalid installed capability facts.
var ErrCapabilities = errors.New("build plystra.capabilities result")

// SupportState is the closed support-stage vocabulary.
type SupportState string

const (
	SupportYes           SupportState = "yes"
	SupportNo            SupportState = "no"
	SupportUnknown       SupportState = "unknown"
	SupportNotApplicable SupportState = "not_applicable"
)

// CapabilitySchemaRole is one canonical public schema category reported by
// installed capability discovery.
type CapabilitySchemaRole string

const (
	CapabilitySchemaContinuation CapabilitySchemaRole = "continuation"
	CapabilitySchemaDiagnostic   CapabilitySchemaRole = "diagnostic"
	CapabilitySchemaGraph        CapabilitySchemaRole = "graph"
	CapabilitySchemaInspection   CapabilitySchemaRole = "inspection"
	CapabilitySchemaRecovery     CapabilitySchemaRole = "recovery"
	CapabilitySchemaResult       CapabilitySchemaRole = "result"
)

var capabilitySchemaRoles = []CapabilitySchemaRole{
	CapabilitySchemaContinuation,
	CapabilitySchemaDiagnostic,
	CapabilitySchemaGraph,
	CapabilitySchemaInspection,
	CapabilitySchemaRecovery,
	CapabilitySchemaResult,
}

// CapabilitySchemaInput is the construction-only installed state for one
// canonical public schema role. An unavailable role has no name or version.
type CapabilitySchemaInput struct {
	Role      CapabilitySchemaRole
	Available bool
	Name      string
	Version   uint32
}

// CapabilitySchema is one immutable public schema availability record.
type CapabilitySchema struct {
	input CapabilitySchemaInput
}

// Role returns the canonical public schema role.
func (s CapabilitySchema) Role() CapabilitySchemaRole { return s.input.Role }

// Available reports whether the installed CLI exposes this schema role.
func (s CapabilitySchema) Available() bool { return s.input.Available }

// Name returns the schema name without a version suffix when available.
func (s CapabilitySchema) Name() string { return s.input.Name }

// Version returns the independent positive schema version when available.
func (s CapabilitySchema) Version() uint32 { return s.input.Version }

// Valid reports whether this record has one canonical role and a consistent
// available or unavailable identity.
func (s CapabilitySchema) Valid() bool { return validateCapabilitySchema(s.input) == nil }

// CapabilitySupportInput is the construction-only support record for one
// installed feature.
type CapabilitySupportInput struct {
	ID        string
	Specified SupportState
	Parsed    SupportState
	Generated SupportState
	Executed  SupportState
	Accepted  SupportState
}

// CapabilitySupport is one immutable, canonically ordered feature support
// record.
type CapabilitySupport struct {
	input CapabilitySupportInput
}

// NewCapabilitySupport validates one standalone installed support record.
func NewCapabilitySupport(input CapabilitySupportInput) (CapabilitySupport, error) {
	if err := validateCapabilitySupport(input); err != nil {
		return CapabilitySupport{}, fmt.Errorf("%w: %v", ErrCapabilities, err)
	}
	return CapabilitySupport{input: input}, nil
}

// ID returns the stable feature identity.
func (s CapabilitySupport) ID() string { return s.input.ID }

// Specified reports whether the feature is present in the specification.
func (s CapabilitySupport) Specified() SupportState { return s.input.Specified }

// Parsed reports whether authored input is accepted by the installed parser.
func (s CapabilitySupport) Parsed() SupportState { return s.input.Parsed }

// Generated reports whether the installed CLI emits the feature projection.
func (s CapabilitySupport) Generated() SupportState { return s.input.Generated }

// Executed reports whether the installed runtime executes the feature.
func (s CapabilitySupport) Executed() SupportState { return s.input.Executed }

// Accepted reports whether the feature has completed its required acceptance.
func (s CapabilitySupport) Accepted() SupportState { return s.input.Accepted }

// Valid reports whether the support record contains one complete closed stage
// set and a safe canonical identity.
func (s CapabilitySupport) Valid() bool { return validateCapabilitySupport(s.input) == nil }

// CapabilitiesInput is the construction-only form of installed capability
// facts. All inputs must describe the running distribution rather than a
// current Project or caller.
type CapabilitiesInput struct {
	InvocationPolicy           CapabilityInvocationPolicy
	CLIVersion                 string
	KernelVersion              string
	SpecificationRevision      string
	GoRequirement              string
	GOOS                       string
	GOARCH                     string
	TransportToolchain         transporttoolchain.Identity
	Schemas                    []CapabilitySchemaInput
	Commands                   []CapabilityCommandInput
	Selectors                  []CapabilitySelectorInput
	DefaultInteraction         CapabilityInteractionMode
	DefaultOutput              CapabilityOutputFormat
	EffectClasses              []EffectClass
	ProjectDocumentBytes       int64
	StartupTimeout             time.Duration
	InvocationTimeout          time.Duration
	InvocationConcurrencyLimit int
	MaximumConcurrencyLimit    int
	Support                    []CapabilitySupportInput
}

// Capabilities is one immutable plystra.capabilities/v1 payload.
type Capabilities struct {
	invocationPolicy           CapabilityInvocationPolicy
	cliVersion                 string
	kernelVersion              string
	specificationRevision      string
	goRequirement              string
	goos                       string
	goarch                     string
	transportToolchain         transporttoolchain.Identity
	schemas                    []CapabilitySchema
	commands                   []CapabilityCommand
	selectors                  []CapabilitySelector
	defaultInteraction         CapabilityInteractionMode
	defaultOutput              CapabilityOutputFormat
	effectClasses              []EffectClass
	projectDocumentBytes       int64
	startupTimeout             time.Duration
	invocationTimeout          time.Duration
	invocationConcurrencyLimit int
	maximumConcurrencyLimit    int
	support                    []CapabilitySupport
	canonicalJSON              []byte
	prepared                   bool
}

type capabilitiesDocument struct {
	InvocationPolicy CapabilityInvocationPolicy    `json:"invocation_policy"`
	Schema           string                        `json:"schema"`
	Installed        capabilitiesInstalledDocument `json:"installed"`
	Schemas          []capabilitySchemaDocument    `json:"schemas"`
	Commands         []capabilityCommandDocument   `json:"commands"`
	Selectors        []capabilitySelectorDocument  `json:"selectors"`
	Effects          []EffectClass                 `json:"effect_classes"`
	Limits           capabilitiesLimitsDocument    `json:"limits"`
	Defaults         capabilitiesDefaultsDocument  `json:"defaults"`
	Support          []capabilitySupportDocument   `json:"support"`
}

// CapabilityInvocationPolicy reports the installed compiled-policy protocol and
// supported timeout bounds. Enabled authored stages are reported separately.
type CapabilityInvocationPolicy struct {
	SchemaVersion        int           `json:"schema_version"`
	CompilerVersion      int           `json:"compiler_version"`
	DefaultsVersion      int           `json:"defaults_version"`
	DurationBytes        int           `json:"duration_bytes"`
	MaximumTimeout       time.Duration `json:"maximum_timeout_ns"`
	DefaultAttempts      int           `json:"default_attempts"`
	CircuitEnabled       bool          `json:"circuit_enabled"`
	RetryEligibility     string        `json:"retry_eligibility"`
	RetryDefaultAttempts int           `json:"retry_default_attempts"`
	MaximumRetryAttempts int           `json:"maximum_retry_attempts"`
	RetryDefaultBackoff  time.Duration `json:"retry_default_backoff_ns"`
	MaximumRetryBackoff  time.Duration `json:"maximum_retry_backoff_ns"`
}

type capabilitiesInstalledDocument struct {
	CLIVersion            string                       `json:"cli_version"`
	KernelVersion         string                       `json:"kernel_version"`
	SpecificationRevision string                       `json:"specification_revision"`
	GoRequirement         string                       `json:"go_requirement"`
	Platform              capabilitiesPlatformDocument `json:"platform"`
	TransportToolchain    json.RawMessage              `json:"transport_toolchain"`
}

type capabilitiesPlatformDocument struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
}

type capabilitySchemaDocument struct {
	Role      CapabilitySchemaRole `json:"role"`
	Available bool                 `json:"available"`
	Name      *string              `json:"name"`
	Version   *uint32              `json:"version"`
}

type capabilitiesLimitsDocument struct {
	ProjectDocumentBytes       int64 `json:"project_document_bytes"`
	InvocationConcurrencyLimit int   `json:"invocation_concurrency_limit"`
}

type capabilitiesDefaultsDocument struct {
	InteractionMode       CapabilityInteractionMode              `json:"interaction_mode"`
	OutputFormat          CapabilityOutputFormat                 `json:"output_format"`
	StartupTimeout        string                                 `json:"startup_timeout"`
	InvocationTimeout     string                                 `json:"invocation_timeout"`
	InvocationConcurrency capabilitiesConcurrencyDefaultDocument `json:"invocation_concurrency"`
}

type capabilitiesConcurrencyDefaultDocument struct {
	DefaultLimit int `json:"default_limit"`
	Queue        int `json:"queue"`
}

type capabilitySupportDocument struct {
	ID        string       `json:"id"`
	Specified SupportState `json:"specified"`
	Parsed    SupportState `json:"parsed"`
	Generated SupportState `json:"generated"`
	Executed  SupportState `json:"executed"`
	Accepted  SupportState `json:"accepted"`
}

// NewCapabilities validates and constructs one installed capability payload.
func NewCapabilities(input CapabilitiesInput) (Capabilities, error) {
	if err := validateCapabilitiesInput(input); err != nil {
		return Capabilities{}, fmt.Errorf("%w: %v", ErrCapabilities, err)
	}
	schemas, err := normalizeCapabilitySchemas(input.Schemas)
	if err != nil {
		return Capabilities{}, fmt.Errorf("%w: %v", ErrCapabilities, err)
	}
	selectors, err := normalizeCapabilitySelectors(input.Selectors)
	if err != nil {
		return Capabilities{}, fmt.Errorf("%w: %v", ErrCapabilities, err)
	}
	commands, err := normalizeCapabilityCommands(input.Commands, selectors, input.DefaultInteraction, input.DefaultOutput)
	if err != nil {
		return Capabilities{}, fmt.Errorf("%w: %v", ErrCapabilities, err)
	}
	effectClasses, err := normalizeCapabilityEffectClasses(input.EffectClasses)
	if err != nil {
		return Capabilities{}, fmt.Errorf("%w: %v", ErrCapabilities, err)
	}
	support, err := normalizeCapabilitySupport(input.Support)
	if err != nil {
		return Capabilities{}, fmt.Errorf("%w: %v", ErrCapabilities, err)
	}
	result := Capabilities{
		invocationPolicy:           input.InvocationPolicy,
		cliVersion:                 input.CLIVersion,
		kernelVersion:              input.KernelVersion,
		specificationRevision:      input.SpecificationRevision,
		goRequirement:              input.GoRequirement,
		goos:                       input.GOOS,
		goarch:                     input.GOARCH,
		transportToolchain:         input.TransportToolchain,
		schemas:                    schemas,
		commands:                   commands,
		selectors:                  selectors,
		defaultInteraction:         input.DefaultInteraction,
		defaultOutput:              input.DefaultOutput,
		effectClasses:              effectClasses,
		projectDocumentBytes:       input.ProjectDocumentBytes,
		startupTimeout:             input.StartupTimeout,
		invocationTimeout:          input.InvocationTimeout,
		invocationConcurrencyLimit: input.InvocationConcurrencyLimit,
		maximumConcurrencyLimit:    input.MaximumConcurrencyLimit,
		support:                    support,
		prepared:                   true,
	}
	canonical, err := json.Marshal(result.document())
	if err != nil {
		return Capabilities{}, fmt.Errorf("%w: encode: %v", ErrCapabilities, err)
	}
	if len(canonical) > maximumCapabilitiesJSON {
		return Capabilities{}, fmt.Errorf("%w: encoded payload exceeds %d bytes", ErrCapabilities, maximumCapabilitiesJSON)
	}
	result.canonicalJSON = canonical
	return result, nil
}

// Valid reports whether NewCapabilities produced this payload and every
// cached canonical byte still agrees with its immutable facts.
func (c Capabilities) Valid() bool {
	if !c.prepared {
		return false
	}
	rebuilt, err := NewCapabilities(c.input())
	return err == nil && bytes.Equal(rebuilt.canonicalJSON, c.canonicalJSON)
}

// Schema returns the immutable payload schema identity.
func (Capabilities) Schema() string { return CapabilitiesSchemaV1 }

// CLIVersion returns the installed CLI version.
func (c Capabilities) CLIVersion() string { return c.cliVersion }

// KernelVersion returns the exact supported Kernel version.
func (c Capabilities) KernelVersion() string { return c.kernelVersion }

// SpecificationRevision returns the exact installed specification revision.
func (c Capabilities) SpecificationRevision() string { return c.specificationRevision }

// GoRequirement returns the installed distribution's Go language requirement.
func (c Capabilities) GoRequirement() string { return c.goRequirement }

// GOOS returns the running platform operating-system identity.
func (c Capabilities) GOOS() string { return c.goos }

// GOARCH returns the running platform architecture identity.
func (c Capabilities) GOARCH() string { return c.goarch }

// TransportToolchain returns the exact immutable embedded toolchain identity.
func (c Capabilities) TransportToolchain() transporttoolchain.Identity {
	return c.transportToolchain
}

// Schemas returns a defensive copy in canonical role order.
func (c Capabilities) Schemas() []CapabilitySchema {
	return append([]CapabilitySchema(nil), c.schemas...)
}

// Commands returns defensive copies of the installed invokable leaf commands
// in canonical command-ID order.
func (c Capabilities) Commands() []CapabilityCommand {
	result := make([]CapabilityCommand, len(c.commands))
	for index, command := range c.commands {
		result[index] = CapabilityCommand{input: cloneCapabilityCommandInput(command.input)}
	}
	return result
}

// Selectors returns defensive copies of installed cross-command selectors in
// canonical selector-ID order.
func (c Capabilities) Selectors() []CapabilitySelector {
	result := make([]CapabilitySelector, len(c.selectors))
	for index, selector := range c.selectors {
		result[index] = CapabilitySelector{input: cloneCapabilitySelectorInput(selector.input)}
	}
	return result
}

// DefaultInteraction returns the installed interaction mode used when a
// command invocation does not select one explicitly.
func (c Capabilities) DefaultInteraction() CapabilityInteractionMode { return c.defaultInteraction }

// DefaultOutput returns the installed output format used when a command does
// not select one explicitly.
func (c Capabilities) DefaultOutput() CapabilityOutputFormat { return c.defaultOutput }

// EffectClasses returns every closed effect class understood by this CLI in
// canonical order.
func (c Capabilities) EffectClasses() []EffectClass {
	return append([]EffectClass(nil), c.effectClasses...)
}

// ProjectDocumentBytes returns the maximum accepted Project declaration size.
func (c Capabilities) ProjectDocumentBytes() int64 { return c.projectDocumentBytes }

// StartupTimeout returns the default runtime startup bound.
func (c Capabilities) StartupTimeout() time.Duration { return c.startupTimeout }

// InvocationTimeout returns the default invocation bound.
func (c Capabilities) InvocationTimeout() time.Duration { return c.invocationTimeout }

// InvocationConcurrencyLimit returns the finite per-binding default, without queueing.
func (c Capabilities) InvocationConcurrencyLimit() int { return c.invocationConcurrencyLimit }

// MaximumConcurrencyLimit returns the installed Kernel's greatest binding limit.
func (c Capabilities) MaximumConcurrencyLimit() int { return c.maximumConcurrencyLimit }

// InvocationPolicy returns a value copy of installed protocol and bound facts.
func (c Capabilities) InvocationPolicy() CapabilityInvocationPolicy { return c.invocationPolicy }

// StartupTimeoutText returns the canonical public duration spelling.
func (c Capabilities) StartupTimeoutText() string {
	return formatCapabilitiesDuration(c.startupTimeout)
}

// InvocationTimeoutText returns the canonical public duration spelling.
func (c Capabilities) InvocationTimeoutText() string {
	return formatCapabilitiesDuration(c.invocationTimeout)
}

// Support returns a defensive copy in canonical feature-ID order.
func (c Capabilities) Support() []CapabilitySupport {
	return append([]CapabilitySupport(nil), c.support...)
}

// CanonicalJSON returns a defensive copy of the payload document.
func (c Capabilities) CanonicalJSON() []byte { return append([]byte(nil), c.canonicalJSON...) }

func (Capabilities) commandPayload() {}

func validateCapabilitiesInput(input CapabilitiesInput) error {
	p := input.InvocationPolicy
	if p.RetryEligibility != "replay_safe" || p.RetryDefaultAttempts < 2 || p.MaximumRetryAttempts < p.RetryDefaultAttempts || p.RetryDefaultBackoff != 0 || p.MaximumRetryBackoff <= 0 {
		return errors.New("invocation retry eligibility, defaults, or bounds are invalid")
	}
	if p.SchemaVersion < 1 || p.CompilerVersion < 1 || p.DefaultsVersion < 1 || p.DurationBytes < 1 || p.MaximumTimeout <= 0 || p.DefaultAttempts != 1 || p.CircuitEnabled || input.InvocationTimeout > p.MaximumTimeout {
		return errors.New("invocation policy protocol, bounds, or absence defaults are invalid")
	}
	if strings.HasPrefix(input.CLIVersion, "v") || !semver.IsValid("v"+input.CLIVersion) {
		return errors.New("cli version must be canonical SemVer without a v prefix")
	}
	if !semver.IsValid(input.KernelVersion) {
		return errors.New("kernel version must be canonical SemVer")
	}
	if !validSpecificationRevision(input.SpecificationRevision) {
		return errors.New("specification revision must be a lowercase 40-character hexadecimal revision")
	}
	if !validGoRequirement(input.GoRequirement) {
		return errors.New("go requirement must contain one canonical major.minor version")
	}
	if !validToken(input.GOOS, 64) || !validToken(input.GOARCH, 64) {
		return errors.New("platform must contain canonical GOOS and GOARCH identities")
	}
	if !input.TransportToolchain.Valid() {
		return errors.New("transport toolchain identity is invalid")
	}
	if !validCapabilityInteractionMode(input.DefaultInteraction) {
		return errors.New("default interaction mode is invalid")
	}
	if !validCapabilityOutputFormat(input.DefaultOutput) {
		return errors.New("default output format is invalid")
	}
	if input.ProjectDocumentBytes <= 0 || input.ProjectDocumentBytes > 1<<40 {
		return errors.New("project document limit is outside the supported range")
	}
	if input.StartupTimeout <= 0 || input.InvocationTimeout < 0 {
		return errors.New("startup timeout must be positive and invocation timeout must be nonnegative")
	}
	if input.MaximumConcurrencyLimit < 1 || input.MaximumConcurrencyLimit > 1<<30 || input.InvocationConcurrencyLimit < 1 || input.InvocationConcurrencyLimit > input.MaximumConcurrencyLimit {
		return errors.New("concurrency limits must be positive and the default must not exceed the maximum")
	}
	return nil
}

func normalizeCapabilitySupport(input []CapabilitySupportInput) ([]CapabilitySupport, error) {
	if len(input) == 0 || len(input) > 4_096 {
		return nil, errors.New("support must contain between 1 and 4096 records")
	}
	ordered := append([]CapabilitySupportInput(nil), input...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	result := make([]CapabilitySupport, len(ordered))
	for index, support := range ordered {
		if err := validateCapabilitySupport(support); err != nil {
			return nil, fmt.Errorf("support[%d]: %v", index, err)
		}
		if index > 0 && ordered[index-1].ID == support.ID {
			return nil, fmt.Errorf("support[%d] duplicates feature %q", index, support.ID)
		}
		result[index] = CapabilitySupport{input: support}
	}
	return result, nil
}

func normalizeCapabilitySchemas(input []CapabilitySchemaInput) ([]CapabilitySchema, error) {
	if len(input) != len(capabilitySchemaRoles) {
		return nil, fmt.Errorf("schemas must contain exactly %d canonical roles", len(capabilitySchemaRoles))
	}
	ordered := append([]CapabilitySchemaInput(nil), input...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].Role < ordered[right].Role })
	result := make([]CapabilitySchema, len(ordered))
	for index, schema := range ordered {
		if schema.Role != capabilitySchemaRoles[index] {
			return nil, fmt.Errorf("schemas must contain role %q exactly once", capabilitySchemaRoles[index])
		}
		if err := validateCapabilitySchema(schema); err != nil {
			return nil, fmt.Errorf("schemas[%d]: %v", index, err)
		}
		result[index] = CapabilitySchema{input: schema}
	}
	return result, nil
}

func validateCapabilitySchema(input CapabilitySchemaInput) error {
	if !validCapabilitySchemaRole(input.Role) {
		return fmt.Errorf("role %q is not supported", input.Role)
	}
	if !input.Available {
		if input.Name != "" || input.Version != 0 {
			return errors.New("unavailable schema must not declare a name or version")
		}
		return nil
	}
	if _, err := diagnosticjson.NewSchema(input.Name, input.Version); err != nil {
		return fmt.Errorf("available schema identity is invalid: %v", err)
	}
	return nil
}

func validCapabilitySchemaRole(value CapabilitySchemaRole) bool {
	switch value {
	case CapabilitySchemaContinuation,
		CapabilitySchemaDiagnostic,
		CapabilitySchemaGraph,
		CapabilitySchemaInspection,
		CapabilitySchemaRecovery,
		CapabilitySchemaResult:
		return true
	default:
		return false
	}
}

func validateCapabilitySupport(input CapabilitySupportInput) error {
	if !validCapabilitySupportID(input.ID) {
		return errors.New("id is not a canonical dotted feature identity")
	}
	stages := []struct {
		name  string
		state SupportState
	}{
		{name: "specified", state: input.Specified},
		{name: "parsed", state: input.Parsed},
		{name: "generated", state: input.Generated},
		{name: "executed", state: input.Executed},
		{name: "accepted", state: input.Accepted},
	}
	for _, stage := range stages {
		if !validSupportState(stage.state) {
			return fmt.Errorf("%s stage %q is invalid", stage.name, stage.state)
		}
	}
	return nil
}

func validCapabilitySupportID(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	segments := strings.Split(value, ".")
	for _, segment := range segments {
		if segment == "*" {
			continue
		}
		if !validLowerKebab(strings.ReplaceAll(segment, "_", "-"), 64) {
			return false
		}
	}
	return true
}

func validSupportState(value SupportState) bool {
	switch value {
	case SupportYes, SupportNo, SupportUnknown, SupportNotApplicable:
		return true
	default:
		return false
	}
}

func validSpecificationRevision(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validGoRequirement(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 3 || len(part) > 1 && part[0] == '0' {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return parts[0] != "0"
}

func formatCapabilitiesDuration(value time.Duration) string {
	if value == 0 {
		return "0s"
	}
	for _, unit := range []struct {
		duration time.Duration
		suffix   string
	}{{time.Hour, "h"}, {time.Minute, "m"}, {time.Second, "s"}} {
		if value%unit.duration == 0 {
			return strconv.FormatInt(int64(value/unit.duration), 10) + unit.suffix
		}
	}
	return value.String()
}

func (c Capabilities) input() CapabilitiesInput {
	schemas := make([]CapabilitySchemaInput, len(c.schemas))
	for index, value := range c.schemas {
		schemas[index] = value.input
	}
	commands := make([]CapabilityCommandInput, len(c.commands))
	for index, value := range c.commands {
		commands[index] = cloneCapabilityCommandInput(value.input)
	}
	selectors := make([]CapabilitySelectorInput, len(c.selectors))
	for index, value := range c.selectors {
		selectors[index] = cloneCapabilitySelectorInput(value.input)
	}
	support := make([]CapabilitySupportInput, len(c.support))
	for index, value := range c.support {
		support[index] = value.input
	}
	return CapabilitiesInput{
		InvocationPolicy:           c.invocationPolicy,
		CLIVersion:                 c.cliVersion,
		KernelVersion:              c.kernelVersion,
		SpecificationRevision:      c.specificationRevision,
		GoRequirement:              c.goRequirement,
		GOOS:                       c.goos,
		GOARCH:                     c.goarch,
		TransportToolchain:         c.transportToolchain,
		Schemas:                    schemas,
		Commands:                   commands,
		Selectors:                  selectors,
		DefaultInteraction:         c.defaultInteraction,
		DefaultOutput:              c.defaultOutput,
		EffectClasses:              append([]EffectClass(nil), c.effectClasses...),
		ProjectDocumentBytes:       c.projectDocumentBytes,
		StartupTimeout:             c.startupTimeout,
		InvocationTimeout:          c.invocationTimeout,
		InvocationConcurrencyLimit: c.invocationConcurrencyLimit,
		MaximumConcurrencyLimit:    c.maximumConcurrencyLimit,
		Support:                    support,
	}
}

func (c Capabilities) document() capabilitiesDocument {
	schemas := make([]capabilitySchemaDocument, len(c.schemas))
	for index, value := range c.schemas {
		document := capabilitySchemaDocument{Role: value.Role(), Available: value.Available()}
		if value.Available() {
			name := value.Name()
			version := value.Version()
			document.Name = &name
			document.Version = &version
		}
		schemas[index] = document
	}
	support := make([]capabilitySupportDocument, len(c.support))
	for index, value := range c.support {
		support[index] = capabilitySupportDocument(value.input)
	}
	return capabilitiesDocument{
		InvocationPolicy: c.invocationPolicy,
		Schema:           CapabilitiesSchemaV1,
		Installed: capabilitiesInstalledDocument{
			CLIVersion:            c.cliVersion,
			KernelVersion:         c.kernelVersion,
			SpecificationRevision: c.specificationRevision,
			GoRequirement:         c.goRequirement,
			Platform:              capabilitiesPlatformDocument{GOOS: c.goos, GOARCH: c.goarch},
			TransportToolchain:    c.transportToolchain.RecordJSON(),
		},
		Schemas:   schemas,
		Commands:  capabilityCommandDocuments(c.commands),
		Selectors: capabilitySelectorDocuments(c.selectors),
		Effects:   append([]EffectClass{}, c.effectClasses...),
		Limits:    capabilitiesLimitsDocument{ProjectDocumentBytes: c.projectDocumentBytes, InvocationConcurrencyLimit: c.maximumConcurrencyLimit},
		Defaults: capabilitiesDefaultsDocument{
			InteractionMode:       c.defaultInteraction,
			OutputFormat:          c.defaultOutput,
			StartupTimeout:        formatCapabilitiesDuration(c.startupTimeout),
			InvocationTimeout:     formatCapabilitiesDuration(c.invocationTimeout),
			InvocationConcurrency: capabilitiesConcurrencyDefaultDocument{DefaultLimit: c.invocationConcurrencyLimit, Queue: 0},
		},
		Support: support,
	}
}
