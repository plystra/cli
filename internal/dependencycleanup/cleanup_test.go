package dependencycleanup_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/dependencycleanup"
	"github.com/plystra/cli/internal/modulemutation"
	"golang.org/x/mod/module"
)

func TestPlanRemovesOnlyResourceBindingWhoseParameterDisappeared(t *testing.T) {
	t.Parallel()
	proxy := writeProviderProxy(t)
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire example.com/dep v1.0.0\n")
	writeFile(t, filepath.Join(root, "project.go"), "package app\n")
	original := `resources:
  instances:
    primary:
      use: example.com/dep/provider.New
    leaf:
      use: example.com/dep/leaf.New
  bind:
    instances:
      primary:
        old: leaf
        retained: leaf
`
	writeFile(t, filepath.Join(root, "plystra.yaml"), original)
	environment := providerEnvironment(t, proxy)
	runGo(t, root, environment, "get", "example.com/dep@v1.0.0")
	before, err := dependencycleanup.Capture(t.Context(), dependencycleanup.Options{Start: root, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	writeProviderVersion(t, root, environment, "v1.1.0")
	before.ObserveChange(true)

	plan, err := dependencycleanup.Plan(context.Background(), before, changerequest.DependencyUpdate, "example.com/dep@v1.1.0")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !plan.Changed {
		t.Fatal("Plan reported no ownership change")
	}
	impact := plan.ConfigurationImpact()
	if !impact.Valid() || len(impact.DerivedOperations()) != 1 || len(impact.NewlyUnownedPaths()) != 1 {
		t.Fatalf("cleanup impact = %s", impact.CanonicalJSON())
	}
	want := `resources:
  instances:
    primary:
      use: example.com/dep/provider.New
    leaf:
      use: example.com/dep/leaf.New
  bind:
    instances:
      primary:
        retained: leaf
`
	if !bytes.Equal(plan.Data, []byte(want)) {
		t.Fatalf("planned document = %q, want %q", plan.Data, want)
	}
	if got := string(readFile(t, filepath.Join(root, "plystra.yaml"))); got != original {
		t.Fatalf("Plan mutated selected document: %q", got)
	}
}

const providerDocument = `resources:
  instances:
    primary: {use: example.com/dep/provider.New}
    leaf: {use: example.com/dep/leaf.New}
  bind:
    instances:
      primary: {old: leaf, retained: leaf}
`

func providerProject(t *testing.T, version, document string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\nrequire example.com/dep "+version+"\n")
	writeFile(t, filepath.Join(root, "project.go"), "package app\n//plystra:resource local.cache/v1\ntype Resource interface { Value() string }\n")
	writeFile(t, filepath.Join(root, "plystra.yaml"), document)
	environment := providerEnvironment(t, writeProviderProxy(t))
	runGo(t, root, environment, "get", "example.com/dep@"+version)
	return root, environment
}

func captureOwnership(t *testing.T, root string, environment []string) dependencycleanup.Snapshot {
	t.Helper()
	before, err := dependencycleanup.Capture(t.Context(), dependencycleanup.Options{Start: root, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	return before
}

func TestPlanRequiresExactOldParameterProof(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, old, next, document string
		valid                     bool
	}{
		{"declared-old-parameter", "v1.0.0", "v1.1.0", providerDocument, true},
		{"undeclared-old-parameter", "v1.1.0", "v1.2.0", providerDocument, false},
		{"unknown-old-declarations", "v0.9.0", "v1.1.0", providerDocument, false},
		{"unknown-old-repair-without-cleanup", "v0.9.0", "v1.1.0", strings.ReplaceAll(providerDocument, "old: leaf, ", ""), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root, environment := providerProject(t, test.old, test.document)
			before := captureOwnership(t, root, environment)
			writeProviderVersion(t, root, environment, test.next)
			before.ObserveChange(true)
			_, err := dependencycleanup.Plan(t.Context(), before, changerequest.DependencyUpdate, "example.com/dep@"+test.next)
			if (err == nil) != test.valid {
				t.Fatalf("Plan error = %v; valid = %v", err, test.valid)
			}
			if string(readFile(t, filepath.Join(root, "plystra.yaml"))) != test.document {
				t.Fatal("planning changed YAML")
			}
		})
	}
}

func TestPlanCleansOnlyProvenLostAutomaticOwners(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, old, next, roots string
		cleanup, valid         bool
	}{
		{"lost", "v1.0.0", "v1.1.0", "require: [echo.run/v1]", true, true},
		{"retained-other-root", "v1.0.0", "v1.2.0", "require: [echo.run/v1, echo.other/v1]", false, true},
		{"retained-dormant-choice", "v1.0.0", "v1.2.0", "require: [echo.run/v1], use: {echo.other/v1: example.com/dep/service.New}", false, true},
		{"unknown-old-owner", "v0.9.0", "v1.1.0", "require: [echo.run/v1]", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\nrequire example.com/dep "+test.old+"\n")
			document := "interfaces: {" + test.roots + "}\nconfig: {example.com/dep/service.New: {name: PRIVATE_OLD}}\n"
			writeFile(t, filepath.Join(root, "plystra.yaml"), document)
			environment := providerEnvironment(t, writeOwnerProxy(t))
			runGo(t, root, environment, "get", "example.com/dep@"+test.old)
			before := captureOwnership(t, root, environment)
			writeProviderVersion(t, root, environment, test.next)
			before.ObserveChange(true)
			plan, err := dependencycleanup.Plan(t.Context(), before, changerequest.DependencyUpdate, "example.com/dep@"+test.next)
			if (err == nil) != test.valid {
				t.Fatalf("Plan error = %v; valid = %v", err, test.valid)
			}
			if !test.valid {
				return
			}
			if plan.Changed != test.cleanup || bytes.Contains(plan.Data, []byte("PRIVATE_OLD")) == test.cleanup {
				t.Fatalf("cleanup = %v, expected %v", plan.Changed, test.cleanup)
			}
			if !test.cleanup && len(plan.ConfigurationImpact().RetainedOwners()) == 0 {
				t.Fatal("retained owner was not reported")
			}
		})
	}
}

