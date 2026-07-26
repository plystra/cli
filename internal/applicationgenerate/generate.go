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
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/assemblygen"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/bootstrapgen"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/connectgen"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/implementationadaptergen"
	"github.com/plystra/cli/internal/implementationassemblygen"
	"github.com/plystra/cli/internal/interfacecompatibility"
	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfaceproxygen"
	"github.com/plystra/cli/internal/intrinsicinterface"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/transporttoolchain"
	kernelintrinsic "github.com/plystra/kernel/intrinsic"
	kernelinvocation "github.com/plystra/kernel/invocation"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
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
	// ErrRuntimeDependency reports generated application source whose normal
	// Go Module runtime requirements are absent, indirect, or too old.
	ErrRuntimeDependency = errors.New("invalid generated application runtime dependency")
)

// Validator validates the complete application while desired generated files
// are installed but still protected by the transaction rollback boundary.
type Validator func(context.Context, string) error

// ModuleRequirement is one direct minimum-version Go Module dependency needed
// by generated application source.
type ModuleRequirement struct {
	path           string
	minimumVersion string
}

// NewModuleRequirement validates one generated runtime dependency contract.
func NewModuleRequirement(modulePath, minimumVersion string) (ModuleRequirement, error) {
	_, pathMajor, pathOK := module.SplitPathVersion(modulePath)
	if module.CheckPath(modulePath) != nil || !pathOK || !semver.IsValid(minimumVersion) || semver.Canonical(minimumVersion) != minimumVersion || module.CheckPathMajor(minimumVersion, pathMajor) != nil {
		return ModuleRequirement{}, fmt.Errorf("%w: invalid minimum requirement %s@%s", ErrRuntimeDependency, modulePath, minimumVersion)
	}
	return ModuleRequirement{path: modulePath, minimumVersion: minimumVersion}, nil
}

// Path returns the required Go Module path.
func (r ModuleRequirement) Path() string { return r.path }

// MinimumVersion returns the oldest supported canonical semantic version.
func (r ModuleRequirement) MinimumVersion() string { return r.minimumVersion }

// ModuleMutation wraps validation and generation-input confirmation while the
// desired generated tree is installed. It lets a higher-level mutating command
// select generated runtime requirements, normalize module-owned metadata, and
// restore that metadata if any later check fails. The supplied operation must
// be called exactly once.
type ModuleMutation func(context.Context, string, []ModuleRequirement, func() error) error

// Options contains the application location, bounded Go helper settings, and
// operation mode. Check performs a read-only comparison. Validate overrides
// the default `go test -mod=readonly ./...` installation validation when
// non-nil. RejectUnexpected makes compound mutating commands fail and roll
// back rather than commit beside unowned generated output.
type Options struct {
	Start                 string
	Check                 bool
	ConfigurationPath     string
	EnvironmentName       string
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
	module                  modulelocate.Module
	report                  generatedfiles.Report
	checked                 bool
	configurationChanged    bool
	configurationPath       string
	maintenancePath         string
	interfaceComparison     interfacecompatibility.Comparison
	metadataComparison      interfacecompatibility.MetadataComparison
	transportComparison     interfacecompatibility.TransportComparison
	javaScriptComparison    interfacecompatibility.JavaScriptComparison
	documentationComparison interfacecompatibility.DocumentationComparison
}

// Module returns the nearest enclosing Go Module.
func (r Result) Module() modulelocate.Module { return r.module }

// Report returns the final generated-output comparison.
func (r Result) Report() generatedfiles.Report { return r.report }

// Checked reports whether the operation was the read-only check mode.
func (r Result) Checked() bool { return r.checked }

// ConfigurationChanged reports dependency-composition drift in the maintained
// current-project document. Check mode reports it without mutation; install
// mode reports that the planned three-way update was committed with generated
// output.
func (r Result) ConfigurationChanged() bool { return r.configurationChanged }

// ConfigurationPath returns the stable Project-relative current-project
// document selected for this operation.
func (r Result) ConfigurationPath() string { return r.configurationPath }

// ConfigurationMaintenancePath returns the dependency-baseline-owned document
// that changed or would change during this operation.
func (r Result) ConfigurationMaintenancePath() string { return r.maintenancePath }

// InterfaceShapeComparison returns the authored Interface Go-shape
// differences observed against the prior owned compatibility baseline.
func (r Result) InterfaceShapeComparison() interfacecompatibility.Comparison {
	return r.interfaceComparison
}

// InterfaceMetadataComparison returns the Interface digest-class differences
// observed against the prior owned metadata compatibility baseline.
func (r Result) InterfaceMetadataComparison() interfacecompatibility.MetadataComparison {
	return r.metadataComparison
}

// InterfaceTransportComparison returns the generated Protobuf descriptor,
// Connect procedure, and wire-map differences observed against the prior owned
// transport compatibility baseline.
func (r Result) InterfaceTransportComparison() interfacecompatibility.TransportComparison {
	return r.transportComparison
}

