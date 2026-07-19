package newproject_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/newproject"
	"github.com/plystra/cli/internal/plugincreate"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

var updateProjectGolden = flag.Bool("update", false, "update generated project scaffold golden files")

func TestMain(main *testing.M) {
	if os.Getenv("PLYSTRA_NEW_PLUGIN_ROLLBACK_HELPER") == "1" {
		switch {
		case len(os.Args) == 3 && os.Args[1] == "mod" && (os.Args[2] == "download" || os.Args[2] == "tidy"):
			os.Exit(0)
		case len(os.Args) == 4 && os.Args[1] == "test" && os.Args[2] == "-mod=readonly" && os.Args[3] == "./...":
			os.Exit(9)
		default:
			os.Exit(8)
		}
	}
	os.Exit(main.Run())
}

func TestCreateAndPublicCommandProduceDeterministicBuildableProjects(t *testing.T) {
	proxy := createKernelProxy(t)
	environment := isolatedGoEnvironment(t, proxy)
	const projectName = "my-app"
	const modulePath = "example.com/acme/my-app"

	directParent := t.TempDir()
	direct, err := newproject.Create(context.Background(), newproject.Options{
		Parent:      directParent,
		ProjectName: projectName,
		ModulePath:  modulePath,
		Git:         true,
		GitHubCI:    true,
		Skills:      true,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if direct.ModulePath() != modulePath || direct.Path() != filepath.Join(directParent, "my-app") {
		t.Fatalf("Create result = module %q, path %q", direct.ModulePath(), direct.Path())
	}

	commandParent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := command.RunIn([]string{"new", projectName, "--module", modulePath, "--git", "--github-ci", "--skills"}, &stdout, &stderr, commandParent, environment); exitCode != 0 {
		t.Fatalf("RunIn exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	commandTarget := filepath.Join(commandParent, "my-app")
	wantOutput := fmt.Sprintf("created %s in %s\n", modulePath, commandTarget)
	if stdout.String() != wantOutput || stderr.Len() != 0 {
		t.Fatalf("RunIn output = stdout %q, stderr %q", stdout.String(), stderr.String())
	}

	directTree := snapshotTree(t, direct.Path())
	commandTree := snapshotTree(t, commandTarget)
	if !reflect.DeepEqual(directTree, commandTree) {
		t.Fatalf("repeated creation differed:\ndirect:  %#v\ncommand: %#v", directTree, commandTree)
	}
	wantFiles := []string{
		".agents/skills/plystra/SKILL.md",
		".agents/skills/plystra/agents/openai.yaml",
		".gitattributes",
		".github/workflows/ci.yml",
		".gitignore",
		"README.md",
		"generated/.plystra-manifest.json",
		"generated/go/assembly/compatibility_gen.go",
		"generated/go/assembly/invocations_gen.go",
		"generated/go/assembly/providers_gen.go",
		"generated/go/bootstrap/bootstrap_gen.go",
		"generated/manifest.json",
		"go.mod",
		"go.sum",
		"plystra.yaml",
	}
	var gotFiles []string
	for name := range directTree {
		gotFiles = append(gotFiles, name)
	}
	sort.Strings(gotFiles)
	if !reflect.DeepEqual(gotFiles, wantFiles) {
		t.Fatalf("project files = %v, want %v", gotFiles, wantFiles)
	}
	goldenTree := snapshotTree(t, "testdata/project")
	delete(directTree, "go.sum")
	if *updateProjectGolden {
		writeGoldenTree(t, "testdata/project", directTree)
		goldenTree = snapshotTree(t, "testdata/project")
	}
	if !reflect.DeepEqual(directTree, goldenTree) {
		t.Fatalf("project scaffold differs from golden files:\n got: %#v\nwant: %#v", directTree, goldenTree)
	}
	if bytes.Contains(directTree["plystra.yaml"], []byte("instance_id")) {
		t.Fatalf("project scaffold contains deprecated instance_id:\n%s", directTree["plystra.yaml"])
	}
	for _, obsolete := range [][]byte{[]byte("database:"), []byte("audit_write:")} {
		if bytes.Contains(directTree["plystra.yaml"], obsolete) {
			t.Fatalf("project scaffold contains obsolete configuration %q:\n%s", obsolete, directTree["plystra.yaml"])
		}
	}
	if !bytes.Contains(directTree["plystra.yaml"], []byte("  aliases: {}")) {
		t.Fatalf("project scaffold omits capabilities.aliases:\n%s", directTree["plystra.yaml"])
	}
	assertReadmeUsesAvailableCommands(t, directTree["README.md"])
	assertCIUsesCurrentActions(t, directTree[".github/workflows/ci.yml"])
	assertPlystraSkill(t, direct.Path(), modulePath)
	assertGitInitialized(t, direct.Path())
	assertGitInitialized(t, commandTarget)
	for name, content := range directTree {
		if bytes.Contains(content, []byte(directParent)) || bytes.Contains(content, []byte(commandParent)) {
			t.Fatalf("%s contains a local absolute path", name)
		}
	}
	assertModuleState(t, direct.Path(), modulePath)
	generated, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       direct.Path(),
		Check:       true,
		Environment: environment,
	})
	if err != nil || !generated.Report().Clean() {
		t.Fatalf("initial generated output = %#v, %v", generated.Report().Changes(), err)
	}
}

func TestPublicCommandDefaultsModulePathToProjectName(t *testing.T) {
	proxy := createKernelProxy(t)
	environment := isolatedGoEnvironment(t, proxy)
	parent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := command.RunIn([]string{"new", "my-app", "--plugin", "records", "--no-git", "--no-github-ci", "--no-skills"}, &stdout, &stderr, parent, environment)
	if exitCode != 0 {
		t.Fatalf("RunIn exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	target := filepath.Join(parent, "my-app")
	if stdout.String() != fmt.Sprintf("created my-app in %s\n", target) || stderr.Len() != 0 {
		t.Fatalf("RunIn output = stdout %q, stderr %q", stdout.String(), stderr.String())
	}
	assertModuleState(t, target, "my-app")
	pluginManifest, err := os.ReadFile(filepath.Join(target, "records", "plugin.yaml"))
	if err != nil || !bytes.Contains(pluginManifest, []byte("id: my-app.records")) {
		t.Fatalf("default-module Plugin manifest = %q, %v", pluginManifest, err)
	}
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: target, Check: true, Environment: environment})
	if err != nil || !checked.Report().Clean() {
		t.Fatalf("default-module generated output = %#v, %v", checked.Report().Changes(), err)
	}
}

func TestCreateFromTemplateDependencyResolvesComposesAndPreservesSources(t *testing.T) {
	proxy := createKernelProxy(t)
	const templatePath = "example.com/acme/platform"
	const templateVersion = "v1.2.3"
	const templateQuery = templatePath + "@" + templateVersion
	const secretReference = "PLYSTRA_TEMPLATE_SMTP_PASSWORD"
	const resolvedSecret = "resolved-template-secret-must-not-leak"
	writeProxyModule(t, proxy, templatePath, templateVersion, map[string][]byte{
		"template.go":             []byte("package platform\n\nconst Identity = \"template-source-only\"\n"),
		"TEMPLATE_ONLY.md":        []byte("This file must remain in the dependency source.\n"),
		"plystra.yaml":            []byte("http:\n  expose:\n    - kernel.health/v1\n\ncapabilities:\n  require:\n    - email.send/v1\n    - kernel.info/v1\n  use:\n    email.send/v1: acme.platform.mailer\n  aliases: {}\n\nconfig:\n  acme.platform.mailer:\n    host: smtp.localhost\n    password:\n      env: " + secretReference + "\n"),
		"plystra.production.yaml": []byte("capabilities:\n  require:\n    - missing.overlay/v1\n"),
		"mailer/plugin.yaml":      []byte("id: acme.platform.mailer\nprovides: [email.send/v1]\nconfig:\n  host: {type: string, required: true}\n  password: {type: secret, required: true}\n"),
		"mailer/capabilities/email.send/v1/capability.yaml": []byte("id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n"),
		"mailer/plugin.go":                                     []byte("package mailer\n\nimport (\n\t\"context\"\n\n\tconfiguration \"example.com/acme/platform/generated/go/configuration\"\n\tcontract \"example.com/acme/platform/generated/go/contracts/email/send/v1\"\n)\n\ntype Config = configuration.MailerConfig\ntype Plugin struct{}\nfunc New(Config) *Plugin { return &Plugin{} }\nfunc (*Plugin) Send(context.Context, contract.Request) (contract.Response, error) { return contract.Response{}, nil }\n"),
		"generated/go/configuration/mailer_gen.go":             []byte("package configuration\n\nimport (\n\t\"context\"\n\n\tkernelconfiguration \"github.com/plystra/kernel/configuration\"\n)\n\ntype MailerConfig struct { Host string; Password kernelconfiguration.Secret }\nfunc DecodeMailer(context.Context, *kernelconfiguration.Resolver, []byte) (MailerConfig, error) { return MailerConfig{}, nil }\n"),
		"generated/go/contracts/email/send/v1/contract_gen.go": []byte("package emailsendv1\n\nconst CapabilityID = \"email.send/v1\"\ntype Request struct{}\ntype Response struct{}\n"),
	})
	environment := isolatedGoEnvironment(t, proxy)
	environment = append(environment, secretReference+"="+resolvedSecret)
	if err := gocommand.Run(t.Context(), gocommand.Options{
		Directory:   t.TempDir(),
		Environment: environment,
	}, "mod", "download", templateQuery); err != nil {
		t.Fatalf("pre-download template: %v", err)
	}
	cacheRoot := moduleCacheRoot(t, environment, templatePath, templateVersion)
	cacheBefore := snapshotTree(t, cacheRoot)
	proxyBefore := snapshotTree(t, proxy)

	parent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	arguments := []string{
		"new", "my-app",
		"--module", "example.com/acme/my-app",
		"--template", templateQuery,
		"--no-git", "--no-github-ci", "--skills",
	}
	if exitCode := command.RunIn(arguments, &stdout, &stderr, parent, environment); exitCode != 0 {
		t.Fatalf("RunIn exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	target := filepath.Join(parent, "my-app")
	wantOutput := fmt.Sprintf("created example.com/acme/my-app from %s in %s\n", templateQuery, target)
	if stdout.String() != wantOutput || stderr.Len() != 0 {
		t.Fatalf("RunIn output = stdout %q, stderr %q", stdout.String(), stderr.String())
	}

	assertDirectRequirement(t, target, templatePath, templateVersion)
	configuration, err := os.ReadFile(filepath.Join(target, "plystra.yaml"))
	if err != nil {
		t.Fatalf("ReadFile(plystra.yaml): %v", err)
	}
	model, err := applicationmeta.Parse(configuration)
	if err != nil {
		t.Fatalf("Parse(plystra.yaml): %v", err)
	}
	exposures := model.HTTPExposures()
	if len(exposures) != 1 || exposures[0].ID().String() != "kernel.health/v1" {
		t.Fatalf("composed HTTP exposures = %#v, want kernel.health/v1", exposures)
	}
	requirements := model.Requirements()
	if len(requirements) != 2 || requirements[0].ID().String() != "email.send/v1" || requirements[1].ID().String() != "kernel.info/v1" {
		t.Fatalf("composed requirements = %#v, want email.send/v1 and kernel.info/v1", requirements)
	}
	choices := model.ProviderChoices()
	if len(choices) != 1 || choices[0].Capability().String() != "email.send/v1" || choices[0].PluginID() != "acme.platform.mailer" {
		t.Fatalf("composed Provider choices = %#v, want email.send/v1 -> acme.platform.mailer", choices)
	}
	pluginConfiguration, exists := model.Configuration("acme.platform.mailer")
	if !exists {
		t.Fatal("composed configuration omits acme.platform.mailer")
	}
	privateConfiguration := pluginConfiguration.YAML()
	if !bytes.Contains(privateConfiguration, []byte("smtp.localhost")) || !bytes.Contains(privateConfiguration, []byte(secretReference)) || bytes.Contains(privateConfiguration, []byte(resolvedSecret)) {
		t.Fatalf("composed Plugin configuration did not preserve unresolved local inputs: %s", privateConfiguration)
	}
	if bytes.Contains(configuration, []byte("missing.overlay/v1")) {
		t.Fatalf("dependency environment overlay was inherited:\n%s", configuration)
	}
	for _, copied := range []string{"template.go", "TEMPLATE_ONLY.md", "plystra.production.yaml", "go.work"} {
		if _, err := os.Lstat(filepath.Join(target, copied)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("template source path %s was copied into the Project: %v", copied, err)
		}
	}
	manifest, err := os.ReadFile(filepath.Join(target, "generated", "manifest.json"))
	if err != nil || !bytes.Contains(manifest, []byte(templatePath)) {
		t.Fatalf("generated manifest template provenance = %q, %v", manifest, err)
	}
	if bytes.Contains(manifest, []byte(secretReference)) || bytes.Contains(manifest, []byte(resolvedSecret)) {
		t.Fatalf("generated manifest leaked a Secret reference or resolved value: %s", manifest)
	}
	for name, data := range snapshotTree(t, filepath.Join(target, "generated")) {
		if bytes.Contains(data, []byte(secretReference)) || bytes.Contains(data, []byte(resolvedSecret)) {
			t.Fatalf("generated path %s leaked a Secret reference or resolved value", name)
		}
	}
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       target,
		Check:       true,
		Environment: environment,
	})
	if err != nil || !checked.Report().Clean() || checked.ConfigurationChanged() {
		t.Fatalf("template generation check = changes %#v, configuration changed %t, %v", checked.Report().Changes(), checked.ConfigurationChanged(), err)
	}
	assertPlystraSkill(t, target, "example.com/acme/my-app")
	if cacheAfter := snapshotTree(t, cacheRoot); !reflect.DeepEqual(cacheAfter, cacheBefore) {
		t.Fatalf("template Module Cache source changed:\nbefore: %#v\nafter:  %#v", cacheBefore, cacheAfter)
	}
	if proxyAfter := snapshotTree(t, proxy); !reflect.DeepEqual(proxyAfter, proxyBefore) {
		t.Fatalf("Go Module proxy changed during template creation:\nbefore: %#v\nafter:  %#v", proxyBefore, proxyAfter)
	}
}

func TestCreateRejectsTemplateWithoutRootProjectMarkerAndRollsBack(t *testing.T) {
	proxy := createKernelProxy(t)
	const templatePath = "example.com/acme/ordinary"
	const templateVersion = "v1.0.0"
	writeProxyModule(t, proxy, templatePath, templateVersion, map[string][]byte{
		"ordinary.go": []byte("package ordinary\n"),
	})
	environment := isolatedGoEnvironment(t, proxy)
	parent := t.TempDir()
	_, err := newproject.Create(t.Context(), newproject.Options{
		Parent:      parent,
		ProjectName: "my-app",
		ModulePath:  "example.com/acme/my-app",
		Template:    templatePath + "@" + templateVersion,
		Environment: environment,
	})
	if !errors.Is(err, newproject.ErrCreate) || !errors.Is(err, newproject.ErrInvalidTemplate) || !strings.Contains(err.Error(), "root plystra.yaml") {
		t.Fatalf("Create error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after markerless template failure: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestCreateRollsBackTemplateGenerationFailure(t *testing.T) {
	proxy := createKernelProxy(t)
	const templatePath = "example.com/acme/incomplete"
	const templateVersion = "v1.0.0"
	writeProxyModule(t, proxy, templatePath, templateVersion, map[string][]byte{
		"incomplete.go": []byte("package incomplete\n"),
		"plystra.yaml":  []byte("capabilities:\n  require:\n    - missing.provider/v1\n"),
	})
	environment := isolatedGoEnvironment(t, proxy)
	parent := t.TempDir()
	_, err := newproject.Create(t.Context(), newproject.Options{
		Parent:      parent,
		ProjectName: "my-app",
		ModulePath:  "example.com/acme/my-app",
		Template:    templatePath + "@" + templateVersion,
		Environment: environment,
	})
	if !errors.Is(err, newproject.ErrCreate) || !errors.Is(err, applicationgenerate.ErrGenerate) {
		t.Fatalf("Create error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after template generation failure: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestCreateRejectsInvalidTemplateQueryBeforeMutation(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	_, err := newproject.Create(t.Context(), newproject.Options{
		Parent:      parent,
		ProjectName: "my-app",
		Template:    "../platform@v1.0.0",
		GoCommand:   filepath.Join(parent, "must-not-run"),
	})
	if !errors.Is(err, newproject.ErrCreate) || !strings.Contains(err.Error(), "template query") {
		t.Fatalf("Create error = %v", err)
	}
	if entries, readErr := os.ReadDir(parent); readErr != nil || len(entries) != 0 {
		t.Fatalf("invalid template query mutated parent: %v, %v", entries, readErr)
	}
}

func assertCIUsesCurrentActions(t *testing.T, workflow []byte) {
	t.Helper()
	for _, action := range [][]byte{[]byte("actions/checkout@v7"), []byte("actions/setup-go@v6")} {
		if !bytes.Contains(workflow, action) {
			t.Fatalf("generated CI omits %q:\n%s", action, workflow)
		}
	}
	if bytes.Contains(workflow, []byte("actions/checkout@v4")) {
		t.Fatalf("generated CI retains obsolete checkout action:\n%s", workflow)
	}
}

func assertReadmeUsesAvailableCommands(t *testing.T, readme []byte) {
	t.Helper()
	for _, unavailable := range [][]byte{[]byte("plystra dev"), []byte("plystra test"), []byte("plystra build")} {
		if bytes.Contains(readme, unavailable) {
			t.Fatalf("generated README advertises unavailable command %q:\n%s", unavailable, readme)
		}
	}
	for _, available := range [][]byte{[]byte("plystra add github.com/acme/platform@v1.0.0"), []byte("plystra plugin create"), []byte("plystra capability create"), []byte("plystra generate --check"), []byte("plystra generate --env"), []byte("PLYSTRA_ENV"), []byte("plystra generate --config"), []byte("PLYSTRA_CONFIG"), []byte("go test ./..."), []byte("go vet ./...")} {
		if !bytes.Contains(readme, available) {
			t.Fatalf("generated README omits available workflow %q:\n%s", available, readme)
		}
	}
	if !bytes.Contains(readme, []byte("--expose")) {
		t.Fatalf("generated README omits exposure workflow:\n%s", readme)
	}
}

func writeGoldenTree(t *testing.T, root string, tree map[string][]byte) {
	t.Helper()
	for name, data := range tree {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
		}
		writeTestFile(t, path, data)
	}
}

func TestCreateWithInitialPluginComposesProjectTransactions(t *testing.T) {
	proxy := createKernelProxy(t)
	environment := isolatedGoEnvironment(t, proxy)
	const modulePath = "example.com/acme/my-app/v2"
	const pluginName = "account-profile"

	directParent := t.TempDir()
	direct, err := newproject.Create(context.Background(), newproject.Options{
		Parent:      directParent,
		ProjectName: "my-app",
		ModulePath:  modulePath,
		Plugin:      pluginName,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Create with plugin: %v", err)
	}

	commandParent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	arguments := []string{"new", "my-app", "--module", modulePath, "--plugin", pluginName, "--no-git", "--no-github-ci", "--no-skills"}
	if exitCode := command.RunIn(arguments, &stdout, &stderr, commandParent, environment); exitCode != 0 {
		t.Fatalf("RunIn exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	commandRoot := filepath.Join(commandParent, "my-app")
	wantOutput := fmt.Sprintf("created %s in %s\n", modulePath, commandRoot)
	if stdout.String() != wantOutput || stderr.Len() != 0 {
		t.Fatalf("RunIn output = stdout %q, stderr %q", stdout.String(), stderr.String())
	}

	golden := pluginScaffoldSnapshot(t, filepath.Join("..", "plugincreate", "testdata", "plugin"), pluginName)
	for kind, root := range map[string]string{"direct": direct.Path(), "command": commandRoot} {
		pluginTree := pluginScaffoldSnapshot(t, root, pluginName)
		if !reflect.DeepEqual(pluginTree, golden) {
			t.Fatalf("%s initial plugin differs from plugin-create golden:\n got: %#v\nwant: %#v", kind, pluginTree, golden)
		}
		assertModuleState(t, root, modulePath)
	}
	for _, root := range []string{direct.Path(), commandRoot} {
		if info, err := os.Stat(filepath.Join(root, "plystra.yaml")); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("Project plystra.yaml = %#v, %v", info, err)
		}
		checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
			Start:       root,
			Check:       true,
			Environment: environment,
		})
		if err != nil || !checked.Report().Clean() {
			t.Fatalf("initial-plugin generation = %#v, %v", checked.Report().Changes(), err)
		}
	}
}

func TestPublicCommandRejectsRemovedLibraryFlagWithoutMutation(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := command.RunIn([]string{"new", "my-app", "--library", "--no-git", "--no-github-ci", "--no-skills"}, &stdout, &stderr, parent, nil)
	if exitCode != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "plystra new <project-name>") || strings.Contains(stderr.String(), "Create a non-runnable") {
		t.Fatalf("RunIn = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatalf("removed flag mutated parent: %v, %v", entries, err)
	}
}

func TestPublicCommandRejectsOldPositionalModulePathWithoutMutation(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := command.RunIn([]string{"new", "example.com/acme/my-app", "--no-git", "--no-github-ci", "--no-skills"}, &stdout, &stderr, parent, nil)
	if exitCode != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "project name") {
		t.Fatalf("RunIn = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatalf("old positional syntax mutated parent: %v, %v", entries, err)
	}
}

func TestPublicCommandRejectsInvalidModuleOverrideWithoutMutation(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := command.RunIn([]string{"new", "my-app", "--module", "local-module", "--no-git", "--no-github-ci", "--no-skills"}, &stdout, &stderr, parent, nil)
	if exitCode != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "invalid explicit Go Module path") {
		t.Fatalf("RunIn = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if entries, err := os.ReadDir(parent); err != nil || len(entries) != 0 {
		t.Fatalf("invalid module override mutated parent: %v, %v", entries, err)
	}
}

func TestCreateHonorsOptionalProjectChoices(t *testing.T) {
	proxy := createKernelProxy(t)
	environment := isolatedGoEnvironment(t, proxy)
	tests := []struct {
		name       string
		modulePath string
		git        bool
		githubCI   bool
		skills     bool
	}{
		{name: "minimal"},
		{name: "git", git: true},
		{name: "github-ci", githubCI: true},
		{name: "skills", skills: true},
		{name: "github-module", modulePath: "github.com/plystra/core-example", skills: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			projectName := "choice-" + test.name
			modulePath := test.modulePath
			expectedModulePath := modulePath
			if expectedModulePath == "" {
				expectedModulePath = projectName
			}
			result, err := newproject.Create(t.Context(), newproject.Options{
				Parent:      parent,
				ProjectName: projectName,
				ModulePath:  modulePath,
				Git:         test.git,
				GitHubCI:    test.githubCI,
				Skills:      test.skills,
				Environment: environment,
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if result.ModulePath() != expectedModulePath || result.Path() != filepath.Join(parent, projectName) {
				t.Fatalf("Create result = module %q path %q", result.ModulePath(), result.Path())
			}
			assertModuleState(t, result.Path(), expectedModulePath)
			assertPathPresence(t, filepath.Join(result.Path(), ".git"), test.git)
			assertPathPresence(t, filepath.Join(result.Path(), ".github", "workflows", "ci.yml"), test.githubCI)
			assertPathPresence(t, filepath.Join(result.Path(), ".agents", "skills", "plystra", "SKILL.md"), test.skills)
			if test.git {
				assertGitInitialized(t, result.Path())
			}
			if test.skills {
				assertPlystraSkill(t, result.Path(), expectedModulePath)
			}
		})
	}
}

func TestPublicCommandRequiresExplicitNonInteractiveChoices(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := command.RunIn([]string{"new", "my-app"}, &stdout, &stderr, parent, nil)
	if exitCode != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "--git or --no-git") || !strings.Contains(stderr.String(), "--github-ci or --no-github-ci") || !strings.Contains(stderr.String(), "--skills or --no-skills") {
		t.Fatalf("RunIn = exit %d, stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("non-interactive choice failure created target: %v", err)
	}
}

func TestCreateRollsBackGitInitializationFailure(t *testing.T) {
	proxy := createKernelProxy(t)
	environment := isolatedGoEnvironment(t, proxy)
	parent := t.TempDir()
	_, err := newproject.Create(t.Context(), newproject.Options{
		Parent:      parent,
		ProjectName: "my-app",
		ModulePath:  "example.com/acme/my-app",
		Git:         true,
		GitCommand:  filepath.Join(parent, "missing-git-command"),
		Environment: environment,
	})
	if !errors.Is(err, newproject.ErrCreate) || !errors.Is(err, newproject.ErrGitInitialization) {
		t.Fatalf("Create error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after Git failure: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestCreateRollsBackGoValidationFailure(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	target := filepath.Join(parent, "my-app")
	_, err := newproject.Create(context.Background(), newproject.Options{
		Parent:      parent,
		ProjectName: "my-app",
		ModulePath:  "example.com/acme/my-app",
		GoCommand:   filepath.Join(parent, "missing-go-command"),
	})
	if !errors.Is(err, newproject.ErrCreate) {
		t.Fatalf("Create error = %v, want ErrCreate", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after failed validation: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestCreateWithInitialPluginRollsBackOuterProjectOnPluginValidationFailure(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	environment := append(os.Environ(), "PLYSTRA_NEW_PLUGIN_ROLLBACK_HELPER=1")
	_, err = newproject.Create(context.Background(), newproject.Options{
		Parent:      parent,
		ProjectName: "my-app",
		ModulePath:  "example.com/acme/my-app",
		Plugin:      "account",
		GoCommand:   command,
		Environment: environment,
	})
	if !errors.Is(err, newproject.ErrCreate) || !errors.Is(err, plugincreate.ErrCreate) || !errors.Is(err, gocommand.ErrRun) {
		t.Fatalf("Create error = %v, want project, plugin, and Go command errors", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "my-app")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target exists after plugin validation failure: %v", err)
	}
	assertNoTransactionFiles(t, parent)
}

func TestCreateRejectsUnsafeProjectNamesBeforeMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		projectName string
	}{
		{name: "empty"},
		{name: "current directory", projectName: "."},
		{name: "parent directory", projectName: ".."},
		{name: "absolute", projectName: string(filepath.Separator) + "absolute"},
		{name: "slash", projectName: "nested/app"},
		{name: "backslash", projectName: `nested\app`},
		{name: "uppercase", projectName: "MyApp"},
		{name: "double hyphen", projectName: "my--app"},
		{name: "trailing hyphen", projectName: "my-app-"},
		{name: "control", projectName: "my\napp"},
		{name: "reserved", projectName: "con"},
		{name: "oversized", projectName: strings.Repeat("a", 65)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			_, err := newproject.Create(context.Background(), newproject.Options{Parent: parent, ProjectName: test.projectName})
			if !errors.Is(err, newproject.ErrCreate) {
				t.Fatalf("Create error = %v, want ErrCreate", err)
			}
			entries, readErr := os.ReadDir(parent)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("parent entries = %v, %v", entries, readErr)
			}
		})
	}
}

func TestCreateRejectsInvalidExplicitModulePathsBeforeMutation(t *testing.T) {
	t.Parallel()

	for _, modulePath := range []string{"my-app", "example.com/acme/../app", "example.com/acme/app/v1", "Example.com/acme/app"} {
		modulePath := modulePath
		t.Run(modulePath, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			_, err := newproject.Create(context.Background(), newproject.Options{Parent: parent, ProjectName: "my-app", ModulePath: modulePath})
			if !errors.Is(err, newproject.ErrCreate) || !strings.Contains(err.Error(), "invalid explicit Go Module path") {
				t.Fatalf("Create error = %v", err)
			}
			entries, readErr := os.ReadDir(parent)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("parent entries = %v, %v", entries, readErr)
			}
		})
	}
}

func TestCreateRejectsInvalidInitialPluginBeforeMutation(t *testing.T) {
	t.Parallel()

	for _, pluginName := range []string{"Account", "generated", "account--profile"} {
		pluginName := pluginName
		t.Run(pluginName, func(t *testing.T) {
			t.Parallel()
			parent := t.TempDir()
			_, err := newproject.Create(context.Background(), newproject.Options{
				Parent:      parent,
				ProjectName: "my-app",
				ModulePath:  "example.com/acme/my-app",
				Plugin:      pluginName,
			})
			if !errors.Is(err, newproject.ErrCreate) || !errors.Is(err, plugincreate.ErrInvalidName) {
				t.Fatalf("Create error = %v, want ErrCreate and ErrInvalidName", err)
			}
			entries, readErr := os.ReadDir(parent)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("parent entries = %v, %v", entries, readErr)
			}
		})
	}
}

func TestCreatePreservesExistingProject(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	target := filepath.Join(parent, "my-app")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	keep := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := newproject.Create(context.Background(), newproject.Options{
		Parent:      parent,
		ProjectName: "my-app",
		ModulePath:  "example.com/acme/my-app",
		GoCommand:   filepath.Join(parent, "must-not-run"),
	})
	if !errors.Is(err, newproject.ErrCreate) {
		t.Fatalf("Create error = %v, want ErrCreate", err)
	}
	content, readErr := os.ReadFile(keep)
	if readErr != nil || string(content) != "keep" {
		t.Fatalf("existing content = %q, %v", content, readErr)
	}
	assertNoTransactionFiles(t, parent)
}

func createKernelProxy(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proxy")
	escapedPath, err := module.EscapePath("github.com/plystra/kernel")
	if err != nil {
		t.Fatalf("EscapePath: %v", err)
	}
	escapedVersion, err := module.EscapeVersion(newproject.KernelVersion)
	if err != nil {
		t.Fatalf("EscapeVersion: %v", err)
	}
	versionRoot := filepath.Join(root, filepath.FromSlash(escapedPath), "@v")
	if err := os.MkdirAll(versionRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeTestFile(t, filepath.Join(versionRoot, "list"), []byte(newproject.KernelVersion+"\n"))
	writeTestFile(t, filepath.Join(versionRoot, escapedVersion+".info"), fmt.Appendf(nil, "{\"Version\":%q,\"Time\":\"2026-07-15T00:00:00Z\"}\n", newproject.KernelVersion))
	moduleFile := []byte("module github.com/plystra/kernel\n\ngo 1.26\n")
	writeTestFile(t, filepath.Join(versionRoot, escapedVersion+".mod"), moduleFile)

	archiveFile, err := os.Create(filepath.Join(versionRoot, escapedVersion+".zip"))
	if err != nil {
		t.Fatalf("Create zip: %v", err)
	}
	archive := zip.NewWriter(archiveFile)
	prefix := "github.com/plystra/kernel@" + newproject.KernelVersion + "/"
	files := []struct {
		name string
		data []byte
	}{
		{name: "assembly/version.go", data: []byte("package assembly\n\nimport \"fmt\"\n\ntype Version uint32\n\nconst V1 Version = 1\n\nfunc RequireVersion(version Version) error {\n\tif version != V1 { return fmt.Errorf(\"unsupported assembly API version %d\", version) }\n\treturn nil\n}\n")},
		{name: "capability/capability.go", data: []byte("package capability\n\nimport \"context\"\n\ntype Contract[Request, Response any] struct{}\ntype Handler[Request, Response any] func(context.Context, Request) (Response, error)\n\nfunc MustParseContractWithSemanticErrors[Request, Response any](string, ...string) Contract[Request, Response] {\n\treturn Contract[Request, Response]{}\n}\n")},
		{name: "configuration/configuration.go", data: []byte("package configuration\n\nimport (\n\t\"context\"\n\t\"errors\"\n\t\"os\"\n\n\t\"github.com/plystra/kernel/plugin/manifest\"\n)\n\nconst MaximumSecretValueBytes = 1 << 20\n\nvar ErrSecretExposure = errors.New(\"Secret serialization is prohibited\")\n\ntype ResolverOptions struct { MaximumValueBytes int }\ntype Resolver struct{}\ntype Secret struct{}\ntype Values struct{}\ntype ObjectMap struct{}\ntype StringMap struct{}\n\nfunc NewResolver(ResolverOptions) (*Resolver, error) { return &Resolver{}, nil }\nfunc LoadDocument(path string) ([]byte, error) { return os.ReadFile(path) }\nfunc (ObjectMap) Names() []string { return nil }\nfunc (ObjectMap) YAML(string) ([]byte, bool) { return nil, false }\nfunc (StringMap) Names() []string { return nil }\nfunc (StringMap) Value(string) (string, bool) { return \"\", false }\nfunc ExtractObjectMap([]byte, string) (ObjectMap, error) { return ObjectMap{}, nil }\nfunc ExtractStringMap([]byte, string) (StringMap, error) { return StringMap{}, nil }\nfunc Decode(context.Context, *Resolver, manifest.Config, []byte) (Values, error) { return Values{}, nil }\n")},
		{name: "go.mod", data: moduleFile},
		{name: "intrinsic/intrinsic.go", data: []byte("package intrinsic\n\nimport (\n\t\"github.com/plystra/kernel/capability\"\n\t\"github.com/plystra/kernel/invocation\"\n)\n\ntype BindingOptions struct { ModuleVersion, BuildIdentity string }\n\ntype HealthRequest struct{}\ntype HealthStatus string\nconst HealthStatusHealthy HealthStatus = \"healthy\"\ntype HealthResponse struct { Status HealthStatus `json:\"status\"` }\ntype InfoRequest struct{}\ntype InfoResponse struct { AssemblyAPI string `json:\"assembly_api\"`; KernelModule string `json:\"kernel_module\"`; KernelVersion string `json:\"kernel_version\"` }\n\nfunc HealthContract() capability.Contract[HealthRequest, HealthResponse] { return capability.Contract[HealthRequest, HealthResponse]{} }\nfunc InfoContract() capability.Contract[InfoRequest, InfoResponse] { return capability.Contract[InfoRequest, InfoResponse]{} }\nfunc NewBindings(BindingOptions) ([]invocation.Binding, error) { return make([]invocation.Binding, 2), nil }\n")},
		{name: "invocation/invocation.go", data: []byte("package invocation\n\nimport (\n\t\"context\"\n\t\"time\"\n\n\t\"github.com/plystra/kernel/capability\"\n\t\"github.com/plystra/kernel/plugin\"\n)\n\ntype Endpoint struct{}\ntype ModuleBuild struct{}\ntype ProviderKind string\ntype SelectionReason string\ntype BindingOptions struct {\n\tProviderKind ProviderKind\n\tProviderID plugin.ID\n\tProviderPackage string\n\tProviderBuild ModuleBuild\n\tSelectionReason SelectionReason\n\tSchemaDigest [32]byte\n}\ntype Binding struct{}\ntype Catalog struct { bindings []Binding }\nconst (\n\tProviderKindKernel ProviderKind = \"kernel\"\n\tProviderKindPlugin ProviderKind = \"plugin\"\n\tSelectionReasonIntrinsic SelectionReason = \"intrinsic\"\n\tSelectionReasonSoleProvider SelectionReason = \"sole-provider\"\n\tSelectionReasonExplicit SelectionReason = \"explicit\"\n)\nfunc NewModuleBuild(string, string, string) (ModuleBuild, error) { return ModuleBuild{}, nil }\nfunc NewEndpoint[Request, Response any](capability.Contract[Request, Response], capability.Handler[Request, Response]) (Endpoint, error) { return Endpoint{}, nil }\nfunc NewBinding(BindingOptions, Endpoint) (Binding, error) { return Binding{}, nil }\nfunc NewCatalog(bindings []Binding) (Catalog, error) { return Catalog{bindings: append([]Binding(nil), bindings...)}, nil }\nfunc (c Catalog) Bindings() []Binding { return append([]Binding(nil), c.bindings...) }\ntype DispatcherOptions struct { DefaultTimeout time.Duration }\ntype Dispatcher struct { published bool }\nfunc NewDispatcher(DispatcherOptions) (*Dispatcher, error) { return &Dispatcher{}, nil }\nfunc (d *Dispatcher) Publish(Catalog) error { d.published = true; return nil }\nfunc (d *Dispatcher) Published() bool { return d != nil && d.published }\ntype Handle[Request, Response any] struct { available bool }\nfunc NewHandle[Request, Response any](_ *Dispatcher, _ capability.Contract[Request, Response], available bool) (Handle[Request, Response], error) { return Handle[Request, Response]{available: available}, nil }\nfunc (h Handle[Request, Response]) Available() bool { return h.available }\nfunc (Handle[Request, Response]) Invoke(context.Context, Request) (Response, error) { var response Response; return response, nil }\ntype ErrorCode string\nconst (\n\tErrorInvalidArgument ErrorCode = \"invalid_argument\"\n\tErrorUnauthenticated ErrorCode = \"unauthenticated\"\n\tErrorDenied ErrorCode = \"denied\"\n\tErrorNotFound ErrorCode = \"not_found\"\n\tErrorConflict ErrorCode = \"conflict\"\n\tErrorVersionIncompatible ErrorCode = \"version_incompatible\"\n\tErrorTimeout ErrorCode = \"timeout\"\n\tErrorUnavailable ErrorCode = \"unavailable\"\n\tErrorResultUnknown ErrorCode = \"result_unknown\"\n\tErrorCancelled ErrorCode = \"cancelled\"\n)\nfunc (code ErrorCode) String() string { return string(code) }\nfunc (code ErrorCode) Valid() bool { return code != \"\" }\ntype Error struct { code ErrorCode; detailCode string }\nfunc (*Error) Error() string { return \"invocation error\" }\nfunc (err *Error) Code() ErrorCode { if err == nil { return \"\" }; return err.code }\nfunc (err *Error) DetailCode() string { if err == nil { return \"\" }; return err.detailCode }\nfunc ValidDetailCode(string) bool { return true }\n")},
		{name: "lifecycle/lifecycle.go", data: []byte("package lifecycle\n\nimport (\n\t\"context\"\n\t\"time\"\n\n\t\"github.com/plystra/kernel/plugin\"\n)\n\ntype Provider interface {\n\tStart(context.Context) error\n\tStop(context.Context) error\n}\n\ntype State string\ntype Binding struct{}\ntype Manager struct{}\ntype ManagerOptions struct { RollbackTimeout time.Duration }\n\nfunc NewBinding(plugin.ID, Provider) (Binding, error) { return Binding{}, nil }\nfunc NewManager(ManagerOptions, []Binding) (*Manager, error) { return &Manager{}, nil }\nfunc (*Manager) State() State { return \"new\" }\nfunc (*Manager) Start(context.Context) error { return nil }\nfunc (*Manager) Stop(context.Context) error { return nil }\n")},
		{name: "plugin/id.go", data: []byte("package plugin\n\ntype ID struct{}\n\nfunc ParseID(string) (ID, error) { return ID{}, nil }\n")},
		{name: "plugin/manifest/config.go", data: []byte("package manifest\n\ntype Config struct{}\n\nfunc ParseConfig([]byte) (Config, error) { return Config{}, nil }\n")},
	}
	for _, file := range files {
		header := &zip.FileHeader{Name: prefix + file.name, Method: zip.Deflate}
		header.SetMode(0o644)
		header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatalf("CreateHeader: %v", err)
		}
		if _, err := writer.Write(file.data); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}
	return root
}

func writeProxyModule(t *testing.T, root, modulePath, version string, source map[string][]byte) {
	t.Helper()
	escapedPath, err := module.EscapePath(modulePath)
	if err != nil {
		t.Fatalf("EscapePath(%s): %v", modulePath, err)
	}
	escapedVersion, err := module.EscapeVersion(version)
	if err != nil {
		t.Fatalf("EscapeVersion(%s): %v", version, err)
	}
	versionRoot := filepath.Join(root, filepath.FromSlash(escapedPath), "@v")
	if err := os.MkdirAll(versionRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", versionRoot, err)
	}
	writeTestFile(t, filepath.Join(versionRoot, "list"), []byte(version+"\n"))
	writeTestFile(t, filepath.Join(versionRoot, escapedVersion+".info"), fmt.Appendf(nil, "{\"Version\":%q,\"Time\":\"2026-07-19T00:00:00Z\"}\n", version))
	moduleFile := []byte("module " + modulePath + "\n\ngo 1.26\n")
	writeTestFile(t, filepath.Join(versionRoot, escapedVersion+".mod"), moduleFile)

	archiveFile, err := os.Create(filepath.Join(versionRoot, escapedVersion+".zip"))
	if err != nil {
		t.Fatalf("Create zip: %v", err)
	}
	archive := zip.NewWriter(archiveFile)
	files := make(map[string][]byte, len(source)+1)
	files["go.mod"] = moduleFile
	for name, data := range source {
		files[name] = data
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	prefix := modulePath + "@" + version + "/"
	for _, name := range names {
		header := &zip.FileHeader{Name: prefix + filepath.ToSlash(name), Method: zip.Deflate}
		header.SetMode(0o644)
		header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatalf("CreateHeader(%s): %v", name, err)
		}
		if _, err := writer.Write(files[name]); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}
}

func moduleCacheRoot(t *testing.T, environment []string, modulePath, version string) string {
	t.Helper()
	escapedPath, err := module.EscapePath(modulePath)
	if err != nil {
		t.Fatalf("EscapePath(%s): %v", modulePath, err)
	}
	escapedVersion, err := module.EscapeVersion(version)
	if err != nil {
		t.Fatalf("EscapeVersion(%s): %v", version, err)
	}
	return filepath.Join(environmentValue(t, environment, "GOMODCACHE"), filepath.FromSlash(escapedPath)+"@"+escapedVersion)
}

func environmentValue(t *testing.T, environment []string, wanted string) string {
	t.Helper()
	for _, entry := range environment {
		key, value, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(key, wanted) {
			return value
		}
	}
	t.Fatalf("environment omits %s", wanted)
	return ""
}

func isolatedGoEnvironment(t *testing.T, proxyRoot string) []string {
	t.Helper()
	proxyPath := filepath.ToSlash(proxyRoot)
	if runtime.GOOS == "windows" {
		proxyPath = "/" + proxyPath
	}
	proxyURL := (&url.URL{Scheme: "file", Path: proxyPath}).String()
	overrides := map[string]string{
		"GOCACHE":     filepath.Join(t.TempDir(), "build-cache"),
		"GOENV":       "off",
		"GOFLAGS":     "-modcacherw",
		"GOMODCACHE":  filepath.Join(t.TempDir(), "module-cache"),
		"GONOPROXY":   "none",
		"GONOSUMDB":   "",
		"GOPRIVATE":   "",
		"GOPROXY":     proxyURL,
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
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func assertModuleState(t *testing.T, root, modulePath string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("ReadFile(go.mod): %v", err)
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		t.Fatalf("Parse(go.mod): %v", err)
	}
	if parsed.Module == nil || parsed.Module.Mod.Path != modulePath {
		t.Fatalf("module directive = %#v", parsed.Module)
	}
	if len(parsed.Require) != 1 || parsed.Require[0].Mod.Path != "github.com/plystra/kernel" || parsed.Require[0].Mod.Version != newproject.KernelVersion || parsed.Require[0].Indirect {
		t.Fatalf("requirements = %#v", parsed.Require)
	}
	if sum, err := os.ReadFile(filepath.Join(root, "go.sum")); err != nil || len(sum) == 0 {
		t.Fatalf("go.sum = %q, %v", sum, err)
	}
}

func assertDirectRequirement(t *testing.T, root, modulePath, version string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("ReadFile(go.mod): %v", err)
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		t.Fatalf("Parse(go.mod): %v", err)
	}
	for _, requirement := range parsed.Require {
		if requirement.Mod.Path == modulePath {
			if requirement.Mod.Version != version || requirement.Indirect {
				t.Fatalf("requirement %s = %s indirect %t, want %s direct", modulePath, requirement.Mod.Version, requirement.Indirect, version)
			}
			return
		}
	}
	t.Fatalf("go.mod omits direct requirement %s@%s", modulePath, version)
}

func snapshotTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	err := fs.WalkDir(os.DirFS(root), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if name == ".git" {
				return fs.SkipDir
			}
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

func assertPathPresence(t *testing.T, name string, expected bool) {
	t.Helper()
	_, err := os.Lstat(name)
	if expected && err != nil {
		t.Fatalf("expected path %s: %v", name, err)
	}
	if !expected && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected path %s: %v", name, err)
	}
}

func assertGitInitialized(t *testing.T, root string) {
	t.Helper()
	command := exec.Command("git", "-C", root, "rev-parse", "--is-inside-work-tree")
	output, err := command.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "true" {
		t.Fatalf("Git repository = %q, %v", output, err)
	}
	command = exec.Command("git", "-C", root, "symbolic-ref", "--short", "HEAD")
	output, err = command.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "main" {
		t.Fatalf("Git initial branch = %q, %v", output, err)
	}
}

func assertPlystraSkill(t *testing.T, root, modulePath string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".agents", "skills", "plystra", "SKILL.md"))
	if err != nil {
		t.Fatalf("read Plystra skill: %v", err)
	}
	for _, required := range []string{
		"name: plystra",
		"The current Go Module path is " + modulePath,
		"## Module and file ownership",
		"plystra new app",
		"plystra new app --module github.com/acme/app",
		"plystra new app --module github.com/acme/app --template github.com/acme/platform@v1.2.3",
		"Template-declared operational values and Secret-reference placeholders",
		"does not read PLATFORM_SMTP_PASSWORD",
		"invent values for required fields omitted by the template",
		"plystra plugin create records",
		"plystra capability create records.read --plugin records --expose",
		"plystra capability implement email.send/v1 --plugin mailer",
		"capabilities/records.read/v1/capability.yaml",
		"plugin.yaml",
		"plystra.yaml",
		"## Compose dependency Project configuration",
		"Every direct or transitive",
		"Dependency files such",
		"as plystra.production.yaml and plystra.test.yaml are never inherited",
		"Resolve an inherited Provider conflict with one exact current-Project choice",
		"plystra use email.send/v1 acme.email.smtp",
		"plystra use email.send/v1 acme.email.production --env production",
		"plystra use email.send/v1 acme.email.customer --config deploy/customer-a.yaml",
		"restores configuration, generated output, go.mod, and go.sum",
		"Remove only exact inherited declarations with sparse edits and null",
		"remove: [diagnostics.internal/v1]",
		"email.send/v1: null",
		"legacy_host: null",
		"Declared objects merge recursively",
		"Dependency http.address, http.transports, http.cors, and timeouts.startup",
		"plystra add github.com/acme/email@v1.4.2",
		"plystra remove github.com/acme/email",
		"plystra update github.com/acme/email@v1.5.0",
		"retains the selected module as a direct",
		"preserves an existing direct requirement",
		"only that module query",
		"restores every transaction-owned module",
		"dependency composition digest",
		"## Select an environment or one complete current-Project configuration",
		"plystra generate --env production",
		"plystra generate --check --env production",
		"PLYSTRA_ENV supplies the same environment name",
		"plystra capability expose records.read/v1 --env production",
		"plystra capability expose records.read/v1 --config deploy/customer-a.yaml",
		"regenerates with the same selection",
		"http.transports is a closed current-Project object",
		"connect defaults to true and rest",
		"null restores that field's",
		"Dependency Project transport",
		"http.cors is an optional closed current-Project object",
		"requires one nonempty allowed_origins list",
		"http.cors to null",
		"CORS settings are ignored",
		"Do not combine --env and --config",
		"preserves the sparse overlay",
		"plystra generate --config deploy/customer-a.yaml",
		"plystra generate --check --config deploy/customer-a.yaml",
		"PLYSTRA_CONFIG supplies the same path",
		"configuration schema v3",
		"environment, or explicit-config mode",
		"root dependency baseline",
		"merged beneath deploy/customer-a.yaml",
		"There is no handwritten provider registration",
		"dependencies.Dependencies",
		"generated/go/dependencies/",
		"bootstrap.New",
		"npm run typecheck",
		"plystra generate --check",
	} {
		if !strings.Contains(string(data), required) {
			t.Fatalf("Plystra skill omits %q:\n%s", required, data)
		}
	}
	processGuidance := strings.ReplaceAll(string(data), modulePath, "module-path")
	lower := strings.ToLower(processGuidance)
	for _, forbidden := range []string{"TODO", "commit", "branch", "push", "pull request", "repository", "version control"} {
		if strings.Contains(lower, strings.ToLower(forbidden)) {
			t.Fatalf("Plystra skill contains forbidden %q guidance:\n%s", forbidden, data)
		}
	}
	metadata, err := os.ReadFile(filepath.Join(root, ".agents", "skills", "plystra", "agents", "openai.yaml"))
	if err != nil || !bytes.Contains(metadata, []byte("Use $plystra")) || !bytes.Contains(metadata, []byte("module, Plugin, or Capability")) {
		t.Fatalf("Plystra skill metadata = %q, %v", metadata, err)
	}
}

func pluginScaffoldSnapshot(t *testing.T, root, pluginName string) map[string][]byte {
	t.Helper()
	tree := snapshotTree(t, root)
	result := make(map[string][]byte)
	pluginPrefix := pluginName + "/"
	generatedSuffix := "/" + pluginName + "_gen.go"
	for name, data := range tree {
		if strings.HasPrefix(name, pluginPrefix) || strings.HasPrefix(name, "generated/") && strings.HasSuffix(name, generatedSuffix) {
			result[name] = data
		}
	}
	return result
}

func writeTestFile(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func assertNoTransactionFiles(t *testing.T, parent string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(parent, ".*.plystra-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("transaction files remain: %v", matches)
	}
}