func writeOwnerProxy(t *testing.T) string {
	t.Helper()
	proxy := filepath.Join(t.TempDir(), "proxy")
	for _, version := range []string{"v0.9.0", "v1.0.0", "v1.1.0", "v1.2.0"} {
		files := map[string]string{"go.mod": "module example.com/dep\n\ngo 1.26\n", "plystra.yaml": "{}\n"}
		for _, name := range []string{"run", "other"} {
			files[name+"/interface.go"] = "package " + name + "\nimport \"context\"\n//plystra:interface echo." + name + "/v1\ntype Interface interface { " + map[string]string{"run": "Run", "other": "Copy"}[name] + "(context.Context, Request)(Response,error) }\ntype Request struct{}\ntype Response struct{}\n"
		}
		implementation := "package service\nimport (\"context\"; \"example.com/dep/run\"; \"example.com/dep/other\")\ntype Config struct {Name string `yaml:\"name\"`}\ntype service struct{}\nfunc (*service) Run(context.Context,run.Request)(run.Response,error){return run.Response{},nil}\nfunc (*service) Copy(context.Context,other.Request)(other.Response,error){return other.Response{},nil}\n"
		if version == "v1.0.0" || version == "v0.9.0" {
			implementation += "//plystra:implements echo.run/v1\n//plystra:implements echo.other/v1\nfunc New(Config)(*service,error){return &service{},nil}\n"
		} else if version == "v1.1.0" {
			implementation += "//plystra:implements echo.run/v1\nfunc Replacement()(*service,error){return &service{},nil}\n"
		} else {
			implementation += "//plystra:implements echo.run/v1\nfunc Replacement()(*service,error){return &service{},nil}\n//plystra:implements echo.other/v1\nfunc New(Config)(*service,error){return &service{},nil}\n"
		}
		if version == "v0.9.0" {
			implementation += "func broken(\n"
		}
		files["service/service.go"] = implementation
		writeModuleZip(t, proxy, "example.com/dep", version, files)
	}
	return proxy
}

