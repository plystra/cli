package applicationgenerate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/generatedfiles"
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

func TestGenerateMigratesOwnedInterfaceShapeV1Transactionally(t *testing.T) {
	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/interface-shape-migration")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeFile(
		t,
		filepath.Join(root, "interfaces", "records", "echo", "v1", "interface.go"),
		compatibilityInterfaceSource(false, "name"),
	)

	options := applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(nil),
		Validate:    func(context.Context, string) error { return nil },
	}
	if _, err := applicationgenerate.Generate(t.Context(), options); err != nil {
		t.Fatalf("Generate(initial): %v", err)
	}
	assertCompatibilityWorkingRecordKinds(t, root)
	legacyShape, legacyOwnership := replaceOwnedShapeWithV1(t, root)

	options.Check = true
	beforeCheck := snapshotTree(t, root)
	checked, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil ||
		!checked.InterfaceShapeComparison().Valid() ||
		!checked.InterfaceShapeComparison().Clean() ||
		!slicesContains(checked.Report().Stale(), interfacecompatibility.Path) ||
		!slicesContains(checked.Report().Stale(), generatedfiles.ManifestPath) {
		t.Fatalf(
			"Generate --check(v1) = changes %#v shape %#v, %v",
			checked.Report().Changes(),
			checked.InterfaceShapeComparison().Changes(),
			err,
		)
	}
	assertEvolutionVersionNeutral(t, checked)
	if afterCheck := snapshotTree(t, root); !reflect.DeepEqual(afterCheck, beforeCheck) {
		t.Fatal("v1 shape migration check mutated the Project")
	}

	options.Check = false
	sentinel := errors.New("forced v1 shape migration validation failure")
	options.Validate = func(context.Context, string) error { return sentinel }
	beforeRollback := snapshotTree(t, root)
	if result, err := applicationgenerate.Generate(t.Context(), options); !errors.Is(err, sentinel) ||
		!reflect.DeepEqual(snapshotTree(t, root), beforeRollback) {
		t.Fatalf("Generate(v1 rollback) = %#v, %v", result, err)
	}
	if !bytes.Equal(readFile(t, root, interfacecompatibility.Path), legacyShape) ||
		!bytes.Equal(readFile(t, root, generatedfiles.ManifestPath), legacyOwnership) {
		t.Fatal("v1 shape migration rollback did not restore shape and ownership")
	}

	options.Validate = func(context.Context, string) error { return nil }
	migrated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !migrated.Report().Clean() || !migrated.InterfaceShapeComparison().Clean() {
		t.Fatalf(
			"Generate(v1 migration) = changes %#v shape %#v, %v",
			migrated.Report().Changes(),
			migrated.InterfaceShapeComparison().Changes(),
			err,
		)
	}
	assertCompatibilityWorkingRecordKinds(t, root)
	assertEvolutionVersionNeutral(t, migrated)
	migratedShape := readFile(t, root, interfacecompatibility.Path)
	migratedOwnership := readFile(t, root, generatedfiles.ManifestPath)
	decoded, err := interfacecompatibility.Decode(migratedShape)
	if err != nil || decoded.Schema() != interfacecompatibility.Schema ||
		bytes.Equal(migratedShape, legacyShape) || bytes.Equal(migratedOwnership, legacyOwnership) {
		t.Fatalf("migrated shape = schema %q, error %v", decoded.Schema(), err)
	}
	owned, exists, err := generatedfiles.ReadOwnedFile(
		root,
		interfacecompatibility.Path,
		interfacecompatibility.MaximumBytes,
	)
	if err != nil || !exists || !bytes.Equal(owned, migratedShape) {
		t.Fatalf("ReadOwnedFile(migrated shape) = %t, %v", exists, err)
	}

	options.Check = true
	clean, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !clean.Report().Clean() || !clean.InterfaceShapeComparison().Clean() {
		t.Fatalf("Generate --check(migrated) = changes %#v, %v", clean.Report().Changes(), err)
	}
}

type legacyShapeInterface struct {
	ID          string               `json:"id"`
	PackagePath string               `json:"package"`
	Method      string               `json:"method"`
	Request     string               `json:"request"`
	Response    string               `json:"response"`
	Messages    []legacyShapeMessage `json:"messages"`
	Digest      string               `json:"digest"`
}

type legacyShapeMessage struct {
	Name   string             `json:"name"`
	Fields []legacyShapeField `json:"fields"`
}

type legacyShapeField struct {
	Number   uint64 `json:"number"`
	GoName   string `json:"go_name"`
	JSONName string `json:"json_name"`
	Required bool   `json:"required"`
	Type     string `json:"type"`
}

