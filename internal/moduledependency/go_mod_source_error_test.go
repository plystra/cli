package moduledependency_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/moduledependency"
)

func TestDiscoverReportsInvalidApplicationDependencySources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		goMod       string
		wantProblem string
		wantLine    int
		wantColumn  int
	}{
		{
			name:        "self requirement",
			goMod:       "module example.com/app\n\ngo 1.26\n\nrequire example.com/app v1.0.0\n",
			wantProblem: "application module cannot require itself",
			wantLine:    5,
			wantColumn:  1,
		},
		{
			name: "duplicate requirement",
			goMod: "module example.com/app\n\ngo 1.26\n\nrequire (\n" +
				"example.com/dependency v1.0.0\n" +
				"example.com/dependency v1.1.0\n" +
				")\n",
			wantProblem: `duplicate requirement "example.com/dependency"`,
			wantLine:    7,
			wantColumn:  1,
		},
		{
			name:        "invalid requirement",
			goMod:       "module example.com/app\n\ngo 1.26\n\nrequire example.com/dependency@invalid v1.0.0\n",
			wantProblem: "malformed module path",
			wantLine:    5,
			wantColumn:  1,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			writeFile(t, filepath.Join(root, "go.mod"), test.goMod)
			application := locate(t, root)

			_, err := moduledependency.Discover(context.Background(), application, moduledependency.Options{})
			if !errors.Is(err, moduledependency.ErrDiscover) || !errors.Is(err, moduledependency.ErrInvalidGoMod) || !strings.Contains(err.Error(), test.wantProblem) {
				t.Fatalf("Discover error = %v, want ErrDiscover and ErrInvalidGoMod containing %q", err, test.wantProblem)
			}
			var source *moduledependency.GoModSourceError
			if !errors.As(err, &source) || source == nil || source.ModulePath() != "example.com/app" || source.SourcePath() != "go.mod" || source.SourceKind() != "module-dependency" || source.Line() != test.wantLine || source.Column() != test.wantColumn {
				t.Fatalf("Discover source = %#v, %v", source, err)
			}
			if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
				t.Fatalf("Discover exposed current Project root %q: %v", root, err)
			}
		})
	}
}

func TestDiscoverReportsChangedModuleDirectiveSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n")
	application := locate(t, root)
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/changed\n\ngo 1.26\n")

	_, err := moduledependency.Discover(context.Background(), application, moduledependency.Options{})
	if !errors.Is(err, moduledependency.ErrDiscover) || !errors.Is(err, moduledependency.ErrInvalidGoMod) || !strings.Contains(err.Error(), `module directive changed from "example.com/app"`) {
		t.Fatalf("Discover error = %v, want changed-module ErrInvalidGoMod", err)
	}
	var source *moduledependency.GoModSourceError
	if !errors.As(err, &source) || source == nil || source.ModulePath() != "example.com/app" || source.SourcePath() != "go.mod" || source.SourceKind() != "module-dependency" || source.Line() != 1 || source.Column() != 1 {
		t.Fatalf("Discover source = %#v, %v", source, err)
	}
}

func TestDiscoverReportsConcurrentApplicationGoModSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n")
	application := locate(t, root)

	_, err := moduledependency.Discover(context.Background(), application, moduledependency.Options{
		GoCommand: os.Args[0],
		Environment: append(os.Environ(),
			"PLYSTRA_MODULE_DEPENDENCY_HELPER=concurrent",
			"PLYSTRA_MODULE_APP_ROOT="+root,
		),
	})
	if !errors.Is(err, moduledependency.ErrDiscover) || !errors.Is(err, moduledependency.ErrConcurrentChange) {
		t.Fatalf("Discover error = %v, want ErrDiscover and ErrConcurrentChange", err)
	}
	var source *moduledependency.GoModSourceError
	if !errors.As(err, &source) || source == nil || source.ModulePath() != "example.com/app" || source.SourcePath() != "go.mod" || source.SourceKind() != "module-dependency" || source.Line() != 0 || source.Column() != 0 {
		t.Fatalf("Discover source = %#v, %v", source, err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
		t.Fatalf("Discover exposed current Project root %q: %v", root, err)
	}
}
