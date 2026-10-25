package projectstage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
)

func TestAcquisitionCacheSharesSourceRechecks(t *testing.T) {
	stage, _, cache := fileProxyFixture(t)
	source := t.TempDir()
	for index := range 128 {
		writeProxyInput(t, source, fmt.Sprintf("value%d.go", index), "package dependency\n")
	}
	if err := stage.RetainSourceTrees(t.Context(), []string{source}); err != nil {
		t.Fatal(err)
	}
	var versions []module.Version
	for index := range 8 {
		version := module.Version{Path: proxyDependency, Version: fmt.Sprintf("v1.0.%d", index)}
		seedAcquisitionCache(t, cache, version)
		versions = append(versions, version)
	}
	ctx := &metadataWalkContext{Context: t.Context()}
	isolated, err := stage.AcquisitionCache(ctx, cache, versions)
	if err != nil || isolated == cache {
		t.Fatal("exact batch did not acquire a private cache", err)
	}
	for _, version := range versions {
		for _, suffix := range []string{".info", ".mod", ".ziphash", ".zip"} {
			relative := acquisitionSeedPath(version) + suffix
			before, err := os.ReadFile(filepath.Join(cache, relative))
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(filepath.Join(isolated, relative))
			if err != nil || string(before) != string(after) {
				t.Fatal("private seed did not preserve captured bytes", err)
			}
		}
	}
	check := &metadataWalkContext{Context: t.Context()}
	if err := stage.Recheck(check); err != nil {
		t.Fatal(err)
	}
	// Count bounded traversal work, without timing thresholds or production hooks.
	// Two complete checks plus per-file work must stay below three full checks.
	t.Logf("cancellation polls: acquisition=%d one-complete-recheck=%d", ctx.polls.Load(), check.polls.Load())
	if check.polls.Load() == 0 || ctx.polls.Load() > 3*check.polls.Load() {
		t.Fatal("exact cache acquisition still repeats complete source traversal per seed")
	}
}

func TestAcquisitionCacheBatchRechecksBeforeCapture(t *testing.T) {
	stage, _, cache := fileProxyFixture(t)
	version := module.Version{Path: proxyDependency, Version: "v1.0.0"}
	seedAcquisitionCache(t, cache, version)
	writeProxyInput(t, stage.project, "changed.go", "package changed\n")
	if selected, err := stage.AcquisitionCache(t.Context(), cache, []module.Version{version}); selected != "" || !errors.Is(err, ErrConcurrentChange) {
		t.Fatal("changed original inputs were accepted", selected, err)
	}
	if len(stage.Inputs().captured().artifacts) != 0 || len(stage.cacheRoots) != 0 {
		t.Fatal("cache work started before original inputs were checked")
	}
}

func TestAcquisitionCacheBatchRechecksAfterCapture(t *testing.T) {
	for _, mode := range []string{"source-bytes", "source-identity", "source-membership", "seed-bytes", "seed-parent", "live-cache", "write-failure", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			stage, _, cache := fileProxyFixture(t)
			source := t.TempDir()
			sourcePath := filepath.Join(source, "value.go")
			writeProxyInput(t, source, "value.go", "package dependency\n")
			if err := stage.RetainSourceTrees(t.Context(), []string{source}); err != nil {
				t.Fatal(err)
			}
			version := module.Version{Path: proxyDependency, Version: "v1.0.0"}
			seedAcquisitionCache(t, cache, version)
			infoPath := filepath.Join(cache, acquisitionSeedPath(version)+".info")
			prior := stage.Inputs()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			changed := false
			hooked := &acquisitionSeedContext{Context: ctx, poll: func() {
				if changed {
					return
				}
				seed := privateAcquisitionSeed(t, stage, version)
				if seed == "" {
					return
				}
				changed = true
				switch mode {
				case "source-bytes", "seed-bytes":
					path := sourcePath
					if mode == "seed-bytes" {
						path = infoPath
					}
					changeAcquisitionBytes(t, path)
				case "source-identity":
					data, err := os.ReadFile(sourcePath)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(sourcePath, sourcePath+".prior"); err != nil {
						t.Fatal(err)
					}
					writeProxyInput(t, source, "value.go", string(data))
				case "source-membership":
					writeProxyInput(t, source, "added.go", "package dependency\n")
				case "seed-parent":
					if err := os.Rename(filepath.Dir(infoPath), filepath.Dir(infoPath)+".prior"); err != nil {
						t.Fatal(err)
					}
				case "live-cache":
					root := stage.cacheRoots[len(stage.cacheRoots)-1]
					if err := os.Rename(root, root+".prior"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(root, 0o700); err != nil {
						t.Fatal(err)
					}
				case "write-failure":
					if err := os.Mkdir(seed[:len(seed)-len(".info")]+".mod", 0o700); err != nil {
						t.Fatal(err)
					}
					changeAcquisitionBytes(t, sourcePath)
				case "cancelled":
					cancel()
				}
			}}
			selected, err := stage.AcquisitionCache(hooked, cache, []module.Version{version})
			if !changed || selected != "" || !errors.Is(err, ErrConcurrentChange) || mode == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("final cache check was lost", changed, selected, err)
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			originalDrift := mode != "live-cache" && mode != "cancelled"
			if err := inputs.Recheck(t.Context()); originalDrift != errors.Is(err, ErrConcurrentChange) || !originalDrift && err != nil {
				t.Fatal("original-side evidence changed during cleanup", err)
			}
			if mode == "seed-bytes" || mode == "seed-parent" {
				if err := prior.Recheck(t.Context()); err != nil {
					t.Fatal("cache capture mutated an earlier snapshot", err)
				}
			}
		})
	}
}

