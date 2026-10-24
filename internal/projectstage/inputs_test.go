package projectstage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/plystra/cli/internal/projectstage"
	"go.yaml.in/yaml/v3"
)

func capturedStage(t *testing.T, root, parent string, settings ...string) *projectstage.Stage {
	t.Helper()
	if len(settings) == 0 {
		settings = []string{"GOWORK=off"}
	}
	stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: parent, Environment: environment(settings...)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := stage.Close(); err != nil {
			t.Error(err)
		}
	})
	return stage
}

func TestInputsOutliveStageAndReturnImmutableOriginalFiles(t *testing.T) {
	root, parent := fixture(t)
	write(t, root, "assets/private.txt", "PRIVATE_AUTHORED_INPUT")
	write(t, root, "empty", "")
	stage := capturedStage(t, root, parent)
	inputs := stage.Inputs()
	write(t, stage.Root(), "assets/private.txt", "CANDIDATE_ONLY")
	write(t, stage.Root(), "new/path.txt", "CANDIDATE_CREATION")
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stage.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("closed Stage recheck = %v", err)
	}
	for _, snapshot := range []projectstage.Inputs{inputs, stage.Inputs()} {
		if err := snapshot.Recheck(t.Context()); err != nil {
			t.Fatal(err)
		}
		image, err := snapshot.ProjectFile(t.Context(), "assets/private.txt")
		info, statErr := os.Stat(filepath.Join(root, "assets", "private.txt"))
		if err != nil || statErr != nil || !image.Exists() || string(image.Data()) != "PRIVATE_AUTHORED_INPUT" || image.Mode() != info.Mode() {
			t.Fatalf("original file = %v, %v, %v", image, err, statErr)
		}
		copy := image.Data()
		copy[0] = '!'
		if string(image.Data()) != "PRIVATE_AUTHORED_INPUT" {
			t.Fatal("Data exposed mutable captured bytes")
		}
		for _, name := range []string{"new/path.txt", "go.sum"} {
			missing, err := snapshot.ProjectFile(t.Context(), name)
			if err != nil || missing.Exists() || missing.Data() != nil || missing.Mode() != 0 {
				t.Fatalf("absent %s = %v, %v", name, missing, err)
			}
		}
		empty, err := snapshot.ProjectFile(t.Context(), "empty")
		if err != nil || !empty.Exists() || len(empty.Data()) != 0 {
			t.Fatalf("empty file lost existence: %v, %v", empty, err)
		}
	}
	// Independent copies can be rechecked and read concurrently after cleanup.
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 4 {
				image, err := inputs.ProjectFile(t.Context(), "assets/private.txt")
				if err != nil || string(image.Data()) != "PRIVATE_AUTHORED_INPUT" {
					t.Error("concurrent original file read failed")
					return
				}
				image.Data()[0] = '!'
				if err := inputs.Recheck(t.Context()); err != nil {
					t.Error(err)
				}
			}
		})
	}
	group.Wait()
}

func TestInputsProjectFileInsideOutermostWorkspaceTree(t *testing.T) {
	outer, parent := t.TempDir(), t.TempDir()
	root := filepath.Join(outer, "nested", "app")
	write(t, outer, "go.mod", "module example.com/outer\n\ngo 1.26\n")
	write(t, outer, "go.work", "go 1.26\nuse (\n .\n ./nested/app\n)\n")
	write(t, outer, "private.txt", "OUTER_ONLY")
	write(t, root, "go.mod", "module example.com/app\n\ngo 1.26\n")
	write(t, root, "plystra.yaml", "{}\n")
	write(t, root, "private.txt", "NESTED_PROJECT")
	stage := capturedStage(t, root, parent, "GOWORK="+filepath.Join(outer, "go.work"))
	inputs := stage.Inputs()
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	image, err := inputs.ProjectFile(t.Context(), "private.txt")
	if err != nil || string(image.Data()) != "NESTED_PROJECT" {
		t.Fatalf("nested project preimage = %v, %v", image, err)
	}
	if _, err := inputs.ProjectFile(t.Context(), "../private.txt"); err == nil {
		t.Fatal("nested Project exposed its outer workspace")
	}
	write(t, outer, "private.txt", "OUTER_DRIFT")
	if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("outermost tree drift accepted: %v", err)
	}
}

