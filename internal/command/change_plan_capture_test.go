package command_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/projectstage"
	"github.com/plystra/cli/internal/scaffoldplan"
	"go.yaml.in/yaml/v3"
)

func TestScaffoldPreviewRetainsPrivateRequestAndWriteImages(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandConfigurableImplementation(t, root, "reports", "reports.read/v1", "reports/read/v1", "Read")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\nconfig:\n  example.com/acme/implementation-use/reports.New: {endpoint: PRIVATE_ORIGINAL}\n")
	before := commandTree(t, root)
	requests := make([]changerequest.Request, 2)
	for index, value := range []string{"PRIVATE_FIRST", "PRIVATE_OTHER"} {
		operations := append(scaffoldCommandOperations(), map[string]any{
			"kind": "configuration", "action": "set", "owner": "example.com/acme/implementation-use/reports.New", "path": "endpoint", "value": value,
		})
		data, err := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": operations})
		if err != nil {
			t.Fatal(err)
		}
		requests[index], err = changerequest.Parse(data)
		if err != nil {
			t.Fatal(err)
		}
	}
	var captures []scaffoldplan.Capture
	for index, request := range requests {
		parent := t.TempDir()
		planned, err := scaffoldplan.Preview(t.Context(), scaffoldplan.Options{Start: root, TemporaryParent: parent, Environment: implementationSelectionCommandEnvironment(nil)}, request)
		if err != nil {
			t.Fatal(err)
		}
		captured := planned.Capture()
		if !captured.Valid() || !captured.MatchesRequest(request) || captured.MatchesRequest(requests[1-index]) || !bytes.Equal(captured.CanonicalRequest(), request.CanonicalJSON()) {
			t.Fatal("private request identity was lost or substituted")
		}
		if captured.ConfigurationSelection().Mode() != "default" || captured.ConfigurationSelection().Path() != "plystra.yaml" {
			t.Fatal("private capture lost the resolved configuration selector")
		}
		if children, err := os.ReadDir(parent); err != nil || len(children) != 0 {
			t.Fatal("successful preview retained its temporary working tree")
		}
		if err := captured.Recheck(t.Context()); err != nil {
			t.Fatal("captured original inputs depend on removed stage")
		}
		writes := captured.Writes()
		foundConfiguration, foundBaseline, foundSource := false, false, false
		for _, write := range writes {
			if write.Before().Exists() != (before[write.Path()] != nil) || !bytes.Equal(write.Before().Data(), before[write.Path()]) {
				t.Fatal("captured preimage differs from original authored bytes")
			}
			if write.AfterExists() && write.AfterMode() == 0 {
				t.Fatal("candidate mode was not retained")
			}
			switch write.Path() {
			case "plystra.yaml":
				foundConfiguration = write.Before().Exists() && write.AfterExists() && bytes.Contains(write.After(), []byte([]string{"PRIVATE_FIRST", "PRIVATE_OTHER"}[index]))
			case "dist/runtime-baseline.json":
				foundBaseline = !write.Before().Exists() && write.AfterExists()
			case "todo/add/implementation.go":
				foundSource = !write.Before().Exists() && bytes.Contains(write.After(), []byte("package add"))
			}
			assertCandidateCaptureRedaction(t, write, root)
			if data := write.After(); len(data) != 0 {
				data[0] ^= 1
				if bytes.Equal(data, write.After()) {
					t.Fatal("candidate write exposes mutable stored bytes")
				}
			}
		}
		if !foundConfiguration || !foundBaseline || !foundSource || len(writes) != len(planned.WriteSet()) {
			t.Fatal("private capture and public writes disagree")
		}
		canonical := captured.CanonicalRequest()
		canonical[0] ^= 1
		writes[0] = scaffoldplan.CapturedWrite{}
		if !captured.MatchesRequest(request) || captured.Writes()[0].Path() == "" {
			t.Fatal("private capture exposes mutable request or write storage")
		}
		assertCandidateCaptureRedaction(t, captured, root)
		if !reflect.DeepEqual(before, commandTree(t, root)) {
			t.Fatal("private capture changed original Project")
		}
		captures = append(captures, captured)
	}
	if captures[0].Writes()[0].Path() != captures[1].Writes()[0].Path() {
		t.Fatal("private request values changed canonical path ordering")
	}
	writeCommandFile(t, filepath.Join(root, "untracked.txt"), "concurrent authored work")
	for _, captured := range captures {
		if err := captured.Recheck(t.Context()); err == nil {
			t.Fatal("closed-stage capture accepted changed original membership")
		}
	}
}

