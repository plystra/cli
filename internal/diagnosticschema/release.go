package diagnosticschema

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"unicode"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/resolutionevidence"
)

const (
	maximumReleaseArtifactCount = 4_096
	releaseEvidenceDirectory    = "dist/release"
	releaseStatusPath           = "dist/release/release-status.json"
)

var (
	// ErrRelease reports incomplete, inconsistent, or unsafe input for the
	// plystra.release v1 result schema.
	ErrRelease = errors.New("build plystra.release result")

	releaseSchemaV1 = mustSchema("plystra.release", 1)

	releasePolicyFieldOrder = []ReleasePolicyField{
		ReleasePolicyAllowLocalReplace,
		ReleasePolicyRequireProviderConformance,
		ReleasePolicyRequireCleanVulnerabilityScan,
		ReleasePolicyRequireCompatibilityBaseline,
	}
	releaseEvidenceKindOrder = []ReleaseEvidenceKind{
		ReleaseEvidenceResolution,
		ReleaseEvidenceCompatibility,
		ReleaseEvidenceConformance,
		ReleaseEvidenceVulnerabilities,
		ReleaseEvidenceSBOM,
		ReleaseEvidenceProvenance,
	}
)

// ReleaseKind classifies a Project-local release attempt independently from
// whether its evidence is complete.
type ReleaseKind string

const (
	ReleaseKindPrereleaseCandidate ReleaseKind = "prerelease_candidate"
	ReleaseKindStable              ReleaseKind = "stable"
)

// ReleaseStatus is the binary status of one complete evidence set.
type ReleaseStatus string

const (
	ReleaseStatusComplete   ReleaseStatus = "complete"
	ReleaseStatusIncomplete ReleaseStatus = "incomplete"
)

// ReleasePolicyField identifies one field in the complete initial release
// policy schema.
type ReleasePolicyField string

const (
	ReleasePolicyAllowLocalReplace             ReleasePolicyField = "allow_local_replace"
	ReleasePolicyRequireProviderConformance    ReleasePolicyField = "require_provider_conformance"
	ReleasePolicyRequireCleanVulnerabilityScan ReleasePolicyField = "require_clean_vulnerability_scan"
	ReleasePolicyRequireCompatibilityBaseline  ReleasePolicyField = "require_compatibility_baseline"
)

// ReleasePolicyOutcome is the closed evaluation result for one policy field.
// A disabled requirement is not-required; a permitted local replacement is a
// passed evaluation rather than another policy state.
type ReleasePolicyOutcome string

const (
	ReleasePolicyPassed        ReleasePolicyOutcome = "passed"
	ReleasePolicyFailed        ReleasePolicyOutcome = "failed"
	ReleasePolicyIndeterminate ReleasePolicyOutcome = "indeterminate"
	ReleasePolicyNotRequired   ReleasePolicyOutcome = "not-required"
)

// ReleaseEvidenceKind identifies one required release-evidence artifact.
type ReleaseEvidenceKind string

const (
	ReleaseEvidenceResolution      ReleaseEvidenceKind = "resolution"
	ReleaseEvidenceCompatibility   ReleaseEvidenceKind = "compatibility"
	ReleaseEvidenceConformance     ReleaseEvidenceKind = "conformance"
	ReleaseEvidenceVulnerabilities ReleaseEvidenceKind = "vulnerabilities"
	ReleaseEvidenceSBOM            ReleaseEvidenceKind = "sbom"
	ReleaseEvidenceProvenance      ReleaseEvidenceKind = "provenance"
)

// ReleaseEvidenceStatus is the binary status of one required evidence file.
type ReleaseEvidenceStatus string

const (
	ReleaseEvidenceComplete   ReleaseEvidenceStatus = "complete"
	ReleaseEvidenceIncomplete ReleaseEvidenceStatus = "incomplete"
)

// ReleasePolicy is the complete four-boolean initial release policy.
type ReleasePolicy struct {
	AllowLocalReplace             bool
	RequireProviderConformance    bool
	RequireCleanVulnerabilityScan bool
	RequireCompatibilityBaseline  bool
}

// ReleasePolicyEvaluation records one deterministic policy decision.
type ReleasePolicyEvaluation struct {
	Field   ReleasePolicyField
	Outcome ReleasePolicyOutcome
	Summary string
	Sources []diagnosticjson.Source
}

