package bootstrapgen

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorconfig"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/runtimebaseline"
)

// Compile the emitted runtime helpers, then remove their module before executing
// them. Full application construction is covered by applicationgenerate tests.
func TestGeneratedTemplateRuntimeWithoutSourceTree(t *testing.T) {
	const config = "type Config struct {\n" +
		"Value string `yaml:\"value\" plystra:\"required\"`\n" +
		"Count int32 `yaml:\"count\" plystra:\"build-visible\" plystra-default:\"7\"`\n" +
		"Nested struct { Left string `yaml:\"left\"`; Right string `yaml:\"right\"` } `yaml:\"nested\"`\n" +
		"Labels map[string]string `yaml:\"labels\"`\n" +
		"}\n"
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, "config.go", "package probe\n"+config+"func New(Config) {}", 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := new(types.Config).Check("example.com/probe", files, []*ast.File{parsed}, nil)
	if err != nil {
		t.Fatal(err)
	}
	schema, present, err := implementationinventory.CompileConfiguration(pkg, pkg.Scope().Lookup("New").(*types.Func))
	if err != nil || !present {
		t.Fatal("compile Config", err)
	}
	input := ConstructorConfigurationInput{Symbol: "example.com/probe.New", Schema: schema, YAML: []byte("value: frozen\n")}
	constructors, err := renderConstructorConfiguration([]ConstructorConfigurationInput{input}, []string{input.Symbol})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := applicationmeta.Parse([]byte("interfaces: {require: [records.read/v1], use: {records.read/v1: example.com/probe.New}}\n"))
	if err != nil {
		t.Fatal(err)
	}
	compatibility, err := NewExecutableApplicationModelCompatibility("sha256:"+strings.Repeat("a", 64), manifest, []string{"records.read/v1"})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := RuntimeBaseline(Options{
		ModulePath: "example.com/app", Template: "example.com/near", ApplicationModelCompatibility: compatibility,
		ConstructorConfigurations: []ConstructorConfigurationInput{input},
		Templates: []runtimebaseline.Template{
			{Module: "example.com/z-old", Version: "v1.0.0", YAML: "interfaces: {require: [records.read/v1], use: {records.read/v1: example.com/probe.New}}\nconfig: {example.com/probe.New: {value: old, nested: {left: inherited}, labels: {old: discarded}}}\n"},
			{Module: "example.com/near", Version: "v1.1.0", Template: "example.com/z-old", YAML: "config: {example.com/probe.New: {value: near, nested: {right: nearest}, labels: {near: retained}}}\n"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	dormant := baselineConstructor{Symbol: "example.com/dormant.New", Interfaces: []string{"records.dormant/v1"}, Schema: &constructorconfig.Schema{Kind: "object", Fields: []constructorconfig.Field{
		{Name: "value", GoName: "Value", Required: true, Value: constructorconfig.Schema{Kind: "string"}},
		{Name: "token", GoName: "Token", Value: constructorconfig.Schema{Kind: "secret"}},
	}}}
	var contract map[string]json.RawMessage
	if err := json.Unmarshal(baseline.Contract, &contract); err != nil {
		t.Fatal(err)
	}
	contract["constructor_inventory"], err = json.Marshal([]baselineConstructor{dormant})
	if err != nil {
		t.Fatal(err)
	}
	baseline.Contract, err = json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	baseline.ContractID = runtimebaseline.ContractID(baseline.Contract)
	baseline.Defaults[dormant.Symbol] = json.RawMessage(`{}`)
	encoded, err := runtimebaseline.Encode(baseline)
	if err != nil {
		t.Fatal(err)
	}
	runtimeSource, err := renderRuntimeConfigurationSupport(nil, []string{"records.read/v1"}, []string{input.Symbol})
	if err != nil {
		t.Fatal(err)
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	write("go.mod", strings.Replace(string(module), "module github.com/plystra/cli", "module github.com/plystra/cli/runtimecheck", 1)+"\nrequire github.com/plystra/cli v0.0.0\nreplace github.com/plystra/cli => "+strconv.Quote(filepath.ToSlash(root))+"\n")
	sums, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	write("go.sum", string(sums))
	write("assembly/config.go", "package assembly\n"+config+"type ConstructorConfiguration struct { Config0 Config }\n")
	write("runtime.go", runtimeTestHeader+
		"const compiledRuntimeContract = "+strconv.Quote(baseline.ContractID)+"\n"+
		"const compiledApplicationModelDigest = "+strconv.Quote(compatibility.ApplicationModelDigest())+"\n"+
		"const compiledApplicationModelCompatibilityDigest = "+strconv.Quote(compatibility.Digest())+"\n"+
		"const initialBaseline = "+strconv.Quote(string(encoded))+"\n"+
		runtimeSource+runtimeBaselineSupport+constructors)
	write("runtime_test.go", runtimeTemplateTests)
	binary := filepath.Join(t.TempDir(), "runtime.test.exe")
	build := exec.CommandContext(t.Context(), "go", "test", "-c", "-race", "-buildvcs=false", "-mod=readonly", "-o", binary, ".")
	build.Dir = source
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile emitted runtime: %v\n%s", err, output)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	run := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=90s")
	run.Dir = t.TempDir()
	run.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOTOOLCHAIN=local", "GOMODCACHE="+t.TempDir())
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("source-independent emitted runtime: %v\n%s", err, output)
	}
}

const runtimeTestHeader = `package runtimecheck
import (
 "bytes"
 "context"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "errors"
 "fmt"
 "go/token"
 "io"
 "net"
 "net/url"
 "os"
 "path/filepath"
 "reflect"
 "sort"
 "strconv"
 "strings"
 "time"
 "unicode"
 applicationassembly "github.com/plystra/cli/runtimecheck/assembly"
 "github.com/plystra/cli/internal/constructorconfig"
 "github.com/plystra/cli/internal/modulepath"
 "github.com/plystra/cli/internal/runtimebaseline"
 kernelconfiguration "github.com/plystra/kernel/configuration"
 kernelplugin "github.com/plystra/kernel/plugin"
 "go.yaml.in/yaml/v3"
)
const defaultRuntimeDocument = "plystra.yaml"
var ErrRuntimeConfiguration = errors.New("invalid selected runtime configuration")
var ErrRuntimeCompatibility = errors.New("runtime configuration is incompatible with compiled application model")
var ErrRuntimeSelector = errors.New("invalid runtime configuration selector")
type RuntimeOptions struct { Arguments, Environment []string }
`

const runtimeTemplateTests = `package runtimecheck
import (
 "bytes"
 "context"
 "encoding/json"
 "errors"
 "os"
 "path/filepath"
 "reflect"
 "strings"
 "testing"
 "github.com/plystra/cli/internal/constructorconfig"
 "github.com/plystra/cli/internal/privatefile"
 "github.com/plystra/cli/internal/runtimebaseline"
 "go.yaml.in/yaml/v3"
)
const rootRelationship = "template: example.com/near\n"

func fixture(t *testing.T) runtimebaseline.Document {
 t.Helper()
 d, err := runtimebaseline.Decode([]byte(initialBaseline))
 if err != nil { t.Fatal(err) }
 return d
}
func compose(t *testing.T, d runtimebaseline.Document, root, selected, overlay string) []byte {
 t.Helper()
 var replacement, environment []byte
 if selected != "" { replacement = []byte(selected) }
 if overlay != "" { environment = []byte(overlay) }
 result, err := composeRuntimeTemplateDocument(d, []byte(root), replacement, environment)
 if err != nil { t.Fatal(err) }
 return result
}
func config(t *testing.T, document []byte) *runtimePreparedConfiguration {
 t.Helper()
 prepared, err := prepareRuntimeConstructorConfiguration(document)
 if err != nil { t.Fatal(err) }
 // This active Config contains no Secrets; binding exercises typed delivery.
 if err := prepared.resolve(context.Background(), nil); err != nil { t.Fatal(err) }
 return prepared
}

func TestOrderedTypedLayersAndSelectors(t *testing.T) {
 for _, mode := range []string{"default", "environment", "replacement"} {
  t.Run(mode, func(t *testing.T) {
   d := fixture(t)
   root, replacement, overlay := rootRelationship, "", ""
   expected := "near"
   if mode == "environment" { overlay = "config: {example.com/probe.New: {value: overlay, nested: {left: current}}}\n"; expected = "overlay" }
   if mode == "replacement" { root += "config: {example.com/probe.New: {count: 99, value: EXCLUDED_PRIVATE_VALUE}}\ninterfaces: {require: []}\nhttp: {address: excluded}\n"; replacement = "{}\n" }
   document := compose(t, d, root, replacement, overlay)
   if err := validateRuntimeApplicationModel(document); err != nil { t.Fatal(err) }
   got := config(t, document).configuration.Config0
   if got.Value != expected || got.Count != 7 || got.Nested.Right != "nearest" || !reflect.DeepEqual(got.Labels, map[string]string{"near":"retained"}) { t.Fatal("incorrect ordered typed composition") }
   if mode != "environment" && got.Nested.Left != "inherited" { t.Fatal("fixed object did not inherit") }
   if mode == "environment" && got.Nested.Left != "current" { t.Fatal("overlay did not win") }
   if bytes.Contains(document, []byte("EXCLUDED_PRIVATE_VALUE")) || bytes.Contains(document, []byte("excluded")) { t.Fatal("replacement activated root values") }
  })
 }
}

func TestCompleteSetsSparseRemovalsAndExposure(t *testing.T) {
 d := fixture(t)
 d.Templates[0].YAML += "http: {expose: {records.read/v1: {transport: connect}}, cors: {allow_credentials: true}}\n"
 d.Templates[1].YAML += "interfaces: {require: {remove: [records.read/v1], add: [records.extra/v1]}, policies: {records.read/v1: {timeout: 5s}}}\nhttp: {cors: {allowed_origins: [https://app.example]}}\n"
 root := rootRelationship + "interfaces: {require: [records.read/v1], policies: {records.read/v1: {$remove: true}}}\nhttp: {address: current-process, expose: {records.read/v1: {$remove: true}}}\ntimeouts: {startup: 9s}\n"
 document := compose(t, d, root, "", "")
 if bytes.Contains(document, []byte("records.extra/v1")) || bytes.Contains(document, []byte("timeout: 5s")) || bytes.Contains(document, []byte("transport: connect")) { t.Fatal("lower declarations survived removals") }
 if !bytes.Contains(document, []byte("current-process")) || !bytes.Contains(document, []byte("startup: 9s")) || !bytes.Contains(document, []byte("https://app.example")) { t.Fatal("lost effective HTTP/process fields") }
}

func TestEquivalentEffectiveDeclarationsRemainCompatible(t *testing.T) {
 d := fixture(t)
 first := compose(t, d, rootRelationship, "", "")
 d.Templates[0].YAML = "config: {example.com/probe.New: {value: old}}\n"
 second := compose(t, d, rootRelationship+"interfaces: {require: [records.read/v1], use: {records.read/v1: example.com/probe.New}}\n", "", "")
 for _, document := range [][]byte{first, second} { if err := validateRuntimeApplicationModel(document); err != nil { t.Fatal(err) } }
}

func TestDormantTypedValidationWithoutResolution(t *testing.T) {
 d := fixture(t)
 d.Templates[1].YAML += "interfaces: {use: {records.dormant/v1: example.com/dormant.New}}\n"
 valid := rootRelationship+"config: {example.com/dormant.New: {value: PRIVATE_SENTINEL, token: {env: NEVER_RESOLVE_DORMANT}}}\n"
 document := compose(t, d, valid, "", "")
 if bytes.Contains(document, []byte("PRIVATE_SENTINEL")) || bytes.Contains(document, []byte("NEVER_RESOLVE_DORMANT")) { t.Fatal("dormant configuration entered runtime delivery") }
 if err := validateRuntimeApplicationModel(document); err != nil { t.Fatal(err) }
 for _, node := range []string{"{token: {env: NEVER_RESOLVE_DORMANT}}", "{value: PRIVATE_SENTINEL, token: {env: INVALID, file: INVALID}}", "{value: !!int PRIVATE_SENTINEL}"} {
  _, err := composeRuntimeTemplateDocument(d, []byte(rootRelationship+"config: {example.com/dormant.New: "+node+"}\n"), nil, nil)
  if err == nil || strings.Contains(err.Error(), "PRIVATE_SENTINEL") || strings.Contains(err.Error(), "NEVER_RESOLVE_DORMANT") { t.Fatal("accepted invalid dormant object or leaked input", err) }
 }
 removed := compose(t, d, valid, "", "interfaces: {use: {records.dormant/v1: {$remove: true}}}\nconfig: {example.com/dormant.New: {$remove: true}}\n")
 if err := validateRuntimeApplicationModel(removed); err != nil { t.Fatal(err) }
}

func TestAncestryIdentityAndLayerRejections(t *testing.T) {
 for name, mutate := range map[string]func(*runtimebaseline.Document){
  "missing": func(d *runtimebaseline.Document) { d.Templates = d.Templates[:1] },
  "duplicate": func(d *runtimebaseline.Document) { d.Templates = append(d.Templates, d.Templates[1]) },
  "cycle": func(d *runtimebaseline.Document) { d.Templates[0].Template = d.Templates[1].Module },
  "current": func(d *runtimebaseline.Document) { d.Templates[0].Module = "example.com/app" },
  "version": func(d *runtimebaseline.Document) { d.Templates[0].Version = "v9.0.0" },
  "gap": func(d *runtimebaseline.Document) { d.Templates[1].Template = "example.com/missing" },
  "process address": func(d *runtimebaseline.Document) { d.Templates[0].YAML += "http: {address: PRIVATE_SENTINEL}\n" },
  "process startup": func(d *runtimebaseline.Document) { d.Templates[0].YAML += "timeouts: {startup: 1s}\n" },
  "nested template": func(d *runtimebaseline.Document) { d.Templates[0].YAML += "template: example.com/other\n" },
  "resources": func(d *runtimebaseline.Document) { d.Templates[0].YAML += "resources: {}\n" },
  "data": func(d *runtimebaseline.Document) { d.Templates[0].YAML += "data: {}\n" },
  "malformed scalar suppressed": func(d *runtimebaseline.Document) { d.Templates[0].YAML = "config: {example.com/probe.New: {value: !!binary PRIVATE_SENTINEL}}\n" },
 } {
  t.Run(name, func(t *testing.T) {
   d := fixture(t); mutate(&d)
   _, err := composeRuntimeTemplateDocument(d, []byte(rootRelationship+"config: {example.com/probe.New: {$remove: true}}\n"), nil, nil)
   if err == nil || strings.Contains(err.Error(), "PRIVATE_SENTINEL") { t.Fatal("accepted invalid layer or exposed input", err) }
  })
 }
 for _, root := range []string{"{}\n", "template: example.com/other\n", "template: [example.com/near]\n", "template: ../PRIVATE_SENTINEL\n"} {
  if _, err := composeRuntimeTemplateDocument(fixture(t), []byte(root), nil, nil); err == nil || strings.Contains(err.Error(), "PRIVATE_SENTINEL") { t.Fatal("accepted root drift or leaked value", err) }
 }
 for _, selected := range []string{"template: example.com/near\n", "resources: {}\n", "data: {}\n", "composition: {}\n"} {
  for _, overlay := range []bool{false, true} {
   var replacement, environment []byte
   if overlay { environment = []byte(selected) } else { replacement = []byte(selected) }
   if _, err := composeRuntimeTemplateDocument(fixture(t), []byte(rootRelationship), replacement, environment); err == nil { t.Fatal("accepted forbidden selected metadata") }
  }
 }
}

func TestRuntimeBuildVisibleDriftAndDefaults(t *testing.T) {
 d := fixture(t)
 document := compose(t, d, rootRelationship+"config: {example.com/probe.New: {count: 8}}\n", "", "")
 if _, err := prepareRuntimeConstructorConfiguration(document); err == nil { t.Fatal("accepted changed build-visible value") }
 d.Defaults["example.com/probe.New"] = json.RawMessage("{\"/count\":8}")
 if err := validateRuntimeBaseline(d); err == nil { t.Fatal("accepted stale compiled defaults") }
 document = compose(t, fixture(t), rootRelationship+"config: {example.com/probe.New: {value: {$remove: true}}}\n", "", "")
 if _, err := prepareRuntimeConstructorConfiguration(document); err == nil { t.Fatal("accepted removed required field") }
}

func TestReplacementRootValidatesRawYAMLBeforeExclusion(t *testing.T) {
 for name, excluded := range map[string]string{
  "integer": "config: {private: {value: !!int PRIVATE_SENTINEL}}\n",
  "null": "config: {private: {value: !!null PRIVATE_SENTINEL}}\n",
  "binary": "config: {private: {value: !!binary PRIVATE_SENTINEL}}\n",
  "nested duplicate": "config: {private: {PRIVATE_KEY: one, PRIVATE_KEY: two}}\n",
  "sequence duplicate": "interfaces: [{PRIVATE_KEY: one, PRIVATE_KEY: two}]\n",
  "non-string key": "config: {private: {true: PRIVATE_SENTINEL}}\n",
  "anchor": "config: {private: &private PRIVATE_SENTINEL}\n",
  "depth": "config: "+strings.Repeat("[",64)+"PRIVATE_SENTINEL"+strings.Repeat("]",64)+"\n",
  "nodes": "config: ["+strings.Repeat("0,",65535)+"]\n",
 } {
  t.Run(name, func(t *testing.T) {
   root := []byte(rootRelationship+excluded)
   before := bytes.Clone(root)
   _, err := composeRuntimeTemplateDocument(fixture(t), root, []byte("{}\n"), nil)
   if !errors.Is(err,ErrRuntimeConfiguration) { t.Fatal("excluded malformed YAML accepted",err) }
   if strings.Contains(err.Error(),"PRIVATE_SENTINEL") || strings.Contains(err.Error(),"PRIVATE_KEY") { t.Fatal("raw YAML diagnostic exposed private input") }
   if !bytes.Equal(root,before) { t.Fatal("raw validation changed authored root") }
  })
 }
}

func TestReplacementRootExcludesValidApplicationTypeErrors(t *testing.T) {
 for _, excluded := range []string{
  "interfaces: PRIVATE_SENTINEL\n",
  "config: {private: [invalid, schema]}\nhttp: {address: false}\ntimeouts: [invalid]\nresources: {private: value}\ndata: {private: value}\n",
  "capabilities: PRIVATE_SENTINEL\n",
  "config: {private: {nilvalue: !!null null, integer: !!int 7, binary: !!binary cHJpdmF0ZQ==}}\n",
  "config: "+strings.Repeat("[",63)+"PRIVATE_SENTINEL"+strings.Repeat("]",63)+"\n",
 } {
  document := compose(t,fixture(t),rootRelationship+excluded,"{}\n","")
  if err := validateRuntimeApplicationModel(document); err != nil { t.Fatal(err) }
  if config(t,document).configuration.Config0.Value != "near" || bytes.Contains(document,[]byte("PRIVATE_SENTINEL")) { t.Fatal("excluded root application value became active") }
 }
 for _, selectors := range [][2][]byte{{nil,nil},{nil,[]byte("{}\n")},{[]byte("capabilities: PRIVATE_SENTINEL\n"),nil}} {
  root := rootRelationship+"capabilities: PRIVATE_SENTINEL\n"
  if _, err := composeRuntimeTemplateDocument(fixture(t),[]byte(root),selectors[0],selectors[1]); err == nil { t.Fatal("capabilities accepted as active runtime configuration") }
 }
}

func TestDeployedSelectorsUseOnlyConfigurationRootAndPrivateBaseline(t *testing.T) {
 root := t.TempDir()
 write := func(name, content string) { t.Helper(); if err := os.WriteFile(filepath.Join(root,name), []byte(content), 0600); err != nil { t.Fatal(err) } }
 write("plystra.yaml", rootRelationship+"config: {example.com/probe.New: {value: root}}\n")
 write("plystra.prod.yaml", "config: {example.com/probe.New: {value: environment}}\n")
 write("replacement.yaml", "{}\n")
 f, err := privatefile.Create(filepath.Join(root,"baseline.json")); if err != nil { t.Fatal(err) }
 if _, err := f.Write([]byte(initialBaseline)); err != nil { t.Fatal(err) }; if err := f.Close(); err != nil { t.Fatal(err) }
 for _, test := range []struct { args, env []string; expected string }{
  {nil,nil,"root"},
  {[]string{"--env","prod"},nil,"environment"},
  {[]string{"--config","replacement.yaml"},nil,"near"},
  {nil,[]string{"PLYSTRA_ENV=prod"},"environment"},
  {nil,[]string{"PLYSTRA_CONFIG=replacement.yaml"},"near"},
  {[]string{"--config","replacement.yaml"},[]string{"PLYSTRA_ENV=prod","PLYSTRA_CONFIG=bad"},"near"},
 } {
  args := append([]string{"--configuration-root",root,"--runtime-baseline","baseline.json"}, test.args...)
  document, err := loadRuntimeDocument(RuntimeOptions{Arguments:args,Environment:test.env}); if err != nil { t.Fatal(err) }
  if err := validateRuntimeApplicationModel(document); err != nil { t.Fatal(err) }
  if config(t,document).configuration.Config0.Value != test.expected { t.Fatal("wrong selector layer") }
 }
 write("plystra.yaml", "template: example.com/drift\n")
 for _, selector := range [][]string{nil,{"--env","prod"},{"--config","replacement.yaml"}} {
  args := append([]string{"--configuration-root",root,"--runtime-baseline","baseline.json"}, selector...)
  if _, err := loadRuntimeDocument(RuntimeOptions{Arguments:args}); !errors.Is(err,ErrRuntimeCompatibility) { t.Fatal("accepted live root drift or lost compatibility error",err) }
 }
}

func TestTypedValidationPrecedesSuppression(t *testing.T) {
 d := fixture(t)
 d.Templates[0].YAML = "config: {example.com/probe.New: {value: !!int PRIVATE_SENTINEL}}\n"
 if _, err := composeRuntimeTemplateDocument(d, []byte(rootRelationship), nil, nil); err == nil || strings.Contains(err.Error(), "PRIVATE_SENTINEL") { t.Fatal("accepted suppressed malformed scalar",err) }
 var node yaml.Node
 if err := yaml.Unmarshal([]byte("value: private\n"), &node); err != nil { t.Fatal(err) }
 schema, _, err := runtimeConstructorSchema("example.com/probe.New"); if err != nil { t.Fatal(err) }
 if _, err := constructorconfig.Normalize(schema,node.Content[0]); err != nil { t.Fatal(err) }
}
`