func replaceOwnedShapeWithV1(t testing.TB, root string) ([]byte, []byte) {
	t.Helper()

	current, err := interfacecompatibility.Decode(readFile(t, root, interfacecompatibility.Path))
	if err != nil || current.Schema() != interfacecompatibility.Schema {
		t.Fatalf("Decode(current shape) = schema %q, %v", current.Schema(), err)
	}
	legacyInterfaces := make([]legacyShapeInterface, len(current.Interfaces()))
	for interfaceIndex, value := range current.Interfaces() {
		messages := value.Messages()
		legacyMessages := make([]legacyShapeMessage, len(messages))
		for messageIndex, message := range messages {
			fields := message.Fields()
			legacyFields := make([]legacyShapeField, len(fields))
			for fieldIndex, field := range fields {
				if field.PointerDepth() != 0 {
					t.Fatalf("shape v1 cannot represent pointer field %s.%s", message.Name(), field.GoName())
				}
				legacyFields[fieldIndex] = legacyShapeField{
					Number:   field.Number(),
					GoName:   field.GoName(),
					JSONName: field.JSONName(),
					Required: field.Required(),
					Type:     field.Type(),
				}
			}
			legacyMessages[messageIndex] = legacyShapeMessage{
				Name:   message.Name(),
				Fields: legacyFields,
			}
		}
		legacyInterfaces[interfaceIndex] = legacyShapeInterface{
			ID:          value.ID(),
			PackagePath: value.PackagePath(),
			Method:      value.Method(),
			Request:     value.Request(),
			Response:    value.Response(),
			Messages:    legacyMessages,
		}
		canonicalShape, err := json.Marshal(struct {
			Schema   string               `json:"schema"`
			ID       string               `json:"id"`
			Package  string               `json:"package"`
			Method   string               `json:"method"`
			Request  string               `json:"request"`
			Response string               `json:"response"`
			Messages []legacyShapeMessage `json:"messages"`
		}{
			Schema:   "plystra.interface-shape/v1",
			ID:       value.ID(),
			Package:  value.PackagePath(),
			Method:   value.Method(),
			Request:  value.Request(),
			Response: value.Response(),
			Messages: legacyMessages,
		})
		if err != nil {
			t.Fatalf("Marshal(legacy Interface shape): %v", err)
		}
		legacyInterfaces[interfaceIndex].Digest = sha256Text(canonicalShape)
	}
	canonical, err := json.Marshal(struct {
		Schema     string                 `json:"schema"`
		Interfaces []legacyShapeInterface `json:"interfaces"`
	}{
		Schema:     "plystra.interface-shape-baseline/v1",
		Interfaces: legacyInterfaces,
	})
	if err != nil {
		t.Fatalf("Marshal(legacy shape canonical): %v", err)
	}
	legacyDigest := sha256Text(canonical)
	legacyShape, err := json.Marshal(struct {
		Schema     string                 `json:"schema"`
		Interfaces []legacyShapeInterface `json:"interfaces"`
		Digest     string                 `json:"digest"`
	}{
		Schema:     "plystra.interface-shape-baseline/v1",
		Interfaces: legacyInterfaces,
		Digest:     legacyDigest,
	})
	if err != nil {
		t.Fatalf("Marshal(legacy shape): %v", err)
	}
	writeFile(t, filepath.Join(root, filepath.FromSlash(interfacecompatibility.Path)), string(legacyShape))

	var ownership metadataOwnershipManifest
	if err := json.Unmarshal(readFile(t, root, generatedfiles.ManifestPath), &ownership); err != nil {
		t.Fatalf("Unmarshal(ownership manifest): %v", err)
	}
	foundFile := false
	foundInput := false
	const inputPrefix = "compatibility:interface-shape:"
	for fileIndex := range ownership.Files {
		file := &ownership.Files[fileIndex]
		if file.Path != interfacecompatibility.Path {
			continue
		}
		foundFile = true
		file.SHA256 = sha256Text(legacyShape)
		file.OutputKind = "compatibility-baseline"
		for inputIndex, input := range file.InputRecordIDs {
			if !strings.HasPrefix(input, inputPrefix) {
				continue
			}
			file.InputRecordIDs[inputIndex] = inputPrefix + legacyDigest
			foundInput = true
		}
	}
	if !foundFile || !foundInput {
		t.Fatalf("ownership manifest lacks shape evidence: file=%t input=%t", foundFile, foundInput)
	}
	legacyOwnership, err := json.MarshalIndent(ownership, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(ownership manifest): %v", err)
	}
	legacyOwnership = append(legacyOwnership, '\n')
	writeFile(t, filepath.Join(root, filepath.FromSlash(generatedfiles.ManifestPath)), string(legacyOwnership))
	owned, exists, err := generatedfiles.ReadOwnedFile(
		root,
		interfacecompatibility.Path,
		interfacecompatibility.MaximumBytes,
	)
	if err != nil || !exists || !bytes.Equal(owned, legacyShape) {
		t.Fatalf("ReadOwnedFile(legacy shape) = %t, %v", exists, err)
	}
	return legacyShape, legacyOwnership
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