func TestScaffoldPreviewCapturesResolvedConfigurationSelection(t *testing.T) {
	for _, mode := range []string{applicationgen.ConfigurationModeEnvironment, applicationgen.ConfigurationModeExplicit} {
		t.Run(mode, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			writeCommandConfigurableImplementation(t, root, "reports", "reports.read/v1", "reports/read/v1", "Read")
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\nconfig:\n  example.com/acme/implementation-use/reports.New: {endpoint: PRIVATE_ROOT}\n")
			selected, original := "plystra.production.yaml", "{}\n"
			environment := map[string]string{"PLYSTRA_ENV": "production"}
			if mode == applicationgen.ConfigurationModeExplicit {
				selected, original = "deploy/customer.yaml", "interfaces:\n  require: [reports.read/v1]\n"
				environment = map[string]string{"PLYSTRA_CONFIG": filepath.Join(root, filepath.FromSlash(selected))}
			}
			writeCommandFile(t, filepath.Join(root, filepath.FromSlash(selected)), original)
			data, err := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": []map[string]any{
				{"kind": "interface_create", "name": "todo.add", "semantics": "query"},
				{"kind": "configuration", "action": "set", "owner": "example.com/acme/implementation-use/reports.New", "path": "endpoint", "value": "PRIVATE_SELECTED"},
			}})
			if err != nil {
				t.Fatal(err)
			}
			request, err := changerequest.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			before := commandTree(t, root)
			planned, err := scaffoldplan.Preview(t.Context(), scaffoldplan.Options{Start: root, Environment: implementationSelectionCommandEnvironment(environment)}, request)
			if err != nil {
				t.Fatal(err)
			}
			captured := planned.Capture()
			if captured.ConfigurationSelection().Mode() != mode || captured.ConfigurationSelection().Path() != selected {
				t.Fatal("capture lost the original resolved ambient selector")
			}
			found := false
			for _, write := range captured.Writes() {
				if write.Path() == "plystra.yaml" {
					t.Fatal("selected configuration capture planned a root edit")
				}
				if write.Path() == selected {
					found = bytes.Equal(write.Before().Data(), []byte(original)) && bytes.Contains(write.After(), []byte("PRIVATE_SELECTED"))
				}
			}
			if !found || !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("selected configuration preimage or postimage differs from captured intent")
			}
		})
	}
}

func TestScaffoldPreviewCaptureRetainsDeletedAndRelocatedFiles(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "http:\n  expose:\n    reports.read/v1: {transport: connect}\n")
	seedScaffoldGeneratedProject(t, root)
	before := commandTree(t, root)
	operations := append(scaffoldCommandOperations(), map[string]any{"kind": "interface_root", "action": "absent", "interface": "reports.read/v1", "root": "expose"})
	data, err := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": operations})
	if err != nil {
		t.Fatal(err)
	}
	request, err := changerequest.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := scaffoldplan.Preview(t.Context(), scaffoldplan.Options{Start: root, Environment: implementationSelectionCommandEnvironment(nil)}, request)
	if err != nil {
		t.Fatal(err)
	}
	foundDeletion := false
	for _, write := range planned.Capture().Writes() {
		if !bytes.Equal(write.Before().Data(), before[write.Path()]) {
			t.Fatal("retained preimage contains relocated or changed bytes")
		}
		if !write.AfterExists() {
			foundDeletion = true
			if !write.Before().Exists() || len(write.Before().Data()) == 0 || write.After() != nil || write.AfterMode() != 0 {
				t.Fatal("deleted file lost its exact original content or retained a postimage")
			}
		}
		if write.Path() == "go.mod" && bytes.Contains(write.After(), []byte("plystra-stage-")) {
			t.Fatal("temporary module replacement escaped into captured output")
		}
	}
	if !foundDeletion || !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatal("generated deletion was not captured without Project writes")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := planned.Capture().Recheck(ctx); err == nil {
		t.Fatal("capture recheck ignored cancellation")
	}
}

