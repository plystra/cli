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
