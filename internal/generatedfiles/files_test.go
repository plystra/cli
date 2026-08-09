package generatedfiles_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/generatedfiles"
)

func TestNewOutputRendersDeterministicOwnershipManifest(t *testing.T) {
	t.Parallel()

	firstData := []byte("first\n")
	secondData := []byte("second\n")
	first := managedFile(t, "generated/z/second.txt", secondData)
	second := managedFile(t, "generated/a/first.txt", firstData)
	output, err := generatedfiles.NewOutput([]generatedfiles.File{first, second})
	if err != nil {
		t.Fatalf("NewOutput: %v", err)
	}
	wantManifest := fmt.Sprintf(`{
  "version": 3,
  "files": [
    {
      "path": "generated/a/first.txt",
      "sha256": %q,
      "generator": "plystra.test-generator/v1",
      "output_kind": "go-source",
      "input_record_ids": [
        "test:generated/a/first.txt"
      ],
      "sources": [
        "test fixture"
      ],
      "cleanup_ownership": "cli-owned"
    },
    {
      "path": "generated/z/second.txt",
      "sha256": %q,
      "generator": "plystra.test-generator/v1",
      "output_kind": "go-source",
      "input_record_ids": [
        "test:generated/z/second.txt"
      ],
      "sources": [
        "test fixture"
      ],
      "cleanup_ownership": "cli-owned"
    }
  ]
}
`, testDigest(firstData), testDigest(secondData))
	if got := string(output.ManifestJSON()); got != wantManifest {
		t.Fatalf("ManifestJSON =\n%s\nwant:\n%s", got, wantManifest)
	}
	files := output.Files()
	if got := []string{files[0].Path(), files[1].Path()}; !slices.Equal(got, []string{"generated/a/first.txt", "generated/z/second.txt"}) {
		t.Fatalf("Files paths = %v", got)
	}

	firstData[0] = 'x'
	secondData[0] = 'x'
	returned := files[0].Data()
	returned[0] = 'x'
	manifest := output.ManifestJSON()
	manifest[0] = 'x'
	if string(output.Files()[0].Data()) != "first\n" || string(output.ManifestJSON()) != wantManifest {
		t.Fatal("managed output exposed mutable file or manifest storage")
	}

	repeated, err := generatedfiles.NewOutput([]generatedfiles.File{
		managedFile(t, "generated/a/first.txt", []byte("first\n")),
		managedFile(t, "generated/z/second.txt", []byte("second\n")),
	})
	if err != nil || !bytes.Equal(repeated.ManifestJSON(), output.ManifestJSON()) {
		t.Fatalf("repeated NewOutput = %v, %v", repeated.ManifestJSON(), err)
	}
}

func TestNewFileNormalizesAndDefensivelyCopiesArtifactProvenance(t *testing.T) {
	t.Parallel()

	input := generatedfiles.ArtifactInput{
		Generator:      "plystra.test-generator/v7",
		Kind:           generatedfiles.ArtifactKindDocumentation,
		InputRecordIDs: []string{"record:z", "record:a"},
		Sources:        []string{"z/source.go:9:1", "a/source.go:3:2"},
	}
	file, err := generatedfiles.NewFile("generated/docs/reference.md", []byte("reference\n"), input)
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	input.InputRecordIDs[0] = "mutated"
	input.Sources[0] = "mutated"

	artifact := file.Artifact()
	if !artifact.Valid() ||
		artifact.Path() != "generated/docs/reference.md" ||
		artifact.SHA256() != testDigest([]byte("reference\n")) ||
		artifact.Generator() != "plystra.test-generator/v7" ||
		artifact.Kind() != generatedfiles.ArtifactKindDocumentation ||
		artifact.CleanupOwnership() != generatedfiles.CleanupOwnershipCLI ||
		!slices.Equal(artifact.InputRecordIDs(), []string{"record:a", "record:z"}) ||
		!slices.Equal(artifact.Sources(), []string{"a/source.go:3:2", "z/source.go:9:1"}) {
		t.Fatalf("Artifact = %#v", artifact)
	}
	inputs := artifact.InputRecordIDs()
	sources := artifact.Sources()
	inputs[0] = "mutated"
	sources[0] = "mutated"
	if file.Artifact().InputRecordIDs()[0] == "mutated" || file.Artifact().Sources()[0] == "mutated" {
		t.Fatal("Artifact accessors exposed mutable storage")
	}

	output, err := generatedfiles.NewOutput([]generatedfiles.File{file})
	if err != nil {
		t.Fatalf("NewOutput: %v", err)
	}
	artifacts := output.Artifacts()
	if len(artifacts) != 2 || artifacts[0].Path() != generatedfiles.ManifestPath || !artifacts[0].Valid() || artifacts[1].Path() != file.Path() {
		t.Fatalf("Output.Artifacts = %#v", artifacts)
	}
	artifacts[1] = generatedfiles.Artifact{}
	if !output.Artifacts()[1].Valid() {
		t.Fatal("Output.Artifacts exposed mutable storage")
	}
}

