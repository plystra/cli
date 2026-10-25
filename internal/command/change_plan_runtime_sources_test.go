package command_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"github.com/plystra/cli/internal/scaffoldplan"
)

func TestScaffoldPreviewPreparesRuntimeBeforeNormalization(t *testing.T) {
	command := sourcePreparationGoWrapper(t)
	for _, mode := range []string{"order", "preparation-failure"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root, environment, marker, _ := generatedRuntimeSourceFixture(t, mode, false)
			request, err := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"todo.add","semantics":"command"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			before, parent := commandTree(t, root), t.TempDir()
			planned, err := scaffoldplan.Preview(t.Context(), scaffoldplan.Options{Start: root, Environment: environment, GoCommand: command, TemporaryParent: parent}, request)
			want := "runtime source prepared before get\nruntime get completed\n"
			if mode == "preparation-failure" {
				want = "runtime preparation failed before get\n"
			}
			if data, markerErr := os.ReadFile(marker); markerErr != nil || string(data) != want {
				t.Fatalf("runtime preparation ordering: %q, %v; preview=%v", data, markerErr, err)
			}
			if mode == "preparation-failure" {
				if !errors.Is(err, moduledependency.ErrSourceIntegrity) || planned.Capture().Valid() || planned.FinalModelDigest() != "" {
					t.Fatalf("runtime preparation failure was not retained: %v", err)
				}
			} else if err != nil || !planned.Capture().Valid() {
				t.Fatalf("prepared runtime source did not normalize: %v", err)
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("runtime preparation changed the authored Project")
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatal("runtime preparation retained private stage files", err)
			}
		})
	}
}

func TestScaffoldPreviewRetainsGeneratedRuntimeSources(t *testing.T) {
	command := sourcePreparationGoWrapper(t)
	for _, test := range []struct {
		mode      string
		workspace bool
	}{
		{mode: "manifest"}, {mode: "source"}, {mode: "manifest", workspace: true}, {mode: "unchanged"},
	} {
		t.Run(fmt.Sprintf("%s/workspace=%t", test.mode, test.workspace), func(t *testing.T) {
			t.Parallel()
			root, environment, marker, path := generatedRuntimeSourceFixture(t, test.mode, test.workspace)
			request, err := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"todo.add","semantics":"command"}]}`))
			if err != nil {
				t.Fatal(err)
			}
			before, parent := commandTree(t, root), t.TempDir()
			planned, err := scaffoldplan.Preview(t.Context(), scaffoldplan.Options{Start: root, Environment: environment, GoCommand: command, TemporaryParent: parent}, request)
			if test.mode == "unchanged" {
				if err != nil || !planned.Capture().Valid() {
					t.Fatalf("unchanged runtime source rejected: %v", err)
				}
				if err := planned.Capture().Recheck(t.Context()); err != nil {
					t.Fatal("runtime inputs did not survive cleanup", err)
				}
				if err := os.Rename(path, path+".prior"); err != nil {
					t.Fatal(err)
				}
				if err := planned.Capture().Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("runtime metadata was not retained after cleanup: %v", err)
				}
			} else {
				if data, markerErr := os.ReadFile(marker); markerErr != nil || string(data) != "runtime input changed after get\n" {
					t.Fatalf("runtime get did not exercise the cache replacement: %v, preview=%v", markerErr, err)
				}
				if !errors.Is(err, projectstage.ErrConcurrentChange) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || planned.Capture().Valid() || planned.FinalModelDigest() != "" {
					t.Fatalf("runtime cache replacement was accepted: %v", err)
				}
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("runtime source preparation changed the authored Project")
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatal("runtime source preview retained private stage files", err)
			}
		})
	}
}

func TestBuiltCLIChangePlanRetainsGeneratedRuntimeSources(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironment()
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root, environment, marker, path := generatedRuntimeSourceFixture(t, "information", false)
	wrapper := sourcePreparationGoWrapper(t)
	goName := "go"
	if runtime.GOOS == "windows" {
		goName += ".exe"
	}
	if err := os.Rename(wrapper, filepath.Join(filepath.Dir(wrapper), goName)); err != nil {
		t.Fatal(err)
	}
	for index, entry := range environment {
		if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "PATH") {
			environment[index] = name + "=" + filepath.Dir(wrapper) + string(os.PathListSeparator) + value
		}
	}
	before := commandTree(t, root)
	command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json")
	command.Dir, command.Env = filepath.Join(root, "reports"), environment
	command.Stdin = strings.NewReader(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"todo.add","semantics":"command"}]}`)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	var result scaffoldPlanDocument
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 3 || stderr.Len() != 0 || json.Unmarshal(output, &result) != nil || result.Status != "validation_failed" || result.Payload.PlanDigest != "" {
		t.Fatalf("compiled CLI accepted runtime source drift: %v\n%s\n%s", err, output, &stderr)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "runtime input changed after get\n" {
		t.Fatal("compiled CLI did not exercise runtime input drift", err)
	}
	for _, private := range []string{root, path, filepath.Dir(marker)} {
		for _, spelling := range []string{private, filepath.ToSlash(private)} {
			encoded, _ := json.Marshal(spelling)
			if bytes.Contains(output, encoded[1:len(encoded)-1]) {
				t.Fatal("compiled CLI disclosed private runtime input paths")
			}
		}
	}
	if !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("compiled runtime preview changed the Project")
	}
	assertNoCommandTransactions(t, root)
}

func generatedRuntimeSourceFixture(t *testing.T, mode string, workspace bool) (string, []string, string, string) {
	t.Helper()
	root := writeImplementationSelectionCommandProject(t)
	environment := previewDependencyEnvironment(t, writeCommandDependencyProxy(t, nil))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\nhttp:\n  expose:\n    kernel.health/v1: {transport: connect}\n")
	// These cases replace an already materialized runtime input. Prepare the
	// unrelated baseline graph so cold acquisition remains a separate fixture.
	if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "download", "all"); err != nil {
		t.Fatal(err)
	}
	if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "download", "connectrpc.com/connect@v1.20.0"); err != nil {
		t.Fatal(err)
	}
	var cache string
	for _, entry := range environment {
		if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "GOMODCACHE") {
			cache = value
		}
	}
	if cache == "" {
		t.Fatal("missing isolated Module Cache")
	}
	path := filepath.Join(cache, "cache", "download", "connectrpc.com", "connect", "@v", "v1.20.0.mod")
	if mode == "information" {
		path = strings.TrimSuffix(path, ".mod") + ".info"
	} else if mode == "source" {
		path = filepath.Join(cache, "connectrpc.com", "connect@v1.20.0", "go.mod")
	}
	if workspace {
		workfile := filepath.Join(t.TempDir(), "go.work")
		writeCommandFile(t, workfile, "go 1.26\nuse "+fmt.Sprintf("%q", filepath.ToSlash(root))+"\n")
		for index, entry := range environment {
			if name, _, _ := strings.Cut(entry, "="); strings.EqualFold(name, "GOWORK") {
				environment[index] = name + "=" + workfile
			}
		}
	}
	marker := filepath.Join(t.TempDir(), "runtime-get")
	environment = append(environment, "PLYSTRA_SOURCE_PREPARATION_PATH="+path, "PLYSTRA_SOURCE_PREPARATION_MARKER="+marker, "PLYSTRA_SOURCE_PREPARATION_MODE=runtime-"+mode)
	return root, environment, marker, path
}
