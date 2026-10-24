package processcommand

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMain(main *testing.M) {
	if os.Getenv("PLYSTRA_PROCESS_COMMAND_TERMINATOR") == "hang" && len(os.Args) > 1 && os.Args[1] == "/T" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(main.Run())
}

func TestProcessCommandHarness(t *testing.T) {
	switch os.Getenv("PLYSTRA_PROCESS_COMMAND_HELPER") {
	case "":
		return
	case "exit":
		os.Exit(0)
	case "wait":
		if err := os.WriteFile(os.Getenv("READY_FILE"), nil, 0o600); err != nil {
			os.Exit(2)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	case "stdout", "stderr":
		stream := os.Stdout
		if os.Getenv("PLYSTRA_PROCESS_COMMAND_HELPER") == "stderr" {
			stream = os.Stderr
		}
		for range 1024 {
			if _, err := io.WriteString(stream, strings.Repeat("x", 4096)); err != nil {
				os.Exit(0)
			}
		}
		os.Exit(0)
	default:
		os.Exit(3)
	}
}

type rejectingWriter struct{ err error }

func (writer rejectingWriter) Write([]byte) (int, error) { return 0, writer.err }

func TestRunClosesPipeAfterCopyFailure(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			t.Parallel()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestProcessCommandHarness$")
			command.Env = append(os.Environ(), "PLYSTRA_PROCESS_COMMAND_HELPER="+stream)
			command.Stdout, command.Stderr = io.Discard, io.Discard
			rejected := errors.New("writer rejected output")
			if stream == "stdout" {
				command.Stdout = rejectingWriter{rejected}
			} else {
				command.Stderr = rejectingWriter{rejected}
			}
			before := time.Now()
			started, err := Run(command)
			if !started || !errors.Is(err, rejected) || !errors.Is(err, ErrUnsettled) || time.Since(before) >= 3*time.Second || ctx.Err() != nil {
				t.Fatalf("copy failure did not release the child: started=%t, %v", started, err)
			}
		})
	}
}
