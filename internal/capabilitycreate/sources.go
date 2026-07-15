package capabilitycreate

import (
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/capabilitysource"
)

// ErrResolveSources reports that a plan's local source-provider set could not
// be loaded coherently.
var ErrResolveSources = errors.New("resolve local capability sources")

// ResolvedSource binds one planned provider to its identity-checked source.
type ResolvedSource struct {
	provider Provider
	source   capabilitysource.Source
}

// Provider returns the local provider that declared the source capability.
func (s ResolvedSource) Provider() Provider { return s.provider }

// Source returns the immutable loaded capability declaration.
func (s ResolvedSource) Source() capabilitysource.Source { return s.source }

// ResolveSources loads every local provider candidate for the plan's source
// version. It returns no partial result when any declaration is unavailable.
func ResolveSources(plan Plan) ([]ResolvedSource, error) {
	if plan.Target().ID() == "" || plan.Version().Target().String() == "" {
		return nil, fmt.Errorf("%w: plan is empty", ErrResolveSources)
	}
	providers := plan.SourceProviders()
	sourceID, hasSource := plan.Version().Source()
	if !hasSource {
		if len(providers) != 0 {
			return nil, fmt.Errorf("%w: plan has providers without a source version", ErrResolveSources)
		}
		return nil, nil
	}
	if len(providers) == 0 {
		return nil, fmt.Errorf("%w: %s has no local provider", ErrResolveSources, sourceID)
	}

	resolved := make([]ResolvedSource, 0, len(providers))
	for _, provider := range providers {
		if provider.Capability() != sourceID {
			return nil, fmt.Errorf("%w: provider %s declares %s, expected %s", ErrResolveSources, provider.PluginID(), provider.Capability(), sourceID)
		}
		source, err := capabilitysource.Load(provider.Path(), sourceID)
		if err != nil {
			return nil, fmt.Errorf("%w: provider %s: %w", ErrResolveSources, provider.PluginID(), err)
		}
		resolved = append(resolved, ResolvedSource{provider: provider, source: source})
	}
	return resolved, nil
}
