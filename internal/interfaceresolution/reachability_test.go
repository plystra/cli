package interfaceresolution_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceresolution"
)

func TestReachableConstructorsRequiresCompleteSelectionNotResourceGraph(t *testing.T) {
	t.Parallel()
	fixture := discoverResolutionFixture(t)
	input := interfaceresolution.Input{
		Interfaces: fixture.Interfaces(), Implementations: fixture.Implementations(),
		Requirements: []interfaceresolution.Requirement{resolutionRequirement(
			mustResolutionID(t, "app.run/v1"), "plystra.yaml interfaces.require", interfaceresolution.RequirementDeclaration,
		)},
		Choices: []interfaceresolution.Choice{{
			InterfaceID: mustResolutionID(t, "cache.read/v1"), Constructor: mustResolutionSymbol(t, "example.com/application/cache.New"),
			Sources: []interfaceresolution.ChoiceSource{resolutionChoiceSource("plystra.yaml interfaces.use", "example.com/application", "plystra.yaml")},
		}},
		ResourceInstances: []constructorgraph.ResourceInstanceInput{{Name: "unbound"}},
	}
	want := []constructorsymbol.Symbol{mustResolutionSymbol(t, "example.com/application/app.New"), mustResolutionSymbol(t, "example.com/application/audit.New")}
	got, err := interfaceresolution.ReachableConstructors(input)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("reachable constructors include dormant optional choice or require Resources: %v, %v", got, err)
	}
	got[0] = constructorsymbol.Symbol{}
	repeated, err := interfaceresolution.ReachableConstructors(input)
	if err != nil || !reflect.DeepEqual(repeated, want) {
		t.Fatalf("repeated reachability aliases caller storage: %v, %v", repeated, err)
	}
	if _, err := interfaceresolution.Resolve(input); err == nil {
		t.Fatal("Interface reachability relaxed final Resource validation")
	}
	input.Requirements = append(input.Requirements, resolutionRequirement(mustResolutionID(t, "cache.read/v1"), "plystra.yaml cache root", interfaceresolution.RequirementDeclaration))
	promoted, err := interfaceresolution.ReachableConstructors(input)
	if err != nil || !reflect.DeepEqual(promoted, append(want, mustResolutionSymbol(t, "example.com/application/cache.New"))) {
		t.Fatalf("independently rooted optional dependency not reachable: %v, %v", promoted, err)
	}
	input.Requirements = nil
	if got, err := interfaceresolution.ReachableConstructors(input); err != nil || len(got) != 0 {
		t.Fatalf("dormant choice became a reachable constructor: %v, %v", got, err)
	}
	input.Choices[0].Constructor = mustResolutionSymbol(t, "example.com/application/unknown.New")
	if got, err := interfaceresolution.ReachableConstructors(input); !errors.Is(err, interfaceresolution.ErrUnknownConstructor) || got != nil {
		t.Fatalf("invalid dormant selection accepted: %v, %v", got, err)
	}
	input.Choices = nil
	for _, test := range []struct {
		id   string
		want error
	}{{"job.run/v1", constructorgraph.ErrMissingBinding}, {"email.send/v1", interfaceresolution.ErrAmbiguousImplementation}, {"absent.call/v1", interfaceresolution.ErrUnknownInterface}} {
		input.Requirements = []interfaceresolution.Requirement{resolutionRequirement(mustResolutionID(t, test.id), "plystra.yaml interfaces.require", interfaceresolution.RequirementDeclaration)}
		if got, err := interfaceresolution.ReachableConstructors(input); !errors.Is(err, test.want) || got != nil {
			t.Fatalf("incomplete %s closure published absence evidence: %v, %v", test.id, got, err)
		}
	}
	if got, err := interfaceresolution.ReachableConstructors(interfaceresolution.Input{}); err != nil || len(got) != 0 {
		t.Fatalf("intrinsic-only closure: %v, %v", got, err)
	}
}

