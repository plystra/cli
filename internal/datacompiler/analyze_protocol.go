package datacompiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	analyzePhase        = "analyze"
	analyzeStatusOK     = "succeeded"
	analyzeStatusReject = "rejected"
	analyzeStatusFail   = "failed"
)

var (
	// ErrAnalyzeRequest identifies a request that cannot be sent to the
	// selected compiler.
	ErrAnalyzeRequest = errors.New("invalid Data analyze request")
	// ErrAnalyzeResponse identifies a response that cannot be trusted.
	ErrAnalyzeResponse = errors.New("invalid Data analyze response")
	// ErrAnalyzeIdentity identifies a response that does not belong to the
	// request sent to the selected compiler.
	ErrAnalyzeIdentity = errors.New("Data analyze response identity mismatch")
	// ErrAnalyzeFrameTooLarge identifies a frame outside the manifest bound.
	ErrAnalyzeFrameTooLarge = errors.New("Data analyze frame exceeds the limit")
)

type analyzeCompilerIdentity struct {
	ModulePath          string `json:"module_path"`
	ModuleVersion       string `json:"module_version"`
	ModuleChecksum      string `json:"module_checksum"`
	ManifestDigest      string `json:"manifest_digest"`
	CommandImportPath   string `json:"command_import_path"`
	AnalyzeProtocol     string `json:"analyze_protocol"`
	EmitProtocol        string `json:"emit_protocol"`
	DeclarationLanguage string `json:"declaration_language"`
	GoToolchain         string `json:"go_toolchain"`
}

type analyzeBuildContext struct {
	GOOS      string   `json:"goos"`
	GOARCH    string   `json:"goarch"`
	BuildTags []string `json:"build_tags,omitempty"`
}

// AnalyzeBuildContext identifies the Go context used to type-check one
// immutable Data source snapshot.
type AnalyzeBuildContext struct {
	GOOS      string
	GOARCH    string
	BuildTags []string
}

// AnalyzeRequestOptions supplies the finite source inventory for one analyze
// request. Snapshot is a JSON object with packages and resources; compiler
// limits are added from the selected distribution manifest.
type AnalyzeRequestOptions struct {
	RequestID string
	Build     AnalyzeBuildContext
	Snapshot  json.RawMessage
}

type analyzeLimits struct {
	MaxRoots           int `json:"max_roots"`
	MaxNodes           int `json:"max_nodes"`
	MaxImports         int `json:"max_imports"`
	MaxNesting         int `json:"max_nesting"`
	MaxSymbolBytes     int `json:"max_symbol_bytes"`
	MaxDiagnostics     int `json:"max_diagnostics"`
	MaxDiagnosticBytes int `json:"max_diagnostic_bytes"`
}

type analyzeSnapshotEnvelope struct {
	Packages  json.RawMessage `json:"packages"`
	Resources json.RawMessage `json:"resources"`
	Limits    analyzeLimits   `json:"limits"`
}

type analyzeSnapshotPackage struct {
	ImportPath      string          `json:"import_path"`
	RootEligibility string          `json:"root_eligibility"`
	Files           json.RawMessage `json:"files"`
}

type analyzeRequestEnvelope struct {
	Schema      string                  `json:"schema"`
	Phase       string                  `json:"phase"`
	RequestID   string                  `json:"request_id"`
	Compiler    analyzeCompilerIdentity `json:"compiler"`
	Build       analyzeBuildContext     `json:"build"`
	Limits      analyzeLimits           `json:"limits"`
	InputDigest string                  `json:"input_digest"`
	Snapshot    json.RawMessage         `json:"snapshot"`
	effective   analyzeLimits           `json:"-"`
}

type analyzeResponseEnvelope struct {
	Schema       string          `json:"schema"`
	Phase        string          `json:"phase"`
	RequestID    string          `json:"request_id"`
	InputDigest  string          `json:"input_digest"`
	Status       string          `json:"status"`
	OutputDigest string          `json:"output_digest,omitempty"`
	Output       json.RawMessage `json:"output"`
	Diagnostics  json.RawMessage `json:"diagnostics"`
	Truncated    bool            `json:"truncated"`
}

