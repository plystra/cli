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
	assertCanonicalInterfaceArtifactTaxonomy(t, root)
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
	beforeInterfaceProvenance := provenanceBefore.InterfaceProvenance()
	if !beforeInterfaceProvenance.Valid() ||
		len(beforeInterfaceProvenance.Interfaces()) != 1 ||
		beforeInterfaceProvenance.Interfaces()[0].ID() != "records.list/v1" ||
		len(beforeInterfaceProvenance.Bindings()) != 1 ||
		beforeInterfaceProvenance.Bindings()[0].Selection().Constructor() != modulePath+"/smtp.New" ||
		len(beforeInterfaceProvenance.Constructors()) != 1 ||
		beforeInterfaceProvenance.Constructors()[0].Symbol() != modulePath+"/smtp.New" ||
		len(beforeInterfaceProvenance.Intrinsics()) != 2 {
		t.Fatalf("initial Interface and constructor provenance = %#v", beforeInterfaceProvenance)
	}
	beforeBinding := beforeInterfaceProvenance.Bindings()[0]
	assertExposedMapping(t, beforeBinding.Mappings(), "records/list/v1", "/plystra.generated.records.list.v1.RecordsListV1Service/Invoke", true)
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
		!slicesContains(replacementCheck.Report().Stale(), assemblyPath) ||
		!slicesContains(replacementCheck.Report().Stale(), adapterPath) ||
		!slicesContains(replacementCheck.Report().Stale(), manifestPath) {
		t.Fatalf(
			"Generate --check(replacement) = stale %#v missing %#v manually modified %#v, %v",
			replacementCheck.Report().Stale(),
			replacementCheck.Report().Missing(),
			replacementCheck.Report().ManuallyModified(),
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
	afterInterfaceProvenance := provenanceAfter.InterfaceProvenance()
	if !afterInterfaceProvenance.Valid() ||
		!reflect.DeepEqual(beforeInterfaceProvenance.Interfaces(), afterInterfaceProvenance.Interfaces()) ||
		!reflect.DeepEqual(beforeInterfaceProvenance.Intrinsics(), afterInterfaceProvenance.Intrinsics()) ||
		len(afterInterfaceProvenance.Bindings()) != 1 ||
		len(afterInterfaceProvenance.Constructors()) != 1 {
		t.Fatalf("compatible replacement changed Interface identities or intrinsic separation:\nbefore: %#v\nafter: %#v", beforeInterfaceProvenance, afterInterfaceProvenance)
	}
	afterBinding := afterInterfaceProvenance.Bindings()[0]
	if afterBinding.InterfaceID() != beforeBinding.InterfaceID() ||
		afterBinding.Selection().Constructor() != modulePath+"/memory.New" ||
		afterBinding.Selection().Reason() != beforeBinding.Selection().Reason() ||
		!reflect.DeepEqual(afterBinding.RootSources(), beforeBinding.RootSources()) ||
		!reflect.DeepEqual(afterBinding.ExposureSources(), beforeBinding.ExposureSources()) ||
		!reflect.DeepEqual(afterBinding.Policy(), beforeBinding.Policy()) ||
		!reflect.DeepEqual(afterBinding.Mappings(), beforeBinding.Mappings()) {
		t.Fatalf("compatible replacement changed wire or source provenance:\nbefore: %#v\nafter: %#v", beforeBinding, afterBinding)
	}
	if afterInterfaceProvenance.Constructors()[0].Symbol() != modulePath+"/memory.New" ||
		afterInterfaceProvenance.Constructors()[0].ConstructionOrder() != 1 {
		t.Fatalf("compatible replacement constructor provenance = %#v", afterInterfaceProvenance.Constructors())
	}
	assertExposedMapping(t, afterBinding.Mappings(), "records/list/v1", "/plystra.generated.records.list.v1.RecordsListV1Service/Invoke", true)

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
	for _, change := range result.Report().Changes() {
		path := change.Path()
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

func assertCanonicalInterfaceArtifactTaxonomy(t testing.TB, root string) {
	t.Helper()
	required := map[string]bool{
		"proxy":     false,
		"adapter":   false,
		"assembly":  false,
		"bootstrap": false,
		"transport": false,
		"sdk":       false,
		"manifest":  false,
	}
	for _, entry := range snapshotGenerated(t, root) {
		if !entry.mode.IsRegular() {
			continue
		}
		class := canonicalInterfaceArtifactClass(entry.path)
		if class == "" {
			t.Fatalf("canonical Interface generation emitted unclassified artifact %s", entry.path)
		}
		if _, exists := required[class]; exists {
			required[class] = true
		}
	}
	for class, seen := range required {
		if !seen {
			t.Fatalf("canonical Interface generation emitted no %s artifact", class)
		}
	}
}

func canonicalInterfaceArtifactClass(path string) string {
	switch {
	case path == "generated/.plystra-manifest.json",
		path == "generated/manifest.json",
		strings.HasPrefix(path, "generated/compatibility/"):
		return "manifest"
	case strings.HasPrefix(path, "generated/go/proxies/"):
		return "proxy"
	case strings.HasPrefix(path, "generated/go/adapters/"):
		return "adapter"
	case strings.HasPrefix(path, "generated/go/assembly/"):
		return "assembly"
	case strings.HasPrefix(path, "generated/go/application/"),
		strings.HasPrefix(path, "generated/go/bootstrap/"),
		strings.HasPrefix(path, "generated/go/configuration/"):
		return "bootstrap"
	case strings.HasPrefix(path, "generated/go/internal/connectschema/"),
		strings.HasPrefix(path, "generated/go/internal/invocationcontext/"),
		strings.HasPrefix(path, "generated/proto/"):
		return "transport"
	case strings.HasPrefix(path, "generated/sdk/"):
		return "sdk"
	case strings.HasPrefix(path, "generated/docs/"):
		return "documentation"
	default:
		return ""
	}
}
