package command_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/testkernel"
)

func TestGenerateRejectsActiveDataMembersBeforeMutation(t *testing.T) {
	for _, arguments := range [][]string{
		{"generate", "--env", "production"},
		{"generate", "--check", "--env", "production"},
	} {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			root := t.TempDir()
			goMod := fmt.Sprintf("module example.com/acme/data-member\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => %s\n", filepath.ToSlash(testkernel.Root(t)))
			writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
			goSum, err := os.ReadFile(filepath.Join(commandRepositoryRoot(t), "go.sum"))
			if err != nil {
				t.Fatal(err)
			}
			writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), `data:
  members:
    authn.persistence/v1: {resource: database.primary}
`)
			before := commandTree(t, root)
			code, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
			if code != 1 || stdout != "" || !strings.Contains(stderr, `plystra.production.yaml data.members["authn.persistence/v1"]`) || !strings.Contains(stderr, "Data compiler integration is unavailable") || !strings.Contains(stderr, "Diagnostic: "+diagnosticcode.DataCompilerUnavailable) || !strings.Contains(stderr, "Source: example.com/acme/data-member:plystra.production.yaml:3:5 (configuration-declaration)") {
				t.Fatalf("command = exit %d, stdout %q, stderr %q", code, stdout, stderr)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("command changed the Project: before %#v, after %#v", before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestGenerateRejectsMalformedDataMemberWithSource(t *testing.T) {
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/data-member\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "data:\n  members:\n    authn.persistence/v1: {resource: PRIVATE_INVALID_RESOURCE}\n")
	before := commandTree(t, root)
	code, stdout, stderr := runCommand(t, []string{"generate", "--check"}, root, commandGoEnvironment())
	if code != 1 || stdout != "" || !strings.Contains(stderr, "Diagnostic: "+diagnosticcode.DataMemberMetadataInvalid) || !strings.Contains(stderr, "Source: example.com/acme/data-member:plystra.yaml:3:38 (configuration-declaration)") || strings.Contains(stderr, "PRIVATE_INVALID_RESOURCE") {
		t.Fatalf("command = exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatal("invalid member changed the Project")
	}
	assertNoCommandTransactions(t, root)
}

func TestGenerateUsesOnlyEffectiveDataMembers(t *testing.T) {
	for _, test := range []struct {
		name, selected, selectedPath string
		arguments                    []string
	}{
		{"overlay removal", `data: {members: {authn.persistence/v1: {$remove: true}}}`, "plystra.production.yaml", []string{"generate", "--env", "production"}},
		{"complete replacement", "{}", "deploy/selected.yaml", []string{"generate", "--config", "deploy/selected.yaml"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			goMod := fmt.Sprintf("module example.com/acme/data-member\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => %s\n", filepath.ToSlash(testkernel.Root(t)))
			writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
			goSum, err := os.ReadFile(filepath.Join(commandRepositoryRoot(t), "go.sum"))
			if err != nil {
				t.Fatal(err)
			}
			writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `data: {members: {authn.persistence/v1: {resource: database.primary}}}`)
			writeCommandFile(t, filepath.Join(root, filepath.FromSlash(test.selectedPath)), test.selected)
			code, stdout, stderr := runCommand(t, test.arguments, root, commandGoEnvironment())
			if code != 0 || strings.Contains(stderr, diagnosticcode.DataCompilerUnavailable) {
				t.Fatalf("effective member exclusion = exit %d, stdout %q, stderr %q", code, stdout, stderr)
			}
			assertCommandFile(t, root, "generated/manifest.json")
			assertNoCommandTransactions(t, root)
		})
	}
}
