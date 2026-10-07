// Package diagnosticcode owns the stable identifiers used by actionable
// Plystra CLI diagnostics and structured diagnostic documents.
package diagnosticcode

import "strings"

const Prefix = "PLYSTRA_"

const (
	GenerateInvocationInvalid            = Prefix + "GENERATE_INVOCATION_INVALID"
	GenerateFailed                       = Prefix + "GENERATE_FAILED"
	TemplateInvalid                      = Prefix + "TEMPLATE_INVALID"
	CapabilityRequirementConflict        = Prefix + "CAPABILITY_REQUIREMENT_CONFLICT"
	ProviderContractConflict             = Prefix + "PROVIDER_CONTRACT_CONFLICT"
	ProviderContractMismatch             = Prefix + "PROVIDER_CONTRACT_MISMATCH"
	CapabilityContractConflict           = Prefix + "CAPABILITY_CONTRACT_CONFLICT"
	CapabilitySchemaConflict             = Prefix + "CAPABILITY_SCHEMA_CONFLICT"
	ProviderSelectionInvalid             = Prefix + "PROVIDER_SELECTION_INVALID"
	ProviderMissing                      = Prefix + "PROVIDER_MISSING"
	ProviderAmbiguous                    = Prefix + "PROVIDER_AMBIGUOUS"
	ProjectManifestInvalid               = Prefix + "PROJECT_MANIFEST_INVALID"
	EnvironmentOverlayInvalid            = Prefix + "ENVIRONMENT_OVERLAY_INVALID"
	ConfigurationInvalid                 = Prefix + "CONFIGURATION_INVALID"
	PolicyNotEnforced                    = Prefix + "POLICY_NOT_ENFORCED"
	PluginConfigurationUnselected        = Prefix + "PLUGIN_CONFIGURATION_UNSELECTED"
	PluginConfigurationPluginMissing     = Prefix + "PLUGIN_CONFIGURATION_PLUGIN_MISSING"
	ConfigurationSelectionInvalid        = Prefix + "CONFIGURATION_SELECTION_INVALID"
	ApplicationDependencyDrift           = Prefix + "APPLICATION_DEPENDENCY_DRIFT"
	ProjectNotFound                      = Prefix + "PROJECT_NOT_FOUND"
	GoModuleNotFound                     = Prefix + "GO_MODULE_NOT_FOUND"
	GoModuleInvalid                      = Prefix + "GO_MODULE_INVALID"
	GoModuleUnavailable                  = Prefix + "GO_MODULE_UNAVAILABLE"
	GoCommandFailed                      = Prefix + "GO_COMMAND_FAILED"
	PluginTargetAmbiguous                = Prefix + "PLUGIN_TARGET_AMBIGUOUS"
	PluginTargetNotFound                 = Prefix + "PLUGIN_TARGET_NOT_FOUND"
	PluginTargetInvalid                  = Prefix + "PLUGIN_TARGET_INVALID"
	GenerationActivationConflict         = Prefix + "GENERATION_ACTIVATION_CONFLICT"
	GenerationActivationMissing          = Prefix + "GENERATION_ACTIVATION_MISSING"
	GenerationProviderExtensionMissing   = Prefix + "GENERATION_PROVIDER_EXTENSION_MISSING"
	GenerationActivationCycle            = Prefix + "GENERATION_ACTIVATION_CYCLE"
	GenerationDependencyCycle            = Prefix + "GENERATION_DEPENDENCY_CYCLE"
	GenerationContributionCycle          = Prefix + "GENERATION_CONTRIBUTION_CYCLE"
	GenerationContributionsUnordered     = Prefix + "GENERATION_CONTRIBUTIONS_UNORDERED"
	GenerationStateRepeated              = Prefix + "GENERATION_STATE_REPEATED"
	GenerationNonconvergent              = Prefix + "GENERATION_NONCONVERGENT"
	GenerationAPIUnsupported             = Prefix + "GENERATION_API_UNSUPPORTED"
	GenerationPackageInvalid             = Prefix + "GENERATION_PACKAGE_INVALID"
	GenerationCompileFailed              = Prefix + "GENERATION_COMPILE_FAILED"
	GenerationExecutionFailed            = Prefix + "GENERATION_EXECUTION_FAILED"
	GenerationExtensionFailed            = Prefix + "GENERATION_EXTENSION_FAILED"
	GenerationCrashed                    = Prefix + "GENERATION_CRASHED"
	GenerationTimeout                    = Prefix + "GENERATION_TIMEOUT"
	GenerationRequestTooLarge            = Prefix + "GENERATION_REQUEST_TOO_LARGE"
	GenerationOutputTooLarge             = Prefix + "GENERATION_OUTPUT_TOO_LARGE"
	GenerationOutputMalformed            = Prefix + "GENERATION_OUTPUT_MALFORMED"
	GenerationOutputInvalid              = Prefix + "GENERATION_OUTPUT_INVALID"
	GenerationExtensionDiagnostic        = Prefix + "GENERATION_EXTENSION_DIAGNOSTIC"
	AliasConflict                        = Prefix + "ALIAS_CONFLICT"
	AliasApplicationInvalid              = Prefix + "ALIAS_APPLICATION_INVALID"
	AliasExtensionOutputInvalid          = Prefix + "ALIAS_EXTENSION_OUTPUT_INVALID"
	AliasResolutionFailed                = Prefix + "ALIAS_RESOLUTION_FAILED"
	ProtobufWireHistoryInvalid           = Prefix + "PROTOBUF_WIRE_HISTORY_INVALID"
	ProtobufIdentityCollision            = Prefix + "PROTOBUF_IDENTITY_COLLISION"
	ProtobufOperationKindUnsupported     = Prefix + "PROTOBUF_OPERATION_KIND_UNSUPPORTED"
	ProtobufPointerProjectionUnsupported = Prefix + "PROTOBUF_POINTER_PROJECTION_UNSUPPORTED"
	GeneratedOwnershipConflict           = Prefix + "GENERATED_OWNERSHIP_CONFLICT"
	GeneratedUnexpectedOutput            = Prefix + "GENERATED_UNEXPECTED_OUTPUT"
	GeneratedManifestInvalid             = Prefix + "GENERATED_MANIFEST_INVALID"
	AgentGuidanceDrift                   = Prefix + "AGENT_GUIDANCE_DRIFT"
	AgentGuidanceManifestInvalid         = Prefix + "AGENT_GUIDANCE_MANIFEST_INVALID"
	CapabilityManifestInvalid            = Prefix + "CAPABILITY_MANIFEST_INVALID"
	ProjectConcurrentChange              = Prefix + "PROJECT_CONCURRENT_CHANGE"
	GeneratedDrift                       = Prefix + "GENERATED_DRIFT"
	ResolveUnknownInterface              = Prefix + "RESOLVE_UNKNOWN_INTERFACE"
	ResolveUnknownImplementation         = Prefix + "RESOLVE_UNKNOWN_IMPLEMENTATION"
	ResolveIncompatibleImplementation    = Prefix + "RESOLVE_INCOMPATIBLE_IMPLEMENTATION"
	ResolveMultipleImplementations       = Prefix + "RESOLVE_MULTIPLE_IMPLEMENTATIONS"
	ResolveMissingImplementation         = Prefix + "RESOLVE_MISSING_IMPLEMENTATION"
	ResolveConstructorCycle              = Prefix + "RESOLVE_CONSTRUCTOR_CYCLE"
	ResolveReservedInterface             = Prefix + "RESOLVE_RESERVED_INTERFACE"
	ResolveIntrinsicInterfaceSelection   = Prefix + "RESOLVE_INTRINSIC_INTERFACE_SELECTION"
	ImplementationDeclarationInvalid     = Prefix + "IMPLEMENTATION_DECLARATION_INVALID"
	ImplementationConfigInvalid          = Prefix + "IMPLEMENTATION_CONFIG_INVALID"
	ImplementationRequiredInvalid        = Prefix + "IMPLEMENTATION_REQUIRED_INTERFACE_INVALID"
	ImplementationResourceInvalid        = Prefix + "IMPLEMENTATION_REQUIRED_RESOURCE_INVALID"
	ImplementationOptionalInvalid        = Prefix + "IMPLEMENTATION_OPTIONAL_INTERFACE_INVALID"
	ImplementationResultInvalid          = Prefix + "IMPLEMENTATION_RESULT_INVALID"
	ImplementationConformanceInvalid     = Prefix + "IMPLEMENTATION_CONFORMANCE_INVALID"
	InterfaceDeclarationInvalid          = Prefix + "INTERFACE_DECLARATION_INVALID"
	InterfaceContractInvalid             = Prefix + "INTERFACE_CONTRACT_INVALID"
	InterfaceMetadataInvalid             = Prefix + "INTERFACE_METADATA_INVALID"
	InterfaceIDDuplicate                 = Prefix + "INTERFACE_ID_DUPLICATE"
	ResourceDeclarationInvalid           = Prefix + "RESOURCE_DECLARATION_INVALID"
	ResourceContractInvalid              = Prefix + "RESOURCE_CONTRACT_INVALID"
	ResourceIDDuplicate                  = Prefix + "RESOURCE_ID_DUPLICATE"
	ResourceProviderDeclarationInvalid   = Prefix + "RESOURCE_PROVIDER_DECLARATION_INVALID"
	ResourceProviderInvalid              = Prefix + "RESOURCE_PROVIDER_INVALID"
	ResourceInstanceInvalid              = Prefix + "RESOURCE_INSTANCE_INVALID"
	ResourceBindingInvalid               = Prefix + "RESOURCE_BINDING_INVALID"
	ResourceBindingMissing               = Prefix + "RESOURCE_BINDING_MISSING"
	ResourceBindingAmbiguous             = Prefix + "RESOURCE_BINDING_AMBIGUOUS"
	ResourceMetadataInvalid              = Prefix + "RESOURCE_METADATA_INVALID"
	ResourceConfigurationSchemaInvalid   = Prefix + "RESOURCE_CONFIGURATION_SCHEMA_INVALID"
	ResourceConfigurationValuesInvalid   = Prefix + "RESOURCE_CONFIGURATION_VALUES_INVALID"
	DataMemberMetadataInvalid            = Prefix + "DATA_MEMBER_METADATA_INVALID"
	DataAssignmentInvalid                = Prefix + "DATA_ASSIGNMENT_INVALID"
	DataCompilerUnavailable              = Prefix + "DATA_COMPILER_UNAVAILABLE"
	AuthoredPackageInvalid               = Prefix + "AUTHORING_PACKAGE_INVALID"
)

