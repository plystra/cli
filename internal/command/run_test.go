package command_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
)

var (
	wantCurrentUsage = strings.NewReplacer(
		"  plystra inspect [modules|interfaces|implementations|configuration] [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n",
		"  plystra inspect capabilities [--format human|json]\n  plystra inspect [modules|interfaces|implementations|resources|configuration] [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n",
	).Replace(wantUsage)
	wantCurrentInspectUsage = strings.NewReplacer(
		"  plystra inspect implementations [--verbose]",
		"  plystra inspect resources [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra inspect implementations [--verbose]",
		"  implementations        Show visible constructors",
		"  resources              Show Resource contracts, named instances, bindings, and owning sources.\n  implementations        Show visible constructors",
		"The configuration view retains",
		"The resources view retains contract_digest on resource-contract nodes, selected\nresource-instance nodes, and depends-on-resource edges with exact parameter\nnames, positions, and explicit or unique-compatible reasons. Inspection never\nconstructs values. Invalid Resource declarations, contracts, or duplicate identities\nreport RESOURCE_DECLARATION_INVALID, RESOURCE_CONTRACT_INVALID, or\nRESOURCE_ID_DUPLICATE codes with the PLYSTRA_ prefix and owning sources.\nNamed instances use resources.instances.<name>.use and typed per-instance config.\nNames are 1..128 ASCII bytes in dot-separated lower-kebab segments. Bindings use\nresources.bind.implementations.<constructor>.<parameter> or\nresources.bind.instances.<consumer-instance>.<parameter> with an exact target name.\nEvery selected instance is active, even unconsumed; one provider under different\nnames is not deduplicated. A provider change discards the old instance config.\nMissing, ambiguous, or invalid bindings fail before generation with\nPLYSTRA_RESOURCE_BINDING_MISSING, PLYSTRA_RESOURCE_BINDING_AMBIGUOUS, or\nPLYSTRA_RESOURCE_BINDING_INVALID. Resources never become Interface roots,\ncatalog entries, governed proxies, or transports. Resource mutation forms of\nuse and implement are not installed; Data remains unsupported.\nThe configuration view retains",
		"configuration provenance, and reachable assembly membership.\n",
		"configuration provenance, and reachable assembly membership.\nDependency edges retain exact parameter_name and one-based parameter_position;\nother edges omit these fields. Repeated parameters remain distinct, and human\noutput includes each dependency name and position.\nResource parameters appear as declared dependencies with reason resource and\nexact resource-contract identity and digest, never as Interface requirements.\nMalformed Resource parameters report PLYSTRA_IMPLEMENTATION_REQUIRED_RESOURCE_INVALID.\nReachable consumers bind to an exact compatible named instance, or implicitly\nto the uniquely compatible selected instance. Dormant Implementations stay\ninactive, but explicit binding addresses are still validated.\n",
		"Usage:\n",
		"Usage:\n  plystra inspect capabilities [--format human|json]\n",
		"\n\nViews:\n",
		"\n\nViews:\n  capabilities           Show installed command, schema, selector, default, effect, limit, toolchain, and support facts.\n",
		"\n\nThe command is read-only and resolves",
		"\n\nInstalled capability discovery is Project-independent and accepts only\n--format. It ignores PLYSTRA_ENV and PLYSTRA_CONFIG, and invalid Projects cannot\nalter its installed facts. Human output summarizes command, selector, and effect\ncounts and identifies omitted command argument, selector, effect-class, and\ntransport component details. JSON nests one plystra.capabilities/v1 payload in\nplystra.result/v1 and reports exact installed command and schema support without\ninventing planned commands or unsupported schema identities.\nAdmission defaults report 64 attempts per exact binding, queue 0, and the\nKernel maximum of 65536; authored concurrency policies remain unsupported.\nExplicit --verbose, --env, or --config is invalid and emits\nPLYSTRA_INSPECT_CAPABILITIES_INVOCATION_INVALID.\n\nThe remaining commands are read-only and resolve",
	).Replace(wantInspectUsage)
)

