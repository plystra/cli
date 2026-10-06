package datacompiler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateAnalyzeRequestBindsCompilerAndBuildIdentity(t *testing.T) {
	artifact := testAnalyzeArtifact(t)
	request := validAnalyzeRequest(t, artifact)
	if _, err := validateAnalyzeRequest(request, artifact); err != nil {
		t.Fatal(err)
	}
	var changed map[string]any
	if err := json.Unmarshal(request, &changed); err != nil {
		t.Fatal(err)
	}
	compiler := changed["compiler"].(map[string]any)
	compiler["module_version"] = "v0.0.1-test"
	changedRequest, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateAnalyzeRequest(changedRequest, artifact); !errors.Is(err, ErrAnalyzeRequest) {
		t.Fatalf("validateAnalyzeRequest() error = %v", err)
	}
	changed["compiler"] = map[string]any{
		"module_path": artifact.ModulePath, "module_version": artifact.ModuleVersion,
		"module_checksum": artifact.ModuleChecksum, "manifest_digest": artifact.ManifestDigest,
		"command_import_path": CommandImportPath, "analyze_protocol": AnalyzeSchema,
		"emit_protocol": EmitSchema, "declaration_language": DeclarationLanguage,
		"go_toolchain": artifact.GoToolchain,
	}
	changed["limits"] = "invalid"
	changedRequest, err = json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateAnalyzeRequest(changedRequest, artifact); !errors.Is(err, ErrAnalyzeRequest) {
		t.Fatalf("malformed limits error = %v", err)
	}
}

func TestValidateAnalyzeRequestRequiresClosedPackageRootEligibility(t *testing.T) {
	artifact := testAnalyzeArtifact(t)
	for _, eligibility := range []string{"eligible", "support", "", "unknown"} {
		var request map[string]any
		if err := json.Unmarshal(validAnalyzeRequest(t, artifact), &request); err != nil {
			t.Fatal(err)
		}
		pkg := map[string]any{
			"import_path": "example.com/model",
			"files":       []any{map[string]any{"path": "model.go", "content": base64.StdEncoding.EncodeToString([]byte("package model\n"))}},
		}
		if eligibility != "" {
			pkg["root_eligibility"] = eligibility
		}
		request["snapshot"].(map[string]any)["packages"] = []any{pkg}
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		_, err = validateAnalyzeRequest(data, artifact)
		if eligibility == "eligible" || eligibility == "support" {
			if err != nil {
				t.Fatalf("eligibility %q rejected: %v", eligibility, err)
			}
		} else if !errors.Is(err, ErrAnalyzeRequest) {
			t.Fatalf("eligibility %q accepted: %v", eligibility, err)
		}
	}
}

func TestValidateAnalyzeResponseRequiresIdentityAndBoundedDiagnostics(t *testing.T) {
	artifact := testAnalyzeArtifact(t)
	request := validAnalyzeRequest(t, artifact)
	envelope, err := validateAnalyzeRequest(request, artifact)
	if err != nil {
		t.Fatal(err)
	}
	response := validAnalyzeResponse(t, envelope)
	decoded, err := validateAnalyzeResponse(response, envelope)
	if err != nil || decoded.Status != analyzeStatusOK || len(decoded.Output) == 0 {
		t.Fatalf("validateAnalyzeResponse() = %#v, %v", decoded, err)
	}
	var changed map[string]any
	if err := json.Unmarshal(response, &changed); err != nil {
		t.Fatal(err)
	}
	changed["request_id"] = "other-request"
	changedResponse, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateAnalyzeResponse(changedResponse, envelope); !errors.Is(err, ErrAnalyzeIdentity) {
		t.Fatalf("validateAnalyzeResponse() error = %v", err)
	}
	changed["request_id"] = envelope.RequestID
	changed["output_digest"] = "sha256:" + strings.Repeat("9", 64)
	changedResponse, err = json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateAnalyzeResponse(changedResponse, envelope); !errors.Is(err, ErrAnalyzeResponse) {
		t.Fatalf("tampered digest error = %v", err)
	}
	changed["output_digest"] = analyzeResultDigest(json.RawMessage(`[null]`), json.RawMessage(`[]`), false)
	changed["output"] = map[string]any{"roots": []any{nil}, "digest": changed["output_digest"], "truncated": false, "valid": true}
	changedResponse, err = json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateAnalyzeResponse(changedResponse, envelope); !errors.Is(err, ErrAnalyzeResponse) {
		t.Fatalf("null root error = %v", err)
	}
}

