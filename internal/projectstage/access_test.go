//go:build windows || linux || darwin

package projectstage_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/privatefile"
	"github.com/plystra/cli/internal/projectstage"
)

func TestInputsRejectNativeAccessDrift(t *testing.T) {
	for _, kind := range []string{"authored-file", "authored-directory", "project-root", "metadata-file", "metadata-parent", "source-file", "source-root", "goenv-file", "goenv-parent"} {
		t.Run(kind, func(t *testing.T) {
			root, parent := fixture(t)
			write(t, root, "assets/value", "PRIVATE_AUTHORED_INPUT")
			external := t.TempDir()
			write(t, external, "value", "PRIVATE_EXTERNAL_INPUT")
			settings := []string{"GOWORK=off", "GOENV=off"}
			if kind == "goenv-file" || kind == "goenv-parent" {
				settings[1] = "GOENV=" + filepath.Join(external, "value")
				write(t, external, "value", "GOPRIVATE=example.com/private\n")
			}
			stage := capturedStage(t, root, parent, settings...)
			path := filepath.Join(root, "assets", "value")
			switch kind {
			case "authored-directory":
				path = filepath.Dir(path)
			case "project-root":
				path = root
			case "metadata-file", "metadata-parent":
				path = filepath.Join(external, "value")
				if err := stage.RetainMetadata(t.Context(), []projectstage.MetadataInput{{Path: path}}); err != nil {
					t.Fatal(err)
				}
				if kind == "metadata-parent" {
					path = external
				}
			case "source-file", "source-root":
				if err := stage.RetainSourceTrees(t.Context(), []string{external}); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(external, "value")
				if kind == "source-root" {
					path = external
				}
			case "goenv-file":
				path = filepath.Join(external, "value")
			case "goenv-parent":
				path = external
			}
			inputs := stage.Inputs()
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			changeNativeAccess(t, path)
			after, err := os.Stat(path)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
				t.Fatalf("ACL-only fixture changed ordinary metadata: %v", err)
			}
			if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("%s native access drift accepted: %v", kind, err)
			}
			if err := stage.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("live stage accepted %s native access drift: %v", kind, err)
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("cleanup lost %s native access evidence: %v", kind, err)
			}
			if kind == "authored-file" || kind == "authored-directory" || kind == "project-root" {
				if _, err := inputs.ProjectFile(t.Context(), "assets/value"); !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("ProjectFile accepted native access drift: %v", err)
				}
			}
		})
	}
}

func TestStageRejectsCandidateNativeAccessDrift(t *testing.T) {
	for _, kind := range []string{"temporary-root", "candidate-root", "frozen-goenv"} {
		t.Run(kind, func(t *testing.T) {
			root, parent := fixture(t)
			settings := filepath.Join(t.TempDir(), "env")
			write(t, filepath.Dir(settings), filepath.Base(settings), "GOPRIVATE=example.com/private\n")
			stage := capturedStage(t, root, parent, "GOWORK=off", "GOENV="+settings)
			path := stage.Root()
			if kind == "temporary-root" {
				path = filepath.Dir(path)
			} else if kind == "frozen-goenv" {
				path = filepath.Join(filepath.Dir(path), "go.env")
			}
			changeNativeAccess(t, path)
			if err := stage.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("%s native permission change accepted: %v", kind, err)
			}
			if err := stage.Inputs().Recheck(t.Context()); err != nil {
				t.Fatal("candidate permissions changed original evidence", err)
			}
		})
	}
}

func TestNativeAccessEvidenceDoesNotRefreshEarlierInputs(t *testing.T) {
	root, parent := fixture(t)
	stage := capturedStage(t, root, parent)
	earlier := stage.Inputs()
	external := t.TempDir()
	write(t, external, "version.info", "PRIVATE_METADATA")
	path := filepath.Join(external, "version.info")
	files := []projectstage.MetadataInput{{Path: path}}
	if err := stage.RetainMetadata(t.Context(), files); err != nil {
		t.Fatal(err)
	}
	retained := stage.Inputs()
	changeNativeAccess(t, path)
	if err := earlier.Recheck(t.Context()); err != nil {
		t.Fatal("later permission evidence changed an earlier snapshot", err)
	}
	if err := stage.RetainMetadata(t.Context(), files); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("recapture refreshed original native permissions: %v", err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := retained.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("earlier metadata permission evidence was lost: %v", err)
	}
}

func TestInputsAcceptExistingNativeAccess(t *testing.T) {
	root, parent := fixture(t)
	changeNativeAccess(t, filepath.Join(root, "app.go"))
	stage := capturedStage(t, root, parent)
	if err := stage.Recheck(t.Context()); err != nil {
		t.Fatal("existing source ACL rejected", err)
	}
	file, err := os.Open(filepath.Join(stage.Root(), "app.go"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := privatefile.Check(file); err != nil {
		t.Fatal("source ACL was copied into private candidate permissions", err)
	}
}
