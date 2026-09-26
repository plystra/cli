package diagnosticschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/pluginid"
	"github.com/plystra/cli/internal/resolutionevidence"
)

const maximumExplanationIdentityLength = 1_024

var (
	// ErrExplain reports incomplete, inconsistent, or unsafe input for the
	// plystra.explain v1 result schema.
	ErrExplain = errors.New("build plystra.explain result")

	explainSchemaV1 = mustSchema("plystra.explain", 1)
)

// ExplainSubjectKind is one closed public explanation target.
type ExplainSubjectKind string

const (
	ExplainSubjectCapability    ExplainSubjectKind = "capability"
	ExplainSubjectPlugin        ExplainSubjectKind = "plugin"
	ExplainSubjectConfiguration ExplainSubjectKind = "configuration"
	ExplainSubjectAlias         ExplainSubjectKind = "alias"
	ExplainSubjectExposure      ExplainSubjectKind = "exposure"
)

// ExplainChangeKind identifies the one supported way to change a decision.
type ExplainChangeKind string

const (
	ExplainChangeFile    ExplainChangeKind = "file"
	ExplainChangeCommand ExplainChangeKind = "command"
)

// ExplainChange is a closed file-or-command union. File changes identify the
// current Project module, one Project-relative document, and one typed field.
// Command changes contain one public Plystra CLI invocation plus its exact,
// shell-independent argument vector.
type ExplainChange struct {
	Kind    ExplainChangeKind
	Module  string
	Path    string
	Field   string
	Command string
	Argv    []string
}

// ExplainInput is the construction-only input for one plystra.explain v1
// result. Outcome and Reason are stable lower-kebab decision codes owned by
// the causal resolver. PrimarySources must belong to the resolution evidence
// or the additional command-owned Sources.
type ExplainInput struct {
	Evidence       resolutionevidence.Evidence
	SubjectKind    ExplainSubjectKind
	Subject        string
	Outcome        string
	Reason         string
	PrimarySources []diagnosticjson.Source
	Change         ExplainChange
	Diagnostics    []diagnosticjson.Diagnostic
	Sources        []diagnosticjson.Source
}

// ExplainResult is one immutable plystra.explain v1 diagnostic result.
type ExplainResult struct {
	envelope               diagnosticjson.Envelope
	evidence               resolutionevidence.Evidence
	subjectKind            ExplainSubjectKind
	subject                string
	outcome                string
	reason                 string
	primarySources         []diagnosticjson.Source
	change                 ExplainChange
	resolutionEvidenceJSON []byte
	prepared               bool
}

type explainDocument struct {
	Subject            explainSubject  `json:"subject"`
	Decision           explainDecision `json:"decision"`
	Reason             explainReason   `json:"reason"`
	Change             explainChange   `json:"change"`
	ResolutionEvidence json.RawMessage `json:"resolution_evidence"`
}

type explainSubject struct {
	Kind ExplainSubjectKind `json:"kind"`
	ID   string             `json:"id"`
}

type explainDecision struct {
	Outcome string `json:"outcome"`
}

type explainReason struct {
	Code    string          `json:"code"`
	Sources []explainSource `json:"sources"`
}

type explainChange struct {
	Kind    ExplainChangeKind `json:"kind"`
	Module  string            `json:"module"`
	Path    string            `json:"path"`
	Field   string            `json:"field"`
	Command string            `json:"command"`
	Argv    []string          `json:"argv"`
}

type explainSource struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// ExplainSchemaV1 returns the immutable command-owned schema descriptor.
func ExplainSchemaV1() diagnosticjson.Schema { return explainSchemaV1 }

