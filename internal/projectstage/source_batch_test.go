package projectstage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

func TestSourceBatchSharesCompleteRechecks(t *testing.T) {
	stage, _, _ := fileProxyFixture(t)
	large := t.TempDir()
	for index := range 96 {
		if err := os.WriteFile(filepath.Join(large, fmt.Sprintf("value%d.go", index)), []byte("package dependency\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := stage.RetainSourceTrees(t.Context(), []string{large}); err != nil {
		t.Fatal(err)
	}
	var sources []Source
	for range 6 {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "value.go"), []byte("package dependency\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		version := module.Version{Path: "example.com/dependency", Version: "v1.0.0"}
		checksum, err := dirhash.HashDir(directory, version.String(), dirhash.Hash1)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, Source{Directory: directory, Module: version, Checksum: checksum})
	}
	if err := stage.RetainSources(t.Context(), sources); err != nil {
		t.Fatal(err)
	}
	complete := &metadataWalkContext{Context: t.Context()}
	if err := stage.Recheck(complete); err != nil {
		t.Fatal(err)
	}
	batched := &metadataWalkContext{Context: t.Context()}
	if err := stage.WithSourceBatch(batched, func(retain SourceRetention) error {
		for _, source := range sources {
			if err := retain.Sources([]Source{source}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("cancellation polls: source callbacks=%d one-complete-recheck=%d", batched.polls.Load(), complete.polls.Load())
	if complete.polls.Load() == 0 || batched.polls.Load() > 3*complete.polls.Load() {
		t.Fatal("source callbacks repeat complete tree traversal")
	}
}

func TestSourceBatchChecksLiveAcquisitionSources(t *testing.T) {
	stage, _, cache := fileProxyFixture(t)
	version := module.Version{Path: proxyDependency, Version: "v1.0.0"}
	isolated, err := stage.AcquisitionCache(t.Context(), cache, []module.Version{version})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(isolated, "dependency")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "value.go")
	if err := os.WriteFile(path, []byte("package dependency\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	checksum, err := dirhash.HashDir(directory, version.String(), dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	err = stage.WithSourceBatch(t.Context(), func(retain SourceRetention) error {
		if err := retain.Sources([]Source{{Directory: directory, Module: version, Checksum: checksum}}); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("package changed\n"), 0o600)
	})
	if !errors.Is(err, ErrConcurrentChange) {
		t.Fatal("batch lost the live private-source recheck", err)
	}
	inputs := stage.Inputs()
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inputs.Recheck(t.Context()); err != nil {
		t.Fatal("private source evidence leaked into durable original Inputs", err)
	}
}
