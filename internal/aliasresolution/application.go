package aliasresolution

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
)

// ErrInvalidApplicationAlias reports an explicit plystra.yaml declaration that
// cannot enter the resolved canonical application model.
var ErrInvalidApplicationAlias = errors.New("invalid application Capability Alias")

// NormalizeApplication validates parsed explicit declarations against the
// resolved canonical catalog and computes their effective inherited or narrowed
// exposure for generation.Context.
func NormalizeApplication(context generation.Context, declarations []applicationmeta.Alias) ([]generation.CapabilityAliasInput, error) {
	if context.APIVersion() != generation.Version || len(context.CanonicalJSON()) == 0 {
		return nil, fmt.Errorf("%w: %w: expected generation API %q context", ErrResolve, ErrInvalidContext, generation.Version)
	}
	values := append([]applicationmeta.Alias(nil), declarations...)
	sort.Slice(values, func(left, right int) bool {
		return values[left].ID().String() < values[right].ID().String()
	})
	result := make([]generation.CapabilityAliasInput, 0, len(values))
	seen := make(map[generation.CapabilityID]string, len(values))
	for _, declaration := range values {
		id, err := generation.ParseCapabilityID(declaration.ID().String())
		if err != nil {
			return nil, applicationAliasError(declaration, "Alias ID %q is not canonical", declaration.ID().String())
		}
		targetID, err := generation.ParseCapabilityID(declaration.Target().String())
		if err != nil {
			return nil, applicationAliasError(declaration, "target ID %q is not canonical", declaration.Target().String())
		}
		if previous, duplicate := seen[id]; duplicate {
			return nil, applicationAliasError(declaration, "Alias %s duplicates %s", id, previous)
		}
		seen[id] = declaration.Source()
		if _, collision := context.Capability(id); collision {
			return nil, applicationAliasError(declaration, "Alias %s collides with a canonical Capability", id)
		}
		if strings.HasPrefix(id.Name(), "kernel.") {
			return nil, applicationAliasError(declaration, "Alias %s uses the reserved kernel.* namespace", id)
		}
		if existing, duplicate := context.CapabilityAlias(id); duplicate {
			return nil, applicationAliasError(declaration, "Alias %s duplicates existing normalized Alias targeting %s", id, existing.Target())
		}
		target, exists := context.Capability(targetID)
		if !exists {
			if chained, alias := context.CapabilityAlias(targetID); alias {
				return nil, applicationAliasError(declaration, "target %s is application-local Alias to %s; Alias chains are forbidden", targetID, chained.Target())
			}
			return nil, applicationAliasError(declaration, "target %s is not a visible canonical Capability", targetID)
		}
		if !containsCapability(context.Requirements(), targetID) {
			return nil, applicationAliasError(declaration, "target %s is not a resolved canonical requirement", targetID)
		}
		if id.Major() != targetID.Major() {
			return nil, applicationAliasError(declaration, "Alias %s and target %s do not use the same version", id, targetID)
		}

		exposure := target.Exposure()
		if narrowed, explicit := declaration.Exposure(); explicit {
			if !exposureSubset(narrowed, target.Exposure()) {
				return nil, applicationAliasError(
					declaration,
					"exposure go=%t http=%t javascript=%t broadens target %s exposure go=%t http=%t javascript=%t",
					narrowed.Go,
					narrowed.HTTP,
					narrowed.JavaScript,
					targetID,
					target.Exposure().Go,
					target.Exposure().HTTP,
					target.Exposure().JavaScript,
				)
			}
			exposure = narrowed
		}
		result = append(result, generation.CapabilityAliasInput{
			ID:         id.String(),
			Target:     targetID.String(),
			Exposure:   exposure,
			Deprecated: declaration.Deprecated(),
			Sources: []generation.AliasSourceInput{{
				Kind: generation.AliasSourceApplication,
				ID:   "application",
			}},
		})
	}
	return result, nil
}

func applicationAliasError(declaration applicationmeta.Alias, format string, arguments ...any) error {
	return fmt.Errorf(
		"%w: %w: %s: %s",
		ErrResolve,
		ErrInvalidApplicationAlias,
		declaration.Source(),
		fmt.Sprintf(format, arguments...),
	)
}
