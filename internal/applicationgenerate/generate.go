// Package applicationgenerate resolves, renders, checks, and transactionally
// installs one complete filesystem-backed application generation result.
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
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/modulelocate"
)

var (
	// ErrGenerate reports failure to resolve, render, check, install, or
	// validate one generated application tree.
	ErrGenerate = errors.New("generate application")
	// ErrConcurrentChange reports application inputs or extension output that
	// changed after the transaction's desired output was prepared.
	ErrConcurrentChange = errors.New("application changed during generation")
)

// Validator validates the complete application while desired generated files
// are installed but still protected by the transaction rollback boundary.
type Validator func(context.Context, string) error

// Options contains the application location, bounded Go helper settings, and
// operation mode. Check performs a read-only comparison. Validate overrides
// the default read-only `go test ./...` installation validation when non-nil.
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
}

// Result identifies the resolved application and its deterministic generated
// output comparison. A successful installation can retain unexpected unowned
// files, which remain visible in Report rather than being overwritten.
type Result struct {
	module  modulelocate.Module
	report  generatedfiles.Report
	checked bool
}

// Module returns the nearest runnable application Go Module.
func (r Result) Module() modulelocate.Module { return r.module }

// Report returns the final generated-output comparison.
func (r Result) Report() generatedfiles.Report { return r.report }

// Checked reports whether the operation was the read-only check mode.
func (r Result) Checked() bool { return r.checked }

// Generate resolves and renders the nearest runnable application. Check mode
// compares without mutation. Install mode atomically installs desired files,
// validates the updated module, and re-resolves inside the transaction so a
// concurrent source edit or nondeterministic extension result causes rollback.
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
	report, err := generatedfiles.Install(prepared.resolved.Module().Path(), prepared.output, func(root string) error {
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
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrGenerate, err)
	}
	return Result{module: prepared.resolved.Module(), report: report}, nil
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
	output, err := applicationgen.Render(applicationgen.Options{
		ModulePath:        resolved.Module().ModulePath(),
		JavaScriptPackage: javaScriptPackage,
		Configurations:    configurations,
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