func TestOwnershipTransactionFailureAndConcurrentEditGuards(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"success", "validation-failure", "cancel", "selected-edit", "old-source-edit", "local-source-edit"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			root, environment := providerProject(t, "v1.0.0", providerDocument)
			before := captureOwnership(t, root, environment)
			modBefore := readFile(t, filepath.Join(root, "go.mod"))
			sumBefore := readFile(t, filepath.Join(root, "go.sum"))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("injected validation failure")
			concurrent := providerDocument + "# PRIVATE_CONCURRENT\n"
			err := modulemutation.Change(ctx, root, modulemutation.ChangeOptions{Environment: environment, Arguments: []string{"get", "example.com/dep@v1.1.0"}, Observe: before.ObserveChange}, func(mutate applicationgenerate.ModuleMutation) error {
				plan, err := dependencycleanup.Plan(ctx, before, changerequest.DependencyUpdate, "example.com/dep@v1.1.0")
				if err != nil {
					return err
				}
				return dependencycleanup.Commit(ctx, root, plan, func(root string) error {
					return plan.GuardMutation(mutate)(ctx, root, nil, func() error {
						switch scenario {
						case "validation-failure":
							return failure
						case "cancel":
							cancel()
						case "selected-edit":
							writeFile(t, filepath.Join(root, "plystra.yaml"), concurrent)
						case "old-source-edit":
							file := oldProviderPath(t, environment)
							if err := os.Chmod(file, 0o644); err != nil {
								t.Fatal(err)
							}
							writeFile(t, file, "package provider\n// PRIVATE_OLD_EDIT\n")
						case "local-source-edit":
							writeFile(t, filepath.Join(root, "project.go"), "package app\n// PRIVATE_LOCAL_EDIT\n")
						}
						return nil
					})
				})
			})
			if scenario == "success" {
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(readFile(t, filepath.Join(root, "plystra.yaml")), []byte("old:")) {
					t.Fatal("obsolete binding survived")
				}
				return
			}
			if err == nil {
				t.Fatal("failed transaction committed")
			}
			if scenario == "validation-failure" && !errors.Is(err, failure) {
				t.Fatal(err)
			}
			if scenario == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if strings.HasSuffix(scenario, "edit") && !errors.Is(err, applicationresolve.ErrConcurrentChange) {
				t.Fatal(err)
			}
			want := providerDocument
			if scenario == "selected-edit" {
				want = concurrent
			}
			if string(readFile(t, filepath.Join(root, "plystra.yaml"))) != want {
				t.Fatal("YAML rollback overwrote concurrent input or lost preimage")
			}
			if !bytes.Equal(modBefore, readFile(t, filepath.Join(root, "go.mod"))) || !bytes.Equal(sumBefore, readFile(t, filepath.Join(root, "go.sum"))) {
				t.Fatal("module metadata did not roll back exactly")
			}
			if scenario == "local-source-edit" && !bytes.Contains(readFile(t, filepath.Join(root, "project.go")), []byte("PRIVATE_LOCAL_EDIT")) {
				t.Fatal("concurrent local edit was lost")
			}
		})
	}
}

func oldProviderPath(t *testing.T, environment []string) string {
	t.Helper()
	for _, value := range environment {
		if root, ok := strings.CutPrefix(value, "GOMODCACHE="); ok {
			return filepath.Join(root, "example.com", "dep@v1.0.0", "provider", "provider.go")
		}
	}
	t.Fatal("module cache is absent")
	return ""
}

func TestCaptureAndPlanRejectStaleOldSources(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"yaml", "local-source", "old-source", "new-source"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			root, environment := providerProject(t, "v1.0.0", providerDocument)
			before := captureOwnership(t, root, environment)
			writeProviderVersion(t, root, environment, "v1.1.0")
			before.ObserveChange(true)
			switch scenario {
			case "yaml":
				writeFile(t, filepath.Join(root, "plystra.yaml"), providerDocument+"# PRIVATE_EDIT\n")
			case "local-source":
				writeFile(t, filepath.Join(root, "project.go"), "package app\n// PRIVATE_EDIT\n")
			case "new-source":
				writeFile(t, filepath.Join(root, "new.go"), "package app\n")
			case "old-source":
				name := oldProviderPath(t, environment)
				if err := os.Chmod(name, 0o644); err != nil {
					t.Fatal(err)
				}
				writeFile(t, name, "package provider\n")
			}
			if _, err := dependencycleanup.Plan(t.Context(), before, changerequest.DependencyUpdate, "example.com/dep@v1.1.0"); !errors.Is(err, applicationresolve.ErrConcurrentChange) {
				t.Fatalf("stale Plan = %v", err)
			}
		})
	}
}

