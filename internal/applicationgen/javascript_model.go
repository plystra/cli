package applicationgen

import (
	"fmt"

	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/sdkmodel"
)

// JavaScriptModel returns the same selected legacy-transition SDK model that
// Render combines with the canonical Interface projection. Keeping this
// extraction at the application boundary lets generation build compatibility
// history before rendering and Render verify that history against its final
// public surface.
func JavaScriptModel(resolution generationresolution.ExtensionResult) (sdkmodel.Model, error) {
	context := resolution.Context()
	if !validContext(context) {
		return sdkmodel.Model{}, fmt.Errorf("%w: %w: final generation context is absent or has an invalid digest", ErrRender, ErrResolution)
	}
	aliases := resolution.AliasResolution()
	if !validAliases(aliases) {
		return sdkmodel.Model{}, fmt.Errorf("%w: %w: final Alias map is absent or has an invalid digest", ErrRender, ErrResolution)
	}
	targets := make([]sdkmodel.CanonicalTargetView, 0)
	for _, id := range context.Requirements() {
		target, exists := context.Capability(id)
		if !exists {
			return sdkmodel.Model{}, fmt.Errorf(
				"%w: %w: required canonical Capability %s is absent from the final context",
				ErrRender,
				ErrResolution,
				id,
			)
		}
		if target.Exposure().JavaScript {
			targets = append(targets, target)
		}
	}
	resolvedAliases := aliases.Aliases()
	aliasViews := make([]sdkmodel.AliasView, len(resolvedAliases))
	for index, alias := range resolvedAliases {
		aliasViews[index] = alias
	}
	model, err := sdkmodel.Build(targets, aliasViews)
	if err != nil {
		return sdkmodel.Model{}, fmt.Errorf("%w: SDK model: %w", ErrRender, err)
	}
	return model, nil
}
