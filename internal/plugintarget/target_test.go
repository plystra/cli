package plugintarget_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/plugintarget"
)

func TestInferHonorsExplicitDirectoryAndIDBeforeLocation(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "acme.app.account")
	writePlugin(t, root, "profile", "acme.app.profile")
	start := filepath.Join(root, "account", "nested")
	if err := os.Mkdir(start, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	for _, explicit := range []string{"profile", "acme.app.profile"} {
		target, err := plugintarget.Infer(plugintarget.Options{Start: start, Explicit: explicit})
		if err != nil {
			t.Fatalf("Infer(%q): %v", explicit, err)
		}
		assertTarget(t, target, root, "profile", "acme.app.profile")
	}
}

func TestInferUsesNearestEnclosingPlugin(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "acme.app.account")
	writePlugin(t, root, "profile", "acme.app.profile")
	start := filepath.Join(root, "account", "capabilities", "account.register", "v1")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	target, err := plugintarget.Infer(plugintarget.Options{Start: start})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	assertTarget(t, target, root, "account", "acme.app.account")
}

func TestInferUsesOnlyLocalPluginAtModuleRoot(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "account", "acme.app.account")
	target, err := plugintarget.Infer(plugintarget.Options{Start: root})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	assertTarget(t, target, root, "account", "acme.app.account")
}

func TestInferHandlesInteractiveAndNonInteractiveAmbiguity(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	writePlugin(t, root, "profile", "acme.app.profile")
	writePlugin(t, root, "account", "acme.app.account")
	if _, err := plugintarget.Infer(plugintarget.Options{Start: root}); !errors.Is(err, plugintarget.ErrInfer) || !errors.Is(err, plugintarget.ErrAmbiguous) || !strings.Contains(err.Error(), "acme.app.account (account), acme.app.profile (profile)") {
		t.Fatalf("non-interactive Infer error = %v", err)
	}

	var output bytes.Buffer
	target, err := plugintarget.Infer(plugintarget.Options{
		Start:  root,
		Select: plugintarget.Prompt(strings.NewReader("2\n"), &output),
	})
	if err != nil {
		t.Fatalf("interactive Infer: %v", err)
	}
	assertTarget(t, target, root, "profile", "acme.app.profile")
	wantOutput := "Multiple local plugins:\n  1. acme.app.account (account)\n  2. acme.app.profile (profile)\nSelect plugin [1-2]: "
	if output.String() != wantOutput {
		t.Fatalf("prompt output = %q, want %q", output.String(), wantOutput)
	}
}

func TestInferRejectsMissingAndInvalidSelections(t *testing.T) {
	t.Parallel()

	root := createModule(t)
	if _, err := plugintarget.Infer(plugintarget.Options{Start: root}); !errors.Is(err, plugintarget.ErrNotFound) {
		t.Fatalf("empty Infer error = %v, want ErrNotFound", err)
	}
	writePlugin(t, root, "account", "acme.app.account")
	writePlugin(t, root, "profile", "acme.app.profile")
	if _, err := plugintarget.Infer(plugintarget.Options{Start: root, Explicit: "missing"}); !errors.Is(err, plugintarget.ErrNotFound) {
		t.Fatalf("explicit Infer error = %v, want ErrNotFound", err)
	}
	if _, err := plugintarget.Infer(plugintarget.Options{Start: root, Select: func([]plugintarget.Target) (int, error) { return 2, nil }}); !errors.Is(err, plugintarget.ErrSelection) {
		t.Fatalf("out-of-range Infer error = %v, want ErrSelection", err)
	}
	wantErr := errors.New("terminal unavailable")
	if _, err := plugintarget.Infer(plugintarget.Options{Start: root, Select: func([]plugintarget.Target) (int, error) { return -1, wantErr }}); !errors.Is(err, plugintarget.ErrSelection) || !strings.Contains(err.Error(), wantErr.Error()) {
		t.Fatalf("selector Infer error = %v", err)
	}
}

func TestPromptRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	candidates := []plugintarget.Target{}
	if _, err := plugintarget.Prompt(strings.NewReader("1\n"), &bytes.Buffer{})(candidates); !errors.Is(err, plugintarget.ErrPrompt) {
		t.Fatalf("empty Prompt error = %v", err)
	}
	root := createModule(t)
	writePlugin(t, root, "account", "acme.app.account")
	writePlugin(t, root, "profile", "acme.app.profile")
	captured := make([]plugintarget.Target, 0, 2)
	_, err := plugintarget.Infer(plugintarget.Options{
		Start: root,
		Select: func(options []plugintarget.Target) (int, error) {
			captured = append(captured, options...)
			return plugintarget.Prompt(strings.NewReader("three\n"), &bytes.Buffer{})(options)
		},
	})
	if !errors.Is(err, plugintarget.ErrSelection) || !strings.Contains(err.Error(), "choice must be") {
		t.Fatalf("invalid Prompt Infer error = %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("captured candidates = %#v", captured)
	}
}

func createModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/app\n\ngo 1.26\n")
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	return canonical
}

func writePlugin(t *testing.T, root, name, id string) {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatalf("Mkdir(%s): %v", name, err)
	}
	writeFile(t, filepath.Join(directory, "plugin.yaml"), "id: "+id+"\n")
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func assertTarget(t *testing.T, target plugintarget.Target, moduleRoot, directory, id string) {
	t.Helper()
	if target.ID() != id || target.Directory() != directory || target.ModuleRoot() != moduleRoot || target.Path() != filepath.Join(moduleRoot, directory) {
		t.Fatalf("target = ID %q, directory %q, module %q, path %q", target.ID(), target.Directory(), target.ModuleRoot(), target.Path())
	}
	wantManifest := []byte("id: " + id + "\n")
	if got := target.ManifestData(); !bytes.Equal(got, wantManifest) {
		t.Fatalf("ManifestData = %q, want %q", got, wantManifest)
	}
	manifest := target.ManifestData()
	manifest[0] = 'x'
	if target.ManifestData()[0] != 'i' {
		t.Fatal("ManifestData exposed mutable target storage")
	}
}
