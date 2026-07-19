package modulemutation

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindRequirementReportsSelectedVersionAndDirectness(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	data := `module example.com/acme/app

go 1.26

require (
	example.com/direct v1.2.3
	example.com/indirect v2.0.0+incompatible // indirect
)
`
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile(go.mod): %v", err)
	}

	tests := []struct {
		path     string
		version  string
		indirect bool
		exists   bool
	}{
		{path: "example.com/direct", version: "v1.2.3", exists: true},
		{path: "example.com/indirect", version: "v2.0.0+incompatible", indirect: true, exists: true},
		{path: "example.com/missing"},
	}
	for _, test := range tests {
		requirement, exists, err := FindRequirement(root, test.path)
		if err != nil || exists != test.exists || requirement.Version() != test.version || requirement.Indirect() != test.indirect {
			t.Fatalf("FindRequirement(%s) = %#v, %t, %v; want version %q, indirect %t, exists %t", test.path, requirement, exists, err, test.version, test.indirect, test.exists)
		}
	}
}
