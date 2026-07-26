package applicationgenerate_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/interfacecompatibility"
)

func TestGenerateComparesCallerVisibleInterfaceJavaScriptAPI(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/interface-javascript-compatibility"
	writeConnectApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  expose:
    - records.list/v1
`)
	interfacePath := filepath.Join(root, "interfaces", "records", "list", "v1", "interface.go")
	initialSource := interfaceProtobufSource(7)
	writeFile(t, interfacePath, initialSource)
	writeFile(t, filepath.Join(root, "records", "service.go"), `package records

import (
	"context"

	listv1 "example.com/interface-javascript-compatibility/interfaces/records/list/v1"
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
	initialComparison := initial.InterfaceJavaScriptComparison()
	if !initialComparison.Valid() ||
		initialComparison.Clean() ||
		!initialComparison.PackageChanged() ||
		len(initialComparison.Changes()) != 1 ||
		initialComparison.Changes()[0].Kind() != interfacecompatibility.ChangeAdded {
		t.Fatalf("initial JavaScript comparison = %#v", initialComparison)
	}
	assertEvolutionVersionNeutral(t, initial)
	initialBaselineData := readFile(t, root, interfacecompatibility.JavaScriptPath)
	initialBaseline, err := interfacecompatibility.DecodeJavaScript(initialBaselineData)
	if err != nil || !initialBaseline.Valid() || len(initialBaseline.Interfaces()) != 1 {
		t.Fatalf("DecodeJavaScript(initial) = %#v, %v", initialBaseline, err)
	}
	for _, forbidden := range []string{
		filepath.ToSlash(root),
		modulePath,
		"RecordsListV1",
		"page_size",
		"Service",
		"secret",
		"config",
	} {
		if bytes.Contains(bytes.ToLower(initialBaselineData), bytes.ToLower([]byte(forbidden))) {
			t.Fatalf("JavaScript baseline contains forbidden value %q: %s", forbidden, initialBaselineData)
		}
	}

	writeFile(t, interfacePath, strings.Replace(initialSource, "PageSize int32", "PageSize int64", 1))
	options.Check = true
	beforeCheck := snapshotTree(t, root)
	drift, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil ||
		!slicesContains(drift.Report().Changed(), interfacecompatibility.JavaScriptPath) ||
		!drift.InterfaceJavaScriptComparison().Valid() ||
		drift.InterfaceJavaScriptComparison().Clean() ||
		drift.InterfaceJavaScriptComparison().PackageChanged() {
		t.Fatalf(
			"Generate --check(changed) = changes %#v JavaScript %#v, %v",
			drift.Report().Changes(),
			drift.InterfaceJavaScriptComparison().Changes(),
			err,
		)
	}
	changes := drift.InterfaceJavaScriptComparison().Changes()
	if len(changes) != 1 ||
		changes[0].Kind() != interfacecompatibility.ChangeChanged ||
		changes[0].ID() != "records.list/v1" ||
		!reflect.DeepEqual(
			changes[0].Classes(),
			[]interfacecompatibility.JavaScriptClass{
				interfacecompatibility.JavaScriptClassTypes,
			},
		) {
		t.Fatalf("JavaScript changes = %#v", changes)
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
		t.Fatal("JavaScript compatibility check mutated the Project")
	}

	options.Check = false
	sentinel := errors.New("forced JavaScript post-install validation failure")
	options.Validate = func(context.Context, string) error { return sentinel }
	beforeRollback := snapshotTree(t, root)
	if result, err := applicationgenerate.Generate(t.Context(), options); !errors.Is(err, sentinel) ||
		!reflect.DeepEqual(snapshotTree(t, root), beforeRollback) {
		t.Fatalf("Generate(rollback) = %#v, %v", result, err)
	}

	options.Validate = func(context.Context, string) error { return nil }
	updated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !updated.Report().Clean() || updated.InterfaceJavaScriptComparison().Clean() {
		t.Fatalf(
			"Generate(updated) = changes %#v JavaScript %#v, %v",
			updated.Report().Changes(),
			updated.InterfaceJavaScriptComparison().Changes(),
			err,
		)
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
	updatedBaseline, err := interfacecompatibility.DecodeJavaScript(
		readFile(t, root, interfacecompatibility.JavaScriptPath),
	)
	if err != nil || updatedBaseline.Digest() == initialBaseline.Digest() {
		t.Fatalf("updated JavaScript baseline = digest %q, %v", updatedBaseline.Digest(), err)
	}

	options.Check = true
	clean, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !clean.Report().Clean() || !clean.InterfaceJavaScriptComparison().Clean() {
		t.Fatalf(
			"Generate --check(clean) = changes %#v JavaScript %#v, %v",
			clean.Report().Changes(),
			clean.InterfaceJavaScriptComparison().Changes(),
			err,
		)
	}
	assertEvolutionVersionNeutral(t, clean)
}
