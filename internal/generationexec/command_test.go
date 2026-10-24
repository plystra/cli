package generationexec

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestRunCommandStreamHarness(t *testing.T) {
	if os.Getenv("PLYSTRA_COMMAND_STREAM_HELPER") != "echo" {
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	if _, err := os.Stdout.Write(data); err != nil {
		os.Exit(3)
	}
	if _, err := os.Stderr.Write(data); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func TestRunCommandTransfersConcurrentStreams(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(strings.Repeat("input-output-", 128*1024))
	result := runCommand(t.Context(), executable, []string{"-test.run=^TestRunCommandStreamHarness$"}, "",
		append(os.Environ(), "PLYSTRA_COMMAND_STREAM_HELPER=echo"), data, len(data)/2, len(data))
	if result.err != nil || !result.stdoutExceeded || result.stderrExceeded || !bytes.Equal(result.stdout, data[:len(data)/2]) || !bytes.Equal(result.stderr, data) {
		t.Fatalf("command stream transfer changed: stdout=%d stderr=%d, %v", len(result.stdout), len(result.stderr), result.err)
	}
}
