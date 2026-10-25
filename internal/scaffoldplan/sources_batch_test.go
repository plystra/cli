package scaffoldplan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

type sourceWorkContext struct {
	context.Context
	polls atomic.Int64
}

func (c *sourceWorkContext) Err() error {
	c.polls.Add(1)
	return c.Context.Err()
}

func TestSourceBatchAdapterSharesRechecks(t *testing.T) {
	stage, _ := cachedArchiveStage(t, false)
	project, err := projectlocate.Find(stage.Root())
	if err != nil {
		t.Fatal(err)
	}
	var selected []moduledependency.Module
	err = moduledependency.PrepareSources(t.Context(), project, []module.Version{{Path: "example.com/dependency", Version: "v1.0.0"}},
		moduledependency.Options{Environment: stage.Environment()}, func(_ context.Context, sources []moduledependency.Module) error {
			selected = append([]moduledependency.Module(nil), sources...)
			return nil
		})
	if err != nil || len(selected) != 1 {
		t.Fatal("seed exact source through public preparation", err)
	}
	large := t.TempDir()
	for index := range 96 {
		if err := os.WriteFile(filepath.Join(large, fmt.Sprintf("value%d.go", index)), []byte("package dependency\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	version := module.Version{Path: "example.com/baseline", Version: "v1.0.0"}
	checksum, err := dirhash.HashDir(large, version.String(), dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.RetainSources(t.Context(), []projectstage.Source{{Directory: large, Module: version, Checksum: checksum}}); err != nil {
		t.Fatal(err)
	}
	if err := retainSources(t.Context(), stage, selected); err != nil {
		t.Fatal(err)
	}
	complete := &sourceWorkContext{Context: t.Context()}
	if err := stage.Recheck(complete); err != nil {
		t.Fatal(err)
	}
	batched := &sourceWorkContext{Context: t.Context()}
	if err := sourceBatch(stage)(batched, func(ctx context.Context) error {
		for range 6 {
			if err := retainSources(ctx, stage, selected); err != nil {
				return err
			}
			if err := retainMetadataFiles(ctx, stage, []string{selected[0].GoModFile()}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("cancellation polls: adapter callbacks=%d one-complete-recheck=%d", batched.polls.Load(), complete.polls.Load())
	if complete.polls.Load() == 0 || batched.polls.Load() > 3*complete.polls.Load() {
		t.Fatal("adapter bypassed the source/metadata batch")
	}
	if err := stage.CheckSources(t.Context()); err != nil {
		t.Fatal("batch accepted unauthenticated sources", err)
	}
}
