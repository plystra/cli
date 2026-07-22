// Package diagnosticjson defines the shared deterministic JSON envelope used
// by CLI diagnostic schemas. Command-specific result schemas remain owned and
// versioned by their commands.
package diagnosticjson

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/modulepath"
)

const (
	maximumEnvelopeSize  = 1 << 20
	maximumJSONDepth     = 64
	maximumJSONNodes     = 65_536
	maximumDiagnostics   = 4_096
	maximumSources       = 16_384
	maximumMessageLength = 4_096
)

// ErrInvalid reports an incomplete, unsafe, or non-canonical diagnostic
// envelope input.
var ErrInvalid = errors.New("invalid diagnostic JSON envelope")

// Severity is the closed shared diagnostic severity vocabulary.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Diagnostic is one bounded user-facing finding. Recovery detail and deeper
// command-specific evidence belong to the independently versioned result
// schema rather than this shared record.
type Diagnostic struct {
	Code     string
	Severity Severity
	Message  string
}

// Source is one stable module-relative declaration reference. A zero line and
// column mean that an exact source location is unavailable.
type Source struct {
	Module string
	Path   string
	Kind   string
	Line   int
	Column int
}

// Input is the construction-only form of one shared envelope. Result must be
// one JSON object belonging to the schema named by Schema and SchemaVersion.
// An omitted Result is normalized to an empty object.
type Input struct {
	Schema                 string
	SchemaVersion          uint32
	ConfigurationMode      generation.ConfigurationMode
	ApplicationModelDigest string
	Diagnostics            []Diagnostic
	Sources                []Source
	Result                 json.RawMessage
}

// Envelope is an immutable, bounded, deterministic shared diagnostic result.
type Envelope struct {
	schema                 string
	schemaVersion          uint32
	configurationMode      generation.ConfigurationMode
	applicationModelDigest string
	diagnostics            []Diagnostic
	sources                []Source
	resultJSON             []byte
	canonicalJSON          []byte
	digest                 string
	prepared               bool
}

type canonicalEnvelope struct {
	Schema                 string                       `json:"schema"`
	SchemaVersion          uint32                       `json:"schema_version"`
	ConfigurationMode      generation.ConfigurationMode `json:"configuration_mode"`
	ApplicationModelDigest string                       `json:"application_model_digest"`
	Diagnostics            []canonicalDiagnostic        `json:"diagnostics"`
	Sources                []canonicalSource            `json:"sources"`
	Result                 json.RawMessage              `json:"result"`
}

type canonicalDiagnostic struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

