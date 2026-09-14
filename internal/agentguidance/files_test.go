package agentguidance

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/atomicfs"
)

func TestCheckAndSyncInstallAndRefreshOwnedGuidance(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	initial := renderTestProjection(t, "example.com/acme/application")
	report, err := Check(root, initial)
	if err != nil {
		t.Fatalf("Check empty Project: %v", err)
	}
	wantMissing := projectionPaths(initial)
	sort.Strings(wantMissing)
	if got := report.Missing(); !reflect.DeepEqual(got, wantMissing) || len(report.Stale()) != 0 || len(report.Unlisted()) != 0 || len(report.ManuallyModified()) != 0 {
		t.Fatalf("empty Project report = %#v, want missing %q", report.Changes(), wantMissing)
	}

	installed, err := Sync(root, initial, SyncOptions{})
	if err != nil {
		t.Fatalf("Sync initial guidance: %v", err)
	}
	if got := installed.Changed(); !reflect.DeepEqual(got, wantMissing) || len(installed.Removed()) != 0 {
		t.Fatalf("initial Sync result = changed %q, removed %q", got, installed.Removed())
	}
	assertGuidanceClean(t, root, initial)

	unchanged, err := Sync(root, initial, SyncOptions{})
	if err != nil || len(unchanged.Changed()) != 0 || len(unchanged.Removed()) != 0 {
		t.Fatalf("no-op Sync = changed %q, removed %q, %v", unchanged.Changed(), unchanged.Removed(), err)
	}

	updated := renderTestProjection(t, "example.com/acme/renamed")
	report, err = Check(root, updated)
	if err != nil {
		t.Fatalf("Check stale guidance: %v", err)
	}
	if report.Clean() || len(report.Stale()) == 0 || len(report.Missing()) != 0 || len(report.Unlisted()) != 0 || len(report.ManuallyModified()) != 0 {
		t.Fatalf("updated guidance report = %#v", report.Changes())
	}
	refreshed, err := Sync(root, updated, SyncOptions{})
	if err != nil {
		t.Fatalf("Sync updated guidance: %v", err)
	}
	if len(refreshed.Changed()) == 0 || len(refreshed.Removed()) != 0 {
		t.Fatalf("updated Sync result = changed %q, removed %q", refreshed.Changed(), refreshed.Removed())
	}
	assertGuidanceClean(t, root, updated)
}

func TestCheckAndSyncPreserveEveryUserOwnedPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	initial := renderTestProjection(t, "example.com/acme/application")
	if _, err := Sync(root, initial, SyncOptions{}); err != nil {
		t.Fatalf("Sync initial guidance: %v", err)
	}
	userFiles := map[string]string{
		Root + "/local.md":                     "Project-specific guidance\n",
		Root + "/notes.md":                     "unlisted guidance\n",
		Root + "/tasks/project-specific.md":    "unlisted task\n",
		".agents/skills/other/SKILL.md":        "sibling skill\n",
		"AGENTS.md":                            "repository guidance\n",
		"docs/unrelated-agent-instructions.md": "unrelated documentation\n",
	}
	for filePath, data := range userFiles {
		writeTestFile(t, root, filePath, []byte(data))
	}

	before := snapshotTestTree(t, root)
	updated := renderTestProjection(t, "example.com/acme/renamed")
	report, err := Check(root, updated)
	if err != nil || report.Clean() {
		t.Fatalf("Check updated guidance = %#v, %v", report.Changes(), err)
	}
	if got := snapshotTestTree(t, root); !reflect.DeepEqual(got, before) {
		t.Fatalf("Check mutated Project tree:\n before: %#v\n after:  %#v", before, got)
	}
	if _, err := Sync(root, updated, SyncOptions{}); err != nil {
		t.Fatalf("Sync updated guidance: %v", err)
	}
	for filePath, want := range userFiles {
		if got := string(readTestFile(t, root, filePath)); got != want {
			t.Fatalf("user-owned %s = %q, want %q", filePath, got, want)
		}
	}
	assertGuidanceClean(t, root, updated)
}