func TestInputsRejectFileDriftAfterClose(t *testing.T) {
	for _, change := range []string{"bytes", "identity", "mode", "removed", "parent-identity", "symbolic-file", "symbolic-parent", "project-identity", "new-file", "new-parent"} {
		t.Run(change, func(t *testing.T) {
			root, parent := fixture(t)
			write(t, root, "assets/private.txt", "PRIVATE_ORIGINAL")
			stage := capturedStage(t, root, parent)
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			name := "assets/private.txt"
			path := filepath.Join(root, filepath.FromSlash(name))
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "bytes":
				write(t, root, name, "PRIVATE_MODIFIED")
				if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "identity", "removed", "symbolic-file":
				if err := os.Rename(path, path+"-old"); err != nil {
					t.Fatal(err)
				}
				if change == "identity" {
					write(t, root, name, "PRIVATE_ORIGINAL")
					if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
						t.Fatal(err)
					}
				} else if change == "symbolic-file" {
					if err := os.Symlink(path+"-old", path); err != nil {
						t.Fatal(err)
					}
				}
			case "mode":
				if err := os.Chmod(path, 0o400); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
			case "parent-identity", "symbolic-parent":
				directory := filepath.Dir(path)
				if err := os.Rename(directory, directory+"-old"); err != nil {
					t.Fatal(err)
				}
				if change == "symbolic-parent" {
					if err := os.Symlink(directory+"-old", directory); err != nil {
						t.Fatal(err)
					}
				} else {
					write(t, root, name, "PRIVATE_ORIGINAL")
				}
			case "project-identity":
				if err := os.Rename(root, root+"-old"); err != nil {
					t.Fatal(err)
				}
				write(t, root, name, "PRIVATE_ORIGINAL")
			case "new-file", "new-parent":
				name = "created.txt"
				if change == "new-parent" {
					name = "new/created.txt"
				}
				missing, err := inputs.ProjectFile(t.Context(), name)
				if err != nil || missing.Exists() {
					t.Fatalf("original absence = %v, %v", missing, err)
				}
				write(t, root, name, "CONCURRENT_CREATION")
			}
			if image, err := inputs.ProjectFile(t.Context(), name); !errors.Is(err, projectstage.ErrConcurrentChange) || image.Exists() || image.Data() != nil {
				t.Fatalf("%s file drift = %v, %v", change, image, err)
			}
			if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("%s original drift accepted: %v", change, err)
			}
		})
	}
}

func TestInputsRetainAllOriginalStagingEvidence(t *testing.T) {
	for _, change := range []string{"dependency-bytes", "dependency-membership", "workspace", "workspace-sum", "workspace-vendor-creation", "workspace-vendor-bytes", "alias"} {
		t.Run(change, func(t *testing.T) {
			root, parent := fixture(t)
			base := filepath.Dir(root)
			for _, name := range []string{"dependency", "other"} {
				write(t, filepath.Join(base, name), "go.mod", "module example.com/dependency\n\ngo 1.26\n")
				write(t, filepath.Join(base, name), "value.go", "package dependency\n")
			}
			alias := filepath.Join(base, "alias")
			if err := os.Symlink(filepath.Join(base, "dependency"), alias); err != nil {
				t.Fatal(err)
			}
			write(t, root, "go.mod", "module example.com/app\n\ngo 1.26\nreplace example.com/dependency => ../alias\n")
			write(t, base, "go.work", "go 1.26\nuse ./app\n")
			if change == "workspace-vendor-bytes" {
				write(t, base, "vendor/modules.txt", "## workspace\n")
			}
			stage := capturedStage(t, root, parent, "GOWORK="+filepath.Join(base, "go.work"))
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "dependency-bytes":
				write(t, filepath.Join(base, "dependency"), "value.go", "package dependency\nconst Edited=1\n")
			case "dependency-membership":
				write(t, filepath.Join(base, "dependency"), "new.go", "package dependency\n")
			case "workspace":
				write(t, base, "go.work", "go 1.26\nuse ./app\n// concurrent edit\n")
			case "workspace-sum":
				write(t, base, "go.work.sum", "concurrent checksum\n")
			case "workspace-vendor-creation":
				write(t, base, "vendor/modules.txt", "## workspace\n")
			case "workspace-vendor-bytes":
				write(t, base, "vendor/modules.txt", "## workspace\n# concurrent edit\n")
			case "alias":
				if err := os.Remove(alias); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(base, "other"), alias); err != nil {
					t.Fatal(err)
				}
			}
			if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("%s drift accepted after close: %v", change, err)
			}
		})
	}
}