func TestNewFileRejectsIncompleteOrNoncanonicalArtifactProvenance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input generatedfiles.ArtifactInput
	}{
		{name: "missing generator", input: generatedfiles.ArtifactInput{Kind: generatedfiles.ArtifactKindGoSource, InputRecordIDs: []string{"input"}, Sources: []string{"source"}}},
		{name: "invalid generator", input: generatedfiles.ArtifactInput{Generator: "test/v1", Kind: generatedfiles.ArtifactKindGoSource, InputRecordIDs: []string{"input"}, Sources: []string{"source"}}},
		{name: "missing output kind", input: generatedfiles.ArtifactInput{Generator: "plystra.test/v1", InputRecordIDs: []string{"input"}, Sources: []string{"source"}}},
		{name: "missing inputs", input: generatedfiles.ArtifactInput{Generator: "plystra.test/v1", Kind: generatedfiles.ArtifactKindGoSource, Sources: []string{"source"}}},
		{name: "duplicate inputs", input: generatedfiles.ArtifactInput{Generator: "plystra.test/v1", Kind: generatedfiles.ArtifactKindGoSource, InputRecordIDs: []string{"input", "input"}, Sources: []string{"source"}}},
		{name: "missing sources", input: generatedfiles.ArtifactInput{Generator: "plystra.test/v1", Kind: generatedfiles.ArtifactKindGoSource, InputRecordIDs: []string{"input"}}},
		{name: "control source", input: generatedfiles.ArtifactInput{Generator: "plystra.test/v1", Kind: generatedfiles.ArtifactKindGoSource, InputRecordIDs: []string{"input"}, Sources: []string{"source\nsecret"}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := generatedfiles.NewFile("generated/test.go", []byte("package generated\n"), test.input); !errors.Is(err, generatedfiles.ErrOutput) {
				t.Fatalf("NewFile error = %v", err)
			}
		})
	}
}

func TestNewOutputRequiresCanonicalApplicationManifestAndLinksSnapshot(t *testing.T) {
	t.Parallel()

	pretty := []byte("{\n  \"configuration\": {\"version\": 1}\n}\n")
	prettyFile, err := generatedfiles.NewFile(generatedfiles.ApplicationManifestPath, pretty, testArtifactInput(generatedfiles.ApplicationManifestPath))
	if err != nil {
		t.Fatalf("NewFile(pretty application manifest): %v", err)
	}
	if _, err := generatedfiles.NewOutput([]generatedfiles.File{prettyFile}); !errors.Is(err, generatedfiles.ErrOutput) || !strings.Contains(err.Error(), "canonical compact JSON") {
		t.Fatalf("NewOutput(pretty application manifest) error = %v", err)
	}

	applicationManifest := []byte("{\"configuration\":{\"version\":1}}\n")
	file := managedFile(t, generatedfiles.ApplicationManifestPath, applicationManifest)
	output, err := generatedfiles.NewOutput([]generatedfiles.File{file})
	if err != nil {
		t.Fatalf("NewOutput: %v", err)
	}
	root := t.TempDir()
	writeOutput(t, root, output)
	artifact, exists, err := generatedfiles.ReadArtifact(root, generatedfiles.ApplicationManifestPath)
	if err != nil || !exists || artifact.SHA256() != testDigest(applicationManifest) {
		t.Fatalf("ReadArtifact(application manifest) = %#v, %t, %v", artifact, exists, err)
	}

	var ownership struct {
		ApplicationManifest json.RawMessage `json:"application_manifest"`
	}
	if err := json.Unmarshal(output.ManifestJSON(), &ownership); err != nil || !bytes.Equal(compactJSON(t, ownership.ApplicationManifest), compactJSON(t, applicationManifest)) {
		t.Fatalf("application_manifest snapshot = %s, %v", ownership.ApplicationManifest, err)
	}

	corrupt := mutateOwnershipManifest(t, output.ManifestJSON(), func(document map[string]any) {
		document["application_manifest"] = map[string]any{"configuration": map[string]any{"version": float64(2)}}
	})
	writeFileBytes(t, root, generatedfiles.ManifestPath, corrupt)
	if _, _, err := generatedfiles.ReadArtifact(root, generatedfiles.ApplicationManifestPath); !errors.Is(err, generatedfiles.ErrManifest) || !strings.Contains(err.Error(), "snapshot digest") {
		t.Fatalf("ReadArtifact(corrupt snapshot) error = %v", err)
	}
}

func TestNewOutputRejectsUnsafeIgnoredAndDuplicatePaths(t *testing.T) {
	t.Parallel()

	invalidPaths := []string{
		"",
		".",
		"README.md",
		"generated",
		"/generated/file.go",
		"generated/../outside.go",
		`generated\file.go`,
		generatedfiles.ManifestPath,
		"generated/sdk/javascript/dist/index.js",
		"generated/sdk/javascript/node_modules/package/index.js",
	}
	for _, filePath := range invalidPaths {
		filePath := filePath
		t.Run(strings.ReplaceAll(filePath, "/", "_"), func(t *testing.T) {
			t.Parallel()
			if _, err := generatedfiles.NewFile(filePath, []byte("data"), testArtifactInput(filePath)); !errors.Is(err, generatedfiles.ErrOutput) || !errors.Is(err, generatedfiles.ErrPath) {
				t.Fatalf("NewFile(%q) error = %v", filePath, err)
			}
		})
	}
	if _, err := generatedfiles.NewOutput([]generatedfiles.File{{}}); !errors.Is(err, generatedfiles.ErrOutput) || !errors.Is(err, generatedfiles.ErrPath) {
		t.Fatalf("NewOutput(zero File) error = %v", err)
	}
	duplicate := managedFile(t, "generated/same.txt", []byte("same"))
	if _, err := generatedfiles.NewOutput([]generatedfiles.File{duplicate, duplicate}); !errors.Is(err, generatedfiles.ErrOutput) {
		t.Fatalf("NewOutput(duplicate) error = %v", err)
	}
}

