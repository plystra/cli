package interfaceresolution_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
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
	if owners, err := interfaceresolution.SelectedConstructors(input); !errors.Is(err, interfaceresolution.ErrUnknownConstructor) || !reflect.DeepEqual(owners, want[:2]) {
		t.Fatalf("unknown choice owners = %v, %v", owners, err)
	}
	input.Choices = nil
	input.Requirements = []interfaceresolution.Requirement{resolutionRequirement(
		mustResolutionID(t, "job.run/v1"), "plystra.yaml interfaces.require[job.run/v1]", interfaceresolution.RequirementDeclaration,
	)}
	if owners, err := interfaceresolution.SelectedConstructors(input); !errors.Is(err, constructorgraph.ErrMissingBinding) || !reflect.DeepEqual(owners, []constructorsymbol.Symbol{mustResolutionSymbol(t, "example.com/application/job.New")}) {
		t.Fatalf("incomplete closure owners = %v, %v", owners, err)
	}
}

func TestSelectedConstructorsContinuesIndependentDependencyBranches(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, firstPath string
		failure         error
		owners          []string
	}{
		{"missing", "job/run/v1", constructorgraph.ErrMissingBinding, []string{"app", "audit", "cache", "job", "worker"}},
		{"ambiguous", "email/send/v1", interfaceresolution.ErrAmbiguousImplementation, []string{"app", "audit", "cache", "worker"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := discoverResolutionFixture(t, func(root string) {
				writeResolutionFile(t, filepath.Join(root, "app/service.go"), fmt.Sprintf(`package app
import (
 "context"
 run "example.com/application/interfaces/app/run/v1"
 first "example.com/application/interfaces/%s"
 audit "example.com/application/interfaces/audit/write/v1"
)
type Service struct{}
//plystra:implements app.run/v1
func New(first first.Interface, audit audit.Interface) (*Service, error) { return &Service{}, nil }
func (*Service) Run(context.Context, run.Request) (run.Response, error) { return run.Response{}, nil }
`, test.firstPath))
				writeResolutionFile(t, filepath.Join(root, "audit/service.go"), `package audit
import (
 "context"
 audit "example.com/application/interfaces/audit/write/v1"
 cache "example.com/application/interfaces/cache/read/v1"
)
type Service struct{}
//plystra:implements audit.write/v1
func New(cache cache.Interface) (*Service, error) { return &Service{}, nil }
func (*Service) Write(context.Context, audit.Request) (audit.Response, error) { return audit.Response{}, nil }
`)
				writeSimpleImplementation(t, root, "replacement", "app.run/v1", "app/run/v1", "Run")
				writeResolutionFile(t, filepath.Join(root, "interfaces/worker/run/v1/interface.go"), resolutionInterfaceSource("workerv1", "worker.run/v1", "Run"))
				writeSimpleImplementation(t, root, "worker", "worker.run/v1", "worker/run/v1", "Run")
			})
			input := interfaceresolution.Input{
				Interfaces: fixture.Interfaces(), Implementations: fixture.Implementations(),
				Requirements: []interfaceresolution.Requirement{ownershipRequirement(t, "app.run/v1"), ownershipRequirement(t, "cache.read/v1"), ownershipRequirement(t, "worker.run/v1")},
				Choices:      []interfaceresolution.Choice{ownershipChoice(t, "app.run/v1", "app")},
			}
			_, before := interfaceresolution.Resolve(input)
			if !errors.Is(before, test.failure) {
				t.Fatalf("initial resolution = %v", before)
			}
			assertPositiveOwners(t, input, test.failure, test.owners...)
			_, after := interfaceresolution.Resolve(input)
			if reflect.TypeOf(before) != reflect.TypeOf(after) || before.Error() != after.Error() {
				t.Fatalf("ownership traversal changed ordinary resolution: before %v, after %v", before, after)
			}
			for _, final := range []struct {
				name         string
				requirements []string
				dormantAudit bool
				owners       []string
			}{
				{"orphaned closure", []string{"app.run/v1"}, false, []string{"replacement"}},
				{"shared transitive owner", []string{"app.run/v1", "audit.write/v1", "cache.read/v1"}, false, []string{"audit", "cache", "replacement"}},
				{"dormant owner only", []string{"app.run/v1"}, true, []string{"audit", "replacement"}},
			} {
				t.Run(final.name, func(t *testing.T) {
					candidate := input
					candidate.Requirements = nil
					for _, id := range final.requirements {
						candidate.Requirements = append(candidate.Requirements, ownershipRequirement(t, id))
					}
					candidate.Choices = []interfaceresolution.Choice{ownershipChoice(t, "app.run/v1", "replacement")}
					if final.dormantAudit {
						candidate.Choices = append(candidate.Choices, ownershipChoice(t, "audit.write/v1", "audit"))
					}
					assertPositiveOwners(t, candidate, nil, final.owners...)
					if _, err := interfaceresolution.Resolve(candidate); err != nil {
						t.Fatalf("complete final resolution = %v", err)
					}
				})
			}
		})
	}
}