func TestSyncRequiresExplicitAuthorityForModifiedOwnedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	projection := renderTestProjection(t, "example.com/acme/application")
	if _, err := Sync(root, projection, SyncOptions{}); err != nil {
		t.Fatalf("Sync initial guidance: %v", err)
	}
	target := Root + "/SKILL.md"
	writeTestFile(t, root, target, []byte("manual generated edit\n"))
	before := snapshotTestTree(t, root)

	_, err := Sync(root, projection, SyncOptions{})
	assertGuidanceFailure(t, err, ErrDrift, target)
	if got := snapshotTestTree(t, root); !reflect.DeepEqual(got, before) {
		t.Fatalf("blocked Sync mutated Project tree:\n before: %#v\n after:  %#v", before, got)
	}

	result, err := Sync(root, projection, SyncOptions{ReplaceGenerated: true})
	if err != nil {
		t.Fatalf("Sync with replacement: %v", err)
	}
	if got := result.Changed(); !reflect.DeepEqual(got, []string{target}) {
		t.Fatalf("replacement changed %q, want %q", got, target)
	}
	assertGuidanceClean(t, root, projection)
}

func TestSyncBlocksMissingOrUnlistedPathsInEveryMode(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		setup func(t *testing.T, root string, projection Projection) string
	}{
		{
			name: "missing prior-owned",
			setup: func(t *testing.T, root string, projection Projection) string {
				t.Helper()
				if _, err := Sync(root, projection, SyncOptions{}); err != nil {
					t.Fatalf("Sync initial guidance: %v", err)
				}
				target := Root + "/SKILL.md"
				if err := os.Remove(testPath(root, target)); err != nil {
					t.Fatalf("Remove %s: %v", target, err)
				}
				return target
			},
		},
		{
			name: "unlisted desired",
			setup: func(t *testing.T, root string, projection Projection) string {
				t.Helper()
				target := Root + "/SKILL.md"
				writeTestFile(t, root, target, projectionData(t, projection, target))
				return target
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			projection := renderTestProjection(t, "example.com/acme/application")
			target := test.setup(t, root, projection)
			before := snapshotTestTree(t, root)
			for _, options := range []SyncOptions{{}, {ReplaceGenerated: true}} {
				_, err := Sync(root, projection, options)
				assertGuidanceFailure(t, err, ErrDrift, target)
				if got := snapshotTestTree(t, root); !reflect.DeepEqual(got, before) {
					t.Fatalf("blocked Sync with options %#v mutated tree:\n before: %#v\n after:  %#v", options, before, got)
				}
			}
		})
	}
}

func TestSyncDoesNotExpandPriorManifestOwnership(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	initial := renderTestProjection(t, "example.com/acme/application")
	if _, err := Sync(root, initial, SyncOptions{}); err != nil {
		t.Fatalf("Sync initial guidance: %v", err)
	}
	target := Root + "/tasks/new-catalog-task.md"
	updated := withProjectionPath(t, initial, target, []byte("# New catalog task\n"))

	report, err := Check(root, updated)
	if err != nil {
		t.Fatalf("Check expanded catalog: %v", err)
	}
	if !reflect.DeepEqual(report.Stale(), []string{ManifestPath}) || !reflect.DeepEqual(report.Missing(), []string{target}) {
		t.Fatalf("expanded catalog report = %#v", report.Changes())
	}
	before := snapshotTestTree(t, root)
	for _, options := range []SyncOptions{{}, {ReplaceGenerated: true}} {
		_, err := Sync(root, updated, options)
		assertGuidanceFailure(t, err, ErrDrift, target)
		if got := snapshotTestTree(t, root); !reflect.DeepEqual(got, before) {
			t.Fatalf("ownership-expanding Sync with options %#v mutated tree:\n before: %#v\n after:  %#v", options, before, got)
		}
	}
}

