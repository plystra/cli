package datacompiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

const defaultEmitTimeout = 30 * time.Second

var (
	ErrEmit         = errors.New("run Data emitter")
	ErrEmitTimeout  = errors.New("Data emitter timed out")
	ErrEmitCrash    = errors.New("Data emitter crashed")
	ErrEmitTooLarge = errors.New("Data emitter output exceeds the limit")
)

// Emit starts the selected compiler for one accepted, frozen request and
// independently validates the returned staged artifact set. It never writes
// to the Project filesystem.
func Emit(ctx context.Context, artifact Artifact, manifest Manifest, request []byte, options AnalyzeOptions) (EmitResponse, error) {
	if err := validateCompilerArtifact(artifact); err != nil {
		return EmitResponse{}, fmt.Errorf("%w: %w", ErrEmit, err)
	}
	envelope, err := validateEmitRequest(request, artifact, manifest)
	if err != nil {
		return EmitResponse{}, fmt.Errorf("%w: %w", ErrEmit, err)
	}
	var input bytes.Buffer
	if err := writeEmitFrame(&input, request); err != nil {
		return EmitResponse{}, fmt.Errorf("%w: %w", ErrEmit, err)
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultEmitTimeout
	}
	executionContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(executionContext, artifact.Path)
	command.Dir = filepath.Dir(artifact.Path)
	command.Env = compilerEnvironment(options.Environment)
	command.Stdin = bytes.NewReader(input.Bytes())
	stdout := newAnalyzeBuffer(manifest.Bounds.MaxFrameBytes + 4)
	stderr := newAnalyzeBuffer(maximumCompilerStderr)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if errors.Is(executionContext.Err(), context.DeadlineExceeded) {
			return EmitResponse{}, fmt.Errorf("%w: %w", ErrEmit, ErrEmitTimeout)
		}
		if executionContext.Err() != nil {
			return EmitResponse{}, fmt.Errorf("%w: %w", ErrEmit, executionContext.Err())
		}
		return EmitResponse{}, fmt.Errorf("%w: %w", ErrEmit, ErrEmitCrash)
	}
	if errors.Is(executionContext.Err(), context.DeadlineExceeded) {
		return EmitResponse{}, fmt.Errorf("%w: %w", ErrEmit, ErrEmitTimeout)
	}
	if stdout.Exceeded() || stderr.Exceeded() {
		return EmitResponse{}, fmt.Errorf("%w: %w", ErrEmit, ErrEmitTooLarge)
	}
	reader := bytes.NewReader(stdout.Bytes())
	responsePayload, err := readEmitFrame(reader, manifest.Bounds.MaxFrameBytes)
	if err != nil {
		return EmitResponse{}, fmt.Errorf("%w: %w", ErrEmit, err)
	}
	if reader.Len() != 0 {
		return EmitResponse{}, fmt.Errorf("%w: extra stdout after terminal frame", ErrEmit)
	}
	response, err := validateEmitResponse(responsePayload, envelope, manifest)
	if err != nil {
		return EmitResponse{}, fmt.Errorf("%w: %w", ErrEmit, err)
	}
	return response, nil
}