func TestOwnershipSnapshotPreservesDiscoveryExclusions(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"unselected-yaml", "nested-module", "inactive-file", "new-inactive-package"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			root, environment := providerProject(t, "v1.0.0", providerDocument)
			writeFile(t, filepath.Join(root, "plystra.unselected.yaml"), "PRIVATE_OLD_UNSELECTED: [\n")
			writeFile(t, filepath.Join(root, "nested/go.mod"), "module example.com/nested\n\ngo 1.26\n")
			writeFile(t, filepath.Join(root, "nested/source.go"), "package nested\n//plystra:resource nested.old/v1\ntype Value interface { Value() string }\n")
			writeFile(t, filepath.Join(root, "ignored.go"), "//go:build never_ownership_fixture\n\npackage app\n")
			before := captureOwnership(t, root, environment)
			writeProviderVersion(t, root, environment, "v1.1.0")
			before.ObserveChange(true)
			switch scenario {
			case "unselected-yaml":
				writeFile(t, filepath.Join(root, "plystra.unselected.yaml"), "PRIVATE_NEW_UNSELECTED: {\n")
			case "nested-module":
				writeFile(t, filepath.Join(root, "nested/source.go"), "package nested\n//plystra:resource nested.new/v1\ntype Other interface { Ping() }\n")
			case "inactive-file":
				writeFile(t, filepath.Join(root, "ignored.go"), "//go:build never_ownership_fixture\n\npackage app\n//plystra:resource ignored.invalid/v1\nfunc broken(\n")
			case "new-inactive-package":
				writeFile(t, filepath.Join(root, "inactive/source.go"), "//go:build never_ownership_fixture\n\npackage inactive\n//plystra:resource ignored.invalid/v1\nfunc broken(\n")
			}
			plan, err := dependencycleanup.Plan(t.Context(), before, changerequest.DependencyUpdate, "example.com/dep@v1.1.0")
			if err != nil || !plan.Changed {
				t.Fatalf("excluded input blocked exact cleanup: %v", err)
			}
		})
	}
}

func TestOwnershipSnapshotBindsCommandPostimage(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"unobserved", "changed-postimage", "duplicate-observation"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			root, environment := providerProject(t, "v1.0.0", providerDocument)
			before := captureOwnership(t, root, environment)
			writeProviderVersion(t, root, environment, "v1.1.0")
			if scenario != "unobserved" {
				before.ObserveChange(true)
			}
			if scenario == "changed-postimage" {
				appendFile(t, filepath.Join(root, "go.mod"), "// PRIVATE_CONCURRENT_MODULE_EDIT\n")
			}
			if scenario == "duplicate-observation" {
				before.ObserveChange(true)
			}
			if _, err := dependencycleanup.Plan(t.Context(), before, changerequest.DependencyUpdate, "example.com/dep@v1.1.0"); !errors.Is(err, applicationresolve.ErrConcurrentChange) {
				t.Fatalf("changed command boundary accepted: %v", err)
			}
		})
	}
}

func TestOwnershipGuardRejectsEditsBeforeNormalization(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"requirement", "old-source"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			root, environment := providerProject(t, "v1.0.0", providerDocument)
			before := captureOwnership(t, root, environment)
			var concurrent []byte
			mutated := false
			err := modulemutation.Change(t.Context(), root, modulemutation.ChangeOptions{Environment: environment, Arguments: []string{"get", "example.com/dep@v1.1.0"}, Observe: before.ObserveChange}, func(mutate applicationgenerate.ModuleMutation) error {
				plan, err := dependencycleanup.Plan(t.Context(), before, changerequest.DependencyUpdate, "example.com/dep@v1.1.0")
				if err != nil {
					return err
				}
				return dependencycleanup.Commit(t.Context(), root, plan, func(root string) error {
					if scenario == "requirement" {
						runGo(t, root, environment, "mod", "edit", "-droprequire=example.com/dep")
						concurrent = readFile(t, filepath.Join(root, "go.mod"))
					} else {
						writeFile(t, filepath.Join(root, "project.go"), "package app\n// PRIVATE_CONCURRENT_SOURCE\n")
					}
					return plan.GuardMutation(func(ctx context.Context, root string, requirements []applicationgenerate.ModuleRequirement, validate func() error) error {
						mutated = true
						return mutate(ctx, root, requirements, validate)
					})(t.Context(), root, nil, func() error { return nil })
				})
			})
			if !errors.Is(err, applicationresolve.ErrConcurrentChange) || mutated {
				t.Fatalf("normalization entered after concurrent edit: mutated=%t err=%v", mutated, err)
			}
			if scenario == "requirement" && !bytes.Equal(concurrent, readFile(t, filepath.Join(root, "go.mod"))) {
				t.Fatal("normalization or rollback overwrote concurrent requirement removal")
			}
			if string(readFile(t, filepath.Join(root, "plystra.yaml"))) != providerDocument {
				t.Fatal("selected document was not restored")
			}
		})
	}
}

