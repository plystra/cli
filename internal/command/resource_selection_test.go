package command_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestBuiltCLISelectsResourceProvider(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(map[string]string{"GOFLAGS": os.Getenv("GOFLAGS")})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root := writeResourceSelectionCommandProject(t)
	for _, args := range [][]string{
		{"use", "database.primary", "example.com/acme/implementation-use/replacement.New", "--env", "production"},
		{"generate", "--check", "--env", "production"},
		{"check", "--env", "production"},
	} {
		command := exec.CommandContext(t.Context(), binary, args...)
		command.Dir, command.Env = filepath.Join(root, "database"), implementationSelectionCommandEnvironment(nil)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		output, err := command.Output()
		if err != nil || stderr.Len() != 0 || len(output) == 0 {
			t.Fatalf("built CLI %v: %v\n%s\n%s", args, err, output, &stderr)
		}
		if args[0] == "use" && !bytes.Contains(output, []byte("selected Resource provider example.com/acme/implementation-use/replacement.New for database.primary")) {
			t.Fatalf("built CLI Resource selection output: %s", output)
		}
	}
	assertNoCommandTransactions(t, root)
}

func TestBuiltCLIRejectsMissingUseTargetsWithoutMutation(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(map[string]string{"GOFLAGS": os.Getenv("GOFLAGS")})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root := writeResourceSelectionCommandProject(t)
	environment := implementationSelectionCommandEnvironment(nil)
	generate := exec.CommandContext(t.Context(), binary, "generate", "--env", "production")
	generate.Dir, generate.Env = root, environment
	var generationStderr bytes.Buffer
	generate.Stderr = &generationStderr
	if output, err := generate.Output(); err != nil || generationStderr.Len() != 0 || len(output) == 0 {
		t.Fatalf("prepare generated Project: %v\n%s\n%s", err, output, &generationStderr)
	}
	before := commandTree(t, root)
	for _, test := range []struct {
		name, target, constructor string
	}{
		{name: "Interface", target: "missing.operation/v1", constructor: "local.New"},
		{name: "Resource", target: "database.missing", constructor: "replacement.New"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), binary, "use", test.target, "example.com/acme/implementation-use/"+test.constructor, "--env", "production")
			command.Dir, command.Env = filepath.Join(root, "database"), environment
			var stderr bytes.Buffer
			command.Stderr = &stderr
			stdout, err := command.Output()
			exitError, ok := err.(*exec.ExitError)
			if !ok || exitError.ExitCode() != 1 || len(stdout) != 0 {
				t.Fatalf("built CLI missing %s: %v\n%s\n%s", test.name, err, stdout, &stderr)
			}
			diagnostic := stderr.String()
			if !strings.Contains(diagnostic, "Diagnostic: "+diagnosticcode.UseTargetNotFound+"\n") || strings.Count(diagnostic, "Recovery:") != 1 || strings.Count(diagnostic, "Diagnostic:") != 1 {
				t.Fatalf("missing target diagnostic: %s", diagnostic)
			}
			for _, recovery := range []string{`plystra inspect interfaces --env "production"`, `plystra inspect resources --env "production"`, `plystra use <target> <constructor-symbol> --env "production"`} {
				if !strings.Contains(diagnostic, recovery) {
					t.Fatalf("missing selector-aware recovery %q: %s", recovery, diagnostic)
				}
			}
			for _, private := range []string{root, filepath.ToSlash(root), "PRIVATE_PRIMARY", "PRIVATE_RETAINED", "PRIVATE_OVERLAY"} {
				if strings.Contains(string(stdout)+diagnostic, private) {
					t.Fatalf("missing target diagnostic exposed %q", private)
				}
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("missing target selection mutated the Project")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestRunUseSelectsResourceProviderAcrossSelectors(t *testing.T) {
	for _, test := range []struct {
		name, path string
		selectors  []string
		env        map[string]string
		overlay    bool
	}{
		{name: "root", path: "plystra.yaml"},
		{name: "environment", path: "plystra.production.yaml", selectors: []string{"--env", "production"}, env: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "deploy/ignored.yaml"}, overlay: true},
		{name: "replacement", path: "deploy/customer.yaml", selectors: []string{"--config", "deploy/customer.yaml"}, env: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "deploy/ignored.yaml"}},
		{name: "ambient environment", path: "plystra.production.yaml", env: map[string]string{"PLYSTRA_ENV": "production"}, overlay: true},
		{name: "ambient replacement", path: "deploy/customer.yaml", env: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			before := commandTree(t, root)
			environment := implementationSelectionCommandEnvironment(test.env)
			const provider = "example.com/acme/implementation-use/replacement.New"
			args := append([]string{"use", "database.primary", provider}, test.selectors...)
			code, stdout, stderr := runCommand(t, args, filepath.Join(root, "database"), environment)
			want := "selected Resource provider " + provider + " for database.primary in " + filepath.Join(root, filepath.FromSlash(test.path)) + "\n"
			if code != 0 || stdout != want || stderr != "" {
				t.Fatalf("resource use = %d, %q, %q", code, stdout, stderr)
			}
			selected := readCommandFile(t, root, test.path)
			if !strings.Contains(string(selected), "# Selected Resource layer.") {
				t.Fatal("selection lost the selected document's comment")
			}
			manifest, err := applicationmeta.Parse(selected)
			if test.overlay {
				manifest, err = applicationmeta.ParseOverlaySource(test.path, selected)
			}
			if err != nil {
				t.Fatal(err)
			}
			wantInstances := 2
			if test.overlay {
				wantInstances = 1
			}
			if len(manifest.ResourceInstances()) != wantInstances {
				t.Fatal("selection deleted an unrelated instance or copied a root-owned one")
			}
			found := false
			for _, instance := range manifest.ResourceInstances() {
				switch instance.Name() {
				case "database.primary":
					found = true
					if instance.Provider().String() != provider || instance.HasConfiguration() {
						t.Fatal("provider selection retained the old instance Config")
					}
				case "database.retained":
					if instance.Provider().String() != "example.com/acme/implementation-use/original.New" || !strings.Contains(string(instance.ConfigurationYAML()), "PRIVATE_RETAINED") {
						t.Fatal("provider selection changed another instance's ownership")
					}
				}
			}
			if !found {
				t.Fatal("selected document has no target instance")
			}
			for path, data := range before {
				if path != test.path && strings.HasSuffix(path, ".yaml") && string(readCommandFile(t, root, path)) != string(data) {
					t.Fatalf("selection changed unselected document %s", path)
				}
			}
			for _, command := range [][]string{{"generate", "--check"}, {"check"}} {
				code, _, stderr := runCommand(t, append(command, test.selectors...), root, environment)
				if code != 0 || stderr != "" {
					t.Fatalf("%v after selection = %d, %q", command, code, stderr)
				}
			}
			idempotent := commandTree(t, root)
			code, stdout, stderr = runCommand(t, args, root, environment)
			want = "Resource provider " + provider + " is already selected for database.primary in " + filepath.Join(root, filepath.FromSlash(test.path)) + "\n"
			if code != 0 || stdout != want || stderr != "" || !reflect.DeepEqual(idempotent, commandTree(t, root)) {
				t.Fatalf("repeated Resource selection = %d, %q, %q or changed Project", code, stdout, stderr)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestRunUseKeepsConfigurationForSameResourceProvider(t *testing.T) {
	root := writeResourceSelectionCommandProject(t)
	before := readCommandFile(t, root, "plystra.yaml")
	code, stdout, stderr := runCommand(t, []string{"use", "database.primary", "example.com/acme/implementation-use/original.New"}, root, implementationSelectionCommandEnvironment(nil))
	if code != 0 || !strings.Contains(stdout, "is already selected for database.primary") || stderr != "" {
		t.Fatalf("same Resource provider = %d, %q, %q", code, stdout, stderr)
	}
	if !reflect.DeepEqual(before, readCommandFile(t, root, "plystra.yaml")) {
		t.Fatal("same-provider selection changed Config or comments")
	}
	assertNoCommandTransactions(t, root)
}

func TestRunUseRejectsResourceSelectionWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name, target, constructor, diagnostic string
	}{
		{name: "malformed instance", target: "database..primary", constructor: "replacement.New", diagnostic: diagnosticcode.UseTargetInvalid},
		{name: "unknown instance", target: "database.missing", constructor: "replacement.New", diagnostic: diagnosticcode.UseTargetNotFound},
		{name: "unknown Interface", target: "missing.operation/v1", constructor: "local.New", diagnostic: diagnosticcode.UseTargetNotFound},
		{name: "Resource contract is not an instance", target: "data.database/v1", constructor: "replacement.New", diagnostic: diagnosticcode.UseTargetNotFound},
		{name: "unknown provider", target: "database.primary", constructor: "missing.New", diagnostic: diagnosticcode.UseProviderIncompatible},
		{name: "Interface constructor is not a provider", target: "database.primary", constructor: "local.New", diagnostic: diagnosticcode.UseProviderIncompatible},
		{name: "wrong Resource contract", target: "database.primary", constructor: "different.New", diagnostic: diagnosticcode.UseProviderIncompatible},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			before := commandTree(t, root)
			code, stdout, stderr := runCommand(t, []string{"use", test.target, "example.com/acme/implementation-use/" + test.constructor, "--env", "production"}, root, implementationSelectionCommandEnvironment(nil))
			if code != 1 || stdout != "" || !strings.Contains(stderr, "Diagnostic: "+test.diagnostic) || !strings.Contains(stderr, `--env "production"`) || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 {
				t.Fatalf("rejected Resource selection = %d, %q, %q", code, stdout, stderr)
			}
			for _, private := range []string{root, filepath.ToSlash(root), "PRIVATE_PRIMARY", "PRIVATE_RETAINED", "PRIVATE_OVERLAY"} {
				if strings.Contains(stdout+stderr, private) {
					t.Fatalf("selection error exposed %q", private)
				}
			}
			if !reflect.DeepEqual(before, commandTree(t, root)) {
				t.Fatal("rejected selection mutated the Project")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func writeResourceSelectionCommandProject(t *testing.T) string {
	t.Helper()
	root := writeImplementationSelectionCommandProject(t)
	manifest := `# Selected Resource layer.
resources:
  instances:
    database.primary:
      use: example.com/acme/implementation-use/original.New
      config: {value: PRIVATE_PRIMARY}
    database.retained:
      use: example.com/acme/implementation-use/original.New
      config: {value: PRIVATE_RETAINED}
`
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), manifest)
	writeCommandFile(t, filepath.Join(root, "deploy/customer.yaml"), manifest)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "# Selected Resource layer.\nresources: {instances: {database.primary: {config: {value: PRIVATE_OVERLAY}}}}\n")
	writeCommandFile(t, filepath.Join(root, "plystra.ignored.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "deploy/ignored.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "database/resource.go"), "package database\n//plystra:resource data.database/v1\ntype Resource interface { Read() }\n")
	writeCommandFile(t, filepath.Join(root, "other/resource.go"), "package other\n//plystra:resource data.other/v1\ntype Resource interface { Other() }\n")
	for _, provider := range []struct{ name, id, method string }{{"original", "data.database/v1", "Read"}, {"replacement", "data.database/v1", "Read"}, {"different", "data.other/v1", "Other"}} {
		writeCommandFile(t, filepath.Join(root, provider.name, "provider.go"), fmt.Sprintf(`package %s
type Config struct { Value string `+"`yaml:\"value\"`"+` }
type Value struct{}
//plystra:implements-resource %s
func New(Config) (*Value, error) { return &Value{}, nil }
func (*Value) %s() {}
`, provider.name, provider.id, provider.method))
	}
	return root
}