// ReleaseEvidenceFileInput records one required evidence file before its fixed
// standard path is assigned by the schema.
type ReleaseEvidenceFileInput struct {
	Kind    ReleaseEvidenceKind
	Status  ReleaseEvidenceStatus
	Digest  string
	Sources []diagnosticjson.Source
}

// ReleaseEvidenceFile is one required standard evidence artifact.
type ReleaseEvidenceFile struct {
	Kind    ReleaseEvidenceKind
	Path    string
	Status  ReleaseEvidenceStatus
	Digest  string
	Sources []diagnosticjson.Source
}

// ReleaseArtifact is one released binary or package identified by a stable
// Project-relative path and canonical content digest.
type ReleaseArtifact struct {
	Kind    string
	Path    string
	Digest  string
	Sources []diagnosticjson.Source
}

// ReleaseInput is the construction-only input for one plystra.release v1
// result.
type ReleaseInput struct {
	Evidence          resolutionevidence.Evidence
	Kind              ReleaseKind
	Policy            ReleasePolicy
	PolicyEvaluations []ReleasePolicyEvaluation
	EvidenceFiles     []ReleaseEvidenceFileInput
	Artifacts         []ReleaseArtifact
	Diagnostics       []diagnosticjson.Diagnostic
	Sources           []diagnosticjson.Source
}

// ReleaseResult is one immutable plystra.release v1 diagnostic result.
type ReleaseResult struct {
	envelope                diagnosticjson.Envelope
	evidence                resolutionevidence.Evidence
	kind                    ReleaseKind
	policy                  ReleasePolicy
	policyEvaluations       []ReleasePolicyEvaluation
	status                  ReleaseStatus
	unsatisfiedPolicyCount  int
	incompleteEvidenceCount int
	evidenceFiles           []ReleaseEvidenceFile
	artifacts               []ReleaseArtifact
	resolutionEvidenceJSON  []byte
	prepared                bool
}

type releaseDocument struct {
	Status                  ReleaseStatus                     `json:"status"`
	ReleaseKind             ReleaseKind                       `json:"release_kind"`
	EvidenceDirectory       string                            `json:"evidence_directory"`
	StatusPath              string                            `json:"status_path"`
	UnsatisfiedPolicyCount  int                               `json:"unsatisfied_policy_count"`
	IncompleteEvidenceCount int                               `json:"incomplete_evidence_count"`
	Policy                  releasePolicyDocument             `json:"policy"`
	PolicyEvaluations       []releasePolicyEvaluationDocument `json:"policy_evaluations"`
	EvidenceFiles           []releaseEvidenceFileDocument     `json:"evidence_files"`
	Artifacts               []releaseArtifactDocument         `json:"artifacts"`
	ResolutionEvidence      json.RawMessage                   `json:"resolution_evidence"`
}

type releasePolicyDocument struct {
	AllowLocalReplace             bool `json:"allow_local_replace"`
	RequireProviderConformance    bool `json:"require_provider_conformance"`
	RequireCleanVulnerabilityScan bool `json:"require_clean_vulnerability_scan"`
	RequireCompatibilityBaseline  bool `json:"require_compatibility_baseline"`
}

type releasePolicyEvaluationDocument struct {
	Field   ReleasePolicyField   `json:"field"`
	Outcome ReleasePolicyOutcome `json:"outcome"`
	Summary string               `json:"summary"`
	Sources []releaseSource      `json:"sources"`
}

type releaseEvidenceFileDocument struct {
	Kind    ReleaseEvidenceKind   `json:"kind"`
	Path    string                `json:"path"`
	Status  ReleaseEvidenceStatus `json:"status"`
	Digest  string                `json:"digest"`
	Sources []releaseSource       `json:"sources"`
}

type releaseArtifactDocument struct {
	Kind    string          `json:"kind"`
	Path    string          `json:"path"`
	Digest  string          `json:"digest"`
	Sources []releaseSource `json:"sources"`
}

type releaseSource struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// ReleaseSchemaV1 returns the immutable command-owned schema descriptor.
func ReleaseSchemaV1() diagnosticjson.Schema { return releaseSchemaV1 }

