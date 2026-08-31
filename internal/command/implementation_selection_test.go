package command_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/interfaceprovenance"
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

func TestRunUseRecordsDormantSelectionWithoutActivation(t *testing.T) {
	root := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	const selected = "example.com/acme/implementation-use/local.New"

	exitCode, stdout, stderr := runCommand(
		t,
		[]string{"use", "email.send/v1", selected},
		filepath.Join(root, "smtp"),
		implementationSelectionCommandEnvironment(nil),
	)
	wantOutput := "selected Implementation " + selected + " for email.send/v1 in " + filepath.Join(root, "plystra.yaml") + "\n"
	if exitCode != 0 || stdout != wantOutput || stderr != "" {
		t.Fatalf("dormant plystra use = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	manifest, err := applicationmeta.Parse(readCommandFile(t, root, "plystra.yaml"))
	if err != nil || len(manifest.InterfaceRequirements()) != 0 || !commandHasImplementationChoice(manifest, "email.send/v1", selected) {
		t.Fatalf("dormant selected configuration = %#v, %v", manifest, err)
	}
	provenance, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("decode dormant generated manifest: %v", err)
	}
	interfaceProvenance := provenance.InterfaceProvenance()
	if len(interfaceProvenance.Bindings()) != 0 || len(interfaceProvenance.Constructors()) != 0 {
		t.Fatalf("dormant selection entered binding or constructor provenance: %#v", interfaceProvenance)
	}
	tree := commandTree(t, root)
	for name := range tree {
		if strings.HasPrefix(name, "generated/go/proxies/email/send/v1/") || strings.HasPrefix(name, "generated/go/adapters/implementations/email/send/v1/") {
			t.Fatalf("dormant selection emitted reachable Interface artifact %s", name)
		}
	}
	assembly := string(tree["generated/go/assembly/interfaces_gen.go"])
	if assembly == "" || strings.Contains(assembly, selected) {
		t.Fatalf("dormant selection entered generated assembly:\n%s", assembly)
	}

	for _, check := range []struct {
		arguments []string
		stdout    string
	}{
		{
			arguments: []string{"generate", "--check"},
			stdout:    "generated output is current for example.com/acme/implementation-use in " + root + "\n",
		},
		{
			arguments: []string{"check"},
			stdout:    "Project checks passed for example.com/acme/implementation-use in " + root + "\n",
		},
	} {
		before := commandTree(t, root)
		exitCode, stdout, stderr = runCommand(t, check.arguments, filepath.Join(root, "local"), implementationSelectionCommandEnvironment(nil))
		if exitCode != 0 || stdout != check.stdout || stderr != "" {
			t.Fatalf("%v after dormant use = exit %d, stdout %q, stderr %q", check.arguments, exitCode, stdout, stderr)
		}
		if after := commandTree(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("%v mutated dormant Project:\nbefore: %#v\nafter:  %#v", check.arguments, before, after)
		}
	}
	assertNoCommandTransactions(t, root)
}

func TestRunUseConvergesBeforeAndAfterInterfaceRequirement(t *testing.T) {
	const selected = "example.com/acme/implementation-use/local.New"
	environment := implementationSelectionCommandEnvironment(nil)

	useFirst := writeImplementationSelectionCommandProject(t)
	writeCommandFile(t, filepath.Join(useFirst, "plystra.yaml"), "{}\n")
	exitCode, stdout, stderr := runCommand(t, []string{"use", "email.send/v1", selected}, filepath.Join(useFirst, "smtp"), environment)
	if exitCode != 0 || stderr != "" || stdout != "selected Implementation "+selected+" for email.send/v1 in "+filepath.Join(useFirst, "plystra.yaml")+"\n" {
		t.Fatalf("use before require = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	dormantManifest, err := applicationmeta.Parse(readCommandFile(t, useFirst, "plystra.yaml"))
	if err != nil || len(dormantManifest.InterfaceRequirements()) != 0 || !commandHasImplementationChoice(dormantManifest, "email.send/v1", selected) {
		t.Fatalf("recorded dormant selection = %#v, %v", dormantManifest, err)
	}
	writeCommandFile(t, filepath.Join(useFirst, "plystra.yaml"), "interfaces: {require: [email.send/v1], use: {email.send/v1: "+selected+"}}\n")
	exitCode, stdout, stderr = runCommand(t, []string{"generate"}, filepath.Join(useFirst, "local"), environment)
	if exitCode != 0 || stderr != "" || stdout != "generated example.com/acme/implementation-use in "+useFirst+"\n" {
		t.Fatalf("generate after dormant selection activation = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	useFirstProvenance, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, useFirst, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("decode use-before-require manifest: %v", err)
	}

	requireFirst := writeImplementationSelectionCommandProject(t)
	exitCode, stdout, stderr = runCommand(t, []string{"use", "email.send/v1", selected}, filepath.Join(requireFirst, "smtp"), environment)
	if exitCode != 0 || stderr != "" || stdout != "selected Implementation "+selected+" for email.send/v1 in "+filepath.Join(requireFirst, "plystra.yaml")+"\n" {
		t.Fatalf("use after require = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	requireFirstProvenance, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, requireFirst, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("decode require-before-use manifest: %v", err)
	}

	if useFirstProvenance.SelectedDigest() != requireFirstProvenance.SelectedDigest() {
		t.Fatalf("authoring order changed normalized selected configuration: %q != %q", useFirstProvenance.SelectedDigest(), requireFirstProvenance.SelectedDigest())
	}
	if useFirstProvenance.ApplicationModelDigest() != requireFirstProvenance.ApplicationModelDigest() {
		t.Fatalf("authoring order changed application_model_digest: %q != %q", useFirstProvenance.ApplicationModelDigest(), requireFirstProvenance.ApplicationModelDigest())
	}
	for sequence, provenance := range map[string]applicationgen.ManifestProvenance{
		"use before require": useFirstProvenance,
		"require before use": requireFirstProvenance,
	} {
		bindings := provenance.InterfaceProvenance().Bindings()
		constructors := provenance.InterfaceProvenance().Constructors()
		if len(bindings) != 1 || bindings[0].InterfaceID() != "email.send/v1" || bindings[0].Selection().Constructor() != selected || bindings[0].Selection().Reason() != interfaceprovenance.SelectionExplicit || len(constructors) != 1 || constructors[0].Symbol() != selected {
			t.Fatalf("%s executable Interface model = bindings %#v, constructors %#v", sequence, bindings, constructors)
		}
	}
	if useFirstAssembly, requireFirstAssembly := readCommandFile(t, useFirst, "generated/go/assembly/interfaces_gen.go"), readCommandFile(t, requireFirst, "generated/go/assembly/interfaces_gen.go"); !reflect.DeepEqual(useFirstAssembly, requireFirstAssembly) {
		t.Fatalf("authoring order changed static Interface assembly:\nuse before require:\n%s\nrequire before use:\n%s", useFirstAssembly, requireFirstAssembly)
	}
	assertNoCommandTransactions(t, useFirst)
	assertNoCommandTransactions(t, requireFirst)
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

func TestRunUseClassifiesMalformedInputsWithoutMutation(t *testing.T) {
	tests := []struct {
		name        string
		interfaceID string
		constructor string
		selectors   []string
		environment map[string]string
		problem     string
		recovery    string
		diagnostic  string
		reject      string
	}{
		{
			name:        "invalid Interface preserves explicit environment",
			interfaceID: "email.send",
			constructor: "example.com/acme/implementation-use/smtp.New",
			selectors:   []string{"--env", "production"},
			environment: map[string]string{
				"PLYSTRA_CONFIG": "deploy/ignored.yaml",
				"PLYSTRA_ENV":    "ignored",
			},
			problem:    "parse exact Interface ID",
			recovery:   "Rerun `plystra use <interface-id> <constructor-symbol> --env \"production\"` with one canonical versioned Interface ID.",
			diagnostic: diagnosticcode.UseInterfaceInvalid,
			reject:     "ignored",
		},
		{
			name:        "invalid constructor preserves ambient configuration",
			interfaceID: "email.send/v1",
			constructor: "example.com/acme/implementation-use/smtp.new",
			environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"},
			problem:     "parse fully qualified Implementation constructor",
			recovery:    "Rerun `plystra use <interface-id> <constructor-symbol> --config \"deploy/customer.yaml\"` with one visible fully qualified exported constructor symbol.",
			diagnostic:  diagnosticcode.UseConstructorInvalid,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeImplementationSelectionCommandProject(t)
			before := commandTree(t, root)
			arguments := append([]string{"use", test.interfaceID, test.constructor}, test.selectors...)
			exitCode, stdout, stderr := runCommand(
				t,
				arguments,
				filepath.Join(root, "smtp"),
				implementationSelectionCommandEnvironment(test.environment),
			)
			if exitCode != 1 || stdout != "" || !commandContainsAll(
				stderr,
				test.problem,
				"Recovery:\n"+test.recovery+"\n",
				"Diagnostic: "+test.diagnostic,
			) {
				t.Fatalf("rejected plystra use = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if strings.Count(stderr, "Recovery:") != 1 || strings.Count(stderr, "Diagnostic:") != 1 || test.reject != "" && strings.Contains(stderr, test.reject) {
				t.Fatalf("rejected plystra use emitted unstable diagnostic framing: %q", stderr)
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

func writeCommandConfigurableImplementation(t testing.TB, root, packageName, id, interfacePath, method string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, packageName, "implementation.go"), fmt.Sprintf(`package %s

import (
	"context"

	contract "example.com/acme/implementation-use/interfaces/%s"
)

type Config struct {
	Endpoint string `+"`plystra:\"required\"`"+`
}

type Service struct{}

//plystra:implements %s
func New(Config) (*Service, error) { return &Service{}, nil }

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
