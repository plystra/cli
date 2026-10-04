package applicationresolve_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulemutation"
)

func TestSelectionPostwriteAcceptsExactSelectedPostimage(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"default", "environment", "replacement", "explicit-root"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root := writeResourceConsumerProject(t, "transitive")
			options, path := selectionOptions(root), "plystra.yaml"
			switch mode {
			case "environment":
				options.EnvironmentName, path = "test", "plystra.test.yaml"
			case "replacement":
				options.ConfigurationPath, path = "deploy/app.yaml", "deploy/app.yaml"
			case "explicit-root":
				options.ConfigurationPath = path
			}
			writeFile(t, filepath.Join(root, path), "{}\n")
			inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			original := inputs.SelectedSnapshot().Data()
			expected := []byte("# private selected postimage\n{}\n")
			err = atomicfs.WriteFiles(root, []atomicfs.Write{{Path: path, Data: expected, Mode: 0o644, ExpectedData: original}}, func(string) error {
				tidy, err := inputs.CaptureTidySnapshot(t.Context())
				if err != nil {
					return err
				}
				before := snapshotTree(t, root)
				if err := inputs.ValidatePostwriteSnapshot(t.Context(), expected, tidy); err != nil {
					return err
				}
				if !reflect.DeepEqual(before, snapshotTree(t, root)) {
					t.Fatal("postwrite validation mutated input")
				}
				if !bytes.Equal(inputs.SelectedSnapshot().Data(), original) {
					t.Fatal("postwrite check reset original snapshot")
				}
				assertSelectionStale(t, root, inputs.ValidateSnapshot(t.Context()))
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSelectionPostwriteRejectsOriginalCleanupEvidenceDrift(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"selected", "root", "template", "module", "dependency-module", "required-interface", "optional-interface", "resource-parameter", "provider-schema", "provider-default", "provider-signature", "contract", "legacy-exposure", "selected-directory"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			root := writeResourceConsumerProject(t, "transitive")
			writeResourceProvider(t, root)
			writePlugin(t, root, "legacy", "id: example.legacy\nprovides: [legacy.run/v1]\n")
			writeFile(t, filepath.Join(root, "plystra.yaml"), "template: example.com/resource-template\n")
			writeFile(t, filepath.Join(root, "deploy/app.yaml"), "{}\n")
			options := selectionOptions(root)
			options.ConfigurationPath = "deploy/app.yaml"
			inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			expected := []byte("# changed selection\n{}\n")
			writeFile(t, filepath.Join(root, options.ConfigurationPath), string(expected))
			tidy, err := inputs.CaptureTidySnapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			path, old, changed := "", "", ""
			switch scenario {
			case "selected":
				path, old, changed = options.ConfigurationPath, "{}", "{http: {address: ':3000'}}"
			case "root":
				path, old, changed = "plystra.yaml", "template:", "# PRIVATE_CONCURRENT\ntemplate:"
			case "template":
				path, old, changed = "base/plystra.yaml", "{}", "{} # PRIVATE_CONCURRENT"
			case "module":
				path, old, changed = "go.mod", "go 1.26", "go 1.26 // PRIVATE_CONCURRENT"
			case "dependency-module":
				path, old, changed = "base/go.mod", "go 1.26", "go 1.26 // PRIVATE_CONCURRENT"
			case "required-interface":
				path, old, changed = "entry/service.go", "worker api.Interface", "worker api.Interface, sibling api.Interface"
			case "optional-interface":
				path, old, changed = "entry/service.go", "worker api.Interface", "worker plystra.Optional[api.Interface]"
				replaceSelectionFile(t, root, path, "\"context\"", "\"context\"\nplystra \"github.com/plystra/kernel\"")
			case "resource-parameter":
				path, old, changed = "consumer/service.go", "primary database.Resource", "changed database.Resource"
			case "provider-schema":
				path, old, changed = "provider/provider.go", "Count int", "Count uint8"
			case "provider-default":
				path, old, changed = "provider/provider.go", "yaml:\"label\"", "yaml:\"label\" plystra-default:\"PRIVATE_CONCURRENT\""
			case "provider-signature":
				path, old, changed = "provider/provider.go", "New(c Config)", "New(c Config, upstream database.Resource)"
				replaceSelectionFile(t, root, path, "package provider", "package provider\nimport database \"example.com/resource-consumer/database\"")
			case "contract":
				path, old, changed = "database/resource.go", "Health() error", "Health() error; Changed() error"
			case "legacy-exposure":
				path, old, changed = "legacy/plugin.yaml", "legacy.run", "legacy.changed"
			case "selected-directory":
				if err := os.Rename(filepath.Join(root, "deploy"), filepath.Join(root, "original-deploy")); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, options.ConfigurationPath), string(expected))
			}
			if path != "" {
				replaceSelectionFile(t, root, path, old, changed)
			}
			before := snapshotTree(t, root)
			assertSelectionStale(t, root, inputs.ValidatePostwriteSnapshot(t.Context(), expected, tidy))
			if !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("rejection rewrote concurrent input")
			}
			if scenario == "required-interface" || scenario == "optional-interface" {
				// Both constructors are dormant in the final document. Final-state
				// validation alone cannot prove the old ownership cleanup was valid.
				fresh, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
				if err != nil {
					t.Fatal(err)
				}
				candidate, err := fresh.ComposeCandidate(expected)
				if err != nil {
					t.Fatal(err)
				}
				if err := fresh.ValidateCandidate(candidate); err != nil {
					t.Fatalf("final candidate should remain valid: %v", err)
				}
			}
		})
	}
}