func TestScaffoldPreviewFailureDoesNotReturnUsableCapture(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	request, err := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"email.send","semantics":"query"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	planned, err := scaffoldplan.Preview(t.Context(), scaffoldplan.Options{Start: root, Environment: implementationSelectionCommandEnvironment(nil)}, request)
	if err == nil || planned.Capture().Valid() || planned.Capture().MatchesRequest(request) || len(planned.Capture().Writes()) != 0 || planned.FinalModelDigest() != "" || !planned.Effects().Valid() {
		t.Fatal("failed candidate retained usable private capture or lost effects")
	}
	if (scaffoldplan.Capture{}).Recheck(t.Context()) == nil {
		t.Fatal("zero capture accepted")
	}
}

func TestScaffoldPreviewRetainsExternalGoEnvironment(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
	settings, parent := t.TempDir(), t.TempDir()
	path := filepath.Join(settings, "env")
	writeCommandFile(t, path, "GOFLAGS=-tags=PRIVATE_GOENV_CAPTURE\n")
	environment := implementationSelectionCommandEnvironment(map[string]string{"GOENV": path})
	filtered := environment[:0]
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "GOFLAGS") {
			filtered = append(filtered, entry)
		}
	}
	request, err := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"todo.add","semantics":"command"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	before := commandTree(t, root)
	planned, err := scaffoldplan.Preview(t.Context(), scaffoldplan.Options{Start: root, TemporaryParent: parent, Environment: filtered}, request)
	if err != nil || !planned.Capture().Valid() {
		t.Fatalf("external GOENV preview failed: %v", err)
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatal("preview retained temporary files")
	}
	if err := planned.Capture().Recheck(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertCandidateCaptureRedaction(t, planned, settings)
	assertCandidateCaptureRedaction(t, planned.Capture(), settings)
	writeCommandFile(t, path, "GOFLAGS=-tags=PRIVATE_GOENV_CHANGED\n")
	if err := planned.Capture().Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("retained capture accepted changed external GOENV: %v", err)
	}
	if !reflect.DeepEqual(before, commandTree(t, root)) || string(readCommandFile(t, settings, "env")) != "GOFLAGS=-tags=PRIVATE_GOENV_CHANGED\n" {
		t.Fatal("preview changed original inputs or overwrote external editing")
	}
}

