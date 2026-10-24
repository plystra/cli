package projectstage_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/privatefile"
	"github.com/plystra/cli/internal/projectstage"
)

func TestInputsRejectExternalGoEnvironmentDrift(t *testing.T) {
	for _, change := range []string{"bytes", "identity", "removed", "created", "created-parent", "parent-identity"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			root, parent := fixture(t)
			settings := filepath.Join(t.TempDir(), "settings")
			name := "env"
			if change == "created-parent" {
				name = "missing/env"
			}
			path := filepath.Join(settings, filepath.FromSlash(name))
			if change != "created" && change != "created-parent" {
				write(t, settings, name, "GOFLAGS=-tags=private_before\n")
			}
			stage := capturedStage(t, root, parent, "GOWORK=off", "GOENV="+path)
			inputs := stage.Inputs()
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			original, statErr := os.Stat(path)
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal(statErr)
			}
			switch change {
			case "identity", "removed":
				if err := os.Rename(path, path+"-old"); err != nil {
					t.Fatal(err)
				}
				if change == "identity" {
					write(t, settings, name, "GOFLAGS=-tags=private_before\n")
				}
			case "parent-identity":
				if err := os.Rename(settings, settings+"-old"); err != nil {
					t.Fatal(err)
				}
				write(t, settings, name, "GOFLAGS=-tags=private_before\n")
			default:
				write(t, settings, name, "GOFLAGS=-tags=private_change\n")
			}
			if change == "bytes" || change == "identity" {
				if err := os.Chtimes(path, original.ModTime(), original.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			if err := inputs.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("%s external GOENV drift accepted: %v", change, err)
			}
			if _, err := inputs.ProjectFile(t.Context(), path); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("external file exposed as a Project file: %v", err)
			}
		})
	}
}

func TestStageUsesSelectedExternalGoEnvironment(t *testing.T) {
	for _, mode := range []string{"absolute", "relative-project", "relative-external", "absent"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root, parent := fixture(t)
			settings := t.TempDir()
			if mode == "relative-project" {
				settings = filepath.Join(root, "settings")
			}
			path := filepath.Join(settings, "env")
			if mode != "absent" {
				write(t, settings, "env", "GOFLAGS=-tags=private_before\n")
			}
			selected := path
			if strings.HasPrefix(mode, "relative-") {
				var err error
				selected, err = filepath.Rel(root, path)
				if err != nil {
					t.Fatal(err)
				}
			}
			write(t, root, "before.go", "//go:build private_before\n\npackage app\nconst Selected = 1\n")
			write(t, root, "after.go", "//go:build private_change\n\npackage app\nconst Selected = 2\n")
			stage := capturedStage(t, root, parent, "GOWORK=off", "GOENV="+selected)
			checkSelection := func() {
				t.Helper()
				output, err := gocommand.Output(t.Context(), gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}, "list", "-f", "{{.GoFiles}}", ".")
				if err != nil || strings.Contains(string(output), "before.go") != (mode != "absent") || strings.Contains(string(output), "after.go") {
					t.Fatalf("selected GOENV did not control candidate sources: %q, %v", output, err)
				}
			}
			checkSelection()
			if err := stage.Recheck(t.Context()); err != nil {
				t.Fatal(err)
			}
			write(t, settings, "env", "GOFLAGS=-tags=private_change\n")
			checkSelection()
			if err := stage.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatalf("live stage accepted changed source selector: %v", err)
			}
		})
	}
}