type canonicalSource struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// New validates and canonicalizes one shared diagnostic envelope.
func New(input Input) (Envelope, error) {
	if err := validateIdentity(input); err != nil {
		return Envelope{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	diagnostics, err := normalizeDiagnostics(input.Diagnostics)
	if err != nil {
		return Envelope{}, fmt.Errorf("%w: diagnostics: %v", ErrInvalid, err)
	}
	sources, err := normalizeSources(input.Sources)
	if err != nil {
		return Envelope{}, fmt.Errorf("%w: sources: %v", ErrInvalid, err)
	}
	result, err := normalizeResult(input.Result)
	if err != nil {
		return Envelope{}, fmt.Errorf("%w: result: %v", ErrInvalid, err)
	}
	canonical, err := encode(input.Schema, input.SchemaVersion, input.ConfigurationMode, input.ApplicationModelDigest, diagnostics, sources, result)
	if err != nil {
		return Envelope{}, fmt.Errorf("%w: encode: %v", ErrInvalid, err)
	}
	if len(canonical) > maximumEnvelopeSize {
		return Envelope{}, fmt.Errorf("%w: canonical envelope exceeds %d bytes", ErrInvalid, maximumEnvelopeSize)
	}
	return Envelope{
		schema:                 input.Schema,
		schemaVersion:          input.SchemaVersion,
		configurationMode:      input.ConfigurationMode,
		applicationModelDigest: input.ApplicationModelDigest,
		diagnostics:            diagnostics,
		sources:                sources,
		resultJSON:             result,
		canonicalJSON:          canonical,
		digest:                 digest(canonical),
		prepared:               true,
	}, nil
}

// Valid reports whether New produced this internally consistent envelope.
func (e Envelope) Valid() bool {
	if !e.prepared {
		return false
	}
	input := Input{
		Schema:                 e.schema,
		SchemaVersion:          e.schemaVersion,
		ConfigurationMode:      e.configurationMode,
		ApplicationModelDigest: e.applicationModelDigest,
		Diagnostics:            e.Diagnostics(),
		Sources:                e.Sources(),
		Result:                 e.ResultJSON(),
	}
	if validateIdentity(input) != nil {
		return false
	}
	diagnostics, err := normalizeDiagnostics(input.Diagnostics)
	if err != nil || !equalDiagnostics(e.diagnostics, diagnostics) {
		return false
	}
	sources, err := normalizeSources(input.Sources)
	if err != nil || !equalSources(e.sources, sources) {
		return false
	}
	result, err := normalizeResult(input.Result)
	if err != nil || !bytes.Equal(e.resultJSON, result) {
		return false
	}
	canonical, err := encode(e.schema, e.schemaVersion, e.configurationMode, e.applicationModelDigest, diagnostics, sources, result)
	return err == nil && bytes.Equal(e.canonicalJSON, canonical) && e.digest == digest(canonical)
}

// Schema returns the command-specific schema identity without its version.
func (e Envelope) Schema() string { return e.schema }

// SchemaVersion returns the command-specific schema version.
func (e Envelope) SchemaVersion() uint32 { return e.schemaVersion }

// ConfigurationMode returns default, environment, or explicit-config.
func (e Envelope) ConfigurationMode() generation.ConfigurationMode {
	return e.configurationMode
}

// ApplicationModelDigest returns the final build-affecting application-model
// identity used to render this diagnostic result.
func (e Envelope) ApplicationModelDigest() string { return e.applicationModelDigest }

// Diagnostics returns a defensive copy in canonical order.
func (e Envelope) Diagnostics() []Diagnostic {
	return append([]Diagnostic(nil), e.diagnostics...)
}

// Sources returns a defensive copy in canonical module-relative order.
func (e Envelope) Sources() []Source { return append([]Source(nil), e.sources...) }

// ResultJSON returns a defensive copy of the canonical command-specific
// result object.
func (e Envelope) ResultJSON() []byte { return append([]byte(nil), e.resultJSON...) }

// CanonicalJSON returns a defensive copy of the complete deterministic JSON
// envelope.
func (e Envelope) CanonicalJSON() []byte {
	return append([]byte(nil), e.canonicalJSON...)
}

// Digest returns the lowercase SHA-256 identity of CanonicalJSON.
func (e Envelope) Digest() string { return e.digest }

func validateIdentity(input Input) error {
	if !validSchema(input.Schema) {
		return fmt.Errorf("schema %q is not a stable plystra lower-kebab identity", input.Schema)
	}
	if input.SchemaVersion == 0 || input.SchemaVersion > math.MaxInt32 {
		return errors.New("schema version must be between 1 and 2147483647")
	}
	switch input.ConfigurationMode {
	case generation.ConfigurationModeDefault, generation.ConfigurationModeEnvironment, generation.ConfigurationModeExplicit:
	default:
		return fmt.Errorf("configuration mode %q is not supported", input.ConfigurationMode)
	}
	if !validDigest(input.ApplicationModelDigest) {
		return errors.New("application-model digest is not a canonical SHA-256 digest")
	}
	return nil
}

func normalizeDiagnostics(input []Diagnostic) ([]Diagnostic, error) {
	if len(input) > maximumDiagnostics {
		return nil, fmt.Errorf("count exceeds %d", maximumDiagnostics)
	}
	diagnostics := append([]Diagnostic(nil), input...)
	for index, diagnostic := range diagnostics {
		if !validDiagnosticCode(diagnostic.Code) {
			return nil, fmt.Errorf("diagnostics[%d].code %q is not a stable PLYSTRA diagnostic code", index, diagnostic.Code)
		}
		switch diagnostic.Severity {
		case SeverityInfo, SeverityWarning, SeverityError:
		default:
			return nil, fmt.Errorf("diagnostics[%d].severity %q is not supported", index, diagnostic.Severity)
		}
		if diagnostic.Message == "" || len(diagnostic.Message) > maximumMessageLength || !utf8.ValidString(diagnostic.Message) || strings.ContainsRune(diagnostic.Message, '\x00') {
			return nil, fmt.Errorf("diagnostics[%d].message must be non-empty valid UTF-8, at most %d bytes, and contain no NUL", index, maximumMessageLength)
		}
	}
	sort.Slice(diagnostics, func(left, right int) bool {
		return diagnosticKey(diagnostics[left]) < diagnosticKey(diagnostics[right])
	})
	for index := 1; index < len(diagnostics); index++ {
		if diagnosticKey(diagnostics[index-1]) == diagnosticKey(diagnostics[index]) {
			return nil, fmt.Errorf("diagnostics[%d] duplicates diagnostic %q", index, diagnostics[index].Code)
		}
	}
	return diagnostics, nil
}

func normalizeSources(input []Source) ([]Source, error) {
	if len(input) > maximumSources {
		return nil, fmt.Errorf("count exceeds %d", maximumSources)
	}
	sources := append([]Source(nil), input...)
	for index, source := range sources {
		if err := modulepath.CheckProject(source.Module); err != nil {
			return nil, fmt.Errorf("sources[%d].module %q is not a valid Project module identity: %v", index, source.Module, err)
		}
		if !validRelativePath(source.Path) {
			return nil, fmt.Errorf("sources[%d].path %q is not a stable module-relative slash path", index, source.Path)
		}
		if !validLowerKebab(source.Kind, 128) {
			return nil, fmt.Errorf("sources[%d].kind %q is not canonical lower kebab case", index, source.Kind)
		}
		if source.Line < 0 || source.Line > math.MaxInt32 {
			return nil, fmt.Errorf("sources[%d].line is outside the supported range", index)
		}
		if source.Column < 0 || source.Column > math.MaxInt32 || source.Column > 0 && source.Line == 0 {
			return nil, fmt.Errorf("sources[%d].column is outside the supported location", index)
		}
	}
	sort.Slice(sources, func(left, right int) bool {
		return sourceKey(sources[left]) < sourceKey(sources[right])
	})
	for index := 1; index < len(sources); index++ {
		if sourceKey(sources[index-1]) == sourceKey(sources[index]) {
			return nil, fmt.Errorf("sources[%d] duplicates %s:%s", index, sources[index].Module, sources[index].Path)
		}
	}
	return sources, nil
}

func normalizeResult(data []byte) ([]byte, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return []byte("{}"), nil
	}
	if len(data) > maximumEnvelopeSize {
		return nil, fmt.Errorf("JSON exceeds %d bytes", maximumEnvelopeSize)
	}
	if !utf8.Valid(data) {
		return nil, errors.New("JSON is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	nodes := 0
	value, err := decodeJSONValue(decoder, 1, &nodes)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values are not allowed")
		}
		return nil, fmt.Errorf("decode trailing JSON: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("must be a JSON object")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return canonical, nil
}

func decodeJSONValue(decoder *json.Decoder, depth int, nodes *int) (any, error) {
	if depth > maximumJSONDepth {
		return nil, fmt.Errorf("JSON exceeds maximum depth %d", maximumJSONDepth)
	}
	(*nodes)++
	if *nodes > maximumJSONNodes {
		return nil, fmt.Errorf("JSON exceeds maximum node count %d", maximumJSONNodes)
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	switch token := token.(type) {
	case json.Delim:
		switch token {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, fmt.Errorf("decode object key: %w", err)
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("object key must be a string")
				}
				if _, duplicate := object[key]; duplicate {
					return nil, fmt.Errorf("object contains duplicate key %q", key)
				}
				value, err := decodeJSONValue(decoder, depth+1, nodes)
				if err != nil {
					return nil, fmt.Errorf("object key %q: %w", key, err)
				}
				object[key] = value
			}
			if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
				return nil, errors.New("object is not closed")
			}
			return object, nil
		case '[':
			array := make([]any, 0)
			for decoder.More() {
				value, err := decodeJSONValue(decoder, depth+1, nodes)
				if err != nil {
					return nil, fmt.Errorf("array item %d: %w", len(array), err)
				}
				array = append(array, value)
			}
			if closing, err := decoder.Token(); err != nil || closing != json.Delim(']') {
				return nil, errors.New("array is not closed")
			}
			return array, nil
		default:
			return nil, fmt.Errorf("unexpected JSON delimiter %q", token)
		}
	case json.Number:
		return normalizeJSONNumber(token)
	case string, bool, nil:
		return token, nil
	default:
		return nil, fmt.Errorf("unsupported JSON token %T", token)
	}
}

func normalizeJSONNumber(number json.Number) (any, error) {
	text := number.String()
	if !strings.ContainsAny(text, ".eE") {
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, errors.New("integer is outside the signed 64-bit range")
		}
		return value, nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return nil, errors.New("number must be finite and representable as float64")
	}
	if value == 0 {
		return float64(0), nil
	}
	return value, nil
}