// NewRelease validates and constructs one complete plystra.release v1 result.
func NewRelease(input ReleaseInput) (ReleaseResult, error) {
	if !input.Evidence.Valid() {
		return ReleaseResult{}, fmt.Errorf("%w: resolution evidence is not valid", ErrRelease)
	}
	selection, exists := input.Evidence.ConfigurationSelection()
	if !exists {
		return ReleaseResult{}, fmt.Errorf("%w: resolution evidence omits selected configuration provenance", ErrRelease)
	}
	if _, exists := input.Evidence.StaticAssembly(); !exists {
		return ReleaseResult{}, fmt.Errorf("%w: resolution evidence omits static assembly membership", ErrRelease)
	}
	if _, exists := input.Evidence.HTTPTransports(); !exists {
		return ReleaseResult{}, fmt.Errorf("%w: resolution evidence omits selected HTTP transports", ErrRelease)
	}
	if !validReleaseKind(input.Kind) {
		return ReleaseResult{}, fmt.Errorf("%w: release kind %q is not supported", ErrRelease, input.Kind)
	}
	errorDiagnosticCount, err := validateReleaseDiagnostics(input.Diagnostics)
	if err != nil {
		return ReleaseResult{}, fmt.Errorf("%w: %v", ErrRelease, err)
	}

	evaluations, unsatisfiedPolicyCount, err := normalizeReleasePolicyEvaluations(input.Policy, selection.Mode(), input.Evidence.BuildModelDigest(), input.PolicyEvaluations)
	if err != nil {
		return ReleaseResult{}, fmt.Errorf("%w: policy evaluations: %v", ErrRelease, err)
	}
	evidenceFiles, incompleteEvidenceCount, err := normalizeReleaseEvidenceFiles(selection.Mode(), input.Evidence.BuildModelDigest(), input.EvidenceFiles)
	if err != nil {
		return ReleaseResult{}, fmt.Errorf("%w: evidence files: %v", ErrRelease, err)
	}
	artifacts, err := normalizeReleaseArtifacts(selection.Mode(), input.Evidence.BuildModelDigest(), input.Artifacts)
	if err != nil {
		return ReleaseResult{}, fmt.Errorf("%w: artifacts: %v", ErrRelease, err)
	}
	status := deriveReleaseStatus(unsatisfiedPolicyCount, incompleteEvidenceCount)
	if status == ReleaseStatusComplete && len(artifacts) == 0 {
		return ReleaseResult{}, fmt.Errorf("%w: a complete release requires at least one released artifact", ErrRelease)
	}
	if status == ReleaseStatusComplete && errorDiagnosticCount != 0 {
		return ReleaseResult{}, fmt.Errorf("%w: a complete release contains an error diagnostic", ErrRelease)
	}
	if status == ReleaseStatusIncomplete && errorDiagnosticCount == 0 {
		return ReleaseResult{}, fmt.Errorf("%w: an incomplete release requires an error diagnostic", ErrRelease)
	}

	allSources := collectSources(input.Evidence, input.Sources)
	for _, evaluation := range evaluations {
		allSources = append(allSources, evaluation.Sources...)
	}
	for _, evidenceFile := range evidenceFiles {
		allSources = append(allSources, evidenceFile.Sources...)
	}
	for _, artifact := range artifacts {
		allSources = append(allSources, artifact.Sources...)
	}
	allSources, err = normalizeReleaseSources(selection.Mode(), input.Evidence.BuildModelDigest(), allSources)
	if err != nil {
		return ReleaseResult{}, fmt.Errorf("%w: sources: %v", ErrRelease, err)
	}

	evidenceJSON := input.Evidence.CanonicalJSON()
	resultJSON, err := encodeReleaseDocument(makeReleaseDocument(
		status,
		input.Kind,
		unsatisfiedPolicyCount,
		incompleteEvidenceCount,
		input.Policy,
		evaluations,
		evidenceFiles,
		artifacts,
		evidenceJSON,
	))
	if err != nil {
		return ReleaseResult{}, fmt.Errorf("%w: encode result: %v", ErrRelease, err)
	}
	envelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 releaseSchemaV1,
		ConfigurationMode:      selection.Mode(),
		ApplicationModelDigest: input.Evidence.BuildModelDigest(),
		Diagnostics:            input.Diagnostics,
		Sources:                allSources,
		Result:                 resultJSON,
	})
	if err != nil {
		return ReleaseResult{}, fmt.Errorf("%w: shared envelope: %v", ErrRelease, err)
	}
	return ReleaseResult{
		envelope:                envelope,
		evidence:                input.Evidence,
		kind:                    input.Kind,
		policy:                  input.Policy,
		policyEvaluations:       evaluations,
		status:                  status,
		unsatisfiedPolicyCount:  unsatisfiedPolicyCount,
		incompleteEvidenceCount: incompleteEvidenceCount,
		evidenceFiles:           evidenceFiles,
		artifacts:               artifacts,
		resolutionEvidenceJSON:  evidenceJSON,
		prepared:                true,
	}, nil
}

