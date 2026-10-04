package interfaceinventory_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceinventory"
)

func TestResourceConsumerDiscoverySharesProjectBoundaryAndGoIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	app := filepath.Join(root, "app")
	for _, name := range []string{"app", "dependency", "transitive"} {
		writeProject(t, filepath.Join(root, name), "example.com/"+name)
		writeFile(t, filepath.Join(root, name, "resource", "resource.go"), fmt.Sprintf("package resource\nimport \"example.com/ordinary\"\n//plystra:resource data.%s/v1\ntype Resource interface{Read() ordinary.Value}\n", name))
	}
	writeFile(t, filepath.Join(app, "go.mod"), `module example.com/app
go 1.26
require (
 example.com/dependency v1.0.0
 example.com/ordinary v1.1.0
 github.com/plystra/kernel v0.0.0
)
replace example.com/dependency => ../dependency
replace example.com/transitive => ../transitive
replace example.com/ordinary => ../ordinary
replace github.com/plystra/kernel => ../kernel
`)
	writeFile(t, filepath.Join(root, "dependency", "go.mod"), "module example.com/dependency\ngo 1.26\nrequire (\nexample.com/transitive v1.2.0\nexample.com/ordinary v1.0.0\ngithub.com/plystra/kernel v0.0.0\n)\nreplace example.com/ordinary => ../wrong\n")
	writeFile(t, filepath.Join(root, "transitive", "go.mod"), "module example.com/transitive\ngo 1.26\nrequire (\nexample.com/ordinary v1.0.0\ngithub.com/plystra/kernel v0.0.0\n)\n")
	writeFile(t, filepath.Join(root, "ordinary", "go.mod"), "module example.com/ordinary\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "ordinary", "ordinary.go"), "package ordinary\n//plystra:resource invalid\ntype Resource interface{Read() Value}\ntype Value struct{Selected bool}\n//plystra:implements invalid\nfunc New(){}\n")
	writeFile(t, filepath.Join(root, "wrong", "go.mod"), "module example.com/ordinary\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "wrong", "ordinary.go"), "package ordinary\ntype Wrong struct{}\n")
	writeFile(t, filepath.Join(root, "kernel", "go.mod"), "module github.com/plystra/kernel\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "kernel", "optional.go"), "package plystra\ntype Optional[T any] struct{}\n")
	prelude := `import (
 local "example.com/app/resource"
 direct "example.com/dependency/resource"
 transitive "example.com/transitive/resource"
 plystra "github.com/plystra/kernel"
)
type Database = transitive.Resource
`
	appSource := resourceConsumerSource("service.app/v1", prelude, "cfg Config, primary local.Resource, operation Interface, Secondary direct.Resource, optional plystra.Optional[Interface], _transitive Database, Primary local.Resource")
	writeFile(t, filepath.Join(app, "consumer", "new.go"), appSource)
	for _, name := range []string{"dependency", "transitive"} {
		imports := fmt.Sprintf("import resource %q\nimport plystra \"github.com/plystra/kernel\"\n", "example.com/"+name+"/resource")
		writeFile(t, filepath.Join(root, name, "consumer", "new.go"), resourceConsumerSource("service."+name+"/v1", imports, "database resource.Resource, optional plystra.Optional[Interface], operation Interface"))
	}
	for _, directory := range []string{"generated", "testdata", "vendor", "fixture", "fixtures", ".private", "_private"} {
		writeFile(t, filepath.Join(app, directory, "new.go"), strings.Replace(appSource, "service.app/v1", "invalid", 1))
	}
	writeFile(t, filepath.Join(app, "inactive", "new.go"), "//go:build resource_inactive\n\n"+strings.Replace(appSource, "service.app/v1", "invalid", 1))
	before := snapshotFiles(t, root)
	environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"})
	discovery := discoverApplication(t, app, environment)
	implementations := discovery.Implementations().Implementations()
	if len(implementations) != 3 || len(discovery.Interfaces().Interfaces()) != 3 || len(discovery.Resources().Resources()) != 3 || len(discovery.ResourceProviders().Providers()) != 0 {
		t.Fatal("ordinary modules or excluded packages changed visible inventory membership")
	}
	for n, name := range []string{"app", "dependency", "transitive"} {
		implementation := implementations[n]
		if implementation.Symbol().String() != "example.com/"+name+"/consumer.New" || implementation.Local() != (n == 0) || implementation.SourcePath() != "consumer/new.go" {
			t.Fatalf("constructor provenance = %s", implementation.Source())
		}
		version := []string{"", "v1.0.0", "v1.2.0"}[n]
		if implementation.ModuleVersion() != version {
			t.Fatalf("constructor version = %q", implementation.ModuleVersion())
		}
		_, hasConfig := implementation.Configuration()
		if hasConfig != (n == 0) {
			t.Fatal("lost optional first Config")
		}
		required, optional := implementation.RequiredInterfaces(), implementation.OptionalInterfaces()
		if len(required) != 1 || len(optional) != 1 || required[0].ParameterName() != "operation" || optional[0].ParameterName() != "optional" || required[0].ParameterPosition() != 3 || required[0].ID().String() != "service."+name+"/v1" || optional[0].ID() != required[0].ID() {
			t.Fatal("Resource parameters leaked into Interface dependencies")
		}
		if n > 0 {
			resources := implementation.RequiredResources()
			if len(resources) != 1 || resources[0].ParameterName() != "database" || resources[0].ParameterPosition() != 1 || resources[0].ID().String() != "data."+name+"/v1" || resources[0].PackagePath() != "example.com/"+name+"/resource" || optional[0].ParameterPosition() != 2 {
				t.Fatalf("dependency Resources = %#v", resources)
			}
		}
	}
	resources := implementations[0].RequiredResources()
	if len(resources) != 4 || implementations[0].OptionalInterfaces()[0].ParameterPosition() != 5 {
		t.Fatal("lost mixed constructor parameters")
	}
	for n, want := range []struct {
		name, owner string
		position    int
	}{{"primary", "app", 2}, {"Secondary", "dependency", 4}, {"_transitive", "transitive", 6}, {"Primary", "app", 7}} {
		if resources[n].ParameterName() != want.name || resources[n].ParameterPosition() != want.position || resources[n].ID().String() != "data."+want.owner+"/v1" || resources[n].PackagePath() != "example.com/"+want.owner+"/resource" {
			t.Fatalf("Resource %d = %#v", n, resources[n])
		}
	}
	second := discoverApplication(t, app, environment)
	for n, implementation := range second.Implementations().Implementations() {
		if !reflect.DeepEqual(implementations[n].RequiredResources(), implementation.RequiredResources()) {
			t.Fatal("unstable Resource dependency inventory")
		}
	}
	if !reflect.DeepEqual(before, snapshotFiles(t, root)) {
		t.Fatal("Resource consumer discovery mutated authored files")
	}
	writeFile(t, filepath.Join(app, "consumer", "new.go"), strings.Replace(appSource, "primary local.Resource, operation Interface", "operation Interface, PRIMARY local.Resource", 1))
	changedBefore := snapshotFiles(t, root)
	changed := discoverApplication(t, app, environment)
	parameter := changed.Implementations().Implementations()[0].RequiredResources()[0]
	if parameter.ParameterName() != "PRIMARY" || parameter.ParameterPosition() != 3 || !reflect.DeepEqual(discovery.Resources().Resources(), changed.Resources().Resources()) {
		t.Fatal("constructor schema change was lost or changed Resource contract identity")
	}
	if !reflect.DeepEqual(changedBefore, snapshotFiles(t, root)) {
		t.Fatal("rediscovery mutated changed authored files")
	}
}

func TestResourceConsumerDiscoveryRejectsInvalidParametersWithOwningSources(t *testing.T) {
	t.Parallel()
	const resourceImport = `import api "example.com/owner/api"`
	for _, test := range []struct {
		name, prelude, parameter string
		sentinel                 error
	}{
		{"unnamed", resourceImport, "api.Resource", implementationinventory.ErrInvalidRequiredResource},
		{"blank", resourceImport, "_ api.Resource", implementationinventory.ErrInvalidRequiredResource},
		{"blank-after-config", resourceImport, "cfg Config, _ api.Resource", implementationinventory.ErrInvalidRequiredResource},
		{"unnamed-after-config", resourceImport, "Config, api.Resource", implementationinventory.ErrInvalidRequiredResource},
		{"pointer", resourceImport, "database *api.Resource", implementationinventory.ErrInvalidRequiredResource},
		{"slice", resourceImport, "database []api.Resource", implementationinventory.ErrInvalidRequiredResource},
		{"variadic", resourceImport, "database ...api.Resource", implementationinventory.ErrInvalidRequiredResource},
		{"array", resourceImport, "database [1]api.Resource", implementationinventory.ErrInvalidRequiredResource},
		{"alias-pointer", resourceImport + "\ntype Database = *api.Resource", "database Database", implementationinventory.ErrInvalidRequiredResource},
		{"local-resource-lookalike", resourceImport + "\ntype Resource interface{Read() api.Value}", "database Resource", implementationinventory.ErrInvalidRequiredResource},
		{"ordinary-module", `import ordinary "example.com/ordinary"`, "database ordinary.Resource", implementationinventory.ErrInvalidRequiredResource},
		{"noncanonical-package", `import api "example.com/owner/unmarked"`, "database api.Resource", implementationinventory.ErrInvalidRequiredResource},
		{"defined-copy", resourceImport + "\ntype Database api.Resource", "database Database", implementationinventory.ErrInvalidRequiredInterface},
		{"anonymous-copy", resourceImport, "database interface{Read() api.Value}", implementationinventory.ErrInvalidRequiredInterface},
		{"optional-resource", resourceImport + "\nimport plystra \"github.com/plystra/kernel\"", "database plystra.Optional[api.Resource]", implementationinventory.ErrInvalidOptionalInterface},
		{"private-struct", "", "database struct{Value string `plystra-default:\"PRIVATE_RESOURCE_DEFAULT\"`}", implementationinventory.ErrInvalidRequiredInterface},
	} {
		for _, location := range []string{"current", "direct", "transitive"} {
			t.Run(test.name+"/"+location, func(t *testing.T) {
				root := t.TempDir()
				owner := filepath.Join(root, "owner")
				writeProject(t, owner, "example.com/owner")
				writeFile(t, filepath.Join(owner, "go.mod"), "module example.com/owner\ngo 1.26\nrequire (\nexample.com/ordinary v1.0.0\ngithub.com/plystra/kernel v0.0.0\n)\nreplace example.com/ordinary => ../ordinary\nreplace github.com/plystra/kernel => ../kernel\n")
				writeFile(t, filepath.Join(owner, "api", "resource.go"), resourceSource)
				writeFile(t, filepath.Join(owner, "unmarked", "resource.go"), strings.Replace(resourceSource, "//plystra:resource data.database/v1\n", "", 1))
				writeFile(t, filepath.Join(owner, "consumer", "new.go"), resourceConsumerSource("service.consumer/v1", test.prelude, test.parameter))
				writeFile(t, filepath.Join(root, "ordinary", "go.mod"), "module example.com/ordinary\ngo 1.26\n")
				writeFile(t, filepath.Join(root, "ordinary", "resource.go"), resourceSource)
				writeFile(t, filepath.Join(root, "kernel", "go.mod"), "module github.com/plystra/kernel\ngo 1.26\n")
				writeFile(t, filepath.Join(root, "kernel", "optional.go"), "package plystra\ntype Optional[T any] struct{}\n")
				app := owner
				if location != "current" {
					app = filepath.Join(root, "app")
					writeProject(t, app, "example.com/app")
					required := "example.com/owner v1.2.3"
					if location == "transitive" {
						required = "example.com/bridge v1.0.0"
						writeProject(t, filepath.Join(root, "bridge"), "example.com/bridge")
						writeFile(t, filepath.Join(root, "bridge", "go.mod"), "module example.com/bridge\ngo 1.26\nrequire example.com/owner v1.2.3\n")
					}
					writeFile(t, filepath.Join(app, "go.mod"), "module example.com/app\ngo 1.26\nrequire "+required+"\nreplace example.com/owner => ../owner\nreplace example.com/bridge => ../bridge\nreplace example.com/ordinary => ../ordinary\nreplace github.com/plystra/kernel => ../kernel\n")
				}
				before := snapshotFiles(t, root)
				_, err := discoverApplicationResult(t, app, goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}))
				var invalid *implementationinventory.ValidationError
				if !errors.Is(err, interfaceinventory.ErrDiscover) || !errors.Is(err, test.sentinel) || !errors.As(err, &invalid) || invalid.ModulePath() != "example.com/owner" || invalid.SourcePath() != "consumer/new.go" || invalid.Line() <= 0 || invalid.Column() != 6 {
					t.Fatalf("missing owning source or sentinel: %v", err)
				}
				for _, private := range []string{root, filepath.ToSlash(root), "plystra-discovery-", "PRIVATE_RESOURCE_DEFAULT", "PRIVATE_CONSUMER_DEFAULT", "constructor-entry"} {
					if strings.Contains(err.Error(), private) {
						t.Fatalf("discovery diagnostic disclosed private data: %v", err)
					}
				}
				if !reflect.DeepEqual(before, snapshotFiles(t, root)) {
					t.Fatal("invalid Resource consumer discovery mutated authored files")
				}
			})
		}
	}
}

func resourceConsumerSource(id, prelude, parameters string) string {
	return fmt.Sprintf(`package consumer
import "context"
%s
//plystra:interface %s
type Interface interface{Run(context.Context, Request)(Response,error)}
type Request struct{}
type Response struct{}
type Config struct{Mode string `+"`plystra-default:\"PRIVATE_CONSUMER_DEFAULT\"`"+`}
type service struct{}
func (*service) Run(context.Context, Request)(Response,error){return Response{},nil}
//plystra:implements %s
func New(%s)(*service,error){panic("constructor-entry")}
`, prelude, id, id, parameters)
}