type analyzeOutputEnvelope struct {
	Roots     json.RawMessage `json:"roots"`
	Digest    string          `json:"digest"`
	Truncated bool            `json:"truncated"`
	Valid     bool            `json:"valid"`
}

// AnalyzeResponse is the independently validated wire result from the Data
// compiler. Output and Diagnostics remain JSON values so CLI does not import
// the independent Data module.
type AnalyzeResponse struct {
	Schema       string
	Phase        string
	RequestID    string
	InputDigest  string
	Status       string
	OutputDigest string
	Output       json.RawMessage
	Diagnostics  []json.RawMessage
	Truncated    bool
}

// BuildAnalyzeRequest creates one canonical request payload for the selected
// compiler without importing the independent Data module.
func BuildAnalyzeRequest(artifact Artifact, manifest Manifest, options AnalyzeRequestOptions) ([]byte, error) {
	if artifact.ModulePath != ModulePath || artifact.ModuleVersion == "" || artifact.ManifestDigest == "" || options.RequestID == "" || options.Build.GOOS == "" || options.Build.GOARCH == "" {
		return nil, fmt.Errorf("%w: compiler identity, request ID, or build context is incomplete", ErrAnalyzeRequest)
	}
	var source struct {
		Packages  json.RawMessage `json:"packages"`
		Resources json.RawMessage `json:"resources"`
	}
	if err := decodeStrictJSON(options.Snapshot, &source); err != nil || len(source.Packages) == 0 || len(source.Resources) == 0 {
		return nil, fmt.Errorf("%w: source snapshot is incomplete", ErrAnalyzeRequest)
	}
	limits := analyzeLimits{
		MaxRoots: manifest.Bounds.MaxRoots, MaxNodes: manifest.Bounds.MaxNodes,
		MaxImports: manifest.Bounds.MaxImports, MaxNesting: manifest.Bounds.MaxNesting,
		MaxSymbolBytes: manifest.Bounds.MaxSymbolBytes, MaxDiagnostics: manifest.Bounds.MaxDiagnostics,
		MaxDiagnosticBytes: manifest.Bounds.MaxDiagnosticBytes,
	}
	snapshot, err := json.Marshal(struct {
		Packages  json.RawMessage `json:"packages"`
		Resources json.RawMessage `json:"resources"`
		Limits    analyzeLimits   `json:"limits"`
	}{source.Packages, source.Resources, limits})
	if err != nil {
		return nil, fmt.Errorf("%w: encode source snapshot: %v", ErrAnalyzeRequest, err)
	}
	inputDigest, err := AnalyzeInputDigest(snapshot, options.Build)
	if err != nil {
		return nil, err
	}
	request := struct {
		Schema      string                  `json:"schema"`
		Phase       string                  `json:"phase"`
		RequestID   string                  `json:"request_id"`
		Compiler    analyzeCompilerIdentity `json:"compiler"`
		Build       analyzeBuildContext     `json:"build"`
		Limits      analyzeLimits           `json:"limits"`
		InputDigest string                  `json:"input_digest"`
		Snapshot    json.RawMessage         `json:"snapshot"`
	}{
		Schema: AnalyzeSchema, Phase: analyzePhase, RequestID: options.RequestID,
		Compiler: analyzeCompilerIdentity{ModulePath: artifact.ModulePath, ModuleVersion: artifact.ModuleVersion, ModuleChecksum: artifact.ModuleChecksum, ManifestDigest: artifact.ManifestDigest, CommandImportPath: CommandImportPath, AnalyzeProtocol: AnalyzeSchema, EmitProtocol: EmitSchema, DeclarationLanguage: DeclarationLanguage, GoToolchain: artifact.GoToolchain},
		Build:    analyzeBuildContext{GOOS: options.Build.GOOS, GOARCH: options.Build.GOARCH, BuildTags: append([]string(nil), options.Build.BuildTags...)},
		Limits:   limits, InputDigest: inputDigest, Snapshot: snapshot,
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("%w: encode request: %v", ErrAnalyzeRequest, err)
	}
	if len(data) > manifest.Bounds.MaxFrameBytes {
		return nil, fmt.Errorf("%w: request exceeds the compiler frame limit", ErrAnalyzeRequest)
	}
	return data, nil
}

