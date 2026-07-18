// Package applicationgenerate resolves, renders, checks, and transactionally
// installs one complete filesystem-backed Plystra Project generation result.
package applicationgenerate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/modulelocate"
	kernelintrinsic "github.com/plystra/kernel/intrinsic"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

var (
	// ErrGenerate reports failure to resolve, render, check, install, or
	// validate one generated Plystra Project tree.
	ErrGenerate = errors.New("generate Plystra Project")
	// ErrConcurrentChange reports module inputs or extension output that
	// changed after the transaction's desired output was prepared.
	ErrConcurrentChange = errors.New("plystra module changed during generation")
	// ErrKernelDependency reports a Plystra Project that does not directly
	// select the Kernel Go Module used by its generated runtime.
	ErrKernelDependency = errors.New("invalid application Kernel dependency")
)

// Validator validates the complete application while desired generated files
// are installed but still protected by the transaction rollback boundary.
type Validator func(context.Context, string) error

// ModuleMutation wraps validation and generation-input confirmation while the
// desired generated tree is installed. It lets a higher-level mutating command
// normalize module-owned metadata and restore that metadata if any later check
// fails. The supplied operation must be called exactly once.
type ModuleMutation func(context.Context, string, func() error) error

// Options contains the application location, bounded Go helper settings, and
// operation mode. Check performs a read-only comparison. Validate overrides
// the default `go test -mod=readonly ./...` installation validation when
// non-nil. RejectUnexpected makes compound mutating commands fail and roll
// back rather than commit beside unowned generated output.
type Options struct {
	Start                 string
	Check                 bool
	GoCommand             string
	Environment           []string
	DependencyOutputLimit int
	CompileTimeout        time.Duration
	ExecutionTimeout      time.Duration
	TemporaryParent       string
	Validate              Validator
	MutateModule          ModuleMutation
	RejectUnexpected      bool
}

// Result identifies the resolved application and its deterministic generated
// output comparison. A successful installation can retain unexpected unowned
// files, which remain visible in Report rather than being overwritten.
type Result struct {
	module  modulelocate.Module
	report  generatedfiles.Report
	checked bool
}

// Module returns the nearest enclosing Go Module.
func (r Result) Module() modulelocate.Module { return r.module }

// Report returns the final generated-output comparison.
func (r Result) Report() generatedfiles.Report { return r.report }

// Checked reports whether the operation was the read-only check mode.
func (r Result) Checked() bool { return r.checked }

// Generate resolves and renders the nearest Plystra Project. Check mode
// compares without mutation. Install mode atomically installs desired files,
// validates the updated Project, and re-resolves inside the transaction so a
// concurrent generation-input edit or nondeterministic extension result causes
// rollback.
func Generate(ctx context.Context, options Options) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("%w: context is nil", ErrGenerate)
	}
	prepared, err := prepare(ctx, options, options.Start)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrGenerate, err)
	}
	if options.Check {
		report, err := generatedfiles.Check(prepared.resolved.Module().Path(), prepared.output)
		if err != nil {
			return Result{}, fmt.Errorf("%w: %w", ErrGenerate, err)
		}
		return Result{module: prepared.resolved.Module(), report: report, checked: true}, nil
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
	report, err := install(prepared.resolved.Module().Path(), prepared.output, func(root string) error {
		return runModuleMutation(ctx, options, root, func() error {
			if err := validate(ctx, root); err != nil {
				return fmt.Errorf("validate generated application: %w", err)
			}
			confirmed, err := prepare(ctx, options, root)
			if err != nil {
				return fmt.Errorf("confirm generation inputs: %w", err)
			}
			if prepared.fingerprint != confirmed.fingerprint {
				return fmt.Errorf("%w: resolved application or generated output no longer matches the planned snapshot", ErrConcurrentChange)
			}
			return nil
		})
	})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrGenerate, err)
	}
	return Result{module: prepared.resolved.Module(), report: report}, nil
}

