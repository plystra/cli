package capabilitycreate

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/capabilitysource"
	"github.com/plystra/cli/internal/pluginindex"
)

// ErrWriteDeclarations reports that capability.yaml and plugin.yaml could not
// be committed as one validated transaction.
var ErrWriteDeclarations = errors.New("write capability declarations")

// WriteDeclarations atomically writes the plan's schema and, when needed,
// target manifest declaration. Validation re-indexes the updated module, loads
// the committed target schema, and rejects source snapshots that changed after
// resolution.
func WriteDeclarations(plan Plan, sources []ResolvedSource) error {
	schemaWrite, err := RenderSchemaWrite(plan, sources)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWriteDeclarations, err)
	}
	manifestWrite, hasManifestWrite, err := RenderManifestWrite(plan)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWriteDeclarations, err)
	}
	writes := []atomicfs.Write{schemaWrite}
	if hasManifestWrite {
		writes = append(writes, manifestWrite)
	}
	if err := atomicfs.WriteFiles(plan.Target().ModuleRoot(), writes, func(updatedRoot string) error {
		return validateDeclarations(updatedRoot, plan, sources)
	}); err != nil {
		return fmt.Errorf("%w: %w", ErrWriteDeclarations, err)
	}
	return nil
}

func validateDeclarations(root string, plan Plan, sources []ResolvedSource) error {
	target := plan.Target()
	capability := plan.Version().Target()
	index, err := pluginindex.Scan(root)
	if err != nil {
		return fmt.Errorf("index updated plugins: %w", err)
	}
	indexedTarget, ok := index.ByName(target.Directory())
	if !ok {
		return fmt.Errorf("target plugin %s is no longer indexed", target.Directory())
	}
	if indexedTarget.ID() != target.ID() {
		return fmt.Errorf("%w: target plugin %s now declares %s instead of %s", atomicfs.ErrConcurrentChange, target.Directory(), indexedTarget.ID(), target.ID())
	}
	if !declares(indexedTarget, capability) {
		return fmt.Errorf("target plugin %s does not provide %s", target.ID(), capability)
	}
	targetSource, err := capabilitysource.Load(filepath.Join(root, filepath.FromSlash(indexedTarget.Path())), capability)
	if err != nil {
		return fmt.Errorf("load target schema: %w", err)
	}
	targetSchema, err := capabilitymeta.NormalizeSchema(targetSource.Data())
	if err != nil {
		return fmt.Errorf("normalize target schema: %w", err)
	}

	for position, resolved := range sources {
		provider := resolved.Provider()
		providerRoot := root
		providerIndex := index
		if !provider.Local() {
			providerRoot = provider.ModuleRoot()
			if providerRoot == "" {
				return fmt.Errorf("source provider %s has no module root provenance", provider.PluginID())
			}
			providerIndex, err = pluginindex.Scan(providerRoot)
			if err != nil {
				return fmt.Errorf("index source provider %s module: %w", provider.PluginID(), err)
			}
		}
		indexedProvider, ok := providerIndex.ByName(provider.Directory())
		if !ok || indexedProvider.ID() != provider.PluginID() || !declares(indexedProvider, resolved.Source().ID()) {
			return fmt.Errorf("%w: source provider %s declaration changed after planning", atomicfs.ErrConcurrentChange, provider.PluginID())
		}
		current, err := capabilitysource.Load(filepath.Join(providerRoot, filepath.FromSlash(indexedProvider.Path())), resolved.Source().ID())
		if err != nil {
			return fmt.Errorf("load source provider %s: %w", provider.PluginID(), err)
		}
		currentData := current.Data()
		if !bytes.Equal(currentData, resolved.Source().Data()) {
			return fmt.Errorf("%w: source provider %s schema changed after resolution", atomicfs.ErrConcurrentChange, provider.PluginID())
		}
		retargeted, err := capabilitymeta.RetargetSchema(currentData, capability)
		if err != nil {
			return fmt.Errorf("retarget source provider %s: %w", provider.PluginID(), err)
		}
		sourceSchema, err := capabilitymeta.NormalizeSchema(retargeted)
		if err != nil {
			return fmt.Errorf("normalize source provider %s: %w", provider.PluginID(), err)
		}
		if !bytes.Equal(targetSchema, sourceSchema) {
			return fmt.Errorf("%w: rendered %s differs from retained source provider %s at position %d", ErrSchemaConflict, capability, provider.PluginID(), position)
		}
	}
	return nil
}

func declares(plugin pluginindex.Plugin, capability capabilityid.Identifier) bool {
	for _, provided := range plugin.Provides() {
		if provided == capability {
			return true
		}
	}
	return false
}
