package projectstage_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/plystra/cli/internal/projectstage"
)

func TestSourceBatchPreservesFailureEvidence(t *testing.T) {
	for _, mode := range []string{"success", "bytes", "identity", "access", "metadata", "callback", "swallowed-error", "cancelled", "panic"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root, parent := fixture(t)
			stage := capturedStage(t, root, parent)
			before := stage.Inputs()
			source := t.TempDir()
			write(t, source, "value.go", "package dependency\n")
			input := sourceInput(t, source)
			metadata := filepath.Join(t.TempDir(), "input.info")
			write(t, filepath.Dir(metadata), filepath.Base(metadata), "original metadata")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			rejected := errors.New("source callback rejected")
			var escaped projectstage.SourceRetention
			var batchErr error
			panicked := false
			func() {
				defer func() { panicked = recover() == rejected }()
				batchErr = stage.WithSourceBatch(ctx, func(retain projectstage.SourceRetention) error {
					escaped = retain
					if err := retain.Trees([]string{source}); err != nil {
						return err
					}
					if err := retain.Sources([]projectstage.Source{input}, projectstage.MetadataInput{Path: metadata}); err != nil {
						return err
					}
					path := filepath.Join(source, "value.go")
					switch mode {
					case "bytes":
						write(t, source, "value.go", "package changed\n")
					case "identity":
						info, err := os.Stat(path)
						if err != nil {
							return err
						}
						if err := os.Rename(path, filepath.Join(t.TempDir(), "prior")); err != nil {
							return err
						}
						write(t, source, "value.go", "package dependency\n")
						return os.Chtimes(path, info.ModTime(), info.ModTime())
					case "access":
						changeNativeAccess(t, path)
					case "metadata":
						return os.Rename(metadata, metadata+".prior")
					case "swallowed-error":
						input.Checksum = "h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
						_ = retain.Sources([]projectstage.Source{input})
					case "cancelled":
						cancel()
					case "panic":
						panic(rejected)
					case "callback":
						return rejected
					}
					return nil
				})
			}()
			drift := mode == "bytes" || mode == "identity" || mode == "access" || mode == "metadata"
			if mode == "success" && batchErr != nil || drift && !errors.Is(batchErr, projectstage.ErrConcurrentChange) ||
				mode == "callback" && !errors.Is(batchErr, rejected) || mode == "swallowed-error" && !errors.Is(batchErr, projectstage.ErrSourceIntegrity) ||
				mode == "cancelled" && !errors.Is(batchErr, context.Canceled) || mode == "panic" && !panicked {
				t.Fatalf("batch lost its failure or final recheck: %v", batchErr)
			}
			if !errors.Is(escaped.Sources(nil), projectstage.ErrStage) || !errors.Is(escaped.Trees(nil), projectstage.ErrStage) ||
				!errors.Is(escaped.Files(nil), projectstage.ErrStage) || !errors.Is(escaped.Archives(nil), projectstage.ErrStage) {
				t.Fatal("source retention escaped the batch lifetime")
			}
			inputs := stage.Inputs()
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := before.Recheck(t.Context()); err != nil {
				t.Fatal("batch refreshed an earlier immutable Inputs", err)
			}
			if drift {
				if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
					t.Fatal("changed source or metadata evidence was lost after cleanup")
				}
				return
			}
			if err := inputs.Recheck(t.Context()); err != nil {
				t.Fatal("unchanged failure evidence did not survive cleanup", err)
			}
			write(t, source, "value.go", "package changed\n")
			if !errors.Is(inputs.Recheck(t.Context()), projectstage.ErrConcurrentChange) {
				t.Fatal("source evidence was discarded after failure or cancellation")
			}
		})
	}
}

func TestSourceBatchSerializesCallbacks(t *testing.T) {
	root, parent := fixture(t)
	stage := capturedStage(t, root, parent)
	source := t.TempDir()
	write(t, source, "value.go", "package dependency\n")
	input := sourceInput(t, source)
	if err := stage.WithSourceBatch(t.Context(), func(retain projectstage.SourceRetention) error {
		var group sync.WaitGroup
		results := make(chan error, 16)
		for range 8 {
			group.Go(func() { results <- retain.Trees([]string{source}) })
			group.Go(func() { results <- retain.Sources([]projectstage.Source{input}) })
		}
		group.Wait()
		close(results)
		var resultErr error
		for err := range results {
			resultErr = errors.Join(resultErr, err)
		}
		return resultErr
	}); err != nil {
		t.Fatal(err)
	}
	if err := stage.CheckSources(t.Context()); err != nil {
		t.Fatal("raw concurrent retention discarded authentication", err)
	}
}
