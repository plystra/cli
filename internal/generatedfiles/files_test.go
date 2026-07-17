package generatedfiles_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
  "version": 1,
  "files": [
    {
      "path": "generated/a/first.txt",
      "sha256": %q
    },
    {
      "path": "generated/z/second.txt",
      "sha256": %q
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
			if _, err := generatedfiles.NewFile(filePath, []byte("data")); !errors.Is(err, generatedfiles.ErrOutput) || !errors.Is(err, generatedfiles.ErrPath) {
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

func TestCheckReportsChangedMissingUnexpectedAndObsoleteFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	oldOutput := managedOutput(t,
		"generated/docs/same.md", "same",
		"generated/go/modified-obsolete.go", "generated before",
		"generated/go/obsolete.go", "obsolete",
		"generated/go/shared.go", "before",
	)
	writeOutput(t, root, oldOutput)
	writeFile(t, root, "generated/go/modified-obsolete.go", "user edit")
	writeFile(t, root, "generated/unowned.txt", "keep")
	before := snapshotFiles(t, root)

	newOutput := managedOutput(t,
		"generated/docs/missing.md", "missing",
		"generated/docs/same.md", "same",
		"generated/go/shared.go", "after",
	)
	report, err := generatedfiles.Check(root, newOutput)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	assertPaths(t, "changed", report.Changed(), []string{
		generatedfiles.ManifestPath,
		"generated/go/modified-obsolete.go",
		"generated/go/shared.go",
	})
	assertPaths(t, "missing", report.Missing(), []string{"generated/docs/missing.md"})
	assertPaths(t, "unexpected", report.Unexpected(), []string{"generated/unowned.txt"})
	assertPaths(t, "obsolete", report.Obsolete(), []string{
		"generated/go/modified-obsolete.go",
		"generated/go/obsolete.go",
	})
	wantKinds := []generatedfiles.ChangeKind{
		generatedfiles.ChangeChanged,
		generatedfiles.ChangeChanged,
		generatedfiles.ChangeChanged,
		generatedfiles.ChangeMissing,
		generatedfiles.ChangeUnexpected,
		generatedfiles.ChangeObsolete,
		generatedfiles.ChangeObsolete,
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
		{name: "unknown field", data: `{"version":1,"files":[],"extra":true}`},
		{name: "unsupported version", data: `{"version":2,"files":[]}`},
		{name: "missing files", data: `{"version":1}`},
		{name: "invalid path", data: fmt.Sprintf(`{"version":1,"files":[{"path":"../outside","sha256":%q}]}`, digest)},
		{name: "manifest self record", data: fmt.Sprintf(`{"version":1,"files":[{"path":%q,"sha256":%q}]}`, generatedfiles.ManifestPath, digest)},
		{name: "invalid digest", data: `{"version":1,"files":[{"path":"generated/file","sha256":"sha256:ABC"}]}`},
		{name: "duplicate", data: fmt.Sprintf(`{"version":1,"files":[{"path":"generated/file","sha256":%q},{"path":"generated/file","sha256":%q}]}`, digest, digest)},
		{name: "trailing JSON", data: `{"version":1,"files":[]} {}`},
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

func TestInstallReplacesAndRemovesManagedFilesWhilePreservingUnownedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	oldOutput := managedOutput(t,
		"generated/docs/same.md", "same",
		"generated/go/modified-obsolete.go", "generated before",
		"generated/go/obsolete.go", "obsolete",
		"generated/go/shared.go", "before",
	)
	writeOutput(t, root, oldOutput)
	writeFile(t, root, "generated/go/modified-obsolete.go", "user edit")
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
		assertFile(t, updatedRoot, "generated/go/modified-obsolete.go", "user edit")
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
		"generated/go/modified-obsolete.go",
		"generated/unowned.txt",
	})
	if len(report.Changed()) != 0 || len(report.Missing()) != 0 || len(report.Obsolete()) != 0 {
		t.Fatalf("post-install managed drift = %#v", report.Changes())
	}
	assertFile(t, root, "generated/go/alias.go", "alias")
	assertFile(t, root, "generated/go/shared.go", "after")
	assertMissing(t, root, "generated/go/obsolete.go")
	assertFile(t, root, "generated/go/modified-obsolete.go", "user edit")
	assertFile(t, root, "generated/unowned.txt", "keep")
	assertFileBytes(t, root, generatedfiles.ManifestPath, newOutput.ManifestJSON())
	checked, err := generatedfiles.Check(root, newOutput)
	if err != nil || !slices.Equal(checked.Unexpected(), report.Unexpected()) {
		t.Fatalf("Check after Install = %#v, %v", checked.Changes(), err)
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

func managedFile(t testing.TB, filePath string, data []byte) generatedfiles.File {
	t.Helper()
	file, err := generatedfiles.NewFile(filePath, data)
	if err != nil {
		t.Fatalf("NewFile(%s): %v", filePath, err)
	}
	return file
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