func runModuleMutation(ctx context.Context, options Options, root string, operation func() error) error {
	if options.MutateModule == nil {
		return operation()
	}
	calls := 0
	err := options.MutateModule(ctx, root, func() error {
		calls++
		if calls != 1 {
			return errors.New("module mutation called generation validation more than once")
		}
		return operation()
	})
	if err != nil {
		return err
	}
	if calls != 1 {
		return errors.New("module mutation did not call generation validation")
	}
	return nil
}

type preparedGeneration struct {
	resolved    applicationresolve.Result
	output      generatedfiles.Output
	fingerprint string
}

func prepare(ctx context.Context, options Options, start string) (preparedGeneration, error) {
	resolved, err := applicationresolve.Resolve(ctx, applicationresolve.Options{
		Start:                 start,
		GoCommand:             options.GoCommand,
		Environment:           append([]string(nil), options.Environment...),
		DependencyOutputLimit: options.DependencyOutputLimit,
		CompileTimeout:        options.CompileTimeout,
		ExecutionTimeout:      options.ExecutionTimeout,
		TemporaryParent:       options.TemporaryParent,
	})
	if err != nil {
		return preparedGeneration{}, err
	}
	javaScriptPackage := ""
	if exposesJavaScript(resolved.Resolution().Context()) {
		javaScriptPackage, err = javascriptgen.InferPackageName(resolved.Module().ModulePath())
		if err != nil {
			return preparedGeneration{}, err
		}
	}
	configurations, err := localConfigurationInputs(resolved.Module().ModulePath(), resolved.Configurations())
	if err != nil {
		return preparedGeneration{}, err
	}
	kernelVersion, kernelBuildIdentity, err := kernelBuildProvenance(resolved)
	if err != nil {
		return preparedGeneration{}, err
	}
	providers, err := providerInputs(resolved)
	if err != nil {
		return preparedGeneration{}, err
	}
	output, err := applicationgen.Render(applicationgen.Options{
		ModulePath:          resolved.Module().ModulePath(),
		JavaScriptPackage:   javaScriptPackage,
		KernelModuleVersion: kernelVersion,
		KernelBuildIdentity: kernelBuildIdentity,
		Configurations:      configurations,
		Providers:           providers,
	}, resolved.Resolution())
	if err != nil {
		return preparedGeneration{}, err
	}
	fingerprint, err := generationFingerprint(resolved, output)
	if err != nil {
		return preparedGeneration{}, err
	}
	return preparedGeneration{resolved: resolved, output: output, fingerprint: fingerprint}, nil
}

func kernelBuildProvenance(resolved applicationresolve.Result) (string, string, error) {
	dependency, exists := resolved.Dependencies().ByPath(kernelintrinsic.ModulePath)
	if !exists || !dependency.Direct() {
		return "", "", fmt.Errorf("%w: go.mod must directly require %s", ErrKernelDependency, kernelintrinsic.ModulePath)
	}
	identity := ""
	if dependency.SelectedVersion() == "" {
		identity = resolved.Resolution().Context().Digest()
	} else if _, replaced := dependency.Replacement(); replaced {
		identity = resolved.Resolution().Context().Digest()
	}
	if _, err := kernelinvocation.NewModuleBuild(kernelintrinsic.ModulePath, dependency.SelectedVersion(), identity); err != nil {
		return "", "", fmt.Errorf("%w: selected %s build provenance: %v", ErrKernelDependency, kernelintrinsic.ModulePath, err)
	}
	return dependency.SelectedVersion(), identity, nil
}