// InterfaceJavaScriptComparison returns shared package-root plus per-Interface
// generated JavaScript API differences observed against the prior owned
// compatibility baseline.
func (r Result) InterfaceJavaScriptComparison() interfacecompatibility.JavaScriptComparison {
	return r.javaScriptComparison
}

// InterfaceDocumentationComparison returns generated documentation artifact
// differences observed against the prior owned compatibility baseline.
func (r Result) InterfaceDocumentationComparison() interfacecompatibility.DocumentationComparison {
	return r.documentationComparison
}

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
		if err := validateRuntimeRequirements(prepared.resolved, prepared.runtimeRequirements); err != nil {
			return Result{}, fmt.Errorf("%w: %w", ErrGenerate, err)
		}
		report, err := generatedfiles.Check(prepared.resolved.Module().Path(), prepared.output)
		if err != nil {
			return Result{}, fmt.Errorf("%w: %w", ErrGenerate, err)
		}
		return Result{
			module:                  prepared.resolved.Module(),
			report:                  report,
			checked:                 true,
			configurationChanged:    prepared.resolved.ConfigurationMaintenance().Changed(),
			configurationPath:       prepared.resolved.ConfigurationSelection().Path(),
			maintenancePath:         prepared.resolved.ConfigurationMaintenancePath(),
			interfaceComparison:     prepared.interfaceComparison,
			metadataComparison:      prepared.metadataComparison,
			transportComparison:     prepared.transportComparison,
			javaScriptComparison:    prepared.javaScriptComparison,
			documentationComparison: prepared.documentationComparison,
		}, nil
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
	additional := make([]atomicfs.Write, 0, 1)
	maintenance := prepared.resolved.ConfigurationMaintenance()
	if maintenance.Changed() {
		additional = append(additional, atomicfs.Write{
			Path:         prepared.resolved.ConfigurationMaintenancePath(),
			Data:         maintenance.Data(),
			ExpectedData: prepared.resolved.ConfigurationMaintenanceSource(),
		})
	}
	install := generatedfiles.InstallWithWrites
	if options.RejectUnexpected {
		install = generatedfiles.InstallStrictWithWrites
	}
	report, err := install(prepared.resolved.Module().Path(), prepared.output, additional, func(root string) error {
		return runModuleMutation(ctx, options, root, prepared.runtimeRequirements, func() error {
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
			if confirmed.resolved.ConfigurationMaintenance().Changed() {
				return fmt.Errorf("%w: dependency-derived Project configuration remains stale after installation", ErrConcurrentChange)
			}
			if err := validateRuntimeRequirements(confirmed.resolved, confirmed.runtimeRequirements); err != nil {
				return err
			}
			return nil
		})
	})
	if err != nil {
		if errors.Is(err, atomicfs.ErrConcurrentChange) && !errors.Is(err, ErrConcurrentChange) {
			err = errors.Join(ErrConcurrentChange, err)
		}
		return Result{}, fmt.Errorf("%w: %w", ErrGenerate, err)
	}
	return Result{
		module:                  prepared.resolved.Module(),
		report:                  report,
		configurationChanged:    maintenance.Changed(),
		configurationPath:       prepared.resolved.ConfigurationSelection().Path(),
		maintenancePath:         prepared.resolved.ConfigurationMaintenancePath(),
		interfaceComparison:     prepared.interfaceComparison,
		metadataComparison:      prepared.metadataComparison,
		transportComparison:     prepared.transportComparison,
		javaScriptComparison:    prepared.javaScriptComparison,
		documentationComparison: prepared.documentationComparison,
	}, nil
}

