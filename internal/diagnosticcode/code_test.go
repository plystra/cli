package diagnosticcode_test

import (
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestBuiltInCodesAreCanonicalAndUnique(t *testing.T) {
	codes := []string{
		diagnosticcode.TemplateInvalid,
		diagnosticcode.CapabilityRequirementConflict,
		diagnosticcode.ProviderContractConflict,
		diagnosticcode.ProviderContractMismatch,
		diagnosticcode.CapabilityContractConflict,
		diagnosticcode.CapabilitySchemaConflict,
		diagnosticcode.ProviderSelectionInvalid,
		diagnosticcode.ProviderMissing,
		diagnosticcode.ProviderAmbiguous,
		diagnosticcode.ProjectManifestInvalid,
		diagnosticcode.ConfigurationInheritedConflict,
		diagnosticcode.ConfigurationOwnershipAmbiguous,
		diagnosticcode.HTTPTransportSelectionInvalid,
		diagnosticcode.ConstructorConfigurationSchemaInvalid,
		diagnosticcode.ConstructorConfigurationValuesInvalid,
		diagnosticcode.ConstructorConfigurationUnselected,
		diagnosticcode.EnvironmentOverlayInvalid,
		diagnosticcode.ConfigurationInvalid,
		diagnosticcode.PluginConfigurationUnselected,
		diagnosticcode.PluginConfigurationPluginMissing,
		diagnosticcode.ConfigurationSelectionInvalid,
		diagnosticcode.ApplicationDependencyDrift,
		diagnosticcode.ProjectNotFound,
		diagnosticcode.GoModuleNotFound,
		diagnosticcode.GoModuleInvalid,
		diagnosticcode.GoModuleUnavailable,
		diagnosticcode.GoCommandFailed,
		diagnosticcode.PluginTargetAmbiguous,
		diagnosticcode.PluginTargetNotFound,
		diagnosticcode.PluginTargetInvalid,
		diagnosticcode.GenerationActivationConflict,
		diagnosticcode.GenerationActivationMissing,
		diagnosticcode.GenerationProviderExtensionMissing,
		diagnosticcode.GenerationActivationCycle,
		diagnosticcode.GenerationDependencyCycle,
		diagnosticcode.GenerationContributionCycle,
		diagnosticcode.GenerationContributionsUnordered,
		diagnosticcode.GenerationStateRepeated,
		diagnosticcode.GenerationNonconvergent,
		diagnosticcode.GenerationAPIUnsupported,
		diagnosticcode.GenerationPackageInvalid,
		diagnosticcode.GenerationCompileFailed,
		diagnosticcode.GenerationExecutionFailed,
		diagnosticcode.GenerationExtensionFailed,
		diagnosticcode.GenerationCrashed,
		diagnosticcode.GenerationTimeout,
		diagnosticcode.GenerationRequestTooLarge,
		diagnosticcode.GenerationOutputTooLarge,
		diagnosticcode.GenerationOutputMalformed,
		diagnosticcode.GenerationOutputInvalid,
		diagnosticcode.GenerationExtensionDiagnostic,
		diagnosticcode.AliasConflict,
		diagnosticcode.AliasApplicationInvalid,
		diagnosticcode.AliasExtensionOutputInvalid,
		diagnosticcode.AliasResolutionFailed,
		diagnosticcode.ProtobufWireHistoryInvalid,
		diagnosticcode.ProtobufIdentityCollision,
		diagnosticcode.ProtobufOperationKindUnsupported,
		diagnosticcode.GeneratedOwnershipConflict,
		diagnosticcode.GeneratedUnexpectedOutput,
		diagnosticcode.GeneratedManifestInvalid,
		diagnosticcode.CapabilityConfirmationRequired,
		diagnosticcode.CapabilityManifestInvalid,
		diagnosticcode.ProjectConcurrentChange,
		diagnosticcode.ConfigurationCompositionDrift,
		diagnosticcode.GeneratedDrift,
		diagnosticcode.ResolveUnknownInterface,
		diagnosticcode.ResolveUnknownImplementation,
		diagnosticcode.ResolveIncompatibleImplementation,
		diagnosticcode.ResolveMultipleImplementations,
		diagnosticcode.ResolveMissingImplementation,
		diagnosticcode.ResolveConstructorCycle,
		diagnosticcode.ResolveReservedInterface,
		diagnosticcode.ResolveIntrinsicInterfaceSelection,
		diagnosticcode.ImplementationDeclarationInvalid,
		diagnosticcode.ImplementationConfigInvalid,
		diagnosticcode.ImplementationRequiredInvalid,
		diagnosticcode.ImplementationOptionalInvalid,
		diagnosticcode.ImplementationResultInvalid,
		diagnosticcode.ImplementationConformanceInvalid,
		diagnosticcode.InterfaceDeclarationInvalid,
		diagnosticcode.InterfaceContractInvalid,
		diagnosticcode.InterfaceMetadataInvalid,
		diagnosticcode.InterfaceIDDuplicate,
		diagnosticcode.AuthoredPackageInvalid,
		diagnosticcode.InterfaceCreateNameInvalid,
		diagnosticcode.InterfaceCreateTargetExists,
		diagnosticcode.ImplementationCreateInterfaceInvalid,
		diagnosticcode.ImplementationCreatePackageInvalid,
		diagnosticcode.ImplementationCreateInterfaceNotFound,
		diagnosticcode.ImplementationCreateTargetExists,
		diagnosticcode.UseInterfaceInvalid,
		diagnosticcode.UseConstructorInvalid,
	}
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if !diagnosticcode.Valid(code) {
			t.Fatalf("built-in code %q is not canonical", code)
		}
		if _, exists := seen[code]; exists {
			t.Fatalf("built-in code %q is duplicated", code)
		}
		seen[code] = struct{}{}
	}
}

func TestValidRejectsNoncanonicalCodes(t *testing.T) {
	for _, value := range []string{"", "PLYSTRA_", "plystra_provider_missing", "PLYSTRA__MISSING", "PLYSTRA_MISSING_", "PLYSTRA-MISSING-VALUE", "PLYSTRA_MISSING-VALUE"} {
		if diagnosticcode.Valid(value) {
			t.Fatalf("Valid(%q) = true", value)
		}
	}
}
