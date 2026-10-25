package projectstage

import (
	"context"
	"errors"
	"sync"
)

// SourceRetention captures module sources and related metadata immediately.
// Its functions serialize under the Stage lock and expire with the callback.
type SourceRetention struct {
	MetadataRetention
	Sources func([]Source, ...MetadataInput) error
	Trees   func([]string) error
}

// WithSourceBatch shares complete before/after checks across one closed local
// capture phase, including failure and cancellation. The callback must use the
// supplied functions, start no Go work, and call no other Stage methods.
func (s *Stage) WithSourceBatch(ctx context.Context, capture func(SourceRetention) error) (resultErr error) {
	defer s.lock()()
	if capture == nil {
		return ErrStage
	}
	if err := s.recheck(ctx); err != nil {
		return err
	}
	var mu sync.Mutex
	active := true
	var captureErr error
	retain := func(operation func() error) error {
		mu.Lock()
		defer mu.Unlock()
		if !active {
			return ErrStage
		}
		if err := ctx.Err(); err != nil {
			captureErr = errors.Join(captureErr, err)
			return err
		}
		err := operation()
		captureErr = errors.Join(captureErr, err)
		return err
	}
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		active = false
		resultErr = errors.Join(resultErr, captureErr, ctx.Err(), s.recheck(ctx))
	}()
	return capture(SourceRetention{
		MetadataRetention: MetadataRetention{
			Files: func(files []MetadataInput) error {
				return retain(func() error { return s.retainMetadata(ctx, files) })
			},
			Archives: func(archives []ArchiveInput) error {
				return retain(func() error { return s.retainArchives(ctx, archives) })
			},
		},
		Sources: func(sources []Source, metadata ...MetadataInput) error {
			return retain(func() error { return s.captureSources(ctx, sources, true, metadata...) })
		},
		Trees: func(directories []string) error {
			return retain(func() error {
				sources := make([]Source, len(directories))
				for index, directory := range directories {
					sources[index].Directory = directory
				}
				return s.captureSources(ctx, sources, false)
			})
		},
	})
}