const (
	ProjectCreateInvocationInvalid              = Prefix + "PROJECT_CREATE_INVOCATION_INVALID"
	ProjectCreateNameInvalid                    = Prefix + "PROJECT_CREATE_NAME_INVALID"
	ProjectCreateModuleInvalid                  = Prefix + "PROJECT_CREATE_MODULE_INVALID"
	ProjectCreateTemplateInvalid                = Prefix + "PROJECT_CREATE_TEMPLATE_INVALID"
	ProjectCreatePluginNameInvalid              = Prefix + "PROJECT_CREATE_PLUGIN_NAME_INVALID"
	ProjectCreatePluginIDInvalid                = Prefix + "PROJECT_CREATE_PLUGIN_ID_INVALID"
	ProjectCreateTargetExists                   = Prefix + "PROJECT_CREATE_TARGET_EXISTS"
	ProjectCreateGitUnavailable                 = Prefix + "PROJECT_CREATE_GIT_UNAVAILABLE"
	ProjectCreateGitInitializationFailed        = Prefix + "PROJECT_CREATE_GIT_INITIALIZATION_FAILED"
	ProjectCreateCancelled                      = Prefix + "PROJECT_CREATE_CANCELLED"
	ProjectCreateFailed                         = Prefix + "PROJECT_CREATE_FAILED"
	PluginCreateNameInvalid                     = Prefix + "PLUGIN_CREATE_NAME_INVALID"
	PluginCreateIDInvalid                       = Prefix + "PLUGIN_CREATE_ID_INVALID"
	PluginCreateTargetExists                    = Prefix + "PLUGIN_CREATE_TARGET_EXISTS"
	InterfaceCreateNameInvalid                  = Prefix + "INTERFACE_CREATE_NAME_INVALID"
	InterfaceCreateTargetExists                 = Prefix + "INTERFACE_CREATE_TARGET_EXISTS"
	ImplementationCreateContractInvalid         = Prefix + "IMPLEMENTATION_CREATE_CONTRACT_INVALID"
	ImplementationCreatePackageInvalid          = Prefix + "IMPLEMENTATION_CREATE_PACKAGE_INVALID"
	ImplementationCreateContractNotFound        = Prefix + "IMPLEMENTATION_CREATE_CONTRACT_NOT_FOUND"
	ImplementationCreateContractAmbiguous       = Prefix + "IMPLEMENTATION_CREATE_CONTRACT_AMBIGUOUS"
	ImplementationCreateContractUnimplementable = Prefix + "IMPLEMENTATION_CREATE_CONTRACT_UNIMPLEMENTABLE"
	ImplementationCreateTargetExists            = Prefix + "IMPLEMENTATION_CREATE_TARGET_EXISTS"
)

