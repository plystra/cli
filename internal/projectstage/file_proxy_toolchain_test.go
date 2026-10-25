package projectstage

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/gocommand"
)

func TestFileProxyCachedToolchainKeepsFileVerificationException(t *testing.T) {
	stage, directory, _ := fileProxyFixture(t)
	const version = "v0.0.1-go1.26.0.windows-amd64"
	const manifest = "module golang.org/toolchain\ngo 1.26\n"
	root := filepath.Join(directory, "..", "..", "..", "golang.org", "toolchain")
	if err := os.MkdirAll(filepath.Join(root, "@v"), 0o700); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("golang.org/toolchain@" + version + "/go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, manifest); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		version + ".mod":  []byte(manifest),
		version + ".info": []byte(`{"Version":"` + version + `","Time":"2026-07-17T00:00:00Z"}`),
		version + ".zip":  buffer.Bytes(),
	} {
		if err := os.WriteFile(filepath.Join(root, "@v", name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	options := gocommand.Options{Directory: stage.Root(), Environment: stage.Environment()}
	query := "golang.org/toolchain@" + version
	output, err := gocommand.Output(t.Context(), options, "mod", "download", "-json", query)
	var downloaded struct{ Sum, Dir string }
	if err != nil || json.Unmarshal(output, &downloaded) != nil || downloaded.Sum == "" || downloaded.Dir == "" {
		t.Fatal("file-proxy toolchain control failed", err)
	}
	if err := os.Rename(root, root+".unpublished"); err != nil {
		t.Fatal(err)
	}
	if _, err := gocommand.Output(t.Context(), options, "mod", "download", "-json", query); err != nil {
		t.Fatal("warm toolchain control depends on the removed proxy entry", err)
	}
	before := strings.Join(stage.Environment(), "\n")
	if _, err := stage.StartFileProxy(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	options.Environment = stage.Environment()
	if _, err := gocommand.Output(t.Context(), options, "mod", "download", "-json", query); err != nil {
		t.Fatal("cached toolchain lost the sole-file-proxy checksum exception", err)
	}
	if stage.proxy != nil || strings.Join(stage.Environment(), "\n") != before {
		t.Fatal("cached toolchain distribution changed acquisition semantics")
	}
}

func TestFileProxyRejectsNewCachedToolchainDistribution(t *testing.T) {
	for _, mode := range []string{"live", "after-cleanup"} {
		t.Run(mode, func(t *testing.T) {
			stage, _, cache := fileProxyFixture(t)
			activeProxy(t, stage)
			inputs := stage.Inputs()
			if mode == "after-cleanup" {
				if err := stage.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Join(cache, "cache", "download", "golang.org", "toolchain"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); !errors.Is(err, ErrConcurrentChange) {
				t.Fatal("cached toolchain exclusion was not retained", err)
			}
			if mode == "live" {
				if err := stage.Recheck(t.Context()); !errors.Is(err, ErrConcurrentChange) {
					t.Fatal("cached toolchain exclusion changed during the session", err)
				}
			}
		})
	}
}