const (
	wantUsage = `Usage:
  plystra help
  plystra version
  plystra new <project-name> [options]
  plystra add <go-module-query>
  plystra remove <go-module-path>
  plystra update <go-module-query>
  plystra use <interface-id> <constructor-symbol> [--env <environment>|--config <yaml-path>]
  plystra plugin create <name>
  plystra interface create <interface-name>
  plystra implement <interface-id> --package <project-relative-package>
  plystra capability create <capability-name> [--query] [--plugin <plugin>] [--interactive] [--confirm] [--expose]
  plystra capability implement <capability-name>/vN [--plugin <plugin>] [--interactive]
  plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]
  plystra guidance sync [--replace-generated]
  plystra guidance check
  plystra inspect [modules|interfaces|implementations|configuration] [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain capability <capability-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain plugin <plugin-id> [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain config <field-path> [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain alias <alias-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain exposure <capability-or-alias-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra check [--env <environment>|--config <yaml-path>]
  plystra generate [--check] [--env <environment>|--config <yaml-path>]

Common actionable failures end with one Recovery block containing the primary
command or file edit and one stable PLYSTRA_<AREA>_<CONDITION> Diagnostic code.
`
	wantUsageSource = "Typed source-bearing failures, including invalid Project and Go Module\ndeclarations, safe missing configuration selections, concurrent Project inputs,\ngenerated-output conflicts and drift, application module dependency drift,\ngeneration-activation failures,\nselected-Provider generation-extension mismatches, ambiguous local Plugin\ntargets, invalid explicit choices, and\nmissing, ambiguous, or cyclic Interface Implementation resolution, add\ncanonical module-relative Source lines first.\n"
	wantAddUsage    = "Usage:\n  plystra add <go-module-query>\n\nAdds one ordinary Go Module dependency, regenerates, tidies, and validates the\ncomplete Project in one rollback boundary. Dependency Project configuration\nremains inert unless reached through the root Project's template ancestry.\n\nMalformed queries emit PLYSTRA_DEPENDENCY_ADD_QUERY_INVALID before Project\ndiscovery or mutation.\n"
	wantRemoveUsage = "Usage:\n  plystra remove <go-module-path>\n\nRemoves one ordinary Go Module dependency, regenerates, tidies, and validates\nthe complete Project in one rollback boundary. Dependency Project configuration\nremains inert unless reached through the root Project's template ancestry.\n\nMalformed paths emit PLYSTRA_DEPENDENCY_REMOVE_PATH_INVALID before Project\ndiscovery or mutation.\nValid paths absent from go.mod emit PLYSTRA_DEPENDENCY_REMOVE_NOT_SELECTED\nbefore mutation.\n"
	wantUpdateUsage = "Usage:\n  plystra update <go-module-query>\n\nUpdates one selected ordinary Go Module dependency, regenerates, tidies, and\nvalidates the complete Project in one rollback boundary. Dependency Project\nconfiguration remains inert unless the selected current Project explicitly\nselects it through the root template ancestry.\n\nMalformed queries emit PLYSTRA_DEPENDENCY_UPDATE_QUERY_INVALID before Project\ndiscovery or mutation.\nValid queries whose module path is absent from go.mod emit\nPLYSTRA_DEPENDENCY_UPDATE_NOT_SELECTED before mutation.\n"
	wantUseUsage    = "Usage:\n  plystra use <interface-id> <constructor-symbol> [--env <environment>|--config <yaml-path>]\n\nOptions:\n  --env <environment>    Write the Implementation choice to plystra.<environment>.yaml.\n  --config <yaml-path>   Write the Implementation choice to one complete replacement configuration.\n\nAn exact compatible choice may be recorded before its Interface is required. It\nremains dormant without creating a root, binding, constructor, or generated\nInterface runtime until that Interface becomes reachable; invalid choices are\nrejected immediately.\n\nAn effective choice for an intrinsic kernel.* Interface emits\nPLYSTRA_RESOLVE_INTRINSIC_INTERFACE_SELECTION with every contributing\nimplementation-selection Source. Set that interfaces.use entry to {$remove: true} in the\nselected current-Project document to remove either a local or template choice.\n\nPLYSTRA_ENV and PLYSTRA_CONFIG supply equivalent selectors when no explicit\nselector is present; setting both is an error. Explicit --env or --config\noverrides both variables, and the two flags cannot be combined. Relative\nconfiguration paths are resolved from the detected Plystra Project root.\nInvalid or conflicting selections emit the stable\nPLYSTRA_CONFIGURATION_SELECTION_INVALID diagnostic.\nA normalized Project-contained selected document that cannot be loaded reports\none span-less configuration-selection source; conflicting or unsafe selectors\nreport none.\n"
	wantNewUsage    = `Usage:
  plystra new <project-name> [--module <go-module-path>] [--template <go-module-query>] [--plugin <name>] [--git] [--github-ci] [--interactive] [--no-agent-guidance] [--format human|json]

Options:
  --module <go-module-path> Set the Go Module path; defaults to the project name.
  --template <module-query> Create from one public, portable Plystra Project dependency.
  --plugin <name>           Create an initial root-level plugin.
  --git                     Initialize a Git repository; default is off.
  --github-ci               Include GitHub Actions CI; default is off.
  --interactive             Prompt for omitted Git and GitHub CI choices.
  --no-agent-guidance       Omit version-matched Plystra Agent guidance.
  --format human|json       Select human output or one plystra.result/v1 document.

Creation is non-interactive by default in every environment. Agent guidance is
generated by default. Only --interactive permits prompts; terminal detection
never activates them.

JSON success nests one plystra.project-created/v1 payload with the Go Module
path and relative created directory. Enter payload.directory and run
plystra check independently before treating creation as complete.

Creation records --template as a direct dependency and root template relationship.
Its linear ancestry immediately supplies the supported Interface baseline,
including CORS, without copying source or configuration or ranking candidates.
Resource and Data inheritance remain unsupported.

Invalid inherited declarations retain their owning source and specific
diagnostic with exit 3. Ambiguous Implementation choices require a decision
with exit 4. Both failures leave the requested target absent.

Invalid Project names, explicit Go Module paths, and template queries emit
PLYSTRA_PROJECT_CREATE_NAME_INVALID, PLYSTRA_PROJECT_CREATE_MODULE_INVALID,
and PLYSTRA_PROJECT_CREATE_TEMPLATE_INVALID.

Invalid initial Plugin names and derived IDs emit
PLYSTRA_PROJECT_CREATE_PLUGIN_NAME_INVALID and
PLYSTRA_PROJECT_CREATE_PLUGIN_ID_INVALID.

An existing Project target emits PLYSTRA_PROJECT_CREATE_TARGET_EXISTS and is
never replaced or modified.

Unavailable Git emits PLYSTRA_PROJECT_CREATE_GIT_UNAVAILABLE. Git processes
that start but fail initialization emit
PLYSTRA_PROJECT_CREATE_GIT_INITIALIZATION_FAILED. Both leave no target Project.

Template dependencies must be public, portable, and generation-stable. Creation
rejects the staged Project unless immediate generation checking, applicable
JavaScript SDK dependency installation plus typecheck/build/package validation,
Project checks, the read-only Go package build, and an isolated lifecycle health
smoke all succeed. Validation-only npm output is removed before installation.
`
	wantNewInvalidInvocation             = wantNewUsage + "\nRecovery:\nReview `plystra new --help`, then rerun `plystra new <project-name> [options]` with one valid argument form.\n\nDiagnostic: PLYSTRA_PROJECT_CREATE_INVOCATION_INVALID\n"
	wantPluginUsage                      = "Usage:\n  plystra plugin create <name>\n"
	wantPluginCreateUsage                = "Usage:\n  plystra plugin create <name>\n\nCreates one root-level Plugin scaffold and derives its exact Plugin ID from the\ncurrent Project module namespace plus the lower-case ASCII kebab-case name. The\nname must not be reserved, and the target directory must not already exist.\n\nInvalid names, unformable derived IDs, and existing targets emit\nPLYSTRA_PLUGIN_CREATE_NAME_INVALID, PLYSTRA_PLUGIN_CREATE_ID_INVALID, and\nPLYSTRA_PLUGIN_CREATE_TARGET_EXISTS respectively.\n"
	wantGenerateUsage                    = "Usage:\n  plystra generate [--check] [--env <environment>|--config <yaml-path>]\n\nOptions:\n  --check                Report drift without modifying configuration or generated files.\n  --env <environment>    Overlay root plystra.yaml with plystra.<environment>.yaml.\n  --config <yaml-path>   Use one complete current-project configuration instead of root plystra.yaml.\n\nPLYSTRA_ENV and PLYSTRA_CONFIG supply equivalent selectors when no explicit\nselector is present; setting both is an error. Explicit --env or --config\noverrides both variables, and the two flags cannot be combined. Relative\nconfiguration paths are resolved from the detected Plystra Project root. Root\nplystra.yaml remains mandatory and is not merged beneath --config. Invalid or\nconflicting selections emit the stable PLYSTRA_CONFIGURATION_SELECTION_INVALID\ndiagnostic.\nA normalized Project-contained selected document that cannot be loaded reports\none span-less configuration-selection source; conflicting or unsafe selectors\nreport none.\nTemplate Project roots compose oldest to nearest below the current Project delta.\nGeneration does not rewrite the selected current-Project configuration document.\n"
	wantCheckUsage                       = "Usage:\n  plystra check [--env <environment>|--config <yaml-path>]\n\nOptions:\n  --env <environment>    Check root plystra.yaml with plystra.<environment>.yaml.\n  --config <yaml-path>   Check one complete current-project configuration instead of root plystra.yaml.\n\nThe check is read-only: it validates the selected application model and generated\noutput, then runs go test -mod=readonly ./... when both are current. PLYSTRA_ENV and\nPLYSTRA_CONFIG supply equivalent selectors when no explicit selector is present;\nsetting both is an error. Explicit --env or --config overrides both variables,\nand the two flags cannot be combined. Relative configuration paths are resolved\nfrom the detected Plystra Project root. Root plystra.yaml remains mandatory and\nis not merged beneath --config. Invalid or conflicting selections emit the\nstable PLYSTRA_CONFIGURATION_SELECTION_INVALID diagnostic.\nA normalized Project-contained selected document that cannot be loaded reports\none span-less configuration-selection source; conflicting or unsafe selectors\nreport none.\nInvalid template ancestry and invalid or unselected constructor configuration\nfailures emit module-relative\nconfiguration-declaration sources before selector-aware recovery.\n"
	wantCapabilityUsage                  = "Usage:\n  plystra capability create <capability-name> [--query] [--plugin <plugin>] [--interactive] [--confirm] [--expose]\n  plystra capability implement <capability-name>/vN [--plugin <plugin>] [--interactive]\n  plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]\n"
	wantCapabilityArgumentUsage          = "usage:\n  plystra capability create <capability-name> [--query] [--plugin <plugin>] [--interactive] [--confirm] [--expose]\n  plystra capability implement <capability-name>/vN [--plugin <plugin>] [--interactive]\n  plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]\n"
	wantCapabilityCreateArgumentUsage    = "usage: plystra capability create <capability-name> [--query] [--plugin <plugin>] [--interactive] [--confirm] [--expose]\n"
	wantCapabilityImplementArgumentUsage = "usage: plystra capability implement <capability-name>/vN [--plugin <plugin>] [--interactive]\n"
	wantGuidanceUsage                    = "Usage:\n  plystra guidance sync [--replace-generated]\n  plystra guidance check\n\nCommands:\n  sync                   Refresh the installed catalog's manifest-owned guidance.\n  check                  Compare guidance with the installed catalog without mutation.\n\nOptions:\n  --replace-generated    Discard edits only in existing bounded regular paths owned by the prior manifest.\n\nOrdinary sync changes or removes only unchanged paths listed by the previous\nmanifest. Missing prior-owned paths and desired paths absent from previous\nownership block both sync modes, whether the desired path is missing or already\noccupied. The CLI never creates, edits, deletes, or claims optional\n.agents/skills/plystra/local.md, unlisted files, sibling skills, or\nrepository-wide Agent instructions.\n\nPLYSTRA_AGENT_GUIDANCE_DRIFT reports every stale, missing, unlisted, or manually\nmodified guidance path as a path-only agent-guidance source. Check is always\nnon-mutating, and a blocked sync leaves every Project file unchanged.\nPLYSTRA_AGENT_GUIDANCE_MANIFEST_INVALID reports the ownership manifest as a\npath-only agent-guidance source. PLYSTRA_PROJECT_CONCURRENT_CHANGE reports each\nknown guidance transaction path that changed after inspection.\n"
	wantInspectUsage                     = "Usage:\n  plystra inspect [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra inspect modules [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra inspect interfaces [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra inspect implementations [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra inspect configuration [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n\nViews:\n  modules                Show the selected current-model module graph.\n  interfaces             Show visible Interfaces, active selections, and constructor dependencies.\n  implementations        Show visible constructors, selection state, dependencies, configuration, and assembly.\n  configuration          Show selected layers, field ownership, precedence, removals, and suppression.\n\nOptions:\n  --verbose              Add the complete deterministic resolution evidence to human output.\n  --format human|json    Select concise human output or the versioned inspect schema.\n  --env <environment>    Inspect root plystra.yaml with plystra.<environment>.yaml.\n  --config <yaml-path>   Inspect one complete current-project configuration instead of root plystra.yaml.\n\nThe command is read-only and resolves the same selected application model used\nby generation and validation. JSON stdout contains exactly one schema document;\nprogress and diagnostics use stderr. Graph views emit the versioned plystra.graph\nv1 schema with project-relative source references and no resolved Secrets or\nunrestricted configuration values. The interfaces view retains visible inactive\nInterfaces, intrinsic Kernel ownership, root requirements, active constructor\nselections and reasons, and required or optional constructor dependencies.\nThe implementations view retains every visible constructor candidate, active\nand dormant selections, declared and resolved dependencies, constructor-owned\nconfiguration provenance, and reachable assembly membership.\nThe configuration view retains the selected current-Project layer, selected\ntemplate ancestry, redacted field summaries, ownership and precedence, effective\nand overridden contributions, explicit removals, and ancestor suppression.\nPLYSTRA_ENV and PLYSTRA_CONFIG\nsupply equivalent selectors when no explicit selector is present; setting both\nis an error. Explicit --env or --config overrides both variables, and the two\nflags cannot be combined. Relative configuration paths are resolved from the\ndetected Plystra Project root. Root plystra.yaml remains mandatory and is not\nmerged beneath --config. Invalid or conflicting selections emit the stable\nPLYSTRA_CONFIGURATION_SELECTION_INVALID diagnostic.\nA normalized Project-contained selected document that cannot be loaded reports\none span-less configuration-selection source; conflicting or unsafe selectors\nreport none.\n"
	wantExplainCapabilityUsage           = "Usage:\n  plystra explain capability <capability-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra explain plugin <plugin-id> [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra explain config <field-path> [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra explain alias <alias-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra explain exposure <capability-or-alias-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n\nOptions:\n  --verbose              Add the complete deterministic resolution evidence to human output.\n  --format human|json    Select concise human output or one plystra.result/v1 document.\n  --env <environment>    Explain the model selected by plystra.<environment>.yaml.\n  --config <yaml-path>   Explain one complete current-project configuration instead of root plystra.yaml.\n\nThe command is read-only and explains one canonical Capability, Plugin, typed\nconfiguration-field, application-local Alias, or public-exposure decision from\nthe same selected application model used by generation and validation. Plugin\nconfiguration fields accept the dotted form config.<plugin-id>.<field>. JSON\nstdout contains one plystra.result/v1 document with a plystra.explain/v1 payload,\nstructured diagnostics, and plystra.recovery/v1 actions. JSON stderr stays empty\nafter initialization; human progress and diagnostics use stderr. Executable\nrecovery uses exact argv; Provider choices require editing capabilities.use,\nwhile Interface Implementation choices use plystra use. Invalid invocation or\nsubject exits 2; invalid Project or missing target exits 3; required decisions\nor unavailable prerequisites exit 4; internal failures exit 8 with redacted\nPLYSTRA_EXPLAIN_FAILED. PLYSTRA_ENV and PLYSTRA_CONFIG supply selectors when no\nexplicit selector is present; setting both is an error. Explicit --env or\n--config overrides both variables,\nand the two flags cannot be combined. Relative configuration paths are resolved\nfrom the detected Plystra Project root. Root plystra.yaml remains mandatory and\nis not merged beneath --config. Invalid or conflicting selections emit the\nstable PLYSTRA_CONFIGURATION_SELECTION_INVALID diagnostic.\nA normalized Project-contained selected document that cannot be loaded reports\none span-less configuration-selection source; conflicting or unsafe selectors\nreport none.\n"
	wantCapabilityExposeUsage            = "Usage:\n  plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]\n\nOptions:\n  --env <environment>    Write exposure to plystra.<environment>.yaml.\n  --config <yaml-path>   Write exposure to one complete replacement configuration.\n\nThe current Connect unary boundary accepts a canonical Capability whose\nexplicit semantics.kind is query or command. Exposing an event or stream fails\nthe transaction; remove it from http.expose until that operation kind is\nsupported rather than relabeling the contract.\nPLYSTRA_PROTOBUF_OPERATION_KIND_UNSUPPORTED reports the effective http.expose\ndocument at 1:1 as an exposure source before selector-aware recovery.\n\nPLYSTRA_ENV and PLYSTRA_CONFIG supply equivalent selectors when no explicit\nselector is present; setting both is an error. Explicit --env or --config\noverrides both variables, and the two flags cannot be combined. Relative\nconfiguration paths are resolved from the detected Plystra Project root.\nInvalid or conflicting selections emit the stable\nPLYSTRA_CONFIGURATION_SELECTION_INVALID diagnostic.\nA normalized Project-contained selected document that cannot be loaded reports\none span-less configuration-selection source; conflicting or unsafe selectors\nreport none.\nMalformed exact Capability IDs emit the stable\nPLYSTRA_CAPABILITY_EXPOSE_REFERENCE_INVALID diagnostic before Project discovery\nor mutation.\nA well-formed exact Capability absent from the selected visible catalog emits\nPLYSTRA_CAPABILITY_EXPOSE_NOT_VISIBLE before write planning or mutation.\nAn invalid authored source contract emits PLYSTRA_CAPABILITY_MANIFEST_INVALID\nwith its owning module-relative capability.yaml at 1:1 as a\nprovider-declaration source before mutation.\nExposure writes an exact http.expose entry with transport: connect.\nSuperseded global switches and exposure set forms fail before mutation.\n"
	wantCapabilityCreateUsage            = `Usage:
  plystra capability create <capability-name> [--query] [--plugin <plugin>] [--interactive] [--confirm] [--expose]

Options:
  --interactive   Prompt for a Plugin only when target inference remains ambiguous.

Intent profiles:
  --query   Create a read-only, safely retryable query contract for a new Capability identity.

A complete explicit --plugin selection is non-interactive. Without it, the
command checks the enclosing Plugin and then a sole local Plugin. Only
--interactive permits a terminal prompt after those forms remain ambiguous;
requesting interaction without an available terminal emits
PLYSTRA_PLUGIN_TARGET_INVALID before mutation.

A new Capability identity requires one explicit intent profile. A later version
copies the complete semantics of its highest visible source contract; omit the
profile flag in that case. Names never imply semantics.

Malformed Capability names or optional exact IDs emit the stable
PLYSTRA_CAPABILITY_CREATE_REFERENCE_INVALID diagnostic before Project discovery
or mutation.
An already-visible exact version emits
PLYSTRA_CAPABILITY_CREATE_ALREADY_VISIBLE and directs the developer to the
implementation command without changing the Project.
An explicit older or skipped new version emits
PLYSTRA_CAPABILITY_CREATE_CONFIRMATION_REQUIRED and requires the same create
command to be repeated with --confirm before mutation.
An omitted version whose highest visible major is already the maximum emits
PLYSTRA_CAPABILITY_CREATE_VERSION_EXHAUSTED and requires a new Capability
identity before mutation.
A missing new-identity profile emits
PLYSTRA_CAPABILITY_CREATE_INTENT_PROFILE_REQUIRED; a profile supplied while
copying a later version emits PLYSTRA_CAPABILITY_CREATE_INTENT_PROFILE_NOT_ALLOWED.
Both failures occur before Project mutation.
If local Plugin inference is ambiguous without --interactive, the command emits
PLYSTRA_PLUGIN_TARGET_AMBIGUOUS with every current-Project candidate plugin.yaml
at 1:1 as a module-relative plugin-declaration source before mutation.
If visible Providers disagree on the source exact contract, the command emits
PLYSTRA_CAPABILITY_SCHEMA_CONFLICT with every module-relative capability.yaml
declaration before mutation.
An invalid authored source contract emits PLYSTRA_CAPABILITY_MANIFEST_INVALID
with its owning module-relative capability.yaml at 1:1 as a
provider-declaration source before mutation.
The --expose option writes an exact http.expose entry with transport: connect.
Invalid exposure configuration fails without installing the creation.
`
	wantCapabilityImplementUsage = `Usage:
  plystra capability implement <capability-name>/vN [--plugin <plugin>] [--interactive]

Options:
  --interactive   Prompt for a Plugin only when target inference remains ambiguous.

A complete explicit --plugin selection is non-interactive. Without it, the
command checks the enclosing Plugin and then a sole local Plugin. Only
--interactive permits a terminal prompt after those forms remain ambiguous;
requesting interaction without an available terminal emits
PLYSTRA_PLUGIN_TARGET_INVALID before mutation.

Malformed exact Capability IDs emit the stable
PLYSTRA_CAPABILITY_IMPLEMENT_REFERENCE_INVALID diagnostic before Project
discovery or mutation.
A valid exact version that is not visible emits
PLYSTRA_CAPABILITY_IMPLEMENT_NOT_VISIBLE and directs the developer to the
creation command without changing the Project.
If local Plugin inference is ambiguous without --interactive, the command emits
PLYSTRA_PLUGIN_TARGET_AMBIGUOUS with every current-Project candidate plugin.yaml
at 1:1 as a module-relative plugin-declaration source before mutation.
If visible Providers disagree on that exact contract, the command emits
PLYSTRA_CAPABILITY_SCHEMA_CONFLICT with every module-relative capability.yaml
declaration before mutation.
An invalid authored source contract emits PLYSTRA_CAPABILITY_MANIFEST_INVALID
with its owning module-relative capability.yaml at 1:1 as a
provider-declaration source before mutation.
`
	wantInterfaceUsage       = "Usage:\n  plystra interface create <interface-name>\n"
	wantInterfaceCreateUsage = "Usage:\n  plystra interface create <interface-name>\n\nCreates the initial v1 canonical Go package for one unversioned Interface name.\nThe name must contain two or more lower-case dot-separated segments. The command\ndoes not create optional metadata or edit application configuration.\nAn existing target reports its module-relative package path. A visible ID in\nanother package reports the owning Interface declaration and its exact span.\nChoose a different name; the existing source is never changed.\n"
	wantImplementUsage       = "Usage:\n  plystra implement <interface-id> --package <project-relative-package>\n\nCreates a new ordinary Go package that implements one visible canonical\nInterface. The package path must begin with ./ and its target directory must not\nalready exist. The scaffold imports the canonical Interface package and creates\nno copied contract, generated substitute, configuration, or registration code.\n"
)