func TestValidateAnalyzeResponseUsesDeclaredRootLimit(t *testing.T) {
	artifact := testAnalyzeArtifact(t)
	var request map[string]any
	if err := json.Unmarshal(validAnalyzeRequest(t, artifact), &request); err != nil {
		t.Fatal(err)
	}
	request["limits"] = map[string]any{"max_roots": 1}
	snapshot := request["snapshot"].(map[string]any)
	snapshot["limits"] = request["limits"]
	requestBytes, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := validateAnalyzeRequest(requestBytes, artifact)
	if err != nil {
		t.Fatal(err)
	}
	root := map[string]any{"member_id": "accounts.user/v1", "package_path": "example.com/model", "file_path": "model.go", "symbol": "Accounts"}
	roots := []any{root, root}
	rootsBytes, err := json.Marshal(roots)
	if err != nil {
		t.Fatal(err)
	}
	digest := analyzeResultDigest(rootsBytes, json.RawMessage(`[]`), false)
	response := map[string]any{
		"schema": AnalyzeSchema, "phase": analyzePhase, "request_id": envelope.RequestID,
		"input_digest": envelope.InputDigest, "status": analyzeStatusOK,
		"output_digest": digest,
		"output":        map[string]any{"roots": roots, "digest": digest, "truncated": false, "valid": true},
		"diagnostics":   []any{}, "truncated": false,
	}
	responseBytes, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateAnalyzeResponse(responseBytes, envelope); !errors.Is(err, ErrAnalyzeResponse) {
		t.Fatalf("root limit error = %v", err)
	}
}

func TestValidateAnalyzeResponseAcceptsDataEmptySliceEncoding(t *testing.T) {
	artifact := testAnalyzeArtifact(t)
	envelope, err := validateAnalyzeRequest(validAnalyzeRequest(t, artifact), artifact)
	if err != nil {
		t.Fatal(err)
	}
	digest := analyzeResultDigest(json.RawMessage(`null`), json.RawMessage(`null`), false)
	response := map[string]any{
		"schema": AnalyzeSchema, "phase": analyzePhase, "request_id": envelope.RequestID,
		"input_digest": envelope.InputDigest, "status": analyzeStatusOK,
		"output_digest": digest,
		"output":        map[string]any{"roots": nil, "digest": digest, "truncated": false, "valid": true},
		"diagnostics":   nil, "truncated": false,
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateAnalyzeResponse(data, envelope); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeFrameRejectsOversizedAndTrailingPayloads(t *testing.T) {
	if err := writeAnalyzeFrame(discardWriter{}, make([]byte, MaxFrameBytes+1)); !errors.Is(err, ErrAnalyzeFrameTooLarge) {
		t.Fatalf("writeAnalyzeFrame() error = %v", err)
	}
	payload := []byte(`{"schema":"x"}`)
	frame := append([]byte{}, frameForTest(payload)...)
	frame = append(frame, 0)
	if got, err := readAnalyzeFrame(strings.NewReader(string(frame))); err != nil || string(got) != string(payload) {
		t.Fatalf("readAnalyzeFrame() = %q, %v", got, err)
	}
}

func testAnalyzeArtifact(t *testing.T) Artifact {
	t.Helper()
	path, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	digest, _, err := fileDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	return Artifact{
		Path: path, ModulePath: ModulePath, ModuleVersion: "v0.0.0-test",
		ModuleChecksum: testAnalyzeModuleChecksum(), ManifestDigest: "sha256:" + strings.Repeat("2", 64),
		BinaryDigest: digest,
		GoToolchain:  runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	}
}

func validAnalyzeRequest(t *testing.T, artifact Artifact) []byte {
	t.Helper()
	request := map[string]any{
		"schema": AnalyzeSchema, "phase": analyzePhase, "request_id": "analyze-test",
		"compiler": map[string]any{
			"module_path": artifact.ModulePath, "module_version": artifact.ModuleVersion,
			"module_checksum": artifact.ModuleChecksum, "manifest_digest": artifact.ManifestDigest,
			"command_import_path": CommandImportPath, "analyze_protocol": AnalyzeSchema,
			"emit_protocol": EmitSchema, "declaration_language": DeclarationLanguage,
			"go_toolchain": artifact.GoToolchain,
		},
		"build":  map[string]any{"goos": artifact.GOOS, "goarch": artifact.GOARCH},
		"limits": map[string]any{}, "input_digest": "sha256:" + strings.Repeat("1", 64),
		"snapshot": map[string]any{"packages": []any{}, "resources": []any{}, "limits": map[string]any{}},
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validAnalyzeResponse(t *testing.T, request analyzeRequestEnvelope) []byte {
	t.Helper()
	digest := analyzeResultDigest(json.RawMessage(`[]`), json.RawMessage(`[]`), false)
	response := map[string]any{
		"schema": AnalyzeSchema, "phase": analyzePhase, "request_id": request.RequestID,
		"input_digest": request.InputDigest, "status": analyzeStatusOK,
		"output_digest": digest,
		"output":        map[string]any{"roots": []any{}, "digest": digest, "truncated": false, "valid": true},
		"diagnostics":   []any{}, "truncated": false,
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testAnalyzeModuleChecksum() string {
	return "h1:" + base64.StdEncoding.EncodeToString(make([]byte, 32))
}

type discardWriter struct{}

func (discardWriter) Write(data []byte) (int, error) { return len(data), nil }

func frameForTest(payload []byte) []byte {
	frame := make([]byte, 4+len(payload))
	frame[0] = byte(len(payload) >> 24)
	frame[1] = byte(len(payload) >> 16)
	frame[2] = byte(len(payload) >> 8)
	frame[3] = byte(len(payload))
	copy(frame[4:], payload)
	return frame
}