func TestCheckReportsStaleMissingUnexpectedAndManuallyModifiedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	oldOutput := managedOutput(t,
		"generated/docs/same.md", "same",
		"generated/go/modified-retired.go", "generated before",
		"generated/go/modified-retained.go", "retained before",
		"generated/go/obsolete.go", "obsolete",
		"generated/go/shared.go", "before",
	)
	writeOutput(t, root, oldOutput)
	writeFile(t, root, "generated/go/modified-retired.go", "user edit")
	writeFile(t, root, "generated/go/modified-retained.go", "retained user edit")
	writeFile(t, root, "generated/go/unowned-conflict.go", "unowned conflict")
	writeFile(t, root, "generated/unowned.txt", "keep")
	before := snapshotFiles(t, root)

	newOutput := managedOutput(t,
		"generated/docs/missing.md", "missing",
		"generated/docs/same.md", "same",
		"generated/go/modified-retained.go", "retained after",
		"generated/go/shared.go", "after",
		"generated/go/unowned-conflict.go", "desired",
	)
	report, err := generatedfiles.Check(root, newOutput)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	assertPaths(t, "stale", report.Stale(), []string{
		generatedfiles.ManifestPath,
		"generated/go/obsolete.go",
		"generated/go/shared.go",
	})
	assertPaths(t, "missing", report.Missing(), []string{"generated/docs/missing.md"})
	assertPaths(t, "unexpected", report.Unexpected(), []string{
		"generated/go/unowned-conflict.go",
		"generated/unowned.txt",
	})
	assertPaths(t, "manually modified", report.ManuallyModified(), []string{
		"generated/go/modified-retained.go",
		"generated/go/modified-retired.go",
	})
	wantKinds := []generatedfiles.ChangeKind{
		generatedfiles.ChangeStale,
		generatedfiles.ChangeStale,
		generatedfiles.ChangeStale,
		generatedfiles.ChangeMissing,
		generatedfiles.ChangeUnexpected,
		generatedfiles.ChangeUnexpected,
		generatedfiles.ChangeManuallyModified,
		generatedfiles.ChangeManuallyModified,
	}
	changes := report.Changes()
	gotKinds := make([]generatedfiles.ChangeKind, len(changes))
	for index, change := range changes {
		gotKinds[index] = change.Kind()
	}
	if !slices.Equal(gotKinds, wantKinds) {
		t.Fatalf("change kinds = %v, want %v", gotKinds, wantKinds)
	}
	if report.Clean() {
		t.Fatal("drift report is clean")
	}
	if after := snapshotFiles(t, root); !equalSnapshots(after, before) {
		t.Fatalf("Check modified files:\nafter: %#v\nbefore: %#v", after, before)
	}
}

func TestCheckIgnoresJavaScriptBuildOutput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	output := managedOutput(t)
	writeOutput(t, root, output)
	writeFile(t, root, "generated/sdk/javascript/node_modules/pkg/index.js", "dependency")
	writeFile(t, root, "generated/sdk/javascript/dist/index.js", "compiled")
	report, err := generatedfiles.Check(root, output)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !report.Clean() {
		t.Fatalf("ignored build output produced drift: %#v", report.Changes())
	}
}

func TestCheckRejectsInvalidOwnershipManifest(t *testing.T) {
	t.Parallel()

	digest := testDigest([]byte("data"))
	tests := []struct {
		name string
		data string
	}{
		{name: "malformed", data: `{`},
		{name: "unknown field", data: `{"version":3,"files":[],"extra":true}`},
		{name: "unsupported version", data: `{"version":2,"files":[]}`},
		{name: "missing files", data: `{"version":3}`},
		{name: "invalid path", data: fmt.Sprintf(`{"version":3,"files":[{"path":"../outside","sha256":%q}]}`, digest)},
		{name: "manifest self record", data: fmt.Sprintf(`{"version":3,"files":[{"path":%q,"sha256":%q}]}`, generatedfiles.ManifestPath, digest)},
		{name: "invalid digest", data: `{"version":3,"files":[{"path":"generated/file","sha256":"sha256:ABC"}]}`},
		{name: "duplicate", data: fmt.Sprintf(`{"version":3,"files":[{"path":"generated/file","sha256":%q},{"path":"generated/file","sha256":%q}]}`, digest, digest)},
		{name: "trailing JSON", data: `{"version":3,"files":[]} {}`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFile(t, root, generatedfiles.ManifestPath, test.data)
			if _, err := generatedfiles.Check(root, managedOutput(t)); !errors.Is(err, generatedfiles.ErrCheck) || !errors.Is(err, generatedfiles.ErrManifest) {
				t.Fatalf("Check error = %v", err)
			}
		})
	}
}

