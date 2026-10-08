package datacompiler

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestEmitAcceptsOneVerifiedStagedArtifactFrame(t *testing.T) {
	artifact := testAnalyzeArtifact(t)
	manifest := emitTestManifest()
	analyzeDigest := analyzeResultDigest(json.RawMessage(`[]`), json.RawMessage(`[]`), false)
	analyzeOutput := []byte(`{"roots":[],"digest":"` + analyzeDigest + `","truncated":false,"valid":true}`)
	request, err := BuildEmitRequest(artifact, manifest, EmitRequestOptions{
		RequestID: "emit-test", Build: AnalyzeBuildContext{GOOS: artifact.GOOS, GOARCH: artifact.GOARCH},
		AnalyzeOutput: analyzeOutput, AnalyzeDigest: analyzeDigest, FrozenModelDigest: "sha256:" + strings.Repeat("3", 64),
		Assignments: []EmitAssignment{{MemberID: "accounts.user/v1", Resource: "database.primary", ResourceContract: "data.database/v1", Provider: "github.com/plystra/data/postgres.New", Backend: "postgres/v1", AllowedRoot: "generated/data/database.primary"}},
	})
	if err != nil {
		t.Fatalf("BuildEmitRequest: %v", err)
	}
	response, err := Emit(context.Background(), artifact, manifest, request, AnalyzeOptions{Environment: []string{"PLYSTRA_EMIT_TEST_MODE=success"}})
	if err != nil || response.Status != emitStatusOK || len(response.Artifacts) != 1 {
		t.Fatalf("Emit() = %#v, %v", response, err)
	}
	if response.Artifacts[0].Path != "generated/data/database.primary/schema.sql" || string(response.Artifacts[0].Bytes) != "SELECT 1;\n" {
		t.Fatalf("staged artifact = %#v", response.Artifacts[0])
	}
}

func TestEmitRejectsInvalidTerminalFrames(t *testing.T) {
	artifact := testAnalyzeArtifact(t)
	manifest := emitTestManifest()
	analyzeDigest := analyzeResultDigest(json.RawMessage(`[]`), json.RawMessage(`[]`), false)
	request, err := BuildEmitRequest(artifact, manifest, EmitRequestOptions{
		RequestID: "emit-test", Build: AnalyzeBuildContext{GOOS: artifact.GOOS, GOARCH: artifact.GOARCH},
		AnalyzeOutput: []byte(`{"roots":[],"digest":"` + analyzeDigest + `","truncated":false,"valid":true}`), AnalyzeDigest: analyzeDigest,
		FrozenModelDigest: "sha256:" + strings.Repeat("3", 64), Assignments: []EmitAssignment{{MemberID: "accounts.user/v1", Resource: "database.primary", ResourceContract: "data.database/v1", Provider: "github.com/plystra/data/postgres.New", Backend: "postgres/v1", AllowedRoot: "generated/data/database.primary"}},
	})
	if err != nil {
		t.Fatalf("BuildEmitRequest: %v", err)
	}
	for _, test := range []struct {
		mode string
		want error
	}{
		{mode: "timeout", want: ErrEmitTimeout},
		{mode: "crash", want: ErrEmitCrash},
		{mode: "mismatch", want: ErrEmitIdentity},
		{mode: "malformed", want: ErrEmitResponse},
		{mode: "tamper", want: ErrEmitResponse},
		{mode: "extra", want: ErrEmit},
		{mode: "oversized", want: ErrEmitResponse},
	} {
		t.Run(test.mode, func(t *testing.T) {
			timeout := 2 * time.Second
			if test.mode == "timeout" {
				timeout = 30 * time.Millisecond
			}
			_, err := Emit(context.Background(), artifact, manifest, request, AnalyzeOptions{Timeout: timeout, Environment: []string{"PLYSTRA_EMIT_TEST_MODE=" + test.mode}})
			if err == nil || !errors.Is(err, test.want) {
				t.Fatalf("Emit(%s) error = %v, want %v", test.mode, err, test.want)
			}
		})
	}
}

