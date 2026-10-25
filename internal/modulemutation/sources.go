package modulemutation

import (
	"bytes"
	"context"
	"errors"
	"os"

	"github.com/plystra/cli/internal/atomicfs"
	"golang.org/x/mod/module"
)

func prepareRuntimeSources(ctx context.Context, root string, before moduleFiles, versions []module.Version, prepare func([]module.Version) error, retainOwnership func(moduleFiles)) (owned moduleFiles, resultErr error) {
	if err := ctx.Err(); err != nil {
		return before, err
	}
	defer func() {
		var captureErr error
		owned, captureErr = runtimePreparationOwnership(root, before)
		retainOwnership(owned) // Also updates the outer rollback during panic unwinding.
		resultErr = errors.Join(resultErr, captureErr, ctx.Err())
	}()
	return nil, prepare(append([]module.Version(nil), versions...))
}

func runtimePreparationOwnership(path string, before moduleFiles) (owned moduleFiles, resultErr error) {
	owned = make(moduleFiles, len(before))
	root, err := os.OpenRoot(path)
	if err != nil {
		return owned, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	var changed []string
	for _, name := range []string{"go.mod", "go.sum"} {
		observed, err := captureModuleFile(root, name)
		original := before[name]
		if err != nil || original.exists != observed.exists || original.mode != observed.mode || !bytes.Equal(original.data, observed.data) {
			changed = append(changed, name)
			resultErr = errors.Join(resultErr, err)
		} else {
			owned[name] = original
		}
	}
	if len(changed) != 0 {
		resultErr = errors.Join(resultErr, atomicfs.NewConcurrentChangeError(changed, atomicfs.ErrConcurrentChange))
	}
	return owned, resultErr
}
