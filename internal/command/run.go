// Package command owns the user-facing Plystra command dispatcher.
package command

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/dependencyadd"
	"github.com/plystra/cli/internal/dependencyremove"
	"github.com/plystra/cli/internal/dependencyupdate"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/newproject"
	"github.com/plystra/cli/internal/plugincreate"
	"github.com/plystra/cli/internal/plugintarget"
	"github.com/plystra/cli/internal/projectcheck"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/version"
	kernelintrinsic "github.com/plystra/kernel/intrinsic"
)

const (
	generationCommandTimeout = 15 * time.Minute
	usage                    = `Usage:
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
  plystra capability create <capability-name> [--query] [--plugin <plugin>] [--confirm] [--expose]
  plystra capability implement <capability-name>/vN [--plugin <plugin>]
  plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]
  plystra inspect [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain capability <capability-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain plugin <plugin-id> [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain config <field-path> [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain alias <alias-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain exposure <capability-or-alias-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra check [--env <environment>|--config <yaml-path>]
  plystra generate [--check] [--env <environment>|--config <yaml-path>]

Common actionable failures end with one Recovery block containing the primary
command or file edit and one stable PLYSTRA_<AREA>_<CONDITION> Diagnostic code.
Typed source-bearing failures, including invalid Project and Go Module
declarations, safe missing configuration selections, current-Project
configuration-composition, concurrent Project inputs, generated-output conflicts
and drift, application module dependency drift, ambiguous local Plugin targets,
invalid explicit choices, and missing, ambiguous, or cyclic Interface
Implementation resolution, add canonical module-relative Source lines first.
`
	addUsage = `Usage:
  plystra add <go-module-query>

Adds one ordinary Go Module dependency, recomposes root plystra.yaml, regenerates,
tidies, and validates the complete Project in one rollback boundary.

Malformed queries emit PLYSTRA_DEPENDENCY_ADD_QUERY_INVALID before Project
discovery or mutation.
`
	removeUsage = `Usage:
  plystra remove <go-module-path>

Removes one ordinary Go Module dependency, recomposes root plystra.yaml,
regenerates, tidies, and validates the complete Project in one rollback boundary.

Malformed paths emit PLYSTRA_DEPENDENCY_REMOVE_PATH_INVALID before Project
discovery or mutation.
Valid paths absent from go.mod emit PLYSTRA_DEPENDENCY_REMOVE_NOT_SELECTED
before mutation.
`
	updateUsage = `Usage:
  plystra update <go-module-query>

Updates one selected ordinary Go Module dependency, recomposes root plystra.yaml,
regenerates, tidies, and validates the complete Project in one rollback boundary.

Malformed queries emit PLYSTRA_DEPENDENCY_UPDATE_QUERY_INVALID before Project
discovery or mutation.
Valid queries whose module path is absent from go.mod emit
PLYSTRA_DEPENDENCY_UPDATE_NOT_SELECTED before mutation.
`
	newUsage = `Usage:
  plystra new <project-name> [--module <go-module-path>] [--template <go-module-query>] [--plugin <name>] [--git|--no-git] [--github-ci|--no-github-ci] [--skills|--no-skills]

Options:
  --module <go-module-path> Set the Go Module path; defaults to the project name.
  --template <module-query> Create from one public, portable Plystra Project dependency.
  --plugin <name>           Create an initial root-level plugin.
  --git, --no-git           Initialize or omit a Git repository.
  --github-ci, --no-github-ci
                            Include or omit GitHub Actions CI.
  --skills, --no-skills     Include or omit Plystra agent skills.

Interactive creation asks for each unspecified choice. Non-interactive creation
must specify one flag from every choice pair. Omitting a pair emits
PLYSTRA_PROJECT_CREATE_CHOICE_REQUIRED before target creation.

Invalid Project names, explicit Go Module paths, and template queries emit
PLYSTRA_PROJECT_CREATE_NAME_INVALID, PLYSTRA_PROJECT_CREATE_MODULE_INVALID,
and PLYSTRA_PROJECT_CREATE_TEMPLATE_INVALID.

Invalid initial Plugin names and derived IDs emit
PLYSTRA_PROJECT_CREATE_PLUGIN_NAME_INVALID and
PLYSTRA_PROJECT_CREATE_PLUGIN_ID_INVALID.

An existing Project target emits PLYSTRA_PROJECT_CREATE_TARGET_EXISTS and is
never replaced or modified.

Requested Git initialization failures emit
PLYSTRA_PROJECT_CREATE_GIT_INITIALIZATION_FAILED and leave no target Project.

Template dependencies must be public, portable, and generation-stable. Creation
rejects the staged Project unless immediate generation checking, applicable
JavaScript SDK dependency installation plus typecheck/build/package validation,
Project checks, the read-only Go package build, and an isolated lifecycle health
smoke all succeed. Validation-only npm output is removed before installation.
`
	pluginUsage = `Usage:
  plystra plugin create <name>
`
	pluginCreateUsage = `Usage:
  plystra plugin create <name>

Creates one root-level Plugin scaffold and derives its exact Plugin ID from the
current Project module namespace plus the lower-case ASCII kebab-case name. The
name must not be reserved, and the target directory must not already exist.

Invalid names, unformable derived IDs, and existing targets emit
PLYSTRA_PLUGIN_CREATE_NAME_INVALID, PLYSTRA_PLUGIN_CREATE_ID_INVALID, and
PLYSTRA_PLUGIN_CREATE_TARGET_EXISTS respectively.
`
	generateUsage = `Usage:
  plystra generate [--check] [--env <environment>|--config <yaml-path>]

Options:
  --check                Report drift without modifying configuration or generated files.
  --env <environment>    Overlay root plystra.yaml with plystra.<environment>.yaml.
  --config <yaml-path>   Use one complete current-project configuration instead of root plystra.yaml.

PLYSTRA_ENV and PLYSTRA_CONFIG supply equivalent selectors when no explicit
selector is present; setting both is an error. Explicit --env or --config
overrides both variables, and the two flags cannot be combined. Relative
configuration paths are resolved from the detected Plystra Project root. Root
plystra.yaml remains mandatory and is not merged beneath --config. Invalid or
conflicting selections emit the stable PLYSTRA_CONFIGURATION_SELECTION_INVALID
diagnostic.
A normalized Project-contained selected document that cannot be loaded reports
one span-less configuration-selection source; conflicting or unsafe selectors
report none.
PLYSTRA_PROJECT_MANIFEST_INVALID reports the owning current or dependency
Project plystra.yaml as a project-marker source. Malformed readable documents
use 1:1; unsafe or unreadable markers omit the unavailable span.
PLYSTRA_CONFIGURATION_INVALID reports a malformed selected environment or
complete-replacement document at 1:1 as a configuration-declaration source.
PLYSTRA_HTTP_TRANSPORT_SELECTION_INVALID reports effective http.expose
documents at 1:1 as exposure sources before selector-aware recovery.
PLYSTRA_ENVIRONMENT_OVERLAY_INVALID reports the selected overlay document at
1:1 as a configuration-declaration source before selector-aware recovery.
PLYSTRA_CONFIGURATION_COMPOSITION_DRIFT reports the maintained current-Project
configuration document at 1:1 as a configuration-declaration source.
PLYSTRA_GENERATED_OWNERSHIP_CONFLICT reports the desired managed path occupied
by different unowned bytes or a non-regular entry as a generated-artifact source
without a fabricated span.
PLYSTRA_GENERATED_MANIFEST_INVALID reports the current Project's
generated/.plystra-manifest.json as a generated-artifact source without a
fabricated span.
PLYSTRA_PROTOBUF_WIRE_HISTORY_INVALID reports the current Project's
generated/proto/wire-map.json as a generated-artifact source without a
fabricated span.
PLYSTRA_GENERATED_DRIFT reports each stale, missing, or manually modified
managed path as a generated-artifact source without a fabricated span.
PLYSTRA_GENERATED_UNEXPECTED_OUTPUT reports each unexpected unowned path as a
generated-artifact source without a fabricated span.
PLYSTRA_GO_MODULE_INVALID reports an exact current-Project go.mod module or
requirement position as a module-dependency source once Project identity is valid.
PLYSTRA_APPLICATION_DEPENDENCY_DRIFT reports current-Project go.mod at 1:1 as
a module-dependency source. Normal generation repairs the required direct
Kernel and generated runtime dependencies; check modes remain read-only.
PLYSTRA_CONSTRUCTOR_CONFIGURATION_SCHEMA_INVALID reports the owning config
document at 1:1 as a configuration-declaration source without values.
PLYSTRA_CONSTRUCTOR_CONFIGURATION_VALUES_INVALID reports the owning config
document at 1:1 while retaining only a redacted-safe field path.
PLYSTRA_CONSTRUCTOR_CONFIGURATION_UNSELECTED reports every effective
contributing config document at 1:1 as a configuration-declaration source
without values.
PLYSTRA_RESOLVE_UNKNOWN_INTERFACE reports every module-relative requirement,
exposure, or Implementation-selection source before selector-aware recovery.
PLYSTRA_RESOLVE_RESERVED_INTERFACE reports the module-relative declaration that
uses the reserved kernel.* namespace before recovery.
PLYSTRA_PROTOBUF_IDENTITY_COLLISION reports the owning Interface Go contract at
its declaration position as an interface-contract source before recovery.
PLYSTRA_PROTOBUF_OPERATION_KIND_UNSUPPORTED reports the effective http.expose
document at 1:1 as an exposure source before selector-aware recovery.
PLYSTRA_CAPABILITY_MANIFEST_INVALID reports the invalid authored capability.yaml
at 1:1 as a provider-declaration source before recovery.
PLYSTRA_PROJECT_CONCURRENT_CHANGE reports each known changed Project
configuration, go.mod/go.sum, or generated path as a sorted path-only
configuration-declaration, module-dependency, or generated-artifact source
without a fabricated span.
`
	checkUsage = `Usage:
  plystra check [--env <environment>|--config <yaml-path>]

Options:
  --env <environment>    Check root plystra.yaml with plystra.<environment>.yaml.
  --config <yaml-path>   Check one complete current-project configuration instead of root plystra.yaml.

The check is read-only: it verifies dependency composition and generated output,
then runs go test -mod=readonly ./... when both are current. PLYSTRA_ENV and
PLYSTRA_CONFIG supply equivalent selectors when no explicit selector is present;
setting both is an error. Explicit --env or --config overrides both variables,
and the two flags cannot be combined. Relative configuration paths are resolved
from the detected Plystra Project root. Root plystra.yaml remains mandatory and
is not merged beneath --config. Invalid or conflicting selections emit the
stable PLYSTRA_CONFIGURATION_SELECTION_INVALID diagnostic.
A normalized Project-contained selected document that cannot be loaded reports
one span-less configuration-selection source; conflicting or unsafe selectors
report none.
Inherited configuration conflicts, ambiguous ownership, and invalid or
unselected constructor configuration failures emit module-relative
configuration-declaration sources before selector-aware recovery.
PLYSTRA_PROJECT_MANIFEST_INVALID reports the owning current or dependency
Project plystra.yaml as a project-marker source. Malformed readable documents
use 1:1; unsafe or unreadable markers omit the unavailable span.
PLYSTRA_CONFIGURATION_INVALID reports a malformed selected environment or
complete-replacement document at 1:1 as a configuration-declaration source.
PLYSTRA_HTTP_TRANSPORT_SELECTION_INVALID reports effective http.expose
documents at 1:1 as exposure sources before selector-aware recovery.
PLYSTRA_ENVIRONMENT_OVERLAY_INVALID reports the selected overlay document at
1:1 as a configuration-declaration source before selector-aware recovery.
PLYSTRA_CONFIGURATION_COMPOSITION_DRIFT reports the maintained current-Project
configuration document at 1:1 as a configuration-declaration source.
PLYSTRA_GENERATED_OWNERSHIP_CONFLICT reports the desired managed path occupied
by different unowned bytes or a non-regular entry as a generated-artifact source
without a fabricated span.
PLYSTRA_GENERATED_MANIFEST_INVALID reports the current Project's
generated/.plystra-manifest.json as a generated-artifact source without a
fabricated span.
PLYSTRA_PROTOBUF_WIRE_HISTORY_INVALID reports the current Project's
generated/proto/wire-map.json as a generated-artifact source without a
fabricated span.
PLYSTRA_GENERATED_DRIFT reports each stale, missing, or manually modified
managed path as a generated-artifact source without a fabricated span.
PLYSTRA_GENERATED_UNEXPECTED_OUTPUT reports each unexpected unowned path as a
generated-artifact source without a fabricated span.
PLYSTRA_GO_MODULE_INVALID reports an exact current-Project go.mod module or
requirement position as a module-dependency source once Project identity is valid.
PLYSTRA_APPLICATION_DEPENDENCY_DRIFT reports current-Project go.mod at 1:1 as
a module-dependency source. Normal generation repairs the required direct
Kernel and generated runtime dependencies; check modes remain read-only.
PLYSTRA_CONSTRUCTOR_CONFIGURATION_SCHEMA_INVALID reports the owning config
document at 1:1 as a configuration-declaration source without values.
PLYSTRA_CONSTRUCTOR_CONFIGURATION_VALUES_INVALID reports the owning config
document at 1:1 while retaining only a redacted-safe field path.
PLYSTRA_CONSTRUCTOR_CONFIGURATION_UNSELECTED reports every effective
contributing config document at 1:1 as a configuration-declaration source
without values.
PLYSTRA_RESOLVE_UNKNOWN_INTERFACE reports every module-relative requirement,
exposure, or Implementation-selection source before selector-aware recovery.
PLYSTRA_RESOLVE_RESERVED_INTERFACE reports the module-relative declaration that
uses the reserved kernel.* namespace before recovery.
PLYSTRA_PROTOBUF_IDENTITY_COLLISION reports the owning Interface Go contract at
its declaration position as an interface-contract source before recovery.
PLYSTRA_PROTOBUF_OPERATION_KIND_UNSUPPORTED reports the effective http.expose
document at 1:1 as an exposure source before selector-aware recovery.
PLYSTRA_CAPABILITY_MANIFEST_INVALID reports the invalid authored capability.yaml
at 1:1 as a provider-declaration source before recovery.
PLYSTRA_PROJECT_CONCURRENT_CHANGE reports each known changed Project
configuration, go.mod/go.sum, or generated path as a sorted path-only
configuration-declaration, module-dependency, or generated-artifact source
without a fabricated span.
`
	inspectUsage = `Usage:
  plystra inspect [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]

Options:
  --verbose              Add the complete deterministic resolution evidence to human output.
  --format human|json    Select concise human output or the plystra.inspect v1 JSON schema.
  --env <environment>    Inspect root plystra.yaml with plystra.<environment>.yaml.
  --config <yaml-path>   Inspect one complete current-project configuration instead of root plystra.yaml.

The command is read-only and resolves the same selected application model used
by generation and validation. JSON stdout contains exactly one schema document;
progress and diagnostics use stderr. PLYSTRA_ENV and PLYSTRA_CONFIG supply
equivalent selectors when no explicit selector is present; setting both is an
error. Explicit --env or --config overrides both variables, and the two flags
cannot be combined. Relative configuration paths are resolved from the detected
Plystra Project root. Root plystra.yaml remains mandatory and is not merged
beneath --config. Invalid or conflicting selections emit the stable
PLYSTRA_CONFIGURATION_SELECTION_INVALID diagnostic.
A normalized Project-contained selected document that cannot be loaded reports
one span-less configuration-selection source; conflicting or unsafe selectors
report none.
`
	explainUsage = `Usage:
  plystra explain capability <capability-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain plugin <plugin-id> [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain config <field-path> [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain alias <alias-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]
  plystra explain exposure <capability-or-alias-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]

Options:
  --verbose              Add the complete deterministic resolution evidence to human output.
  --format human|json    Select concise human output or the plystra.explain v1 JSON schema.
  --env <environment>    Explain the model selected by plystra.<environment>.yaml.
  --config <yaml-path>   Explain one complete current-project configuration instead of root plystra.yaml.

The command is read-only and explains one canonical Capability, Plugin, typed
configuration-field, application-local Alias, or public-exposure decision from
the same selected application model used by generation and validation. Plugin
configuration fields accept the dotted form config.<plugin-id>.<field>. JSON
stdout contains exactly one schema document; progress and diagnostics use
stderr. PLYSTRA_ENV and PLYSTRA_CONFIG supply equivalent selectors when no
explicit selector is present; setting both is an error. Explicit --env or
--config overrides both variables,
and the two flags cannot be combined. Relative configuration paths are resolved
from the detected Plystra Project root. Root plystra.yaml remains mandatory and
is not merged beneath --config. Invalid or conflicting selections emit the
stable PLYSTRA_CONFIGURATION_SELECTION_INVALID diagnostic.
A normalized Project-contained selected document that cannot be loaded reports
one span-less configuration-selection source; conflicting or unsafe selectors
report none.
`
)