func runModuleMutation(ctx context.Context, options Options, root string, requirements []ModuleRequirement, operation func() error) error {
	if options.MutateModule == nil {
		return operation()
	}
	calls := 0
	err := options.MutateModule(ctx, root, append([]ModuleRequirement(nil), requirements...), func() error {
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
	resolved                applicationresolve.Result
	output                  generatedfiles.Output
	runtimeRequirements     []ModuleRequirement
	interfaceComparison     interfacecompatibility.Comparison
	metadataComparison      interfacecompatibility.MetadataComparison
	transportComparison     interfacecompatibility.TransportComparison
	javaScriptComparison    interfacecompatibility.JavaScriptComparison
	documentationComparison interfacecompatibility.DocumentationComparison
	fingerprint             string
}

func prepare(ctx context.Context, options Options, start string) (preparedGeneration, error) {
	resolved, err := applicationresolve.Resolve(ctx, applicationresolve.Options{
		Start:                 start,
		ConfigurationPath:     options.ConfigurationPath,
		EnvironmentName:       options.EnvironmentName,
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
	definitions := resolved.Interfaces().Interfaces()
	contracts := make([]interfacecontract.Contract, 0, len(definitions))
	metadataInputs := make([]interfacecompatibility.MetadataInput, 0, len(definitions))
	for _, definition := range definitions {
		contracts = append(contracts, definition.Contract())
		metadataInputs = append(metadataInputs, interfacecompatibility.MetadataInput{
			ID:                  definition.Contract().ID().String(),
			ContractDigest:      definition.ContractDigest(),
			DocumentationDigest: definition.DocumentationDigest(),
			ExampleDigest:       definition.ExampleDigest(),
		})
	}
	previousInterfaceBaseline, previousInterfaceBaselineExists, err := generatedfiles.ReadOwnedFile(
		resolved.Module().Path(),
		interfacecompatibility.Path,
		interfacecompatibility.MaximumBytes,
	)
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("read prior authored Interface compatibility baseline: %w", err)
	}
	interfaceBaseline, interfaceComparison, err := interfacecompatibility.Reconcile(
		contracts,
		previousInterfaceBaseline,
		previousInterfaceBaselineExists,
	)
	if err != nil {
		return preparedGeneration{}, err
	}
	previousMetadataBaseline, previousMetadataBaselineExists, err := generatedfiles.ReadOwnedFile(
		resolved.Module().Path(),
		interfacecompatibility.MetadataPath,
		interfacecompatibility.MetadataMaximumBytes,
	)
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("read prior Interface metadata compatibility baseline: %w", err)
	}
	metadataBaseline, metadataComparison, err := interfacecompatibility.ReconcileMetadata(
		metadataInputs,
		previousMetadataBaseline,
		previousMetadataBaselineExists,
	)
	if err != nil {
		return preparedGeneration{}, err
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
	httpTransports := resolved.Manifest().HTTPTransports()
	var httpCORS *applicationmeta.HTTPCORS
	if selected, exists := resolved.Manifest().HTTPCORS(); exists {
		httpCORS = &selected
	}
	interfaceProtobufModel, err := interfaceProtobufProjection(ctx, resolved, httpTransports, options)
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("build Interface Protobuf projection: %w", err)
	}
	javaScriptPackage := ""
	if exposesJavaScript(resolved.Resolution().Context()) || len(interfaceProtobufModel.Operations()) != 0 {
		javaScriptPackage, err = javascriptgen.InferPackageName(resolved.Module().ModulePath())
		if err != nil {
			return preparedGeneration{}, err
		}
	}
	javaScriptModel, err := applicationgen.JavaScriptModel(resolved.Resolution())
	if err != nil {
		return preparedGeneration{}, err
	}
	javaScriptAPI, err := javascriptgen.BuildPublicAPI(
		javaScriptPackage,
		javaScriptModel,
		interfaceProtobufModel,
	)
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("build generated JavaScript public API: %w", err)
	}
	previousJavaScriptBaseline, previousJavaScriptBaselineExists, err := generatedfiles.ReadOwnedFile(
		resolved.Module().Path(),
		interfacecompatibility.JavaScriptPath,
		interfacecompatibility.JavaScriptMaximumBytes,
	)
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("read prior Interface JavaScript compatibility baseline: %w", err)
	}
	javaScriptBaseline, javaScriptComparison, err := interfacecompatibility.ReconcileJavaScript(
		javaScriptAPI,
		previousJavaScriptBaseline,
		previousJavaScriptBaselineExists,
	)
	if err != nil {
		return preparedGeneration{}, err
	}
	previousDocumentationData, previousDocumentationExists, err := generatedfiles.ReadOwnedFile(
		resolved.Module().Path(),
		interfacecompatibility.DocumentationPath,
		interfacecompatibility.DocumentationMaximumBytes,
	)
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("read prior Interface documentation compatibility baseline: %w", err)
	}
	interfaceProxies, err := interfaceProxyInputs(resolved, interfaceProtobufModel)
	if err != nil {
		return preparedGeneration{}, err
	}
	implementationAdapters, err := implementationAdapterInputs(resolved)
	if err != nil {
		return preparedGeneration{}, err
	}
	implementationAssembly, err := implementationAssemblyInput(resolved, interfaceProtobufModel, kernelVersion, kernelBuildIdentity)
	if err != nil {
		return preparedGeneration{}, err
	}
	protobufProjection, err := applicationgen.ProtobufProjection(httpTransports, resolved.Resolution())
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("build final Protobuf projection: %w", err)
	}
	previousWireMap, previousWireMapExists, err := generatedfiles.ReadOwnedFile(resolved.Module().Path(), protobufwiremap.Path, protobufwiremap.MaximumBytes)
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("read prior Protobuf wire history: %w", err)
	}
	wireMap, err := protobufwiremap.Build(
		protobufProjection,
		interfaceProtobufModel,
		previousWireMap,
		previousWireMapExists,
		resolved.PreviousManifestProvenance().ProtobufWireMapDigest(),
	)
	if err != nil {
		return preparedGeneration{}, err
	}
	descriptorEvidence, err := protobufdescriptor.BuildWithInterfaces(
		protobufProjection,
		wireMap,
		interfaceProtobufModel,
	)
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("build Protobuf descriptor evidence: %w", err)
	}
	previousTransportBaseline, previousTransportBaselineExists, err := generatedfiles.ReadOwnedFile(
		resolved.Module().Path(),
		interfacecompatibility.TransportPath,
		interfacecompatibility.TransportMaximumBytes,
	)
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("read prior Interface transport compatibility baseline: %w", err)
	}
	transportBaseline, transportComparison, err := interfacecompatibility.ReconcileTransport(
		wireMap,
		descriptorEvidence,
		previousTransportBaseline,
		previousTransportBaselineExists,
	)
	if err != nil {
		return preparedGeneration{}, err
	}
	runtimeRequirements, err := generatedRuntimeRequirements(protobufProjection, interfaceProtobufModel)
	if err != nil {
		return preparedGeneration{}, err
	}
	modelDigest, err := applicationgen.ApplicationModelDigest(applicationgen.ApplicationModelOptions{
		ModulePath:             resolved.Module().ModulePath(),
		JavaScriptPackage:      javaScriptPackage,
		KernelModuleVersion:    kernelVersion,
		KernelBuildIdentity:    kernelBuildIdentity,
		HTTPTransports:         httpTransports,
		HTTPCORS:               httpCORS,
		Configurations:         configurations,
		Providers:              providers,
		InterfaceProxies:       interfaceProxies,
		ImplementationAdapters: implementationAdapters,
		ImplementationAssembly: implementationAssembly,
		InterfacePolicies:      resolved.Manifest().InterfacePolicies(),
		Resolution:             resolved.Resolution(),
		ProtobufWireMap:        wireMap,
		InterfaceProtobufModel: interfaceProtobufModel,
	})
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("digest final application model: %w", err)
	}
	selection := resolved.ConfigurationSelection()
	selectedData := resolved.ConfigurationMaintenance().Data()
	if selection.Mode() == applicationgen.ConfigurationModeEnvironment {
		selectedData = resolved.ConfigurationSource()
	}
	toolchain, err := transporttoolchain.Current()
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("identify embedded transport toolchain: %w", err)
	}
	provenance, err := applicationgen.NewManifestProvenance(applicationgen.ManifestProvenanceOptions{
		Mode:                   selection.Mode(),
		Environment:            selection.Environment(),
		RootPath:               "plystra.yaml",
		RootData:               resolved.RootConfigurationData(),
		SelectedPath:           selection.Path(),
		SelectedData:           selectedData,
		Composition:            resolved.Composition(),
		ProtobufWireMapDigest:  wireMap.Digest(),
		ApplicationModelDigest: modelDigest,
		TransportToolchain:     toolchain,
		Previous:               resolved.PreviousManifestProvenance(),
	})
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("construct application manifest provenance: %w", err)
	}
	output, err := applicationgen.Render(applicationgen.Options{
		ModulePath:             resolved.Module().ModulePath(),
		JavaScriptPackage:      javaScriptPackage,
		KernelModuleVersion:    kernelVersion,
		KernelBuildIdentity:    kernelBuildIdentity,
		HTTPTransports:         httpTransports,
		HTTPCORS:               httpCORS,
		Composition:            resolved.Composition(),
		ManifestProvenance:     provenance,
		Configurations:         configurations,
		Providers:              providers,
		InterfaceProxies:       interfaceProxies,
		ImplementationAdapters: implementationAdapters,
		ImplementationAssembly: implementationAssembly,
		InterfaceCompatibility: interfaceBaseline,
		InterfaceMetadata:      metadataBaseline,
		InterfaceTransport:     transportBaseline,
		InterfaceJavaScript:    javaScriptBaseline,
		InterfaceProtobufModel: interfaceProtobufModel,
		ProtobufWireMap:        wireMap,
	}, resolved.Resolution())
	if err != nil {
		return preparedGeneration{}, err
	}
	var currentDocumentationData []byte
	for _, file := range output.Files() {
		if file.Path() == interfacecompatibility.DocumentationPath {
			currentDocumentationData = file.Data()
			break
		}
	}
	if len(currentDocumentationData) == 0 {
		return preparedGeneration{}, errors.New("rendered output omits the Interface documentation compatibility baseline")
	}
	currentDocumentationBaseline, err := interfacecompatibility.DecodeDocumentation(currentDocumentationData)
	if err != nil {
		return preparedGeneration{}, fmt.Errorf("restore rendered Interface documentation compatibility baseline: %w", err)
	}
	previousDocumentationBaseline, err := interfacecompatibility.NewDocumentation(nil)
	if err != nil {
		return preparedGeneration{}, err
	}
	if previousDocumentationExists {
		previousDocumentationBaseline, err = interfacecompatibility.DecodeDocumentation(previousDocumentationData)
		if err != nil {
			return preparedGeneration{}, err
		}
	} else if len(previousDocumentationData) != 0 {
		return preparedGeneration{}, fmt.Errorf("%w: absent prior documentation record has bytes", interfacecompatibility.ErrHistory)
	}
	documentationComparison, err := interfacecompatibility.CompareDocumentation(
		previousDocumentationBaseline,
		currentDocumentationBaseline,
	)
	if err != nil {
		return preparedGeneration{}, err
	}
	fingerprint, err := generationFingerprint(resolved, output)
	if err != nil {
		return preparedGeneration{}, err
	}
	return preparedGeneration{
		resolved:                resolved,
		output:                  output,
		runtimeRequirements:     runtimeRequirements,
		interfaceComparison:     interfaceComparison,
		metadataComparison:      metadataComparison,
		transportComparison:     transportComparison,
		javaScriptComparison:    javaScriptComparison,
		documentationComparison: documentationComparison,
		fingerprint:             fingerprint,
	}, nil
}

