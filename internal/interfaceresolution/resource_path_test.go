package interfaceresolution_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/interfaceresolution"
)

func TestResourceBindingErrorsRetainInterfaceActivationPath(t *testing.T) {
	t.Parallel()
	base := resourceResolutionInput(t)
	for _, activation := range []string{"direct", "transitive", "optional"} {
		for _, outcome := range []string{"missing", "ambiguous"} {
			t.Run(activation+"/"+outcome, func(t *testing.T) {
				input := base
				rootID := "app.run/v1"
				if activation != "direct" {
					rootID = "app.entry/v1"
					constructor := "example.com/application/entry.New"
					if activation == "optional" {
						constructor = "example.com/application/entry.Optional"
					}
					input.Choices = []interfaceresolution.Choice{{
						InterfaceID: mustResolutionID(t, rootID), Constructor: mustResolutionSymbol(t, constructor),
						Sources: []interfaceresolution.ChoiceSource{resolutionChoiceSource("entry selection", "example.com/application", "deploy/customer.yaml")},
					}}
				}
				identifier := mustResolutionID(t, rootID)
				input.Requirements = []interfaceresolution.Requirement{
					resolutionRequirement(identifier, "require "+rootID, interfaceresolution.RequirementDeclaration),
					resolutionRequirement(identifier, "expose "+rootID, interfaceresolution.RequirementExposure),
				}
				for index := range input.Requirements {
					input.Requirements[index].Source.Path = "deploy/customer.yaml"
				}
				if activation == "optional" {
					input.Requirements = append(input.Requirements, resolutionRequirement(mustResolutionID(t, "app.middle/v1"), "independent requirement", interfaceresolution.RequirementDeclaration))
				}
				condition := constructorgraph.ErrMissingResourceBinding
				if outcome == "ambiguous" {
					condition = constructorgraph.ErrAmbiguousResourceBinding
					for _, name := range []string{"secondary", "primary"} {
						input.ResourceInstances = append(input.ResourceInstances, constructorgraph.ResourceInstanceInput{
							Name: name, Provider: mustResolutionSymbol(t, "example.com/application/store.New"),
							Sources: []constructorgraph.ResourceSource{{Reference: "select " + name, ModulePath: "example.com/application", Path: "plystra.yaml", Line: 1, Column: 1}},
						})
					}
				}
				_, err := interfaceresolution.Resolve(input)
				var failure *constructorgraph.ResourceBindingError
				if !errors.Is(err, condition) || !errors.As(err, &failure) {
					t.Fatalf("Resource failure = %v", err)
				}
				if failure.Root().InterfaceID() != identifier || failure.Constructor().String() != "example.com/application/service.New" || failure.ParameterName() != "database" || failure.ParameterPosition() != 1 {
					t.Fatalf("activation identity lost: %v", err)
				}
				sources := failure.RequirementSources()
				if len(sources) != 2 || sources[0].Kind != constructorgraph.RequirementExposure || sources[1].Kind != constructorgraph.RequirementDeclaration {
					t.Fatalf("root provenance = %#v", sources)
				}
				for _, source := range sources {
					if source.ModulePath != "example.com/application" || source.Path != "deploy/customer.yaml" || source.Line != 1 || source.Column != 1 {
						t.Fatalf("typed activation source lost: %#v", source)
					}
				}
				steps := failure.Steps()
				wantSteps := 0
				if activation != "direct" {
					wantSteps = 2
				}
				if len(steps) != wantSteps {
					t.Fatalf("ordered activation steps = %#v", steps)
				}
				for index, step := range steps {
					requiring, selected, parameter, edgeID := "example.com/application/entry.New", "example.com/application/middle.New", "next", "app.middle/v1"
					if activation == "optional" {
						requiring = "example.com/application/entry.Optional"
					}
					if index == 1 {
						requiring, selected, parameter, edgeID = "example.com/application/middle.New", "example.com/application/service.New", "worker", "app.run/v1"
					}
					if step.RequiringConstructor().String() != requiring || step.SelectedConstructor().String() != selected || step.InterfaceID().String() != edgeID || step.ParameterName() != parameter || step.ParameterPosition() != 1 || step.Optional() != (activation == "optional" && index == 0) || step.SelectionReason() != constructorgraph.SelectionUnique || len(step.SelectionSources()) != 1 || step.RequiringModulePath() != "example.com/application" || step.RequiringSourcePath() == "" || step.RequiringLine() < 1 || step.RequiringColumn() < 1 {
						t.Fatalf("activation step %d = %#v", index, step)
					}
				}
				message := err.Error()
				if !strings.Contains(message, "reached from "+rootID) || !strings.Contains(message, "expose "+rootID) || strings.Contains(message, "constructor executed") || activation == "optional" && !strings.Contains(message, "optionally uses") {
					t.Fatalf("activation message = %s", message)
				}
				slices.Reverse(input.Requirements)
				slices.Reverse(input.ResourceInstances)
				_, repeated := interfaceresolution.Resolve(input)
				if repeated == nil || repeated.Error() != message {
					t.Fatalf("input permutation changed activation path: %v", repeated)
				}
				sources[0].ModulePath = "changed"
				failure.Root().RequirementSources()[0].Path = "changed"
				input.Requirements[0].Source.Reference = "changed"
				if len(steps) > 0 {
					steps[0].SelectionSources()[0] = "changed"
					steps[0] = constructorgraph.PathStep{}
				}
				var repeatedFailure *constructorgraph.ResourceBindingError
				if !errors.As(repeated, &repeatedFailure) || !reflect.DeepEqual(failure.RequirementSources(), repeatedFailure.RequirementSources()) || !reflect.DeepEqual(failure.Steps(), repeatedFailure.Steps()) || failure.Error() != repeatedFailure.Error() {
					t.Fatal("Resource activation evidence exposed mutable storage")
				}
			})
		}
	}
}