func TestSelectionPostwriteTidyGeneratedImportsAndRollback(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"valid", "new-runtime-requirement", "declaration-during-validation", "new-module-during-validation"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			root := writeResourceConsumerProject(t, "transitive")
			replaceSelectionFile(t, root, "go.mod", "github.com/plystra/kernel v0.0.0", "github.com/plystra/kernel v0.0.0 // indirect")
			replaceSelectionFile(t, root, "go.mod", "module example.com/resource-consumer", "module \"example.com/resource-consumer\"")
			replaceSelectionFile(t, root, "go.mod", "=> ./kernel", "=> \"./kernel\"")
			writeModule(t, filepath.Join(root, "runtime"), "example.com/runtime")
			writeFile(t, filepath.Join(root, "runtime/runtime.go"), "package runtime\n")
			replaceSelectionFile(t, root, "go.mod", "go 1.26", "go 1.26\nreplace example.com/runtime => ./runtime")
			options := selectionOptions(root)
			inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			originalMod := inputs.ModuleMetadata()[0].Snapshot().Data()
			expected := []byte("# selection postimage\n{}\n")
			writeFile(t, filepath.Join(root, "plystra.yaml"), string(expected))
			writeFile(t, filepath.Join(root, "generated/runtime.go"), "package generated\nimport _ \"example.com/runtime\"\n")
			requirement, err := applicationgenerate.NewModuleRequirement("github.com/plystra/kernel", "v0.0.0")
			if err != nil {
				t.Fatal(err)
			}
			requirements := []applicationgenerate.ModuleRequirement{requirement}
			if scenario == "new-runtime-requirement" {
				runtime, err := applicationgenerate.NewModuleRequirement("example.com/runtime", "v1.0.0")
				if err != nil {
					t.Fatal(err)
				}
				requirements = append(requirements, runtime)
			}
			err = modulemutation.Tidy(t.Context(), root, "", options.Environment, func(mutate applicationgenerate.ModuleMutation) error {
				return mutate(t.Context(), root, requirements, func() error {
					tidy, err := inputs.CaptureTidySnapshot(t.Context())
					if err != nil {
						return err
					}
					if _, exists := inputs.Dependencies().ByPath("example.com/runtime"); exists {
						t.Fatal("new ordinary module was already present before Tidy")
					}
					selected, exists, err := modulemutation.FindRequirement(root, "example.com/runtime")
					if err != nil || !exists || selected.Version() == "" {
						t.Fatalf("generated import not selected: %v, %v", exists, err)
					}
					if scenario == "new-runtime-requirement" && selected.Version() != "v1.0.0" {
						t.Fatalf("runtime requirement selected at %s", selected.Version())
					}
					if strings.Contains(string(readSelectionFile(t, root, "go.mod")), "// indirect") {
						t.Fatal("runtime requirement was not promoted to direct")
					}
					switch scenario {
					case "declaration-during-validation":
						replaceSelectionFile(t, root, "entry/service.go", "worker api.Interface", "worker api.Interface, sibling api.Interface")
					case "new-module-during-validation":
						replaceSelectionFile(t, root, "runtime/go.mod", "go 1.26", "go 1.26 // PRIVATE_CONCURRENT")
					}
					before := snapshotTree(t, root)
					err = inputs.ValidatePostwriteSnapshot(t.Context(), expected, tidy)
					if !reflect.DeepEqual(before, snapshotTree(t, root)) {
						t.Fatal("postwrite validation mutated input")
					}
					return err
				})
			})
			if scenario == "valid" || scenario == "new-runtime-requirement" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				assertSelectionStale(t, root, err)
				if !bytes.Equal(originalMod, readSelectionFile(t, root, "go.mod")) {
					t.Fatal("postwrite failure escaped Tidy module rollback")
				}
			}
		})
	}
}