// NewExplain validates and constructs one complete plystra.explain v1 result.
func NewExplain(input ExplainInput) (ExplainResult, error) {
	if !input.Evidence.Valid() {
		return ExplainResult{}, fmt.Errorf("%w: resolution evidence is not valid", ErrExplain)
	}
	selection, exists := input.Evidence.ConfigurationSelection()
	if !exists {
		return ExplainResult{}, fmt.Errorf("%w: resolution evidence omits selected configuration provenance", ErrExplain)
	}
	if _, exists := input.Evidence.StaticAssembly(); !exists {
		return ExplainResult{}, fmt.Errorf("%w: resolution evidence omits static assembly membership", ErrExplain)
	}
	if _, exists := input.Evidence.HTTPTransports(); !exists {
		return ExplainResult{}, fmt.Errorf("%w: resolution evidence omits selected HTTP transports", ErrExplain)
	}
	if err := ValidateExplainSubject(input.SubjectKind, input.Subject); err != nil {
		return ExplainResult{}, fmt.Errorf("%w: subject: %v", ErrExplain, err)
	}
	if !validExplanationCode(input.Outcome) {
		return ExplainResult{}, fmt.Errorf("%w: outcome %q is not canonical lower kebab case", ErrExplain, input.Outcome)
	}
	if !validExplanationCode(input.Reason) {
		return ExplainResult{}, fmt.Errorf("%w: reason %q is not canonical lower kebab case", ErrExplain, input.Reason)
	}
	for index, diagnostic := range input.Diagnostics {
		if err := validateDisplayText(fmt.Sprintf("diagnostics[%d].message", index), diagnostic.Message); err != nil {
			return ExplainResult{}, fmt.Errorf("%w: %v", ErrExplain, err)
		}
	}

	currentModule := currentProjectModule(input.Evidence)
	if currentModule == "" {
		return ExplainResult{}, fmt.Errorf("%w: resolution evidence omits the current Project module", ErrExplain)
	}
	change, changeSource, err := normalizeExplainChange(input.Change, currentModule, selection.Mode(), input.Evidence.BuildModelDigest())
	if err != nil {
		return ExplainResult{}, fmt.Errorf("%w: change: %v", ErrExplain, err)
	}

	allSources := collectSources(input.Evidence, input.Sources)
	if changeSource != nil {
		allSources = append(allSources, *changeSource)
	}
	allSources, err = normalizeExplainSources(selection.Mode(), input.Evidence.BuildModelDigest(), allSources)
	if err != nil {
		return ExplainResult{}, fmt.Errorf("%w: sources: %v", ErrExplain, err)
	}
	primarySources, err := normalizeExplainSources(selection.Mode(), input.Evidence.BuildModelDigest(), input.PrimarySources)
	if err != nil {
		return ExplainResult{}, fmt.Errorf("%w: primary sources: %v", ErrExplain, err)
	}
	if len(primarySources) == 0 {
		return ExplainResult{}, fmt.Errorf("%w: at least one primary source is required", ErrExplain)
	}
	availableSources := make(map[diagnosticjson.Source]struct{}, len(allSources))
	for _, source := range allSources {
		availableSources[source] = struct{}{}
	}
	for _, source := range primarySources {
		if _, exists := availableSources[source]; !exists {
			return ExplainResult{}, fmt.Errorf("%w: primary source %s:%s (%s) is absent from resolution evidence and command sources", ErrExplain, source.Module, source.Path, source.Kind)
		}
	}

	evidenceJSON := input.Evidence.CanonicalJSON()
	resultJSON, err := encodeExplainDocument(explainDocument{
		Subject:  explainSubject{Kind: input.SubjectKind, ID: input.Subject},
		Decision: explainDecision{Outcome: input.Outcome},
		Reason: explainReason{
			Code:    input.Reason,
			Sources: explainSources(primarySources),
		},
		Change:             explainChangeDocument(change),
		ResolutionEvidence: evidenceJSON,
	})
	if err != nil {
		return ExplainResult{}, fmt.Errorf("%w: encode result: %v", ErrExplain, err)
	}
	envelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 explainSchemaV1,
		ConfigurationMode:      selection.Mode(),
		ApplicationModelDigest: input.Evidence.BuildModelDigest(),
		Diagnostics:            input.Diagnostics,
		Sources:                allSources,
		Result:                 resultJSON,
	})
	if err != nil {
		return ExplainResult{}, fmt.Errorf("%w: shared envelope: %v", ErrExplain, err)
	}
	return ExplainResult{
		envelope:               envelope,
		evidence:               input.Evidence,
		subjectKind:            input.SubjectKind,
		subject:                input.Subject,
		outcome:                input.Outcome,
		reason:                 input.Reason,
		primarySources:         primarySources,
		change:                 cloneExplainChange(change),
		resolutionEvidenceJSON: evidenceJSON,
		prepared:               true,
	}, nil
}

