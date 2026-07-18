package applicationgenerate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/capabilitysource"
	"github.com/plystra/cli/internal/clientgen"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/dependencygen"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/intrinsiccatalog"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/cli/internal/providergen"
)

var (
	// ErrLibraryGeneration reports invalid or inconsistent module-owned output
	// for a non-runnable Plystra library module.
	ErrLibraryGeneration = errors.New("generate Plystra library module")
	// ErrLibraryContractConflict reports local providers carrying different
	// exact contracts for one Capability ID.
	ErrLibraryContractConflict = errors.New("conflicting local library Capability contracts")
)

type preparedLibrary struct {
	output      generatedfiles.Output
	fingerprint string
}

func generateLibrary(ctx context.Context, options Options, module modulelocate.Module) (Result, error) {
	prepared, err := prepareLibrary(ctx, options, module)
	if err != nil {
		return Result{}, err
	}
	if options.Check {
		report, err := generatedfiles.Check(module.Path(), prepared.output)
		if err != nil {
			return Result{}, err
		}
		return Result{module: module, report: report, checked: true}, nil
	}

	validate := options.Validate
	if validate == nil {
		validate = func(ctx context.Context, root string) error {
			return gocommand.Run(ctx, gocommand.Options{
				Command:     options.GoCommand,
				Directory:   root,
				Environment: options.Environment,
			}, "test", "-mod=readonly", "./...")
		}
	}
	install := generatedfiles.Install
	if options.RejectUnexpected {
		install = generatedfiles.InstallStrict
	}
	report, err := install(module.Path(), prepared.output, func(root string) error {
		return runModuleMutation(ctx, options, root, func() error {
			if err := validate(ctx, root); err != nil {
				return fmt.Errorf("validate generated library: %w", err)
			}
			confirmedModule, err := modulelocate.Find(root)
			if err != nil {
				return fmt.Errorf("confirm library module: %w", err)
			}
			confirmed, err := prepareLibrary(ctx, options, confirmedModule)
			if err != nil {
				return fmt.Errorf("confirm library generation inputs: %w", err)
			}
			if prepared.fingerprint != confirmed.fingerprint {
				return fmt.Errorf("%w: library declarations or generated output changed during generation", ErrConcurrentChange)
			}
			return nil
		})
	})
	if err != nil {
		return Result{}, err
	}
	return Result{module: module, report: report}, nil
}