// Valid reports whether NewRelease produced this internally consistent result.
func (r ReleaseResult) Valid() bool {
	if !r.prepared || !r.evidence.Valid() || !r.envelope.Valid() || r.envelope.Schema() != releaseSchemaV1 || r.envelope.ApplicationModelDigest() != r.evidence.BuildModelDigest() || !validReleaseKind(r.kind) {
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
	errorDiagnosticCount, err := validateReleaseDiagnostics(r.envelope.Diagnostics())
	if err != nil {
		return false
	}
	evaluations, unsatisfiedPolicyCount, err := normalizeReleasePolicyEvaluations(r.policy, selection.Mode(), r.evidence.BuildModelDigest(), r.policyEvaluations)
	if err != nil || unsatisfiedPolicyCount != r.unsatisfiedPolicyCount || !equalReleasePolicyEvaluations(evaluations, r.policyEvaluations) {
		return false
	}
	evidenceFiles, incompleteEvidenceCount, err := normalizeReleaseEvidenceFiles(selection.Mode(), r.evidence.BuildModelDigest(), releaseEvidenceInputs(r.evidenceFiles))
	if err != nil || incompleteEvidenceCount != r.incompleteEvidenceCount || !equalReleaseEvidenceFiles(evidenceFiles, r.evidenceFiles) {
		return false
	}
	artifacts, err := normalizeReleaseArtifacts(selection.Mode(), r.evidence.BuildModelDigest(), r.artifacts)
	if err != nil || !equalReleaseArtifacts(artifacts, r.artifacts) {
		return false
	}
	status := deriveReleaseStatus(unsatisfiedPolicyCount, incompleteEvidenceCount)
	if status != r.status || status == ReleaseStatusComplete && len(artifacts) == 0 || status == ReleaseStatusComplete && errorDiagnosticCount != 0 || status == ReleaseStatusIncomplete && errorDiagnosticCount == 0 {
		return false
	}
	if !bytes.Equal(r.resolutionEvidenceJSON, r.evidence.CanonicalJSON()) {
		return false
	}
	resultJSON, err := encodeReleaseDocument(makeReleaseDocument(
		r.status,
		r.kind,
		r.unsatisfiedPolicyCount,
		r.incompleteEvidenceCount,
		r.policy,
		r.policyEvaluations,
		r.evidenceFiles,
		r.artifacts,
		append([]byte(nil), r.resolutionEvidenceJSON...),
	))
	if err != nil {
		return false
	}
	canonicalEnvelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 releaseSchemaV1,
		ConfigurationMode:      selection.Mode(),
		ApplicationModelDigest: r.evidence.BuildModelDigest(),
		Diagnostics:            r.envelope.Diagnostics(),
		Sources:                r.envelope.Sources(),
		Result:                 resultJSON,
	})
	return err == nil && bytes.Equal(canonicalEnvelope.CanonicalJSON(), r.envelope.CanonicalJSON())
}

// Envelope returns the immutable shared diagnostic envelope.
func (r ReleaseResult) Envelope() diagnosticjson.Envelope { return r.envelope }

// Kind returns prerelease_candidate or stable.
func (r ReleaseResult) Kind() ReleaseKind { return r.kind }

// Status returns complete or incomplete.
func (r ReleaseResult) Status() ReleaseStatus { return r.status }

// Policy returns the exact evaluated four-boolean policy.
func (r ReleaseResult) Policy() ReleasePolicy { return r.policy }