func validateAnalyzeRequest(data []byte, artifact Artifact) (analyzeRequestEnvelope, error) {
	var request analyzeRequestEnvelope
	if err := decodeStrictJSON(data, &request); err != nil {
		return analyzeRequestEnvelope{}, fmt.Errorf("%w: malformed request", ErrAnalyzeRequest)
	}
	if request.Schema != AnalyzeSchema || request.Phase != analyzePhase || strings.TrimSpace(request.RequestID) == "" || !validDigest(request.InputDigest) || len(request.Snapshot) == 0 {
		return analyzeRequestEnvelope{}, fmt.Errorf("%w: schema, identity, limits, or snapshot is incomplete", ErrAnalyzeRequest)
	}
	var snapshot analyzeSnapshotEnvelope
	if err := decodeStrictJSON(request.Snapshot, &snapshot); err != nil || snapshot.Limits != request.Limits || len(snapshot.Packages) == 0 || len(snapshot.Resources) == 0 {
		return analyzeRequestEnvelope{}, fmt.Errorf("%w: snapshot and declared limits differ or are malformed", ErrAnalyzeRequest)
	}
	if err := validateAnalyzePackages(snapshot.Packages); err != nil {
		return analyzeRequestEnvelope{}, fmt.Errorf("%w: package inventory is malformed", ErrAnalyzeRequest)
	}
	if err := validateAnalyzeArray(snapshot.Resources); err != nil {
		return analyzeRequestEnvelope{}, fmt.Errorf("%w: Resource inventory is malformed", ErrAnalyzeRequest)
	}
	effective, ok := effectiveAnalyzeLimits(request.Limits)
	if !ok {
		return analyzeRequestEnvelope{}, fmt.Errorf("%w: declared limits exceed the compiler manifest", ErrAnalyzeRequest)
	}
	request.effective = effective
	expectedInput, err := AnalyzeInputDigest(request.Snapshot, AnalyzeBuildContext{
		GOOS: request.Build.GOOS, GOARCH: request.Build.GOARCH, BuildTags: request.Build.BuildTags,
	})
	if err != nil || request.InputDigest != expectedInput {
		return analyzeRequestEnvelope{}, fmt.Errorf("%w: input digest does not match snapshot and build context", ErrAnalyzeRequest)
	}
	if request.Compiler.ModulePath != artifact.ModulePath || request.Compiler.ModuleVersion != artifact.ModuleVersion || request.Compiler.ModuleChecksum != artifact.ModuleChecksum || request.Compiler.ManifestDigest != artifact.ManifestDigest || request.Compiler.CommandImportPath != CommandImportPath || request.Compiler.AnalyzeProtocol != AnalyzeSchema || request.Compiler.EmitProtocol != EmitSchema || request.Compiler.DeclarationLanguage != DeclarationLanguage || request.Compiler.GoToolchain != artifact.GoToolchain || !validModuleChecksum(request.Compiler.ModuleChecksum) || !validDigest(request.Compiler.ManifestDigest) {
		return analyzeRequestEnvelope{}, fmt.Errorf("%w: compiler identity differs from selected artifact", ErrAnalyzeRequest)
	}
	if request.Build.GOOS != artifact.GOOS || request.Build.GOARCH != artifact.GOARCH || request.Build.GOOS == "" || request.Build.GOARCH == "" {
		return analyzeRequestEnvelope{}, fmt.Errorf("%w: build context differs from selected artifact", ErrAnalyzeRequest)
	}
	return request, nil
}

