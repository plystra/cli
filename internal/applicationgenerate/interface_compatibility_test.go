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

func TestGenerateComparesAuthoredInterfaceShapeAgainstOwnedBaseline(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/interface-compatibility"
	writeApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	interfacePath := filepath.Join(root, "interfaces", "records", "echo", "v1", "interface.go")
	writeFile(t, interfacePath, compatibilityInterfaceSource(false, "name"))

	options := applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(nil),
		Validate:    func(context.Context, string) error { return nil },
	}
	initial, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !initial.Report().Clean() {
		t.Fatalf("Generate(initial) = changes %#v, %v", initial.Report().Changes(), err)
	}
	initialComparison := initial.InterfaceShapeComparison()
	if !initialComparison.Valid() || initialComparison.Clean() {
		t.Fatalf("initial comparison = %#v", initialComparison)
	}
	if changes := initialComparison.Changes(); len(changes) != 1 ||
		changes[0].Kind() != interfacecompatibility.ChangeAdded ||
		changes[0].ID() != "records.echo/v1" {
		t.Fatalf("initial comparison changes = %#v", changes)
	}
	assertEvolutionVersionNeutral(t, initial)
	initialBaselineData := readFile(t, root, interfacecompatibility.Path)
	initialBaseline, err := interfacecompatibility.Decode(initialBaselineData)
	if err != nil || !initialBaseline.Valid() || len(initialBaseline.Interfaces()) != 1 {
		t.Fatalf("Decode(initial baseline) = %#v, %v", initialBaseline, err)
	}
	for _, forbidden := range []string{filepath.ToSlash(root), "interface.go", "local", "resolved-secret"} {
		if bytes.Contains(initialBaselineData, []byte(forbidden)) {
			t.Fatalf("Interface baseline contains forbidden provenance %q: %s", forbidden, initialBaselineData)
		}
	}

	writeFile(t, interfacePath, compatibilityInterfaceSource(true, "name"))
	options.Check = true
	reorderedBefore := snapshotTree(t, root)
	reordered, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !reordered.Report().Clean() || !reordered.InterfaceShapeComparison().Clean() {
		t.Fatalf("Generate --check(reordered) = changes %#v comparison %#v, %v", reordered.Report().Changes(), reordered.InterfaceShapeComparison().Changes(), err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, reorderedBefore) {
		t.Fatal("equivalent declaration reordering mutated the Project")
	}

	writeFile(t, interfacePath, compatibilityInterfaceSource(true, "value"))
	driftBefore := snapshotTree(t, root)
	drift, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil ||
		!slicesContains(drift.Report().Stale(), interfacecompatibility.Path) ||
		drift.InterfaceShapeComparison().Clean() ||
		!drift.InterfaceShapeComparison().Valid() {
		t.Fatalf("Generate --check(changed) = changes %#v comparison %#v, %v", drift.Report().Changes(), drift.InterfaceShapeComparison().Changes(), err)
	}
	shapeChanges := drift.InterfaceShapeComparison().Changes()
	if len(shapeChanges) != 1 ||
		shapeChanges[0].Kind() != interfacecompatibility.ChangeChanged ||
		shapeChanges[0].ID() != "records.echo/v1" ||
		shapeChanges[0].PreviousDigest() == shapeChanges[0].CurrentDigest() {
		t.Fatalf("shape changes = %#v", shapeChanges)
	}
	assertEvolutionVersionRequired(
		t,
		drift,
		"records.echo/v1",
		[]interfacecompatibility.VersionSurface{
			interfacecompatibility.VersionSurfaceGoShape,
			interfacecompatibility.VersionSurfaceContract,
		},
	)
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, driftBefore) {
		t.Fatal("Generate --check changed the Project while reporting Interface compatibility drift")
	}

	options.Check = false
	updated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !updated.Report().Clean() || updated.InterfaceShapeComparison().Clean() {
		t.Fatalf("Generate(updated) = changes %#v comparison %#v, %v", updated.Report().Changes(), updated.InterfaceShapeComparison().Changes(), err)
	}
	assertEvolutionVersionRequired(
		t,
		updated,
		"records.echo/v1",
		[]interfacecompatibility.VersionSurface{
			interfacecompatibility.VersionSurfaceGoShape,
			interfacecompatibility.VersionSurfaceContract,
		},
	)
	updatedBaseline, err := interfacecompatibility.Decode(readFile(t, root, interfacecompatibility.Path))
	if err != nil || updatedBaseline.Digest() == initialBaseline.Digest() {
		t.Fatalf("updated baseline = digest %q, %v", updatedBaseline.Digest(), err)
	}
	options.Check = true
	clean, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !clean.Report().Clean() || !clean.InterfaceShapeComparison().Clean() {
		t.Fatalf("Generate --check(clean) = changes %#v comparison %#v, %v", clean.Report().Changes(), clean.InterfaceShapeComparison().Changes(), err)
	}
	assertEvolutionVersionNeutral(t, clean)

	writeFile(t, interfacePath, strings.Replace(compatibilityInterfaceSource(true, "value"), "Name string", "Name []byte", 1))
	rollbackBefore := snapshotTree(t, root)
	options.Check = false
	sentinel := errors.New("forced post-install validation failure")
	options.Validate = func(context.Context, string) error { return sentinel }
	if result, err := applicationgenerate.Generate(t.Context(), options); !errors.Is(err, sentinel) || !reflect.DeepEqual(snapshotTree(t, root), rollbackBefore) {
		t.Fatalf("Generate(rollback) = %#v, %v", result, err)
	}
}

