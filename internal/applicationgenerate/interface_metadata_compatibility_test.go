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

func TestGenerateComparesInterfaceMetadataByDeclaredCompatibilityClass(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		changed string
		class   interfacecompatibility.MetadataClass
		install bool
	}{
		{
			name:    "semantics",
			initial: "semantics:\n  kind: query\n",
			changed: "semantics:\n  kind: command\n",
			class:   interfacecompatibility.MetadataClassContract,
		},
		{
			name:    "semantic error codes",
			initial: "errors:\n  - code: rejected\n",
			changed: "errors:\n  - code: unavailable\n",
			class:   interfacecompatibility.MetadataClassContract,
		},
		{
			name:    "constraints",
			initial: "constraints:\n  request.name:\n    min_length: 1\n",
			changed: "constraints:\n  request.name:\n    min_length: 2\n",
			class:   interfacecompatibility.MetadataClassContract,
		},
		{
			name: "examples",
			initial: `examples:
  - name: accepted
    request:
      name: alpha
    response:
      accepted: true
`,
			changed: `examples:
  - name: accepted
    request:
      name: alpha
    response:
      accepted: false
`,
			class: interfacecompatibility.MetadataClassExamples,
		},
		{
			name:    "deprecation",
			initial: "deprecation:\n  message: Use the replacement Interface.\n",
			changed: "deprecation:\n  message: Use the stable replacement Interface.\n",
			class:   interfacecompatibility.MetadataClassDocumentation,
			install: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeApplicationModule(t, root, "example.com/interface-metadata-compatibility")
			writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			writeFile(t, filepath.Join(root, "interfaces", "records", "echo", "v1", "interface.go"), metadataCompatibilityInterfaceSource())
			metadataPath := filepath.Join(root, "interfaces", "records", "echo", "v1", "interface.yaml")
			writeFile(t, metadataPath, test.initial)

			options := applicationgenerate.Options{
				Start:       root,
				Environment: goEnvironment(nil),
				Validate:    func(context.Context, string) error { return nil },
			}
			initial, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil || !initial.Report().Clean() {
				t.Fatalf("Generate(initial) = changes %#v, %v", initial.Report().Changes(), err)
			}
			initialComparison := initial.InterfaceMetadataComparison()
			if !initialComparison.Valid() || initialComparison.Clean() {
				t.Fatalf("initial metadata comparison = %#v", initialComparison)
			}
			assertEvolutionVersionNeutral(t, initial)
			initialBaselineData := readFile(t, root, interfacecompatibility.MetadataPath)
			initialBaseline, err := interfacecompatibility.DecodeMetadata(initialBaselineData)
			if err != nil || !initialBaseline.Valid() || len(initialBaseline.Interfaces()) != 1 {
				t.Fatalf("DecodeMetadata(initial) = %#v, %v", initialBaseline, err)
			}
			for _, forbidden := range []string{
				filepath.ToSlash(root),
				"interface.yaml",
				"query",
				"command",
				"rejected",
				"unavailable",
				"alpha",
				"replacement Interface",
			} {
				if bytes.Contains(initialBaselineData, []byte(forbidden)) {
					t.Fatalf("metadata baseline contains forbidden value %q: %s", forbidden, initialBaselineData)
				}
			}

			writeFile(t, metadataPath, test.changed)
			options.Check = true
			before := snapshotTree(t, root)
			drift, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil ||
				!slicesContains(drift.Report().Stale(), interfacecompatibility.MetadataPath) ||
				!drift.InterfaceShapeComparison().Clean() ||
				drift.InterfaceMetadataComparison().Clean() ||
				!drift.InterfaceMetadataComparison().Valid() {
				t.Fatalf(
					"Generate --check(changed) = changes %#v shape %#v metadata %#v, %v",
					drift.Report().Changes(),
					drift.InterfaceShapeComparison().Changes(),
					drift.InterfaceMetadataComparison().Changes(),
					err,
				)
			}
			changes := drift.InterfaceMetadataComparison().Changes()
			if len(changes) != 1 ||
				changes[0].Kind() != interfacecompatibility.ChangeChanged ||
				changes[0].ID() != "records.echo/v1" ||
				!reflect.DeepEqual(changes[0].Classes(), []interfacecompatibility.MetadataClass{test.class}) {
				t.Fatalf("metadata changes = %#v", changes)
			}
			previousDigest, previousExists := changes[0].PreviousDigest(test.class)
			currentDigest, currentExists := changes[0].CurrentDigest(test.class)
			if !previousExists || !currentExists || previousDigest == currentDigest {
				t.Fatalf("class %s digests = %q, %t -> %q, %t", test.class, previousDigest, previousExists, currentDigest, currentExists)
			}
			if test.class == interfacecompatibility.MetadataClassContract {
				assertEvolutionVersionRequired(
					t,
					drift,
					"records.echo/v1",
					[]interfacecompatibility.VersionSurface{
						interfacecompatibility.VersionSurfaceContract,
					},
				)
			} else {
				assertEvolutionVersionNeutral(t, drift)
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("metadata compatibility check mutated the Project")
			}

			if !test.install {
				return
			}
			options.Check = false
			updated, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil || !updated.Report().Clean() || updated.InterfaceMetadataComparison().Clean() {
				t.Fatalf("Generate(updated) = changes %#v metadata %#v, %v", updated.Report().Changes(), updated.InterfaceMetadataComparison().Changes(), err)
			}
			assertEvolutionVersionNeutral(t, updated)
			updatedBaseline, err := interfacecompatibility.DecodeMetadata(readFile(t, root, interfacecompatibility.MetadataPath))
			if err != nil || updatedBaseline.Digest() == initialBaseline.Digest() {
				t.Fatalf("updated metadata baseline = digest %q, %v", updatedBaseline.Digest(), err)
			}
			options.Check = true
			clean, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil || !clean.Report().Clean() || !clean.InterfaceMetadataComparison().Clean() {
				t.Fatalf("Generate --check(clean) = changes %#v metadata %#v, %v", clean.Report().Changes(), clean.InterfaceMetadataComparison().Changes(), err)
			}
			assertEvolutionVersionNeutral(t, clean)
		})
	}
}

