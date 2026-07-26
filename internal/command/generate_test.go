package command_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/bootstrapgen"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/connectgen"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/transportprovenance"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

func TestRunGenerateAndCheckUsePublicApplicationSurface(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/app

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	start := filepath.Join(root, "docs", "work")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	environment := commandGoEnvironment()

	before := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"generate", "--check"}, start, environment)
	if exitCode != 1 || stdout != "" {
		t.Fatalf("initial check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	wantMissing := "generated output is not current:\n" +
		"  missing generated/.plystra-manifest.json\n" +
		"  missing generated/compatibility/interface-metadata.json\n" +
		"  missing generated/compatibility/interface-transport.json\n" +
		"  missing generated/compatibility/interfaces.json\n" +
		"  missing generated/go/application/main_gen.go\n" +
		"  missing generated/go/assembly/compatibility_gen.go\n" +
		"  missing generated/go/assembly/interfaces_gen.go\n" +
		"  missing generated/go/assembly/invocations_gen.go\n" +
		"  missing generated/go/assembly/providers_gen.go\n" +
		"  missing generated/go/bootstrap/bootstrap_gen.go\n" +
		"  missing generated/manifest.json\n" +
		"  missing generated/proto/descriptor-set.pb\n" +
		"  missing generated/proto/wire-map.json\n\n" +
		"Recovery:\nRun `plystra generate` to restore the selected generated output.\n\n" +
		"Diagnostic: " + diagnosticcode.GeneratedDrift + "\n"
	if stderr != wantMissing {
		t.Fatalf("initial check stderr = %q, want %q", stderr, wantMissing)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("generate --check mutated application:\nbefore: %#v\nafter:  %#v", before, after)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"generate"}, start, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/app in "+root+"\n" {
		t.Fatalf("generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, name := range []string{
		"generated/.plystra-manifest.json",
		"generated/compatibility/interface-metadata.json",
		"generated/compatibility/interface-transport.json",
		"generated/compatibility/interfaces.json",
		"generated/go/application/main_gen.go",
		"generated/go/assembly/compatibility_gen.go",
		"generated/go/assembly/interfaces_gen.go",
		"generated/go/assembly/invocations_gen.go",
		"generated/go/assembly/providers_gen.go",
		"generated/go/bootstrap/bootstrap_gen.go",
		"generated/manifest.json",
		"generated/proto/descriptor-set.pb",
		"generated/proto/wire-map.json",
	} {
		assertCommandFile(t, root, name)
	}
	assertCommandBootstrapProvenanceMatchesManifest(t, root)

	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, start, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/app in "+root+"\n" {
		t.Fatalf("clean check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	writeCommandFile(t, filepath.Join(root, "generated", "manifest.json"), "drift\n")
	drifted := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, start, environment)
	if exitCode != 1 || stdout != "" || stderr != "generated output is not current:\n  changed generated/manifest.json\n\nRecovery:\nRun `plystra generate` to restore the selected generated output.\n\nDiagnostic: "+diagnosticcode.GeneratedDrift+"\n" {
		t.Fatalf("drift check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, drifted) {
		t.Fatalf("drift check mutated application:\nbefore: %#v\nafter:  %#v", drifted, after)
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate"}, start, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("repair generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	writeCommandFile(t, filepath.Join(root, "generated", "manual.txt"), "preserve\n")
	exitCode, stdout, stderr = runCommand(t, []string{"generate"}, start, environment)
	if exitCode != 1 || stdout != "" || stderr != "generated output remains inconsistent after installation:\n  unexpected generated/manual.txt\n\nRecovery:\nMove every unexpected unowned path outside generated/, then run `plystra generate`.\n\nDiagnostic: "+diagnosticcode.GeneratedUnexpectedOutput+"\n" {
		t.Fatalf("unexpected output = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got := string(readCommandFile(t, root, "generated/manual.txt")); got != "preserve\n" {
		t.Fatalf("unexpected user file = %q", got)
	}
	assertNoCommandTransactions(t, root)
}

func TestRunGenerateProjectsAuthoredInterfaceMessages(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	writeCommandFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module example.com/acme/interface-protobuf

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "http: {expose: [records.list/v1]}\n")
	writeCommandFile(t, filepath.Join(root, "interfaces", "records", "list", "v1", "interface.go"), `package listv1

import "context"

//plystra:interface records.list/v1
type Interface interface {
	List(context.Context, Request) (Response, error)
}

type Request struct {
	PageSize int32 `+"`json:\"page_size\" plystra:\"7,required\"`"+`
}

type Response struct {
	Count uint64 `+"`json:\"count\" plystra:\"3,required\"`"+`
}
`)
	writeCommandFile(t, filepath.Join(root, "records", "service.go"), `package records

import (
	"context"

	listv1 "example.com/acme/interface-protobuf/interfaces/records/list/v1"
)

type Service struct{}

//plystra:implements records.list/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) List(context.Context, listv1.Request) (listv1.Response, error) {
	return listv1.Response{}, nil
}
`)
	environment := commandGoEnvironment()
	exitCode, stdout, stderr := runCommand(t, []string{"generate"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/interface-protobuf in "+root+"\n" {
		t.Fatalf("generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	path := "generated/proto/plystra/generated/records/list/v1/interface.proto"
	source := readCommandFile(t, root, path)
	for _, want := range []string{
		`optional sint32 page_size = 7 [json_name = "page_size"];`,
		`optional uint64 count = 3 [json_name = "count"];`,
	} {
		if !bytes.Contains(source, []byte(want)) {
			t.Fatalf("%s omits %q:\n%s", path, want, source)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "generated", "proto", "plystra", "generated", "records", "list", "v1", "capability.proto")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("obsolete capability.proto exists or could not be inspected: %v", err)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/interface-protobuf in "+root+"\n" {
		t.Fatalf("generate --check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestRunGenerateInstallsConnectRuntimeRequirementsAndCheckIsReadOnly(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/connect-runtime

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	addLegacyProtobufReplacement(t, root)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `http:
  transports:
    connect: true
    rest: false
  expose:
    - kernel.health/v1
`)
	environment := commandGoEnvironment()

	beforeCheck := commandTree(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 1 || stdout != "" {
		t.Fatalf("initial Connect check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, want := range []string{
		"invalid generated application runtime dependency",
		connectgen.ConnectModulePath,
		connectgen.ConnectModuleVersion,
		"\n\nRecovery:\nRun `plystra generate` to repair the required direct application runtime dependencies.\n\nDiagnostic: " + diagnosticcode.ApplicationDependencyDrift + "\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("initial Connect check stderr %q omits %q", stderr, want)
		}
	}
	if count := strings.Count(stderr, "Recovery:"); count != 1 {
		t.Fatalf("initial Connect check Recovery count = %d in %q", count, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, beforeCheck) {
		t.Fatalf("generate --check changed the Project before runtime installation:\nbefore: %#v\nafter:  %#v", beforeCheck, after)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"generate"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/connect-runtime in "+root+"\n" {
		t.Fatalf("Connect generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	assertCommandFile(t, root, "generated/go/adapters/connect/kernel/health/v1/handler_gen.go")

	modData := readCommandFile(t, root, "go.mod")
	parsed, err := modfile.Parse("go.mod", modData, nil)
	if err != nil {
		t.Fatalf("parse generated go.mod: %v", err)
	}
	requirements := make(map[string]*modfile.Require, len(parsed.Require))
	for _, requirement := range parsed.Require {
		requirements[requirement.Mod.Path] = requirement
	}
	for _, expected := range []struct {
		path    string
		version string
	}{
		{path: connectgen.ConnectModulePath, version: connectgen.ConnectModuleVersion},
		{path: connectgen.ProtobufModulePath, version: connectgen.ProtobufModuleVersion},
		{path: bootstrapgen.YAMLModulePath, version: bootstrapgen.YAMLModuleVersion},
	} {
		requirement, exists := requirements[expected.path]
		if !exists {
			t.Fatalf("generated go.mod omits %s", expected.path)
		}
		if requirement.Indirect {
			t.Fatalf("generated runtime requirement %s remains indirect", expected.path)
		}
		if semver.Compare(requirement.Mod.Version, expected.version) < 0 {
			t.Fatalf("generated runtime requirement %s = %s, want at least %s", expected.path, requirement.Mod.Version, expected.version)
		}
	}

	beforeCleanCheck := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/connect-runtime in "+root+"\n" {
		t.Fatalf("clean Connect check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, beforeCleanCheck) {
		t.Fatalf("clean generate --check changed the Project:\nbefore: %#v\nafter:  %#v", beforeCleanCheck, after)
	}
}

func TestRunGenerateRequiresConnectForJavaScriptSDKWithoutMutation(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/javascript-connect

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "http: {transports: {connect: false, rest: true}, expose: [kernel.health/v1]}\n")
	before := commandTree(t, root)

	for _, arguments := range [][]string{{"generate", "--check"}, {"generate"}} {
		exitCode, stdout, stderr := runCommand(t, arguments, root, commandGoEnvironment())
		if exitCode != 1 || stdout != "" {
			t.Fatalf("%v = exit %d, stdout %q, stderr %q", arguments, exitCode, stdout, stderr)
		}
		for _, want := range []string{
			"invalid JavaScript SDK transport selection",
			`http.transports.connect is false for selected configuration "plystra.yaml"`,
			"official generated JavaScript SDK requires Connect for Capability kernel.health/v1",
			"enable http.transports.connect",
		} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("%v stderr %q does not contain %q", arguments, stderr, want)
			}
		}
		if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("%v mutated rejected Project:\nbefore: %#v\nafter:  %#v", arguments, before, after)
		}
		assertNoCommandTransactions(t, root)
	}
}

func TestRunGenerateCheckReportsDependencyCompositionDriftWithoutMutation(t *testing.T) {
	root := t.TempDir()
	applicationRoot := filepath.Join(root, "application")
	dependencyRoot := filepath.Join(root, "platform")
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))

	writeCommandFile(t, filepath.Join(dependencyRoot, "go.mod"), "module example.com/platform\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities:\n  require: [kernel.health/v1]\n")
	goMod := fmt.Sprintf(`module example.com/acme/composed

go 1.26

require (
	example.com/platform v0.0.0
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace example.com/platform => %s

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(dependencyRoot), filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(applicationRoot, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "capabilities:\n  require: []\n")
	environment := commandGoEnvironment()

	exitCode, stdout, stderr := runCommand(t, []string{"generate"}, applicationRoot, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/composed in "+applicationRoot+"\n" {
		t.Fatalf("initial generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities:\n  require: [kernel.info/v1]\n")
	before := commandTree(t, applicationRoot)

	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, applicationRoot, environment)
	if exitCode != 1 || stdout != "" || !strings.HasPrefix(stderr, "Project configuration or generated output is not current:\n  changed plystra.yaml (dependency composition)\n") || !strings.Contains(stderr, "\n\nRecovery:\nRun `plystra generate` to restore the selected generated output.\n\nDiagnostic: "+diagnosticcode.ConfigurationCompositionDrift+"\n") || strings.Count(stderr, "Recovery:") != 1 {
		t.Fatalf("composition check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("composition check mutated application:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestRunGeneratePreservesCurrentProjectInterfaceEditAcrossDependencyBaselineChange(t *testing.T) {
	parent := t.TempDir()
	applicationRoot := filepath.Join(parent, "application")
	dependencyRoot := filepath.Join(parent, "platform")
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))

	writeCommandFile(t, filepath.Join(dependencyRoot, "go.mod"), "module example.com/platform\n\ngo 1.26\n")
	writeCommandGraphInterface(t, dependencyRoot, "email/send/v1", "sendv1", "email.send/v1", "Send")
	writeCommandGraphInterface(t, dependencyRoot, "audit/write/v1", "writev1", "audit.write/v1", "Write")
	writeCommandFile(t, filepath.Join(dependencyRoot, "smtp", "service.go"), `package smtp

import (
	"context"

	sendv1 "example.com/platform/interfaces/email/send/v1"
)

type Service struct{}

//plystra:implements email.send/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) {
	return sendv1.Response{}, nil
}
`)
	writeCommandFile(t, filepath.Join(dependencyRoot, "other", "service.go"), `package other

import (
	"context"

	sendv1 "example.com/platform/interfaces/email/send/v1"
)

type Service struct{}

//plystra:implements email.send/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) {
	return sendv1.Response{}, nil
}
`)
	writeCommandFile(t, filepath.Join(dependencyRoot, "audit", "service.go"), `package audit

import (
	"context"

	writev1 "example.com/platform/interfaces/audit/write/v1"
)

type Service struct{}

//plystra:implements audit.write/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Write(context.Context, writev1.Request) (writev1.Response, error) {
	return writev1.Response{}, nil
}
`)
	writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), `interfaces:
  require: [email.send/v1]
  use:
    email.send/v1: example.com/platform/smtp.New
`)

	goMod := fmt.Sprintf(`module example.com/acme/maintenance

go 1.26

require (
	example.com/platform v1.0.0
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace example.com/platform => %s

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(dependencyRoot), filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(applicationRoot, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(applicationRoot, "local", "service.go"), `package local

import (
	"context"

	sendv1 "example.com/platform/interfaces/email/send/v1"
)

type Service struct{}

//plystra:implements email.send/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) {
	return sendv1.Response{}, nil
}
`)
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "# shared application configuration\n{}\n")
	environment := commandGoEnvironment()

	exitCode, stdout, stderr := runCommand(t, []string{"generate"}, applicationRoot, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/maintenance in "+applicationRoot+"\n" {
		t.Fatalf("initial generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	oldRoot := readCommandFile(t, applicationRoot, "plystra.yaml")
	locallyEdited := bytes.Replace(oldRoot, []byte("example.com/platform/smtp.New"), []byte("example.com/acme/maintenance/local.New"), 1)
	if bytes.Equal(locallyEdited, oldRoot) {
		t.Fatalf("initial dependency baseline omitted selected Implementation:\n%s", oldRoot)
	}
	locallyEdited = bytes.Replace(locallyEdited, []byte("# shared application configuration"), []byte("# shared application configuration\n# explicit local selection"), 1)
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), string(locallyEdited))
	writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), `interfaces:
  require: [audit.write/v1, email.send/v1]
  use:
    audit.write/v1: example.com/platform/audit.New
    email.send/v1: example.com/platform/other.New
`)

	exitCode, stdout, stderr = runCommand(t, []string{"generate"}, applicationRoot, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/maintenance in "+applicationRoot+"\n" {
		t.Fatalf("updated generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	maintained := readCommandFile(t, applicationRoot, "plystra.yaml")
	for _, expected := range [][]byte{
		[]byte("# shared application configuration"),
		[]byte("# explicit local selection"),
		[]byte("audit.write/v1"),
		[]byte("example.com/platform/audit.New"),
		[]byte("example.com/acme/maintenance/local.New"),
	} {
		if !bytes.Contains(maintained, expected) {
			t.Fatalf("maintained root omits %q:\n%s", expected, maintained)
		}
	}
	for _, overwritten := range [][]byte{[]byte("example.com/platform/smtp.New"), []byte("example.com/platform/other.New")} {
		if bytes.Contains(maintained, overwritten) {
			t.Fatalf("dependency baseline overwrote local selection with %q:\n%s", overwritten, maintained)
		}
	}
	generatedAssembly := readCommandFile(t, applicationRoot, "generated/go/assembly/interfaces_gen.go")
	for _, selected := range [][]byte{[]byte("example.com/platform/audit.New"), []byte("example.com/acme/maintenance/local.New")} {
		if !bytes.Contains(generatedAssembly, selected) {
			t.Fatalf("generated assembly omits selected constructor %q:\n%s", selected, generatedAssembly)
		}
	}
	beforeCheck := commandTree(t, applicationRoot)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, applicationRoot, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/maintenance in "+applicationRoot+"\n" {
		t.Fatalf("generate --check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, beforeCheck) {
		t.Fatal("generate --check mutated the maintained Project")
	}
	assertNoCommandTransactions(t, applicationRoot)
}

func TestRunGenerateSelectsEnvironmentOverlayThroughPublicCommand(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/environment

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	rootConfiguration := "# shared root\nhttp: {cors: {allowed_origins: [https://app.example.com], allow_credentials: true}}\ninterfaces: {require: [kernel.health/v1]}\n"
	overlayConfiguration := "# sparse production overlay\nhttp: {cors: {allow_credentials: null}}\ninterfaces:\n  require:\n    add: [kernel.info/v1]\n    remove: [kernel.health/v1]\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootConfiguration)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlayConfiguration)
	start := filepath.Join(root, "nested")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	environment := commandGoEnvironment()

	exitCode, stdout, stderr := runCommand(t, []string{"generate", "--env", "production"}, start, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/environment in "+root+"\n" {
		t.Fatalf("generate --env = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got := string(readCommandFile(t, root, "plystra.yaml")); got != rootConfiguration {
		t.Fatalf("environment generation changed root configuration:\n%s", got)
	}
	if got := string(readCommandFile(t, root, "plystra.production.yaml")); got != overlayConfiguration {
		t.Fatalf("environment generation changed overlay:\n%s", got)
	}
	provenance, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
	if err != nil || provenance.Mode() != applicationgen.ConfigurationModeEnvironment || provenance.Environment() != "production" || provenance.SelectedPath() != "plystra.production.yaml" {
		t.Fatalf("environment provenance = mode %q environment %q path %q, %v", provenance.Mode(), provenance.Environment(), provenance.SelectedPath(), err)
	}
	assertCommandBootstrapProvenanceMatchesManifest(t, root)

	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, start, commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "production"}))
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/environment in "+root+"\n" {
		t.Fatalf("PLYSTRA_ENV check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	explicitEnvironment := commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "missing.yaml"})
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--env", "production"}, start, explicitEnvironment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("explicit --env did not override ambient selectors = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	writeCommandFile(t, filepath.Join(root, "generated", "manifest.json"), "drift\n")
	beforeEnvironmentDrift := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--env", "production"}, start, explicitEnvironment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "\n\nRecovery:\nRun `plystra generate --env \"production\"` to restore the selected generated output.\n\nDiagnostic: "+diagnosticcode.GeneratedDrift+"\n") || strings.Count(stderr, "Recovery:") != 1 {
		t.Fatalf("environment drift recovery = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, beforeEnvironmentDrift) {
		t.Fatal("environment drift check mutated the Project")
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--env", "production"}, start, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("repair environment generation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	for _, runtime := range []struct {
		name        string
		arguments   []string
		environment []string
	}{
		{
			name:        "explicit environment overrides ambient",
			arguments:   []string{"run", "./generated/go/application", "--smoke", "--env", "production"},
			environment: commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "missing", "PLYSTRA_CONFIG": "missing.yaml"}),
		},
		{
			name:        "ambient environment",
			arguments:   []string{"run", "./generated/go/application", "--smoke"},
			environment: commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "production"}),
		},
	} {
		t.Run("generated binary "+runtime.name, func(t *testing.T) {
			process := exec.CommandContext(t.Context(), "go", runtime.arguments...)
			process.Dir = root
			process.Env = runtime.environment
			if output, err := process.CombinedOutput(); err != nil {
				t.Fatalf("generated application %s: %v\n%s", runtime.name, err, output)
			}
		})
	}
	changedOverlay := strings.Replace(overlayConfiguration, "add: [kernel.info/v1]", "add: [kernel.health/v1]", 1)
	changedOverlay = strings.Replace(changedOverlay, "remove: [kernel.health/v1]", "remove: [kernel.info/v1]", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), changedOverlay)
	process := exec.CommandContext(t.Context(), "go", "run", "./generated/go/application", "--smoke", "--env", "production")
	process.Dir = root
	process.Env = environment
	output, runtimeErr := process.CombinedOutput()
	if runtimeErr == nil || !strings.Contains(string(output), "runtime configuration is incompatible with compiled application model") || !strings.Contains(string(output), "rebuild with the same --env or --config selection") {
		t.Fatalf("generated application accepted build-affecting environment drift: %v\n%s", runtimeErr, output)
	}
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlayConfiguration)

	credentialedWildcardOverlay := strings.Replace(
		overlayConfiguration,
		"http: {cors: {allow_credentials: null}}",
		"http: {cors: {allowed_origins: ['*']}}",
		1,
	)
	if credentialedWildcardOverlay == overlayConfiguration {
		t.Fatal("test overlay does not contain the expected CORS declaration")
	}
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), credentialedWildcardOverlay)
	beforeCredentialedWildcardCheck := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--env", "production"}, start, environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "http.cors cannot combine wildcard origin") || !strings.Contains(stderr, "\n\nRecovery:\nEdit plystra.production.yaml so every value matches a selected Plugin's closed typed schema, then rerun the command.\n\nDiagnostic: "+diagnosticcode.EnvironmentOverlayInvalid+"\n") || strings.Count(stderr, "Recovery:") != 1 {
		t.Fatalf("credentialed wildcard check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, beforeCredentialedWildcardCheck) {
		t.Fatal("credentialed wildcard check mutated the Project")
	}
	process = exec.CommandContext(t.Context(), "go", "run", "./generated/go/application", "--smoke", "--env", "production")
	process.Dir = root
	process.Env = environment
	output, runtimeErr = process.CombinedOutput()
	if runtimeErr == nil || !strings.Contains(string(output), "http.cors cannot combine wildcard origin with allow_credentials: true") {
		t.Fatalf("generated application accepted credentialed wildcard CORS: %v\n%s", runtimeErr, output)
	}
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlayConfiguration)

	for _, runtime := range []struct {
		name      string
		arguments []string
		want      string
	}{
		{name: "missing environment", arguments: []string{"run", "./generated/go/application", "--smoke", "--env", "missing"}, want: "requires plystra.missing.yaml"},
		{name: "unsafe environment", arguments: []string{"run", "./generated/go/application", "--smoke", "--env", "../production"}, want: "safe filename component"},
	} {
		t.Run("generated binary "+runtime.name, func(t *testing.T) {
			process := exec.CommandContext(t.Context(), "go", runtime.arguments...)
			process.Dir = root
			process.Env = environment
			output, err := process.CombinedOutput()
			if err == nil || !strings.Contains(string(output), runtime.want) {
				t.Fatalf("generated application %s = %v, %s", runtime.name, err, output)
			}
		})
	}

	beforeConflict := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, start, commandGoEnvironmentWith(map[string]string{"PLYSTRA_ENV": "production", "PLYSTRA_CONFIG": "plystra.yaml"}))
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "PLYSTRA_CONFIG and PLYSTRA_ENV cannot be used together") {
		t.Fatalf("ambient selector conflict = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, beforeConflict) {
		t.Fatal("ambient selector conflict mutated the Project")
	}

	for _, test := range []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"generate", "--check", "--env", "missing"}, want: "plystra.missing.yaml"},
		{arguments: []string{"generate", "--check", "--env", "../production"}, want: "safe filename component"},
	} {
		before := commandTree(t, root)
		exitCode, stdout, stderr = runCommand(t, test.arguments, start, environment)
		if exitCode != 1 || stdout != "" || !strings.Contains(stderr, test.want) {
			t.Fatalf("generate %q = exit %d, stdout %q, stderr %q", test.arguments, exitCode, stdout, stderr)
		}
		if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("generate %q mutated the Project", test.arguments)
		}
	}
}