func interfaceProtobufProjection(ctx context.Context, resolved applicationresolve.Result, transports applicationmeta.HTTPTransports, options Options) (protobufmodel.InterfaceModel, error) {
	if !transports.Connect {
		return protobufmodel.BuildInterfaces(false, nil)
	}
	definitions := make(map[string]protobufmodel.InterfaceInput)
	for _, definition := range resolved.Interfaces().Interfaces() {
		contract := definition.Contract()
		identifier := contract.ID().String()
		if _, duplicate := definitions[identifier]; duplicate {
			return protobufmodel.InterfaceModel{}, fmt.Errorf("canonical Interface %s has more than one visible definition while projecting Protobuf messages", identifier)
		}
		semanticErrors := definition.SemanticErrors()
		errorCodes := make([]string, len(semanticErrors))
		for index, semanticError := range semanticErrors {
			errorCodes[index] = semanticError.Code()
		}
		definitions[identifier] = protobufmodel.InterfaceInput{
			InterfaceID:    contract.ID(),
			PackagePath:    contract.PackagePath(),
			Source:         definition.Source(),
			MetadataSource: definition.MetadataSource(),
			Contract:       contract,
			ContractDigest: definition.ContractDigest(),
			SemanticErrors: errorCodes,
		}
	}

	exposures := resolved.Manifest().HTTPExposures()
	intrinsicIDs := make([]string, 0, len(exposures))
	for _, exposure := range exposures {
		if strings.HasPrefix(exposure.ID().Name(), "kernel.") {
			intrinsicIDs = append(intrinsicIDs, exposure.ID().String())
		}
	}
	intrinsics, err := intrinsicInterfaceProtobufInputs(ctx, resolved, intrinsicIDs, options)
	if err != nil {
		return protobufmodel.InterfaceModel{}, err
	}
	for _, input := range intrinsics {
		identifier := input.InterfaceID.String()
		if previous, duplicate := definitions[identifier]; duplicate {
			return protobufmodel.InterfaceModel{}, fmt.Errorf("intrinsic Interface %s at %s duplicates visible authored definition at %s", identifier, input.Source, previous.Source)
		}
		definitions[identifier] = input
	}

	inputs := make([]protobufmodel.InterfaceInput, 0, len(exposures))
	for _, exposure := range exposures {
		identifier := exposure.ID()
		input, exists := definitions[identifier.String()]
		if !exists {
			// Pre-Gate-14 legacy-only exposures are deliberately excluded by
			// resolveInterfaces and remain on the existing transport path.
			continue
		}
		inputs = append(inputs, input)
	}
	return protobufmodel.BuildInterfaces(true, inputs)
}

