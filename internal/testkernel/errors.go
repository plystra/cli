package testkernel

import (
	"os"
	"path/filepath"
	"testing"
)

// WriteErrorBoundary installs the selected Kernel's production error boundary
// beside a fixture's lightweight invocation handle. Error semantics are never
// approximated by the handle fixture; full dispatch is tested independently.
func WriteErrorBoundary(t testing.TB, root string) {
	t.Helper()
	for _, relative := range []string{
		"invocation/code.go", "invocation/error.go", "invocation/semantic_error.go",
		"invocation/completion.go", "invocation/error_tree.go", "capability/semantic_error.go",
	} {
		data, err := os.ReadFile(filepath.Join(Root(t), filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(root, "kernel", filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