func TestGenerateRollsBackInterfaceMetadataBaselineAfterValidationFailure(t *testing.T) {
	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/interface-metadata-rollback")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(root, "interfaces", "records", "echo", "v1", "interface.go"), metadataCompatibilityInterfaceSource())
	metadataPath := filepath.Join(root, "interfaces", "records", "echo", "v1", "interface.yaml")
	writeFile(t, metadataPath, "semantics:\n  kind: query\n")

	options := applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(nil),
		Validate:    func(context.Context, string) error { return nil },
	}
	if _, err := applicationgenerate.Generate(t.Context(), options); err != nil {
		t.Fatalf("Generate(initial): %v", err)
	}
	writeFile(t, metadataPath, "semantics:\n  kind: command\n")
	before := snapshotTree(t, root)
	sentinel := errors.New("forced metadata post-install validation failure")
	options.Validate = func(context.Context, string) error { return sentinel }
	if result, err := applicationgenerate.Generate(t.Context(), options); !errors.Is(err, sentinel) ||
		!reflect.DeepEqual(snapshotTree(t, root), before) {
		t.Fatalf("Generate(rollback) = %#v, %v", result, err)
	}
}

func TestGenerateMigratesOwnedInterfaceMetadataV1Transactionally(t *testing.T) {
	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/interface-metadata-migration")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(root, "interfaces", "records", "echo", "v1", "interface.go"), metadataCompatibilityInterfaceSource())

	options := applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(nil),
		Validate:    func(context.Context, string) error { return nil },
	}
	if _, err := applicationgenerate.Generate(t.Context(), options); err != nil {
		t.Fatalf("Generate(initial): %v", err)
	}
	assertCompatibilityWorkingRecordKinds(t, root)
	legacyMetadata, legacyOwnership := replaceOwnedMetadataWithV1(t, root)

	options.Check = true
	beforeCheck := snapshotTree(t, root)
	checked, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil ||
		!checked.InterfaceMetadataComparison().Valid() ||
		!checked.InterfaceMetadataComparison().Clean() ||
		!slicesContains(checked.Report().Stale(), interfacecompatibility.MetadataPath) ||
		!slicesContains(checked.Report().Stale(), generatedfiles.ManifestPath) {
		t.Fatalf(
			"Generate --check(v1) = changes %#v metadata %#v, %v",
			checked.Report().Changes(),
			checked.InterfaceMetadataComparison().Changes(),
			err,
		)
	}
	assertEvolutionVersionNeutral(t, checked)
	if afterCheck := snapshotTree(t, root); !reflect.DeepEqual(afterCheck, beforeCheck) {
		t.Fatal("v1 metadata migration check mutated the Project")
	}

	options.Check = false
	sentinel := errors.New("forced v1 metadata migration validation failure")
	options.Validate = func(context.Context, string) error { return sentinel }
	beforeRollback := snapshotTree(t, root)
	if result, err := applicationgenerate.Generate(t.Context(), options); !errors.Is(err, sentinel) ||
		!reflect.DeepEqual(snapshotTree(t, root), beforeRollback) {
		t.Fatalf("Generate(v1 rollback) = %#v, %v", result, err)
	}
	if !bytes.Equal(readFile(t, root, interfacecompatibility.MetadataPath), legacyMetadata) ||
		!bytes.Equal(readFile(t, root, generatedfiles.ManifestPath), legacyOwnership) {
		t.Fatal("v1 metadata migration rollback did not restore metadata and ownership")
	}

	options.Validate = func(context.Context, string) error { return nil }
	migrated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !migrated.Report().Clean() || !migrated.InterfaceMetadataComparison().Clean() {
		t.Fatalf(
			"Generate(v1 migration) = changes %#v metadata %#v, %v",
			migrated.Report().Changes(),
			migrated.InterfaceMetadataComparison().Changes(),
			err,
		)
	}
	assertCompatibilityWorkingRecordKinds(t, root)
	assertEvolutionVersionNeutral(t, migrated)
	migratedMetadata := readFile(t, root, interfacecompatibility.MetadataPath)
	migratedOwnership := readFile(t, root, generatedfiles.ManifestPath)
	decoded, err := interfacecompatibility.DecodeMetadata(migratedMetadata)
	if err != nil || decoded.Schema() != interfacecompatibility.MetadataSchema ||
		bytes.Equal(migratedMetadata, legacyMetadata) || bytes.Equal(migratedOwnership, legacyOwnership) {
		t.Fatalf("migrated metadata = schema %q, error %v", decoded.Schema(), err)
	}
	owned, exists, err := generatedfiles.ReadOwnedFile(
		root,
		interfacecompatibility.MetadataPath,
		interfacecompatibility.MetadataMaximumBytes,
	)
	if err != nil || !exists || !bytes.Equal(owned, migratedMetadata) {
		t.Fatalf("ReadOwnedFile(migrated metadata) = %t, %v", exists, err)
	}

	options.Check = true
	clean, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !clean.Report().Clean() || !clean.InterfaceMetadataComparison().Clean() {
		t.Fatalf("Generate --check(migrated) = changes %#v, %v", clean.Report().Changes(), err)
	}
}

