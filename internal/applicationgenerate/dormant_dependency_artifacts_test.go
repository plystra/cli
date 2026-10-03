package applicationgenerate_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/resolutionevidence"
	"go.yaml.in/yaml/v3"
)

func TestGenerateIsolatesDormantDependencyArtifactProvenance(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"default", "environment", "explicit-config"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			const dependencyModule = "example.com/platform/dormant-artifacts"
			root := t.TempDir()
			dependencyRoot := filepath.Join(root, "platform")
			applicationRoot := filepath.Join(root, "application")
			writeApplicationModule(t, dependencyRoot, dependencyModule)
			constructor := writeConstructorConfigurationOwner(t, dependencyRoot, dependencyModule, true)
			writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
			writeConnectApplicationModule(t, applicationRoot, "example.com/acme/dormant-artifacts")
			goModPath := filepath.Join(applicationRoot, "go.mod")
			writeFile(t, goModPath, string(readAbsoluteFile(t, goModPath))+fmt.Sprintf(
				"\nrequire %s v1.0.0\n\nreplace %s => %s\n", dependencyModule, dependencyModule, filepath.ToSlash(dependencyRoot)))
			selectedPath := "plystra.yaml"
			options := applicationgenerate.Options{
				Start:       applicationRoot,
				Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
				Validate:    func(context.Context, string) error { return nil },
			}
			switch mode {
			case "environment":
				options.EnvironmentName = "test"
				selectedPath = "plystra.test.yaml"
				writeFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "{}\n")
			case "explicit-config":
				options.ConfigurationPath = "deploy/test.yaml"
				selectedPath = options.ConfigurationPath
				writeFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "{}\n")
			}
			const exposure = "http: {expose: {kernel.health/v1: {transport: connect}}}\n"
			relationship := fmt.Sprintf("template: %s\n", dependencyModule)
			writeFile(t, filepath.Join(applicationRoot, "plystra.yaml"), relationship)
			selectedMetadata := ""
			if selectedPath == "plystra.yaml" {
				selectedMetadata = relationship
			}
			writeFile(t, filepath.Join(applicationRoot, selectedPath), selectedMetadata+exposure)
			generate := func() applicationgen.ManifestProvenance {
				t.Helper()
				dependencyBefore := snapshotTree(t, dependencyRoot)
				result, err := applicationgenerate.Generate(t.Context(), options)
				if err != nil || !result.Report().Clean() {
					t.Fatalf("Generate = changes %#v, %v", result.Report().Changes(), err)
				}
				if !reflect.DeepEqual(snapshotTree(t, dependencyRoot), dependencyBefore) {
					t.Fatal("generation mutated dependency source")
				}
				assertNoTransactions(t, applicationRoot)
				provenance, err := applicationgen.DecodeManifestProvenance(readFile(t, applicationRoot, generatedfiles.ApplicationManifestPath))
				if err != nil {
					t.Fatalf("DecodeManifestProvenance: %v", err)
				}
				return provenance
			}
			baseline := generate()
			baselineArtifacts := snapshotExecutablePublicArtifactEvidence(t, applicationRoot)
			for _, prefix := range []string{"generated/go/bootstrap/", "generated/go/adapters/connect/", "generated/sdk/javascript/", "generated/docs/"} {
				if !slices.ContainsFunc(baselineArtifacts, func(artifact artifactEvidenceSnapshot) bool { return strings.HasPrefix(artifact.path, prefix) }) {
					t.Fatalf("fixture has no %s artifact", prefix)
				}
			}
			previousDigest := baseline.DependencyBaseline().Digest()
			emptyTemplate := "{}\n"
			selection := fmt.Sprintf("interfaces: {use: {configuration.owner/v1: %s}}\n", constructor)
			selectionAndConfiguration := selection + fmt.Sprintf("config: {%s: {endpoint: private.internal, password: {env: PLYSTRA_DORMANT_DEPENDENCY_SECRET}}}\n", constructor)
			for _, source := range []string{selection, selectionAndConfiguration, selection, emptyTemplate} {
				writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), source)
				beforeCheck := snapshotTree(t, root)
				checkOptions := options
				checkOptions.Check = true
				checked, err := applicationgenerate.Generate(t.Context(), checkOptions)
				if err != nil || !checked.Checked() || checked.Report().Clean() {
					t.Fatalf("Check dormant dependency change = %#v, %v", checked.Report().Changes(), err)
				}
				if !reflect.DeepEqual(snapshotTree(t, root), beforeCheck) {
					t.Fatal("generation check mutated source or generated files")
				}
				for _, change := range checked.Report().Changes() {
					if change.Path() != generatedfiles.ManifestPath && change.Path() != generatedfiles.ApplicationManifestPath {
						t.Fatalf("dormant dependency changed executable/public artifact: %#v", change)
					}
				}
				provenance := generate()
				if provenance.ApplicationModelDigest() != baseline.ApplicationModelDigest() {
					t.Fatal("dormant dependency changed executable application-model digest")
				}
				if provenance.DependencyBaseline().Digest() == previousDigest {
					t.Fatal("dormant dependency change did not update composition provenance")
				}
				previousDigest = provenance.DependencyBaseline().Digest()
				if len(provenance.InterfaceProvenance().Bindings()) != 0 || len(provenance.InterfaceProvenance().Constructors()) != 0 {
					t.Fatal("dormant dependency entered executable binding or constructor provenance")
				}
				artifacts := snapshotExecutablePublicArtifactEvidence(t, applicationRoot)
				if !reflect.DeepEqual(artifacts, baselineArtifacts) {
					for index, artifact := range artifacts {
						if index >= len(baselineArtifacts) || !reflect.DeepEqual(artifact, baselineArtifacts[index]) {
							t.Fatalf("dormant dependency changed artifact provenance for %s: sources %v", artifact.path, artifact.sources)
						}
					}
					t.Fatal("dormant dependency changed executable/public artifact count")
				}
				manifestArtifact, exists, err := generatedfiles.ReadArtifact(applicationRoot, generatedfiles.ApplicationManifestPath)
				if err != nil || !exists {
					t.Fatalf("ReadArtifact(manifest) = exists %t, %v", exists, err)
				}
				for _, record := range provenance.DependencyBaseline().Records() {
					for _, source := range record.Sources {
						if !slices.Contains(manifestArtifact.Sources(), source) {
							t.Fatalf("manifest artifact lost configuration source %s", source)
						}
					}
				}
				for _, name := range []string{generatedfiles.ManifestPath, generatedfiles.ApplicationManifestPath} {
					data := readFile(t, applicationRoot, name)
					for _, forbidden := range []string{"private.internal", "PLYSTRA_DORMANT_DEPENDENCY_SECRET"} {
						if bytes.Contains(data, []byte(forbidden)) {
							t.Fatalf("%s leaked %s", name, forbidden)
						}
					}
				}
			}

			writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), selectionAndConfiguration)
			writeFile(t, filepath.Join(applicationRoot, selectedPath), selectedMetadata+exposure+"interfaces: {require: [configuration.owner/v1]}\n")
			active := generate()
			bindings := active.InterfaceProvenance().Bindings()
			if len(bindings) != 1 || bindings[0].Selection().Constructor() != constructor {
				t.Fatalf("activated dependency binding = %#v", bindings)
			}
			if active.ApplicationModelDigest() == baseline.ApplicationModelDigest() || len(active.DormantImplementationSelections()) != 0 || len(active.DormantConstructorConfigurations()) != 0 {
				t.Fatal("activation did not promote dormant intent into executable provenance")
			}
			selectionSource := dependencyModule + `@v1.0.0/plystra.yaml interfaces.use["configuration.owner/v1"]`
			configurationSource := fmt.Sprintf("%s@v1.0.0/plystra.yaml config[%q]", dependencyModule, constructor)
			for _, name := range []string{bindings[0].Mappings().ProxyPath(), bindings[0].Mappings().AssemblyPath(), "generated/go/bootstrap/bootstrap_gen.go"} {
				artifact, exists, err := generatedfiles.ReadArtifact(applicationRoot, name)
				if err != nil || !exists || !slices.Contains(artifact.Sources(), selectionSource) || !slices.Contains(artifact.Sources(), configurationSource) {
					t.Fatalf("active artifact %s lost selection or configuration provenance: %v, %v", name, artifact.Sources(), err)
				}
			}
			var selected yaml.Node
			if err := yaml.Unmarshal(readFile(t, applicationRoot, selectedPath), &selected); err != nil {
				t.Fatalf("decode selected configuration for deactivation: %v", err)
			}
			for index, field := range selected.Content[0].Content {
				if index%2 != 0 || field.Value != "interfaces" {
					continue
				}
				fields := selected.Content[0].Content[index+1].Content
				for index, field := range fields {
					if index%2 == 0 && field.Value == "require" {
						fields[index+1].Content = nil
					}
				}
			}
			deactivated, err := yaml.Marshal(&selected)
			if err != nil {
				t.Fatalf("encode deactivated configuration: %v", err)
			}
			writeFile(t, filepath.Join(applicationRoot, selectedPath), string(deactivated))
			inactive := generate()
			if inactive.ApplicationModelDigest() != baseline.ApplicationModelDigest() || !reflect.DeepEqual(snapshotExecutablePublicArtifactEvidence(t, applicationRoot), baselineArtifacts) {
				t.Fatal("deactivation did not restore executable/public artifact provenance")
			}
			beforeCheck := snapshotTree(t, root)
			options.Check = true
			checked, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil || !checked.Checked() || !checked.Report().Clean() || checked.ConfigurationChanged() {
				t.Fatalf("stable dormant dependency check = %#v, %v", checked.Report().Changes(), err)
			}
			if !reflect.DeepEqual(snapshotTree(t, root), beforeCheck) {
				t.Fatal("stable dormant dependency check mutated Project")
			}
		})
	}
}

