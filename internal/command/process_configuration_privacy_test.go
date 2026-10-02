package command_test

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPublicProcessSettingChangesKeepGeneratedIdentity(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			path := "plystra.yaml"
			var selector []string
			switch mode {
			case "environment":
				path, selector = "plystra.production.yaml", []string{"--env", "production"}
			case "replacement":
				path, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
			}
			if mode != "default" {
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "http: {address: 'PRIVATE_ROOT:18080'}\ntimeouts: {startup: 5s}\n")
			}
			write := func(data string) { writeCommandFile(t, filepath.Join(root, filepath.FromSlash(path)), data) }
			invoke := func(arguments ...string) string {
				t.Helper()
				code, stdout, stderr := runCommand(t, append(arguments, selector...), root, commandGoEnvironment())
				if code != 0 || strings.Contains(stdout+stderr, "PRIVATE_") {
					t.Fatalf("%v: %d, %s, %s", arguments, code, stdout, stderr)
				}
				return stdout
			}
			write("http: {address: 'PRIVATE_FIRST:19080'}\ntimeouts: {startup: 1s}\n")
			invoke("generate")
			before := commandTree(t, filepath.Join(root, "generated"))
			inspection := invoke("inspect", "configuration", "--format", "json")
			explanation := invoke("explain", "config", "http.address", "--format", "json")
			write("http: {address: 'PRIVATE_SECOND:29080'}\ntimeouts: {startup: 2m}\n")
			unchanged := commandTree(t, root)
			invoke("generate", "--check")
			invoke("check")
			if got := invoke("inspect", "configuration", "--format", "json"); got != inspection {
				t.Fatalf("private process edit changed public inspection:\nbefore: %s\nafter: %s", inspection, got)
			}
			if got := invoke("explain", "config", "http.address", "--format", "json"); !bytes.Equal(canonicalExplainCommandResult(t, got), canonicalExplainCommandResult(t, explanation)) {
				t.Fatalf("private process edit changed public explanation:\nbefore: %s\nafter: %s", explanation, got)
			}
			if !reflect.DeepEqual(unchanged, commandTree(t, root)) {
				t.Fatal("read-only command changed the Project")
			}
			invoke("generate")
			if !reflect.DeepEqual(before, commandTree(t, filepath.Join(root, "generated"))) {
				t.Fatal("private process edit changed generated output")
			}
			write("http: {address: 'PRIVATE_SECOND:29080', cors: {allowed_origins: ['https://example.test']}}\ntimeouts: {startup: 2m}\n")
			unchanged = commandTree(t, root)
			code, _, stderr := runCommand(t, append([]string{"generate", "--check"}, selector...), root, commandGoEnvironment())
			if code == 0 || !strings.Contains(stderr, "PLYSTRA_GENERATED_DRIFT") || !reflect.DeepEqual(unchanged, commandTree(t, root)) {
				t.Fatalf("CORS drift check = %d, %s", code, stderr)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}