const (
	UseTargetInvalid        = Prefix + "USE_TARGET_INVALID"
	UseTargetNotFound       = Prefix + "USE_TARGET_NOT_FOUND"
	UseProviderIncompatible = Prefix + "USE_PROVIDER_INCOMPATIBLE"
	UseConstructorInvalid   = Prefix + "USE_CONSTRUCTOR_INVALID"
)

const (
	InspectCapabilitiesInvocationInvalid = Prefix + "INSPECT_CAPABILITIES_INVOCATION_INVALID"
	InspectCapabilitiesFailed            = Prefix + "INSPECT_CAPABILITIES_FAILED"
)

const (
	DoctorInvocationInvalid   = Prefix + "DOCTOR_INVOCATION_INVALID"
	DoctorPrerequisiteMissing = Prefix + "DOCTOR_PREREQUISITE_MISSING"
	DoctorFailed              = Prefix + "DOCTOR_FAILED"
)

const (
	ExplainInvocationInvalid = Prefix + "EXPLAIN_INVOCATION_INVALID"
	ExplainSubjectInvalid    = Prefix + "EXPLAIN_SUBJECT_INVALID"
	ExplainTargetNotFound    = Prefix + "EXPLAIN_TARGET_NOT_FOUND"
	ExplainFailed            = Prefix + "EXPLAIN_FAILED"
)

