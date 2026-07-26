package applicationgenerate_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationgenerate"
)

func TestGenerateTreatsCompatibleImplementationReplacementAsWireNeutral(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/implementation-replacement"
	writeConnectApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), `interfaces:
  require: [records.list/v1]
  use:
    records.list/v1: example.com/implementation-replacement/smtp.New
http:
  expose: [records.list/v1]
`)
	overlayPath := filepath.Join(root, "plystra.test.yaml")
	writeFile(t, overlayPath, "# Test-only Implementation replacement.\n{}\n")
	writeFile(
		t,
		filepath.Join(root, "interfaces", "records", "list", "v1", "interface.go"),
		interfaceProtobufSource(7),
	)
	for _, implementation := range []string{"memory", "smtp"} {
		writeFile(
			t,
			filepath.Join(root, implementation, "service.go"),
			compatibleListImplementationSource(implementation, modulePath),
		)
	}

	options := applicationgenerate.Options{
		Start:           root,
		EnvironmentName: "test",
		Environment:     goEnvironment(nil),
		Validate:        func(context.Context, string) error { return nil },
	}
	initial, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !initial.Report().Clean() {
		t.Fatalf("Generate(initial) = changes %#v, %v", initial.Report().Changes(), err)
	}
	options.Check = true
	cleanBeforeReplacement, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !cleanBeforeReplacement.Report().Clean() {
		t.Fatalf(
			"Generate --check(initial) = changes %#v, %v",
			cleanBeforeReplacement.Report().Changes(),
			err,
		)
	}
	assertCleanInterfaceCompatibility(t, cleanBeforeReplacement)
	assessmentDigest := cleanBeforeReplacement.InterfaceEvolutionAssessment().Digest()

	for _, path := range []string{
		"generated/go/adapters/connect/records/list/v1/handler_gen.go",
		"generated/go/proxies/records/list/v1/proxy_gen.go",
		"generated/proto/plystra/generated/records/list/v1/interface.proto",
		"generated/sdk/javascript/src/interfaces/records/list/v1.ts",
	} {
		assertFileExists(t, root, path)
	}
	projectionsBefore := snapshotInterfaceProjections(t, root)
	assemblyPath := "generated/go/assembly/interfaces_gen.go"
	adapterPath := "generated/go/adapters/implementations/records/list/v1/adapter_gen.go"
	manifestPath := "generated/manifest.json"
	assemblyBefore := readFile(t, root, assemblyPath)
	adapterBefore := readFile(t, root, adapterPath)
	manifestBefore := readFile(t, root, manifestPath)
	provenanceBefore, err := applicationgen.DecodeManifestProvenance(manifestBefore)
	if err != nil ||
		provenanceBefore.Mode() != applicationgen.ConfigurationModeEnvironment ||
		provenanceBefore.Environment() != "test" ||
		provenanceBefore.SelectedPath() != "plystra.test.yaml" {
		t.Fatalf("initial manifest provenance = %#v, %v", provenanceBefore, err)
	}
	if !bytes.Contains(assemblyBefore, []byte(modulePath+"/smtp.New")) ||
		bytes.Contains(assemblyBefore, []byte(modulePath+"/memory.New")) ||
		!bytes.Contains(adapterBefore, []byte(modulePath+"/smtp.New")) ||
		bytes.Contains(adapterBefore, []byte(modulePath+"/memory.New")) {
		t.Fatalf(
			"initial selection is not confined to smtp:\nassembly:\n%s\nadapter:\n%s",
			assemblyBefore,
			adapterBefore,
		)
	}

	writeFile(t, overlayPath, `# Test-only Implementation replacement.
interfaces:
  use:
    records.list/v1: example.com/implementation-replacement/memory.New
`)
	beforeCheck := snapshotTree(t, root)
	replacementCheck, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil ||
		replacementCheck.Report().Clean() ||
		!slicesContains(replacementCheck.Report().Changed(), assemblyPath) ||
		!slicesContains(replacementCheck.Report().Changed(), adapterPath) ||
		!slicesContains(replacementCheck.Report().Changed(), manifestPath) {
		t.Fatalf(
			"Generate --check(replacement) = changed %#v missing %#v obsolete %#v, %v",
			replacementCheck.Report().Changed(),
			replacementCheck.Report().Missing(),
			replacementCheck.Report().Obsolete(),
			err,
		)
	}
	assertCleanInterfaceCompatibility(t, replacementCheck)
	if replacementCheck.InterfaceEvolutionAssessment().Digest() != assessmentDigest {
		t.Fatalf(
			"wire-neutral assessment digest = %q, want %q",
			replacementCheck.InterfaceEvolutionAssessment().Digest(),
			assessmentDigest,
		)
	}
	assertNoInterfaceProjectionReportDrift(t, replacementCheck)
	if afterCheck := snapshotTree(t, root); !reflect.DeepEqual(afterCheck, beforeCheck) {
		t.Fatal("wire-neutral replacement check mutated the Project")
	}

	options.Check = false
	updated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !updated.Report().Clean() {
		t.Fatalf("Generate(replacement) = changes %#v, %v", updated.Report().Changes(), err)
	}
	assertCleanInterfaceCompatibility(t, updated)
	if updated.InterfaceEvolutionAssessment().Digest() != assessmentDigest {
		t.Fatalf(
			"installed wire-neutral assessment digest = %q, want %q",
			updated.InterfaceEvolutionAssessment().Digest(),
			assessmentDigest,
		)
	}
	if projectionsAfter := snapshotInterfaceProjections(t, root); !reflect.DeepEqual(projectionsAfter, projectionsBefore) {
		t.Fatalf(
			"compatible Implementation replacement changed Interface projections:\nbefore: %#v\nafter:  %#v",
			projectionsBefore,
			projectionsAfter,
		)
	}

	assemblyAfter := readFile(t, root, assemblyPath)
	adapterAfter := readFile(t, root, adapterPath)
	manifestAfter := readFile(t, root, manifestPath)
	if bytes.Equal(assemblyAfter, assemblyBefore) ||
		!bytes.Contains(assemblyAfter, []byte(modulePath+"/memory.New")) ||
		bytes.Contains(assemblyAfter, []byte(modulePath+"/smtp.New")) ||
		bytes.Equal(adapterAfter, adapterBefore) ||
		!bytes.Contains(adapterAfter, []byte(modulePath+"/memory.New")) ||
		bytes.Contains(adapterAfter, []byte(modulePath+"/smtp.New")) {
		t.Fatalf(
			"replacement did not update only selected assembly inputs:\nassembly:\n%s\nadapter:\n%s",
			assemblyAfter,
			adapterAfter,
		)
	}
	if bytes.Equal(manifestAfter, manifestBefore) {
		t.Fatal("compatible Implementation replacement did not update build provenance")
	}
	provenanceAfter, err := applicationgen.DecodeManifestProvenance(manifestAfter)
	if err != nil ||
		provenanceAfter.Mode() != provenanceBefore.Mode() ||
		provenanceAfter.Environment() != provenanceBefore.Environment() ||
		provenanceAfter.SelectedPath() != provenanceBefore.SelectedPath() ||
		provenanceAfter.ApplicationModelDigest() == provenanceBefore.ApplicationModelDigest() ||
		provenanceAfter.ProtobufWireMapDigest() != provenanceBefore.ProtobufWireMapDigest() {
		t.Fatalf(
			"replacement provenance = before %#v after %#v, %v",
			provenanceBefore,
			provenanceAfter,
			err,
		)
	}

	options.Check = true
	cleanAfterReplacement, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !cleanAfterReplacement.Report().Clean() {
		t.Fatalf(
			"Generate --check(replacement clean) = changes %#v, %v",
			cleanAfterReplacement.Report().Changes(),
			err,
		)
	}
	assertCleanInterfaceCompatibility(t, cleanAfterReplacement)
	if cleanAfterReplacement.InterfaceEvolutionAssessment().Digest() != assessmentDigest {
		t.Fatalf(
			"clean replacement assessment digest = %q, want %q",
			cleanAfterReplacement.InterfaceEvolutionAssessment().Digest(),
			assessmentDigest,
		)
	}
	assertNoTransactions(t, root)
}

