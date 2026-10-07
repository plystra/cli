package datacompiler

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("PLYSTRA_ANALYZE_TEST_MODE"); mode != "" {
		runAnalyzeTestCompiler(mode)
		return
	}
	if mode := os.Getenv("PLYSTRA_EMIT_TEST_MODE"); mode != "" {
		runEmitTestCompiler(mode)
		return
	}
	os.Exit(m.Run())
}

func TestAnalyzeAcceptsOneVerifiedSuccessFrame(t *testing.T) {
	artifact := testAnalyzeArtifact(t)
	var observations []Observation
	response, err := Analyze(context.Background(), artifact, validAnalyzeRequest(t, artifact), AnalyzeOptions{
		Environment: []string{"PLYSTRA_ANALYZE_TEST_MODE=success"},
		Observe:     func(observation Observation) { observations = append(observations, observation) },
	})
	if err != nil || response.Status != analyzeStatusOK || len(response.Output) == 0 {
		t.Fatalf("Analyze() = %#v, %v", response, err)
	}
	if len(observations) != 1 || observations[0].ID != "data-compiler-analyze-execution" || observations[0].Class != ObservationTrustedExecution {
		t.Fatalf("Analyze observations = %#v", observations)
	}
}

func TestAnalyzePreservesCompilerDiagnosticsForRejectedInput(t *testing.T) {
	artifact := testAnalyzeArtifact(t)
	response, err := Analyze(context.Background(), artifact, validAnalyzeRequest(t, artifact), AnalyzeOptions{Environment: []string{"PLYSTRA_ANALYZE_TEST_MODE=rejected"}})
	if err != nil || response.Status != analyzeStatusReject || len(response.Diagnostics) != 1 {
		t.Fatalf("Analyze() = %#v, %v", response, err)
	}
}

func TestAnalyzeRejectsTamperedCompilerArtifactBeforeExecution(t *testing.T) {
	artifact := testAnalyzeArtifact(t)
	artifact.BinaryDigest = "sha256:" + strings.Repeat("0", 64)
	_, err := Analyze(context.Background(), artifact, validAnalyzeRequest(t, artifact), AnalyzeOptions{Environment: []string{"PLYSTRA_ANALYZE_TEST_MODE=success"}})
	if !errors.Is(err, ErrAnalyzeRequest) {
		t.Fatalf("Analyze() error = %v", err)
	}
}

func TestAnalyzeRejectsTimeoutCrashIdentityAndExtraOutput(t *testing.T) {
	artifact := testAnalyzeArtifact(t)
	request := validAnalyzeRequest(t, artifact)
	for _, test := range []struct {
		mode string
		want error
	}{
		{mode: "timeout", want: ErrAnalyzeTimeout},
		{mode: "crash", want: ErrAnalyzeCrash},
		{mode: "mismatch", want: ErrAnalyzeIdentity},
		{mode: "extra", want: ErrAnalyzeResponse},
		{mode: "malformed", want: ErrAnalyzeResponse},
		{mode: "oversized", want: ErrAnalyzeFrameTooLarge},
	} {
		t.Run(test.mode, func(t *testing.T) {
			timeout := 2 * time.Second
			if test.mode == "timeout" {
				timeout = 30 * time.Millisecond
			}
			_, err := Analyze(context.Background(), artifact, request, AnalyzeOptions{Timeout: timeout, Environment: []string{"PLYSTRA_ANALYZE_TEST_MODE=" + test.mode}})
			if err == nil || !errors.Is(err, test.want) {
				t.Fatalf("Analyze() error = %v, want %v", err, test.want)
			}
		})
	}
}

func runAnalyzeTestCompiler(mode string) {
	if mode == "timeout" {
		time.Sleep(2 * time.Second)
		return
	}
	if mode == "crash" {
		os.Exit(17)
	}
	requestPayload, err := readAnalyzeFrame(os.Stdin)
	if err != nil {
		os.Exit(18)
	}
	var request struct {
		RequestID   string `json:"request_id"`
		InputDigest string `json:"input_digest"`
	}
	if err := json.Unmarshal(requestPayload, &request); err != nil {
		os.Exit(19)
	}
	if mode == "mismatch" {
		request.RequestID = "different-request"
	}
	if mode == "malformed" {
		_ = writeAnalyzeFrame(os.Stdout, []byte("not-json"))
		return
	}
	if mode == "oversized" {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(MaxFrameBytes+1))
		_, _ = os.Stdout.Write(header[:])
		return
	}
	status := analyzeStatusOK
	diagnostics := []any{}
	if mode == "rejected" {
		status = analyzeStatusReject
		diagnostics = []any{map[string]any{"code": "invalid-snapshot", "message": "rejected", "action": "correct input"}}
	}
	diagnosticBytes, _ := json.Marshal(diagnostics)
	digest := analyzeResultDigest(json.RawMessage(`[]`), diagnosticBytes, false)
	response := map[string]any{
		"schema": AnalyzeSchema, "phase": analyzePhase, "request_id": request.RequestID,
		"input_digest": request.InputDigest, "status": status,
		"output":      map[string]any{"roots": []any{}, "digest": digest, "truncated": false, "valid": status == analyzeStatusOK},
		"diagnostics": diagnostics, "truncated": false,
	}
	response["output_digest"] = digest
	data, err := json.Marshal(response)
	if err != nil || writeAnalyzeFrame(os.Stdout, data) != nil {
		os.Exit(20)
	}
	if mode == "extra" {
		_, _ = os.Stdout.Write([]byte{0})
	}
}
