package command

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCommandOutputRoutesHumanAndJSONStreams(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		format     commandFormat
		wantStdout string
		wantStderr string
	}{
		{name: "human", format: commandFormatHuman, wantStdout: "result\nprogress\n", wantStderr: "diagnostic\n"},
		{name: "json", format: commandFormatJSON, wantStdout: "result\n", wantStderr: "progress\ndiagnostic\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			output, err := newCommandOutput(test.format, &stdout, &stderr)
			if err != nil {
				t.Fatalf("newCommandOutput: %v", err)
			}
			if output.format != test.format {
				t.Fatalf("format = %q, want %q", output.format, test.format)
			}
			for _, write := range []struct {
				writer io.Writer
				value  string
			}{
				{writer: output.resultWriter(), value: "result\n"},
				{writer: output.progressWriter(), value: "progress\n"},
				{writer: output.diagnosticWriter(), value: "diagnostic\n"},
			} {
				writer, value := write.writer, write.value
				if _, err := io.WriteString(writer, value); err != nil {
					t.Fatalf("write %q: %v", strings.TrimSpace(value), err)
				}
			}
			if stdout.String() != test.wantStdout || stderr.String() != test.wantStderr {
				t.Fatalf("streams = stdout %q stderr %q; want stdout %q stderr %q", stdout.String(), stderr.String(), test.wantStdout, test.wantStderr)
			}
		})
	}
}

func TestCommandOutputRejectsIncompleteOrUnknownConfiguration(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	for _, test := range []struct {
		name   string
		format commandFormat
		stdout io.Writer
		stderr io.Writer
		want   string
	}{
		{name: "nil stdout", format: commandFormatHuman, stderr: &output, want: "stdout"},
		{name: "nil stderr", format: commandFormatHuman, stdout: &output, want: "stderr"},
		{name: "unknown format", format: "yaml", stdout: &output, stderr: &output, want: "format"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, err := newCommandOutput(test.format, test.stdout, test.stderr)
			if !errors.Is(err, errCommandOutput) || !strings.Contains(err.Error(), test.want) || result != (commandOutput{}) {
				t.Fatalf("newCommandOutput = %#v, %v; want zero output and errCommandOutput containing %q", result, err, test.want)
			}
		})
	}
}