func TestReachableConstructorsDeduplicatesSharedAndCyclicClosures(t *testing.T) {
	t.Parallel()
	fixture := discoverResolutionFixture(t, func(root string) {
		writeResolutionFile(t, filepath.Join(root, "audit/service.go"), `package audit
import (
 "context"
 audit "example.com/application/interfaces/audit/write/v1"
 cache "example.com/application/interfaces/cache/read/v1"
)
type Service struct{}
//plystra:implements audit.write/v1
//plystra:implements cache.read/v1
func New()(*Service,error){return &Service{},nil}
func (*Service) Write(context.Context,audit.Request)(audit.Response,error){return audit.Response{},nil}
func (*Service) Read(context.Context,cache.Request)(cache.Response,error){return cache.Response{},nil}
`)
		writeResolutionFile(t, filepath.Join(root, "cache/service.go"), `package cache
import (
 "context"
 app "example.com/application/interfaces/app/run/v1"
 cache "example.com/application/interfaces/cache/read/v1"
)
type Service struct{}
//plystra:implements cache.read/v1
func New(dependency app.Interface)(*Service,error){return &Service{},nil}
func (*Service) Read(context.Context,cache.Request)(cache.Response,error){return cache.Response{},nil}
`)
	})
	input := interfaceresolution.Input{
		Interfaces: fixture.Interfaces(), Implementations: fixture.Implementations(),
		Requirements: []interfaceresolution.Requirement{
			resolutionRequirement(mustResolutionID(t, "app.run/v1"), "plystra.yaml app", interfaceresolution.RequirementDeclaration),
			resolutionRequirement(mustResolutionID(t, "cache.read/v1"), "plystra.yaml cache", interfaceresolution.RequirementDeclaration),
		},
		Choices: []interfaceresolution.Choice{{
			InterfaceID: mustResolutionID(t, "cache.read/v1"), Constructor: mustResolutionSymbol(t, "example.com/application/audit.New"),
			Sources: []interfaceresolution.ChoiceSource{resolutionChoiceSource("plystra.yaml interfaces.use", "example.com/application", "plystra.yaml")},
		}},
	}
	want := []constructorsymbol.Symbol{mustResolutionSymbol(t, "example.com/application/app.New"), mustResolutionSymbol(t, "example.com/application/audit.New")}
	if got, err := interfaceresolution.ReachableConstructors(input); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("shared constructor was not deduplicated: %v, %v", got, err)
	}
	if _, err := interfaceresolution.Resolve(input); err != nil {
		t.Fatalf("shared acyclic constructor failed ordinary resolution: %v", err)
	}
	input.Choices[0].Constructor = mustResolutionSymbol(t, "example.com/application/cache.New")
	if got, err := interfaceresolution.ReachableConstructors(input); err != nil || !reflect.DeepEqual(got, append(want, mustResolutionSymbol(t, "example.com/application/cache.New"))) {
		t.Fatalf("independently activated optional cycle lost complete reachability: %v, %v", got, err)
	}
	if _, err := interfaceresolution.Resolve(input); !errors.Is(err, constructorgraph.ErrCycle) {
		t.Fatalf("reachability accepted an optional executable cycle: %v", err)
	}
	input.Choices = nil
	input.Requirements = []interfaceresolution.Requirement{resolutionRequirement(mustResolutionID(t, "cycle.a/v1"), "plystra.yaml cycle", interfaceresolution.RequirementDeclaration)}
	want = []constructorsymbol.Symbol{mustResolutionSymbol(t, "example.com/application/cyclea.New"), mustResolutionSymbol(t, "example.com/application/cycleb.New")}
	if got, err := interfaceresolution.ReachableConstructors(input); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("required cycle lost complete reachability: %v, %v", got, err)
	}
	if _, err := interfaceresolution.Resolve(input); !errors.Is(err, constructorgraph.ErrCycle) {
		t.Fatalf("reachability accepted a required executable cycle: %v", err)
	}
}
