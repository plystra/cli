package applicationgen

import (
	"fmt"

	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/sdkmodel"
)

// DocumentationModel returns the selected legacy-transition HTTP documentation
// surface consumed by the current application documentation renderer. It is not
// the complete canonical Interface documentation model planned for Gate 20.
func DocumentationModel(resolution generationresolution.ExtensionResult) (sdkmodel.Model, error) {
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
		if target.Exposure().HTTP {
			targets = append(targets, target)
		}
	}
	resolvedAliases := aliases.Aliases()
	aliasViews := make([]sdkmodel.AliasView, len(resolvedAliases))
	for index, alias := range resolvedAliases {
		aliasViews[index] = alias
	}
	model, err := sdkmodel.BuildHTTP(targets, aliasViews)
	if err != nil {
		return sdkmodel.Model{}, fmt.Errorf("%w: documentation model: %w", ErrRender, err)
	}
	return model, nil
}
