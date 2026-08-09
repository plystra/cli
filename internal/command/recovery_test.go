package command

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/capabilitycreate"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/generationactivation"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/implementationcreate"
	"github.com/plystra/cli/internal/implementationdecl"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacecreate"
	"github.com/plystra/cli/internal/interfacedecl"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfacemeta"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/newproject"
	"github.com/plystra/cli/internal/pluginindex"
	"github.com/plystra/cli/internal/pluginmeta"
	"github.com/plystra/cli/internal/plugintarget"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/protobufidentity"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/providerresolution"
)

func TestWriteCommandFailureAddsOnePrimaryRecoveryForCommonTypedFailures(t *testing.T) {
	t.Parallel()

	missing, ambiguous, invalidChoice, contractConflict := recoveryProviderFailures(t)
	tests := []struct {
		name    string
		err     error
		context recoveryContext
		want    string
		code    string
	}{
		{
			name: "invalid template with nested Provider ambiguity",
			err: fmt.Errorf(
				"%w: template cannot qualify: %w; correction: publish a corrected template version",
				newproject.ErrInvalidTemplate,
				ambiguous,
			),
			want: "publish a corrected template version",
			code: diagnosticTemplateInvalid,
		},
		{
			name: "missing Provider",
			err:  missing,
			want: "Add an intended dependency with `plystra add <go-module-query>` whose Plugin provides email.send/v1.",
			code: diagnosticProviderMissing,
		},
		{
			name:    "ambiguous Provider",
			err:     ambiguous,
			context: commandRecoveryContext("", "production", nil),
			want:    "Select one compatible Provider explicitly by running `plystra use email.send/v1 <plugin-id> --env \"production\"`.",
			code:    diagnosticProviderAmbiguous,
		},
		{
			name:    "invalid Provider choice",
			err:     invalidChoice,
			context: commandRecoveryContext("deploy/customer.yaml", "", nil),
			want:    "Replace the invalid Provider choice with one visible compatible Plugin by running `plystra use email.send/v1 <plugin-id> --config \"deploy/customer.yaml\"`.",
			code:    diagnosticProviderSelectionInvalid,
		},
		{
			name: "Provider contract conflict",
			err:  contractConflict,
			want: "Make every Provider of email.send/v1 carry one identical provider-independent capability.yaml.",
			code: diagnosticProviderContractMismatch,
		},
		{
			name:    "inherited configuration conflict",
			err:     fmt.Errorf("compose dependencies: %w", applicationmeta.ErrInheritedConflict),
			context: commandRecoveryContext("", "test", nil),
			want:    "Set or remove the conflicting field explicitly in plystra.test.yaml, then rerun the command.",
			code:    diagnosticConfigurationInheritedConflict,
		},
		{
			name:    "constructor configuration schema",
			err:     fmt.Errorf("compose configuration: %w", applicationmeta.ErrConfigurationSchema),
			context: commandRecoveryContext("", "test", nil),
			want:    "Use the fully qualified symbol of a discovered constructor with a compiled Go Config schema in plystra.test.yaml, or remove that constructor configuration entry, then rerun the command.",
			code:    diagnosticConstructorConfigurationSchemaInvalid,
		},
		{
			name:    "constructor configuration value",
			err:     fmt.Errorf("parse configuration: %w", applicationmeta.ErrConfigurationValues),
			context: commandRecoveryContext("deploy/customer.yaml", "", nil),
			want:    "Correct the reported constructor configuration field in deploy/customer.yaml to match its compiled Go Config field type, then rerun the command.",
			code:    diagnosticConstructorConfigurationValuesInvalid,
		},
		{
			name: "configuration selection",
			err:  fmt.Errorf("resolve: %w", applicationresolve.ErrConfigurationSelection),
			want: "Select exactly one existing Project configuration with `--env <environment>` or `--config <yaml-path>`, then rerun the command.",
			code: diagnosticConfigurationSelectionInvalid,
		},
		{
			name:    "application runtime dependency",
			err:     fmt.Errorf("%w: go.mod is stale; run plystra generate to repair module metadata transactionally", applicationgenerate.ErrRuntimeDependency),
			context: commandRecoveryContext("", "production", nil),
			want:    "Run `plystra generate --env \"production\"` to repair the required direct application runtime dependencies.",
			code:    diagnosticApplicationDependencyDrift,
		},
		{
			name: "invalid selected configuration",
			err: errors.Join(
				applicationresolve.ErrConfigurationSelection,
				applicationresolve.ErrManifest,
				applicationmeta.ErrInvalidManifest,
			),
			context: commandRecoveryContext("", "production", nil),
			want:    "Edit plystra.production.yaml so every value matches a selected Plugin's closed typed schema, then rerun the command.",
			code:    diagnosticConfigurationInvalid,
		},
		{
			name: "invalid dependency Project manifest",
			err:  errors.Join(applicationresolve.ErrManifest, applicationmeta.ErrInvalidManifest),
			want: "Correct the reported root or dependency Project plystra.yaml, then rerun the command.",
			code: diagnosticProjectManifestInvalid,
		},
		{
			name: "Plugin targeting",
			err:  fmt.Errorf("author Capability: %w", plugintarget.ErrAmbiguous),
			want: "Rerun with `--plugin <plugin-directory-or-id>` to select one exact local Plugin.",
			code: diagnosticPluginTargetAmbiguous,
		},
		{
			name: "generation helper failure",
			err:  fmt.Errorf("run extension: %w", generationexec.ErrCompile),
			want: "Fix the selected generation package reported above, then rerun the command.",
			code: diagnosticGenerationCompileFailed,
		},
		{
			name:    "generated ownership",
			err:     fmt.Errorf("install: %w", generatedfiles.ErrUnexpected),
			context: commandRecoveryContext("", "staging", nil),
			want:    "Move the reported unowned path outside generated/, then run `plystra generate --env \"staging\"`.",
			code:    diagnosticGeneratedUnexpectedOutput,
		},
		{
			name: "Protobuf history",
			err:  fmt.Errorf("allocate fields: %w", protobufwiremap.ErrHistory),
			want: "Restore generated/proto/wire-map.json from its last known-good generated state, then regenerate.",
			code: diagnosticProtobufWireHistoryInvalid,
		},
		{
			name: "Capability confirmation",
			err:  fmt.Errorf("create Capability: %w", capabilitycreate.ErrConfirmationRequired),
			want: "Review the visible Capability versions, then rerun the create command with `--confirm`.",
			code: diagnosticCapabilityConfirmationRequired,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output strings.Builder
			writeCommandFailure(&output, "command failed", test.err, test.context)
			got := output.String()
			if !strings.Contains(got, "command failed: ") || !strings.Contains(got, "\n\nRecovery:\n"+test.want+"\n\nDiagnostic: "+test.code+"\n") {
				t.Fatalf("writeCommandFailure() = %q, want recovery %q and code %q", got, test.want, test.code)
			}
			if count := strings.Count(got, "Recovery:"); count != 1 {
				t.Fatalf("Recovery count = %d in %q", count, got)
			}
			if count := strings.Count(got, "Diagnostic:"); count != 1 {
				t.Fatalf("Diagnostic count = %d in %q", count, got)
			}
			if strings.Contains(got, "correction:") {
				t.Fatalf("embedded recovery was not removed: %q", got)
			}
		})
	}
}

