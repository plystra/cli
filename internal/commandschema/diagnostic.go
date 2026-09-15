package commandschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
)

// ErrDiagnostic reports an invalid result diagnostic.
var ErrDiagnostic = errors.New("build Plystra result diagnostic")

// DiagnosticInput is the construction-only form of one result diagnostic.
type DiagnosticInput struct {
	Code      string
	Severity  diagnosticjson.Severity
	Message   string
	Locations []diagnosticjson.Source
}

// Diagnostic is one immutable, source-bearing result diagnostic.
type Diagnostic struct {
	code          string
	severity      diagnosticjson.Severity
	message       string
	locations     []diagnosticjson.Source
	canonicalJSON []byte
	prepared      bool
}

type diagnosticDocument struct {
	Code      string                       `json:"code"`
	Severity  diagnosticjson.Severity      `json:"severity"`
	Message   string                       `json:"message"`
	Locations []diagnosticLocationDocument `json:"locations"`
}

type diagnosticLocationDocument struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// NewDiagnostic validates and constructs one result diagnostic.
func NewDiagnostic(input DiagnosticInput) (Diagnostic, error) {
	locations, err := normalizeDiagnosticInput(input)
	if err != nil {
		return Diagnostic{}, fmt.Errorf("%w: %v", ErrDiagnostic, err)
	}
	diagnostic := Diagnostic{
		code:      input.Code,
		severity:  input.Severity,
		message:   input.Message,
		locations: locations,
		prepared:  true,
	}
	canonical, err := json.Marshal(diagnostic.document())
	if err != nil {
		return Diagnostic{}, fmt.Errorf("%w: encode: %v", ErrDiagnostic, err)
	}
	diagnostic.canonicalJSON = canonical
	return diagnostic, nil
}

// Valid reports whether NewDiagnostic produced this diagnostic.
func (d Diagnostic) Valid() bool {
	if !d.prepared {
		return false
	}
	rebuilt, err := NewDiagnostic(DiagnosticInput{
		Code:      d.code,
		Severity:  d.severity,
		Message:   d.message,
		Locations: d.Locations(),
	})
	return err == nil && bytes.Equal(rebuilt.canonicalJSON, d.canonicalJSON)
}

// Code returns the stable diagnostic identifier.
func (d Diagnostic) Code() string { return d.code }

// Severity returns info, warning, or error.
func (d Diagnostic) Severity() diagnosticjson.Severity { return d.severity }

// Message returns the bounded public diagnostic message.
func (d Diagnostic) Message() string { return d.message }

// Locations returns a defensive copy in canonical source order.
func (d Diagnostic) Locations() []diagnosticjson.Source {
	return append([]diagnosticjson.Source(nil), d.locations...)
}

// CanonicalJSON returns a defensive copy of the diagnostic document.
func (d Diagnostic) CanonicalJSON() []byte {
	return append([]byte(nil), d.canonicalJSON...)
}

func normalizeDiagnosticInput(input DiagnosticInput) ([]diagnosticjson.Source, error) {
	if !diagnosticcode.Valid(input.Code) {
		return nil, errors.New("code is invalid")
	}
	switch input.Severity {
	case diagnosticjson.SeverityInfo, diagnosticjson.SeverityWarning, diagnosticjson.SeverityError:
	default:
		return nil, errors.New("severity is invalid")
	}
	if !validSafeText(input.Message, maximumMessageLength) {
		return nil, errors.New("message is invalid")
	}
	locations, err := diagnosticjson.CanonicalizeSources(input.Locations)
	if err != nil {
		return nil, fmt.Errorf("locations: %v", err)
	}
	return locations, nil
}

func (d Diagnostic) document() diagnosticDocument {
	return diagnosticDocument{
		Code:      d.code,
		Severity:  d.severity,
		Message:   d.message,
		Locations: diagnosticLocationDocuments(d.locations),
	}
}

func diagnosticDocuments(values []Diagnostic) []diagnosticDocument {
	result := make([]diagnosticDocument, len(values))
	for index, value := range values {
		result[index] = value.document()
	}
	return result
}

func diagnosticLocationDocuments(values []diagnosticjson.Source) []diagnosticLocationDocument {
	result := make([]diagnosticLocationDocument, len(values))
	for index, value := range values {
		result[index] = diagnosticLocationDocument{
			Module: value.Module,
			Path:   value.Path,
			Kind:   value.Kind,
			Line:   value.Line,
			Column: value.Column,
		}
	}
	return result
}
