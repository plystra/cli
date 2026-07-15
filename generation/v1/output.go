package generation

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// ErrInvalidOutput reports malformed or context-inconsistent extension output.
var ErrInvalidOutput = errors.New("invalid generation output")

// GenerateFunc is the required signature of a generation package's exported
// Generate function.
type GenerateFunc func(GenerationContext) (Output, error)

// Output is the construction form returned by an extension. The CLI validates
// and canonicalizes it with NormalizeOutput before using any member.
//
// The unexported field intentionally prevents unkeyed external literals so v1
// can add backward-compatible structured result fields.
type Output struct {
	Requirements []Requirement `json:"requirements"`
	Diagnostics  []Diagnostic  `json:"diagnostics"`
	_            struct{}
}

// Requirement records one exact generation-derived canonical Capability
// requirement and the rule input that introduced it.
type Requirement struct {
	RuleID     string       `json:"rule_id"`
	Namespace  string       `json:"namespace"`
	Source     CapabilityID `json:"source"`
	Capability CapabilityID `json:"capability"`
	_          struct{}
}

// DiagnosticSeverity classifies one structured extension diagnostic.
type DiagnosticSeverity string

const (
	DiagnosticInfo    DiagnosticSeverity = "info"
	DiagnosticWarning DiagnosticSeverity = "warning"
	DiagnosticError   DiagnosticSeverity = "error"
)

// Diagnostic records one actionable extension finding and its normalized rule
// input provenance.
type Diagnostic struct {
	Code      string             `json:"code"`
	Severity  DiagnosticSeverity `json:"severity"`
	Message   string             `json:"message"`
	Namespace string             `json:"namespace"`
	Source    CapabilityID       `json:"source"`
	RuleID    string             `json:"rule_id"`
	_         struct{}
}

// NormalizedOutput is immutable, deterministically ordered extension output.
type NormalizedOutput struct {
	requirements  []Requirement
	diagnostics   []Diagnostic
	canonicalJSON []byte
	digest        string
}

// NormalizeOutput validates one extension result against the exact context it
// received, then returns immutable canonical output.
func NormalizeOutput(context Context, output Output) (NormalizedOutput, error) {
	requirements, err := normalizeOutputRequirements(context, output.Requirements)
	if err != nil {
		return NormalizedOutput{}, err
	}
	diagnostics, err := normalizeOutputDiagnostics(context, output.Diagnostics)
	if err != nil {
		return NormalizedOutput{}, err
	}
	canonical, err := encodeOutput(requirements, diagnostics)
	if err != nil {
		return NormalizedOutput{}, invalidOutput("encode canonical output: %v", err)
	}
	if len(canonical) > maximumJSONSize {
		return NormalizedOutput{}, invalidOutput("canonical output exceeds %d bytes", maximumJSONSize)
	}
	return NormalizedOutput{
		requirements:  requirements,
		diagnostics:   diagnostics,
		canonicalJSON: canonical,
		digest:        outputDigest(canonical),
	}, nil
}

// Requirements returns defensive copies in canonical provenance order.
func (o NormalizedOutput) Requirements() []Requirement {
	return append([]Requirement(nil), o.requirements...)
}

// Diagnostics returns defensive copies in canonical diagnostic order.
func (o NormalizedOutput) Diagnostics() []Diagnostic {
	return append([]Diagnostic(nil), o.diagnostics...)
}

// CanonicalJSON returns a defensive copy of the normalized protocol output.
func (o NormalizedOutput) CanonicalJSON() []byte {
	return append([]byte(nil), o.canonicalJSON...)
}

// Digest returns the sha256 digest of CanonicalJSON with a sha256: prefix.
func (o NormalizedOutput) Digest() string { return o.digest }

func normalizeOutputRequirements(context Context, inputs []Requirement) ([]Requirement, error) {
	requirements := append([]Requirement(nil), inputs...)
	seen := make(map[string]struct{}, len(requirements))
	for index, requirement := range requirements {
		field := fmt.Sprintf("requirements[%d]", index)
		if !validStableID(requirement.RuleID) {
			return nil, invalidOutput("%s.rule_id %q is not a stable lower-kebab identifier", field, requirement.RuleID)
		}
		if err := validateOutputSource(context, field, requirement.Namespace, requirement.Source); err != nil {
			return nil, err
		}
		if _, exists := context.Capability(requirement.Capability); !exists {
			return nil, invalidOutput("%s.capability %q is not a visible canonical Capability", field, requirement.Capability.String())
		}
		key := requirement.Namespace + "\x00" + requirement.Source.String() + "\x00" + requirement.RuleID + "\x00" + requirement.Capability.String()
		if _, duplicate := seen[key]; duplicate {
			return nil, invalidOutput("%s duplicates generation requirement %q", field, requirement.Capability.String())
		}
		seen[key] = struct{}{}
	}
	sort.Slice(requirements, func(left, right int) bool {
		return requirementSortKey(requirements[left]) < requirementSortKey(requirements[right])
	})
	return requirements, nil
}

