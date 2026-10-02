package applicationgenerate_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
)

func TestGeneratedBootstrapDeliversTypedConstructorConfiguration(t *testing.T) {
	root := t.TempDir()
	const module = "example.com/typed-configuration"
	writeApplicationModule(t, root, module)
	writeAssemblyInterface(t, root, "probe/run/v1", "runv1", "probe.run/v1", "Run", "type Request struct{}\ntype Response struct{}\n")
	writeFile(t, filepath.Join(root, "service", "service.go"), strings.ReplaceAll(`package service
import (
 "context"
 "net/url"
 "os"
 "errors"
 "time"
 "github.com/plystra/kernel/configuration"
 "go.yaml.in/yaml/v3"
 runv1 "example.com/typed-configuration/interfaces/probe/run/v1"
)
type Label string
func (*Label) UnmarshalYAML(*yaml.Node) error { panic("user unmarshalling must not run") }
type Key string
type Nested struct {
 Name string @@yaml:"name" plystra-default:"private-nested-default"@@
 Count int8 @@yaml:"count" plystra-default:"+03"@@
}
type Mixed struct {
 Public int @@yaml:"public" plystra:"build-visible"@@
 Private string @@yaml:"private"@@
}
type Config struct {
 Required string @@yaml:"required" plystra:"required"@@
 Label Label @@yaml:"label" plystra-default:"private-default-label"@@
 Signed int64 @@yaml:"signed"@@
 Unsigned uint64 @@yaml:"unsigned"@@
 Number float32 @@yaml:"number" plystra-default:"+1.25e0"@@
 Enabled bool @@yaml:"enabled" plystra-default:"true"@@
 Duration time.Duration @@yaml:"duration" plystra-default:"90s"@@
 URL url.URL @@yaml:"url" plystra-default:"https://private.example.test/default"@@
 Nested Nested @@yaml:"nested"@@
 Pointer **Nested @@yaml:"pointer"@@
 Items []Nested @@yaml:"items"@@
 Array [2]Nested @@yaml:"array"@@
 Map map[Key]Nested @@yaml:"map"@@
 Public int8 @@yaml:"public" plystra:"build-visible" plystra-default:"7"@@
 PublicLabel string @@yaml:"public_label" plystra:"build-visible" plystra-default:"compiled-build-default"@@
 Mixed []Mixed @@yaml:"mixed"@@
 Password configuration.Secret @@yaml:"password"@@
 Ignored string @@yaml:"-"@@
}
var Last Config
var Constructions int
type service struct{}
//plystra:implements probe.run/v1
func New(config Config) (*service, error) {
 Last = config; Constructions++
 if marker:=os.Getenv("PLYSTRA_TYPED_TEST_MARKER"); marker!="" {
  if config.Required!="binary" || config.Nested.Count!=3 || config.Label!="private-default-label" {return nil,errors.New("unexpected typed input")}
  if err:=os.WriteFile(marker,[]byte("constructed"),0600);err!=nil{return nil,err}
 }
 return &service{}, nil
}
func (*service) Run(context.Context, runv1.Request) (runv1.Response, error) { return runv1.Response{}, nil }
`, "@@", "`"))
	const document = "interfaces: {require: [probe.run/v1]}\nconfig:\n  " + module + "/service.New:\n    required: ready\n    label: private-authored-label\n    mixed: [{public: 2, private: private-authored-mixed}]\n"
	writeFile(t, filepath.Join(root, "plystra.yaml"), document)
	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("generate = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	for _, data := range snapshotTree(t, filepath.Join(root, "generated")) {
		for _, secret := range []string{"private-default-label", "private-nested-default", "private.example.test", "private-authored-label", "private-authored-mixed", "compiled-build-default"} {
			if bytes.Contains(data.data, []byte(secret)) {
				t.Fatalf("generated %s contains private configuration", data.path)
			}
		}
	}
	before := snapshotGenerated(t, root)
	writeFile(t, filepath.Join(root, "plystra.yaml"), strings.ReplaceAll(document, "private-authored", "edited-private"))
	stdout.Reset()
	stderr.Reset()
	if code := command.RunIn([]string{"generate", "--check"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("private edit changed generated output: %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), strings.ReplaceAll(document, "public: 2", "public: 3"))
	stdout.Reset()
	stderr.Reset()
	if code := command.RunIn([]string{"generate", "--check"}, &stdout, &stderr, root, goEnvironment(nil)); code == 0 {
		t.Fatal("build-visible edit did not report drift")
	}
	if !reflect.DeepEqual(before, snapshotGenerated(t, root)) {
		t.Fatal("read-only check changed generated output")
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), document)
	deployment := t.TempDir()
	binary := filepath.Join(deployment, "typed-app")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-race", "-mod=readonly", "-o", binary, "./generated/go/application")
	build.Dir, build.Env = root, goEnvironment(nil)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build typed binary: %v\n%s", err, output)
	}
	configurationRoot := t.TempDir()
	writeFile(t, filepath.Join(configurationRoot, "plystra.yaml"), "interfaces: {require: [probe.run/v1]}\nconfig:\n  "+module+"/service.New:\n    required: binary\n    mixed: [{public: 2}]\n")
	marker := filepath.Join(deployment, "constructed")
	processBinary := exec.CommandContext(t.Context(), binary, "--smoke", "--configuration-root", configurationRoot)
	processBinary.Dir = deployment
	for _, entry := range goEnvironment(map[string]string{"PLYSTRA_TYPED_TEST_MARKER": marker}) {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "PLYSTRA_ENV") && !strings.EqualFold(name, "PLYSTRA_CONFIG") {
			processBinary.Env = append(processBinary.Env, entry)
		}
	}
	if output, err := processBinary.CombinedOutput(); err != nil {
		t.Fatalf("source-independent typed binary: %v\n%s", err, output)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "constructed" {
		t.Fatal("typed binary did not enter constructor with expected values")
	}
	if _, err := os.Stat(filepath.Join(configurationRoot, "go.mod")); !os.IsNotExist(err) {
		t.Fatal("deployment has a module")
	}
	writeFile(t, filepath.Join(root, "runtime_test.go"), typedConfigurationRuntimeTest)
	process := exec.CommandContext(t.Context(), "go", "test", "-race", "-mod=readonly", ".")
	process.Dir, process.Env = root, goEnvironment(nil)
	if output, err := process.CombinedOutput(); err != nil {
		t.Fatalf("typed runtime: %v\n%s", err, output)
	}
}

func TestGeneratedBootstrapRejectsInvalidRecompiledConfigurationMetadata(t *testing.T) {
	root := t.TempDir()
	const module = "example.com/recompiled-configuration"
	writeApplicationModule(t, root, module)
	writeAssemblyInterface(t, root, "probe/run/v1", "runv1", "probe.run/v1", "Run", "type Request struct{}\ntype Response struct{}\n")
	source := strings.ReplaceAll(`package service
import (
 "context"
 "net/url"
 "os"
 "time"
 "github.com/plystra/kernel/configuration"
 runv1 "example.com/recompiled-configuration/interfaces/probe/run/v1"
)
type Config struct {
 Value string @@yaml:"value" plystra:"required"@@
 Enabled bool @@yaml:"enabled" plystra-default:"true"@@
 Duration time.Duration @@yaml:"duration" plystra-default:"1s"@@
 URL url.URL @@yaml:"url" plystra-default:"https://example.test"@@
 Password configuration.Secret @@yaml:"password"@@
 Ignored string @@yaml:"-"@@
 hidden string
}
type service struct{}
//plystra:implements probe.run/v1
func New(config Config) (*service, error) {
 if err:=os.WriteFile(os.Getenv("PLYSTRA_METADATA_TEST_MARKER"), []byte("constructed"), 0600);err!=nil{return nil,err}
 return &service{},nil
}
func (*service) Run(context.Context, runv1.Request) (runv1.Response,error) {return runv1.Response{},nil}
`, "@@", "`")
	servicePath := filepath.Join(root, "service", "service.go")
	writeFile(t, servicePath, source)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [probe.run/v1]}\nconfig:\n  "+module+"/service.New:\n    value: ready\n    enabled: false\n    duration: 2s\n    url: https://runtime.example.test\n    password: {env: PLYSTRA_METADATA_TEST_SECRET}\n")
	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("generate = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	generated := snapshotGenerated(t, root)
	for _, tc := range []struct{ name, from, to string }{
		{"duplicate policy option", `plystra:"required"`, `plystra:"required,required"`},
		{"empty policy option", `plystra:"required"`, `plystra:"required,"`},
		{"duplicate YAML tag", `yaml:"value"`, `yaml:"value" yaml:"private-invalid-key"`},
		{"malformed tag", `yaml:"value"`, `yaml:"value"private-invalid`},
		{"masked boolean default", `plystra-default:"true"`, `plystra-default:"private-invalid"`},
		{"masked duration default", `plystra-default:"1s"`, `plystra-default:"private-invalid"`},
		{"masked URL default", `plystra-default:"https://example.test"`, `plystra-default:"https://private.example/%zz"`},
		{"ignored policy", `yaml:"-"`, `yaml:"-" plystra:"required"`},
		{"private field default", "hidden string", "hidden string `plystra-default:\"private-invalid\"`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeFile(t, servicePath, strings.Replace(source, tc.from, tc.to, 1))
			marker := filepath.Join(t.TempDir(), "constructor-entered")
			process := exec.CommandContext(t.Context(), "go", "run", "-race", "-mod=readonly", "./generated/go/application", "--smoke", "--configuration-root", root)
			process.Dir = root
			process.Env = goEnvironment(map[string]string{"PLYSTRA_METADATA_TEST_MARKER": marker})
			for i := len(process.Env) - 1; i >= 0; i-- {
				name, _, _ := strings.Cut(process.Env[i], "=")
				if strings.EqualFold(name, "PLYSTRA_METADATA_TEST_SECRET") || strings.EqualFold(name, "PLYSTRA_ENV") || strings.EqualFold(name, "PLYSTRA_CONFIG") {
					process.Env = append(process.Env[:i], process.Env[i+1:]...)
				}
			}
			output, err := process.CombinedOutput()
			if err == nil || !bytes.Contains(output, []byte("compiled constructor Config schema changed; regenerate and rebuild")) {
				t.Fatalf("stale compiled metadata = %v\n%s", err, output)
			}
			for _, private := range []string{"private-invalid", "private.example", "PLYSTRA_METADATA_TEST_SECRET", root} {
				if bytes.Contains(output, []byte(private)) {
					t.Fatal("compiled metadata diagnostic leaked private input")
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("invalid compiled metadata entered constructor")
			}
			if !reflect.DeepEqual(generated, snapshotGenerated(t, root)) {
				t.Fatal("startup modified generated output")
			}
		})
	}
	t.Run("compatible recompiled metadata", func(t *testing.T) {
		changed := strings.Replace(source, `plystra-default:"true"`, `plystra-default:"false"`, 1)
		changed = strings.Replace(changed, `yaml:"value"`, `yaml:"value" json:"value"`, 1)
		writeFile(t, servicePath, changed)
		marker := filepath.Join(t.TempDir(), "constructor-entered")
		process := exec.CommandContext(t.Context(), "go", "run", "-race", "-mod=readonly", "./generated/go/application", "--smoke", "--configuration-root", root)
		process.Dir = root
		for _, entry := range goEnvironment(map[string]string{"PLYSTRA_METADATA_TEST_MARKER": marker, "PLYSTRA_METADATA_TEST_SECRET": "private-value"}) {
			name, _, _ := strings.Cut(entry, "=")
			if !strings.EqualFold(name, "PLYSTRA_ENV") && !strings.EqualFold(name, "PLYSTRA_CONFIG") {
				process.Env = append(process.Env, entry)
			}
		}
		if output, err := process.CombinedOutput(); err != nil {
			t.Fatalf("compatible recompiled metadata = %v\n%s", err, output)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("compatible metadata did not enter constructor")
		}
		if !reflect.DeepEqual(generated, snapshotGenerated(t, root)) {
			t.Fatal("compatible metadata changed generated output")
		}
	})
}

func TestGeneratedBootstrapRejectsAdoptedTypedConfigurationUntilBaselineAvailable(t *testing.T) {
	root := t.TempDir()
	const module = "example.com/adopted-runtime-config"
	writeApplicationModule(t, root, module)
	owner := writeConstructorConfigurationOwner(t, root, module, false)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [configuration.owner/v1]}\ncomposition:\n  exports:\n    common:\n      config:\n        "+owner+": {label: private-adopted-value}\n  adopt: [{module: "+module+", export: common}]\n")
	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("generate = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	process := exec.CommandContext(t.Context(), "go", "run", "./generated/go/application", "--smoke", "--configuration-root", root)
	process.Dir, process.Env = root, goEnvironment(nil)
	output, err := process.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("requires private runtime-baseline support")) {
		t.Fatalf("adopted startup = %v\n%s", err, output)
	}
	if bytes.Contains(output, []byte("private-adopted-value")) {
		t.Fatal("error leaked adopted value")
	}
}