// UnsatisfiedPolicyCount returns the number of failed or indeterminate policy
// evaluations.
func (r ReleaseResult) UnsatisfiedPolicyCount() int { return r.unsatisfiedPolicyCount }

// IncompleteEvidenceCount returns the number of incomplete required evidence
// files.
func (r ReleaseResult) IncompleteEvidenceCount() int { return r.incompleteEvidenceCount }

// PolicyEvaluations returns a defensive copy in canonical field order.
func (r ReleaseResult) PolicyEvaluations() []ReleasePolicyEvaluation {
	return cloneReleasePolicyEvaluations(r.policyEvaluations)
}

// EvidenceFiles returns a defensive copy in fixed standard order.
func (r ReleaseResult) EvidenceFiles() []ReleaseEvidenceFile {
	return cloneReleaseEvidenceFiles(r.evidenceFiles)
}

// Artifacts returns a defensive copy in canonical kind and path order.
func (r ReleaseResult) Artifacts() []ReleaseArtifact {
	return cloneReleaseArtifacts(r.artifacts)
}

// ResolutionEvidenceJSON returns a defensive copy of the complete canonical
// resolution-evidence document embedded in this command result.
func (r ReleaseResult) ResolutionEvidenceJSON() []byte {
	return append([]byte(nil), r.resolutionEvidenceJSON...)
}

// EvidenceDirectory returns the fixed Project-relative release-evidence root.
func (ReleaseResult) EvidenceDirectory() string { return releaseEvidenceDirectory }

// StatusPath returns the fixed Project-relative release-status path.
func (ReleaseResult) StatusPath() string { return releaseStatusPath }

func normalizeReleasePolicyEvaluations(policy ReleasePolicy, mode generation.ConfigurationMode, digest string, input []ReleasePolicyEvaluation) ([]ReleasePolicyEvaluation, int, error) {
	if len(input) != len(releasePolicyFieldOrder) {
		return nil, 0, fmt.Errorf("exactly %d policy evaluations are required", len(releasePolicyFieldOrder))
	}
	byField := make(map[ReleasePolicyField]ReleasePolicyEvaluation, len(input))
	unsatisfiedCount := 0
	for index, value := range input {
		if !validReleasePolicyField(value.Field) {
			return nil, 0, fmt.Errorf("policy_evaluations[%d].field %q is not supported", index, value.Field)
		}
		if _, duplicate := byField[value.Field]; duplicate {
			return nil, 0, fmt.Errorf("policy_evaluations[%d].field %q is duplicated", index, value.Field)
		}
		if err := validateReleasePolicyOutcome(policy, value.Field, value.Outcome); err != nil {
			return nil, 0, fmt.Errorf("policy_evaluations[%d]: %v", index, err)
		}
		if err := validateDisplayText(fmt.Sprintf("policy_evaluations[%d].summary", index), value.Summary); err != nil {
			return nil, 0, err
		}
		sources, err := normalizeReleaseSources(mode, digest, value.Sources)
		if err != nil {
			return nil, 0, fmt.Errorf("policy_evaluations[%d].sources: %v", index, err)
		}
		if value.Outcome == ReleasePolicyFailed || value.Outcome == ReleasePolicyIndeterminate {
			unsatisfiedCount++
		}
		byField[value.Field] = ReleasePolicyEvaluation{Field: value.Field, Outcome: value.Outcome, Summary: value.Summary, Sources: sources}
	}
	result := make([]ReleasePolicyEvaluation, len(releasePolicyFieldOrder))
	for index, field := range releasePolicyFieldOrder {
		value, exists := byField[field]
		if !exists {
			return nil, 0, fmt.Errorf("policy evaluation for %s is missing", field)
		}
		result[index] = value
	}
	return result, unsatisfiedCount, nil
}

func validateReleasePolicyOutcome(policy ReleasePolicy, field ReleasePolicyField, outcome ReleasePolicyOutcome) error {
	if field == ReleasePolicyAllowLocalReplace && policy.AllowLocalReplace {
		if outcome != ReleasePolicyPassed {
			return fmt.Errorf("%s must be passed when local replacements are allowed", field)
		}
		return nil
	}
	required := releasePolicyFieldRequired(policy, field)
	if !required {
		if outcome != ReleasePolicyNotRequired {
			return fmt.Errorf("%s must be not-required when its requirement is disabled", field)
		}
		return nil
	}
	switch outcome {
	case ReleasePolicyPassed, ReleasePolicyFailed, ReleasePolicyIndeterminate:
		return nil
	default:
		return fmt.Errorf("%s outcome %q is not supported for an evaluated policy", field, outcome)
	}
}