func TestCheckAndSyncRejectCaseAliasedGuidancePaths(t *testing.T) {
	t.Run("desired file", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		projection := renderTestProjection(t, "example.com/acme/application")
		target := Root + "/SKILL.md"
		writeTestFile(t, root, Root+"/skill.md", projectionData(t, projection, target))
		before := snapshotTestTree(t, root)

		report, err := Check(root, projection)
		if err != nil {
			t.Fatalf("Check aliased desired path: %v", err)
		}
		if !reflect.DeepEqual(report.Unlisted(), []string{target}) {
			t.Fatalf("aliased desired report = %#v", report.Changes())
		}
		for _, options := range []SyncOptions{{}, {ReplaceGenerated: true}} {
			_, err := Sync(root, projection, options)
			assertGuidanceFailure(t, err, ErrDrift, target)
			if got := snapshotTestTree(t, root); !reflect.DeepEqual(got, before) {
				t.Fatalf("aliased desired Sync with options %#v mutated tree:\n before: %#v\n after:  %#v", options, before, got)
			}
		}
	})

	t.Run("manifest parent", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		projection := renderTestProjection(t, "example.com/acme/application")
		writeTestFile(t, root, ".Agents/skills/plystra/manifest.json", []byte("{}\n"))
		before := snapshotTestTree(t, root)

		if _, err := Check(root, projection); err == nil || !errors.Is(err, ErrManifest) {
			t.Fatalf("Check aliased manifest parent error = %v", err)
		} else {
			assertSourcePaths(t, err, ManifestPath)
		}
		for _, options := range []SyncOptions{{}, {ReplaceGenerated: true}} {
			_, err := Sync(root, projection, options)
			assertGuidanceFailure(t, err, ErrManifest, ManifestPath)
			if got := snapshotTestTree(t, root); !reflect.DeepEqual(got, before) {
				t.Fatalf("aliased manifest Sync with options %#v mutated tree:\n before: %#v\n after:  %#v", options, before, got)
			}
		}
	})
}

func TestSyncRejectsUnsafeOwnedEntriesEvenWithReplacement(t *testing.T) {
	for _, kind := range []string{"directory", "oversized", "symlink"} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			projection := renderTestProjection(t, "example.com/acme/application")
			if _, err := Sync(root, projection, SyncOptions{}); err != nil {
				t.Fatalf("Sync initial guidance: %v", err)
			}
			target := Root + "/SKILL.md"
			absoluteTarget := testPath(root, target)
			if err := os.Remove(absoluteTarget); err != nil {
				t.Fatalf("Remove generated target: %v", err)
			}
			switch kind {
			case "directory":
				if err := os.Mkdir(absoluteTarget, 0o755); err != nil {
					t.Fatalf("Mkdir target: %v", err)
				}
			case "oversized":
				writeTestFile(t, root, target, bytes.Repeat([]byte("x"), maximumGuidanceFileBytes+1))
			case "symlink":
				outside := filepath.Join(t.TempDir(), "outside.md")
				if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
					t.Fatalf("Write outside file: %v", err)
				}
				if err := os.Symlink(outside, absoluteTarget); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("symlink creation is unavailable: %v", err)
					}
					t.Fatalf("Symlink target: %v", err)
				}
			}
			before := snapshotTestTree(t, root)
			_, err := Sync(root, projection, SyncOptions{ReplaceGenerated: true})
			assertGuidanceFailure(t, err, ErrDrift, target)
			if got := snapshotTestTree(t, root); !reflect.DeepEqual(got, before) {
				t.Fatalf("unsafe replacement mutated tree:\n before: %#v\n after:  %#v", before, got)
			}
		})
	}
}

func TestSyncRemovesOnlyUnchangedRetiredOwnedFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	initial := renderTestProjection(t, "example.com/acme/application")
	if _, err := Sync(root, initial, SyncOptions{}); err != nil {
		t.Fatalf("Sync initial guidance: %v", err)
	}
	retired := Root + "/tasks/resources-and-data.md"
	updated := withoutProjectionPath(t, initial, retired)
	writeTestFile(t, root, Root+"/local.md", []byte("keep local\n"))
	writeTestFile(t, root, Root+"/tasks/custom.md", []byte("keep custom\n"))

	result, err := Sync(root, updated, SyncOptions{})
	if err != nil {
		t.Fatalf("Sync retired guidance: %v", err)
	}
	if !reflect.DeepEqual(result.Removed(), []string{retired}) {
		t.Fatalf("removed paths = %q, want %q", result.Removed(), retired)
	}
	if _, err := os.Lstat(testPath(root, retired)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("retired path remains: %v", err)
	}
	if got := string(readTestFile(t, root, Root+"/local.md")); got != "keep local\n" {
		t.Fatalf("local.md = %q", got)
	}
	if got := string(readTestFile(t, root, Root+"/tasks/custom.md")); got != "keep custom\n" {
		t.Fatalf("custom task = %q", got)
	}
	assertGuidanceClean(t, root, updated)
}

