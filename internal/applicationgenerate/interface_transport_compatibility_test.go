package applicationgenerate_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/interfacecompatibility"
)

func TestGenerateComparesInterfaceDescriptorProcedureAndWireMapProjections(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/interface-transport-compatibility"
	writeConnectApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  expose:
    - records.list/v1
`)
	interfacePath := filepath.Join(root, "interfaces", "records", "list", "v1", "interface.go")
	writeFile(t, interfacePath, interfaceProtobufSource(7))
	writeFile(t, filepath.Join(root, "records", "service.go"), `package records

import (
	"context"

	listv1 "example.com/interface-transport-compatibility/interfaces/records/list/v1"
)

type Service struct{}

//plystra:implements records.list/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) List(context.Context, listv1.Request) (listv1.Response, error) {
	return listv1.Response{}, nil
}
`)

	options := applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(nil),
		Validate:    func(context.Context, string) error { return nil },
	}
	initial, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !initial.Report().Clean() {
		t.Fatalf("Generate(initial) = changes %#v, %v", initial.Report().Changes(), err)
	}
	initialComparison := initial.InterfaceTransportComparison()
	if !initialComparison.Valid() || initialComparison.Clean() {
		t.Fatalf("initial transport comparison = %#v", initialComparison)
	}
	assertEvolutionVersionNeutral(t, initial)
	initialBaselineData := readFile(t, root, interfacecompatibility.TransportPath)
	initialBaseline, err := interfacecompatibility.DecodeTransport(initialBaselineData)
	if err != nil || !initialBaseline.Valid() || len(initialBaseline.Interfaces()) != 1 {
		t.Fatalf("DecodeTransport(initial) = %#v, %v", initialBaseline, err)
	}
	for _, forbidden := range []string{
		filepath.ToSlash(root),
		modulePath,
		"RecordsListV1Service",
		"/plystra.generated.records.list.v1",
		"page_size",
		"Service",
	} {
		if bytes.Contains(initialBaselineData, []byte(forbidden)) {
			t.Fatalf("transport baseline contains forbidden value %q: %s", forbidden, initialBaselineData)
		}
	}

	writeFile(t, interfacePath, interfaceProtobufSource(0))
	options.Check = true
	beforeCheck := snapshotTree(t, root)
	drift, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil ||
		!slicesContains(drift.Report().Stale(), interfacecompatibility.TransportPath) ||
		!drift.InterfaceShapeComparison().Valid() ||
		drift.InterfaceShapeComparison().Clean() ||
		!drift.InterfaceTransportComparison().Valid() ||
		drift.InterfaceTransportComparison().Clean() {
		t.Fatalf(
			"Generate --check(changed) = changes %#v shape %#v transport %#v, %v",
			drift.Report().Changes(),
			drift.InterfaceShapeComparison().Changes(),
			drift.InterfaceTransportComparison().Changes(),
			err,
		)
	}
	changes := drift.InterfaceTransportComparison().Changes()
	if len(changes) != 1 ||
		changes[0].Kind() != interfacecompatibility.ChangeChanged ||
		changes[0].ID() != "records.list/v1" ||
		!reflect.DeepEqual(
			changes[0].Classes(),
			[]interfacecompatibility.TransportClass{
				interfacecompatibility.TransportClassDescriptor,
				interfacecompatibility.TransportClassWireMap,
			},
		) {
		t.Fatalf("transport changes = %#v", changes)
	}
	assertEvolutionVersionRequired(
		t,
		drift,
		"records.list/v1",
		[]interfacecompatibility.VersionSurface{
			interfacecompatibility.VersionSurfaceGoShape,
			interfacecompatibility.VersionSurfaceContract,
			interfacecompatibility.VersionSurfaceProtobufDescriptor,
			interfacecompatibility.VersionSurfaceWireMap,
			interfacecompatibility.VersionSurfaceJavaScriptTypes,
		},
	)
	if afterCheck := snapshotTree(t, root); !reflect.DeepEqual(afterCheck, beforeCheck) {
		t.Fatal("transport compatibility check mutated the Project")
	}

	options.Check = false
	sentinel := errors.New("forced transport post-install validation failure")
	options.Validate = func(context.Context, string) error { return sentinel }
	beforeRollback := snapshotTree(t, root)
	if result, err := applicationgenerate.Generate(t.Context(), options); !errors.Is(err, sentinel) ||
		!reflect.DeepEqual(snapshotTree(t, root), beforeRollback) {
		t.Fatalf("Generate(rollback) = %#v, %v", result, err)
	}

	options.Validate = func(context.Context, string) error { return nil }
	updated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !updated.Report().Clean() || updated.InterfaceTransportComparison().Clean() {
		t.Fatalf("Generate(updated) = changes %#v transport %#v, %v", updated.Report().Changes(), updated.InterfaceTransportComparison().Changes(), err)
	}
	assertEvolutionVersionRequired(
		t,
		updated,
		"records.list/v1",
		[]interfacecompatibility.VersionSurface{
			interfacecompatibility.VersionSurfaceGoShape,
			interfacecompatibility.VersionSurfaceContract,
			interfacecompatibility.VersionSurfaceProtobufDescriptor,
			interfacecompatibility.VersionSurfaceWireMap,
			interfacecompatibility.VersionSurfaceJavaScriptTypes,
		},
	)
	updatedBaseline, err := interfacecompatibility.DecodeTransport(readFile(t, root, interfacecompatibility.TransportPath))
	if err != nil || updatedBaseline.Digest() == initialBaseline.Digest() {
		t.Fatalf("updated transport baseline = digest %q, %v", updatedBaseline.Digest(), err)
	}

	options.Check = true
	clean, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !clean.Report().Clean() || !clean.InterfaceTransportComparison().Clean() {
		t.Fatalf("Generate --check(clean) = changes %#v transport %#v, %v", clean.Report().Changes(), clean.InterfaceTransportComparison().Changes(), err)
	}
	assertEvolutionVersionNeutral(t, clean)
}
