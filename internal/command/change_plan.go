package command

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/implementationselect"
)

const changePlanUsage = `Usage:
  plystra change plan --request <path|-> [--format human|json] [--env <environment>|--config <yaml-path>]

Plans one bounded declarative change request without modifying the Project. This
installed slice accepts batches of interface_root present/absent (require or
expose), implementation_selection set/remove, resource_selection set/remove,
and configuration set/remove operations (at most 1024 requested operations).
It analyzes the final ownership graph.
Root changes preserve complete-set boundaries
or sparse environment intent and clean up only owners lost in the final state.
Resource removal deletes the whole exact instance, including provider and Config,
and only that consumer's outgoing bindings. Surviving explicit bindings are never
erased or retargeted; implicit dependencies resolve against the final instance set.
Single-root and single-selection previews report missing newly required,
statically addressable constructor or Resource provider Config fields,
including non-pointer fixed-object children, as unresolved decisions.
Required Implementation fields compare complete Interface selection closures;
old Resource errors do not hide them. Dormant ownership is not reachability.
An incomplete old Interface closure does not prove newly reachable fields.
Previous-state alternatives require complete validation of that previous state.
Ambiguous implicit bindings introduced on one Resource instance are previewed as
complete explicit assignments only when they pass bounded final-state validation,
possibly with statically addressable missing Config fields. The preview fails
closed without a partial plan when more than 64 candidate bindings are probed.
Final-state binding choices do not require a valid old provider state; invalid
previous states are never offered as provider-retention alternatives.
Configuration requests name an exact owned Implementation constructor or effective
Resource instance and an owner-relative declared field path. Paths traverse only
non-pointer fixed objects; atomic values must be supplied whole. Complete documents
delete removed fields; environments record exact field exclusions. All final Config
and binding obligations must validate, including already-satisfied removals.
Configuration-only batches edit disjoint paths and validate only the complete final
state. Same-owner ancestor/descendant targets conflict regardless of request order.
Every multi-operation selection batch requires a valid complete final state;
it does not enumerate missing-value decisions. Edits use the final owners' schemas,
and obsolete Config and binding cleanup happens in the same document edit.
Resource set requires an existing effective instance. A provider change discards
old selected Config, including its exclusion; matching fields are never migrated.
Other operation kinds remain unsupported. Public plans allow at most 1024
derived operations separately; cleanup does not consume the request limit.
Public request identity omits submitted values. Every configuration set declares a
planned write, even for equal values, so private equality is not disclosed.
This preview is non_exact and cannot authorize apply. Change apply remains uninstalled.
Other operation kinds return a structured unsupported result until their staged
planners are installed.
`

type changePlanArguments struct {
	request           string
	format            commandFormat
	configurationPath string
	environmentName   string
}

