package commandschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/plystra/cli/internal/modulepath"
)

const RecoverySchemaV1 = "plystra.recovery/v1"

// ErrRecovery reports an invalid typed recovery action.
var ErrRecovery = errors.New("build plystra.recovery action")

// RecoveryKind is the closed recovery-action vocabulary.
type RecoveryKind string

const (
	RecoveryExecute             RecoveryKind = "execute"
	RecoveryEditSource          RecoveryKind = "edit_source"
	RecoveryChoose              RecoveryKind = "choose"
	RecoverySatisfyPrerequisite RecoveryKind = "satisfy_prerequisite"
	RecoveryManual              RecoveryKind = "manual"
)

// RecoveryTarget identifies the exact subject of a recovery action.
type RecoveryTarget struct {
	Kind string
	ID   string
}

// Owner identifies a Project-owned result, recovery, or effect subject.
type Owner struct {
	Module string
	Path   string
}

// RecoverySource is one bounded project-relative provenance location.
type RecoverySource struct {
	Module string
	Path   string
	Kind   string
	Line   int
	Column int
}

// RecoverySelector identifies an optional selected Project view.
type RecoverySelector struct {
	Mode string
	Name string
	Path string
}

// RecoveryFact is one bounded typed precondition or intended effect.
type RecoveryFact struct {
	Kind  string
	Value string
	Count int
}

// Verification is one exact non-shell command used to verify an action.
type Verification struct {
	WorkingDirectory string
	Argv             []string
}

// RecoveryOption is one closed choice value and its optional fully bound
// public command.
type RecoveryOption struct {
	Value            string
	WorkingDirectory string
	Argv             []string
}

// RecoveryInput is the construction-only form of one recovery action.
type RecoveryInput struct {
	ID               string
	Kind             RecoveryKind
	Target           RecoveryTarget
	Owner            *Owner
	Provenance       []RecoverySource
	Selector         *RecoverySelector
	Preconditions    []RecoveryFact
	Effects          []RecoveryFact
	Verification     Verification
	WorkingDirectory string
	Argv             []string
	Options          []RecoveryOption
}

// Recovery is one immutable plystra.recovery/v1 action.
type Recovery struct {
	input         RecoveryInput
	canonicalJSON []byte
	prepared      bool
}

type recoveryDocument struct {
	Schema           string                    `json:"schema"`
	ID               string                    `json:"id"`
	Kind             RecoveryKind              `json:"kind"`
	Target           recoveryTargetDocument    `json:"target"`
	Owner            *recoveryOwnerDocument    `json:"owner"`
	Provenance       []recoverySourceDocument  `json:"provenance"`
	Selector         *recoverySelectorDocument `json:"selector"`
	Preconditions    []recoveryFactDocument    `json:"preconditions"`
	Effects          []recoveryFactDocument    `json:"effects"`
	Verification     verificationDocument      `json:"verification"`
	WorkingDirectory string                    `json:"working_directory,omitempty"`
	Argv             []string                  `json:"argv,omitempty"`
	Options          []recoveryOptionDocument  `json:"options,omitempty"`
}

type recoveryTargetDocument struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type recoveryOwnerDocument struct {
	Module string `json:"module"`
	Path   string `json:"path"`
}

type recoverySourceDocument struct {
	Module string `json:"module,omitempty"`
	Path   string `json:"path"`
	Kind   string `json:"kind,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

type recoverySelectorDocument struct {
	Mode string `json:"mode"`
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

type recoveryFactDocument struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
	Count int    `json:"count,omitempty"`
}

type verificationDocument struct {
	WorkingDirectory string   `json:"working_directory"`
	Argv             []string `json:"argv"`
}

type recoveryOptionDocument struct {
	Value            string   `json:"value"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
	Argv             []string `json:"argv,omitempty"`
}

// NewRecovery validates and constructs one typed recovery action.
func NewRecovery(input RecoveryInput) (Recovery, error) {
	normalized, err := normalizeRecovery(input)
	if err != nil {
		return Recovery{}, fmt.Errorf("%w: %v", ErrRecovery, err)
	}
	canonical, err := json.Marshal(recoveryDocumentFrom(normalized))
	if err != nil {
		return Recovery{}, fmt.Errorf("%w: encode: %v", ErrRecovery, err)
	}
	return Recovery{input: normalized, canonicalJSON: canonical, prepared: true}, nil
}

// Valid reports whether NewRecovery produced this action.
func (r Recovery) Valid() bool {
	if !r.prepared {
		return false
	}
	normalized, err := normalizeRecovery(r.input)
	if err != nil {
		return false
	}
	canonical, err := json.Marshal(recoveryDocumentFrom(normalized))
	return err == nil && bytes.Equal(canonical, r.canonicalJSON)
}

// ID returns the stable action identity.
func (r Recovery) ID() string { return r.input.ID }

// Kind returns the closed action kind.
func (r Recovery) Kind() RecoveryKind { return r.input.Kind }