func encode(schema string, schemaVersion uint32, mode generation.ConfigurationMode, applicationModelDigest string, diagnostics []Diagnostic, sources []Source, result []byte) ([]byte, error) {
	canonicalDiagnostics := make([]canonicalDiagnostic, len(diagnostics))
	for index, diagnostic := range diagnostics {
		canonicalDiagnostics[index] = canonicalDiagnostic(diagnostic)
	}
	canonicalSources := make([]canonicalSource, len(sources))
	for index, source := range sources {
		canonicalSources[index] = canonicalSource(source)
	}
	return json.Marshal(canonicalEnvelope{
		Schema:                 schema,
		SchemaVersion:          schemaVersion,
		ConfigurationMode:      mode,
		ApplicationModelDigest: applicationModelDigest,
		Diagnostics:            canonicalDiagnostics,
		Sources:                canonicalSources,
		Result:                 json.RawMessage(result),
	})
}

func validSchema(value string) bool {
	if len(value) > 256 || !strings.HasPrefix(value, "plystra.") {
		return false
	}
	segments := strings.Split(value, ".")
	if len(segments) < 2 {
		return false
	}
	for _, segment := range segments {
		if !validLowerKebab(segment, 128) {
			return false
		}
	}
	return true
}

func validDiagnosticCode(value string) bool {
	if len(value) <= len("PLYSTRA-") || len(value) > 128 || !strings.HasPrefix(value, "PLYSTRA-") {
		return false
	}
	previousHyphen := true
	for index := len("PLYSTRA-"); index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'A' && character <= 'Z', character >= '0' && character <= '9':
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}

func validLowerKebab(value string, maximum int) bool {
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

func validRelativePath(value string) bool {
	if value == "" || len(value) > 1_024 || !utf8.ValidString(value) || path.IsAbs(value) || path.Clean(value) != value || value == "." || value == ".." || strings.HasPrefix(value, "../") || strings.Contains(value, "\\") || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	if len(value) >= 2 && value[1] == ':' && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) {
		return false
	}
	return true
}

func diagnosticKey(value Diagnostic) string {
	return value.Code + "\x00" + string(value.Severity) + "\x00" + value.Message
}

func sourceKey(value Source) string {
	return strings.Join([]string{
		value.Module,
		value.Path,
		value.Kind,
		fmt.Sprintf("%010d", value.Line),
		fmt.Sprintf("%010d", value.Column),
	}, "\x00")
}

func equalDiagnostics(left, right []Diagnostic) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalSources(left, right []Source) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
