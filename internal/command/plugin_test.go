package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
)

func TestRunPluginCreateClassifiesAuthoringFailuresWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		modulePath string
		pluginName string
		target     bool
		problem    string
		recovery   string
		diagnostic string
	}{
		{
			name:       "invalid name before Project discovery",
			pluginName: "Account",
			problem:    "invalid plugin name",
			recovery:   "Run `plystra plugin create <plugin-name>` with one lower-case ASCII kebab-case name that is not reserved at the Project root.",
			diagnostic: diagnosticcode.PluginCreateNameInvalid,
		},
		{
			name:       "invalid derived ID",
			modulePath: "example.com",
			pluginName: "account",
			problem:    "derive plugin ID",
			recovery:   "Correct the current Project module path in go.mod or choose a shorter canonical Plugin name so their derived identity is valid, then rerun `plystra plugin create <plugin-name>`.",
			diagnostic: diagnosticcode.PluginCreateIDInvalid,
		},
		{
			name:       "existing target",
			modulePath: "example.com/acme/app",
			pluginName: "account",
			target:     true,
			problem:    "plugin target already exists",
			recovery:   "Rerun `plystra plugin create <plugin-name>` with a different canonical name whose root-level directory does not exist.",
			diagnostic: diagnosticcode.PluginCreateTargetExists,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeCommandFile(t, filepath.Join(root, "keep.txt"), "keep\n")
			start := filepath.Join(root, "missing")
			if test.modulePath != "" {
				writeCommandFile(t, filepath.Join(root, "go.mod"), "module "+test.modulePath+"\n\ngo 1.26\n")
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
				start = filepath.Join(root, "cmd", "server")
				writeCommandFile(t, filepath.Join(start, "keep.txt"), "keep\n")
			}
			if test.target {
				writeCommandFile(t, filepath.Join(root, test.pluginName, "keep.txt"), "keep\n")
			}
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(t, []string{"plugin", "create", test.pluginName}, start, commandGoEnvironment())
			if exitCode != 1 || stdout != "" || !commandContainsAll(
				stderr,
				test.problem,
				"Recovery:\n"+test.recovery+"\n",
				"Diagnostic: "+test.diagnostic,
			) {
				t.Fatalf("plugin create %q = exit %d, stdout %q, stderr %q", test.pluginName, exitCode, stdout, stderr)
			}
			if strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 || strings.Contains(strings.ToLower(stderr), "usage:") {
				t.Fatalf("plugin create emitted unstable diagnostic framing: %q", stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("plugin create mutated Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}