func providerInputs(resolved applicationresolve.Result) ([]assemblygen.ProviderInput, error) {
	bindings := resolved.Configurations().Bindings()
	inputs := make([]assemblygen.ProviderInput, len(bindings))
	context := resolved.Resolution().Context()
	for index, binding := range bindings {
		plugin, exists := resolved.Inventory().ByID(binding.PluginID())
		if !exists {
			return nil, fmt.Errorf("selected plugin %q is absent from the visible inventory", binding.PluginID())
		}
		required := plugin.Requires()
		dependencies := make([]assemblygen.DependencyInput, len(required))
		for dependencyIndex, identifier := range required {
			generationID, err := generation.ParseCapabilityID(identifier.String())
			if err != nil {
				return nil, fmt.Errorf("selected plugin %q dependency %s: %v", binding.PluginID(), identifier, err)
			}
			capability, exists := context.Capability(generationID)
			if !exists {
				return nil, fmt.Errorf("selected plugin %q dependency %s has no resolved canonical contract", binding.PluginID(), identifier)
			}
			dependencies[dependencyIndex] = assemblygen.DependencyInput{
				Capability:   identifier.String(),
				ContractJSON: capability.ContractJSON(),
			}
		}
		inputs[index] = assemblygen.ProviderInput{
			PluginID:      binding.PluginID(),
			ModulePath:    binding.ModulePath(),
			ModuleVersion: binding.ModuleVersion(),
			ImportPath:    binding.ImportPath(),
			Dependencies:  dependencies,
		}
	}
	return inputs, nil
}

func localConfigurationInputs(modulePath string, resolved configurationresolve.Result) ([]configurationgen.Input, error) {
	bindings := resolved.Bindings()
	inputs := make([]configurationgen.Input, 0, len(bindings))
	for _, binding := range bindings {
		if binding.ModulePath() != modulePath {
			continue
		}
		pluginName, found := strings.CutPrefix(binding.ImportPath(), modulePath+"/")
		if !found || pluginName == "" || strings.Contains(pluginName, "/") {
			return nil, fmt.Errorf("local selected plugin %q has non-root import path %q", binding.PluginID(), binding.ImportPath())
		}
		inputs = append(inputs, configurationgen.Input{
			PluginName: pluginName,
			PluginID:   binding.PluginID(),
			Schema:     binding.Schema(),
		})
	}
	return inputs, nil
}

func exposesJavaScript(context generation.Context) bool {
	for _, id := range context.Requirements() {
		capability, exists := context.Capability(id)
		if exists && capability.Exposure().JavaScript {
			return true
		}
	}
	return false
}

type fingerprintDocument struct {
	ModulePath      string                 `json:"module_path"`
	ContextDigest   string                 `json:"context_digest"`
	AliasDigest     string                 `json:"alias_digest"`
	ConfigDigest    string                 `json:"configuration_digest"`
	Passes          int                    `json:"passes"`
	Extensions      []extensionFingerprint `json:"extensions"`
	OutputOwnership json.RawMessage        `json:"output_ownership"`
}

type extensionFingerprint struct {
	PluginID   string   `json:"plugin_id"`
	API        string   `json:"api"`
	Package    string   `json:"package"`
	Namespaces []string `json:"namespaces"`
	Digest     string   `json:"digest"`
}

func generationFingerprint(resolved applicationresolve.Result, output generatedfiles.Output) (string, error) {
	resolution := resolved.Resolution()
	outputs := resolution.Outputs()
	extensions := make([]extensionFingerprint, len(outputs))
	for index, extension := range outputs {
		extensions[index] = extensionFingerprint{
			PluginID:   extension.PluginID(),
			API:        extension.API(),
			Package:    extension.Package(),
			Namespaces: extension.Namespaces(),
			Digest:     extension.Output().Digest(),
		}
	}
	document := fingerprintDocument{
		ModulePath:      resolved.Module().ModulePath(),
		ContextDigest:   resolution.Context().Digest(),
		AliasDigest:     resolution.AliasResolution().Digest(),
		ConfigDigest:    resolved.Configurations().Digest(),
		Passes:          resolution.Passes(),
		Extensions:      extensions,
		OutputOwnership: output.ManifestJSON(),
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode generation fingerprint: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
