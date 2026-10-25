package command_test

import (
	"bytes"
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
	"github.com/plystra/cli/internal/projectstage"
	"github.com/plystra/cli/internal/scaffoldplan"
)

const fileProxyPreviewModule = "example.com/preview/file-origin"
const fileProxyPreviewRequest = `{"schema":"plystra.change/v1","operations":[{"kind":"dependency","action":"add","module":"` + fileProxyPreviewModule + `@v1.0.0"},{"kind":"interface_create","name":"todo.add","semantics":"command"}]}`

func fileProxyPreviewFixture(t *testing.T) (root, proxy string, environment []string) {
	t.Helper()
	return fileProxyPreviewFixtureVersion(t, "v1.0.0")
}

func fileProxyPreviewFixtureVersion(t *testing.T, version string) (root, proxy string, environment []string) {
	t.Helper()
	proxy = writeCommandDependencyProxy(t, []commandProxyModule{{path: fileProxyPreviewModule, version: version, ordinary: true}})
	root = writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
	environment = previewDependencyEnvironment(t, proxy)
	// Establish the real generated runtime and its transitive metadata first;
	// the requested module remains cold in this isolated consuming cache.
	if code, stdout, stderr := runCommand(t, []string{"generate"}, root, environment); code != 0 {
		t.Fatalf("prepare unrelated generated runtime: %d\n%s\n%s", code, stdout, stderr)
	}
	// Keep real runtime sources local so this test exercises cold acquisition
	// of the requested module without repeatedly authenticating unrelated
	// versioned runtime trees at every discovery callback.
	options := gocommand.Options{Directory: root, Environment: environment}
	if err := gocommand.Run(t.Context(), options, "mod", "download", "all"); err != nil {
		t.Fatal(err)
	}
	output, err := gocommand.Output(t.Context(), options, "list", "-m", "-f", "{{if and (not .Main) (not .Replace)}}{{.Path}}={{.Dir}}{{end}}", "all")
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{"mod", "edit"}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		path, directory, ok := strings.Cut(line, "=")
		if !ok || path == fileProxyPreviewModule || !filepath.IsAbs(directory) {
			t.Fatal("invalid unrelated runtime source")
		}
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); errors.Is(err, os.ErrNotExist) {
			continue // Pre-module sources need Go's synthesized cache manifest.
		} else if err != nil {
			t.Fatal(err)
		}
		arguments = append(arguments, "-replace="+path+"="+directory)
	}
	if err := gocommand.Run(t.Context(), options, arguments...); err != nil {
		t.Fatal(err)
	}
	for _, entry := range commandDependencyEnvironment(t, proxy) {
		if strings.HasPrefix(entry, "GOPROXY=") {
			environment = append(environment, entry)
		}
	}
	return root, proxy, environment
}

func TestScaffoldPreviewRetainsFileProxyOriginAfterCleanup(t *testing.T) {
	root, proxy, environment := fileProxyPreviewFixture(t)
	request, err := changerequest.Parse([]byte(fileProxyPreviewRequest))
	if err != nil {
		t.Fatal(err)
	}
	before, parent := commandTree(t, root), t.TempDir()
	planned, err := scaffoldplan.Preview(t.Context(), scaffoldplan.Options{Start: root, Environment: environment, TemporaryParent: parent}, request)
	if err != nil || !planned.Capture().Valid() {
		t.Fatal("cold file-proxy preview failed", err)
	}
	if err := planned.Capture().Recheck(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proxy, filepath.FromSlash(fileProxyPreviewModule), "@v", "v1.0.0.zip")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".prior"); err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, path, string(data))
	if err := planned.Capture().Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatal("preview retained only the later cache copy", err)
	}
	if !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("preview changed authored files")
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatal("preview retained its stage", err)
	}
	assertCandidateCaptureRedaction(t, planned, proxy)
}

func TestBuiltCLIChangePlanRejectsReplacedFileProxyOrigin(t *testing.T) {
	assertBuiltCLIChangePlanRejectsArchiveReplacement(t, false)
}

func TestBuiltCLIChangePlanRejectsReplacedCachedArchive(t *testing.T) {
	assertBuiltCLIChangePlanRejectsArchiveReplacement(t, true)
}