// AnalyzeInputDigest computes the Data protocol input identity without
// importing the independent Data module. Snapshot must be the canonical JSON
// object containing packages, resources, and limits.
func AnalyzeInputDigest(snapshot []byte, build AnalyzeBuildContext) (string, error) {
	var envelope analyzeSnapshotEnvelope
	if err := decodeStrictJSON(snapshot, &envelope); err != nil {
		return "", fmt.Errorf("%w: snapshot is malformed", ErrAnalyzeRequest)
	}
	if err := validateAnalyzePackages(envelope.Packages); err != nil {
		return "", fmt.Errorf("%w: package inventory is malformed: %v", ErrAnalyzeRequest, err)
	}
	if err := validateAnalyzeArray(envelope.Resources); err != nil {
		return "", fmt.Errorf("%w: Resource inventory is malformed: %v", ErrAnalyzeRequest, err)
	}
	limits, ok := effectiveAnalyzeLimits(envelope.Limits)
	if !ok {
		return "", fmt.Errorf("%w: snapshot limits exceed the compiler manifest", ErrAnalyzeRequest)
	}
	var packages []struct {
		ImportPath      string `json:"import_path"`
		RootEligibility string `json:"root_eligibility"`
		Files           []struct {
			Path    string `json:"path"`
			Content []byte `json:"content"`
		} `json:"files"`
	}
	if err := decodeStrictJSON(envelope.Packages, &packages); err != nil {
		return "", fmt.Errorf("%w: package inventory is malformed", ErrAnalyzeRequest)
	}
	type fileIdentity struct {
		Path   string `json:"path"`
		Digest string `json:"digest"`
		Bytes  int    `json:"bytes"`
	}
	type packageIdentity struct {
		ImportPath      string         `json:"import_path"`
		RootEligibility string         `json:"root_eligibility"`
		Files           []fileIdentity `json:"files"`
	}
	identities := make([]packageIdentity, 0, len(packages))
	for _, pkg := range packages {
		identity := packageIdentity{ImportPath: pkg.ImportPath, RootEligibility: pkg.RootEligibility, Files: make([]fileIdentity, 0, len(pkg.Files))}
		for _, file := range pkg.Files {
			sum := sha256.Sum256(file.Content)
			identity.Files = append(identity.Files, fileIdentity{Path: file.Path, Digest: "sha256:" + hex.EncodeToString(sum[:]), Bytes: len(file.Content)})
		}
		sort.Slice(identity.Files, func(left, right int) bool { return identity.Files[left].Path < identity.Files[right].Path })
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(left, right int) bool { return identities[left].ImportPath < identities[right].ImportPath })
	var resources []struct {
		ImportPath string `json:"import_path"`
		TypeName   string `json:"type_name"`
		ID         string `json:"id"`
	}
	if err := decodeStrictJSON(envelope.Resources, &resources); err != nil {
		return "", fmt.Errorf("%w: Resource inventory is malformed", ErrAnalyzeRequest)
	}
	sort.Slice(resources, func(left, right int) bool {
		if resources[left].ID != resources[right].ID {
			return resources[left].ID < resources[right].ID
		}
		if resources[left].ImportPath != resources[right].ImportPath {
			return resources[left].ImportPath < resources[right].ImportPath
		}
		return resources[left].TypeName < resources[right].TypeName
	})
	snapshotPayload, err := json.Marshal(struct {
		Packages  any           `json:"packages"`
		Resources any           `json:"resources"`
		Limits    analyzeLimits `json:"limits"`
	}{identities, resources, limits})
	if err != nil {
		return "", fmt.Errorf("%w: snapshot digest: %v", ErrAnalyzeRequest, err)
	}
	snapshotSum := sha256.Sum256(append([]byte("plystra.data.snapshot/v1\x00"), snapshotPayload...))
	tags := append([]string(nil), build.BuildTags...)
	sort.Strings(tags)
	inputPayload, err := json.Marshal(struct {
		SnapshotDigest string              `json:"snapshot_digest"`
		Build          analyzeBuildContext `json:"build"`
	}{"sha256:" + hex.EncodeToString(snapshotSum[:]), analyzeBuildContext{GOOS: build.GOOS, GOARCH: build.GOARCH, BuildTags: tags}})
	if err != nil {
		return "", fmt.Errorf("%w: input digest: %v", ErrAnalyzeRequest, err)
	}
	inputSum := sha256.Sum256(append([]byte("plystra.data.analyze-input/v1\x00"), inputPayload...))
	return "sha256:" + hex.EncodeToString(inputSum[:]), nil
}

