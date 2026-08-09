package applicationgenerate_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/apidocgen"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/interfacecompatibility"
)

func TestGenerateComparesGeneratedDocumentation(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/interface-documentation-compatibility"
	writeConnectApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  expose:
    - email.send/v1
`)
	writePlugin(t, root, "business", "id: acme.business\nprovides: [email.send/v1]\n")
	initialContract := `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`
	writeCapability(t, root, "business", "email.send/v1", initialContract)
	contractPath := filepath.Join(
		root,
		"business",
		"capabilities",
		"email.send",
		"v1",
		"capability.yaml",
	)

	options := applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(nil),
		Validate:    func(context.Context, string) error { return nil },
	}
	initial, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !initial.Report().Clean() {
		t.Fatalf("Generate(initial) = changes %#v, %v", initial.Report().Changes(), err)
	}
	initialComparison := initial.InterfaceDocumentationComparison()
	if !initialComparison.Valid() ||
		initialComparison.Clean() ||
		len(initialComparison.Changes()) != 2 {
		t.Fatalf("initial documentation comparison = %#v", initialComparison)
	}
	for index, change := range initialComparison.Changes() {
		if change.Kind() != interfacecompatibility.ChangeAdded ||
			!reflect.DeepEqual(
				change.Classes(),
				[]interfacecompatibility.DocumentationClass{
					interfacecompatibility.DocumentationClassKind,
					interfacecompatibility.DocumentationClassContent,
				},
			) {
			t.Fatalf("initial documentation change[%d] = %#v", index, change)
		}
	}
	initialBaselineData := readFile(t, root, interfacecompatibility.DocumentationPath)
	initialBaseline, err := interfacecompatibility.DecodeDocumentation(initialBaselineData)
	if err != nil || !initialBaseline.Valid() || len(initialBaseline.Artifacts()) != 2 {
		t.Fatalf("DecodeDocumentation(initial) = %#v, %v", initialBaseline, err)
	}
	if initialBaseline.Artifacts()[0].Path() != apidocgen.InterfaceReferencePath ||
		initialBaseline.Artifacts()[1].Path() != apidocgen.OpenAPIPath {
		t.Fatalf("initial documentation artifacts = %#v", initialBaseline.Artifacts())
	}
	for _, forbidden := range []string{
		filepath.ToSlash(root),
		modulePath,
		"acme.business",
		"email.send/v1",
		"invalid_recipient",
		"secret",
		"config",
	} {
		if bytes.Contains(bytes.ToLower(initialBaselineData), bytes.ToLower([]byte(forbidden))) {
			t.Fatalf("documentation baseline contains forbidden value %q: %s", forbidden, initialBaselineData)
		}
	}

	changedContract := strings.Replace(initialContract, "to:", "recipient:", 1)
	writeFile(t, contractPath, withQuerySemantics(changedContract))
	options.Check = true
	beforeCheck := snapshotTree(t, root)
	drift, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil ||
		!slicesContains(drift.Report().Stale(), interfacecompatibility.DocumentationPath) ||
		!drift.InterfaceDocumentationComparison().Valid() ||
		drift.InterfaceDocumentationComparison().Clean() {
		t.Fatalf(
			"Generate --check(changed) = changes %#v documentation %#v, %v",
			drift.Report().Changes(),
			drift.InterfaceDocumentationComparison().Changes(),
			err,
		)
	}
	documentationChanges := drift.InterfaceDocumentationComparison().Changes()
	if len(documentationChanges) != 2 {
		t.Fatalf("documentation changes = %#v", documentationChanges)
	}
	for index, change := range documentationChanges {
		if change.Kind() != interfacecompatibility.ChangeChanged ||
			!reflect.DeepEqual(
				change.Classes(),
				[]interfacecompatibility.DocumentationClass{
					interfacecompatibility.DocumentationClassContent,
				},
			) {
			t.Fatalf("documentation change[%d] = %#v", index, change)
		}
	}
	if afterCheck := snapshotTree(t, root); !reflect.DeepEqual(afterCheck, beforeCheck) {
		t.Fatal("documentation compatibility check mutated the Project")
	}

	options.Check = false
	sentinel := errors.New("forced documentation post-install validation failure")
	options.Validate = func(context.Context, string) error { return sentinel }
	beforeRollback := snapshotTree(t, root)
	if result, err := applicationgenerate.Generate(t.Context(), options); !errors.Is(err, sentinel) ||
		!reflect.DeepEqual(snapshotTree(t, root), beforeRollback) {
		t.Fatalf("Generate(rollback) = %#v, %v", result, err)
	}

	options.Validate = func(context.Context, string) error { return nil }
	updated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil ||
		!updated.Report().Clean() ||
		updated.InterfaceDocumentationComparison().Clean() {
		t.Fatalf(
			"Generate(updated) = changes %#v documentation %#v, %v",
			updated.Report().Changes(),
			updated.InterfaceDocumentationComparison().Changes(),
			err,
		)
	}
	updatedBaseline, err := interfacecompatibility.DecodeDocumentation(
		readFile(t, root, interfacecompatibility.DocumentationPath),
	)
	if err != nil || updatedBaseline.Digest() == initialBaseline.Digest() {
		t.Fatalf("updated documentation baseline = digest %q, %v", updatedBaseline.Digest(), err)
	}

	options.Check = true
	clean, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil ||
		!clean.Report().Clean() ||
		!clean.InterfaceDocumentationComparison().Clean() {
		t.Fatalf(
			"Generate --check(clean) = changes %#v documentation %#v, %v",
			clean.Report().Changes(),
			clean.InterfaceDocumentationComparison().Changes(),
			err,
		)
	}

	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	beforeRemovalCheck := snapshotTree(t, root)
	removed, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil ||
		!slicesContains(removed.Report().Stale(), interfacecompatibility.DocumentationPath) ||
		len(removed.InterfaceDocumentationComparison().Changes()) != 2 {
		t.Fatalf(
			"Generate --check(removed) = changes %#v documentation %#v, %v",
			removed.Report().Changes(),
			removed.InterfaceDocumentationComparison().Changes(),
			err,
		)
	}
	for index, change := range removed.InterfaceDocumentationComparison().Changes() {
		if change.Kind() != interfacecompatibility.ChangeRemoved {
			t.Fatalf("removed documentation change[%d] = %#v", index, change)
		}
	}
	if afterRemovalCheck := snapshotTree(t, root); !reflect.DeepEqual(afterRemovalCheck, beforeRemovalCheck) {
		t.Fatal("documentation removal check mutated the Project")
	}

	options.Check = false
	removedInstall, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil ||
		!removedInstall.Report().Clean() ||
		len(removedInstall.InterfaceDocumentationComparison().Changes()) != 2 {
		t.Fatalf(
			"Generate(removed) = changes %#v documentation %#v, %v",
			removedInstall.Report().Changes(),
			removedInstall.InterfaceDocumentationComparison().Changes(),
			err,
		)
	}
	emptyBaseline, err := interfacecompatibility.DecodeDocumentation(
		readFile(t, root, interfacecompatibility.DocumentationPath),
	)
	if err != nil || len(emptyBaseline.Artifacts()) != 0 {
		t.Fatalf("removed documentation baseline = %#v, %v", emptyBaseline.Artifacts(), err)
	}
	for _, path := range []string{
		apidocgen.InterfaceReferencePath,
		apidocgen.OpenAPIPath,
	} {
		assertFileMissing(t, root, path)
	}

	options.Check = true
	clean, err = applicationgenerate.Generate(t.Context(), options)
	if err != nil ||
		!clean.Report().Clean() ||
		!clean.InterfaceDocumentationComparison().Clean() {
		t.Fatalf(
			"Generate --check(clean empty) = changes %#v documentation %#v, %v",
			clean.Report().Changes(),
			clean.InterfaceDocumentationComparison().Changes(),
			err,
		)
	}

	writeFile(t, filepath.Join(root, interfacecompatibility.DocumentationPath), "{}")
	tampered := snapshotTree(t, root)
	if result, err := applicationgenerate.Generate(t.Context(), options); err == nil ||
		!strings.Contains(err.Error(), "documentation compatibility baseline") ||
		!reflect.DeepEqual(snapshotTree(t, root), tampered) {
		t.Fatalf("Generate --check(tampered history) = %#v, %v", result, err)
	}
}
