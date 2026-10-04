package applicationgenerate_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/runtimebaseline"
	"go.yaml.in/yaml/v3"
)

func TestGeneratedBinaryComposesPrivateTemplateAncestry(t *testing.T) {
	const module = "example.com/template-consumer"
	const dependency = "example.com/template-oldest"
	const nearest = "example.com/template-nearest"
	const symbol = dependency + "/service.New"
	sources := filepath.Join(t.TempDir(), "sources")
	root, dependencyRoot := filepath.Join(sources, "application"), filepath.Join(sources, "oldest")
	nearestRoot := filepath.Join(sources, "nearest")
	writeConnectApplicationModule(t, root, module)
	writeApplicationModule(t, dependencyRoot, dependency)
	writeModule(t, nearestRoot, nearest, "require "+dependency+" v1.0.0\n")
	writeAssemblyInterface(t, dependencyRoot, "probe/run/v1", "runv1", "probe.run/v1", "Run", "type Request struct{}\ntype Response struct{}\n")
	writeFile(t, filepath.Join(dependencyRoot, "service/service.go"), strings.ReplaceAll(`package service
import (
 "context"
 "encoding/json"
 "os"
 "github.com/plystra/kernel/configuration"
 runv1 "example.com/template-oldest/interfaces/probe/run/v1"
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
 List []Nested @@yaml:"list"@@
 Array [2]Nested @@yaml:"array"@@
 Password configuration.Secret @@yaml:"password"@@
}
type service struct{}
//plystra:implements probe.run/v1
func New(c Config) (*service,error) {
 if marker:=os.Getenv("PLYSTRA_TEMPLATE_MARKER");marker!="" {
  data,err:=json.Marshal(map[string]any{"first":c.First,"second":c.Second,"public":c.Public,"nested":c.Nested,"pointer":c.Pointer,"mapping":c.Mapping,"list":c.List,"array":c.Array,"secret":string(c.Password.Bytes())})
  if err!=nil{return nil,err}
  if err:=os.WriteFile(marker,data,0600);err!=nil{return nil,err}
 }
 return &service{},nil
}
func (*service) Run(context.Context,runv1.Request)(runv1.Response,error){return runv1.Response{},nil}
`, "@@", "`"))
	const oldestConfiguration = `interfaces:
  require: [probe.run/v1]
  use: {probe.run/v1: example.com/template-oldest/service.New}
  policies: {probe.run/v1: {timeout: 1s}}
http:
  expose: {kernel.health/v1: {transport: connect}}
  cors: {allowed_origins: [https://template.example], allow_credentials: true}
  address: private-inert-address
timeouts: {startup: 1ns}
config:
  example.com/template-oldest/service.New:
    first: private-first
    public: 7
    nested: {name: private-inherited}
    pointer: {name: private-pointer, count: 9}
    mapping: {private-key: private-value}
    list: [{name: private-element, count: 9}]
    array: [{name: private-array, count: 9}, {name: private-array, count: 9}]
`
	const nearestConfiguration = `template: example.com/template-oldest
interfaces:
  require: [probe.run/v1]
  use: {probe.run/v1: example.com/template-oldest/service.New}
  policies: {probe.run/v1: {timeout: 1000ms}}
http:
  cors: {allow_credentials: {$remove: true}}
config:
  example.com/template-oldest/service.New:
    second: private-second
    nested: {count: 5}
    password: {env: PRIVATE_TEMPLATE_SECRET}
`
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), oldestConfiguration)
	writeFile(t, filepath.Join(nearestRoot, "plystra.yaml"), nearestConfiguration)
	writeFile(t, filepath.Join(dependencyRoot, "plystra.test.yaml"), "invalid dependency overlay: [")
	writeFile(t, filepath.Join(nearestRoot, "selected.yaml"), "invalid dependency replacement: [")
	goMod := string(readAbsoluteFile(t, filepath.Join(root, "go.mod"))) + "\nrequire " + nearest + " v1.0.0\nreplace " + dependency + " => " + filepath.ToSlash(dependencyRoot) + "\nreplace " + nearest + " => " + filepath.ToSlash(nearestRoot) + "\n"
	writeFile(t, filepath.Join(root, "go.mod"), goMod)
	const relationship = "template: " + nearest + "\n"
	writeFile(t, filepath.Join(root, "plystra.yaml"), relationship)
	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("generate = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	for _, file := range snapshotTree(t, filepath.Join(root, "generated")) {
		for _, private := range []string{"private-first", "private-second", "private-inherited", "private-pointer", "private-key", "private-value", "private-element", "private-array", "private-default", "PRIVATE_TEMPLATE_SECRET"} {
			if bytes.Contains(file.data, []byte(private)) {
				t.Fatalf("generated artifact %s exposed private template input", file.path)
			}
		}
	}
	baselineBytes := readAbsoluteFile(t, filepath.Join(root, "dist/runtime-baseline.json"))
	baseline, err := runtimebaseline.Decode(baselineBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Templates) != 2 || baseline.Templates[0].Module != dependency || baseline.Templates[1].Module != nearest || baseline.Templates[0].Template != "" || baseline.Templates[1].Template != dependency {
		t.Fatal("baseline did not capture oldest-to-nearest ancestry")
	}
	if strings.Contains(baseline.Templates[0].YAML, "private-inert-address") || strings.Contains(baseline.Templates[0].YAML, "startup:") {
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
	change := func(d *runtimebaseline.Document, layer int, value string, path ...string) {
		t.Helper()
		var document, replacement yaml.Node
		if err := yaml.Unmarshal([]byte(d.Templates[layer].YAML), &document); err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal([]byte(value), &replacement); err != nil {
			t.Fatal(err)
		}
		node := document.Content[0]
		for i, key := range path {
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
			if i == len(path)-1 {
				node.Content[index] = replacement.Content[0]
				break
			}
			node = node.Content[index]
		}
		data, err := yaml.Marshal(&document)
		if err != nil {
			t.Fatal(err)
		}
		d.Templates[layer].YAML = string(data)
	}
	type runtimeCase struct {
		name, root, overlay, selected, rule string
		edit                                func(*runtimebaseline.Document)
		check                               func(*testing.T, map[string]any)
	}
	cases := []runtimeCase{
		{name: "template-only deployment", check: func(t *testing.T, got map[string]any) {
			if got["first"] != "private-first" || got["second"] != "private-second" || got["public"] != float64(7) || got["secret"] != "private-resolved" || got["nested"].(map[string]any)["Count"] != float64(5) {
				t.Fatal("template inputs were not delivered")
			}
		}},
		{name: "private baseline refresh", edit: func(d *runtimebaseline.Document) {
			change(d, 0, "private-refreshed", "config", symbol, "first")
		}, check: func(t *testing.T, got map[string]any) {
			if got["first"] != "private-refreshed" {
				t.Fatal("private refresh did not take effect")
			}
		}},
		{name: "nearest replaces oldest", edit: func(d *runtimebaseline.Document) {
			change(d, 1, "private-nearest", "config", symbol, "first")
		}, check: func(t *testing.T, got map[string]any) {
			if got["first"] != "private-nearest" {
				t.Fatal("nearest template lost precedence")
			}
		}},
		{name: "current replaces templates", root: relationship + config("{first: local}"), check: func(t *testing.T, got map[string]any) {
			if got["first"] != "local" || got["second"] != "private-second" {
				t.Fatal("wrong current precedence")
			}
		}},
		{name: "oldest nearest current overlay order", root: relationship + config("{first: current, nested: {count: 6}}"), overlay: config("{first: overlay, nested: {count: 7}}"), edit: func(d *runtimebaseline.Document) {
			change(d, 1, "nearest", "config", symbol, "first")
		}, check: func(t *testing.T, got map[string]any) {
			if got["first"] != "overlay" || got["second"] != "private-second" || got["nested"].(map[string]any)["Count"] != float64(7) {
				t.Fatal("linear precedence was lost")
			}
		}},
		{name: "overlay tombstones and atomic replacement", root: relationship + config("{nested: {name: {$remove: true}}}"), overlay: config("{nested: {count: 6}, pointer: {}, mapping: {}}"), check: func(t *testing.T, got map[string]any) {
			if got["nested"].(map[string]any)["Name"] != "private-default" || got["nested"].(map[string]any)["Count"] != float64(6) || got["pointer"].(map[string]any)["Count"] != float64(3) || len(got["mapping"].(map[string]any)) != 0 {
				t.Fatal("typed removals or atomic replacements were lost")
			}
		}},
		{name: "template tombstone restores default", edit: func(d *runtimebaseline.Document) {
			change(d, 1, "{$remove: true}", "config", symbol, "nested", "name")
		}, check: func(t *testing.T, got map[string]any) {
			if got["nested"].(map[string]any)["Name"] != "private-default" {
				t.Fatal("template tombstone did not restore default")
			}
		}},
		{name: "atomic collection replacement across templates", edit: func(d *runtimebaseline.Document) {
			change(d, 1, "[{}]", "config", symbol, "list")
			change(d, 1, "[{}, {name: private-nearest}]", "config", symbol, "array")
		}, check: func(t *testing.T, got map[string]any) {
			list := got["list"].([]any)
			array := got["array"].([]any)
			if len(list) != 1 || list[0].(map[string]any)["Name"] != "private-default" || list[0].(map[string]any)["Count"] != float64(3) || len(array) != 2 || array[0].(map[string]any)["Name"] != "private-default" || array[1].(map[string]any)["Name"] != "private-nearest" || array[1].(map[string]any)["Count"] != float64(3) {
				t.Fatal("atomic collections inherited fields or lost compiled defaults")
			}
		}},
		{name: "full replacement excludes root application", root: relationship + "interfaces: {require: [missing.inert/v1]}\nhttp: {address: private-root-address}\ntimeouts: {startup: 1ns}\n" + config("{first: wrong, public: 99}"), selected: config("{first: replacement}"), check: func(t *testing.T, got map[string]any) {
			if got["first"] != "replacement" || got["second"] != "private-second" || got["public"] != float64(7) {
				t.Fatal("replacement retained root application declarations")
			}
		}},
		{name: "local values preserve effective template configuration", root: relationship + config("{second: private-second, nested: {count: 5}, password: {env: PRIVATE_TEMPLATE_SECRET}}")},
		{name: "sparse requirement removal", root: relationship + "interfaces: {require: {add: [unused.intent/v1], remove: []}}\n", overlay: "interfaces: {require: {remove: [unused.intent/v1]}}\n"},
		{name: "nearest complete requirement boundary", edit: func(d *runtimebaseline.Document) {
			change(d, 0, "[probe.run/v1, unused.intent/v1]", "interfaces", "require")
		}},
		{name: "current complete requirement boundary", root: relationship + "interfaces: {require: [probe.run/v1]}\n", edit: func(d *runtimebaseline.Document) {
			change(d, 1, "[probe.run/v1, unused.intent/v1]", "interfaces", "require")
		}},
		{name: "empty requirement set changes model", overlay: "interfaces: {require: []}\n", rule: "build-affecting"},
		{name: "missing root relationship", root: "{}\n", rule: "root template relationship changed"},
		{name: "changed live relationship", root: "template: " + dependency + "\n", rule: "root template relationship changed"},
		{name: "unknown live relationship", root: "template: example.com/absent\n", rule: "root template relationship changed"},
		{name: "live self cycle", root: "template: " + module + "\n", rule: "root template relationship changed"},
		{name: "replacement retains relationship validation", root: "template: example.com/absent\n", selected: "{}\n", rule: "root template relationship changed"},
		{name: "overlay cannot replace relationship", overlay: relationship, rule: "unknown key"},
		{name: "replacement cannot replace relationship", selected: relationship, rule: "unknown key"},
		{name: "missing template", edit: func(d *runtimebaseline.Document) { d.Templates = d.Templates[:1] }, rule: "baseline"},
		{name: "duplicate template", edit: func(d *runtimebaseline.Document) { d.Templates = append(d.Templates, d.Templates[0]) }, rule: "baseline"},
		{name: "unknown template module", edit: func(d *runtimebaseline.Document) { d.Templates[0].Module = "example.com/absent" }, rule: "baseline"},
		{name: "wrong template version", edit: func(d *runtimebaseline.Document) { d.Templates[0].Version = "v2.0.0" }, rule: "baseline"},
		{name: "wrong ancestor relationship", edit: func(d *runtimebaseline.Document) { d.Templates[1].Template = "example.com/absent" }, rule: "baseline"},
		{name: "cyclic ancestry", edit: func(d *runtimebaseline.Document) { d.Templates[0].Template = nearest }, rule: "baseline"},
		{name: "current project in ancestry", edit: func(d *runtimebaseline.Document) { d.Templates[0].Module = module }, rule: "baseline"},
		{name: "reordered ancestry", edit: func(d *runtimebaseline.Document) { d.Templates[0], d.Templates[1] = d.Templates[1], d.Templates[0] }, rule: "baseline"},
		{name: "template YAML cannot redeclare relationship", edit: func(d *runtimebaseline.Document) {
			change(d, 0, nearest, "template")
		}, rule: "unknown key"},
		{name: "template process timeout rejected", edit: func(d *runtimebaseline.Document) {
			change(d, 0, "{startup: 1s}", "timeouts")
		}, rule: "process settings"},
		{name: "template listener rejected", edit: func(d *runtimebaseline.Document) {
			change(d, 0, "private-inert-address", "http", "address")
		}, rule: "unknown key"},
		{name: "invalid ancestor hidden by removal", overlay: config("{nested: {$remove: true}}"), edit: func(d *runtimebaseline.Document) {
			change(d, 0, "128", "config", symbol, "nested", "count")
		}, rule: "compiled Go type"},
		{name: "nearest policy replaces oldest", edit: func(d *runtimebaseline.Document) {
			change(d, 0, "{timeout: 2s}", "interfaces", "policies", "probe.run/v1")
		}},
		{name: "current restores effective policy", overlay: "interfaces: {policies: {probe.run/v1: {timeout: 1s}}}\n", edit: func(d *runtimebaseline.Document) {
			change(d, 1, "{timeout: 2s}", "interfaces", "policies", "probe.run/v1")
		}},
		{name: "template build-visible drift", edit: func(d *runtimebaseline.Document) {
			change(d, 0, "8", "config", symbol, "public")
		}, rule: "build-visible"},
		{name: "current restores effective build-visible value", overlay: config("{public: 7}"), edit: func(d *runtimebaseline.Document) {
			change(d, 0, "8", "config", symbol, "public")
		}},
		{name: "template policy drift", edit: func(d *runtimebaseline.Document) {
			change(d, 1, "{timeout: 2s}", "interfaces", "policies", "probe.run/v1")
		}, rule: "build-affecting"},
		{name: "template selection drift", edit: func(d *runtimebaseline.Document) {
			change(d, 1, dependency+"/service.Other", "interfaces", "use", "probe.run/v1")
		}, rule: "build-affecting"},
		{name: "template exposure drift", overlay: "http: {expose: {kernel.health/v1: {$remove: true}}}\n", rule: "build-affecting"},
		{name: "CORS whole removal changes model", overlay: "http: {cors: {$remove: true}}\n", rule: "build-affecting"},
		{name: "CORS whole tombstone then restoration", root: relationship + "http: {cors: {$remove: true}}\n", overlay: "http: {cors: {allowed_origins: [https://template.example]}}\n"},
		{name: "CORS origins tombstone then restoration", root: relationship + "http: {cors: {allowed_origins: {$remove: true}}}\n", overlay: "http: {cors: {allowed_origins: [https://template.example]}}\n"},
		{name: "CORS credentials tombstone restores false", root: relationship + "http: {cors: {allow_credentials: true}}\n", overlay: "http: {cors: {allow_credentials: {$remove: true}}}\n"},
		{name: "replacement excludes root CORS null", root: relationship + "http: {cors: null}\n", selected: "{}\n"},
		{name: "required field removed", overlay: config("{first: {$remove: true}}"), rule: "required field is absent"},
		{name: "object removed", overlay: config("{$remove: true}"), rule: "required field is absent"},
		{name: "array length invalid", overlay: config("{array: [{}]}"), rule: "compiled Go type"},
		{name: "dynamic mapping keeps ordinary removal-named key", overlay: config("{mapping: {$remove: private-value, ordinary: private-value}}"), check: func(t *testing.T, got map[string]any) {
			mapping := got["mapping"].(map[string]any)
			if len(mapping) != 2 || mapping["$remove"] != "private-value" || mapping["ordinary"] != "private-value" {
				t.Fatal("dynamic map was confused with the exact removal marker")
			}
		}},
	}
	for _, value := range []string{"null", "{allowed_origins: null}", "{allow_credentials: null}"} {
		for _, mode := range []string{"template", "root", "environment", "replacement"} {
			tc := runtimeCase{name: "CORS null rejected/" + mode + "/" + value, rule: "cors"}
			switch mode {
			case "template":
				tc.edit = func(d *runtimebaseline.Document) { change(d, 1, value, "http", "cors") }
			case "root":
				tc.root = relationship + "http: {cors: " + value + "}\n"
			case "environment":
				tc.overlay = "http: {cors: " + value + "}\n"
			case "replacement":
				tc.selected = "http: {cors: " + value + "}\n"
			}
			cases = append(cases, tc)
		}
	}
	for _, modulePath := range []string{
		"../private-platform", "/private-platform", "local/private-platform",
		"example.com//private-platform", "example.com/../private-platform",
		"example.com/private-platform@v1.0.0", "example.com/private-platform/v1",
		"example.com/private-platform/v02", "example.com/private-platform/",
		"NUL", "example.com/private-platform?query", "https://example.com/private-platform",
	} {
		for _, mode := range []string{"default", "environment", "replacement"} {
			tc := runtimeCase{name: "invalid relationship/" + mode + "/" + modulePath, root: "template: " + strconv.Quote(modulePath) + "\n", rule: "template"}
			if mode == "environment" {
				tc.overlay = "{}\n"
			}
			if mode == "replacement" {
				tc.selected = "{}\n"
			}
			cases = append(cases, tc)
		}
	}
	for _, value := range []string{"null", "[]", "{}", "1", "true", "[example.com/template-nearest]", "{module: example.com/template-nearest}"} {
		cases = append(cases, runtimeCase{name: "non-scalar relationship/" + value, root: "template: " + value + "\n", rule: "template"})
	}
	for _, modulePath := range []string{"my-app", "example.com/absent", "example.com/platform/v2", "gopkg.in/yaml.v3", "example.com/" + strings.Repeat("segment/", 160) + "module"} {
		cases = append(cases, runtimeCase{name: "valid but unlinked relationship/" + modulePath, root: "template: " + strconv.Quote(modulePath) + "\n", rule: "root template relationship changed"})
	}
	// Invalid Resource declarations fail before Secrets; empty collections add no
	// instances. A replacement still excludes root application declarations.
	for _, resource := range []struct {
		yaml, rule string
	}{
		{`null`, "invalid Resource declaration mapping"},
		{`{private-key: private-value}`, "invalid Resource declaration mapping"},
		{`{instances: null}`, "invalid Resource declaration mapping"},
		{`{instances: []}`, "invalid Resource declaration mapping"},
		{`{instances: {database.1: {}}}`, "invalid Resource instance name"},
		{`{instances: {database--primary: {}}}`, "invalid Resource instance name"},
		{`{instances: {` + strings.Repeat("a", 129) + `: {}}}`, "invalid Resource instance name"},
		{`{instances: {database: null}}`, "invalid Resource declaration mapping"},
		{`{instances: {database: {private-key: private-value}}}`, "invalid Resource declaration mapping"},
		{`{instances: {database: {use: example.com/db.new}}}`, "invalid Resource provider identity"},
		{`{instances: {database: {use: null}}}`, "invalid Resource provider identity"},
		{`{instances: {database: {config: null}}}`, "Resource config must be a mapping or removal"},
		{`{instances: {database: {config: []}}}`, "Resource config must be a mapping or removal"},
		{`{bind: null}`, "invalid Resource declaration mapping"},
		{`{bind: {private-key: private-value}}`, "invalid Resource declaration mapping"},
		{`{bind: {instances: null}}`, "invalid Resource declaration mapping"},
		{`{bind: {implementations: {private-key: {database: primary}}}}`, "invalid Resource binding consumer"},
		{`{bind: {instances: {Bad: {database: primary}}}}`, "invalid Resource binding consumer"},
		{`{bind: {instances: {primary: []}}}`, "invalid Resource declaration mapping"},
		{`{bind: {instances: {primary: {_ : secondary}}}}`, "invalid Resource dependency parameter"},
		{`{bind: {instances: {primary: {database: null}}}}`, "invalid Resource binding target"},
		{`{bind: {instances: {primary: {database: Database}}}}`, "invalid Resource binding target"},
		{`{bind: {instances: {primary: {database: {instance: secondary}}}}}`, "invalid Resource binding target"},
		{`{instances: {database: {}}}`, "Resource instance has no visible provider"},
		{`{instances: {` + strings.Repeat("a", 128) + `: {}}}`, "Resource instance has no visible provider"},
		{`{instances: {database-1.primary: {use: example.com/db.New, config: {private: null, options: [], labels: {$remove: true, ordinary: 1}}}}}`, "Resource provider has no Config schema"},
		{`{bind: {implementations: {example.com/service.New: {Database: database-1.primary}}, instances: {cache: {"\u03b4": database}}}}`, "Resource binding consumer is not visible or selected"},
		{`{}`, ""},
		{`{instances: {}}`, ""},
		{`{bind: {implementations: {}, instances: {}}}`, ""},
	} {
		cases = append(cases,
			runtimeCase{name: "inherited resource/" + resource.yaml, rule: resource.rule, edit: func(d *runtimebaseline.Document) {
				change(d, 1, resource.yaml, "resources")
			}},
			runtimeCase{name: "current resource/" + resource.yaml, root: relationship + "resources: " + resource.yaml + "\n", rule: resource.rule},
			runtimeCase{name: "excluded root resource/" + resource.yaml, root: relationship + "resources: " + resource.yaml + "\n", selected: "{}\n"},
		)
	}
	for _, resource := range []string{
		`{instances: {database: {config: {private: {1: private-value}}}}}`,
		`{instances: {database: {config: {private: {private-key: 1, private-key: 2}}}}}`,
	} {
		cases = append(cases,
			runtimeCase{name: "invalid raw inherited resource/" + resource, rule: "baseline", edit: func(d *runtimebaseline.Document) { change(d, 1, resource, "resources") }},
			runtimeCase{name: "invalid raw current resource/" + resource, root: relationship + "resources: " + resource + "\n", rule: "unique string keys"},
			runtimeCase{name: "invalid raw excluded resource/" + resource, root: relationship + "resources: " + resource + "\n", selected: "{}\n", rule: "unique string keys"},
		)
	}
	for _, value := range []struct {
		name, yaml string
		invalid    bool
		bounds     bool
	}{
		{name: "integer", yaml: "!!int PRIVATE_VALUE", invalid: true},
		{name: "boolean", yaml: "!!bool PRIVATE_VALUE", invalid: true},
		{name: "float", yaml: "!!float PRIVATE_VALUE", invalid: true},
		{name: "timestamp", yaml: "!!timestamp PRIVATE_VALUE", invalid: true},
		{name: "binary", yaml: "!!binary PRIVATE_VALUE", invalid: true},
		{name: "null", yaml: "!!null PRIVATE_VALUE", invalid: true},
		{name: "valid scalars", yaml: "[!!int 7, !!bool true, !!float 1.5, !!timestamp 2026-10-04, !!binary " + base64.StdEncoding.EncodeToString([]byte("private-binary")) + ", !!str PRIVATE_VALUE, !!null null, {PRIVATE_KEY: false}]"},
		{name: "wide", yaml: "[" + strings.Repeat("0,", 65_000) + "0]"},
		{name: "deep", yaml: strings.Repeat("[", 50) + "0" + strings.Repeat("]", 50)},
		{name: "too wide", yaml: "[" + strings.Repeat("0,", 65_536) + "0]", bounds: true},
		{name: "too deep", yaml: strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65), bounds: true},
	} {
		for _, shape := range []string{"%s", "[%s]", "{PRIVATE_KEY: %s}"} {
			payload := fmt.Sprintf(shape, value.yaml)
			for _, mode := range []string{"template", "root", "environment", "replacement"} {
				tc := runtimeCase{name: "untyped constructor scalar/" + mode + "/" + value.name + "/" + shape, rule: "constructor inventory"}
				if value.invalid {
					tc.rule = "invalid"
				}
				if value.bounds {
					tc.rule = "traversal bounds"
					if mode == "template" {
						tc.rule = "baseline"
					}
				}
				fragment := "config: {example.com/unavailable/service.New: {value: " + payload + "}}\n"
				switch mode {
				case "template":
					tc.edit = func(d *runtimebaseline.Document) {
						change(d, 1, "{value: "+payload+"}", "config", "example.com/unavailable/service.New")
					}
				case "root":
					tc.root = relationship + fragment
				case "environment":
					tc.overlay = fragment
				case "replacement":
					tc.selected = fragment
				}
				cases = append(cases, tc)
			}
			excluded := runtimeCase{name: "replacement root scalar/" + value.name + "/" + shape, root: relationship + "config: {example.com/unavailable/service.New: {value: " + payload + "}}\n", selected: "{}\n"}
			if value.invalid {
				excluded.rule = "invalid"
			}
			if value.bounds {
				excluded.rule = "traversal bounds"
			}
			cases = append(cases, excluded)
		}
	}
	for _, value := range []string{
		"{$remove: true, private-key: private-value}", "{$remove: false, private-key: private-value}",
		"{$remove: null, private-key: private-value}", "{$remove: \"private-value\", private-key: private-value}",
		"{$remove: {private-key: private-value}, private-key: private-value}", "{private-key: private-value, $remove: null}",
	} {
		for _, mode := range []string{"template", "root", "environment", "replacement"} {
			tc := runtimeCase{name: "invalid constructor removal/" + mode + "/" + value, rule: "unknown field"}
			switch mode {
			case "template":
				tc.edit = func(d *runtimebaseline.Document) { change(d, 1, value, "config", symbol) }
			case "root":
				tc.root = relationship + config(value)
			case "environment":
				tc.overlay = config(value)
			case "replacement":
				tc.selected = config(value)
			}
			cases = append(cases, tc)
		}
	}
	for _, value := range []string{"{$remove: false}", "{$remove: null}", "{$remove: private-value}"} {
		cases = append(cases, runtimeCase{name: "malformed exact removal/" + value, overlay: config(value), rule: "removal"})
	}
	for _, field := range []string{"pointer", "mapping", "list"} {
		for _, value := range []string{"!!null PRIVATE_VALUE", "null"} {
			for _, mode := range []string{"template", "root", "environment", "replacement"} {
				tc := runtimeCase{name: "nullable scalar/" + mode + "/" + field + "/" + value}
				if value != "null" {
					tc.rule = "invalid"
				} else {
					tc.check = func(t *testing.T, got map[string]any) {
						if value, exists := got[field]; !exists || value != nil {
							t.Fatal("genuine null was not delivered")
						}
					}
				}
				current := config("{" + field + ": " + value + "}")
				switch mode {
				case "template":
					tc.edit = func(d *runtimebaseline.Document) { change(d, 1, value, "config", symbol, field) }
				case "root":
					tc.root = relationship + current
				case "environment":
					tc.overlay = current
				case "replacement":
					tc.selected = current
				}
				cases = append(cases, tc)
			}
		}
	}
	for _, tc := range cases {
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
				current = relationship
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
			before := snapshotTree(t, configurationRoot)
			process := exec.CommandContext(t.Context(), binary, args...)
			process.Dir = deployment
			for _, entry := range goEnvironment(map[string]string{"PLYSTRA_TEMPLATE_MARKER": marker, "PRIVATE_TEMPLATE_SECRET": "private-resolved", "GOMODCACHE": filepath.Join(deployment, "absent-module-cache"), "GOWORK": "off"}) {
				name, _, _ := strings.Cut(entry, "=")
				if strings.EqualFold(name, "PLYSTRA_ENV") || strings.EqualFold(name, "PLYSTRA_CONFIG") || tc.rule != "" && strings.EqualFold(name, "PRIVATE_TEMPLATE_SECRET") {
					continue
				}
				process.Env = append(process.Env, entry)
			}
			output, err := process.CombinedOutput()
			if !bytes.Equal(data, readAbsoluteFile(t, privatePath)) {
				t.Fatal("startup changed private baseline inputs")
			}
			if tc.rule != "" && !reflect.DeepEqual(before, snapshotTree(t, configurationRoot)) {
				t.Fatal("rejected startup changed configuration inputs")
			}
			if tc.rule != "" {
				if bytes.Contains(output, []byte("resolve constructor Secret")) {
					t.Fatal("invalid configuration reached Secret resolution")
				}
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
