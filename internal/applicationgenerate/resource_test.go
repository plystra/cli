package applicationgenerate_test

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
)

func TestResourceContractDiscoveryDoesNotGenerateRuntimeOrTransport(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/resource-contract")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	run := func(arguments ...string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if exit := command.RunIn(arguments, &stdout, &stderr, root, goEnvironment(nil)); exit != 0 {
			t.Fatalf("%v = %d, %s, %s", arguments, exit, &stdout, &stderr)
		}
	}
	run("generate")
	before := snapshotTree(t, filepath.Join(root, "generated"))
	source := "package api\n//plystra:resource data.database/v1\ntype Resource interface{Read() Value}\ntype Value struct{Data string}\n"
	for _, body := range []string{source, strings.Replace(source, "Data string", "Data []byte", 1)} {
		writeFile(t, filepath.Join(root, "api", "resource.go"), body)
		projectBefore := snapshotTree(t, root)
		run("generate", "--check")
		if !reflect.DeepEqual(projectBefore, snapshotTree(t, root)) {
			t.Fatal("Resource contract check mutated Project")
		}
		run("generate")
		if !reflect.DeepEqual(before, snapshotTree(t, filepath.Join(root, "generated"))) {
			t.Fatal("unselected Resource contract generated executable or transport artifacts")
		}
		run("generate", "--check")
	}
	run("check")
}
