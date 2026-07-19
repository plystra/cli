// Package applicationinput loads indexed filesystem contracts into the
// existing deterministic provider and generation-resolution input model.
package applicationinput

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/capabilitysource"
	"github.com/plystra/cli/internal/generationactivation"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/intrinsiccatalog"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/cli/internal/providerresolution"
)

const maximumSourceSize = 1024

var (
	// ErrBuild reports that filesystem-backed resolution input could not be
	// constructed completely.
	ErrBuild = errors.New("build filesystem application resolution input")
	// ErrContractConflict reports visible providers carrying different exact
	// contracts for one canonical Capability ID.
	ErrContractConflict = errors.New("conflicting visible Capability contracts")
	// ErrIntrinsicProvider reports an ordinary plugin claiming a reserved
	// kernel.* Capability.
	ErrIntrinsicProvider = errors.New("plugin provides intrinsic Capability")
)

// Build loads every indexed provider contract, merges the exact visible
// canonical catalog with Kernel intrinsics, and constructs the existing
// fixed-point resolver input. It performs no provider selection itself.
func Build(manifest applicationmeta.Manifest, inventory plugininventory.Index, buildOptions generationexec.BuildOptions) (generationresolution.ExtensionInput, error) {
	groups := make(map[capabilityid.Identifier]*contractGroup)
	for _, definition := range intrinsiccatalog.Definitions() {
		groups[definition.ID()] = &contractGroup{
			intrinsic: true,
			variants: []contractVariant{{
				contract: definition.ContractJSON(),
				digest:   definition.ContractDigest(),
				sources:  []string{definition.Source()},
			}},
		}
	}

	plugins := make([]generationresolution.Plugin, 0, len(inventory.Plugins()))
	candidates := make([]providerresolution.Candidate, 0)
	declarations := make([]generationactivation.Declaration, 0)
	for _, plugin := range inventory.Plugins() {
		provides := identifierStrings(plugin.Provides())
		requires := identifierStrings(plugin.Requires())
		plugins = append(plugins, generationresolution.Plugin{
			Context: generation.PluginInput{
				ID:                plugin.ID(),
				ModulePath:        plugin.ModulePath(),
				ModuleVersion:     plugin.ModuleVersion(),
				Provides:          provides,
				Requires:          requires,
				BuildMetadataJSON: []byte("{}"),
			},
			Local:      plugin.Local(),
			ModuleRoot: plugin.ModuleRoot(),
			PluginPath: plugin.Path(),
		})
		if declaration, exists := plugin.Generation(); exists {
			declarations = append(declarations, generationactivation.Declaration{
				PluginID:   plugin.ID(),
				Source:     boundedSource(plugin.Source()),
				Generation: declaration,
			})
		}
		for _, provided := range plugin.Provides() {
			if strings.HasPrefix(provided.Name(), "kernel.") {
				return generationresolution.ExtensionInput{}, fmt.Errorf("%w: %w: plugin %q at %s claims %s", ErrBuild, ErrIntrinsicProvider, plugin.ID(), plugin.Source(), provided)
			}
			source, err := capabilitysource.Load(plugin.PluginRoot(), provided)
			if err != nil {
				return generationresolution.ExtensionInput{}, fmt.Errorf("%w: plugin %q Capability %s: %w", ErrBuild, plugin.ID(), provided, err)
			}
			canonical, err := capabilitymeta.NormalizeSchema(source.Data())
			if err != nil {
				return generationresolution.ExtensionInput{}, fmt.Errorf("%w: plugin %q Capability %s: normalize contract: %w", ErrBuild, plugin.ID(), provided, err)
			}
			provenance := capabilityProvenance(plugin, source.RelativePath())
			group, exists := groups[provided]
			if !exists {
				group = &contractGroup{}
				groups[provided] = group
			}
			group.add(canonical, provenance)
			candidates = append(candidates, providerresolution.Candidate{
				PluginID: plugin.ID(),
				Contract: append([]byte(nil), canonical...),
				Source:   provenance,
			})
		}
	}

	identifiers := make([]capabilityid.Identifier, 0, len(groups))
	for identifier := range groups {
		identifiers = append(identifiers, identifier)
	}
	sort.Slice(identifiers, func(left, right int) bool {
		return identifiers[left].String() < identifiers[right].String()
	})
	var conflicts []error
	capabilities := make([]generation.CapabilityInput, 0, len(identifiers))
	for _, identifier := range identifiers {
		group := groups[identifier]
		group.normalize()
		if len(group.variants) != 1 {
			variants := make([]ContractVariant, len(group.variants))
			for index, variant := range group.variants {
				variants[index] = ContractVariant{digest: variant.digest, sources: append([]string(nil), variant.sources...)}
			}
			conflicts = append(conflicts, &ContractConflictError{id: identifier, variants: variants})
			continue
		}
		capabilities = append(capabilities, generation.CapabilityInput{
			ContractJSON: append([]byte(nil), group.variants[0].contract...),
			Sources:      append([]string(nil), group.variants[0].sources...),
			Intrinsic:    group.intrinsic,
			Exposure:     generation.Exposure{Go: true},
		})
	}
	if len(conflicts) != 0 {
		return generationresolution.ExtensionInput{}, fmt.Errorf("%w: %w", ErrBuild, errors.Join(conflicts...))
	}

	requirements := make([]providerresolution.Requirement, 0, len(manifest.Requirements()))
	for _, declared := range manifest.Requirements() {
		requirement := providerresolution.Requirement{
			Capability: declared.ID().String(),
			Source:     declared.Source(),
		}
		if group, exists := groups[declared.ID()]; exists && len(group.variants) == 1 {
			requirement.Contract = append([]byte(nil), group.variants[0].contract...)
		}
		requirements = append(requirements, requirement)
	}
	choices := make([]providerresolution.Choice, 0, len(manifest.ProviderChoices()))
	for _, declared := range manifest.ProviderChoices() {
		choices = append(choices, providerresolution.Choice{
			Capability: declared.Capability().String(),
			PluginID:   declared.PluginID(),
			Source:     declared.Source(),
		})
	}
	activations, err := generationactivation.New(declarations)
	if err != nil {
		return generationresolution.ExtensionInput{}, fmt.Errorf("%w: %w", ErrBuild, err)
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].PluginID != candidates[right].PluginID {
			return candidates[left].PluginID < candidates[right].PluginID
		}
		return candidates[left].Source < candidates[right].Source
	})
	buildOptions.BuildEnvironment = append([]string(nil), buildOptions.BuildEnvironment...)
	return generationresolution.ExtensionInput{
		Input: generationresolution.Input{
			Requirements: requirements,
			Candidates:   candidates,
			Choices:      choices,
			Activations:  activations,
		},
		Plugins:                  plugins,
		Capabilities:             capabilities,
		ApplicationHTTPExposures: manifest.HTTPExposures(),
		ApplicationAliases:       manifest.Aliases(),
		BuildOptions:             buildOptions,
	}, nil
}

