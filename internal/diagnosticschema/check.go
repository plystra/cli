package diagnosticschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/resolutionevidence"
)

const (
	maximumCheckCount         = 1_024
	maximumCheckFindingCount  = 4_096
	maximumCheckSubjectLength = 1_024
)

var (
	// ErrCheck reports incomplete, inconsistent, or unsafe input for the
	// plystra.check v1 result schema.
	ErrCheck = errors.New("build plystra.check result")

	checkSchemaV1 = mustSchema("plystra.check", 1)
)

// CheckStatus is the closed state of one check. A complete result itself is
// passed or failed; skipped applies only to an ordered check that did not run.
type CheckStatus string

const (
	CheckStatusPassed  CheckStatus = "passed"
	CheckStatusFailed  CheckStatus = "failed"
	CheckStatusSkipped CheckStatus = "skipped"
)

// CheckFinding is one structured failure detail owned by a failed check.
type CheckFinding struct {
	Kind    string
	Subject string
	Summary string
	Sources []diagnosticjson.Source
}

// Check is one deterministically ordered validation outcome. Order is
// contiguous and one-based, and ID is a stable lower-kebab identity.
type Check struct {
	Order    uint32
	ID       string
	Status   CheckStatus
	Summary  string
	Findings []CheckFinding
	Sources  []diagnosticjson.Source
}

// CheckInput is the construction-only input for one plystra.check v1 result.
type CheckInput struct {
	Evidence    resolutionevidence.Evidence
	Checks      []Check
	Diagnostics []diagnosticjson.Diagnostic
	Sources     []diagnosticjson.Source
}

// CheckResult is one immutable plystra.check v1 diagnostic result.
type CheckResult struct {
	envelope               diagnosticjson.Envelope
	evidence               resolutionevidence.Evidence
	status                 CheckStatus
	failedCheckCount       int
	checks                 []Check
	resolutionEvidenceJSON []byte
	prepared               bool
}

type checkDocument struct {
	Status             CheckStatus          `json:"status"`
	FailedCheckCount   int                  `json:"failed_check_count"`
	Checks             []checkDocumentCheck `json:"checks"`
	ResolutionEvidence json.RawMessage      `json:"resolution_evidence"`
}

type checkDocumentCheck struct {
	Order    uint32                 `json:"order"`
	ID       string                 `json:"id"`
	Status   CheckStatus            `json:"status"`
	Summary  string                 `json:"summary"`
	Findings []checkDocumentFinding `json:"findings"`
	Sources  []checkSource          `json:"sources"`
}

type checkDocumentFinding struct {
	Kind    string        `json:"kind"`
	Subject string        `json:"subject"`
	Summary string        `json:"summary"`
	Sources []checkSource `json:"sources"`
}

type checkSource struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// CheckSchemaV1 returns the immutable command-owned schema descriptor.
func CheckSchemaV1() diagnosticjson.Schema { return checkSchemaV1 }

