package applicationresolve_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/constructorgraph"
)

func TestResolveResourceConsumersRemainDormantUntilReached(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			for _, activation := range []string{"unselected", "dormant", "direct", "transitive", "optional-absent", "optional-present"} {
				t.Run(activation, func(t *testing.T) {
					t.Parallel()
					root := writeResourceConsumerProject(t, activation)
					configuration := "{}\n"
					switch activation {
					case "dormant":
						configuration = "interfaces: {use: {app.resource/v1: example.com/resource-consumer/consumer.New}}\n"
					case "direct", "optional-present":
						configuration = "interfaces: {require: [app.resource/v1]}\n"
					case "transitive", "optional-absent":
						configuration = "interfaces: {require: [app.entry/v1]}\n"
					}
					if activation == "optional-present" {
						configuration = "interfaces: {require: [app.entry/v1, app.resource/v1]}\n"
					}
					options := applicationresolve.Options{Start: root, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})}
					path := "plystra.yaml"
					owner := "example.com/resource-consumer"
					switch mode {
					case "environment":
						path, options.EnvironmentName = "plystra.production.yaml", "production"
					case "replacement":
						path, options.ConfigurationPath = "deploy/customer.yaml", "deploy/customer.yaml"
					}
					writeFile(t, filepath.Join(root, filepath.FromSlash(path)), configuration)
					before := snapshotTree(t, root)
					resolved, err := applicationresolve.Resolve(t.Context(), options)
					active := activation == "direct" || activation == "transitive" || activation == "optional-present"
					if !active {
						if err != nil {
							t.Fatal(err)
						}
						for _, node := range resolved.InterfaceResolution().Graph().ConstructionOrder() {
							if node.Symbol().String() == "example.com/resource-consumer/consumer.New" {
								t.Fatal("dormant Resource consumer entered assembly")
							}
						}
						for _, requirement := range resolved.InterfaceResolution().Graph().Roots() {
							if requirement.InterfaceID().String() == "storage.database/v1" {
								t.Fatal("Resource dependency became an Interface root")
							}
						}
					} else {
						var unsupported *constructorgraph.ResourceBindingError
						if !errors.Is(err, constructorgraph.ErrMissingResourceBinding) || !errors.As(err, &unsupported) {
							t.Fatalf("reachable Resource consumer error = %v", err)
						}
						if unsupported.Constructor().String() != "example.com/resource-consumer/consumer.New" || unsupported.ResourceID().String() != "storage.database/v1" || unsupported.ParameterName() != "primary" || unsupported.ParameterPosition() != 1 || unsupported.ModulePath() != "example.com/resource-consumer" || unsupported.SourcePath() != "consumer/service.go" || unsupported.Line() != 9 || unsupported.Column() != 6 {
							t.Fatalf("Resource source/parameter identity lost: %v", err)
						}
						sources := unsupported.RequirementSources()
						if len(sources) != 1 || sources[0].ModulePath != owner || sources[0].Path != path {
							t.Fatalf("Resource activation sources = %#v", sources)
						}
						wantSteps := 0
						if activation != "direct" {
							wantSteps = 1
						}
						steps := unsupported.Steps()
						if len(steps) != wantSteps || (wantSteps != 0 && (steps[0].ParameterName() != "worker" || steps[0].ParameterPosition() != 1 || steps[0].Optional() != (activation == "optional-present"))) {
							t.Fatalf("Resource activation path = %#v", steps)
						}
						sources[0].ModulePath = "changed"
						if unsupported.RequirementSources()[0].ModulePath != owner {
							t.Fatal("Resource failure source accessor is not defensive")
						}
						if len(steps) != 0 {
							steps[0] = constructorgraph.PathStep{}
							if unsupported.Steps()[0].ParameterName() != "worker" {
								t.Fatal("Resource failure path accessor is not defensive")
							}
						}
						for _, private := range []string{root, filepath.ToSlash(root), "PRIVATE_CONSTRUCTOR_ENTRY"} {
							if strings.Contains(err.Error(), private) {
								t.Fatal("Resource failure exposed private input")
							}
						}
					}
					if !reflect.DeepEqual(before, snapshotTree(t, root)) {
						t.Fatal("Resource consumer resolution changed input files")
					}
				})
			}
		})
	}
}

func writeResourceConsumerProject(t testing.TB, activation string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/resource-consumer\ngo 1.26\nrequire (\ngithub.com/plystra/kernel v0.0.0\nexample.com/resource-dependency v1.0.0\n)\nreplace github.com/plystra/kernel => ./kernel\nreplace example.com/resource-dependency => ./base\n")
	writeModule(t, filepath.Join(root, "kernel"), "github.com/plystra/kernel")
	writeFile(t, filepath.Join(root, "kernel", "optional.go"), "package plystra\ntype Optional[T any] struct{}\n")
	writeModule(t, filepath.Join(root, "base"), "example.com/resource-dependency")
	writeFile(t, filepath.Join(root, "base", "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeResolvedInterface(t, root, "app/resource/v1", "resourcev1", "app.resource/v1", "Run")
	writeResolvedInterface(t, root, "app/entry/v1", "entryv1", "app.entry/v1", "Run")
	writeFile(t, filepath.Join(root, "database", "resource.go"), "package database\n//plystra:resource storage.database/v1\ntype Resource interface { Health() error }\n")
	writeFile(t, filepath.Join(root, "consumer", "service.go"), `package consumer
import (
 "context"
 api "example.com/resource-consumer/interfaces/app/resource/v1"
 database "example.com/resource-consumer/database"
)
type Service struct{}
//plystra:implements app.resource/v1
func New(primary database.Resource, Replica database.Resource) (*Service,error) { panic("PRIVATE_CONSTRUCTOR_ENTRY") }
func (*Service) Run(context.Context, api.Request) (api.Response,error) { return api.Response{},nil }
`)
	parameter := "worker api.Interface"
	optionalImport := ""
	if strings.HasPrefix(activation, "optional-") {
		parameter = "worker plystra.Optional[api.Interface]"
		optionalImport = "plystra \"github.com/plystra/kernel\"\n"
	}
	writeFile(t, filepath.Join(root, "entry", "service.go"), "package entry\nimport (\n\"context\"\napi \"example.com/resource-consumer/interfaces/app/resource/v1\"\nentry \"example.com/resource-consumer/interfaces/app/entry/v1\"\n"+optionalImport+")\ntype Service struct{}\n//plystra:implements app.entry/v1\nfunc New("+parameter+") (*Service,error) { panic(\"PRIVATE_CONSTRUCTOR_ENTRY\") }\nfunc (*Service) Run(context.Context, entry.Request) (entry.Response,error) { return entry.Response{},nil }\n")
	return root
}
