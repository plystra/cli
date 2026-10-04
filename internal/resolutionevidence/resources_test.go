package resolutionevidence_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfaceprovenance"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestResourceEvidenceProjectsResolvedGraphWithoutPrivateValues(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":               "module example.com/app\n\ngo 1.26\n",
		"plystra.yaml":         "{}\n",
		"database/resource.go": "package database\n//plystra:resource data.database/v1\ntype Resource interface{Read()}\n",
		"view/resource.go":     "package view\n//plystra:resource data.view/v1\ntype Resource interface{View()}\n",
		"provider/new.go":      "package provider\ntype Config struct{ Password string `plystra-default:\"PRIVATE_DEFAULT\"` }\ntype Value struct{}\nfunc (*Value) Read(){}\n//plystra:implements-resource data.database/v1\nfunc New(cfg Config)(*Value,error){panic(\"PRIVATE_CONSTRUCTOR\")}\n",
		"wrapper/new.go":       "package wrapper\nimport api \"example.com/app/database\"\ntype Value struct{}\nfunc (*Value) View(){}\n//plystra:implements-resource data.view/v1\nfunc New(upstream api.Resource)(*Value,error){panic(\"PRIVATE_CONSTRUCTOR\")}\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	project, err := projectlocate.Find(root)
	if err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	modules, err := moduledependency.Discover(t.Context(), project, moduledependency.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := interfaceinventory.DiscoverApplication(t.Context(), project, modules, interfaceinventory.Options{Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := constructorsymbol.Parse("example.com/app/provider.New")
	if err != nil {
		t.Fatal(err)
	}
	wrapper, err := constructorsymbol.Parse("example.com/app/wrapper.New")
	if err != nil {
		t.Fatal(err)
	}
	sources := []constructorgraph.ResourceSource{{Reference: "PRIVATE_REFERENCE", ModulePath: "example.com/app", Path: "plystra.yaml", Line: 1, Column: 1}}
	input := constructorgraph.Input{ResourceProviders: discovery.ResourceProviders(), ResourceInstances: []constructorgraph.ResourceInstanceInput{{Name: "database.primary", Provider: provider, Sources: sources}, {Name: "database.unconsumed", Provider: provider, Sources: sources}, {Name: "view", Provider: wrapper, Sources: sources}}, ResourceBindings: []constructorgraph.ResourceBindingInput{{Namespace: constructorgraph.ResourceConsumerInstance, Consumer: "view", Parameter: "upstream", Target: "database.primary", Sources: sources}}}
	graph, err := constructorgraph.Build(input)
	if err != nil {
		t.Fatal(err)
	}
	base, err := resolutionevidence.Build(resolutionEvidenceInput(t, selectedContext(t, false, "a", true), participatingModules(false), participatingPluginCandidates(false)))
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := resolutionevidence.WithResources(base, graph, discovery.Resources())
	if err != nil || !evidence.Valid() {
		t.Fatalf("WithResources: %v", err)
	}
	if len(evidence.Resources()) != 3 || len(evidence.ResourceBindings()) != 1 || len(base.Resources()) != 0 || base.Digest() == evidence.Digest() || base.BuildModelDigest() != evidence.BuildModelDigest() {
		t.Fatal("invalid Resource evidence identity or membership")
	}
	resources, bindings := evidence.Resources(), evidence.ResourceBindings()
	if resources[0].Provider != resources[1].Provider || resources[0].Name == resources[1].Name || resources[0].ConfigurationOwner == resources[1].ConfigurationOwner || resources[0].ModuleVersion != "local" || resources[2].Name != "view" || bindings[0].ConsumerKind != "instances" || bindings[0].ParameterName != "upstream" || bindings[0].InstanceName != "database.primary" || bindings[0].Reason != interfaceprovenance.SelectionExplicit {
		t.Fatalf("incomplete records: %#v %#v", resources, bindings)
	}
	before := evidence.CanonicalJSON()
	for _, private := range []string{root, filepath.ToSlash(root), "PRIVATE_DEFAULT", "PRIVATE_CONSTRUCTOR", "PRIVATE_REFERENCE"} {
		if bytes.Contains(before, []byte(private)) {
			t.Fatalf("evidence leaks %q", private)
		}
	}
	resources[0].SelectionSources[0].Path = "changed.yaml"
	bindings[0].BindingSources[0].Path = "changed.yaml"
	bindings[0].SelectionSources[0].Path = "changed.yaml"
	bindings[0].ConsumerSelectionSources[0].Path = "changed.yaml"
	if !evidence.Valid() || !bytes.Equal(before, evidence.CanonicalJSON()) {
		t.Fatal("mutable Resource evidence escaped")
	}
	projectedResources, projectedBindings, err := interfaceprovenance.ResourceInputs(graph, discovery.Resources())
	if err != nil || !reflect.DeepEqual(projectedResources, evidence.Resources()) || !reflect.DeepEqual(projectedBindings, evidence.ResourceBindings()) {
		t.Fatalf("live and persisted Resource schemas disagree: %v", err)
	}
	if _, err := resolutionevidence.WithResources(base, graph, interfaceinventory.ResourceIndex{}); !errors.Is(err, interfaceprovenance.ErrInvalid) {
		t.Fatalf("missing Resource catalog accepted: %v", err)
	}
	if _, err := resolutionevidence.WithResources(resolutionevidence.Evidence{}, graph, discovery.Resources()); !errors.Is(err, resolutionevidence.ErrBuild) {
		t.Fatalf("invalid base accepted: %v", err)
	}
	for name, content := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || string(data) != content {
			t.Fatalf("Resource evidence changed authored %s: %v", name, err)
		}
	}
	if strings.Contains(string(before), `"interface_id":"data.database/v1"`) {
		t.Fatal("Resource became an Interface binding")
	}
}
