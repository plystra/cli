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

func TestSourceTreesRequireIndependentAuthentication(t *testing.T) {
	for _, mode := range []string{"unchanged", "wrong-checksum", "identity", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root, parent := fixture(t)
			stage := capturedStage(t, root, parent)
			source := t.TempDir()
			write(t, source, "value.go", "package dependency\nconst Value = 1\n")
			expected := sourceInput(t, source)
			before := stage.Inputs()
			if err := stage.RetainSourceTrees(t.Context(), []string{source, source}); err != nil {
				t.Fatal(err)
			}
			pending := stage.Inputs()
			if !errors.Is(stage.CheckSources(t.Context()), projectstage.ErrSourceIntegrity) {
				t.Fatal("an unauthenticated tree was accepted")
			}
			path := filepath.Join(source, "value.go")
			if mode == "identity" {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path, filepath.Join(t.TempDir(), "old-value.go")); err != nil {
					t.Fatal(err)
				}
				write(t, source, "value.go", "package dependency\nconst Value = 1\n")
				if err := errors.Join(os.Chmod(path, info.Mode()), os.Chtimes(path, info.ModTime(), info.ModTime())); err != nil {
					t.Fatal(err)
				}
			} else if mode == "bytes" {
				write(t, source, "value.go", "package dependency\nconst Value = 2\n")
			} else if mode == "wrong-checksum" {
				expected.Checksum = "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
			}
			err := stage.RetainSources(t.Context(), []projectstage.Source{expected})
			switch mode {
			case "identity", "bytes":
				if !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("authentication refreshed the original tree: %v", err)
				}
			case "wrong-checksum":
				if !errors.Is(err, projectstage.ErrSourceIntegrity) || !errors.Is(stage.CheckSources(t.Context()), projectstage.ErrSourceIntegrity) {
					t.Fatalf("wrong checksum authenticated a pending tree: %v", err)
				}
			case "unchanged":
				if err != nil || stage.CheckSources(t.Context()) != nil {
					t.Fatalf("unchanged original tree did not authenticate: %v", err)
				}
				if err := stage.RetainSourceTrees(t.Context(), []string{source}); err != nil || stage.CheckSources(t.Context()) != nil {
					t.Fatal("raw recapture discarded existing authentication")
				}
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := before.Recheck(t.Context()); err != nil {
				t.Fatal("later evidence changed an earlier Inputs value")
			}
			if mode != "identity" && mode != "bytes" {
				if err := pending.Recheck(t.Context()); err != nil {
					t.Fatal("authentication changed the pending snapshot")
				}
				write(t, source, "value.go", "package dependency\nconst Value = 3\n")
			}
			if !errors.Is(inputs.Recheck(context.Background()), projectstage.ErrConcurrentChange) ||
				!errors.Is(pending.Recheck(context.Background()), projectstage.ErrConcurrentChange) {
				t.Fatal("retained tree evidence was lost after cleanup")
			}
		})
	}
}

func TestSourceTreesShareCaptureSafety(t *testing.T) {
	for _, mode := range []string{"relative", "missing", "candidate", "symlink-file", "oversized", "nil-context", "closed"} {
		t.Run(mode, func(t *testing.T) {
			root, parent := fixture(t)
			stage := capturedStage(t, root, parent)
			source := t.TempDir()
			ctx := t.Context()
			switch mode {
			case "relative":
				source = "relative"
			case "missing":
				source = filepath.Join(source, "missing")
			case "candidate":
				source = stage.Root()
			case "symlink-file":
				target := filepath.Join(t.TempDir(), "private")
				write(t, filepath.Dir(target), filepath.Base(target), "private")
				if err := os.Symlink(target, filepath.Join(source, "link")); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				write(t, source, "large", "")
				if err := os.Truncate(filepath.Join(source, "large"), projectstage.MaximumFileBytes+1); err != nil {
					t.Fatal(err)
				}
			case "nil-context":
				ctx = nil
			case "closed":
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := stage.RetainSourceTrees(ctx, []string{source}); err == nil {
				t.Fatalf("accepted unsafe %s tree", mode)
			}
		})
	}
}

func TestSourceTreesShareTheAuthenticatedRootBudget(t *testing.T) {
	root, parent := fixture(t)
	stage := capturedStage(t, root, parent, "GOWORK=off", "GOENV=off")
	base := t.TempDir()
	var directories []string
	for index := range projectstage.MaximumRoots {
		path := filepath.Join(base, fmt.Sprint(index))
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		directories = append(directories, path)
	}
	if err := stage.RetainSourceTrees(t.Context(), directories[:projectstage.MaximumRoots-2]); err != nil {
		t.Fatal(err)
	}
	if err := stage.RetainSources(t.Context(), []projectstage.Source{sourceInput(t, directories[0]), sourceInput(t, directories[projectstage.MaximumRoots-2])}); err != nil {
		t.Fatal(err)
	}
	if err := stage.RetainSourceTrees(t.Context(), directories[projectstage.MaximumRoots-1:]); !errors.Is(err, projectstage.ErrStage) {
		t.Fatalf("pending and authenticated trees used separate root budgets: %v", err)
	}
	if err := stage.Recheck(t.Context()); err != nil {
		t.Fatal("rejected overflow invalidated earlier evidence")
	}
}
