package applicationgenerate_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/runtimebaseline"
	"go.yaml.in/yaml/v3"
)

func TestGeneratedBinaryComposesPrivateDependencyExports(t *testing.T) {
	const module = "example.com/adopted-consumer"
	const dependency = "example.com/adopted-dependency"
	const symbol = dependency + "/service.New"
	sources := filepath.Join(t.TempDir(), "sources")
	root, dependencyRoot := filepath.Join(sources, "application"), filepath.Join(sources, "dependency")
	writeApplicationModule(t, root, module)
	writeApplicationModule(t, dependencyRoot, dependency)
	writeAssemblyInterface(t, dependencyRoot, "probe/run/v1", "runv1", "probe.run/v1", "Run", "type Request struct{}\ntype Response struct{}\n")
	writeFile(t, filepath.Join(dependencyRoot, "service/service.go"), strings.ReplaceAll(`package service
import (
 "context"
 "encoding/json"
 "os"
 "github.com/plystra/kernel/configuration"
 runv1 "example.com/adopted-dependency/interfaces/probe/run/v1"
)
type Nested struct {
 Name string @@yaml:"name" plystra-default:"private-default"@@
 Count int8 @@yaml:"count" plystra-default:"3"@@
}
type Config struct {
 First string @@yaml:"first" plystra:"required"@@
 Second string @@yaml:"second" plystra:"required"@@
 Public int8 @@yaml:"public" plystra:"build-visible"@@
 Nested Nested @@yaml:"nested"@@
 Pointer *Nested @@yaml:"pointer"@@
 Mapping map[string]string @@yaml:"mapping"@@
 Password configuration.Secret @@yaml:"password"@@
}
type service struct{}
//plystra:implements probe.run/v1
func New(c Config) (*service,error) {
 if marker:=os.Getenv("PLYSTRA_ADOPTED_MARKER");marker!="" {
  data,err:=json.Marshal(map[string]any{"first":c.First,"second":c.Second,"public":c.Public,"nested":c.Nested,"pointer":c.Pointer,"mapping":c.Mapping,"secret":string(c.Password.Bytes())})
  if err!=nil{return nil,err}
  if err:=os.WriteFile(marker,data,0600);err!=nil{return nil,err}
 }
 return &service{},nil
}
func (*service) Run(context.Context,runv1.Request)(runv1.Response,error){return runv1.Response{},nil}
`, "@@", "`"))
	const exports = `composition:
  exports:
    first:
      interfaces:
        require: [probe.run/v1]
        use: {probe.run/v1: example.com/adopted-dependency/service.New}
        policies: {probe.run/v1: {timeout: 1s}}
      config:
        example.com/adopted-dependency/service.New:
          first: private-first
          public: 7
          nested: {name: private-inherited}
          pointer: {name: private-pointer, count: 9}
          mapping: {private-key: private-value}
    second:
      interfaces:
        require: [probe.run/v1]
        use: {probe.run/v1: example.com/adopted-dependency/service.New}
        policies: {probe.run/v1: {timeout: 1000ms}}
      config:
        example.com/adopted-dependency/service.New:
          second: private-second
          nested: {count: 5}
          password: {env: PRIVATE_ADOPTED_SECRET}
`
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), exports+"interfaces: {require: [missing.inert/v1]}\nhttp: {address: private-inert-address}\n")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.test.yaml"), "invalid dependency overlay: [")
	goMod := string(readAbsoluteFile(t, filepath.Join(root, "go.mod"))) + "\nrequire " + dependency + " v1.0.0\nreplace " + dependency + " => " + filepath.ToSlash(dependencyRoot) + "\n"
	writeFile(t, filepath.Join(root, "go.mod"), goMod)
	const adopt = "composition: {adopt: [{module: " + dependency + ", export: first}, {module: " + dependency + ", export: second}]}\n"
	writeFile(t, filepath.Join(root, "plystra.yaml"), adopt)
	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("generate = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	for _, file := range snapshotTree(t, filepath.Join(root, "generated")) {
		for _, private := range []string{"private-first", "private-second", "private-inherited", "private-pointer", "private-key", "private-value", "PRIVATE_ADOPTED_SECRET"} {
			if bytes.Contains(file.data, []byte(private)) {
				t.Fatalf("generated artifact %s exposed private adopted input", file.path)
			}
		}
	}
	baselineBytes := readAbsoluteFile(t, filepath.Join(root, "dist/runtime-baseline.json"))
	baseline, err := runtimebaseline.Decode(baselineBytes)
	if err != nil {
		t.Fatal(err)
	}
	dependencyIndex := -1
	for i, export := range baseline.Exports {
		if export.Module == dependency {
			dependencyIndex = i
		}
	}
	if dependencyIndex < 0 {
		t.Fatal("dependency inventory was not captured")
	}
	if strings.Contains(baseline.Exports[dependencyIndex].YAML, "private-inert-address") {
		t.Fatal("inert process settings entered baseline")
	}
	deployment := t.TempDir()
	binary := filepath.Join(deployment, "app")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-race", "-mod=readonly", "-o", binary, "./generated/go/application")
	build.Dir, build.Env = root, goEnvironment(nil)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	privatePath := filepath.Join(deployment, "baseline.json")
	copyPrivateBaseline(t, filepath.Join(root, "dist/runtime-baseline.json"), privatePath)
	if err := os.Rename(sources, sources+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	config := func(value string) string { return "config:\n  " + symbol + ": " + value + "\n" }
	change := func(d *runtimebaseline.Document, value string, path ...string) {
		t.Helper()
		var document, replacement yaml.Node
		if err := yaml.Unmarshal([]byte(d.Exports[dependencyIndex].YAML), &document); err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal([]byte(value), &replacement); err != nil {
			t.Fatal(err)
		}
		node := document.Content[0]
		for i, key := range append([]string{"composition", "exports"}, path...) {
			if node.Kind != yaml.MappingNode {
				t.Fatal("invalid test edit path")
			}
			index := -1
			for j := 0; j < len(node.Content); j += 2 {
				if node.Content[j].Value == key {
					index = j + 1
					break
				}
			}
			if index < 0 {
				node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"})
				index = len(node.Content) - 1
			}
			if i == len(path)+1 {
				node.Content[index] = replacement.Content[0]
				break
			}
			node = node.Content[index]
		}
		data, err := yaml.Marshal(&document)
		if err != nil {
			t.Fatal(err)
		}
		d.Exports[dependencyIndex].YAML = string(data)
	}
	for _, tc := range []struct {
		name, root, overlay, selected, rule string
		edit                                func(*runtimebaseline.Document)
		check                               func(*testing.T, map[string]any)
	}{
		{name: "dependency-only deployment", check: func(t *testing.T, got map[string]any) {
			if got["first"] != "private-first" || got["second"] != "private-second" || got["public"] != float64(7) || got["secret"] != "private-resolved" || got["nested"].(map[string]any)["Count"] != float64(5) {
				t.Fatal("adopted inputs were not delivered")
			}
		}},
		{name: "private baseline refresh", edit: func(d *runtimebaseline.Document) {
			d.Exports[dependencyIndex].YAML = strings.ReplaceAll(d.Exports[dependencyIndex].YAML, "private-first", "private-refreshed")
		}, check: func(t *testing.T, got map[string]any) {
			if got["first"] != "private-refreshed" {
				t.Fatal("private refresh did not take effect")
			}
		}},
		{name: "root replacement", root: adopt + config("{first: local}"), check: func(t *testing.T, got map[string]any) {
			if got["first"] != "local" || got["second"] != "private-second" {
				t.Fatal("wrong root precedence")
			}
		}},
		{name: "overlay tombstones and atomic replacement", root: adopt + config("{nested: {name: {$remove: true}}}"), overlay: config("{nested: {count: 6}, pointer: {}, mapping: {}}"), check: func(t *testing.T, got map[string]any) {
			if got["nested"].(map[string]any)["Name"] != "private-default" || got["nested"].(map[string]any)["Count"] != float64(6) || got["pointer"].(map[string]any)["Count"] != float64(3) || len(got["mapping"].(map[string]any)) != 0 {
				t.Fatal("typed removals or atomic replacements were lost")
			}
		}},
		{name: "full replacement excludes root", root: "interfaces: {require: [missing.inert/v1]}\ncomposition: {adopt: PRIVATE_INERT_ADOPTION}\n" + config("{first: wrong}"), selected: adopt + config("{first: replacement}"), check: func(t *testing.T, got map[string]any) {
			if got["first"] != "replacement" {
				t.Fatal("replacement retained root")
			}
		}},
		{name: "adoption order", root: "composition: {adopt: [{module: " + dependency + ", export: second}, {module: " + dependency + ", export: first}]}\n"},
		{name: "sparse requirement removal", root: adopt + "interfaces: {require: {add: [unused.intent/v1], remove: []}}\n", overlay: "interfaces: {require: {remove: [unused.intent/v1]}}\n"},
		{name: "complete requirement suppression", root: adopt + "interfaces: {require: [probe.run/v1]}\n", edit: func(d *runtimebaseline.Document) {
			change(d, "[probe.run/v1, unused.intent/v1]", "first", "interfaces", "require")
		}},
		{name: "empty requirement set changes model", overlay: "interfaces: {require: []}\n", rule: "build-affecting"},
		{name: "sparse adoption reset", root: "composition: {adopt: [{module: example.com/absent, export: unused}]}\n", overlay: adopt},
		{name: "missing export", root: strings.Replace(adopt, "export: second", "export: absent", 1), rule: "has no export"},
		{name: "unknown module", root: strings.Replace(adopt, dependency, "example.com/absent", 1), rule: "outside the runtime baseline"},
		{name: "duplicate adoption", root: strings.Replace(adopt, "export: second", "export: first", 1), rule: "duplicates"},
		{name: "duplicate inventory", edit: func(d *runtimebaseline.Document) { d.Exports = append(d.Exports, d.Exports[dependencyIndex]) }, rule: "baseline"},
		{name: "unknown inventory module", edit: func(d *runtimebaseline.Document) { d.Exports[dependencyIndex].Module = "example.com/absent" }, rule: "baseline"},
		{name: "wrong dependency version", edit: func(d *runtimebaseline.Document) { d.Exports[dependencyIndex].Version = "v2.0.0" }, rule: "baseline"},
		{name: "recursive adoption", edit: func(d *runtimebaseline.Document) {
			change(d, "{adopt: []}", "first", "composition")
		}, rule: "baseline"},
		{name: "export process settings", edit: func(d *runtimebaseline.Document) {
			change(d, "{startup: 1s}", "first", "timeouts")
		}, rule: "baseline"},
		{name: "invalid peer hidden by removal", overlay: config("{nested: {$remove: true}}"), edit: func(d *runtimebaseline.Document) {
			d.Exports[dependencyIndex].YAML = strings.ReplaceAll(d.Exports[dependencyIndex].YAML, "count: 5", "count: 128")
		}, rule: "compiled Go type"},
		{name: "export tombstone", edit: func(d *runtimebaseline.Document) {
			d.Exports[dependencyIndex].YAML = strings.ReplaceAll(d.Exports[dependencyIndex].YAML, "private-first", "{$remove: true}")
		}, rule: "baseline"},
		{name: "peer private conflict", edit: func(d *runtimebaseline.Document) {
			change(d, "private-conflict", "second", "config", symbol, "first")
		}, rule: "adopted exports conflict"},
		{name: "current suppresses peer conflict", root: adopt + config("{first: chosen}"), edit: func(d *runtimebaseline.Document) {
			change(d, "private-conflict", "second", "config", symbol, "first")
		}, check: func(t *testing.T, got map[string]any) {
			if got["first"] != "chosen" {
				t.Fatal("current did not resolve conflict")
			}
		}},
		{name: "peer policy conflict", edit: func(d *runtimebaseline.Document) {
			d.Exports[dependencyIndex].YAML = strings.ReplaceAll(d.Exports[dependencyIndex].YAML, "1000ms", "2s")
		}, rule: "adopted exports conflict"},
		{name: "current suppresses policy conflict", overlay: "interfaces: {policies: {probe.run/v1: {timeout: 1s}}}\n", edit: func(d *runtimebaseline.Document) {
			d.Exports[dependencyIndex].YAML = strings.ReplaceAll(d.Exports[dependencyIndex].YAML, "1000ms", "2s")
		}},
		{name: "adopted build-visible drift", edit: func(d *runtimebaseline.Document) {
			d.Exports[dependencyIndex].YAML = strings.ReplaceAll(d.Exports[dependencyIndex].YAML, "public: 7", "public: 8")
		}, rule: "build-visible"},
		{name: "adopted policy drift", edit: func(d *runtimebaseline.Document) {
			d.Exports[dependencyIndex].YAML = strings.ReplaceAll(strings.ReplaceAll(d.Exports[dependencyIndex].YAML, "timeout: 1s", "timeout: 2s"), "1000ms", "2000ms")
		}, rule: "build-affecting"},
		{name: "adopted selection drift", edit: func(d *runtimebaseline.Document) {
			change(d, dependency+"/service.Other", "first", "interfaces", "use", "probe.run/v1")
			change(d, dependency+"/service.Other", "second", "interfaces", "use", "probe.run/v1")
		}, rule: "build-affecting"},
		{name: "required field removed", overlay: config("{first: {$remove: true}}"), rule: "required field is absent"},
		{name: "object removed", overlay: config("{$remove: true}"), rule: "required field is absent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document, err := runtimebaseline.Decode(baselineBytes)
			if err != nil {
				t.Fatal(err)
			}
			if tc.edit != nil {
				tc.edit(&document)
			}
			data, err := runtimebaseline.Encode(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(privatePath, data, 0600); err != nil {
				t.Fatal(err)
			}
			configurationRoot := t.TempDir()
			current := tc.root
			if current == "" {
				current = adopt
			}
			writeFile(t, filepath.Join(configurationRoot, "plystra.yaml"), current)
			args := []string{"--smoke", "--configuration-root", configurationRoot, "--runtime-baseline", privatePath}
			if tc.overlay != "" {
				writeFile(t, filepath.Join(configurationRoot, "plystra.test.yaml"), tc.overlay)
				args = append(args, "--env", "test")
			}
			if tc.selected != "" {
				writeFile(t, filepath.Join(configurationRoot, "selected.yaml"), tc.selected)
				args = append(args, "--config", "selected.yaml")
			}
			marker := filepath.Join(configurationRoot, "constructor.json")
			process := exec.CommandContext(t.Context(), binary, args...)
			process.Dir = deployment
			for _, entry := range goEnvironment(map[string]string{"PLYSTRA_ADOPTED_MARKER": marker, "PRIVATE_ADOPTED_SECRET": "private-resolved"}) {
				name, _, _ := strings.Cut(entry, "=")
				if strings.EqualFold(name, "PLYSTRA_ENV") || strings.EqualFold(name, "PLYSTRA_CONFIG") || tc.rule != "" && strings.EqualFold(name, "PRIVATE_ADOPTED_SECRET") {
					continue
				}
				process.Env = append(process.Env, entry)
			}
			output, err := process.CombinedOutput()
			if tc.rule != "" {
				if err == nil || !bytes.Contains(output, []byte(tc.rule)) {
					t.Fatalf("startup = %v; expected %q\n%s", err, tc.rule, output)
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("invalid configuration entered constructor")
				}
				for _, private := range []string{"private-", "PRIVATE_", configurationRoot, privatePath} {
					if bytes.Contains(output, []byte(private)) {
						t.Fatal("startup diagnostic exposed private inputs")
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("startup = %v\n%s", err, output)
			}
			var got map[string]any
			if err := json.Unmarshal(readAbsoluteFile(t, marker), &got); err != nil {
				t.Fatal(err)
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}