func TestOwnershipSnapshotSupportsForkAndLegacyModules(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"fork", "legacy-support", "legacy-workspace", "legacy-workspace-override"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			proxy := t.TempDir()
			root := t.TempDir()
			mod := "module example.com/app\ngo 1.26\nrequire example.com/original v1.0.0\n"
			if scenario == "fork" {
				mod += "replace example.com/original => example.com/fork v1.0.0\n"
				writeModuleZip(t, proxy, "example.com/fork", "v1.0.0", map[string]string{
					"go.mod": "module example.com/fork\ngo 1.26\n", "plystra.yaml": "{}\n",
					"api/resource.go": "package api\n//plystra:resource fork.cache/v1\ntype Resource interface { Ping() }\n",
				})
			} else {
				writeModuleZip(t, proxy, "example.com/original", "v1.0.0", map[string]string{"types.go": "package original\ntype Value struct{}\n"})
				if strings.HasPrefix(scenario, "legacy-workspace") {
					writeModuleZip(t, proxy, "example.com/fork", "v1.0.0", map[string]string{"types.go": "package original\ntype Value struct{}\n"})
				}
				if scenario == "legacy-workspace-override" {
					mod += "replace example.com/original v1.0.0 => example.com/module-fork v1.0.0\n"
					writeModuleZip(t, proxy, "example.com/module-fork", "v1.0.0", map[string]string{"types.go": "package original\ntype Value struct{}\n"})
				}
				writeFile(t, filepath.Join(root, "resource.go"), "package app\nimport \"example.com/original\"\n//plystra:resource app.cache/v1\ntype Resource interface { Read() original.Value }\n")
			}
			writeFile(t, filepath.Join(root, "go.mod"), mod)
			writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
			environment := providerEnvironment(t, proxy)
			runGo(t, root, environment, "mod", "download", "all")
			if strings.HasPrefix(scenario, "legacy-workspace") {
				work := filepath.Join(root, "go.work")
				writeFile(t, work, "go 1.26\nuse .\nreplace example.com/original v1.0.0 => example.com/fork v1.0.0\n")
				environment = append(environment, "GOWORK="+work)
				runGo(t, root, environment, "mod", "download", "all")
			}
			modBefore := readFile(t, filepath.Join(root, "go.mod"))
			sumBefore := readFile(t, filepath.Join(root, "go.sum"))
			options := applicationresolve.Options{Start: root, Environment: environment}
			if _, err := applicationresolve.DiscoverSelectionInputs(t.Context(), options); err != nil {
				t.Fatalf("ordinary discovery rejected supported fixture: %v", err)
			}
			snapshot, err := applicationresolve.CaptureDependencyOwnership(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Previous().Module().Path() == "" {
				t.Fatal("supported old discovery lost declaration proof")
			}
			if !bytes.Equal(modBefore, readFile(t, filepath.Join(root, "go.mod"))) || !bytes.Equal(sumBefore, readFile(t, filepath.Join(root, "go.sum"))) {
				t.Fatal("source capture modified authored module metadata")
			}
			appendFile(t, filepath.Join(root, "go.mod"), "// intentional module postimage\n")
			if err := snapshot.ValidateAuthoredSnapshot(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			dependency, exists := snapshot.Previous().Dependencies().ByPath("example.com/original")
			if !exists {
				t.Fatal("fixture dependency missing")
			}
			if scenario != "fork" {
				if _, err := os.Stat(filepath.Join(dependency.Root(), "go.mod")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("legacy source acquired a go.mod: %v", err)
				}
			}
			name := filepath.Join(dependency.Root(), "api", "resource.go")
			if scenario != "fork" {
				name = filepath.Join(dependency.Root(), "types.go")
			}
			info, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(name, info.Mode()|0o200); err != nil {
				t.Fatal(err)
			}
			appendFile(t, name, "// PRIVATE_CAPTURED_TYPE_EDIT\n")
			if err := os.Chmod(name, info.Mode()); err != nil {
				t.Fatal(err)
			}
			if err := snapshot.ValidateAuthoredSnapshot(t.Context(), nil); !errors.Is(err, applicationresolve.ErrConcurrentChange) {
				t.Fatalf("captured source edit accepted: %v", err)
			}
		})
	}
}