func intrinsicInterfaceProtobufInputs(
	ctx context.Context,
	resolved applicationresolve.Result,
	identifiers []string,
	options Options,
) ([]protobufmodel.InterfaceInput, error) {
	if len(identifiers) == 0 {
		return nil, nil
	}
	dependency, exists := resolved.Dependencies().ByPath(kernelintrinsic.ModulePath)
	if !exists {
		return nil, fmt.Errorf("selected application graph omits intrinsic Interface module %s", kernelintrinsic.ModulePath)
	}

	expected := make(map[string]intrinsicinterface.Definition, len(identifiers))
	packagePaths := make([]string, 0, len(identifiers))
	selectedPackages := make(map[string]struct{}, len(identifiers))
	for _, value := range identifiers {
		identifier, err := interfaceid.Parse(value)
		if err != nil {
			return nil, fmt.Errorf("intrinsic Interface exposure %q is invalid: %v", value, err)
		}
		definition, exists := intrinsicinterface.Lookup(identifier)
		if !exists {
			return nil, fmt.Errorf("intrinsic Interface exposure %s is absent from the selected Kernel API", identifier)
		}
		if _, duplicate := expected[value]; duplicate {
			return nil, fmt.Errorf("intrinsic Interface exposure %s appears more than once", identifier)
		}
		expected[value] = definition
		if _, selected := selectedPackages[definition.PackagePath()]; !selected {
			selectedPackages[definition.PackagePath()] = struct{}{}
			packagePaths = append(packagePaths, definition.PackagePath())
		}
	}

	discovered, err := interfaceinventory.DiscoverExactInterfaces(ctx, interfaceinventory.ExactInterfacePackages{
		ModulePath:            dependency.Path(),
		ModuleVersion:         dependency.SelectedVersion(),
		ModuleRoot:            dependency.Root(),
		ApplicationModulePath: resolved.Module().ModulePath(),
		PackagePaths:          packagePaths,
	}, interfaceinventory.Options{
		GoCommand:   options.GoCommand,
		Environment: append([]string(nil), options.Environment...),
		OutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("load selected Kernel Interface packages: %w", err)
	}
	interfaces := discovered.Interfaces()
	inputs := make([]protobufmodel.InterfaceInput, 0, len(expected))
	for _, discovered := range interfaces {
		definition, exists := expected[discovered.ID()]
		if !exists {
			identifier, parseErr := interfaceid.Parse(discovered.ID())
			known, knownDefinition := intrinsicinterface.Lookup(identifier)
			if parseErr == nil && knownDefinition && known.PackagePath() == discovered.PackagePath() {
				continue
			}
			return nil, fmt.Errorf("selected Kernel package %s declares unexpected intrinsic Interface %s", discovered.PackagePath(), discovered.ID())
		}
		if discovered.ModulePath() != kernelintrinsic.ModulePath || discovered.PackagePath() != definition.PackagePath() {
			return nil, fmt.Errorf(
				"intrinsic Interface %s resolved to package %s in module %s; selected Kernel API requires %s in %s",
				discovered.ID(),
				discovered.PackagePath(),
				discovered.ModulePath(),
				definition.PackagePath(),
				kernelintrinsic.ModulePath,
			)
		}
		semanticErrors := discovered.SemanticErrors()
		errorCodes := make([]string, len(semanticErrors))
		for errorIndex, semanticError := range semanticErrors {
			errorCodes[errorIndex] = semanticError.Code()
		}
		contract := discovered.Contract()
		inputs = append(inputs, protobufmodel.InterfaceInput{
			InterfaceID:    contract.ID(),
			PackagePath:    contract.PackagePath(),
			Source:         discovered.Source(),
			MetadataSource: discovered.MetadataSource(),
			Contract:       contract,
			ContractDigest: discovered.ContractDigest(),
			SemanticErrors: errorCodes,
		})
	}
	if len(inputs) != len(expected) {
		return nil, fmt.Errorf("selected Kernel exposes %d requested Interface definitions for %d intrinsic exposures", len(inputs), len(expected))
	}
	return inputs, nil
}

func generatedRuntimeRequirements(model protobufmodel.Model, interfaces protobufmodel.InterfaceModel) ([]ModuleRequirement, error) {
	inputs := make([][2]string, 0, 3)
	if len(model.Operations()) != 0 || len(model.Aliases()) != 0 || len(interfaces.Operations()) != 0 {
		inputs = append(inputs,
			[2]string{connectgen.ConnectModulePath, connectgen.ConnectModuleVersion},
			[2]string{connectgen.ProtobufModulePath, connectgen.ProtobufModuleVersion},
		)
	}
	inputs = append(inputs, [2]string{bootstrapgen.YAMLModulePath, bootstrapgen.YAMLModuleVersion})
	result := make([]ModuleRequirement, len(inputs))
	for index, input := range inputs {
		requirement, err := NewModuleRequirement(input[0], input[1])
		if err != nil {
			return nil, err
		}
		result[index] = requirement
	}
	return result, nil
}

func validateRuntimeRequirements(resolved applicationresolve.Result, requirements []ModuleRequirement) error {
	for _, requirement := range requirements {
		dependency, exists := resolved.Dependencies().ByPath(requirement.Path())
		if !exists || !dependency.Direct() || dependency.Indirect() || !semver.IsValid(dependency.RequiredVersion()) || semver.Compare(dependency.RequiredVersion(), requirement.MinimumVersion()) < 0 {
			return fmt.Errorf(
				"%w: go.mod must directly require %s at %s or newer without // indirect; run plystra generate to repair module metadata transactionally",
				ErrRuntimeDependency,
				requirement.Path(),
				requirement.MinimumVersion(),
			)
		}
	}
	return nil
}

func kernelBuildProvenance(resolved applicationresolve.Result) (string, string, error) {
	dependency, exists := resolved.Dependencies().ByPath(kernelintrinsic.ModulePath)
	if !exists || !dependency.Direct() {
		return "", "", fmt.Errorf("%w: go.mod must directly require %s", ErrKernelDependency, kernelintrinsic.ModulePath)
	}
	identity := ""
	if dependency.SelectedVersion() == "" {
		identity = resolved.Resolution().Context().BuildModelDigest()
	} else if _, replaced := dependency.Replacement(); replaced {
		identity = resolved.Resolution().Context().BuildModelDigest()
	}
	if _, err := kernelinvocation.NewModuleBuild(kernelintrinsic.ModulePath, dependency.SelectedVersion(), identity); err != nil {
		return "", "", fmt.Errorf("%w: selected %s build provenance: %v", ErrKernelDependency, kernelintrinsic.ModulePath, err)
	}
	return dependency.SelectedVersion(), identity, nil
}

func interfaceProxyInputs(resolved applicationresolve.Result, interfaceModel protobufmodel.InterfaceModel) ([]interfaceproxygen.Input, error) {
	visible := resolved.Interfaces().Interfaces()
	definitions := make(map[string]interfaceproxygen.Input, len(visible))
	for _, definition := range visible {
		contract := definition.Contract()
		identifier := contract.ID().String()
		if _, duplicate := definitions[identifier]; duplicate {
			return nil, fmt.Errorf("canonical Interface %s has more than one visible definition while generating typed proxies", identifier)
		}
		definitions[identifier] = interfaceproxygen.Input{
			InterfaceID:  contract.ID(),
			PackagePath:  contract.PackagePath(),
			MethodName:   contract.MethodName(),
			RequestName:  contract.RequestName(),
			ResponseName: contract.ResponseName(),
		}
	}

	selections := resolved.InterfaceResolution().Selections()
	inputs := make([]interfaceproxygen.Input, 0, len(selections)+len(interfaceModel.Operations()))
	for _, selection := range selections {
		input, exists := definitions[selection.InterfaceID.String()]
		if !exists {
			return nil, fmt.Errorf("reachable Interface %s has no canonical authored contract for typed proxy generation", selection.InterfaceID)
		}
		inputs = append(inputs, input)
	}
	for _, operation := range interfaceModel.Operations() {
		if !strings.HasPrefix(operation.ID().Name(), "kernel.") {
			continue
		}
		if _, duplicate := definitions[operation.ID().String()]; duplicate {
			return nil, fmt.Errorf("intrinsic Interface %s duplicates an authored Interface while generating typed proxies", operation.ID())
		}
		inputs = append(inputs, interfaceproxygen.Input{
			InterfaceID:  operation.ID(),
			PackagePath:  operation.PackagePath(),
			MethodName:   operation.MethodName(),
			RequestName:  operation.RequestGoName(),
			ResponseName: operation.ResponseGoName(),
		})
	}
	return inputs, nil
}

func implementationAdapterInputs(resolved applicationresolve.Result) ([]implementationadaptergen.Input, error) {
	visible := resolved.Interfaces().Interfaces()
	definitions := make(map[string]implementationadaptergen.Input, len(visible))
	for _, definition := range visible {
		contract := definition.Contract()
		identifier := contract.ID().String()
		if _, duplicate := definitions[identifier]; duplicate {
			return nil, fmt.Errorf("canonical Interface %s has more than one visible definition while generating Implementation adapters", identifier)
		}
		semanticErrors := definition.SemanticErrors()
		errorCodes := make([]string, len(semanticErrors))
		for index, semanticError := range semanticErrors {
			errorCodes[index] = semanticError.Code()
		}
		definitions[identifier] = implementationadaptergen.Input{
			InterfaceID:    contract.ID(),
			PackagePath:    contract.PackagePath(),
			MethodName:     contract.MethodName(),
			RequestName:    contract.RequestName(),
			ResponseName:   contract.ResponseName(),
			SemanticErrors: errorCodes,
		}
	}

	selections := resolved.InterfaceResolution().Selections()
	inputs := make([]implementationadaptergen.Input, len(selections))
	for index, selection := range selections {
		input, exists := definitions[selection.InterfaceID.String()]
		if !exists {
			return nil, fmt.Errorf("reachable Interface %s has no canonical authored contract for Implementation adapter generation", selection.InterfaceID)
		}
		implementation, exists := resolved.Implementations().BySymbol(selection.Constructor)
		if !exists {
			return nil, fmt.Errorf("reachable Interface %s selects absent Implementation constructor %s while generating its adapter", selection.InterfaceID, selection.Constructor)
		}
		input.Constructor = selection.Constructor
		input.ConcreteType = implementation.ConcreteType().String()
		inputs[index] = input
	}
	return inputs, nil
}

func implementationAssemblyInput(resolved applicationresolve.Result, interfaceModel protobufmodel.InterfaceModel, kernelVersion, kernelBuildIdentity string) (implementationassemblygen.Options, error) {
	type interfaceDefinition struct {
		packagePath string
		digest      [sha256.Size]byte
	}
	definitions := make(map[string]interfaceDefinition)
	for _, visible := range resolved.Interfaces().Interfaces() {
		identifier := visible.Contract().ID().String()
		if _, duplicate := definitions[identifier]; duplicate {
			return implementationassemblygen.Options{}, fmt.Errorf("canonical Interface %s has more than one visible definition while generating static assembly", identifier)
		}
		digest, err := decodeSemanticDigest(visible.ContractDigest())
		if err != nil {
			return implementationassemblygen.Options{}, fmt.Errorf("canonical Interface %s contract digest: %v", identifier, err)
		}
		definitions[identifier] = interfaceDefinition{packagePath: visible.Contract().PackagePath(), digest: digest}
	}

	graph := resolved.InterfaceResolution().Graph()
	graphBindings := graph.Bindings()
	bindings := make([]implementationassemblygen.BindingInput, len(graphBindings))
	for index, binding := range graphBindings {
		definition, exists := definitions[binding.InterfaceID().String()]
		if !exists {
			return implementationassemblygen.Options{}, fmt.Errorf("reachable Interface %s has no canonical contract while generating static assembly", binding.InterfaceID())
		}
		reason := implementationassemblygen.SelectionReason("")
		switch binding.Reason() {
		case constructorgraph.SelectionExplicit:
			reason = implementationassemblygen.SelectionExplicit
		case constructorgraph.SelectionUnique:
			reason = implementationassemblygen.SelectionUniqueCompatible
		default:
			return implementationassemblygen.Options{}, fmt.Errorf("reachable Interface %s has unsupported selection reason %q", binding.InterfaceID(), binding.Reason())
		}
		bindings[index] = implementationassemblygen.BindingInput{
			InterfaceID:     binding.InterfaceID(),
			PackagePath:     definition.packagePath,
			Constructor:     binding.Constructor(),
			SelectionReason: reason,
			ContractDigest:  definition.digest,
		}
	}

	nodes := graph.ConstructionOrder()
	constructors := make([]implementationassemblygen.ConstructorInput, len(nodes))
	for index, node := range nodes {
		implementation := node.Implementation()
		_, hasConfiguration := implementation.Configuration()
		dependencies := node.Dependencies()
		inputs := make([]implementationassemblygen.DependencyInput, len(dependencies))
		for dependencyIndex, dependency := range dependencies {
			inputs[dependencyIndex] = implementationassemblygen.DependencyInput{
				InterfaceID:       dependency.InterfaceID(),
				PackagePath:       dependency.PackagePath(),
				ParameterName:     dependency.ParameterName(),
				ParameterPosition: dependency.ParameterPosition(),
				Optional:          dependency.Optional(),
				Available:         dependency.Available(),
			}
		}
		constructors[index] = implementationassemblygen.ConstructorInput{
			Symbol:           node.Symbol(),
			ModulePath:       implementation.ModulePath(),
			ModuleVersion:    implementation.ModuleVersion(),
			HasConfiguration: hasConfiguration,
			Dependencies:     inputs,
		}
	}
	intrinsics := make([]implementationassemblygen.IntrinsicBindingInput, 0, len(interfaceModel.Operations()))
	for _, operation := range interfaceModel.Operations() {
		if !strings.HasPrefix(operation.ID().Name(), "kernel.") {
			continue
		}
		intrinsics = append(intrinsics, implementationassemblygen.IntrinsicBindingInput{
			InterfaceID: operation.ID(),
			PackagePath: operation.PackagePath(),
			MethodName:  operation.MethodName(),
		})
	}

	return implementationassemblygen.Options{
		ModulePath:               resolved.Module().ModulePath(),
		ApplicationBuildIdentity: resolved.Resolution().Context().BuildModelDigest(),
		KernelModuleVersion:      kernelVersion,
		KernelBuildIdentity:      kernelBuildIdentity,
		DefaultTimeout:           applicationmeta.DefaultInvocationTimeout,
		Bindings:                 bindings,
		IntrinsicBindings:        intrinsics,
		Constructors:             constructors,
	}, nil
}

func decodeSemanticDigest(value string) ([sha256.Size]byte, error) {
	encoded, found := strings.CutPrefix(value, "sha256:")
	if !found || len(encoded) != sha256.Size*2 {
		return [sha256.Size]byte{}, fmt.Errorf("expected canonical sha256 digest")
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != sha256.Size {
		return [sha256.Size]byte{}, fmt.Errorf("expected canonical sha256 digest")
	}
	var digest [sha256.Size]byte
	copy(digest[:], decoded)
	if digest == [sha256.Size]byte{} {
		return [sha256.Size]byte{}, fmt.Errorf("digest must not be zero")
	}
	return digest, nil
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
			PluginID:            binding.PluginID(),
			ModulePath:          binding.ModulePath(),
			ModuleVersion:       binding.ModuleVersion(),
			ImportPath:          binding.ImportPath(),
			ConfigurationSchema: binding.Schema(),
			Dependencies:        dependencies,
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
	ModulePath                  string                 `json:"module_path"`
	ConfigurationMode           string                 `json:"configuration_mode"`
	ConfigurationEnvironment    string                 `json:"configuration_environment,omitempty"`
	ConfigurationPath           string                 `json:"configuration_path"`
	SelectedConfigurationDigest string                 `json:"selected_configuration_digest"`
	PrivateConfigurationDigest  string                 `json:"private_configuration_digest"`
	ContextDigest               string                 `json:"context_digest"`
	AliasDigest                 string                 `json:"alias_digest"`
	Passes                      int                    `json:"passes"`
	Extensions                  []extensionFingerprint `json:"extensions"`
	OutputOwnership             json.RawMessage        `json:"output_ownership"`
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
		ModulePath:                  resolved.Module().ModulePath(),
		ConfigurationMode:           resolved.ConfigurationSelection().Mode(),
		ConfigurationEnvironment:    resolved.ConfigurationSelection().Environment(),
		ConfigurationPath:           resolved.ConfigurationSelection().Path(),
		SelectedConfigurationDigest: resolved.ConfigurationSelection().Digest(),
		PrivateConfigurationDigest:  resolved.Configurations().Digest(),
		ContextDigest:               resolution.Context().Digest(),
		AliasDigest:                 resolution.AliasResolution().Digest(),
		Passes:                      resolution.Passes(),
		Extensions:                  extensions,
		OutputOwnership:             output.ManifestJSON(),
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode generation fingerprint: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