func runChange(arguments []string, stdout, stderr io.Writer, workingDirectory string, environment []string) int {
	if len(arguments) == 2 && isHelp(arguments[1]) || len(arguments) == 3 && arguments[1] == "plan" && isHelp(arguments[2]) {
		_, _ = io.WriteString(stdout, changePlanUsage)
		return 0
	}
	parsed, ok := parseChangePlanArguments(arguments)
	if !ok {
		_, _ = io.WriteString(stderr, changePlanUsage)
		return 2
	}
	if rejectConflictingConfigurationSelectors(stderr, parsed.configurationPath, parsed.environmentName) {
		return 1
	}
	output, err := newCommandOutput(parsed.format, stdout, stderr)
	if err != nil {
		return 2
	}
	invocationID, err := generateInvocationID()
	if err != nil {
		return 8
	}
	emptyEffects, err := commandschema.NewEffects(commandschema.EffectsInput{})
	if err != nil {
		return 8
	}
	fail := func(status commandschema.Status, code, message string) int {
		result, buildErr := newChangePlanFailure(invocationID, emptyEffects, status, code, message)
		if buildErr != nil {
			return 8
		}
		return writeChangePlanResult(output, result, message)
	}
	requestBytes, err := readChangeRequest(workingDirectory, parsed.request)
	if err != nil {
		return fail(commandschema.StatusValidationFailed, diagnosticcode.ChangePlanRequestInvalid, "The change request could not be read.")
	}
	request, err := changerequest.Parse(requestBytes)
	if err != nil {
		return fail(commandschema.StatusValidationFailed, diagnosticcode.ChangePlanRequestInvalid, "The change request is invalid.")
	}
	operations := request.Operations()
	configurations, implementationSelections, resourceSelections, roots := 0, 0, 0, 0
	for _, operation := range operations {
		switch operation.Kind() {
		case changerequest.KindConfiguration:
			configurations++
		case changerequest.KindImplementationSelection:
			implementationSelections++
		case changerequest.KindResourceSelection:
			resourceSelections++
		case changerequest.KindInterfaceRoot:
			roots++
		}
	}
	selectionBatch := len(operations) > 1 && roots+implementationSelections+resourceSelections > 0 && roots+implementationSelections+resourceSelections+configurations == len(operations)
	if len(operations) == 0 || len(operations) != 1 && configurations != len(operations) && !selectionBatch {
		return fail(commandschema.StatusUnsupported, diagnosticcode.ChangePlanOperationUnsupported, "This installed change-plan slice supports Interface root, Implementation/Resource selection, and configuration batches.")
	}
	var target, constructor string
	requiresConstructor := true
	planner := implementationselect.Preview
	if selectionBatch {
		requiresConstructor = false
		planner = func(ctx context.Context, options implementationselect.Options) (implementationselect.PlanResult, error) {
			return implementationselect.PreviewSelectionConfigurations(ctx, options, request)
		}
	} else {
		switch operations[0].Kind() {
		case changerequest.KindImplementationSelection:
			selection, _ := operations[0].ImplementationSelection()
			if selection.Action() == changerequest.SelectionRemove {
				requiresConstructor = false
				planner = implementationselect.PreviewRemoval
			} else if selection.Action() != changerequest.SelectionSet {
				return fail(commandschema.StatusUnsupported, diagnosticcode.ChangePlanOperationUnsupported, "This installed change-plan slice does not support that selection action.")
			}
			target = selection.Interface()
			constructor, _ = selection.Constructor()
		case changerequest.KindResourceSelection:
			selection, _ := operations[0].ResourceSelection()
			if selection.Action() == changerequest.SelectionRemove {
				requiresConstructor = false
				planner = implementationselect.PreviewResourceRemoval
			} else if selection.Action() != changerequest.SelectionSet {
				return fail(commandschema.StatusUnsupported, diagnosticcode.ChangePlanOperationUnsupported, "This installed change-plan slice does not support that selection action.")
			}
			target = selection.Instance()
			constructor, _ = selection.Provider()
		case changerequest.KindInterfaceRoot:
			root, _ := operations[0].InterfaceRoot()
			target = root.Interface()
			requiresConstructor = false
			planner = func(ctx context.Context, options implementationselect.Options) (implementationselect.PlanResult, error) {
				return implementationselect.PreviewInterfaceRoot(ctx, options, string(root.Root()), root.Action() == changerequest.RootPresent)
			}
		case changerequest.KindConfiguration:
			requiresConstructor = false
			planner = func(ctx context.Context, options implementationselect.Options) (implementationselect.PlanResult, error) {
				return implementationselect.PreviewConfigurations(ctx, options, request)
			}
		default:
			return fail(commandschema.StatusUnsupported, diagnosticcode.ChangePlanOperationUnsupported, "This installed change-plan slice supports Interface root, Implementation/Resource selection, and configuration batches.")
		}
	}
	if constructor == "" && requiresConstructor {
		return fail(commandschema.StatusValidationFailed, diagnosticcode.ChangePlanRequestInvalid, "The selection does not name a constructor.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
	defer cancel()
	planned, err := planner(ctx, implementationselect.Options{
		Start: workingDirectory, Target: target, Constructor: constructor,
		ConfigurationPath: parsed.configurationPath, EnvironmentName: parsed.environmentName,
		Environment: environment,
	})
	if err != nil {
		return fail(commandschema.StatusValidationFailed, diagnosticcode.ChangePlanFailed, "The change could not be planned against the selected Project.")
	}
	payload, effects, err := buildChangePlanPayload(request, planned)
	if err != nil {
		return fail(commandschema.StatusExecutionFailed, diagnosticcode.ChangePlanFailed, "The change plan could not be encoded.")
	}
	snapshot, err := changePlanSelectorSnapshot(planned.Inputs().ConfigurationSelection())
	if err != nil {
		return fail(commandschema.StatusExecutionFailed, diagnosticcode.ChangePlanFailed, "The selected configuration snapshot could not be encoded.")
	}
	result, err := commandschema.NewResult(commandschema.ResultInput{
		Operation: "change.plan", InvocationID: invocationID, Snapshot: &snapshot,
		Status: commandschema.StatusSuccess, Effects: effects, Payload: payload,
	})
	if err != nil {
		return fail(commandschema.StatusExecutionFailed, diagnosticcode.ChangePlanFailed, "The change plan result could not be built.")
	}
	return writeChangePlanResult(output, result, fmt.Sprintf("change plan %s (%s)", payload.PlanDigest(), payload.Completeness()))
}

func parseChangePlanArguments(arguments []string) (changePlanArguments, bool) {
	if len(arguments) < 3 || arguments[0] != "change" || arguments[1] != "plan" {
		return changePlanArguments{}, false
	}
	result := changePlanArguments{format: commandFormatHuman}
	requestSet, formatSet, configurationSet, environmentSet := false, false, false, false
	for index := 2; index < len(arguments); index++ {
		switch arguments[index] {
		case "--request":
			if requestSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return changePlanArguments{}, false
			}
			requestSet = true
			index++
			result.request = arguments[index]
		case "--format":
			if formatSet || index+1 >= len(arguments) || strings.HasPrefix(arguments[index+1], "--") {
				return changePlanArguments{}, false
			}
			formatSet = true
			index++
			if arguments[index] != string(commandFormatHuman) && arguments[index] != string(commandFormatJSON) {
				return changePlanArguments{}, false
			}
			result.format = commandFormat(arguments[index])
		case "--config":
			if configurationSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return changePlanArguments{}, false
			}
			configurationSet = true
			index++
			result.configurationPath = arguments[index]
		case "--env":
			if environmentSet || index+1 >= len(arguments) || strings.TrimSpace(arguments[index+1]) == "" || strings.HasPrefix(arguments[index+1], "--") {
				return changePlanArguments{}, false
			}
			environmentSet = true
			index++
			result.environmentName = arguments[index]
		default:
			return changePlanArguments{}, false
		}
	}
	return result, requestSet
}

