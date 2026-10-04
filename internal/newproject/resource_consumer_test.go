package newproject_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
)

func TestCreateRejectsReachableTemplateResourceConsumerWithoutInstallation(t *testing.T) {
	const module = "example.com/acme/resource-consumer-template"
	proxy := createKernelProxy(t)
	writeProxyModule(t, proxy, module, "v1.0.0", map[string][]byte{
		"plystra.yaml": []byte("interfaces: {require: [app.resource/v1]}\n"),
		"consumer.go": []byte(`package consumer
import "context"
//plystra:interface app.resource/v1
type Interface interface { Run(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct{}
//plystra:resource storage.database/v1
type Resource interface { Health() error }
type Service struct{}
//plystra:implements app.resource/v1
func New(primary Resource) (*Service, error) { panic("PRIVATE_RESOURCE_CONSTRUCTOR_ENTRY") }
func (*Service) Run(context.Context, Request) (Response, error) { return Response{}, nil }
`),
	})
	before := snapshotTree(t, proxy)
	for _, format := range []string{"human", "json"} {
		t.Run(format, func(t *testing.T) {
			parent := t.TempDir()
			var stdout, stderr bytes.Buffer
			arguments := []string{"new", "my-app", "--module", "example.com/acme/my-app", "--template", module + "@v1.0.0", "--format", format}
			exit := command.RunIn(arguments, &stdout, &stderr, parent, isolatedGoEnvironment(t, proxy))
			if exit != 3 {
				t.Fatalf("create = %d: %s %s", exit, &stdout, &stderr)
			}
			if format == "json" {
				var result struct {
					Status      string `json:"status"`
					Diagnostics []struct {
						Code      string                  `json:"code"`
						Locations []diagnosticjson.Source `json:"locations"`
					} `json:"diagnostics"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || stderr.Len() != 0 || result.Status != "validation_failed" || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != diagnosticcode.ResourceBindingMissing {
					t.Fatalf("structured Resource failure: %v: %s %s", err, &stdout, &stderr)
				}
				found := false
				for _, location := range result.Diagnostics[0].Locations {
					if location.Module == module && location.Path == "consumer.go" && location.Kind == "implementation-constructor" && location.Line == 11 && location.Column == 6 {
						found = true
					}
				}
				if !found {
					t.Fatal("creation lost template constructor source")
				}
			} else if stdout.Len() != 0 || !strings.Contains(stderr.String(), diagnosticcode.ResourceBindingMissing) || !strings.Contains(stderr.String(), "primary") {
				t.Fatalf("human Resource failure: %s %s", &stdout, &stderr)
			}
			for _, private := range []string{parent, proxy, "PRIVATE_RESOURCE_CONSTRUCTOR_ENTRY"} {
				if strings.Contains(stdout.String()+stderr.String(), private) {
					t.Fatal("creation disclosed private input")
				}
			}
			if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("target exists after missing Resource binding: %v", err)
			}
			assertNoTransactionFiles(t, parent)
		})
	}
	if !reflect.DeepEqual(before, snapshotTree(t, proxy)) {
		t.Fatal("creation changed the template proxy")
	}
}

func TestPublicCreateQualifiesTemplateResourceLifecycle(t *testing.T) {
	const module = "example.com/acme/resource-lifecycle-template"
	proxy := createKernelProxy(t)
	writeProxyModule(t, proxy, module, "v1.0.0", map[string][]byte{
		"plystra.yaml": []byte(`resources:
  instances:
    database.primary:
      use: example.com/acme/resource-lifecycle-template.New
      config: {name: primary}
    database.replica:
      use: example.com/acme/resource-lifecycle-template.New
      config: {name: replica}
`),
		"resource.go": []byte(strings.ReplaceAll(`package database
import ("context"; "errors"; "os")
//plystra:resource storage.database/v1
type Resource interface { Name() string }
type Config struct { Name string @@yaml:"name" plystra:"required"@@ }
type value struct { name string }
//plystra:implements-resource storage.database/v1
func New(c Config) (*value,error) {return &value{name:c.Name},nil}
func (v *value) Name() string {return v.name}
func (v *value) record(event string) error {
 f,err:=os.OpenFile(os.Getenv("RESOURCE_CREATION_EVENTS"),os.O_CREATE|os.O_WRONLY|os.O_APPEND,0600)
 if err!=nil{return err};defer f.Close()
 _,err=f.WriteString(event+":"+v.name+"\n");return err
}
func (v *value) Start(context.Context) error {
 if err:=v.record("start");err!=nil{return err}
 if v.name=="replica" {
  switch os.Getenv("RESOURCE_CREATION_FAILURE") {
  case "error": return errors.New("PRIVATE_RESOURCE_START_FAILURE")
  case "panic": panic("PRIVATE_RESOURCE_START_FAILURE")
  }
 }
 return nil
}
func (v *value) Stop(context.Context) error {return v.record("stop")}
`, "@@", "`")),
	})
	before := snapshotTree(t, proxy)
	for _, failure := range []string{"", "error", "panic"} {
		name := failure
		if name == "" {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			marker := filepath.Join(t.TempDir(), "events")
			environment := isolatedGoEnvironment(t, proxy)
			environment = setEnvironmentValue(environment, "RESOURCE_CREATION_EVENTS", marker)
			environment = setEnvironmentValue(environment, "RESOURCE_CREATION_FAILURE", failure)
			var stdout, stderr bytes.Buffer
			exit := command.RunIn([]string{"new", "my-app", "--module", "example.com/acme/my-app", "--template", module + "@v1.0.0"}, &stdout, &stderr, parent, environment)
			root := filepath.Join(parent, "my-app")
			if failure == "" {
				if exit != 0 {
					t.Fatalf("Resource template creation = %d: %s %s", exit, &stdout, &stderr)
				}
				document, err := os.ReadFile(filepath.Join(root, "plystra.yaml"))
				if err != nil || !bytes.Contains(document, []byte("template: "+module)) || bytes.Contains(document, []byte("resources:")) {
					t.Fatalf("template relationship not retained as a delta: %s, %v", document, err)
				}
				stdout.Reset()
				stderr.Reset()
				if code := command.RunIn([]string{"generate", "--check"}, &stdout, &stderr, root, environment); code != 0 {
					t.Fatalf("created Resource Project is stale: %d: %s %s", code, &stdout, &stderr)
				}
			} else {
				if exit == 0 || !strings.Contains(stderr.String(), "lifecycle smoke failed") || strings.Contains(stdout.String()+stderr.String(), "PRIVATE_RESOURCE_START_FAILURE") {
					t.Fatalf("Resource smoke failure = %d: %s %s", exit, &stdout, &stderr)
				}
				if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("Resource smoke failure left target: %v", err)
				}
			}
			events, err := os.ReadFile(marker)
			if err != nil || string(events) != "start:primary\nstart:replica\nstop:replica\nstop:primary\n" {
				t.Fatalf("selected unconsumed Resource lifecycle = %q, %v", events, err)
			}
			assertNoTransactionFiles(t, parent)
		})
	}
	if !reflect.DeepEqual(before, snapshotTree(t, proxy)) {
		t.Fatal("Resource template qualification changed source modules")
	}
}
