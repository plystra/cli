package applicationresolve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfigurationSelectorPrecedenceAndNormalization(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	ambient, err := resolveConfigurationSelector(root, "", []string{"PLYSTRA_CONFIG=deploy/ambient.yaml"})
	if err != nil || ambient.mode != configurationModeExplicit || ambient.path != "deploy/ambient.yaml" {
		t.Fatalf("ambient selector = %#v, %v", ambient, err)
	}
	explicit, err := resolveConfigurationSelector(root, "deploy/explicit.yaml", []string{"PLYSTRA_CONFIG=deploy/ambient.yaml", "PLYSTRA_CONFIG=duplicate.yaml"})
	if err != nil || explicit.mode != configurationModeExplicit || explicit.path != "deploy/explicit.yaml" {
		t.Fatalf("explicit selector = %#v, %v", explicit, err)
	}
	absolute := filepath.Join(root, "deploy", "customer.yaml")
	selected, err := resolveConfigurationSelector(root, absolute, nil)
	if err != nil || selected.path != "deploy/customer.yaml" {
		t.Fatalf("absolute selector = %#v, %v", selected, err)
	}
	defaultSelection, err := resolveConfigurationSelector(root, "", []string{"UNRELATED=value"})
	if err != nil || defaultSelection.mode != configurationModeDefault || defaultSelection.path != applicationManifestName {
		t.Fatalf("default selector = %#v, %v", defaultSelection, err)
	}
	explicitRoot, err := resolveConfigurationSelector(root, applicationManifestName, nil)
	if err != nil || explicitRoot.mode != configurationModeExplicit || explicitRoot.path != applicationManifestName {
		t.Fatalf("explicit root selector = %#v, %v", explicitRoot, err)
	}
}

func TestResolveConfigurationSelectorRejectsUnsafeAndAmbiguousInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.yaml")
	tests := []struct {
		name        string
		explicit    string
		environment []string
		want        string
	}{
		{name: "empty ambient", environment: []string{"PLYSTRA_CONFIG=   "}, want: "empty configuration path"},
		{name: "duplicate ambient", environment: []string{"PLYSTRA_CONFIG=a.yaml", "PLYSTRA_CONFIG=b.yaml"}, want: "more than once"},
		{name: "parent traversal", explicit: "../outside.yaml", want: "within the Project root"},
		{name: "absolute outside", explicit: outside, want: "within the Project root"},
		{name: "root directory", explicit: ".", want: "identify a file"},
		{name: "nul", explicit: "deploy/\x00.yaml", want: "NUL byte"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveConfigurationSelector(root, test.explicit, test.environment)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("resolveConfigurationSelector error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReadManifestSnapshotSupportsNestedConfigurationAndRejectsSymbolicComponents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "deploy"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "deploy", "customer.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	snapshot, manifest, err := loadConfiguration(root, "deploy/customer.yaml")
	if err != nil || snapshot.Path() != "deploy/customer.yaml" || manifest.StartupTimeout() <= 0 {
		t.Fatalf("loadConfiguration = path %q, manifest %#v, error %v", snapshot.Path(), manifest, err)
	}
	if err := os.Symlink(filepath.Join(root, "deploy"), filepath.Join(root, "linked")); err != nil {
		t.Skipf("symbolic-link creation unavailable: %v", err)
	}
	if _, _, err := loadConfiguration(root, "linked/customer.yaml"); err == nil || !strings.Contains(err.Error(), "symbolic path component") {
		t.Fatalf("loadConfiguration(symbolic component) error = %v", err)
	}
}

func TestManifestPathStateComparisonDetectsIntermediateDirectoryReplacement(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	configurationDirectory := filepath.Join(projectRoot, "deploy")
	configurationPath := filepath.Join(configurationDirectory, "customer.yaml")
	if err := os.MkdirAll(configurationDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configurationPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	root, err := os.OpenRoot(projectRoot)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	defer root.Close()
	before, err := inspectManifestPath(root, filepath.FromSlash("deploy/customer.yaml"))
	if err != nil {
		t.Fatalf("inspectManifestPath(before): %v", err)
	}

	displacedDirectory := filepath.Join(projectRoot, "deploy-before")
	if err := os.Rename(configurationDirectory, displacedDirectory); err != nil {
		t.Fatalf("Rename directory: %v", err)
	}
	if err := os.Mkdir(configurationDirectory, 0o755); err != nil {
		t.Fatalf("Mkdir replacement directory: %v", err)
	}
	if err := os.Rename(filepath.Join(displacedDirectory, "customer.yaml"), configurationPath); err != nil {
		t.Fatalf("Move original configuration into replacement directory: %v", err)
	}
	after, err := inspectManifestPath(root, filepath.FromSlash("deploy/customer.yaml"))
	if err != nil {
		t.Fatalf("inspectManifestPath(after): %v", err)
	}
	if !sameFile(before[len(before)-1].info, after[len(after)-1].info) {
		t.Fatal("test setup replaced the final file identity")
	}
	if sameManifestPathStates(before, after) {
		t.Fatal("sameManifestPathStates accepted an intermediate directory replacement")
	}
}

func TestManifestSnapshotComparisonDetectsIntermediateDirectoryReplacement(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	configurationDirectory := filepath.Join(projectRoot, "deploy")
	configurationPath := filepath.Join(configurationDirectory, "customer.yaml")
	if err := os.MkdirAll(configurationDirectory, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(configurationPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	before, _, err := loadConfiguration(projectRoot, "deploy/customer.yaml")
	if err != nil {
		t.Fatalf("loadConfiguration(before): %v", err)
	}

	displacedDirectory := filepath.Join(projectRoot, "deploy-before")
	if err := os.Rename(configurationDirectory, displacedDirectory); err != nil {
		t.Fatalf("Rename directory: %v", err)
	}
	if err := os.Mkdir(configurationDirectory, 0o755); err != nil {
		t.Fatalf("Mkdir replacement directory: %v", err)
	}
	if err := os.Rename(filepath.Join(displacedDirectory, "customer.yaml"), configurationPath); err != nil {
		t.Fatalf("Move original configuration into replacement directory: %v", err)
	}
	after, _, err := loadConfiguration(projectRoot, "deploy/customer.yaml")
	if err != nil {
		t.Fatalf("loadConfiguration(after): %v", err)
	}
	if !sameFile(before.file, after.file) {
		t.Fatal("test setup replaced the final file identity")
	}
	if sameManifestSnapshot(before, after) {
		t.Fatal("sameManifestSnapshot accepted an intermediate directory replacement")
	}
}