func changePlanFormatIntent(arguments []string) commandFormat {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == "--format" && arguments[index+1] == string(commandFormatJSON) {
			return commandFormatJSON
		}
	}
	return commandFormatHuman
}

func readChangeRequest(base, path string) ([]byte, error) {
	if path == "-" {
		limited := io.LimitReader(os.Stdin, changerequest.MaximumRequestBytes+1)
		data, err := io.ReadAll(limited)
		if err != nil {
			return nil, err
		}
		if len(data) > changerequest.MaximumRequestBytes {
			return nil, errors.New("change request exceeds the maximum size")
		}
		return data, nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, changerequest.MaximumRequestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > changerequest.MaximumRequestBytes {
		return nil, errors.New("change request exceeds the maximum size")
	}
	return data, nil
}

func buildChangePlanPayload(request changerequest.Request, planned implementationselect.PlanResult) (commandschema.ChangePlan, commandschema.Effects, error) {
	inputs := planned.Inputs()
	selection := inputs.ConfigurationSelection()
	verification := []string{"plystra", "check"}
	switch generation.ConfigurationMode(selection.Mode()) {
	case generation.ConfigurationModeEnvironment:
		verification = append(verification, "--env", selection.Environment())
	case generation.ConfigurationModeExplicit:
		path := selection.Path()
		if strings.HasPrefix(path, "--") {
			path = "./" + path
		}
		verification = append(verification, "--config", path)
	}
	rootDigest, selectedDigest, afterDigest, err := inputs.ConfigurationLayerDigests(planned.Updated())
	if err != nil {
		return commandschema.ChangePlan{}, commandschema.Effects{}, err
	}
	readSet := []commandschema.ChangePlanPathInput{{Path: inputs.RootSnapshot().Path(), Kind: "configuration", BeforeDigest: rootDigest}}
	if inputs.SelectedSnapshot().Path() != inputs.RootSnapshot().Path() {
		readSet = append(readSet, commandschema.ChangePlanPathInput{Path: inputs.SelectedSnapshot().Path(), Kind: "configuration", BeforeDigest: selectedDigest})
	}
	var operations []commandschema.ChangePlanOperationInput
	configurationRequested := false
	for _, requested := range request.Operations() {
		operations = append(operations, requestedSelectionOperation(requested))
		configurationRequested = configurationRequested || requested.Kind() == changerequest.KindConfiguration
	}
	requestedCount := len(operations)
	for _, derived := range planned.ConfigurationImpact().DerivedOperations() {
		operations = append(operations, commandschema.ChangePlanOperationInput{Kind: derived.Kind, Action: derived.Action, Owner: derived.Owner, Path: derived.Path, Derived: true})
	}
	var writeSet []commandschema.ChangePlanPathInput
	var plannedEffects []commandschema.Effect
	if planned.Changed() {
		writeSet = append(writeSet, commandschema.ChangePlanPathInput{Path: selection.Path(), Kind: "configuration", BeforeDigest: selectedDigest, AfterDigest: afterDigest})
		effect, err := commandschema.NewEffect(commandschema.EffectInput{
			ID: "apply-change-plan", Class: commandschema.EffectProjectWrite, Phase: "apply", Target: selection.Path(),
			Owner: commandschema.Owner{Module: inputs.Module().ModulePath(), Path: "."}, Reason: "planned_configuration_change", Reversibility: "rollback", Verification: verification,
		})
		if err != nil {
			return commandschema.ChangePlan{}, commandschema.Effects{}, err
		}
		plannedEffects = append(plannedEffects, effect)
	}
	effects, err := commandschema.NewEffects(commandschema.EffectsInput{Planned: plannedEffects})
	if err != nil {
		return commandschema.ChangePlan{}, commandschema.Effects{}, err
	}
	requestDigest := request.Digest()
	if configurationRequested {
		// Non-exact previews bind a public request projection. The private
		// canonical request digest would reveal submitted value equality.
		identity, err := json.Marshal(operations[:requestedCount])
		if err != nil {
			return commandschema.ChangePlan{}, commandschema.Effects{}, err
		}
		requestDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(append([]byte("plystra.change-plan/v1:public-request\x00"), identity...)))
	}
	payload, err := commandschema.NewChangePlan(commandschema.ChangePlanInput{
		RequestDigest:       requestDigest,
		InputSnapshot:       commandschema.ChangePlanSnapshotInput{ModulePath: inputs.Module().ModulePath(), ConfigurationMode: selection.Mode(), ConfigurationPath: selection.Path(), Environment: selection.Environment(), RootDigest: rootDigest, SelectedDigest: selectedDigest},
		ReadSet:             readSet,
		WriteSet:            writeSet,
		Operations:          operations,
		ConfigurationScope:  commandschema.ChangePlanConfigurationScopeInput{Mode: selection.Mode(), Path: selection.Path(), Environment: selection.Environment()},
		ConfigurationImpact: planned.ConfigurationImpact(), EffectRequirements: plannedEffects, Effects: effects, FinalModelDigest: planned.CompositionDigest(),
		Verification: verification, Completeness: "non_exact",
	})
	return payload, effects, err
}