func TestStageProtectsItsPrivateGoEnvironmentCopy(t *testing.T) {
	root, parent := fixture(t)
	settings := t.TempDir()
	original := filepath.Join(settings, "env")
	write(t, settings, "env", "GOFLAGS=-tags=PRIVATE_GOENV_COPY\n")
	stage := capturedStage(t, root, parent, "GOWORK=off", "GOENV="+original)
	inputs := stage.Inputs()
	var copied string
	for _, entry := range stage.Environment() {
		if name, value, ok := strings.Cut(entry, "="); ok && strings.EqualFold(name, "GOENV") {
			copied = value
		}
	}
	if copied == "" || copied == original || !filepath.IsAbs(copied) {
		t.Fatal("candidate did not use an isolated GOENV copy")
	}
	file, err := os.Open(copied)
	if err != nil {
		t.Fatal(err)
	}
	if err := privatefile.Check(file); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Dir(copied), filepath.Base(copied), "GOFLAGS=-tags=PRIVATE_TAMPERED_COPY\n")
	if err := stage.Recheck(t.Context()); !errors.Is(err, projectstage.ErrConcurrentChange) {
		t.Fatalf("changed private GOENV copy accepted: %v", err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inputs.Recheck(t.Context()); err != nil {
		t.Fatalf("retained original inputs depend on the removed copy: %v", err)
	}
}

func TestCreateRejectsGoEnvironmentChangeAfterSelection(t *testing.T) {
	root, parent := fixture(t)
	settings := t.TempDir()
	path := filepath.Join(settings, "env")
	write(t, settings, "env", "GOFLAGS=-tags=private_before\n")
	stage, err := projectstage.Create(t.Context(), projectstage.Options{
		Start: root, TemporaryParent: parent, Environment: environment("GOWORK=off", "GOENV="+path),
		Observe: func(event projectstage.Event) {
			if event == projectstage.GoEnvironmentStarted {
				write(t, settings, "env", "GOFLAGS=-tags=private_after\n")
			}
		},
	})
	if stage != nil {
		t.Cleanup(func() { _ = stage.Close() })
	}
	if !errors.Is(err, projectstage.ErrConcurrentChange) || stage != nil {
		t.Fatalf("selection changed before capture: %v, %v", stage, err)
	}
	if err != nil && (strings.Contains(err.Error(), settings) || strings.Contains(err.Error(), "private_")) {
		t.Fatal("private Go environment leaked in failure")
	}
}

func TestCreateRejectsUnsafeGoEnvironmentInputs(t *testing.T) {
	for _, kind := range []string{"directory", "symbolic-file", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			root, parent := fixture(t)
			settings := t.TempDir()
			path := filepath.Join(settings, "env")
			switch kind {
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symbolic-file":
				write(t, settings, "original", "GOFLAGS=-tags=PRIVATE_GOENV\n")
				if err := os.Symlink(filepath.Join(settings, "original"), path); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				write(t, settings, "env", strings.Repeat("GOPRIVATE=PRIVATE_VALUE\n", 50_000))
			}
			stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, TemporaryParent: parent, Environment: environment("GOWORK=off", "GOENV="+path)})
			if stage != nil {
				t.Cleanup(func() { _ = stage.Close() })
			}
			if !errors.Is(err, projectstage.ErrStage) || stage != nil || strings.Contains(err.Error(), settings) || strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatalf("unsafe GOENV input %s was not rejected privately", kind)
			}
			if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
				t.Fatal("unsafe GOENV input retained temporary output")
			}
		})
	}
}

func TestStageCapturesEnvironmentBeforeObservation(t *testing.T) {
	root, parent := fixture(t)
	settings := t.TempDir()
	path := filepath.Join(settings, "env")
	write(t, settings, "env", "GOFLAGS=-tags=PRIVATE_CALLER_CHANGE\n")
	env := environment("GOWORK=off", "GOENV=off")
	stage, err := projectstage.Create(t.Context(), projectstage.Options{
		Start: root, TemporaryParent: parent, Environment: env,
		Observe: func(event projectstage.Event) {
			if event == projectstage.GoEnvironmentStarted {
				for index, entry := range env {
					if strings.HasPrefix(entry, "GOENV=") {
						env[index] = "GOENV=" + path
					}
				}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	for _, entry := range stage.Environment() {
		if strings.HasPrefix(entry, "GOENV=") && entry != "GOENV=off" {
			t.Fatal("caller changed the environment after Go selection")
		}
	}
}