func TestResourceBindingPathDoesNotCreateActivation(t *testing.T) {
	t.Parallel()
	input := resourceResolutionInput(t)
	input.Requirements = []interfaceresolution.Requirement{resolutionRequirement(mustResolutionID(t, "app.entry/v1"), "entry root", interfaceresolution.RequirementDeclaration)}
	input.Choices = []interfaceresolution.Choice{{
		InterfaceID: mustResolutionID(t, "app.entry/v1"), Constructor: mustResolutionSymbol(t, "example.com/application/entry.Optional"),
		Sources: []interfaceresolution.ChoiceSource{resolutionChoiceSource("entry selection", "example.com/application", "plystra.yaml")},
	}}
	result, err := interfaceresolution.Resolve(input)
	if err != nil || len(result.Graph().ConstructionOrder()) != 1 {
		t.Fatalf("absent optional activated a Resource consumer: %v", err)
	}
	input.ResourceBindings = []constructorgraph.ResourceBindingInput{{
		Namespace: constructorgraph.ResourceConsumerImplementation, Consumer: "example.com/application/service.New", Parameter: "database", Target: "missing",
		Sources: []constructorgraph.ResourceSource{{Reference: "explicit binding", ModulePath: "example.com/application", Path: "plystra.yaml", Line: 1, Column: 1}},
	}}
	_, err = interfaceresolution.Resolve(input)
	var failure *constructorgraph.ResourceBindingError
	if !errors.Is(err, constructorgraph.ErrInvalidResourceBinding) || !errors.As(err, &failure) || len(failure.RequirementSources()) != 0 || len(failure.Steps()) != 0 || failure.Root().InterfaceID().String() != "" {
		t.Fatalf("dormant explicit binding acquired activation: %v", err)
	}
	var absent *constructorgraph.ResourceBindingError
	if absent.Root().InterfaceID().String() != "" || absent.RequirementSources() != nil || absent.Steps() != nil {
		t.Fatal("nil error exposed activation")
	}
}
