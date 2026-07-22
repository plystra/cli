package command

import (
	"errors"
	"fmt"
	"io"
)

var errCommandOutput = errors.New("configure command output")

type commandFormat string

const (
	commandFormatHuman commandFormat = "human"
	commandFormatJSON  commandFormat = "json"
)

// commandOutput owns the process-stream contract independently from command
// rendering. Structured results have exclusive stdout ownership; progress and
// human diagnostics share stderr without entering the schema document.
type commandOutput struct {
	format      commandFormat
	result      io.Writer
	progress    io.Writer
	diagnostics io.Writer
}

func newCommandOutput(format commandFormat, stdout, stderr io.Writer) (commandOutput, error) {
	if stdout == nil {
		return commandOutput{}, fmt.Errorf("%w: stdout is nil", errCommandOutput)
	}
	if stderr == nil {
		return commandOutput{}, fmt.Errorf("%w: stderr is nil", errCommandOutput)
	}
	switch format {
	case commandFormatHuman:
		return commandOutput{format: format, result: stdout, progress: stdout, diagnostics: stderr}, nil
	case commandFormatJSON:
		return commandOutput{format: format, result: stdout, progress: stderr, diagnostics: stderr}, nil
	default:
		return commandOutput{}, fmt.Errorf("%w: format %q is not supported", errCommandOutput, format)
	}
}

func (o commandOutput) resultWriter() io.Writer { return o.result }

func (o commandOutput) progressWriter() io.Writer { return o.progress }

func (o commandOutput) diagnosticWriter() io.Writer { return o.diagnostics }
