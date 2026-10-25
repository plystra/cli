//go:build windows || linux || darwin

package projectstage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/projectstage"
	"golang.org/x/mod/module"
)

func TestAcquisitionCacheBatchRejectsNativeAccessDrift(t *testing.T) {
	for _, kind := range []string{"seed-file", "seed-parent", "live-cache"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			root, parent := fixture(t)
			stage := capturedStage(t, root, parent, "GOWORK=off", "GOENV=off")
			cache := t.TempDir()
			relative := filepath.Join("cache", "download", "example.com", "dependency", "@v", "v1.0.0.info")
			original := filepath.Join(cache, relative)
			write(t, cache, filepath.ToSlash(relative), `{"Version":"v1.0.0"}`)
			prior := stage.Inputs()
			changed := false
			ctx := &acquisitionAccessContext{Context: t.Context(), poll: func() {
				if changed {
					return
				}
				caches, err := filepath.Glob(filepath.Join(filepath.Dir(stage.Root()), "module-cache-*"))
				if err != nil {
					t.Fatal(err)
				}
				for _, selected := range caches {
					if _, err := os.Stat(filepath.Join(selected, relative)); errors.Is(err, os.ErrNotExist) {
						continue
					} else if err != nil {
						t.Fatal(err)
					}
					changed = true
					path := original
					if kind == "seed-parent" {
						path = filepath.Dir(original)
					} else if kind == "live-cache" {
						path = selected
					}
					before, err := os.Stat(path)
					if err != nil {
						t.Fatal(err)
					}
					changeNativeAccess(t, path)
					after, err := os.Stat(path)
					if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
						t.Fatal("native access fixture changed ordinary metadata", err)
					}
					break
				}
			}}
			selected, err := stage.AcquisitionCache(ctx, cache, []module.Version{{Path: "example.com/dependency", Version: "v1.0.0"}})
			if !changed || selected != "" || !errors.Is(err, projectstage.ErrConcurrentChange) {
				t.Fatal("native access drift escaped the complete batch check", changed, selected, err)
			}
			if !errors.Is(stage.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
				t.Fatal("live stage lost native access evidence")
			}
			if err := prior.Recheck(t.Context()); err != nil {
				t.Fatal("cache native access capture changed an earlier snapshot", err)
			}
			if err := stage.Inputs().Recheck(t.Context()); (kind != "live-cache") != errors.Is(err, projectstage.ErrConcurrentChange) || kind == "live-cache" && err != nil {
				t.Fatal("native access evidence escaped its original or private lifetime", err)
			}
		})
	}
}

type acquisitionAccessContext struct {
	context.Context
	poll func()
}

func (c *acquisitionAccessContext) Err() error {
	c.poll()
	return c.Context.Err()
}
