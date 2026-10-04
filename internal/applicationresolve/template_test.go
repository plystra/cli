package applicationresolve_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
)

func TestResolveTemplateAncestryPreservesSelectorsAndGraphOrder(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement", "workspace"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			root := filepath.Join(parent, "app")
			oldest := filepath.Join(parent, "oldest")
			nearest := filepath.Join(parent, "nearest")
			ordinary := filepath.Join(parent, "ordinary")
			for _, module := range []struct{ root, path string }{{oldest, "example.com/z-oldest"}, {nearest, "example.com/a-nearest"}, {ordinary, "example.com/ordinary"}} {
				writeModule(t, module.root, module.path)
			}
			writeFile(t, filepath.Join(oldest, "plystra.yaml"), "interfaces: {require: [kernel.health/v1, kernel.info/v1]}\nhttp: {address: private-oldest:123, cors: {allowed_origins: [https://oldest.example.test]}}\ntimeouts: {startup: 99s}\n")
			writeFile(t, filepath.Join(nearest, "plystra.yaml"), "template: example.com/z-oldest\ninterfaces: {require: {remove: [kernel.info/v1]}}\nhttp: {cors: {allowed_origins: [https://nearest.example.test]}}\n")
			writeFile(t, filepath.Join(ordinary, "plystra.yaml"), "template: example.com/missing\ninterfaces: {require: [unknown.missing/v1]}\nconfig: inactive-private-value\n")
			writeFile(t, filepath.Join(nearest, "plystra.production.yaml"), "invalid: overlay-must-stay-inert\n")
			writeFile(t, filepath.Join(nearest, "deploy", "customer.yaml"), "invalid: replacement-must-stay-inert\n")
			writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire (\n example.com/z-oldest v1.2.0\n example.com/a-nearest v1.3.0\n example.com/ordinary v1.0.0\n)\nreplace example.com/z-oldest => ../oldest\nreplace example.com/a-nearest => ../nearest\nreplace example.com/ordinary => ../ordinary\n")
			writeFile(t, filepath.Join(root, "plystra.yaml"), "template: example.com/a-nearest\nhttp: {address: ':8123', cors: {allow_credentials: true}}\n")
			options := applicationresolve.Options{Start: root, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})}
			wantRequirements := []string{"kernel.health/v1"}
			switch mode {
			case "environment":
				writeFile(t, filepath.Join(root, "plystra.production.yaml"), "interfaces: {require: {add: [kernel.info/v1]}}\n")
				options.EnvironmentName = "production"
				wantRequirements = []string{"kernel.health/v1", "kernel.info/v1"}
			case "replacement":
				writeFile(t, filepath.Join(root, "plystra.yaml"), "template: example.com/a-nearest\ninterfaces: invalid-excluded-root-value\nhttp: {address: private-excluded-root}\n")
				writeFile(t, filepath.Join(root, "deploy", "customer.yaml"), "http: {address: ':8123'}\n")
				options.ConfigurationPath = "deploy/customer.yaml"
			case "workspace":
				work := filepath.Join(parent, "go.work")
				writeFile(t, work, "go 1.26\nuse (\n ./app\n ./oldest\n ./nearest\n ./ordinary\n)\n")
				options.Environment = goEnvironment(map[string]string{"GOWORK": work, "GOPROXY": "off"})
			}
			before := snapshotTree(t, parent)
			resolved, err := applicationresolve.Resolve(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.Template() != "example.com/a-nearest" {
				t.Fatalf("root relationship lost under %s selector", mode)
			}
			var requirements []string
			for _, requirement := range resolved.Manifest().InterfaceRequirements() {
				requirements = append(requirements, requirement.ID().String())
			}
			if !reflect.DeepEqual(requirements, wantRequirements) {
				t.Fatalf("requirements = %v, want %v", requirements, wantRequirements)
			}
			if address, exists := resolved.Manifest().HTTPAddress(); !exists || address != ":8123" {
				t.Fatalf("current process address = %q, %t", address, exists)
			}
			cors, exists := resolved.Manifest().HTTPCORS()
			if !exists || !reflect.DeepEqual(cors.AllowedOrigins, []string{"https://nearest.example.test"}) || cors.AllowCredentials != (mode != "replacement") || resolved.Manifest().StartupTimeout() != applicationmeta.DefaultStartupTimeout {
				t.Fatal("template CORS precedence or process startup ownership changed")
			}
			templates, err := resolved.RuntimeTemplates()
			if err != nil || len(templates) != 2 || templates[0].Module != "example.com/z-oldest" || templates[0].Template != "" || templates[1].Module != "example.com/a-nearest" || templates[1].Template != "example.com/z-oldest" {
				t.Fatalf("private ancestry order/relationships invalid: %v", err)
			}
			for _, layer := range templates {
				if strings.Contains(layer.YAML, "private-oldest") || strings.Contains(layer.YAML, "startup") || strings.Contains(layer.YAML, "template:") || strings.Contains(layer.YAML, "overlay-must") {
					t.Fatal("private ancestry retained excluded process/selector/relationship material")
				}
				if mode != "workspace" && layer.Version == "" {
					t.Fatal("selected Go Module version lost")
				}
			}
			if !reflect.DeepEqual(before, snapshotTree(t, parent)) {
				t.Fatal("template resolution mutated authored or dependency sources")
			}
		})
	}
}

