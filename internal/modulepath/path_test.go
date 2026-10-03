package modulepath_test

import (
	"strings"
	"testing"

	"github.com/plystra/cli/internal/modulepath"
)

func TestCheckProjectAcceptsStandardAndInitialLocalModulePaths(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"github.com/acme/my-app", "github.com/acme/my-app/v2", "my-app", "example.com/" + strings.Repeat("segment/", 160) + "module"} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if err := modulepath.CheckProject(value); err != nil {
				t.Fatalf("CheckProject(%q): %v", value, err)
			}
		})
	}
}

func TestCheckProjectRejectsUnsafeAndNonstandardNestedPaths(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", ".", "..", "local/app", "github.com/acme/app/v1", "../app", "not a module"} {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			if err := modulepath.CheckProject(value); err == nil {
				t.Fatalf("CheckProject(%q) succeeded", value)
			}
		})
	}
}