const (
	DependencyAddQueryInvalid    = Prefix + "DEPENDENCY_ADD_QUERY_INVALID"
	DependencyRemovePathInvalid  = Prefix + "DEPENDENCY_REMOVE_PATH_INVALID"
	DependencyRemoveNotSelected  = Prefix + "DEPENDENCY_REMOVE_NOT_SELECTED"
	DependencyUpdateQueryInvalid = Prefix + "DEPENDENCY_UPDATE_QUERY_INVALID"
	DependencyUpdateNotSelected  = Prefix + "DEPENDENCY_UPDATE_NOT_SELECTED"
)

const (
	CapabilityCreateReferenceInvalid        = Prefix + "CAPABILITY_CREATE_REFERENCE_INVALID"
	CapabilityCreateAlreadyVisible          = Prefix + "CAPABILITY_CREATE_ALREADY_VISIBLE"
	CapabilityCreateConfirmationRequired    = Prefix + "CAPABILITY_CREATE_CONFIRMATION_REQUIRED"
	CapabilityCreateVersionExhausted        = Prefix + "CAPABILITY_CREATE_VERSION_EXHAUSTED"
	CapabilityCreateIntentProfileRequired   = Prefix + "CAPABILITY_CREATE_INTENT_PROFILE_REQUIRED"
	CapabilityCreateIntentProfileNotAllowed = Prefix + "CAPABILITY_CREATE_INTENT_PROFILE_NOT_ALLOWED"
	CapabilityImplementReferenceInvalid     = Prefix + "CAPABILITY_IMPLEMENT_REFERENCE_INVALID"
	CapabilityImplementNotVisible           = Prefix + "CAPABILITY_IMPLEMENT_NOT_VISIBLE"
	CapabilityExposeReferenceInvalid        = Prefix + "CAPABILITY_EXPOSE_REFERENCE_INVALID"
	CapabilityExposeNotVisible              = Prefix + "CAPABILITY_EXPOSE_NOT_VISIBLE"
)

const (
	ConstructorConfigurationSchemaInvalid = Prefix + "CONSTRUCTOR_CONFIGURATION_SCHEMA_INVALID"
	ConstructorConfigurationValuesInvalid = Prefix + "CONSTRUCTOR_CONFIGURATION_VALUES_INVALID"
	ConstructorConfigurationUnselected    = Prefix + "CONSTRUCTOR_CONFIGURATION_UNSELECTED"
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
