//go:build windows || linux || darwin

package projectstage_test

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/projectstage"
)

func TestFileProxyMissingRequestRetainsNativeAccess(t *testing.T) {
	root, parent := fixture(t)
	origin := t.TempDir()
	path := filepath.ToSlash(origin)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	stage := capturedStage(t, root, parent, "GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local",
		"GOMODCACHE="+t.TempDir(), "GOPROXY="+(&url.URL{Scheme: "file", Path: path}).String())
	if _, err := stage.StartFileProxy(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	var base string
	for _, entry := range stage.Environment() {
		if value, found := strings.CutPrefix(entry, "GOPROXY=http://127.0.0.1:"); found {
			base = "http://127.0.0.1:" + value
		}
	}
	if base == "" {
		t.Fatal("missing-input native-access fixture did not start its proxy")
	}
	response, err := http.Get(base + "example.com/missing/@v/v1.0.0.mod")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("missing native-access fixture was not accepted", response.StatusCode)
	}
	inputs := stage.Inputs()
	before, err := os.Stat(origin)
	if err != nil {
		t.Fatal(err)
	}
	changeNativeAccess(t, origin)
	after, err := os.Stat(origin)
	if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("native access fixture changed ordinary directory metadata", err)
	}
	if !errors.Is(stage.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
		t.Fatal("missing-input capture ignored native access drift")
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
		t.Fatal("cleanup discarded missing-input native access evidence")
	}
}
