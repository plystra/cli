package applicationgenerate_test

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationgenerate"
)

func TestGenerateIsDeterministicAcrossEffectiveGraphPermutations(t *testing.T) {
	orders := modulePermutations([]string{"a", "b", "c"})
	if len(orders) != 6 {
		t.Fatalf("module permutations = %d, want 6", len(orders))
	}

	var expectedConfiguration []byte
	var expectedGenerated []treeEntry
	var expectedDependencyDigest string
	var expectedApplicationDigest string
	for index, order := range orders {
		root := t.TempDir()
		appRoot := filepath.Join(root, "app")
		dependencyRoots := make(map[string]string, len(order))
		for position, name := range order {
			dependencyRoots[name] = filepath.Join(root, fmt.Sprintf("replacement-%d", position))
		}

		writeModule(t, dependencyRoots["b"], "example.com/platform/b", "")
		writeFile(t, filepath.Join(dependencyRoots["b"], "plystra.yaml"), "capabilities: {require: [kernel.health/v1]}\n")
		writeFile(t, filepath.Join(dependencyRoots["b"], "plystra.production.yaml"), "capabilities: {require: [ignored.environment/v1]}\n")

		writeModule(t, dependencyRoots["c"], "example.com/platform/c", "")
		writeFile(t, filepath.Join(dependencyRoots["c"], "plystra.yaml"), "capabilities: {require: [kernel.info/v1]}\n")

		writeModule(t, dependencyRoots["a"], "example.com/platform/a", "require example.com/platform/b v1.0.0\n")
		configurationOwner := writeConstructorConfigurationOwner(t, dependencyRoots["a"], "example.com/platform/a", false)
		writeFile(t, filepath.Join(dependencyRoots["a"], "plystra.yaml"), fmt.Sprintf(`interfaces:
  require: [configuration.owner/v1]
http:
  expose: [email.send/v1]
capabilities:
  require: [email.send/v1]
  use: {email.send/v1: example.smtp}
  aliases: {mail.send/v1: email.send/v1}
config:
  %s: {endpoint: smtp.example}
`, configurationOwner))
		writePlugin(t, dependencyRoots["a"], "smtp", "id: example.smtp\nprovides: [email.send/v1]\nconfig:\n  endpoint: {type: string}\n")
		writeCapability(t, dependencyRoots["a"], "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")

		writeConnectApplicationModule(t, appRoot, "example.com/acme/permutation-app")
		goModPath := filepath.Join(appRoot, "go.mod")
		goMod := string(readAbsoluteFile(t, goModPath)) + permutationModuleDirectives(order, dependencyRoots)
		writeFile(t, goModPath, goMod)
		writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "# stable current Project\nhttp: {address: \":8080\"}\n")

		result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
			Start:       appRoot,
			Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
			Validate:    func(context.Context, string) error { return nil },
		})
		if err != nil || !result.ConfigurationChanged() || !result.Report().Clean() {
			t.Fatalf("Generate(permutation %d %v) = configuration changed %t, report %#v, %v", index, order, result.ConfigurationChanged(), result.Report().Changes(), err)
		}
		configuration := readFile(t, appRoot, "plystra.yaml")
		generated := snapshotGenerated(t, appRoot)
		provenance, err := applicationgen.DecodeManifestProvenance(readFile(t, appRoot, "generated/manifest.json"))
		if err != nil {
			t.Fatalf("DecodeManifestProvenance(permutation %d): %v", index, err)
		}
		dependencyDigest := provenance.DependencyBaseline().Digest()
		applicationDigest := provenance.ApplicationModelDigest()
		if dependencyDigest == "" || applicationDigest == "" {
			t.Fatalf("permutation %d has empty provenance digests", index)
		}
		sources := baselineSources(provenance)
		for _, modulePath := range []string{"example.com/platform/a@v1.0.0", "example.com/platform/b@v1.0.0", "example.com/platform/c@v1.0.0"} {
			if !strings.Contains(sources, modulePath) {
				t.Fatalf("permutation %d baseline omits %s: %s", index, modulePath, sources)
			}
		}
		if strings.Contains(string(configuration), "ignored.environment/v1") {
			t.Fatalf("permutation %d inherited a dependency environment overlay:\n%s", index, configuration)
		}

		if index == 0 {
			expectedConfiguration = configuration
			expectedGenerated = generated
			expectedDependencyDigest = dependencyDigest
			expectedApplicationDigest = applicationDigest
			continue
		}
		if !reflect.DeepEqual(configuration, expectedConfiguration) {
			t.Fatalf("permutation %d changed maintained configuration:\nwant:\n%s\ngot:\n%s", index, expectedConfiguration, configuration)
		}
		if !reflect.DeepEqual(generated, expectedGenerated) {
			t.Fatalf("permutation %d changed generated tree:\nwant: %#v\ngot:  %#v", index, expectedGenerated, generated)
		}
		if dependencyDigest != expectedDependencyDigest || applicationDigest != expectedApplicationDigest {
			t.Fatalf("permutation %d changed digests: dependency %s application %s; want %s %s", index, dependencyDigest, applicationDigest, expectedDependencyDigest, expectedApplicationDigest)
		}
	}
}

func permutationModuleDirectives(order []string, roots map[string]string) string {
	var result strings.Builder
	result.WriteString("\nrequire (\n")
	for _, name := range order {
		if name == "b" {
			continue
		}
		fmt.Fprintf(&result, "\texample.com/platform/%s v1.0.0\n", name)
	}
	result.WriteString(")\n\nreplace (\n")
	for _, name := range order {
		fmt.Fprintf(&result, "\texample.com/platform/%s => %s\n", name, filepath.ToSlash(roots[name]))
	}
	result.WriteString(")\n")
	return result.String()
}

func baselineSources(provenance applicationgen.ManifestProvenance) string {
	var sources []string
	for _, record := range provenance.DependencyBaseline().Records() {
		sources = append(sources, record.Sources...)
	}
	return strings.Join(sources, "\n")
}

func modulePermutations(values []string) [][]string {
	working := append([]string(nil), values...)
	result := make([][]string, 0, 6)
	var visit func(int)
	visit = func(index int) {
		if index == len(working) {
			result = append(result, append([]string(nil), working...))
			return
		}
		for candidate := index; candidate < len(working); candidate++ {
			working[index], working[candidate] = working[candidate], working[index]
			visit(index + 1)
			working[index], working[candidate] = working[candidate], working[index]
		}
	}
	visit(0)
	return result
}