func TestPrimaryActionableDiagnosticAssignsStableCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "Project manifest", err: applicationresolve.ErrManifest, code: diagnosticcode.ProjectManifestInvalid},
		{name: "inherited configuration conflict", err: applicationmeta.ErrInheritedConflict, code: diagnosticcode.ConfigurationInheritedConflict},
		{name: "configuration ownership", err: applicationmeta.ErrAmbiguousConfigurationOwnership, code: diagnosticcode.ConfigurationOwnershipAmbiguous},
		{name: "HTTP transport", err: applicationmeta.ErrHTTPTransportSelection, code: diagnosticcode.HTTPTransportSelectionInvalid},
		{name: "constructor configuration schema", err: applicationmeta.ErrConfigurationSchema, code: diagnosticcode.ConstructorConfigurationSchemaInvalid},
		{name: "constructor configuration values", err: applicationmeta.ErrConfigurationValues, code: diagnosticcode.ConstructorConfigurationValuesInvalid},
		{name: "environment overlay", err: applicationmeta.ErrApplyOverlay, code: diagnosticcode.EnvironmentOverlayInvalid},
		{name: "current configuration", err: applicationmeta.ErrInvalidManifest, code: diagnosticcode.ConfigurationInvalid},
		{name: "resolved configuration", err: configurationresolve.ErrInvalidConfiguration, code: diagnosticcode.ConfigurationInvalid},
		{name: "unselected Plugin configuration", err: configurationresolve.ErrUnselectedConfiguration, code: diagnosticcode.PluginConfigurationUnselected},
		{name: "missing configured Plugin", err: configurationresolve.ErrMissingPlugin, code: diagnosticcode.PluginConfigurationPluginMissing},
		{name: "configuration selection", err: applicationresolve.ErrConfigurationSelection, code: diagnosticcode.ConfigurationSelectionInvalid},
		{name: "Project marker", err: projectlocate.ErrInvalidManifest, code: diagnosticcode.ProjectManifestInvalid},
		{name: "Kernel dependency drift", err: applicationgenerate.ErrKernelDependency, code: diagnosticcode.ApplicationDependencyDrift},
		{name: "runtime dependency drift", err: applicationgenerate.ErrRuntimeDependency, code: diagnosticcode.ApplicationDependencyDrift},
		{name: "Project not found", err: projectlocate.ErrNotFound, code: diagnosticcode.ProjectNotFound},
		{name: "Go Module not found", err: modulelocate.ErrNotFound, code: diagnosticcode.GoModuleNotFound},
		{name: "located go.mod invalid", err: modulelocate.ErrInvalidGoMod, code: diagnosticcode.GoModuleInvalid},
		{name: "dependency go.mod invalid", err: moduledependency.ErrInvalidGoMod, code: diagnosticcode.GoModuleInvalid},
		{name: "module unavailable", err: moduledependency.ErrModuleUnavailable, code: diagnosticcode.GoModuleUnavailable},
		{name: "Go command", err: gocommand.ErrRun, code: diagnosticcode.GoCommandFailed},
		{name: "Plugin target ambiguous", err: plugintarget.ErrAmbiguous, code: diagnosticcode.PluginTargetAmbiguous},
		{name: "Plugin target missing", err: plugintarget.ErrNotFound, code: diagnosticcode.PluginTargetNotFound},
		{name: "Plugin target invalid", err: plugintarget.ErrSelection, code: diagnosticcode.PluginTargetInvalid},
		{name: "generation activation conflict", err: generationactivation.ErrAssociationConflict, code: diagnosticcode.GenerationActivationConflict},
		{name: "generation activation missing", err: generationactivation.ErrMissingAssociation, code: diagnosticcode.GenerationActivationMissing},
		{name: "generation Provider extension", err: generationactivation.ErrSelectedProviderExtension, code: diagnosticcode.GenerationProviderExtensionMissing},
		{name: "generation activation cycle", err: generationresolution.ErrActivationCycle, code: diagnosticcode.GenerationActivationCycle},
		{name: "generation dependency cycle", err: generationresolution.ErrDependencyCycle, code: diagnosticcode.GenerationDependencyCycle},
		{name: "generation contribution cycle", err: generationresolution.ErrContributionCycle, code: diagnosticcode.GenerationContributionCycle},
		{name: "generation contribution order", err: generationresolution.ErrUnorderedContributions, code: diagnosticcode.GenerationContributionsUnordered},
		{name: "generation repeated state", err: generationresolution.ErrRepeatedState, code: diagnosticcode.GenerationStateRepeated},
		{name: "generation convergence", err: generationresolution.ErrExtensionConvergence, code: diagnosticcode.GenerationNonconvergent},
		{name: "generation helper API", err: generationexec.ErrUnsupportedAPI, code: diagnosticcode.GenerationAPIUnsupported},
		{name: "Plugin generation API", err: pluginmeta.ErrUnsupportedGenerationAPI, code: diagnosticcode.GenerationAPIUnsupported},
		{name: "generation package", err: pluginindex.ErrInvalidGenerationPackage, code: diagnosticcode.GenerationPackageInvalid},
		{name: "generation compile", err: generationexec.ErrCompile, code: diagnosticcode.GenerationCompileFailed},
		{name: "generation execute", err: generationexec.ErrExecute, code: diagnosticcode.GenerationExecutionFailed},
		{name: "generation resolution execute", err: generationresolution.ErrExtensionExecution, code: diagnosticcode.GenerationExecutionFailed},
		{name: "generation extension", err: generationexec.ErrExtension, code: diagnosticcode.GenerationExtensionFailed},
		{name: "generation crash", err: generationexec.ErrCrash, code: diagnosticcode.GenerationCrashed},
		{name: "generation timeout", err: generationexec.ErrTimeout, code: diagnosticcode.GenerationTimeout},
		{name: "generation request size", err: generationexec.ErrRequestTooLarge, code: diagnosticcode.GenerationRequestTooLarge},
		{name: "generation output size", err: generationexec.ErrOutputTooLarge, code: diagnosticcode.GenerationOutputTooLarge},
		{name: "generation malformed output", err: generationexec.ErrMalformedOutput, code: diagnosticcode.GenerationOutputMalformed},
		{name: "generation invalid output", err: generationexec.ErrInvalidOutput, code: diagnosticcode.GenerationOutputInvalid},
		{name: "wrapped generation crash", err: errors.Join(generationexec.ErrExecute, generationexec.ErrCrash), code: diagnosticcode.GenerationCrashed},
		{name: "wrapped generation timeout", err: errors.Join(generationresolution.ErrExtensionExecution, generationexec.ErrTimeout), code: diagnosticcode.GenerationTimeout},
		{name: "compile timeout", err: errors.Join(generationexec.ErrCompile, generationexec.ErrTimeout), code: diagnosticcode.GenerationTimeout},
		{name: "generation extension diagnostic", err: generationresolution.ErrExtensionDiagnostic, code: diagnosticcode.GenerationExtensionDiagnostic},
		{name: "Alias conflict", err: aliasresolution.ErrConflict, code: diagnosticcode.AliasConflict},
		{name: "application Alias", err: aliasresolution.ErrInvalidApplicationAlias, code: diagnosticcode.AliasApplicationInvalid},
		{name: "extension Alias", err: aliasresolution.ErrInvalidExtensionOutput, code: diagnosticcode.AliasExtensionOutputInvalid},
		{name: "Alias resolution", err: generationresolution.ErrAliasResolution, code: diagnosticcode.AliasResolutionFailed},
		{name: "Protobuf wire history", err: protobufwiremap.ErrHistory, code: diagnosticcode.ProtobufWireHistoryInvalid},
		{name: "Protobuf identity", err: protobufidentity.ErrCollision, code: diagnosticcode.ProtobufIdentityCollision},
		{name: "Protobuf operation kind", err: protobufmodel.ErrOperationKind, code: diagnosticcode.ProtobufOperationKindUnsupported},
		{name: "generated ownership", err: generatedfiles.ErrConflict, code: diagnosticcode.GeneratedOwnershipConflict},
		{name: "unexpected generated output", err: generatedfiles.ErrUnexpected, code: diagnosticcode.GeneratedUnexpectedOutput},
		{name: "generated manifest", err: generatedfiles.ErrManifest, code: diagnosticcode.GeneratedManifestInvalid},
		{name: "Capability confirmation", err: capabilitycreate.ErrConfirmationRequired, code: diagnosticcode.CapabilityConfirmationRequired},
		{name: "Capability manifest", err: capabilitymeta.ErrInvalidManifest, code: diagnosticcode.CapabilityManifestInvalid},
		{name: "atomic concurrent change", err: atomicfs.ErrConcurrentChange, code: diagnosticcode.ProjectConcurrentChange},
		{name: "resolution concurrent change", err: applicationresolve.ErrConcurrentChange, code: diagnosticcode.ProjectConcurrentChange},
		{name: "generation concurrent change", err: applicationgenerate.ErrConcurrentChange, code: diagnosticcode.ProjectConcurrentChange},
		{name: "unknown Interface", err: interfaceresolution.ErrUnknownInterface, code: diagnosticcode.ResolveUnknownInterface},
		{name: "unknown Implementation", err: interfaceresolution.ErrUnknownConstructor, code: diagnosticcode.ResolveUnknownImplementation},
		{name: "incompatible Implementation", err: interfaceresolution.ErrIncompatibleChoice, code: diagnosticcode.ResolveIncompatibleImplementation},
		{name: "multiple Implementations", err: interfaceresolution.ErrAmbiguousImplementation, code: diagnosticcode.ResolveMultipleImplementations},
		{name: "missing Implementation", err: constructorgraph.ErrMissingBinding, code: diagnosticcode.ResolveMissingImplementation},
		{name: "constructor cycle", err: constructorgraph.ErrCycle, code: diagnosticcode.ResolveConstructorCycle},
		{name: "reserved Interface", err: interfaceresolution.ErrReservedInterface, code: diagnosticcode.ResolveReservedInterface},
		{name: "intrinsic Interface selection", err: interfaceresolution.ErrIntrinsicChoice, code: diagnosticcode.ResolveIntrinsicInterfaceSelection},
		{name: "invalid Implementation declaration", err: implementationdecl.ErrInvalid, code: diagnosticcode.ImplementationDeclarationInvalid},
		{name: "invalid Implementation Config", err: implementationinventory.ErrInvalidConfiguration, code: diagnosticcode.ImplementationConfigInvalid},
		{name: "invalid required Interface parameter", err: implementationinventory.ErrInvalidRequiredInterface, code: diagnosticcode.ImplementationRequiredInvalid},
		{name: "invalid optional Interface parameter", err: implementationinventory.ErrInvalidOptionalInterface, code: diagnosticcode.ImplementationOptionalInvalid},
		{name: "invalid Implementation result", err: implementationinventory.ErrInvalidResult, code: diagnosticcode.ImplementationResultInvalid},
		{name: "invalid Implementation conformance", err: implementationinventory.ErrInvalidConformance, code: diagnosticcode.ImplementationConformanceInvalid},
		{name: "invalid Interface declaration", err: interfacedecl.ErrInvalid, code: diagnosticcode.InterfaceDeclarationInvalid},
		{name: "invalid Interface contract", err: interfacecontract.ErrInvalid, code: diagnosticcode.InterfaceContractInvalid},
		{name: "invalid Interface metadata", err: interfacemeta.ErrInvalid, code: diagnosticcode.InterfaceMetadataInvalid},
		{name: "duplicate Interface ID", err: interfaceinventory.ErrDuplicateID, code: diagnosticcode.InterfaceIDDuplicate},
		{name: "invalid authored package", err: interfaceinventory.ErrPackage, code: diagnosticcode.AuthoredPackageInvalid},
		{name: "invalid Interface create name", err: interfacecreate.ErrInvalidName, code: diagnosticcode.InterfaceCreateNameInvalid},
		{name: "existing Interface create target", err: interfacecreate.ErrTargetExists, code: diagnosticcode.InterfaceCreateTargetExists},
		{name: "invalid Implementation create Interface", err: implementationcreate.ErrInvalidInterface, code: diagnosticcode.ImplementationCreateInterfaceInvalid},
		{name: "invalid Implementation create package", err: implementationcreate.ErrInvalidPackage, code: diagnosticcode.ImplementationCreatePackageInvalid},
		{name: "missing Implementation create Interface", err: implementationcreate.ErrInterfaceNotFound, code: diagnosticcode.ImplementationCreateInterfaceNotFound},
		{name: "existing Implementation create target", err: implementationcreate.ErrTargetExists, code: diagnosticcode.ImplementationCreateTargetExists},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			diagnostic, ok := primaryActionableDiagnostic(test.err, recoveryContext{})
			if !ok || diagnostic.code != test.code || diagnostic.recovery == "" || !diagnosticcode.Valid(diagnostic.code) {
				t.Fatalf("primaryActionableDiagnostic(%v) = %#v, %t; want canonical code %q", test.err, diagnostic, ok, test.code)
			}
		})
	}
}

