//go:build linux || darwin

package privatefile_test

import (
	"os"
	"testing"
)

func makePublic(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}