func TestRunGenerateSelectsCompleteConfigurationThroughPublicCommand(t *testing.T) {
	root := t.TempDir()
	applicationRoot := filepath.Join(root, "application")
	dependencyRoot := filepath.Join(root, "platform")
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))

	writeCommandFile(t, filepath.Join(dependencyRoot, "go.mod"), "module example.com/platform\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "interfaces: {require: [kernel.health/v1]}\n")
	goMod := fmt.Sprintf(`module example.com/acme/config-select

go 1.26

require (
	example.com/platform v0.0.0
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace example.com/platform => %s

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(dependencyRoot), filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(applicationRoot, "go.mod"), goMod)
	addLegacyProtobufReplacement(t, applicationRoot)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(applicationRoot, "go.sum"), string(goSum))
	rootConfiguration := `# shared root configuration must be excluded in replacement mode
http:
  expose: [kernel.health/v1]
interfaces:
  require: [kernel.health/v1]
`
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), rootConfiguration)
	selectedConfiguration := `# complete customer configuration
http:
  expose: [kernel.info/v1]
interfaces:
  require: [kernel.info/v1]
`
	selectedPath := filepath.Join(applicationRoot, "deploy", "customer.yaml")
	writeCommandFile(t, selectedPath, selectedConfiguration)
	ambientPath := filepath.Join(applicationRoot, "deploy", "ambient.yaml")
	writeCommandFile(t, ambientPath, rootConfiguration)

	nestedStart := filepath.Join(applicationRoot, "nested")
	if err := os.MkdirAll(nestedStart, 0o755); err != nil {
		t.Fatalf("MkdirAll nested start: %v", err)
	}
	environment := commandGoEnvironment()
	exitCode, stdout, stderr := runCommand(t, []string{"generate", "--config", selectedPath}, nestedStart, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/config-select in "+applicationRoot+"\n" {
		t.Fatalf("generate --config from nested Plugin = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if got := string(readCommandFile(t, applicationRoot, "plystra.yaml")); got != rootConfiguration {
		t.Fatalf("replacement generation changed root configuration:\n%s", got)
	}
	selected := readCommandFile(t, applicationRoot, "deploy/customer.yaml")
	for _, required := range [][]byte{[]byte("# complete customer configuration"), []byte("kernel.health/v1"), []byte("kernel.info/v1")} {
		if !bytes.Contains(selected, required) {
			t.Fatalf("maintained selected configuration omits %q:\n%s", required, selected)
		}
	}
	provenance, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, applicationRoot, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance: %v", err)
	}
	if provenance.Mode() != applicationgen.ConfigurationModeExplicit || provenance.RootPath() != "plystra.yaml" || provenance.SelectedPath() != "deploy/customer.yaml" {
		t.Fatalf("selected manifest provenance = mode %q root %q selected %q", provenance.Mode(), provenance.RootPath(), provenance.SelectedPath())
	}
	assertCommandBootstrapProvenanceMatchesManifest(t, applicationRoot)
	for _, name := range []string{
		"generated/go/adapters/http/kernel/info/v1/handler_gen.go",
		"generated/go/clients/kernel/info/v1/client_gen.go",
		"generated/go/invocation/kernel/info/v1/invocation_gen.go",
		"generated/sdk/javascript/src/interfaces/kernel/info/v1.ts",
		"generated/docs/api.md",
		"generated/docs/openapi.json",
	} {
		assertCommandFile(t, applicationRoot, name)
	}
	for _, name := range []string{
		"generated/go/adapters/http/kernel/health/v1/handler_gen.go",
		"generated/sdk/javascript/src/interfaces/kernel/health/v1.ts",
	} {
		if _, statErr := os.Lstat(filepath.Join(applicationRoot, filepath.FromSlash(name))); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("root-only exposure generated %s: %v", name, statErr)
		}
	}
	apiDocumentation := readCommandFile(t, applicationRoot, "generated/docs/api.md")
	if !bytes.Contains(apiDocumentation, []byte("kernel.info/v1")) || bytes.Contains(apiDocumentation, []byte("kernel.health/v1")) {
		t.Fatalf("selected API documentation does not match replacement exposure:\n%s", apiDocumentation)
	}
	for name, content := range commandTree(t, filepath.Join(applicationRoot, "generated")) {
		for _, forbidden := range []string{applicationRoot, "runtime-private", "resolved-secret"} {
			if bytes.Contains(content, []byte(forbidden)) {
				t.Fatalf("generated/%s leaked %q", name, forbidden)
			}
		}
	}

	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "root: [intentionally-invalid\n")
	for _, runtime := range []struct {
		name        string
		arguments   []string
		environment []string
	}{
		{
			name:        "explicit relative replacement overrides ambient selectors",
			arguments:   []string{"run", "./generated/go/application", "--smoke", "--config", "deploy/customer.yaml"},
			environment: commandGoEnvironmentWith(map[string]string{"PLYSTRA_CONFIG": "missing.yaml", "PLYSTRA_ENV": "missing"}),
		},
		{
			name:        "explicit absolute replacement",
			arguments:   []string{"run", "./generated/go/application", "--smoke", "--config", selectedPath},
			environment: environment,
		},
		{
			name:        "ambient replacement",
			arguments:   []string{"run", "./generated/go/application", "--smoke"},
			environment: commandGoEnvironmentWith(map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"}),
		},
	} {
		t.Run("generated binary "+runtime.name, func(t *testing.T) {
			process := exec.CommandContext(t.Context(), "go", runtime.arguments...)
			process.Dir = applicationRoot
			process.Env = runtime.environment
			if output, err := process.CombinedOutput(); err != nil {
				t.Fatalf("generated application %s: %v\n%s", runtime.name, err, output)
			}
		})
	}
	changedSelected := bytes.Replace(selected, []byte("expose: [kernel.info/v1]"), []byte("expose: [kernel.health/v1]"), 1)
	if bytes.Equal(changedSelected, selected) {
		t.Fatal("test replacement did not contain the compiled exposure declaration")
	}
	writeCommandFile(t, selectedPath, string(changedSelected))
	process := exec.CommandContext(t.Context(), "go", "run", "./generated/go/application", "--smoke", "--config", "deploy/customer.yaml")
	process.Dir = applicationRoot
	process.Env = environment
	output, runtimeErr := process.CombinedOutput()
	if runtimeErr == nil || !strings.Contains(string(output), "runtime configuration is incompatible with compiled application model") || !strings.Contains(string(output), "rebuild with the same --env or --config selection") {
		t.Fatalf("generated application accepted build-affecting replacement drift: %v\n%s", runtimeErr, output)
	}
	writeCommandFile(t, selectedPath, string(selected))
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), rootConfiguration)

	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--config", "deploy/customer.yaml"}, nestedStart, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/config-select in "+applicationRoot+"\n" {
		t.Fatalf("clean generate --check --config = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	ambientEnvironment := commandGoEnvironmentWith(map[string]string{"PLYSTRA_CONFIG": "deploy/ambient.yaml"})
	beforeAmbientCheck := commandTree(t, applicationRoot)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, nestedStart, ambientEnvironment)
	if exitCode != 1 || stdout != "" || !strings.HasPrefix(stderr, "generated output is not current:\n") || !strings.Contains(stderr, "\n\nRecovery:\nRun `plystra generate --config \"deploy/ambient.yaml\"` to restore the selected generated output.\n\nDiagnostic: "+diagnosticcode.GeneratedDrift+"\n") || strings.Count(stderr, "Recovery:") != 1 {
		t.Fatalf("PLYSTRA_CONFIG check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, beforeAmbientCheck) {
		t.Fatal("PLYSTRA_CONFIG generate --check mutated the Project")
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--config", "deploy/customer.yaml"}, nestedStart, ambientEnvironment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("explicit --config did not override PLYSTRA_CONFIG = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}

	writeCommandFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "interfaces: {require: [kernel.info/v1]}\n")
	beforeCompositionCheck := commandTree(t, applicationRoot)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--config", "deploy/customer.yaml"}, nestedStart, environment)
	if exitCode != 1 || stdout != "" || !strings.HasPrefix(stderr, "Project configuration or generated output is not current:\n  changed deploy/customer.yaml (dependency composition)\n") || !strings.Contains(stderr, "\n\nRecovery:\nRun `plystra generate --config \"deploy/customer.yaml\"` to restore the selected generated output.\n\nDiagnostic: "+diagnosticcode.ConfigurationCompositionDrift+"\n") || strings.Count(stderr, "Recovery:") != 1 {
		t.Fatalf("selected-path composition drift = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, beforeCompositionCheck) {
		t.Fatal("selected-path drift check mutated the Project")
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--config", "deploy/customer.yaml"}, nestedStart, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("repair selected composition = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	selected = readCommandFile(t, applicationRoot, "deploy/customer.yaml")
	if bytes.Contains(selected, []byte("kernel.health/v1")) || !bytes.Contains(selected, []byte("kernel.info/v1")) {
		t.Fatalf("selected composition was not updated independently:\n%s", selected)
	}
	if got := string(readCommandFile(t, applicationRoot, "plystra.yaml")); got != rootConfiguration {
		t.Fatalf("selected composition repair changed root configuration:\n%s", got)
	}

	outsidePath := filepath.Join(root, "outside.yaml")
	writeCommandFile(t, outsidePath, "{}\n")
	beforeOutsideSelection := commandTree(t, applicationRoot)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--config", outsidePath}, nestedStart, environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "within the Project root") {
		t.Fatalf("outside-Project configuration = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, beforeOutsideSelection) {
		t.Fatal("outside-Project selector rejection mutated the Project")
	}
	linkedDeploy := filepath.Join(applicationRoot, "linked-deploy")
	beforeSymbolicSelection := commandTree(t, applicationRoot)
	if err := os.Symlink(filepath.Join(applicationRoot, "deploy"), linkedDeploy); err == nil {
		exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--config", "linked-deploy/customer.yaml"}, nestedStart, environment)
		if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "symbolic path component") {
			t.Fatalf("symbolic configuration path = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
		}
		if err := os.Remove(linkedDeploy); err != nil {
			t.Fatalf("remove symbolic configuration directory: %v", err)
		}
		if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, beforeSymbolicSelection) {
			t.Fatal("symbolic selector rejection mutated the Project")
		}
	}

	if err := os.Remove(filepath.Join(applicationRoot, "plystra.yaml")); err != nil {
		t.Fatalf("remove root Project marker: %v", err)
	}
	beforeMissingMarkerCheck := commandTree(t, applicationRoot)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--config", "deploy/customer.yaml"}, nestedStart, environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "plystra.yaml") {
		t.Fatalf("missing mandatory root marker = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, applicationRoot); !reflect.DeepEqual(after, beforeMissingMarkerCheck) {
		t.Fatal("missing-marker failure mutated the Project")
	}
	writeCommandFile(t, filepath.Join(applicationRoot, "plystra.yaml"), rootConfiguration)
}

func TestRunGenerateCarriesInterfacePoliciesThroughEveryConfigurationMode(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/policy

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandGraphInterface(t, root, "email/send/v1", "sendv1", "email.send/v1", "Send")
	writeCommandFile(t, filepath.Join(root, "smtp", "service.go"), `package smtp

import (
	"context"

	sendv1 "example.com/acme/policy/interfaces/email/send/v1"
)

type Service struct{}

//plystra:implements email.send/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Send(context.Context, sendv1.Request) (sendv1.Response, error) {
	return sendv1.Response{}, nil
}
`)
	rootConfiguration := `interfaces:
  require: [email.send/v1]
  policies:
    email.send/v1: {timeout: 5000ms}
`
	overlayConfiguration := `# environment-specific policy
interfaces:
  policies:
    email.send/v1: {timeout: 2s}
`
	replacementConfiguration := `# complete replacement policy
interfaces:
  require: [email.send/v1]
  policies:
    email.send/v1: {timeout: 7s}
`
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootConfiguration)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlayConfiguration)
	writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), replacementConfiguration)
	environment := commandGoEnvironment()

	exitCode, stdout, stderr := runCommand(t, []string{"generate"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/policy in "+root+"\n" {
		t.Fatalf("default policy generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	defaultManifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance(default): %v", err)
	}
	defaultBootstrap := readCommandFile(t, root, "generated/go/bootstrap/bootstrap_gen.go")
	if !bytes.Contains(defaultBootstrap, []byte(`\"interface_policies\":[{\"interface\":\"email.send/v1\",\"timeout\":\"5s\"}]`)) {
		t.Fatalf("default generated bootstrap omits normalized Interface policy:\n%s", defaultBootstrap)
	}
	beforeDefaultCheck := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 0 || stderr != "" || !strings.Contains(stdout, "generated output is current") {
		t.Fatalf("default policy check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, beforeDefaultCheck) {
		t.Fatal("default policy generate --check mutated the Project")
	}
	removedOverlay := `# environment-specific policy removal
interfaces:
  policies:
    email.send/v1: null
`
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), removedOverlay)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--env", "production"}, root, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("removed environment policy generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	removedBootstrap := readCommandFile(t, root, "generated/go/bootstrap/bootstrap_gen.go")
	if !bytes.Contains(removedBootstrap, []byte(`\"interface_policies\":[]`)) || bytes.Contains(removedBootstrap, []byte(`\"timeout\":\"5s\"`)) {
		t.Fatalf("environment policy removal was not projected exactly:\n%s", removedBootstrap)
	}
	process := exec.CommandContext(t.Context(), "go", "run", "./generated/go/application", "--smoke", "--env", "production")
	process.Dir = root
	process.Env = environment
	if output, runErr := process.CombinedOutput(); runErr != nil {
		t.Fatalf("generated application rejected matching removed environment policy: %v\n%s", runErr, output)
	}
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlayConfiguration)

	beforeEnvironmentCheck := commandTree(t, root)
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check", "--env", "production"}, root, environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "generated output is not current") {
		t.Fatalf("environment policy drift check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, beforeEnvironmentCheck) {
		t.Fatal("environment policy drift check mutated the Project")
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--env", "production"}, root, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("environment policy generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	environmentManifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
	if err != nil || environmentManifest.Mode() != applicationgen.ConfigurationModeEnvironment || environmentManifest.Environment() != "production" {
		t.Fatalf("environment policy provenance = %#v, %v", environmentManifest, err)
	}
	if environmentManifest.ApplicationModelDigest() == defaultManifest.ApplicationModelDigest() {
		t.Fatal("environment policy replacement did not change the build-affecting application model")
	}
	environmentBootstrap := readCommandFile(t, root, "generated/go/bootstrap/bootstrap_gen.go")
	if !bytes.Contains(environmentBootstrap, []byte(`\"interface_policies\":[{\"interface\":\"email.send/v1\",\"timeout\":\"2s\"}]`)) {
		t.Fatalf("environment generated bootstrap omits selected Interface policy:\n%s", environmentBootstrap)
	}
	process = exec.CommandContext(t.Context(), "go", "run", "./generated/go/application", "--smoke", "--env", "production")
	process.Dir = root
	process.Env = environment
	if output, runErr := process.CombinedOutput(); runErr != nil {
		t.Fatalf("generated application rejected matching environment policy: %v\n%s", runErr, output)
	}
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), strings.Replace(overlayConfiguration, "2s", "3s", 1))
	process = exec.CommandContext(t.Context(), "go", "run", "./generated/go/application", "--smoke", "--env", "production")
	process.Dir = root
	process.Env = environment
	output, runErr := process.CombinedOutput()
	if runErr == nil || !strings.Contains(string(output), "runtime configuration is incompatible with compiled application model") || !strings.Contains(string(output), "rebuild with the same --env or --config selection") {
		t.Fatalf("generated application accepted build-affecting policy drift: %v\n%s", runErr, output)
	}
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlayConfiguration)

	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--config", "deploy/customer.yaml"}, root, environment)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("replacement policy generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	replacementManifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
	if err != nil || replacementManifest.Mode() != applicationgen.ConfigurationModeExplicit || replacementManifest.SelectedPath() != "deploy/customer.yaml" {
		t.Fatalf("replacement policy provenance = %#v, %v", replacementManifest, err)
	}
	if replacementManifest.ApplicationModelDigest() == defaultManifest.ApplicationModelDigest() || replacementManifest.ApplicationModelDigest() == environmentManifest.ApplicationModelDigest() {
		t.Fatal("replacement policy did not produce its own build-affecting application model")
	}
	process = exec.CommandContext(t.Context(), "go", "run", "./generated/go/application", "--smoke", "--config", "deploy/customer.yaml")
	process.Dir = root
	process.Env = environment
	if output, runErr := process.CombinedOutput(); runErr != nil {
		t.Fatalf("generated application rejected matching replacement policy: %v\n%s", runErr, output)
	}

	invalidOverlay := `# invalid environment policy must be preserved
interfaces:
  policies:
    email.send/v1: {timeout: 2s, retry: 2}
`
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), invalidOverlay)
	beforeInvalid := commandTree(t, root)
	process = exec.CommandContext(t.Context(), "go", "run", "./generated/go/application", "--smoke", "--env", "production")
	process.Dir = root
	process.Env = environment
	output, runErr = process.CombinedOutput()
	if runErr == nil || !strings.Contains(string(output), `interfaces.policies["email.send/v1"] contains unknown key "retry"`) {
		t.Fatalf("generated application accepted invalid environment policy: %v\n%s", runErr, output)
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--env", "production"}, root, environment)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, `interfaces.policies["email.send/v1"] contains unknown key "retry"`) {
		t.Fatalf("invalid environment policy = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, beforeInvalid) {
		t.Fatal("invalid environment policy changed the Project")
	}
	assertNoCommandTransactions(t, root)
}

