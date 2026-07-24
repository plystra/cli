package interfaceinventory_test

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfaceinventory"
)

func TestDiscoverExactInterfacesLoadsOnlyNamedOrdinaryModulePackages(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/kernel\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "interfaces", "health", "v1", "interface.go"), exactInterfaceSource("kernel.health/v1", "Health", "Status", "string", 7))
	writeFile(t, filepath.Join(root, "interfaces", "info", "v1", "interface.go"), exactInterfaceSource("kernel.info/v1", "Info", "Version", "string", 3))
	writeFile(t, filepath.Join(root, "interfaces", "ignored", "v1", "interface.go"), exactInterfaceSource("kernel.ignored/v1", "Ignore", "Ignored", "bool", 1))
	before := snapshotFiles(t, root)

	index, err := interfaceinventory.DiscoverExactInterfaces(t.Context(), interfaceinventory.ExactInterfacePackages{
		ModulePath:            "example.com/kernel",
		ModuleVersion:         "v1.2.3",
		ModuleRoot:            root,
		ApplicationModulePath: "example.com/application",
		PackagePaths: []string{
			"example.com/kernel/interfaces/info/v1",
			"example.com/kernel/interfaces/health/v1",
		},
	}, interfaceinventory.Options{
		Environment: goEnvironment(map[string]string{
			"GOPROXY": "off",
			"GOSUMDB": "off",
			"GOWORK":  "off",
		}),
	})
	if err != nil {
		t.Fatalf("DiscoverExactInterfaces: %v", err)
	}
	interfaces := index.Interfaces()
	if len(interfaces) != 2 || interfaces[0].ID() != "kernel.health/v1" || interfaces[1].ID() != "kernel.info/v1" {
		t.Fatalf("Interfaces = %#v", interfaces)
	}
	health := interfaces[0]
	fields := health.Contract().ResponseFields()
	if health.ModulePath() != "example.com/kernel" ||
		health.ModuleVersion() != "v1.2.3" ||
		health.Local() ||
		health.PackagePath() != "example.com/kernel/interfaces/health/v1" ||
		!strings.HasPrefix(health.Source(), "example.com/kernel@v1.2.3/interfaces/health/v1/interface.go:") ||
		len(fields) != 1 ||
		fields[0].Name() != "Status" ||
		fields[0].Number() != 7 ||
		!fields[0].Required() ||
		fields[0].Type().Kind() != interfacecontract.TypeString {
		t.Fatalf("health Interface = %#v fields %#v", health, fields)
	}
	if after := snapshotFiles(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("exact Interface discovery mutated the selected ordinary Go Module")
	}
}

func TestDiscoverExactInterfacesRejectsInvalidPackageSelection(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/kernel\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "interfaces", "health", "v1", "interface.go"), exactInterfaceSource("kernel.health/v1", "Health", "Status", "string", 1))
	writeFile(t, filepath.Join(root, "plain", "plain.go"), "package plain\n")
	writeFile(t, filepath.Join(root, "internal", "private", "interface.go"), exactInterfaceSource("kernel.private/v1", "Private", "Value", "string", 1))
	before := snapshotFiles(t, root)
	environment := goEnvironment(map[string]string{
		"GOPROXY": "off",
		"GOSUMDB": "off",
		"GOWORK":  "off",
	})

	tests := []struct {
		name     string
		packages []string
		want     string
	}{
		{
			name: "duplicate",
			packages: []string{
				"example.com/kernel/interfaces/health/v1",
				"example.com/kernel/interfaces/health/v1",
			},
			want: "selected more than once",
		},
		{name: "missing", packages: []string{"example.com/kernel/interfaces/missing/v1"}, want: "is absent"},
		{name: "no Interface directive", packages: []string{"example.com/kernel/plain"}, want: "is absent"},
		{name: "inaccessible internal package", packages: []string{"example.com/kernel/internal/private"}, want: "is not importable"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			index, err := interfaceinventory.DiscoverExactInterfaces(t.Context(), interfaceinventory.ExactInterfacePackages{
				ModulePath:            "example.com/kernel",
				ModuleVersion:         "v1.2.3",
				ModuleRoot:            root,
				ApplicationModulePath: "example.com/application",
				PackagePaths:          test.packages,
			}, interfaceinventory.Options{Environment: environment})
			if err == nil || len(index.Interfaces()) != 0 || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("DiscoverExactInterfaces = %#v, %v; want %q", index.Interfaces(), err, test.want)
			}
		})
	}
	if after := snapshotFiles(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("rejected exact Interface discovery mutated the selected ordinary Go Module")
	}
}

func exactInterfaceSource(identifier, method, field, fieldType string, number int) string {
	tag := fmt.Sprintf("`json:%q plystra:%q`", strings.ToLower(field), fmt.Sprintf("%d,required", number))
	return fmt.Sprintf(`package v1

import "context"

//plystra:interface %s
type Interface interface {
	%s(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct {
	%s %s %s
}
`, identifier, method, field, fieldType, tag)
}
