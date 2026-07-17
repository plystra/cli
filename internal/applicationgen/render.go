// Package applicationgen renders one complete currently supported application
// generated tree from the stable generation-resolution result.
package applicationgen

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/apidocgen"
	"github.com/plystra/cli/internal/clientgen"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/generationlowering"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/httpgen"
	"github.com/plystra/cli/internal/invocationgen"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/providergen"
	"github.com/plystra/cli/internal/sdkmodel"
)

const aliasManifestPath = "generated/manifest.json"

var (
	// ErrRender reports an incomplete or inconsistent resolved application or
	// failure in one deterministic renderer.
	ErrRender = errors.New("render generated application")
	// ErrResolution reports absent or internally inconsistent final resolution.
	ErrResolution = errors.New("invalid generation resolution result")
)

// Options carries application-owned generated package identities.
type Options struct {
	ModulePath        string
	JavaScriptPackage string
}

// Render lowers final selected contributions once and renders contracts,
// providers, canonical and Alias clients, canonical invocation paths, HTTP
// adapters, the JavaScript SDK, API documentation, and the current Alias
// manifest into one managed output model.
func Render(options Options, resolution generationresolution.ExtensionResult) (generatedfiles.Output, error) {
	context := resolution.Context()
	if !validContext(context) {
		return generatedfiles.Output{}, fmt.Errorf("%w: %w: final generation context is absent or has an invalid digest", ErrRender, ErrResolution)
	}
	aliases := resolution.AliasResolution()
	if !validAliases(aliases) {
		return generatedfiles.Output{}, fmt.Errorf("%w: %w: final Alias map is absent or has an invalid digest", ErrRender, ErrResolution)
	}
	plan, err := generationlowering.Lower(options.ModulePath, resolution.Contributions())
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: lower contributions: %w", ErrRender, err)
	}

	files := make([]generatedfiles.File, 0)
	add := func(filePath string, data []byte) error {
		file, err := generatedfiles.NewFile(filePath, data)
		if err != nil {
			return err
		}
		files = append(files, file)
		return nil
	}
	aliasManifest := append(aliases.CanonicalJSON(), '\n')
	if err := add(aliasManifestPath, aliasManifest); err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: Alias manifest: %w", ErrRender, err)
	}

	requirements := context.Requirements()
	if len(requirements) != 0 {
		invocationContext, err := invocationgen.RenderContext()
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: invocation context: %w", ErrRender, err)
		}
		if err := add(invocationContext.Path(), invocationContext.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: invocation context: %w", ErrRender, err)
		}
	}

	targets := make(map[generation.CapabilityID]generation.CapabilityView, len(requirements))
	javaScriptTargets := make([]sdkmodel.CanonicalTargetView, 0)
	httpTargets := 0
	for _, id := range requirements {
		target, exists := context.Capability(id)
		if !exists {
			return generatedfiles.Output{}, fmt.Errorf("%w: %w: required canonical Capability %s is absent from the final context", ErrRender, ErrResolution, id)
		}
		targets[id] = target
		if target.Exposure().JavaScript {
			javaScriptTargets = append(javaScriptTargets, target)
		}
		if target.Exposure().HTTP {
			httpTargets++
		}

		contract, err := contractgen.Render(target.ContractJSON())
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: contract %s: %w", ErrRender, id, err)
		}
		if err := add(contract.Path(), contract.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: contract %s: %w", ErrRender, id, err)
		}
		client, err := clientgen.Render(options.ModulePath, target.ContractJSON())
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: client %s: %w", ErrRender, id, err)
		}
		if err := add(client.Path(), client.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: client %s: %w", ErrRender, id, err)
		}
		if !target.Intrinsic() {
			provider, err := providergen.Render(options.ModulePath, target.ContractJSON())
			if err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: provider %s: %w", ErrRender, id, err)
			}
			if err := add(provider.Path(), provider.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: provider %s: %w", ErrRender, id, err)
			}
		}
		invocation, err := invocationgen.RenderPlan(options.ModulePath, target.ContractJSON(), plan)
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: invocation %s: %w", ErrRender, id, err)
		}
		if err := add(invocation.Path(), invocation.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: invocation %s: %w", ErrRender, id, err)
		}
		if target.Exposure().HTTP {
			handler, err := httpgen.RenderPlan(options.ModulePath, target, plan)
			if err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: HTTP adapter %s: %w", ErrRender, id, err)
			}
			if err := add(handler.Path(), handler.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: HTTP adapter %s: %w", ErrRender, id, err)
			}
		}
	}

	resolvedAliases := aliases.Aliases()
	aliasViews := make([]sdkmodel.AliasView, len(resolvedAliases))
	docAliasViews := make([]apidocgen.AliasView, len(resolvedAliases))
	httpAliases := 0
	for index, alias := range resolvedAliases {
		aliasViews[index] = alias
		docAliasViews[index] = alias
		target, exists := targets[alias.Target()]
		if !exists {
			return generatedfiles.Output{}, fmt.Errorf("%w: %w: Alias %s target %s is not a generated requirement", ErrRender, ErrResolution, alias.ID(), alias.Target())
		}
		if alias.Exposure().Go {
			client, err := clientgen.RenderAlias(options.ModulePath, alias, target)
			if err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: Alias client %s: %w", ErrRender, alias.ID(), err)
			}
			if err := add(client.Path(), client.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: Alias client %s: %w", ErrRender, alias.ID(), err)
			}
		}
		if alias.Exposure().HTTP {
			httpAliases++
			handler, err := httpgen.RenderAlias(options.ModulePath, alias, target)
			if err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: Alias HTTP adapter %s: %w", ErrRender, alias.ID(), err)
			}
			if err := add(handler.Path(), handler.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: Alias HTTP adapter %s: %w", ErrRender, alias.ID(), err)
			}
		}
	}

	model, err := sdkmodel.Build(javaScriptTargets, aliasViews)
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: SDK model: %w", ErrRender, err)
	}
	if len(javaScriptTargets) != 0 {
		javaScript, err := javascriptgen.Render(javascriptgen.Options{PackageName: options.JavaScriptPackage}, model)
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: JavaScript SDK: %w", ErrRender, err)
		}
		for _, file := range javaScript {
			if err := add(file.Path(), file.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: JavaScript SDK: %w", ErrRender, err)
			}
		}
	}
	if httpTargets != 0 || httpAliases != 0 {
		docs, err := apidocgen.Render(model, docAliasViews)
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: API documentation: %w", ErrRender, err)
		}
		for _, file := range docs {
			if err := add(file.Path(), file.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: API documentation: %w", ErrRender, err)
			}
		}
	}

	output, err := generatedfiles.NewOutput(files)
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: finalize managed output: %w", ErrRender, err)
	}
	return output, nil
}

func validContext(context generation.Context) bool {
	canonical := context.CanonicalJSON()
	return context.APIVersion() == generation.Version && len(canonical) != 0 && context.Digest() == digest(canonical)
}

func validAliases(aliases aliasresolution.Result) bool {
	canonical := aliases.CanonicalJSON()
	return len(canonical) != 0 && aliases.Digest() == digest(canonical)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
