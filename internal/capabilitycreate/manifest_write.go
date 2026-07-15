package capabilitycreate

import (
	"errors"
	"fmt"
	"path"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/pluginmeta"
)

// ErrRenderManifest reports that a capability creation plan could not produce
// a safe target plugin.yaml write.
var ErrRenderManifest = errors.New("render capability manifest write")

// RenderManifestWrite returns the guarded module-relative plugin.yaml write
// for plan without changing the filesystem. The retained manifest snapshot is
// both the rendering source and the exact replacement precondition. An already
// declared capability needs no write.
func RenderManifestWrite(plan Plan) (atomicfs.Write, bool, error) {
	targetPlugin := plan.Target()
	targetCapability := plan.Version().Target()
	if targetPlugin.ID() == "" || targetPlugin.Directory() == "" || targetPlugin.ModuleRoot() == "" || targetCapability.String() == "" {
		return atomicfs.Write{}, false, fmt.Errorf("%w: plan is empty", ErrRenderManifest)
	}

	snapshot := targetPlugin.ManifestData()
	data, changed, err := pluginmeta.AddProvided(snapshot, targetCapability)
	if err != nil {
		return atomicfs.Write{}, false, fmt.Errorf("%w: %w", ErrRenderManifest, err)
	}
	if !changed {
		return atomicfs.Write{}, false, nil
	}
	return atomicfs.Write{
		Path:         path.Join(targetPlugin.Directory(), "plugin.yaml"),
		Data:         data,
		ExpectedData: snapshot,
	}, true, nil
}