func TestCheckAndSyncRejectInvalidManifestWithoutMutation(t *testing.T) {
	for _, kind := range []string{"malformed", "directory", "oversized", "symlink"} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			projection := renderTestProjection(t, "example.com/acme/application")
			if _, err := Sync(root, projection, SyncOptions{}); err != nil {
				t.Fatalf("Sync initial guidance: %v", err)
			}
			manifest := testPath(root, ManifestPath)
			if err := os.Remove(manifest); err != nil {
				t.Fatalf("Remove manifest: %v", err)
			}
			switch kind {
			case "malformed":
				writeTestFile(t, root, ManifestPath, []byte("{\n"))
			case "directory":
				if err := os.Mkdir(manifest, 0o755); err != nil {
					t.Fatalf("Mkdir manifest: %v", err)
				}
			case "oversized":
				writeTestFile(t, root, ManifestPath, bytes.Repeat([]byte("x"), maximumGuidanceFileBytes+1))
			case "symlink":
				outside := filepath.Join(t.TempDir(), "manifest.json")
				if err := os.WriteFile(outside, []byte("{}\n"), 0o644); err != nil {
					t.Fatalf("Write outside manifest: %v", err)
				}
				if err := os.Symlink(outside, manifest); err != nil {
					if runtime.GOOS == "windows" {
						t.Skipf("symlink creation is unavailable: %v", err)
					}
					t.Fatalf("Symlink manifest: %v", err)
				}
			}
			before := snapshotTestTree(t, root)
			if _, err := Check(root, projection); err == nil || !errors.Is(err, ErrManifest) {
				t.Fatalf("Check invalid manifest error = %v", err)
			} else {
				assertSourcePaths(t, err, ManifestPath)
			}
			if _, err := Sync(root, projection, SyncOptions{ReplaceGenerated: true}); err == nil || !errors.Is(err, ErrManifest) {
				t.Fatalf("Sync invalid manifest error = %v", err)
			} else {
				assertSourcePaths(t, err, ManifestPath)
			}
			if got := snapshotTestTree(t, root); !reflect.DeepEqual(got, before) {
				t.Fatalf("invalid-manifest operations mutated tree:\n before: %#v\n after:  %#v", before, got)
			}
		})
	}
}

func TestCheckRejectsPriorOwnershipAliasOfInstalledCatalog(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	projection := renderTestProjection(t, "example.com/acme/application")
	manifest := projection.Manifest()
	for index := range manifest.Files {
		if manifest.Files[index].Path == Root+"/SKILL.md" {
			manifest.Files[index].Path = Root + "/skill.md"
		}
	}
	sort.Slice(manifest.Files, func(left, right int) bool { return manifest.Files[left].Path < manifest.Files[right].Path })
	writeTestFile(t, root, ManifestPath, encodeTestManifest(t, manifest))
	before := snapshotTestTree(t, root)

	_, err := Check(root, projection)
	if err == nil || !errors.Is(err, ErrManifest) || !strings.Contains(err.Error(), "aliases installed catalog path") {
		t.Fatalf("Check ownership alias error = %v", err)
	}
	assertSourcePaths(t, err, ManifestPath)
	if got := snapshotTestTree(t, root); !reflect.DeepEqual(got, before) {
		t.Fatalf("alias Check mutated tree:\n before: %#v\n after:  %#v", before, got)
	}
}

func TestInspectRejectsManifestChangeDuringPathInspection(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	projection := renderTestProjection(t, "example.com/acme/application")
	if _, err := Sync(root, projection, SyncOptions{}); err != nil {
		t.Fatalf("Sync initial guidance: %v", err)
	}
	mutated := false
	inspectFile := func(openRoot *os.Root, filePath string) (actualFile, error) {
		actual, err := inspectActual(openRoot, filePath)
		if err == nil && filePath != ManifestPath && !mutated {
			mutated = true
			if writeErr := os.WriteFile(testPath(root, ManifestPath), []byte("{}\n"), 0o644); writeErr != nil {
				t.Fatalf("mutate manifest: %v", writeErr)
			}
		}
		return actual, err
	}
	_, err := inspectWith(root, projection, inspectFile)
	if err == nil || !errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("inspectWith manifest race error = %v", err)
	}
	assertSourcePaths(t, err, ManifestPath)
}

