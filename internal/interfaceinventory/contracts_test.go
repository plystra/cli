package interfaceinventory_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
)

func TestMain(main *testing.M) {
	if os.Getenv("PLYSTRA_CONTRACT_GO_HELPER") == "1" {
		arguments := os.Args[1:]
		if slices.Contains(arguments, "-export") && (slices.Contains(arguments, "example.com/app/pending") || slices.Contains(arguments, "example.com/app/provider")) {
			_, _ = fmt.Fprintln(os.Stderr, "attempted to compile a pending constructor package without an active contract")
			os.Exit(2)
		}
		command := exec.Command(os.Getenv("PLYSTRA_CONTRACT_REAL_GO"), arguments...)
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := command.Run(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(main.Run())
}

func TestDiscoverContractsExcludesPendingImplementationsAndProviders(t *testing.T) {
	base := t.TempDir()
	root, dependency := filepath.Join(base, "app"), filepath.Join(base, "dependency")
	writeProject(t, root, "example.com/app")
	writeProject(t, dependency, "example.com/dependency")
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\nrequire example.com/dependency v1.0.0\nreplace example.com/dependency => ../dependency\n")
	writeFile(t, filepath.Join(root, "api", "interface.go"), interfaceSource("api", "email.send/v1", "Send"))
	writeFile(t, filepath.Join(root, "api", "interface.yaml"), "semantics:\n  kind: command\n")
	writeFile(t, filepath.Join(dependency, "database", "resource.go"), "package database\n//plystra:resource storage.database/v1\ntype Resource interface { Health() error }\n")
	environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": ""})
	baseline := discoverApplication(t, root, environment)
	for _, owner := range []string{root, dependency} {
		writeFile(t, filepath.Join(owner, "pending", "implementation.go"), `package pending
import _ "example.com/app/not-yet-materialized"
//plystra:implements malformed
func New() {}
`)
		writeFile(t, filepath.Join(owner, "provider", "provider.go"), `package provider
import _ "example.com/app/another-pending-package"
//plystra:implements-resource malformed
func New() {}
`)
	}
	// An inactive contract does not make the active Implementation authoritative
	// to contract-only discovery, even when Go cannot compile that package yet.
	writeFile(t, filepath.Join(root, "pending", "inactive.go"), "//go:build contract_inactive\n\n"+interfaceSource("pending", "inactive.operation/v1", "Run"))
	writeFile(t, filepath.Join(root, "provider", "inactive.go"), "//go:build contract_inactive\n\npackage provider\n//plystra:resource inactive.resource/v1\ntype Resource interface { Read() error }\n")
	before, dependencyBefore := snapshotFiles(t, root), snapshotFiles(t, dependency)
	project, err := projectlocate.Find(root)
	if err != nil {
		t.Fatal(err)
	}
	dependencies, err := moduledependency.Discover(t.Context(), project, moduledependency.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	goCommand, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	helperEnvironment := append(append([]string(nil), environment...), "PLYSTRA_CONTRACT_GO_HELPER=1", "PLYSTRA_CONTRACT_REAL_GO="+goCommand)
	discovered, err := interfaceinventory.DiscoverContracts(t.Context(), project, dependencies, interfaceinventory.Options{GoCommand: helper, Environment: helperEnvironment})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inventorySummary(discovered.Interfaces()), inventorySummary(baseline.Interfaces())) || len(discovered.Resources().Resources()) != 1 {
		t.Fatal("contract-only discovery changed the typed contracts")
	}
	if discovered.Resources().Resources()[0].ContractDigest() != baseline.Resources().Resources()[0].ContractDigest() || discovered.Resources().Resources()[0].Local() {
		t.Fatal("dependency Resource lost its contract or provenance")
	}
	if len(discovered.Implementations().Implementations()) != 0 || len(discovered.ResourceProviders().Providers()) != 0 {
		t.Fatal("contract-only discovery returned constructor candidates")
	}
	if _, err := interfaceinventory.DiscoverApplication(t.Context(), project, dependencies, interfaceinventory.Options{Environment: environment}); err == nil {
		t.Fatal("full discovery unexpectedly accepted invalid pending constructors")
	}
	if !reflect.DeepEqual(before, snapshotFiles(t, root)) || !reflect.DeepEqual(dependencyBefore, snapshotFiles(t, dependency)) {
		t.Fatal("contract discovery changed authored inputs")
	}
}

func TestDiscoverContractsRetainsTypedSourceAndMetadataValidation(t *testing.T) {
	for _, failure := range []string{"interface type", "interface signature", "resource type", "metadata", "dependency import"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			writeProject(t, root, "example.com/app")
			source := interfaceSource("api", "email.send/v1", "Send")
			switch failure {
			case "interface type":
				source = strings.Replace(source, "Value string", "Value Undefined", 1)
			case "interface signature":
				source = strings.Replace(source, "(context.Context, Request)", "(context.Context, *Request)", 1)
			case "resource type":
				writeFile(t, filepath.Join(root, "resource", "resource.go"), "package resource\n//plystra:resource storage.database/v1\ntype Resource interface { Read(Undefined) }\n")
			case "metadata":
				writeFile(t, filepath.Join(root, "api", "interface.yaml"), "semantics:\n  kind: unsupported\n")
			case "dependency import":
				writeFile(t, filepath.Join(root, "api", "dependency.go"), "package api\nimport _ \"example.com/app/pending\"\n")
				writeFile(t, filepath.Join(root, "pending", "implementation.go"), "package pending\nimport _ \"example.com/app/absent\"\n//plystra:implements email.send/v1\nfunc New() {}\n")
			}
			writeFile(t, filepath.Join(root, "api", "interface.go"), source)
			project, err := projectlocate.Find(root)
			if err != nil {
				t.Fatal(err)
			}
			before := snapshotFiles(t, root)
			_, err = interfaceinventory.DiscoverContracts(t.Context(), project, moduledependency.Index{}, interfaceinventory.Options{Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOFLAGS": ""})})
			if !errors.Is(err, interfaceinventory.ErrDiscover) {
				t.Fatalf("invalid contract accepted: %v", err)
			}
			if !reflect.DeepEqual(before, snapshotFiles(t, root)) {
				t.Fatal("failed contract discovery changed authored files")
			}
		})
	}
}

func TestDiscoverContractsRequiresContextAndProject(t *testing.T) {
	project, err := projectlocate.Find(t.TempDir())
	if err == nil {
		t.Fatal("unexpected Project")
	}
	for _, ctx := range []context.Context{nil, t.Context()} {
		if _, err := interfaceinventory.DiscoverContracts(ctx, project, moduledependency.Index{}, interfaceinventory.Options{}); !errors.Is(err, interfaceinventory.ErrDiscover) {
			t.Fatalf("invalid discovery input: %v", err)
		}
	}
}
