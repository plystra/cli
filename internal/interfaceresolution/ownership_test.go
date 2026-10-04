package interfaceresolution_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceresolution"
)

func TestSelectedConstructorsIncludesDormantOwnersWithoutRequiringResourceBindings(t *testing.T) {
	t.Parallel()
	fixture := discoverResolutionFixture(t)
	input := interfaceresolution.Input{
		Interfaces: fixture.Interfaces(), Implementations: fixture.Implementations(),
		Requirements: []interfaceresolution.Requirement{resolutionRequirement(
			mustResolutionID(t, "app.run/v1"), "plystra.yaml interfaces.require[app.run/v1]", interfaceresolution.RequirementDeclaration,
		)},
		Choices: []interfaceresolution.Choice{{
			InterfaceID: mustResolutionID(t, "cache.read/v1"),
			Constructor: mustResolutionSymbol(t, "example.com/application/cache.New"),
			Sources: []interfaceresolution.ChoiceSource{resolutionChoiceSource(
				"plystra.yaml interfaces.use[cache.read/v1]", "example.com/application", "plystra.yaml",
			)},
		}},
		ResourceBindings: []constructorgraph.ResourceBindingInput{{
			Namespace: constructorgraph.ResourceConsumerImplementation,
			Consumer:  "example.com/application/removed.New", Parameter: "storage", Target: "missing",
		}},
	}
	owners, err := interfaceresolution.SelectedConstructors(input)
	if err != nil {
		t.Fatal(err)
	}
	want := []constructorsymbol.Symbol{
		mustResolutionSymbol(t, "example.com/application/app.New"),
		mustResolutionSymbol(t, "example.com/application/audit.New"),
		mustResolutionSymbol(t, "example.com/application/cache.New"),
	}
	if !reflect.DeepEqual(owners, want) {
		t.Fatalf("owners = %v, want %v", owners, want)
	}
	owners[0] = constructorsymbol.Symbol{}
	repeated, err := interfaceresolution.SelectedConstructors(input)
	if err != nil || !reflect.DeepEqual(repeated, want) {
		t.Fatalf("repeated owners = %v, %v", repeated, err)
	}
	if _, err := interfaceresolution.Resolve(input); err == nil {
		t.Fatal("ownership planning accepted an invalid executable graph")
	}
	input.Choices[0].Constructor = mustResolutionSymbol(t, "example.com/application/unknown.New")
	if owners, err := interfaceresolution.SelectedConstructors(input); !errors.Is(err, interfaceresolution.ErrUnknownConstructor) || owners != nil {
		t.Fatalf("unknown choice owners = %v, %v", owners, err)
	}
	input.Choices = nil
	input.Requirements = []interfaceresolution.Requirement{resolutionRequirement(
		mustResolutionID(t, "job.run/v1"), "plystra.yaml interfaces.require[job.run/v1]", interfaceresolution.RequirementDeclaration,
	)}
	if owners, err := interfaceresolution.SelectedConstructors(input); !errors.Is(err, constructorgraph.ErrMissingBinding) || owners != nil {
		t.Fatalf("incomplete closure owners = %v, %v", owners, err)
	}
}
