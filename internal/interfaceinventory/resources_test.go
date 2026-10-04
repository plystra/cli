package interfaceinventory_test

import (
	"errors"
	"go/types"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/resourcecontract"
	"github.com/plystra/cli/internal/resourcedecl"
)

const resourceSource = "package api\n//plystra:resource data.database/v1\ntype Resource interface{Read() Value}\ntype Value struct{Data string; Next *Value}\n"

func TestDiscoverResourceContractsUsesSharedProjectBoundary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	app := filepath.Join(root, "app")
	writeProject(t, app, "example.com/app")
	writeProject(t, filepath.Join(root, "dependency"), "example.com/dependency")
	writeFile(t, filepath.Join(app, "go.mod"), "module example.com/app\ngo 1.26\nrequire (\nexample.com/dependency v1.0.0\nexample.com/ordinary v1.0.0\n)\nreplace example.com/dependency => ../dependency\nreplace example.com/ordinary => ../ordinary\nreplace example.com/transitive => ../transitive\n")
	writeFile(t, filepath.Join(root, "dependency", "go.mod"), "module example.com/dependency\ngo 1.26\nrequire example.com/transitive v1.1.0\n")
	writeProject(t, filepath.Join(root, "transitive"), "example.com/transitive")
	writeFile(t, filepath.Join(root, "transitive", "api.go"), strings.ReplaceAll(resourceSource, "data.database", "data.transitive"))
	writeFile(t, filepath.Join(root, "ordinary", "go.mod"), "module example.com/ordinary\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "ordinary", "api.go"), strings.ReplaceAll(resourceSource, "data.database/v1", "invalid"))
	writeFile(t, filepath.Join(app, "api", "resource.go"), resourceSource)
	writeFile(t, filepath.Join(root, "dependency", "resource.go"), strings.ReplaceAll(resourceSource, "data.database", "data.dependency"))
	writeFile(t, filepath.Join(app, "api", "interface.yaml"), "[malformed; Resource does not use this document\n")
	for _, directory := range []string{"generated", "testdata", "vendor", "fixture", "fixtures", ".private", "_private"} {
		writeFile(t, filepath.Join(app, directory, "resource.go"), strings.ReplaceAll(resourceSource, "data.database/v1", "invalid"))
	}
	writeFile(t, filepath.Join(app, "inactive", "resource.go"), "//go:build resource_inactive\n\n"+strings.ReplaceAll(resourceSource, "data.database/v1", "invalid"))
	writeFile(t, filepath.Join(app, "tests", "resource_test.go"), strings.ReplaceAll(resourceSource, "data.database/v1", "invalid"))
	writeFile(t, filepath.Join(app, "ordinary", "ordinary.go"), "package ordinary\nconst marker = `//plystra:resource invalid`\n")
	writeProject(t, filepath.Join(app, "nested"), "example.com/nested")
	writeFile(t, filepath.Join(app, "nested", "api.go"), strings.ReplaceAll(resourceSource, "data.database/v1", "invalid"))
	before := snapshotFiles(t, root)
	environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"})
	first := discoverApplication(t, app, environment).Resources().Resources()
	second := discoverApplication(t, app, environment).Resources().Resources()
	if len(first) != 3 || !reflect.DeepEqual(first, second) {
		t.Fatalf("inventory = %#v / %#v", first, second)
	}
	for i, id := range []string{"data.database/v1", "data.dependency/v1", "data.transitive/v1"} {
		if first[i].ID() != id || len(first[i].ContractDigest()) != 71 || first[i].Declaration().Position().Line != 2 {
			t.Fatalf("Resource = %#v", first[i])
		}
	}
	methods, err := first[0].Contract().ImplementationMethods("example.com/app/generated", func(pkg *types.Package) string { return pkg.Name() })
	if err != nil || len(methods) != 1 || methods[0].Name() != "Read" || methods[0].Signature() != "func() api.Value" {
		t.Fatalf("discovered Resource scaffold methods = %#v, %v", methods, err)
	}
	if !first[0].Local() || first[0].ModuleVersion() != "" || first[1].Local() || first[1].ModuleVersion() != "v1.0.0" || first[2].ModuleVersion() != "v1.1.0" {
		t.Fatal("lost module provenance")
	}
	if after := snapshotFiles(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("Resource discovery modified a Project or dependency")
	}
	writeFile(t, filepath.Join(app, "api", "resource.go"), strings.Replace(resourceSource, "Data string", "Data []byte", 1))
	changed := discoverApplication(t, app, environment).Resources().Resources()
	if changed[0].ContractDigest() == first[0].ContractDigest() || changed[1].ContractDigest() != first[1].ContractDigest() {
		t.Fatal("compiled export-data public shape digest is stale or unstable")
	}
	imported := "package api\nimport ordinary \"example.com/ordinary\"\n//plystra:resource data.database/v1\ntype Resource interface{Read() ordinary.Value}\n"
	writeFile(t, filepath.Join(app, "api", "resource.go"), imported)
	firstImported := discoverApplication(t, app, environment).Resources().Resources()[0].ContractDigest()
	writeFile(t, filepath.Join(root, "ordinary", "api.go"), strings.Replace(strings.ReplaceAll(resourceSource, "data.database/v1", "invalid"), "Data string", "Data []byte", 1))
	secondImported := discoverApplication(t, app, environment).Resources().Resources()[0].ContractDigest()
	if firstImported == secondImported {
		t.Fatal("unchanged external named type concealed its changed public shape")
	}
}