// CanonicalJSON returns a defensive copy of the action document.
func (r Recovery) CanonicalJSON() []byte { return append([]byte(nil), r.canonicalJSON...) }

func normalizeRecovery(input RecoveryInput) (RecoveryInput, error) {
	if !validLowerKebab(input.ID, 128) {
		return RecoveryInput{}, errors.New("id must be canonical lower kebab case")
	}
	if !validRecoveryKind(input.Kind) {
		return RecoveryInput{}, fmt.Errorf("kind %q is not supported", input.Kind)
	}
	if !validToken(input.Target.Kind, 128) || !validSafeText(input.Target.ID, maximumIdentityLength) {
		return RecoveryInput{}, errors.New("target must contain a bounded kind and identity")
	}
	owner, err := normalizeRecoveryOwner(input.Owner)
	if err != nil {
		return RecoveryInput{}, err
	}
	provenance, err := normalizeRecoverySources(input.Provenance)
	if err != nil {
		return RecoveryInput{}, err
	}
	selector, err := normalizeRecoverySelector(input.Selector)
	if err != nil {
		return RecoveryInput{}, err
	}
	preconditions, err := normalizeRecoveryFacts("preconditions", input.Preconditions)
	if err != nil {
		return RecoveryInput{}, err
	}
	effects, err := normalizeRecoveryFacts("effects", input.Effects)
	if err != nil {
		return RecoveryInput{}, err
	}
	verification, err := normalizeVerification(input.Verification)
	if err != nil {
		return RecoveryInput{}, err
	}
	options, err := normalizeRecoveryOptions(input.Options)
	if err != nil {
		return RecoveryInput{}, err
	}

	workingDirectory := input.WorkingDirectory
	argv := cloneStrings(input.Argv)
	switch input.Kind {
	case RecoveryExecute:
		if !validRelativePath(workingDirectory, true) || !validArgumentVector(argv) {
			return RecoveryInput{}, errors.New("execute requires a safe working directory and fully bound argv")
		}
		if len(options) != 0 {
			return RecoveryInput{}, errors.New("execute cannot contain choice options")
		}
	case RecoveryChoose:
		if workingDirectory != "" || len(argv) != 0 || len(options) == 0 {
			return RecoveryInput{}, errors.New("choose requires options and cannot be directly executable")
		}
	default:
		if workingDirectory != "" || len(argv) != 0 || len(options) != 0 {
			return RecoveryInput{}, errors.New("non-executable recovery cannot contain argv or options")
		}
	}
	return RecoveryInput{
		ID:               input.ID,
		Kind:             input.Kind,
		Target:           input.Target,
		Owner:            owner,
		Provenance:       provenance,
		Selector:         selector,
		Preconditions:    preconditions,
		Effects:          effects,
		Verification:     verification,
		WorkingDirectory: workingDirectory,
		Argv:             argv,
		Options:          options,
	}, nil
}

func validRecoveryKind(kind RecoveryKind) bool {
	switch kind {
	case RecoveryExecute, RecoveryEditSource, RecoveryChoose, RecoverySatisfyPrerequisite, RecoveryManual:
		return true
	default:
		return false
	}
}

func normalizeRecoveryOwner(owner *Owner) (*Owner, error) {
	if owner == nil {
		return nil, nil
	}
	if modulepath.CheckProject(owner.Module) != nil || !validRelativePath(owner.Path, true) {
		return nil, errors.New("owner must identify a valid module and project-relative path")
	}
	copy := *owner
	return &copy, nil
}

func normalizeRecoverySources(input []RecoverySource) ([]RecoverySource, error) {
	if len(input) > 4_096 {
		return nil, errors.New("provenance count exceeds 4096")
	}
	result := append([]RecoverySource(nil), input...)
	for index, source := range result {
		if source.Module != "" && modulepath.CheckProject(source.Module) != nil {
			return nil, fmt.Errorf("provenance[%d].module is invalid", index)
		}
		if !validRelativePath(source.Path, false) || source.Kind != "" && !validToken(source.Kind, 128) || source.Line < 0 || source.Column < 0 || source.Column > 0 && source.Line == 0 {
			return nil, fmt.Errorf("provenance[%d] is invalid", index)
		}
	}
	sort.Slice(result, func(left, right int) bool { return recoverySourceKey(result[left]) < recoverySourceKey(result[right]) })
	for index := 1; index < len(result); index++ {
		if recoverySourceKey(result[index-1]) == recoverySourceKey(result[index]) {
			return nil, fmt.Errorf("provenance[%d] is duplicated", index)
		}
	}
	return result, nil
}

func normalizeRecoverySelector(selector *RecoverySelector) (*RecoverySelector, error) {
	if selector == nil {
		return nil, nil
	}
	if !validToken(selector.Mode, 128) || selector.Name != "" && !validSafeText(selector.Name, maximumIdentityLength) || selector.Path != "" && !validRelativePath(selector.Path, false) {
		return nil, errors.New("selector is invalid")
	}
	copy := *selector
	return &copy, nil
}

