package applicationresolve_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/modulemutation"
)

func TestSelectionTidyAcceptsExistingWorkspaceRequirement(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"ordinary", "project"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			root := filepath.Join(parent, "app")
			writeModule(t, root, "example.com/app")
			writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			writeModule(t, filepath.Join(parent, "runtime"), "example.com/runtime")
			writeFile(t, filepath.Join(parent, "runtime/runtime.go"), "package runtime\n")
			if kind == "project" {
				writeFile(t, filepath.Join(parent, "runtime/plystra.yaml"), "{}\n")
			}
			replaceSelectionFile(t, root, "go.mod", "go 1.26", "go 1.26\nreplace example.com/runtime => ../runtime")
			work := filepath.Join(parent, "go.work")
			writeFile(t, work, "go 1.26.0\nuse (\n./app\n./runtime\n)\n")
			options := applicationresolve.Options{Start: root, Environment: goEnvironment(map[string]string{"GOWORK": work, "GOPROXY": "off"})}
			inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			member, exists := inputs.Dependencies().ByPath("example.com/runtime")
			if !exists || !member.Workspace() || member.Project() != (kind == "project") || member.Direct() || member.SelectedVersion() != "" {
				t.Fatal("fixture must start with an unrequired workspace member of the expected kind")
			}
			writeFile(t, filepath.Join(root, "generated/runtime.go"), "package generated\nimport _ \"example.com/runtime\"\n")
			err = modulemutation.Tidy(t.Context(), root, "", options.Environment, func(mutate applicationgenerate.ModuleMutation) error {
				return mutate(t.Context(), root, nil, func() error {
					requirement, exists, err := modulemutation.FindRequirement(root, "example.com/runtime")
					if err != nil || !exists || requirement.Version() == "" {
						t.Fatalf("real Tidy did not materialize the workspace requirement: %v, %v", exists, err)
					}
					before := snapshotTree(t, parent)
					tidy, err := inputs.CaptureTidySnapshot(t.Context())
					if err != nil {
						return err
					}
					if err := inputs.ValidatePostwriteSnapshot(t.Context(), inputs.SelectedSnapshot().Data(), tidy); err != nil {
						return err
					}
					if !reflect.DeepEqual(before, snapshotTree(t, parent)) {
						t.Fatal("snapshot validation changed workspace inputs")
					}
					return nil
				})
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSelectionTidyAcceptsRemovalOfImpliedToolchain(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\ngo 1.26.0\ntoolchain go1.26.0\n")
	writeFile(t, filepath.Join(root, "app.go"), "package app\n")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	work := filepath.Join(root, "go.work")
	writeFile(t, work, "go 1.26.0\nuse .\n")
	options := applicationresolve.Options{Start: root, Environment: goEnvironment(map[string]string{"GOWORK": work, "GOPROXY": "off"})}
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	err = modulemutation.Tidy(t.Context(), root, "", options.Environment, func(mutate applicationgenerate.ModuleMutation) error {
		return mutate(t.Context(), root, nil, func() error {
			if strings.Contains(string(readSelectionFile(t, root, "go.mod")), "toolchain") {
				t.Fatal("real Tidy did not remove the implied toolchain directive")
			}
			before := snapshotTree(t, root)
			tidy, err := inputs.CaptureTidySnapshot(t.Context())
			if err != nil {
				return err
			}
			if err := inputs.ValidatePostwriteSnapshot(t.Context(), inputs.SelectedSnapshot().Data(), tidy); err != nil {
				return err
			}
			if !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("snapshot validation changed input")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSelectionTidyWorkspaceRequirementRetainsOriginalEvidence(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"member-metadata", "member-source", "member-becomes-project", "new-member", "new-project-member", "existing-requirement-version"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			root := filepath.Join(parent, "app")
			writeModule(t, root, "example.com/app")
			writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			writeModule(t, filepath.Join(parent, "runtime"), "example.com/runtime")
			writeFile(t, filepath.Join(parent, "runtime/runtime.go"), "package runtime\n")
			replaceSelectionFile(t, root, "go.mod", "go 1.26", "go 1.26\nreplace example.com/runtime => ../runtime")
			if scenario == "existing-requirement-version" {
				replaceSelectionFile(t, root, "go.mod", "go 1.26", "go 1.26\nrequire example.com/runtime v1.0.0")
			}
			work := filepath.Join(parent, "go.work")
			writeFile(t, work, "go 1.26.0\nuse (\n./app\n./runtime\n)\n")
			options := applicationresolve.Options{Start: root, Environment: goEnvironment(map[string]string{"GOWORK": work, "GOPROXY": "off"})}
			inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(root, "generated/runtime.go"), "package generated\nimport _ \"example.com/runtime\"\n")
			err = modulemutation.Tidy(t.Context(), root, "", options.Environment, func(mutate applicationgenerate.ModuleMutation) error {
				return mutate(t.Context(), root, nil, func() error {
					switch scenario {
					case "member-metadata":
						replaceSelectionFile(t, parent, "runtime/go.mod", "go 1.26", "go 1.26 // PRIVATE_CONCURRENT")
					case "member-source":
						if err := os.Rename(filepath.Join(parent, "runtime"), filepath.Join(parent, "old-runtime")); err != nil {
							t.Fatal(err)
						}
						writeModule(t, filepath.Join(parent, "runtime"), "example.com/runtime")
						writeFile(t, filepath.Join(parent, "runtime/runtime.go"), "package runtime\n")
					case "member-becomes-project":
						writeFile(t, filepath.Join(parent, "runtime/plystra.yaml"), "{}\n")
					case "new-member", "new-project-member":
						writeModule(t, filepath.Join(parent, "added"), "example.com/added")
						if scenario == "new-project-member" {
							writeFile(t, filepath.Join(parent, "added/plystra.yaml"), "{}\n")
						}
						replaceSelectionFile(t, parent, "go.work", "./runtime", "./runtime\n./added")
					case "existing-requirement-version":
						replaceSelectionFile(t, root, "go.mod", "v1.0.0", "v1.0.1")
					}
					before := snapshotTree(t, parent)
					_, err := inputs.CaptureTidySnapshot(t.Context())
					assertSelectionStale(t, parent, err)
					if !reflect.DeepEqual(before, snapshotTree(t, parent)) {
						t.Fatal("capture overwrote concurrent changes")
					}
					return err
				})
			})
			assertSelectionStale(t, parent, err)
		})
	}
}

func TestSelectionTidyRejectsMeaningfulToolchainChanges(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct{ name, before, after string }{
		{"remove-patch-selection", "go1.26.1", ""},
		{"remove-default-selection", "default", ""},
		{"change-patch-selection", "go1.26.1", "go1.26.2"},
		{"change-implied-selection", "go1.26.0", "go1.26.1"},
		{"add-implied-selection", "", "go1.26.0"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			base := "module example.com/app\ngo 1.26.0\n"
			original := base
			if scenario.before != "" {
				original += "toolchain " + scenario.before + "\n"
			}
			writeFile(t, filepath.Join(root, "go.mod"), original)
			writeFile(t, filepath.Join(root, "app.go"), "package app\n")
			writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			work := filepath.Join(root, "go.work")
			writeFile(t, work, "go 1.26.0\nuse .\n")
			inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), applicationresolve.Options{
				Start: root, Environment: goEnvironment(map[string]string{"GOWORK": work, "GOPROXY": "off"}),
			})
			if err != nil {
				t.Fatal(err)
			}
			changed := base
			if scenario.after != "" {
				changed += "toolchain " + scenario.after + "\n"
			}
			writeFile(t, filepath.Join(root, "go.mod"), changed)
			before := snapshotTree(t, root)
			_, err = inputs.CaptureTidySnapshot(t.Context())
			assertSelectionStale(t, root, err)
			if !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("rejection changed input")
			}
		})
	}
}