func releasePolicyFieldRequired(policy ReleasePolicy, field ReleasePolicyField) bool {
	switch field {
	case ReleasePolicyAllowLocalReplace:
		return !policy.AllowLocalReplace
	case ReleasePolicyRequireProviderConformance:
		return policy.RequireProviderConformance
	case ReleasePolicyRequireCleanVulnerabilityScan:
		return policy.RequireCleanVulnerabilityScan
	case ReleasePolicyRequireCompatibilityBaseline:
		return policy.RequireCompatibilityBaseline
	default:
		return false
	}
}

func validReleasePolicyField(value ReleasePolicyField) bool {
	switch value {
	case ReleasePolicyAllowLocalReplace, ReleasePolicyRequireProviderConformance, ReleasePolicyRequireCleanVulnerabilityScan, ReleasePolicyRequireCompatibilityBaseline:
		return true
	default:
		return false
	}
}

func normalizeReleaseEvidenceFiles(mode generation.ConfigurationMode, digest string, input []ReleaseEvidenceFileInput) ([]ReleaseEvidenceFile, int, error) {
	if len(input) != len(releaseEvidenceKindOrder) {
		return nil, 0, fmt.Errorf("exactly %d evidence files are required", len(releaseEvidenceKindOrder))
	}
	byKind := make(map[ReleaseEvidenceKind]ReleaseEvidenceFile, len(input))
	incompleteCount := 0
	for index, value := range input {
		path, exists := releaseEvidencePath(value.Kind)
		if !exists {
			return nil, 0, fmt.Errorf("evidence_files[%d].kind %q is not supported", index, value.Kind)
		}
		if _, duplicate := byKind[value.Kind]; duplicate {
			return nil, 0, fmt.Errorf("evidence_files[%d].kind %q is duplicated", index, value.Kind)
		}
		if value.Status != ReleaseEvidenceComplete && value.Status != ReleaseEvidenceIncomplete {
			return nil, 0, fmt.Errorf("evidence_files[%d].status %q is not supported", index, value.Status)
		}
		if !validReleaseDigest(value.Digest) {
			return nil, 0, fmt.Errorf("evidence_files[%d].digest is not a canonical SHA-256 digest", index)
		}
		sources, err := normalizeReleaseSources(mode, digest, value.Sources)
		if err != nil {
			return nil, 0, fmt.Errorf("evidence_files[%d].sources: %v", index, err)
		}
		if value.Status == ReleaseEvidenceIncomplete {
			incompleteCount++
		}
		byKind[value.Kind] = ReleaseEvidenceFile{Kind: value.Kind, Path: path, Status: value.Status, Digest: value.Digest, Sources: sources}
	}
	result := make([]ReleaseEvidenceFile, len(releaseEvidenceKindOrder))
	for index, kind := range releaseEvidenceKindOrder {
		value, exists := byKind[kind]
		if !exists {
			return nil, 0, fmt.Errorf("evidence file for %s is missing", kind)
		}
		result[index] = value
	}
	return result, incompleteCount, nil
}

func releaseEvidencePath(kind ReleaseEvidenceKind) (string, bool) {
	switch kind {
	case ReleaseEvidenceResolution:
		return releaseEvidenceDirectory + "/resolution.json", true
	case ReleaseEvidenceCompatibility:
		return releaseEvidenceDirectory + "/compatibility.json", true
	case ReleaseEvidenceConformance:
		return releaseEvidenceDirectory + "/conformance.json", true
	case ReleaseEvidenceVulnerabilities:
		return releaseEvidenceDirectory + "/vulnerabilities.json", true
	case ReleaseEvidenceSBOM:
		return releaseEvidenceDirectory + "/sbom.spdx.json", true
	case ReleaseEvidenceProvenance:
		return releaseEvidenceDirectory + "/provenance.intoto.jsonl", true
	default:
		return "", false
	}
}