func TestScaffoldPreviewRetainsSelectedDependencySources(t *testing.T) {
	for _, mode := range []string{"existing", "added", "updated", "removed", "versioned-replacement", "transitive", "aliased-cache"} {
		t.Run(mode, func(t *testing.T) {
			modules := []commandProxyModule{
				previewDependency(t, "v1.0.0", false), previewDependency(t, "v1.1.0", false),
			}
			const support = "example.com/preview/width"
			if mode == "transitive" {
				modules[0].goMod = "module " + previewDependencyModule + "\n\ngo 1.26\nrequire " + support + " v1.0.0\n"
				modules[0].sources["remote/implementation.go"] = strings.ReplaceAll(modules[0].sources["remote/implementation.go"], "\"context\"", "\"context\"\nwidth \""+support+"\"")
				modules[0].sources["remote/implementation.go"] = strings.ReplaceAll(modules[0].sources["remote/implementation.go"], "type Config struct {", "type Config struct { Padding [width.Size]int64;")
				modules = append(modules, commandProxyModule{path: support, version: "v1.0.0", ordinary: true, sources: map[string]string{"width.go": "package project\nconst Size = 1\n"}})
			}
			proxy := writeCommandDependencyProxy(t, modules)
			env := previewDependencyEnvironment(t, proxy)
			if mode == "aliased-cache" {
				for index, entry := range env {
					if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "GOMODCACHE") {
						alias := filepath.Join(t.TempDir(), "cache")
						if err := os.Symlink(value, alias); err != nil {
							t.Fatal(err)
						}
						env[index] = name + "=" + alias
					}
				}
			}
			root := writeImplementationSelectionCommandProject(t)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\n")
			if mode != "added" {
				if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: env}, "get", previewDependencyModule+"@v1.0.0"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "existing" || mode == "versioned-replacement" || mode == "transitive" || mode == "aliased-cache" {
				writeCommandFile(t, filepath.Join(root, "keep", "keep.go"), "package keep\nimport _ \""+previewDependencyModule+"/echo/v1\"\n")
			}
			if mode == "versioned-replacement" {
				if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: env}, "mod", "edit", "-replace="+previewDependencyModule+"="+previewDependencyModule+"@v1.1.0"); err != nil {
					t.Fatal(err)
				}
				if err := gocommand.Run(t.Context(), gocommand.Options{Directory: root, Environment: env}, "mod", "download", previewDependencyModule); err != nil {
					t.Fatal(err)
				}
			}
			operations := []map[string]any{{"kind": "interface_create", "name": "todo.add", "semantics": "command"}}
			if mode == "added" || mode == "updated" {
				action, version := "add", "v1.0.0"
				if mode == "updated" {
					action, version = "update", "v1.1.0"
				}
				operations = append(operations, map[string]any{"kind": "dependency", "action": action, "module": previewDependencyModule + "@" + version})
			} else if mode == "removed" {
				operations = append(operations, map[string]any{"kind": "dependency", "action": "remove", "module": previewDependencyModule})
			}
			data, err := json.Marshal(map[string]any{"schema": "plystra.change/v1", "operations": operations})
			if err != nil {
				t.Fatal(err)
			}
			request, err := changerequest.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			parent := t.TempDir()
			before := commandTree(t, root)
			planned, err := scaffoldplan.Preview(t.Context(), scaffoldplan.Options{Start: root, Environment: env, TemporaryParent: parent}, request)
			if err != nil || !planned.Capture().Valid() {
				t.Fatalf("%s selected source preview failed: %v", mode, err)
			}
			if err := planned.Capture().Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatal("preview retained temporary files")
			}
			var cache string
			for _, entry := range env {
				if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "GOMODCACHE") {
					cache = value
				}
			}
			if cache == "" {
				t.Fatal("test has no isolated module cache")
			}
			writeCommandFile(t, filepath.Join(cache, "unrelated@v1.0.0", "value.go"), "unrelated cache input")
			if err := planned.Capture().Recheck(t.Context()); err != nil {
				t.Fatal("unrelated cache edit invalidated retained inputs")
			}
			versions := []string{"v1.0.0"}
			if mode == "updated" {
				versions = append(versions, "v1.1.0")
			} else if mode == "versioned-replacement" {
				versions = []string{"v1.1.0"}
			}
			for _, version := range versions {
				path := filepath.Join(cache, filepath.FromSlash(previewDependencyModule+"@"+version), "remote", "implementation.go")
				if mode == "transitive" {
					path = filepath.Join(cache, filepath.FromSlash(support+"@"+version), "width.go")
				}
				original, err := os.ReadFile(path)
				info, statErr := os.Stat(path)
				if err != nil || statErr != nil {
					t.Fatalf("missing consumed %s source: %v, %v", version, err, statErr)
				}
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
				changed := bytes.ReplaceAll(original, []byte("string"), []byte("uint64"))
				if mode == "transitive" {
					changed = bytes.ReplaceAll(original, []byte("Size = 1"), []byte("Size = 2"))
				}
				if bytes.Equal(original, changed) {
					t.Fatal("test did not change a compiled field type")
				}
				writeCommandFile(t, path, string(changed))
				if err := os.Chmod(path, info.Mode()); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
				if err := planned.Capture().Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("%s consumed %s source drift accepted: %v", mode, version, err)
				}
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
				writeCommandFile(t, path, string(original))
				if err := os.Chmod(path, info.Mode()); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
				if err := planned.Capture().Recheck(t.Context()); err != nil {
					t.Fatal("restored exact source preimage failed recheck")
				}
			}
			assertCandidateCaptureRedaction(t, planned, cache)
			if mode == "aliased-cache" {
				if err := os.Remove(cache); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), cache); err != nil {
					t.Fatal(err)
				}
				if err := planned.Capture().Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("retained capture accepted retargeted Go source directory: %v", err)
				}
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("preview modified original Project")
			}
		})
	}
}