// NewCheck validates and constructs one complete plystra.check v1 result.
func NewCheck(input CheckInput) (CheckResult, error) {
	if !input.Evidence.Valid() {
		return CheckResult{}, fmt.Errorf("%w: resolution evidence is not valid", ErrCheck)
	}
	selection, exists := input.Evidence.ConfigurationSelection()
	if !exists {
		return CheckResult{}, fmt.Errorf("%w: resolution evidence omits selected configuration provenance", ErrCheck)
	}
	if _, exists := input.Evidence.StaticAssembly(); !exists {
		return CheckResult{}, fmt.Errorf("%w: resolution evidence omits static assembly membership", ErrCheck)
	}
	if _, exists := input.Evidence.HTTPTransports(); !exists {
		return CheckResult{}, fmt.Errorf("%w: resolution evidence omits selected HTTP transports", ErrCheck)
	}
	for index, diagnostic := range input.Diagnostics {
		if err := validateDisplayText(fmt.Sprintf("diagnostics[%d].message", index), diagnostic.Message); err != nil {
			return CheckResult{}, fmt.Errorf("%w: %v", ErrCheck, err)
		}
	}

	checks, status, failedCount, err := normalizeChecks(selection.Mode(), input.Evidence.BuildModelDigest(), input.Checks)
	if err != nil {
		return CheckResult{}, fmt.Errorf("%w: %v", ErrCheck, err)
	}
	allSources := collectSources(input.Evidence, input.Sources)
	for _, check := range checks {
		allSources = append(allSources, check.Sources...)
		for _, finding := range check.Findings {
			allSources = append(allSources, finding.Sources...)
		}
	}
	allSources, err = normalizeCheckSources(selection.Mode(), input.Evidence.BuildModelDigest(), allSources)
	if err != nil {
		return CheckResult{}, fmt.Errorf("%w: sources: %v", ErrCheck, err)
	}

	evidenceJSON := input.Evidence.CanonicalJSON()
	resultJSON, err := encodeCheckDocument(checkDocument{
		Status:             status,
		FailedCheckCount:   failedCount,
		Checks:             checkDocuments(checks),
		ResolutionEvidence: evidenceJSON,
	})
	if err != nil {
		return CheckResult{}, fmt.Errorf("%w: encode result: %v", ErrCheck, err)
	}
	envelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 checkSchemaV1,
		ConfigurationMode:      selection.Mode(),
		ApplicationModelDigest: input.Evidence.BuildModelDigest(),
		Diagnostics:            input.Diagnostics,
		Sources:                allSources,
		Result:                 resultJSON,
	})
	if err != nil {
		return CheckResult{}, fmt.Errorf("%w: shared envelope: %v", ErrCheck, err)
	}
	return CheckResult{
		envelope:               envelope,
		evidence:               input.Evidence,
		status:                 status,
		failedCheckCount:       failedCount,
		checks:                 checks,
		resolutionEvidenceJSON: evidenceJSON,
		prepared:               true,
	}, nil
}

// Valid reports whether NewCheck produced this internally consistent result.
func (r CheckResult) Valid() bool {
	if !r.prepared || !r.evidence.Valid() || !r.envelope.Valid() || r.envelope.Schema() != checkSchemaV1 || r.envelope.ApplicationModelDigest() != r.evidence.BuildModelDigest() {
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
	for index, diagnostic := range r.envelope.Diagnostics() {
		if validateDisplayText(fmt.Sprintf("diagnostics[%d].message", index), diagnostic.Message) != nil {
			return false
		}
	}
	checks, status, failedCount, err := normalizeChecks(selection.Mode(), r.evidence.BuildModelDigest(), r.checks)
	if err != nil || status != r.status || failedCount != r.failedCheckCount || !equalChecks(checks, r.checks) {
		return false
	}
	if !bytes.Equal(r.resolutionEvidenceJSON, r.evidence.CanonicalJSON()) {
		return false
	}
	resultJSON, err := encodeCheckDocument(checkDocument{
		Status:             r.status,
		FailedCheckCount:   r.failedCheckCount,
		Checks:             checkDocuments(r.checks),
		ResolutionEvidence: append([]byte(nil), r.resolutionEvidenceJSON...),
	})
	if err != nil {
		return false
	}
	canonicalEnvelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 checkSchemaV1,
		ConfigurationMode:      selection.Mode(),
		ApplicationModelDigest: r.evidence.BuildModelDigest(),
		Diagnostics:            r.envelope.Diagnostics(),
		Sources:                r.envelope.Sources(),
		Result:                 resultJSON,
	})
	return err == nil && bytes.Equal(canonicalEnvelope.CanonicalJSON(), r.envelope.CanonicalJSON())
}

// Envelope returns the immutable shared diagnostic envelope.
func (r CheckResult) Envelope() diagnosticjson.Envelope { return r.envelope }

// Status returns passed or failed.
func (r CheckResult) Status() CheckStatus { return r.status }

// FailedCheckCount returns the number of failed ordered checks.
func (r CheckResult) FailedCheckCount() int { return r.failedCheckCount }

// Checks returns a defensive copy in contiguous execution order.
func (r CheckResult) Checks() []Check { return cloneChecks(r.checks) }

// ResolutionEvidenceJSON returns a defensive copy of the complete canonical
// resolution-evidence document embedded in this command result.
func (r CheckResult) ResolutionEvidenceJSON() []byte {
	return append([]byte(nil), r.resolutionEvidenceJSON...)
}

