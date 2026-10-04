package applicationgenerate_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestDormantResourceConsumerGeneratesAndRunsWithoutResourceAssembly(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			const module = "example.com/dormant-resource"
			root := t.TempDir()
			writeApplicationModule(t, root, module)
			constructor := writeConstructorConfigurationOwner(t, root, module, true)
			writeFile(t, filepath.Join(root, "database", "resource.go"), "package database\n//plystra:resource storage.database/v1\ntype Resource interface { Health() error }\n")
			path := filepath.Join(root, "configowner", "implementation.go")
			source := string(readAbsoluteFile(t, path))
			source = strings.Replace(source, "import (", "import (\n database \""+module+"/database\"", 1)
			source = strings.Replace(source, "New(Config)", "New(cfg Config, primary database.Resource, Replica database.Resource)", 1)
			source = strings.Replace(source, "return &Service{}, nil", "panic(\"PRIVATE_RESOURCE_CONSTRUCTOR_ENTRY\")", 1)
			writeFile(t, path, source)
			writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			document := "plystra.yaml"
			var selector []string
			switch mode {
			case "environment":
				document, selector = "plystra.production.yaml", []string{"--env", "production"}
			case "replacement":
				document, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
			}
			run := func(arguments ...string) (int, string, string) {
				t.Helper()
				var stdout, stderr bytes.Buffer
				exit := command.RunIn(append(arguments, selector...), &stdout, &stderr, root, goEnvironment(nil))
				return exit, stdout.String(), stderr.String()
			}
			for _, configuration := range []string{
				"{}\n",
				"interfaces: {use: {configuration.owner/v1: " + constructor + "}}\nconfig: {" + constructor + ": {password: {env: PRIVATE_RESOURCE_SECRET}}}\n",
			} {
				writeFile(t, filepath.Join(root, filepath.FromSlash(document)), configuration)
				if exit, stdout, stderr := run("generate"); exit != 0 {
					t.Fatalf("generate = %d: %s %s", exit, stdout, stderr)
				}
				provenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
				if err != nil {
					t.Fatal(err)
				}
				if len(provenance.InterfaceProvenance().Constructors()) != 0 || len(provenance.InterfaceProvenance().Bindings()) != 0 {
					t.Fatal("dormant Resource consumer entered executable provenance")
				}
				for _, file := range snapshotGenerated(t, root) {
					for _, forbidden := range []string{"PRIVATE_RESOURCE_SECRET", "PRIVATE_RESOURCE_CONSTRUCTOR_ENTRY"} {
						if bytes.Contains(file.data, []byte(forbidden)) {
							t.Fatalf("%s contains dormant Resource or private data", file.path)
						}
					}
				}
				if bytes.Contains(readFile(t, root, "generated/go/assembly/interfaces_gen.go"), []byte(constructor)) ||
					bytes.Contains(readFile(t, root, "generated/go/bootstrap/bootstrap_gen.go"), []byte("\""+module+"/configowner\"")) {
					t.Fatal("dormant consumer entered active bootstrap imports or assembly")
				}
				before := snapshotTree(t, root)
				for _, arguments := range [][]string{{"generate", "--check"}, {"check"}} {
					if exit, stdout, stderr := run(arguments...); exit != 0 {
						t.Fatalf("%v = %d: %s %s", arguments, exit, stdout, stderr)
					}
				}
				if !reflect.DeepEqual(before, snapshotTree(t, root)) {
					t.Fatal("dormant consumer checks changed Project files")
				}
			}
			binary := filepath.Join(t.TempDir(), "application")
			if runtime.GOOS == "windows" {
				binary += ".exe"
			}
			build := exec.CommandContext(t.Context(), "go", "build", "-race", "-mod=readonly", "-o", binary, "./generated/go/application")
			build.Dir, build.Env = root, goEnvironment(nil)
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, output)
			}
			arguments := append([]string{"--smoke", "--configuration-root", root, "--runtime-baseline", filepath.Join(root, "dist/runtime-baseline.json")}, selector...)
			smoke := exec.CommandContext(t.Context(), binary, arguments...)
			smoke.Dir, smoke.Env = t.TempDir(), goEnvironment(nil)
			if output, err := smoke.CombinedOutput(); err != nil {
				t.Fatalf("dormant runtime: %v\n%s", err, output)
			}
			writeFile(t, filepath.Join(root, filepath.FromSlash(document)), "interfaces: {require: [configuration.owner/v1]}\n")
			before := snapshotTree(t, root)
			for _, arguments := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
				if exit, stdout, stderr := run(arguments...); exit != 1 || !strings.Contains(stderr, diagnosticcode.ResourceBindingMissing) {
					t.Fatalf("activated consumer %v = %d: %s %s", arguments, exit, stdout, stderr)
				}
				if !reflect.DeepEqual(before, snapshotTree(t, root)) {
					t.Fatal("missing Resource binding changed Project files")
				}
			}
		})
	}
}