func TestRecoverySelectorPreservesModeWithoutEchoingUnsafePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		context    recoveryContext
		wantSuffix string
		reject     string
	}{
		{name: "default", context: commandRecoveryContext("", "", nil), wantSuffix: "`plystra generate`"},
		{name: "explicit environment", context: commandRecoveryContext("", "production", []string{"PLYSTRA_CONFIG=ignored.yaml"}), wantSuffix: "`plystra generate --env \"production\"`"},
		{name: "ambient environment", context: commandRecoveryContext("", "", []string{"PLYSTRA_ENV=test"}), wantSuffix: "`plystra generate --env \"test\"`"},
		{name: "explicit configuration", context: commandRecoveryContext("deploy/customer.yaml", "", []string{"PLYSTRA_ENV=ignored"}), wantSuffix: "`plystra generate --config \"deploy/customer.yaml\"`"},
		{name: "ambient configuration", context: commandRecoveryContext("", "", []string{"PLYSTRA_CONFIG=deploy/ambient.yaml"}), wantSuffix: "`plystra generate --config \"deploy/ambient.yaml\"`"},
		{name: "absolute configuration", context: commandRecoveryContext(filepath.Join(t.TempDir(), "private-token.yaml"), "", nil), wantSuffix: "`plystra generate --config <yaml-path>`", reject: "private-token"},
		{name: "shell-sensitive configuration", context: commandRecoveryContext("deploy/`private-token`.yaml", "", nil), wantSuffix: "`plystra generate --config <yaml-path>`", reject: "private-token"},
		{name: "unsafe environment", context: commandRecoveryContext("", "production\nprivate-token", nil), wantSuffix: "`plystra generate --env <environment>`", reject: "private-token"},
		{name: "shell-sensitive environment", context: commandRecoveryContext("", "$env:private-token", nil), wantSuffix: "`plystra generate --env <environment>`", reject: "private-token"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			diagnostic, ok := primaryActionableDiagnostic(generatedfiles.ErrConflict, test.context)
			if !ok || diagnostic.code != diagnosticGeneratedOwnershipConflict || !strings.Contains(diagnostic.recovery, test.wantSuffix) {
				t.Fatalf("primaryActionableDiagnostic() = %#v, %t; want code %q and recovery %q", diagnostic, ok, diagnosticGeneratedOwnershipConflict, test.wantSuffix)
			}
			if test.reject != "" && strings.Contains(diagnostic.recovery, test.reject) {
				t.Fatalf("primaryActionableDiagnostic() leaked %q in %q", test.reject, diagnostic.recovery)
			}
		})
	}
}

