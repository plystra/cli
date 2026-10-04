package applicationresolve_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationresolve"
)

func TestSelectionInputsSnapshotRejectsDocumentModuleAndDeclarationDrift(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"root", "selected", "template", "module", "dependency-module", "replacement", "parameter", "provider-schema", "provider-default", "contract", "new-declaration", "invalid-declaration", "legacy-exposure"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			root := writeResourceConsumerProject(t, "transitive")
			writeResourceProvider(t, root)
			writePlugin(t, root, "legacy", "id: example.legacy\nprovides: [legacy.run/v1]\n")
			writeFile(t, filepath.Join(root, "plystra.yaml"), "template: example.com/resource-template\n")
			writeFile(t, filepath.Join(root, "plystra.test.yaml"), "{}\n")
			options := selectionOptions(root)
			options.EnvironmentName = "test"
			inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			path := "plystra.yaml"
			switch scenario {
			case "selected":
				path = "plystra.test.yaml"
			case "template":
				path = "base/plystra.yaml"
			case "module":
				path = "go.mod"
			case "dependency-module":
				path = "base/go.mod"
			case "replacement", "parameter", "invalid-declaration":
				path = "consumer/service.go"
			case "provider-schema", "provider-default":
				path = "provider/provider.go"
			case "contract":
				path = "database/resource.go"
			case "new-declaration":
				path = "new-provider/provider.go"
			case "legacy-exposure":
				path = "legacy/plugin.yaml"
			}
			data := ""
			if scenario != "new-declaration" {
				old, err := os.ReadFile(filepath.Join(root, path))
				if err != nil {
					t.Fatal(err)
				}
				data = string(old)
			}
			switch scenario {
			case "parameter":
				data = strings.Replace(data, "primary database.Resource", "changed database.Resource", 1)
			case "provider-schema":
				data = strings.Replace(data, "Count int", "Count uint8", 1)
			case "provider-default":
				data = strings.Replace(data, "yaml:\"label\"", "yaml:\"label\" plystra-default:\"PRIVATE_NEW_DEFAULT\"", 1)
			case "contract":
				data = strings.Replace(data, "Health() error", "Health() error; Changed() error", 1)
			case "new-declaration":
				data = "package provider\ntype Value struct{}\n//plystra:implements-resource storage.database/v1\nfunc New() (*Value,error) { return nil,nil }\nfunc (*Value) Health() error { return nil }\n"
			case "invalid-declaration":
				data = "package consumer\nfunc invalid(\n"
			case "legacy-exposure":
				data = strings.Replace(data, "legacy.run", "legacy.changed", 1)
			case "replacement":
				if err := os.Rename(filepath.Join(root, "plystra.test.yaml"), filepath.Join(root, "original.yaml")); err != nil {
					t.Fatal(err)
				}
				path, data = "plystra.test.yaml", "{}\n"
			case "root", "selected", "template":
				data += "# concurrent private document edit\n"
			default:
				data += "\n// concurrent private module edit\n"
			}
			writeFile(t, filepath.Join(root, path), data)
			changed := snapshotTree(t, root)
			err = inputs.ValidateSnapshot(t.Context())
			if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, applicationresolve.ErrConcurrentChange) {
				t.Fatalf("%s drift accepted: %v", scenario, err)
			}
			if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "PRIVATE_NEW_DEFAULT") {
				t.Fatalf("snapshot failure exposed private data: %v", err)
			}
			if !reflect.DeepEqual(changed, snapshotTree(t, root)) {
				t.Fatal("snapshot rejection altered concurrent edits")
			}
		})
	}
}

func TestSelectionInputsSnapshotRechecksWorkspaceMembership(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	root := filepath.Join(parent, "app")
	writeModule(t, root, "example.com/app")
	writeModule(t, filepath.Join(parent, "added"), "example.com/added")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	work := filepath.Join(parent, "go.work")
	writeFile(t, work, "go 1.26\nuse ./app\n")
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), applicationresolve.Options{Start: root, Environment: goEnvironment(map[string]string{"GOWORK": work, "GOPROXY": "off"})})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, work, "go 1.26\nuse (\n./app\n./added\n)\n")
	if err := inputs.ValidateSnapshot(t.Context()); !errors.Is(err, applicationresolve.ErrConcurrentChange) {
		t.Fatalf("changed module membership accepted: %v", err)
	}
}

func TestSelectionInputsSnapshotIgnoresUnselectedDocumentChanges(t *testing.T) {
	t.Parallel()
	root := writeResourceConsumerProject(t, "direct")
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), selectionOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "plystra.unselected.yaml"), "PRIVATE_UNSELECTED_INVALID: [\n")
	writeFile(t, filepath.Join(root, "base/plystra.yaml"), "invalid-inert-config: PRIVATE_UNSELECTED\n")
	if err := inputs.ValidateSnapshot(t.Context()); err != nil {
		t.Fatalf("unselected configuration invalidated selected snapshot: %v", err)
	}
}