func TestGenerateRetainsAdditivePointerEvidenceWithoutWaivingStableVersioning(t *testing.T) {
	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/interface-pointer-addition")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	interfacePath := filepath.Join(root, "interfaces", "records", "echo", "v1", "interface.go")
	writeFile(t, interfacePath, pointerEvolutionInterfaceSource("string", false))

	options := applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(nil),
		Validate:    func(context.Context, string) error { return nil },
	}
	if _, err := applicationgenerate.Generate(t.Context(), options); err != nil {
		t.Fatalf("Generate(initial): %v", err)
	}
	writeFile(t, interfacePath, pointerEvolutionInterfaceSource("string", true))
	options.Check = true
	before := snapshotTree(t, root)
	result, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !result.InterfaceShapeComparison().Valid() || !result.InterfaceMetadataComparison().Valid() {
		t.Fatalf("Generate --check(pointer addition) = %#v, %v", result.Report().Changes(), err)
	}
	shapeChanges := result.InterfaceShapeComparison().Changes()
	metadataChanges := result.InterfaceMetadataComparison().Changes()
	if len(shapeChanges) != 1 || !shapeChanges[0].AdditivePointerFieldsOnly() ||
		len(metadataChanges) != 1 || !metadataChanges[0].ContractShapeOnly() {
		t.Fatalf("pointer addition changes = shape %#v metadata %#v", shapeChanges, metadataChanges)
	}
	assertEvolutionVersionRequired(
		t,
		result,
		"records.echo/v1",
		[]interfacecompatibility.VersionSurface{
			interfacecompatibility.VersionSurfaceGoShape,
			interfacecompatibility.VersionSurfaceContract,
		},
	)
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("pointer addition compatibility check mutated the Project")
	}
}

