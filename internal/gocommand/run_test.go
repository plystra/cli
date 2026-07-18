package gocommand

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	case "gowork":
		got, exists := os.LookupEnv("GOWORK")
		want := os.Getenv("EXPECTED_GOWORK")
		if want == "<unset>" && exists || want != "<unset>" && (!exists || got != want) {
			_, _ = fmt.Fprintf(os.Stderr, "GOWORK = %q, present %t; want %q\n", got, exists, want)
			os.Exit(3)
		}
		os.Exit(0)
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

func TestRunIsolatesModulesExcludedFromAnImplicitParentWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	included := filepath.Join(root, "included")
	excluded := filepath.Join(root, "excluded")
	for _, directory := range []string{included, excluded, filepath.Join(included, "nested"), filepath.Join(excluded, "nested")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", directory, err)
		}
	}
	for _, directory := range []string{included, excluded} {
		if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte("module example.com/"+filepath.Base(directory)+"\n\ngo 1.26\n"), 0o644); err != nil {
			t.Fatalf("WriteFile(go.mod): %v", err)
		}
	}
	workspacePath := filepath.Join(root, "go.work")
	if err := os.WriteFile(workspacePath, []byte("go 1.26\n\nuse ./included\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(go.work): %v", err)
	}
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	baseEnvironment := withoutEnvironmentKey(os.Environ(), "GOWORK")
	tests := []struct {
		name      string
		directory string
		explicit  string
		want      string
	}{
		{name: "included", directory: filepath.Join(included, "nested"), want: "<unset>"},
		{name: "excluded", directory: filepath.Join(excluded, "nested"), want: "off"},
		{name: "explicit", directory: filepath.Join(excluded, "nested"), explicit: workspacePath, want: workspacePath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environment := append(append([]string(nil), baseEnvironment...),
				"PLYSTRA_GO_COMMAND_HELPER=gowork",
				"EXPECTED_GOWORK="+test.want,
			)
			if test.explicit != "" {
				environment = append(environment, "GOWORK="+test.explicit)
			}
			if err := Run(t.Context(), Options{Command: command, Directory: test.directory, Environment: environment}, "test", "./..."); err != nil {
				t.Fatalf("Run: %v", err)
			}
		})
	}
}

func TestRunDoesNotMaskInvalidImplicitParentWorkspace(t *testing.T) {
	t.Parallel()

	command, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	baseEnvironment := withoutEnvironmentKey(os.Environ(), "GOWORK")
	tests := []struct {
		name         string
		workspace    string
		usedGoMod    string
		createUseDir bool
	}{
		{name: "malformed workspace", workspace: "go 1.26\n\nuse (\n"},
		{name: "missing used module", workspace: "go 1.26\n\nuse ./included\n"},
		{
			name:         "invalid used module",
			workspace:    "go 1.26\n\nuse ./included\n",
			usedGoMod:    "module invalid path\n\ngo 1.26\n",
			createUseDir: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			excluded := filepath.Join(root, "excluded")
			if err := os.MkdirAll(filepath.Join(excluded, "nested"), 0o755); err != nil {
				t.Fatalf("MkdirAll(excluded): %v", err)
			}
			if err := os.WriteFile(filepath.Join(excluded, "go.mod"), []byte("module example.com/excluded\n\ngo 1.26\n"), 0o644); err != nil {
				t.Fatalf("WriteFile(excluded go.mod): %v", err)
			}
			if test.createUseDir {
				included := filepath.Join(root, "included")
				if err := os.MkdirAll(included, 0o755); err != nil {
					t.Fatalf("MkdirAll(included): %v", err)
				}
				if err := os.WriteFile(filepath.Join(included, "go.mod"), []byte(test.usedGoMod), 0o644); err != nil {
					t.Fatalf("WriteFile(included go.mod): %v", err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "go.work"), []byte(test.workspace), 0o644); err != nil {
				t.Fatalf("WriteFile(go.work): %v", err)
			}
			environment := append(append([]string(nil), baseEnvironment...),
				"PLYSTRA_GO_COMMAND_HELPER=gowork",
				"EXPECTED_GOWORK=<unset>",
			)
			if err := Run(t.Context(), Options{Command: command, Directory: filepath.Join(excluded, "nested"), Environment: environment}, "test", "./..."); err != nil {
				t.Fatalf("Run: %v", err)
			}
		})
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

func withoutEnvironmentKey(environment []string, unwanted string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, unwanted) {
			result = append(result, entry)
		}
	}
	return result
}