func TestCheckRejectsInvalidArtifactProvenanceAndNoncanonicalManifest(t *testing.T) {
	t.Parallel()

	base := managedOutput(t,
		"generated/a.txt", "a\n",
		"generated/b.txt", "b\n",
	).ManifestJSON()
	tests := []struct {
		name         string
		want         string
		mutate       func(map[string]any)
		noncanonical bool
	}{
		{name: "missing generator", want: "generator identity", mutate: func(document map[string]any) { delete(manifestFixtureRecord(document, 0), "generator") }},
		{name: "invalid output kind", want: "output kind", mutate: func(document map[string]any) { manifestFixtureRecord(document, 0)["output_kind"] = "archive" }},
		{name: "missing inputs", want: "input record IDs", mutate: func(document map[string]any) { delete(manifestFixtureRecord(document, 0), "input_record_ids") }},
		{name: "duplicate inputs", want: "input record IDs", mutate: func(document map[string]any) {
			manifestFixtureRecord(document, 0)["input_record_ids"] = []any{"input", "input"}
		}},
		{name: "unordered inputs", want: "input record IDs", mutate: func(document map[string]any) {
			manifestFixtureRecord(document, 0)["input_record_ids"] = []any{"z", "a"}
		}},
		{name: "missing sources", want: "sources", mutate: func(document map[string]any) { delete(manifestFixtureRecord(document, 0), "sources") }},
		{name: "invalid cleanup ownership", want: "cleanup ownership", mutate: func(document map[string]any) { manifestFixtureRecord(document, 0)["cleanup_ownership"] = "user-owned" }},
		{name: "unordered records", want: "uniquely ordered", mutate: func(document map[string]any) {
			files := document["files"].([]any)
			files[0], files[1] = files[1], files[0]
		}},
		{name: "duplicate records", want: "uniquely ordered", mutate: func(document map[string]any) {
			files := document["files"].([]any)
			document["files"] = append(files, files[1])
		}},
		{name: "noncanonical JSON", want: "not canonical", noncanonical: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			var data []byte
			if test.noncanonical {
				data = append(append([]byte(nil), base...), ' ')
			} else {
				data = mutateOwnershipManifest(t, base, test.mutate)
			}
			writeFileBytes(t, root, generatedfiles.ManifestPath, data)
			if _, err := generatedfiles.Check(root, managedOutput(t)); !errors.Is(err, generatedfiles.ErrCheck) || !errors.Is(err, generatedfiles.ErrManifest) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Check error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestInstallReplacesAndRemovesManagedFilesWhilePreservingUnownedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	oldOutput := managedOutput(t,
		"generated/docs/same.md", "same",
		"generated/go/modified-retired.go", "generated before",
		"generated/go/obsolete.go", "obsolete",
		"generated/go/shared.go", "before",
	)
	writeOutput(t, root, oldOutput)
	writeFile(t, root, "generated/go/modified-retired.go", "user edit")
	writeFile(t, root, "generated/unowned.txt", "keep")
	newOutput := managedOutput(t,
		"generated/docs/same.md", "same",
		"generated/go/alias.go", "alias",
		"generated/go/shared.go", "after",
	)

	validated := false
	report, err := generatedfiles.Install(root, newOutput, func(updatedRoot string) error {
		validated = true
		assertFile(t, updatedRoot, "generated/go/alias.go", "alias")
		assertFile(t, updatedRoot, "generated/go/shared.go", "after")
		assertMissing(t, updatedRoot, "generated/go/obsolete.go")
		assertFile(t, updatedRoot, "generated/go/modified-retired.go", "user edit")
		assertFile(t, updatedRoot, "generated/unowned.txt", "keep")
		return nil
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !validated {
		t.Fatal("validation callback did not run")
	}
	assertPaths(t, "unexpected", report.Unexpected(), []string{
		"generated/go/modified-retired.go",
		"generated/unowned.txt",
	})
	if len(report.Stale()) != 0 || len(report.Missing()) != 0 || len(report.ManuallyModified()) != 0 {
		t.Fatalf("post-install managed drift = %#v", report.Changes())
	}
	assertFile(t, root, "generated/go/alias.go", "alias")
	assertFile(t, root, "generated/go/shared.go", "after")
	assertMissing(t, root, "generated/go/obsolete.go")
	assertFile(t, root, "generated/go/modified-retired.go", "user edit")
	assertFile(t, root, "generated/unowned.txt", "keep")
	assertFileBytes(t, root, generatedfiles.ManifestPath, newOutput.ManifestJSON())
	checked, err := generatedfiles.Check(root, newOutput)
	if err != nil || !slices.Equal(checked.Unexpected(), report.Unexpected()) {
		t.Fatalf("Check after Install = %#v, %v", checked.Changes(), err)
	}
	assertNoTransaction(t, root)
}

func TestInstallWithWritesCommitsAuthoredAndGeneratedFilesTogether(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	oldOutput := managedOutput(t, "generated/go/shared.go", "before")
	writeOutput(t, root, oldOutput)
	writeFile(t, root, "plystra.yaml", "before\n")
	newOutput := managedOutput(t,
		"generated/go/new.go", "new",
		"generated/go/shared.go", "after",
	)
	validated := false
	report, err := generatedfiles.InstallWithWrites(root, newOutput, []atomicfs.Write{{
		Path:         "plystra.yaml",
		Data:         []byte("after\n"),
		ExpectedData: []byte("before\n"),
	}}, func(updatedRoot string) error {
		validated = true
		assertFile(t, updatedRoot, "plystra.yaml", "after\n")
		assertFile(t, updatedRoot, "generated/go/new.go", "new")
		assertFile(t, updatedRoot, "generated/go/shared.go", "after")
		return nil
	})
	if err != nil || !report.Clean() {
		t.Fatalf("InstallWithWrites = %#v, %v", report.Changes(), err)
	}
	if !validated {
		t.Fatal("validation callback did not observe the combined transaction")
	}
	assertFile(t, root, "plystra.yaml", "after\n")
	assertFile(t, root, "generated/go/new.go", "new")
	assertFile(t, root, "generated/go/shared.go", "after")
	assertNoTransaction(t, root)
}

func TestInstallWithWritesRollsBackAuthoredAndGeneratedFiles(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		panic bool
	}{
		{name: "error"},
		{name: "panic", panic: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			oldOutput := managedOutput(t,
				"generated/go/obsolete.go", "obsolete",
				"generated/go/shared.go", "before",
			)
			writeOutput(t, root, oldOutput)
			writeFile(t, root, "plystra.yaml", "before\n")
			before := snapshotFiles(t, root)
			newOutput := managedOutput(t,
				"generated/go/new.go", "new",
				"generated/go/shared.go", "after",
			)
			validationErr := errors.New("combined validation failed")
			var installErr error
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				_, installErr = generatedfiles.InstallWithWrites(root, newOutput, []atomicfs.Write{{
					Path:         "plystra.yaml",
					Data:         []byte("after\n"),
					ExpectedData: []byte("before\n"),
				}}, func(string) error {
					if test.panic {
						panic("combined validation panic")
					}
					return validationErr
				})
			}()
			if test.panic {
				if recovered != "combined validation panic" {
					t.Fatalf("recovered = %#v", recovered)
				}
			} else if !errors.Is(installErr, generatedfiles.ErrInstall) || !errors.Is(installErr, validationErr) {
				t.Fatalf("InstallWithWrites error = %v", installErr)
			}
			if after := snapshotFiles(t, root); !equalSnapshots(after, before) {
				t.Fatalf("combined rollback state:\nafter: %#v\nbefore: %#v", after, before)
			}
			assertNoTransaction(t, root)
		})
	}
}

func TestInstallWithWritesPreservesEmptyExpectedDataPrecondition(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	output := managedOutput(t)
	writeOutput(t, root, output)
	writeFile(t, root, "plystra.yaml", "concurrent edit\n")
	before := snapshotFiles(t, root)
	validated := false
	nonNilEmpty := make([]byte, 0)
	_, err := generatedfiles.InstallWithWrites(root, output, []atomicfs.Write{{
		Path:         "plystra.yaml",
		Data:         []byte("replacement\n"),
		ExpectedData: nonNilEmpty,
	}}, func(string) error {
		validated = true
		return nil
	})
	if !errors.Is(err, generatedfiles.ErrInstall) || !errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("InstallWithWrites error = %v", err)
	}
	if validated {
		t.Fatal("validation ran after a stale empty-file precondition")
	}
	if after := snapshotFiles(t, root); !equalSnapshots(after, before) {
		t.Fatalf("stale precondition changed files:\nafter: %#v\nbefore: %#v", after, before)
	}
	assertNoTransaction(t, root)
}