func TestDiscoverResourceFailuresRetainOwningSources(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, source, kind string
		sentinel           error
	}{
		{"identity", strings.ReplaceAll(resourceSource, "data.database/v1", "bad"), "resource-declaration", resourcedecl.ErrInvalid},
		{"lifecycle", strings.Replace(resourceSource, "Read() Value", "Close()", 1), "resource-contract", resourcecontract.ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeProject(t, root, "example.com/app")
			writeFile(t, filepath.Join(root, "api", "resource.go"), test.source)
			before := snapshotFiles(t, root)
			_, err := discoverApplicationResult(t, root, goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}))
			var source *interfaceinventory.SourceError
			if !errors.Is(err, test.sentinel) || !errors.As(err, &source) || source.ModulePath() != "example.com/app" || source.SourcePath() != "api/resource.go" || source.SourceKind() != test.kind || source.Line() != 2 {
				t.Fatalf("error = %v", err)
			}
			if after := snapshotFiles(t, root); !reflect.DeepEqual(before, after) {
				t.Fatal("invalid Resource discovery mutated files")
			}
		})
	}
}

func TestDiscoverDuplicateResourceIDsRetainsEveryDefinition(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeProject(t, root, "example.com/app")
	for _, name := range []string{"z", "a", "m"} {
		writeFile(t, filepath.Join(root, name, "resource.go"), resourceSource)
	}
	_, err := discoverApplicationResult(t, root, goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}))
	var duplicate *interfaceinventory.DuplicateResourceIDError
	if !errors.Is(err, interfaceinventory.ErrDuplicateResourceID) || !errors.As(err, &duplicate) || duplicate.ID() != "data.database/v1" || len(duplicate.Definitions()) != 3 {
		t.Fatalf("error = %v", err)
	}
	for i, name := range []string{"a", "m", "z"} {
		definition := duplicate.Definitions()[i]
		if definition.SourcePath() != name+"/resource.go" || !strings.Contains(err.Error(), definition.Source()) {
			t.Fatalf("missing duplicate source: %v", err)
		}
	}
	view := duplicate.Definitions()
	view[0] = interfaceinventory.Resource{}
	if duplicate.Definitions()[0].ID() == "" {
		t.Fatal("duplicate definitions expose mutable storage")
	}
}
