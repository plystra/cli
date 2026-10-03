package projectsmoke_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/projectsmoke"
)

func TestRunBuildsRunsAndCleansGeneratedApplication(t *testing.T) {
	const source = `package main

import (
	"os"
	"path/filepath"
)

func main() {
	root, err := os.Getwd()
	if err != nil || len(os.Args) != 6 || os.Args[1] != "--smoke" || os.Args[2] != "--configuration-root" || !filepath.IsAbs(os.Args[3]) || os.Args[4] != "--runtime-baseline" || os.Args[5] != "dist/runtime-baseline.json" {
		os.Exit(2)
	}
	working, workingErr := os.Stat(root)
	configured, configuredErr := os.Stat(os.Args[3])
	if workingErr != nil || configuredErr != nil || !os.SameFile(working, configured) {
		os.Exit(3)
	}
}
`
	for _, mode := range []string{"direct", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			root := writeSmokeProject(t, source)
			selected := root
			if mode == "symlink" {
				selected = filepath.Join(t.TempDir(), "project")
				if err := os.Symlink(root, selected); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("directory symlink unavailable: %v", err)
					}
					t.Fatal(err)
				}
			}
			var environment []string
			for _, entry := range os.Environ() {
				key, _, _ := strings.Cut(entry, "=")
				if !strings.EqualFold(key, "PWD") {
					environment = append(environment, entry)
				}
			}
			// Force Getwd to use the filesystem instead of an inherited logical path.
			environment = append(environment, "PWD="+t.TempDir())
			if err := projectsmoke.Run(t.Context(), projectsmoke.Options{Root: selected, Environment: environment}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			assertSmokeOutputRemoved(t, root)
		})
	}
}

func TestRunCleansAfterBuildFailure(t *testing.T) {
	root := writeSmokeProject(t, "package main\n\nfunc main( {\n")
	err := projectsmoke.Run(t.Context(), projectsmoke.Options{Root: root})
	if !errors.Is(err, projectsmoke.ErrRun) || !errors.Is(err, projectsmoke.ErrBuild) {
		t.Fatalf("Run error = %v", err)
	}
	assertSmokeOutputRemoved(t, root)
}

func TestRunSuppressesChildOutputAndCleansAfterSmokeFailure(t *testing.T) {
	const secret = "resolved-smoke-secret-must-not-leak"
	root := writeSmokeProject(t, `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stdout, "`+secret+`")
	fmt.Fprintln(os.Stderr, "`+secret+`")
	os.Exit(9)
}
`)
	err := projectsmoke.Run(t.Context(), projectsmoke.Options{Root: root})
	if !errors.Is(err, projectsmoke.ErrRun) || !errors.Is(err, projectsmoke.ErrSmoke) {
		t.Fatalf("Run error = %v", err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), root) {
		t.Fatalf("Run error leaked child output or private root: %v", err)
	}
	assertSmokeOutputRemoved(t, root)
}

func TestRunBoundsSmokeTimeout(t *testing.T) {
	source := `package main

import "time"

func main() {
	for {
		time.Sleep(time.Hour)
	}
}
	`
	root := writeSmokeProject(t, source)
	err := projectsmoke.Run(t.Context(), projectsmoke.Options{Root: root, SmokeTimeout: 200 * time.Millisecond})
	if !errors.Is(err, projectsmoke.ErrSmoke) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v", err)
	}
	assertSmokeOutputRemoved(t, root)
}

func TestRunCancelsRunningSmoke(t *testing.T) {
	root := writeSmokeProject(t, `package main

import (
	"os"
	"time"
)

func main() {
	_ = os.WriteFile(os.Getenv("PLYSTRA_PROJECT_SMOKE_STARTED"), []byte("started"), 0o600)
	for {
		time.Sleep(time.Hour)
	}
}
`)
	started := filepath.Join(t.TempDir(), "started")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		timer := time.NewTimer(30 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ticker.C:
				if _, err := os.Stat(started); err == nil {
					cancel()
					return
				}
			case <-timer.C:
				cancel()
				return
			}
		}
	}()
	err := projectsmoke.Run(ctx, projectsmoke.Options{
		Root:         root,
		Environment:  append(os.Environ(), "PLYSTRA_PROJECT_SMOKE_STARTED="+started),
		SmokeTimeout: 30 * time.Second,
	})
	<-watcherDone
	if !errors.Is(err, projectsmoke.ErrSmoke) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v", err)
	}
	assertSmokeOutputRemoved(t, root)
}

func TestRunRejectsConflictingTemporaryOutputWithoutRemovingIt(t *testing.T) {
	root := writeSmokeProject(t, "package main\n\nfunc main() {}\n")
	temporaryRoot := filepath.Join(root, ".plystra-smoke")
	if err := os.Mkdir(temporaryRoot, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	marker := filepath.Join(temporaryRoot, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	err := projectsmoke.Run(t.Context(), projectsmoke.Options{Root: root})
	if !errors.Is(err, projectsmoke.ErrTemporaryOutput) {
		t.Fatalf("Run error = %v", err)
	}
	if data, readErr := os.ReadFile(marker); readErr != nil || string(data) != "keep" {
		t.Fatalf("existing temporary output = %q, %v", data, readErr)
	}
}

func writeSmokeProject(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	writeSmokeFile(t, filepath.Join(root, "go.mod"), "module example.com/project-smoke\n\ngo 1.26\n")
	writeSmokeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeSmokeFile(t, filepath.Join(root, "generated", "go", "application", "main.go"), source)
	return root
}

func writeSmokeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func assertSmokeOutputRemoved(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, ".plystra-smoke")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary smoke output remains: %v", err)
	}
}