func prepareLibrary(ctx context.Context, options Options, module modulelocate.Module) (preparedLibrary, error) {
	output, err := renderLibrary(ctx, options, module)
	if err != nil {
		return preparedLibrary{}, err
	}
	sum := sha256.Sum256(output.ManifestJSON())
	return preparedLibrary{
		output:      output,
		fingerprint: "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

func renderLibrary(ctx context.Context, options Options, module modulelocate.Module) (generatedfiles.Output, error) {
	if module.Path() == "" || module.ModulePath() == "" {
		return generatedfiles.Output{}, fmt.Errorf("%w: module is empty", ErrLibraryGeneration)
	}
	dependencies, err := moduledependency.Discover(ctx, module, moduledependency.Options{
		GoCommand:   options.GoCommand,
		Environment: append([]string(nil), options.Environment...),
		OutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: discover dependency modules: %w", ErrLibraryGeneration, err)
	}
	inventory, err := plugininventory.Build(module, dependencies)
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: index visible plugins: %w", ErrLibraryGeneration, err)
	}
	contracts, err := visibleLibraryContracts(inventory)
	if err != nil {
		return generatedfiles.Output{}, err
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
	compatibility, err := assemblygen.RenderCompatibility("assembly")
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: Kernel assembly compatibility: %w", ErrLibraryGeneration, err)
	}
	if err := add("generated/go/assembly/compatibility_gen.go", compatibility); err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: Kernel assembly compatibility: %w", ErrLibraryGeneration, err)
	}

	configurationTypes := make(map[string]string)
	provided := make(map[string]string)
	required := make(map[string]string)
	for _, plugin := range inventory.Plugins() {
		if !plugin.Local() {
			continue
		}
		configuration, err := configurationgen.Render(configurationgen.Input{
			PluginName: plugin.Name(),
			PluginID:   plugin.ID(),
			Schema:     plugin.Config(),
		})
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: configuration for plugin %q: %w", ErrLibraryGeneration, plugin.ID(), err)
		}
		if previous, collision := configurationTypes[configuration.TypeName()]; collision {
			return generatedfiles.Output{}, fmt.Errorf("%w: configurations for plugins %q and %q both generate Go type %s", ErrLibraryGeneration, previous, plugin.ID(), configuration.TypeName())
		}
		configurationTypes[configuration.TypeName()] = plugin.ID()
		if err := add(configuration.Path(), configuration.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: configuration for plugin %q: %w", ErrLibraryGeneration, plugin.ID(), err)
		}

		for _, identifier := range plugin.Provides() {
			if strings.HasPrefix(identifier.Name(), "kernel.") {
				return generatedfiles.Output{}, fmt.Errorf("%w: plugin %q cannot provide intrinsic Capability %s", ErrLibraryGeneration, plugin.ID(), identifier)
			}
			key := identifier.String()
			if _, exists := contracts[key]; !exists {
				return generatedfiles.Output{}, fmt.Errorf("%w: local plugin %q Capability %s has no visible canonical contract", ErrLibraryGeneration, plugin.ID(), identifier)
			}
			if previous, exists := provided[key]; !exists {
				provided[key] = plugin.ID()
			} else if previous == "" {
				provided[key] = plugin.ID()
			}
		}

		pluginRequirements := plugin.Requires()
		if len(pluginRequirements) != 0 {
			identifiers := make([]string, len(pluginRequirements))
			for index, identifier := range pluginRequirements {
				key := identifier.String()
				if _, exists := contracts[key]; !exists {
					return generatedfiles.Output{}, fmt.Errorf("%w: plugin %q requires %s, but no exact contract is visible through this module or its direct dependencies", ErrLibraryGeneration, plugin.ID(), identifier)
				}
				required[key] = plugin.ID()
				identifiers[index] = key
			}
			dependenciesFile, err := dependencygen.Render(module.ModulePath(), plugin.Name(), plugin.ID(), identifiers)
			if err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: dependencies for plugin %q: %w", ErrLibraryGeneration, plugin.ID(), err)
			}
			if err := add(dependenciesFile.Path(), dependenciesFile.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: dependencies for plugin %q: %w", ErrLibraryGeneration, plugin.ID(), err)
			}
		}
	}

	identifiers := make([]string, 0, len(provided)+len(required))
	seenIdentifiers := make(map[string]struct{}, len(provided)+len(required))
	for identifier := range provided {
		seenIdentifiers[identifier] = struct{}{}
	}
	for identifier := range required {
		seenIdentifiers[identifier] = struct{}{}
	}
	for identifier := range seenIdentifiers {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	for _, identifier := range identifiers {
		contractInput := contracts[identifier]
		var contract contractgen.File
		if strings.HasPrefix(identifier, "kernel.") {
			contract, err = contractgen.RenderIntrinsic(contractInput.schema)
		} else {
			contract, err = contractgen.Render(contractInput.schema)
		}
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: contract %s: %w", ErrLibraryGeneration, identifier, err)
		}
		if err := add(contract.Path(), contract.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: contract %s: %w", ErrLibraryGeneration, identifier, err)
		}
		if _, exists := provided[identifier]; exists {
			provider, err := providergen.Render(module.ModulePath(), contractInput.schema)
			if err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: provider %s: %w", ErrLibraryGeneration, identifier, err)
			}
			if err := add(provider.Path(), provider.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: provider %s: %w", ErrLibraryGeneration, identifier, err)
			}
		}
		if _, exists := required[identifier]; exists {
			client, err := clientgen.Render(module.ModulePath(), contractInput.schema)
			if err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: client %s: %w", ErrLibraryGeneration, identifier, err)
			}
			if err := add(client.Path(), client.Data()); err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: client %s: %w", ErrLibraryGeneration, identifier, err)
			}
		}
	}
	output, err := generatedfiles.NewOutput(files)
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: finalize managed output: %w", ErrLibraryGeneration, err)
	}
	return output, nil
}

type libraryContract struct {
	schema  []byte
	sources []string
}

func visibleLibraryContracts(inventory plugininventory.Index) (map[string]libraryContract, error) {
	contracts := make(map[string]libraryContract)
	for _, definition := range intrinsiccatalog.Definitions() {
		contracts[definition.ID().String()] = libraryContract{
			schema:  definition.ContractJSON(),
			sources: []string{definition.Source()},
		}
	}
	for _, plugin := range inventory.Plugins() {
		for _, identifier := range plugin.Provides() {
			if strings.HasPrefix(identifier.Name(), "kernel.") {
				return nil, fmt.Errorf("%w: plugin %q cannot provide intrinsic Capability %s", ErrLibraryGeneration, plugin.ID(), identifier)
			}
			source, err := capabilitysource.Load(plugin.PluginRoot(), identifier)
			if err != nil {
				return nil, fmt.Errorf("%w: plugin %q Capability %s: %w", ErrLibraryGeneration, plugin.ID(), identifier, err)
			}
			canonical, err := capabilitymeta.NormalizeSchema(source.Data())
			if err != nil {
				return nil, fmt.Errorf("%w: plugin %q Capability %s: %w", ErrLibraryGeneration, plugin.ID(), identifier, err)
			}
			key := identifier.String()
			provenance := plugin.Source() + ":" + source.RelativePath()
			if previous, exists := contracts[key]; exists {
				if !bytes.Equal(previous.schema, canonical) {
					return nil, fmt.Errorf("%w: %w: %s differs between [%s] and %s", ErrLibraryGeneration, ErrLibraryContractConflict, identifier, strings.Join(previous.sources, ", "), provenance)
				}
				previous.sources = append(previous.sources, provenance)
				contracts[key] = previous
				continue
			}
			contracts[key] = libraryContract{schema: canonical, sources: []string{provenance}}
		}
	}
	return contracts, nil
}