const typedConfigurationRuntimeTest = `package application_test
import (
 "context"
 "errors"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"
 "example.com/typed-configuration/generated/go/bootstrap"
 "example.com/typed-configuration/service"
)

func TestTypedStartup(t *testing.T) {
 t.Setenv("TYPED_RUNTIME_PASSWORD", "resolved-private-password")
 prefix := "interfaces: {require: [probe.run/v1]}\nconfig:\n  example.com/typed-configuration/service.New:\n"
 initial := prefix + "    required: ready\n    mixed: [{public: 2, private: runtime-only}]\n"
 for _, tc := range []struct{name, base, overlay, selected, rule string; check func(*testing.T)}{
  {name:"defaults", base:initial, check:func(t *testing.T){
   c:=service.Last
   if c.Label!="private-default-label" || c.Nested.Name!="private-nested-default" || c.Nested.Count!=3 || c.Array[1].Count!=3 || c.Pointer!=nil || c.Items!=nil || c.Map!=nil || c.Number!=1.25 || !c.Enabled || c.Duration!=90*time.Second || c.URL.String()!="https://private.example.test/default" || c.Public!=7 || c.PublicLabel!="compiled-build-default" {t.Fatal("defaults or Go zero values were not delivered")}
  }},
  {name:"all values", base:initial+"    label: runtime-label\n    signed: -9223372036854775808\n    unsigned: 18446744073709551615\n    number: 1.23456789\n    enabled: false\n    duration: 2m\n    url: https://runtime.example.test/path\n    nested: {name: authored}\n    pointer: {name: pointed}\n    items: [{count: -4}]\n    array: [{}, {count: 5}]\n    map: {dynamic: {name: mapped}}\n    password: {env: TYPED_RUNTIME_PASSWORD}\n", check:func(t *testing.T){
   c:=service.Last
   if c.Label!="runtime-label" || c.Signed!=-9223372036854775808 || c.Unsigned!=18446744073709551615 || c.Number!=float32(1.23456789) || c.Enabled || c.Duration!=2*time.Minute || c.URL.Host!="runtime.example.test" || c.Nested.Count!=3 || (**c.Pointer).Name!="pointed" || c.Items[0].Count!=-4 || c.Array[0].Count!=3 || c.Array[1].Count!=5 || c.Map["dynamic"].Name!="mapped" || string(c.Password.Bytes())!="resolved-private-password" {t.Fatal("typed fields were not delivered")}
  }},
  {name:"overlay", base:initial+"    label: lower\n    nested: {name: inherited, count: 9}\n    pointer: {name: lower, count: 11}\n    items: [{name: lower}]\n    map: {lower: {}}\n", overlay:"config:\n  example.com/typed-configuration/service.New:\n    label: {$remove: true}\n    nested: {count: {$remove: true}}\n    pointer: {}\n    items: []\n    map: {}\n", check:func(t *testing.T){
   c:=service.Last
   if c.Label!="private-default-label" || c.Nested.Name!="inherited" || c.Nested.Count!=3 || (**c.Pointer).Name!="private-nested-default" || (**c.Pointer).Count!=3 || c.Items==nil || len(c.Items)!=0 || c.Map==nil || len(c.Map)!=0 {t.Fatal("typed overlay algebra was not applied")}
  }},
  {name:"nil", base:initial+"    pointer: null\n    items: ~\n    map:\n", check:func(t *testing.T){if service.Last.Pointer!=nil || service.Last.Items!=nil || service.Last.Map!=nil {t.Fatal("nil changed")}}},
  {name:"duplicate field", base:initial+"    required: duplicate\n", rule:"unique string keys"},
  {name:"replacement excludes root", base:"config: {example.com/typed-configuration/service.New: {required: wrong}}\n", selected:prefix+"    required: replacement\n    mixed: [{public: 2}]\n", check:func(t *testing.T){if service.Last.Required!="replacement" {t.Fatal("root participated in replacement")}}},
  {name:"missing required", base:"interfaces: {require: [probe.run/v1]}\n", rule:"required field is absent"},
  {name:"removed required", base:initial, overlay:"config:\n  example.com/typed-configuration/service.New:\n    required: {$remove: true}\n", rule:"required field is absent"},
  {name:"removed object", base:initial, overlay:"config:\n  example.com/typed-configuration/service.New: {$remove: true}\n", rule:"required field is absent"},
  {name:"unknown", base:initial+"    private-unknown-key: private-invalid-value\n", rule:"unknown field"},
  {name:"overflow", base:initial+"    nested: {count: 128}\n", rule:"compiled Go type"},
  {name:"null scalar", base:initial+"    label: null\n", rule:"compiled Go type"},
  {name:"null array", base:initial+"    array: null\n", rule:"compiled Go type"},
  {name:"array length", base:initial+"    array: [{}]\n", rule:"compiled Go type"},
  {name:"atomic removal", base:initial+"    pointer: {count: {$remove: true}}\n", rule:"reserved removal mapping"},
  {name:"map removal", base:initial+"    map: {private-dynamic-key: {count: {$remove: true}}}\n", rule:"reserved removal mapping"},
  {name:"drift before secret", base:initial+"    public: 8\n    password: {env: MISSING_PRIVATE_SECRET}\n", rule:"build-visible constructor configuration"},
  {name:"nested drift", base:prefix+"    required: ready\n    mixed: [{public: 3, private: private-invalid-value}]\n", rule:"build-visible constructor configuration"},
  {name:"invalid before secret", base:initial+"    signed: quoted\n    password: {env: MISSING_PRIVATE_SECRET}\n", rule:"compiled Go type"},
  {name:"secret failure", base:initial+"    password: {env: MISSING_PRIVATE_SECRET}\n", rule:"resolve constructor Secret"},
 } {
  t.Run(tc.name,func(t *testing.T){
   root:=t.TempDir()
   if err:=os.WriteFile(filepath.Join(root,"plystra.yaml"),[]byte(tc.base),0600);err!=nil{t.Fatal(err)}
   args:=[]string{"--configuration-root",root}
   if tc.overlay!="" {if err:=os.WriteFile(filepath.Join(root,"plystra.test.yaml"),[]byte(tc.overlay),0600);err!=nil{t.Fatal(err)}; args=append(args,"--env","test")}
   if tc.selected!="" {if err:=os.WriteFile(filepath.Join(root,"selected.yaml"),[]byte(tc.selected),0600);err!=nil{t.Fatal(err)}; args=append(args,"--config","selected.yaml")}
   before:=service.Constructions
   app,err:=bootstrap.New(context.Background(),bootstrap.RuntimeOptions{Arguments:args,Environment:[]string{}})
   if tc.rule!="" {
    if err==nil || !strings.Contains(err.Error(),tc.rule) {t.Fatalf("failure = %v; want %s",err,tc.rule)}
    if service.Constructions!=before {t.Fatal("invalid configuration entered constructor")}
    for _,private:=range []string{"private-unknown-key","private-invalid-value","private-dynamic-key","MISSING_PRIVATE_SECRET",root} {if strings.Contains(err.Error(),private){t.Fatal("diagnostic leaked private input")}}
    if strings.Contains(tc.rule,"build-visible") && !errors.Is(err,bootstrap.ErrRuntimeCompatibility){t.Fatal("missing compatibility classification")}
    return
   }
   if err!=nil {t.Fatal(err)}
   if service.Constructions!=before+1 {t.Fatal("constructor did not run once")}
   tc.check(t)
   if err:=app.Stop(context.Background());err!=nil {t.Fatal(err)}
  })
 }
}
`
