package projectstage

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/mod/module"
)

func TestRetainArchivesKeepsDistinctOriginals(t *testing.T) {
	stage, upstream, _ := fileProxyFixture(t)
	cache := filepath.Join(t.TempDir(), "v1.0.0.zip")
	if err := os.WriteFile(cache, proxyArchive(t, "v1.0.0"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := ArchiveInput{Path: cache, Module: module.Version{Path: proxyDependency, Version: "v1.0.0"}}
	if err := stage.RetainArchives(t.Context(), []ArchiveInput{input, input}); err != nil {
		t.Fatal(err)
	}
	if _, err := stage.retainProxyFile(t.Context(), filepath.Join(upstream, "v1.0.0.zip"), proxyDependency+"/@v/v1.0.0.zip"); err != nil {
		t.Fatal("cache identity shadowed the distinct proxy origin", err)
	}
	if len(stage.artifacts) != 2 {
		t.Fatal("repeated capture refreshed or duplicated an original")
	}
	inputs := stage.Inputs()
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inputs.Recheck(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(cache, cache+".prior"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(inputs.Recheck(t.Context()), ErrConcurrentChange) {
		t.Fatal("cache evidence disappeared after cleanup")
	}
}

func TestRetainArchivesUsesBoundedArchiveCapture(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "larger-than-metadata", true: "invalid"}[invalid], func(t *testing.T) {
			t.Parallel()
			stage, _, _ := fileProxyFixture(t)
			var data []byte
			if invalid {
				data = []byte("invalid private archive")
			} else {
				var content bytes.Buffer
				writer := zip.NewWriter(&content)
				file, err := writer.CreateHeader(&zip.FileHeader{Name: proxyDependency + "@v1.0.0/value.go", Method: zip.Store})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.Write(bytes.Repeat([]byte{'a'}, maximumMetadataBytes+1)); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				data = content.Bytes()
			}
			path := filepath.Join(t.TempDir(), "v1.0.0.zip")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			err := stage.RetainArchives(t.Context(), []ArchiveInput{{Path: path, Module: module.Version{Path: proxyDependency, Version: "v1.0.0"}}})
			if invalid != errors.Is(err, ErrSourceIntegrity) || !invalid && err != nil {
				t.Fatal("archive limits were treated as metadata limits, or invalid content was accepted", err)
			}
			if invalid && stage.Recheck(t.Context()) == nil {
				t.Fatal("archive rejection did not remain fatal to the stage")
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal("complete read was discarded after validation failure", err)
			}
			if err := os.Rename(path, path+".prior"); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(inputs.Recheck(t.Context()), ErrConcurrentChange) {
				t.Fatal("archive evidence lost after cleanup")
			}
		})
	}
}
