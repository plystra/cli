package command_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestRunUseSelectsImplementationOnlyInRequestedConfigurationLayer(t *testing.T) {
	tests := []struct {
		name        string
		selectors   []string
		environment map[string]string
		wantPath    string
		overlay     bool
	}{
		{name: "root", wantPath: "plystra.yaml"},
		{
			name:      "explicit environment overrides ambient selectors",
			selectors: []string{"--env", "production"},
			environment: map[string]string{
				"PLYSTRA_CONFIG": "deploy/ignored.yaml",
				"PLYSTRA_ENV":    "ignored",
			},
			wantPath: "plystra.production.yaml",
			overlay:  true,
		},
		{
			name:        "ambient environment",
			environment: map[string]string{"PLYSTRA_ENV": "staging"},
			wantPath:    "plystra.staging.yaml",
			overlay:     true,
		},
		{
			name:      "explicit replacement overrides ambient selectors",
			selectors: []string{"--config", "deploy/customer.yaml"},
			environment: map[string]string{
				"PLYSTRA_CONFIG": "deploy/ignored.yaml",
				"PLYSTRA_ENV":    "ignored",
			},
			wantPath: "deploy/customer.yaml",
		},
		{
			name:        "ambient replacement",
			environment: map[string]string{"PLYSTRA_CONFIG": "deploy/automation.yaml"},
			wantPath:    "deploy/automation.yaml",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			configuration := map[string]string{
				"plystra.yaml":            "# Shared Implementation choices.\ninterfaces:\n  require: [email.send/v1]\n",
				"plystra.production.yaml": "# Production Implementation choices.\n{}\n",
				"plystra.staging.yaml":    "# Staging Implementation choices.\n{}\n",
				"deploy/customer.yaml":    "# Customer Implementation choices.\ninterfaces:\n  require: [email.send/v1]\n",
				"deploy/automation.yaml":  "# Automation Implementation choices.\ninterfaces:\n  require: [email.send/v1]\n",
				"deploy/ignored.yaml":     "# Ignored replacement.\ninterfaces:\n  require: [email.send/v1]\n",
				"plystra.ignored.yaml":    "# Ignored overlay.\n{}\n",
			}
			for name, data := range configuration {
				writeCommandFile(t, filepath.Join(root, filepath.FromSlash(name)), data)
			}
			before := make(map[string]string, len(configuration))
			for name := range configuration {
				before[name] = string(readCommandFile(t, root, name))
			}

			environment := implementationSelectionCommandEnvironment(test.environment)
			const selected = "example.com/acme/implementation-use/local.New"
			arguments := append([]string{"use", "email.send/v1", selected}, test.selectors...)
			exitCode, stdout, stderr := runCommand(t, arguments, filepath.Join(root, "smtp"), environment)
			wantAbsolutePath := filepath.Join(root, filepath.FromSlash(test.wantPath))
			wantOutput := "selected Implementation " + selected + " for email.send/v1 in " + wantAbsolutePath + "\n"
			if exitCode != 0 || stdout != wantOutput || stderr != "" {
				t.Fatalf("plystra use = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			selectedData := readCommandFile(t, root, test.wantPath)
			if !strings.Contains(string(selectedData), strings.Split(before[test.wantPath], "\n")[0]) {
				t.Fatalf("selected configuration lost its leading comment:\n%s", selectedData)
			}
			manifest, err := applicationmeta.Parse(selectedData)
			if test.overlay {
				manifest, err = applicationmeta.ParseOverlaySource(test.wantPath, selectedData)
			}
			if err != nil || !commandHasImplementationChoice(manifest, "email.send/v1", selected) {
				t.Fatalf("selected configuration Implementation choices = %#v, %v\n%s", manifest.ImplementationChoices(), err, selectedData)
			}
			for name, data := range before {
				if name == test.wantPath {
					continue
				}
				if got := string(readCommandFile(t, root, name)); got != data {
					t.Fatalf("plystra use changed unselected %s:\n%s", name, got)
				}
			}
			assembly := string(readCommandFile(t, root, "generated/go/assembly/interfaces_gen.go"))
			if !strings.Contains(assembly, `"example.com/acme/implementation-use/local.New"`) || strings.Contains(assembly, `"example.com/acme/implementation-use/smtp.New"`) {
				t.Fatalf("generated Interface assembly does not use the explicit Implementation:\n%s", assembly)
			}

			checkArguments := append([]string{"generate", "--check"}, test.selectors...)
			exitCode, stdout, stderr = runCommand(t, checkArguments, filepath.Join(root, "local"), environment)
			if exitCode != 0 || stderr != "" || stdout != "generated output is current for example.com/acme/implementation-use in "+root+"\n" {
				t.Fatalf("post-selection generate --check = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}

			if test.name == "root" {
				idempotentBefore := commandTree(t, root)
				exitCode, stdout, stderr = runCommand(t, arguments, filepath.Join(root, "local"), environment)
				wantOutput = "Implementation " + selected + " is already selected for email.send/v1 in " + wantAbsolutePath + "\n"
				if exitCode != 0 || stdout != wantOutput || stderr != "" {
					t.Fatalf("idempotent plystra use = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
				}
				if after := commandTree(t, root); !reflect.DeepEqual(after, idempotentBefore) {
					t.Fatalf("idempotent plystra use changed the Project:\nbefore: %#v\nafter:  %#v", idempotentBefore, after)
				}
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestRunUseRejectsInvalidImplementationChoicesAndRestoresProject(t *testing.T) {
	tests := []struct {
		name        string
		interfaceID string
		constructor string
		want        string
	}{
		{name: "invalid Interface", interfaceID: "email.send", constructor: "example.com/acme/implementation-use/smtp.New", want: "parse exact Interface ID"},
		{name: "intrinsic Interface", interfaceID: "kernel.health/v1", constructor: "example.com/acme/implementation-use/smtp.New", want: "intrinsic kernel.* Interface"},
		{name: "absent Interface", interfaceID: "missing.operation/v1", constructor: "example.com/acme/implementation-use/smtp.New", want: "unknown Interface"},
		{name: "invalid constructor", interfaceID: "email.send/v1", constructor: "example.com/acme/implementation-use/smtp.new", want: "parse fully qualified Implementation constructor"},
		{name: "unknown constructor", interfaceID: "email.send/v1", constructor: "example.com/acme/implementation-use/missing.New", want: "unknown Implementation constructor"},
		{name: "incompatible constructor", interfaceID: "email.send/v1", constructor: "example.com/acme/implementation-use/reports.New", want: "incompatible Implementation choice"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			before := commandTree(t, root)
			exitCode, stdout, stderr := runCommand(
				t,
				[]string{"use", test.interfaceID, test.constructor},
				filepath.Join(root, "smtp"),
				implementationSelectionCommandEnvironment(nil),
			)
			if exitCode != 1 || stdout != "" || !strings.Contains(stderr, test.want) {
				t.Fatalf("rejected plystra use = exit %d, stdout %q, stderr %q; want %q", exitCode, stdout, stderr, test.want)
			}
			if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected plystra use changed the Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestRunUseRejectsAmbientSelectorConflictWithoutMutation(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), "interfaces: {require: [email.send/v1]}\n")
	before := commandTree(t, root)
	environment := implementationSelectionCommandEnvironment(map[string]string{
		"PLYSTRA_CONFIG": "deploy/customer.yaml",
		"PLYSTRA_ENV":    "production",
	})
	exitCode, stdout, stderr := runCommand(
		t,
		[]string{"use", "email.send/v1", "example.com/acme/implementation-use/local.New"},
		filepath.Join(root, "smtp"),
		environment,
	)
	if exitCode != 1 || stdout != "" || !strings.Contains(stderr, "PLYSTRA_CONFIG and PLYSTRA_ENV cannot be used together") {
		t.Fatalf("ambient selector conflict = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("ambient selector conflict changed the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
	assertNoCommandTransactions(t, root)
}

func writeImplementationSelectionCommandProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	writeCommandFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module example.com/acme/implementation-use

go 1.26

require github.com/plystra/kernel v0.0.0

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "# Shared Implementation choices.\ninterfaces:\n  require: [email.send/v1]\n")
	writeCommandInterface(t, root, "email/send/v1", "sendv1", "email.send/v1", "Send")
	writeCommandInterface(t, root, "reports/read/v1", "readv1", "reports.read/v1", "Read")
	writeCommandImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
	writeCommandImplementation(t, root, "local", "email.send/v1", "email/send/v1", "Send")
	writeCommandImplementation(t, root, "reports", "reports.read/v1", "reports/read/v1", "Read")
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize Implementation command Project: %v", err)
	}
	return canonical
}

func writeCommandInterface(t testing.TB, root, relative, packageName, id, method string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, "interfaces", filepath.FromSlash(relative), "interface.go"), fmt.Sprintf(`package %s

import "context"

//plystra:interface %s
type Interface interface {
	%s(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`, packageName, id, method))
}

func writeCommandImplementation(t testing.TB, root, packageName, id, interfacePath, method string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, packageName, "implementation.go"), fmt.Sprintf(`package %s

import (
	"context"

	contract "example.com/acme/implementation-use/interfaces/%s"
)

type Service struct{}

//plystra:implements %s
func New() (*Service, error) { return &Service{}, nil }

func (*Service) %s(context.Context, contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}

var _ contract.Interface = (*Service)(nil)
`, packageName, interfacePath, id, method))
}

func commandHasImplementationChoice(manifest applicationmeta.Manifest, interfaceID, constructor string) bool {
	for _, choice := range manifest.ImplementationChoices() {
		if choice.InterfaceID().String() == interfaceID && choice.Constructor().String() == constructor {
			return true
		}
	}
	return false
}

func implementationSelectionCommandEnvironment(overrides map[string]string) []string {
	environment := commandGoEnvironment()
	filtered := environment[:0]
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, "PLYSTRA_CONFIG") || strings.EqualFold(name, "PLYSTRA_ENV") {
			continue
		}
		filtered = append(filtered, entry)
	}
	keys := make([]string, 0, len(overrides))
	for name := range overrides {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		filtered = append(filtered, name+"="+overrides[name])
	}
	return filtered
}