func TestGenerateClassifiesExistingPointerStateChangesAsStableVersionRequirements(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		changed string
	}{
		{name: "ordinary to pointer", initial: "string", changed: "*string"},
		{name: "pointer to nullable", initial: "*string", changed: "**string"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeApplicationModule(t, root, "example.com/interface-pointer-change")
			writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			interfacePath := filepath.Join(root, "interfaces", "records", "echo", "v1", "interface.go")
			writeFile(t, interfacePath, pointerEvolutionInterfaceSource(test.initial, false))
			options := applicationgenerate.Options{
				Start:       root,
				Environment: goEnvironment(nil),
				Validate:    func(context.Context, string) error { return nil },
			}
			if _, err := applicationgenerate.Generate(t.Context(), options); err != nil {
				t.Fatalf("Generate(initial): %v", err)
			}
			writeFile(t, interfacePath, pointerEvolutionInterfaceSource(test.changed, false))
			options.Check = true
			before := snapshotTree(t, root)
			result, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil {
				t.Fatalf("Generate --check(pointer state change): %v", err)
			}
			assertEvolutionVersionRequired(
				t,
				result,
				"records.echo/v1",
				[]interfacecompatibility.VersionSurface{
					interfacecompatibility.VersionSurfaceGoShape,
					interfacecompatibility.VersionSurfaceContract,
				},
			)
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatal("pointer state compatibility check mutated the Project")
			}
		})
	}
}

type metadataOwnershipManifest struct {
	Version             int                     `json:"version"`
	Files               []metadataOwnershipFile `json:"files"`
	ApplicationManifest json.RawMessage         `json:"application_manifest,omitempty"`
}

type metadataOwnershipFile struct {
	Path             string   `json:"path"`
	SHA256           string   `json:"sha256"`
	Generator        string   `json:"generator"`
	OutputKind       string   `json:"output_kind"`
	InputRecordIDs   []string `json:"input_record_ids"`
	Sources          []string `json:"sources"`
	CleanupOwnership string   `json:"cleanup_ownership"`
}

type legacyMetadataInterface struct {
	ID                  string `json:"id"`
	ContractDigest      string `json:"contract_digest"`
	DocumentationDigest string `json:"documentation_digest"`
	ExampleDigest       string `json:"example_digest"`
}

