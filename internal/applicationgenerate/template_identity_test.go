package applicationgenerate_test

import (
	"bytes"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/runtimebaseline"
)

func TestGenerateEquivalentTemplatesPreserveExecutableIdentityAndRelinkRuntimeContract(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			const module = "example.com/equivalent-templates"
			root := t.TempDir()
			writeApplicationModule(t, root, module)
			owner := writeConstructorConfigurationOwner(t, root, module, false)
			fragment := "interfaces: {require: [configuration.owner/v1]}\nconfig: {" + owner + ": {label: private-value}}\n"
			goMod := string(readFile(t, root, "go.mod"))
			for _, name := range []string{"first", "equivalent"} {
				dependency := t.TempDir()
				path := "example.com/templates/" + name
				writeModule(t, dependency, path, "")
				writeFile(t, filepath.Join(dependency, "plystra.yaml"), fragment)
				goMod += "\nrequire " + path + " v1.0.0\nreplace " + path + " => " + filepath.ToSlash(dependency) + "\n"
			}
			writeFile(t, filepath.Join(root, "go.mod"), goMod)
			selectedPath := "plystra.yaml"
			args := []string{"generate"}
			options := applicationgenerate.Options{Start: root, Environment: goEnvironment(nil), Check: true}
			switch mode {
			case "environment":
				selectedPath, options.EnvironmentName = "plystra.test.yaml", "test"
				args = append(args, "--env", "test")
			case "replacement":
				selectedPath, options.ConfigurationPath = "selected.yaml", "selected.yaml"
				args = append(args, "--config", "selected.yaml")
			}
			writeSelection := func(name string) {
				t.Helper()
				writeFile(t, filepath.Join(root, "plystra.yaml"), "template: example.com/templates/"+name+"\n")
				if mode != "default" {
					writeFile(t, filepath.Join(root, selectedPath), "{}\n")
				}
			}
			generate := func() applicationgen.ManifestProvenance {
				t.Helper()
				var stdout, stderr bytes.Buffer
				if code := command.RunIn(args, &stdout, &stderr, root, options.Environment); code != 0 {
					t.Fatalf("generate = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
				}
				manifest, err := applicationgen.DecodeManifestProvenance(readFile(t, root, generatedfiles.ApplicationManifestPath))
				if err != nil {
					t.Fatal(err)
				}
				return manifest
			}
			artifacts := func() map[string]string {
				result := make(map[string]string)
				for _, entry := range snapshotGenerated(t, root) {
					if entry.mode.IsRegular() && entry.path != generatedfiles.ManifestPath && entry.path != generatedfiles.ApplicationManifestPath && entry.path != "generated/go/bootstrap/bootstrap_gen.go" {
						result[entry.path] = string(entry.data)
					}
				}
				return result
			}
			writeSelection("first")
			before := generate()
			beforeArtifacts := artifacts()
			beforeBootstrap := readFile(t, root, "generated/go/bootstrap/bootstrap_gen.go")
			baseline, err := runtimebaseline.Decode(readFile(t, root, "dist/runtime-baseline.json"))
			if err != nil {
				t.Fatal(err)
			}
			writeSelection("equivalent")
			rootSource := readFile(t, root, "plystra.yaml")
			selectedSource := readFile(t, root, selectedPath)
			snapshot := snapshotTree(t, root)
			checked, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil || checked.Report().Clean() {
				t.Fatalf("equivalent template check = %v", err)
			}
			for _, change := range checked.Report().Changes() {
				if change.Path() != generatedfiles.ManifestPath && change.Path() != generatedfiles.ApplicationManifestPath && change.Path() != "generated/go/bootstrap/bootstrap_gen.go" {
					t.Errorf("equivalent template changed unrelated artifact %s", change.Path())
				}
			}
			if !reflect.DeepEqual(snapshot, snapshotTree(t, root)) {
				t.Fatal("generation check mutated files")
			}
			after := generate()
			if before.ApplicationModelDigest() != after.ApplicationModelDigest() || !reflect.DeepEqual(beforeArtifacts, artifacts()) {
				t.Fatal("equivalent template changed the executable model or unrelated public artifacts")
			}
			if before.DependencyBaseline().Digest() == after.DependencyBaseline().Digest() {
				t.Fatal("equivalent template lost ancestry provenance")
			}
			refreshed, err := runtimebaseline.Decode(readFile(t, root, "dist/runtime-baseline.json"))
			if err != nil || baseline.ContractID == refreshed.ContractID || bytes.Equal(beforeBootstrap, readFile(t, root, "generated/go/bootstrap/bootstrap_gen.go")) {
				t.Fatal("changed ancestry did not relink the public runtime contract and generated bootstrap")
			}
			for _, document := range []runtimebaseline.Document{baseline, refreshed} {
				if bytes.Contains(document.Contract, []byte("private-value")) {
					t.Fatal("runtime contract exposed private template configuration")
				}
			}
			if !bytes.Equal(rootSource, readFile(t, root, "plystra.yaml")) || !bytes.Equal(selectedSource, readFile(t, root, selectedPath)) {
				t.Fatal("generation rewrote the authored template relationship")
			}
		})
	}
}