func validateAnalyzeResponse(data []byte, request analyzeRequestEnvelope) (AnalyzeResponse, error) {
	var response analyzeResponseEnvelope
	if err := decodeStrictJSON(data, &response); err != nil {
		return AnalyzeResponse{}, fmt.Errorf("%w: malformed terminal frame", ErrAnalyzeResponse)
	}
	if response.Schema != AnalyzeSchema || response.Phase != analyzePhase || response.RequestID != request.RequestID || response.InputDigest != request.InputDigest {
		return AnalyzeResponse{}, fmt.Errorf("%w: schema, phase, request ID, or input digest differs", ErrAnalyzeIdentity)
	}
	if response.Status != analyzeStatusOK && response.Status != analyzeStatusReject && response.Status != analyzeStatusFail {
		return AnalyzeResponse{}, fmt.Errorf("%w: unsupported terminal status", ErrAnalyzeResponse)
	}
	diagnostics, err := decodeDiagnostics(response.Diagnostics, request.effective)
	if err != nil {
		return AnalyzeResponse{}, fmt.Errorf("%w: malformed diagnostics", ErrAnalyzeResponse)
	}
	var output analyzeOutputEnvelope
	if err := decodeStrictJSON(response.Output, &output); err != nil {
		return AnalyzeResponse{}, fmt.Errorf("%w: malformed analyze output", ErrAnalyzeResponse)
	}
	if len(output.Roots) == 0 {
		return AnalyzeResponse{}, fmt.Errorf("%w: roots are missing", ErrAnalyzeResponse)
	}
	var roots []json.RawMessage
	if err := decodeStrictJSON(output.Roots, &roots); err != nil || len(roots) > request.effective.MaxRoots {
		return AnalyzeResponse{}, fmt.Errorf("%w: roots exceed the bound or are malformed", ErrAnalyzeResponse)
	}
	for _, root := range roots {
		if err := validateAnalyzeObject(root, "member_id", "package_path", "file_path", "symbol"); err != nil {
			return AnalyzeResponse{}, fmt.Errorf("%w: malformed root", ErrAnalyzeResponse)
		}
		var value struct {
			Model          json.RawMessage `json:"model"`
			SourceDigest   string          `json:"source_digest"`
			DeclarationPos json.RawMessage `json:"declaration_pos"`
		}
		if err := json.Unmarshal(root, &value); err != nil || len(value.Model) == 0 || string(value.Model) == "null" || !validDigest(value.SourceDigest) || len(value.DeclarationPos) == 0 || string(value.DeclarationPos) == "null" {
			return AnalyzeResponse{}, fmt.Errorf("%w: root model or provenance is incomplete", ErrAnalyzeResponse)
		}
	}
	if output.Truncated != response.Truncated {
		return AnalyzeResponse{}, fmt.Errorf("%w: inconsistent truncation", ErrAnalyzeResponse)
	}
	if response.OutputDigest != "" {
		if !validDigest(response.OutputDigest) || output.Digest != response.OutputDigest || response.OutputDigest != analyzeResultDigest(output.Roots, response.Diagnostics, response.Truncated) {
			return AnalyzeResponse{}, fmt.Errorf("%w: output digest mismatch", ErrAnalyzeResponse)
		}
	} else if output.Digest != "" || len(roots) != 0 {
		return AnalyzeResponse{}, fmt.Errorf("%w: undigested output", ErrAnalyzeResponse)
	}
	if response.Status == analyzeStatusOK {
		if response.OutputDigest == "" || !output.Valid || len(diagnostics) != 0 || response.Truncated {
			return AnalyzeResponse{}, fmt.Errorf("%w: successful response has invalid output, diagnostics, or truncation", ErrAnalyzeResponse)
		}
	} else if output.Valid || (len(diagnostics) == 0 && !response.Truncated) {
		return AnalyzeResponse{}, fmt.Errorf("%w: rejected or failed response has invalid output or no diagnostics", ErrAnalyzeResponse)
	}
	return AnalyzeResponse{
		Schema: response.Schema, Phase: response.Phase, RequestID: response.RequestID,
		InputDigest: response.InputDigest, Status: response.Status,
		OutputDigest: response.OutputDigest, Output: append(json.RawMessage(nil), response.Output...),
		Diagnostics: diagnostics, Truncated: response.Truncated,
	}, nil
}

