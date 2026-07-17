package gocommand

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestMain(main *testing.M) {
	switch os.Getenv("PLYSTRA_GO_COMMAND_HELPER") {
	case "success":
		if len(os.Args) != 3 || os.Args[1] != "test" || os.Args[2] != "./..." {
			_, _ = fmt.Fprintln(os.Stderr, "unexpected helper arguments")
			os.Exit(3)
		}
		if !samePath(mustGetwd(), os.Getenv("EXPECTED_DIRECTORY")) {
			_, _ = fmt.Fprintln(os.Stderr, "helper working directory differs from expected directory")
			os.Exit(3)
		}
		os.Exit(0)
	case "output":
		if len(os.Args) != 4 || os.Args[1] != "list" || os.Args[2] != "-m" || os.Args[3] != "example.com/plugin" {
			_, _ = fmt.Fprintln(os.Stderr, "unexpected output helper arguments")
			os.Exit(3)
		}
		_, _ = fmt.Fprintln(os.Stdout, `{"Path":"example.com/plugin"}`)
		_, _ = fmt.Fprintln(os.Stderr, "non-fatal go warning")
		os.Exit(0)
	case "oversized":
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 1025))
		os.Exit(0)
	case "failure":
		_, _ = fmt.Fprintln(os.Stderr, "go: reading https://person:secret@example.com/private?token=secret: denied")
		_, _ = fmt.Fprintln(os.Stderr, strings.Repeat(mustGetwd()+" ", 300))
		os.Exit(4)
	}
	os.Exit(main.Run())
}

func TestSanitizeOutput(t *testing.T) {
	t.Parallel()

	const staging = `C:\Users\person\workspace\.app.plystra-123`
	input := "go: reading https://person:secret@example.com/private?token=secret: denied\n" + staging + `\go.mod`
	want := "go: reading <redacted-url> denied\n.\\go.mod"
	if got := SanitizeOutput(input, staging); got != want {
		t.Fatalf("SanitizeOutput() = %q, want %q", got, want)
	}
}

func TestRunUsesDirectoryEnvironmentAndArguments(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	environment := append(os.Environ(), "PLYSTRA_GO_COMMAND_HELPER=success", "EXPECTED_DIRECTORY="+directory)
	if err := Run(context.Background(), Options{Command: command, Directory: directory, Environment: environment}, "test", "./..."); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunReportsFailuresWithoutLeakingURLsOrLongOutput(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	command, executableErr := os.Executable()
	if executableErr != nil {
		t.Fatalf("Executable: %v", executableErr)
	}
	err := Run(context.Background(), Options{
		Command:     command,
		Directory:   directory,
		Environment: append(os.Environ(), "PLYSTRA_GO_COMMAND_HELPER=failure"),
	}, "test", "./...")
	if !errors.Is(err, ErrRun) {
		t.Fatalf("Run error = %v, want ErrRun", err)
	}
	message := err.Error()
	if strings.Contains(message, directory) || strings.Contains(message, "person:secret") {
		t.Fatalf("Run error leaked private command details: %v", err)
	}
	if !strings.Contains(message, "<redacted-url>") || len(message) > 4200 {
		t.Fatalf("Run error was not redacted and bounded: length %d, value %q", len(message), message)
	}
}

func TestOutputReturnsBoundedStdout(t *testing.T) {
	t.Parallel()

	command, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	output, err := Output(context.Background(), Options{
		Command:     command,
		Directory:   t.TempDir(),
		Environment: append(os.Environ(), "PLYSTRA_GO_COMMAND_HELPER=output"),
		OutputLimit: 1024,
	}, "list", "-m", "example.com/plugin")
	if err != nil || string(output) != "{\"Path\":\"example.com/plugin\"}\n" {
		t.Fatalf("Output = %q, %v", output, err)
	}
	output[0] = 'x'
	if string(output) == "{\"Path\":\"example.com/plugin\"}\n" {
		t.Fatal("test did not mutate returned output")
	}
}

func TestOutputRejectsOversizedStdout(t *testing.T) {
	t.Parallel()

	command, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	output, err := Output(context.Background(), Options{
		Command:     command,
		Environment: append(os.Environ(), "PLYSTRA_GO_COMMAND_HELPER=oversized"),
		OutputLimit: 1024,
	}, "list")
	if !errors.Is(err, ErrOutputTooLarge) || output != nil {
		t.Fatalf("Output = %q, %v, want ErrOutputTooLarge", output, err)
	}
}

func TestOutputReportsRedactedFailure(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	output, err := Output(context.Background(), Options{
		Command:     command,
		Directory:   directory,
		Environment: append(os.Environ(), "PLYSTRA_GO_COMMAND_HELPER=failure"),
	}, "list", "-m")
	if !errors.Is(err, ErrRun) || output != nil || strings.Contains(err.Error(), directory) || strings.Contains(err.Error(), "person:secret") || !strings.Contains(err.Error(), "<redacted-url>") {
		t.Fatalf("Output = %q, %v", output, err)
	}
}

func TestRunHonorsCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command, executableErr := os.Executable()
	if executableErr != nil {
		t.Fatalf("Executable: %v", executableErr)
	}
	err := Run(ctx, Options{Command: command}, "test", "./...")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func mustGetwd() string {
	directory, err := os.Getwd()
	if err != nil {
		os.Exit(5)
	}
	return directory
}

func samePath(left, right string) bool {
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