func writeProviderProxy(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proxy")
	for _, version := range []string{"v0.9.0", "v1.0.0", "v1.1.0", "v1.2.0"} {
		provider := `package provider
import "example.com/dep/api"
type database struct{}
func (*database) Ping() {}
//plystra:implements-resource storage.database/v1
` + "func New(" + func() string {
			if version == "v1.0.0" {
				return "old api.Resource, retained api.Resource"
			}
			return "retained api.Resource"
		}() + `)(*database,error){ return &database{}, nil }
`
		if version == "v0.9.0" {
			provider += "func broken(\n"
		}
		writeModuleZip(t, root, "example.com/dep", version, map[string]string{
			"go.mod":               "module example.com/dep\n\ngo 1.26\n",
			"api/api.go":           "package api\n//plystra:resource storage.database/v1\ntype Resource interface { Ping() }\n",
			"provider/provider.go": provider,
			"leaf/leaf.go":         "package leaf\ntype database struct{}\nfunc (*database) Ping() {}\n//plystra:implements-resource storage.database/v1\nfunc New()(*database,error){ return &database{}, nil }\n",
			"plystra.yaml":         "{}\n",
		})
	}
	return root
}

func writeProviderVersion(t *testing.T, root string, environment []string, version string) {
	t.Helper()
	runGo(t, root, environment, "get", "example.com/dep@"+version)
}

func runGo(t *testing.T, root string, environment []string, arguments ...string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "go", arguments...)
	command.Dir = root
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go %v: %v\n%s", arguments, err, output)
	}
}

func providerEnvironment(t *testing.T, proxy string) []string {
	t.Helper()
	proxyPath := filepath.ToSlash(proxy)
	if runtime.GOOS == "windows" {
		proxyPath = "/" + proxyPath
	}
	proxyURL, err := url.Parse((&url.URL{Scheme: "file", Path: proxyPath}).String())
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(filepath.Join(temp, "modcache"), func(path string, entry os.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				_ = os.Chmod(path, 0o755)
			}
			return nil
		})
	})
	for _, name := range []string{"modcache", "gocache", "gotmp", "tmp", "temp"} {
		if err := os.MkdirAll(filepath.Join(temp, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return append(os.Environ(),
		"GOWORK=off",
		"GOPROXY="+proxyURL.String(),
		"GOSUMDB=off",
		"GONOSUMDB=*",
		"GONOPROXY=none",
		"GOMODCACHE="+filepath.Join(temp, "modcache"),
		"GOCACHE="+filepath.Join(temp, "gocache"),
		"GOTMPDIR="+filepath.Join(temp, "gotmp"),
		"TMP="+filepath.Join(temp, "tmp"),
		"TEMP="+filepath.Join(temp, "temp"),
	)
}

func writeModuleZip(t *testing.T, proxy, path, version string, files map[string]string) {
	t.Helper()
	escapedPath, err := module.EscapePath(path)
	if err != nil {
		t.Fatal(err)
	}
	escapedVersion, err := module.EscapeVersion(version)
	if err != nil {
		t.Fatal(err)
	}
	versionRoot := filepath.Join(proxy, filepath.FromSlash(escapedPath), "@v")
	if err := os.MkdirAll(versionRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	appendFile(t, filepath.Join(versionRoot, "list"), version+"\n")
	writeFile(t, filepath.Join(versionRoot, escapedVersion+".info"), fmt.Sprintf("{\"Version\":%q,\"Time\":%q}\n", version, "2026-10-05T00:00:00Z"))
	goMod := []byte(files["go.mod"])
	if len(goMod) == 0 {
		goMod = []byte("module " + path + "\n")
	}
	writeFile(t, filepath.Join(versionRoot, escapedVersion+".mod"), string(goMod))
	archiveFile, err := os.Create(filepath.Join(versionRoot, escapedVersion+".zip"))
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(archiveFile)
	prefix := path + "@" + version + "/"
	for name, data := range files {
		header := &zip.FileHeader{Name: prefix + name, Method: zip.Deflate}
		header.SetMode(0o444)
		header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			_ = archive.Close()
			_ = archiveFile.Close()
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(data)); err != nil {
			_ = archive.Close()
			_ = archiveFile.Close()
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		_ = archiveFile.Close()
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, data string) {
	t.Helper()
	previous, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	writeFile(t, path, string(previous)+data)
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