func TestRunChangePlanCapturedValuesKeepPublicIdentityPrivate(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandConfigurableImplementation(t, root, "reports", "reports.read/v1", "reports/read/v1", "Read")
	source := string(readCommandFile(t, root, "reports/implementation.go"))
	source = strings.Replace(source, `"context"`, "\"context\"\n\"github.com/plystra/kernel/configuration\"", 1)
	source = strings.Replace(source, "type Config struct {", "type Config struct {\n Password configuration.Secret", 1)
	writeCommandFile(t, filepath.Join(root, "reports", "implementation.go"), source)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "interfaces:\n  require: [reports.read/v1]\nconfig:\n  example.com/acme/implementation-use/reports.New: {endpoint: PRIVATE_ORIGINAL, password: {env: PRIVATE_TARGET_A}}\n")
	var previous json.RawMessage
	for _, pair := range [][2]string{{"PRIVATE_ORIGINAL", "PRIVATE_TARGET_A"}, {"PRIVATE_DIFFERENT", "PRIVATE_TARGET_B"}} {
		operations := append(scaffoldCommandOperations(),
			map[string]any{"kind": "configuration", "action": "set", "owner": "example.com/acme/implementation-use/reports.New", "path": "endpoint", "value": pair[0]},
			map[string]any{"kind": "configuration", "action": "set", "owner": "example.com/acme/implementation-use/reports.New", "path": "password", "value": map[string]any{"env": pair[1]}},
		)
		result, payload, output := runScaffoldPlan(t, root, operations)
		if result.Payload.Completeness != "non_exact" || strings.Contains(output, "PRIVATE_") {
			t.Fatal("private capture changed preview completeness or disclosed requested data")
		}
		if previous != nil && !bytes.Equal(previous, payload) {
			t.Fatal("captured submitted values or Secret targets changed public equality")
		}
		previous = payload
	}
}

func assertCandidateCaptureRedaction(t *testing.T, value interface {
	MarshalText() ([]byte, error)
}, root string) {
	t.Helper()
	wrappers := []any{value, struct{ private any }{value}, struct{ private []any }{[]any{value}}, struct{ private [1]any }{[1]any{value}}, struct{ private map[string]any }{map[string]any{"capture": value}}}
	switch typed := value.(type) {
	case scaffoldplan.Capture:
		wrappers = append(wrappers, struct{ private scaffoldplan.Capture }{typed})
	case scaffoldplan.CapturedWrite:
		wrappers = append(wrappers, struct{ private scaffoldplan.CapturedWrite }{typed})
	}
	hexBytes := fmt.Sprintf("%#v", []byte("PRIVATE_"))
	_, hexNumeric, _ := strings.Cut(hexBytes, "{")
	hexNumeric = strings.TrimSuffix(hexNumeric, "}")
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		for _, nested := range wrappers {
			text := fmt.Sprintf(format, nested)
			numeric := strings.Trim(fmt.Sprint([]byte("PRIVATE_")), "[]")
			if strings.Contains(text, "PRIVATE_") || strings.Contains(text, numeric) || strings.Contains(text, hexNumeric) || strings.Contains(text, root) || strings.Contains(text, "sha256:") {
				t.Fatal("private candidate capture leaked through direct or nested formatting")
			}
		}
	}
	if data, err := json.Marshal(value); err == nil || data != nil {
		t.Fatal("private candidate capture accepted JSON serialization")
	}
	if data, err := yaml.Marshal(value); err == nil || data != nil {
		t.Fatal("private candidate capture accepted YAML serialization")
	}
	if data, err := value.MarshalText(); err == nil || data != nil {
		t.Fatal("private candidate capture accepted text serialization")
	}
	var output bytes.Buffer
	for _, handler := range []slog.Handler{slog.NewTextHandler(&output, nil), slog.NewJSONHandler(&output, nil)} {
		slog.New(handler).Info("capture", "value", value)
		if strings.Contains(output.String(), "PRIVATE_") || strings.Contains(output.String(), root) || strings.Contains(output.String(), "sha256:") {
			t.Fatal("private candidate capture leaked through structured logging")
		}
		output.Reset()
	}
}
