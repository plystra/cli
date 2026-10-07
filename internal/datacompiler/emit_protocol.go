package datacompiler

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

const (
	emitPhase        = "emit"
	emitStatusOK     = "succeeded"
	emitStatusReject = "rejected"
	emitStatusFail   = "failed"
)

var (
	ErrEmitRequest  = errors.New("invalid Data emit request")
	ErrEmitResponse = errors.New("invalid Data emit response")
	ErrEmitIdentity = errors.New("Data emit response identity mismatch")
)

type EmitAssignment struct {
	MemberID         string `json:"member_id"`
	Resource         string `json:"resource"`
	ResourceContract string `json:"resource_contract"`
	Provider         string `json:"provider"`
	Backend          string `json:"backend"`
	AllowedRoot      string `json:"allowed_root"`
}

type EmitRequestOptions struct {
	RequestID         string
	Build             AnalyzeBuildContext
	AnalyzeOutput     json.RawMessage
	AnalyzeDigest     string
	FrozenModelDigest string
	Assignments       []EmitAssignment
}

type emitCompilerIdentity struct {
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

type emitBuildContext struct {
	GOOS      string   `json:"goos"`
	GOARCH    string   `json:"goarch"`
	BuildTags []string `json:"build_tags,omitempty"`
}

type emitLimits struct {
	MaxRoots           int `json:"max_roots"`
	MaxNodes           int `json:"max_nodes"`
	MaxImports         int `json:"max_imports"`
	MaxNesting         int `json:"max_nesting"`
	MaxSymbolBytes     int `json:"max_symbol_bytes"`
	MaxDiagnostics     int `json:"max_diagnostics"`
	MaxDiagnosticBytes int `json:"max_diagnostic_bytes"`
}

type emitRequestEnvelope struct {
	Schema            string               `json:"schema"`
	Phase             string               `json:"phase"`
	RequestID         string               `json:"request_id"`
	Compiler          emitCompilerIdentity `json:"compiler"`
	Build             emitBuildContext     `json:"build"`
	Limits            emitLimits           `json:"limits"`
	InputDigest       string               `json:"input_digest"`
	Analyze           json.RawMessage      `json:"analyze"`
	AnalyzeDigest     string               `json:"analyze_digest"`
	FrozenModelDigest string               `json:"frozen_model_digest"`
	Assignments       []EmitAssignment     `json:"assignments"`
}

type emitOutputEnvelope struct {
	Artifacts json.RawMessage `json:"artifacts"`
}

type emitResponseEnvelope struct {
	Schema       string             `json:"schema"`
	Phase        string             `json:"phase"`
	RequestID    string             `json:"request_id"`
	InputDigest  string             `json:"input_digest"`
	Status       string             `json:"status"`
	OutputDigest string             `json:"output_digest,omitempty"`
	Output       emitOutputEnvelope `json:"output"`
	Diagnostics  json.RawMessage    `json:"diagnostics"`
	Truncated    bool               `json:"truncated"`
}

type EmitArtifact struct {
	Path              string               `json:"path"`
	Mode              uint32               `json:"mode"`
	Bytes             []byte               `json:"bytes"`
	Digest            string               `json:"digest"`
	OwningMembers     []string             `json:"owning_members"`
	Resource          string               `json:"resource"`
	ResourceContract  string               `json:"resource_contract"`
	Provider          string               `json:"provider"`
	Backend           string               `json:"backend"`
	Compiler          emitCompilerIdentity `json:"compiler"`
	FrozenModelDigest string               `json:"frozen_model_digest"`
	Query             json.RawMessage      `json:"query,omitempty"`
	Migration         json.RawMessage      `json:"migration,omitempty"`
}

type EmitResponse struct {
	Schema       string
	Phase        string
	RequestID    string
	InputDigest  string
	Status       string
	OutputDigest string
	Artifacts    []EmitArtifact
	Diagnostics  []json.RawMessage
	Truncated    bool
}

func BuildEmitRequest(artifact Artifact, manifest Manifest, options EmitRequestOptions) ([]byte, error) {
	if artifact.ModulePath != ModulePath || artifact.ModuleVersion == "" || artifact.ManifestDigest == "" || options.RequestID == "" || options.Build.GOOS == "" || options.Build.GOARCH == "" {
		return nil, fmt.Errorf("%w: compiler identity, request ID, or build context is incomplete", ErrEmitRequest)
	}
	var analyze struct {
		Roots     json.RawMessage `json:"roots"`
		Digest    string          `json:"digest"`
		Truncated bool            `json:"truncated"`
		Valid     bool            `json:"valid"`
	}
	if err := decodeStrictJSON(options.AnalyzeOutput, &analyze); err != nil || !analyze.Valid || analyze.Truncated || analyze.Digest != options.AnalyzeDigest || !validDigest(options.AnalyzeDigest) {
		return nil, fmt.Errorf("%w: accepted analyze output is incomplete", ErrEmitRequest)
	}
	assignments := append([]EmitAssignment(nil), options.Assignments...)
	sort.Slice(assignments, func(left, right int) bool { return assignments[left].MemberID < assignments[right].MemberID })
	request := emitRequestEnvelope{
		Schema: EmitSchema, Phase: emitPhase, RequestID: options.RequestID,
		Compiler: emitCompilerIdentity{ModulePath: artifact.ModulePath, ModuleVersion: artifact.ModuleVersion, ModuleChecksum: artifact.ModuleChecksum, ManifestDigest: artifact.ManifestDigest, CommandImportPath: CommandImportPath, AnalyzeProtocol: AnalyzeSchema, EmitProtocol: EmitSchema, DeclarationLanguage: DeclarationLanguage, GoToolchain: artifact.GoToolchain},
		Build:    emitBuildContext{GOOS: options.Build.GOOS, GOARCH: options.Build.GOARCH, BuildTags: append([]string(nil), options.Build.BuildTags...)},
		Limits:   emitLimits{MaxRoots: manifest.Bounds.MaxRoots, MaxNodes: manifest.Bounds.MaxNodes, MaxImports: manifest.Bounds.MaxImports, MaxNesting: manifest.Bounds.MaxNesting, MaxSymbolBytes: manifest.Bounds.MaxSymbolBytes, MaxDiagnostics: manifest.Bounds.MaxDiagnostics, MaxDiagnosticBytes: manifest.Bounds.MaxDiagnosticBytes},
		Analyze:  options.AnalyzeOutput, AnalyzeDigest: options.AnalyzeDigest, FrozenModelDigest: options.FrozenModelDigest, Assignments: assignments,
	}
	if !validDigest(options.FrozenModelDigest) {
		return nil, fmt.Errorf("%w: frozen model digest is invalid", ErrEmitRequest)
	}
	input, err := emitInputDigest(request)
	if err != nil {
		return nil, err
	}
	request.InputDigest = input
	data, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("%w: encode request: %v", ErrEmitRequest, err)
	}
	if len(data) > manifest.Bounds.MaxFrameBytes {
		return nil, fmt.Errorf("%w: request exceeds the compiler frame limit", ErrEmitRequest)
	}
	return data, nil
}