func TestSyncClassifiesTransactionTargetChangesAfterUntypedApplyFailures(t *testing.T) {
	t.Run("appeared write target", func(t *testing.T) {
		root := t.TempDir()
		projection := renderTestProjection(t, "example.com/acme/application")
		target := Root + "/SKILL.md"
		apply := func(rootPath string, writes []atomicfs.Write, removes []atomicfs.Remove, validate func(string) error) error {
			writeTestFile(t, rootPath, target, []byte("concurrent owner\n"))
			return atomicfs.ApplyFiles(rootPath, writes, removes, validate)
		}
		_, err := syncWithApply(root, projection, SyncOptions{}, apply)
		if err == nil || !errors.Is(err, atomicfs.ErrConcurrentChange) {
			t.Fatalf("appeared target error = %v", err)
		}
		assertSourcePaths(t, err, target)
		if got := string(readTestFile(t, root, target)); got != "concurrent owner\n" {
			t.Fatalf("concurrent target = %q", got)
		}
		if _, err := os.Lstat(testPath(root, ManifestPath)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("manifest was installed after concurrent target appeared: %v", err)
		}
	})

	t.Run("missing removal target", func(t *testing.T) {
		root := t.TempDir()
		initial := renderTestProjection(t, "example.com/acme/application")
		if _, err := Sync(root, initial, SyncOptions{}); err != nil {
			t.Fatalf("Sync initial guidance: %v", err)
		}
		retired := Root + "/tasks/resources-and-data.md"
		updated := withoutProjectionPath(t, initial, retired)
		apply := func(rootPath string, writes []atomicfs.Write, removes []atomicfs.Remove, validate func(string) error) error {
			if err := os.Remove(testPath(rootPath, retired)); err != nil {
				t.Fatalf("remove transaction target: %v", err)
			}
			return atomicfs.ApplyFiles(rootPath, writes, removes, validate)
		}
		_, err := syncWithApply(root, updated, SyncOptions{}, apply)
		if err == nil || !errors.Is(err, atomicfs.ErrConcurrentChange) {
			t.Fatalf("missing removal error = %v", err)
		}
		assertSourcePaths(t, err, retired)
		if _, err := os.Lstat(testPath(root, retired)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("retired path unexpectedly restored: %v", err)
		}
		manifest, parseErr := ParseManifest(readTestFile(t, root, ManifestPath))
		if parseErr != nil || !sameManifest(manifest, initial.Manifest()) {
			t.Fatalf("manifest changed after failed removal = %#v, %v", manifest, parseErr)
		}
	})
}

func TestSyncDoesNotInventConcurrencyForUnchangedApplyFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	projection := renderTestProjection(t, "example.com/acme/application")
	wantErr := errors.New("injected apply failure")
	_, err := syncWithApply(root, projection, SyncOptions{}, func(string, []atomicfs.Write, []atomicfs.Remove, func(string) error) error {
		return wantErr
	})
	if !errors.Is(err, ErrSync) || !errors.Is(err, wantErr) || errors.Is(err, atomicfs.ErrConcurrentChange) {
		t.Fatalf("unchanged apply failure = %v", err)
	}
	var source *SourceError
	if errors.As(err, &source) {
		t.Fatalf("unchanged apply failure unexpectedly has sources %q", source.Sources())
	}
	if got := snapshotTestTree(t, root); len(got) != 0 {
		t.Fatalf("failed apply mutated tree: %#v", got)
	}
}

func renderTestProjection(t *testing.T, modulePath string) Projection {
	t.Helper()
	projection, err := Render(modulePath)
	if err != nil {
		t.Fatalf("Render(%q): %v", modulePath, err)
	}
	return projection
}

func withoutProjectionPath(t *testing.T, projection Projection, removed string) Projection {
	t.Helper()
	manifest := projection.Manifest()
	filteredManifest := manifest.Files[:0]
	for _, file := range manifest.Files {
		if file.Path != removed {
			filteredManifest = append(filteredManifest, file)
		}
	}
	if len(filteredManifest) == len(manifest.Files) {
		t.Fatalf("projection does not contain %s", removed)
	}
	manifest.Files = append([]ManifestFile(nil), filteredManifest...)
	manifestData := encodeTestManifest(t, manifest)
	files := make([]File, 0, len(projection.files)-1)
	for _, file := range projection.files {
		switch file.path {
		case removed:
			continue
		case ManifestPath:
			files = append(files, File{path: ManifestPath, data: manifestData})
		default:
			files = append(files, File{path: file.path, data: file.Data()})
		}
	}
	result := Projection{modulePath: projection.modulePath, files: files, manifest: manifest}
	if err := validateProjection(result); err != nil {
		t.Fatalf("retired projection is invalid: %v", err)
	}
	return result
}

