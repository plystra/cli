// Package commandschema defines immutable, validated command result,
// recovery, effect, and command-payload schemas.
package commandschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"unicode"
	"unicode/utf8"

	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
)

const ResultSchemaV1 = "plystra.result/v1"

// ErrResult reports an invalid shared command result.
var ErrResult = errors.New("build plystra.result")

// Status is the closed finite-command result vocabulary.
type Status string

const (
	StatusSuccess             Status = "success"
	StatusNoOp                Status = "no_op"
	StatusChanged             Status = "changed"
	StatusInvalidInvocation   Status = "invalid_invocation"
	StatusUnsupported         Status = "unsupported"
	StatusValidationFailed    Status = "validation_failed"
	StatusDecisionRequired    Status = "decision_required"
	StatusAuthorityRequired   Status = "authority_required"
	StatusPrerequisiteMissing Status = "prerequisite_missing"
	StatusCancelled           Status = "cancelled"
	StatusPartial             Status = "partial"
	StatusUncertainExternal   Status = "uncertain_external"
	StatusExecutionFailed     Status = "execution_failed"
)

type commandPayload interface {
	Valid() bool
	Schema() string
	CanonicalJSON() []byte
	commandPayload()
}

// ResultInput is the construction-only form of one finite command result.
// Snapshot, changes, support, and continuation remain explicitly empty in
// this first result-protocol slice rather than being omitted.
type ResultInput struct {
	Operation    string
	InvocationID string
	Status       Status
	Diagnostics  []diagnosticjson.Diagnostic
	Recovery     []Recovery
	Effects      Effects
	Payload      commandPayload
}

// Result is one immutable plystra.result/v1 command result.
type Result struct {
	operation     string
	invocationID  string
	status        Status
	exitClass     int
	diagnostics   []diagnosticjson.Diagnostic
	recovery      []Recovery
	effects       Effects
	payload       commandPayload
	canonicalJSON []byte
	prepared      bool
}

type resultDocument struct {
	Schema       string               `json:"schema"`
	Operation    string               `json:"operation"`
	InvocationID string               `json:"invocation_id"`
	Snapshot     any                  `json:"snapshot"`
	Status       Status               `json:"status"`
	ExitClass    int                  `json:"exit_class"`
	Changes      []emptyDocument      `json:"changes"`
	Diagnostics  []diagnosticDocument `json:"diagnostics"`
	Recovery     []recoveryDocument   `json:"recovery"`
	Effects      effectsDocument      `json:"effects"`
	Support      []emptyDocument      `json:"support"`
	Payload      json.RawMessage      `json:"payload"`
	Continuation any                  `json:"continuation"`
}

type emptyDocument struct{}

type diagnosticDocument struct {
	Code     string                  `json:"code"`
	Severity diagnosticjson.Severity `json:"severity"`
	Message  string                  `json:"message"`
}

// NewResult validates and constructs one complete command result.
func NewResult(input ResultInput) (Result, error) {
	if !validToken(input.Operation, 128) {
		return Result{}, fmt.Errorf("%w: operation is invalid", ErrResult)
	}
	if !validInvocationID(input.InvocationID) {
		return Result{}, fmt.Errorf("%w: invocation ID is not a bounded lowercase identifier", ErrResult)
	}
	exitClass, ok := ExitClass(input.Status)
	if !ok {
		return Result{}, fmt.Errorf("%w: status %q is not supported", ErrResult, input.Status)
	}
	diagnostics, err := normalizeResultDiagnostics(input.Diagnostics)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrResult, err)
	}
	recovery, err := normalizeResultRecovery(input.Recovery)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrResult, err)
	}
	if !input.Effects.Valid() {
		return Result{}, fmt.Errorf("%w: effects are invalid", ErrResult)
	}
	if input.Payload != nil && !input.Payload.Valid() {
		return Result{}, fmt.Errorf("%w: payload is invalid", ErrResult)
	}
	if exitClass != 0 && !hasErrorDiagnostic(diagnostics) {
		return Result{}, fmt.Errorf("%w: failure status requires one error diagnostic", ErrResult)
	}
	if input.Status == StatusChanged {
		if input.Payload == nil || len(input.Effects.Observed()) == 0 {
			return Result{}, fmt.Errorf("%w: changed status requires a payload and observed effect", ErrResult)
		}
		if len(input.Effects.Unverified()) != 0 {
			return Result{}, fmt.Errorf("%w: changed status cannot contain unverified effects", ErrResult)
		}
	}

	result := Result{
		operation:    input.Operation,
		invocationID: input.InvocationID,
		status:       input.Status,
		exitClass:    exitClass,
		diagnostics:  diagnostics,
		recovery:     recovery,
		effects:      input.Effects,
		payload:      input.Payload,
		prepared:     true,
	}
	canonical, err := json.Marshal(result.document())
	if err != nil {
		return Result{}, fmt.Errorf("%w: encode: %v", ErrResult, err)
	}
	result.canonicalJSON = canonical
	return result, nil
}

// Valid reports whether NewResult produced this result.
func (r Result) Valid() bool {
	if !r.prepared {
		return false
	}
	rebuilt, err := NewResult(ResultInput{
		Operation:    r.operation,
		InvocationID: r.invocationID,
		Status:       r.status,
		Diagnostics:  r.Diagnostics(),
		Recovery:     r.Recovery(),
		Effects:      r.effects,
		Payload:      r.payload,
	})
	return err == nil && bytes.Equal(rebuilt.canonicalJSON, r.canonicalJSON)
}