func normalizeChecks(mode generation.ConfigurationMode, digest string, input []Check) ([]Check, CheckStatus, int, error) {
	if len(input) == 0 {
		return nil, "", 0, errors.New("at least one check is required")
	}
	if len(input) > maximumCheckCount {
		return nil, "", 0, fmt.Errorf("check count exceeds %d", maximumCheckCount)
	}
	checks := make([]Check, len(input))
	ids := make(map[string]struct{}, len(input))
	orders := make(map[uint32]struct{}, len(input))
	failedCount := 0
	completedCount := 0
	totalFindingCount := 0
	for index, value := range input {
		if value.Order == 0 {
			return nil, "", 0, fmt.Errorf("checks[%d].order must be positive", index)
		}
		if _, duplicate := orders[value.Order]; duplicate {
			return nil, "", 0, fmt.Errorf("checks[%d].order %d is duplicated", index, value.Order)
		}
		if !validExplanationCode(value.ID) {
			return nil, "", 0, fmt.Errorf("checks[%d].id %q is not canonical lower kebab case", index, value.ID)
		}
		if _, duplicate := ids[value.ID]; duplicate {
			return nil, "", 0, fmt.Errorf("checks[%d].id %q is duplicated", index, value.ID)
		}
		if !validCheckStatus(value.Status) {
			return nil, "", 0, fmt.Errorf("checks[%d].status %q is not supported", index, value.Status)
		}
		if err := validateDisplayText(fmt.Sprintf("checks[%d].summary", index), value.Summary); err != nil {
			return nil, "", 0, err
		}
		totalFindingCount += len(value.Findings)
		if totalFindingCount > maximumCheckFindingCount {
			return nil, "", 0, fmt.Errorf("total finding count exceeds %d", maximumCheckFindingCount)
		}
		findings, err := normalizeCheckFindings(mode, digest, index, value.Findings)
		if err != nil {
			return nil, "", 0, err
		}
		switch value.Status {
		case CheckStatusPassed:
			completedCount++
			if len(findings) != 0 {
				return nil, "", 0, fmt.Errorf("checks[%d] passed but contains failure findings", index)
			}
		case CheckStatusFailed:
			completedCount++
			failedCount++
			if len(findings) == 0 {
				return nil, "", 0, fmt.Errorf("checks[%d] failed without a structured finding", index)
			}
		case CheckStatusSkipped:
			if len(findings) != 0 {
				return nil, "", 0, fmt.Errorf("checks[%d] was skipped but contains failure findings", index)
			}
		}
		sources, err := normalizeCheckSources(mode, digest, value.Sources)
		if err != nil {
			return nil, "", 0, fmt.Errorf("checks[%d].sources: %v", index, err)
		}
		ids[value.ID] = struct{}{}
		orders[value.Order] = struct{}{}
		checks[index] = Check{Order: value.Order, ID: value.ID, Status: value.Status, Summary: value.Summary, Findings: findings, Sources: sources}
	}
	if completedCount == 0 {
		return nil, "", 0, errors.New("all checks are skipped")
	}
	sort.Slice(checks, func(left, right int) bool { return checks[left].Order < checks[right].Order })
	for index, check := range checks {
		if check.Order != uint32(index+1) {
			return nil, "", 0, fmt.Errorf("check order must be contiguous from 1; position %d has order %d", index+1, check.Order)
		}
	}
	status := CheckStatusPassed
	if failedCount > 0 {
		status = CheckStatusFailed
	}
	return checks, status, failedCount, nil
}

func normalizeCheckFindings(mode generation.ConfigurationMode, digest string, checkIndex int, input []CheckFinding) ([]CheckFinding, error) {
	if len(input) > maximumCheckFindingCount {
		return nil, fmt.Errorf("checks[%d].finding count exceeds %d", checkIndex, maximumCheckFindingCount)
	}
	findings := make([]CheckFinding, len(input))
	identities := make(map[string]struct{}, len(input))
	for index, value := range input {
		if !validExplanationCode(value.Kind) {
			return nil, fmt.Errorf("checks[%d].findings[%d].kind %q is not canonical lower kebab case", checkIndex, index, value.Kind)
		}
		if !validCheckSubject(value.Subject) {
			return nil, fmt.Errorf("checks[%d].findings[%d].subject %q is not a safe stable identity", checkIndex, index, value.Subject)
		}
		identity := value.Kind + "\x00" + value.Subject
		if _, duplicate := identities[identity]; duplicate {
			return nil, fmt.Errorf("checks[%d].findings[%d] duplicates %s for %s", checkIndex, index, value.Kind, value.Subject)
		}
		if err := validateDisplayText(fmt.Sprintf("checks[%d].findings[%d].summary", checkIndex, index), value.Summary); err != nil {
			return nil, err
		}
		sources, err := normalizeCheckSources(mode, digest, value.Sources)
		if err != nil {
			return nil, fmt.Errorf("checks[%d].findings[%d].sources: %v", checkIndex, index, err)
		}
		identities[identity] = struct{}{}
		findings[index] = CheckFinding{Kind: value.Kind, Subject: value.Subject, Summary: value.Summary, Sources: sources}
	}
	sort.Slice(findings, func(left, right int) bool {
		leftKey := findings[left].Kind + "\x00" + findings[left].Subject
		rightKey := findings[right].Kind + "\x00" + findings[right].Subject
		return leftKey < rightKey
	})
	return findings, nil
}