const (
	wantDiagnosticSourceUsage             = "PLYSTRA_PROJECT_MANIFEST_INVALID reports the owning current or dependency\nProject plystra.yaml as a project-marker source. Malformed readable documents\nuse 1:1; unsafe or unreadable markers omit the unavailable span.\nPLYSTRA_CONFIGURATION_INVALID reports a malformed selected environment or\ncomplete-replacement document at 1:1 as a configuration-declaration source.\nPLYSTRA_ENVIRONMENT_OVERLAY_INVALID reports the selected overlay document at\n1:1 as a configuration-declaration source before selector-aware recovery.\nPLYSTRA_GENERATED_OWNERSHIP_CONFLICT reports the desired managed path occupied\nby different unowned bytes or a non-regular entry as a generated-artifact source\nwithout a fabricated span.\nPLYSTRA_GENERATED_MANIFEST_INVALID reports the current Project's\ngenerated/.plystra-manifest.json as a generated-artifact source without a\nfabricated span.\nPLYSTRA_PROTOBUF_WIRE_HISTORY_INVALID reports the current Project's\ngenerated/proto/wire-map.json as a generated-artifact source without a\nfabricated span.\nPLYSTRA_GENERATED_DRIFT reports each stale, missing, or manually modified\nmanaged path as a generated-artifact source without a fabricated span.\nPLYSTRA_GENERATED_UNEXPECTED_OUTPUT reports each unexpected unowned path as a\ngenerated-artifact source without a fabricated span.\nPLYSTRA_GO_MODULE_INVALID reports an exact current-Project go.mod module or\nrequirement position as a module-dependency source once Project identity is valid.\nPLYSTRA_APPLICATION_DEPENDENCY_DRIFT reports current-Project go.mod at 1:1 as\na module-dependency source. Normal generation repairs the required direct\nKernel and generated runtime dependencies; check modes remain read-only.\nPLYSTRA_CONSTRUCTOR_CONFIGURATION_SCHEMA_INVALID reports the owning config\ndocument at 1:1 as a configuration-declaration source without values.\nPLYSTRA_CONSTRUCTOR_CONFIGURATION_VALUES_INVALID reports the owning config\ndocument at 1:1 while retaining only a redacted-safe field path.\nPLYSTRA_CONSTRUCTOR_CONFIGURATION_UNSELECTED reports every effective\ncontributing config document at 1:1 as a configuration-declaration source\nwithout values.\nPLYSTRA_RESOLVE_UNKNOWN_INTERFACE reports every module-relative requirement,\nexposure, or Implementation-selection source before selector-aware recovery.\nPLYSTRA_RESOLVE_RESERVED_INTERFACE reports the module-relative declaration that\nuses the reserved kernel.* namespace before recovery.\n"
	wantProtobufIdentitySourceUsage       = "PLYSTRA_PROTOBUF_IDENTITY_COLLISION reports the owning Interface Go contract at\nits declaration position as an interface-contract source before recovery.\n"
	wantProtobufOperationSourceUsage      = "PLYSTRA_PROTOBUF_OPERATION_KIND_UNSUPPORTED reports the effective http.expose\ndocument at 1:1 as an exposure source before selector-aware recovery.\n"
	wantProtobufPointerSourceUsage        = "PLYSTRA_PROTOBUF_POINTER_PROJECTION_UNSUPPORTED reports the effective http.expose\ndocument at 1:1 as an exposure source before selector-aware recovery.\n"
	wantCapabilityManifestSourceUsage     = "PLYSTRA_CAPABILITY_MANIFEST_INVALID reports the invalid authored capability.yaml\nat 1:1 as a provider-declaration source before recovery.\n"
	wantGenerationActivationSourceUsage   = "PLYSTRA_GENERATION_ACTIVATION_CONFLICT reports each conflicting plugin.yaml\ngeneration.activations entry at its exact position as a sorted and deduplicated\nplugin-declaration source.\nPLYSTRA_GENERATION_ACTIVATION_MISSING reports retained typed Capability\nrequirement sources for the reported unclaimed extension namespace, including\ndeclaration and exposure sources, in shared sorted and deduplicated order. It\nnever fabricates a source for the absent generation.activations declaration.\nPLYSTRA_GENERATION_PROVIDER_EXTENSION_MISSING reports the selected Provider's\ncapability.yaml at 1:1 as a provider-declaration source and every effective\ncapabilities.use document at 1:1 as a provider-selection source. Sources are\nsorted and deduplicated; it never fabricates a source for absent generation\nsupport.\nPLYSTRA_GENERATION_ACTIVATION_CYCLE reports retained typed sources carried by\nthe complete cycle edges, including declaration, exposure, and derived\nactivation facts, in sorted and deduplicated order. A bare cycle sentinel has no\nsource.\nPLYSTRA_GENERATION_DEPENDENCY_CYCLE reports retained typed sources carried by\nthe complete mixed activation and generated-requirement cycle, including\ndeclaration, exposure, activation, and generation-rule facts, in sorted and\ndeduplicated order. A bare dependency-cycle sentinel has no source.\n"
	wantGenerationContributionSourceUsage = "PLYSTRA_GENERATION_CONTRIBUTION_CYCLE reports the generation-rule source for\nevery contribution in the complete token-dependency cycle, in sorted and\ndeduplicated order. A bare contribution-cycle sentinel has no source.\nPLYSTRA_GENERATION_CONTRIBUTIONS_UNORDERED reports the generation-rule source\nfor every simultaneously ready contribution at the ordered generation point,\nin sorted and deduplicated order. A bare unordered sentinel has no source.\nPLYSTRA_GENERATION_STATE_REPEATED reports each selected extension declaration\nwhose output changed for identical normalized input as a sorted and deduplicated\nplugin-declaration source. A bare repeated-state sentinel has no source.\nPLYSTRA_GENERATION_NONCONVERGENT reports generation-rule sources from the most\nrecent pass that added unseen requirements, in sorted and deduplicated order. A\nbare convergence sentinel has no source.\nPLYSTRA_GENERATION_EXTENSION_DIAGNOSTIC reports generation-rule sources for\nevery distinct selected-extension rule that returned a structured error\ndiagnostic, in sorted and deduplicated order. Several rules owned by one Plugin\nshare one human source, info and warning diagnostics do not fail generation,\nand a bare extension-diagnostic sentinel has no source.\nPLYSTRA_GENERATION_API_UNSUPPORTED reports the exact unsupported generation.api\nscalar as a plugin-declaration source. A bare API sentinel has no source.\nPLYSTRA_GENERATION_PACKAGE_INVALID reports the exact generation.package scalar\nas a plugin-declaration source. A bare package sentinel has no source.\nPLYSTRA_GENERATION_COMPILE_FAILED reports the selected helper's exact\ngeneration.package scalar as a plugin-declaration source. A bare compile\nsentinel has no source.\nPLYSTRA_GENERATION_* helper invocation failures report the selected helper's\nexact generation.package scalar as a plugin-declaration source. A compile\ntimeout uses PLYSTRA_GENERATION_TIMEOUT with the same source. Bare or unlocated\ninvocation, orchestration, and aggregate cleanup errors have no source.\n"
	wantConcurrentSourceUsage             = "PLYSTRA_PROJECT_CONCURRENT_CHANGE reports each known changed Project\nconfiguration, go.mod/go.sum, or generated path as a sorted path-only\nconfiguration-declaration, module-dependency, or generated-artifact source\nwithout a fabricated span.\n"
	wantPolicyUsage                       = "PLYSTRA_POLICY_NOT_ENFORCED rejects a reachable Interface policy unless the\ninstalled CLI/Kernel pair both generates and executes it. The diagnostic reports\nthe field, support stages, and module-relative configuration declarations.\nRemove the policy or use a compatible stack; inspect capabilities reports support.\nDormant policies remain intent, not enforced guarantees.\n"
	wantGenerateUsageWithSources          = wantGenerateUsage + wantPolicyUsage + wantDiagnosticSourceUsage + wantProtobufIdentitySourceUsage + wantProtobufOperationSourceUsage + wantProtobufPointerSourceUsage + wantCapabilityManifestSourceUsage + wantGenerationActivationSourceUsage + wantGenerationContributionSourceUsage + wantConcurrentSourceUsage
	wantCheckUsageWithSources             = wantCheckUsage + wantPolicyUsage + wantDiagnosticSourceUsage + wantProtobufIdentitySourceUsage + wantProtobufOperationSourceUsage + wantProtobufPointerSourceUsage + wantCapabilityManifestSourceUsage + wantGenerationActivationSourceUsage + wantGenerationContributionSourceUsage + wantConcurrentSourceUsage
)