func TestBuiltCLIChangePlanIsolatesExactAcquisition(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, test := range []struct {
		name              string
		partial, exempted bool
	}{
		{name: "partial=false"},
		{name: "partial=true", partial: true},
		{name: "sumdb-exempt", exempted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root, proxy, environment := fileProxyPreviewFixture(t)
			cacheOutput, err := gocommand.Output(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "env", "GOMODCACHE")
			if err != nil {
				t.Fatal(err)
			}
			cache := strings.TrimSpace(string(cacheOutput))
			original := filepath.Join(cache, filepath.FromSlash(fileProxyPreviewModule)+"@v1.0.0")
			if test.partial {
				output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "download", "-json", fileProxyPreviewModule+"@v1.0.0")
				var downloaded struct{ Dir, Zip string }
				if err != nil || json.Unmarshal(output, &downloaded) != nil || downloaded.Dir != original || downloaded.Zip == "" {
					t.Fatal("seed partial source", err)
				}
				marker := strings.TrimSuffix(downloaded.Zip, ".zip") + ".partial"
				writeCommandFile(t, marker, "partial extraction\n")
				writeCommandFile(t, filepath.Join(original, "partial-only.go"), "invalid partial Go source\n")
				environment = append(environment, "PLYSTRA_SOURCE_PARTIAL_PATH="+marker)
			}
			if test.exempted {
				environment = append(environment, "GOSUMDB=invalid.example", "GONOSUMDB="+fileProxyPreviewModule,
					"PLYSTRA_SOURCE_EXPECTED_SUMDB=invalid.example")
			}
			wrapper := sourcePreparationGoWrapper(t)
			goName := "go"
			if runtime.GOOS == "windows" {
				goName += ".exe"
			}
			if err := os.Rename(wrapper, filepath.Join(filepath.Dir(wrapper), goName)); err != nil {
				t.Fatal(err)
			}
			for index, entry := range environment {
				if key, value, _ := strings.Cut(entry, "="); strings.EqualFold(key, "PATH") {
					environment[index] = key + "=" + filepath.Dir(wrapper) + string(os.PathListSeparator) + value
				}
			}
			marker := filepath.Join(t.TempDir(), "private-acquisition-observed")
			environment = append(environment, "PLYSTRA_SOURCE_PREPARATION_PATH="+original,
				"PLYSTRA_SOURCE_ORIGINAL_CACHE="+cache,
				"PLYSTRA_SOURCE_PREPARATION_MARKER="+marker,
				"PLYSTRA_SOURCE_PREPARATION_QUERY="+fileProxyPreviewModule+"@v1.0.0",
				"PLYSTRA_SOURCE_PREPARATION_MODE=isolated-acquisition")
			before := commandTree(t, root)
			process := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json")
			process.Dir, process.Env, process.Stdin = filepath.Join(root, "reports"), environment, strings.NewReader(fileProxyPreviewRequest)
			var stdout, stderr bytes.Buffer
			process.Stdout, process.Stderr = &stdout, &stderr
			commandErr := process.Run()
			var result scaffoldPlanDocument
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || commandErr != nil || stderr.Len() != 0 || result.Status != "success" || result.Payload.PlanDigest == "" {
				observation, _ := os.ReadFile(marker)
				t.Fatalf("public private-acquisition preview failed: %v, %v, acquisition=%q\n%s\n%s", commandErr, err, observation, &stdout, &stderr)
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "exact acquisition consumed a private cache\n" {
				t.Fatal("public preview did not isolate exact acquisition", err)
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("private-acquisition preview changed the Project")
			}
			for _, private := range []string{cache, original, proxy, "127.0.0.1"} {
				for _, spelling := range []string{private, filepath.ToSlash(private)} {
					encoded, err := json.Marshal(spelling)
					if err != nil || bytes.Contains(stdout.Bytes(), encoded[1:len(encoded)-1]) {
						t.Fatal("public preview leaked acquisition paths", err)
					}
				}
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestBuiltCLIChangePlanRetainsMissingFileProxyInput(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed=%t", changed), func(t *testing.T) {
			t.Parallel()
			root, proxy, environment := fileProxyPreviewFixtureVersion(t, "v0.0.0-20260717000000-0123456789ab")
			path := filepath.Join(proxy, filepath.FromSlash(fileProxyPreviewModule), "@latest")
			marker := filepath.Join(t.TempDir(), "missing-origin-created")
			if changed {
				wrapper := sourcePreparationGoWrapper(t)
				goName := "go"
				if runtime.GOOS == "windows" {
					goName += ".exe"
				}
				if err := os.Rename(wrapper, filepath.Join(filepath.Dir(wrapper), goName)); err != nil {
					t.Fatal(err)
				}
				for index, entry := range environment {
					if key, value, _ := strings.Cut(entry, "="); strings.EqualFold(key, "PATH") {
						environment[index] = key + "=" + filepath.Dir(wrapper) + string(os.PathListSeparator) + value
					}
				}
				environment = append(environment,
					"PLYSTRA_SOURCE_PREPARATION_PATH="+path,
					"PLYSTRA_SOURCE_PREPARATION_MARKER="+marker,
					"PLYSTRA_SOURCE_PREPARATION_QUERY="+fileProxyPreviewModule+"@v0.0.0-20260717000000-0123456789ab",
					"PLYSTRA_SOURCE_PREPARATION_MODE=missing-origin-after-get")
			}
			before := commandTree(t, root)
			request := strings.Replace(fileProxyPreviewRequest, "@v1.0.0", "@latest", 1)
			process := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json")
			process.Dir, process.Env, process.Stdin = filepath.Join(root, "reports"), environment, strings.NewReader(request)
			var stdout, stderr bytes.Buffer
			process.Stdout, process.Stderr = &stdout, &stderr
			commandErr := process.Run()
			var result scaffoldPlanDocument
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || stderr.Len() != 0 {
				t.Fatal("missing-input preview did not return structured output", commandErr, err, stderr.String())
			}
			if changed {
				if _, err := os.Stat(marker); err != nil {
					t.Fatal("missing-input appearance was not exercised", commandErr, err)
				}
				var exited *exec.ExitError
				if !errors.As(commandErr, &exited) || exited.ExitCode() != 3 || result.Status != "validation_failed" || result.Payload.PlanDigest != "" {
					t.Fatal("public preview accepted changed negative evidence", commandErr, result.Status)
				}
			} else if commandErr != nil || result.Status != "success" || result.Payload.PlanDigest == "" {
				t.Fatal("unchanged latest fallback failed", commandErr, result.Status)
			}
			for _, private := range []string{root, proxy, path, "PRIVATE_NEW_LATEST", "127.0.0.1"} {
				for _, spelling := range []string{private, filepath.ToSlash(private)} {
					encoded, err := json.Marshal(spelling)
					if err != nil || bytes.Contains(stdout.Bytes(), encoded[1:len(encoded)-1]) {
						t.Fatal("missing-input preview disclosed private origin evidence", err)
					}
				}
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("missing-input preview changed authored files")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func assertBuiltCLIChangePlanRejectsArchiveReplacement(t *testing.T, cached bool) {
	t.Helper()
	root, proxy, environment := fileProxyPreviewFixture(t)
	path := filepath.Join(proxy, filepath.FromSlash(fileProxyPreviewModule), "@v", "v1.0.0.zip")
	if cached {
		output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "download", "-json", fileProxyPreviewModule+"@v1.0.0")
		var downloaded struct{ Dir, Zip string }
		if err != nil || json.Unmarshal(output, &downloaded) != nil || !filepath.IsAbs(downloaded.Dir) || !filepath.IsAbs(downloaded.Zip) {
			t.Fatal("seed target cache", err)
		}
		cache, err := gocommand.Output(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "env", "GOMODCACHE")
		if err != nil {
			t.Fatal(err)
		}
		relative, err := filepath.Rel(strings.TrimSpace(string(cache)), downloaded.Dir)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			t.Fatal("source removal escaped isolated cache")
		}
		if err := os.RemoveAll(downloaded.Dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(path, path+".unavailable"); err != nil {
			t.Fatal(err)
		}
		path = downloaded.Zip
	}
	wrapper := sourcePreparationGoWrapper(t)
	goName := "go"
	if runtime.GOOS == "windows" {
		goName += ".exe"
	}
	if err := os.Rename(wrapper, filepath.Join(filepath.Dir(wrapper), goName)); err != nil {
		t.Fatal(err)
	}
	for index, entry := range environment {
		if key, value, _ := strings.Cut(entry, "="); strings.EqualFold(key, "PATH") {
			environment[index] = key + "=" + filepath.Dir(wrapper) + string(os.PathListSeparator) + value
		}
	}
	marker := filepath.Join(t.TempDir(), "origin-replaced")
	environment = append(environment,
		"PLYSTRA_SOURCE_PREPARATION_PATH="+path,
		"PLYSTRA_SOURCE_PREPARATION_MARKER="+marker,
		"PLYSTRA_SOURCE_PREPARATION_MODE=source-identity-after-download",
		"PLYSTRA_SOURCE_PREPARATION_QUERY="+fileProxyPreviewModule+"@v1.0.0")
	before := commandTree(t, root)
	process := exec.CommandContext(t.Context(), buildSourceIntegrityCLI(t), "change", "plan", "--request", "-", "--format", "json")
	process.Dir, process.Env, process.Stdin = root, environment, strings.NewReader(fileProxyPreviewRequest)
	var stdout, stderr bytes.Buffer
	process.Stdout, process.Stderr = &stdout, &stderr
	if err := process.Run(); err == nil || !strings.Contains(stdout.String(), "PLYSTRA_CHANGE_PLAN_FAILED") || stderr.Len() != 0 {
		t.Fatalf("public origin replacement was accepted: %v\n%s\n%s", err, &stdout, &stderr)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("origin replacement was not exercised", err)
	}
	var result any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal("public failure did not return structured JSON", err)
	}
	public := fmt.Sprint(result)
	if strings.Contains(public, proxy) || strings.Contains(public, path) || strings.Contains(public, filepath.ToSlash(path)) ||
		strings.Contains(public, "127.0.0.1") || !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("failed preview disclosed private paths or changed authored files")
	}
	assertNoCommandTransactions(t, root)
}
