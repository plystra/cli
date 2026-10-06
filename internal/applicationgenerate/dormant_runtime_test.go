package applicationgenerate_test

import (
	"bytes"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/runtimebaseline"
)

func TestDormantConfigurationStaysOutsideExecutableArtifacts(t *testing.T) {
	const module = "example.com/dormant-runtime"
	root := t.TempDir()
	writeApplicationModule(t, root, module)
	symbol := writeConstructorConfigurationOwner(t, root, module, true)
	writeFile(t, root+`/plystra.yaml`, "interfaces:\n  use:\n    configuration.owner/v1: "+symbol+"\nconfig:\n  "+symbol+": {endpoint: private-endpoint, password: {env: PRIVATE_DORMANT_SECRET}}\n")

	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("generate = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	for _, file := range snapshotGenerated(t, root) {
		for _, private := range []string{"private-endpoint", "PRIVATE_DORMANT_SECRET"} {
			if bytes.Contains(file.data, []byte(private)) {
				t.Fatalf("generated artifact %s contains private dormant configuration", file.path)
			}
		}
	}
	manifest, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.DormantConstructorConfigurations()) != 1 {
		t.Fatalf("dormant constructor configurations = %d, want 1", len(manifest.DormantConstructorConfigurations()))
	}
	baseline, err := runtimebaseline.Decode(readFile(t, root, "dist/runtime-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Defaults) == 0 {
		t.Fatal("runtime baseline omitted compiled constructor defaults")
	}
	bootstrap := readFile(t, root, "generated/go/bootstrap/bootstrap_gen.go")
	if bytes.Contains(bootstrap, []byte(symbol)) {
		t.Fatal("dormant constructor entered generated bootstrap")
	}
}
