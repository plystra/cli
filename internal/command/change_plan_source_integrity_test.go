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
	"strings"
	"testing"

	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectstage"
	"github.com/plystra/cli/internal/scaffoldplan"
)

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
)
func check(err error) { if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(9) } }
func main() {
 backup := ""
 firstGet := len(os.Args) > 1 && os.Args[1] == "get"
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