// ExitClass returns the documented process exit class for one status.
func ExitClass(status Status) (int, bool) {
	switch status {
	case StatusSuccess, StatusNoOp, StatusChanged:
		return 0, true
	case StatusInvalidInvocation, StatusUnsupported:
		return 2, true
	case StatusValidationFailed:
		return 3, true
	case StatusDecisionRequired, StatusAuthorityRequired, StatusPrerequisiteMissing:
		return 4, true
	case StatusCancelled:
		return 5, true
	case StatusPartial:
		return 6, true
	case StatusUncertainExternal:
		return 7, true
	case StatusExecutionFailed:
		return 8, true
	default:
		return 0, false
	}
}

// Operation returns the public command operation.
func (r Result) Operation() string { return r.operation }

// InvocationID returns the bounded lowercase invocation identity.
func (r Result) InvocationID() string { return r.invocationID }

// Status returns the closed result status.
func (r Result) Status() Status { return r.status }

// ExitClass returns the result's process exit class.
func (r Result) ExitClass() int { return r.exitClass }

// Diagnostics returns a defensive copy in canonical order.
func (r Result) Diagnostics() []diagnosticjson.Diagnostic {
	return append([]diagnosticjson.Diagnostic(nil), r.diagnostics...)
}

// Recovery returns a defensive copy in stable action-ID order.
func (r Result) Recovery() []Recovery { return append([]Recovery(nil), r.recovery...) }

// Effects returns immutable complete effect accounting.
func (r Result) Effects() Effects { return r.effects }

// CanonicalJSON returns a defensive copy of the complete result document.
func (r Result) CanonicalJSON() []byte { return append([]byte(nil), r.canonicalJSON...) }

func normalizeResultDiagnostics(input []diagnosticjson.Diagnostic) ([]diagnosticjson.Diagnostic, error) {
	if len(input) > 4_096 {
		return nil, errors.New("diagnostic count exceeds 4096")
	}
	result := append([]diagnosticjson.Diagnostic(nil), input...)
	for index, diagnostic := range result {
		if !diagnosticcode.Valid(diagnostic.Code) {
			return nil, fmt.Errorf("diagnostics[%d].code is invalid", index)
		}
		switch diagnostic.Severity {
		case diagnosticjson.SeverityInfo, diagnosticjson.SeverityWarning, diagnosticjson.SeverityError:
		default:
			return nil, fmt.Errorf("diagnostics[%d].severity is invalid", index)
		}
		if diagnostic.Message == "" || len(diagnostic.Message) > maximumMessageLength || !utf8.ValidString(diagnostic.Message) || bytes.IndexByte([]byte(diagnostic.Message), 0) >= 0 || containsControl(diagnostic.Message) {
			return nil, fmt.Errorf("diagnostics[%d].message is invalid", index)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return resultDiagnosticKey(result[left]) < resultDiagnosticKey(result[right])
	})
	for index := 1; index < len(result); index++ {
		if resultDiagnosticKey(result[index-1]) == resultDiagnosticKey(result[index]) {
			return nil, fmt.Errorf("diagnostics[%d] is duplicated", index)
		}
	}
	return result, nil
}

func normalizeResultRecovery(input []Recovery) ([]Recovery, error) {
	if len(input) > 1_024 {
		return nil, errors.New("recovery count exceeds 1024")
	}
	result := append([]Recovery(nil), input...)
	for index, action := range result {
		if !action.Valid() {
			return nil, fmt.Errorf("recovery[%d] is invalid", index)
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].ID() < result[right].ID() })
	for index := 1; index < len(result); index++ {
		if result[index-1].ID() == result[index].ID() {
			return nil, fmt.Errorf("recovery[%d].id is duplicated", index)
		}
	}
	return result, nil
}

func hasErrorDiagnostic(diagnostics []diagnosticjson.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == diagnosticjson.SeverityError {
			return true
		}
	}
	return false
}

func containsControl(value string) bool {
	return bytes.IndexFunc([]byte(value), func(character rune) bool { return unicode.IsControl(character) }) >= 0
}

func resultDiagnosticKey(value diagnosticjson.Diagnostic) string {
	return value.Code + "\x00" + string(value.Severity) + "\x00" + value.Message
}

func (r Result) document() resultDocument {
	payload := json.RawMessage("null")
	if r.payload != nil {
		payload = r.payload.CanonicalJSON()
	}
	diagnostics := make([]diagnosticDocument, len(r.diagnostics))
	for index, value := range r.diagnostics {
		diagnostics[index] = diagnosticDocument{Code: value.Code, Severity: value.Severity, Message: value.Message}
	}
	recovery := make([]recoveryDocument, len(r.recovery))
	for index, value := range r.recovery {
		recovery[index] = recoveryDocumentFrom(value.input)
	}
	return resultDocument{
		Schema:       ResultSchemaV1,
		Operation:    r.operation,
		InvocationID: r.invocationID,
		Snapshot:     nil,
		Status:       r.status,
		ExitClass:    r.exitClass,
		Changes:      make([]emptyDocument, 0),
		Diagnostics:  diagnostics,
		Recovery:     recovery,
		Effects:      effectsDocumentFrom(r.effects),
		Support:      make([]emptyDocument, 0),
		Payload:      payload,
		Continuation: nil,
	}
}
