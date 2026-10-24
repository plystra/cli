package applicationresolve_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/modulemutation"
)

func TestDependencyOwnershipSnapshotPreservesWorkspaceAndRelativeReplacements(t *testing.T) {
	t.Parallel()
	for _, workspace := range []bool{false, true} {
		for _, known := range []bool{false, true} {
			t.Run(fmt.Sprintf("workspace=%t/known=%t", workspace, known), func(t *testing.T) {
				t.Parallel()
				root := writeResourceConsumerProject(t, "optional-absent")
				options := selectionOptions(root)
				work := filepath.Join(root, "go.work")
				if workspace {
					writeFile(t, work, "go 1.26\nuse (\n.\n./base\n./kernel\n)\n")
					options.Environment = goEnvironment(map[string]string{"GOWORK": work, "GOPROXY": "off"})
				}
				if !known {
					writeFile(t, filepath.Join(root, "consumer/service.go"), "package consumer\n//plystra:implements app.resource/v1\nfunc broken(\n")
				}
				snapshot, err := applicationresolve.CaptureDependencyOwnership(t.Context(), options)
				if err != nil {
					t.Fatal(err)
				}
				if (snapshot.Previous().Module().Path() != "") != known {
					t.Fatal("old discovery certainty was lost")
				}
				mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, "go.mod"), strings.Replace(string(mod), "v1.0.0", "v1.1.0", 1))
				if err := snapshot.ValidateSnapshot(t.Context()); !errors.Is(err, applicationresolve.ErrConcurrentChange) {
					t.Fatalf("pre-change guard accepted changed metadata: %v", err)
				}
				if err := snapshot.ValidateAuthoredSnapshot(t.Context(), nil); err != nil {
					t.Fatal(err)
				}
				expected := []byte("# intentional selected postimage\n{}\n")
				if err := atomicfs.WriteFiles(root, []atomicfs.Write{{Path: "plystra.yaml", Data: expected, ExpectedData: []byte("{}\n")}}, func(root string) error {
					if err := snapshot.ValidateAuthoredSnapshot(t.Context(), expected); err != nil {
						return err
					}
					if !known {
						return nil
					}
					current, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options)
					if err != nil {
						return err
					}
					return modulemutation.Tidy(t.Context(), root, "", options.Environment, func(mutate applicationgenerate.ModuleMutation) error {
						return mutate(t.Context(), root, nil, func() error {
							tidy, err := current.CaptureTidySnapshot(t.Context())
							if err != nil {
								return err
							}
							if err := snapshot.ValidateAuthoredSnapshot(t.Context(), expected); err != nil {
								return err
							}
							return current.ValidatePostwriteSnapshot(t.Context(), expected, tidy)
						})
					})
				}); err != nil {
					t.Fatal(err)
				}
				if workspace {
					writeFile(t, work, "go 1.26\nuse (\n.\n./base\n./kernel\n)\n// PRIVATE_WORKSPACE_EDIT\n")
					if err := snapshot.ValidateAuthoredSnapshot(t.Context(), expected); !errors.Is(err, applicationresolve.ErrConcurrentChange) {
						t.Fatalf("workspace edit accepted: %v", err)
					}
				}
			})
		}
	}
}

func TestDependencyOwnershipSnapshotBindsImportedTypeSources(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"imported-source", "imported-membership", "new-declaration", "unknown-yaml"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			root := writeResourceConsumerProject(t, "optional-absent")
			options := selectionOptions(root)
			if scenario == "unknown-yaml" {
				writeFile(t, filepath.Join(root, "consumer/service.go"), "package consumer\n//plystra:implements app.resource/v1\nfunc broken(\n")
			}
			snapshot, err := applicationresolve.CaptureDependencyOwnership(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "imported-source":
				writeFile(t, filepath.Join(root, "kernel/optional.go"), "package plystra\ntype Optional[T any] struct{}\n// PRIVATE_IMPORTED_SOURCE_EDIT\n")
			case "imported-membership":
				writeFile(t, filepath.Join(root, "kernel/added.go"), "package plystra\n")
			case "new-declaration":
				writeFile(t, filepath.Join(root, "new/resource.go"), "package resource\n//plystra:resource extra.cache/v1\ntype Resource interface{ Ping() }\n")
			case "unknown-yaml":
				writeFile(t, filepath.Join(root, "plystra.yaml"), "# PRIVATE_CONCURRENT_YAML\n{}\n")
			}
			if err := snapshot.ValidateAuthoredSnapshot(t.Context(), nil); !errors.Is(err, applicationresolve.ErrConcurrentChange) {
				t.Fatalf("old type or YAML evidence changed: %v", err)
			}
			for _, private := range []string{root, "PRIVATE_"} {
				if strings.Contains(fmt.Sprintf("%+v %#v", snapshot, snapshot), private) {
					t.Fatal("snapshot formatting disclosed private input")
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if err := snapshot.ValidateAuthoredSnapshot(ctx, nil); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}

func TestDependencyOwnershipSnapshotStreamsLargeGeneratedTypeSupport(t *testing.T) {
	t.Parallel()
	root := writeResourceConsumerProject(t, "transitive")
	writeFile(t, filepath.Join(root, "generated/support/types.go"), "package support\ntype Result = error\n/*"+strings.Repeat("x", (16<<20)+1)+"*/\n")
	writeFile(t, filepath.Join(root, "database/resource.go"), "package database\nimport \"example.com/resource-consumer/generated/support\"\n//plystra:resource storage.database/v1\ntype Resource interface{ Health() support.Result }\n")
	snapshot, err := applicationresolve.CaptureDependencyOwnership(t.Context(), selectionOptions(root))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Previous().Module().Path() == "" {
		t.Fatal("large ordinary type support erased valid old declarations")
	}
	if err := snapshot.ValidateAuthoredSnapshot(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
}