func FuzzRecoverySelectorDoesNotInjectDiagnosticLines(f *testing.F) {
	for _, seed := range []string{"production", "deploy/customer.yaml", "../outside.yaml", "value\nRecovery:\nunsafe", "$env:SECRET", "`command`", strings.Repeat("x", 600)} {
		f.Add(true, seed)
		f.Add(false, seed)
	}
	f.Fuzz(func(t *testing.T, environmentMode bool, value string) {
		context := commandRecoveryContext(value, "", nil)
		if environmentMode {
			context = commandRecoveryContext("", value, nil)
		}
		diagnostic, ok := primaryActionableDiagnostic(generatedfiles.ErrConflict, context)
		if !ok {
			t.Fatal("generated ownership conflict was not actionable")
		}
		if strings.ContainsAny(diagnostic.recovery, "\r\n\x00") {
			t.Fatalf("recovery action contains an injected line or NUL: %q", diagnostic.recovery)
		}
		if len(diagnostic.recovery) > 1000 {
			t.Fatalf("recovery action is unbounded: %d bytes", len(diagnostic.recovery))
		}
	})
}

func TestWriteCommandFailureChoosesOneJoinedProblemAndLeavesUnknownErrorsUnchanged(t *testing.T) {
	t.Parallel()

	_, ambiguous, invalidChoice, _ := recoveryProviderFailures(t)
	joined := errors.Join(ambiguous, invalidChoice)
	var output strings.Builder
	writeCommandFailure(&output, "", joined, recoveryContext{})
	got := output.String()
	for _, want := range []string{"selected Plugin ID is not visible", "\n\nRecovery:\n", "\nDiagnostic: " + diagnosticProviderSelectionInvalid + "\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("joined output = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "ambiguous canonical Capability provider") {
		t.Fatalf("joined output included a non-primary problem: %q", got)
	}
	if strings.Contains(got, "correction:") || strings.Count(got, "Recovery:") != 1 {
		t.Fatalf("joined output contains duplicate recovery advice: %q", got)
	}

	output.Reset()
	unknown := errors.New("internal renderer failed")
	writeCommandFailure(&output, "inspect", unknown, recoveryContext{})
	if got := output.String(); got != "inspect: internal renderer failed\n" {
		t.Fatalf("unknown output = %q", got)
	}
}

