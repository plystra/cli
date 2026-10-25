package projectstage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"golang.org/x/mod/module"
)

type metadataWalkContext struct {
	context.Context
	polls atomic.Int64
}

func (c *metadataWalkContext) Err() error {
	c.polls.Add(1)
	return c.Context.Err()
}

func TestMetadataBatchSharesSourceRechecks(t *testing.T) {
	stage, upstream, _ := fileProxyFixture(t)
	source := t.TempDir()
	for index := range 32 {
		if err := os.WriteFile(filepath.Join(source, fmt.Sprintf("value%d.go", index)), []byte("package dependency\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := stage.RetainSourceTrees(t.Context(), []string{source}); err != nil {
		t.Fatal(err)
	}
	files := []MetadataInput{{Path: filepath.Join(upstream, "v1.0.0.mod")}, {Path: filepath.Join(upstream, "v1.0.0.info")}}
	archives := []ArchiveInput{{Path: filepath.Join(upstream, "v1.0.0.zip"), Module: module.Version{Path: proxyDependency, Version: "v1.0.0"}}}
	operations := func(retain MetadataRetention) error {
		return errors.Join(retain.Files(files[:1]), retain.Archives(archives), retain.Files(files[1:]), retain.Archives(archives))
	}
	if err := stage.WithMetadataBatch(t.Context(), operations); err != nil {
		t.Fatal(err)
	}
	// Both measurements use the same already-retained inputs. Cancellation
	// polls count bounded tree work without clocks, sleeps, or production hooks.
	separate := &metadataWalkContext{Context: t.Context()}
	if err := operations(MetadataRetention{
		Files:    func(files []MetadataInput) error { return stage.RetainMetadata(separate, files) },
		Archives: func(archives []ArchiveInput) error { return stage.RetainArchives(separate, archives) },
	}); err != nil {
		t.Fatal(err)
	}
	batched := &metadataWalkContext{Context: t.Context()}
	if err := stage.WithMetadataBatch(batched, operations); err != nil {
		t.Fatal(err)
	}
	t.Logf("cancellation polls across retained trees: separate=%d batched=%d", separate.polls.Load(), batched.polls.Load())
	if batched.polls.Load() == 0 || separate.polls.Load() < 3*batched.polls.Load() {
		t.Fatal("metadata callbacks still repeat complete source traversal")
	}
}

func TestMetadataBatchRechecksOnFailure(t *testing.T) {
	for _, mode := range []string{"source", "metadata", "archive", "parent", "partial", "invalid-archive", "cancelled", "panic"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			stage, upstream, _ := fileProxyFixture(t)
			source := t.TempDir()
			path := filepath.Join(source, "value.go")
			if err := os.WriteFile(path, []byte("package dependency\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := stage.RetainSourceTrees(t.Context(), []string{source}); err != nil {
				t.Fatal(err)
			}
			metadata := filepath.Join(upstream, "v1.0.0.info")
			archive := filepath.Join(upstream, "v1.0.0.zip")
			if mode == "invalid-archive" {
				if err := os.WriteFile(archive, []byte("invalid ZIP"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			rejected := errors.New("capture callback rejected")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var retained MetadataRetention
			var captureErr, resultErr error
			panicked := false
			func() {
				defer func() { panicked = recover() == rejected }()
				resultErr = stage.WithMetadataBatch(ctx, func(retain MetadataRetention) error {
					retained = retain
					files := []MetadataInput{{Path: metadata}}
					if mode == "partial" {
						files = append(files, MetadataInput{Path: "relative"})
					}
					captureErr = errors.Join(retain.Files(files), retain.Archives([]ArchiveInput{{Path: archive,
						Module: module.Version{Path: proxyDependency, Version: "v1.0.0"}}}))
					if mode == "partial" || mode == "invalid-archive" {
						return errors.Join(rejected, captureErr)
					}
					if captureErr != nil {
						return captureErr
					}
					var err error
					switch mode {
					case "source":
						err = os.WriteFile(path, []byte("package changed\n"), 0o600)
					case "metadata", "archive":
						target := map[string]string{"metadata": metadata, "archive": archive}[mode]
						err = os.Rename(target, target+".prior")
					case "parent":
						err = os.Rename(upstream, upstream+".prior")
					case "cancelled":
						cancel()
					case "panic":
						panic(rejected)
					}
					return errors.Join(rejected, err)
				})
			}()
			if mode == "panic" {
				if !panicked {
					t.Fatal("callback panic was swallowed")
				}
			} else if !errors.Is(resultErr, rejected) {
				t.Fatal("callback failure was lost", resultErr)
			}
			drift := mode == "source" || mode == "metadata" || mode == "archive" || mode == "parent"
			if drift && !errors.Is(resultErr, ErrConcurrentChange) || mode == "cancelled" && !errors.Is(resultErr, context.Canceled) ||
				mode == "invalid-archive" && !errors.Is(resultErr, ErrSourceIntegrity) || mode == "partial" && !errors.Is(resultErr, ErrStage) {
				t.Fatal("final recheck or capture rejection was lost", resultErr)
			}
			if retained.Files == nil || !errors.Is(retained.Files(nil), ErrStage) || !errors.Is(retained.Archives(nil), ErrStage) {
				t.Fatal("batch retention escaped its callback lifetime")
			}
			if mode == "invalid-archive" && stage.Recheck(t.Context()) == nil {
				t.Fatal("archive rejection stopped poisoning the stage")
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if drift {
				if !errors.Is(inputs.Recheck(t.Context()), ErrConcurrentChange) {
					t.Fatal("changed evidence was lost after cleanup")
				}
				return
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal("unchanged partial evidence was lost", err)
			}
			if err := os.Rename(metadata, metadata+".prior"); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(inputs.Recheck(t.Context()), ErrConcurrentChange) {
				t.Fatal("batch metadata was not retained after failure and cleanup")
			}
		})
	}
}
