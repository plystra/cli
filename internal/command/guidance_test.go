package command_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/agentguidance"
	"github.com/plystra/cli/internal/diagnosticcode"
)

const guidanceTestModule = "example.com/acme/guidance"

func TestRunGuidanceCheckSyncAndReplaceThroughPublicSurface(t *testing.T) {
	root, nested := createGuidanceCommandProject(t)
	userFiles := map[string]string{
		agentguidance.Root + "/local.md":              "Project-specific guidance\n",
		agentguidance.Root + "/notes.md":              "Unlisted guidance\n",
		".agents/skills/other/SKILL.md":               "Sibling skill\n",
		"AGENTS.md":                                   "Repository instructions\n",
		"docs/project-specific-agent-instructions.md": "Project instructions\n",
	}
	for name, content := range userFiles {
		writeCommandFile(t, filepath.Join(root, filepath.FromSlash(name)), content)
	}

	projection, err := agentguidance.Render(guidanceTestModule)
	if err != nil {
		t.Fatalf("Render Agent guidance: %v", err)
	}
	beforeCheck := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"guidance", "check"}, nested, commandGoEnvironment())
	if exitCode != 1 || stdout != "" || !strings.HasPrefix(stderr, "Agent guidance is not current:\n") || !strings.Contains(stderr, "Diagnostic: "+diagnosticcode.AgentGuidanceDrift+"\n") {
		t.Fatalf("initial guidance check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, file := range projection.Files() {
		if !strings.Contains(stderr, "missing "+file.Path()+"\n") || !strings.Contains(stderr, "Source: "+guidanceTestModule+":"+file.Path()+" (agent-guidance)\n") {
			t.Fatalf("initial guidance check omits %s: %s", file.Path(), stderr)
		}
	}
	if strings.Count(stderr, "Source: ") != len(projection.Files()) || strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic: ") != 1 {
		t.Fatalf("initial guidance check has unstable diagnostic structure: %s", stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, beforeCheck) {
		t.Fatal("guidance check mutated the Project")
	}

	exitCode, stdout, stderr = runCommand(t, []string{"guidance", "sync"}, nested, commandGoEnvironment())
	wantPrefix := "synchronized Agent guidance for " + guidanceTestModule + " in " + commandCanonicalPath(t, root) + ":\n"
	if exitCode != 0 || !strings.HasPrefix(stdout, wantPrefix) || stderr != "" {
		t.Fatalf("initial guidance sync = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, file := range projection.Files() {
		if !strings.Contains(stdout, "  changed "+file.Path()+"\n") {
			t.Fatalf("initial sync omits changed path %s: %s", file.Path(), stdout)
		}
		if got := readCommandFile(t, root, file.Path()); !reflect.DeepEqual(got, file.Data()) {
			t.Fatalf("installed %s differs from the catalog projection", file.Path())
		}
	}
	assertGuidanceUserFiles(t, root, userFiles)
	assertNoCommandTransactions(t, root)

	wantCurrent := "Agent guidance is current for " + guidanceTestModule + " in " + commandCanonicalPath(t, root) + "\n"
	exitCode, stdout, stderr = runCommand(t, []string{"guidance", "check"}, nested, commandGoEnvironment())
	if exitCode != 0 || stdout != wantCurrent || stderr != "" {
		t.Fatalf("clean guidance check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	target := agentguidance.Root + "/SKILL.md"
	writeCommandFile(t, filepath.Join(root, filepath.FromSlash(target)), "manual generated edit\n")
	drifted := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"guidance", "check"}, nested, commandGoEnvironment())
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "  manually-modified "+target+"\n") || !strings.Contains(stderr, "Diagnostic: "+diagnosticcode.AgentGuidanceDrift+"\n") {
		t.Fatalf("drifted guidance check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, drifted) {
		t.Fatal("drifted guidance check mutated the Project")
	}

	exitCode, stdout, stderr = runCommand(t, []string{"guidance", "sync"}, nested, commandGoEnvironment())
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "manually-modified "+target) || !strings.Contains(stderr, "Source: "+guidanceTestModule+":"+target+" (agent-guidance)\n") || !strings.Contains(stderr, "Diagnostic: "+diagnosticcode.AgentGuidanceDrift+"\n") {
		t.Fatalf("blocked guidance sync = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, drifted) {
		t.Fatal("blocked guidance sync mutated the Project")
	}

	exitCode, stdout, stderr = runCommand(t, []string{"guidance", "sync", "--replace-generated"}, nested, commandGoEnvironment())
	wantReplace := wantPrefix + "  changed " + target + "\n"
	if exitCode != 0 || stdout != wantReplace || stderr != "" {
		t.Fatalf("replacement guidance sync = exit %d, stdout %q, stderr %q; want stdout %q", exitCode, stdout, stderr, wantReplace)
	}
	assertGuidanceUserFiles(t, root, userFiles)
	assertNoCommandTransactions(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"guidance", "check"}, nested, commandGoEnvironment())
	if exitCode != 0 || stdout != wantCurrent || stderr != "" {
		t.Fatalf("post-replacement guidance check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestRunGuidanceRejectsInvalidManifestWithoutMutation(t *testing.T) {
	root, nested := createGuidanceCommandProject(t)
	exitCode, stdout, stderr := runCommand(t, []string{"guidance", "sync"}, nested, commandGoEnvironment())
	if exitCode != 0 || stderr != "" {
		t.Fatalf("initial guidance sync = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	writeCommandFile(t, filepath.Join(root, filepath.FromSlash(agentguidance.ManifestPath)), "{\n")
	before := commandTree(t, root)

	for _, arguments := range [][]string{
		{"guidance", "check"},
		{"guidance", "sync"},
		{"guidance", "sync", "--replace-generated"},
	} {
		exitCode, stdout, stderr = runCommand(t, arguments, nested, commandGoEnvironment())
		if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "Source: "+guidanceTestModule+":"+agentguidance.ManifestPath+" (agent-guidance)\n") || !strings.Contains(stderr, "Diagnostic: "+diagnosticcode.AgentGuidanceManifestInvalid+"\n") {
			t.Fatalf("invalid-manifest %q = exit %d, stdout %q, stderr %q", arguments, exitCode, stdout, stderr)
		}
		if strings.Contains(stderr, root) || strings.Contains(stderr, filepath.ToSlash(root)) {
			t.Fatalf("invalid-manifest %q exposes the Project path: %s", arguments, stderr)
		}
		if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("invalid-manifest %q mutated the Project", arguments)
		}
	}
	assertNoCommandTransactions(t, root)
}

func createGuidanceCommandProject(t testing.TB) (string, string) {
	t.Helper()
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module "+guidanceTestModule+"\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll nested directory: %v", err)
	}
	return root, nested
}

func assertGuidanceUserFiles(t testing.TB, root string, files map[string]string) {
	t.Helper()
	for name, want := range files {
		if got := string(readCommandFile(t, root, name)); got != want {
			t.Fatalf("user-owned %s = %q, want %q", name, got, want)
		}
	}
}
