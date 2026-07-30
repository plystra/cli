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

func TestGenerateProducesByteIdenticalOutputForEqualNormalizedInputs(t *testing.T) {
	const modulePath = "example.com/acme/normalized-inputs"
	roots := []string{
		filepath.Join(t.TempDir(), "application"),
		filepath.Join(t.TempDir(), "application"),
	}
	for index, root := range roots {
		writeNormalizedInputProject(t, root, modulePath, index)
	}
	environment := goEnvironment(map[string]string{
		"GOWORK":  "off",
		"GOPROXY": "off",
		"GOSUMDB": "off",
		"GOFLAGS": "-mod=readonly",
	})
	cases := []struct {
		name        string
		selected    string
		overlay     bool
		environment string
		config      string
		prepare     func(testing.TB, string, int)
	}{
		{
			name:     "default",
			selected: "plystra.yaml",
			prepare:  func(testing.TB, string, int) {},
		},
		{
			name:        "environment",
			selected:    "plystra.production.yaml",
			overlay:     true,
			environment: "production",
			prepare: func(t testing.TB, root string, variant int) {
				t.Helper()
				writeFile(t, filepath.Join(root, "plystra.production.yaml"), normalizedEnvironmentConfiguration(modulePath, variant))
			},
		},
		{
			name:     "explicit config",
			selected: "deploy/customer.yaml",
			config:   "deploy/customer.yaml",
			prepare: func(t testing.TB, root string, variant int) {
				t.Helper()
				writeFile(t, filepath.Join(root, "deploy", "customer.yaml"), normalizedExplicitConfiguration(modulePath, variant))
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for index, root := range roots {
				test.prepare(t, root, index)
			}
			var expectedGenerated []treeEntry
			var expectedIdentity normalizedGenerationIdentity
			rawDigests := make([]string, len(roots))
			for index, root := range roots {
				options := applicationgenerate.Options{
					Start:             root,
					EnvironmentName:   test.environment,
					ConfigurationPath: test.config,
					Environment:       environment,
					Validate:          func(context.Context, string) error { return nil },
				}
				result, err := applicationgenerate.Generate(t.Context(), options)
				if err != nil || !result.Report().Clean() {
					t.Fatalf("Generate(%s, variant %d) = report %#v, %v", test.name, index, result.Report().Changes(), err)
				}
				generated := snapshotGenerated(t, root)
				provenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
				if err != nil {
					t.Fatalf("DecodeManifestProvenance(%s, variant %d): %v", test.name, index, err)
				}
				identity := normalizedGenerationIdentity{
					rootDigest:        provenance.RootDigest(),
					selectedDigest:    provenance.SelectedDigest(),
					dependencyDigest:  provenance.DependencyBaseline().Digest(),
					applicationDigest: provenance.ApplicationModelDigest(),
					interfaceDigest:   provenance.InterfaceProvenance().Digest(),
					toolchainDigest:   provenance.TransportToolchain().Digest(),
				}
				selectedData := readFile(t, root, test.selected)
				digestFunction := applicationgen.ConfigurationDigest
				if test.overlay {
					digestFunction = applicationgen.EnvironmentOverlayDigest
				}
				rawDigests[index], err = digestFunction(selectedData)
				if err != nil {
					t.Fatalf("raw configuration digest(%s, variant %d): %v", test.name, index, err)
				}

				beforeCheck := snapshotTree(t, root)
				checkOptions := options
				checkOptions.Check = true
				checkOptions.Validate = nil
				check, err := applicationgenerate.Generate(t.Context(), checkOptions)
				if err != nil || !check.Checked() || check.ConfigurationChanged() || !check.Report().Clean() {
					t.Fatalf("Generate --check(%s, variant %d) = checked %t configuration changed %t report %#v, %v", test.name, index, check.Checked(), check.ConfigurationChanged(), check.Report().Changes(), err)
				}
				if afterCheck := snapshotTree(t, root); !reflect.DeepEqual(afterCheck, beforeCheck) {
					t.Fatalf("Generate --check(%s, variant %d) modified the Project", test.name, index)
				}

				if index == 0 {
					expectedGenerated = generated
					expectedIdentity = identity
					continue
				}
				if !reflect.DeepEqual(generated, expectedGenerated) {
					t.Fatalf("%s equivalent normalized inputs changed generated bytes or modes:\nwant: %#v\ngot:  %#v", test.name, expectedGenerated, generated)
				}
				if identity != expectedIdentity {
					t.Fatalf("%s equivalent normalized inputs changed generated identity:\nwant: %#v\ngot:  %#v", test.name, expectedIdentity, identity)
				}
			}
			if rawDigests[0] == rawDigests[1] {
				t.Fatalf("%s fixture did not exercise presentation-sensitive raw YAML digests", test.name)
			}
		})
	}
}

type normalizedGenerationIdentity struct {
	rootDigest        string
	selectedDigest    string
	dependencyDigest  string
	applicationDigest string
	interfaceDigest   string
	toolchainDigest   string
}

