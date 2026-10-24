package projectstage_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
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
			input := sourceInput(t, source)
			if err := stage.RetainSources(t.Context(), []projectstage.Source{input, input}); err != nil {
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
		if err := stage.RetainSources(t.Context(), []projectstage.Source{sourceInput(t, source)}); err != nil {
			t.Fatal(err)
		}
	}
	write(t, old, "value.go", "package dependency\nconst Value = 3\n")
	if err := stage.RetainSources(t.Context(), []projectstage.Source{sourceInput(t, next)}); !errors.Is(err, projectstage.ErrConcurrentChange) {
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
			input := projectstage.Source{Directory: source, Module: module.Version{Path: "example.com/dependency", Version: "v1.0.0"},
				Checksum: "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="}
			if err := stage.RetainSources(ctx, []projectstage.Source{input}); err == nil {
				t.Fatalf("accepted %s source capture", mode)
			}
		})
	}
}

func TestRetainSourcesSharesRootBoundAcrossCalls(t *testing.T) {
	root, parent := fixture(t)
	stage := capturedStage(t, root, parent, "GOWORK=off", "GOENV=off")
	base := t.TempDir()
	var roots []projectstage.Source
	for index := range projectstage.MaximumRoots {
		path := filepath.Join(base, fmt.Sprint(index))
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		roots = append(roots, sourceInput(t, path))
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

func sourceInput(t *testing.T, root string) projectstage.Source {
	t.Helper()
	version := module.Version{Path: "example.com/dependency", Version: "v1.0.0"}
	checksum, err := dirhash.HashDir(root, version.String(), dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	return projectstage.Source{Directory: root, Module: version, Checksum: checksum}
}

func TestRetainSourcesIncludesRelatedMetadata(t *testing.T) {
	for _, mode := range []string{"source-identity", "metadata-identity", "prior-source-drift", "prior-metadata-drift", "bad-archive", "bad-metadata"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root, parent := fixture(t)
			stage := capturedStage(t, root, parent)
			earlier := stage.Inputs()
			source, metadata := t.TempDir(), t.TempDir()
			write(t, source, "value.go", "package dependency\nconst Value = 1\n")
			input := sourceInput(t, source)
			write(t, metadata, "v1.0.0.ziphash", input.Checksum)
			path := filepath.Join(metadata, "v1.0.0.ziphash")
			file := projectstage.MetadataInput{Path: path, Expected: []byte(input.Checksum)}
			if mode == "bad-archive" {
				input.Checksum = "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
			} else if mode == "bad-metadata" {
				file.Expected = []byte("conflicting checksum")
			}
			err := stage.RetainSources(t.Context(), []projectstage.Source{input, input}, file, file)
			if mode == "bad-metadata" {
				if !errors.Is(err, projectstage.ErrStage) || !errors.Is(err, projectstage.ErrSourceIntegrity) {
					t.Fatalf("conflicting metadata was accepted: %v", err)
				}
				return
			}
			if mode == "bad-archive" {
				if !errors.Is(err, projectstage.ErrSourceIntegrity) {
					t.Fatalf("conflicting archive was accepted: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			inputs := stage.Inputs()
			if strings.Contains(mode, "source-") {
				path = filepath.Join(source, "value.go")
			}
			if strings.HasPrefix(mode, "prior-") {
				write(t, filepath.Dir(path), filepath.Base(path), "changed earlier evidence\n")
				next := t.TempDir()
				write(t, next, "new.ziphash", "new metadata")
				if err := stage.RetainSources(t.Context(), nil, projectstage.MetadataInput{Path: filepath.Join(next, "new.ziphash")}); !errors.Is(err, projectstage.ErrConcurrentChange) {
					t.Fatalf("combined capture refreshed earlier inputs: %v", err)
				}
				return
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil || !os.SameFile(info, info) {
				t.Fatal("missing input identity", err)
			}
			if err := os.Rename(path, filepath.Join(t.TempDir(), "prior")); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Dir(path), filepath.Base(path), string(data))
			if err := os.Chmod(path, info.Mode()); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			current, err := os.Stat(path)
			if err != nil || os.SameFile(info, current) {
				t.Fatal("fixture did not replace input identity")
			}
			if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("combined capture lost evidence after cleanup: %v", err)
			}
			if err := earlier.Recheck(t.Context()); err != nil {
				t.Fatal("combined capture changed an earlier immutable input value", err)
			}
		})
	}
}

func TestRetainedSourcesValidateCompleteArchiveInventory(t *testing.T) {
	for _, mode := range []string{"ordinary", "legacy", "vcs"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root, parent := fixture(t)
			source := t.TempDir()
			for _, name := range []string{"a.go", "a-1.go", "a/value.go", "nested/z.go", "z.go"} {
				write(t, source, name, "package dependency\n")
			}
			if mode != "legacy" {
				write(t, source, "go.mod", "module example.com/Upper/Dependency/v2\n")
			}
			if mode == "vcs" {
				for _, name := range []string{".git/config", ".hg/history", ".svn/entries"} {
					write(t, source, name, "archive content\n")
				}
			}
			input := projectstage.Source{Directory: source, Module: module.Version{Path: "example.com/Upper/Dependency/v2", Version: "v2.1.0"}}
			var err error
			input.Checksum, err = dirhash.HashDir(source, input.Module.String(), dirhash.Hash1)
			if err != nil {
				t.Fatal(err)
			}
			stage := capturedStage(t, root, parent)
			if err := stage.RetainSources(t.Context(), []projectstage.Source{input}); err != nil {
				t.Fatalf("Go Hash1 inventory rejected: %v", err)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			if mode == "legacy" {
				if _, err := os.Stat(filepath.Join(source, "go.mod")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("legacy source was modified to synthesize go.mod")
				}
			}
			write(t, source, ".git/config", "injected content\n")
			if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("VCS path drift escaped complete source inventory: %v", err)
			}
		})
	}
}

func TestRetainSourcesRejectsInvalidChecksumClaims(t *testing.T) {
	for _, mode := range []string{"changed-bytes", "injected-file", "missing", "malformed", "noncanonical", "algorithm", "path", "version", "duplicate", "earlier", "ancestor"} {
		t.Run(mode, func(t *testing.T) {
			root, parent := fixture(t)
			source := t.TempDir()
			write(t, source, "value.go", "package dependency\nconst Value = 1\n")
			input := sourceInput(t, source)
			stage := capturedStage(t, root, parent)
			switch mode {
			case "changed-bytes":
				path := filepath.Join(source, "value.go")
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				write(t, source, "value.go", "package dependency\nconst Value = 2\n")
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "injected-file":
				write(t, source, ".hg/extra", "not in the archive")
			case "missing":
				input.Checksum = ""
			case "malformed":
				input.Checksum = "h1:invalid"
			case "noncanonical":
				input.Checksum = "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAB="
			case "algorithm":
				input.Checksum = strings.Replace(input.Checksum, "h1:", "h2:", 1)
			case "path":
				input.Module.Path = "example.com/different"
			case "version":
				input.Module.Version = "v1.1.0"
			case "earlier":
				if err := stage.RetainSources(t.Context(), []projectstage.Source{input}); err != nil {
					t.Fatal(err)
				}
				input.Module.Version = "v1.1.0"
			case "ancestor":
				ancestor := t.TempDir()
				source = filepath.Join(ancestor, "nested")
				write(t, source, "value.go", "package dependency\n")
				if err := stage.RetainSources(t.Context(), []projectstage.Source{sourceInput(t, ancestor)}); err != nil {
					t.Fatal(err)
				}
				input = sourceInput(t, source)
				input.Module.Version = "v1.1.0"
			}
			claims := []projectstage.Source{input}
			if mode == "duplicate" {
				changed := input
				changed.Module.Version = "v1.1.0"
				claims = append(claims, changed)
			}
			earlier := stage.Inputs()
			if err := stage.RetainSources(t.Context(), claims); !errors.Is(err, projectstage.ErrSourceIntegrity) {
				t.Fatalf("invalid %s source accepted: %v", mode, err)
			}
			if err := earlier.Recheck(t.Context()); err != nil {
				t.Fatal("a rejected source claim changed earlier inputs")
			}
		})
	}
}
