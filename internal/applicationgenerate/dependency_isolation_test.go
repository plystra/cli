package applicationgenerate_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/runtimebaseline"
)

func TestGeneratedBinaryIgnoresDependencyApplicationAdoptionChains(t *testing.T) {
	const direct = "example.com/isolation-direct"
	const transitive = "example.com/isolation-transitive"
	sources := filepath.Join(t.TempDir(), "sources")
	root := filepath.Join(sources, "consumer")
	directRoot, transitiveRoot := filepath.Join(sources, "direct"), filepath.Join(sources, "transitive")
	writeApplicationModule(t, root, "example.com/isolation-consumer")
	for _, dependency := range []struct {
		root, module, id, marker string
	}{
		{directRoot, direct, "safe.run/v1", "PLYSTRA_ISOLATION_DIRECT_MARKER"},
		{transitiveRoot, transitive, "hidden.run/v1", "PLYSTRA_ISOLATION_TRANSITIVE_MARKER"},
	} {
		writeApplicationModule(t, dependency.root, dependency.module)
		writeAssemblyInterface(t, dependency.root, "probe/run/v1", "runv1", dependency.id, "Run", "type Request struct{}\ntype Response struct{}\n")
		writeFile(t, filepath.Join(dependency.root, "service/service.go"), fmt.Sprintf(`package service
import (
 "context"
 "os"
 "github.com/plystra/kernel/configuration"
 runv1 %q
)
type Config struct { Value string; Password configuration.Secret }
type service struct{}
//plystra:implements %s
func New(c Config) (*service, error) {
 value := c.Value
 if c.Password.Valid() { value += ":" + string(c.Password.Bytes()) }
 if err := os.WriteFile(os.Getenv(%q), []byte(value), 0600); err != nil { return nil, err }
 return &service{}, nil
}
func (*service) Run(context.Context, runv1.Request) (runv1.Response, error) { return runv1.Response{}, nil }
`, dependency.module+"/interfaces/probe/run/v1", dependency.id, dependency.marker))
	}
	writeFile(t, filepath.Join(directRoot, "go.mod"), string(readAbsoluteFile(t, filepath.Join(directRoot, "go.mod")))+fmt.Sprintf("\nrequire %s v1.0.0\nreplace %s => %s\n", transitive, transitive, filepath.ToSlash(transitiveRoot)))
	writeFile(t, filepath.Join(root, "go.mod"), string(readAbsoluteFile(t, filepath.Join(root, "go.mod")))+fmt.Sprintf("\nrequire %s v1.0.0\nreplace %s => %s\nreplace %s => %s\n", direct, direct, filepath.ToSlash(directRoot), transitive, filepath.ToSlash(transitiveRoot)))
	initialGoMod := string(readAbsoluteFile(t, filepath.Join(root, "go.mod")))
	initialGoSum := string(readAbsoluteFile(t, filepath.Join(root, "go.sum")))
	var cliEnvironment []string
	for _, entry := range goEnvironment(nil) {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "PLYSTRA_ENV") && !strings.EqualFold(name, "PLYSTRA_CONFIG") {
			cliEnvironment = append(cliEnvironment, entry)
		}
	}
	assertTransitiveGraph := func(t testing.TB) {
		t.Helper()
		list := exec.CommandContext(t.Context(), "go", "list", "-mod=readonly", "-m", "-json", direct, transitive)
		list.Dir, list.Env = root, cliEnvironment
		output, err := list.Output()
		if err != nil {
			t.Fatalf("list dependency graph: %v", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(output))
		for _, module := range []string{direct, transitive} {
			var selected struct {
				Path     string
				Indirect bool
			}
			if err := decoder.Decode(&selected); err != nil || selected.Path != module || selected.Indirect != (module == transitive) {
				t.Fatalf("dependency graph for %s = %#v, %v", module, selected, err)
			}
		}
	}
	const directExports = `  exports:
    safe:
      interfaces:
        require: [safe.run/v1]
        use: {safe.run/v1: example.com/isolation-direct/service.New}
        policies: {safe.run/v1: {timeout: 1s}}
      config:
        example.com/isolation-direct/service.New: {value: private-safe}
`
	const transitiveExports = `  exports:
    standalone:
      interfaces:
        require: [hidden.run/v1]
        use: {hidden.run/v1: example.com/isolation-transitive/service.New}
      config:
        example.com/isolation-transitive/service.New:
          value: private-activated
          password: {env: PLYSTRA_ISOLATION_SECRET}
`
	writeDependencies := func(t testing.TB, suffix string) {
		t.Helper()
		for _, dependency := range []struct{ root, exports, adopt string }{
			{directRoot, directExports, "{module: " + transitive + ", export: standalone}"},
			{transitiveRoot, transitiveExports, "{module: " + direct + ", export: safe}"},
		} {
			ignored := fmt.Sprintf(`interfaces:
  require: [missing.ignored/v1]
  use: {safe.run/v1: example.com/absent/service.New}
  policies: {safe.run/v1: {timeout: 2s}}
config:
  example.com/isolation-direct/service.New: {value: private-ignored-%s}
  example.com/isolation-transitive/service.New: {password: {env: PLYSTRA_IGNORED_SECRET}}
http:
  address: private-ignored-%s
  expose: {hidden.run/v1: {transport: connect}}
timeouts: {startup: 1ns}
resources:
  instances: {ignored.database: {use: example.com/absent/database.New, config: {private: ignored}}}
  bind: {implementations: {example.com/isolation-direct/service.New: {database: ignored.database}}}
`, suffix, suffix)
			adopt := dependency.adopt
			if suffix == "changed" {
				adopt += ", {module: example.com/missing, export: ignored}"
			}
			writeFile(t, filepath.Join(dependency.root, "plystra.yaml"), "composition:\n  adopt: ["+adopt+"]\n"+dependency.exports+ignored)
			writeFile(t, filepath.Join(dependency.root, "plystra.test.yaml"), "ignored dependency overlay: ["+suffix)
			writeFile(t, filepath.Join(dependency.root, "selected.yaml"), "ignored dependency replacement: ["+suffix)
		}
	}
	consumerDocuments := func(t testing.TB, directory, mode string, activate bool) []string {
		t.Helper()
		adoptions := "{module: " + direct + ", export: safe}"
		if activate {
			adoptions += ", {module: " + transitive + ", export: standalone}"
		}
		selected := "composition: {adopt: [" + adoptions + "]}\n"
		rootDocument, overlay, replacement := selected, "{}\n", "{}\n"
		var selector []string
		switch mode {
		case "environment":
			rootDocument, overlay = "{}\n", selected
			selector = []string{"--env", "test"}
		case "replacement":
			rootDocument, replacement = "interfaces: {require: [missing.root/v1]}\n", selected
			selector = []string{"--config", "selected.yaml"}
		}
		writeFile(t, filepath.Join(directory, "plystra.yaml"), rootDocument)
		writeFile(t, filepath.Join(directory, "plystra.test.yaml"), overlay)
		writeFile(t, filepath.Join(directory, "selected.yaml"), replacement)
		return selector
	}
	runCLI := func(t testing.TB, args ...string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := command.RunIn(args, &stdout, &stderr, root, cliEnvironment); code != 0 {
			t.Fatalf("%v = %d: %s\n%s", args, code, stdout.Bytes(), stderr.Bytes())
		}
	}
	deployment := t.TempDir()
	type application struct {
		activate         bool
		binary, baseline string
	}
	var applications []application
	for _, activate := range []bool{false, true} {
		for _, mode := range []string{"default", "environment", "replacement"} {
			t.Run(fmt.Sprintf("generation/%t/%s", activate, mode), func(t *testing.T) {
				writeFile(t, filepath.Join(root, "go.mod"), initialGoMod)
				writeFile(t, filepath.Join(root, "go.sum"), initialGoSum)
				selector := consumerDocuments(t, root, mode, activate)
				writeDependencies(t, "original")
				assertTransitiveGraph(t)
				directBefore, transitiveBefore := snapshotTree(t, directRoot), snapshotTree(t, transitiveRoot)
				runCLI(t, append([]string{"generate"}, selector...)...)
				if !activate {
					assertTransitiveGraph(t)
				}
				generated := snapshotGenerated(t, root)
				baselinePath := filepath.Join(root, "dist/runtime-baseline.json")
				baselineBytes := readAbsoluteFile(t, baselinePath)
				baseline, err := runtimebaseline.Decode(baselineBytes)
				if err != nil || len(baseline.Exports) != 2 {
					t.Fatalf("dependency inventories = %d, %v", len(baseline.Exports), err)
				}
				for _, inventory := range baseline.Exports {
					if inventory.Module != direct && inventory.Module != transitive || strings.Contains(inventory.YAML, "adopt:") || strings.Contains(inventory.YAML, "ignored") {
						t.Fatal("baseline included dependency application declarations")
					}
				}
				provenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
				if err != nil {
					t.Fatal(err)
				}
				var bindings []string
				for _, binding := range provenance.InterfaceProvenance().Bindings() {
					bindings = append(bindings, binding.InterfaceID())
				}
				wantBindings := []string{"safe.run/v1"}
				if activate {
					wantBindings = []string{"hidden.run/v1", "safe.run/v1"}
				}
				if !reflect.DeepEqual(bindings, wantBindings) {
					t.Fatalf("effective bindings = %v, want %v", bindings, wantBindings)
				}
				for _, entry := range generated {
					for _, excluded := range []string{"private-ignored", "PLYSTRA_IGNORED_SECRET", "missing.ignored/v1", "ignored.database", "private-safe", "private-activated", "PLYSTRA_ISOLATION_SECRET"} {
						if bytes.Contains(entry.data, []byte(excluded)) {
							t.Fatalf("%s exposed ignored or private configuration", entry.path)
						}
					}
				}
				if !reflect.DeepEqual(directBefore, snapshotTree(t, directRoot)) || !reflect.DeepEqual(transitiveBefore, snapshotTree(t, transitiveRoot)) {
					t.Fatal("generation changed a dependency Project")
				}
				writeDependencies(t, "changed")
				before := snapshotTree(t, sources)
				runCLI(t, append([]string{"generate", "--check"}, selector...)...)
				runCLI(t, append([]string{"check"}, selector...)...)
				if !reflect.DeepEqual(before, snapshotTree(t, sources)) {
					t.Fatal("public checks changed the consumer or a dependency")
				}
				runCLI(t, append([]string{"generate"}, selector...)...)
				if !reflect.DeepEqual(generated, snapshotGenerated(t, root)) || !bytes.Equal(baselineBytes, readAbsoluteFile(t, baselinePath)) {
					t.Fatal("ignored dependency edits changed generated output or private inventories")
				}
				if !reflect.DeepEqual(before, snapshotTree(t, sources)) {
					t.Fatal("regeneration changed authored or unselected documents")
				}
			})
		}
		if t.Failed() {
			return
		}
		// Prepare deployment independently of which generation subtests were selected.
		writeDependencies(t, "changed")
		selector := consumerDocuments(t, root, "replacement", activate)
		runCLI(t, append([]string{"generate"}, selector...)...)
		binary := filepath.Join(deployment, fmt.Sprintf("application-%t", activate))
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		build := exec.CommandContext(t.Context(), "go", "build", "-race", "-mod=readonly", "-o", binary, "./generated/go/application")
		build.Dir, build.Env = root, goEnvironment(nil)
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, output)
		}
		baseline := filepath.Join(deployment, fmt.Sprintf("baseline-%t.json", activate))
		copyPrivateBaseline(t, filepath.Join(root, "dist/runtime-baseline.json"), baseline)
		applications = append(applications, application{activate: activate, binary: binary, baseline: baseline})
	}
	if err := os.Rename(sources, sources+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	for _, app := range applications {
		for _, mode := range []string{"default", "environment", "replacement"} {
			for _, supplySecret := range []bool{false, true} {
				if !app.activate && supplySecret {
					continue
				}
				t.Run(fmt.Sprintf("deployment/%t/%s/secret-%t", app.activate, mode, supplySecret), func(t *testing.T) {
					configurationRoot, markers := t.TempDir(), t.TempDir()
					selector := consumerDocuments(t, configurationRoot, mode, app.activate)
					directMarker, transitiveMarker := filepath.Join(markers, "direct"), filepath.Join(markers, "transitive")
					args := append([]string{"--smoke", "--configuration-root", configurationRoot, "--runtime-baseline", app.baseline}, selector...)
					process := exec.CommandContext(t.Context(), app.binary, args...)
					process.Dir = deployment
					for _, entry := range goEnvironment(map[string]string{"PLYSTRA_ISOLATION_DIRECT_MARKER": directMarker, "PLYSTRA_ISOLATION_TRANSITIVE_MARKER": transitiveMarker}) {
						name, _, _ := strings.Cut(entry, "=")
						if strings.EqualFold(name, "PLYSTRA_ENV") || strings.EqualFold(name, "PLYSTRA_CONFIG") || strings.EqualFold(name, "PLYSTRA_ISOLATION_SECRET") || strings.EqualFold(name, "PLYSTRA_IGNORED_SECRET") {
							continue
						}
						process.Env = append(process.Env, entry)
					}
					if supplySecret {
						process.Env = append(process.Env, "PLYSTRA_ISOLATION_SECRET=private-resolved")
					}
					before := snapshotTree(t, configurationRoot)
					output, err := process.CombinedOutput()
					if !reflect.DeepEqual(before, snapshotTree(t, configurationRoot)) {
						t.Fatal("startup changed selected or unselected configuration")
					}
					for _, excluded := range []string{"private-", "PLYSTRA_ISOLATION_SECRET", "PLYSTRA_IGNORED_SECRET"} {
						if bytes.Contains(output, []byte(excluded)) {
							t.Fatal("startup exposed private inputs")
						}
					}
					if app.activate && !supplySecret {
						if err == nil || !bytes.Contains(output, []byte("resolve constructor Secret")) {
							t.Fatalf("adopted missing Secret = %v\n%s", err, output)
						}
						assertFileMissing(t, markers, "direct")
						assertFileMissing(t, markers, "transitive")
						return
					}
					if err != nil {
						t.Fatalf("startup = %v\n%s", err, output)
					}
					if string(readAbsoluteFile(t, directMarker)) != "private-safe" {
						t.Fatal("dependency application configuration replaced the selected export")
					}
					if !app.activate {
						assertFileMissing(t, markers, "transitive")
					} else if string(readAbsoluteFile(t, transitiveMarker)) != "private-activated:private-resolved" {
						t.Fatal("explicit transitive adoption was not delivered")
					}
				})
			}
		}
	}
}