func writeNormalizedInputProject(t testing.TB, root, modulePath string, variant int) {
	t.Helper()
	writeConnectApplicationModule(t, root, modulePath)
	interfaceSource := `package ownerv1

import "context"

//plystra:interface configuration.owner/v1
type Interface interface {
	Load(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`
	implementationSource := `package configowner

import (
	"context"
	"time"

	ownerv1 "` + modulePath + `/interfaces/configuration/owner/v1"
)

type Config struct {
	Delay time.Duration
	Endpoint string
	Targets []string
}

type Service struct{}

//plystra:implements configuration.owner/v1
func New(Config) (*Service, error) { return &Service{}, nil }

func (*Service) Load(context.Context, ownerv1.Request) (ownerv1.Response, error) {
	return ownerv1.Response{}, nil
}
`
	if variant == 0 {
		writeFile(t, filepath.Join(root, "interfaces", "configuration", "owner", "v1", "interface.go"), interfaceSource)
		writeFile(t, filepath.Join(root, "configowner", "implementation.go"), implementationSource)
	} else {
		writeFile(t, filepath.Join(root, "configowner", "implementation.go"), strings.ReplaceAll(implementationSource, "\n", "\r\n"))
		writeFile(t, filepath.Join(root, "interfaces", "configuration", "owner", "v1", "interface.go"), strings.ReplaceAll(interfaceSource, "\n", "\r\n"))
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), normalizedRootConfiguration(modulePath, variant))
}

func normalizedRootConfiguration(modulePath string, variant int) string {
	constructor := modulePath + "/configowner.New"
	if variant == 0 {
		return `# equivalent normalized root input
interfaces:
  require: [configuration.owner/v1, kernel.health/v1]
  use: {configuration.owner/v1: ` + constructor + `}
  policies:
    configuration.owner/v1: {timeout: 5s}
http:
  address: ":8080"
  transports: {connect: true, rest: false}
  cors:
    allowed_origins: [https://B.example:443, https://a.example, https://a.example:443]
    allow_credentials: false
  expose: [configuration.owner/v1, kernel.health/v1]
timeouts: {startup: 5s}
config:
  ` + constructor + `:
    delay: 5s
    endpoint: service.example
    targets: [primary, secondary]
`
	}
	return strings.ReplaceAll(`config:
  `+constructor+`: {targets: [primary, secondary], endpoint: "service.example", delay: 5000ms}
timeouts:
  startup: 5000ms
http:
  expose: [kernel.health/v1, configuration.owner/v1]
  cors: {allow_credentials: false, allowed_origins: [https://a.example:443, https://b.example]}
  transports:
    rest: false
    connect: true
  address: ':8080'
interfaces:
  policies:
    configuration.owner/v1:
      timeout: 5000ms
  use:
    configuration.owner/v1: `+constructor+`
  require: [kernel.health/v1, configuration.owner/v1]
`, "\n", "\r\n")
}

func normalizedEnvironmentConfiguration(modulePath string, variant int) string {
	constructor := modulePath + "/configowner.New"
	if variant == 0 {
		return `# sparse production differences
interfaces:
  policies: {configuration.owner/v1: {timeout: 7s}}
http:
  cors:
    allowed_origins: [https://B.example:443, https://a.example]
config:
  ` + constructor + `: {delay: 7s}
timeouts: {startup: 7s}
`
	}
	return strings.ReplaceAll(`timeouts:
  startup: 7000ms
config:
  `+constructor+`:
    delay: 7000ms
http:
  cors: {allowed_origins: [https://a.example:443, https://b.example]}
interfaces:
  policies:
    configuration.owner/v1: {timeout: 7000ms}
`, "\n", "\r\n")
}

func normalizedExplicitConfiguration(modulePath string, variant int) string {
	constructor := modulePath + "/configowner.New"
	if variant == 0 {
		return `# complete customer configuration
interfaces:
  require: [configuration.owner/v1, kernel.health/v1]
  use: {configuration.owner/v1: ` + constructor + `}
  policies: {configuration.owner/v1: {timeout: 9s}}
http:
  address: ":9090"
  transports: {connect: true, rest: false}
  cors:
    allowed_origins: [https://CUSTOMER.example:443, https://admin.customer.example]
    allow_credentials: false
  expose: [configuration.owner/v1, kernel.health/v1]
timeouts: {startup: 9s}
config:
  ` + constructor + `:
    delay: 9s
    endpoint: customer.example
    targets: [primary, secondary]
`
	}
	return strings.ReplaceAll(`config:
  `+constructor+`: {endpoint: "customer.example", targets: [primary, secondary], delay: 9000ms}
timeouts: {startup: 9000ms}
http:
  cors: {allow_credentials: false, allowed_origins: [https://admin.customer.example, https://customer.example]}
  expose: [kernel.health/v1, configuration.owner/v1]
  address: ':9090'
  transports:
    rest: false
    connect: true
interfaces:
  use: {configuration.owner/v1: `+constructor+`}
  policies:
    configuration.owner/v1: {timeout: 9000ms}
  require: [kernel.health/v1, configuration.owner/v1]
`, "\n", "\r\n")
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