func requestedSelectionOperation(operation changerequest.Operation) commandschema.ChangePlanOperationInput {
	result := commandschema.ChangePlanOperationInput{Kind: string(operation.Kind())}
	if selection, ok := operation.ImplementationSelection(); ok {
		result.Action = string(selection.Action())
		result.Interface = selection.Interface()
		result.Constructor, _ = selection.Constructor()
	} else if selection, ok := operation.ResourceSelection(); ok {
		result.Action = string(selection.Action())
		result.Instance = selection.Instance()
		result.Provider, _ = selection.Provider()
	} else if root, ok := operation.InterfaceRoot(); ok {
		result.Action = string(root.Action())
		result.Interface = root.Interface()
		result.Root = string(root.Root())
	} else if configuration, ok := operation.Configuration(); ok {
		result.Action = string(configuration.Action())
		result.Owner = configuration.Owner()
		result.Path = configuration.Path()
	}
	return result
}

func changePlanSelectorSnapshot(selection applicationresolve.ConfigurationSelection) (commandschema.SelectorSnapshot, error) {
	mode := generation.ConfigurationMode(selection.Mode())
	input := commandschema.SelectorSnapshotInput{Mode: mode}
	switch mode {
	case generation.ConfigurationModeEnvironment:
		input.Name = selection.Environment()
	case generation.ConfigurationModeExplicit:
		input.Path = selection.Path()
	}
	return commandschema.NewSelectorSnapshot(input)
}

func newChangePlanFailure(invocationID string, effects commandschema.Effects, status commandschema.Status, code, message string) (commandschema.Result, error) {
	diagnostic, err := commandschema.NewDiagnostic(commandschema.DiagnosticInput{Code: code, Severity: diagnosticjson.SeverityError, Message: message})
	if err != nil {
		return commandschema.Result{}, err
	}
	return commandschema.NewResult(commandschema.ResultInput{Operation: "change.plan", InvocationID: invocationID, Status: status, Diagnostics: []commandschema.Diagnostic{diagnostic}, Effects: effects})
}

func writeChangePlanResult(output commandOutput, result commandschema.Result, human string) int {
	if output.format == commandFormatJSON {
		_, err := output.resultWriter().Write(append(result.CanonicalJSON(), '\n'))
		if err != nil {
			return 8
		}
		return result.ExitClass()
	}
	if result.ExitClass() != 0 {
		_, _ = fmt.Fprintln(output.diagnosticWriter(), human)
		return result.ExitClass()
	}
	_, _ = fmt.Fprintln(output.resultWriter(), human)
	return 0
}
