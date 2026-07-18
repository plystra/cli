package applicationgenerate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/capabilitysource"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/pluginindex"
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
	prepared, err := prepareLibrary(module)
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
	report, err := generatedfiles.Install(module.Path(), prepared.output, func(root string) error {
		return runModuleMutation(ctx, options, root, func() error {
			if err := validate(ctx, root); err != nil {
				return fmt.Errorf("validate generated library: %w", err)
			}
			confirmedModule, err := modulelocate.Find(root)
			if err != nil {
				return fmt.Errorf("confirm library module: %w", err)
			}
			confirmed, err := prepareLibrary(confirmedModule)
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

func prepareLibrary(module modulelocate.Module) (preparedLibrary, error) {
	output, err := renderLibrary(module)
	if err != nil {
		return preparedLibrary{}, err
	}
	sum := sha256.Sum256(output.ManifestJSON())
	return preparedLibrary{
		output:      output,
		fingerprint: "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

func renderLibrary(module modulelocate.Module) (generatedfiles.Output, error) {
	if module.Path() == "" || module.ModulePath() == "" {
		return generatedfiles.Output{}, fmt.Errorf("%w: module is empty", ErrLibraryGeneration)
	}
	index, err := pluginindex.Scan(module.Path())
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: index local plugins: %w", ErrLibraryGeneration, err)
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

	contracts := make(map[string]libraryContract)
	configurationTypes := make(map[string]string)
	for _, plugin := range index.Plugins() {
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

		pluginRoot := filepath.Join(module.Path(), filepath.FromSlash(plugin.Path()))
		for _, identifier := range plugin.Provides() {
			if strings.HasPrefix(identifier.Name(), "kernel.") {
				return generatedfiles.Output{}, fmt.Errorf("%w: plugin %q cannot provide intrinsic Capability %s", ErrLibraryGeneration, plugin.ID(), identifier)
			}
			source, err := capabilitysource.Load(pluginRoot, identifier)
			if err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: plugin %q Capability %s: %w", ErrLibraryGeneration, plugin.ID(), identifier, err)
			}
			canonical, err := capabilitymeta.NormalizeSchema(source.Data())
			if err != nil {
				return generatedfiles.Output{}, fmt.Errorf("%w: plugin %q Capability %s: %w", ErrLibraryGeneration, plugin.ID(), identifier, err)
			}
			key := identifier.String()
			if previous, exists := contracts[key]; exists {
				if !bytes.Equal(previous.schema, canonical) {
					return generatedfiles.Output{}, fmt.Errorf("%w: %w: %s differs between plugins %q and %q", ErrLibraryGeneration, ErrLibraryContractConflict, identifier, previous.pluginID, plugin.ID())
				}
				continue
			}
			contracts[key] = libraryContract{pluginID: plugin.ID(), schema: canonical}
		}
	}

	identifiers := make([]string, 0, len(contracts))
	for identifier := range contracts {
		identifiers = append(identifiers, identifier)
	}
	sort.Strings(identifiers)
	for _, identifier := range identifiers {
		contractInput := contracts[identifier]
		contract, err := contractgen.Render(contractInput.schema)
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: contract %s: %w", ErrLibraryGeneration, identifier, err)
		}
		if err := add(contract.Path(), contract.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: contract %s: %w", ErrLibraryGeneration, identifier, err)
		}
		provider, err := providergen.Render(module.ModulePath(), contractInput.schema)
		if err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: provider %s: %w", ErrLibraryGeneration, identifier, err)
		}
		if err := add(provider.Path(), provider.Data()); err != nil {
			return generatedfiles.Output{}, fmt.Errorf("%w: provider %s: %w", ErrLibraryGeneration, identifier, err)
		}
	}
	output, err := generatedfiles.NewOutput(files)
	if err != nil {
		return generatedfiles.Output{}, fmt.Errorf("%w: finalize managed output: %w", ErrLibraryGeneration, err)
	}
	return output, nil
}

type libraryContract struct {
	pluginID string
	schema   []byte
}
