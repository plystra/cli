package command_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
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