func analyzeResultDigest(roots, diagnostics json.RawMessage, truncated bool) string {
	payload, _ := json.Marshal(struct {
		Roots       json.RawMessage `json:"roots"`
		Diagnostics json.RawMessage `json:"diagnostics"`
		Truncated   bool            `json:"truncated"`
	}{roots, diagnostics, truncated})
	sum := sha256.Sum256(append([]byte("plystra.data.analyze/v1\x00"), payload...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func decodeDiagnostics(data []byte, limits analyzeLimits) ([]json.RawMessage, error) {
	if len(data) == 0 {
		return nil, errors.New("diagnostics are missing")
	}
	var diagnostics []json.RawMessage
	if err := decodeStrictJSON(data, &diagnostics); err != nil {
		return nil, fmt.Errorf("diagnostics are invalid: %v", err)
	}
	if len(diagnostics) > limits.MaxDiagnostics {
		return nil, errors.New("diagnostic count exceeds the limit")
	}
	bytesUsed := 0
	for _, diagnostic := range diagnostics {
		bytesUsed += len(diagnostic)
		if len(diagnostic) == 0 || bytesUsed > limits.MaxDiagnosticBytes || !json.Valid(diagnostic) {
			return nil, errors.New("diagnostic exceeds the limit")
		}
		if err := validateAnalyzeObject(diagnostic, "code", "message", "action"); err != nil {
			return nil, errors.New("diagnostic is malformed")
		}
	}
	return diagnostics, nil
}

func validateAnalyzeArray(data []byte) error {
	var values []json.RawMessage
	if err := decodeStrictJSON(data, &values); err != nil {
		return err
	}
	return nil
}

func validateAnalyzePackages(data []byte) error {
	var packages []analyzeSnapshotPackage
	if err := decodeStrictJSON(data, &packages); err != nil {
		return err
	}
	for _, pkg := range packages {
		if strings.TrimSpace(pkg.ImportPath) == "" || (pkg.RootEligibility != "eligible" && pkg.RootEligibility != "support") || len(pkg.Files) == 0 {
			return errors.New("package identity or root eligibility is invalid")
		}
		var files []json.RawMessage
		if err := decodeStrictJSON(pkg.Files, &files); err != nil || len(files) == 0 {
			return errors.New("package files are missing")
		}
	}
	return nil
}

func validateAnalyzeObject(data []byte, required ...string) error {
	var value map[string]json.RawMessage
	if err := decodeStrictJSON(data, &value); err != nil || value == nil {
		return errors.New("value is not an object")
	}
	for _, name := range required {
		var field string
		if len(value[name]) == 0 || decodeStrictJSON(value[name], &field) != nil || strings.TrimSpace(field) == "" {
			return errors.New("required string field is absent")
		}
	}
	return nil
}

func effectiveAnalyzeLimits(limits analyzeLimits) (analyzeLimits, bool) {
	fields := []struct {
		value   *int
		maximum int
	}{
		{&limits.MaxRoots, MaxRoots}, {&limits.MaxNodes, MaxNodes},
		{&limits.MaxImports, MaxImports}, {&limits.MaxNesting, MaxNesting},
		{&limits.MaxSymbolBytes, MaxSymbolBytes}, {&limits.MaxDiagnostics, MaxDiagnostics},
		{&limits.MaxDiagnosticBytes, MaxDiagnosticBytes},
	}
	for _, field := range fields {
		if *field.value < 0 || *field.value > field.maximum {
			return analyzeLimits{}, false
		}
		if *field.value == 0 {
			*field.value = field.maximum
		}
	}
	return limits, true
}

func decodeStrictJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON")
		}
		return err
	}
	return nil
}

func writeAnalyzeFrame(w io.Writer, payload []byte) error {
	if len(payload) == 0 || len(payload) > MaxFrameBytes {
		return ErrAnalyzeFrameTooLarge
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAnalyzeAll(w, header[:]); err != nil {
		return err
	}
	return writeAnalyzeAll(w, payload)
}

func writeAnalyzeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := w.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(data) {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func readAnalyzeFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, fmt.Errorf("%w: missing or truncated frame header", ErrAnalyzeResponse)
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 {
		return nil, fmt.Errorf("%w: empty frame", ErrAnalyzeResponse)
	}
	if size > MaxFrameBytes {
		return nil, ErrAnalyzeFrameTooLarge
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("%w: truncated frame", ErrAnalyzeResponse)
	}
	return payload, nil
}