func TestInstallWithWritesRejectsUnsafeOrGeneratedAdditionalPaths(t *testing.T) {
	t.Parallel()

	for _, filePath := range []string{
		"generated",
		"generated/manual.txt",
		"../outside.yaml",
		`nested\outside.yaml`,
		".",
	} {
		filePath := filePath
		t.Run(strings.ReplaceAll(strings.ReplaceAll(filePath, "/", "_"), `\`, "_"), func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			output := managedOutput(t)
			writeOutput(t, root, output)
			before := snapshotFiles(t, root)
			validated := false
			_, err := generatedfiles.InstallWithWrites(root, output, []atomicfs.Write{{Path: filePath, Data: []byte("unsafe")}}, func(string) error {
				validated = true
				return nil
			})
			if !errors.Is(err, generatedfiles.ErrInstall) {
				t.Fatalf("InstallWithWrites(%q) error = %v", filePath, err)
			}
			if validated {
				t.Fatalf("validation ran for rejected additional path %q", filePath)
			}
			if after := snapshotFiles(t, root); !equalSnapshots(after, before) {
				t.Fatalf("rejected path %q changed files:\nafter: %#v\nbefore: %#v", filePath, after, before)
			}
			assertNoTransaction(t, root)
		})
	}
}

func TestInstallStrictRejectsUnexpectedOutputAndRollsBack(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	oldOutput := managedOutput(t, "generated/go/shared.go", "before")
	writeOutput(t, root, oldOutput)
	writeFile(t, root, "generated/manual.txt", "keep")
	writeFile(t, root, "generated/notes.txt", "keep too")
	before := snapshotFiles(t, root)
	newOutput := managedOutput(t,
		"generated/go/new.go", "new",
		"generated/go/shared.go", "after",
	)
	validated := false
	_, err := generatedfiles.InstallStrict(root, newOutput, func(string) error {
		validated = true
		return nil
	})
	if !errors.Is(err, generatedfiles.ErrInstall) || !errors.Is(err, generatedfiles.ErrUnexpected) || !strings.Contains(err.Error(), "generated/manual.txt") || !strings.Contains(err.Error(), "generated/notes.txt") {
		t.Fatalf("InstallStrict error = %v", err)
	}
	if validated {
		t.Fatal("strict installation validated beside unexpected output")
	}
	if after := snapshotFiles(t, root); !equalSnapshots(after, before) {
		t.Fatalf("strict rollback state:\nafter: %#v\nbefore: %#v", after, before)
	}
	assertNoTransaction(t, root)
}

func TestInstallRollsBackOnValidationFailureAndPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		panic bool
	}{
		{name: "error"},
		{name: "panic", panic: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			oldOutput := managedOutput(t,
				"generated/go/obsolete.go", "obsolete",
				"generated/go/shared.go", "before",
			)
			writeOutput(t, root, oldOutput)
			before := snapshotFiles(t, root)
			newOutput := managedOutput(t,
				"generated/go/new.go", "new",
				"generated/go/shared.go", "after",
			)
			validationErr := errors.New("application validation failed")
			var installErr error
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				_, installErr = generatedfiles.Install(root, newOutput, func(string) error {
					if test.panic {
						panic("validation panic")
					}
					return validationErr
				})
			}()
			if test.panic {
				if recovered != "validation panic" {
					t.Fatalf("recovered = %#v", recovered)
				}
			} else if !errors.Is(installErr, generatedfiles.ErrInstall) || !errors.Is(installErr, validationErr) {
				t.Fatalf("Install error = %v", installErr)
			}
			if after := snapshotFiles(t, root); !equalSnapshots(after, before) {
				t.Fatalf("rollback state:\nafter: %#v\nbefore: %#v", after, before)
			}
			assertNoTransaction(t, root)
		})
	}
}

func TestInstallPreservesConcurrentValidationEditAndRecoveryBackup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	oldOutput := managedOutput(t, "generated/go/shared.go", "before")
	writeOutput(t, root, oldOutput)
	newOutput := managedOutput(t, "generated/go/shared.go", "after")
	target := filepath.Join(root, filepath.FromSlash("generated/go/shared.go"))
	_, err := generatedfiles.Install(root, newOutput, func(string) error {
		return os.WriteFile(target, []byte("concurrent user edit"), 0o644)
	})
	if !errors.Is(err, generatedfiles.ErrInstall) || !errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("Install error = %v", err)
	}
	if !strings.Contains(err.Error(), "recovery data retained in .plystra-files-") {
		t.Fatalf("Install error does not identify recovery data: %v", err)
	}
	assertFile(t, root, "generated/go/shared.go", "concurrent user edit")
	backups, globErr := filepath.Glob(filepath.Join(root, ".plystra-files-*", "backup", "*"))
	if globErr != nil || len(backups) == 0 {
		t.Fatalf("recovery backups = %v, %v", backups, globErr)
	}
	foundOriginal := false
	for _, backup := range backups {
		if data, readErr := os.ReadFile(backup); readErr == nil && string(data) == "before" {
			foundOriginal = true
		}
	}
	if !foundOriginal {
		t.Fatalf("recovery backups omit original managed file: %v", backups)
	}
	transactionRoot := filepath.Dir(filepath.Dir(backups[0]))
	t.Cleanup(func() {
		if err := os.RemoveAll(transactionRoot); err != nil {
			t.Errorf("remove recovery transaction: %v", err)
		}
	})
}

func TestInstallRejectsDifferentUnownedCollisionAndAdoptsIdenticalFile(t *testing.T) {
	t.Parallel()

	t.Run("different", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, root, "generated/go/alias.go", "user file")
		output := managedOutput(t, "generated/go/alias.go", "generated")
		validated := false
		_, err := generatedfiles.Install(root, output, func(string) error {
			validated = true
			return nil
		})
		if !errors.Is(err, generatedfiles.ErrInstall) || !errors.Is(err, generatedfiles.ErrConflict) {
			t.Fatalf("Install error = %v", err)
		}
		if validated {
			t.Fatal("validation ran for unowned collision")
		}
		assertFile(t, root, "generated/go/alias.go", "user file")
		assertMissing(t, root, generatedfiles.ManifestPath)
		assertNoTransaction(t, root)
	})

	t.Run("identical", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, root, "generated/go/alias.go", "generated")
		output := managedOutput(t, "generated/go/alias.go", "generated")
		report, err := generatedfiles.Install(root, output, func(string) error { return nil })
		if err != nil || !report.Clean() {
			t.Fatalf("Install = %#v, %v", report.Changes(), err)
		}
		assertFile(t, root, "generated/go/alias.go", "generated")
		assertFileBytes(t, root, generatedfiles.ManifestPath, output.ManifestJSON())
		assertNoTransaction(t, root)
	})
}

func TestCheckRejectsSymbolicGeneratedPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "generated")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation is unavailable: %v", err)
		}
		t.Fatalf("Symlink: %v", err)
	}
	if _, err := generatedfiles.Check(root, managedOutput(t)); !errors.Is(err, generatedfiles.ErrCheck) {
		t.Fatalf("Check error = %v", err)
	}
}

func TestCheckRejectsZeroOutputAndInstallRequiresValidation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := generatedfiles.Check(root, generatedfiles.Output{}); !errors.Is(err, generatedfiles.ErrCheck) || !errors.Is(err, generatedfiles.ErrOutput) {
		t.Fatalf("Check zero output error = %v", err)
	}
	if _, err := generatedfiles.Install(root, managedOutput(t), nil); !errors.Is(err, generatedfiles.ErrInstall) {
		t.Fatalf("Install nil validation error = %v", err)
	}
}

func TestReadApplicationManifestRecoveryReturnsEmbeddedSnapshot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	applicationManifest := []byte("{\"configuration\":{\"version\":1}}\n")
	output, err := generatedfiles.NewOutput([]generatedfiles.File{
		managedFile(t, generatedfiles.ApplicationManifestPath, applicationManifest),
	})
	if err != nil {
		t.Fatalf("NewOutput: %v", err)
	}
	writeOutput(t, root, output)
	recovered, exists, err := generatedfiles.ReadApplicationManifestRecovery(root)
	if err != nil || !exists || !bytes.Equal(compactJSON(t, recovered), compactJSON(t, applicationManifest)) {
		t.Fatalf("ReadApplicationManifestRecovery = %q, %t, %v", recovered, exists, err)
	}
	recovered[0] = 'x'
	repeated, exists, err := generatedfiles.ReadApplicationManifestRecovery(root)
	if err != nil || !exists || !bytes.Equal(compactJSON(t, repeated), compactJSON(t, applicationManifest)) {
		t.Fatalf("repeated ReadApplicationManifestRecovery = %q, %t, %v", repeated, exists, err)
	}

	withoutApplication := t.TempDir()
	writeOutput(t, withoutApplication, managedOutput(t))
	if data, exists, err := generatedfiles.ReadApplicationManifestRecovery(withoutApplication); err != nil || exists || data != nil {
		t.Fatalf("missing recovery = %q, %t, %v", data, exists, err)
	}
}

func TestReadApplicationManifestRecoveryRejectsMalformedOrUnsafeState(t *testing.T) {
	t.Parallel()

	t.Run("malformed", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, root, generatedfiles.ManifestPath, "{\"version\":3")
		if _, _, err := generatedfiles.ReadApplicationManifestRecovery(root); !errors.Is(err, generatedfiles.ErrManifest) {
			t.Fatalf("ReadApplicationManifestRecovery error = %v", err)
		}
	})
	t.Run("non-regular", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(generatedfiles.ManifestPath)), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if _, _, err := generatedfiles.ReadApplicationManifestRecovery(root); !errors.Is(err, generatedfiles.ErrManifest) {
			t.Fatalf("ReadApplicationManifestRecovery error = %v", err)
		}
	})
}

func TestReadOwnedFileRequiresExactManagedHistory(t *testing.T) {
	t.Parallel()

	const historyPath = "generated/proto/wire-map.json"
	history := []byte("{\"projection_schema\":\"plystra.proto-wire-map/v2\"}\n")
	root := t.TempDir()
	if data, exists, err := generatedfiles.ReadOwnedFile(root, historyPath, 1024); err != nil || exists || data != nil {
		t.Fatalf("initial ReadOwnedFile = %q, %t, %v", data, exists, err)
	}
	writeOutput(t, root, managedOutput(t, historyPath, string(history)))
	data, exists, err := generatedfiles.ReadOwnedFile(root, historyPath, 1024)
	if err != nil || !exists || !bytes.Equal(data, history) {
		t.Fatalf("ReadOwnedFile = %q, %t, %v", data, exists, err)
	}
	data[0] = 'x'
	repeated, exists, err := generatedfiles.ReadOwnedFile(root, historyPath, 1024)
	if err != nil || !exists || !bytes.Equal(repeated, history) {
		t.Fatalf("repeated ReadOwnedFile = %q, %t, %v", repeated, exists, err)
	}

	writeFile(t, root, historyPath, "manual drift\n")
	if _, _, err := generatedfiles.ReadOwnedFile(root, historyPath, 1024); !errors.Is(err, generatedfiles.ErrManifest) || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("drifted ReadOwnedFile error = %v", err)
	}

	missing := t.TempDir()
	writeOutput(t, missing, managedOutput(t, historyPath, string(history)))
	if err := os.Remove(filepath.Join(missing, filepath.FromSlash(historyPath))); err != nil {
		t.Fatalf("Remove(history): %v", err)
	}
	if _, _, err := generatedfiles.ReadOwnedFile(missing, historyPath, 1024); !errors.Is(err, generatedfiles.ErrManifest) || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing ReadOwnedFile error = %v", err)
	}

	unowned := t.TempDir()
	writeOutput(t, unowned, managedOutput(t, "generated/other.txt", "owned\n"))
	writeFileBytes(t, unowned, historyPath, history)
	if _, _, err := generatedfiles.ReadOwnedFile(unowned, historyPath, 1024); !errors.Is(err, generatedfiles.ErrManifest) || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("unowned ReadOwnedFile error = %v", err)
	}

	bounded := t.TempDir()
	writeOutput(t, bounded, managedOutput(t, historyPath, string(history)))
	if _, _, err := generatedfiles.ReadOwnedFile(bounded, historyPath, int64(len(history)-1)); !errors.Is(err, generatedfiles.ErrManifest) || !strings.Contains(err.Error(), "bounded") {
		t.Fatalf("bounded ReadOwnedFile error = %v", err)
	}
}

func TestReadOwnedFileRejectsSymbolicManagedParent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "generated"), 0o755); err != nil {
		t.Fatalf("Mkdir(generated): %v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "generated", "proto")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("create directory symlink: %v", err)
		}
		t.Fatalf("Symlink: %v", err)
	}
	if _, _, err := generatedfiles.ReadOwnedFile(root, "generated/proto/wire-map.json", 1024); !errors.Is(err, generatedfiles.ErrManifest) || !strings.Contains(err.Error(), "non-symbolic") {
		t.Fatalf("symbolic parent error = %v", err)
	}
}

func TestReadArtifactExplainsOwnedMissingModifiedAndManifestPaths(t *testing.T) {
	t.Parallel()

	const ownedPath = "generated/go/owned.go"
	root := t.TempDir()
	output := managedOutput(t, ownedPath, "package generated\n")
	writeOutput(t, root, output)
	manifestData := output.ManifestJSON()

	want, exists, err := generatedfiles.ReadArtifact(root, ownedPath)
	if err != nil || !exists || !want.Valid() || want.Path() != ownedPath ||
		want.Generator() != "plystra.test-generator/v1" ||
		want.Kind() != generatedfiles.ArtifactKindGoSource ||
		want.CleanupOwnership() != generatedfiles.CleanupOwnershipCLI {
		t.Fatalf("ReadArtifact(owned) = %#v, %t, %v", want, exists, err)
	}

	if err := os.Remove(filepath.Join(root, filepath.FromSlash(ownedPath))); err != nil {
		t.Fatalf("Remove(owned): %v", err)
	}
	missing, exists, err := generatedfiles.ReadArtifact(root, ownedPath)
	if err != nil || !exists || missing.SHA256() != want.SHA256() {
		t.Fatalf("ReadArtifact(missing owned) = %#v, %t, %v", missing, exists, err)
	}

	writeFile(t, root, ownedPath, "manual edit\n")
	modified, exists, err := generatedfiles.ReadArtifact(root, ownedPath)
	if err != nil || !exists || modified.SHA256() != want.SHA256() {
		t.Fatalf("ReadArtifact(modified owned) = %#v, %t, %v", modified, exists, err)
	}
	writeFile(t, root, "generated/go/unowned.go", "package generated\n")
	if artifact, exists, err := generatedfiles.ReadArtifact(root, "generated/go/unowned.go"); err != nil || exists || artifact.Valid() {
		t.Fatalf("ReadArtifact(unowned) = %#v, %t, %v", artifact, exists, err)
	}

	manifest, exists, err := generatedfiles.ReadArtifact(root, generatedfiles.ManifestPath)
	if err != nil || !exists || !manifest.Valid() ||
		manifest.Path() != generatedfiles.ManifestPath ||
		manifest.SHA256() != testDigest(manifestData) ||
		manifest.Generator() != "plystra.generated-ownership/v3" ||
		manifest.Kind() != generatedfiles.ArtifactKindOwnershipManifest ||
		manifest.CleanupOwnership() != generatedfiles.CleanupOwnershipCLI ||
		len(manifest.InputRecordIDs()) != 2 ||
		!strings.HasPrefix(manifest.InputRecordIDs()[0], "artifact-set:sha256:") ||
		!slices.Equal(manifest.Sources(), []string{"generated output model"}) {
		t.Fatalf("ReadArtifact(ownership manifest) = %#v, %t, %v", manifest, exists, err)
	}
	inputs := manifest.InputRecordIDs()
	sources := manifest.Sources()
	inputs[0] = "mutated"
	sources[0] = "mutated"
	repeated, exists, err := generatedfiles.ReadArtifact(root, generatedfiles.ManifestPath)
	if err != nil || !exists || repeated.InputRecordIDs()[0] == "mutated" || repeated.Sources()[0] == "mutated" {
		t.Fatalf("ReadArtifact(ownership manifest defensive access) = %#v, %t, %v", repeated, exists, err)
	}

	if _, _, err := generatedfiles.ReadArtifact(root, "../outside"); !errors.Is(err, generatedfiles.ErrManifest) {
		t.Fatalf("ReadArtifact(unsafe path) error = %v", err)
	}
	withoutManifest := t.TempDir()
	if artifact, exists, err := generatedfiles.ReadArtifact(withoutManifest, ownedPath); err != nil || exists || artifact.Valid() {
		t.Fatalf("ReadArtifact(without manifest) = %#v, %t, %v", artifact, exists, err)
	}
}

func managedFile(t testing.TB, filePath string, data []byte) generatedfiles.File {
	t.Helper()
	file, err := generatedfiles.NewFile(filePath, data, testArtifactInput(filePath))
	if err != nil {
		t.Fatalf("NewFile(%s): %v", filePath, err)
	}
	return file
}

func testArtifactInput(filePath string) generatedfiles.ArtifactInput {
	kind := generatedfiles.ArtifactKindGoSource
	if filePath == generatedfiles.ApplicationManifestPath {
		kind = generatedfiles.ArtifactKindApplicationManifest
	}
	return generatedfiles.ArtifactInput{
		Generator:      "plystra.test-generator/v1",
		Kind:           kind,
		InputRecordIDs: []string{"test:" + filePath},
		Sources:        []string{"test fixture"},
	}
}

func mutateOwnershipManifest(t testing.TB, data []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode ownership manifest fixture: %v", err)
	}
	mutate(document)
	result, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatalf("encode ownership manifest fixture: %v", err)
	}
	return append(result, '\n')
}

func manifestFixtureRecord(document map[string]any, index int) map[string]any {
	return document["files"].([]any)[index].(map[string]any)
}

func compactJSON(t testing.TB, data []byte) []byte {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		t.Fatalf("compact JSON: %v", err)
	}
	return compact.Bytes()
}

func managedOutput(t testing.TB, pathData ...string) generatedfiles.Output {
	t.Helper()
	if len(pathData)%2 != 0 {
		t.Fatal("managedOutput requires path/data pairs")
	}
	files := make([]generatedfiles.File, 0, len(pathData)/2)
	for index := 0; index < len(pathData); index += 2 {
		files = append(files, managedFile(t, pathData[index], []byte(pathData[index+1])))
	}
	output, err := generatedfiles.NewOutput(files)
	if err != nil {
		t.Fatalf("NewOutput: %v", err)
	}
	return output
}

func writeOutput(t testing.TB, root string, output generatedfiles.Output) {
	t.Helper()
	for _, file := range output.Files() {
		writeFileBytes(t, root, file.Path(), file.Data())
	}
	writeFileBytes(t, root, generatedfiles.ManifestPath, output.ManifestJSON())
}

func writeFile(t testing.TB, root, filePath, data string) {
	t.Helper()
	writeFileBytes(t, root, filePath, []byte(data))
}

func writeFileBytes(t testing.TB, root, filePath string, data []byte) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(filePath))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filePath, err)
	}
	if err := os.WriteFile(absolute, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", filePath, err)
	}
}

func assertFile(t testing.TB, root, filePath, want string) {
	t.Helper()
	assertFileBytes(t, root, filePath, []byte(want))
}

func assertFileBytes(t testing.TB, root, filePath string, want []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(filePath)))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", filePath, err)
	}
	if !bytes.Equal(data, want) {
		t.Fatalf("%s = %q, want %q", filePath, data, want)
	}
}

func assertMissing(t testing.TB, root, filePath string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(filePath))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists: %v", filePath, err)
	}
}

func assertPaths(t testing.TB, name string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s paths = %v, want %v", name, got, want)
	}
}

func assertNoTransaction(t testing.TB, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".plystra-files-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("transaction directories remain: %v", matches)
	}
}

func snapshotFiles(t testing.TB, root string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	err := filepath.WalkDir(root, func(filePath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot files: %v", err)
	}
	return result
}

func equalSnapshots(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for filePath, leftData := range left {
		if !bytes.Equal(leftData, right[filePath]) {
			return false
		}
	}
	return true
}

func testDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
