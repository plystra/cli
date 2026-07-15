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
		if len(os.Args) == 3 && os.Args[1] == "test" && os.Args[2] == "./..." && samePath(mustGetwd(), os.Getenv("EXPECTED_DIRECTORY")) {
			os.Exit(0)
		}
		os.Exit(3)
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
	if got := sanitizeOutput(input, staging); got != want {
		t.Fatalf("sanitizeOutput() = %q, want %q", got, want)
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
	return strings.EqualFold(strings.TrimRight(left, `\/`), strings.TrimRight(right, `\/`))
}
