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
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || stderr.Len() != 0 || result.Status != "validation_failed" || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != diagnosticcode.ResourceBindingUnsupported {
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
			} else if stdout.Len() != 0 || !strings.Contains(stderr.String(), diagnosticcode.ResourceBindingUnsupported) || !strings.Contains(stderr.String(), "primary") {
				t.Fatalf("human Resource failure: %s %s", &stdout, &stderr)
			}
			for _, private := range []string{parent, proxy, "PRIVATE_RESOURCE_CONSTRUCTOR_ENTRY"} {
				if strings.Contains(stdout.String()+stderr.String(), private) {
					t.Fatal("creation disclosed private input")
				}
			}
			if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("target exists after unsupported consumer: %v", err)
			}
			assertNoTransactionFiles(t, parent)
		})
	}
	if !reflect.DeepEqual(before, snapshotTree(t, proxy)) {
		t.Fatal("creation changed the template proxy")
	}
}
