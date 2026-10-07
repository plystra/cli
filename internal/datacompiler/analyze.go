package datacompiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultAnalyzeTimeout = 30 * time.Second
	maximumCompilerStderr = 64 << 10
)

var (
	// ErrAnalyze identifies failure to execute or validate one analyze call.
	ErrAnalyze = errors.New("run Data analyzer")
	// ErrAnalyzeTimeout identifies a compiler that exceeded its execution
	// budget or a caller context that expired while it was running.
	ErrAnalyzeTimeout = errors.New("Data analyzer timed out")
	// ErrAnalyzeCrash identifies a compiler that exited abnormally or without
	// a terminal protocol response.
	ErrAnalyzeCrash = errors.New("Data analyzer crashed")
	// ErrAnalyzeOutputTooLarge identifies bounded stdout or stderr overflow.
	ErrAnalyzeOutputTooLarge = errors.New("Data analyzer output exceeds the limit")
)

// AnalyzeOptions controls one bounded Data compiler analyze invocation.
type AnalyzeOptions struct {
	Timeout     time.Duration
	Environment []string
	Observe     ObservationSink
}

// Analyze starts the selected compiler, sends one immutable request frame,
// and validates exactly one terminal response without importing Data.
func Analyze(ctx context.Context, artifact Artifact, request []byte, options AnalyzeOptions) (AnalyzeResponse, error) {
	if err := validateCompilerArtifact(artifact); err != nil {
		return AnalyzeResponse{}, fmt.Errorf("%w: %w", ErrAnalyze, err)
	}
	envelope, err := validateAnalyzeRequest(request, artifact)
	if err != nil {
		return AnalyzeResponse{}, fmt.Errorf("%w: %w", ErrAnalyze, err)
	}
	var input bytes.Buffer
	if err := writeAnalyzeFrame(&input, request); err != nil {
		return AnalyzeResponse{}, fmt.Errorf("%w: %w", ErrAnalyze, err)
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultAnalyzeTimeout
	}
	executionContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(executionContext, artifact.Path)
	command.Dir = filepath.Dir(artifact.Path)
	command.Env = compilerEnvironment(options.Environment)
	command.Stdin = bytes.NewReader(input.Bytes())
	stdout := newAnalyzeBuffer(MaxFrameBytes + 4)
	stderr := newAnalyzeBuffer(maximumCompilerStderr)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		if errors.Is(executionContext.Err(), context.DeadlineExceeded) {
			return AnalyzeResponse{}, fmt.Errorf("%w: %w", ErrAnalyze, ErrAnalyzeTimeout)
		}
		if executionContext.Err() != nil {
			return AnalyzeResponse{}, fmt.Errorf("%w: %w", ErrAnalyze, executionContext.Err())
		}
		return AnalyzeResponse{}, fmt.Errorf("%w: %w", ErrAnalyze, ErrAnalyzeCrash)
	}
	recordObservation(options.Observe, Observation{
		ID: "data-compiler-analyze-execution", Class: ObservationTrustedExecution,
		Phase: "analyze", Target: compilerObservationTarget(artifact.ManifestDigest, artifact.GOOS, artifact.GOARCH),
		Reason: "trusted_compiler_analyze", Reversibility: "none",
		Verification: []string{"plystra", "generate", "--check"},
	})
	if err := command.Wait(); err != nil {
		if errors.Is(executionContext.Err(), context.DeadlineExceeded) {
			return AnalyzeResponse{}, fmt.Errorf("%w: %w", ErrAnalyze, ErrAnalyzeTimeout)
		}
		if executionContext.Err() != nil {
			return AnalyzeResponse{}, fmt.Errorf("%w: %w", ErrAnalyze, executionContext.Err())
		}
		return AnalyzeResponse{}, fmt.Errorf("%w: %w", ErrAnalyze, ErrAnalyzeCrash)
	}
	if errors.Is(executionContext.Err(), context.DeadlineExceeded) {
		return AnalyzeResponse{}, fmt.Errorf("%w: %w", ErrAnalyze, ErrAnalyzeTimeout)
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return AnalyzeResponse{}, fmt.Errorf("%w: %w", ErrAnalyze, ErrAnalyzeOutputTooLarge)
	}
	reader := bytes.NewReader(stdout.Bytes())
	responsePayload, err := readAnalyzeFrame(reader)
	if err != nil {
		return AnalyzeResponse{}, fmt.Errorf("%w: %w", ErrAnalyze, err)
	}
	if reader.Len() != 0 {
		return AnalyzeResponse{}, fmt.Errorf("%w: %w: extra stdout after terminal frame", ErrAnalyze, ErrAnalyzeResponse)
	}
	response, err := validateAnalyzeResponse(responsePayload, envelope)
	if err != nil {
		return AnalyzeResponse{}, fmt.Errorf("%w: %w", ErrAnalyze, err)
	}
	return response, nil
}

func validateCompilerArtifact(artifact Artifact) error {
	if artifact.Path == "" || !filepath.IsAbs(artifact.Path) || artifact.ModulePath != ModulePath || artifact.ModuleVersion == "" || !validModuleChecksum(artifact.ModuleChecksum) || !validDigest(artifact.ManifestDigest) || !validDigest(artifact.BinaryDigest) || artifact.GoToolchain == "" || artifact.GOOS == "" || artifact.GOARCH == "" {
		return ErrAnalyzeRequest
	}
	info, err := os.Lstat(artifact.Path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrAnalyzeRequest
	}
	digest, _, err := fileDigest(artifact.Path)
	if err != nil || digest != artifact.BinaryDigest {
		return ErrAnalyzeRequest
	}
	return nil
}

func compilerEnvironment(overrides []string) []string {
	environment := append([]string(nil), os.Environ()...)
	for _, entry := range overrides {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		environment = replaceEnvironment(environment, name, value)
	}
	return environment
}

type analyzeBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func newAnalyzeBuffer(limit int) *analyzeBuffer { return &analyzeBuffer{limit: limit} }

func (b *analyzeBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.exceeded = true
		return len(data), nil
	}
	if len(data) > remaining {
		_, _ = b.Buffer.Write(data[:remaining])
		b.exceeded = true
		return len(data), nil
	}
	return b.Buffer.Write(data)
}

func (b *analyzeBuffer) Bytes() []byte { return append([]byte(nil), b.Buffer.Bytes()...) }

func (b *analyzeBuffer) Exceeded() bool { return b.exceeded }

var _ io.Writer = (*analyzeBuffer)(nil)