func TestResolveTemplateAncestryRejectsInvalidEdgesWithSources(t *testing.T) {
	for _, test := range []struct {
		name, target, dependency string
		marker                   bool
		want                     error
		chain                    []string
	}{
		{"current cycle", "example.com/app", "", true, applicationresolve.ErrTemplateCycle, []string{"example.com/app", "example.com/app"}},
		{"dependency cycle", "example.com/base", "template: example.com/base\n", true, applicationresolve.ErrTemplateCycle, []string{"example.com/app", "example.com/base", "example.com/base"}},
		{"cycle to current", "example.com/base", "template: example.com/app\n", true, applicationresolve.ErrTemplateCycle, []string{"example.com/app", "example.com/base", "example.com/app"}},
		{"missing root target", "example.com/missing", "{}\n", true, applicationresolve.ErrTemplateNotFound, []string{"example.com/app", "example.com/missing"}},
		{"missing ancestor", "example.com/base", "template: example.com/missing\n", true, applicationresolve.ErrTemplateNotFound, []string{"example.com/app", "example.com/base", "example.com/missing"}},
		{"ordinary target", "example.com/base", "", false, applicationresolve.ErrTemplateNotProject, []string{"example.com/app", "example.com/base"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			root := filepath.Join(parent, "app")
			base := filepath.Join(parent, "base")
			writeModule(t, base, "example.com/base")
			if test.marker {
				writeFile(t, filepath.Join(base, "plystra.yaml"), test.dependency)
			}
			writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\nrequire example.com/base v1.0.0\nreplace example.com/base => ../base\n")
			writeFile(t, filepath.Join(root, "plystra.yaml"), "# root metadata\ntemplate: "+test.target+"\n")
			before := snapshotTree(t, parent)
			_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})})
			var detail *applicationresolve.TemplateError
			if !errors.Is(err, applicationresolve.ErrTemplate) || !errors.Is(err, test.want) || !errors.As(err, &detail) {
				t.Fatalf("Resolve = %v, want %v", err, test.want)
			}
			if !reflect.DeepEqual(detail.Modules(), test.chain) || len(detail.Sources()) != len(test.chain)-1 {
				t.Fatalf("template error lost chain/sources: %v", err)
			}
			for index, source := range detail.Sources() {
				line := 1
				if index == 0 {
					line = 2
				}
				if source.ModulePath() != test.chain[index] || source.Path() != "plystra.yaml" || source.Line() != line || source.Column() != 1 {
					t.Fatalf("template source %d = %#v", index, source)
				}
			}
			if strings.Contains(fmt.Sprint(err), parent) || !reflect.DeepEqual(before, snapshotTree(t, parent)) {
				t.Fatal("template failure leaked local paths or mutated inputs")
			}
		})
	}
}

