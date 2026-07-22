// Package diagnosticcode owns the stable identifiers used by actionable
// Plystra CLI diagnostics and structured diagnostic documents.
package diagnosticcode

import "strings"

const Prefix = "PLYSTRA-"

const (
	TemplateInvalid                    = Prefix + "TEMPLATE-INVALID"
	CapabilityRequirementConflict      = Prefix + "CAPABILITY-REQUIREMENT-CONFLICT"
	ProviderContractConflict           = Prefix + "PROVIDER-CONTRACT-CONFLICT"
	ProviderContractMismatch           = Prefix + "PROVIDER-CONTRACT-MISMATCH"
	CapabilityContractConflict         = Prefix + "CAPABILITY-CONTRACT-CONFLICT"
	CapabilitySchemaConflict           = Prefix + "CAPABILITY-SCHEMA-CONFLICT"
	ProviderSelectionInvalid           = Prefix + "PROVIDER-SELECTION-INVALID"
	ProviderMissing                    = Prefix + "PROVIDER-MISSING"
	ProviderAmbiguous                  = Prefix + "PROVIDER-AMBIGUOUS"
	ProjectManifestInvalid             = Prefix + "PROJECT-MANIFEST-INVALID"
	ConfigurationInheritedConflict     = Prefix + "CONFIGURATION-INHERITED-CONFLICT"
	ConfigurationOwnershipAmbiguous    = Prefix + "CONFIGURATION-OWNERSHIP-AMBIGUOUS"
	HTTPTransportSelectionInvalid      = Prefix + "HTTP-TRANSPORT-SELECTION-INVALID"
	PluginConfigurationSchemaInvalid   = Prefix + "PLUGIN-CONFIGURATION-SCHEMA-INVALID"
	EnvironmentOverlayInvalid          = Prefix + "ENVIRONMENT-OVERLAY-INVALID"
	ConfigurationInvalid               = Prefix + "CONFIGURATION-INVALID"
	PluginConfigurationUnselected      = Prefix + "PLUGIN-CONFIGURATION-UNSELECTED"
	PluginConfigurationPluginMissing   = Prefix + "PLUGIN-CONFIGURATION-PLUGIN-MISSING"
	ConfigurationSelectionInvalid      = Prefix + "CONFIGURATION-SELECTION-INVALID"
	ApplicationDependencyDrift         = Prefix + "APPLICATION-DEPENDENCY-DRIFT"
	ProjectNotFound                    = Prefix + "PROJECT-NOT-FOUND"
	GoModuleNotFound                   = Prefix + "GO-MODULE-NOT-FOUND"
	GoModuleInvalid                    = Prefix + "GO-MODULE-INVALID"
	GoModuleUnavailable                = Prefix + "GO-MODULE-UNAVAILABLE"
	GoCommandFailed                    = Prefix + "GO-COMMAND-FAILED"
	PluginTargetAmbiguous              = Prefix + "PLUGIN-TARGET-AMBIGUOUS"
	PluginTargetNotFound               = Prefix + "PLUGIN-TARGET-NOT-FOUND"
	PluginTargetInvalid                = Prefix + "PLUGIN-TARGET-INVALID"
	GenerationActivationConflict       = Prefix + "GENERATION-ACTIVATION-CONFLICT"
	GenerationActivationMissing        = Prefix + "GENERATION-ACTIVATION-MISSING"
	GenerationProviderExtensionMissing = Prefix + "GENERATION-PROVIDER-EXTENSION-MISSING"
	GenerationActivationCycle          = Prefix + "GENERATION-ACTIVATION-CYCLE"
	GenerationDependencyCycle          = Prefix + "GENERATION-DEPENDENCY-CYCLE"
	GenerationContributionCycle        = Prefix + "GENERATION-CONTRIBUTION-CYCLE"
	GenerationContributionsUnordered   = Prefix + "GENERATION-CONTRIBUTIONS-UNORDERED"
	GenerationStateRepeated            = Prefix + "GENERATION-STATE-REPEATED"
	GenerationNonconvergent            = Prefix + "GENERATION-NONCONVERGENT"
	GenerationAPIUnsupported           = Prefix + "GENERATION-API-UNSUPPORTED"
	GenerationPackageInvalid           = Prefix + "GENERATION-PACKAGE-INVALID"
	GenerationCompileFailed            = Prefix + "GENERATION-COMPILE-FAILED"
	GenerationExecutionFailed          = Prefix + "GENERATION-EXECUTION-FAILED"
	GenerationExtensionFailed          = Prefix + "GENERATION-EXTENSION-FAILED"
	GenerationCrashed                  = Prefix + "GENERATION-CRASHED"
	GenerationTimeout                  = Prefix + "GENERATION-TIMEOUT"
	GenerationRequestTooLarge          = Prefix + "GENERATION-REQUEST-TOO-LARGE"
	GenerationOutputTooLarge           = Prefix + "GENERATION-OUTPUT-TOO-LARGE"
	GenerationOutputMalformed          = Prefix + "GENERATION-OUTPUT-MALFORMED"
	GenerationOutputInvalid            = Prefix + "GENERATION-OUTPUT-INVALID"
	GenerationExtensionDiagnostic      = Prefix + "GENERATION-EXTENSION-DIAGNOSTIC"
	AliasConflict                      = Prefix + "ALIAS-CONFLICT"
	AliasApplicationInvalid            = Prefix + "ALIAS-APPLICATION-INVALID"
	AliasExtensionOutputInvalid        = Prefix + "ALIAS-EXTENSION-OUTPUT-INVALID"
	AliasResolutionFailed              = Prefix + "ALIAS-RESOLUTION-FAILED"
	ProtobufWireHistoryInvalid         = Prefix + "PROTOBUF-WIRE-HISTORY-INVALID"
	ProtobufIdentityCollision          = Prefix + "PROTOBUF-IDENTITY-COLLISION"
	ProtobufOperationKindUnsupported   = Prefix + "PROTOBUF-OPERATION-KIND-UNSUPPORTED"
	GeneratedOwnershipConflict         = Prefix + "GENERATED-OWNERSHIP-CONFLICT"
	GeneratedUnexpectedOutput          = Prefix + "GENERATED-UNEXPECTED-OUTPUT"
	GeneratedManifestInvalid           = Prefix + "GENERATED-MANIFEST-INVALID"
	CapabilityConfirmationRequired     = Prefix + "CAPABILITY-CONFIRMATION-REQUIRED"
	CapabilityManifestInvalid          = Prefix + "CAPABILITY-MANIFEST-INVALID"
	ProjectConcurrentChange            = Prefix + "PROJECT-CONCURRENT-CHANGE"
	ConfigurationCompositionDrift      = Prefix + "CONFIGURATION-COMPOSITION-DRIFT"
	GeneratedDrift                     = Prefix + "GENERATED-DRIFT"
)

// Valid reports whether value is one bounded canonical Plystra diagnostic
// identifier. It validates the stable wire format, not membership in the
// current built-in catalog, so extension-owned codes remain possible.
func Valid(value string) bool {
	if len(value) <= len(Prefix) || len(value) > 128 || !strings.HasPrefix(value, Prefix) {
		return false
	}
	previousHyphen := true
	for index := len(Prefix); index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'A' && character <= 'Z', character >= '0' && character <= '9':
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}