func compatibleListImplementationSource(packageName, modulePath string) string {
	return fmt.Sprintf(`package %s

import (
	"context"

	listv1 "%s/interfaces/records/list/v1"
)

type Service struct{}

//plystra:implements records.list/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) List(context.Context, listv1.Request) (listv1.Response, error) {
	return listv1.Response{}, nil
}
`, packageName, modulePath)
}

func assertCleanInterfaceCompatibility(t testing.TB, result applicationgenerate.Result) {
	t.Helper()
	if !result.InterfaceShapeComparison().Valid() ||
		!result.InterfaceShapeComparison().Clean() ||
		!result.InterfaceMetadataComparison().Valid() ||
		!result.InterfaceMetadataComparison().Clean() ||
		!result.InterfaceTransportComparison().Valid() ||
		!result.InterfaceTransportComparison().Clean() ||
		!result.InterfaceJavaScriptComparison().Valid() ||
		!result.InterfaceJavaScriptComparison().Clean() ||
		!result.InterfaceDocumentationComparison().Valid() ||
		!result.InterfaceDocumentationComparison().Clean() {
		t.Fatalf(
			"Interface compatibility changed: shape %#v metadata %#v transport %#v JavaScript %#v documentation %#v",
			result.InterfaceShapeComparison().Changes(),
			result.InterfaceMetadataComparison().Changes(),
			result.InterfaceTransportComparison().Changes(),
			result.InterfaceJavaScriptComparison().Changes(),
			result.InterfaceDocumentationComparison().Changes(),
		)
	}
	assertEvolutionVersionNeutral(t, result)
}

func snapshotInterfaceProjections(t testing.TB, root string) []treeEntry {
	t.Helper()
	prefixes := []string{
		"generated/compatibility",
		"generated/docs",
		"generated/go/adapters/connect",
		"generated/go/adapters/http",
		"generated/go/clients",
		"generated/go/contracts",
		"generated/go/internal/connectschema",
		"generated/go/internal/invocationcontext",
		"generated/go/invocation",
		"generated/go/proxies",
		"generated/proto",
		"generated/sdk",
	}
	var result []treeEntry
	for _, prefix := range prefixes {
		result = append(result, snapshotSubtree(t, root, prefix)...)
	}
	return result
}

func assertNoInterfaceProjectionReportDrift(
	t testing.TB,
	result applicationgenerate.Result,
) {
	t.Helper()
	for _, path := range append(
		append(
			append([]string(nil), result.Report().Changed()...),
			result.Report().Missing()...,
		),
		result.Report().Obsolete()...,
	) {
		for _, prefix := range []string{
			"generated/compatibility/",
			"generated/docs/",
			"generated/go/adapters/connect/",
			"generated/go/adapters/http/",
			"generated/go/clients/",
			"generated/go/contracts/",
			"generated/go/internal/connectschema/",
			"generated/go/internal/invocationcontext/",
			"generated/go/invocation/",
			"generated/go/proxies/",
			"generated/proto/",
			"generated/sdk/",
		} {
			if strings.HasPrefix(path, prefix) {
				t.Fatalf("compatible Implementation replacement reported Interface projection drift at %s", path)
			}
		}
	}
}