func TestSelectionTidyRejectsDependencyChanges(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"required-version", "removed-requirement", "replacement", "go-version", "toolchain", "exclude", "godebug", "dependency-metadata", "new-project", "demotion"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			root := writeResourceConsumerProject(t, "transitive")
			writeModule(t, filepath.Join(root, "added"), "example.com/added")
			writeFile(t, filepath.Join(root, "added/plystra.yaml"), "{}\n")
			replaceSelectionFile(t, root, "go.mod", "go 1.26", "go 1.26\nreplace example.com/added => ./added")
			inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), selectionOptions(root))
			if err != nil {
				t.Fatal(err)
			}
			path, old, changed := "go.mod", "go 1.26", ""
			switch scenario {
			case "required-version":
				old, changed = "v1.0.0", "v1.0.1"
			case "removed-requirement":
				old, changed = "example.com/resource-template v1.0.0", ""
			case "replacement":
				old, changed = "=> ./base", "=> ./added"
			case "go-version":
				changed = "go 1.26.1"
			case "toolchain":
				changed = "go 1.26\ntoolchain go1.26.5"
			case "exclude":
				changed = "go 1.26\nexclude example.com/resource-template v0.9.0"
			case "godebug":
				changed = "go 1.26\ngodebug panicnil=1"
			case "dependency-metadata":
				path, changed = "base/go.mod", "go 1.26 // PRIVATE_CONCURRENT"
			case "new-project":
				changed = "go 1.26\nrequire example.com/added v1.0.0"
			case "demotion":
				old, changed = "example.com/resource-template v1.0.0", "example.com/resource-template v1.0.0 // indirect"
			}
			replaceSelectionFile(t, root, path, old, changed)
			before := snapshotTree(t, root)
			_, err = inputs.CaptureTidySnapshot(t.Context())
			assertSelectionStale(t, root, err)
			if !errors.Is(err, moduledependency.ErrConcurrentChange) {
				t.Fatalf("missing module concurrent-change classification: %v", err)
			}
			if !reflect.DeepEqual(before, snapshotTree(t, root)) {
				t.Fatal("capture changed module metadata")
			}
		})
	}
}

func TestSelectionTidyAcceptsMaterializedTransitiveRequirement(t *testing.T) {
	t.Parallel()
	root := writeResourceConsumerProject(t, "transitive")
	writeModule(t, filepath.Join(root, "leaf"), "example.com/leaf")
	replaceSelectionFile(t, root, "base/go.mod", "go 1.26", "go 1.26\nrequire example.com/leaf v1.0.0")
	replaceSelectionFile(t, root, "go.mod", "go 1.26", "go 1.26\nreplace example.com/leaf => ./leaf")
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), selectionOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	leaf, exists := inputs.Dependencies().ByPath("example.com/leaf")
	if !exists || leaf.Direct() {
		t.Fatal("fixture did not capture a transitive dependency")
	}
	replaceSelectionFile(t, root, "go.mod", "go 1.26", "go 1.26\nrequire example.com/leaf v1.0.0 // indirect")
	tidy, err := inputs.CaptureTidySnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := inputs.ValidatePostwriteSnapshot(t.Context(), inputs.SelectedSnapshot().Data(), tidy); err != nil {
		t.Fatal(err)
	}
}