func normalizeOutputDiagnostics(context Context, inputs []Diagnostic) ([]Diagnostic, error) {
	diagnostics := append([]Diagnostic(nil), inputs...)
	seen := make(map[string]struct{}, len(diagnostics))
	for index, diagnostic := range diagnostics {
		field := fmt.Sprintf("diagnostics[%d]", index)
		if !validStableID(diagnostic.Code) {
			return nil, invalidOutput("%s.code %q is not a stable lower-kebab identifier", field, diagnostic.Code)
		}
		if !validStableID(diagnostic.RuleID) {
			return nil, invalidOutput("%s.rule_id %q is not a stable lower-kebab identifier", field, diagnostic.RuleID)
		}
		switch diagnostic.Severity {
		case DiagnosticInfo, DiagnosticWarning, DiagnosticError:
		default:
			return nil, invalidOutput("%s.severity %q is not supported", field, diagnostic.Severity)
		}
		if diagnostic.Message == "" || len(diagnostic.Message) > 4096 || !utf8.ValidString(diagnostic.Message) || strings.ContainsRune(diagnostic.Message, '\x00') {
			return nil, invalidOutput("%s.message must be non-empty valid UTF-8, at most 4096 bytes, and contain no NUL", field)
		}
		if err := validateOutputSource(context, field, diagnostic.Namespace, diagnostic.Source); err != nil {
			return nil, err
		}
		key := diagnosticSortKey(diagnostic)
		if _, duplicate := seen[key]; duplicate {
			return nil, invalidOutput("%s duplicates diagnostic %q", field, diagnostic.Code)
		}
		seen[key] = struct{}{}
	}
	sort.Slice(diagnostics, func(left, right int) bool {
		return diagnosticSortKey(diagnostics[left]) < diagnosticSortKey(diagnostics[right])
	})
	return diagnostics, nil
}

func validateOutputSource(context Context, field, namespace string, source CapabilityID) error {
	if !validNamespace(namespace) {
		return invalidOutput("%s.namespace %q is not canonical lower kebab case", field, namespace)
	}
	capability, exists := context.Capability(source)
	if !exists {
		return invalidOutput("%s.source %q is not a visible canonical Capability", field, source.String())
	}
	if !containsCapabilityID(context.requirements, source) {
		return invalidOutput("%s.source %q is not a current canonical requirement", field, source.String())
	}
	if _, exists := capability.Extension(namespace); !exists {
		return invalidOutput("%s.source %q has no extensions.%s metadata", field, source.String(), namespace)
	}
	return nil
}

type canonicalOutput struct {
	Requirements []canonicalOutputRequirement `json:"requirements"`
	Diagnostics  []canonicalOutputDiagnostic  `json:"diagnostics"`
}

type canonicalOutputRequirement struct {
	RuleID     string `json:"rule_id"`
	Namespace  string `json:"namespace"`
	Source     string `json:"source"`
	Capability string `json:"capability"`
}

type canonicalOutputDiagnostic struct {
	Code      string             `json:"code"`
	Severity  DiagnosticSeverity `json:"severity"`
	Message   string             `json:"message"`
	Namespace string             `json:"namespace"`
	Source    string             `json:"source"`
	RuleID    string             `json:"rule_id"`
}

func encodeOutput(requirements []Requirement, diagnostics []Diagnostic) ([]byte, error) {
	canonical := canonicalOutput{
		Requirements: make([]canonicalOutputRequirement, len(requirements)),
		Diagnostics:  make([]canonicalOutputDiagnostic, len(diagnostics)),
	}
	for index, requirement := range requirements {
		canonical.Requirements[index] = canonicalOutputRequirement{
			RuleID:     requirement.RuleID,
			Namespace:  requirement.Namespace,
			Source:     requirement.Source.String(),
			Capability: requirement.Capability.String(),
		}
	}
	for index, diagnostic := range diagnostics {
		canonical.Diagnostics[index] = canonicalOutputDiagnostic{
			Code:      diagnostic.Code,
			Severity:  diagnostic.Severity,
			Message:   diagnostic.Message,
			Namespace: diagnostic.Namespace,
			Source:    diagnostic.Source.String(),
			RuleID:    diagnostic.RuleID,
		}
	}
	return json.Marshal(canonical)
}

func requirementSortKey(requirement Requirement) string {
	return requirement.Namespace + "\x00" + requirement.Source.String() + "\x00" + requirement.RuleID + "\x00" + requirement.Capability.String()
}

func diagnosticSortKey(diagnostic Diagnostic) string {
	return diagnostic.Code + "\x00" + string(diagnostic.Severity) + "\x00" + diagnostic.Namespace + "\x00" + diagnostic.Source.String() + "\x00" + diagnostic.RuleID + "\x00" + diagnostic.Message
}

func validNamespace(value string) bool {
	return validLowerKebabSegment(value, 128)
}

func validStableID(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	segments := strings.Split(value, ".")
	for _, segment := range segments {
		if !validLowerKebabSegment(segment, 128) {
			return false
		}
	}
	return true
}

func validLowerKebabSegment(value string, maximum int) bool {
	if value == "" || len(value) > maximum || value[0] < 'a' || value[0] > 'z' {
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

func outputDigest(data []byte) string {
	return sha256Digest(data)
}

func invalidOutput(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOutput, fmt.Sprintf(format, arguments...))
}
