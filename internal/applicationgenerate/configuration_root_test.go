package applicationgenerate_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/privatefile"
)

func TestGeneratedBinaryUsesExplicitConfigurationRoot(t *testing.T) {
	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/configuration-root")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "timeouts: {startup: 17s}\n")
	environment := goEnvironment(nil)
	filtered := environment[:0]
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "PLYSTRA_ENV") && !strings.EqualFold(name, "PLYSTRA_CONFIG") {
			filtered = append(filtered, entry)
		}
	}
	environment = filtered
	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, environment); code != 0 {
		t.Fatalf("generate = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	deployment := t.TempDir()
	binary := filepath.Join(deployment, "application")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-race", "-mod=readonly", "-o", binary, "./generated/go/application")
	build.Dir, build.Env = root, environment
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build generated application: %v\n%s", err, output)
	}
	configuration := filepath.Join(deployment, "--smoke")
	writeFile(t, filepath.Join(configuration, "plystra.yaml"), "timeouts: {startup: 23s}\n")
	writeFile(t, filepath.Join(configuration, "plystra.production.yaml"), "timeouts: {startup: 31s}\n")
	writeFile(t, filepath.Join(configuration, "deploy", "customer.yaml"), "timeouts: {startup: 41s}\n")
	writeFile(t, filepath.Join(configuration, "plystra.ignored.yaml"), "invalid: [\n")
	writeFile(t, filepath.Join(deployment, "plystra.yaml"), "invalid: [unrelated-working-directory\n")
	baseline := filepath.Join(deployment, "runtime-baseline.json")
	copyPrivateBaseline(t, filepath.Join(root, "dist/runtime-baseline.json"), baseline)
	before := snapshotTree(t, configuration)
	for _, test := range []struct {
		name        string
		arguments   []string
		selectors   []string
		wantFailure string
	}{
		{name: "default absolute", arguments: []string{"--configuration-root", configuration}},
		{name: "root value resembles smoke flag", arguments: []string{"--configuration-root", "--smoke"}},
		{name: "explicit environment", arguments: []string{"--env", "production", "--configuration-root", configuration}, selectors: []string{"PLYSTRA_ENV=missing", "PLYSTRA_CONFIG=missing"}},
		{name: "ambient environment", arguments: []string{"--configuration-root", configuration}, selectors: []string{"PLYSTRA_ENV=production"}},
		{name: "relative replacement", arguments: []string{"--configuration-root", configuration, "--config", "deploy/customer.yaml"}},
		{name: "absolute replacement", arguments: []string{"--config", filepath.Join(configuration, "deploy", "customer.yaml"), "--configuration-root", configuration}},
		{name: "ambient replacement", arguments: []string{"--configuration-root", configuration}, selectors: []string{"PLYSTRA_CONFIG=deploy/customer.yaml"}},
		{name: "missing root", wantFailure: "--configuration-root <directory> is required"},
		{name: "duplicate root", arguments: []string{"--configuration-root", configuration, "--configuration-root", configuration}, wantFailure: "requires one nonempty directory"},
		{name: "duplicate baseline", arguments: []string{"--configuration-root", configuration, "--runtime-baseline", baseline}, wantFailure: "requires one nonempty path"},
		{name: "outside root", arguments: []string{"--configuration-root", configuration, "--config", "../plystra.yaml"}, wantFailure: "within the configuration root"},
		{name: "conflicting ambient selectors", arguments: []string{"--configuration-root", configuration}, selectors: []string{"PLYSTRA_ENV=production", "PLYSTRA_CONFIG=deploy/customer.yaml"}, wantFailure: "cannot be used together"},
	} {
		t.Run(test.name, func(t *testing.T) {
			process := exec.CommandContext(t.Context(), binary, append([]string{"--smoke", "--runtime-baseline", baseline}, test.arguments...)...)
			process.Dir = deployment
			process.Env = append(append([]string(nil), environment...), test.selectors...)
			output, err := process.CombinedOutput()
			if test.wantFailure == "" && err != nil {
				t.Fatalf("generated binary: %v\n%s", err, output)
			}
			if test.wantFailure != "" && (err == nil || !strings.Contains(string(output), test.wantFailure)) {
				t.Fatalf("generated binary failure = %v, output %s", err, output)
			}
			if bytes.Contains(output, []byte(configuration)) {
				t.Fatalf("diagnostic disclosed private root: %s", output)
			}
		})
	}
	if after := snapshotTree(t, configuration); !reflect.DeepEqual(before, after) {
		t.Fatal("generated binary modified deployment configuration")
	}
	if _, err := os.Stat(filepath.Join(configuration, "go.mod")); !os.IsNotExist(err) {
		t.Fatalf("deployment unexpectedly contains a module: %v", err)
	}
	missing := exec.CommandContext(t.Context(), binary, "--smoke", "--configuration-root", configuration)
	missing.Dir, missing.Env = deployment, environment
	if output, err := missing.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("--runtime-baseline <path> is required")) {
		t.Fatalf("missing baseline argument accepted: %v\n%s", err, output)
	}
	if err := os.Remove(filepath.Join(root, "dist/runtime-baseline.json")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := command.RunIn([]string{"generate", "--check"}, &stdout, &stderr, root, environment); code != 0 {
		t.Fatalf("public check requires private output: %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	if _, err := os.Stat(filepath.Join(root, "dist/runtime-baseline.json")); !os.IsNotExist(err) {
		t.Fatal("read-only check created a private baseline")
	}
}

func copyPrivateBaseline(t testing.TB, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	file, err := privatefile.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
}