func TestRunHelp(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		arguments := arguments
		t.Run(commandName(arguments), func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run(arguments, &stdout, &stderr); exitCode != 0 {
				t.Fatalf("Run(%q) exit code = %d, want 0", arguments, exitCode)
			}
			if stdout.String() != wantCurrentUsage+wantUsageSource {
				t.Fatalf("Run(%q) stdout = %q, want %q", arguments, stdout.String(), wantCurrentUsage+wantUsageSource)
			}
			if stderr.Len() != 0 {
				t.Fatalf("Run(%q) stderr = %q, want empty", arguments, stderr.String())
			}
		})
	}
}

func TestRunGenerateHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"generate", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantGenerateUsageWithSources || stderr.Len() != 0 {
			t.Fatalf("Run(generate %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunCheckHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"check", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantCheckUsageWithSources || stderr.Len() != 0 {
			t.Fatalf("Run(check %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunGuidanceHelp(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{
		{"guidance", "help"},
		{"guidance", "-h"},
		{"guidance", "--help"},
		{"guidance", "check", "help"},
		{"guidance", "check", "-h"},
		{"guidance", "check", "--help"},
		{"guidance", "sync", "help"},
		{"guidance", "sync", "-h"},
		{"guidance", "sync", "--help"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run(arguments, &stdout, &stderr); exitCode != 0 || stdout.String() != wantGuidanceUsage || stderr.Len() != 0 {
			t.Fatalf("Run(%q) = %d, stdout %q, stderr %q", arguments, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunInspectHelp(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{
		{"inspect", "help"},
		{"inspect", "-h"},
		{"inspect", "--help"},
		{"inspect", "capabilities", "help"},
		{"inspect", "capabilities", "-h"},
		{"inspect", "capabilities", "--help"},
		{"inspect", "modules", "help"},
		{"inspect", "modules", "-h"},
		{"inspect", "modules", "--help"},
		{"inspect", "interfaces", "help"},
		{"inspect", "interfaces", "-h"},
		{"inspect", "interfaces", "--help"},
		{"inspect", "resources", "help"},
		{"inspect", "resources", "-h"},
		{"inspect", "resources", "--help"},
		{"inspect", "implementations", "help"},
		{"inspect", "implementations", "-h"},
		{"inspect", "implementations", "--help"},
		{"inspect", "configuration", "help"},
		{"inspect", "configuration", "-h"},
		{"inspect", "configuration", "--help"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run(arguments, &stdout, &stderr); exitCode != 0 || stdout.String() != wantCurrentInspectUsage || stderr.Len() != 0 {
			t.Fatalf("Run(%q) = %d, stdout %q, stderr %q", arguments, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunExplainHelp(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{
		{"explain", "help"},
		{"explain", "-h"},
		{"explain", "--help"},
		{"explain", "capability", "help"},
		{"explain", "capability", "-h"},
		{"explain", "capability", "--help"},
		{"explain", "plugin", "help"},
		{"explain", "plugin", "-h"},
		{"explain", "plugin", "--help"},
		{"explain", "config", "help"},
		{"explain", "config", "-h"},
		{"explain", "config", "--help"},
		{"explain", "alias", "help"},
		{"explain", "alias", "-h"},
		{"explain", "alias", "--help"},
		{"explain", "exposure", "help"},
		{"explain", "exposure", "-h"},
		{"explain", "exposure", "--help"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run(arguments, &stdout, &stderr); exitCode != 0 || stdout.String() != wantExplainCapabilityUsage || stderr.Len() != 0 {
			t.Fatalf("Run(%q) = exit %d, stdout %q, stderr %q", arguments, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunNewHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"new", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantNewUsage || stderr.Len() != 0 {
			t.Fatalf("Run(new %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunAddHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"add", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantAddUsage || stderr.Len() != 0 {
			t.Fatalf("Run(add %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunRemoveHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"remove", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantRemoveUsage || stderr.Len() != 0 {
			t.Fatalf("Run(remove %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunUpdateHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"update", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantUpdateUsage || stderr.Len() != 0 {
			t.Fatalf("Run(update %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunUseHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"use", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantUseUsage || stderr.Len() != 0 {
			t.Fatalf("Run(use %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunPluginHelp(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"plugin", "help"}, want: wantPluginUsage},
		{arguments: []string{"plugin", "create", "--help"}, want: wantPluginCreateUsage},
	} {
		test := test
		t.Run(strings.Join(test.arguments, "-"), func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run(test.arguments, &stdout, &stderr); exitCode != 0 || stdout.String() != test.want || stderr.Len() != 0 {
				t.Fatalf("Run(%q) = exit %d, stdout %q, stderr %q", test.arguments, exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"version", "-version", "--version"} {
		argument := argument
		t.Run(argument, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run([]string{argument}, &stdout, &stderr); exitCode != 0 {
				t.Fatalf("Run(%q) exit code = %d, want 0", argument, exitCode)
			}
			if stdout.String() != "plystra 0.1.0\n" {
				t.Fatalf("Run(%q) stdout = %q", argument, stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("Run(%q) stderr = %q, want empty", argument, stderr.String())
			}
		})
	}
}

func TestRunCapabilityHelp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"capability", "help"}, want: wantCapabilityUsage},
		{arguments: []string{"capability", "create", "--help"}, want: wantCapabilityCreateUsage},
		{arguments: []string{"capability", "implement", "-h"}, want: wantCapabilityImplementUsage},
		{arguments: []string{"capability", "expose", "help"}, want: wantCapabilityExposeUsage},
	}
	for _, test := range tests {
		test := test
		t.Run(strings.Join(test.arguments, "-"), func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run(test.arguments, &stdout, &stderr); exitCode != 0 || stdout.String() != test.want || stderr.Len() != 0 {
				t.Fatalf("Run(%q) = exit %d, stdout %q, stderr %q", test.arguments, exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunInterfaceHelp(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"interface", "help"}, want: wantInterfaceUsage},
		{arguments: []string{"interface", "create", "--help"}, want: wantInterfaceCreateUsage},
	} {
		test := test
		t.Run(strings.Join(test.arguments, "-"), func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run(test.arguments, &stdout, &stderr); exitCode != 0 || stdout.String() != test.want || stderr.Len() != 0 {
				t.Fatalf("Run(%q) = exit %d, stdout %q, stderr %q", test.arguments, exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunImplementHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"implement", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantImplementUsage || stderr.Len() != 0 {
			t.Fatalf("Run(implement %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunRejectsUnknownCommandAndExtraArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		wantError string
	}{
		{name: "unknown", arguments: []string{"unknown"}, wantError: "unknown command \"unknown\"\n\n" + wantCurrentUsage + wantUsageSource},
		{name: "help arguments", arguments: []string{"help", "extra"}, wantError: "help does not accept arguments\n"},
		{name: "version arguments", arguments: []string{"version", "extra"}, wantError: "version does not accept arguments\n"},
		{name: "new missing project", arguments: []string{"new"}, wantError: wantNewInvalidInvocation},
		{name: "new unknown option", arguments: []string{"new", "app", "--unknown"}, wantError: wantNewInvalidInvocation},
		{name: "new missing module path", arguments: []string{"new", "app", "--module"}, wantError: wantNewInvalidInvocation},
		{name: "new duplicate module path", arguments: []string{"new", "app", "--module", "example.com/a", "--module", "example.com/b"}, wantError: wantNewInvalidInvocation},
		{name: "new missing template query", arguments: []string{"new", "app", "--template"}, wantError: wantNewInvalidInvocation},
		{name: "new duplicate template query", arguments: []string{"new", "app", "--template", "example.com/a", "--template", "example.com/b"}, wantError: wantNewInvalidInvocation},
		{name: "new missing adopted export", arguments: []string{"new", "app", "--template", "example.com/a", "--adopt-export"}, wantError: wantNewInvalidInvocation},
		{name: "new adopted export without template", arguments: []string{"new", "app", "--adopt-export", "defaults"}, wantError: wantNewInvalidInvocation},
		{name: "new removed adopted export", arguments: []string{"new", "app", "--template", "example.com/a", "--adopt-export", "defaults"}, wantError: wantNewInvalidInvocation},
		{name: "new removed adopted export equals form", arguments: []string{"new", "app", "--template", "example.com/a", "--adopt-export=defaults"}, wantError: wantNewInvalidInvocation},
		{name: "new removed repeated adopted exports", arguments: []string{"new", "app", "--template", "example.com/a", "--adopt-export", "defaults", "--adopt-export", "runtime"}, wantError: wantNewInvalidInvocation},
		{name: "new missing plugin name", arguments: []string{"new", "app", "--plugin"}, wantError: wantNewInvalidInvocation},
		{name: "new removed library option", arguments: []string{"new", "app", "--library"}, wantError: wantNewInvalidInvocation},
		{name: "new extra argument", arguments: []string{"new", "app", "extra"}, wantError: wantNewInvalidInvocation},
		{name: "new duplicate git", arguments: []string{"new", "app", "--git", "--git"}, wantError: wantNewInvalidInvocation},
		{name: "new removed no git", arguments: []string{"new", "app", "--no-git"}, wantError: wantNewInvalidInvocation},
		{name: "new removed no github ci", arguments: []string{"new", "app", "--no-github-ci"}, wantError: wantNewInvalidInvocation},
		{name: "new removed skills", arguments: []string{"new", "app", "--skills"}, wantError: wantNewInvalidInvocation},
		{name: "new removed no skills", arguments: []string{"new", "app", "--no-skills"}, wantError: wantNewInvalidInvocation},
		{name: "new missing format", arguments: []string{"new", "app", "--format"}, wantError: wantNewInvalidInvocation},
		{name: "new invalid format", arguments: []string{"new", "app", "--format", "yaml"}, wantError: wantNewInvalidInvocation},
		{name: "add missing query", arguments: []string{"add"}, wantError: wantAddUsage},
		{name: "add option", arguments: []string{"add", "--upgrade"}, wantError: wantAddUsage},
		{name: "add extra argument", arguments: []string{"add", "example.com/platform", "extra"}, wantError: wantAddUsage},
		{name: "remove missing path", arguments: []string{"remove"}, wantError: wantRemoveUsage},
		{name: "remove option", arguments: []string{"remove", "--all"}, wantError: wantRemoveUsage},
		{name: "remove extra argument", arguments: []string{"remove", "example.com/platform", "extra"}, wantError: wantRemoveUsage},
		{name: "update missing query", arguments: []string{"update"}, wantError: wantUpdateUsage},
		{name: "update option", arguments: []string{"update", "--all"}, wantError: wantUpdateUsage},
		{name: "update extra argument", arguments: []string{"update", "example.com/platform", "extra"}, wantError: wantUpdateUsage},
		{name: "use missing arguments", arguments: []string{"use"}, wantError: wantUseUsage},
		{name: "use missing Plugin", arguments: []string{"use", "email.send/v1"}, wantError: wantUseUsage},
		{name: "plugin missing subcommand", arguments: []string{"plugin"}, wantError: wantPluginUsage},
		{name: "plugin unknown subcommand", arguments: []string{"plugin", "remove", "account"}, wantError: wantPluginUsage},
		{name: "plugin missing name", arguments: []string{"plugin", "create"}, wantError: wantPluginCreateUsage},
		{name: "plugin extra argument", arguments: []string{"plugin", "create", "account", "extra"}, wantError: wantPluginCreateUsage},
		{name: "interface missing subcommand", arguments: []string{"interface"}, wantError: wantInterfaceUsage},
		{name: "interface unknown subcommand", arguments: []string{"interface", "remove"}, wantError: wantInterfaceUsage},
		{name: "interface create missing name", arguments: []string{"interface", "create"}, wantError: wantInterfaceCreateUsage},
		{name: "interface create extra argument", arguments: []string{"interface", "create", "records.list", "extra"}, wantError: wantInterfaceCreateUsage},
		{name: "interface create unavailable intent option", arguments: []string{"interface", "create", "records.list", "--query"}, wantError: wantInterfaceCreateUsage},
		{name: "implement missing Interface", arguments: []string{"implement"}, wantError: wantImplementUsage},
		{name: "implement missing package option", arguments: []string{"implement", "records.list/v1"}, wantError: wantImplementUsage},
		{name: "implement missing package value", arguments: []string{"implement", "records.list/v1", "--package"}, wantError: wantImplementUsage},
		{name: "implement unknown option", arguments: []string{"implement", "records.list/v1", "--plugin", "records"}, wantError: wantImplementUsage},
		{name: "implement duplicate package option", arguments: []string{"implement", "records.list/v1", "--package", "./memory", "--package", "./postgres"}, wantError: wantImplementUsage},
		{name: "implement extra argument", arguments: []string{"implement", "records.list/v1", "--package", "./postgres", "extra"}, wantError: wantImplementUsage},
		{name: "capability missing subcommand", arguments: []string{"capability"}, wantError: wantCapabilityArgumentUsage},
		{name: "capability unknown subcommand", arguments: []string{"capability", "remove", "records.create/v1"}, wantError: wantCapabilityArgumentUsage},
		{name: "capability create missing reference", arguments: []string{"capability", "create"}, wantError: wantCapabilityCreateArgumentUsage},
		{name: "capability implement missing reference", arguments: []string{"capability", "implement"}, wantError: wantCapabilityImplementArgumentUsage},
		{name: "capability expose missing reference", arguments: []string{"capability", "expose"}, wantError: "usage: plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]\n"},
		{name: "capability implement confirm", arguments: []string{"capability", "implement", "records.create/v1", "--confirm"}, wantError: wantCapabilityImplementArgumentUsage},
		{name: "capability implement expose", arguments: []string{"capability", "implement", "records.create/v1", "--expose"}, wantError: wantCapabilityImplementArgumentUsage},
		{name: "capability create duplicate interactive", arguments: []string{"capability", "create", "records.create", "--interactive", "--interactive"}, wantError: wantCapabilityCreateArgumentUsage},
		{name: "capability implement duplicate interactive", arguments: []string{"capability", "implement", "records.create/v1", "--interactive", "--interactive"}, wantError: wantCapabilityImplementArgumentUsage},
		{name: "capability expose interactive", arguments: []string{"capability", "expose", "records.create/v1", "--interactive"}, wantError: "usage: plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]\n"},
		{name: "capability expose extra option", arguments: []string{"capability", "expose", "records.create/v1", "--confirm"}, wantError: "usage: plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]\n"},
		{name: "capability create missing plugin", arguments: []string{"capability", "create", "records.create", "--plugin"}, wantError: wantCapabilityCreateArgumentUsage},
		{name: "guidance missing action", arguments: []string{"guidance"}, wantError: wantGuidanceUsage},
		{name: "guidance unknown action", arguments: []string{"guidance", "update"}, wantError: wantGuidanceUsage},
		{name: "guidance check replacement", arguments: []string{"guidance", "check", "--replace-generated"}, wantError: wantGuidanceUsage},
		{name: "guidance sync duplicate replacement", arguments: []string{"guidance", "sync", "--replace-generated", "--replace-generated"}, wantError: wantGuidanceUsage},
		{name: "guidance sync unknown option", arguments: []string{"guidance", "sync", "--unknown"}, wantError: wantGuidanceUsage},
		{name: "inspect unknown option", arguments: []string{"inspect", "--graph"}, wantError: wantCurrentInspectUsage},
		{name: "inspect unknown view", arguments: []string{"inspect", "unknown"}, wantError: wantCurrentInspectUsage},
		{name: "inspect duplicate view", arguments: []string{"inspect", "modules", "modules"}, wantError: wantCurrentInspectUsage},
		{name: "inspect view help with extra option", arguments: []string{"inspect", "modules", "--help", "--format", "json"}, wantError: wantCurrentInspectUsage},
		{name: "inspect duplicate verbose", arguments: []string{"inspect", "--verbose", "--verbose"}, wantError: wantCurrentInspectUsage},
		{name: "inspect missing format", arguments: []string{"inspect", "--format"}, wantError: wantCurrentInspectUsage},
		{name: "inspect unknown format", arguments: []string{"inspect", "--format", "yaml"}, wantError: wantCurrentInspectUsage},
		{name: "inspect duplicate format", arguments: []string{"inspect", "--format", "human", "--format", "json"}, wantError: wantCurrentInspectUsage},
		{name: "inspect missing configuration path", arguments: []string{"inspect", "--config"}, wantError: wantCurrentInspectUsage},
		{name: "inspect duplicate configuration", arguments: []string{"inspect", "--config", "a.yaml", "--config", "b.yaml"}, wantError: wantCurrentInspectUsage},
		{name: "inspect missing environment", arguments: []string{"inspect", "--env"}, wantError: wantCurrentInspectUsage},
		{name: "inspect duplicate environment", arguments: []string{"inspect", "--env", "test", "--env", "production"}, wantError: wantCurrentInspectUsage},
		{name: "explain missing subject", arguments: []string{"explain", "capability"}, wantError: wantExplainCapabilityUsage},
		{name: "explain missing Plugin subject", arguments: []string{"explain", "plugin"}, wantError: wantExplainCapabilityUsage},
		{name: "explain missing Alias subject", arguments: []string{"explain", "alias"}, wantError: wantExplainCapabilityUsage},
		{name: "explain missing exposure subject", arguments: []string{"explain", "exposure"}, wantError: wantExplainCapabilityUsage},
		{name: "explain unsupported subject kind", arguments: []string{"explain", "configuration", "config.acme.email.host"}, wantError: wantExplainCapabilityUsage},
		{name: "explain unknown option", arguments: []string{"explain", "capability", "email.send/v1", "--graph"}, wantError: wantExplainCapabilityUsage},
		{name: "explain duplicate verbose", arguments: []string{"explain", "capability", "email.send/v1", "--verbose", "--verbose"}, wantError: wantExplainCapabilityUsage},
		{name: "explain missing format", arguments: []string{"explain", "capability", "email.send/v1", "--format"}, wantError: wantExplainCapabilityUsage},
		{name: "explain unknown format", arguments: []string{"explain", "capability", "email.send/v1", "--format", "yaml"}, wantError: wantExplainCapabilityUsage},
		{name: "explain missing configuration", arguments: []string{"explain", "capability", "email.send/v1", "--config"}, wantError: wantExplainCapabilityUsage},
		{name: "explain duplicate configuration", arguments: []string{"explain", "capability", "email.send/v1", "--config", "a.yaml", "--config", "b.yaml"}, wantError: wantExplainCapabilityUsage},
		{name: "explain missing environment", arguments: []string{"explain", "capability", "email.send/v1", "--env"}, wantError: wantExplainCapabilityUsage},
		{name: "explain duplicate environment", arguments: []string{"explain", "capability", "email.send/v1", "--env", "test", "--env", "production"}, wantError: wantExplainCapabilityUsage},
		{name: "generate unknown option", arguments: []string{"generate", "--write"}, wantError: wantGenerateUsageWithSources},
		{name: "generate duplicate check", arguments: []string{"generate", "--check", "--check"}, wantError: wantGenerateUsageWithSources},
		{name: "generate missing configuration path", arguments: []string{"generate", "--config"}, wantError: wantGenerateUsageWithSources},
		{name: "generate duplicate configuration", arguments: []string{"generate", "--config", "a.yaml", "--config", "b.yaml"}, wantError: wantGenerateUsageWithSources},
		{name: "generate missing environment", arguments: []string{"generate", "--env"}, wantError: wantGenerateUsageWithSources},
		{name: "generate duplicate environment", arguments: []string{"generate", "--env", "test", "--env", "production"}, wantError: wantGenerateUsageWithSources},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run(test.arguments, &stdout, &stderr); exitCode != 2 {
				t.Fatalf("Run(%q) exit code = %d, want 2", test.arguments, exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("Run(%q) stdout = %q, want empty", test.arguments, stdout.String())
			}
			wantError := test.wantError
			if test.arguments[0] == "explain" {
				wantError += "\nRecovery:\nReview `plystra explain --help`, then rerun one complete explanation command.\n\nDiagnostic: PLYSTRA_EXPLAIN_INVOCATION_INVALID\n"
			}
			if stderr.String() != wantError {
				t.Fatalf("Run(%q) stderr = %q, want %q", test.arguments, stderr.String(), wantError)
			}
		})
	}
}

func TestRunNewRejectsRemovedExportAdoptionWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, form := range [][]string{
		{"--adopt-export", "defaults"},
		{"--adopt-export=defaults"},
		{"--adopt-export", "defaults", "--adopt-export", "runtime"},
	} {
		for _, format := range []string{"human", "json"} {
			t.Run(strings.Join(form, " ")+"/"+format, func(t *testing.T) {
				t.Parallel()
				parent := t.TempDir()
				arguments := []string{"new", "app", "--template", "example.com/acme/platform@v1.2.3", "--interactive"}
				arguments = append(arguments, form...)
				arguments = append(arguments, "--format", format)
				var stdout, stderr bytes.Buffer
				if exitCode := command.RunIn(arguments, &stdout, &stderr, parent, []string{"PATH=", "GOPROXY=off"}); exitCode != 2 {
					t.Fatalf("RunIn = %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
				}
				if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
					t.Fatalf("removed option mutated parent: %v, %v", entries, err)
				}
			})
		}
	}
}

func TestRunNewPreservesJSONIntentForDuplicateFormat(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := command.Run([]string{"new", "app", "--format", "human", "--format", "json"}, &stdout, &stderr)
	if exitCode != 2 || stderr.Len() != 0 || !strings.HasSuffix(stdout.String(), "\n") || strings.Count(strings.TrimSpace(stdout.String()), "\n") != 0 {
		t.Fatalf("Run duplicate format = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	var document struct {
		Schema      string `json:"schema"`
		Operation   string `json:"operation"`
		Status      string `json:"status"`
		ExitClass   int    `json:"exit_class"`
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("decode duplicate-format result: %v", err)
	}
	if document.Schema != "plystra.result/v1" || document.Operation != "new" || document.Status != "invalid_invocation" || document.ExitClass != 2 || len(document.Diagnostics) != 1 || document.Diagnostics[0].Code != "PLYSTRA_PROJECT_CREATE_INVOCATION_INVALID" {
		t.Fatalf("duplicate-format result = %#v", document)
	}
}

func TestRunExplainPreservesJSONIntentForDuplicateFormat(t *testing.T) {
	t.Parallel()
	for _, formats := range [][]string{{"human", "json"}, {"json", "human"}} {
		var stdout, stderr bytes.Buffer
		exitCode := command.Run([]string{"explain", "capability", "email.send/v1", "--format", formats[0], "--format", formats[1]}, &stdout, &stderr)
		if exitCode != 2 || stderr.Len() != 0 || strings.Count(stdout.String(), "\n") != 1 {
			t.Fatalf("duplicate format = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
		}
		document := decodeExplainCommandEnvelope(t, stdout.String())
		if document.Schema != "plystra.result/v1" || document.Operation != "explain.capability" || document.Status != "invalid_invocation" || document.ExitClass != 2 || len(document.Diagnostics) != 1 || document.Diagnostics[0].Code != "PLYSTRA_EXPLAIN_INVOCATION_INVALID" {
			t.Fatalf("duplicate format result = %#v", document)
		}
	}
}

func TestRunRejectsMissingWriters(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	if exitCode := command.Run(nil, nil, &output); exitCode != 2 {
		t.Fatalf("Run with nil stdout exit code = %d, want 2", exitCode)
	}
	if exitCode := command.Run(nil, &output, nil); exitCode != 2 {
		t.Fatalf("Run with nil stderr exit code = %d, want 2", exitCode)
	}
}

func commandName(arguments []string) string {
	if len(arguments) == 0 {
		return "empty"
	}
	return arguments[0]
}