func TestRunGenerateBuildsUnrequiredLocalCapabilityDeveloperSurfaces(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/authoring

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "business", "plugin.yaml"), "id: acme.business\nprovides: [email.send/v1]\n")
	writeCommandFile(t, filepath.Join(root, "business", "capabilities", "email.send", "v1", "capability.yaml"), `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`)
	writeCommandFile(t, filepath.Join(root, "business", "plugin.go"), `package business

import (
	"context"

	configuration "example.com/acme/authoring/generated/go/configuration"
	contract "example.com/acme/authoring/generated/go/contracts/email/send/v1"
)

type Config = configuration.BusinessConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Send(_ context.Context, request contract.Request) (contract.Response, error) {
	return contract.Response{Accepted: request.To != ""}, nil
}
`)
	environment := commandGoEnvironment()
	exitCode, stdout, stderr := runCommand(t, []string{"generate"}, filepath.Join(root, "business"), environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/authoring in "+root+"\n" {
		t.Fatalf("generate = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, name := range []string{
		"generated/go/configuration/business_gen.go",
		"generated/go/contracts/email/send/v1/contract_gen.go",
		"generated/go/providers/email/send/v1/provider_gen.go",
	} {
		assertCommandFile(t, root, name)
	}
	generated := commandTree(t, filepath.Join(root, "generated"))
	for _, name := range []string{
		"docs/api.md",
		"go/adapters/http/email/send/v1/handler_gen.go",
		"go/clients/email/send/v1/client_gen.go",
		"go/invocation/email/send/v1/invocation_gen.go",
		"sdk/javascript/package.json",
	} {
		if _, exists := generated[name]; exists {
			t.Fatalf("unrequired local Capability received application surface generated/%s", name)
		}
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/authoring in "+root+"\n" {
		t.Fatalf("clean check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestRunGenerateBuildsCompleteProjectAssembly(t *testing.T) {
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/library

go 1.26

require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeCommandFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "email", "plugin.yaml"), "id: acme.library.email\nprovides: [email.send/v1]\n")
	writeCommandFile(t, filepath.Join(root, "email", "capabilities", "email.send", "v1", "capability.yaml"), "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCommandFile(t, filepath.Join(root, "email", "plugin.go"), `package email

import (
	"context"

	configuration "example.com/acme/library/generated/go/configuration"
	contract "example.com/acme/library/generated/go/contracts/email/send/v1"
)

type Config = configuration.EmailConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Send(_ context.Context, _ contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`)
	environment := commandGoEnvironment()
	exitCode, stdout, stderr := runCommand(t, []string{"generate"}, filepath.Join(root, "email"), environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/library in "+root+"\n" {
		t.Fatalf("generate Project = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	for _, name := range []string{
		"generated/.plystra-manifest.json",
		"generated/go/application/main_gen.go",
		"generated/go/assembly/compatibility_gen.go",
		"generated/go/assembly/providers_gen.go",
		"generated/go/bootstrap/bootstrap_gen.go",
		"generated/go/configuration/email_gen.go",
		"generated/go/contracts/email/send/v1/contract_gen.go",
		"generated/go/providers/email/send/v1/provider_gen.go",
		"generated/manifest.json",
		"generated/proto/descriptor-set.pb",
		"generated/proto/wire-map.json",
	} {
		assertCommandFile(t, root, name)
	}
	exitCode, stdout, stderr = runCommand(t, []string{"generate", "--check"}, root, environment)
	if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/library in "+root+"\n" {
		t.Fatalf("clean Project check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func runCommand(t testing.TB, arguments []string, start string, environment []string) (int, string, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := command.RunIn(arguments, &stdout, &stderr, start, environment)
	return exitCode, stdout.String(), stderr.String()
}

func commandGoEnvironment() []string {
	overrides := map[string]string{
		"GOENV":       "off",
		"GOFLAGS":     "",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[strings.ToUpper(key)]; !replaced {
			environment = append(environment, entry)
		}
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+overrides[key])
	}
	return environment
}

func commandGoEnvironmentWith(overrides map[string]string) []string {
	environment := commandGoEnvironment()
	replaced := make(map[string]string, len(overrides))
	for key, value := range overrides {
		replaced[strings.ToUpper(key)] = value
	}
	filtered := environment[:0]
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if _, exists := replaced[strings.ToUpper(key)]; !exists {
			filtered = append(filtered, entry)
		}
	}
	keys := make([]string, 0, len(replaced))
	for key := range replaced {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		filtered = append(filtered, key+"="+replaced[key])
	}
	return filtered
}

func writeCommandFile(t testing.TB, name, content string) {
	t.Helper()
	if filepath.Base(name) == "capability.yaml" && !strings.Contains(content, "\nsemantics:") {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += commandQuerySemanticsYAML
	}
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

const commandQuerySemanticsYAML = `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`

func addLegacyProtobufReplacement(t testing.TB, moduleRoot string) {
	t.Helper()
	legacyRoot := filepath.Join(t.TempDir(), "legacy-protobuf")
	writeCommandFile(t, filepath.Join(legacyRoot, "go.mod"), "module github.com/golang/protobuf\n\ngo 1.26\n")
	goModPath := filepath.Join(moduleRoot, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", goModPath, err)
	}
	data = append(data, []byte("\nreplace github.com/golang/protobuf => "+filepath.ToSlash(legacyRoot)+"\n")...)
	if err := os.WriteFile(goModPath, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", goModPath, err)
	}
}

func readCommandFile(t testing.TB, root, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	return data
}

func assertCommandBootstrapProvenanceMatchesManifest(t testing.TB, root string) {
	t.Helper()
	manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance: %v", err)
	}
	provenance, err := transportprovenance.New(transportprovenance.Input{
		Mode:                        generation.ConfigurationMode(manifest.Mode()),
		Environment:                 manifest.Environment(),
		RootPath:                    manifest.RootPath(),
		RootDigest:                  manifest.RootDigest(),
		SelectedPath:                manifest.SelectedPath(),
		SelectedDigest:              manifest.SelectedDigest(),
		DependencyCompositionDigest: manifest.DependencyBaseline().Digest(),
		ApplicationModelDigest:      manifest.ApplicationModelDigest(),
	})
	if err != nil {
		t.Fatalf("transportprovenance.New from generated manifest: %v", err)
	}
	bootstrap := readCommandFile(t, root, "generated/go/bootstrap/bootstrap_gen.go")
	for _, required := range []string{
		strconv.Quote(string(provenance.CanonicalJSON())),
		strconv.Quote(provenance.Digest()),
		"compiledApplicationModelCompatibilityJSON",
		"compiledApplicationModelCompatibilityDigest",
		"compiledApplicationModelDigest",
		strconv.Quote(manifest.ApplicationModelDigest()),
	} {
		if !bytes.Contains(bootstrap, []byte(required)) {
			t.Fatalf("generated bootstrap provenance disagrees with generated manifest %q", required)
		}
	}
}

func assertCommandFile(t testing.TB, root, name string) {
	t.Helper()
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file: %v", name, err)
	}
}

func commandTree(t testing.TB, root string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	err := fs.WalkDir(os.DirFS(root), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		result[filepath.ToSlash(name)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	return result
}

func assertNoCommandTransactions(t testing.TB, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".plystra-files-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("transaction files = %v, %v", matches, err)
	}
}

func commandRepositoryRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatalf("Abs(repository root): %v", err)
	}
	return root
}
