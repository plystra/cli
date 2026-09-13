// Package testkernel locates the Kernel version selected by the CLI module for
// cross-module test fixtures.
package testkernel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var cachedRoot = sync.OnceValues(resolveRoot)

// Root returns the source directory for the Kernel module pinned in go.mod.
func Root(t testing.TB) string {
	t.Helper()
	root, err := cachedRoot()
	if err != nil {
		t.Fatalf("locate pinned Kernel module: %v", err)
	}
	return root
}

func resolveRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("locate CLI source: runtime.Caller failed")
	}
	cliRoot, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		return "", fmt.Errorf("resolve CLI root: %w", err)
	}
	command := exec.Command("go", "list", "-mod=readonly", "-m", "-f", "{{.Dir}}", "github.com/plystra/kernel")
	command.Dir = cliRoot
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("query selected Kernel module: %w: %s", err, strings.TrimSpace(string(output)))
	}
	root := filepath.Clean(strings.TrimSpace(string(output)))
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("selected Kernel module returned non-absolute directory %q", root)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("inspect selected Kernel module directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("selected Kernel module directory %q is not a directory", root)
	}
	return root, nil
}