func normalizeReleaseArtifacts(mode generation.ConfigurationMode, digest string, input []ReleaseArtifact) ([]ReleaseArtifact, error) {
	if len(input) > maximumReleaseArtifactCount {
		return nil, fmt.Errorf("artifact count exceeds %d", maximumReleaseArtifactCount)
	}
	result := make([]ReleaseArtifact, len(input))
	paths := make(map[string]struct{}, len(input))
	for index, value := range input {
		if !validExplanationCode(value.Kind) {
			return nil, fmt.Errorf("artifacts[%d].kind %q is not canonical lower kebab case", index, value.Kind)
		}
		if !validReleaseArtifactPath(value.Path) {
			return nil, fmt.Errorf("artifacts[%d].path %q is not a safe Project-relative path", index, value.Path)
		}
		if _, duplicate := paths[value.Path]; duplicate {
			return nil, fmt.Errorf("artifacts[%d].path %q is duplicated", index, value.Path)
		}
		if !validReleaseDigest(value.Digest) {
			return nil, fmt.Errorf("artifacts[%d].digest is not a canonical SHA-256 digest", index)
		}
		sources, err := normalizeReleaseSources(mode, digest, value.Sources)
		if err != nil {
			return nil, fmt.Errorf("artifacts[%d].sources: %v", index, err)
		}
		paths[value.Path] = struct{}{}
		result[index] = ReleaseArtifact{Kind: value.Kind, Path: value.Path, Digest: value.Digest, Sources: sources}
	}
	sort.Slice(result, func(left, right int) bool {
		leftKey := result[left].Kind + "\x00" + result[left].Path
		rightKey := result[right].Kind + "\x00" + result[right].Path
		return leftKey < rightKey
	})
	return result, nil
}

func validReleaseArtifactPath(value string) bool {
	if value == "" || value == "." || !fs.ValidPath(value) || strings.ContainsAny(value, "\\:") || hasWindowsDrivePrefix(value) {
		return false
	}
	return strings.IndexFunc(value, func(character rune) bool {
		return unicode.IsControl(character) || unicode.IsSpace(character)
	}) < 0
}

func validReleaseDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	hexValue := strings.TrimPrefix(value, "sha256:")
	if hexValue != strings.ToLower(hexValue) {
		return false
	}
	decoded, err := hex.DecodeString(hexValue)
	return err == nil && len(decoded) == 32
}

func validReleaseKind(value ReleaseKind) bool {
	return value == ReleaseKindPrereleaseCandidate || value == ReleaseKindStable
}

func validateReleaseDiagnostics(values []diagnosticjson.Diagnostic) (int, error) {
	errorCount := 0
	for index, diagnostic := range values {
		if err := validateDisplayText(fmt.Sprintf("diagnostics[%d].message", index), diagnostic.Message); err != nil {
			return 0, err
		}
		if diagnostic.Severity == diagnosticjson.SeverityError {
			errorCount++
		}
	}
	return errorCount, nil
}

func deriveReleaseStatus(unsatisfiedPolicyCount, incompleteEvidenceCount int) ReleaseStatus {
	if unsatisfiedPolicyCount != 0 || incompleteEvidenceCount != 0 {
		return ReleaseStatusIncomplete
	}
	return ReleaseStatusComplete
}

func normalizeReleaseSources(mode generation.ConfigurationMode, digest string, values []diagnosticjson.Source) ([]diagnosticjson.Source, error) {
	return normalizeSchemaSources(releaseSchemaV1, mode, digest, values)
}

func makeReleaseDocument(status ReleaseStatus, kind ReleaseKind, unsatisfiedPolicyCount, incompleteEvidenceCount int, policy ReleasePolicy, evaluations []ReleasePolicyEvaluation, evidenceFiles []ReleaseEvidenceFile, artifacts []ReleaseArtifact, evidenceJSON []byte) releaseDocument {
	return releaseDocument{
		Status:                  status,
		ReleaseKind:             kind,
		EvidenceDirectory:       releaseEvidenceDirectory,
		StatusPath:              releaseStatusPath,
		UnsatisfiedPolicyCount:  unsatisfiedPolicyCount,
		IncompleteEvidenceCount: incompleteEvidenceCount,
		Policy:                  releasePolicyDocument(policy),
		PolicyEvaluations:       releasePolicyEvaluationDocuments(evaluations),
		EvidenceFiles:           releaseEvidenceFileDocuments(evidenceFiles),
		Artifacts:               releaseArtifactDocuments(artifacts),
		ResolutionEvidence:      append([]byte(nil), evidenceJSON...),
	}
}