func replaceOwnedMetadataWithV1(t testing.TB, root string) ([]byte, []byte) {
	t.Helper()

	current, err := interfacecompatibility.DecodeMetadata(readFile(t, root, interfacecompatibility.MetadataPath))
	if err != nil || current.Schema() != interfacecompatibility.MetadataSchema {
		t.Fatalf("DecodeMetadata(current) = schema %q, %v", current.Schema(), err)
	}
	legacyInterfaces := make([]legacyMetadataInterface, len(current.Interfaces()))
	for index, value := range current.Interfaces() {
		legacyInterfaces[index] = legacyMetadataInterface{
			ID:                  value.ID(),
			ContractDigest:      value.ContractDigest(),
			DocumentationDigest: value.DocumentationDigest(),
			ExampleDigest:       value.ExampleDigest(),
		}
	}
	canonical, err := json.Marshal(struct {
		Schema     string                    `json:"schema"`
		Interfaces []legacyMetadataInterface `json:"interfaces"`
	}{
		Schema:     "plystra.interface-metadata-baseline/v1",
		Interfaces: legacyInterfaces,
	})
	if err != nil {
		t.Fatalf("Marshal(legacy metadata canonical): %v", err)
	}
	legacyDigest := sha256Text(canonical)
	legacyMetadata, err := json.Marshal(struct {
		Schema     string                    `json:"schema"`
		Interfaces []legacyMetadataInterface `json:"interfaces"`
		Digest     string                    `json:"digest"`
	}{
		Schema:     "plystra.interface-metadata-baseline/v1",
		Interfaces: legacyInterfaces,
		Digest:     legacyDigest,
	})
	if err != nil {
		t.Fatalf("Marshal(legacy metadata): %v", err)
	}
	writeFile(t, filepath.Join(root, filepath.FromSlash(interfacecompatibility.MetadataPath)), string(legacyMetadata))

	var ownership metadataOwnershipManifest
	if err := json.Unmarshal(readFile(t, root, generatedfiles.ManifestPath), &ownership); err != nil {
		t.Fatalf("Unmarshal(ownership manifest): %v", err)
	}
	foundFile := false
	foundInput := false
	const inputPrefix = "compatibility:interface-metadata:"
	for fileIndex := range ownership.Files {
		file := &ownership.Files[fileIndex]
		if file.Path != interfacecompatibility.MetadataPath {
			continue
		}
		foundFile = true
		file.SHA256 = sha256Text(legacyMetadata)
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
		t.Fatalf("ownership manifest lacks metadata evidence: file=%t input=%t", foundFile, foundInput)
	}
	legacyOwnership, err := json.MarshalIndent(ownership, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(ownership manifest): %v", err)
	}
	legacyOwnership = append(legacyOwnership, '\n')
	writeFile(t, filepath.Join(root, filepath.FromSlash(generatedfiles.ManifestPath)), string(legacyOwnership))
	owned, exists, err := generatedfiles.ReadOwnedFile(
		root,
		interfacecompatibility.MetadataPath,
		interfacecompatibility.MetadataMaximumBytes,
	)
	if err != nil || !exists || !bytes.Equal(owned, legacyMetadata) {
		t.Fatalf("ReadOwnedFile(legacy metadata) = %t, %v", exists, err)
	}
	return legacyMetadata, legacyOwnership
}

func assertCompatibilityWorkingRecordKinds(t testing.TB, root string) {
	t.Helper()
	var ownership metadataOwnershipManifest
	if err := json.Unmarshal(readFile(t, root, generatedfiles.ManifestPath), &ownership); err != nil {
		t.Fatalf("Unmarshal(ownership manifest): %v", err)
	}
	wanted := map[string]bool{
		interfacecompatibility.Path:              false,
		interfacecompatibility.MetadataPath:      false,
		interfacecompatibility.TransportPath:     false,
		interfacecompatibility.JavaScriptPath:    false,
		interfacecompatibility.DocumentationPath: false,
	}
	for _, file := range ownership.Files {
		if _, exists := wanted[file.Path]; !exists {
			continue
		}
		if file.OutputKind != "compatibility-working-record" {
			t.Fatalf("compatibility file %s output kind = %q", file.Path, file.OutputKind)
		}
		wanted[file.Path] = true
	}
	for filePath, found := range wanted {
		if !found {
			t.Fatalf("ownership manifest omits compatibility working record %s", filePath)
		}
	}
}

func pointerEvolutionInterfaceSource(valueType string, addOptionalPointer bool) string {
	extra := ""
	if addOptionalPointer {
		extra = "\n\tOptional *string `plystra:\"2\" json:\"optional\"`"
	}
	return `package echov1

import "context"

//plystra:interface records.echo/v1
type Interface interface {
	Echo(context.Context, Request) (Response, error)
}

type Request struct {
	Value ` + valueType + " `plystra:\"1,required\" json:\"value\"`" + extra + `
}

type Response struct{}
`
}

func metadataCompatibilityInterfaceSource() string {
	return `package echov1

import "context"

//plystra:interface records.echo/v1
type Interface interface {
	Echo(context.Context, Request) (Response, error)
}

type Request struct {
	Name string ` + "`" + `plystra:"1,required" json:"name"` + "`" + `
}

type Response struct {
	Accepted bool ` + "`" + `plystra:"1" json:"accepted"` + "`" + `
}
`
}