type contractGroup struct {
	intrinsic bool
	variants  []contractVariant
}

type contractVariant struct {
	contract []byte
	digest   string
	sources  []string
}

func (g *contractGroup) add(contract []byte, source string) {
	for index := range g.variants {
		if bytes.Equal(g.variants[index].contract, contract) {
			g.variants[index].sources = append(g.variants[index].sources, source)
			return
		}
	}
	g.variants = append(g.variants, contractVariant{
		contract: append([]byte(nil), contract...),
		digest:   contractDigest(contract),
		sources:  []string{source},
	})
}

func (g *contractGroup) normalize() {
	for index := range g.variants {
		sort.Strings(g.variants[index].sources)
	}
	sort.Slice(g.variants, func(left, right int) bool {
		if g.variants[left].digest != g.variants[right].digest {
			return g.variants[left].digest < g.variants[right].digest
		}
		return bytes.Compare(g.variants[left].contract, g.variants[right].contract) < 0
	})
}

func identifierStrings(identifiers []capabilityid.Identifier) []string {
	values := make([]string, len(identifiers))
	for index, identifier := range identifiers {
		values[index] = identifier.String()
	}
	return values
}

func capabilityProvenance(plugin plugininventory.Plugin, relativePath string) string {
	version := plugin.ModuleVersion()
	if version == "" {
		version = "local"
	}
	return boundedSource(path.Join(plugin.ModulePath()+"@"+version, plugin.Path(), relativePath))
}

func boundedSource(value string) string {
	if len(value) <= maximumSourceSize {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	suffix := "...#sha256:" + hex.EncodeToString(digest[:])
	limit := maximumSourceSize - len(suffix)
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + suffix
}

func contractDigest(contract []byte) string {
	digest := sha256.Sum256(contract)
	return "sha256:" + hex.EncodeToString(digest[:])
}

// ContractVariant records one exact conflicting digest and every provider
// source carrying it.
type ContractVariant struct {
	digest  string
	sources []string
}

// Digest returns the canonical contract SHA-256 digest.
func (v ContractVariant) Digest() string { return v.digest }

// Sources returns deterministic provider provenance for this exact variant.
func (v ContractVariant) Sources() []string {
	return append([]string(nil), v.sources...)
}

// ContractConflictError reports every exact variant visible under one
// canonical Capability ID.
type ContractConflictError struct {
	id       capabilityid.Identifier
	variants []ContractVariant
}

// ID returns the conflicting canonical Capability ID.
func (e *ContractConflictError) ID() capabilityid.Identifier {
	if e == nil {
		return capabilityid.Identifier{}
	}
	return e.id
}

// Variants returns defensive digest/source views in deterministic order.
func (e *ContractConflictError) Variants() []ContractVariant {
	if e == nil {
		return nil
	}
	result := make([]ContractVariant, len(e.variants))
	for index, variant := range e.variants {
		result[index] = ContractVariant{digest: variant.digest, sources: append([]string(nil), variant.sources...)}
	}
	return result
}

func (e *ContractConflictError) Error() string {
	if e == nil {
		return ErrContractConflict.Error()
	}
	var message strings.Builder
	fmt.Fprintf(&message, "%s for %s", ErrContractConflict, e.id)
	for _, variant := range e.variants {
		fmt.Fprintf(&message, "; %s at [%s]", variant.digest, strings.Join(variant.sources, ", "))
	}
	return message.String()
}

// Unwrap supports errors.Is with ErrContractConflict.
func (*ContractConflictError) Unwrap() error { return ErrContractConflict }