var (
	errNewChoiceRequired = errors.New("new project choice is required")
	errNewChoicePrompt   = errors.New("prompt for new project choice")
)

// Run executes one Plystra command and returns its process exit code.
func Run(arguments []string, stdout, stderr io.Writer) int {
	if stdout == nil || stderr == nil {
		return 2
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "determine working directory: %v\n", err)
		return 1
	}
	return runIn(arguments, stdout, stderr, workingDirectory, os.Environ(), terminalPluginSelector(os.Stdin, stderr), terminalNewProjectPrompter(os.Stdin, stderr))
}

// RunIn executes a command in an explicit environment. It exists so command
// integration tests can isolate filesystem and Go Module state. It remains
// non-interactive so automation never consumes an implicit input stream.
func RunIn(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string) int {
	return runIn(arguments, stdout, stderr, workingDirectory, environment, nil, nil)
}

func runIn(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string, selectPlugin plugintarget.Selector, promptNew newProjectPrompter) int {
	output, err := newCommandOutput(commandFormatHuman, stdout, stderr)
	if err != nil {
		return 2
	}
	stdout = output.resultWriter()
	stderr = output.diagnosticWriter()
	if len(arguments) == 0 {
		_, _ = io.WriteString(stdout, usage)
		return 0
	}

	switch arguments[0] {
	case "help", "-h", "--help":
		if len(arguments) != 1 {
			return rejectArguments(stderr, arguments[0])
		}
		_, _ = io.WriteString(stdout, usage)
		return 0
	case "version", "-version", "--version":
		if len(arguments) != 1 {
			return rejectArguments(stderr, arguments[0])
		}
		_, _ = fmt.Fprintf(stdout, "plystra %s\n", version.Current)
		return 0
	case "new":
		if len(arguments) == 2 && (arguments[1] == "help" || arguments[1] == "-h" || arguments[1] == "--help") {
			_, _ = io.WriteString(stdout, newUsage)
			return 0
		}
		options, ok := parseNewArguments(arguments)
		if !ok {
			_, _ = io.WriteString(stderr, newUsage)
			return 2
		}
		choices, err := resolveNewChoices(options, promptNew)
		if err != nil {
			if errors.Is(err, errNewChoiceRequired) {
				writeCommandFailure(stderr, "", err, recoveryContext{})
				return 1
			}
			_, _ = fmt.Fprintf(stderr, "choose new project options: %v\n", err)
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		result, err := newproject.Create(ctx, newproject.Options{
			Parent:      workingDirectory,
			ProjectName: options.projectName,
			ModulePath:  options.modulePath,
			Template:    options.template,
			Plugin:      options.plugin,
			Git:         choices.git,
			GitHubCI:    choices.githubCI,
			Skills:      choices.skills,
			Environment: environment,
		})
		if err != nil {
			writeCommandFailure(stderr, "create project", err, recoveryContext{})
			return 1
		}
		if options.template != "" {
			_, _ = fmt.Fprintf(
				stdout,
				"Created %s from %s\nConfiguration scaffolded\nGenerated, checked, built, and locally verified\n\nNext:\n  cd %s\n  plystra check\n",
				options.projectName,
				options.template,
				options.projectName,
			)
		} else {
			_, _ = fmt.Fprintf(stdout, "created %s in %s\n", result.ModulePath(), result.Path())
		}
		return 0
	case "add":
		if len(arguments) == 2 && isHelp(arguments[1]) {
			_, _ = io.WriteString(stdout, addUsage)
			return 0
		}
		if len(arguments) != 2 || strings.TrimSpace(arguments[1]) == "" || strings.HasPrefix(arguments[1], "--") {
			_, _ = io.WriteString(stderr, addUsage)
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
		defer cancel()
		result, err := dependencyadd.Add(ctx, dependencyadd.Options{
			Start:       workingDirectory,
			Query:       arguments[1],
			Environment: environment,
		})
		if err != nil {
			writeCommandFailure(stderr, "", err, commandRecoveryContext("", "", environment))
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "added dependency %s to %s in %s\n", result.Query(), result.Module().ModulePath(), result.Module().Path())
		return 0
	case "remove":
		if len(arguments) == 2 && isHelp(arguments[1]) {
			_, _ = io.WriteString(stdout, removeUsage)
			return 0
		}
		if len(arguments) != 2 || strings.TrimSpace(arguments[1]) == "" || strings.HasPrefix(arguments[1], "--") {
			_, _ = io.WriteString(stderr, removeUsage)
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
		defer cancel()
		result, err := dependencyremove.Remove(ctx, dependencyremove.Options{
			Start:       workingDirectory,
			ModulePath:  arguments[1],
			Environment: environment,
		})
		if err != nil {
			writeCommandFailure(stderr, "", err, commandRecoveryContext("", "", environment))
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "removed dependency %s from %s in %s\n", result.ModulePath(), result.Module().ModulePath(), result.Module().Path())
		return 0
	case "update":
		if len(arguments) == 2 && isHelp(arguments[1]) {
			_, _ = io.WriteString(stdout, updateUsage)
			return 0
		}
		if len(arguments) != 2 || strings.TrimSpace(arguments[1]) == "" || strings.HasPrefix(arguments[1], "--") {
			_, _ = io.WriteString(stderr, updateUsage)
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
		defer cancel()
		result, err := dependencyupdate.Update(ctx, dependencyupdate.Options{
			Start:       workingDirectory,
			Query:       arguments[1],
			Environment: environment,
		})
		if err != nil {
			writeCommandFailure(stderr, "", err, commandRecoveryContext("", "", environment))
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "updated dependency %s in %s at %s\n", result.Query(), result.Module().ModulePath(), result.Module().Path())
		return 0
	case "use":
		return runUse(arguments, stdout, stderr, workingDirectory, environment)
	case "plugin":
		if len(arguments) == 2 && isHelp(arguments[1]) {
			_, _ = io.WriteString(stdout, pluginUsage)
			return 0
		}
		if len(arguments) == 3 && arguments[1] == "create" && isHelp(arguments[2]) {
			_, _ = io.WriteString(stdout, pluginCreateUsage)
			return 0
		}
		if len(arguments) < 2 || arguments[1] != "create" {
			_, _ = io.WriteString(stderr, pluginUsage)
			return 2
		}
		if len(arguments) != 3 || strings.TrimSpace(arguments[2]) == "" {
			_, _ = io.WriteString(stderr, pluginCreateUsage)
			return 2
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		result, err := plugincreate.Create(ctx, plugincreate.Options{
			Start:       workingDirectory,
			Name:        arguments[2],
			Environment: environment,
		})
		if err != nil {
			writeCommandFailure(stderr, "create plugin", err, commandRecoveryContext("", "", environment))
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "created plugin %s in %s\n", result.ID(), result.Path())
		return 0
	case "interface":
		return runInterface(arguments, stdout, stderr, workingDirectory, environment)
	case "implement":
		return runImplement(arguments, stdout, stderr, workingDirectory, environment)
	case "capability":
		return runCapability(arguments, stdout, stderr, workingDirectory, environment, selectPlugin)
	case "inspect":
		if len(arguments) == 2 && isHelp(arguments[1]) {
			_, _ = io.WriteString(stdout, inspectUsage)
			return 0
		}
		return runInspect(arguments, stdout, stderr, workingDirectory, environment)
	case "explain":
		if len(arguments) == 2 && isHelp(arguments[1]) || len(arguments) == 3 && (arguments[1] == "capability" || arguments[1] == "plugin" || arguments[1] == "config" || arguments[1] == "alias" || arguments[1] == "exposure") && isHelp(arguments[2]) {
			_, _ = io.WriteString(stdout, explainUsage)
			return 0
		}
		return runExplain(arguments, stdout, stderr, workingDirectory, environment)
	case "check":
		if len(arguments) == 2 && isHelp(arguments[1]) {
			_, _ = io.WriteString(stdout, checkUsage)
			return 0
		}
		check, ok := parseCheckArguments(arguments)
		if !ok {
			_, _ = io.WriteString(stderr, checkUsage)
			return 2
		}
		if rejectConflictingConfigurationSelectors(stderr, check.configurationPath, check.environmentName) {
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
		defer cancel()
		result, err := projectcheck.Check(ctx, projectcheck.Options{
			Start:             workingDirectory,
			ConfigurationPath: check.configurationPath,
			EnvironmentName:   check.environmentName,
			Environment:       environment,
		})
		if err != nil {
			writeCommandFailure(stderr, "", err, commandRecoveryContext(check.configurationPath, check.environmentName, environment))
			return 1
		}
		if !result.Clean() {
			heading := "generated output is not current"
			if result.ConfigurationChanged() {
				heading = "Project configuration or generated output is not current"
			}
			writeGenerationReport(stderr, heading, result.Module().ModulePath(), result.ConfigurationChanged(), result.ConfigurationMaintenancePath(), result.Report(), commandRecoveryContext(check.configurationPath, check.environmentName, environment))
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "Project checks passed for %s in %s\n", result.Module().ModulePath(), result.Module().Path())
		return 0
	case "generate":
		if len(arguments) == 2 && (arguments[1] == "help" || arguments[1] == "-h" || arguments[1] == "--help") {
			_, _ = io.WriteString(stdout, generateUsage)
			return 0
		}
		generate, ok := parseGenerateArguments(arguments)
		if !ok {
			_, _ = io.WriteString(stderr, generateUsage)
			return 2
		}
		if rejectConflictingConfigurationSelectors(stderr, generate.configurationPath, generate.environmentName) {
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
		defer cancel()
		options := applicationgenerate.Options{
			Start:             workingDirectory,
			Check:             generate.check,
			ConfigurationPath: generate.configurationPath,
			EnvironmentName:   generate.environmentName,
			Environment:       environment,
		}
		var result applicationgenerate.Result
		var err error
		if generate.check {
			result, err = applicationgenerate.Generate(ctx, options)
		} else {
			project, locateErr := projectlocate.Find(workingDirectory)
			if locateErr != nil {
				err = fmt.Errorf("locate Project: %w", locateErr)
			} else {
				generateWithMutation := func(mutate applicationgenerate.ModuleMutation) error {
					options.MutateModule = mutate
					var generateErr error
					result, generateErr = applicationgenerate.Generate(ctx, options)
					return generateErr
				}
				err = modulemutation.Tidy(ctx, project.Path(), options.GoCommand, environment, generateWithMutation)
				var dependencySource *applicationgenerate.DependencySourceError
				if errors.Is(err, applicationgenerate.ErrKernelDependency) && errors.As(err, &dependencySource) {
					err = modulemutation.Change(ctx, project.Path(), modulemutation.ChangeOptions{
						GoCommand:          options.GoCommand,
						Environment:        environment,
						Arguments:          []string{"get", kernelintrinsic.ModulePath + "@" + newproject.KernelVersion},
						DirectRequirements: []string{kernelintrinsic.ModulePath},
					}, generateWithMutation)
				}
			}
		}
		if err != nil {
			writeCommandFailure(stderr, "", err, commandRecoveryContext(generate.configurationPath, generate.environmentName, environment))
			return 1
		}
		configurationDrift := result.Checked() && result.ConfigurationChanged()
		if configurationDrift || !result.Report().Clean() {
			heading := "generated output remains inconsistent after installation"
			if result.Checked() {
				heading = "generated output is not current"
				if configurationDrift {
					heading = "Project configuration or generated output is not current"
				}
			}
			writeGenerationReport(stderr, heading, result.Module().ModulePath(), configurationDrift, result.ConfigurationMaintenancePath(), result.Report(), commandRecoveryContext(generate.configurationPath, generate.environmentName, environment))
			return 1
		}
		if result.Checked() {
			_, _ = fmt.Fprintf(stdout, "generated output is current for %s in %s\n", result.Module().ModulePath(), result.Module().Path())
		} else {
			_, _ = fmt.Fprintf(stdout, "generated %s in %s\n", result.Module().ModulePath(), result.Module().Path())
		}
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n%s", arguments[0], usage)
		return 2
	}
}

func terminalPluginSelector(input *os.File, output io.Writer) plugintarget.Selector {
	outputFile, ok := output.(*os.File)
	if !ok || !terminalFile(input) || !terminalFile(outputFile) {
		return nil
	}
	return plugintarget.Prompt(input, output)
}

func terminalNewProjectPrompter(input *os.File, output io.Writer) newProjectPrompter {
	outputFile, ok := output.(*os.File)
	if !ok || !terminalFile(input) || !terminalFile(outputFile) {
		return nil
	}
	return promptNewProject(input, output)
}

func terminalFile(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

type generateArguments struct {
	check             bool
	configurationPath string
	environmentName   string
}

type checkArguments struct {
	configurationPath string
	environmentName   string
}

func parseCheckArguments(arguments []string) (checkArguments, bool) {
	if len(arguments) == 0 || arguments[0] != "check" {
		return checkArguments{}, false
	}
	var result checkArguments
	configurationSet := false
	environmentSet := false
	for index := 1; index < len(arguments); index++ {
		switch arguments[index] {
		case "--config":
			if configurationSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return checkArguments{}, false
			}
			configurationSet = true
			index++
			result.configurationPath = arguments[index]
		case "--env":
			if environmentSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return checkArguments{}, false
			}
			environmentSet = true
			index++
			result.environmentName = arguments[index]
		default:
			return checkArguments{}, false
		}
	}
	return result, true
}

func parseGenerateArguments(arguments []string) (generateArguments, bool) {
	if len(arguments) == 0 || arguments[0] != "generate" {
		return generateArguments{}, false
	}
	var result generateArguments
	configurationSet := false
	environmentSet := false
	for index := 1; index < len(arguments); index++ {
		switch arguments[index] {
		case "--check":
			if result.check {
				return generateArguments{}, false
			}
			result.check = true
		case "--config":
			if configurationSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return generateArguments{}, false
			}
			configurationSet = true
			index++
			result.configurationPath = arguments[index]
		case "--env":
			if environmentSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return generateArguments{}, false
			}
			environmentSet = true
			index++
			result.environmentName = arguments[index]
		default:
			return generateArguments{}, false
		}
	}
	return result, true
}

func writeGenerationReport(writer io.Writer, heading, modulePath string, configurationDrift bool, configurationPath string, report generatedfiles.Report, context recoveryContext) {
	_, _ = fmt.Fprintf(writer, "%s:\n", heading)
	if configurationDrift {
		_, _ = fmt.Fprintf(writer, "  changed %s (dependency composition)\n", configurationPath)
	}
	for _, change := range report.Changes() {
		_, _ = fmt.Fprintf(writer, "  %s %s\n", change.Kind(), change.Path())
	}
	action := "Run `plystra generate" + context.selectorSuffix() + "` to restore the selected generated output."
	code := diagnosticGeneratedDrift
	if configurationDrift {
		code = diagnosticConfigurationCompositionDrift
	}
	if len(report.Unexpected()) > 0 {
		action = "Move every unexpected unowned path outside generated/, then run `plystra generate" + context.selectorSuffix() + "`."
		code = diagnosticGeneratedUnexpectedOutput
	}
	var sourceInputs []diagnosticjson.Source
	switch code {
	case diagnosticConfigurationCompositionDrift:
		sourceInputs = []diagnosticjson.Source{{
			Module: modulePath,
			Path:   configurationPath,
			Kind:   "configuration-declaration",
			Line:   1,
			Column: 1,
		}}
	case diagnosticGeneratedDrift:
		for _, change := range report.Changes() {
			sourceInputs = append(sourceInputs, diagnosticjson.Source{
				Module: modulePath,
				Path:   change.Path(),
				Kind:   "generated-artifact",
			})
		}
	case diagnosticGeneratedUnexpectedOutput:
		for _, unexpectedPath := range report.Unexpected() {
			sourceInputs = append(sourceInputs, diagnosticjson.Source{
				Module: modulePath,
				Path:   unexpectedPath,
				Kind:   "generated-artifact",
			})
		}
	}
	sources, err := diagnosticjson.CanonicalizeSources(sourceInputs)
	if err == nil {
		for index, source := range sources {
			if index == 0 {
				_, _ = fmt.Fprintln(writer)
			}
			_, _ = fmt.Fprintf(writer, "Source: %s\n", explainSourceSummary(source))
		}
	}
	_, _ = fmt.Fprintf(writer, "\nRecovery:\n%s\n\nDiagnostic: %s\n", action, code)
}

type newArguments struct {
	projectName string
	modulePath  string
	template    string
	plugin      string
	git         booleanChoice
	githubCI    booleanChoice
	skills      booleanChoice
}

type booleanChoice uint8

const (
	choiceUnspecified booleanChoice = iota
	choiceYes
	choiceNo
)

type resolvedNewChoices struct {
	git      bool
	githubCI bool
	skills   bool
}

type newProjectPrompter func(question string, defaultValue bool) (bool, error)

func parseNewArguments(arguments []string) (newArguments, bool) {
	if len(arguments) < 2 || arguments[1] == "" || strings.HasPrefix(arguments[1], "--") {
		return newArguments{}, false
	}
	result := newArguments{projectName: arguments[1]}
	moduleSet := false
	templateSet := false
	pluginSet := false
	for index := 2; index < len(arguments); index++ {
		switch arguments[index] {
		case "--module":
			if moduleSet || index+1 >= len(arguments) || arguments[index+1] == "" || strings.HasPrefix(arguments[index+1], "--") {
				return newArguments{}, false
			}
			moduleSet = true
			index++
			result.modulePath = arguments[index]
		case "--template":
			if templateSet || index+1 >= len(arguments) || arguments[index+1] == "" || strings.HasPrefix(arguments[index+1], "--") {
				return newArguments{}, false
			}
			templateSet = true
			index++
			result.template = arguments[index]
		case "--plugin":
			if pluginSet || index+1 >= len(arguments) || arguments[index+1] == "" || strings.HasPrefix(arguments[index+1], "--") {
				return newArguments{}, false
			}
			pluginSet = true
			index++
			result.plugin = arguments[index]
		case "--git":
			if result.git != choiceUnspecified {
				return newArguments{}, false
			}
			result.git = choiceYes
		case "--no-git":
			if result.git != choiceUnspecified {
				return newArguments{}, false
			}
			result.git = choiceNo
		case "--github-ci":
			if result.githubCI != choiceUnspecified {
				return newArguments{}, false
			}
			result.githubCI = choiceYes
		case "--no-github-ci":
			if result.githubCI != choiceUnspecified {
				return newArguments{}, false
			}
			result.githubCI = choiceNo
		case "--skills":
			if result.skills != choiceUnspecified {
				return newArguments{}, false
			}
			result.skills = choiceYes
		case "--no-skills":
			if result.skills != choiceUnspecified {
				return newArguments{}, false
			}
			result.skills = choiceNo
		default:
			return newArguments{}, false
		}
	}
	return result, true
}

func resolveNewChoices(arguments newArguments, prompt newProjectPrompter) (resolvedNewChoices, error) {
	choices := []struct {
		value    booleanChoice
		question string
		flags    string
		set      func(bool)
	}{
		{value: arguments.git, question: "Initialize a Git repository?", flags: "--git or --no-git"},
		{value: arguments.githubCI, question: "Include GitHub Actions CI?", flags: "--github-ci or --no-github-ci"},
		{value: arguments.skills, question: "Include Plystra development skills?", flags: "--skills or --no-skills"},
	}
	var result resolvedNewChoices
	choices[0].set = func(value bool) { result.git = value }
	choices[1].set = func(value bool) { result.githubCI = value }
	choices[2].set = func(value bool) { result.skills = value }
	missing := make([]string, 0, len(choices))
	for _, choice := range choices {
		switch choice.value {
		case choiceYes:
			choice.set(true)
		case choiceNo:
			choice.set(false)
		case choiceUnspecified:
			if prompt == nil {
				missing = append(missing, choice.flags)
				continue
			}
			value, err := prompt(choice.question, true)
			if err != nil {
				return resolvedNewChoices{}, fmt.Errorf("%w: %s: %v", errNewChoicePrompt, choice.question, err)
			}
			choice.set(value)
		default:
			return resolvedNewChoices{}, fmt.Errorf("%w: invalid parsed choice", errNewChoicePrompt)
		}
	}
	if len(missing) != 0 {
		return resolvedNewChoices{}, fmt.Errorf("%w in non-interactive mode; specify %s", errNewChoiceRequired, strings.Join(missing, ", "))
	}
	return result, nil
}

func promptNewProject(input io.Reader, output io.Writer) newProjectPrompter {
	scanner := bufio.NewScanner(input)
	return func(question string, defaultValue bool) (bool, error) {
		if input == nil || output == nil {
			return false, errors.New("input and output are required")
		}
		suffix := " [y/N]: "
		if defaultValue {
			suffix = " [Y/n]: "
		}
		for {
			if _, err := io.WriteString(output, question+suffix); err != nil {
				return false, fmt.Errorf("write prompt: %v", err)
			}
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return false, fmt.Errorf("read choice: %v", err)
				}
				return false, errors.New("input ended before a choice")
			}
			switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
			case "":
				return defaultValue, nil
			case "y", "yes":
				return true, nil
			case "n", "no":
				return false, nil
			default:
				if _, err := io.WriteString(output, "Please enter yes or no.\n"); err != nil {
					return false, fmt.Errorf("write retry guidance: %v", err)
				}
			}
		}
	}
}

func rejectArguments(stderr io.Writer, command string) int {
	_, _ = fmt.Fprintf(stderr, "%s does not accept arguments\n", command)
	return 2
}