func assertEvolutionVersionNeutral(
	t testing.TB,
	result applicationgenerate.Result,
) {
	t.Helper()

	assessment := result.InterfaceEvolutionAssessment()
	if !assessment.Valid() ||
		assessment.RequiresNewVersion() ||
		len(assessment.Requirements()) != 0 {
		t.Fatalf("Interface evolution assessment = %#v", assessment.Requirements())
	}
	if err := assessment.ValidateStableVersioning(); err != nil {
		t.Fatalf("ValidateStableVersioning = %v", err)
	}
}

func assertEvolutionVersionRequired(
	t testing.TB,
	result applicationgenerate.Result,
	identifier string,
	wantSurfaces []interfacecompatibility.VersionSurface,
) {
	t.Helper()

	assessment := result.InterfaceEvolutionAssessment()
	if !assessment.Valid() || !assessment.RequiresNewVersion() {
		t.Fatalf("Interface evolution assessment = %#v", assessment.Requirements())
	}
	requirements := assessment.Requirements()
	if len(requirements) != 1 || requirements[0].ID() != identifier {
		t.Fatalf("Interface evolution requirements = %#v", requirements)
	}
	changes := requirements[0].Changes()
	gotSurfaces := make([]interfacecompatibility.VersionSurface, len(changes))
	for index, change := range changes {
		gotSurfaces[index] = change.Surface()
		if change.Kind() != interfacecompatibility.ChangeChanged {
			t.Fatalf("Interface evolution change = %#v", change)
		}
	}
	if !reflect.DeepEqual(gotSurfaces, wantSurfaces) {
		t.Fatalf("Interface evolution surfaces = %#v, want %#v", gotSurfaces, wantSurfaces)
	}
	stableErr := assessment.ValidateStableVersioning()
	if !errors.Is(stableErr, interfacecompatibility.ErrStableVersionRequired) ||
		!strings.Contains(stableErr.Error(), identifier) ||
		!strings.Contains(stableErr.Error(), "new higher /vN") {
		t.Fatalf("ValidateStableVersioning = %v", stableErr)
	}
	var typed *interfacecompatibility.StableVersionError
	if !errors.As(stableErr, &typed) ||
		len(typed.Requirements()) != 1 ||
		typed.Requirements()[0].ID() != identifier {
		t.Fatalf("stable version evidence = %#v, %v", typed, stableErr)
	}
}

func compatibilityInterfaceSource(reordered bool, jsonName string) string {
	fields := "\tName string `plystra:\"1,required\" json:\"" + jsonName + "\"`\n\tCount int64 `plystra:\"2\" json:\"count\"`"
	if reordered {
		fields = "\tCount int64 `plystra:\"2\" json:\"count\"`\n\tName string `plystra:\"1,required\" json:\"" + jsonName + "\"`"
	}
	return `package echov1

import "context"

//plystra:interface records.echo/v1
type Interface interface {
	Echo(context.Context, Request) (Response, error)
}

type Request struct {
` + fields + `
}

type Response struct{}
`
}
