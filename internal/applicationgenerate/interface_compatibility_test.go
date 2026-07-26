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
		!slicesContains(drift.Report().Changed(), interfacecompatibility.Path) ||
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
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, driftBefore) {
		t.Fatal("Generate --check changed the Project while reporting Interface compatibility drift")
	}

	options.Check = false
	updated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !updated.Report().Clean() || updated.InterfaceShapeComparison().Clean() {
		t.Fatalf("Generate(updated) = changes %#v comparison %#v, %v", updated.Report().Changes(), updated.InterfaceShapeComparison().Changes(), err)
	}
	updatedBaseline, err := interfacecompatibility.Decode(readFile(t, root, interfacecompatibility.Path))
	if err != nil || updatedBaseline.Digest() == initialBaseline.Digest() {
		t.Fatalf("updated baseline = digest %q, %v", updatedBaseline.Digest(), err)
	}
	options.Check = true
	clean, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !clean.Report().Clean() || !clean.InterfaceShapeComparison().Clean() {
		t.Fatalf("Generate --check(clean) = changes %#v comparison %#v, %v", clean.Report().Changes(), clean.InterfaceShapeComparison().Changes(), err)
	}

	writeFile(t, interfacePath, strings.Replace(compatibilityInterfaceSource(true, "value"), "Name string", "Name []byte", 1))
	rollbackBefore := snapshotTree(t, root)
	options.Check = false
	sentinel := errors.New("forced post-install validation failure")
	options.Validate = func(context.Context, string) error { return sentinel }
	if result, err := applicationgenerate.Generate(t.Context(), options); !errors.Is(err, sentinel) || !reflect.DeepEqual(snapshotTree(t, root), rollbackBefore) {
		t.Fatalf("Generate(rollback) = %#v, %v", result, err)
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
