package newproject_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/privatefile"
	"github.com/plystra/cli/internal/runtimebaseline"
)

func TestPublicGenerateBuildsWithoutTests(t *testing.T) {
	testPublicGenerationBuild(t, false)
}

func TestPublicAddBuildsWithoutTests(t *testing.T) {
	testPublicGenerationBuild(t, true)
}

func testPublicGenerationBuild(t *testing.T, add bool) {
	t.Helper()
	proxy := createKernelProxy(t)
	const dependencyPath = "example.com/acme/dependency"
	writeProxyModule(t, proxy, dependencyPath, "v1.0.0", map[string][]byte{
		"plystra.yaml":  []byte("{}\n"),
		"dependency.go": []byte("package dependency\n"),
	})
	environment, log := observePublicGo(t, isolatedGoEnvironment(t, proxy))
	parent := t.TempDir()
	var stdout, stderr bytes.Buffer
	if exit := command.RunIn([]string{"new", "my-app", "--module", "example.com/acme/my-app", "--no-agent-guidance"}, &stdout, &stderr, parent, environment); exit != 0 {
		t.Fatalf("bootstrap: exit=%d stderr=%q", exit, stderr.String())
	}
	if !add {
		if err := gocommand.Run(t.Context(), gocommand.Options{Directory: filepath.Join(parent, "my-app"), Environment: environment}, "mod", "download", "all"); err != nil {
			t.Fatalf("prepare offline inputs: %v", err)
		}
	}
	base := snapshotTree(t, filepath.Join(parent, "my-app"))
	modes := []string{"online"}
	if !add {
		modes = append(modes, "offline")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			for _, fail := range []bool{false, true} {
				t.Run(fmt.Sprintf("build-fails-%t", fail), func(t *testing.T) {
					root := t.TempDir()
					for path, data := range base {
						file := filepath.Join(root, filepath.FromSlash(path))
						if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
							t.Fatal(err)
						}
						if path == runtimebaseline.Path {
							private, err := privatefile.Create(file)
							if err != nil {
								t.Fatal(err)
							}
							_, writeErr := private.Write(data)
							closeErr := private.Close()
							if writeErr != nil || closeErr != nil {
								t.Fatalf("copy private baseline: %v, %v", writeErr, closeErr)
							}
						} else {
							writeTestFile(t, file, data)
						}
					}
					// Require generated output installation so failure must undo it.
					generated := filepath.Join(root, "generated", "go", "assembly", "providers_gen.go")
					if err := os.Remove(generated); err != nil {
						t.Fatal(err)
					}
					before := snapshotTree(t, root)
					if err := os.WriteFile(log, nil, 0o600); err != nil {
						t.Fatal(err)
					}
					selectedEnvironment := environment
					if fail {
						selectedEnvironment = setEnvironmentValue(environment, "PLYSTRA_TEST_BREAK_BUILD", "1")
					}
					arguments := []string{"generate"}
					if mode == "offline" {
						arguments = append(arguments, "--offline")
					}
					if add {
						arguments = []string{"add", dependencyPath + "@v1.0.0"}
					}
					stdout.Reset()
					stderr.Reset()
					exit := command.RunIn(arguments, &stdout, &stderr, root, selectedEnvironment)
					builds := 0
					for _, invocation := range readGoInvocations(t, log) {
						if reflect.DeepEqual(invocation.Arguments, []string{"build", "-mod=readonly", "./..."}) {
							builds++
						}
					}
					if builds != 1 {
						t.Fatalf("%v: builds=%d exit=%d stderr=%q", arguments, builds, exit, stderr.String())
					}
					if fail {
						if exit == 0 || !strings.Contains(stderr.String(), "missingBuildValidationSymbol") {
							t.Fatalf("%v failed build: exit=%d stdout=%q stderr=%q", arguments, exit, stdout.String(), stderr.String())
						}
						if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
							t.Fatal("build failure did not restore module, generated output, and private baseline")
						}
					} else {
						if exit != 0 || stderr.Len() != 0 {
							t.Fatalf("%v: exit=%d stdout=%q stderr=%q", arguments, exit, stdout.String(), stderr.String())
						}
						assertPathPresence(t, generated, true)
						if add {
							assertDirectRequirement(t, root, dependencyPath, "v1.0.0")
						}
					}
					if _, err := runtimebaseline.Read(filepath.Join(root, runtimebaseline.Path)); err != nil {
						t.Fatalf("baseline lost private permissions or valid content: %v", err)
					}
					assertNoTransactionFiles(t, root)
				})
			}
		})
	}
}
