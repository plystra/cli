package testmodulecache_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/testmodulecache"
)

func TestCompleteGraphPreparationPreservesModuleFiles(t *testing.T) {
	root := t.TempDir()
	module := []byte("module example.com/cache-fixture\n\ngo 1.26\n\nrequire go.yaml.in/yaml/v3 v3.0.5\n")
	for name, data := range map[string][]byte{"go.mod": module, "go.sum": {}} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	testmodulecache.Ensure(t, "all")
	for name, expected := range map[string][]byte{"go.mod": module, "go.sum": {}} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || !bytes.Equal(data, expected) {
			t.Fatalf("dependency preparation changed %s: %v", name, err)
		}
	}
}