// Valid reports whether NewExplain produced this internally consistent result.
func (r ExplainResult) Valid() bool {
	if !r.prepared || !r.evidence.Valid() || !r.envelope.Valid() || r.envelope.Schema() != explainSchemaV1 || r.envelope.ApplicationModelDigest() != r.evidence.BuildModelDigest() {
		return false
	}
	selection, exists := r.evidence.ConfigurationSelection()
	if !exists || selection.Mode() != r.envelope.ConfigurationMode() {
		return false
	}
	if _, exists := r.evidence.StaticAssembly(); !exists {
		return false
	}
	if _, exists := r.evidence.HTTPTransports(); !exists {
		return false
	}
	if ValidateExplainSubject(r.subjectKind, r.subject) != nil || !validExplanationCode(r.outcome) || !validExplanationCode(r.reason) {
		return false
	}
	for index, diagnostic := range r.envelope.Diagnostics() {
		if validateDisplayText(fmt.Sprintf("diagnostics[%d].message", index), diagnostic.Message) != nil {
			return false
		}
	}
	change, changeSource, err := normalizeExplainChange(r.change, currentProjectModule(r.evidence), selection.Mode(), r.evidence.BuildModelDigest())
	if err != nil || !equalExplainChange(change, r.change) {
		return false
	}
	allSources := collectSources(r.evidence, nil)
	if changeSource != nil {
		allSources = append(allSources, *changeSource)
	}
	allSources = append(allSources, r.envelope.Sources()...)
	allSources = deduplicateDiagnosticSources(allSources)
	primarySources, err := normalizeExplainSources(selection.Mode(), r.evidence.BuildModelDigest(), r.primarySources)
	if err != nil || len(primarySources) == 0 || !equalDiagnosticSources(primarySources, r.primarySources) {
		return false
	}
	availableSources := make(map[diagnosticjson.Source]struct{}, len(allSources))
	for _, source := range allSources {
		availableSources[source] = struct{}{}
	}
	for _, source := range primarySources {
		if _, exists := availableSources[source]; !exists {
			return false
		}
	}
	if !bytes.Equal(r.resolutionEvidenceJSON, r.evidence.CanonicalJSON()) {
		return false
	}
	resultJSON, err := encodeExplainDocument(explainDocument{
		Subject:  explainSubject{Kind: r.subjectKind, ID: r.subject},
		Decision: explainDecision{Outcome: r.outcome},
		Reason: explainReason{
			Code:    r.reason,
			Sources: explainSources(r.primarySources),
		},
		Change:             explainChangeDocument(r.change),
		ResolutionEvidence: append([]byte(nil), r.resolutionEvidenceJSON...),
	})
	if err != nil {
		return false
	}
	canonicalEnvelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 explainSchemaV1,
		ConfigurationMode:      selection.Mode(),
		ApplicationModelDigest: r.evidence.BuildModelDigest(),
		Diagnostics:            r.envelope.Diagnostics(),
		Sources:                r.envelope.Sources(),
		Result:                 resultJSON,
	})
	return err == nil && bytes.Equal(canonicalEnvelope.CanonicalJSON(), r.envelope.CanonicalJSON())
}

// Envelope returns the immutable shared diagnostic envelope.
func (r ExplainResult) Envelope() diagnosticjson.Envelope { return r.envelope }

// SubjectKind returns capability, plugin, configuration, alias, or exposure.
func (r ExplainResult) SubjectKind() ExplainSubjectKind { return r.subjectKind }

// Subject returns the exact canonical explanation target.
func (r ExplainResult) Subject() string { return r.subject }

// Outcome returns the stable resulting-decision code.
func (r ExplainResult) Outcome() string { return r.outcome }

// Reason returns the stable direct-cause code.
func (r ExplainResult) Reason() string { return r.reason }

// PrimarySources returns the direct causal sources in canonical order.
func (r ExplainResult) PrimarySources() []diagnosticjson.Source {
	return append([]diagnosticjson.Source(nil), r.primarySources...)
}

// Change returns the exact current-Project file/field or public CLI command
// that changes the explained decision.
func (r ExplainResult) Change() ExplainChange { return cloneExplainChange(r.change) }