func TestInputsRejectInvalidPathsZeroAndCancellation(t *testing.T) {
	root, parent := fixture(t)
	write(t, root, "assets/value", "private")
	write(t, root, ".git/config", "PRIVATE_VCS")
	inputs := capturedStage(t, root, parent).Inputs()
	for _, path := range []string{"", ".", "..", "../go.mod", "/go.mod", "a/../go.mod", "assets//value", `assets\value`, "C:/go.mod", "go.mod:stream", "bad\x00path", "assets", "go.mod/child", ".git/config", "nested/.hg/file", ".svn"} {
		if image, err := inputs.ProjectFile(t.Context(), path); err == nil || image.Exists() {
			t.Fatalf("unsafe/non-file path %q = %v, %v", path, image, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{nil, ctx} {
		if err := inputs.Recheck(ctx); !errors.Is(err, projectstage.ErrConcurrentChange) {
			t.Fatalf("invalid context recheck = %v", err)
		}
		if image, err := inputs.ProjectFile(ctx, "go.mod"); !errors.Is(err, projectstage.ErrConcurrentChange) || image.Exists() {
			t.Fatalf("invalid context file = %v, %v", image, err)
		}
	}
	var nilStage *projectstage.Stage
	for _, zero := range []projectstage.Inputs{{}, nilStage.Inputs(), new(projectstage.Stage).Inputs()} {
		if err := zero.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
			t.Fatalf("zero recheck = %v", err)
		}
		if _, err := zero.ProjectFile(t.Context(), "go.mod"); err == nil {
			t.Fatal("zero Inputs yielded a file")
		}
	}
	var absent projectstage.FileImage
	if absent.Exists() || absent.Data() != nil || absent.Mode() != 0 {
		t.Fatal("zero FileImage is not absent")
	}
}

func TestInputsAndFileImagesArePrivate(t *testing.T) {
	root, parent := fixture(t)
	const secret = "PRIVATE_CAPTURE_VALUE"
	write(t, root, "private.txt", secret)
	stage := capturedStage(t, root, parent)
	inputs := stage.Inputs()
	image, err := inputs.ProjectFile(t.Context(), "private.txt")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(secret))
	for _, value := range []any{inputs, &inputs, image, &image, projectstage.Inputs{}, projectstage.FileImage{}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			rendered := fmt.Sprintf(format, value)
			for _, private := range []string{root, stage.Root(), secret, fmt.Sprintf("%x", sum)} {
				if strings.Contains(rendered, private) {
					t.Fatalf("format leaked private input: %s", rendered)
				}
			}
		}
		if data, err := json.Marshal(value); err == nil || bytes.Contains(data, []byte(secret)) {
			t.Fatal("private JSON serialization accepted")
		}
		if data, err := yaml.Marshal(value); err == nil || bytes.Contains(data, []byte(secret)) {
			t.Fatal("private YAML serialization accepted")
		}
		text := value.(interface{ MarshalText() ([]byte, error) })
		if data, err := text.MarshalText(); err == nil || data != nil {
			t.Fatal("private text serialization accepted")
		}
		var output bytes.Buffer
		slog.New(slog.NewJSONHandler(&output, nil)).Info("capture", "value", value)
		if strings.Contains(output.String(), secret) || strings.Contains(output.String(), root) || strings.Contains(output.String(), fmt.Sprintf("%x", sum)) {
			t.Fatal("private structured log leaked")
		}
	}
}