func validCheckStatus(value CheckStatus) bool {
	return value == CheckStatusPassed || value == CheckStatusFailed || value == CheckStatusSkipped
}

func validCheckSubject(value string) bool {
	if value == "" || len(value) > maximumCheckSubjectLength || !utf8.ValidString(value) || strings.HasPrefix(value, "/") || hasWindowsDrivePrefix(value) || strings.Contains(value, "\\") || containsAbsolutePath(value) {
		return false
	}
	return strings.IndexFunc(value, func(character rune) bool { return unicode.IsControl(character) || unicode.IsSpace(character) }) < 0
}

func normalizeCheckSources(mode generation.ConfigurationMode, digest string, values []diagnosticjson.Source) ([]diagnosticjson.Source, error) {
	return normalizeSchemaSources(checkSchemaV1, mode, digest, values)
}

func checkDocuments(values []Check) []checkDocumentCheck {
	result := make([]checkDocumentCheck, len(values))
	for index, check := range values {
		result[index] = checkDocumentCheck{
			Order:    check.Order,
			ID:       check.ID,
			Status:   check.Status,
			Summary:  check.Summary,
			Findings: checkFindingDocuments(check.Findings),
			Sources:  checkSources(check.Sources),
		}
	}
	return result
}

func checkFindingDocuments(values []CheckFinding) []checkDocumentFinding {
	result := make([]checkDocumentFinding, len(values))
	for index, finding := range values {
		result[index] = checkDocumentFinding{Kind: finding.Kind, Subject: finding.Subject, Summary: finding.Summary, Sources: checkSources(finding.Sources)}
	}
	return result
}

func checkSources(values []diagnosticjson.Source) []checkSource {
	result := make([]checkSource, len(values))
	for index, source := range values {
		result[index] = checkSource{Module: source.Module, Path: source.Path, Kind: source.Kind, Line: source.Line, Column: source.Column}
	}
	return result
}

func cloneChecks(values []Check) []Check {
	result := make([]Check, len(values))
	for index, check := range values {
		result[index] = check
		result[index].Sources = append([]diagnosticjson.Source(nil), check.Sources...)
		result[index].Findings = cloneCheckFindings(check.Findings)
	}
	return result
}

func cloneCheckFindings(values []CheckFinding) []CheckFinding {
	result := make([]CheckFinding, len(values))
	for index, finding := range values {
		result[index] = finding
		result[index].Sources = append([]diagnosticjson.Source(nil), finding.Sources...)
	}
	return result
}

func equalChecks(left, right []Check) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Order != right[index].Order || left[index].ID != right[index].ID || left[index].Status != right[index].Status || left[index].Summary != right[index].Summary || !equalDiagnosticSources(left[index].Sources, right[index].Sources) || !equalCheckFindings(left[index].Findings, right[index].Findings) {
			return false
		}
	}
	return true
}

func equalCheckFindings(left, right []CheckFinding) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Kind != right[index].Kind || left[index].Subject != right[index].Subject || left[index].Summary != right[index].Summary || !equalDiagnosticSources(left[index].Sources, right[index].Sources) {
			return false
		}
	}
	return true
}

func encodeCheckDocument(document checkDocument) ([]byte, error) {
	if len(document.ResolutionEvidence) == 0 || !json.Valid(document.ResolutionEvidence) {
		return nil, errors.New("resolution evidence is not valid JSON")
	}
	return json.Marshal(document)
}
