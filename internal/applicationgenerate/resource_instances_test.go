package applicationgenerate_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/generatedfiles"
)

func TestGeneratedNamedResourceInstances(t *testing.T) {
	root := t.TempDir()
	const module = "example.com/resource-instances"
	writeApplicationModule(t, root, module)
	writeAssemblyInterface(t, root, "probe/run/v1", "runv1", "probe.run/v1", "Run", "type Request struct{}\ntype Response struct { Value string `plystra:\"1\"` }\n")
	writeAssemblyInterface(t, root, "probe/echo/v1", "echov1", "probe.echo/v1", "Echo", "type Request struct{}\ntype Response struct{}\n")
	writeAssemblyInterface(t, root, "probe/absent/v1", "absentv1", "probe.absent/v1", "Absent", "type Request struct{}\ntype Response struct{}\n")
	writeFile(t, filepath.Join(root, "database/resource.go"), `package database
//plystra:resource storage.database/v1
type Resource interface { Value() string }
`)
	writeFile(t, filepath.Join(root, "probe/probe.go"), `package probe
import "sync"
var mu sync.Mutex
var events []string
func Record(event string) { mu.Lock(); defer mu.Unlock(); events=append(events,event) }
func Events() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil),events...) }
func Reset() { mu.Lock(); defer mu.Unlock(); events=nil }
`)
	writeFile(t, filepath.Join(root, "provider/provider.go"), strings.ReplaceAll(`package provider
import (
 "context"
 "errors"
 "github.com/plystra/kernel/configuration"
 "example.com/resource-instances/probe"
)
type Nested struct {
 Label string @@yaml:"label" plystra-default:"private-default"@@
 Count int8 @@yaml:"count" plystra-default:"3"@@
}

type Config struct {
 Name string @@yaml:"name" plystra:"required"@@
 Backend string @@yaml:"backend" plystra:"build-visible" plystra-default:"memory"@@
 Nested Nested @@yaml:"nested"@@
 Password configuration.Secret @@yaml:"password"@@
}
var Configs []Config
var FailName, Result, FailStart, FailStop string
var Cancel context.CancelFunc
type value struct { name string; ready bool }
//plystra:implements-resource storage.database/v1
func New(c Config) (*value,error) {
 Configs=append(Configs,c)
 probe.Record("construct:"+c.Name)
 v:=&value{name:c.Name}
 if c.Name==FailName {
  switch Result {
  case "nil-error": return nil,errors.New("PRIVATE_RESOURCE_FAILURE")
  case "partial-error": return v,errors.New("PRIVATE_RESOURCE_FAILURE")
  case "nil-nil": return nil,nil
  case "panic": panic("PRIVATE_RESOURCE_PANIC")
  }
 }
 return v,nil
}
func (v *value) Value() string { if !v.ready {panic("dependency not ready")}; return v.name }
func (v *value) Start(ctx context.Context) error {
 probe.Record("start:"+v.name)
 if v.name==FailStart {
  if Result=="panic" {panic("PRIVATE_RESOURCE_START")}
  if Result=="cancel" {Cancel();return ctx.Err()}
  return errors.New("PRIVATE_RESOURCE_START")
 }
 if err:=ctx.Err();err!=nil{return err}
 v.ready=true;return nil
}
func (v *value) Stop(context.Context) error {
 probe.Record("stop:"+v.name)
 if v.name==FailStop {FailStop="";return errors.New("PRIVATE_RESOURCE_STOP")}
 v.ready=false;return nil
}
`, "@@", "`"))
	writeFile(t, filepath.Join(root, "plain/plain.go"), `package plain
type value struct{}
//plystra:implements-resource storage.database/v1
func New() (*value,error) {return &value{},nil}
func (*value) Value() string {return "plain"}
`)
	writeFile(t, filepath.Join(root, "echo/echo.go"), `package echo
import (
 "context"
 echov1 "example.com/resource-instances/interfaces/probe/echo/v1"
)
type service struct{}
//plystra:implements probe.echo/v1
func New() (*service,error) {return &service{},nil}
func (*service) Echo(context.Context,echov1.Request)(echov1.Response,error) {return echov1.Response{},nil}
`)
	writeFile(t, filepath.Join(root, "wrapper/wrapper.go"), strings.ReplaceAll(`package wrapper
import (
 "context"
 "errors"
 "example.com/resource-instances/database"
 "example.com/resource-instances/probe"
)
type Config struct { Label string @@yaml:"label" plystra:"required"@@ }
var FailStop string
type value struct { upstream database.Resource; ready bool }
//plystra:implements-resource storage.database/v1
func New(c Config, upstream database.Resource) (*value,error) {
 if c.Label!="private-wrapper" {panic("wrong wrapper Config index")}
 probe.Record("construct:wrapper");return &value{upstream:upstream},nil
}
func (v *value) Value() string { return "wrapped:"+v.upstream.Value() }
func (v *value) Start(context.Context) error { probe.Record("start:wrapper:"+v.upstream.Value());v.ready=true;return nil }
func (v *value) Stop(context.Context) error {
 if !v.ready {probe.Record("stop:wrapper");return nil}
 probe.Record("stop:wrapper:"+v.upstream.Value())
 if mode:=FailStop;mode!="" {FailStop="";if mode=="panic" {panic("PRIVATE_WRAPPER_STOP")};return errors.New("PRIVATE_WRAPPER_STOP")}
 v.ready=false;return nil
}
`, "@@", "`"))
	writeFile(t, filepath.Join(root, "service/service.go"), strings.ReplaceAll(`package service
import (
 "context"
 plystra "github.com/plystra/kernel"
 "example.com/resource-instances/database"
 "example.com/resource-instances/probe"
 echov1 "example.com/resource-instances/interfaces/probe/echo/v1"
 absentv1 "example.com/resource-instances/interfaces/probe/absent/v1"
 runv1 "example.com/resource-instances/interfaces/probe/run/v1"
)
type Config struct { Label string @@yaml:"label" plystra:"required"@@ }
type service struct { primary, duplicate, replica, wrapped database.Resource; ready bool }
//plystra:implements probe.run/v1
func New(c Config, primary database.Resource, echo echov1.Interface, duplicate database.Resource, optional plystra.Optional[echov1.Interface], replica database.Resource, absent plystra.Optional[absentv1.Interface], wrapped database.Resource) (*service,error) {
 if c.Label!="private-service" || echo==nil || !optional.Available() || absent.Available() {panic("mixed constructor parameters not delivered")}
 if primary!=duplicate || primary==replica {panic("wrong instance sharing")}
 probe.Record("construct:service")
 return &service{primary:primary,duplicate:duplicate,replica:replica,wrapped:wrapped},nil
}
func (s *service) Run(context.Context,runv1.Request)(runv1.Response,error) { return runv1.Response{Value:s.primary.Value()+"/"+s.replica.Value()+"/"+s.wrapped.Value()},nil }
func (s *service) Start(context.Context) error { probe.Record("start:service:"+s.wrapped.Value());s.ready=true;return nil }
func (s *service) Stop(context.Context) error {
 if !s.ready {probe.Record("stop:service");return nil}
 probe.Record("stop:service:"+s.wrapped.Value());s.ready=false;return nil
}
`, "@@", "`"))
	writeFile(t, filepath.Join(root, "plystra.yaml"), namedResourceDocument)
	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("generate = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	provenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, resource := range provenance.InterfaceProvenance().Resources() {
		order = append(order, resource.Name)
		if resource.ContractSource.Path != "database/resource.go" || resource.DeclarationSource.Module != module || len(resource.SelectionSources) == 0 {
			t.Fatalf("Resource provenance incomplete: %#v", resource)
		}
	}
	if !reflect.DeepEqual(order, []string{"database.primary", "database.a-wrapper", "database.nop", "database.replica"}) || len(provenance.InterfaceProvenance().ResourceBindings()) != 5 {
		t.Fatalf("persisted Resource graph differs from assembly: %v", order)
	}
	for _, path := range []string{"generated/manifest.json", "generated/go/assembly/interfaces_gen.go", "generated/go/bootstrap/bootstrap_gen.go"} {
		artifact, exists, err := generatedfiles.ReadArtifact(root, path)
		if err != nil || !exists || !slices.Contains(artifact.InputRecordIDs(), "resource-instance:database.primary") || !slices.Contains(artifact.InputRecordIDs(), "resource-binding:instances:database.a-wrapper:upstream") {
			t.Fatalf("%s lost Resource artifact ownership: %v", path, err)
		}
	}
	for _, file := range snapshotGenerated(t, root) {
		for _, private := range []string{"private-default", "PRIVATE_RESOURCE_SECRET", "private-primary", "private-replica"} {
			if bytes.Contains(file.data, []byte(private)) {
				t.Fatalf("%s exposed private Resource configuration", file.path)
			}
		}
		if strings.Contains(file.path, "proto/") || strings.Contains(file.path, "javascript/") {
			if bytes.Contains(file.data, []byte("storage.database/v1")) {
				t.Fatalf("Resource entered transport output %s", file.path)
			}
		}
	}
	before := snapshotTree(t, root)
	stdout.Reset()
	stderr.Reset()
	if code := command.RunIn([]string{"generate", "--check"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("check = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	if !reflect.DeepEqual(before, snapshotTree(t, root)) {
		t.Fatal("check mutated the Project")
	}
	for _, change := range []struct {
		name, document string
		exit           int
	}{
		{"runtime-only", strings.ReplaceAll(strings.ReplaceAll(namedResourceDocument, "private-primary", "private-changed"), "PRIVATE_RESOURCE_SECRET", "OTHER_RESOURCE_SECRET"), 0},
		{"build-visible", strings.Replace(namedResourceDocument, "name: private-primary", "backend: postgres, name: private-primary", 1), 1},
	} {
		writeFile(t, filepath.Join(root, "plystra.yaml"), change.document)
		before := snapshotTree(t, root)
		stdout.Reset()
		stderr.Reset()
		if code := command.RunIn([]string{"generate", "--check"}, &stdout, &stderr, root, goEnvironment(nil)); code != change.exit {
			t.Fatalf("%s Resource identity check = %d: %s %s", change.name, code, &stdout, &stderr)
		}
		if !reflect.DeepEqual(before, snapshotTree(t, root)) {
			t.Fatal("Resource identity check mutated Project")
		}
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), namedResourceDocument)
	writeFile(t, filepath.Join(root, "resource_runtime_test.go"), strings.ReplaceAll(namedResourceRuntimeTest, "RESOURCE_DOCUMENT", "`"+namedResourceDocument+"`"))
	process := exec.CommandContext(t.Context(), "go", "test", "-race", "-mod=readonly", "-count=1", ".")
	process.Dir, process.Env = root, goEnvironment(nil)
	if output, err := process.CombinedOutput(); err != nil {
		t.Fatalf("generated Resource runtime: %v\n%s", err, output)
	}
}

func TestGeneratedResourceTemplateBinaryIsSourceIndependent(t *testing.T) {
	sources := filepath.Join(t.TempDir(), "sources")
	root, template := filepath.Join(sources, "application"), filepath.Join(sources, "template")
	const module, dependency = "example.com/resource-deployment", "example.com/resource-template"
	writeApplicationModule(t, root, module)
	writeApplicationModule(t, template, dependency)
	writeFile(t, filepath.Join(template, "database/resource.go"), "package database\n//plystra:resource storage.database/v1\ntype Resource interface { Value() string }\n")
	writeFile(t, filepath.Join(template, "provider/provider.go"), strings.ReplaceAll(`package provider
import ("context";"os";"github.com/plystra/kernel/configuration")
type Config struct {
 Name string @@yaml:"name" plystra:"required"@@
 Count int8 @@yaml:"count" plystra-default:"3"@@
 Password configuration.Secret @@yaml:"password"@@
}
type value struct { config Config }
//plystra:implements-resource storage.database/v1
func New(c Config) (*value,error) {return &value{config:c},nil}
func (v *value) Value() string {return v.config.Name}
func (v *value) Start(context.Context) error {
 if v.config.Count!=3 || string(v.config.Password.Bytes())!="resolved-template-secret" {panic("wrong per-instance typed config")}
 file,err:=os.OpenFile(os.Getenv("RESOURCE_DEPLOYMENT_RESULT"),os.O_CREATE|os.O_WRONLY|os.O_APPEND,0600)
 if err!=nil{return err};defer file.Close()
 _,err=file.WriteString(v.config.Name+"\n");return err
}
func (*value) Stop(context.Context) error {return nil}
`, "@@", "`"))
	writeFile(t, filepath.Join(template, "plystra.yaml"), `resources:
  instances:
    database.primary:
      use: example.com/resource-template/provider.New
      config: {name: private-inherited, password: {env: RESOURCE_TEMPLATE_SECRET}}
    database.replica:
      use: example.com/resource-template/provider.New
      config: {name: private-replica, password: {env: RESOURCE_TEMPLATE_SECRET}}
`)
	writeFile(t, filepath.Join(root, "go.mod"), string(readAbsoluteFile(t, filepath.Join(root, "go.mod")))+"\nrequire "+dependency+" v1.0.0\nreplace "+dependency+" => "+filepath.ToSlash(template)+"\n")
	const relationship = "template: example.com/resource-template\n"
	const delta = "resources: {instances: {database.primary: {config: {name: private-current}}}}\n"
	writeFile(t, filepath.Join(root, "plystra.yaml"), relationship+delta)
	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("generate template Resources = %d: %s %s", code, stdout.Bytes(), stderr.Bytes())
	}
	deployment := t.TempDir()
	binary := filepath.Join(deployment, "application")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-race", "-mod=readonly", "-o", binary, "./generated/go/application")
	build.Dir, build.Env = root, goEnvironment(nil)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	baseline := filepath.Join(deployment, "baseline.json")
	copyPrivateBaseline(t, filepath.Join(root, "dist/runtime-baseline.json"), baseline)
	if err := os.Rename(sources, sources+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			configRoot := t.TempDir()
			writeFile(t, filepath.Join(configRoot, "plystra.yaml"), relationship+delta)
			args := []string{"--smoke", "--configuration-root", configRoot, "--runtime-baseline", baseline}
			switch mode {
			case "environment":
				writeFile(t, filepath.Join(configRoot, "plystra.yaml"), relationship)
				writeFile(t, filepath.Join(configRoot, "plystra.test.yaml"), delta)
				args = append(args, "--env", "test")
			case "replacement":
				writeFile(t, filepath.Join(configRoot, "plystra.yaml"), relationship)
				writeFile(t, filepath.Join(configRoot, "selected.yaml"), delta)
				args = append(args, "--config", "selected.yaml")
			}
			marker := filepath.Join(t.TempDir(), "resource-values")
			process := exec.CommandContext(t.Context(), binary, args...)
			process.Dir, process.Env = t.TempDir(), goEnvironment(map[string]string{"RESOURCE_DEPLOYMENT_RESULT": marker, "RESOURCE_TEMPLATE_SECRET": "resolved-template-secret", "GOMODCACHE": filepath.Join(t.TempDir(), "absent-module-cache")})
			if output, err := process.CombinedOutput(); err != nil {
				t.Fatalf("source-independent Resource runtime: %v\n%s", err, output)
			}
			if got := string(readAbsoluteFile(t, marker)); got != "private-current\nprivate-replica\n" {
				t.Fatalf("selected unconsumed Resource values = %q", got)
			}
		})
	}
}

