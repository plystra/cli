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
	"strconv"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"github.com/plystra/cli/internal/scaffoldplan"
)

func TestBuiltCLIChangePlanPreservesModuleVersionQueries(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(map[string]string{"GOFLAGS": os.Getenv("GOFLAGS")})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	for _, mode := range []string{"requirement", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			// Only the latest matching selection supplies the required Interface.
			latest := previewDependency(t, "v1.1.0", false)
			selectedModule := latest
			if mode == "replacement" {
				selectedModule = commandProxyModule{path: previewDependencyModule, version: "v1.1.0", ordinary: true}
			}
			proxy := writeCommandDependencyProxy(t, []commandProxyModule{
				{path: previewDependencyModule, version: "v1.0.0", ordinary: true},
				selectedModule,
			})
			environment := previewDependencyEnvironment(t, proxy)
			root := writeImplementationSelectionCommandProject(t)
			options := gocommand.Options{Directory: root, Environment: environment}
			if err := gocommand.Run(t.Context(), options, "get", previewDependencyModule+"@v1.1.0"); err != nil {
				t.Fatal(err)
			}
			module, err := os.ReadFile(filepath.Join(root, "go.mod"))
			if err != nil || !bytes.Contains(module, []byte(previewDependencyModule+" v1.1.0")) {
				t.Fatalf("missing exact seed requirement: %v", err)
			}
			local := ""
			if mode == "replacement" {
				local = t.TempDir()
				writeCommandFile(t, filepath.Join(local, "go.mod"), "module "+previewDependencyModule+"\ngo 1.26\n")
				writeCommandFile(t, filepath.Join(local, "plystra.yaml"), latest.manifest)
				for name, source := range latest.sources {
					writeCommandFile(t, filepath.Join(local, filepath.FromSlash(name)), source)
				}
				writeCommandFile(t, filepath.Join(root, "go.mod"), string(module)+"\nreplace "+previewDependencyModule+" v1 => "+strconv.Quote(filepath.ToSlash(local))+"\n")
			} else {
				writeCommandFile(t, filepath.Join(root, "go.mod"), strings.Replace(string(module), previewDependencyModule+" v1.1.0", previewDependencyModule+" v1", 1))
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [remote.echo/v1]\nconfig:\n  "+previewDependencyModule+"/remote.New: {endpoint: PRIVATE_ENDPOINT}\n")
			workRoot := t.TempDir()
			writeCommandFile(t, filepath.Join(workRoot, "go.work"), "go 1.26\nuse "+strconv.Quote(filepath.ToSlash(root))+"\n")
			environment = append(environment, "GOWORK="+filepath.Join(workRoot, "go.work"))
			options.Environment = environment
			selected, err := gocommand.Output(t.Context(), options, "list", "-m", "-mod=readonly", "-f", "{{.Version}}", previewDependencyModule)
			if err != nil || strings.TrimSpace(string(selected)) != "v1.1.0" {
				t.Fatalf("original version query = %q, %v", selected, err)
			}
			before, beforeWorkspace := commandTree(t, root), commandTree(t, workRoot)
			var beforeLocal map[string][]byte
			if local != "" {
				beforeLocal = commandTree(t, local)
			}
			command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json")
			command.Dir, command.Env = filepath.Join(root, "reports"), environment
			command.Stdin = strings.NewReader(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"todo.add","semantics":"command"}]}`)
			var stderr bytes.Buffer
			command.Stderr = &stderr
			output, err := command.Output()
			var result scaffoldPlanDocument
			if err != nil || stderr.Len() != 0 || json.Unmarshal(output, &result) != nil || result.Status != "success" || result.Payload.Completeness != "non_exact" || result.Payload.PlanDigest == "" || len(result.Payload.WriteSet) == 0 {
				t.Fatalf("compiled query preview: %v\n%s\n%s", err, output, &stderr)
			}
			if bytes.Contains(output, []byte("PRIVATE_")) || bytes.Contains(output, []byte(root)) || bytes.Contains(output, []byte(workRoot)) || local != "" && bytes.Contains(output, []byte(local)) {
				t.Fatal("compiled preview disclosed private inputs")
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) || !reflect.DeepEqual(beforeWorkspace, commandTree(t, workRoot)) || local != "" && !reflect.DeepEqual(beforeLocal, commandTree(t, local)) {
				t.Fatal("compiled query preview changed authored inputs")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestRunChangePlanRejectsPreexistingSourceTampering(t *testing.T) {
	for _, mode := range []string{"existing", "added", "updated", "removed", "injected-vcs", "removed-invalid-manifest"} {
		t.Run(mode, func(t *testing.T) {
			proxy := writeCommandDependencyProxy(t, []commandProxyModule{
				previewDependency(t, "v1.0.0", false), previewDependency(t, "v1.1.0", false),
			})
			environment := previewDependencyEnvironment(t, proxy)
			root := writeImplementationSelectionCommandProject(t)
			if mode != "added" {
				if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "get", previewDependencyModule+"@v1.0.0"); err != nil {
					t.Fatal(err)
				}
			}
			version := "v1.0.0"
			if mode == "updated" {
				version = "v1.1.0"
			}
			output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "download", "-json", previewDependencyModule+"@"+version)
			if err != nil {
				t.Fatal(err)
			}
			var downloaded struct{ Dir string }
			if err := json.Unmarshal(output, &downloaded); err != nil || downloaded.Dir == "" {
				t.Fatalf("download source fixture: %s, %v", output, err)
			}
			path := filepath.Join(downloaded.Dir, "remote", "implementation.go")
			var changed []byte
			if mode == "injected-vcs" {
				path = filepath.Join(downloaded.Dir, ".git", "injected")
				changed = []byte("unexpected archive content")
				writeCommandFile(t, path, string(changed))
			} else if mode == "removed-invalid-manifest" {
				path = filepath.Join(downloaded.Dir, "go.mod")
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
				changed = []byte("module example.com/tampered\n\ngo 1.26\n")
				writeCommandFile(t, path, string(changed))
			} else {
				original, err := os.ReadFile(path)
				info, statErr := os.Stat(path)
				if err != nil || statErr != nil {
					t.Fatalf("source fixture: %v, %v", err, statErr)
				}
				changed = bytes.ReplaceAll(original, []byte("Endpoint string"), []byte("Endpoint uint64"))
				if bytes.Equal(original, changed) {
					t.Fatal("fixture did not change a compiled Config type")
				}
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
				writeCommandFile(t, path, string(changed))
				if err := os.Chmod(path, info.Mode()); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			operations := []map[string]any{{"kind": "interface_create", "name": "todo.add", "semantics": "command"}}
			if mode == "added" || mode == "updated" {
				action := "add"
				if mode == "updated" {
					action = "update"
				}
				operations = append(operations, map[string]any{"kind": "dependency", "action": action, "module": previewDependencyModule + "@" + version})
			} else if strings.HasPrefix(mode, "removed") {
				operations = append(operations, map[string]any{"kind": "dependency", "action": "remove", "module": previewDependencyModule})
			}
			requestBytes, err := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": operations})
			if err != nil {
				t.Fatal(err)
			}
			writeCommandFile(t, filepath.Join(root, "change.json"), string(requestBytes))
			before := commandTree(t, root)
			code, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, environment)
			var result scaffoldPlanDocument
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if code != 3 || stderr != "" || result.Status != "validation_failed" || result.Payload.PlanDigest != "" || len(result.Effects.Planned) != 0 {
				t.Fatalf("tampered %s source accepted: %d %s %s", mode, code, stdout, stderr)
			}
			if strings.Contains(stdout, downloaded.Dir) || strings.Contains(stdout, root) || strings.Contains(stdout, string(changed)) {
				t.Fatal("private source evidence disclosed")
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("failed source validation changed original Project")
			}
			if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, changed) {
				t.Fatal("preview repaired or replaced the tampered dependency source")
			}
			if strings.HasPrefix(mode, "removed") {
				request, err := changerequest.Parse(requestBytes)
				if err != nil {
					t.Fatal(err)
				}
				parent := t.TempDir()
				_, err = scaffoldplan.Preview(t.Context(), scaffoldplan.Options{Start: root, Environment: environment, TemporaryParent: parent}, request)
				if !errors.Is(err, projectstage.ErrSourceIntegrity) && !errors.Is(err, moduledependency.ErrSourceIntegrity) {
					t.Fatalf("old-source integrity failure was swallowed by removal: %v", err)
				}
				if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
					t.Fatal("failed checksum validation retained private stage files")
				}
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestScaffoldPreviewCapturesExactTargetsBeforeGoGet(t *testing.T) {
	command := sourcePreparationGoWrapper(t)
	for _, mode := range []string{"cold", "added", "updated", "replacement", "module-manifest", "module-checksum", "workspace-cold", "workspace", "workspace-replacement", "workspace-module-replacement", "workspace-initial-replacement", "workspace-initial-local", "workspace-module-manifest", "workspace-module-checksum"} {
		t.Run(mode, func(t *testing.T) {
			proxy := writeCommandDependencyProxy(t, []commandProxyModule{previewDependency(t, "v1.0.0", false), previewDependency(t, "v1.1.0", false)})
			environment := previewDependencyEnvironment(t, proxy)
			root := writeImplementationSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
			action, version := "add", "v1.0.0"
			if mode == "updated" {
				if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "get", previewDependencyModule+"@v1.0.0"); err != nil {
					t.Fatal(err)
				}
				action, version = "update", "v1.1.0"
			}
			effectiveVersion := version
			initialLocal := ""
			if mode == "replacement" {
				effectiveVersion = "v1.1.0"
				if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "edit", "-replace="+previewDependencyModule+"="+previewDependencyModule+"@"+effectiveVersion); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(mode, "workspace") {
				workspace := t.TempDir()
				support := filepath.Join(workspace, "support")
				manifest := "module example.com/workspace-support\n\ngo 1.26\n"
				work := "go 1.26\nuse (\n" + fmt.Sprintf("%q\n%q\n", filepath.ToSlash(root), filepath.ToSlash(support)) + ")\n"
				if strings.HasSuffix(mode, "replacement") || mode == "workspace-initial-local" {
					effectiveVersion = "v1.1.0"
					replacement := "replace " + previewDependencyModule + " => " + previewDependencyModule + "@" + effectiveVersion + "\n"
					replacement = strings.ReplaceAll(replacement, "@", " ")
					if mode == "workspace-module-replacement" {
						manifest += replacement
					} else {
						work += replacement
					}
				}
				writeCommandFile(t, filepath.Join(support, "go.mod"), manifest)
				writeCommandFile(t, filepath.Join(support, "support.go"), "package support\n")
				path := filepath.Join(workspace, "go.work")
				writeCommandFile(t, path, work)
				if mode == "workspace-initial-local" {
					initialLocal = filepath.Join(workspace, "initial")
					writeCommandFile(t, filepath.Join(initialLocal, "go.mod"), "module "+previewDependencyModule+"\n\ngo 1.26\n")
					writeCommandFile(t, filepath.Join(initialLocal, "plystra.yaml"), "{}\n")
					for name, content := range previewDependency(t, "v1.0.0", false).sources {
						writeCommandFile(t, filepath.Join(initialLocal, filepath.FromSlash(name)), content)
					}
					if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "edit", "-replace="+previewDependencyModule+"="+filepath.ToSlash(initialLocal)); err != nil {
						t.Fatal(err)
					}
				}
				for index, entry := range environment {
					if name, _, _ := strings.Cut(entry, "="); strings.EqualFold(name, "GOWORK") {
						environment[index] = name + "=" + path
					}
				}
			}
			if mode == "workspace-initial-replacement" {
				effectiveVersion = version
			}
			if !strings.HasSuffix(mode, "cold") {
				if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "download", previewDependencyModule+"@"+effectiveVersion); err != nil {
					t.Fatal(err)
				}
			}
			var cache string
			for _, entry := range environment {
				if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "GOMODCACHE") {
					cache = value
				}
			}
			if cache == "" {
				t.Fatal("missing isolated source cache")
			}
			path := filepath.Join(cache, filepath.FromSlash(previewDependencyModule+"@"+effectiveVersion), "remote", "implementation.go")
			if strings.HasSuffix(mode, "module-manifest") || strings.HasSuffix(mode, "module-checksum") {
				extension := ".mod"
				if strings.HasSuffix(mode, "module-checksum") {
					extension = ".ziphash"
				}
				path = filepath.Join(cache, "cache", "download", filepath.FromSlash(previewDependencyModule), "@v", effectiveVersion+extension)
			}
			if initialLocal != "" {
				path = filepath.Join(initialLocal, "remote", "implementation.go")
			}
			marker := filepath.Join(t.TempDir(), "get-started")
			environment = append(environment, "PLYSTRA_SOURCE_PREPARATION_PATH="+path, "PLYSTRA_SOURCE_PREPARATION_MARKER="+marker, "PLYSTRA_SOURCE_PREPARATION_MODE="+mode, "PLYSTRA_SOURCE_PREPARATION_BACKUP="+filepath.Join(cache, "source-before-get.go"))
			request, err := changerequest.Parse([]byte(fmt.Sprintf(`{"schema":"plystra.change/v1","operations":[{"kind":"dependency","action":%q,"module":%q}]}`, action, previewDependencyModule+"@"+version)))
			if err != nil {
				t.Fatal(err)
			}
			before, parent := commandTree(t, root), t.TempDir()
			planned, err := scaffoldplan.Preview(t.Context(), scaffoldplan.Options{Start: root, Environment: environment, GoCommand: command, TemporaryParent: parent}, request)
			if markerBytes, markerErr := os.ReadFile(marker); markerErr != nil || string(markerBytes) != "source present before get\nget completed\n" {
				t.Fatalf("exact target was not available before get: %v, preview=%v", markerErr, err)
			}
			if strings.HasSuffix(mode, "cold") {
				if err != nil || !planned.Capture().Valid() {
					t.Fatalf("cold prepared target rejected: %v", err)
				}
				if err := planned.Capture().Recheck(t.Context()); err != nil {
					t.Fatalf("retained candidate inputs were invalid after cleanup: %v", err)
				}
				extension := ".mod"
				if mode == "workspace-cold" {
					extension = ".ziphash"
				}
				metadata := filepath.Join(cache, "cache", "download", filepath.FromSlash(previewDependencyModule), "@v", effectiveVersion+extension)
				if err := os.Rename(metadata, metadata+".old"); err != nil {
					t.Fatal(err)
				}
				if err := planned.Capture().Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("removed cache metadata accepted after preview cleanup: %v", err)
				}
			} else if !errors.Is(err, projectstage.ErrConcurrentChange) || planned.Capture().Valid() || planned.FinalModelDigest() != "" {
				t.Fatalf("same-byte source replacement during get was accepted: %v", err)
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("source preparation changed the authored Project")
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatal("source preparation retained private stage files")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func sourcePreparationGoWrapper(t *testing.T) string {
	t.Helper()
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	source := `package main
import (
 "fmt"
 "os"
 "os/exec"
 "path/filepath"
 "strings"
)
func check(err error) { if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(9) } }
func main() {
 backup := ""
 mode := os.Getenv("PLYSTRA_SOURCE_PREPARATION_MODE")
 if source := os.Getenv("PLYSTRA_SOURCE_PREPARATION_LATE_MANIFEST"); source != "" && len(os.Args) > 1 && os.Args[1] == "env" {
  directory, err := os.Getwd(); check(err)
  cacheQuery := false
  for _, argument := range os.Args[2:] { cacheQuery = cacheQuery || argument == "GOMODCACHE" }
  if cacheQuery && strings.HasPrefix(filepath.Base(directory), "plystra-source-preparation-") {
   countPath := os.Getenv("PLYSTRA_SOURCE_PREPARATION_MARKER") + ".cache-queries"
   count, err := os.ReadFile(countPath)
   if err != nil && !os.IsNotExist(err) { check(err) }
   count = append(count, 'x')
   check(os.WriteFile(countPath, count, 0600))
   if len(count) == 2 {
    path := os.Getenv("PLYSTRA_SOURCE_PREPARATION_PATH")
    if _, err := os.Lstat(path); !os.IsNotExist(err) { os.Exit(12) }
    data, err := os.ReadFile(source); check(err)
    check(os.WriteFile(path, data, 0600))
   }
  }
 }
 requestedDownload := mode == "manifest-identity-after-download" || mode == "source-identity-after-download" || mode == "source-manifest-identity-after-download" || mode == "information-identity-after-download" || mode == "information-removed-after-download" || mode == "ziphash-removed-after-download"
 requestedList := mode == "fixed-manifest-identity-after-list" || mode == "information-identity-after-list" || mode == "source-identity-after-list" || mode == "source-manifest-identity-after-list"
 requestedGraph := mode == "manifest-identity-after-graph" || mode == "information-identity-after-graph" || mode == "source-identity-after-graph" || mode == "source-manifest-identity-after-graph"
 isolatedDownload := false
 checkOriginalCache := func() {
  if partial := os.Getenv("PLYSTRA_SOURCE_PARTIAL_PATH"); partial != "" {
   if _, err := os.Stat(partial); err != nil {
    check(os.WriteFile(os.Getenv("PLYSTRA_SOURCE_PREPARATION_MARKER"), []byte("original partial marker missing\n"), 0600)); os.Exit(12)
   }
  } else if _, err := os.Lstat(os.Getenv("PLYSTRA_SOURCE_PREPARATION_PATH")); !os.IsNotExist(err) {
   check(os.WriteFile(os.Getenv("PLYSTRA_SOURCE_PREPARATION_MARKER"), []byte("original source unexpectedly present\n"), 0600)); os.Exit(12)
  }
 }
 if mode == "isolated-acquisition" && len(os.Args) > 2 && os.Args[1] == "mod" && os.Args[2] == "download" {
  directory, err := os.Getwd(); check(err)
  matched := false
  for _, argument := range os.Args[3:] { matched = matched || argument == os.Getenv("PLYSTRA_SOURCE_PREPARATION_QUERY") }
  isolatedDownload = matched && strings.HasPrefix(filepath.Base(directory), "plystra-source-preparation-")
  if isolatedDownload {
   checkOriginalCache()
   if os.Getenv("GOMODCACHE") == os.Getenv("PLYSTRA_SOURCE_ORIGINAL_CACHE") {
    check(os.WriteFile(os.Getenv("PLYSTRA_SOURCE_PREPARATION_MARKER"), []byte("original acquisition cache selected\n"), 0600)); os.Exit(12)
   }
  }
 }
 firstGet := len(os.Args) > 1 && os.Args[1] == "get" && mode != "unchanged" && mode != "isolated-acquisition" && mode != "missing-origin-after-get" && mode != "manifest-after-list" && mode != "manifest-after-download" && mode != "manifest-identity-after-list" && !requestedDownload && !requestedList && !requestedGraph
 if firstGet {
  _, err := os.Stat(os.Getenv("PLYSTRA_SOURCE_PREPARATION_MARKER"))
  if err == nil { firstGet = false } else if !os.IsNotExist(err) { check(err) }
 }
 if firstGet {
  path := os.Getenv("PLYSTRA_SOURCE_PREPARATION_PATH")
  data, err := os.ReadFile(path); check(err)
  info, err := os.Stat(path); check(err)
  if !os.SameFile(info, info) { os.Exit(10) }
  check(os.WriteFile(os.Getenv("PLYSTRA_SOURCE_PREPARATION_MARKER"), []byte("source present before get\n"), 0600))
  mode := os.Getenv("PLYSTRA_SOURCE_PREPARATION_MODE")
  if mode != "cold" && mode != "workspace-cold" {
   backup = os.Getenv("PLYSTRA_SOURCE_PREPARATION_BACKUP")
   check(os.Rename(path, backup))
   check(os.WriteFile(path, data, info.Mode().Perm()))
   check(os.Chtimes(path, info.ModTime(), info.ModTime()))
   replacement, err := os.Stat(path); check(err)
   if os.SameFile(info, replacement) { os.Exit(10) }
  }
 }
 command := exec.Command(REAL_GO, os.Args[1:]...)
 command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
 err := command.Run()
 if err == nil && isolatedDownload {
  checkOriginalCache()
  check(os.WriteFile(os.Getenv("PLYSTRA_SOURCE_PREPARATION_MARKER"), []byte("exact acquisition consumed a private cache\n"), 0600))
 }
 if err == nil && mode == "missing-origin-after-get" && len(os.Args) > 1 && os.Args[1] == "get" {
  marker := os.Getenv("PLYSTRA_SOURCE_PREPARATION_MARKER")
  if _, markerErr := os.Stat(marker); os.IsNotExist(markerErr) {
   path := os.Getenv("PLYSTRA_SOURCE_PREPARATION_PATH")
   if _, pathErr := os.Lstat(path); !os.IsNotExist(pathErr) { os.Exit(12) }
   _, version, ok := strings.Cut(os.Getenv("PLYSTRA_SOURCE_PREPARATION_QUERY"), "@")
   if !ok { os.Exit(12) }
   data := fmt.Sprintf("{\"Version\":%q,\"Time\":\"2026-07-17T00:00:00Z\",\"Private\":\"PRIVATE_NEW_LATEST\"}", version)
   check(os.WriteFile(path, []byte(data), 0600))
   check(os.WriteFile(marker, []byte("missing origin appeared after Go get\n"), 0600))
  } else { check(markerErr) }
 }
 if err == nil && os.Getenv("PLYSTRA_SOURCE_PREPARATION_MODE") == "checksum-after-list" && len(os.Args) > 2 && os.Args[1] == "list" && os.Args[2] == "-m" {
  marker := os.Getenv("PLYSTRA_SOURCE_PREPARATION_MARKER")
  if _, markerErr := os.Stat(marker); os.IsNotExist(markerErr) {
   path := os.Getenv("PLYSTRA_SOURCE_PREPARATION_PATH")
   info, statErr := os.Stat(path); check(statErr)
   check(os.Chmod(path, 0600))
   check(os.WriteFile(path, []byte("h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="), 0600))
   check(os.Chmod(path, info.Mode()))
   check(os.WriteFile(marker, []byte("checksum changed after module listing\n"), 0600))
  } else { check(markerErr) }
 }
 afterList := len(os.Args) > 2 && os.Args[1] == "list" && os.Args[2] == "-m"
 afterDownload := len(os.Args) > 2 && os.Args[1] == "mod" && os.Args[2] == "download"
 if requestedDownload {
  matched := false
  for _, argument := range os.Args[1:] {
   if argument == os.Getenv("PLYSTRA_SOURCE_PREPARATION_QUERY") { matched = true }
  }
  afterDownload = afterDownload && matched
 }
 if requestedList {
  matched := false
  for _, argument := range os.Args[1:] {
   if argument + "@v1.0.0" == os.Getenv("PLYSTRA_SOURCE_PREPARATION_QUERY") { matched = true }
  }
  afterList = afterList && matched
 }
 if requestedGraph {
  matched := false
  for _, argument := range os.Args[1:] { if argument == "all" { matched = true } }
  afterList = afterList && matched
 }
 if err == nil && ((mode == "manifest-after-list" || mode == "manifest-identity-after-list" || requestedList || requestedGraph) && afterList || (mode == "manifest-after-download" || requestedDownload) && afterDownload) {
  if (requestedDownload || requestedList || requestedGraph) && os.Getenv("PLYSTRA_SOURCE_PREPARATION_WORKSPACE") != "" && (os.Getenv("GOWORK") == "" || os.Getenv("GOWORK") == "off") { os.Exit(11) }
  marker := os.Getenv("PLYSTRA_SOURCE_PREPARATION_MARKER")
  path := os.Getenv("PLYSTRA_SOURCE_PREPARATION_PATH")
  _, pathErr := os.Stat(path)
  if _, markerErr := os.Stat(marker); os.IsNotExist(markerErr) && pathErr == nil {
   data, readErr := os.ReadFile(path); check(readErr)
   info, statErr := os.Stat(path); check(statErr)
   if mode == "manifest-identity-after-list" || requestedDownload || requestedList || requestedGraph {
    if !os.SameFile(info, info) { os.Exit(10) }
    backup = path + ".prior"
    check(os.Rename(path, backup))
    if mode != "information-removed-after-download" && mode != "ziphash-removed-after-download" {
     check(os.WriteFile(path, data, info.Mode().Perm()))
     check(os.Chtimes(path, info.ModTime(), info.ModTime()))
     current, statErr := os.Stat(path); check(statErr)
     if os.SameFile(info, current) { os.Exit(10) }
    }
   } else {
    check(os.Chmod(path, 0600))
    check(os.WriteFile(path, append(data, []byte("\n// changed after verified Go output\n")...), 0600))
    check(os.Chmod(path, info.Mode()))
   }
   check(os.WriteFile(marker, []byte("manifest changed after Go output\n"), 0600))
  } else if markerErr != nil && !os.IsNotExist(markerErr) { check(markerErr) }
 }
 if backup != "" { check(os.Chmod(backup, 0600)); check(os.Remove(backup)) }
 if err != nil {
  if exited, ok := err.(*exec.ExitError); ok { os.Exit(exited.ExitCode()) }
  check(err)
 }
 if firstGet {
  marker, err := os.OpenFile(os.Getenv("PLYSTRA_SOURCE_PREPARATION_MARKER"), os.O_APPEND|os.O_WRONLY, 0600); check(err)
  _, err = marker.WriteString("get completed\n"); check(err); check(marker.Close())
 }
}
`
	source = strings.ReplaceAll(source, "REAL_GO", fmt.Sprintf("%q", realGo))
	writeCommandFile(t, filepath.Join(root, "main.go"), source)
	binary := filepath.Join(root, "go-wrapper.exe")
	build := exec.CommandContext(t.Context(), realGo, "build", "-buildvcs=false", "-o", binary, filepath.Join(root, "main.go"))
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(map[string]string{"GOFLAGS": os.Getenv("GOFLAGS")})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Go source-preparation wrapper: %v\n%s", err, output)
	}
	return binary
}

func TestScaffoldPreviewRejectsChangedChecksumReferenceBeforeRetention(t *testing.T) {
	proxy := writeCommandDependencyProxy(t, []commandProxyModule{previewDependency(t, "v1.0.0", false)})
	environment := previewDependencyEnvironment(t, proxy)
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
	if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "get", previewDependencyModule+"@v1.0.0"); err != nil {
		t.Fatal(err)
	}
	var cache string
	for _, entry := range environment {
		if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "GOMODCACHE") {
			cache = value
		}
	}
	if cache == "" {
		t.Fatal("missing isolated source cache")
	}
	path := filepath.Join(cache, "cache", "download", filepath.FromSlash(previewDependencyModule), "@v", "v1.0.0.ziphash")
	marker := filepath.Join(t.TempDir(), "metadata-changed")
	environment = append(environment, "PLYSTRA_SOURCE_PREPARATION_PATH="+path, "PLYSTRA_SOURCE_PREPARATION_MARKER="+marker, "PLYSTRA_SOURCE_PREPARATION_MODE=checksum-after-list")
	request, err := changerequest.Parse([]byte(fmt.Sprintf(`{"schema":"plystra.change/v1","operations":[{"kind":"dependency","action":"remove","module":%q}]}`, previewDependencyModule)))
	if err != nil {
		t.Fatal(err)
	}
	before, parent := commandTree(t, root), t.TempDir()
	planned, err := scaffoldplan.Preview(t.Context(), scaffoldplan.Options{
		Start: root, Environment: environment, GoCommand: sourcePreparationGoWrapper(t), TemporaryParent: parent,
	}, request)
	if data, markerErr := os.ReadFile(marker); markerErr != nil || string(data) != "checksum changed after module listing\n" {
		t.Fatalf("checksum-reference race was not exercised: %v, preview=%v", markerErr, err)
	}
	if !errors.Is(err, moduledependency.ErrSourceIntegrity) || planned.Capture().Valid() || planned.FinalModelDigest() != "" {
		t.Fatalf("checksum conflict did not fail as source integrity before removal: %v", err)
	}
	if !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("failed metadata capture changed the original Project")
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatal("failed metadata capture retained private stage files")
	}
	assertNoCommandTransactions(t, root)
}

func TestScaffoldPreviewRejectsChangedModuleManifestBeforeRetention(t *testing.T) {
	command := sourcePreparationGoWrapper(t)
	for _, mode := range []string{"unchanged", "existing", "removal", "prepared", "workspace", "replacement", "existing-identity", "removal-identity", "replacement-identity", "workspace-identity", "workspace-unchanged", "workspace-removal-identity", "workspace-replacement-identity", "transitive-identity", "workspace-transitive-identity", "transitive-unchanged", "workspace-transitive-unchanged"} {
		t.Run(mode, func(t *testing.T) {
			identity := strings.HasSuffix(mode, "-identity")
			mode = strings.TrimSuffix(mode, "-identity")
			workspace := mode == "workspace" || strings.HasPrefix(mode, "workspace-")
			mode = strings.TrimPrefix(mode, "workspace-")
			transitive := strings.HasPrefix(mode, "transitive")
			unchanged := mode == "unchanged" || mode == "transitive-unchanged"
			const transitivePath = "example.com/preview/transitive"
			modules := []commandProxyModule{previewDependency(t, "v1.0.0", false)}
			if transitive {
				modules[0].goMod = "module " + previewDependencyModule + "\ngo 1.26\nrequire " + transitivePath + " v1.0.0\n"
				modules = append(modules, commandProxyModule{path: transitivePath, version: "v1.0.0", ordinary: true})
			}
			proxy := writeCommandDependencyProxy(t, modules)
			environment := previewDependencyEnvironment(t, proxy)
			root := writeImplementationSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
			required := previewDependencyModule
			if mode == "replacement" {
				required = "example.com/preview/original"
				if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "edit", "-replace="+required+"="+previewDependencyModule+"@v1.0.0"); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "prepared" {
				if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "get", required+"@v1.0.0"); err != nil {
					t.Fatal(err)
				}
			}
			if transitive {
				seedPreviewTransitiveManifest(t, root, environment, transitivePath)
			}
			if workspace {
				work := filepath.Join(t.TempDir(), "go.work")
				writeCommandFile(t, work, "go 1.26\nuse "+fmt.Sprintf("%q\n", filepath.ToSlash(root)))
				environment = append(environment, "GOWORK="+work)
			}
			var cache string
			for _, entry := range environment {
				if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "GOMODCACHE") {
					cache = value
				}
			}
			if cache == "" {
				t.Fatal("missing isolated source cache")
			}
			path := filepath.Join(cache, "cache", "download", filepath.FromSlash(previewDependencyModule), "@v", "v1.0.0.mod")
			if transitive {
				path = filepath.Join(cache, "cache", "download", filepath.FromSlash(transitivePath), "@v", "v1.0.0.mod")
			}
			marker := filepath.Join(t.TempDir(), "manifest-changed")
			race := "manifest-after-list"
			if mode == "prepared" || mode == "workspace" {
				race = "manifest-after-download"
			} else if unchanged {
				race = "unchanged"
			}
			if identity {
				race = "manifest-identity-after-list"
			}
			environment = append(environment, "PLYSTRA_SOURCE_PREPARATION_PATH="+path, "PLYSTRA_SOURCE_PREPARATION_MARKER="+marker, "PLYSTRA_SOURCE_PREPARATION_MODE="+race)
			data := []byte(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"todo.add","semantics":"command"}]}`)
			if mode == "removal" {
				data = []byte(fmt.Sprintf(`{"schema":"plystra.change/v1","operations":[{"kind":"dependency","action":"remove","module":%q}]}`, required))
			} else if mode == "prepared" {
				data = []byte(fmt.Sprintf(`{"schema":"plystra.change/v1","operations":[{"kind":"dependency","action":"add","module":%q}]}`, required+"@v1.0.0"))
			}
			request, err := changerequest.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			before, parent := commandTree(t, root), t.TempDir()
			planned, err := scaffoldplan.Preview(t.Context(), scaffoldplan.Options{
				Start: root, Environment: environment, GoCommand: command, TemporaryParent: parent,
			}, request)
			if unchanged {
				if err != nil || !planned.Capture().Valid() || planned.FinalModelDigest() == "" {
					t.Fatalf("unchanged module manifest rejected: %v", err)
				}
				if err := planned.Capture().Recheck(t.Context()); err != nil {
					t.Fatal(err)
				}
			} else {
				if data, markerErr := os.ReadFile(marker); markerErr != nil || string(data) != "manifest changed after Go output\n" {
					t.Fatalf("module-manifest race was not exercised: %v, preview=%v", markerErr, err)
				}
				if !errors.Is(err, moduledependency.ErrSourceIntegrity) || planned.Capture().Valid() || planned.FinalModelDigest() != "" {
					t.Fatalf("changed module manifest was not rejected at capture: %v, capture=%v", err, planned.Capture().Valid())
				}
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("metadata validation changed the original Project")
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatal("metadata validation retained private stage files")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestBuiltCLIChangePlanRejectsChangedModuleManifest(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{"manifest-after-list", "manifest-identity-after-list", "workspace-manifest-identity-after-list", "transitive-manifest-identity-after-list", "workspace-transitive-manifest-identity-after-list", "prepared-manifest-identity-after-download", "workspace-prepared-manifest-identity-after-download"} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRejectsChangedPreparedSource(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{"prepared-source-identity-after-download", "workspace-prepared-source-identity-after-download", "workspace-prepared-source-identity-after-list", "workspace-prepared-source-manifest-identity-after-list"} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanPreservesWarmPreparedWorkspaceSource(t *testing.T) {
	testBuiltCLIChangePlanCacheInput(t, buildSourceIntegrityCLI(t), "workspace-prepared-unchanged")
}

func TestBuiltCLIChangePlanPreparedWorkspaceSelectedGraph(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{"source-identity-after-list", "source-manifest-identity-after-list", "unchanged"} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, "selected-graph-workspace-prepared-"+mode)
		})
	}
}