func TestGenerateKeepsDormantTemplateDeclarationOrder(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			const module = "example.com/dormant-history"
			const oldest = "example.com/z-oldest"
			const nearest = "example.com/a-nearest"
			root := t.TempDir()
			writeApplicationModule(t, root, module)
			constructor := writeConstructorConfigurationOwner(t, root, module, true)
			declarations := fmt.Sprintf("interfaces: {use: {configuration.owner/v1: %s}}\nconfig: {%s: {endpoint: private-equal, password: {env: PRIVATE_TEMPLATE_HISTORY_SECRET}}}\n", constructor, constructor)
			for _, name := range []string{oldest, nearest} {
				templateRoot := t.TempDir()
				writeModule(t, templateRoot, name, "")
				document := declarations
				if name == nearest {
					document = "template: " + oldest + "\n" + document
				}
				writeFile(t, filepath.Join(templateRoot, "plystra.yaml"), document)
				writeFile(t, filepath.Join(root, "go.mod"), string(readFile(t, root, "go.mod"))+"\nrequire "+name+" v1.0.0\nreplace "+name+" => "+filepath.ToSlash(templateRoot)+"\n")
			}
			writeFile(t, filepath.Join(root, "plystra.yaml"), "template: "+nearest+"\n")
			selected := "plystra.yaml"
			options := applicationgenerate.Options{Start: root, Environment: goEnvironment(nil), Validate: func(context.Context, string) error { return nil }}
			switch mode {
			case "environment":
				selected, options.EnvironmentName = "plystra.test.yaml", "test"
			case "replacement":
				selected, options.ConfigurationPath = "selected.yaml", "selected.yaml"
			}
			if selected != "plystra.yaml" {
				writeFile(t, filepath.Join(root, selected), "{}\n")
			}
			var beforeArtifacts []artifactEvidenceSnapshot
			var beforeDigest string
			for _, current := range []bool{false, true} {
				if current {
					document := declarations
					if selected == "plystra.yaml" {
						document = "template: " + nearest + "\n" + document
					}
					writeFile(t, filepath.Join(root, selected), document)
				}
				if _, err := applicationgenerate.Generate(t.Context(), options); err != nil {
					t.Fatal(err)
				}
				data := readFile(t, root, generatedfiles.ApplicationManifestPath)
				provenance, err := applicationgen.DecodeManifestProvenance(data)
				if err != nil {
					t.Fatal(err)
				}
				selections := provenance.DormantImplementationSelections()
				if len(selections) != 1 {
					t.Fatalf("dormant selections = %d, want 1", len(selections))
				}
				field := dormantConfigurationField(t, onlyDormantConstructorConfiguration(t, provenance, constructor), fmt.Sprintf("config[%q][\"endpoint\"]", constructor))
				for _, contributions := range [][]applicationgen.DormantSelectionContribution{selections[0].Contributions(), field.Contributions()} {
					wantCount := 2
					if current {
						wantCount++
					}
					if len(contributions) != wantCount {
						t.Fatalf("equal declarations collapsed: got %d contributions, want %d", len(contributions), wantCount)
					}
					for index, contribution := range contributions {
						wantModule, wantPath, wantOwner, wantPrecedence, wantOrder := module, selected, string(resolutionevidence.ConfigurationOwnerRoot), 2, 0
						if index < 2 {
							wantModule = []string{oldest, nearest}[index]
							wantPath, wantOwner, wantPrecedence, wantOrder = "plystra.yaml", string(resolutionevidence.ConfigurationOwnerTemplate), 1, index+1
						} else if mode == "environment" {
							wantOwner, wantPrecedence = string(resolutionevidence.ConfigurationOwnerEnvironment), 3
						} else if mode == "replacement" {
							wantOwner = string(resolutionevidence.ConfigurationOwnerExplicit)
						}
						sources := contribution.Sources()
						if contribution.TemplateOrder() != wantOrder || contribution.Precedence() != wantPrecedence || contribution.Owner() != wantOwner || contribution.Effective() != (index == wantCount-1) || len(sources) != 1 || sources[0].Module() != wantModule || sources[0].Path() != wantPath {
							t.Fatalf("contribution %d lost declaration order, ownership, or source: %#v", index, contribution)
						}
					}
				}
				for _, value := range []string{"private-equal", "PRIVATE_TEMPLATE_HISTORY_SECRET"} {
					if bytes.Contains(data, []byte(value)) {
						t.Fatal("dormant history exposed private values")
					}
				}
				if !bytes.Contains(data, []byte(`"template_order":1`)) || !bytes.Contains(data, []byte(`"template_order":2`)) {
					t.Fatal("public manifest omitted template order")
				}
				if !current {
					beforeArtifacts, beforeDigest = snapshotExecutablePublicArtifactEvidence(t, root), provenance.ApplicationModelDigest()
				} else if beforeDigest != provenance.ApplicationModelDigest() || !reflect.DeepEqual(beforeArtifacts, snapshotExecutablePublicArtifactEvidence(t, root)) {
					t.Fatal("dormant declaration history changed executable output")
				}
			}
		})
	}
}