func withProjectionPath(t *testing.T, projection Projection, added string, data []byte) Projection {
	t.Helper()
	if _, found := projectionFile(projection, added); found {
		t.Fatalf("projection already contains %s", added)
	}
	manifest := projection.Manifest()
	manifest.Files = append(manifest.Files, ManifestFile{Path: added, SHA256: digest(data)})
	sort.Slice(manifest.Files, func(left, right int) bool { return manifest.Files[left].Path < manifest.Files[right].Path })
	manifestData := encodeTestManifest(t, manifest)
	files := make([]File, 0, len(projection.files)+1)
	for _, file := range projection.files {
		if file.path != ManifestPath {
			files = append(files, File{path: file.path, data: file.Data()})
		}
	}
	files = append(files, File{path: added, data: append([]byte(nil), data...)}, File{path: ManifestPath, data: manifestData})
	result := Projection{modulePath: projection.modulePath, files: files, manifest: manifest}
	if err := validateProjection(result); err != nil {
		t.Fatalf("expanded projection is invalid: %v", err)
	}
	return result
}

func encodeTestManifest(t *testing.T, manifest Manifest) []byte {
	t.Helper()
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("Marshal manifest: %v", err)
	}
	return append(data, '\n')
}

func projectionPaths(projection Projection) []string {
	paths := make([]string, 0, len(projection.files))
	for _, file := range projection.files {
		paths = append(paths, file.path)
	}
	return paths
}

func projectionData(t *testing.T, projection Projection, filePath string) []byte {
	t.Helper()
	data, found := projectionFile(projection, filePath)
	if !found {
		t.Fatalf("projection does not contain %s", filePath)
	}
	return append([]byte(nil), data...)
}

func assertGuidanceClean(t *testing.T, root string, projection Projection) {
	t.Helper()
	report, err := Check(root, projection)
	if err != nil || !report.Clean() {
		t.Fatalf("guidance is not clean: changes %#v, %v", report.Changes(), err)
	}
}

func assertGuidanceFailure(t *testing.T, err, want error, paths ...string) {
	t.Helper()
	if err == nil || !errors.Is(err, ErrSync) || !errors.Is(err, want) {
		t.Fatalf("guidance failure = %v, want ErrSync and %v", err, want)
	}
	assertSourcePaths(t, err, paths...)
}

func assertSourcePaths(t *testing.T, err error, paths ...string) {
	t.Helper()
	var source *SourceError
	if !errors.As(err, &source) || source == nil {
		t.Fatalf("error has no Agent-guidance sources: %v", err)
	}
	want := append([]string(nil), paths...)
	sort.Strings(want)
	gotSources := source.Sources()
	got := make([]string, len(gotSources))
	for index, item := range gotSources {
		if item.SourceKind() != "agent-guidance" || item.Line() != 0 || item.Column() != 0 {
			t.Fatalf("source %#v is not a path-only Agent-guidance source", item)
		}
		got[index] = item.SourcePath()
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("source paths = %q, want %q", got, want)
	}
}

func writeTestFile(t *testing.T, root, filePath string, data []byte) {
	t.Helper()
	absolute := testPath(root, filePath)
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filePath, err)
	}
	if err := os.WriteFile(absolute, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", filePath, err)
	}
}

func readTestFile(t *testing.T, root, filePath string) []byte {
	t.Helper()
	data, err := os.ReadFile(testPath(root, filePath))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", filePath, err)
	}
	return data
}

func testPath(root, filePath string) string {
	return filepath.Join(root, filepath.FromSlash(filePath))
}

func snapshotTestTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root || entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(name)
			if err != nil {
				return err
			}
			result[filepath.ToSlash(relative)] = []byte("symlink:" + target)
			return nil
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot tree: %v", err)
	}
	return result
}