func releasePolicyEvaluationDocuments(values []ReleasePolicyEvaluation) []releasePolicyEvaluationDocument {
	result := make([]releasePolicyEvaluationDocument, len(values))
	for index, value := range values {
		result[index] = releasePolicyEvaluationDocument{Field: value.Field, Outcome: value.Outcome, Summary: value.Summary, Sources: releaseSources(value.Sources)}
	}
	return result
}

func releaseEvidenceFileDocuments(values []ReleaseEvidenceFile) []releaseEvidenceFileDocument {
	result := make([]releaseEvidenceFileDocument, len(values))
	for index, value := range values {
		result[index] = releaseEvidenceFileDocument{Kind: value.Kind, Path: value.Path, Status: value.Status, Digest: value.Digest, Sources: releaseSources(value.Sources)}
	}
	return result
}

func releaseArtifactDocuments(values []ReleaseArtifact) []releaseArtifactDocument {
	result := make([]releaseArtifactDocument, len(values))
	for index, value := range values {
		result[index] = releaseArtifactDocument{Kind: value.Kind, Path: value.Path, Digest: value.Digest, Sources: releaseSources(value.Sources)}
	}
	return result
}

func releaseSources(values []diagnosticjson.Source) []releaseSource {
	result := make([]releaseSource, len(values))
	for index, source := range values {
		result[index] = releaseSource{Module: source.Module, Path: source.Path, Kind: source.Kind, Line: source.Line, Column: source.Column}
	}
	return result
}

func releaseEvidenceInputs(values []ReleaseEvidenceFile) []ReleaseEvidenceFileInput {
	result := make([]ReleaseEvidenceFileInput, len(values))
	for index, value := range values {
		result[index] = ReleaseEvidenceFileInput{Kind: value.Kind, Status: value.Status, Digest: value.Digest, Sources: append([]diagnosticjson.Source(nil), value.Sources...)}
	}
	return result
}

func cloneReleasePolicyEvaluations(values []ReleasePolicyEvaluation) []ReleasePolicyEvaluation {
	result := make([]ReleasePolicyEvaluation, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Sources = append([]diagnosticjson.Source(nil), value.Sources...)
	}
	return result
}

func cloneReleaseEvidenceFiles(values []ReleaseEvidenceFile) []ReleaseEvidenceFile {
	result := make([]ReleaseEvidenceFile, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Sources = append([]diagnosticjson.Source(nil), value.Sources...)
	}
	return result
}

func cloneReleaseArtifacts(values []ReleaseArtifact) []ReleaseArtifact {
	result := make([]ReleaseArtifact, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Sources = append([]diagnosticjson.Source(nil), value.Sources...)
	}
	return result
}

func equalReleasePolicyEvaluations(left, right []ReleasePolicyEvaluation) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Field != right[index].Field || left[index].Outcome != right[index].Outcome || left[index].Summary != right[index].Summary || !equalDiagnosticSources(left[index].Sources, right[index].Sources) {
			return false
		}
	}
	return true
}

func equalReleaseEvidenceFiles(left, right []ReleaseEvidenceFile) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Kind != right[index].Kind || left[index].Path != right[index].Path || left[index].Status != right[index].Status || left[index].Digest != right[index].Digest || !equalDiagnosticSources(left[index].Sources, right[index].Sources) {
			return false
		}
	}
	return true
}

func equalReleaseArtifacts(left, right []ReleaseArtifact) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Kind != right[index].Kind || left[index].Path != right[index].Path || left[index].Digest != right[index].Digest || !equalDiagnosticSources(left[index].Sources, right[index].Sources) {
			return false
		}
	}
	return true
}

func encodeReleaseDocument(document releaseDocument) ([]byte, error) {
	if len(document.ResolutionEvidence) == 0 || !json.Valid(document.ResolutionEvidence) {
		return nil, errors.New("resolution evidence is not valid JSON")
	}
	return json.Marshal(document)
}