func normalizeRecoveryFacts(label string, input []RecoveryFact) ([]RecoveryFact, error) {
	if len(input) > 1_024 {
		return nil, fmt.Errorf("%s count exceeds 1024", label)
	}
	result := append([]RecoveryFact(nil), input...)
	for index, fact := range result {
		if !validToken(fact.Kind, 128) || fact.Value != "" && !validSafeText(fact.Value, maximumIdentityLength) || fact.Count < 0 {
			return nil, fmt.Errorf("%s[%d] is invalid", label, index)
		}
	}
	sort.Slice(result, func(left, right int) bool { return recoveryFactKey(result[left]) < recoveryFactKey(result[right]) })
	return result, nil
}

func normalizeVerification(input Verification) (Verification, error) {
	if !validRelativePath(input.WorkingDirectory, true) || !validArgumentVector(input.Argv) {
		return Verification{}, errors.New("verification requires a safe working directory and fully bound argv")
	}
	return Verification{WorkingDirectory: input.WorkingDirectory, Argv: cloneStrings(input.Argv)}, nil
}

func normalizeRecoveryOptions(input []RecoveryOption) ([]RecoveryOption, error) {
	if len(input) > 256 {
		return nil, errors.New("option count exceeds 256")
	}
	result := make([]RecoveryOption, len(input))
	for index, option := range input {
		if !validSafeText(option.Value, maximumIdentityLength) {
			return nil, fmt.Errorf("options[%d].value is invalid", index)
		}
		hasCommand := option.WorkingDirectory != "" || len(option.Argv) != 0
		if hasCommand && (!validRelativePath(option.WorkingDirectory, true) || !validArgumentVector(option.Argv)) {
			return nil, fmt.Errorf("options[%d] command is not fully bound", index)
		}
		result[index] = RecoveryOption{Value: option.Value, WorkingDirectory: option.WorkingDirectory, Argv: cloneStrings(option.Argv)}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Value < result[right].Value })
	for index := 1; index < len(result); index++ {
		if result[index-1].Value == result[index].Value {
			return nil, fmt.Errorf("options[%d].value is duplicated", index)
		}
	}
	return result, nil
}

func recoveryDocumentFrom(input RecoveryInput) recoveryDocument {
	return recoveryDocument{
		Schema:           RecoverySchemaV1,
		ID:               input.ID,
		Kind:             input.Kind,
		Target:           recoveryTargetDocument(input.Target),
		Owner:            recoveryOwnerDocumentFrom(input.Owner),
		Provenance:       recoverySourceDocuments(input.Provenance),
		Selector:         recoverySelectorDocumentFrom(input.Selector),
		Preconditions:    recoveryFactDocuments(input.Preconditions),
		Effects:          recoveryFactDocuments(input.Effects),
		Verification:     verificationDocument{WorkingDirectory: input.Verification.WorkingDirectory, Argv: cloneStrings(input.Verification.Argv)},
		WorkingDirectory: input.WorkingDirectory,
		Argv:             cloneStrings(input.Argv),
		Options:          recoveryOptionDocuments(input.Options),
	}
}

func recoveryOwnerDocumentFrom(owner *Owner) *recoveryOwnerDocument {
	if owner == nil {
		return nil
	}
	return &recoveryOwnerDocument{Module: owner.Module, Path: owner.Path}
}

func recoverySelectorDocumentFrom(selector *RecoverySelector) *recoverySelectorDocument {
	if selector == nil {
		return nil
	}
	return &recoverySelectorDocument{Mode: selector.Mode, Name: selector.Name, Path: selector.Path}
}

func recoverySourceDocuments(values []RecoverySource) []recoverySourceDocument {
	result := make([]recoverySourceDocument, len(values))
	for index, value := range values {
		result[index] = recoverySourceDocument(value)
	}
	return result
}

func recoveryFactDocuments(values []RecoveryFact) []recoveryFactDocument {
	result := make([]recoveryFactDocument, len(values))
	for index, value := range values {
		result[index] = recoveryFactDocument(value)
	}
	return result
}

func recoveryOptionDocuments(values []RecoveryOption) []recoveryOptionDocument {
	if len(values) == 0 {
		return nil
	}
	result := make([]recoveryOptionDocument, len(values))
	for index, value := range values {
		result[index] = recoveryOptionDocument{Value: value.Value, WorkingDirectory: value.WorkingDirectory, Argv: cloneStrings(value.Argv)}
	}
	return result
}

func recoverySourceKey(value RecoverySource) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%010d\x00%010d", value.Module, value.Path, value.Kind, value.Line, value.Column)
}

func recoveryFactKey(value RecoveryFact) string {
	return fmt.Sprintf("%s\x00%s\x00%010d", value.Kind, value.Value, value.Count)
}