func TestAcquisitionCacheBatchRetainsFailedCaptures(t *testing.T) {
	for _, mode := range []string{"unsafe-seed", "invalid-archive"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			stage, _, cache := fileProxyFixture(t)
			version := module.Version{Path: proxyDependency, Version: "v1.0.0"}
			seedAcquisitionCache(t, cache, version)
			prefix := filepath.Join(cache, acquisitionSeedPath(version))
			want := ErrSourceIntegrity
			if mode == "unsafe-seed" {
				if err := os.Remove(prefix + ".mod"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(prefix+".mod", 0o700); err != nil {
					t.Fatal(err)
				}
				want = ErrStage
			} else {
				writeProxyInput(t, filepath.Dir(prefix), filepath.Base(prefix)+".zip", "invalid archive")
			}
			if selected, err := stage.AcquisitionCache(t.Context(), cache, []module.Version{version}); selected != "" || !errors.Is(err, want) {
				t.Fatal("capture failure was not preserved", selected, err)
			}
			if err := stage.Recheck(t.Context()); err == nil {
				t.Fatal("failed capture stopped poisoning the live stage")
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal("failed validation discarded unchanged positive evidence", err)
			}
			changeAcquisitionBytes(t, prefix+".info")
			if !errors.Is(inputs.Recheck(t.Context()), ErrConcurrentChange) {
				t.Fatal("earlier seed evidence was lost after failure and cleanup")
			}
		})
	}
}

type acquisitionSeedContext struct {
	context.Context
	poll func()
}

func (c *acquisitionSeedContext) Err() error {
	c.poll()
	return c.Context.Err()
}

func acquisitionSeedPath(version module.Version) string {
	return filepath.Join("cache", "download", filepath.FromSlash(version.Path), "@v", version.Version)
}

func seedAcquisitionCache(t *testing.T, cache string, version module.Version) {
	t.Helper()
	prefix := filepath.Join(cache, acquisitionSeedPath(version))
	writeProxyInput(t, filepath.Dir(prefix), filepath.Base(prefix)+".info", fmt.Sprintf(`{"Version":%q}`, version.Version))
	writeProxyInput(t, filepath.Dir(prefix), filepath.Base(prefix)+".mod", proxyManifest)
	writeProxyInput(t, filepath.Dir(prefix), filepath.Base(prefix)+".zip", string(proxyArchive(t, version.Version)))
	checksum, err := dirhash.HashZip(prefix+".zip", dirhash.Hash1)
	if err != nil {
		t.Fatal(err)
	}
	writeProxyInput(t, filepath.Dir(prefix), filepath.Base(prefix)+".ziphash", checksum)
}

func privateAcquisitionSeed(t *testing.T, stage *Stage, version module.Version) string {
	t.Helper()
	for _, root := range stage.cacheRoots {
		seed := filepath.Join(root, acquisitionSeedPath(version)+".info")
		if _, err := os.Stat(seed); err == nil {
			return seed
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	return ""
}

func changeAcquisitionBytes(t *testing.T, path string) {
	t.Helper()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		t.Fatal("missing mutation input", err)
	}
	data[0] ^= 1
	if err := os.WriteFile(path, data, before.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
}
