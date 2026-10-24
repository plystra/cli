package newproject

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/agentguidance"
)

var updateDocumentationGolden = flag.Bool("update-docs", false, "update Project documentation goldens without application execution")

func TestPopulateKeepsProjectDocumentationCurrent(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/acme/my-app"
	if err := populate(t.Context(), root, modulePath, "my-app", true, true); err != nil {
		t.Fatal(err)
	}
	guidance, err := agentguidance.Render(modulePath)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"README.md"}
	for _, file := range guidance.Files() {
		paths = append(paths, file.Path())
	}
	for _, path := range paths {
		current, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		goldenPath := filepath.Join("testdata", "project", filepath.FromSlash(path))
		golden, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(current, golden) {
			continue
		}
		if *updateDocumentationGolden {
			if err := os.WriteFile(goldenPath, current, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		t.Errorf("Project documentation %s is stale; regenerate with -update-docs", path)
	}
}
