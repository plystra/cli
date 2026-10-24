package projectstage_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/projectstage"
)

func TestRetainedSourcesRejectDriftAfterCleanup(t *testing.T) {
	for _, change := range []string{"bytes", "identity", "removed", "added", "directory"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			root, parent := fixture(t)
			source, unrelated := t.TempDir(), t.TempDir()
			write(t, source, "value.go", "package dependency\nconst Value = 1\n")
			stage := capturedStage(t, root, parent)
			earlier := stage.Inputs()
			if err := stage.RetainSources(t.Context(), []string{source, source}); err != nil {
				t.Fatal(err)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			write(t, unrelated, "value.go", "unrelated cache entry")
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(source, "value.go")
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "bytes":
				write(t, source, "value.go", "package dependency\nconst Value = 2\n")
			case "identity", "removed":
				if err := os.Rename(path, path+"-old"); err != nil {
					t.Fatal(err)
				}
				if change == "identity" {
					write(t, source, "value.go", "package dependency\nconst Value = 1\n")
				}
			case "added":
				write(t, source, "new.go", "package dependency\n")
			case "directory":
				if err := os.Rename(source, source+"-old"); err != nil {
					t.Fatal(err)
				}
				write(t, source, "value.go", "package dependency\nconst Value = 1\n")
			}
			if change == "bytes" || change == "identity" {
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("retained %s source drift accepted: %v", change, err)
			}
			if err := earlier.Recheck(t.Context()); err != nil {
				t.Fatal("retaining sources changed an earlier immutable input value")
			}
			if _, err := inputs.ProjectFile(t.Context(), filepath.Join(source, "value.go")); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatal("external sources exposed as Project file images")
			}
		})
	}
}

func TestRetainSourcesKeepsEarlierVersions(t *testing.T) {
	root, parent := fixture(t)
	old, next := t.TempDir(), t.TempDir()
	write(t, old, "value.go", "package dependency\nconst Value = 1\n")
	write(t, next, "value.go", "package dependency\nconst Value = 2\n")
	stage := capturedStage(t, root, parent)
	for _, source := range []string{old, next, old} {
		if err := stage.RetainSources(t.Context(), []string{source}); err != nil {
			t.Fatal(err)
		}
	}
	write(t, old, "value.go", "package dependency\nconst Value = 3\n")
	if err := stage.RetainSources(t.Context(), []string{next}); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("a later capture discarded or refreshed earlier evidence: %v", err)
	}
}

func TestRetainSourcesRejectsUnsafeInputs(t *testing.T) {
	for _, mode := range []string{"relative", "missing", "candidate", "ancestor", "symbolic-file", "oversized", "cancelled", "closed", "nil-context"} {
		t.Run(mode, func(t *testing.T) {
			root, parent := fixture(t)
			stage := capturedStage(t, root, parent)
			source := t.TempDir()
			var ctx context.Context = t.Context()
			switch mode {
			case "relative":
				source = "relative"
			case "missing":
				source = filepath.Join(source, "missing")
			case "candidate":
				source = stage.Root()
			case "ancestor":
				source = parent
			case "symbolic-file":
				target := t.TempDir()
				write(t, target, "external", "private")
				if err := os.Symlink(filepath.Join(target, "external"), filepath.Join(source, "link")); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				write(t, source, "too-large", "")
				if err := os.Truncate(filepath.Join(source, "too-large"), projectstage.MaximumFileBytes+1); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			case "closed":
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
			case "nil-context":
				ctx = nil
			}
			if err := stage.RetainSources(ctx, []string{source}); err == nil {
				t.Fatalf("accepted %s source capture", mode)
			}
		})
	}
}

func TestRetainSourcesSharesRootBoundAcrossCalls(t *testing.T) {
	root, parent := fixture(t)
	stage := capturedStage(t, root, parent, "GOWORK=off", "GOENV=off")
	base := t.TempDir()
	var roots []string
	for index := range projectstage.MaximumRoots {
		path := filepath.Join(base, fmt.Sprint(index))
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		roots = append(roots, path)
	}
	if err := stage.RetainSources(t.Context(), roots[:projectstage.MaximumRoots-2]); err != nil {
		t.Fatal(err)
	}
	if err := stage.RetainSources(t.Context(), roots[projectstage.MaximumRoots-2:projectstage.MaximumRoots-1]); err != nil {
		t.Fatal(err)
	}
	if err := stage.RetainSources(t.Context(), roots[projectstage.MaximumRoots-1:]); !errors.Is(err, projectstage.ErrStage) {
		t.Fatalf("separate calls reset the cumulative root bound: %v", err)
	}
	if err := stage.Recheck(t.Context()); err != nil {
		t.Fatal("a rejected capture invalidated earlier evidence")
	}
}