func TestEmitAcceptsRejectedResponseDiagnostics(t *testing.T) {
	artifact := testAnalyzeArtifact(t)
	manifest := emitTestManifest()
	analyzeDigest := analyzeResultDigest(json.RawMessage(`[]`), json.RawMessage(`[]`), false)
	request, err := BuildEmitRequest(artifact, manifest, EmitRequestOptions{
		RequestID: "emit-rejected", Build: AnalyzeBuildContext{GOOS: artifact.GOOS, GOARCH: artifact.GOARCH},
		AnalyzeOutput: []byte(`{"roots":[],"digest":"` + analyzeDigest + `","truncated":false,"valid":true}`), AnalyzeDigest: analyzeDigest,
		FrozenModelDigest: "sha256:" + strings.Repeat("3", 64), Assignments: []EmitAssignment{{MemberID: "accounts.user/v1", Resource: "database.primary", ResourceContract: "data.database/v1", Provider: "github.com/plystra/data/postgres.New", Backend: "postgres/v1", AllowedRoot: "generated/data/database.primary"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := Emit(context.Background(), artifact, manifest, request, AnalyzeOptions{Environment: []string{"PLYSTRA_EMIT_TEST_MODE=rejected"}})
	if err != nil || response.Status != emitStatusReject || len(response.Diagnostics) != 1 {
		t.Fatalf("rejected Emit() = %#v, %v", response, err)
	}
}

func TestEmitRejectsManifestBounds(t *testing.T) {
	artifact := testAnalyzeArtifact(t)
	manifest := emitTestManifest()
	analyzeDigest := analyzeResultDigest(json.RawMessage(`[]`), json.RawMessage(`[]`), false)
	request, err := BuildEmitRequest(artifact, manifest, EmitRequestOptions{
		RequestID: "emit-bounds", Build: AnalyzeBuildContext{GOOS: artifact.GOOS, GOARCH: artifact.GOARCH},
		AnalyzeOutput: []byte(`{"roots":[],"digest":"` + analyzeDigest + `","truncated":false,"valid":true}`), AnalyzeDigest: analyzeDigest,
		FrozenModelDigest: "sha256:" + strings.Repeat("3", 64), Assignments: []EmitAssignment{{MemberID: "accounts.user/v1", Resource: "database.primary", ResourceContract: "data.database/v1", Provider: "github.com/plystra/data/postgres.New", Backend: "postgres/v1", AllowedRoot: "generated/data/database.primary"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("request frame", func(t *testing.T) {
		small := manifest
		small.Bounds.MaxFrameBytes = 128
		if _, err := Emit(context.Background(), artifact, small, request, AnalyzeOptions{Environment: []string{"PLYSTRA_EMIT_TEST_MODE=success"}}); err == nil || !errors.Is(err, ErrEmitRequest) {
			t.Fatalf("request bound error = %v", err)
		}
	})
	t.Run("response frame", func(t *testing.T) {
		small := manifest
		small.Bounds.MaxFrameBytes = len(request)
		if _, err := Emit(context.Background(), artifact, small, request, AnalyzeOptions{Environment: []string{"PLYSTRA_EMIT_TEST_MODE=oversized"}}); err == nil || !errors.Is(err, ErrEmitResponse) {
			t.Fatalf("response bound error = %v", err)
		}
	})
	t.Run("diagnostics", func(t *testing.T) {
		small := manifest
		small.Bounds.MaxDiagnosticBytes = 16
		if _, err := Emit(context.Background(), artifact, small, request, AnalyzeOptions{Environment: []string{"PLYSTRA_EMIT_TEST_MODE=rejected"}}); err == nil || !errors.Is(err, ErrEmitResponse) {
			t.Fatalf("diagnostic bound error = %v", err)
		}
	})
}

func runEmitTestCompiler(mode string) {
	if mode == "timeout" {
		time.Sleep(2 * time.Second)
		return
	}
	if mode == "crash" {
		os.Exit(17)
	}
	requestPayload, err := readEmitFrame(os.Stdin, MaxFrameBytes)
	if err != nil {
		os.Exit(18)
	}
	var request struct {
		RequestID         string               `json:"request_id"`
		InputDigest       string               `json:"input_digest"`
		Compiler          emitCompilerIdentity `json:"compiler"`
		FrozenModelDigest string               `json:"frozen_model_digest"`
		Assignments       []EmitAssignment     `json:"assignments"`
	}
	if err := json.Unmarshal(requestPayload, &request); err != nil || len(request.Assignments) != 1 {
		os.Exit(19)
	}
	if mode == "mismatch" {
		request.RequestID = "different-request"
	}
	if mode == "malformed" {
		_ = writeEmitFrame(os.Stdout, []byte("not-json"))
		return
	}
	if mode == "oversized" {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(MaxFrameBytes+1))
		_, _ = os.Stdout.Write(header[:])
		return
	}
	assignment := request.Assignments[0]
	content := []byte("SELECT 1;\n")
	staged := EmitArtifact{Path: assignment.AllowedRoot + "/schema.sql", Mode: 0644, Bytes: content, Digest: digestBytes("", content), OwningMembers: []string{assignment.MemberID}, Resource: assignment.Resource, ResourceContract: assignment.ResourceContract, Provider: assignment.Provider, Backend: assignment.Backend, Compiler: request.Compiler, FrozenModelDigest: request.FrozenModelDigest}
	if mode == "tamper" {
		staged.Digest = "sha256:" + strings.Repeat("0", 64)
	}
	artifacts := []EmitArtifact{staged}
	artifactBytes, _ := json.Marshal(artifacts)
	status := emitStatusOK
	diagnostics := []any{}
	if mode == "rejected" {
		status = emitStatusReject
		artifacts = nil
		artifactBytes, _ = json.Marshal(artifacts)
		diagnostics = []any{map[string]any{"code": "emit-input", "message": "rejected", "action": "correct input"}}
	}
	response := map[string]any{
		"schema": EmitSchema, "phase": emitPhase, "request_id": request.RequestID, "input_digest": request.InputDigest,
		"status": status, "output": map[string]any{"artifacts": artifacts}, "diagnostics": diagnostics, "truncated": false,
	}
	if status == emitStatusOK {
		sum := sha256.Sum256(append([]byte("plystra.data.emit-output/v1\x00"), artifactBytes...))
		response["output_digest"] = "sha256:" + hex.EncodeToString(sum[:])
	}
	data, err := json.Marshal(response)
	if err != nil || writeEmitFrame(os.Stdout, data) != nil {
		os.Exit(20)
	}
	if mode == "extra" {
		_, _ = os.Stdout.Write([]byte{0})
	}
}

func emitTestManifest() Manifest {
	return Manifest{Schema: DistributionSchema, ModulePath: ModulePath, CommandImportPath: CommandImportPath, AnalyzeProtocol: AnalyzeSchema, EmitProtocol: EmitSchema, DeclarationLanguage: DeclarationLanguage, Bounds: Bounds{MaxRoots: MaxRoots, MaxNodes: MaxNodes, MaxImports: MaxImports, MaxNesting: MaxNesting, MaxSymbolBytes: MaxSymbolBytes, MaxDiagnostics: MaxDiagnostics, MaxDiagnosticBytes: MaxDiagnosticBytes, MaxFrameBytes: MaxFrameBytes, MaxArtifacts: MaxArtifacts, MaxArtifactBytes: MaxArtifactBytes, MaxArtifactPathBytes: MaxArtifactPathBytes}}
}
