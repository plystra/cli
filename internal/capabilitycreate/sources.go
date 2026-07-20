package capabilitycreate

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/capabilitysource"
)

// ErrResolveSources reports that a plan's local source-provider set could not
// be loaded coherently.
var ErrResolveSources = errors.New("resolve local capability sources")

// ResolvedSource binds one planned provider to its schema-validated source.
type ResolvedSource struct {
	provider Provider
	source   capabilitysource.Source
}

// Provider returns the local provider that declared the source capability.
func (s ResolvedSource) Provider() Provider { return s.provider }

// Source returns the immutable loaded capability declaration.
func (s ResolvedSource) Source() capabilitysource.Source { return s.source }

// ResolveSources loads and exactly compares every local provider candidate for
// the plan's source version, including normalized extension metadata. It
// returns no partial result when any declaration is unavailable, invalid, or
// inconsistent.
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
	var baselineSchema []byte
	for _, provider := range providers {
		if provider.Capability() != sourceID {
			return nil, fmt.Errorf("%w: provider %s declares %s, expected %s", ErrResolveSources, provider.PluginID(), provider.Capability(), sourceID)
		}
		source, err := capabilitysource.Load(provider.Path(), sourceID)
		if err != nil {
			return nil, fmt.Errorf("%w: provider %s at %s: %w", ErrResolveSources, provider.PluginID(), provider.Path(), err)
		}
		canonical, err := capabilitymeta.NormalizeSchema(source.Data())
		if err != nil {
			return nil, fmt.Errorf("%w: provider %s source %s: %w", ErrResolveSources, provider.PluginID(), source.Path(), err)
		}
		candidate := ResolvedSource{provider: provider, source: source}
		if len(resolved) == 0 {
			baselineSchema = canonical
		} else if !bytes.Equal(baselineSchema, canonical) {
			conflict, err := newSchemaConflict(resolved[0], baselineSchema, candidate, canonical)
			if err != nil {
				return nil, fmt.Errorf("%w: compare provider schemas: %w", ErrResolveSources, err)
			}
			return nil, fmt.Errorf("%w: %w", ErrResolveSources, conflict)
		}
		resolved = append(resolved, candidate)
	}
	return resolved, nil
}