func TestResolveTemplateMetadataFailuresAreLocatedAndRedacted(t *testing.T) {
	for _, mode := range []string{"root", "ancestor", "replacement", "overlay"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			root := filepath.Join(parent, "app")
			base := filepath.Join(parent, "base")
			writeModule(t, base, "example.com/base")
			writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\nrequire example.com/base v1.0.0\nreplace example.com/base => ../base\n")
			writeFile(t, filepath.Join(base, "plystra.yaml"), "{}\n")
			writeFile(t, filepath.Join(root, "plystra.yaml"), "template: example.com/base\n")
			options := applicationresolve.Options{Start: root, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})}
			owner, path := "example.com/app", "plystra.yaml"
			invalid := "# root-only relationship\ntemplate: [PRIVATE_INVALID_VALUE]\n"
			switch mode {
			case "root":
				writeFile(t, filepath.Join(root, path), invalid)
			case "ancestor":
				owner = "example.com/base"
				writeFile(t, filepath.Join(base, path), invalid)
			case "replacement":
				path = "deploy/customer.yaml"
				options.ConfigurationPath = path
				writeFile(t, filepath.Join(root, path), invalid)
			case "overlay":
				path = "plystra.production.yaml"
				options.EnvironmentName = "production"
				writeFile(t, filepath.Join(root, path), invalid)
			}
			_, err := applicationresolve.Resolve(t.Context(), options)
			var source *applicationresolve.ManifestSourceError
			if !errors.Is(err, applicationresolve.ErrTemplate) || !errors.As(err, &source) || source.ModulePath() != owner || source.SourcePath() != path || source.Line() != 2 || source.Column() != 1 {
				t.Fatalf("metadata failure source = %#v, %v", source, err)
			}
			if strings.Contains(err.Error(), "PRIVATE_INVALID_VALUE") || strings.Contains(err.Error(), parent) {
				t.Fatal("template metadata failure exposed private values or paths")
			}
		})
	}
}

func TestReplacementOfRootCannotRedeclareTemplateMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})
	for _, relationship := range []bool{false, true} {
		data := "interfaces: {require: [kernel.health/v1]}\n"
		if relationship {
			data += "template: example.com/missing\n"
		}
		writeFile(t, filepath.Join(root, "plystra.yaml"), data)
		before := snapshotTree(t, root)
		resolved, resolveErr := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, ConfigurationPath: "plystra.yaml", Environment: environment})
		_, targetErr := applicationresolve.SelectConfigurationTarget("example.com/app", root, "plystra.yaml", "", environment)
		for _, err := range []error{resolveErr, targetErr} {
			if !relationship {
				if err != nil {
					t.Fatal(err)
				}
				continue
			}
			var source *applicationresolve.ManifestSourceError
			if !errors.Is(err, applicationresolve.ErrConfigurationSelection) || !errors.Is(err, applicationresolve.ErrTemplate) || !errors.As(err, &source) || source.ModulePath() != "example.com/app" || source.SourcePath() != "plystra.yaml" || source.Line() != 2 || source.Column() != 1 {
				t.Fatalf("root selected as replacement = %v", err)
			}
		}
		if !relationship && (len(resolved.Manifest().InterfaceRequirements()) != 1 || resolved.Manifest().InterfaceRequirements()[0].ID().String() != "kernel.health/v1") {
			t.Fatal("root selected as replacement lost its application declarations")
		}
		if !reflect.DeepEqual(before, snapshotTree(t, root)) {
			t.Fatal("replacement metadata validation changed authored input")
		}
	}
}
