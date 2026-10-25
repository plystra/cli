package implementationselect_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/implementationselect"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/testkernel"
)

func TestDependencyWholeOwnerExclusionGeneratesAndLoads(t *testing.T) {
	t.Parallel()
	document := "interfaces: {require: [app.main/v1]}\nconfig: {" + dependencyOwner + ": {name: PRIVATE_OLD}}\n"
	fixture := dependencyFixtureOptions{fields: "Name string `yaml:\"name\"`", document: document, onlyService: true, mode: "environment"}
	old := dependencyInputs(t, fixture)
	fixture.onlyService, fixture.onlyAlternate = false, true
	current := dependencyInputs(t, fixture)
	plan, err := implementationselect.PreviewDependencyConfigurations(t.Context(), old, current, dependencyRequest(t, dependencyOperation))
	if err != nil {
		t.Fatal(err)
	}
	root := current.Module().Path()
	writeImplementationFile(t, filepath.Join(root, "plystra.test.yaml"), string(plan.Updated()))
	environment := implementationTestEnvironment()
	options := gocommand.Options{Directory: root, Environment: environment}
	if err := gocommand.Run(t.Context(), options, "mod", "edit", "-require=github.com/plystra/kernel@v0.0.0", "-replace=github.com/plystra/kernel="+testkernel.Root(t)); err != nil {
		t.Fatal(err)
	}
	// Add a known Secret field after the fixture has its Kernel dependency, so
	// the generated runtime also exercises strict lower-layer Secret syntax.
	implementationPath := filepath.Join(root, "alternate/implementation.go")
	implementation, err := os.ReadFile(implementationPath)
	if err != nil {
		t.Fatal(err)
	}
	writeImplementationFile(t, implementationPath, strings.Replace(string(implementation), "type Config struct {", "type Config struct { Password runtimeSecret;", 1))
	writeImplementationFile(t, filepath.Join(root, "alternate/secret.go"), "package alternate\nimport \"github.com/plystra/kernel/configuration\"\ntype runtimeSecret = configuration.Secret\n")
	validated := false
	err = modulemutation.Tidy(t.Context(), root, modulemutation.TidyOptions{Environment: environment}, func(mutate applicationgenerate.ModuleMutation) error {
		_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
			Start: root, EnvironmentName: "test", Environment: environment, MutateModule: mutate,
			Validate: func(ctx context.Context, directory string) error {
				validated = true
				return gocommand.Run(ctx, gocommand.Options{Directory: directory, Environment: environment}, "build", "-mod=readonly", "./...")
			},
		})
		return err
	})
	if err != nil || !validated {
		t.Fatalf("ordinary installed generation/build: validated=%t, %v", validated, err)
	}
	writeImplementationFile(t, filepath.Join(root, "generated/go/bootstrap/ownership_tombstone_test.go"), dependencyWholeOwnerRuntimeTest)
	if err := gocommand.Run(t.Context(), options, "test", "-race", "-mod=readonly", "./generated/go/bootstrap", "-run", "^TestRemovedConstructorConfiguration$", "-count=1"); err != nil {
		t.Fatal(err)
	}
}

const dependencyWholeOwnerRuntimeTest = `package bootstrap

import (
 "bytes"
 "errors"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestRemovedConstructorConfiguration(t *testing.T) {
 baseline, err := filepath.Abs("../../../dist/runtime-baseline.json")
 if err != nil { t.Fatal(err) }
 const old = "example.com/dependency-planning/service.New"
 const known = "example.com/dependency-planning/alternate.New"
 const rootDocument = "interfaces: {require: [app.main/v1]}\nconfig: {"+old+": {name: PRIVATE_OLD}}\n"
 for _, test := range []struct { name, root, overlay string; valid bool }{
  {"suppressed unknown", rootDocument, "config: {"+old+": {$remove: true}}\n", true},
  {"unmasked unknown", rootDocument, "{}\n", false},
  {"unrelated exclusion", rootDocument, "config: {example.com/other.New: {$remove: true}}\n", false},
  {"nested exclusion", rootDocument, "config: {"+old+": {name: {$remove: true}}}\n", false},
  {"known invalid lower", "interfaces: {require: [app.main/v1]}\nconfig: {"+known+": {name: [PRIVATE_INVALID]}}\n", "config: {"+known+": {$remove: true}}\n", false},
  {"known invalid secret", "interfaces: {require: [app.main/v1]}\nconfig: {"+known+": {password: {env: PRIVATE_ENV, file: PRIVATE_FILE}}}\n", "config: {"+known+": {$remove: true}}\n", false},
  {"known valid lower", "interfaces: {require: [app.main/v1]}\nconfig: {"+known+": {name: PRIVATE_VALID}}\n", "config: {"+known+": {$remove: true}}\n", true},
  {"invalid tombstone", rootDocument, "config: {"+old+": {$remove: true, name: PRIVATE_INVALID}}\n", false},
  {"invalid lower mapping", "config: {"+old+": [PRIVATE_INVALID]}\n", "config: {"+old+": {$remove: true}}\n", false},
  {"duplicate lower key", "config: {"+old+": {name: PRIVATE_FIRST, name: PRIVATE_SECOND}}\n", "config: {"+old+": {$remove: true}}\n", false},
  {"lower anchor", "config: {"+old+": {name: &private PRIVATE_VALUE}}\n", "config: {"+old+": {$remove: true}}\n", false},
  {"nested root tombstone", "config: {"+old+": {name: {$remove: true}}}\n", "config: {"+old+": {$remove: true}}\n", false},
  {"sequence root tombstone", "config: {"+old+": {names: [{$remove: true}]}}\n", "config: {"+old+": {$remove: true}}\n", false},
  {"root tombstone", "config: {"+old+": {$remove: true}}\n", "{}\n", false},
 } {
  t.Run(test.name, func(t *testing.T) {
   root := t.TempDir()
   for name, data := range map[string]string{"plystra.yaml":test.root, "plystra.test.yaml":test.overlay} {
    if err := os.WriteFile(filepath.Join(root,name), []byte(data), 0600); err != nil { t.Fatal(err) }
   }
   document, err := loadRuntimeDocument(RuntimeOptions{Arguments: []string{"--configuration-root",root,"--runtime-baseline",baseline,"--env","test"}, Environment: []string{}})
   if test.valid {
    if err != nil || bytes.Contains(document, []byte("PRIVATE_")) || bytes.Contains(document, []byte(old)) { t.Fatalf("suppressed config reached runtime: %v",err) }
   } else if !errors.Is(err, ErrRuntimeConfiguration) || strings.Contains(err.Error(), "PRIVATE_") { t.Fatalf("invalid configuration accepted or disclosed: %v",err) }
  })
 }
}
`