func emitInputDigest(request emitRequestEnvelope) (string, error) {
	assignments := append([]EmitAssignment(nil), request.Assignments...)
	sort.Slice(assignments, func(left, right int) bool { return assignments[left].MemberID < assignments[right].MemberID })
	build := request.Build
	build.BuildTags = append([]string(nil), build.BuildTags...)
	sort.Strings(build.BuildTags)
	payload, err := json.Marshal(struct {
		Compiler          emitCompilerIdentity `json:"compiler"`
		Build             emitBuildContext     `json:"build"`
		Limits            emitLimits           `json:"limits"`
		AnalyzeDigest     string               `json:"analyze_digest"`
		FrozenModelDigest string               `json:"frozen_model_digest"`
		Assignments       []EmitAssignment     `json:"assignments"`
	}{request.Compiler, build, request.Limits, request.AnalyzeDigest, request.FrozenModelDigest, assignments})
	if err != nil {
		return "", fmt.Errorf("%w: input digest: %v", ErrEmitRequest, err)
	}
	sum := sha256.Sum256(append([]byte("plystra.data.emit-input/v1\x00"), payload...))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateEmitRequest(data []byte, artifact Artifact, manifest Manifest) (emitRequestEnvelope, error) {
	var request emitRequestEnvelope
	if err := decodeStrictJSON(data, &request); err != nil {
		return request, fmt.Errorf("%w: malformed request", ErrEmitRequest)
	}
	if request.Schema != EmitSchema || request.Phase != emitPhase || strings.TrimSpace(request.RequestID) == "" || !validDigest(request.InputDigest) || !validDigest(request.AnalyzeDigest) || !validDigest(request.FrozenModelDigest) || len(request.Analyze) == 0 {
		return request, fmt.Errorf("%w: schema, identity, or analyze input is incomplete", ErrEmitRequest)
	}
	if request.Compiler.ModulePath != artifact.ModulePath || request.Compiler.ModuleVersion != artifact.ModuleVersion || request.Compiler.ModuleChecksum != artifact.ModuleChecksum || request.Compiler.ManifestDigest != artifact.ManifestDigest || request.Compiler.CommandImportPath != CommandImportPath || request.Compiler.AnalyzeProtocol != AnalyzeSchema || request.Compiler.EmitProtocol != EmitSchema || request.Compiler.DeclarationLanguage != DeclarationLanguage || request.Compiler.GoToolchain != artifact.GoToolchain {
		return request, fmt.Errorf("%w: compiler identity differs from selected artifact", ErrEmitRequest)
	}
	if request.Build.GOOS != artifact.GOOS || request.Build.GOARCH != artifact.GOARCH || request.Build.GOOS == "" || request.Build.GOARCH == "" {
		return request, fmt.Errorf("%w: build context differs from selected artifact", ErrEmitRequest)
	}
	var analyze struct {
		Roots     json.RawMessage `json:"roots"`
		Digest    string          `json:"digest"`
		Truncated bool            `json:"truncated"`
		Valid     bool            `json:"valid"`
	}
	if err := decodeStrictJSON(request.Analyze, &analyze); err != nil || !analyze.Valid || analyze.Truncated || analyze.Digest != request.AnalyzeDigest {
		return request, fmt.Errorf("%w: analyze output is incomplete", ErrEmitRequest)
	}
	if len(request.Assignments) > manifest.Bounds.MaxRoots {
		return request, fmt.Errorf("%w: assignment limit exceeded", ErrEmitRequest)
	}
	if expected, err := emitInputDigest(request); err != nil || expected != request.InputDigest {
		return request, fmt.Errorf("%w: input digest mismatch", ErrEmitRequest)
	}
	return request, nil
}

func writeEmitFrame(w io.Writer, payload []byte) error {
	if len(payload) == 0 || len(payload) > MaxFrameBytes {
		return ErrEmitRequest
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readEmitFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > MaxFrameBytes {
		return nil, ErrEmitResponse
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func validateEmitResponse(data []byte, request emitRequestEnvelope, manifest Manifest) (EmitResponse, error) {
	var wire emitResponseEnvelope
	if err := decodeStrictJSON(data, &wire); err != nil {
		return EmitResponse{}, fmt.Errorf("%w: malformed terminal frame", ErrEmitResponse)
	}
	if wire.Schema != EmitSchema || wire.Phase != emitPhase || wire.RequestID != request.RequestID || wire.InputDigest != request.InputDigest {
		return EmitResponse{}, ErrEmitIdentity
	}
	if wire.Status != emitStatusOK && wire.Status != emitStatusReject && wire.Status != emitStatusFail {
		return EmitResponse{}, fmt.Errorf("%w: unsupported terminal status", ErrEmitResponse)
	}
	var diagnostics []json.RawMessage
	if err := decodeStrictJSON(wire.Diagnostics, &diagnostics); err != nil {
		return EmitResponse{}, fmt.Errorf("%w: malformed diagnostics", ErrEmitResponse)
	}
	var artifacts []EmitArtifact
	if err := decodeStrictJSON(wire.Output.Artifacts, &artifacts); err != nil || len(artifacts) > manifest.Bounds.MaxArtifacts {
		return EmitResponse{}, fmt.Errorf("%w: malformed artifact output", ErrEmitResponse)
	}
	if wire.Status != emitStatusOK {
		if len(artifacts) != 0 || len(diagnostics) == 0 || wire.OutputDigest != "" {
			return EmitResponse{}, fmt.Errorf("%w: rejected response contains artifacts or no diagnostics", ErrEmitResponse)
		}
	} else {
		if wire.Truncated || len(diagnostics) != 0 || !validDigest(wire.OutputDigest) {
			return EmitResponse{}, fmt.Errorf("%w: successful response has invalid diagnostics or truncation", ErrEmitResponse)
		}
		if wire.OutputDigest != digestBytes("plystra.data.emit-output/v1", wire.Output.Artifacts) {
			return EmitResponse{}, fmt.Errorf("%w: output digest mismatch", ErrEmitResponse)
		}
	}
	assignments := make(map[string]EmitAssignment, len(request.Assignments))
	for _, assignment := range request.Assignments {
		assignments[assignment.MemberID] = assignment
	}
	previous := ""
	seen := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		if len(artifact.Path) > MaxArtifactPathBytes || !safeEmitPath(artifact.Path) || artifact.Mode != 0644 || len(artifact.Bytes) == 0 || len(artifact.Bytes) > manifest.Bounds.MaxArtifactBytes || artifact.Digest != digestBytes("", artifact.Bytes) {
			return EmitResponse{}, fmt.Errorf("%w: invalid staged artifact path, mode, bytes, or digest", ErrEmitResponse)
		}
		if previous != "" && artifact.Path <= previous {
			return EmitResponse{}, fmt.Errorf("%w: staged artifacts are not in canonical path order", ErrEmitResponse)
		}
		previous = artifact.Path
		key := strings.ToLower(artifact.Path)
		if _, exists := seen[key]; exists {
			return EmitResponse{}, fmt.Errorf("%w: duplicate staged artifact path", ErrEmitResponse)
		}
		seen[key] = struct{}{}
		if len(artifact.OwningMembers) == 0 {
			return EmitResponse{}, fmt.Errorf("%w: staged artifact has no owning member", ErrEmitResponse)
		}
		for index, memberID := range artifact.OwningMembers {
			assignment, exists := assignments[memberID]
			if !exists || assignment.Resource != artifact.Resource || assignment.ResourceContract != artifact.ResourceContract || assignment.Provider != artifact.Provider || assignment.Backend != artifact.Backend || assignment.AllowedRoot == "" || !strings.HasPrefix(artifact.Path, assignment.AllowedRoot+"/") || (index > 0 && artifact.OwningMembers[index-1] >= memberID) {
				return EmitResponse{}, fmt.Errorf("%w: staged artifact ownership or backend mismatch", ErrEmitResponse)
			}
		}
		if artifact.Compiler != request.Compiler || artifact.FrozenModelDigest != request.FrozenModelDigest {
			return EmitResponse{}, fmt.Errorf("%w: staged artifact compiler or frozen model mismatch", ErrEmitResponse)
		}
	}
	return EmitResponse{Schema: wire.Schema, Phase: wire.Phase, RequestID: wire.RequestID, InputDigest: wire.InputDigest, Status: wire.Status, OutputDigest: wire.OutputDigest, Artifacts: artifacts, Diagnostics: diagnostics, Truncated: wire.Truncated}, nil
}

func safeEmitPath(value string) bool {
	if value == "" || strings.ContainsAny(value, "\\:\x00") || strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func digestBytes(prefix string, value []byte) string {
	sum := sha256.Sum256(value)
	if prefix != "" {
		sum = sha256.Sum256(append([]byte(prefix+"\x00"), value...))
	}
	return "sha256:" + hex.EncodeToString(sum[:])
}
