// Package diagnosticcode owns the stable identifiers used by actionable
// Plystra CLI diagnostics and structured diagnostic documents.
package diagnosticcode

import "strings"

const Prefix = "PLYSTRA_"

const (
	TemplateInvalid                    = Prefix + "TEMPLATE_INVALID"
	CapabilityRequirementConflict      = Prefix + "CAPABILITY_REQUIREMENT_CONFLICT"
	ProviderContractConflict           = Prefix + "PROVIDER_CONTRACT_CONFLICT"
	ProviderContractMismatch           = Prefix + "PROVIDER_CONTRACT_MISMATCH"
	CapabilityContractConflict         = Prefix + "CAPABILITY_CONTRACT_CONFLICT"
	CapabilitySchemaConflict           = Prefix + "CAPABILITY_SCHEMA_CONFLICT"
	ProviderSelectionInvalid           = Prefix + "PROVIDER_SELECTION_INVALID"
	ProviderMissing                    = Prefix + "PROVIDER_MISSING"
	ProviderAmbiguous                  = Prefix + "PROVIDER_AMBIGUOUS"
	ProjectManifestInvalid             = Prefix + "PROJECT_MANIFEST_INVALID"
	ConfigurationInheritedConflict     = Prefix + "CONFIGURATION_INHERITED_CONFLICT"
	ConfigurationOwnershipAmbiguous    = Prefix + "CONFIGURATION_OWNERSHIP_AMBIGUOUS"
	HTTPTransportSelectionInvalid      = Prefix + "HTTP_TRANSPORT_SELECTION_INVALID"
	EnvironmentOverlayInvalid          = Prefix + "ENVIRONMENT_OVERLAY_INVALID"
	ConfigurationInvalid               = Prefix + "CONFIGURATION_INVALID"
	PluginConfigurationUnselected      = Prefix + "PLUGIN_CONFIGURATION_UNSELECTED"
	PluginConfigurationPluginMissing   = Prefix + "PLUGIN_CONFIGURATION_PLUGIN_MISSING"
	ConfigurationSelectionInvalid      = Prefix + "CONFIGURATION_SELECTION_INVALID"
	ApplicationDependencyDrift         = Prefix + "APPLICATION_DEPENDENCY_DRIFT"
	ProjectNotFound                    = Prefix + "PROJECT_NOT_FOUND"
	GoModuleNotFound                   = Prefix + "GO_MODULE_NOT_FOUND"
	GoModuleInvalid                    = Prefix + "GO_MODULE_INVALID"
	GoModuleUnavailable                = Prefix + "GO_MODULE_UNAVAILABLE"
	GoCommandFailed                    = Prefix + "GO_COMMAND_FAILED"
	PluginTargetAmbiguous              = Prefix + "PLUGIN_TARGET_AMBIGUOUS"
	PluginTargetNotFound               = Prefix + "PLUGIN_TARGET_NOT_FOUND"
	PluginTargetInvalid                = Prefix + "PLUGIN_TARGET_INVALID"
	GenerationActivationConflict       = Prefix + "GENERATION_ACTIVATION_CONFLICT"
	GenerationActivationMissing        = Prefix + "GENERATION_ACTIVATION_MISSING"
	GenerationProviderExtensionMissing = Prefix + "GENERATION_PROVIDER_EXTENSION_MISSING"
	GenerationActivationCycle          = Prefix + "GENERATION_ACTIVATION_CYCLE"
	GenerationDependencyCycle          = Prefix + "GENERATION_DEPENDENCY_CYCLE"
	GenerationContributionCycle        = Prefix + "GENERATION_CONTRIBUTION_CYCLE"
	GenerationContributionsUnordered   = Prefix + "GENERATION_CONTRIBUTIONS_UNORDERED"
	GenerationStateRepeated            = Prefix + "GENERATION_STATE_REPEATED"
	GenerationNonconvergent            = Prefix + "GENERATION_NONCONVERGENT"
	GenerationAPIUnsupported           = Prefix + "GENERATION_API_UNSUPPORTED"
	GenerationPackageInvalid           = Prefix + "GENERATION_PACKAGE_INVALID"
	GenerationCompileFailed            = Prefix + "GENERATION_COMPILE_FAILED"
	GenerationExecutionFailed          = Prefix + "GENERATION_EXECUTION_FAILED"
	GenerationExtensionFailed          = Prefix + "GENERATION_EXTENSION_FAILED"
	GenerationCrashed                  = Prefix + "GENERATION_CRASHED"
	GenerationTimeout                  = Prefix + "GENERATION_TIMEOUT"
	GenerationRequestTooLarge          = Prefix + "GENERATION_REQUEST_TOO_LARGE"
	GenerationOutputTooLarge           = Prefix + "GENERATION_OUTPUT_TOO_LARGE"
	GenerationOutputMalformed          = Prefix + "GENERATION_OUTPUT_MALFORMED"
	GenerationOutputInvalid            = Prefix + "GENERATION_OUTPUT_INVALID"
	GenerationExtensionDiagnostic      = Prefix + "GENERATION_EXTENSION_DIAGNOSTIC"
	AliasConflict                      = Prefix + "ALIAS_CONFLICT"
	AliasApplicationInvalid            = Prefix + "ALIAS_APPLICATION_INVALID"
	AliasExtensionOutputInvalid        = Prefix + "ALIAS_EXTENSION_OUTPUT_INVALID"
	AliasResolutionFailed              = Prefix + "ALIAS_RESOLUTION_FAILED"
	ProtobufWireHistoryInvalid         = Prefix + "PROTOBUF_WIRE_HISTORY_INVALID"
	ProtobufIdentityCollision          = Prefix + "PROTOBUF_IDENTITY_COLLISION"
	ProtobufOperationKindUnsupported   = Prefix + "PROTOBUF_OPERATION_KIND_UNSUPPORTED"
	GeneratedOwnershipConflict         = Prefix + "GENERATED_OWNERSHIP_CONFLICT"
	GeneratedUnexpectedOutput          = Prefix + "GENERATED_UNEXPECTED_OUTPUT"
	GeneratedManifestInvalid           = Prefix + "GENERATED_MANIFEST_INVALID"
	CapabilityConfirmationRequired     = Prefix + "CAPABILITY_CONFIRMATION_REQUIRED"
	CapabilityManifestInvalid          = Prefix + "CAPABILITY_MANIFEST_INVALID"
	ProjectConcurrentChange            = Prefix + "PROJECT_CONCURRENT_CHANGE"
	ConfigurationCompositionDrift      = Prefix + "CONFIGURATION_COMPOSITION_DRIFT"
	GeneratedDrift                     = Prefix + "GENERATED_DRIFT"
	ResolveUnknownInterface            = Prefix + "RESOLVE_UNKNOWN_INTERFACE"
	ResolveUnknownImplementation       = Prefix + "RESOLVE_UNKNOWN_IMPLEMENTATION"
	ResolveIncompatibleImplementation  = Prefix + "RESOLVE_INCOMPATIBLE_IMPLEMENTATION"
	ResolveMultipleImplementations     = Prefix + "RESOLVE_MULTIPLE_IMPLEMENTATIONS"
	ResolveMissingImplementation       = Prefix + "RESOLVE_MISSING_IMPLEMENTATION"
	ResolveConstructorCycle            = Prefix + "RESOLVE_CONSTRUCTOR_CYCLE"
	ResolveReservedInterface           = Prefix + "RESOLVE_RESERVED_INTERFACE"
	ResolveIntrinsicInterfaceSelection = Prefix + "RESOLVE_INTRINSIC_INTERFACE_SELECTION"
	ImplementationDeclarationInvalid   = Prefix + "IMPLEMENTATION_DECLARATION_INVALID"
	ImplementationConfigInvalid        = Prefix + "IMPLEMENTATION_CONFIG_INVALID"
	ImplementationRequiredInvalid      = Prefix + "IMPLEMENTATION_REQUIRED_INTERFACE_INVALID"
	ImplementationOptionalInvalid      = Prefix + "IMPLEMENTATION_OPTIONAL_INTERFACE_INVALID"
	ImplementationResultInvalid        = Prefix + "IMPLEMENTATION_RESULT_INVALID"
	ImplementationConformanceInvalid   = Prefix + "IMPLEMENTATION_CONFORMANCE_INVALID"
)

const (
	ConstructorConfigurationSchemaInvalid = Prefix + "CONSTRUCTOR_CONFIGURATION_SCHEMA_INVALID"
	ConstructorConfigurationValuesInvalid = Prefix + "CONSTRUCTOR_CONFIGURATION_VALUES_INVALID"
)

// Valid reports whether value is one bounded canonical Plystra diagnostic
// identifier. It validates the stable wire format, not membership in the
// current built-in catalog, so extension-owned codes remain possible.
func Valid(value string) bool {
	if len(value) <= len(Prefix) || len(value) > 128 || !strings.HasPrefix(value, Prefix) {
		return false
	}
	previousSeparator := true
	for index := len(Prefix); index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'A' && character <= 'Z', character >= '0' && character <= '9':
			previousSeparator = false
		case character == '_' && !previousSeparator:
			previousSeparator = true
		default:
			return false
		}
	}
	return !previousSeparator
}