func TestSelectedConstructorsIsolatesInvalidRequirementsAndChoices(t *testing.T) {
	t.Parallel()
	fixture := discoverResolutionFixture(t)
	invalidSource := ownershipRequirement(t, "job.run/v1")
	invalidSource.Source.Line = 0
	invalidChoiceSource := ownershipChoice(t, "audit.write/v1", "audit")
	invalidChoiceSource.Sources = nil
	emptyConstructor := ownershipChoice(t, "audit.write/v1", "audit")
	emptyConstructor.Constructor = constructorsymbol.Symbol{}
	emptyChoice := ownershipChoice(t, "audit.write/v1", "audit")
	emptyChoice.InterfaceID = interfaceid.Identifier{}
	for _, test := range []struct {
		name         string
		requirements []interfaceresolution.Requirement
		choices      []interfaceresolution.Choice
		failure      error
		owners       []string
	}{
		{"unknown root", []interfaceresolution.Requirement{ownershipRequirement(t, "unknown.root/v1")}, nil, interfaceresolution.ErrUnknownInterface, []string{"app", "audit", "cache"}},
		{"empty root", []interfaceresolution.Requirement{{}}, nil, interfaceresolution.ErrInvalidInput, []string{"app", "audit", "cache"}},
		{"invalid root source", []interfaceresolution.Requirement{invalidSource}, nil, interfaceresolution.ErrInvalidInput, []string{"app", "audit", "cache"}},
		{"missing root", []interfaceresolution.Requirement{ownershipRequirement(t, "missing.need/v1")}, nil, constructorgraph.ErrMissingBinding, []string{"app", "audit", "cache"}},
		{"ambiguous root", []interfaceresolution.Requirement{ownershipRequirement(t, "email.send/v1")}, nil, interfaceresolution.ErrAmbiguousImplementation, []string{"app", "audit", "cache"}},
		{"unknown requested choice blocks root fallback", nil, []interfaceresolution.Choice{ownershipChoice(t, "app.run/v1", "unknown")}, interfaceresolution.ErrUnknownConstructor, []string{"cache"}},
		{"unknown constructor blocks transitive fallback", nil, []interfaceresolution.Choice{ownershipChoice(t, "audit.write/v1", "unknown")}, interfaceresolution.ErrUnknownConstructor, []string{"app", "cache"}},
		{"invalid root does not bypass invalid choice", []interfaceresolution.Requirement{ownershipRequirement(t, "unknown.root/v1")}, []interfaceresolution.Choice{ownershipChoice(t, "audit.write/v1", "unknown")}, interfaceresolution.ErrUnknownInterface, []string{"app", "cache"}},
		{"incompatible choice blocks fallback", nil, []interfaceresolution.Choice{ownershipChoice(t, "audit.write/v1", "app")}, interfaceresolution.ErrIncompatibleChoice, []string{"app", "cache"}},
		{"invalid choice source blocks fallback", nil, []interfaceresolution.Choice{invalidChoiceSource}, interfaceresolution.ErrInvalidInput, []string{"app", "cache"}},
		{"empty constructor blocks fallback", nil, []interfaceresolution.Choice{emptyConstructor}, interfaceresolution.ErrInvalidInput, []string{"app", "cache"}},
		{"empty choice interface", nil, []interfaceresolution.Choice{emptyChoice}, interfaceresolution.ErrInvalidInput, []string{"app", "audit", "cache"}},
		{"valid duplicate cannot mask invalid choice", nil, []interfaceresolution.Choice{ownershipChoice(t, "audit.write/v1", "audit"), ownershipChoice(t, "audit.write/v1", "unknown")}, interfaceresolution.ErrUnknownConstructor, []string{"app", "cache"}},
		{"unknown choice interface", nil, []interfaceresolution.Choice{ownershipChoice(t, "unknown.root/v1", "audit")}, interfaceresolution.ErrUnknownInterface, []string{"app", "audit", "cache"}},
		{"intrinsic choice", nil, []interfaceresolution.Choice{ownershipChoice(t, "kernel.health/v1", "audit")}, interfaceresolution.ErrIntrinsicChoice, []string{"app", "audit", "cache"}},
		{"conflicting choices block both", []interfaceresolution.Requirement{ownershipRequirement(t, "email.send/v1")}, []interfaceresolution.Choice{ownershipChoice(t, "email.send/v1", "emailone"), ownershipChoice(t, "email.send/v1", "emailtwo")}, interfaceresolution.ErrInvalidInput, []string{"app", "audit", "cache"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := interfaceresolution.Input{
				Interfaces: fixture.Interfaces(), Implementations: fixture.Implementations(),
				Requirements: append(slices.Clone(test.requirements), ownershipRequirement(t, "app.run/v1")),
				Choices:      append(slices.Clone(test.choices), ownershipChoice(t, "cache.read/v1", "cache")),
			}
			_, before := interfaceresolution.Resolve(input)
			if !errors.Is(before, test.failure) {
				t.Fatalf("initial resolution = %v", before)
			}
			assertPositiveOwners(t, input, test.failure, test.owners...)
			_, after := interfaceresolution.Resolve(input)
			if before.Error() != after.Error() {
				t.Fatalf("ordinary resolution changed: before %v, after %v", before, after)
			}
			slices.Reverse(input.Requirements)
			slices.Reverse(input.Choices)
			assertPositiveOwners(t, input, test.failure, test.owners...)
		})
	}
}