func TestBuiltCLIChangePlanRetainsWarmTreesWithoutMetadata(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{
		"missing-manifest-prepared-source-identity-after-download",
		"missing-ziphash-prepared-source-manifest-identity-after-download",
		"archive-only-workspace-prepared-source-identity-after-download",
		"missing-ziphash-prepared-unchanged",
		"missing-ziphash-prepared-ziphash-removed-after-download",
	} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRetainsTreesBeforeChecksumAcquisition(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{
		"manifest-only-prepared-source-identity-after-download",
		"no-authority-workspace-prepared-source-manifest-identity-after-download",
		"no-authority-prepared-unchanged",
	} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRetainsPendingSourcesBeforeSelection(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{
		"manifest-only-source-identity-after-graph",
		"manifest-only-workspace-prepared-source-identity-after-list",
		"manifest-only-selected-graph-workspace-prepared-source-manifest-identity-after-list",
		"manifest-only-selected-graph-workspace-prepared-unchanged",
	} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRetainsFixedRootTreesBeforeIncompleteSelection(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{
		"manifest-only-fixed-root-source-identity-after-graph",
		"no-authority-fixed-root-workspace-source-identity-after-graph",
		"manifest-only-fixed-root-unchanged",
	} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRetainsFixedRootInformationBeforeIncompleteSelection(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{
		"manifest-only-fixed-root-information-identity-after-graph",
		"no-authority-fixed-root-workspace-information-identity-after-graph",
		"no-authority-fixed-root-workspace-unchanged",
	} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRetainsFixedRootManifestsBeforeSelection(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{
		"no-authority-fixed-root-workspace-manifest-identity-after-graph",
		"no-authority-fixed-root-workspace-prepared-fixed-manifest-identity-after-list",
	} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRejectsChangedModuleInformation(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{"prepared-information-identity-after-download", "workspace-prepared-information-identity-after-download", "workspace-prepared-information-identity-after-list", "prepared-information-removed-after-download", "workspace-prepared-information-removed-after-download"} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRejectsChangedGraphInformation(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{"information-identity-after-graph", "workspace-information-identity-after-graph"} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRejectsChangedGraphSource(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{"source-identity-after-graph", "workspace-source-identity-after-graph", "source-manifest-identity-after-graph", "workspace-source-manifest-identity-after-graph"} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRejectsChangedListedSource(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{"workspace-cold-graph-source-identity-after-download", "workspace-cold-graph-source-manifest-identity-after-download", "cold-graph-duplicate-sum-source-identity-after-download", "cold-graph-duplicate-sum-source-manifest-identity-after-download"} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRejectsChangedAcquiredSource(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{"cold-source-transitive-source-identity-after-download", "cold-source-transitive-source-manifest-identity-after-download", "workspace-cold-source-transitive-source-identity-after-download", "workspace-cold-source-transitive-source-manifest-identity-after-download"} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRetainsUnchangedAcquiredSource(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{"cold-source-transitive-unchanged", "workspace-cold-source-transitive-unchanged"} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRejectsChangedAcquisitionManifest(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{
		"cold-manifests-cold-source-transitive-manifest-identity-after-download",
		"cold-manifests-workspace-cold-source-transitive-manifest-identity-after-download",
	} {
		t.Run(mode, func(t *testing.T) {
			testBuiltCLIChangePlanCacheInput(t, binary, mode)
		})
	}
}

func TestBuiltCLIChangePlanRejectsLatePreparationManifest(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	testBuiltCLIChangePlanCacheInput(t, binary, "late-manifest-prepared-manifest-identity-after-download")
}

func TestBuiltCLIChangePlanRejectsUnrecordedAcquisitionManifest(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{"no-authority-prepared-manifest-identity-after-download", "no-authority-prepared-workspace-selected-graph-manifest-identity-after-download"} {
		t.Run(mode, func(t *testing.T) { testBuiltCLIChangePlanCacheInput(t, binary, mode) })
	}
}

func TestBuiltCLIChangePlanRetainsRawRootManifestBeforeListing(t *testing.T) {
	binary := buildSourceIntegrityCLI(t)
	for _, mode := range []string{"warm-cache-no-authority-workspace-manifest-identity-after-graph", "warm-cache-no-authority-workspace-unchanged"} {
		t.Run(mode, func(t *testing.T) { testBuiltCLIChangePlanCacheInput(t, binary, mode) })
	}
}

func buildSourceIntegrityCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-buildvcs=false", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(map[string]string{"GOFLAGS": os.Getenv("GOFLAGS")})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	return binary
}

func testBuiltCLIChangePlanCacheInput(t *testing.T, binary, mode string) {
	t.Helper()
	warmCache := strings.HasPrefix(mode, "warm-cache-")
	mode = strings.TrimPrefix(mode, "warm-cache-")
	lateManifest := strings.HasPrefix(mode, "late-manifest-")
	mode = strings.TrimPrefix(mode, "late-manifest-")
	coldManifests := strings.HasPrefix(mode, "cold-manifests-")
	mode = strings.TrimPrefix(mode, "cold-manifests-")
	missingMetadata := ""
	for _, prefix := range []string{"missing-manifest-", "missing-ziphash-", "archive-only-", "manifest-only-", "no-authority-"} {
		if remaining, matches := strings.CutPrefix(mode, prefix); matches {
			missingMetadata, mode = prefix, remaining
			break
		}
	}
	fixedRoot := strings.Contains(mode, "fixed-root-")
	mode = strings.ReplaceAll(mode, "fixed-root-", "")
	selectedGraph := strings.Contains(mode, "selected-graph-")
	mode = strings.ReplaceAll(mode, "selected-graph-", "")
	coldGraph := strings.Contains(mode, "cold-graph-")
	mode = strings.ReplaceAll(mode, "cold-graph-", "")
	coldSource := strings.Contains(mode, "cold-source-")
	mode = strings.ReplaceAll(mode, "cold-source-", "")
	duplicateSum := strings.Contains(mode, "duplicate-sum-")
	mode = strings.ReplaceAll(mode, "duplicate-sum-", "")
	prepared := strings.Contains(mode, "prepared-")
	mode = strings.ReplaceAll(mode, "prepared-", "")
	transitive := strings.Contains(mode, "transitive-")
	mode = strings.ReplaceAll(mode, "transitive-", "")
	const transitivePath = "example.com/preview/transitive"
	modules := []commandProxyModule{previewDependency(t, "v1.0.0", false)}
	if transitive {
		modules[0].goMod = "module " + previewDependencyModule + "\ngo 1.26\nrequire " + transitivePath + " v1.0.0\n"
		modules = append(modules, commandProxyModule{path: transitivePath, version: "v1.0.0", ordinary: true})
	}
	proxy := writeCommandDependencyProxy(t, modules)
	environment := previewDependencyEnvironment(t, proxy)
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
	seedPreviewDependency(t, root, environment, "v1.0.0")
	if warmCache {
		// These cases target warm root manifests. Populate unrelated runtime
		// dependencies before removing the target's original checksum authority.
		if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "download", "all"); err != nil {
			t.Fatal(err)
		}
	}
	if fixedRoot {
		if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "edit", "-replace="+previewDependencyModule+"="+previewDependencyModule+"@v1.0.0"); err != nil {
			t.Fatal(err)
		}
	}
	if duplicateSum {
		path := filepath.Join(root, "go.sum")
		sums, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		writeCommandFile(t, path, string(sums)+previewDependencyModule+" v1.0.0/go.mod h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n")
	}
	if coldGraph {
		environment = append(environment, "PLYSTRA_SOURCE_PREPARATION_QUERY="+previewDependencyModule+"@v1.0.0")
	}
	if prepared {
		if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "edit", "-droprequire="+previewDependencyModule); err != nil {
			t.Fatal(err)
		}
		sums, err := os.ReadFile(filepath.Join(root, "go.sum"))
		if err != nil || !bytes.Contains(sums, []byte(previewDependencyModule+" v1.0.0/go.mod h1:")) {
			t.Fatalf("prepared fixture lacks original manifest checksum authority: %v", err)
		}
		environment = append(environment, "PLYSTRA_SOURCE_PREPARATION_QUERY="+previewDependencyModule+"@v1.0.0")
	}
	if transitive {
		if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "get", previewDependencyModule+"@v1.0.0"); err != nil {
			t.Fatal(err)
		}
		seedPreviewTransitiveManifest(t, root, environment, transitivePath)
	}
	if coldSource {
		if !transitive {
			t.Fatal("cold acquisition race requires a later selected source")
		}
		environment = append(environment, "PLYSTRA_SOURCE_PREPARATION_QUERY="+transitivePath+"@v1.0.0")
	}
	workspaceRoot, initialLocal := "", ""
	if workspaceMode, workspace := strings.CutPrefix(mode, "workspace-"); workspace {
		workspaceRoot = t.TempDir()
		if coldGraph || strings.HasSuffix(workspaceMode, "-after-graph") {
			environment = append(environment, "PLYSTRA_SOURCE_PREPARATION_WORKSPACE=1")
		}
		work := filepath.Join(workspaceRoot, "go.work")
		workSource := "go 1.26\nuse " + fmt.Sprintf("%q\n", filepath.ToSlash(root))
		if prepared {
			initialLocal = t.TempDir()
			writeCommandFile(t, filepath.Join(initialLocal, "go.mod"), "module "+previewDependencyModule+"\ngo 1.26\n")
			if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: environment}, "mod", "edit", "-replace="+previewDependencyModule+"="+filepath.ToSlash(initialLocal)); err != nil {
				t.Fatal(err)
			}
			old := previewDependencyModule
			if selectedGraph {
				old += " v1.0.0"
			}
			workSource += "replace " + old + " => " + previewDependencyModule + " v1.0.0\n"
			environment = append(environment, "PLYSTRA_SOURCE_PREPARATION_WORKSPACE=1")
		}
		writeCommandFile(t, work, workSource)
		environment = append(environment, "GOWORK="+work)
		mode = workspaceMode
	}
	var cache string
	for _, entry := range environment {
		if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "GOMODCACHE") {
			cache = value
		}
	}
	if cache == "" {
		t.Fatal("missing isolated source cache")
	}
	switch missingMetadata {
	case "missing-manifest-", "missing-ziphash-":
		suffix := ".mod"
		if missingMetadata == "missing-ziphash-" {
			suffix = ".ziphash"
		}
		if err := os.Remove(filepath.Join(cache, "cache", "download", filepath.FromSlash(previewDependencyModule), "@v", "v1.0.0"+suffix)); err != nil {
			t.Fatal(err)
		}
	case "archive-only-", "manifest-only-", "no-authority-":
		path := filepath.Join(root, "go.sum")
		sums, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var retained []string
		for _, row := range strings.Split(string(sums), "\n") {
			archive := strings.HasPrefix(row, previewDependencyModule+" v1.0.0 ")
			manifest := strings.HasPrefix(row, previewDependencyModule+" v1.0.0/go.mod ")
			remove := missingMetadata == "archive-only-" && manifest || missingMetadata == "manifest-only-" && archive || missingMetadata == "no-authority-" && (archive || manifest)
			if !remove {
				retained = append(retained, row)
			}
		}
		writeCommandFile(t, path, strings.Join(retained, "\n"))
	}
	if fixedRoot && workspaceRoot == "" {
		if err := os.Remove(filepath.Join(cache, "cache", "download", filepath.FromSlash(previewDependencyModule), "@v", "v1.0.0.mod")); err != nil {
			t.Fatal(err)
		}
	}
	if coldGraph {
		if err := os.Remove(filepath.Join(cache, "cache", "download", filepath.FromSlash(previewDependencyModule), "@v", "v1.0.0.mod")); err != nil {
			t.Fatal(err)
		}
	}
	if coldSource {
		for _, modulePath := range []string{previewDependencyModule, transitivePath} {
			path := filepath.Join(cache, filepath.FromSlash(modulePath+"@v1.0.0"))
			if !strings.HasPrefix(path, filepath.Clean(cache)+string(filepath.Separator)) {
				t.Fatal("cold source escaped isolated cache")
			}
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
		}
	}
	if coldManifests {
		if !coldSource || !transitive {
			t.Fatal("cold manifest acquisition requires missing selected source trees")
		}
		for _, modulePath := range []string{previewDependencyModule, transitivePath} {
			for _, suffix := range []string{".mod", ".info"} {
				if err := os.Remove(filepath.Join(cache, "cache", "download", filepath.FromSlash(modulePath), "@v", "v1.0.0"+suffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
			}
		}
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
		if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "PATH") {
			environment[index] = name + "=" + filepath.Dir(wrapper) + string(os.PathListSeparator) + value
		}
	}
	path := filepath.Join(cache, "cache", "download", filepath.FromSlash(previewDependencyModule), "@v", "v1.0.0.mod")
	if strings.Contains(mode, "information-") {
		path = strings.TrimSuffix(path, ".mod") + ".info"
	} else if strings.Contains(mode, "ziphash-") {
		path = strings.TrimSuffix(path, ".mod") + ".ziphash"
	}
	if strings.Contains(mode, "source-identity") {
		path = filepath.Join(cache, filepath.FromSlash(previewDependencyModule+"@v1.0.0"), "remote", "implementation.go")
	}
	if strings.Contains(mode, "source-manifest-identity") {
		path = filepath.Join(cache, filepath.FromSlash(previewDependencyModule+"@v1.0.0"), "go.mod")
	}
	if transitive && (!coldSource || coldManifests) {
		path = filepath.Join(cache, "cache", "download", filepath.FromSlash(transitivePath), "@v", "v1.0.0.mod")
	}
	marker := filepath.Join(t.TempDir(), "manifest-changed")
	if lateManifest {
		if !prepared || workspaceRoot != "" {
			t.Fatal("late manifest fixture requires exact single-module preparation")
		}
		manifest, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		saved := filepath.Join(t.TempDir(), "original.mod")
		writeCommandFile(t, saved, string(manifest))
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		directory := filepath.Join(cache, filepath.FromSlash(previewDependencyModule+"@v1.0.0"))
		relative, err := filepath.Rel(cache, directory)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			t.Fatal("late source removal escaped isolated cache")
		}
		if err := os.RemoveAll(directory); err != nil {
			t.Fatal(err)
		}
		environment = append(environment, "PLYSTRA_SOURCE_PREPARATION_LATE_MANIFEST="+saved)
	}
	environment = append(environment, "PLYSTRA_SOURCE_PREPARATION_PATH="+path, "PLYSTRA_SOURCE_PREPARATION_MARKER="+marker, "PLYSTRA_SOURCE_PREPARATION_MODE="+mode)
	before := commandTree(t, root)
	var beforeWorkspace, beforeLocal map[string][]byte
	if workspaceRoot != "" {
		beforeWorkspace = commandTree(t, workspaceRoot)
	}
	if initialLocal != "" {
		beforeLocal = commandTree(t, initialLocal)
	}
	command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json")
	command.Dir, command.Env = root, environment
	if transitive || prepared || coldGraph || strings.HasSuffix(mode, "-after-graph") {
		command.Dir = filepath.Join(root, "reports")
	}
	command.Stdin = strings.NewReader(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"todo.add","semantics":"command"}]}`)
	if prepared {
		command.Stdin = strings.NewReader(fmt.Sprintf(`{"schema":"plystra.change/v1","operations":[{"kind":"dependency","action":"add","module":%q}]}`, previewDependencyModule+"@v1.0.0"))
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	var exited *exec.ExitError
	var result scaffoldPlanDocument
	decodeErr := json.Unmarshal(output, &result)
	if mode == "unchanged" {
		if err != nil || stderr.Len() != 0 || decodeErr != nil || result.Status != "success" || result.Payload.Completeness != "non_exact" || result.Payload.PlanDigest == "" {
			t.Fatalf("compiled cold acquisition preview failed: error=%v, decode=%v, status=%q, stderr-bytes=%d", err, decodeErr, result.Status, stderr.Len())
		}
		if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("unchanged acquisition changed cache input")
		}
		paths := []string{filepath.Join(cache, filepath.FromSlash(previewDependencyModule+"@v1.0.0"), "remote", "implementation.go")}
		if transitive {
			paths = append(paths, filepath.Join(cache, filepath.FromSlash(transitivePath+"@v1.0.0"), "go.mod"))
		}
		for _, path := range paths {
			if _, err := os.Stat(path); err != nil {
				t.Fatal("cold acquisition did not materialize selected source", err)
			}
		}
	} else {
		if data, err := os.ReadFile(marker); err != nil || string(data) != "manifest changed after Go output\n" {
			t.Fatalf("compiled CLI did not exercise cache-input race: %v", err)
		}
		if !errors.As(err, &exited) || exited.ExitCode() != 3 || stderr.Len() != 0 || decodeErr != nil || result.Status != "validation_failed" || result.Payload.PlanDigest != "" {
			t.Fatalf("compiled CLI accepted cache-input drift: error=%v, decode=%v, status=%q, plan-present=%t, stderr-bytes=%d", err, decodeErr, result.Status, result.Payload.PlanDigest != "", stderr.Len())
		}
	}
	for _, private := range []string{cache, root, workspaceRoot, initialLocal, "changed after verified Go output"} {
		if private == "" {
			continue
		}
		for _, spelling := range []string{private, filepath.ToSlash(private)} {
			encoded, err := json.Marshal(spelling)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(output, encoded[1:len(encoded)-1]) {
				t.Fatal("compiled CLI disclosed private manifest evidence")
			}
		}
	}
	if !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("compiled CLI changed the authored Project")
	}
	if workspaceRoot != "" && !reflect.DeepEqual(beforeWorkspace, commandTree(t, workspaceRoot)) || initialLocal != "" && !reflect.DeepEqual(beforeLocal, commandTree(t, initialLocal)) {
		t.Fatal("compiled CLI changed the selected workspace or local replacement")
	}
	assertNoCommandTransactions(t, root)
}

func seedPreviewTransitiveManifest(t *testing.T, root string, environment []string, path string) {
	t.Helper()
	options := gocommand.Options{Directory: root, Environment: environment}
	output, err := gocommand.Output(t.Context(), options, "mod", "download", "-json", path+"@v1.0.0")
	var downloaded struct{ Sum, GoModSum string }
	if err != nil || json.Unmarshal(output, &downloaded) != nil || downloaded.Sum == "" || downloaded.GoModSum == "" {
		t.Fatalf("prepare transitive checksum authority: %v", err)
	}
	sums, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(sums)+path+" v1.0.0 "+downloaded.Sum+"\n"+path+" v1.0.0/go.mod "+downloaded.GoModSum+"\n")
	if err := gocommand.Run(t.Context(), options, "mod", "edit", "-droprequire="+path); err != nil {
		t.Fatal(err)
	}
}