func TestSelectionTidyAcceptsEquivalentDirectiveSpelling(t *testing.T) {
	t.Parallel()
	root := writeResourceConsumerProject(t, "transitive")
	replaceSelectionFile(t, root, "go.mod", "go 1.26", "go 1.26\nexclude (\n example.com/z v1.0.0\n example.com/a v1.0.0\n)\n")
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), selectionOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	replaceSelectionFile(t, root, "go.mod", "go 1.26", "go 1.26.0")
	replaceSelectionFile(t, root, "go.mod", "module example.com/resource-consumer", "module \"example.com/resource-consumer\"")
	replaceSelectionFile(t, root, "go.mod", "=> ./base", "=> \"./base\"")
	replaceSelectionFile(t, root, "go.mod", "exclude (\n example.com/z v1.0.0\n example.com/a v1.0.0\n)", "exclude example.com/a v1.0.0\nexclude example.com/z v1.0.0")
	replaceSelectionFile(t, root, "go.mod", "replace github.com/plystra/kernel => ./kernel\nreplace example.com/resource-template => \"./base\"", "replace (\nexample.com/resource-template => \"./base\"\ngithub.com/plystra/kernel => ./kernel\n)")
	tidy, err := inputs.CaptureTidySnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := inputs.ValidatePostwriteSnapshot(t.Context(), inputs.SelectedSnapshot().Data(), tidy); err != nil {
		t.Fatal(err)
	}
	assertSelectionStale(t, root, inputs.ValidateSnapshot(t.Context()))
}

func TestSelectionPostwriteRejectsWorkspaceDrift(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"explicit-edit", "explicit-membership", "automatic-edit", "automatic-nearer", "created-after-discovery"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			root := filepath.Join(parent, "app")
			writeModule(t, root, "example.com/app")
			writeModule(t, filepath.Join(parent, "added"), "example.com/added")
			writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			work := filepath.Join(parent, "go.work")
			if scenario != "created-after-discovery" {
				writeFile(t, work, "go 1.26\nuse ./app\n")
			}
			environment := goEnvironment(map[string]string{"GOWORK": "", "GOPROXY": "off"})
			if strings.HasPrefix(scenario, "explicit-") {
				environment = goEnvironment(map[string]string{"GOWORK": work, "GOPROXY": "off"})
			}
			inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), applicationresolve.Options{Start: root, Environment: environment})
			if err != nil {
				t.Fatal(err)
			}
			tidy, err := inputs.CaptureTidySnapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "explicit-edit", "automatic-edit":
				writeFile(t, work, "go 1.26\nuse ./app\n// PRIVATE_CONCURRENT\n")
			case "explicit-membership":
				writeFile(t, work, "go 1.26\nuse (\n./app\n./added\n)\n")
			case "automatic-nearer", "created-after-discovery":
				writeFile(t, filepath.Join(root, "go.work"), "go 1.26\nuse .\n")
			}
			before := snapshotTree(t, parent)
			assertSelectionStale(t, parent, inputs.ValidatePostwriteSnapshot(t.Context(), inputs.SelectedSnapshot().Data(), tidy))
			_, err = inputs.CaptureTidySnapshot(t.Context())
			assertSelectionStale(t, parent, err)
			if !reflect.DeepEqual(before, snapshotTree(t, parent)) {
				t.Fatal("workspace rejection changed input")
			}
		})
	}
}