func assertPositiveOwners(t testing.TB, input interfaceresolution.Input, failure error, packages ...string) {
	t.Helper()
	want := make([]constructorsymbol.Symbol, 0, len(packages))
	for _, name := range packages {
		want = append(want, mustResolutionSymbol(t, "example.com/application/"+name+".New"))
	}
	slices.SortFunc(want, func(a, b constructorsymbol.Symbol) int { return strings.Compare(a.String(), b.String()) })
	owners, err := interfaceresolution.SelectedConstructors(input)
	if !errors.Is(err, failure) || !reflect.DeepEqual(owners, want) {
		t.Fatalf("owners = %v, %v; want %v, %v", owners, err, want, failure)
	}
	if err != nil && !errors.Is(err, constructorgraph.ErrMissingBinding) {
		_, strictErr := interfaceresolution.Resolve(input)
		if strictErr == nil || strictErr.Error() != err.Error() {
			t.Fatalf("ownership error = %v; ordinary resolution error = %v", err, strictErr)
		}
	}
	if len(owners) != 0 {
		owners[0] = constructorsymbol.Symbol{}
	}
	repeated, repeatedErr := interfaceresolution.SelectedConstructors(input)
	if !reflect.DeepEqual(repeated, want) || fmt.Sprint(err) != fmt.Sprint(repeatedErr) {
		t.Fatalf("repeated owners = %v, %v; want %v, %v", repeated, repeatedErr, want, err)
	}
}

func ownershipRequirement(t testing.TB, id string) interfaceresolution.Requirement {
	t.Helper()
	return resolutionRequirement(mustResolutionID(t, id), "plystra.yaml interfaces.require["+id+"]", interfaceresolution.RequirementDeclaration)
}

func ownershipChoice(t testing.TB, id, name string) interfaceresolution.Choice {
	t.Helper()
	return interfaceresolution.Choice{
		InterfaceID: mustResolutionID(t, id),
		Constructor: mustResolutionSymbol(t, "example.com/application/"+name+".New"),
		Sources:     []interfaceresolution.ChoiceSource{resolutionChoiceSource("plystra.yaml interfaces.use["+id+"]", "example.com/application", "plystra.yaml")},
	}
}