// ResolutionEvidenceJSON returns a defensive copy of the complete canonical
// resolution-evidence document embedded in this command result.
func (r ExplainResult) ResolutionEvidenceJSON() []byte {
	return append([]byte(nil), r.resolutionEvidenceJSON...)
}

// ValidateExplainSubject validates one exact public explanation target.
func ValidateExplainSubject(kind ExplainSubjectKind, subject string) error {
	if containsExplainPlaceholder(subject) {
		return errors.New("subject contains an unresolved placeholder")
	}
	switch kind {
	case ExplainSubjectCapability, ExplainSubjectAlias, ExplainSubjectExposure:
		if _, err := capabilityid.Parse(subject); err != nil {
			return fmt.Errorf("%s %q is not a canonical exact Capability ID: %v", kind, subject, err)
		}
	case ExplainSubjectPlugin:
		if err := pluginid.Validate(subject); err != nil {
			return fmt.Errorf("plugin %q is not a canonical Plugin ID: %v", subject, err)
		}
	case ExplainSubjectConfiguration:
		if !validConfigurationSubject(subject) {
			return fmt.Errorf("configuration %q is not a canonical typed field path", subject)
		}
	default:
		return fmt.Errorf("kind %q is not supported", kind)
	}
	return nil
}

func validConfigurationSubject(value string) bool {
	if value == "" || len(value) > maximumExplanationIdentityLength || !utf8.ValidString(value) || strings.Contains(value, "\\") || strings.IndexFunc(value, func(character rune) bool {
		return unicode.IsControl(character) || unicode.IsSpace(character)
	}) >= 0 || containsAbsolutePath(value) {
		return false
	}
	index := 0
	if !consumeExplainPathSegment(value, &index) {
		return false
	}
	for index < len(value) {
		switch value[index] {
		case '.':
			index++
			if !consumeExplainPathSegment(value, &index) {
				return false
			}
		case '[':
			if !consumeExplainPathKey(value, &index) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func consumeExplainPathSegment(value string, index *int) bool {
	start := *index
	for *index < len(value) && value[*index] != '.' && value[*index] != '[' {
		if strings.ContainsRune("]\"'", rune(value[*index])) {
			return false
		}
		*index++
	}
	return *index > start
}

func consumeExplainPathKey(value string, index *int) bool {
	start := *index
	if start+3 >= len(value) || value[start] != '[' || value[start+1] != '"' {
		return false
	}
	escaped := false
	quoteEnd := -1
	for cursor := start + 2; cursor < len(value); cursor++ {
		switch {
		case escaped:
			escaped = false
		case value[cursor] == '\\':
			escaped = true
		case value[cursor] == '"':
			quoteEnd = cursor
			cursor = len(value)
		}
	}
	if quoteEnd < 0 || quoteEnd+1 >= len(value) || value[quoteEnd+1] != ']' {
		return false
	}
	quoted := value[start+1 : quoteEnd+1]
	key, err := strconv.Unquote(quoted)
	if err != nil || key == "" || strconv.Quote(key) != quoted || strings.IndexFunc(key, func(character rune) bool {
		return unicode.IsControl(character) || unicode.IsSpace(character)
	}) >= 0 {
		return false
	}
	*index = quoteEnd + 2
	return true
}

func validExplanationCode(value string) bool {
	if value == "" || len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousHyphen := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}

func currentProjectModule(evidence resolutionevidence.Evidence) string {
	for _, module := range evidence.Modules() {
		if module.Role() == resolutionevidence.ModuleRoleCurrent {
			return module.Path()
		}
	}
	return ""
}

func normalizeExplainChange(input ExplainChange, currentModule string, mode generation.ConfigurationMode, digest string) (ExplainChange, *diagnosticjson.Source, error) {
	switch input.Kind {
	case ExplainChangeFile:
		if input.Module != currentModule {
			return ExplainChange{}, nil, fmt.Errorf("file module %q must be the current Project %q", input.Module, currentModule)
		}
		if !validConfigurationSubject(input.Field) {
			return ExplainChange{}, nil, fmt.Errorf("file field %q is not a canonical typed field path", input.Field)
		}
		if input.Command != "" || len(input.Argv) != 0 {
			return ExplainChange{}, nil, errors.New("file change must not contain a command or argv")
		}
		source := diagnosticjson.Source{Module: input.Module, Path: input.Path, Kind: "change-target"}
		normalized, err := normalizeExplainSources(mode, digest, []diagnosticjson.Source{source})
		if err != nil {
			return ExplainChange{}, nil, fmt.Errorf("file target: %v", err)
		}
		result := cloneExplainChange(input)
		result.Argv = make([]string, 0)
		return result, &normalized[0], nil
	case ExplainChangeCommand:
		if input.Module != "" || input.Path != "" || input.Field != "" {
			return ExplainChange{}, nil, errors.New("command change must not contain file fields")
		}
		if err := validateDisplayText("change command", input.Command); err != nil {
			return ExplainChange{}, nil, err
		}
		if input.Command != "plystra" && !strings.HasPrefix(input.Command, "plystra ") {
			return ExplainChange{}, nil, errors.New("command change must use the public plystra CLI")
		}
		if strings.ContainsAny(input.Command, "&|;<>") {
			return ExplainChange{}, nil, errors.New("command change must contain exactly one Plystra invocation")
		}
		if len(input.Argv) == 0 || input.Argv[0] != "plystra" {
			return ExplainChange{}, nil, errors.New("command change must contain exact public Plystra argv")
		}
		for index, argument := range input.Argv {
			if err := validateDisplayText(fmt.Sprintf("change argv[%d]", index), argument); err != nil {
				return ExplainChange{}, nil, err
			}
			if containsExplainPlaceholder(argument) {
				return ExplainChange{}, nil, fmt.Errorf("change argv[%d] contains an unresolved placeholder", index)
			}
		}
		if input.Command != explainCommandDisplay(input.Argv) {
			return ExplainChange{}, nil, errors.New("command change display does not match its exact argv")
		}
		return cloneExplainChange(input), nil, nil
	default:
		return ExplainChange{}, nil, fmt.Errorf("kind %q is not supported", input.Kind)
	}
}

func containsExplainPlaceholder(value string) bool {
	if strings.Contains(value, "${") || strings.Contains(value, "{{") || strings.Contains(value, "}}") {
		return true
	}
	start := strings.IndexByte(value, '<')
	return start >= 0 && strings.IndexByte(value[start+1:], '>') >= 0
}

func explainCommandDisplay(argv []string) string {
	var result strings.Builder
	for index, argument := range argv {
		if index != 0 {
			result.WriteByte(' ')
		}
		if index > 0 && (argv[index-1] == "--env" || argv[index-1] == "--config") || strings.IndexFunc(argument, unicode.IsSpace) >= 0 {
			result.WriteString(strconv.Quote(argument))
			continue
		}
		result.WriteString(argument)
	}
	return result.String()
}

func cloneExplainChange(value ExplainChange) ExplainChange {
	value.Argv = append([]string(nil), value.Argv...)
	return value
}

func equalExplainChange(left, right ExplainChange) bool {
	if left.Kind != right.Kind || left.Module != right.Module || left.Path != right.Path || left.Field != right.Field || left.Command != right.Command || len(left.Argv) != len(right.Argv) {
		return false
	}
	for index := range left.Argv {
		if left.Argv[index] != right.Argv[index] {
			return false
		}
	}
	return true
}

func explainChangeDocument(value ExplainChange) explainChange {
	return explainChange{
		Kind:    value.Kind,
		Module:  value.Module,
		Path:    value.Path,
		Field:   value.Field,
		Command: value.Command,
		Argv:    append([]string{}, value.Argv...),
	}
}

func normalizeExplainSources(mode generation.ConfigurationMode, digest string, values []diagnosticjson.Source) ([]diagnosticjson.Source, error) {
	return normalizeSchemaSources(explainSchemaV1, mode, digest, values)
}

func explainSources(values []diagnosticjson.Source) []explainSource {
	result := make([]explainSource, len(values))
	for index, source := range values {
		result[index] = explainSource{
			Module: source.Module,
			Path:   source.Path,
			Kind:   source.Kind,
			Line:   source.Line,
			Column: source.Column,
		}
	}
	return result
}

func encodeExplainDocument(document explainDocument) ([]byte, error) {
	if len(document.ResolutionEvidence) == 0 || !json.Valid(document.ResolutionEvidence) {
		return nil, errors.New("resolution evidence is not valid JSON")
	}
	return json.Marshal(document)
}
