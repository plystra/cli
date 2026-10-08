package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationgen"
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
initial installed slice accepts one implementation_selection set operation and
validates its final Interface/Resource ownership graph. Other operation kinds
return a structured unsupported result until their staged planners are installed.
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
	if len(operations) != 1 || operations[0].Kind() != changerequest.KindImplementationSelection {
		return fail(commandschema.StatusUnsupported, diagnosticcode.ChangePlanOperationUnsupported, "This installed change-plan slice supports one implementation_selection operation.")
	}
	selection, ok := operations[0].ImplementationSelection()
	if !ok || selection.Action() != changerequest.SelectionSet {
		return fail(commandschema.StatusUnsupported, diagnosticcode.ChangePlanOperationUnsupported, "This installed change-plan slice supports implementation_selection set only.")
	}
	constructor, ok := selection.Constructor()
	if !ok {
		return fail(commandschema.StatusValidationFailed, diagnosticcode.ChangePlanRequestInvalid, "The implementation selection does not name a constructor.")
	}

	ctx, cancel := context.WithTimeout(context.Background(), generationCommandTimeout)
	defer cancel()
	planned, err := implementationselect.Plan(ctx, implementationselect.Options{
		Start: workingDirectory, Target: selection.Interface(), Constructor: constructor,
		ConfigurationPath: parsed.configurationPath, EnvironmentName: parsed.environmentName,
		Environment: environment,
	})
	if err != nil {
		return fail(commandschema.StatusValidationFailed, diagnosticcode.ChangePlanFailed, "The implementation selection could not be planned against the selected Project.")
	}
	payload, effects, err := buildChangePlanPayload(request, operations[0], planned)
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

func buildChangePlanPayload(request changerequest.Request, requested changerequest.Operation, planned implementationselect.PlanResult) (commandschema.ChangePlan, commandschema.Effects, error) {
	inputs := planned.Inputs()
	selection := inputs.ConfigurationSelection()
	rootDigest, err := applicationgen.ConfigurationDigest(inputs.RootSnapshot().Data())
	if err != nil {
		return commandschema.ChangePlan{}, commandschema.Effects{}, err
	}
	selectedDigest, err := selectedConfigurationDigest(selection.Mode(), inputs.SelectedSnapshot().Data())
	if err != nil {
		return commandschema.ChangePlan{}, commandschema.Effects{}, err
	}
	afterDigest, err := selectedConfigurationDigest(selection.Mode(), planned.Updated())
	if err != nil {
		return commandschema.ChangePlan{}, commandschema.Effects{}, err
	}
	readSet := []commandschema.ChangePlanPathInput{{Path: inputs.RootSnapshot().Path(), Kind: "configuration", BeforeDigest: rootDigest}}
	if inputs.SelectedSnapshot().Path() != inputs.RootSnapshot().Path() {
		readSet = append(readSet, commandschema.ChangePlanPathInput{Path: inputs.SelectedSnapshot().Path(), Kind: "configuration", BeforeDigest: selectedDigest})
	}
	operations := []commandschema.ChangePlanOperationInput{{
		Kind: string(requested.Kind()), Action: string(selectionAction(requested)), Interface: requestedInterface(requested), Constructor: requestedConstructor(requested),
	}}
	for _, derived := range planned.ConfigurationImpact().DerivedOperations() {
		operations = append(operations, commandschema.ChangePlanOperationInput{Kind: derived.Kind, Action: derived.Action, Owner: derived.Owner, Path: derived.Path, Derived: true})
	}
	effect, err := commandschema.NewEffect(commandschema.EffectInput{
		ID: "apply-change-plan", Class: commandschema.EffectProjectWrite, Phase: "apply", Target: selection.Path(),
		Owner: commandschema.Owner{Module: inputs.Module().ModulePath(), Path: "."}, Reason: "planned_configuration_change", Reversibility: "rollback", Verification: []string{"plystra", "check"},
	})
	if err != nil {
		return commandschema.ChangePlan{}, commandschema.Effects{}, err
	}
	effects, err := commandschema.NewEffects(commandschema.EffectsInput{Planned: []commandschema.Effect{effect}})
	if err != nil {
		return commandschema.ChangePlan{}, commandschema.Effects{}, err
	}
	payload, err := commandschema.NewChangePlan(commandschema.ChangePlanInput{
		RequestDigest:       request.Digest(),
		InputSnapshot:       commandschema.ChangePlanSnapshotInput{ModulePath: inputs.Module().ModulePath(), ConfigurationMode: selection.Mode(), ConfigurationPath: selection.Path(), Environment: selection.Environment(), RootDigest: rootDigest, SelectedDigest: selectedDigest},
		ReadSet:             readSet,
		WriteSet:            []commandschema.ChangePlanPathInput{{Path: selection.Path(), Kind: "configuration", BeforeDigest: selectedDigest, AfterDigest: afterDigest}},
		Operations:          operations,
		ConfigurationScope:  commandschema.ChangePlanConfigurationScopeInput{Mode: selection.Mode(), Path: selection.Path(), Environment: selection.Environment()},
		ConfigurationImpact: planned.ConfigurationImpact(), Effects: effects, FinalModelDigest: planned.CompositionDigest(),
		Verification: []string{"plystra", "check"}, Completeness: "non_exact",
	})
	return payload, effects, err
}

func selectedConfigurationDigest(mode string, data []byte) (string, error) {
	if mode == string(generation.ConfigurationModeEnvironment) {
		return applicationgen.EnvironmentOverlayDigest(data)
	}
	return applicationgen.ConfigurationDigest(data)
}

func selectionAction(operation changerequest.Operation) changerequest.SelectionAction {
	selection, _ := operation.ImplementationSelection()
	return selection.Action()
}
func requestedInterface(operation changerequest.Operation) string {
	selection, _ := operation.ImplementationSelection()
	return selection.Interface()
}
func requestedConstructor(operation changerequest.Operation) string {
	selection, _ := operation.ImplementationSelection()
	constructor, _ := selection.Constructor()
	return constructor
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