const namedResourceDocument = `interfaces:
  require: [probe.run/v1]
config:
  example.com/resource-instances/service.New: {label: private-service}
resources:
  instances:
    database.a-wrapper:
      use: example.com/resource-instances/wrapper.New
      config: {label: private-wrapper}
    database.nop:
      use: example.com/resource-instances/plain.New
    database.primary:
      use: example.com/resource-instances/provider.New
      config: {name: private-primary, password: {env: PRIVATE_RESOURCE_SECRET}}
    database.replica:
      use: example.com/resource-instances/provider.New
      config: {name: private-replica}
  bind:
    instances:
      database.a-wrapper: {upstream: database.primary}
    implementations:
      example.com/resource-instances/service.New:
        primary: database.primary
        duplicate: database.primary
        replica: database.replica
        wrapped: database.a-wrapper
`

const namedResourceRuntimeTest = `package application_test
import (
 "context"
 "errors"
 "os"
 "path/filepath"
 "reflect"
 "strings"
 "testing"
 kernelconfiguration "github.com/plystra/kernel/configuration"
 "example.com/resource-instances/generated/go/bootstrap"
 "example.com/resource-instances/probe"
 "example.com/resource-instances/provider"
 "example.com/resource-instances/wrapper"
 runv1 "example.com/resource-instances/interfaces/probe/run/v1"
)
const document = RESOURCE_DOCUMENT

func reset() { probe.Reset(); provider.Configs=nil;provider.FailName="";provider.Result="";provider.FailStart="";provider.FailStop="";provider.Cancel=nil;wrapper.FailStop="" }
func options(t *testing.T, base, overlay, selected string) bootstrap.RuntimeOptions {
 t.Helper()
 baseline,err:=filepath.Abs("dist/runtime-baseline.json");if err!=nil{t.Fatal(err)}
 root:=t.TempDir()
 if err:=os.WriteFile(filepath.Join(root,"plystra.yaml"),[]byte(base),0600);err!=nil{t.Fatal(err)}
 args:=[]string{"--configuration-root",root,"--runtime-baseline",baseline}
 if overlay!="" {if err:=os.WriteFile(filepath.Join(root,"plystra.test.yaml"),[]byte(overlay),0600);err!=nil{t.Fatal(err)};args=append(args,"--env","test")}
 if selected!="" {if err:=os.WriteFile(filepath.Join(root,"selected.yaml"),[]byte(selected),0600);err!=nil{t.Fatal(err)};args=append(args,"--config","selected.yaml")}
 return bootstrap.RuntimeOptions{Arguments:args,Environment:[]string{}}
}
func TestResourceRuntime(t *testing.T) {
 t.Setenv("PRIVATE_RESOURCE_SECRET","resolved-resource-secret")
 for _,mode:=range []string{"default","overlay","replacement"} {t.Run(mode,func(t *testing.T){
  reset()
  base,overlay,selected:=document,"",""
  if mode=="overlay" {overlay="resources: {instances: {database.primary: {config: {nested: {count: 9}}}}}\n"}
  if mode=="replacement" {base="interfaces: {require: [absent.required/v1]}\n";selected=document}
  app,err:=bootstrap.New(context.Background(),options(t,base,overlay,selected));if err!=nil{t.Fatal(err)}
  if len(provider.Configs)!=2 || provider.Configs[0].Name!="private-primary" || provider.Configs[1].Name!="private-replica" || string(provider.Configs[0].Password.Bytes())!="resolved-resource-secret" || provider.Configs[0].Backend!="memory" || provider.Configs[0].Nested.Label!="private-default" {t.Fatal("distinct typed configuration was not delivered")}
  wantCount:=int8(3);if mode=="overlay"{wantCount=9};if provider.Configs[0].Nested.Count!=wantCount{t.Fatal("config-only overlay lost provider or struct inheritance")}
  for _,binding:=range app.Interfaces().Catalog().Bindings(){if strings.Contains(binding.InterfaceID().String(),"storage.database") {t.Fatal("Resource became an Interface")}}
  if err:=app.Start(context.Background());err!=nil{t.Fatal(err)}
  response,err:=app.Interfaces().ProbeRunV1().Run(context.Background(),runv1.Request{})
  if err!=nil || response.Value!="private-primary/private-replica/wrapped:private-primary"{t.Fatalf("shared values: %#v, %v",response,err)}
  if err:=app.Stop(context.Background());err!=nil{t.Fatal(err)}
  want:=[]string{"construct:private-primary","construct:wrapper","construct:private-replica","construct:service","start:private-primary","start:wrapper:private-primary","start:private-replica","start:service:wrapped:private-primary","stop:service:wrapped:private-primary","stop:private-replica","stop:wrapper:private-primary","stop:private-primary"}
  if !reflect.DeepEqual(probe.Events(),want){t.Fatalf("lifecycle order = %v",probe.Events())}
 })}
}
func TestResourceFailuresBeforeConstruction(t *testing.T) {
 t.Setenv("PRIVATE_RESOURCE_SECRET", "")
 for name,change:=range map[string]func(string)string{
  "build visible":func(s string)string{return strings.Replace(s,"name: private-primary","backend: changed, name: private-primary",1)},
  "name":func(s string)string{return strings.Replace(s,"database.replica:","database.changed:",1)},
  "provider":func(s string)string{return strings.Replace(s,"provider.New","wrapper.New",1)},
  "binding":func(s string)string{return strings.Replace(s,"primary: database.primary","primary: database.replica",1)},
  "cycle":func(s string)string{return strings.Replace(s,"upstream: database.primary","upstream: database.a-wrapper",1)},
  "required":func(s string)string{return strings.Replace(s,"name: private-primary, ","",1)},
  "type":func(s string)string{return strings.Replace(s,"name: private-primary","nested: {count: 128}, name: private-primary",1)},
 } {t.Run(name,func(t *testing.T){
  reset()
  _,err:=bootstrap.New(context.Background(),options(t,change(document),"",""))
  if err==nil || len(provider.Configs)!=0 {t.Fatalf("invalid Resource input entered constructors: %v",err)}
  if errors.Is(err,kernelconfiguration.ErrResolve) {t.Fatalf("invalid Resource model resolved a Secret before validation: %v",err)}
  if !errors.Is(err,bootstrap.ErrRuntimeConfiguration) && !errors.Is(err,bootstrap.ErrRuntimeCompatibility) {t.Fatalf("invalid Resource input lost classification: %v",err)}
  if strings.Contains(err.Error(),"PRIVATE_RESOURCE_SECRET") || strings.Contains(err.Error(),"private-primary") {t.Fatal("private failure data leaked")}
  if name=="build visible" && !errors.Is(err,bootstrap.ErrRuntimeCompatibility){t.Fatal("build-visible drift lacked compatibility classification")}
 })}
 reset()
 if _,err:=bootstrap.New(context.Background(),options(t,document,"",""));!errors.Is(err,kernelconfiguration.ErrResolve) {t.Fatalf("missing Secret sentinel not exercised: %v",err)}
}
func TestResourceConstructorResults(t *testing.T) {
 t.Setenv("PRIVATE_RESOURCE_SECRET","resolved-resource-secret")
 for _,result:=range []string{"nil-error","partial-error","nil-nil","panic"} {t.Run(result,func(t *testing.T){
  reset();provider.FailName="private-replica";provider.Result=result
  _,err:=bootstrap.New(context.Background(),options(t,document,"",""))
  if err==nil || strings.Contains(err.Error(),"PRIVATE_RESOURCE_"){t.Fatalf("constructor result = %v",err)}
  want:=[]string{"construct:private-primary","construct:wrapper","construct:private-replica"}
  if result=="partial-error" {want=append(want,"stop:private-replica")}
  want=append(want,"stop:wrapper","stop:private-primary")
  if !reflect.DeepEqual(probe.Events(),want){t.Fatalf("rollback = %v, want %v",probe.Events(),want)}
 })}
}
func TestResourceStartupFailures(t *testing.T) {
 t.Setenv("PRIVATE_RESOURCE_SECRET","resolved-resource-secret")
 for _,result:=range []string{"error","panic","cancel"} {t.Run(result,func(t *testing.T){
  reset();provider.FailStart="private-replica";provider.Result=result
  app,err:=bootstrap.New(context.Background(),options(t,document,"",""));if err!=nil{t.Fatal(err)}
  ctx,cancel:=context.WithCancel(context.Background());defer cancel();provider.Cancel=cancel
  err=app.Start(ctx)
  if err==nil || strings.Contains(err.Error(),"PRIVATE_RESOURCE_"){t.Fatalf("startup failure = %v",err)}
  if result=="cancel" && (!errors.Is(err,context.Canceled) || ctx.Err()==nil){t.Fatalf("real startup cancellation not preserved: %v",err)}
  want:=[]string{"construct:private-primary","construct:wrapper","construct:private-replica","construct:service","start:private-primary","start:wrapper:private-primary","start:private-replica","stop:service","stop:private-replica","stop:wrapper:private-primary","stop:private-primary"}
  if !reflect.DeepEqual(probe.Events(),want){t.Fatalf("startup rollback = %v",probe.Events())}
  if _,err:=app.Interfaces().ProbeRunV1().Run(context.Background(),runv1.Request{});err==nil{t.Fatal("startup failure accepted public work")}
  if err:=app.Stop(context.Background());err!=nil{t.Fatal(err)}
  if !reflect.DeepEqual(probe.Events(),want){t.Fatal("successful rollback hooks repeated")}
 })}
}
func TestResourceCleanupRetry(t *testing.T) {
 t.Setenv("PRIVATE_RESOURCE_SECRET","resolved-resource-secret")
 reset()
 app,err:=bootstrap.New(context.Background(),options(t,document,"",""));if err!=nil{t.Fatal(err)}
 if err:=app.Start(context.Background());err!=nil{t.Fatal(err)}
 provider.FailStop="private-replica"
 if err:=app.Stop(context.Background());err==nil || strings.Contains(err.Error(),"PRIVATE_RESOURCE_"){t.Fatalf("stop failure = %v",err)}
 if err:=app.Stop(context.Background());err!=nil{t.Fatal(err)}
 counts:=map[string]int{}
 for _,event:=range probe.Events(){counts[event]++}
 if counts["stop:service:wrapped:private-primary"]!=1 || counts["stop:private-replica"]!=2 || counts["stop:wrapper:private-primary"]!=1 || counts["stop:private-primary"]!=1 {t.Fatalf("cleanup retry = %v",probe.Events())}
 reset();provider.FailName="private-replica";provider.Result="partial-error";provider.FailStop="private-replica"
 _,err=bootstrap.New(context.Background(),options(t,document,"",""))
 var retry interface {RetryCleanup(context.Context) error}
 if !errors.As(err,&retry){t.Fatalf("partial constructor cleanup not retryable: %v",err)}
 if err:=retry.RetryCleanup(context.Background());err!=nil{t.Fatal(err)}
 counts=map[string]int{}
 for _,event:=range probe.Events(){counts[event]++}
 if counts["stop:private-replica"]!=2 || counts["stop:wrapper"]!=1 || counts["stop:private-primary"]!=1 {t.Fatalf("constructor cleanup retry = %v",probe.Events())}
}
func TestResourceFailedConsumerRetainsReadyDependency(t *testing.T) {
 t.Setenv("PRIVATE_RESOURCE_SECRET","resolved-resource-secret")
 for _,phase:=range []string{"shutdown","rollback"} {for _,outcome:=range []string{"error","panic"} {t.Run(phase+"/"+outcome,func(t *testing.T){
  reset()
  app,err:=bootstrap.New(context.Background(),options(t,document,"",""));if err!=nil{t.Fatal(err)}
  wrapper.FailStop=outcome
  if phase=="rollback" {provider.FailStart="private-replica";err=app.Start(context.Background())} else {
   if err:=app.Start(context.Background());err!=nil{t.Fatal(err)}
   err=app.Stop(context.Background())
  }
  if err==nil || strings.Contains(err.Error(),"PRIVATE_"){t.Fatalf("consumer cleanup failure = %v",err)}
  var stops []string
  for _,event:=range probe.Events(){if strings.HasPrefix(event,"stop:"){stops=append(stops,event)}}
  serviceStop:="stop:service:wrapped:private-primary";if phase=="rollback"{serviceStop="stop:service"}
  want:=[]string{serviceStop,"stop:private-replica","stop:wrapper:private-primary"}
  if !reflect.DeepEqual(stops,want){t.Fatalf("failed consumer stopped its dependency: %v",stops)}
  if err:=app.Stop(context.Background());err!=nil{t.Fatalf("retry lost ready upstream: %v",err)}
  stops=nil
  for _,event:=range probe.Events(){if strings.HasPrefix(event,"stop:"){stops=append(stops,event)}}
  want=append(want,"stop:wrapper:private-primary","stop:private-primary")
  if !reflect.DeepEqual(stops,want){t.Fatalf("retry order = %v",stops)}
 })}}
}
`