func recoveryProviderFailures(t *testing.T) (missing, ambiguous, invalidChoice, contractConflict error) {
	t.Helper()
	contract := recoveryContract("string")
	requirement := providerresolution.Requirement{
		Contract: contract,
		Source: providerresolution.RequirementSource{
			Kind:       providerresolution.RequirementDeclaration,
			Reference:  "plystra.yaml capabilities.require[email.send/v1]",
			ModulePath: "example.com/project",
			Path:       "plystra.yaml",
			Line:       1,
			Column:     1,
		},
	}
	_, missing = providerresolution.Resolve(providerresolution.Input{Requirements: []providerresolution.Requirement{requirement}})
	candidates := []providerresolution.Candidate{
		{PluginID: "acme.email.local", Contract: contract, Source: "local/capability.yaml"},
		{PluginID: "acme.email.smtp", Contract: contract, Source: "smtp/capability.yaml"},
	}
	_, ambiguous = providerresolution.Resolve(providerresolution.Input{Requirements: []providerresolution.Requirement{requirement}, Candidates: candidates})
	_, invalidChoice = providerresolution.Resolve(providerresolution.Input{
		Requirements: []providerresolution.Requirement{requirement},
		Candidates:   candidates,
		Choices: []providerresolution.Choice{{
			Capability: "email.send/v1",
			PluginID:   "missing.email",
			Sources: []providerresolution.ChoiceSource{{
				Kind:       providerresolution.ChoiceSourceCurrentProject,
				Reference:  "plystra.yaml capabilities.use[email.send/v1]",
				ModulePath: "example.com/project",
				Path:       "plystra.yaml",
				Line:       2,
				Column:     3,
			}},
		}},
	})
	_, contractConflict = providerresolution.Resolve(providerresolution.Input{
		Requirements: []providerresolution.Requirement{requirement},
		Candidates: []providerresolution.Candidate{{
			PluginID: "acme.email.local",
			Contract: recoveryContract("boolean"),
			Source:   "local/capability.yaml",
		}},
	})
	for name, err := range map[string]error{
		"missing": missing, "ambiguous": ambiguous, "invalid choice": invalidChoice, "contract conflict": contractConflict,
	} {
		if err == nil {
			t.Fatalf("%s provider input unexpectedly resolved", name)
		}
	}
	return missing, ambiguous, invalidChoice, contractConflict
}

func recoveryContract(fieldType string) []byte {
	return []byte("id: email.send/v1\nrequest: {value: {type: " + fieldType + "}}\n" + `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`)
}