func TestSelectionPostwriteKeepsBodyAndGeneratedBytesOutsideCleanupProof(t *testing.T) {
	t.Parallel()
	root := writeResourceConsumerProject(t, "transitive")
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), selectionOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	tidy, err := inputs.CaptureTidySnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	replaceSelectionFile(t, root, "entry/service.go", "PRIVATE_CONSTRUCTOR_ENTRY", "PRIVATE_BODY_CHANGE")
	writeFile(t, filepath.Join(root, "generated/output.go"), "private generated bytes are not cleanup evidence")
	writeFile(t, filepath.Join(root, "plystra.unselected.yaml"), "PRIVATE_UNSELECTED: [\n")
	if err := inputs.ValidatePostwriteSnapshot(t.Context(), inputs.SelectedSnapshot().Data(), tidy); err != nil {
		t.Fatal(err)
	}
}

func TestSelectionPostwriteSnapshotGuardsAndPrivacy(t *testing.T) {
	t.Parallel()
	root := writeResourceConsumerProject(t, "transitive")
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), selectionOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	tidy, err := inputs.CaptureTidySnapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{fmt.Sprintf("%+v %#v %s", tidy, tidy, tidy), tidy.LogValue().String()} {
		if strings.Contains(value, root) || strings.Contains(value, "resource-consumer") || !strings.Contains(value, "private-selection-tidy-snapshot") {
			t.Fatalf("tidy snapshot not redacted: %s", value)
		}
	}
	var output bytes.Buffer
	slog.New(slog.NewJSONHandler(&output, nil)).Info("snapshot", "tidy", tidy)
	if strings.Contains(output.String(), root) || strings.Contains(output.String(), "resource-consumer") {
		t.Fatal("structured logging exposed tidy evidence")
	}
	selected := inputs.SelectedSnapshot().Data()
	assertSelectionStale(t, root, inputs.ValidatePostwriteSnapshot(t.Context(), selected, applicationresolve.SelectionTidySnapshot{}))
	otherRoot := writeResourceConsumerProject(t, "transitive")
	other, err := applicationresolve.DiscoverSelectionInputs(t.Context(), selectionOptions(otherRoot))
	if err != nil {
		t.Fatal(err)
	}
	assertSelectionStale(t, otherRoot, other.ValidatePostwriteSnapshot(t.Context(), selected, tidy))
	var empty applicationresolve.SelectionInputs
	if err := empty.ValidatePostwriteSnapshot(t.Context(), selected, tidy); !errors.Is(err, applicationresolve.ErrResolve) {
		t.Fatalf("empty inputs accepted: %v", err)
	}
	if _, err := empty.CaptureTidySnapshot(t.Context()); !errors.Is(err, applicationresolve.ErrResolve) {
		t.Fatalf("empty inputs captured: %v", err)
	}
	//lint:ignore SA1012 Verify the public boundary rejects a nil context.
	if err := inputs.ValidatePostwriteSnapshot(nil, selected, tidy); !errors.Is(err, applicationresolve.ErrResolve) {
		t.Fatalf("nil context accepted: %v", err)
	}
	//lint:ignore SA1012 Verify the public boundary rejects a nil context.
	if _, err := inputs.CaptureTidySnapshot(nil); !errors.Is(err, applicationresolve.ErrResolve) {
		t.Fatalf("nil context captured: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := inputs.ValidatePostwriteSnapshot(ctx, selected, tidy); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context accepted: %v", err)
	}
	if _, err := inputs.CaptureTidySnapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context captured: %v", err)
	}
}

func assertSelectionStale(t testing.TB, root string, err error) {
	t.Helper()
	if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, applicationresolve.ErrConcurrentChange) {
		t.Fatalf("expected stale selection evidence: %v", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "PRIVATE_CONCURRENT") {
		t.Fatalf("snapshot failure exposed private input: %v", err)
	}
	var source *applicationresolve.ManifestSourceError
	if !errors.As(err, &source) || source.ModulePath() == "" || source.SourcePath() == "" || source.SourceKind() == "" {
		t.Fatalf("snapshot error lost source evidence: %v", err)
	}
}

func readSelectionFile(t testing.TB, root, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func replaceSelectionFile(t testing.TB, root, path, old, changed string) {
	t.Helper()
	data := string(readSelectionFile(t, root, path))
	if !strings.Contains(data, old) {
		t.Fatalf("%s does not contain fixture target %q", path, old)
	}
	writeFile(t, filepath.Join(root, path), strings.Replace(data, old, changed, 1))
}
