package newproject_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
)

type goInvocation struct {
	Arguments []string
	Directory string
}

// runObservedGo delegates every permitted command to the real toolchain. Tests
// are rejected, never replaced by a successful result.
func runObservedGo() int {
	directory, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	log, err := os.OpenFile(os.Getenv("PLYSTRA_TEST_GO_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		panic(err)
	}
	err = json.NewEncoder(log).Encode(goInvocation{os.Args[1:], directory})
	closeErr := log.Close()
	if err != nil || closeErr != nil {
		panic(errors.Join(err, closeErr))
	}
	if len(os.Args) > 1 && os.Args[1] == "test" {
		fmt.Fprintln(os.Stderr, "unexpected application test invocation")
		return 97
	}
	if reflect.DeepEqual(os.Args[1:], []string{"build", "-mod=readonly", "./..."}) && os.Getenv("PLYSTRA_TEST_BREAK_BUILD") == "1" {
		// An unrelated package must also compile, not just the generated entry point.
		probe := filepath.Join(directory, "buildvalidation")
		if err := os.MkdirAll(probe, 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(probe, "probe.go"), []byte("package buildvalidation\nvar _ = missingBuildValidationSymbol\n"), 0o644); err != nil {
			panic(err)
		}
	}
	process := exec.Command(os.Getenv("PLYSTRA_TEST_REAL_GO"), os.Args[1:]...)
	process.Stdin, process.Stdout, process.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := process.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 98
	}
	return 0
}

func observePublicGo(t *testing.T, environment []string) ([]string, string) {
	t.Helper()
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, filepath.Base(realGo)), data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := filepath.Join(t.TempDir(), "go.jsonl")
	for key, value := range map[string]string{
		"PATH": os.Getenv("PATH"), "PLYSTRA_TEST_REAL_GO": realGo,
		"PLYSTRA_TEST_GO_LOG": log, "GORACE": "atexit_sleep_ms=0",
	} {
		environment = setEnvironmentValue(environment, key, value)
	}
	return environment, log
}

func readGoInvocations(t *testing.T, log string) []goInvocation {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var invocations []goInvocation
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var invocation goInvocation
		if err := json.Unmarshal(line, &invocation); err != nil {
			t.Fatal(err)
		}
		if len(invocation.Arguments) > 0 && invocation.Arguments[0] == "test" {
			t.Fatalf("creation invoked application tests: %v", invocation.Arguments)
		}
		invocations = append(invocations, invocation)
	}
	return invocations
}

func TestPublicNewBuildsStagedProjectWithoutTests(t *testing.T) {
	proxy := createKernelProxy(t)
	const templatePath = "example.com/acme/platform"
	writeProxyModule(t, proxy, templatePath, "v1.0.0", map[string][]byte{
		"plystra.yaml": []byte("interfaces:\n  require: [ghost.missing/v1]\n"),
		"platform.go":  []byte("package platform\n"),
		"template.txt": []byte("not copied\n"),
	})
	environment := isolatedGoEnvironment(t, proxy)
	for _, route := range []struct {
		name      string
		arguments []string
		builds    int
	}{
		{"plain", nil, 1},
		{"plugin", []string{"--plugin", "account"}, 1},
		{"template", []string{"--template", templatePath + "@v1.0.0"}, 1},
		{"plugin-template", []string{"--plugin", "account", "--template", templatePath + "@v1.0.0"}, 2},
	} {
		t.Run(route.name, func(t *testing.T) {
			for _, fail := range []bool{false, true} {
				t.Run(fmt.Sprintf("build-fails-%t", fail), func(t *testing.T) {
					environment, log := observePublicGo(t, environment)
					if fail {
						environment = setEnvironmentValue(environment, "PLYSTRA_TEST_BREAK_BUILD", "1")
					}
					parent := t.TempDir()
					arguments := append([]string{"new", "my-app", "--module", "example.com/acme/my-app", "--no-agent-guidance"}, route.arguments...)
					var stdout, stderr bytes.Buffer
					exit := command.RunIn(arguments, &stdout, &stderr, parent, environment)
					builds := 0
					for _, invocation := range readGoInvocations(t, log) {
						if reflect.DeepEqual(invocation.Arguments, []string{"build", "-mod=readonly", "./..."}) {
							builds++
							if filepath.Dir(invocation.Directory) != parent || !strings.HasPrefix(filepath.Base(invocation.Directory), ".my-app.plystra-") {
								t.Fatalf("build ran outside creation stage: %s", invocation.Directory)
							}
						}
					}
					target := filepath.Join(parent, "my-app")
					if fail {
						if exit == 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "missingBuildValidationSymbol") || builds != 1 {
							t.Fatalf("failed build: exit=%d builds=%d stdout=%q stderr=%q", exit, builds, stdout.String(), stderr.String())
						}
						assertPathPresence(t, target, false)
					} else {
						if exit != 0 || stderr.Len() != 0 || builds != route.builds {
							t.Fatalf("creation: exit=%d builds=%d stdout=%q stderr=%q", exit, builds, stdout.String(), stderr.String())
						}
						assertPathPresence(t, filepath.Join(target, "generated", "go", "application", "main_gen.go"), true)
						if strings.Contains(route.name, "template") {
							assertDirectRequirement(t, target, templatePath, "v1.0.0")
							assertPathPresence(t, filepath.Join(target, "template.txt"), false)
							if !strings.Contains(stdout.String(), "Generated and built") {
								t.Fatalf("template output = %q", stdout.String())
							}
						}
						if strings.Contains(route.name, "plugin") {
							assertPathPresence(t, filepath.Join(target, "account", "plugin.go"), true)
						}
					}
					assertNoTransactionFiles(t, parent)
				})
			}
		})
	}
}
